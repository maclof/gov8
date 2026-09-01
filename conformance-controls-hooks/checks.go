//go:build windows && amd64

// The controls/hooks checks, in the fixed contractual order (the order is
// part of the observable contract). One function per oracle check; every
// check renders an expectation/observation pair that must be byte-identical
// to the pinned fixture line.
package main

import (
	"strconv"
	"strings"
	"sync"

	gov8 "github.com/maclof/gov8"
)

// --- shared callback state (fixture callbacks only append small normalized
// records; everything runs on the isolate's own thread) ----------------------

var (
	useCounterSeen   []uint32
	promiseHookSeq   []string
	promiseRejectSeq []string
	prepareStackCDs  []string
	codegenCalls     []string
	messageLog       []string
	logMu            sync.Mutex

	// 0 = block, 1 = allow with modified source "999", 2 = allow unchanged.
	codegenMode = 0
)

func drainStrings(slot *[]string) jsonValue {
	logMu.Lock()
	defer logMu.Unlock()
	vals := make([]jsonValue, len(*slot))
	for i, s := range *slot {
		vals[i] = jstr(s)
	}
	*slot = (*slot)[:0]
	return jarr(vals...)
}

func jsonStrings(values ...string) jsonValue {
	vals := make([]jsonValue, len(values))
	for i, v := range values {
		vals[i] = jstr(v)
	}
	return jarr(vals...)
}

func optionalJSON(value string, ok bool) jsonValue {
	if !ok {
		return jnull()
	}
	return jstr(value)
}

// entropyFill42 / entropyFill7 are the oracle's fixed entropy sources.
func entropyFill42(buf []byte) bool {
	for i := range buf {
		buf[i] = 42
	}
	return true
}

func entropyFill7(buf []byte) bool {
	for i := range buf {
		buf[i] = 7
	}
	return true
}

// setupResult carries the normalized observations of the pre-initialize
// steps (the process setup order is itself part of the contract).
type setupResult struct {
	commandLineUnrecognized []string
	commandLineWithUsage    []string
	wasmTrapHandlerEnabled  bool
}

var (
	setupMu       sync.Mutex
	setupDone     bool
	setupObserved setupResult
)

// ensureSetup runs the whole process setup in the contractual order, exactly
// once:
//  1. EnableWebAssemblyTrapHandler(false): exact platform/build capability.
//  2. SetFlagsFromCommandLineWithUsage (pre-init): usage is accepted while
//     recognized flags are consumed normally.
//  3. SetFlagsFromCommandLine (pre-init): "--log-colour" is recognized by
//     this engine and consumed; anything else is returned to the embedder.
//  4. SetFlagsFromString("--expose-gc"): required for
//     RequestGarbageCollectionForTesting (fatal CHECK without it) and to
//     expose the JS gc() global in contexts created afterwards.
//  5. SetEntropySource(fill42): pins Math.random().
//  6. gov8.Initialize(): identical platform config to the oracle
//     (new_default_platform(0, false)). The flag set is frozen afterwards.
func ensureSetup(t tester) setupResult {
	t.Helper()
	setupMu.Lock()
	defer setupMu.Unlock()
	if setupDone {
		return setupObserved
	}
	trapEnabled, err := gov8.EnableWebAssemblyTrapHandler(false)
	if err != nil {
		t.Fatalf("EnableWebAssemblyTrapHandler: %v", err)
	}
	usageLeftover, err := gov8.SetFlagsFromCommandLineWithUsage([]string{
		"usage-probe", "--log-colour", "--usage-leftover",
	}, "Usage: usage-probe [v8 flags]\n")
	if err != nil {
		t.Fatalf("SetFlagsFromCommandLineWithUsage: %v", err)
	}
	leftover, err := gov8.SetFlagsFromCommandLine([]string{
		"conformance-controls-hooks", "--log-colour", "--should-be-ignored",
	})
	if err != nil {
		t.Fatalf("SetFlagsFromCommandLine: %v", err)
	}
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		t.Fatalf("SetFlagsFromString: %v", err)
	}
	if err := gov8.SetEntropySource(entropyFill42); err != nil {
		t.Fatalf("SetEntropySource: %v", err)
	}
	if err := gov8.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	setupObserved = setupResult{
		commandLineUnrecognized: leftover,
		commandLineWithUsage:    usageLeftover,
		wasmTrapHandlerEnabled:  trapEnabled,
	}
	setupDone = true
	return setupObserved
}

// --- the checks, in contractual order ----------------------------------------

func chWasmTrapHandlerPreinit(t tester) obs {
	t.Helper()
	setup := ensureSetup(t)
	return wantGot("controls/wasm_trap_handler_preinit", jbool(true),
		jbool(setup.wasmTrapHandlerEnabled))
}

func chFlagsCommandLineWithUsagePreinit(t tester) obs {
	t.Helper()
	setup := ensureSetup(t)
	got := make([]jsonValue, len(setup.commandLineWithUsage))
	for i, s := range setup.commandLineWithUsage {
		got[i] = jstr(s)
	}
	return wantGot("controls/flags_command_line_with_usage_preinit",
		jsonStrings("usage-probe", "--usage-leftover"), jarr(got...))
}

