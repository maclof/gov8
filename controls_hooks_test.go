//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	gov8 "gov8"
)

// In-process behavior tests for the controls/hooks slice, mirroring the
// in-process checks of rust-oracle/src/bin/conformance-controls-hooks.rs and
// the in-process negative test (declining entropy source). The engine-fatal
// paths are characterized out-of-process in controls_hooks_fatal_test.go and
// in the conformance-controls-hooks runner.

// chRuntime is one isolate+context+scope triple for the controls/hooks
// tests, closed in dependency order.
type chRuntime struct {
	t     *testing.T
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newCHRuntime(t *testing.T) *chRuntime {
	t.Helper()
	iso := newIso(t)
	t.Cleanup(func() { _ = iso.Close() })
	ctx := newCtx(t, iso)
	t.Cleanup(func() { _ = ctx.Close() })
	scope := newScope(t, iso)
	t.Cleanup(func() { _ = scope.Close() })
	return &chRuntime{t: t, iso: iso, ctx: ctx, scope: scope}
}

// eval is the fixed compile/run path every check uses.
func (r *chRuntime) eval(source string) (string, bool) {
	r.t.Helper()
	return evalText(r.t, r.ctx, r.scope, source)
}

// evalCaught evaluates under a fresh TryCatch and returns the completion
// text (ok=false on failure) plus the caught exception text ("" when
// nothing was caught).
func (r *chRuntime) evalCaught(source string) (result string, caught string, ok bool) {
	r.t.Helper()
	tc, err := newTryCatch(r.t, r.iso)
	if err != nil {
		r.t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	v, rerr := r.evalWithTC(source, tc)
	if rerr {
		result = v
		ok = true
	}
	if has, herr := tc.HasCaught(); herr == nil && has {
		if txt, terr := tc.ExceptionText(r.scope, r.ctx); terr == nil {
			caught = txt
		}
	}
	return result, caught, ok
}

// evalWithTC mirrors evalText under an explicit TryCatch.
func (r *chRuntime) evalWithTC(source string, tc *gov8.TryCatch) (string, bool) {
	r.t.Helper()
	script, cerr := r.ctx.Compile(r.scope, source, tc)
	if cerr != nil {
		return "", false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(r.scope, tc)
	if rerr != nil {
		return "", false
	}
	txt, terr := v.ToString(r.ctx)
	if terr != nil {
		return "", false
	}
	return txt, true
}

// --- entropy source -------------------------------------------------------------

// TestEntropySourcePinsMathRandom mirrors entropy_source_before_init and
// entropy_source_replace_after_init: fill-42 installed before any isolate
// creation seeds every fresh isolate identically (the pinned constant), and
// replacing the source still affects isolates created afterwards with a
// different pinned constant.
func TestEntropySourcePinsMathRandom(t *testing.T) {
	if err := gov8.SetEntropySource(func(buf []byte) bool {
		for i := range buf {
			buf[i] = 42
		}
		return true
	}); err != nil {
		t.Fatalf("SetEntropySource(42): %v", err)
	}

	// Fresh isolates, evaluated in a fresh goroutine per isolate (isolate
	// thread affinity): the constant must be identical across all of them.
	const want = "0.41480742418592154"
	var wg sync.WaitGroup
	results := make([]string, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			iso, err := gov8.NewIsolate()
			if err != nil {
				t.Errorf("NewIsolate: %v", err)
				return
			}
			defer func() { _ = iso.Close() }()
			ctx, err := iso.NewContext()
			if err != nil {
				t.Errorf("NewContext: %v", err)
				return
			}
			defer func() { _ = ctx.Close() }()
			scope, err := iso.NewScope()
			if err != nil {
				t.Errorf("NewScope: %v", err)
				return
			}
			defer func() { _ = scope.Close() }()
			v, ok := evalText(t, ctx, scope, "Math.random()")
			if !ok {
				t.Errorf("Math.random() failed (isolate %d)", idx)
				return
			}
			results[idx] = v
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if got != want {
			t.Errorf("isolate %d Math.random() = %q, want pinned %q", i, got, want)
		}
	}

	// Replacing the source AFTER Initialize still pins fresh isolates, to a
	// different constant.
	if err := gov8.SetEntropySource(func(buf []byte) bool {
		for i := range buf {
			buf[i] = 7
		}
		return true
	}); err != nil {
		t.Fatalf("SetEntropySource(7): %v", err)
	}
	r := newCHRuntime(t)
	got, ok := r.eval("Math.random()")
	if !ok {
		t.Fatal("Math.random() failed after entropy replacement")
	}
	const want7 = "0.8960919850226692"
	if got != want7 {
		t.Errorf("Math.random() = %q after fill-7 seeding, want pinned %q", got, want7)
	}
}

// TestEntropySourceDeclineFallsBack mirrors
// entropy_source_returning_false_falls_back_cleanly: a declining source
// keeps Math.random() a valid float in [0, 1) (the exact values are not
// deterministic in this mode).
func TestEntropySourceDeclineFallsBack(t *testing.T) {
	if err := gov8.SetEntropySource(func(buf []byte) bool {
		for i := range buf {
			buf[i] = 0
		}
		return false
	}); err != nil {
		t.Fatalf("SetEntropySource(decline): %v", err)
	}
	for i := 0; i < 3; i++ {
		r := newCHRuntime(t)
		got, ok := r.eval("Math.random()")
		if !ok {
			t.Fatalf("Math.random() failed (iteration %d)", i)
		}
		f, err := strconv.ParseFloat(got, 64)
		if err != nil {
			t.Fatalf("Math.random() = %q must parse: %v", got, err)
		}
		if f < 0.0 || f >= 1.0 {
			t.Fatalf("Math.random() = %s out of range [0, 1)", got)
		}
	}
}

// --- flags ------------------------------------------------------------------------

// TestFlagsFromCommandLineUnrecognizedReturnLeftover pins the pre-init
// contract on the only side observable in an already-initialized process:
// unrecognized args are returned untouched and nothing fatals when no flag
// VALUE changes (the recognized flag consumed by the oracle's fixture run is
// covered by the conformance runner, which owns its process setup order).
func TestFlagsFromCommandLineUnrecognizedReturnLeftover(t *testing.T) {
	args := []string{"gov8-test-binary", "--gov8-definitely-not-a-flag", "--gov8-another-fake-one"}
	leftover, err := gov8.SetFlagsFromCommandLine(args)
	if err != nil {
		t.Fatalf("SetFlagsFromCommandLine: %v", err)
	}
	if len(leftover) != len(args) {
		t.Fatalf("leftover = %v, want all %d args returned", leftover, len(args))
	}
	for i := range args {
		if leftover[i] != args[i] {
			t.Errorf("leftover[%d] = %q, want %q", i, leftover[i], args[i])
		}
	}
}

// --- GC controls ---------------------------------------------------------------------

// TestClearKeptObjectsKeepsIsolateUsable exercises ClearKeptObjects in
// isolation (the full WeakRef kept-objects lifecycle requires --expose-gc
// set before Initialize and is pinned by the conformance runner, whose
// process owns that setup order).
func TestClearKeptObjectsKeepsIsolateUsable(t *testing.T) {
	r := newCHRuntime(t)
	if _, ok := r.eval("globalThis.w = [new WeakRef({a: 1})]; 'ok'"); !ok {
		t.Fatal("setup eval failed")
	}
	if err := r.iso.ClearKeptObjects(); err != nil {
		t.Fatalf("ClearKeptObjects: %v", err)
	}
	got, ok := r.eval("1 + 1")
	if !ok || got != "2" {
		t.Fatalf("isolate unusable after ClearKeptObjects: %q ok=%v", got, ok)
	}
}

// --- memory pressure ------------------------------------------------------------------

// TestMemoryPressureLevels mirrors memory_pressure_levels: all three levels
// back-to-back and the isolate stays fully usable.
func TestMemoryPressureLevels(t *testing.T) {
	r := newCHRuntime(t)
	for _, level := range []gov8.MemoryPressureLevel{
		gov8.MemoryPressureModerate,
		gov8.MemoryPressureCritical,
		gov8.MemoryPressureNone,
	} {
		if err := r.iso.MemoryPressureNotification(level); err != nil {
			t.Fatalf("MemoryPressureNotification(%d): %v", level, err)
		}
	}
	got, ok := r.eval("1 + 1")
	if !ok || got != "2" {
		t.Fatalf("isolate unusable after pressure notifications: %q ok=%v", got, ok)
	}
}

// TestLowMemoryNotificationReclaimsExternalMemory mirrors
// low_memory_notification_external_memory: 32 x 1 MiB ArrayBuffers raise
// external memory by exactly 32 MiB and a low-memory notification returns it
// to the baseline after the buffers are dropped.
func TestLowMemoryNotificationReclaimsExternalMemory(t *testing.T) {
	r := newCHRuntime(t)
	base, err := r.iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	if _, ok := r.eval("globalThis.bufs = []; for (let i = 0; i < 32; i++) bufs.push(new ArrayBuffer(1 << 20)); 'ok'"); !ok {
		t.Fatal("allocation eval failed")
	}
	afterAlloc, err := r.iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	if delta := afterAlloc.ExternalMemory - base.ExternalMemory; delta != 32<<20 {
		t.Fatalf("external memory delta = %d, want %d", delta, 32<<20)
	}
	if _, ok := r.eval("bufs = null; 'ok'"); !ok {
		t.Fatal("drop eval failed")
	}
	if err := r.iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	afterGC, err := r.iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	if afterGC.ExternalMemory != base.ExternalMemory {
		t.Fatalf("external memory after GC = %d, want baseline %d",
			afterGC.ExternalMemory, base.ExternalMemory)
	}
}

// --- atomics wait ---------------------------------------------------------------------

// TestAtomicsWaitToggle mirrors atomics_wait_toggle: allowed (timeout 0)
// returns "timed-out"; disallowed throws the context TypeError before any
// blocking; the toggle flips repeatedly on a live isolate.
func TestAtomicsWaitToggle(t *testing.T) {
	r := newCHRuntime(t)
	if got, ok := r.eval("globalThis.a = new Int32Array(new SharedArrayBuffer(4)); 'ok'"); !ok || got != "ok" {
		t.Fatalf("SharedArrayBuffer setup failed: %q ok=%v", got, ok)
	}
	const wait = "Atomics.wait(globalThis.a, 0, 0, 0)"
	for _, allow := range []bool{true, false, true} {
		if err := r.iso.SetAllowAtomicsWait(allow); err != nil {
			t.Fatalf("SetAllowAtomicsWait(%v): %v", allow, err)
		}
		result, caught, _ := r.evalCaught(wait)
		if allow {
			if result != "timed-out" {
				t.Fatalf("allowed wait result = %q, want timed-out", result)
			}
			if caught != "" {
				t.Fatalf("allowed wait threw: %q", caught)
			}
		} else {
			if result != "" {
				t.Fatalf("disallowed wait returned %q, want failure", result)
			}
			const wantErr = "TypeError: Atomics.wait cannot be called in this context"
			if caught != wantErr {
				t.Fatalf("disallowed wait error = %q, want %q", caught, wantErr)
			}
		}
	}
}

// --- background tasks / idle / timezone ------------------------------------------------

// TestHasPendingBackgroundTasks mirrors has_pending_background_tasks: false
// for a fresh isolate and stays false after plain script execution.
func TestHasPendingBackgroundTasks(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	fresh, err := iso.HasPendingBackgroundTasks()
	if err != nil {
		t.Fatalf("HasPendingBackgroundTasks: %v", err)
	}
	if fresh {
		t.Fatal("fresh isolate has pending background tasks")
	}
	r := &chRuntime{t: t, iso: iso}
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()
	r.ctx, r.scope = ctx, scope
	if _, ok := r.eval("2 + 2"); !ok {
		t.Fatal("eval failed")
	}
	after, err := iso.HasPendingBackgroundTasks()
	if err != nil {
		t.Fatalf("HasPendingBackgroundTasks: %v", err)
	}
	if after {
		t.Fatal("isolate has pending background tasks after plain script")
	}
}

// TestSetIdle mirrors set_idle: the flag toggles cleanly on the isolate's
// thread and the surrounding script still evaluates normally.
func TestSetIdle(t *testing.T) {
	r := newCHRuntime(t)
	if err := r.iso.SetIdle(true); err != nil {
		t.Fatalf("SetIdle(true): %v", err)
	}
	got, ok := r.eval("40 + 2")
	if !ok || got != "42" {
		t.Fatalf("script during idle = %q ok=%v, want 42", got, ok)
	}
	if err := r.iso.SetIdle(false); err != nil {
		t.Fatalf("SetIdle(false): %v", err)
	}
}

// TestDateTimeConfigurationChangeNotification mirrors
// date_time_configuration_change_notification: both detection modes are
// accepted, UTC date math never changes, and the host time zone offset is
// stable across the notifications.
func TestDateTimeConfigurationChangeNotification(t *testing.T) {
	r := newCHRuntime(t)
	const iso = "new Date(Date.UTC(2020, 0, 2, 3, 4, 5)).toISOString()"
	const offset = "new Date(0).getTimezoneOffset()"
	before, ok := r.eval(iso)
	if !ok || before != "2020-01-02T03:04:05.000Z" {
		t.Fatalf("ISO before = %q ok=%v", before, ok)
	}
	offsetBefore, ok := r.eval(offset)
	if !ok {
		t.Fatal("offset eval failed")
	}
	if err := r.iso.DateTimeConfigurationChangeNotification(gov8.TZSkip); err != nil {
		t.Fatalf("DateTimeConfigurationChangeNotification(Skip): %v", err)
	}
	afterSkip, _ := r.eval(iso)
	offsetAfterSkip, _ := r.eval(offset)
	if err := r.iso.DateTimeConfigurationChangeNotification(gov8.TZRedetect); err != nil {
		t.Fatalf("DateTimeConfigurationChangeNotification(Redetect): %v", err)
	}
	afterRedetect, _ := r.eval(iso)
	offsetAfterRedetect, _ := r.eval(offset)
	if !(before == afterSkip && afterSkip == afterRedetect) {
		t.Fatalf("ISO changed across notifications: %q, %q, %q", before, afterSkip, afterRedetect)
	}
	if !(offsetBefore == offsetAfterSkip && offsetAfterSkip == offsetAfterRedetect) {
		t.Fatalf("timezone offset changed across notifications: %q, %q, %q",
			offsetBefore, offsetAfterSkip, offsetAfterRedetect)
	}
}

// --- promise hook -----------------------------------------------------------------------

// TestPromiseHookSequence mirrors promise_hook_sequence: Init/Resolve fire
// synchronously during the script, Before/Resolve/After during the
// microtask checkpoint, and a second checkpoint is empty.
func TestPromiseHookSequence(t *testing.T) {
	r := newCHRuntime(t)
	if err := r.iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	var mu sync.Mutex
	var seq []string
	if err := r.iso.SetPromiseHook(func(pt gov8.PromiseHookType, _, _ gov8.Value) {
		mu.Lock()
		defer mu.Unlock()
		switch pt {
		case gov8.PromiseHookInit:
			seq = append(seq, "Init")
		case gov8.PromiseHookResolve:
			seq = append(seq, "Resolve")
		case gov8.PromiseHookBefore:
			seq = append(seq, "Before")
		case gov8.PromiseHookAfter:
			seq = append(seq, "After")
		}
	}); err != nil {
		t.Fatalf("SetPromiseHook: %v", err)
	}
	if _, ok := r.eval("const p1 = new Promise(r => r(1)); const p2 = p1.then(() => 2); globalThis.p2 = p2;"); !ok {
		t.Fatal("promise script failed")
	}
	mu.Lock()
	afterRun := append([]string(nil), seq...)
	seq = seq[:0]
	mu.Unlock()
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("PerformMicrotaskCheckpoint: %v", err)
	}
	mu.Lock()
	afterCheckpoint := append([]string(nil), seq...)
	seq = seq[:0]
	mu.Unlock()
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("second PerformMicrotaskCheckpoint: %v", err)
	}
	mu.Lock()
	afterSecond := append([]string(nil), seq...)
	mu.Unlock()

	assertStrings := func(what string, got, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
	assertStrings("after_run", afterRun, []string{"Init", "Resolve", "Init"})
	assertStrings("after_checkpoint", afterCheckpoint, []string{"Before", "Resolve", "After"})
	assertStrings("after_second_checkpoint", afterSecond, nil)
}

// --- prepare stack trace --------------------------------------------------------------------

// TestPrepareStackTraceCallback mirrors prepare_stack_trace_callback: the
// native hook formats the message, sees the CallSite count, replaces the
// stack value with 42 once per distinct error, and disables the JS
// Error.prepareStackTrace hook.
func TestPrepareStackTraceCallback(t *testing.T) {
	r := newCHRuntime(t)
	var mu sync.Mutex
	var calls []string
	if err := r.iso.SetPrepareStackTraceCallback(func(s *gov8.Scope, errValue, sites gov8.Value) (gov8.Value, bool) {
		text, err := gov8.ExceptionMessageText(s, errValue)
		if err != nil {
			t.Errorf("ExceptionMessageText: %v", err)
			return gov8.Value{}, false
		}
		n, err := gov8.ArrayLength(s, sites)
		if err != nil {
			t.Errorf("ArrayLength: %v", err)
			return gov8.Value{}, false
		}
		mu.Lock()
		calls = append(calls, text+":"+strconv.Itoa(n))
		mu.Unlock()
		v, err := s.Int32(42)
		if err != nil {
			t.Errorf("Int32: %v", err)
			return gov8.Value{}, false
		}
		return v, true
	}); err != nil {
		t.Fatalf("SetPrepareStackTraceCallback: %v", err)
	}

	stack, ok := r.eval("function g() { throw new Error(\"boom\") }\n" +
		"function f() { g() }\n" +
		"try { f() } catch (e) { e.stack }\n")
	if !ok || stack != "42" {
		t.Fatalf("first stack value = %q ok=%v, want 42", stack, ok)
	}
	mu.Lock()
	firstCalls := append([]string(nil), calls...)
	calls = calls[:0]
	mu.Unlock()
	if strings.Join(firstCalls, "|") != "Uncaught Error: boom:3" {
		t.Fatalf("first call log = %v", firstCalls)
	}

	if _, ok := r.eval("Error.prepareStackTrace = function(e, s) { globalThis.jsHookUsed = true; return 'js'; };"); !ok {
		t.Fatal("JS hook install failed")
	}
	stack2, ok := r.eval("try { (function zq(){ throw new Error('boom2') })() } catch (e2) { e2.stack }")
	if !ok || stack2 != "42" {
		t.Fatalf("second stack value = %q ok=%v, want 42", stack2, ok)
	}
	jsUsed, _ := r.eval("globalThis.jsHookUsed === true")
	if jsUsed == "true" {
		t.Fatal("JS Error.prepareStackTrace ran despite the native hook")
	}
	mu.Lock()
	secondCalls := append([]string(nil), calls...)
	mu.Unlock()
	if strings.Join(secondCalls, "|") != "Uncaught Error: boom2:2" {
		t.Fatalf("second call log = %v", secondCalls)
	}
}

// --- use counter ------------------------------------------------------------------------------

// TestUseCounterFeatures mirrors use_counter_features: the deterministic
// subset of UseCounterFeature triggers with the pinned discriminants.
func TestUseCounterFeatures(t *testing.T) {
	r := newCHRuntime(t)
	var mu sync.Mutex
	var seen []uint32
	if err := r.iso.SetUseCounterCallback(func(feature uint32) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, feature)
	}); err != nil {
		t.Fatalf("SetUseCounterCallback: %v", err)
	}
	workload := []struct {
		label   string
		script  string
		want    []uint32
		mustRun bool
	}{
		{"strict_script", "\"use strict\"; 1", []uint32{9}, true},
		{"sloppy_script", "var x = 1; x", nil, true},
		{"html_comment", "<!-- html comment\n 1", []uint32{21, 20}, true},
		{"capture_stack_trace", "Error.captureStackTrace({})", []uint32{43}, true},
		{"string_replace_all", "\"abc\".replaceAll(\"a\", \"b\")", []uint32{159}, true},
		{"promise_with_resolvers", "Promise.withResolvers()", []uint32{155}, true},
		{"weak_ref", "new WeakRef({})", []uint32{161}, true},
		{"string_normalize", "\"x\".normalize()", []uint32{75}, true},
		{"string_to_well_formed", "\"x\".toWellFormed()", []uint32{160}, true},
		// The for-in initializer throws at runtime; only the feature fires.
		{"for_in_initializer", "for (var i = 0 in {}) {}", []uint32{23}, false},
	}
	for _, w := range workload {
		mu.Lock()
		before := len(seen)
		mu.Unlock()
		_, _ = r.eval(w.script)
		mu.Lock()
		fired := append([]uint32(nil), seen[before:]...)
		mu.Unlock()
		if len(fired) != len(w.want) {
			t.Errorf("%s: fired %v, want %v", w.label, fired, w.want)
			continue
		}
		for i := range w.want {
			if fired[i] != w.want[i] {
				t.Errorf("%s: fired %v, want %v", w.label, fired, w.want)
				break
			}
		}
	}
}

