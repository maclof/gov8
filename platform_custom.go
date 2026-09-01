//go:build windows && amd64

package gov8

import (
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"syscall"
)

// CustomPlatformOptions configures the DefaultPlatform-backed custom task
// dispatcher installed by ConfigureCustomPlatform. ThreadPoolSize follows
// rusty_v8: zero selects the hardware default and values above 16 are clamped.
type CustomPlatformOptions struct {
	ThreadPoolSize  uint32
	IdleTaskSupport bool
	Unprotected     bool
}

// PlatformIsolate is the stable, opaque identity supplied with a foreground
// task. It is comparable and suitable as a queue key, but cannot be converted
// into an Isolate. Task.Run verifies it against the caller-supplied Isolate.
type PlatformIsolate uintptr

// PlatformImpl receives ownership of foreground tasks posted by V8. Calls may
// arrive concurrently from arbitrary native threads; implementations must be
// concurrency-safe. Retain the task, then run it later on the isolate's owning
// thread, or Close it without running.
type PlatformImpl interface {
	PostTask(PlatformIsolate, *Task)
	PostNonNestableTask(PlatformIsolate, *Task)
	PostDelayedTask(PlatformIsolate, *Task, float64)
	PostNonNestableDelayedTask(PlatformIsolate, *Task, float64)
	PostIdleTask(PlatformIsolate, *IdleTask)
}

// PlatformImplCloser is optionally implemented by PlatformImpl. Close is
// invoked exactly once when DisposePlatform destroys the native platform,
// after outstanding task wrappers have been closed.
type PlatformImplCloser interface{ Close() }

// PlatformImplFuncs adapts function fields into PlatformImpl. A nil callback
// closes (drops) its task. In particular, it never runs non-nestable work
// synchronously: rusty_v8's immediate default deadlocks when Atomics.notify
// posts such work while holding V8's waiter lock, so Go chooses safe dropping
// as the explicit normalization.
type PlatformImplFuncs struct {
	Task                   func(PlatformIsolate, *Task)
	NonNestableTask        func(PlatformIsolate, *Task)
	DelayedTask            func(PlatformIsolate, *Task, float64)
	NonNestableDelayedTask func(PlatformIsolate, *Task, float64)
	IdleTask               func(PlatformIsolate, *IdleTask)
}

func (f PlatformImplFuncs) PostTask(isolate PlatformIsolate, task *Task) {
	if f.Task != nil {
		f.Task(isolate, task)
		return
	}
	_ = task.Close()
}

func (f PlatformImplFuncs) PostNonNestableTask(isolate PlatformIsolate, task *Task) {
	if f.NonNestableTask != nil {
		f.NonNestableTask(isolate, task)
		return
	}
	_ = task.Close()
}

func (f PlatformImplFuncs) PostDelayedTask(isolate PlatformIsolate, task *Task, delay float64) {
	if f.DelayedTask != nil {
		f.DelayedTask(isolate, task, delay)
		return
	}
	_ = task.Close()
}

func (f PlatformImplFuncs) PostNonNestableDelayedTask(isolate PlatformIsolate, task *Task, delay float64) {
	if f.NonNestableDelayedTask != nil {
		f.NonNestableDelayedTask(isolate, task, delay)
		return
	}
	_ = task.Close()
}

func (f PlatformImplFuncs) PostIdleTask(isolate PlatformIsolate, task *IdleTask) {
	if f.IdleTask != nil {
		f.IdleTask(isolate, task)
		return
	}
	_ = task.Close()
}

type customPlatformConfiguration struct {
	idle        bool
	unprotected bool
	threads     uint32
	id          uint64
}

var selectedCustomPlatform *customPlatformConfiguration

type customPlatformEntry struct {
	impl PlatformImpl
	mu   sync.Mutex
	next uint64
	work map[uint64]customPlatformWork
}

type customPlatformWork interface{ closeFromPlatform() }

var customPlatformRegistry = struct {
	sync.Mutex
	next    uint64
	entries map[uint64]*customPlatformEntry
}{entries: make(map[uint64]*customPlatformEntry)}