// chFlagsCommandLinePreinit: recognized flags are consumed, everything else
// (including argv[0]) is returned.
func chFlagsCommandLinePreinit(t tester) obs {
	t.Helper()
	setup := ensureSetup(t)
	got := make([]jsonValue, len(setup.commandLineUnrecognized))
	for i, s := range setup.commandLineUnrecognized {
		got[i] = jstr(s)
	}
	return wantGot("controls/flags_command_line_preinit",
		jobj(kv("command_line_unrecognized",
			jsonStrings("conformance-controls-hooks", "--should-be-ignored"))),
		jobj(kv("command_line_unrecognized", jarr(got...))))
}

// chFlagsExposeGcPreinit: --expose-gc set before initialization exposes the
// JS gc() global in every context created afterwards.
func chFlagsExposeGcPreinit(t tester) obs {
	t.Helper()
	ensureSetup(t)
	r := newRuntime(t)
	defer r.close(t)
	typeofGC, ok1 := r.evalText(t, "typeof gc")
	gcCallable, ok2 := r.evalText(t, "gc(); typeof gc === 'function'")
	if !ok1 || !ok2 {
		t.Fatalf("gc evals failed: %q %q", typeofGC, gcCallable)
	}
	return wantGot("controls/flags_expose_gc_preinit",
		jobj(
			kv("typeof_gc", jstr("function")),
			kv("gc_callable", jbool(true)),
		),
		jobj(
			kv("typeof_gc", jstr(typeofGC)),
			kv("gc_callable", jbool(gcCallable == "true")),
		))
}

// chEntropySourceBeforeInit: the pre-init entropy source seeds every fresh
// isolate's PRNG identically.
func chEntropySourceBeforeInit(t tester) obs {
	t.Helper()
	ensureSetup(t)
	const want = "0.41480742418592154"
	var results []string
	for i := 0; i < 3; i++ {
		r := newRuntime(t)
		v, ok := r.evalText(t, "Math.random()")
		r.close(t)
		if !ok {
			t.Fatalf("Math.random() failed (isolate %d)", i)
		}
		results = append(results, v)
	}
	allEqual := true
	for _, v := range results {
		if v != results[0] {
			allEqual = false
		}
	}
	return wantGot("controls/entropy_source_before_init",
		jobj(
			kv("identical_across_isolates", jbool(true)),
			kv("value", jstr(want)),
		),
		jobj(
			kv("identical_across_isolates", jbool(allEqual)),
			kv("value", jstr(results[0])),
		))
}

// chEntropySourceReplaceAfterInit: replacing the entropy source after
// Initialize still affects isolates created afterwards, with a different
// constant.
func chEntropySourceReplaceAfterInit(t tester) obs {
	t.Helper()
	ensureSetup(t)
	if err := gov8.SetEntropySource(entropyFill7); err != nil {
		t.Fatalf("SetEntropySource(fill7): %v", err)
	}
	r := newRuntime(t)
	defer r.close(t)
	value, ok := r.evalText(t, "Math.random()")
	if !ok {
		t.Fatalf("Math.random() failed after entropy replacement")
	}
	return wantGot("controls/entropy_source_replace_after_init",
		jobj(
			kv("value", jstr("0.8960919850226692")),
			kv("differs_from_pre_init_seed", jbool(true)),
		),
		jobj(
			kv("value", jstr(value)),
			kv("differs_from_pre_init_seed", jbool(value != "0.41480742418592154")),
		))
}

// chFrozenFlagsFatalSubprocess: post-initialization flag mutation is fatal
// (the flag set is frozen during Initialize); characterized in a
// subprocess.
func chFrozenFlagsFatalSubprocess(t tester) obs {
	t.Helper()
	stdout, stderr, code := spawnSelf(t, "sub-fatal-frozen-flags")
	handlerFile := "<unmatched>"
	if strings.Contains(stderr, `FATAL file=""`) {
		handlerFile = ""
	}
	handlerLine := int64(-1)
	if strings.Contains(stderr, " line=0 ") {
		handlerLine = 0
	}
	handlerMessage := "<unmatched>"
	if strings.Contains(stderr, `message="Check failed: !IsFrozen()."`) {
		handlerMessage = "Check failed: !IsFrozen()."
	}
	return wantGot("controls/frozen_flags_fatal_subprocess",
		jobj(
			kv("exit_code", jint(-2147483645)),
			kv("exit_code_hex", jstr("0x80000003")),
			kv("handler_called", jbool(true)),
			kv("handler_file", jstr("")),
			kv("handler_line", jint(0)),
			kv("handler_message", jstr("Check failed: !IsFrozen().")),
			kv("banner_in_stderr", jbool(true)),
			kv("survived", jbool(false)),
		),
		jobj(
			kv("exit_code", exitCodeJSON(code)),
			kv("exit_code_hex", exitCodeHexJSON(code)),
			kv("handler_called", jbool(strings.Contains(stderr, "FATAL "))),
			kv("handler_file", jstr(handlerFile)),
			kv("handler_line", jint(handlerLine)),
			kv("handler_message", jstr(handlerMessage)),
			kv("banner_in_stderr", jbool(strings.Contains(stderr, "# Fatal error"))),
			kv("survived", jbool(strings.Contains(stdout, "SURVIVED"))),
		))
}

