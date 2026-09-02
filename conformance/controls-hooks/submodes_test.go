//go:build windows && amd64

// Subprocess mode bodies for the conformance-controls-hooks runner. This
// binary re-invokes itself with a GOV8_CH_MODE environment marker (the Go
// analog of the oracle binary's spawn_self modes). Each mode fully controls
// its own process-level V8 setup: TestMain skips the shared setup when the
// mode marker is present, so modes that need a different setup order (no
// --expose-gc, fatal handler installed before Initialize) are exact.
package main

import (
	"fmt"
	"os"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// TestCHSubMode is the child-process entry point. In the parent (no mode
// marker) it is a no-op skip.
func TestCHSubMode(t *testing.T) {
	mode := os.Getenv(gov8ModeEnv)
	if mode == "" {
		t.Skip("parent process: not a subprocess mode")
	}
	chRawAbortExit()
	modes := map[string]func(){
		"sub-run-all":                   subRunAll,
		"sub-near-heap-limit":           subNearHeapLimit,
		"sub-fatal-frozen-flags":        subFatalFrozenFlags,
		"sub-gc-without-expose-gc-full": func() { subGcWithoutExposeGc("full") },
		"sub-gc-without-expose-gc-minor": func() {
			subGcWithoutExposeGc("minor")
		},
		"sub-oom-fatal":              subOomFatal,
		"sub-near-heap-limit-shrink": subNearHeapLimitShrink,
		"sub-oom-default":            subOOMDefault,
		"sub-invalid-flag-preinit":   subInvalidFlagPreinit,
	}
	body, ok := modes[mode]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown mode: %s\n", mode)
		os.Exit(1)
	}
	body()
}

// subRunAll runs the full check registry in this fresh process and writes
// the rendered report to the path in GOV8_CH_REPORT_OUT (the parent tests
// compare it against the pinned fixture; see runFreshReport).
func subRunAll() {
	ensureSetup(modeTester)
	report := runAll(modeTester)
	out := os.Getenv("GOV8_CH_REPORT_OUT")
	if out == "" {
		fmt.Fprintln(os.Stderr, "GOV8_CH_REPORT_OUT not set")
		os.Exit(1)
	}
	if err := os.WriteFile(out, []byte(report), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(1)
	}
}

// modeTester satisfies tester inside mode subprocesses, where failures are
// reported to stderr and terminate the mode (the parent asserts the
// observable outcome from the captured output).
type modeT struct{}

func (modeT) Helper() {}
func (modeT) Fatalf(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "mode fatal: "+f+"\n", a...)
	os.Exit(1)
}
func (modeT) Errorf(f string, a ...interface{}) { fmt.Fprintf(os.Stderr, "mode error: "+f+"\n", a...) }

var modeTester tester = modeT{}

// subNearHeapLimit: two registrations; only the most recent callback is
// invoked; it doubles the limit exactly once, after which the JS loop
// stops. Prints the RESULT protocol line parsed by parseResultValues.
func subNearHeapLimit() {
	ensureModeSetup()
	firstCalls := 0
	secondCalls := 0
	secondInitial := 0
	secondCurrent := 0
	secondReturned := 0
	iso, err := gov8.NewIsolateWithLimits(0, 10<<20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewIsolateWithLimits: %v\n", err)
		os.Exit(1)
	}
	// The first registration is replaced: the engine keeps ONE slot, so the
	// replaced callback must never run.
	if err := iso.AddNearHeapLimitCallback(func(current, initial uint64) uint64 {
		firstCalls++
		return 0
	}); err != nil {
		fmt.Fprintf(os.Stderr, "AddNearHeapLimitCallback(first): %v\n", err)
		os.Exit(1)
	}
	if err := iso.AddNearHeapLimitCallback(func(current, initial uint64) uint64 {
		secondCalls++
		secondCurrent = int(current)
		secondInitial = int(initial)
		raised := current * 2
		secondReturned = int(raised)
		return raised
	}); err != nil {
		fmt.Fprintf(os.Stderr, "AddNearHeapLimitCallback(second): %v\n", err)
		os.Exit(1)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewContext: %v\n", err)
		os.Exit(1)
	}
	scope, err := iso.NewScope()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewScope: %v\n", err)
		os.Exit(1)
	}
	r := &runtime{iso: iso, ctx: ctx, scope: scope}
	for i := 0; i < 1_000_000; i++ {
		if _, ok := r.evalText(modeTester, chHeapWorkload); !ok {
			break
		}
		if secondCalls > 0 {
			break
		}
	}
	fmt.Printf("RESULT calls_first=%d calls_second=%d initial_limit_bytes=%d current_limit_bytes=%d returned_limit_bytes=%d\n",
		firstCalls, secondCalls, secondInitial, secondCurrent, secondReturned)
}