// ConfigureCustomPlatform selects a custom task dispatcher for the next
// Initialize. The implementation is retained until DisposePlatform.
func ConfigureCustomPlatform(options CustomPlatformOptions, impl PlatformImpl) error {
	if impl == nil {
		return errors.New("gov8: custom platform implementation is required")
	}
	if err := loadShim(); err != nil {
		return err
	}
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	if loadPlatform() != stateUninitialized {
		return fmt.Errorf("gov8: ConfigureCustomPlatform must be called before Initialize")
	}
	if selectedPlatform != nil || selectedCustomPlatform != nil {
		return fmt.Errorf("gov8: platform already configured")
	}
	if err := ensureCustomPlatformDispatcher(); err != nil {
		return err
	}
	threads := options.ThreadPoolSize
	if threads > 16 {
		threads = 16
	}
	customPlatformRegistry.Lock()
	customPlatformRegistry.next++
	id := customPlatformRegistry.next
	customPlatformRegistry.entries[id] = &customPlatformEntry{
		impl: impl,
		work: make(map[uint64]customPlatformWork),
	}
	customPlatformRegistry.Unlock()
	selectedCustomPlatform = &customPlatformConfiguration{
		idle: options.IdleTaskSupport, unprotected: options.Unprotected,
		threads: threads, id: id,
	}
	return nil
}

// initializeCustomPlatformIfConfigured is called with lifecycleMu held.
func initializeCustomPlatformIfConfigured() (bool, error) {
	configuration := selectedCustomPlatform
	if configuration == nil {
		return false, nil
	}
	idle, unprotected := uintptr(0), uintptr(0)
	if configuration.idle {
		idle = 1
	}
	if configuration.unprotected {
		unprotected = 1
	}
	err := callErr("Initialize", proc("gov8_pc_initialize_custom"),
		uintptr(configuration.threads), idle, unprotected, uintptr(configuration.id))
	return true, err
}

const (
	platformDispatchTask int32 = iota
	platformDispatchNonNestable
	platformDispatchDelayed
	platformDispatchNonNestableDelayed
	platformDispatchIdle
	platformDispatchDrop
)

type customPlatformFrame struct {
	kind    int32
	pad     int32
	id      uint64
	isolate uintptr
	task    uintptr
	delay   uint64
}

var customPlatformDispatcherOnce sync.Once
var customPlatformDispatcherErr error
var customPlatformDispatcher = syscall.NewCallback(customPlatformDispatch)

func ensureCustomPlatformDispatcher() error {
	customPlatformDispatcherOnce.Do(func() {
		customPlatformDispatcherErr = callErr("ConfigureCustomPlatform",
			proc("gov8_pc_set_dispatcher"), customPlatformDispatcher)
	})
	return customPlatformDispatcherErr
}

func lookupCustomPlatformEntry(id uint64) *customPlatformEntry {
	customPlatformRegistry.Lock()
	entry := customPlatformRegistry.entries[id]
	customPlatformRegistry.Unlock()
	return entry
}

func customPlatformDispatch(frame *customPlatformFrame) uintptr {
	if frame == nil {
		fatalHostMisuse("custom platform dispatch received a nil frame")
		return 0
	}
	entry := lookupCustomPlatformEntry(frame.id)
	if entry == nil {
		fatalHostMisuse("custom platform dispatch for unknown handle %d", frame.id)
		return 0
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintf(os.Stderr, "gov8: panic in custom platform callback: %v\n", recovered)
			proc("gov8_host_panic_abort").Call()
		}
	}()
	isolate := PlatformIsolate(frame.isolate)
	switch frame.kind {
	case platformDispatchTask, platformDispatchNonNestable,
		platformDispatchDelayed, platformDispatchNonNestableDelayed:
		if frame.isolate == 0 || frame.task == 0 {
			fatalHostMisuse("custom platform task dispatch received a null native handle")
			return 0
		}
		task := newPlatformTask(entry, frame.id, isolate, frame.task)
		switch frame.kind {
		case platformDispatchTask:
			entry.impl.PostTask(isolate, task)
		case platformDispatchNonNestable:
			entry.impl.PostNonNestableTask(isolate, task)
		case platformDispatchDelayed:
			entry.impl.PostDelayedTask(isolate, task, math.Float64frombits(frame.delay))
		case platformDispatchNonNestableDelayed:
			entry.impl.PostNonNestableDelayedTask(isolate, task, math.Float64frombits(frame.delay))
		}
		task.armFinalizer()
	case platformDispatchIdle:
		if frame.isolate == 0 || frame.task == 0 {
			fatalHostMisuse("custom platform idle dispatch received a null native handle")
			return 0
		}
		task := newPlatformIdleTask(entry, frame.id, isolate, frame.task)
		entry.impl.PostIdleTask(isolate, task)
		task.armFinalizer()
	case platformDispatchDrop:
		dropCustomPlatformEntry(frame.id, entry)
	default:
		fatalHostMisuse("custom platform dispatch for unknown kind %d", frame.kind)
	}
	return 0
}