// chGcRequestRequiresExposeGcSubprocess: both collection kinds without
// --expose-gc fail the fatal CHECK and abort; the API fatal handler is NOT
// invoked at this site.
func chGcRequestRequiresExposeGcSubprocess(t tester) []obs {
	t.Helper()
	var out []obs
	for _, kind := range []string{"full", "minor"} {
		stdout, stderr, code := spawnSelf(t, "sub-gc-without-expose-gc-"+kind)
		fatalFunction := "<unmatched>"
		if strings.Contains(stderr, "# Fatal error in v8::Isolate::RequestGarbageCollectionForTesting") {
			fatalFunction = "v8::Isolate::RequestGarbageCollectionForTesting"
		}
		fatalMessage := "<unmatched>"
		if strings.Contains(stderr, "# Must use --expose-gc") {
			fatalMessage = "Must use --expose-gc"
		}
		out = append(out, wantGot("controls/gc_request_requires_expose_gc_subprocess",
			jobj(
				kv("kind", jstr(kind)),
				kv("exit_code", jint(-2147483645)),
				kv("exit_code_hex", jstr("0x80000003")),
				kv("fatal_function", jstr("v8::Isolate::RequestGarbageCollectionForTesting")),
				kv("fatal_message", jstr("Must use --expose-gc")),
				kv("api_fatal_handler_called", jbool(false)),
				kv("survived", jbool(false)),
			),
			jobj(
				kv("kind", jstr(kind)),
				kv("exit_code", exitCodeJSON(code)),
				kv("exit_code_hex", exitCodeHexJSON(code)),
				kv("fatal_function", jstr(fatalFunction)),
				kv("fatal_message", jstr(fatalMessage)),
				kv("api_fatal_handler_called", jbool(strings.Contains(stderr, "FATAL "))),
				kv("survived", jbool(strings.Contains(stdout, "SURVIVED"))),
			)))
	}
	return out
}

// chGcRequestFullMinorClearKeptObjects: a full collection keeps still-
// referenced WeakRef targets (the kept-objects set); clear_kept_objects
// drops it; a minor request runs without fatal error.
func chGcRequestFullMinorClearKeptObjects(t tester) obs {
	t.Helper()
	ensureSetup(t)
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	if _, ok := r.evalText(t, "globalThis.w = []; for (let i = 0; i < 4242; i++) w.push(new WeakRef({ i }));"); !ok {
		t.Fatalf("WeakRef setup failed")
	}
	if err := r.iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		t.Fatalf("RequestGarbageCollectionForTesting(full): %v", err)
	}
	kept, ok := r.evalText(t, "w.every(r => r.deref() !== undefined)")
	if !ok {
		t.Fatalf("kept eval failed")
	}
	if err := r.iso.ClearKeptObjects(); err != nil {
		t.Fatalf("ClearKeptObjects: %v", err)
	}
	if err := r.iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		t.Fatalf("RequestGarbageCollectionForTesting(full, after clear): %v", err)
	}
	cleared, ok := r.evalText(t, "w.every(r => r.deref() === undefined)")
	if !ok {
		t.Fatalf("cleared eval failed")
	}
	minorSurvived := r.iso.RequestGarbageCollectionForTesting(gov8.GcMinor) == nil
	return wantGot("controls/gc_request_full_minor_clear_kept_objects",
		jobj(
			kv("kept_after_full_gc", jbool(true)),
			kv("cleared_after_clear_kept_objects", jbool(true)),
			kv("minor_request_survived", jbool(true)),
		),
		jobj(
			kv("kept_after_full_gc", jbool(kept == "true")),
			kv("cleared_after_clear_kept_objects", jbool(cleared == "true")),
			kv("minor_request_survived", jbool(minorSurvived)),
		))
}

// chMemoryPressureLevels: all three levels back-to-back and the isolate
// stays fully usable.
func chMemoryPressureLevels(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	for _, level := range []gov8.MemoryPressureLevel{
		gov8.MemoryPressureModerate, gov8.MemoryPressureCritical, gov8.MemoryPressureNone,
	} {
		if err := r.iso.MemoryPressureNotification(level); err != nil {
			t.Fatalf("MemoryPressureNotification(%d): %v", level, err)
		}
	}
	stillRunning, ok := r.evalText(t, "1 + 1")
	if !ok {
		t.Fatalf("eval after pressure notifications failed")
	}
	return wantGot("controls/memory_pressure_levels",
		jobj(
			kv("levels", jsonStrings("Moderate", "Critical", "None")),
			kv("isolate_still_running", jstr("2")),
		),
		jobj(
			kv("levels", jsonStrings("Moderate", "Critical", "None")),
			kv("isolate_still_running", jstr(stillRunning)),
		))
}

// chLowMemoryNotificationExternalMemory: a low-memory notification reclaims
// unreachable ArrayBuffer backing stores (32 x 1 MiB back to baseline).
func chLowMemoryNotificationExternalMemory(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	baseStats, err := r.iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	if _, ok := r.evalText(t, "globalThis.bufs = []; for (let i = 0; i < 32; i++) bufs.push(new ArrayBuffer(1 << 20));"); !ok {
		t.Fatalf("allocation eval failed")
	}
	afterAlloc, err := r.iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	if _, ok := r.evalText(t, "bufs = null;"); !ok {
		t.Fatalf("drop eval failed")
	}
	if err := r.iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	afterGC, err := r.iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	return wantGot("controls/low_memory_notification_external_memory",
		jobj(
			kv("baseline_bytes", jint(21)),
			kv("after_alloc_bytes", jint(33554453)),
			kv("after_gc_bytes", jint(21)),
		),
		jobj(
			kv("baseline_bytes", jint(int64(baseStats.ExternalMemory))),
			kv("after_alloc_bytes", jint(int64(afterAlloc.ExternalMemory))),
			kv("after_gc_bytes", jint(int64(afterGC.ExternalMemory))),
		))
}

