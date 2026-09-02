//go:build windows && amd64

package platformcustomconformance

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-platform-custom-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

var getCurrentThreadID = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")

func threadID() uintptr {
	id, _, _ := getCurrentThreadID.Call()
	return id
}

type runtimeState struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime() (*runtimeState, error) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		return nil, err
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		return nil, err
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = ctx.Close()
		_ = iso.Close()
		return nil, err
	}
	return &runtimeState{iso, ctx, scope}, nil
}

func (r *runtimeState) close() error {
	if err := r.scope.Close(); err != nil {
		return err
	}
	if err := r.ctx.Close(); err != nil {
		return err
	}
	return r.iso.Close()
}

func (r *runtimeState) eval(source string) (gov8.Value, error) {
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		return gov8.Value{}, err
	}
	defer func() { _ = script.Close() }()
	return script.Run(r.scope, nil)
}

type capturedTask struct {
	isolate PlatformIdentity
	main    bool
	task    *gov8.Task
	delay   float64
}

type capturedIdle struct {
	isolate PlatformIdentity
	main    bool
	task    *gov8.IdleTask
}

type PlatformIdentity = gov8.PlatformIsolate

type recorder struct {
	mainThread uintptr
	mu         sync.Mutex
	task       []*capturedTask
	nonnest    []*capturedTask
	delayed    []*capturedTask
	nonDelay   []*capturedTask
	idle       []*capturedIdle
	closed     atomic.Int32
}

func (r *recorder) capture(isolate PlatformIdentity, task *gov8.Task, delay float64) *capturedTask {
	return &capturedTask{isolate: isolate, main: threadID() == r.mainThread, task: task, delay: delay}
}

func (r *recorder) PostTask(isolate PlatformIdentity, task *gov8.Task) {
	r.mu.Lock()
	r.task = append(r.task, r.capture(isolate, task, 0))
	r.mu.Unlock()
}

func (r *recorder) PostNonNestableTask(isolate PlatformIdentity, task *gov8.Task) {
	r.mu.Lock()
	r.nonnest = append(r.nonnest, r.capture(isolate, task, 0))
	r.mu.Unlock()
}

func (r *recorder) PostDelayedTask(isolate PlatformIdentity, task *gov8.Task, delay float64) {
	r.mu.Lock()
	r.delayed = append(r.delayed, r.capture(isolate, task, delay))
	r.mu.Unlock()
}

func (r *recorder) PostNonNestableDelayedTask(isolate PlatformIdentity, task *gov8.Task, delay float64) {
	r.mu.Lock()
	r.nonDelay = append(r.nonDelay, r.capture(isolate, task, delay))
	r.mu.Unlock()
}

func (r *recorder) PostIdleTask(isolate PlatformIdentity, task *gov8.IdleTask) {
	r.mu.Lock()
	r.idle = append(r.idle, &capturedIdle{isolate, threadID() == r.mainThread, task})
	r.mu.Unlock()
}

func (r *recorder) Close() { r.closed.Add(1) }

func (r *recorder) pop(which string) any {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch which {
	case "task":
		if len(r.task) > 0 {
			item := r.task[0]
			r.task = r.task[1:]
			return item
		}
	case "nonnest":
		if len(r.nonnest) > 0 {
			item := r.nonnest[0]
			r.nonnest = r.nonnest[1:]
			return item
		}
	case "delayed":
		if len(r.delayed) > 0 {
			item := r.delayed[0]
			r.delayed = r.delayed[1:]
			return item
		}
	case "nonDelay":
		if len(r.nonDelay) > 0 {
			item := r.nonDelay[0]
			r.nonDelay = r.nonDelay[1:]
			return item
		}
	case "idle":
		if len(r.idle) > 0 {
			item := r.idle[0]
			r.idle = r.idle[1:]
			return item
		}
	}
	return nil
}

