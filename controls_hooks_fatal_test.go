//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	gov8 "github.com/maclof/gov8"
)

// Engine-fatal controls/hooks paths, characterized out-of-process exactly
// like rust-oracle/tests/controls_hooks_negative.rs: fatal CHECK failures
// and the controlled fatal OOM abort the process by design, so every probe
// below runs in a dedicated subprocess (this test binary re-invoked with a
// GOV8_CH_PROBE marker, the Go analog of the oracle's current_exe probes).
//
// Every heap-pressure subprocess caps its heap with
// NewIsolateWithLimits(0, 10 MiB): the OOMs here are the intended, bounded
// fatal path, never uncontrolled process growth.
//
// Environment notes (pinned build, x86_64-pc-windows-msvc, v8 =152.2.0):
//   - fatal CHECK failures abort with STATUS_BREAKPOINT, observed by
//     ExitCode as -2147483645 (0x80000003);
//   - the API fatal handler fires for the flags-freeze CHECK and the OOM
//     path, but NOT for the "Must use --expose-gc" FATAL;
//   - unrecognized V8 flags print to the engine's stderr (pre-init only)
//     and are otherwise ignored.

// exitStatusBreakpoint is the Windows STATUS_BREAKPOINT code (0x80000003)
// V8 aborts with, in Go's ExitCode representation (the unsigned 32-bit
// code, matching the repo-wide convention pinned in host_callback_test.go;
// Rust's ExitStatus reports the same code sign-extended as -2147483645).
const exitStatusBreakpoint = 2147483651

// chHeapWorkload is the shared heap-pressure loop body (the oracle's
// run_until_oom workload).
const chHeapWorkload = "\"hello world\"\n  .repeat(10)\n  .split(\"w\")\n" +
	"  .map((s) => s.repeat(100).split(\"o\"))\n"

// chHeapLoop evaluates the workload until the engine gives up (OOM). The
// heap ceiling makes this the bounded, intended fatal path.
func chHeapLoop(t *testing.T, r *chRuntime) {
	t.Helper()
	for i := 0; i < 1_000_000; i++ {
		if _, ok := r.eval(chHeapWorkload); !ok {
			return
		}
	}
}

// runCHProbe spawns this test binary for one probe and returns its combined
// output plus the exit code.
func runCHProbe(t *testing.T, probeName string) (string, int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run", "^"+probeName+"$", "-test.v=false")
	cmd.Env = append(os.Environ(), "GOV8_CH_PROBE="+probeName)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("probe %s: %v (output:\n%s)", probeName, err, out)
		}
	}
	return string(out), code
}

// chProbe marks the probe body: true when this process is the child. Every
// probe installs the raw-abort exit filter before touching the engine.
func chProbe(t *testing.T, name string) bool {
	t.Helper()
	if os.Getenv("GOV8_CH_PROBE") != name {
		return false
	}
	chRawAbortExit()
	return true
}

// chFatalHandler is the shared observing fatal handler used by probes that
// install one (same output shape as the oracle's fatal_handler).
func chFatalHandler(file string, line int32, message string) {
	fmt.Fprintf(os.Stderr, "FATAL file=%q line=%d message=%q\n", file, line, message)
}

// wordToPtr converts an engine word to a pointer using the module-wide
// vet-clean idiom (the word round-trips through a local's address).
func wordToPtrCH(w uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&w))
}

// chRawAbortExit registers a first-chance vectored exception handler that
// terminates the process with the raw STATUS_BREAKPOINT code. A plain
// C++/Rust binary dies this way (the pinned oracle); the Go runtime's own
// vectored handler would otherwise intercept the engine's int3, dump all
// goroutines, and exit(2), masking the code this slice pins. Probe
// subprocesses only: they exist to observe the fatal path, so preempting
// the runtime's crash handler for the breakpoint exception is safe here.
// Every other exception continues to the runtime untouched.
func chRawAbortExit() {
	var (
		kernel32 = syscall.NewLazyDLL("kernel32.dll")
		addVEH   = kernel32.NewProc("AddVectoredExceptionHandler")
		exitProc = kernel32.NewProc("ExitProcess")
	)
	filter := syscall.NewCallback(func(excPointers uintptr) uintptr {
		// EXCEPTION_POINTERS { ExceptionRecord *; ContextRecord * }; the
		// record starts with the 4-byte ExceptionCode.
		record := *(*uintptr)(wordToPtrCH(excPointers))
		code := *(*uint32)(wordToPtrCH(record))
		if code == 0x80000003 { // STATUS_BREAKPOINT (V8 IMMEDIATE_CRASH int3)
			exitProc.Call(uintptr(code))
		}
		return 0 // EXCEPTION_CONTINUE_SEARCH for everything else
	})
	addVEH.Call(1, filter)
}