// chAtomicsWaitToggle: allowed (timeout 0) deterministically returns
// "timed-out"; disallowed throws a TypeError before any blocking; the
// toggle flips repeatedly on a live isolate.
func chAtomicsWaitToggle(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	if _, ok := r.evalText(t, "globalThis.a = new Int32Array(new SharedArrayBuffer(4)); 'ok'"); !ok {
		t.Fatalf("SharedArrayBuffer setup failed")
	}
	const wait = "Atomics.wait(globalThis.a, 0, 0, 0)"
	var actual []jsonValue
	for _, allow := range []bool{true, false, true} {
		if err := r.iso.SetAllowAtomicsWait(allow); err != nil {
			t.Fatalf("SetAllowAtomicsWait(%v): %v", allow, err)
		}
		result, caught, _ := r.evalTextCaught(t, wait)
		actual = append(actual, jobj(
			kv("allowed", jbool(allow)),
			kv("result", optionalJSON(result, result != "")),
			kv("error", optionalJSON(caught, caught != "")),
		))
	}
	expected := jarr(
		jobj(kv("allowed", jbool(true)), kv("result", jstr("timed-out")), kv("error", jnull())),
		jobj(kv("allowed", jbool(false)), kv("result", jnull()),
			kv("error", jstr("TypeError: Atomics.wait cannot be called in this context"))),
		jobj(kv("allowed", jbool(true)), kv("result", jstr("timed-out")), kv("error", jnull())),
	)
	return wantGot("controls/atomics_wait_toggle", expected, jarr(actual...))
}

// chHasPendingBackgroundTasks: false for a fresh isolate and stays false
// after plain script execution.
func chHasPendingBackgroundTasks(t tester) obs {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	fresh, err := iso.HasPendingBackgroundTasks()
	if err != nil {
		t.Fatalf("HasPendingBackgroundTasks: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	r := &runtime{iso: iso, ctx: ctx, scope: scope}
	if _, ok := r.evalText(t, "2 + 2"); !ok {
		t.Fatalf("eval failed")
	}
	afterScript, err := iso.HasPendingBackgroundTasks()
	if err != nil {
		t.Fatalf("HasPendingBackgroundTasks: %v", err)
	}
	r.close(t)
	return wantGot("controls/has_pending_background_tasks",
		jobj(kv("fresh", jbool(false)), kv("after_script", jbool(false))),
		jobj(kv("fresh", jbool(fresh)), kv("after_script", jbool(afterScript))))
}

// chSetIdle: the flag toggles on the isolate's thread with no synchronous
// observable effect; the surrounding script still evaluates normally.
func chSetIdle(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetIdle(true); err != nil {
		t.Fatalf("SetIdle(true): %v", err)
	}
	during, ok := r.evalText(t, "40 + 2")
	if !ok {
		t.Fatalf("eval during idle failed")
	}
	if err := r.iso.SetIdle(false); err != nil {
		t.Fatalf("SetIdle(false): %v", err)
	}
	return wantGot("controls/set_idle",
		jobj(kv("script_result_during_idle", jstr("42"))),
		jobj(kv("script_result_during_idle", jstr(during))))
}

// chDateTimeConfigurationChangeNotification: both detection modes are
// accepted; UTC date math never changes; the host offset is stable.
func chDateTimeConfigurationChangeNotification(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	const isoExpr = "new Date(Date.UTC(2020, 0, 2, 3, 4, 5)).toISOString()"
	const offsetExpr = "new Date(0).getTimezoneOffset()"
	isoBefore, ok := r.evalText(t, isoExpr)
	if !ok {
		t.Fatalf("ISO eval failed")
	}
	offsetBefore, ok := r.evalText(t, offsetExpr)
	if !ok {
		t.Fatalf("offset eval failed")
	}
	if err := r.iso.DateTimeConfigurationChangeNotification(gov8.TZSkip); err != nil {
		t.Fatalf("DateTimeConfigurationChangeNotification(Skip): %v", err)
	}
	isoAfterSkip, _ := r.evalText(t, isoExpr)
	offsetAfterSkip, _ := r.evalText(t, offsetExpr)
	if err := r.iso.DateTimeConfigurationChangeNotification(gov8.TZRedetect); err != nil {
		t.Fatalf("DateTimeConfigurationChangeNotification(Redetect): %v", err)
	}
	isoAfterRedetect, _ := r.evalText(t, isoExpr)
	offsetAfterRedetect, _ := r.evalText(t, offsetExpr)
	return wantGot("controls/date_time_configuration_change_notification",
		jobj(
			kv("iso_constant", jstr("2020-01-02T03:04:05.000Z")),
			kv("utc_unchanged_by_notifications", jbool(true)),
			kv("timezone_offset_unchanged", jbool(true)),
		),
		jobj(
			kv("iso_constant", jstr(isoBefore)),
			kv("utc_unchanged_by_notifications",
				jbool(isoBefore == isoAfterSkip && isoAfterSkip == isoAfterRedetect)),
			kv("timezone_offset_unchanged",
				jbool(offsetBefore == offsetAfterSkip && offsetAfterSkip == offsetAfterRedetect)),
		))
}

// chPromiseHookSequence: Init/Resolve synchronously; Before/Resolve/After
// at the checkpoint; a second checkpoint is empty.
func chPromiseHookSequence(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	if err := r.iso.SetPromiseHook(func(pt gov8.PromiseHookType, _, _ gov8.Value) {
		logMu.Lock()
		defer logMu.Unlock()
		switch pt {
		case gov8.PromiseHookInit:
			promiseHookSeq = append(promiseHookSeq, "Init")
		case gov8.PromiseHookResolve:
			promiseHookSeq = append(promiseHookSeq, "Resolve")
		case gov8.PromiseHookBefore:
			promiseHookSeq = append(promiseHookSeq, "Before")
		case gov8.PromiseHookAfter:
			promiseHookSeq = append(promiseHookSeq, "After")
		}
	}); err != nil {
		t.Fatalf("SetPromiseHook: %v", err)
	}
	if _, ok := r.evalText(t, "const p1 = new Promise(r => r(1)); const p2 = p1.then(() => 2); globalThis.p2 = p2;"); !ok {
		t.Fatalf("promise script failed")
	}
	logMu.Lock()
	afterRun := drainStringsLocked(&promiseHookSeq)
	logMu.Unlock()
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("PerformMicrotaskCheckpoint: %v", err)
	}
	logMu.Lock()
	afterCheckpoint := drainStringsLocked(&promiseHookSeq)
	logMu.Unlock()
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("second PerformMicrotaskCheckpoint: %v", err)
	}
	logMu.Lock()
	afterSecond := drainStringsLocked(&promiseHookSeq)
	logMu.Unlock()
	return wantGot("controls/promise_hook_sequence",
		jobj(
			kv("after_run", jsonStrings("Init", "Resolve", "Init")),
			kv("after_checkpoint", jsonStrings("Before", "Resolve", "After")),
			kv("after_second_checkpoint", jsonStrings()),
		),
		jobj(
			kv("after_run", afterRun),
			kv("after_checkpoint", afterCheckpoint),
			kv("after_second_checkpoint", afterSecond),
		))
}