func (r *recorder) wait(which string, timeout time.Duration) any {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if item := r.pop(which); item != nil {
			return item
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

func (r *recorder) dropAll() {
	r.mu.Lock()
	tasks := append([]*capturedTask{}, r.task...)
	tasks = append(tasks, r.nonnest...)
	tasks = append(tasks, r.delayed...)
	tasks = append(tasks, r.nonDelay...)
	idle := append([]*capturedIdle{}, r.idle...)
	r.task, r.nonnest, r.delayed, r.nonDelay, r.idle = nil, nil, nil, nil, nil
	r.mu.Unlock()
	for _, item := range tasks {
		_ = item.task.Close()
	}
	for _, item := range idle {
		_ = item.task.Close()
	}
}

func transferTask(task *gov8.Task) *gov8.Task {
	channel := make(chan *gov8.Task, 1)
	go func() { channel <- task }()
	return <-channel
}

func promiseState(p gov8.Promise) string {
	state, err := p.State()
	if err != nil {
		panic(err)
	}
	return state.String()
}

func defaultChild(impl gov8.PlatformImpl) {
	if err := gov8.SetFlagsFromString("--allow-natives-syntax"); err != nil {
		panic(err)
	}
	if err := gov8.ConfigureCustomPlatform(gov8.CustomPlatformOptions{ThreadPoolSize: 1, Unprotected: true}, impl); err != nil {
		panic(err)
	}
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	r, err := newRuntime()
	if err != nil {
		panic(err)
	}
	_, err = r.eval("(()=>{const b=new SharedArrayBuffer(4);globalThis.a=new Int32Array(b);return Atomics.waitAsync(a,0,0,Infinity).value})()")
	if err != nil {
		panic(err)
	}
	fmt.Println("before-notify")
	_, err = r.eval("Atomics.notify(a,0,1)")
	if err != nil {
		panic(err)
	}
	fmt.Println("after-notify")
	if err := r.close(); err != nil {
		panic(err)
	}
	if err := gov8.Shutdown(); err != nil {
		panic(err)
	}
}

type queuedObservation struct {
	PostTaskReceived                    bool     `json:"post_task_received"`
	PostTaskCallbackOnMain              bool     `json:"post_task_callback_on_main"`
	PostTaskSendRoundtripThenRun        bool     `json:"post_task_send_roundtrip_then_run"`
	WasmPromiseAfterRun                 string   `json:"wasm_promise_after_run"`
	NonNestableReceived                 bool     `json:"non_nestable_received"`
	NonNestableCallbackOnMain           bool     `json:"non_nestable_callback_on_main"`
	NonNestableSendRoundtripThenRun     bool     `json:"non_nestable_send_roundtrip_then_run"`
	NonNestableResult                   string   `json:"non_nestable_result"`
	NonNestableDelayedReceived          bool     `json:"non_nestable_delayed_received"`
	NonNestableDelaySeconds             float64  `json:"non_nestable_delay_seconds"`
	NonNestableDelayedCallbackOnMain    bool     `json:"non_nestable_delayed_callback_on_main"`
	NonNestableDelayedRoundtripThenDrop bool     `json:"non_nestable_delayed_send_roundtrip_then_drop"`
	TimeoutPromiseAfterDrop             string   `json:"timeout_promise_after_drop"`
	DelayedReceived                     bool     `json:"delayed_received"`
	DelayedSeconds                      *float64 `json:"delayed_seconds"`
	DelayedCallbackOnMain               *bool    `json:"delayed_callback_on_main"`
	IdleReceived                        bool     `json:"idle_received"`
	IdleCallbackOnMain                  *bool    `json:"idle_callback_on_main"`
	IdleRunDeadline                     *string  `json:"idle_run_deadline"`
	AllIsolatePointersNonzero           bool     `json:"all_isolate_pointers_nonzero"`
	AllIsolatePointersEqual             bool     `json:"all_isolate_pointers_equal"`
	UnderlyingPumpExecutedTask          bool     `json:"underlying_pump_executed_task"`
	ImplDropsBeforeLastPlatformRef      int32    `json:"impl_drops_before_last_platform_ref"`
	ImplDropsAfterLastPlatformRef       int32    `json:"impl_drops_after_last_platform_ref"`
}

func queuedChild() {
	if err := gov8.SetFlagsFromString("--allow-natives-syntax --expose-gc --lazy-compile-dispatcher --parallel-compile-tasks-for-lazy"); err != nil {
		panic(err)
	}
	recorder := &recorder{mainThread: threadID()}
	if err := gov8.ConfigureCustomPlatform(gov8.CustomPlatformOptions{ThreadPoolSize: 2, IdleTaskSupport: true, Unprotected: true}, recorder); err != nil {
		panic(err)
	}
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	r, err := newRuntime()
	if err != nil {
		panic(err)
	}
	recorder.dropAll()

	wasmValue, err := r.eval("WebAssembly.compile(new Uint8Array([0,97,115,109,1,0,0,0]))")
	if err != nil {
		panic(err)
	}
	wasm := gov8.Promise{Value: wasmValue}
	posted, _ := recorder.wait("task", 10*time.Second).(*capturedTask)
	if posted == nil {
		panic("timed out waiting for post_task")
	}
	foreign, err := gov8.NewIsolate()
	if err != nil {
		panic(err)
	}
	if err := posted.task.Run(foreign); err == nil || !strings.Contains(err.Error(), "different isolate") {
		panic(fmt.Sprintf("foreign-isolate Task.Run = %v", err))
	}
	if err := foreign.Close(); err != nil {
		panic(err)
	}
	wrongThread := make(chan error, 1)
	go func() { wrongThread <- posted.task.Run(r.iso) }()
	if err := <-wrongThread; err == nil || !strings.Contains(err.Error(), "affinity") {
		panic(fmt.Sprintf("wrong-thread Task.Run = %v", err))
	}
	if err := transferTask(posted.task).Run(r.iso); err != nil {
		panic(err)
	}
	if err := posted.task.Run(r.iso); err == nil {
		panic("Task allowed double Run")
	}
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		panic(err)
	}

	atomicsValue, err := r.eval("(()=>{const b=new SharedArrayBuffer(4);globalThis.a=new Int32Array(b);return Atomics.waitAsync(a,0,0,Infinity).value})()")
	if err != nil {
		panic(err)
	}
	atomics := gov8.Promise{Value: atomicsValue}
	if _, err := r.eval("Atomics.notify(a,0,1)"); err != nil {
		panic(err)
	}
	nonnest, _ := recorder.wait("nonnest", 10*time.Second).(*capturedTask)
	if nonnest == nil {
		panic("timed out waiting for non-nestable task")
	}
	if err := transferTask(nonnest.task).Run(r.iso); err != nil {
		panic(err)
	}
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		panic(err)
	}
	atomicsResult, err := atomics.Result(r.scope)
	if err != nil {
		panic(err)
	}
	atomicsText, err := atomicsResult.ToString(r.ctx)
	if err != nil {
		panic(err)
	}

	timeoutValue, err := r.eval("(()=>{const b=new SharedArrayBuffer(4);globalThis.a=new Int32Array(b);return Atomics.waitAsync(a,0,0,5000).value})()")
	if err != nil {
		panic(err)
	}
	timeoutPromise := gov8.Promise{Value: timeoutValue}
	timeout, _ := recorder.wait("nonDelay", 10*time.Second).(*capturedTask)
	if timeout == nil {
		panic("timed out waiting for non-nestable delayed task")
	}
	if err := transferTask(timeout.task).Close(); err != nil {
		panic(err)
	}
	if err := timeout.task.Close(); err == nil {
		panic("Task allowed double Close")
	}
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		panic(err)
	}

	if _, err := r.eval("let garbage=[];for(let i=0;i<20000;i++)garbage.push({i});garbage=null;gc();true"); err != nil {
		panic(err)
	}
	if err := r.iso.MemoryPressureNotification(gov8.MemoryPressureModerate); err != nil {
		panic(err)
	}
	if err := r.iso.MemoryPressureNotification(gov8.MemoryPressureNone); err != nil {
		panic(err)
	}
	var delayedObservation *capturedTask
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if task, _ := recorder.pop("delayed").(*capturedTask); task != nil {
			delayedObservation = task
			break
		}
		if task, _ := recorder.pop("task").(*capturedTask); task != nil {
			_ = task.task.Run(r.iso)
			_ = r.iso.PerformMicrotaskCheckpoint()
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	if delayedObservation != nil {
		if err := delayedObservation.task.Close(); err != nil {
			panic(err)
		}
	}

	var lazy strings.Builder
	for index := 0; index < 64; index++ {
		fmt.Fprintf(&lazy, "function lazy%d(x){let y=x;", index)
		for offset := 0; offset < 128; offset++ {
			fmt.Fprintf(&lazy, "y=(y*33+%d)|0;", offset)
		}
		lazy.WriteString("return y;}\n")
	}
	lazy.WriteString("true")
	if _, err := r.eval(lazy.String()); err != nil {
		panic(err)
	}
	idle, _ := recorder.wait("idle", 2*time.Second).(*capturedIdle)
	if idle != nil {
		if err := idle.task.Run(r.iso, math.Inf(1)); err != nil {
			panic(err)
		}
	}
	pump, err := r.iso.PumpMessageLoop(false)
	if err != nil {
		panic(err)
	}
	recorder.dropAll()

	allNonzero := posted.isolate != 0 && nonnest.isolate != 0 && timeout.isolate != 0
	allEqual := posted.isolate == nonnest.isolate && posted.isolate == timeout.isolate
	var delayedSeconds *float64
	var delayedOnMain *bool
	if delayedObservation != nil {
		delayedSeconds = &delayedObservation.delay
		delayedOnMain = &delayedObservation.main
		allNonzero = allNonzero && delayedObservation.isolate != 0
		allEqual = allEqual && delayedObservation.isolate == posted.isolate
	}
	var idleOnMain *bool
	var idleDeadline *string
	if idle != nil {
		idleOnMain = &idle.main
		infinity := "Infinity"
		idleDeadline = &infinity
		allNonzero = allNonzero && idle.isolate != 0
		allEqual = allEqual && idle.isolate == posted.isolate
	}
	observation := queuedObservation{
		PostTaskReceived: true, PostTaskCallbackOnMain: posted.main,
		PostTaskSendRoundtripThenRun: true, WasmPromiseAfterRun: promiseState(wasm),
		NonNestableReceived: true, NonNestableCallbackOnMain: nonnest.main,
		NonNestableSendRoundtripThenRun: true, NonNestableResult: atomicsText,
		NonNestableDelayedReceived: true, NonNestableDelaySeconds: timeout.delay,
		NonNestableDelayedCallbackOnMain:    timeout.main,
		NonNestableDelayedRoundtripThenDrop: true,
		TimeoutPromiseAfterDrop:             promiseState(timeoutPromise),
		DelayedReceived:                     delayedObservation != nil,
		DelayedSeconds:                      delayedSeconds,
		DelayedCallbackOnMain:               delayedOnMain,
		IdleReceived:                        idle != nil,
		IdleCallbackOnMain:                  idleOnMain,
		IdleRunDeadline:                     idleDeadline,
		AllIsolatePointersNonzero:           allNonzero,
		AllIsolatePointersEqual:             allEqual,
		UnderlyingPumpExecutedTask:          pump,
		ImplDropsBeforeLastPlatformRef:      recorder.closed.Load(),
	}
	if err := r.close(); err != nil {
		panic(err)
	}
	if _, err := gov8.Dispose(); err != nil {
		panic(err)
	}
	if err := gov8.DisposePlatform(); err != nil {
		panic(err)
	}
	observation.ImplDropsAfterLastPlatformRef = recorder.closed.Load()
	encoded, _ := json.Marshal(struct {
		Check string            `json:"check"`
		OK    bool              `json:"ok"`
		Value queuedObservation `json:"value"`
	}{"platform_custom/queued_all_callbacks", true, observation})
	fmt.Println(string(encoded))
}

