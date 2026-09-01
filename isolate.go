//go:build windows && amd64

package gov8

import (
	"fmt"
	"runtime"
	"sync"
)

// Isolate is a V8 isolate. V8 isolates are strictly thread-affine: creating
// an Isolate locks the calling goroutine to its OS thread (runtime.
// LockOSThread) for the lifetime of the isolate, and every operation
// validates that it runs on that thread. This surfaces the engine's real
// threading contract instead of hiding it: to use several isolates
// concurrently, run each one's operations on its own goroutine (see the
// concurrency tests).
//
// Resources derived from an isolate (scopes, contexts, values, scripts,
// try-catches, microtask queues) must normally be closed before Isolate.Close.
// Outstanding cppgc persistent handles are drained during native teardown and
// their later Close is a no-op. Isolate.Close must run on the owning thread.
type Isolate struct {
	mu                         sync.Mutex // guards lifecycle/configuration flags; calls are thread-serialized by affinity
	handle                     uintptr
	tid                        uint32
	closed                     bool
	contextsCreated            bool
	advancedCounterHandle      uintptr
	advancedExternalReferences bool
}

// NewIsolate creates a fresh isolate with a default ArrayBuffer allocator.
// Creation is serialized against Dispose/DisposePlatform: the engine
// allocation happens while the process teardown lock is held and the new
// isolate is registered as live before the lock is released, so an isolate
// can never come into existence across (or after) process teardown.
func NewIsolate() (*Isolate, error) {
	if err := requireInitialized(); err != nil {
		return nil, err
	}
	// Pin the goroutine to its OS thread BEFORE the engine allocates the
	// isolate so every subsequent engine call on this goroutine lands on the
	// same thread.
	runtime.LockOSThread()
	tid := currentThreadID()
	if err := beginIsolateCreate(); err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}
	h, err := callHandle("Isolate.New", proc("gov8_isolate_new"))
	if err != nil {
		abandonIsolateCreate()
		runtime.UnlockOSThread()
		return nil, err
	}
	iso := &Isolate{handle: h, tid: tid}
	finishIsolateCreate(iso)
	return iso, nil
}

// check validates isolate state and thread affinity. The lock-protected
// fields are read under mu; the thread-id comparison must run before callers
// touch any child wrapper state so foreign-thread misuse never races on it.
func (i *Isolate) check() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return fmt.Errorf("gov8: isolate used after Close")
	}
	if currentThreadID() != i.tid {
		return fmt.Errorf("gov8: isolate thread affinity violated: isolate is bound to thread %s, called from thread %s",
			quoteThreadID(i.tid), quoteThreadID(currentThreadID()))
	}
	return nil
}

// Close disposes the isolate and releases the owning goroutine's OS-thread
// lock. It must be called from the goroutine that created the isolate.
func (i *Isolate) Close() error {
	i.mu.Lock()
	if currentThreadID() != i.tid {
		i.mu.Unlock()
		return fmt.Errorf("gov8: isolate Close called from wrong thread (owner %s, caller %s)",
			quoteThreadID(i.tid), quoteThreadID(currentThreadID()))
	}
	if i.closed {
		i.mu.Unlock()
		return fmt.Errorf("gov8: isolate already closed")
	}
	if err := requireInitialized(); err != nil {
		i.mu.Unlock()
		return err
	}
	// Inspector and session wrappers retain native objects that refer to this
	// isolate. Reject before the native dispose; cleanup after disposal would
	// have to dereference dead V8 state.
	if err := inspectorIsolateCloseError(i); err != nil {
		i.mu.Unlock()
		return err
	}
	if err := inspectorInspectableIsolateCloseError(i); err != nil {
		i.mu.Unlock()
		return err
	}
	if err := heapSnapshotIsolateCloseError(i); err != nil {
		i.mu.Unlock()
		return err
	}
	disposedHandle := i.handle
	r1, _, _ := proc("gov8_isolate_dispose").Call(disposedHandle)
	// V8 has now torn down the default cppgc heap and cleared its persistent
	// nodes. Destroy any still-live native wrapper handles on the same owner
	// thread before publishing the isolate as closed.
	cppgcPersistentCleanupErr := afterCppGCPersistentIsolateDispose(i, disposedHandle)
	var fastAPICleanupErr error
	if fastAPIIsolateTracked(i) {
		// V8 retains the raw CFunction overload array and nested type metadata
		// through FunctionTemplate lifetime. Release its native-owned copy only
		// after isolate disposal, using disposedHandle as an opaque map key.
		fastAPICleanupErr = afterFastAPIIsolateDispose(i, disposedHandle)
	}
	if i.advancedCounterHandle != 0 || i.advancedExternalReferences {
		_, _, _ = proc("gov8_ia_after_isolate_dispose").Call(disposedHandle)
		dropIsolateCounter(i.advancedCounterHandle)
		i.advancedCounterHandle = 0
		i.advancedExternalReferences = false
	}
	i.closed = true
	i.handle = 0
	i.mu.Unlock()
	// The engine isolate no longer exists at this point; drop it from the
	// live set (also on the error path: the wrapper will never use the
	// handle again, so it must not wedge a later Dispose).
	unregisterIsolate(i)
	runtime.UnlockOSThread()
	if int64(r1) < 0 {
		return shimError("Isolate.Close", r1)
	}
	if fastAPICleanupErr != nil {
		return fastAPICleanupErr
	}
	if cppgcPersistentCleanupErr != nil {
		return cppgcPersistentCleanupErr
	}
	return nil
}

// handle returns the raw shim isolate pointer after affinity checks.
func (i *Isolate) handleChecked() (uintptr, error) {
	if err := i.check(); err != nil {
		return 0, err
	}
	if err := requireInitialized(); err != nil {
		return 0, err
	}
	return i.handle, nil
}

// handleAssumingCheck returns the raw engine isolate handle for code that
// already validated lifecycle state and thread affinity in the same
// operation (via check, directly or through a child wrapper's check). Close
// — the only writer of handle — runs on the owner thread behind the same
// affinity proof this operation just passed, so it cannot interleave: the
// unsynchronized read is exact. This exists because the hot paths previously
// ran the full check twice per operation (once on the wrapper, once inside
// handleChecked); the mutex round-trip and the extra thread-id read are a
// measurable share of small value operations.
func (i *Isolate) handleAssumingCheck() uintptr {
	return i.handle
}