func drainStringsLocked(slot *[]string) jsonValue {
	vals := make([]jsonValue, len(*slot))
	for i, s := range *slot {
		vals[i] = jstr(s)
	}
	*slot = (*slot)[:0]
	return jarr(vals...)
}

// chPromiseRejectNotification: RejectWithNoHandler fires synchronously at
// rejection time; HandlerAddedAfterReject fires on late handler attachment;
// re-settling a settled promise delivers nothing in this build.
func chPromiseRejectNotification(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	rejectName := func(e gov8.PromiseRejectEvent) string {
		switch e {
		case gov8.PromiseRejectWithNoHandler:
			return "RejectWithNoHandler"
		case gov8.PromiseHandlerAddedAfterReject:
			return "HandlerAddedAfterReject"
		case gov8.PromiseRejectAfterResolved:
			return "RejectAfterResolved"
		case gov8.PromiseResolveAfterResolved:
			return "ResolveAfterResolved"
		}
		return "Unknown"
	}
	// The promise slice's SetPromiseRejectCallback anchors delivered handles
	// to the supplied scope.
	if err := r.iso.SetPromiseRejectCallback(r.scope, func(m gov8.PromiseRejectMessage) {
		logMu.Lock()
		defer logMu.Unlock()
		promiseRejectSeq = append(promiseRejectSeq, rejectName(m.Event))
	}); err != nil {
		t.Fatalf("SetPromiseRejectCallback: %v", err)
	}
	if _, ok := r.evalText(t, "globalThis.p1 = Promise.reject(1);"+
		"globalThis.p2 = Promise.resolve(2);"+
		"p1.catch(() => {});"+
		"p2.then(x => x, y => y);"); !ok {
		t.Fatalf("reject script failed")
	}
	logMu.Lock()
	afterRun := drainStringsLocked(&promiseRejectSeq)
	logMu.Unlock()
	if _, ok := r.evalText(t, "const r = Promise.withResolvers();"+
		"r.promise.then(x => {}, y => {});"+
		"r.resolve(1);"+
		"r.resolve(2);"+
		"r.reject(3);"+
		"'a'"); !ok {
		t.Fatalf("re-resolve script failed")
	}
	logMu.Lock()
	reResolve := drainStringsLocked(&promiseRejectSeq)
	logMu.Unlock()
	if _, ok := r.evalText(t, "const r2 = Promise.withResolvers();"+
		"r2.promise.then(x => {}, y => {});"+
		"r2.reject(1);"+
		"r2.reject(2);"+
		"'b'"); !ok {
		t.Fatalf("re-reject script failed")
	}
	logMu.Lock()
	reReject := drainStringsLocked(&promiseRejectSeq)
	logMu.Unlock()
	return wantGot("controls/promise_reject_notification",
		jobj(
			kv("after_run", jsonStrings("RejectWithNoHandler", "HandlerAddedAfterReject")),
			kv("re_resolve_settled", jsonStrings()),
			kv("re_reject_settled", jsonStrings()),
		),
		jobj(
			kv("after_run", afterRun),
			kv("re_resolve_settled", reResolve),
			kv("re_reject_settled", reReject),
		))
}