func (entry *customPlatformEntry) add(work customPlatformWork) uint64 {
	entry.mu.Lock()
	entry.next++
	id := entry.next
	entry.work[id] = work
	entry.mu.Unlock()
	return id
}

func (entry *customPlatformEntry) remove(id uint64) {
	entry.mu.Lock()
	delete(entry.work, id)
	entry.mu.Unlock()
}

func dropCustomPlatformEntry(id uint64, entry *customPlatformEntry) {
	customPlatformRegistry.Lock()
	if customPlatformRegistry.entries[id] == entry {
		delete(customPlatformRegistry.entries, id)
	}
	customPlatformRegistry.Unlock()
	entry.mu.Lock()
	work := make([]customPlatformWork, 0, len(entry.work))
	for _, item := range entry.work {
		work = append(work, item)
	}
	entry.work = make(map[uint64]customPlatformWork)
	entry.mu.Unlock()
	for _, item := range work {
		item.closeFromPlatform()
	}
	if closer, ok := entry.impl.(PlatformImplCloser); ok {
		closer.Close()
	}
}

// Task is a transferred V8 foreground task. Run and Close are one-shot and
// mutually exclusive. Close may run on any thread; Run requires the owning
// live Isolate and therefore cannot accidentally execute on a worker thread.
type Task struct {
	mu             sync.Mutex
	handle         uintptr
	isolate        PlatformIsolate
	entry          *customPlatformEntry
	workID         uint64
	finalizerArmed bool
}

func newPlatformTask(entry *customPlatformEntry, _ uint64, isolate PlatformIsolate, handle uintptr) *Task {
	task := &Task{handle: handle, isolate: isolate, entry: entry}
	task.workID = entry.add(task)
	return task
}

// armFinalizer is deliberately delayed until the posting callback returns.
// Synchronously consumed tasks are the common path and remain live through
// the callback, so installing and immediately removing a runtime finalizer
// only adds bookkeeping. A retained task receives the same finalizer before
// the dispatcher releases its local reference.
func (task *Task) armFinalizer() {
	task.mu.Lock()
	if task.handle != 0 {
		runtime.SetFinalizer(task, func(task *Task) { _ = task.Close() })
		task.finalizerArmed = true
	}
	task.mu.Unlock()
}

func (task *Task) take() (uintptr, error) {
	if task == nil {
		return 0, fmt.Errorf("gov8: nil Task")
	}
	task.mu.Lock()
	if task.handle == 0 {
		task.mu.Unlock()
		return 0, fmt.Errorf("gov8: Task already consumed")
	}
	handle := task.handle
	task.handle = 0
	finalizerArmed := task.finalizerArmed
	task.finalizerArmed = false
	task.mu.Unlock()
	if finalizerArmed {
		runtime.SetFinalizer(task, nil)
	}
	task.entry.remove(task.workID)
	return handle, nil
}

// Run executes and destroys the task. isolate must be the exact live isolate
// named by the posting callback and Run must occur on its owning thread.
func (task *Task) Run(isolate *Isolate) error {
	if task == nil {
		return fmt.Errorf("gov8: nil Task")
	}
	if isolate == nil {
		return fmt.Errorf("gov8: Task.Run requires an isolate")
	}
	if err := isolate.check(); err != nil {
		return err
	}
	if PlatformIsolate(isolate.handleAssumingCheck()) != task.isolate {
		return foreignIsolate("task")
	}
	handle, err := task.take()
	if err != nil {
		return err
	}
	r1, _, _ := syscall.Syscall(proc("gov8_pc_task_run_delete").Addr(), 1, handle, 0, 0)
	if int64(r1) < 0 {
		return shimError("Task.Run", r1)
	}
	return nil
}

