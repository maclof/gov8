//go:build windows && amd64

package gov8_test

import (
	"sync"
	"testing"

	gov8 "gov8"
)

// Shared-isolate/Locker thread tests, mirroring the oracle's
// core-advanced/thread checks.

// lockedEval locks the shared isolate, runs source, and returns the int64
// completion value (-1 on failure). Mirrors the oracle's locked_eval.
func lockedEval(t *testing.T, shared *gov8.SharedIsolate, source string) int64 {
	t.Helper()
	locker, err := shared.Lock()
	if err != nil {
		t.Errorf("Lock: %v", err)
		return -1
	}
	defer func() { _ = locker.Close() }()
	iso := locker.Isolate()
	scope, err := iso.NewScope()
	if err != nil {
		t.Errorf("NewScope: %v", err)
		return -1
	}
	defer func() { _ = scope.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Errorf("NewContext: %v", err)
		return -1
	}
	defer func() { _ = ctx.Close() }()
	n, ok := evalInt(t, scope, ctx, source)
	if !ok {
		return -1
	}
	return n
}

// TestSharedIsolateCrossThreadLocks mirrors
// core-advanced/thread/shared_isolate_cross_thread_locks: two workers and
// the main thread serialize through one shared isolate.
func TestSharedIsolateCrossThreadLocks(t *testing.T) {
	iso := newIso(t)
	shared, err := iso.TryIntoShared()
	if err != nil {
		t.Fatalf("TryIntoShared: %v", err)
	}

	results := make(chan int64, 2)
	var wg sync.WaitGroup
	for _, source := range []string{"6 * 7", "20 + 2"} {
		source := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- lockedEval(t, shared, source)
		}()
	}
	wg.Wait()
	close(results)
	a, b := <-results, <-results
	workerResults := []int64{a, b}
	if workerResults[0] > workerResults[1] {
		workerResults[0], workerResults[1] = workerResults[1], workerResults[0]
	}
	if workerResults[0] != 22 || workerResults[1] != 42 {
		t.Errorf("worker results = %v, want [22 42]", workerResults)
	}

	if got := lockedEval(t, shared, "40 + 2"); got != 42 {
		t.Errorf("main result = %d, want 42", got)
	}
	if err := shared.Close(); err != nil {
		t.Fatalf("shared Close: %v", err)
	}
}

// TestSharedTerminateWhileLocked mirrors
// core-advanced/thread/shared_terminate_while_locked.
func TestSharedTerminateWhileLocked(t *testing.T) {
	iso := newIso(t)
	handle := iso.ThreadSafeHandle()
	shared, err := iso.TryIntoShared()
	if err != nil {
		t.Fatalf("TryIntoShared: %v", err)
	}

	locker, err := shared.Lock()
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	wiso := locker.Isolate()
	scope, err := wiso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ctx, err := wiso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	tc, err := wiso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}

	// Foreign-thread request while this thread holds the lock and is about
	// to run the endless loop; delivery happens at the loop's first
	// interrupt checkpoint.
	requested := make(chan bool, 1)
	go func() { requested <- handle.TerminateExecution() }()

	script, cerr := ctx.Compile(scope, "for (;;) {}", tc)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	_, runErr := script.Run(scope, tc)
	ran := runErr == nil
	stillTerminating := handle.IsExecutionTerminating()
	requestOK := <-requested

	caught, _ := tc.HasCaught()
	terminated, _ := tc.HasTerminated()
	canContinue, _ := tc.CanContinue()

	_ = tc.Close()
	_ = script.Close()
	_ = ctx.Close()
	_ = scope.Close()
	if err := locker.Close(); err != nil {
		t.Fatalf("locker Close: %v", err)
	}

	if !requestOK {
		t.Error("terminate request refused")
	}
	if ran {
		t.Error("endless loop completed")
	}
	if !caught {
		t.Error("mid-execution termination did not mark the trycatch caught")
	}
	if !terminated {
		t.Error("trycatch does not report terminated")
	}
	if canContinue {
		t.Error("can continue after termination")
	}
	if !stillTerminating {
		t.Error("terminating flag not visible right after the run")
	}

	if !handle.CancelTerminateExecution() {
		t.Error("cancel refused")
	}
	if got := lockedEval(t, shared, "40 + 2"); got != 42 {
		t.Errorf("recovered = %d, want 42", got)
	}
	if err := shared.Close(); err != nil {
		t.Fatalf("shared Close: %v", err)
	}
}