// chPrepareStackTraceCallback: the native callback formats the message,
// sees the CallSite count, replaces the stack value with 42 once per
// distinct error, and disables the JS Error.prepareStackTrace hook.
func chPrepareStackTraceCallback(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetPrepareStackTraceCallback(func(s *gov8.Scope, errValue, sites gov8.Value) (gov8.Value, bool) {
		message, err := gov8.ExceptionMessageText(s, errValue)
		if err != nil {
			t.Errorf("ExceptionMessageText: %v", err)
			return gov8.Value{}, false
		}
		n, err := gov8.ArrayLength(s, sites)
		if err != nil {
			t.Errorf("ArrayLength: %v", err)
			return gov8.Value{}, false
		}
		logMu.Lock()
		prepareStackCDs = append(prepareStackCDs, message+":"+strconv.Itoa(n))
		logMu.Unlock()
		v, err := s.Int32(42)
		if err != nil {
			t.Errorf("Int32: %v", err)
			return gov8.Value{}, false
		}
		return v, true
	}); err != nil {
		t.Fatalf("SetPrepareStackTraceCallback: %v", err)
	}
	stack, ok := r.evalText(t, "function g() { throw new Error(\"boom\") }\n"+
		"function f() { g() }\n"+
		"try { f() } catch (e) { e.stack }\n")
	if !ok {
		t.Fatalf("first stack eval failed")
	}
	logMu.Lock()
	firstCalls := drainStringsLocked(&prepareStackCDs)
	logMu.Unlock()
	if _, ok := r.evalText(t, "Error.prepareStackTrace = function(e, s) { globalThis.jsHookUsed = true; return 'js'; };"); !ok {
		t.Fatalf("JS hook install failed")
	}
	stack2, ok := r.evalText(t, "try { (function zq(){ throw new Error('boom2') })() } catch (e2) { e2.stack }")
	if !ok {
		t.Fatalf("second stack eval failed")
	}
	jsUsed, _ := r.evalText(t, "globalThis.jsHookUsed === true")
	logMu.Lock()
	secondCalls := drainStringsLocked(&prepareStackCDs)
	logMu.Unlock()
	return wantGot("controls/prepare_stack_trace_callback",
		jobj(
			kv("stack_value", jstr("42")),
			kv("first_call", jsonStrings("Uncaught Error: boom:3")),
			kv("second_stack_value", jstr("42")),
			kv("second_call", jsonStrings("Uncaught Error: boom2:2")),
			kv("js_prepare_stack_trace_disabled", jbool(true)),
		),
		jobj(
			kv("stack_value", jstr(stack)),
			kv("first_call", firstCalls),
			kv("second_stack_value", jstr(stack2)),
			kv("second_call", secondCalls),
			kv("js_prepare_stack_trace_disabled", jbool(jsUsed != "true")),
		))
}

// chUseCounterFeatures: engine feature IDs for a fixed JS workload (the
// deterministic subset of UseCounterFeature reachable from plain JS).
func chUseCounterFeatures(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetUseCounterCallback(func(feature uint32) {
		logMu.Lock()
		defer logMu.Unlock()
		useCounterSeen = append(useCounterSeen, feature)
	}); err != nil {
		t.Fatalf("SetUseCounterCallback: %v", err)
	}
	workload := []struct {
		label  string
		script string
		want   []int64
	}{
		{"strict_script", "\"use strict\"; 1", []int64{9}},
		{"sloppy_script", "var x = 1; x", nil},
		{"html_comment", "<!-- html comment\n 1", []int64{21, 20}},
		{"capture_stack_trace", "Error.captureStackTrace({})", []int64{43}},
		{"string_replace_all", "\"abc\".replaceAll(\"a\", \"b\")", []int64{159}},
		{"promise_with_resolvers", "Promise.withResolvers()", []int64{155}},
		{"weak_ref", "new WeakRef({})", []int64{161}},
		{"string_normalize", "\"x\".normalize()", []int64{75}},
		{"string_to_well_formed", "\"x\".toWellFormed()", []int64{160}},
		{"for_in_initializer", "for (var i = 0 in {}) {}", []int64{23}},
	}
	var expected, actual []jsonValue
	for _, w := range workload {
		logMu.Lock()
		before := len(useCounterSeen)
		logMu.Unlock()
		_, _ = r.evalText(t, w.script)
		logMu.Lock()
		firedVals := make([]jsonValue, 0, len(useCounterSeen)-before)
		for _, id := range useCounterSeen[before:] {
			firedVals = append(firedVals, jint(int64(id)))
		}
		logMu.Unlock()
		wantVals := make([]jsonValue, 0, len(w.want))
		for _, id := range w.want {
			wantVals = append(wantVals, jint(id))
		}
		expected = append(expected, jobj(kv("label", jstr(w.label)), kv("features", jarr(wantVals...))))
		actual = append(actual, jobj(kv("label", jstr(w.label)), kv("features", jarr(firedVals...))))
	}
	return wantGot("controls/use_counter_features", jarr(expected...), jarr(actual...))
}