// --- prepare-stack callback result validation ------------------------------------

// TestPrepareStackTraceInvalidResultIsFatal proves that a Go callback cannot
// hand V8 a Local slot captured from a different HandleScope. rusty_v8's
// callback lifetime makes this shape unrepresentable; Go rejects it at the
// synchronous host boundary and aborts rather than returning into V8.
func TestPrepareStackTraceInvalidResultIsFatal(t *testing.T) {
	for _, probe := range []struct {
		name       string
		diagnostic string
	}{
		{"TestProbePrepareStackTraceWrongScope", "value is not owned by the callback scope"},
		{"TestProbePrepareStackTraceNoResult", "callback returned no value"},
	} {
		out, code := runCHProbe(t, probe.name)
		if !strings.Contains(out, "MARK:before-stack-access") {
			t.Fatalf("%s: marker missing; output:\n%s", probe.name, out)
		}
		if !strings.Contains(out, "invalid prepare stack trace callback result: "+probe.diagnostic) {
			t.Fatalf("%s: validation diagnostic missing; output:\n%s", probe.name, out)
		}
		if strings.Contains(out, "MARK:after-stack-access") {
			t.Fatalf("%s: invalid callback result returned into V8; output:\n%s", probe.name, out)
		}
		const callbackAbort = 3221226505 // 0xC0000409, gov8_host_panic_abort
		if code != callbackAbort {
			t.Fatalf("%s: exit code = %d, want %d (callback fail-fast); output:\n%s", probe.name, code, callbackAbort, out)
		}
	}
}

func TestProbePrepareStackTraceWrongScope(t *testing.T) {
	if !chProbe(t, "TestProbePrepareStackTraceWrongScope") {
		t.Skip("probe body")
	}
	r := newCHRuntime(t)
	outer, err := r.scope.Int32(42)
	if err != nil {
		t.Fatalf("outer Int32: %v", err)
	}
	if err := r.iso.SetPrepareStackTraceCallback(func(*gov8.Scope, gov8.Value, gov8.Value) (gov8.Value, bool) {
		return outer, true
	}); err != nil {
		t.Fatalf("SetPrepareStackTraceCallback: %v", err)
	}
	fmt.Fprintln(os.Stderr, "MARK:before-stack-access")
	_, _ = r.eval("new Error('wrong-scope').stack")
	fmt.Fprintln(os.Stderr, "MARK:after-stack-access")
}

func TestProbePrepareStackTraceNoResult(t *testing.T) {
	if !chProbe(t, "TestProbePrepareStackTraceNoResult") {
		t.Skip("probe body")
	}
	r := newCHRuntime(t)
	if err := r.iso.SetPrepareStackTraceCallback(func(*gov8.Scope, gov8.Value, gov8.Value) (gov8.Value, bool) {
		return gov8.Value{}, false
	}); err != nil {
		t.Fatalf("SetPrepareStackTraceCallback: %v", err)
	}
	fmt.Fprintln(os.Stderr, "MARK:before-stack-access")
	_, _ = r.eval("new Error('no-result').stack")
	fmt.Fprintln(os.Stderr, "MARK:after-stack-access")
}

// --- frozen flags ---------------------------------------------------------------

// TestFrozenFlagsMutationIsFatal mirrors post_init_flag_value_change_is_fatal:
// changing a flag value after Initialize trips the frozen-flags CHECK; the
// registered handler observes (file="", line=0, "Check failed:
// !IsFrozen().") and the process aborts with STATUS_BREAKPOINT.
func TestFrozenFlagsMutationIsFatal(t *testing.T) {
	out, code := runCHProbe(t, "TestProbeFrozenFlags")
	if !strings.Contains(out, "MARK:before-flag-change") {
		t.Fatalf("marker missing; output:\n%s", out)
	}
	if strings.Contains(out, "probe:survived") {
		t.Fatalf("flag mutation survived; output:\n%s", out)
	}
	if !strings.Contains(out, "FATAL ") {
		t.Fatalf("fatal handler not invoked; output:\n%s", out)
	}
	if !strings.Contains(out, `FATAL file="" line=0`) {
		t.Fatalf("handler file/line mismatch; output:\n%s", out)
	}
	if !strings.Contains(out, `message="Check failed: !IsFrozen()."`) {
		t.Fatalf("handler message mismatch; output:\n%s", out)
	}
	if !strings.Contains(out, "# Fatal error") {
		t.Fatalf("fatal banner missing; output:\n%s", out)
	}
	if code != exitStatusBreakpoint {
		t.Fatalf("exit code = %d, want %d (STATUS_BREAKPOINT); output:\n%s",
			code, exitStatusBreakpoint, out)
	}
}