// --- code generation from strings ---------------------------------------------------------------

// TestModifyCodeGenerationFromStrings mirrors modify_code_generation_from_strings:
// plain evals skip the callback; disallowed contexts consult it (block,
// rewrite); non-string sources in allowed contexts pass through.
func TestModifyCodeGenerationFromStrings(t *testing.T) {
	r := newCHRuntime(t)
	var mu sync.Mutex
	var callLog []string
	// Callback verdicts, mirroring the oracle's CODEGEN_* modes:
	// 0 = block, 1 = allow with modified source "999", 2 = allow unchanged.
	const (
		codegenBlock     = 0
		codegenModify    = 1
		codegenAllowAsIs = 2
	)
	mode := codegenBlock
	if err := r.iso.SetModifyCodeGenerationFromStringsCallback(func(source gov8.Value, isCodeLike bool) (bool, *string) {
		text := "<not-a-string>"
		if is, _ := source.IsString(); is {
			if txt, err := source.StringValue(); err == nil {
				text = txt
			}
		}
		mu.Lock()
		callLog = append(callLog, text+":"+boolWord(isCodeLike))
		mu.Unlock()
		switch mode {
		case codegenBlock:
			return false, nil
		case codegenAllowAsIs:
			return true, nil
		default:
			nine := "999"
			return true, &nine
		}
	}); err != nil {
		t.Fatalf("SetModifyCodeGenerationFromStringsCallback: %v", err)
	}

	allowed, err := r.ctx.IsCodeGenerationFromStringsAllowed()
	if err != nil {
		t.Fatalf("IsCodeGenerationFromStringsAllowed: %v", err)
	}
	if !allowed {
		t.Fatal("code generation from strings must be allowed by default")
	}
	plain, ok := r.eval("eval('1+1')")
	if !ok || plain != "2" {
		t.Fatalf("plain eval = %q ok=%v, want 2", plain, ok)
	}
	mu.Lock()
	callsAfterPlain := len(callLog)
	mu.Unlock()
	if callsAfterPlain != 0 {
		t.Fatalf("plain eval consulted the callback %d times, want 0", callsAfterPlain)
	}

	if err := r.ctx.AllowCodeGenerationFromStrings(false); err != nil {
		t.Fatalf("AllowCodeGenerationFromStrings(false): %v", err)
	}
	disallowed, err := r.ctx.IsCodeGenerationFromStringsAllowed()
	if err != nil {
		t.Fatalf("IsCodeGenerationFromStringsAllowed: %v", err)
	}
	if disallowed {
		t.Fatal("context still allows codegen after AllowCodeGenerationFromStrings(false)")
	}

	mode = codegenBlock
	_, caught, _ := r.evalCaught("eval('2+2')")
	mu.Lock()
	callsAfterBlock := len(callLog)
	mu.Unlock()
	const wantBlockedErr = "EvalError: Code generation from strings disallowed for this context"
	if caught != wantBlockedErr {
		t.Fatalf("blocked eval error = %q, want %q", caught, wantBlockedErr)
	}
	if callsAfterBlock != 1 {
		t.Fatalf("calls after block = %d, want 1", callsAfterBlock)
	}

	mode = codegenModify
	rewritten, ok := r.eval("eval('3+3')")
	if !ok || rewritten != "999" {
		t.Fatalf("rewritten eval = %q ok=%v, want 999", rewritten, ok)
	}
	mu.Lock()
	callsAfterRewrite := len(callLog)
	mu.Unlock()
	if callsAfterRewrite != 2 {
		t.Fatalf("calls after rewrite = %d, want 2", callsAfterRewrite)
	}

	// Allowed context + non-string source: the callback is consulted and
	// allow-unchanged hands the source back (the Symbol passes through).
	if err := r.ctx.AllowCodeGenerationFromStrings(true); err != nil {
		t.Fatalf("AllowCodeGenerationFromStrings(true): %v", err)
	}
	mode = codegenAllowAsIs
	symbolPass, ok := r.eval("globalThis.sym = Symbol('s'); typeof eval(globalThis.sym)")
	if !ok || symbolPass != "symbol" {
		t.Fatalf("symbol passthrough = %q ok=%v, want symbol", symbolPass, ok)
	}
	mu.Lock()
	total := len(callLog)
	log := append([]string(nil), callLog...)
	mu.Unlock()
	if total != 4 {
		t.Fatalf("total calls = %d, want 4 (%v)", total, log)
	}
	wantLog := "2+2:false|3+3:false|<not-a-string>:false|<not-a-string>:false"
	if strings.Join(log, "|") != wantLog {
		t.Fatalf("call log = %v, want %s", log, wantLog)
	}
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// --- message listeners --------------------------------------------------------------------------

// TestMessageListenerUncaughtOnly mirrors message_listener_uncaught_only:
// only exceptions escaping every TryCatch are reported, duplicate
// registrations deliver twice, a WARNING-filtered listener never sees an
// ERROR-level throw, and the isolate stays usable afterwards.
func TestMessageListenerUncaughtOnly(t *testing.T) {
	r := newCHRuntime(t)
	var mu sync.Mutex
	var log []string
	listener := func(msg *gov8.CallbackMessage, exception gov8.Value) {
		text, err := msg.Text()
		if err != nil {
			t.Errorf("msg.Text: %v", err)
			return
		}
		line, lineOK, err := msg.LineNumber()
		if err != nil {
			t.Errorf("msg.LineNumber: %v", err)
			return
		}
		level, err := msg.ErrorLevel()
		if err != nil {
			t.Errorf("msg.ErrorLevel: %v", err)
			return
		}
		start, err := msg.StartPosition()
		if err != nil {
			t.Errorf("msg.StartPosition: %v", err)
			return
		}
		end, err := msg.EndPosition()
		if err != nil {
			t.Errorf("msg.EndPosition: %v", err)
			return
		}
		excText, err := msg.ValueText(exception)
		if err != nil {
			t.Errorf("ValueText: %v", err)
			return
		}
		lineText := "None"
		if lineOK {
			lineText = "Some(" + strconv.Itoa(int(line)) + ")"
		}
		mu.Lock()
		log = append(log, text+"|"+lineText+"|"+
			strconv.FormatInt(level, 10)+"|"+
			strconv.FormatInt(start, 10)+".."+strconv.FormatInt(end, 10)+
			"|"+excText)
		mu.Unlock()
	}
	ok1, err := r.iso.AddMessageListener(listener)
	if err != nil {
		t.Fatalf("AddMessageListener: %v", err)
	}
	ok2, err := r.iso.AddMessageListener(listener)
	if err != nil {
		t.Fatalf("AddMessageListener (second): %v", err)
	}
	okW, err := r.iso.AddMessageListenerWithErrorLevel(listener, gov8.MsgWarning)
	if err != nil {
		t.Fatalf("AddMessageListenerWithErrorLevel: %v", err)
	}
	if !(ok1 && ok2 && okW) {
		t.Fatalf("registrations = %v/%v/%v, want all true", ok1, ok2, okW)
	}

	// Uncaught throw on line 2 (no TryCatch active): the compile succeeds,
	// the run fails, and the listeners observe the escaping exception.
	script, cerr := r.ctx.Compile(r.scope, "let a = 1;\nthrow new Error('boom');\nlet b = 2;", nil)
	if cerr != nil {
		t.Fatalf("compile failed: %v", cerr)
	}
	if _, rerr := script.RunUncaught(r.scope); rerr == nil {
		t.Fatal("uncaught run of a throwing script must fail")
	}
	_ = script.Close()
	mu.Lock()
	uncaught := append([]string(nil), log...)
	log = log[:0]
	mu.Unlock()
	want := "Uncaught Error: boom|Some(2)|8|11..12|Error: boom"
	if len(uncaught) != 2 || uncaught[0] != want || uncaught[1] != want {
		t.Fatalf("uncaught calls = %q, want [%s %s]", uncaught, want, want)
	}

	// The isolate remains usable after the uncaught exception.
	recovered, ok := r.eval("40 + 2")
	if !ok || recovered != "42" {
		t.Fatalf("recovered eval = %q ok=%v", recovered, ok)
	}

	// Exceptions caught by an active TryCatch are never reported.
	tc, err := newTryCatch(t, r.iso)
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	if _, ok := r.evalWithTC("try { null.x } catch (e) { e }", tc); !ok {
		t.Error("caught-throw eval failed")
	}
	if _, ok := r.evalWithTC("null.x", tc); ok {
		t.Error("null.x under TryCatch unexpectedly succeeded")
	}
	_ = tc.Close()
	mu.Lock()
	caught := append([]string(nil), log...)
	mu.Unlock()
	if len(caught) != 0 {
		t.Fatalf("TryCatch-caught exceptions were reported: %v", caught)
	}
}

// TestControlsConcurrentIsolates exercises the control surface on several
// isolates running concurrently (each pinned to its own OS thread by the
// wrapper): the process-level registry must route every dispatch to its own
// isolate's registration without cross-talk.
func TestControlsConcurrentIsolates(t *testing.T) {
	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			iso, err := gov8.NewIsolate()
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = iso.Close() }()
			if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
				errs <- err
				return
			}
			// Per-isolate hook registration: each isolate must observe only
			// its own marker value.
			var fired []uint32
			if err := iso.SetUseCounterCallback(func(feature uint32) {
				fired = append(fired, feature)
			}); err != nil {
				errs <- err
				return
			}
			ctx, err := iso.NewContext()
			if err != nil {
				errs <- err
				return
			}
			scope, err := iso.NewScope()
			if err != nil {
				errs <- err
				return
			}
			script, cerr := ctx.Compile(scope, "\"use strict\"; 40 + 2", nil)
			if cerr != nil {
				errs <- cerr
				return
			}
			v, rerr := script.Run(scope, nil)
			if rerr != nil {
				errs <- rerr
				return
			}
			got, ok, terr := v.IntegerValue(ctx)
			if terr != nil || !ok || got != 42 {
				errs <- fmt.Errorf("goroutine %d: got %d ok=%v err=%v", idx, got, ok, terr)
				return
			}
			if len(fired) == 0 || fired[0] != 9 {
				errs <- fmt.Errorf("goroutine %d: use counter fired %v, want [9]", idx, fired)
				return
			}
			if err := iso.MemoryPressureNotification(gov8.MemoryPressureModerate); err != nil {
				errs <- err
				return
			}
			if err := iso.SetIdle(true); err != nil {
				errs <- err
				return
			}
			if pending, err := iso.HasPendingBackgroundTasks(); err != nil || pending {
				errs <- fmt.Errorf("goroutine %d: pending=%v err=%v", idx, pending, err)
				return
			}
			_ = scope.Close()
			_ = ctx.Close()
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