// chModifyCodeGenerationFromStrings: the callback is consulted only when the
// context disallows codegen from strings or the source is not a string; it
// can block, rewrite, or pass through.
func chModifyCodeGenerationFromStrings(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetModifyCodeGenerationFromStringsCallback(func(source gov8.Value, isCodeLike bool) (bool, *string) {
		text := "<not-a-string>"
		if is, _ := source.IsString(); is {
			if txt, err := source.StringValue(); err == nil {
				text = txt
			}
		}
		logMu.Lock()
		codegenCalls = append(codegenCalls, text+":"+boolWord(isCodeLike))
		logMu.Unlock()
		switch codegenMode {
		case 0:
			return false, nil
		case 2:
			return true, nil
		default:
			nine := "999"
			return true, &nine
		}
	}); err != nil {
		t.Fatalf("SetModifyCodeGenerationFromStringsCallback: %v", err)
	}

	// 1. Allowed context + string source: callback skipped entirely.
	allowedDefault, err := r.ctx.IsCodeGenerationFromStringsAllowed()
	if err != nil {
		t.Fatalf("IsCodeGenerationFromStringsAllowed: %v", err)
	}
	plainEval, ok := r.evalText(t, "eval('1+1')")
	if !ok {
		t.Fatalf("plain eval failed")
	}
	logMu.Lock()
	callsAfterPlain := len(codegenCalls)
	logMu.Unlock()

	// 2. Disallowed context + block: EvalError, callback saw the source.
	if err := r.ctx.AllowCodeGenerationFromStrings(false); err != nil {
		t.Fatalf("AllowCodeGenerationFromStrings(false): %v", err)
	}
	disallowed, err := r.ctx.IsCodeGenerationFromStringsAllowed()
	if err != nil {
		t.Fatalf("IsCodeGenerationFromStringsAllowed: %v", err)
	}
	codegenMode = 0
	blockedResult, blockedError, _ := r.evalTextCaught(t, "eval('2+2')")
	logMu.Lock()
	callsAfterBlock := len(codegenCalls)
	logMu.Unlock()

	// 3. Disallowed context + allow with modified source: eval returns 999.
	codegenMode = 1
	rewritten, ok := r.evalText(t, "eval('3+3')")
	if !ok {
		t.Fatalf("rewritten eval failed")
	}
	logMu.Lock()
	callsAfterRewrite := len(codegenCalls)
	logMu.Unlock()

	// 4. Allowed context + non-string source: callback consulted; allow
	// unchanged hands the source back (the Symbol passes through).
	codegenMode = 2
	if err := r.ctx.AllowCodeGenerationFromStrings(true); err != nil {
		t.Fatalf("AllowCodeGenerationFromStrings(true): %v", err)
	}
	symbolPassthrough, ok := r.evalText(t, "globalThis.sym = Symbol('s'); typeof eval(globalThis.sym)")
	if !ok {
		t.Fatalf("symbol passthrough eval failed")
	}
	logMu.Lock()
	callsTotal := len(codegenCalls)
	callLog := drainStringsLocked(&codegenCalls)
	logMu.Unlock()

	return wantGot("controls/modify_code_generation_from_strings",
		jobj(
			kv("allowed_by_default", jbool(true)),
			kv("plain_eval_skips_callback", jstr("2")),
			kv("calls_after_plain", jint(0)),
			kv("context_disallowed_after_set_false", jbool(true)),
			kv("blocked_result", jnull()),
			kv("blocked_error", jstr("EvalError: Code generation from strings disallowed for this context")),
			kv("calls_after_block", jint(1)),
			kv("rewritten_eval", jstr("999")),
			kv("calls_after_rewrite", jint(2)),
			kv("symbol_passthrough_typeof", jstr("symbol")),
			kv("calls_total", jint(4)),
			kv("call_log", jsonStrings("2+2:false", "3+3:false", "<not-a-string>:false", "<not-a-string>:false")),
		),
		jobj(
			kv("allowed_by_default", jbool(allowedDefault)),
			kv("plain_eval_skips_callback", jstr(plainEval)),
			kv("calls_after_plain", jint(int64(callsAfterPlain))),
			kv("context_disallowed_after_set_false", jbool(!disallowed)),
			kv("blocked_result", optionalJSON(blockedResult, blockedResult != "")),
			kv("blocked_error", optionalJSON(blockedError, blockedError != "")),
			kv("calls_after_block", jint(int64(callsAfterBlock))),
			kv("rewritten_eval", jstr(rewritten)),
			kv("calls_after_rewrite", jint(int64(callsAfterRewrite))),
			kv("symbol_passthrough_typeof", jstr(symbolPassthrough)),
			kv("calls_total", jint(int64(callsTotal))),
			kv("call_log", callLog),
		))
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// chMessageListenerUncaughtOnly: only exceptions escaping every TryCatch
// are reported; duplicate registrations deliver twice; a WARNING-filtered
// listener never sees an ERROR-level throw; the isolate stays usable.
func chMessageListenerUncaughtOnly(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
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
			t.Errorf("msg.ValueText: %v", err)
			return
		}
		lineText := "None"
		if lineOK {
			lineText = "Some(" + strconv.Itoa(int(line)) + ")"
		}
		logMu.Lock()
		messageLog = append(messageLog, text+"|"+lineText+"|"+
			strconv.FormatInt(level, 10)+"|"+
			strconv.FormatInt(start, 10)+".."+strconv.FormatInt(end, 10)+
			"|"+excText)
		logMu.Unlock()
	}
	added1, err := r.iso.AddMessageListener(listener)
	if err != nil {
		t.Fatalf("AddMessageListener: %v", err)
	}
	added2, err := r.iso.AddMessageListener(listener)
	if err != nil {
		t.Fatalf("AddMessageListener (second): %v", err)
	}
	addedWarning, err := r.iso.AddMessageListenerWithErrorLevel(listener, gov8.MsgWarning)
	if err != nil {
		t.Fatalf("AddMessageListenerWithErrorLevel: %v", err)
	}

	// Uncaught throw on line 2; no TryCatch active.
	source := "let a = 1;\nthrow new Error('boom');\nlet b = 2;"
	script, cerr := r.ctx.Compile(r.scope, source, nil)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	_, runErr := script.RunUncaught(r.scope)
	runFailed := runErr != nil
	_ = script.Close()
	logMu.Lock()
	uncaught := drainStringsLocked(&messageLog)
	logMu.Unlock()

	// The isolate remains usable after the uncaught exception.
	recovered, ok := r.evalText(t, "40 + 2")
	if !ok {
		t.Fatalf("recovery eval failed")
	}

	// Exceptions caught by an active TryCatch are never reported.
	_, _, _ = r.evalTextCaught(t, "try { null.x } catch (e) { e }")
	_, _, _ = r.evalTextCaught(t, "null.x")
	logMu.Lock()
	caught := drainStringsLocked(&messageLog)
	logMu.Unlock()

	return wantGot("controls/message_listener_uncaught_only",
		jobj(
			kv("registrations_ok", jarr(jbool(true), jbool(true), jbool(true))),
			kv("run_failed", jbool(true)),
			kv("uncaught_calls", jsonStrings(
				"Uncaught Error: boom|Some(2)|8|11..12|Error: boom",
				"Uncaught Error: boom|Some(2)|8|11..12|Error: boom")),
			kv("recovered_after_uncaught", jstr("42")),
			kv("caught_calls", jsonStrings()),
		),
		jobj(
			kv("registrations_ok", jarr(jbool(added1), jbool(added2), jbool(addedWarning))),
			kv("run_failed", jbool(runFailed)),
			kv("uncaught_calls", uncaught),
			kv("recovered_after_uncaught", jstr(recovered)),
			kv("caught_calls", caught),
		))
}