type panicImpl struct{ gov8.PlatformImplFuncs }

func (panicImpl) PostNonNestableTask(gov8.PlatformIsolate, *gov8.Task) {
	panic("platform callback panic marker")
}

func panicChild() {
	if err := gov8.SetFlagsFromString("--allow-natives-syntax"); err != nil {
		panic(err)
	}
	if err := gov8.ConfigureCustomPlatform(gov8.CustomPlatformOptions{ThreadPoolSize: 1, Unprotected: true}, panicImpl{}); err != nil {
		panic(err)
	}
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	r, err := newRuntime()
	if err != nil {
		panic(err)
	}
	_, _ = r.eval("(()=>{const b=new SharedArrayBuffer(4);globalThis.a=new Int32Array(b);return Atomics.waitAsync(a,0,0,Infinity).value})()")
	_, _ = r.eval("Atomics.notify(a,0,1)")
}

func negativeChild() {
	if err := gov8.ConfigureCustomPlatform(gov8.CustomPlatformOptions{}, nil); err == nil {
		panic("nil implementation accepted")
	}
	if err := gov8.ConfigureCustomPlatform(gov8.CustomPlatformOptions{}, gov8.PlatformImplFuncs{}); err != nil {
		panic(err)
	}
	if err := gov8.ConfigurePlatform(gov8.PlatformOptions{}); err == nil {
		panic("second platform configuration accepted")
	}
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	if err := gov8.ConfigureCustomPlatform(gov8.CustomPlatformOptions{}, gov8.PlatformImplFuncs{}); err == nil {
		panic("post-initialize configuration accepted")
	}
	if err := gov8.Shutdown(); err != nil {
		panic(err)
	}
	fmt.Println("negative-ok")
}