// ensureModeSetup runs the contractual setup without capturing observations
// (mode bodies have no expectations of their own).
func ensureModeSetup() {
	leftover, err := gov8.SetFlagsFromCommandLine([]string{
		"conformance-controls-hooks", "--log-colour", "--should-be-ignored",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SetFlagsFromCommandLine: %v\n", err)
		os.Exit(1)
	}
	_ = leftover
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		fmt.Fprintf(os.Stderr, "SetFlagsFromString: %v\n", err)
		os.Exit(1)
	}
	if err := gov8.SetEntropySource(entropyFill42); err != nil {
		fmt.Fprintf(os.Stderr, "SetEntropySource: %v\n", err)
		os.Exit(1)
	}
	if err := gov8.Initialize(); err != nil {
		fmt.Fprintf(os.Stderr, "Initialize: %v\n", err)
		os.Exit(1)
	}
}

// subFatalFrozenFlags: the fatal handler is registered BEFORE Initialize;
// the post-init value-changing flag write trips the frozen-flags CHECK; the
// handler observes it and V8 aborts.
func subFatalFrozenFlags() {
	if err := gov8.SetFatalErrorHandler(chFatalHandler); err != nil {
		fmt.Fprintf(os.Stderr, "SetFatalErrorHandler: %v\n", err)
		os.Exit(1)
	}
	ensureModeSetup()
	fmt.Fprintln(os.Stderr, "MARK:before-flag-change")
	// The flag set is frozen after Initialize(); only a value CHANGING write
	// trips the CHECK (a no-op write of the current value does not) --
	// --expose-gc was set above, so --no-expose-gc flips it.
	if err := gov8.SetFlagsFromString("--no-expose-gc"); err != nil {
		fmt.Fprintf(os.Stderr, "SetFlagsFromString: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "MARK:after-flag-change")
	fmt.Println("SURVIVED")
}

// subGcWithoutExposeGc: deliberately does NOT set --expose-gc; the request
// hits the fatal CHECK. The API fatal handler is registered (and must NOT
// fire at this site).
func subGcWithoutExposeGc(kind string) {
	if err := gov8.SetFatalErrorHandler(chFatalHandler); err != nil {
		fmt.Fprintf(os.Stderr, "SetFatalErrorHandler: %v\n", err)
		os.Exit(1)
	}
	if err := gov8.Initialize(); err != nil {
		fmt.Fprintf(os.Stderr, "Initialize: %v\n", err)
		os.Exit(1)
	}
	iso, err := gov8.NewIsolate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewIsolate: %v\n", err)
		os.Exit(1)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewContext: %v\n", err)
		os.Exit(1)
	}
	scope, err := iso.NewScope()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewScope: %v\n", err)
		os.Exit(1)
	}
	_ = ctx // the request is isolate-level; the context mirrors the oracle's setup
	_ = scope
	gct := gov8.GcFull
	if kind == "minor" {
		gct = gov8.GcMinor
	}
	fmt.Fprintln(os.Stderr, "MARK:before-gc-request")
	if err := iso.RequestGarbageCollectionForTesting(gct); err != nil {
		fmt.Fprintf(os.Stderr, "RequestGarbageCollectionForTesting: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "MARK:after-gc-request")
	fmt.Println("SURVIVED")
}

// subOomFatal: heap capped at 10 MiB; the OOM handler observes the details
// and the process fatal handler observes the post-OOM fatal; V8 aborts.
func subOomFatal() {
	if err := gov8.SetFatalErrorHandler(chFatalHandler); err != nil {
		fmt.Fprintf(os.Stderr, "SetFatalErrorHandler: %v\n", err)
		os.Exit(1)
	}
	ensureModeSetup()
	iso, err := gov8.NewIsolateWithLimits(0, 10<<20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewIsolateWithLimits: %v\n", err)
		os.Exit(1)
	}
	if err := iso.SetOOMErrorHandler(chOomHandler); err != nil {
		fmt.Fprintf(os.Stderr, "SetOOMErrorHandler: %v\n", err)
		os.Exit(1)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewContext: %v\n", err)
		os.Exit(1)
	}
	scope, err := iso.NewScope()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewScope: %v\n", err)
		os.Exit(1)
	}
	runUntilOOM(&runtime{iso: iso, ctx: ctx, scope: scope})
}

// subNearHeapLimitShrink (negative-test mode): the near-heap-limit callback
// deliberately halves the limit so the controlled fatal OOM is reached
// quickly.
func subNearHeapLimitShrink() {
	ensureModeSetup()
	if err := gov8.SetFatalErrorHandler(chFatalHandler); err != nil {
		fmt.Fprintf(os.Stderr, "SetFatalErrorHandler: %v\n", err)
		os.Exit(1)
	}
	iso, err := gov8.NewIsolateWithLimits(0, 10<<20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewIsolateWithLimits: %v\n", err)
		os.Exit(1)
	}
	if err := iso.SetOOMErrorHandler(chOomHandler); err != nil {
		fmt.Fprintf(os.Stderr, "SetOOMErrorHandler: %v\n", err)
		os.Exit(1)
	}
	calls := 0
	if err := iso.AddNearHeapLimitCallback(func(current, initial uint64) uint64 {
		calls++
		fmt.Fprintf(os.Stderr, "SHRINK call=%d current=%d\n", calls, current)
		return current / 2
	}); err != nil {
		fmt.Fprintf(os.Stderr, "AddNearHeapLimitCallback: %v\n", err)
		os.Exit(1)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewContext: %v\n", err)
		os.Exit(1)
	}
	scope, err := iso.NewScope()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewScope: %v\n", err)
		os.Exit(1)
	}
	runUntilOOM(&runtime{iso: iso, ctx: ctx, scope: scope})
}