// chNearHeapLimitSubprocess: only the most recently added callback is
// invoked; the heap is capped at 10 MiB; the callback reports V8's
// configured limit (4 MiB) and doubles it exactly once.
func chNearHeapLimitSubprocess(t tester) obs {
	t.Helper()
	stdout, _, code := spawnSelf(t, "sub-near-heap-limit")
	values := parseResultValues(stdout)
	sentinel := func(index int) jsonValue {
		if index < len(values) {
			return jint(values[index])
		}
		return jint(-1)
	}
	return wantGot("controls/near_heap_limit_subprocess",
		jobj(
			kv("calls_first", jint(0)),
			kv("calls_second", jint(1)),
			kv("initial_limit_bytes", jint(4194304)),
			kv("current_limit_bytes", jint(4194304)),
			kv("returned_limit_bytes", jint(8388608)),
			kv("exit_ok", jbool(true)),
		),
		jobj(
			kv("calls_first", sentinel(0)),
			kv("calls_second", sentinel(1)),
			kv("initial_limit_bytes", sentinel(2)),
			kv("current_limit_bytes", sentinel(3)),
			kv("returned_limit_bytes", sentinel(4)),
			kv("exit_ok", jbool(code == 0)),
		))
}

// chOomFatalHandlersSubprocess: the isolate OOM handler observes the
// heap-OOM details and the process fatal handler observes the post-OOM
// fatal; then V8 aborts with STATUS_BREAKPOINT (heap capped at 10 MiB).
func chOomFatalHandlersSubprocess(t tester) obs {
	t.Helper()
	stdout, stderr, code := spawnSelf(t, "sub-oom-fatal")
	oomObservation := "<unmatched>"
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "OOM location=") {
			oomObservation = line
			break
		}
	}
	fatalObservation := "<unmatched>"
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "FATAL ") {
			fatalObservation = line
			break
		}
	}
	return wantGot("controls/oom_fatal_handlers_subprocess",
		jobj(
			kv("exit_code", jint(-2147483645)),
			kv("exit_code_hex", jstr("0x80000003")),
			kv("oom_observation", jstr(`OOM location="Reached heap limit" is_heap_oom=true detail=""`)),
			kv("fatal_observation", jstr(`FATAL file="" line=0 message="API fatal error handler returned after process out of memory"`)),
			kv("survived", jbool(false)),
		),
		jobj(
			kv("exit_code", exitCodeJSON(code)),
			kv("exit_code_hex", exitCodeHexJSON(code)),
			kv("oom_observation", jstr(oomObservation)),
			kv("fatal_observation", jstr(fatalObservation)),
			kv("survived", jbool(strings.Contains(stdout, "SURVIVED"))),
		))
}

// checks is the fixed registry, in the contractual oracle order. The oracle
// check functions return a vec of outcomes; the gc-without-expose-gc check
// emits two lines (full and minor) under the same id.
var checks = []func(tester) []obs{
	one(chWasmTrapHandlerPreinit),
	one(chFlagsCommandLineWithUsagePreinit),
	one(chFlagsCommandLinePreinit),
	one(chFlagsExposeGcPreinit),
	one(chEntropySourceBeforeInit),
	one(chEntropySourceReplaceAfterInit),
	one(chFrozenFlagsFatalSubprocess),
	two(chGcRequestRequiresExposeGcSubprocess),
	one(chGcRequestFullMinorClearKeptObjects),
	one(chMemoryPressureLevels),
	one(chLowMemoryNotificationExternalMemory),
	one(chAtomicsWaitToggle),
	one(chHasPendingBackgroundTasks),
	one(chSetIdle),
	one(chDateTimeConfigurationChangeNotification),
	one(chPromiseHookSequence),
	one(chPromiseRejectNotification),
	one(chPrepareStackTraceCallback),
	one(chUseCounterFeatures),
	one(chModifyCodeGenerationFromStrings),
	one(chMessageListenerUncaughtOnly),
	one(chNearHeapLimitSubprocess),
	one(chOomFatalHandlersSubprocess),
}

// one adapts a single-outcome check to the registry shape.
func one(f func(tester) obs) func(tester) []obs {
	return func(t tester) []obs { return []obs{f(t)} }
}

// two adapts a multi-outcome check to the registry shape.
func two(f func(tester) []obs) func(tester) []obs { return f }