type lifecycleImpl struct {
	gov8.PlatformImplFuncs
	mu               sync.Mutex
	delayed          *gov8.Task
	closeCount       atomic.Int32
	closeSawConsumed atomic.Bool
}

func (impl *lifecycleImpl) PostNonNestableDelayedTask(_ gov8.PlatformIsolate, task *gov8.Task, _ float64) {
	impl.mu.Lock()
	impl.delayed = task
	impl.mu.Unlock()
}

func (impl *lifecycleImpl) task() *gov8.Task {
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return impl.delayed
}

func (impl *lifecycleImpl) Close() {
	impl.closeCount.Add(1)
	if task := impl.task(); task != nil {
		err := task.Close()
		impl.closeSawConsumed.Store(err != nil && strings.Contains(err.Error(), "already consumed"))
	}
}

func lifecycleChild() {
	if err := gov8.SetFlagsFromString("--allow-natives-syntax"); err != nil {
		panic(err)
	}
	impl := &lifecycleImpl{}
	if err := gov8.ConfigureCustomPlatform(gov8.CustomPlatformOptions{ThreadPoolSize: 1, Unprotected: true}, impl); err != nil {
		panic(err)
	}
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	r, err := newRuntime()
	if err != nil {
		panic(err)
	}
	if _, err := r.eval("(()=>{const b=new SharedArrayBuffer(4);globalThis.a=new Int32Array(b);return Atomics.waitAsync(a,0,0,5000).value})()"); err != nil {
		panic(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for impl.task() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	task := impl.task()
	if task == nil {
		panic("timed out waiting for lifecycle task")
	}
	// Retained tasks stay registered across GC and are still drained exactly
	// once during platform shutdown.
	runtime.GC()
	runtime.GC()
	if err := r.close(); err != nil {
		panic(err)
	}
	if err := task.Run(r.iso); err == nil || !strings.Contains(err.Error(), "after Close") {
		panic(fmt.Sprintf("task with closed isolate = %v", err))
	}
	if err := gov8.Shutdown(); err != nil {
		panic(err)
	}
	if impl.closeCount.Load() != 1 || !impl.closeSawConsumed.Load() {
		panic(fmt.Sprintf("platform close: count=%d drained_before_close=%v", impl.closeCount.Load(), impl.closeSawConsumed.Load()))
	}
	if err := task.Close(); err == nil || !strings.Contains(err.Error(), "already consumed") {
		panic(fmt.Sprintf("task after platform disposal = %v", err))
	}
	fmt.Println("lifecycle-ok")
}

func TestCustomPlatformChild(t *testing.T) {
	switch os.Getenv("GOV8_CUSTOM_PLATFORM_CHILD") {
	case "default":
		defaultChild(gov8.PlatformImplDefaults{})
	case "safe-drop":
		defaultChild(gov8.PlatformImplFuncs{})
	case "queued":
		queuedChild()
	case "panic":
		panicChild()
	case "negative":
		negativeChild()
	case "lifecycle":
		lifecycleChild()
	default:
		t.Skip("subprocess helper")
	}
}

func runChild(t *testing.T, mode string, timeout time.Duration) ([]byte, []byte, int, bool) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestCustomPlatformChild$", "-test.v=false")
	command.Env = append(os.Environ(), "GOV8_CUSTOM_PLATFORM_CHILD="+mode)
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		code := 0
		if err != nil {
			code = command.ProcessState.ExitCode()
		}
		return []byte(stdout.String()), []byte(stderr.String()), code, false
	case <-time.After(timeout):
		_ = command.Process.Kill()
		<-done
		return []byte(stdout.String()), []byte(stderr.String()), command.ProcessState.ExitCode(), true
	}
}