// TestProbeFrozenFlags is the child of TestFrozenFlagsMutationIsFatal. The
// gov8 test binary initializes V8 without --expose-gc, so "--expose-gc" is
// a value-CHANGING post-init write (the exact frozen-flags trigger).
func TestProbeFrozenFlags(t *testing.T) {
	if !chProbe(t, "TestProbeFrozenFlags") {
		t.Skip("probe body")
	}
	if err := gov8.SetFatalErrorHandler(chFatalHandler); err != nil {
		t.Fatalf("SetFatalErrorHandler: %v", err)
	}
	fmt.Fprintln(os.Stderr, "MARK:before-flag-change")
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		t.Fatalf("SetFlagsFromString: %v", err)
	}
	fmt.Fprintln(os.Stderr, "MARK:after-flag-change")
	fmt.Println("probe:survived")
}

// --- GC request without --expose-gc ----------------------------------------------

// TestGcRequestWithoutExposeGcIsFatal mirrors
// gc_request_without_expose_gc_is_fatal_for_full_and_minor: both collection
// kinds fail the fatal CHECK and abort; the API fatal handler is NOT
// invoked at this site (site-specific engine behavior).
func TestGcRequestWithoutExposeGcIsFatal(t *testing.T) {
	for _, kind := range []struct {
		probe string
		gct   gov8.GarbageCollectionType
	}{{"TestProbeGcFull", gov8.GcFull}, {"TestProbeGcMinor", gov8.GcMinor}} {
		out, code := runCHProbe(t, kind.probe)
		if !strings.Contains(out, "MARK:before-gc-request") {
			t.Fatalf("%s: marker missing; output:\n%s", kind.probe, out)
		}
		if strings.Contains(out, "probe:survived") {
			t.Fatalf("%s: gc request survived; output:\n%s", kind.probe, out)
		}
		if !strings.Contains(out, "# Fatal error in v8::Isolate::RequestGarbageCollectionForTesting") {
			t.Fatalf("%s: fatal function banner missing; output:\n%s", kind.probe, out)
		}
		if !strings.Contains(out, "# Must use --expose-gc") {
			t.Fatalf("%s: fatal message missing; output:\n%s", kind.probe, out)
		}
		// The handler is installed but must NOT fire at this site.
		if strings.Contains(out, "FATAL file=") {
			t.Fatalf("%s: API fatal handler unexpectedly invoked; output:\n%s", kind.probe, out)
		}
		if code != exitStatusBreakpoint {
			t.Fatalf("%s: exit code = %d, want %d; output:\n%s", kind.probe, code, exitStatusBreakpoint, out)
		}
	}
}

// TestProbeGcFull / TestProbeGcMinor are the children of
// TestGcRequestWithoutExposeGcIsFatal.
func TestProbeGcFull(t *testing.T) {
	if !chProbe(t, "TestProbeGcFull") {
		t.Skip("probe body")
	}
	chGcProbeBody(t, gov8.GcFull)
}

func TestProbeGcMinor(t *testing.T) {
	if !chProbe(t, "TestProbeGcMinor") {
		t.Skip("probe body")
	}
	chGcProbeBody(t, gov8.GcMinor)
}

func chGcProbeBody(t *testing.T, gct gov8.GarbageCollectionType) {
	t.Helper()
	if err := gov8.SetFatalErrorHandler(chFatalHandler); err != nil {
		t.Fatalf("SetFatalErrorHandler: %v", err)
	}
	// The test binary's Initialize does NOT set --expose-gc (unlike the
	// conformance runner), so this request hits the fatal CHECK.
	r := newCHRuntime(t)
	fmt.Fprintln(os.Stderr, "MARK:before-gc-request")
	if err := r.iso.RequestGarbageCollectionForTesting(gct); err != nil {
		t.Fatalf("RequestGarbageCollectionForTesting: %v", err)
	}
	fmt.Fprintln(os.Stderr, "MARK:after-gc-request")
	fmt.Println("probe:survived")
}

// --- near-heap-limit shrink -> controlled OOM -------------------------------------