// TestIntoSharedRejections mirrors core-advanced/thread/into_shared_rejections:
// conversion rejections and recovery.
func TestIntoSharedRejections(t *testing.T) {
	// Part A: a second isolate entered on top rejects the conversion.
	bottom := newIso(t)
	top := newIso(t)
	shared, err := bottom.TryIntoShared()
	if err == nil {
		_ = shared.Close()
		t.Fatal("conversion with another isolate entered succeeded")
	}
	intoShared, ok := err.(*gov8.IntoSharedError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if intoShared.Kind != gov8.KindAnotherIsolateEntered {
		t.Fatalf("kind = %s, want another_isolate_entered", intoShared.Kind)
	}
	recoveredBottom := intoShared.IntoIsolate()
	if recoveredBottom != bottom {
		t.Fatal("recovered isolate differs")
	}
	// Reverse creation order teardown.
	if err := top.Close(); err != nil {
		t.Fatalf("top Close: %v", err)
	}
	if err := bottom.Close(); err != nil {
		t.Fatalf("bottom Close: %v", err)
	}

	// Part B: a live weak handle rejects; closing it allows conversion.
	// The finalizer variant is used because the Go-side liveness registry
	// counts finalizer registrations (a finalizer-less weak is invisible
	// to it; see the parity notes).
	owned := newIso(t)
	weakHolder := func() *gov8.Weak {
		scope := newScope(t, owned)
		defer func() { _ = scope.Close() }()
		ctx := newCtx(t, owned)
		defer func() { _ = ctx.Close() }()
		obj, err := scope.NewObject(ctx)
		if err != nil {
			t.Fatalf("NewObject: %v", err)
		}
		g, err := gov8.NewGlobal(scope, obj.Value)
		if err != nil {
			t.Fatalf("NewGlobal: %v", err)
		}
		w, err := g.NewWeakWithFinalizer(func(*gov8.Isolate) {})
		if err != nil {
			t.Fatalf("NewWeak: %v", err)
		}
		// The weak outlives the scope/context via the returned handle; the
		// global stays alive until the isolate closes (its cell is released
		// with the isolate).
		return w
	}()
	shared2, err := owned.TryIntoShared()
	if err == nil {
		_ = shared2.Close()
		t.Fatal("conversion with a live weak succeeded")
	}
	intoShared, ok = err.(*gov8.IntoSharedError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if intoShared.Kind != gov8.KindLiveWeakHandlesOrPendingFinalizers {
		t.Fatalf("kind = %s, want live_weak_handles_or_pending_finalizers", intoShared.Kind)
	}
	owned = intoShared.IntoIsolate()

	// Drop the weak; the retry must succeed.
	if err := weakHolder.Close(); err != nil {
		t.Fatalf("weak Close: %v", err)
	}
	shared2, err = owned.TryIntoShared()
	if err != nil {
		t.Fatalf("retry TryIntoShared: %v", err)
	}
	if got := lockedEval(t, shared2, "3 * 3"); got != 9 {
		t.Errorf("locked run after recovery = %d, want 9", got)
	}
	if err := shared2.Close(); err != nil {
		t.Fatalf("shared Close: %v", err)
	}
}

// TestLockerUnlockWindow mirrors core-advanced/thread/locker_unlock_window:
// the window lets a blocked worker run, then the same locker re-owns the
// isolate and runs scripts.
func TestLockerUnlockWindow(t *testing.T) {
	iso := newIso(t)
	shared, err := iso.TryIntoShared()
	if err != nil {
		t.Fatalf("TryIntoShared: %v", err)
	}

	started := make(chan struct{})
	var workerResult int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-started
		// Blocks in locked_eval until the main goroutine opens the window.
		workerResult = lockedEval(t, shared, "2 + 3")
	}()

	locker, err := shared.Lock()
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	close(started)

	var windowErr error
	if werr := locker.UnlockWindow(func() error {
		wg.Wait()
		return nil
	}); werr != nil {
		windowErr = werr
	}
	if windowErr != nil {
		t.Fatalf("unlock window: %v", windowErr)
	}
	if workerResult != 5 {
		t.Errorf("worker result = %d, want 5", workerResult)
	}

	// Back from the window: the same locker still owns the isolate.
	wiso := locker.Isolate()
	scope, err := wiso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ctx, err := wiso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	mainResult, ok := evalInt(t, scope, ctx, "1 + 1")
	_ = ctx.Close()
	_ = scope.Close()
	if err := locker.Close(); err != nil {
		t.Fatalf("locker Close: %v", err)
	}
	if !ok || mainResult != 2 {
		t.Errorf("main result after window = %d/%v, want 2", mainResult, ok)
	}
	if err := shared.Close(); err != nil {
		t.Fatalf("shared Close: %v", err)
	}
}
