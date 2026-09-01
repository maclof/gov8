//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Promise callback panics mirror rusty_v8's non-unwinding extern-C
// boundary. Each probe re-invokes this test binary because fail-fast abort
// intentionally terminates the process.
const promisePanicAbortCode = 3221226505 // 0xC0000409 fail-fast

func runPromisePanicProbe(t *testing.T, probe, entered, panicDiagnostic, after string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run", "^"+probe+"$", "-test.count=1", "-test.v=false")
	cmd.Env = append(os.Environ(), "GOV8_PROMISE_PANIC_PROBE="+probe)
	out, err := cmd.CombinedOutput()
	combined := string(out)

	for _, marker := range []string{"marker:promise-panic-before", entered, panicDiagnostic} {
		if !strings.Contains(combined, marker) {
			t.Errorf("%s: missing %q; output:\n%s", probe, marker, combined)
		}
	}
	if strings.Contains(combined, after) {
		t.Errorf("%s: execution returned past panicking callback; output:\n%s", probe, combined)
	}
	if err == nil {
		t.Fatalf("%s: process exited cleanly; output:\n%s", probe, combined)
	}
	var exitErr *exec.ExitError
	if !asExitError(err, &exitErr) {
		t.Fatalf("%s: expected ExitError, got %T: %v", probe, err, err)
	}
	if got := exitErr.ExitCode(); got != promisePanicAbortCode {
		t.Errorf("%s: exit code = %d, want %d (0xC0000409); output:\n%s",
			probe, got, promisePanicAbortCode, combined)
	}
}

func promisePanicProbe(t *testing.T, name string) bool {
	t.Helper()
	return os.Getenv("GOV8_PROMISE_PANIC_PROBE") == name
}

func TestNativePromiseHandlerPanicAbortsProcess(t *testing.T) {
	runPromisePanicProbe(
		t,
		"TestProbeNativePromiseHandlerPanic",
		"marker:promise-native-entered",
		"gov8: panic in native promise handler: promise-native-handler-panic",
		"marker:promise-native-after-checkpoint",
	)
}

func TestPromiseRejectCallbackPanicAbortsProcess(t *testing.T) {
	runPromisePanicProbe(
		t,
		"TestProbePromiseRejectCallbackPanic",
		"marker:promise-reject-entered",
		"gov8: panic in promise reject callback: promise-reject-callback-panic",
		"marker:promise-reject-after-reject",
	)
}

func TestProbeNativePromiseHandlerPanic(t *testing.T) {
	if !promisePanicProbe(t, "TestProbeNativePromiseHandlerPanic") {
		t.Skip("probe body")
	}
	rt := newPromiseRT(t)
	resolver, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(rt.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}
	handler, err := rt.scope.NewNativeFunction(rt.ctx, func([]gov8.Value) (gov8.Value, bool) {
		fmt.Fprintln(os.Stderr, "marker:promise-native-entered")
		panic("promise-native-handler-panic")
	})
	if err != nil {
		t.Fatalf("NewNativeFunction: %v", err)
	}
	if _, err := promise.Then(rt.ctx, handler.Value()); err != nil {
		t.Fatalf("Then: %v", err)
	}
	undefined, err := rt.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	if ok, err := resolver.Resolve(rt.ctx, undefined); err != nil || !ok {
		t.Fatalf("Resolve = (%v, %v), want (true, nil)", ok, err)
	}
	fmt.Fprintln(os.Stderr, "marker:promise-panic-before")
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("PerformMicrotaskCheckpoint: %v", err)
	}
	fmt.Fprintln(os.Stderr, "marker:promise-native-after-checkpoint")
}

func TestProbePromiseRejectCallbackPanic(t *testing.T) {
	if !promisePanicProbe(t, "TestProbePromiseRejectCallbackPanic") {
		t.Skip("probe body")
	}
	rt := newPromiseRT(t)
	if err := rt.iso.SetPromiseRejectCallback(rt.scope, func(gov8.PromiseRejectMessage) {
		fmt.Fprintln(os.Stderr, "marker:promise-reject-entered")
		panic("promise-reject-callback-panic")
	}); err != nil {
		t.Fatalf("SetPromiseRejectCallback: %v", err)
	}
	resolver, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	undefined, err := rt.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	fmt.Fprintln(os.Stderr, "marker:promise-panic-before")
	if ok, err := resolver.Reject(rt.ctx, undefined); err != nil || !ok {
		t.Fatalf("Reject = (%v, %v), want (true, nil)", ok, err)
	}
	fmt.Fprintln(os.Stderr, "marker:promise-reject-after-reject")
}