// subOOMDefault (negative-test mode): fatal heap OOM with NO handlers
// installed, pinning the default abort behavior.
func subOOMDefault() {
	ensureModeSetup()
	iso, err := gov8.NewIsolateWithLimits(0, 10<<20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewIsolateWithLimits: %v\n", err)
		os.Exit(1)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewContext: %v\n", err)
		os.Exit(1)
	}
	scope, err := iso.NewScope()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewScope: %v\n", err)
		os.Exit(1)
	}
	runUntilOOM(&runtime{iso: iso, ctx: ctx, scope: scope})
}

// runUntilOOM is the shared heap-pressure loop for the OOM subprocess
// modes. The heap is hard-capped by the 10 MiB limit, so this always ends
// in the controlled fatal OOM and never allocates unbounded process memory.
func runUntilOOM(r *runtime) {
	fmt.Fprintln(os.Stderr, "MARK:before-loop")
	for i := 0; i < 1_000_000; i++ {
		if _, ok := r.evalText(modeTester, chHeapWorkload); !ok {
			break
		}
	}
	fmt.Fprintln(os.Stderr, "MARK:after-loop")
	fmt.Println("SURVIVED")
}

// subInvalidFlagPreinit (negative-test mode): unrecognized V8 flags before
// initialization print an error to stderr and are otherwise ignored;
// the recognized flag in the same string still takes effect.
func subInvalidFlagPreinit() {
	if err := gov8.SetFlagsFromString("--definitely-not-a-real-flag"); err != nil {
		fmt.Fprintf(os.Stderr, "SetFlagsFromString: %v\n", err)
		os.Exit(1)
	}
	if err := gov8.SetFlagsFromString("--expose-gc --and-another-bogus-one"); err != nil {
		fmt.Fprintf(os.Stderr, "SetFlagsFromString: %v\n", err)
		os.Exit(1)
	}
	ensureModeSetup()
	iso, err := gov8.NewIsolate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewIsolate: %v\n", err)
		os.Exit(1)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewContext: %v\n", err)
		os.Exit(1)
	}
	scope, err := iso.NewScope()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewScope: %v\n", err)
		os.Exit(1)
	}
	r := &runtime{iso: iso, ctx: ctx, scope: scope}
	result, ok := r.evalText(modeTester, "1 + 1")
	if !ok {
		fmt.Fprintln(os.Stderr, "eval failed")
		os.Exit(1)
	}
	// The bogus tail must not have disabled the recognized --expose-gc flag
	// that precedes it.
	gcType, ok := r.evalText(modeTester, "typeof gc")
	if !ok {
		fmt.Fprintln(os.Stderr, "typeof gc eval failed")
		os.Exit(1)
	}
	gcTypeNum := 0
	if gcType == "function" {
		gcTypeNum = 1
	}
	fmt.Printf("RESULT result=%s gc_type=%d\n", result, gcTypeNum)
}