func TestCustomPlatformConformance(t *testing.T) {
	defaultOut, _, _, timedOut := runChild(t, "default", 500*time.Millisecond)
	defaultText := string(defaultOut)
	defaultValue := struct {
		CallbackKind              string `json:"callback_kind"`
		DefaultBody               string `json:"default_body"`
		EnteredAtomicsNotify      bool   `json:"entered_atomics_notify"`
		ReturnedFromAtomicsNotify bool   `json:"returned_from_atomics_notify"`
		DidNotExitWithin500ms     bool   `json:"did_not_exit_within_500ms"`
		TerminatedByHarness       bool   `json:"terminated_by_harness"`
	}{"post_non_nestable_task", "task.run()", strings.Contains(defaultText, "before-notify"), strings.Contains(defaultText, "after-notify"), timedOut, timedOut}
	first, _ := json.Marshal(struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value any    `json:"value"`
	}{"platform_custom/default_immediate_deadlock", true, defaultValue})

	queued, queuedErr, code, timedOut := runChild(t, "queued", 30*time.Second)
	if timedOut || code != 0 {
		t.Fatalf("queued child: timeout=%v code=%#x\nstdout:\n%s\nstderr:\n%s", timedOut, uint32(code), queued, queuedErr)
	}
	queued = []byte(jsonLine(t, queued, `{"check":"platform_custom/queued_all_callbacks"`))
	_, panicErr, panicCode, panicTimeout := runChild(t, "panic", 10*time.Second)
	panicValue := struct {
		Success            bool   `json:"success"`
		WindowsStatus      string `json:"windows_status"`
		PanicMarkerPresent bool   `json:"panic_marker_present"`
	}{false, fmt.Sprintf("0x%08X", uint32(panicCode)), strings.Contains(string(panicErr), "platform callback panic marker")}
	if panicTimeout {
		t.Fatal("panic child timed out")
	}
	third, _ := json.Marshal(struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value any    `json:"value"`
	}{"platform_custom/callback_panic", true, panicValue})

	actual := string(first) + "\n" + string(queued) + "\n" + string(third) + "\n{\"summary\":{\"total\":3,\"passed\":3,\"failed\":0}}\n"
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	want := string(fixture)
	if actual != want {
		t.Fatalf("normalized report differs:\nwant:\n%s\ngot:\n%s", want, actual)
	}
}

