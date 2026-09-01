//go:build windows && amd64

package gov8_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	gov8 "github.com/maclof/gov8"
)

// Termination tests, mirroring the pinned Rust oracle's
// terminate/request_and_cancel_during_js check and the dedicated
// terminate_from_other_thread process (rust-oracle/tests/
// terminate_from_other_thread.rs).

// termRequestCb is the native callback that records the termination flag
// around its own request and stores both on JS globals (Rust writes them
// from the callback because captures are unavailable there; Go records the
// observed values in the closure and mirrors the globals for parity).
func termRequestCb(t *testing.T, ctx *gov8.Context, before, after *bool) gov8.FunctionCallback {
	return func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		iso := cs.Isolate()
		sc := cs.Scope()

		flagBefore, err := iso.IsExecutionTerminating()
		if err != nil {
			t.Errorf("callback IsExecutionTerminating: %v", err)
			return
		}
		if err := iso.TerminateExecution(); err != nil {
			t.Errorf("callback TerminateExecution: %v", err)
			return
		}
		flagAfter, err := iso.IsExecutionTerminating()
		if err != nil {
			t.Errorf("callback IsExecutionTerminating 2: %v", err)
			return
		}
		*before = flagBefore
		*after = flagAfter

		global, err := ctx.GlobalObject(sc)
		if err != nil {
			t.Errorf("callback GlobalObject: %v", err)
			return
		}
		b1, err := sc.Boolean(flagBefore)
		if err != nil {
			t.Errorf("callback Boolean: %v", err)
			return
		}
		if _, err := global.SetByName(sc, ctx, "__termFlagBefore", b1); err != nil {
			t.Errorf("callback set __termFlagBefore: %v", err)
		}
		b2, err := sc.Boolean(flagAfter)
		if err != nil {
			t.Errorf("callback Boolean 2: %v", err)
			return
		}
		if _, err := global.SetByName(sc, ctx, "__termFlagAfter", b2); err != nil {
			t.Errorf("callback set __termFlagAfter: %v", err)
		}
	}
}

// TestTerminateRequestAndCancelDuringJS mirrors
// terminate/request_and_cancel_during_js: the request is delivered at the
// next interrupt check (not synchronously inside the native callback), the
// interrupted script reports through the TryCatch, the flag is cleared
// after the abort, and cancellation restores the isolate.
func TestTerminateRequestAndCancelDuringJS(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	handle := iso.ThreadSafeHandle()

	var flagBefore, flagAfter bool
	request, err := iso.NewFunction(scope, ctx, termRequestCb(t, ctx, &flagBefore, &flagAfter), nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if ok, err := global.SetByName(scope, ctx, "__requestTerminate", request.Value); err != nil || !ok {
		t.Fatalf("seed __requestTerminate: ok=%v err=%v", ok, err)
	}

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}

	// The script must keep running for the request to land at the loop
	// back-edge interrupt check.
	script, err := ctx.Compile(scope, "__requestTerminate(); while (true) { }", tc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, runErr := script.Run(scope, tc)
	_ = script.Close()
	ranOK := runErr == nil

	hasCaught, err := tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	canContinue, err := tc.CanContinue()
	if err != nil {
		t.Fatalf("CanContinue: %v", err)
	}
	hasTerminated, err := tc.HasTerminated()
	if err != nil {
		t.Fatalf("HasTerminated: %v", err)
	}
	// Closing the TryCatch clears the pending termination exception from
	// the isolate (v8 ~TryCatch/Reset); the pinned runner reads the flag
	// after its TryCatch is gone.
	if err := tc.Close(); err != nil {
		t.Fatalf("tc.Close: %v", err)
	}
	// Once the termination exception fully unwound to the embedder, V8 has
	// already cleared the terminate flag.
	flagAfterAbort, err := iso.IsExecutionTerminating()
	if err != nil {
		t.Fatalf("IsExecutionTerminating: %v", err)
	}
	cancelled := handle.CancelTerminateExecution()
	idleAgain := handle.IsExecutionTerminating()

	flagBeforeText := snapEvalText(t, ctx, scope, "String(__termFlagBefore)")
	flagAfterText := snapEvalText(t, ctx, scope, "String(__termFlagAfter)")
	next := snapEvalText(t, ctx, scope, "String(7 * 6)")

	if flagBefore {
		t.Error("flag must be false before the request")
	}
	if flagAfter {
		t.Error("flag must still be false right after the request (delivery is asynchronous)")
	}
	if ranOK {
		t.Error("terminated script must not complete")
	}
	if !hasCaught {
		t.Error("termination must surface in the TryCatch")
	}
	if canContinue {
		t.Error("termination is not recoverable in-trycatch")
	}
	if !hasTerminated {
		t.Error("TryCatch must report terminated")
	}
	if flagAfterAbort {
		t.Error("terminate flag must be cleared after the abort")
	}
	if !cancelled {
		t.Error("cancel_terminate_execution rejected")
	}
	if idleAgain {
		t.Error("isolate must be idle after cancellation")
	}
	if flagBeforeText != "false" || flagAfterText != "false" {
		t.Errorf("flag globals = %q, %q; want false, false", flagBeforeText, flagAfterText)
	}
	if next != "42" {
		t.Errorf("isolate must be reusable after cancellation: %q", next)
	}
}

// TestSubprocessTerminateLoopFromOtherThread runs the cross-thread
// termination scenario in a dedicated subprocess (own process like the
// pinned tests/terminate_from_other_thread.rs): a foreign goroutine
// requests termination through a cloned thread-safe handle while a tight
// JS loop runs; the loop reports through the TryCatch, cancellation
// restores the isolate, and a follow-up script evaluates normally. The
// interrupt flag persists until delivered, so the outcome is deterministic.
func TestSubprocessTerminateLoopFromOtherThread(t *testing.T) {
	if os.Getenv("GOV8_TEST_SUBPROCESS") == "terminate-loop" {
		runTerminateLoopSubprocess(t)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSubprocessTerminateLoopFromOtherThread$")
	cmd.Env = append(os.Environ(), "GOV8_TEST_SUBPROCESS=terminate-loop")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, out)
	}
	line := extractJSONLine(string(out), `{"mode":"terminate-loop"`)
	want := `{"mode":"terminate-loop","requested":true,"ran_ok":false,` +
		`"has_caught":true,"can_continue":false,"cancel_ok":true,"reused":"42"}`
	if line != want {
		t.Fatalf("termination report diverged:\n want: %s\n got:  %s", want, line)
	}
}