// TestNearHeapLimitShrinkForcesControlledOom mirrors
// shrinking_near_heap_limit_callback_forces_controlled_oom: a callback that
// halves the limit forces the controlled fatal OOM; the OOM handler
// observes the heap-OOM details before the abort.
func TestNearHeapLimitShrinkForcesControlledOom(t *testing.T) {
	out, code := runCHProbe(t, "TestProbeNearHeapLimitShrink")
	if !strings.Contains(out, `SHRINK call=1 current=4194304`) {
		t.Fatalf("shrink observation missing; output:\n%s", out)
	}
	if !strings.Contains(out, `OOM location="Reached heap limit" is_heap_oom=true`) {
		t.Fatalf("OOM observation missing; output:\n%s", out)
	}
	if !strings.Contains(out, `detail=""`) {
		t.Fatalf("OOM detail must be empty on the heap-OOM path; output:\n%s", out)
	}
	if strings.Contains(out, "probe:survived") {
		t.Fatalf("shrunk limit must end in fatal OOM; output:\n%s", out)
	}
	if code != exitStatusBreakpoint {
		t.Fatalf("exit code = %d, want %d; output:\n%s", code, exitStatusBreakpoint, out)
	}
}

// TestProbeNearHeapLimitShrink is the child of
// TestNearHeapLimitShrinkForcesControlledOom.
func TestProbeNearHeapLimitShrink(t *testing.T) {
	if !chProbe(t, "TestProbeNearHeapLimitShrink") {
		t.Skip("probe body")
	}
	if err := gov8.SetFatalErrorHandler(chFatalHandler); err != nil {
		t.Fatalf("SetFatalErrorHandler: %v", err)
	}
	iso, err := gov8.NewIsolateWithLimits(0, 10<<20)
	if err != nil {
		t.Fatalf("NewIsolateWithLimits: %v", err)
	}
	if err := iso.SetOOMErrorHandler(func(location, detail string, isHeapOOM bool) {
		fmt.Fprintf(os.Stderr, "OOM location=%q is_heap_oom=%v detail=%q\n", location, isHeapOOM, detail)
	}); err != nil {
		t.Fatalf("SetOOMErrorHandler: %v", err)
	}
	calls := 0
	if err := iso.AddNearHeapLimitCallback(func(current, initial uint64) uint64 {
		calls++
		fmt.Fprintf(os.Stderr, "SHRINK call=%d current=%d\n", calls, current)
		return current / 2
	}); err != nil {
		t.Fatalf("AddNearHeapLimitCallback: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	chHeapLoop(t, &chRuntime{t: t, iso: iso, ctx: ctx, scope: scope})
	fmt.Println("probe:survived")
}

// --- default OOM path (no handlers) -------------------------------------------------

// TestOOMWithoutHandlersUsesDefaultFatalPath mirrors
// heap_oom_without_handlers_uses_default_fatal_path: no handler markers,
// the default fatal banner, STATUS_BREAKPOINT.
func TestOOMWithoutHandlersUsesDefaultFatalPath(t *testing.T) {
	out, code := runCHProbe(t, "TestProbeOOMDefault")
	if !strings.Contains(out, "MARK:before-loop") {
		t.Fatalf("marker missing; output:\n%s", out)
	}
	if !strings.Contains(out, "# Fatal JavaScript out of memory: Reached heap limit") {
		t.Fatalf("default fatal banner missing; output:\n%s", out)
	}
	if strings.Contains(out, "OOM location=") {
		t.Fatalf("no OOM handler was installed; output:\n%s", out)
	}
	if strings.Contains(out, "FATAL file=") {
		t.Fatalf("no fatal handler was installed; output:\n%s", out)
	}
	if strings.Contains(out, "probe:survived") {
		t.Fatalf("default OOM path must abort; output:\n%s", out)
	}
	if code != exitStatusBreakpoint {
		t.Fatalf("exit code = %d, want %d; output:\n%s", code, exitStatusBreakpoint, out)
	}
}

// TestProbeOOMDefault is the child of
// TestOOMWithoutHandlersUsesDefaultFatalPath.
func TestProbeOOMDefault(t *testing.T) {
	if !chProbe(t, "TestProbeOOMDefault") {
		t.Skip("probe body")
	}
	iso, err := gov8.NewIsolateWithLimits(0, 10<<20)
	if err != nil {
		t.Fatalf("NewIsolateWithLimits: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	fmt.Fprintln(os.Stderr, "MARK:before-loop")
	chHeapLoop(t, &chRuntime{t: t, iso: iso, ctx: ctx, scope: scope})
	fmt.Fprintln(os.Stderr, "MARK:after-loop")
	fmt.Println("probe:survived")
}