// Close destroys a task without executing it and may be called from any
// thread. It returns an error after Run or another Close.
func (task *Task) Close() error {
	handle, err := task.take()
	if err != nil {
		return err
	}
	r1, _, _ := syscall.Syscall(proc("gov8_pc_task_delete").Addr(), 1, handle, 0, 0)
	if int64(r1) < 0 {
		return shimError("Task.Close", r1)
	}
	return nil
}

func (task *Task) closeFromPlatform() {
	if task == nil {
		return
	}
	task.mu.Lock()
	if task.handle == 0 {
		task.mu.Unlock()
		return
	}
	handle := task.handle
	task.handle = 0
	finalizerArmed := task.finalizerArmed
	task.finalizerArmed = false
	task.mu.Unlock()
	if finalizerArmed {
		runtime.SetFinalizer(task, nil)
	}
	_, _, _ = syscall.Syscall(proc("gov8_pc_task_delete").Addr(), 1, handle, 0, 0)
}

// IdleTask is a transferred V8 idle task with the same one-shot ownership and
// affinity contract as Task.
type IdleTask struct {
	mu             sync.Mutex
	handle         uintptr
	isolate        PlatformIsolate
	entry          *customPlatformEntry
	workID         uint64
	finalizerArmed bool
}

func newPlatformIdleTask(entry *customPlatformEntry, _ uint64, isolate PlatformIsolate, handle uintptr) *IdleTask {
	task := &IdleTask{handle: handle, isolate: isolate, entry: entry}
	task.workID = entry.add(task)
	return task
}

func (task *IdleTask) armFinalizer() {
	task.mu.Lock()
	if task.handle != 0 {
		runtime.SetFinalizer(task, func(task *IdleTask) { _ = task.Close() })
		task.finalizerArmed = true
	}
	task.mu.Unlock()
}

func (task *IdleTask) take() (uintptr, error) {
	if task == nil {
		return 0, fmt.Errorf("gov8: nil IdleTask")
	}
	task.mu.Lock()
	if task.handle == 0 {
		task.mu.Unlock()
		return 0, fmt.Errorf("gov8: IdleTask already consumed")
	}
	handle := task.handle
	task.handle = 0
	finalizerArmed := task.finalizerArmed
	task.finalizerArmed = false
	task.mu.Unlock()
	if finalizerArmed {
		runtime.SetFinalizer(task, nil)
	}
	task.entry.remove(task.workID)
	return handle, nil
}

// Run executes the idle task with an absolute deadline and destroys it.
func (task *IdleTask) Run(isolate *Isolate, deadlineInSeconds float64) error {
	if task == nil {
		return fmt.Errorf("gov8: nil IdleTask")
	}
	if isolate == nil {
		return fmt.Errorf("gov8: IdleTask.Run requires an isolate")
	}
	if err := isolate.check(); err != nil {
		return err
	}
	if PlatformIsolate(isolate.handleAssumingCheck()) != task.isolate {
		return foreignIsolate("idle task")
	}
	handle, err := task.take()
	if err != nil {
		return err
	}
	r1, _, _ := syscall.Syscall(proc("gov8_pc_idle_task_run_delete").Addr(), 2,
		handle, uintptr(math.Float64bits(deadlineInSeconds)), 0)
	if int64(r1) < 0 {
		return shimError("IdleTask.Run", r1)
	}
	return nil
}

// Close destroys an idle task without running it and may run on any thread.
func (task *IdleTask) Close() error {
	handle, err := task.take()
	if err != nil {
		return err
	}
	r1, _, _ := syscall.Syscall(proc("gov8_pc_idle_task_delete").Addr(), 1, handle, 0, 0)
	if int64(r1) < 0 {
		return shimError("IdleTask.Close", r1)
	}
	return nil
}

func (task *IdleTask) closeFromPlatform() {
	if task == nil {
		return
	}
	task.mu.Lock()
	if task.handle == 0 {
		task.mu.Unlock()
		return
	}
	handle := task.handle
	task.handle = 0
	finalizerArmed := task.finalizerArmed
	task.finalizerArmed = false
	task.mu.Unlock()
	if finalizerArmed {
		runtime.SetFinalizer(task, nil)
	}
	_, _, _ = syscall.Syscall(proc("gov8_pc_idle_task_delete").Addr(), 1, handle, 0, 0)
}