func runTerminateLoopSubprocess(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	handle := iso.ThreadSafeHandle()
	// One copy moves to the terminating goroutine, one stays with the host
	// for the cancellation afterwards (the pinned handle is Clone; Go
	// handles are shared values).
	terminatorHandle := iso.ThreadSafeHandle()

	requested := false
	done := make(chan bool, 1)
	go func() {
		// The flag persists until delivered (or cancelled), so this sleep
		// only bounds the total time; the outcome is identical either way.
		time.Sleep(100 * time.Millisecond)
		done <- terminatorHandle.TerminateExecution()
	}()

	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	script, err := ctx.Compile(scope, "while (true) { }", tc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, runErr := script.Run(scope, tc)
	_ = script.Close()
	ranOK := runErr == nil
	requested = <-done

	hasCaught, _ := tc.HasCaught()
	canContinue, _ := tc.CanContinue()
	_ = tc.Close()

	cancelled := handle.CancelTerminateExecution()
	reused := ""
	func() {
		s2, err := iso.NewScope()
		if err != nil {
			t.Fatalf("scope 2: %v", err)
		}
		defer func() { _ = s2.Close() }()
		reused = snapEvalText(t, ctx, s2, "String(40 + 2)")
	}()

	report := `{"mode":"terminate-loop","requested":` + b2sJSON(requested) +
		`,"ran_ok":` + b2sJSON(ranOK) +
		`,"has_caught":` + b2sJSON(hasCaught) +
		`,"can_continue":` + b2sJSON(canContinue) +
		`,"cancel_ok":` + b2sJSON(cancelled) +
		`,"reused":"` + reused + `"}`
	ok := requested && !ranOK && hasCaught && !canContinue && cancelled && reused == "42"
	println(report)
	_ = scope.Close()
	_ = ctx.Close()
	_ = iso.Close()
	if !ok {
		os.Exit(1)
	}
	os.Exit(0)
}

// TestThreadSafeHandleAfterIsolateClose pins the destroyed-isolate
// contract: all three handle methods answer false after the isolate was
// closed, from a foreign goroutine.
func TestThreadSafeHandleAfterIsolateClose(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	handle := iso.ThreadSafeHandle()
	if err := iso.Close(); err != nil {
		t.Fatalf("iso.Close: %v", err)
	}

	errCh := make(chan [3]bool, 1)
	go func() {
		errCh <- [3]bool{
			handle.TerminateExecution(),
			handle.CancelTerminateExecution(),
			handle.IsExecutionTerminating(),
		}
	}()
	got := <-errCh
	if got != [3]bool{false, false, false} {
		t.Errorf("handle after close = %v; want all false", got)
	}
}

// TestLowMemoryNotificationIsUsable exercises the forced-GC path on a live
// isolate (weak integration is covered by the handle tests).
func TestLowMemoryNotificationIsUsable(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	if _, err := eval(t, ctx, scope, "globalThis.a = 1;"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := iso.LowMemoryNotification(); err != nil {
			t.Fatalf("LowMemoryNotification: %v", err)
		}
	}
	if got := snapEvalText(t, ctx, scope, "String(a)"); got != "1" {
		t.Fatalf("isolate unusable after GC: %q", got)
	}
}