func TestCustomPlatformNilSafeAdapterAvoidsDefaultDeadlock(t *testing.T) {
	stdout, stderr, code, timedOut := runChild(t, "safe-drop", 5*time.Second)
	if timedOut || code != 0 {
		t.Fatalf("safe-drop child: timeout=%v code=%#x\nstdout:\n%s\nstderr:\n%s", timedOut, uint32(code), stdout, stderr)
	}
	text := string(stdout)
	if !strings.Contains(text, "before-notify") || !strings.Contains(text, "after-notify") {
		t.Fatalf("safe-drop adapter did not return from Atomics.notify:\n%s", text)
	}
}

func jsonLine(t *testing.T, output []byte, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("missing JSON line %q in output:\n%s", prefix, output)
	return ""
}

func TestCustomPlatformNegativeLifecycle(t *testing.T) {
	for _, test := range []struct {
		mode, marker string
	}{
		{"negative", "negative-ok"},
		{"lifecycle", "lifecycle-ok"},
	} {
		stdout, stderr, code, timedOut := runChild(t, test.mode, 15*time.Second)
		if timedOut || code != 0 || !strings.Contains(string(stdout), test.marker) {
			t.Fatalf("%s child: timeout=%v code=%#x stdout=%s stderr=%s", test.mode, timedOut, uint32(code), stdout, stderr)
		}
	}
}
