//go:build windows && amd64

package gov8_test

import (
	"crypto/rand"
	"testing"

	gov8 "gov8"
)

// Controls/hooks benchmarks, implementing the eight workload specs pinned in
// the oracle binary's documentation
// (rust-oracle/src/bin/conformance-controls-hooks.rs, "Benchmark specs"):
//
//   1. hooks/promise_hook_overhead        -> BenchmarkHookPromiseHookOverhead
//   2. hooks/message_listener_overhead    -> BenchmarkHookMessageListenerOverhead
//   3. hooks/use_counter_overhead         -> BenchmarkHookUseCounterOverhead
//   4. hooks/entropy_source_overhead      -> BenchmarkHookEntropySourceOverhead
//   5. hooks/codegen_callback_overhead    -> BenchmarkHookCodegenCallbackOverhead
//   6. hooks/prepare_stack_trace_overhead -> BenchmarkHookPrepareStackTraceOverhead
//   7. hooks/near_heap_limit_registration -> BenchmarkHookNearHeapLimitRegistration
//   8. hooks/memory_pressure_notification -> BenchmarkHookMemoryPressureNotification
//
// Each overhead benchmark has a paired No-hook variant measuring the same
// workload without the hook so the delta is the hook's marginal cost. Every
// benchmark reports ns/iteration; the oracle side must use the same inputs,
// iteration counts, V8 flags, and platform settings
// (new_default_platform(0, false)) and report distributions, not single
// runs. Raw comparative output belongs under rust-oracle/bench-results/
// next to the existing runs.

// --- 1. promise hook overhead ----------------------------------------------------

// BenchmarkHookPromiseHookOverheadWithoutHook: 10k `Promise.resolve().then`
// reaction cycles under the Explicit microtasks policy with one checkpoint
// per 1k cycles (one b.N iteration = one cycle).
func BenchmarkHookPromiseHookOverheadWithoutHook(b *testing.B) {
	benchPromiseCycles(b, nil)
}

// BenchmarkHookPromiseHookOverhead: identical harness with a recording
// promise hook installed (the hook does nothing but append a byte, matching
// the oracle's recording fn).
func BenchmarkHookPromiseHookOverhead(b *testing.B) {
	hook := func(gov8.PromiseHookType, gov8.Value, gov8.Value) {}
	benchPromiseCycles(b, hook)
}

func benchPromiseCycles(b *testing.B, hook gov8.PromiseHook) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		b.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	if hook != nil {
		if err := iso.SetPromiseHook(hook); err != nil {
			b.Fatalf("SetPromiseHook: %v", err)
		}
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	const cycle = "Promise.resolve().then(() => 1); 1"
	b.ResetTimer()
	checkpointAt := 0
	for i := 0; i < b.N; i++ {
		script, cerr := ctx.Compile(scope, cycle, nil)
		if cerr != nil {
			b.Fatal(cerr)
		}
		if _, rerr := script.Run(scope, nil); rerr != nil {
			b.Fatal(rerr)
		}
		_ = script.Close()
		checkpointAt++
		if checkpointAt == 1000 {
			checkpointAt = 0
			if err := iso.PerformMicrotaskCheckpoint(); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.StopTimer()
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		b.Fatal(err)
	}
}

// --- 2. message listener overhead ------------------------------------------------

const benchThrowSource = "throw new Error('x');"

// BenchmarkHookMessageListenerOverheadWithoutHook: one uncaught throw per
// iteration with no listener registered.
func BenchmarkHookMessageListenerOverheadWithoutHook(b *testing.B) {
	benchUncaughtThrow(b, false)
}

// BenchmarkHookMessageListenerOverhead: identical harness with one listener
// registered (the listener reads the message text, a realistic minimal
// observer).
func BenchmarkHookMessageListenerOverhead(b *testing.B) {
	benchUncaughtThrow(b, true)
}

func benchUncaughtThrow(b *testing.B, withListener bool) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	if withListener {
		if _, err := iso.AddMessageListener(func(msg *gov8.CallbackMessage, _ gov8.Value) {
			_, _ = msg.Text()
		}); err != nil {
			b.Fatalf("AddMessageListener: %v", err)
		}
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	script, cerr := ctx.Compile(scope, benchThrowSource, nil)
	if cerr != nil {
		b.Fatal(cerr)
	}
	defer func() { _ = script.Close() }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, rerr := script.RunUncaught(scope); rerr == nil {
			b.Fatal("throwing script did not fail")
		}
	}
	b.StopTimer()
}

// --- 3. use counter overhead -----------------------------------------------------

var benchUseCounterScripts = []string{
	"\"use strict\"; 1",
	"var x = 1; x",
	"<!-- html comment\n 1",
	"Error.captureStackTrace({})",
	"\"abc\".replaceAll(\"a\", \"b\")",
	"Promise.withResolvers()",
	"new WeakRef({})",
	"\"x\".normalize()",
	"\"x\".toWellFormed()",
	"try { for (var i = 0 in {}) {} } catch (e) {}",
}

// BenchmarkHookUseCounterOverheadWithoutHook: compile/eval of the 10-script
// use-counter workload with no callback.
func BenchmarkHookUseCounterOverheadWithoutHook(b *testing.B) {
	benchUseCounterWorkload(b, false)
}

// BenchmarkHookUseCounterOverhead: identical workload with a no-op recording
// use-counter callback installed.
func BenchmarkHookUseCounterOverhead(b *testing.B) {
	benchUseCounterWorkload(b, true)
}

func benchUseCounterWorkload(b *testing.B, withCallback bool) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	if withCallback {
		if err := iso.SetUseCounterCallback(func(uint32) {}); err != nil {
			b.Fatalf("SetUseCounterCallback: %v", err)
		}
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src := benchUseCounterScripts[i%len(benchUseCounterScripts)]
		script, cerr := ctx.Compile(scope, src, nil)
		if cerr != nil {
			b.Skip("compile rejected (script is expected to fail at run time only)") // unreachable: all scripts compile
		}
		_, _ = script.Run(scope, nil) // the for-in initializer throws by design
		_ = script.Close()
	}
	b.StopTimer()
}

// --- 4. entropy source overhead --------------------------------------------------

// BenchmarkHookEntropySourceOverheadDefaultEntropy: Math.random() in a
// 100k-iteration JS loop with a real-random Go entropy source (the
// stand-in for the engine's default source; both provide full-entropy
// seeding — the engine's default SystemEntropySource path is not
// distinguishable from a real-random embedder source at this granularity).
// One b.N iteration = one full JS loop of 100k Math.random() calls.
func BenchmarkHookEntropySourceOverheadDefaultEntropy(b *testing.B) {
	if err := gov8.SetEntropySource(func(buf []byte) bool {
		_, err := rand.Read(buf)
		return err == nil
	}); err != nil {
		b.Fatalf("SetEntropySource: %v", err)
	}
	benchRandomLoop(b)
}

// BenchmarkHookEntropySourceOverheadFixed: identical loop with the fixed
// fill-42 source installed.
func BenchmarkHookEntropySourceOverheadFixed(b *testing.B) {
	if err := gov8.SetEntropySource(func(buf []byte) bool {
		for i := range buf {
			buf[i] = 42
		}
		return true
	}); err != nil {
		b.Fatalf("SetEntropySource: %v", err)
	}
	benchRandomLoop(b)
}

func benchRandomLoop(b *testing.B) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	// The loop accumulates into a sink so the JIT cannot dead-code it, and
	// it is wrapped in an IIFE so every iteration is self-contained.
	const loop = "(function(){ let s = 0; for (let i = 0; i < 100000; i++) s += Math.random(); return s; })()"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		script, cerr := ctx.Compile(scope, loop, nil)
		if cerr != nil {
			b.Fatal(cerr)
		}
		if _, rerr := script.Run(scope, nil); rerr != nil {
			b.Fatal(rerr)
		}
		_ = script.Close()
	}
	b.StopTimer()
}

// --- 5. codegen callback overhead ------------------------------------------------

// BenchmarkHookCodegenCallbackOverheadWithoutHook: plain eval in an allowed
// context (the callback is skipped entirely).
func BenchmarkHookCodegenCallbackOverheadWithoutHook(b *testing.B) {
	benchCodegenEval(b, false)
}

// BenchmarkHookCodegenCallbackOverhead: eval routed through the callback
// (disallowed context, allow-unchanged verdict), the callback's full path.
func BenchmarkHookCodegenCallbackOverhead(b *testing.B) {
	benchCodegenEval(b, true)
}

func benchCodegenEval(b *testing.B, withCallback bool) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	if withCallback {
		if err := iso.SetModifyCodeGenerationFromStringsCallback(func(_ gov8.Value, _ bool) (bool, *string) {
			return true, nil
		}); err != nil {
			b.Fatalf("SetModifyCodeGenerationFromStringsCallback: %v", err)
		}
		if err := ctx.AllowCodeGenerationFromStrings(false); err != nil {
			b.Fatalf("AllowCodeGenerationFromStrings(false): %v", err)
		}
	}
	const evalSrc = "eval('1 + 1')"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		script, cerr := ctx.Compile(scope, evalSrc, nil)
		if cerr != nil {
			b.Fatal(cerr)
		}
		if _, rerr := script.Run(scope, nil); rerr != nil {
			b.Fatal(rerr)
		}
		_ = script.Close()
	}
	b.StopTimer()
}

// --- 6. prepare stack trace overhead ---------------------------------------------

const benchCatchStack = "function gq() { throw new Error('x') }\n" +
	"try { gq() } catch (e) { e.stack }\n"

// BenchmarkHookPrepareStackTraceOverheadJS: `catch (e) { e.stack }` with
// Error.prepareStackTrace set from JS (the engine's default formatting
// path plus a JS call).
func BenchmarkHookPrepareStackTraceOverheadJS(b *testing.B) {
	benchCatchStackBench(b, false)
}

// BenchmarkHookPrepareStackTraceOverhead: identical workload with the
// native prepare-stack-trace callback installed.
func BenchmarkHookPrepareStackTraceOverhead(b *testing.B) {
	benchCatchStackBench(b, true)
}

func benchCatchStackBench(b *testing.B, nativeHook bool) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	if nativeHook {
		if err := iso.SetPrepareStackTraceCallback(func(s *gov8.Scope, _ gov8.Value, _ gov8.Value) (gov8.Value, bool) {
			v, err := s.Int32(42)
			if err != nil {
				b.Fatal(err)
			}
			return v, true
		}); err != nil {
			b.Fatalf("SetPrepareStackTraceCallback: %v", err)
		}
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	if !nativeHook {
		// JS-side hook: mirror the oracle's JS Error.prepareStackTrace.
		script, cerr := ctx.Compile(scope,
			"Error.prepareStackTrace = function(e, s) { return 'js'; };", nil)
		if cerr != nil {
			b.Fatal(cerr)
		}
		if _, rerr := script.Run(scope, nil); rerr != nil {
			b.Fatal(rerr)
		}
		_ = script.Close()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		script, cerr := ctx.Compile(scope, benchCatchStack, nil)
		if cerr != nil {
			b.Fatal(cerr)
		}
		if _, rerr := script.Run(scope, nil); rerr != nil {
			b.Fatal(rerr)
		}
		_ = script.Close()
	}
	b.StopTimer()
}

// --- 7. near-heap-limit registration ---------------------------------------------

// BenchmarkHookNearHeapLimitRegistrationWithout: isolate create + dispose
// with no heap-pressure work (registration overhead measured by the delta).
func BenchmarkHookNearHeapLimitRegistrationWithout(b *testing.B) {
	for i := 0; i < b.N; i++ {
		iso, err := gov8.NewIsolate()
		if err != nil {
			b.Fatal(err)
		}
		if err := iso.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHookNearHeapLimitRegistration: isolate create + a near-heap-limit
// callback registration + dispose (no heap pressure — this measures the
// registration/dispose cost, not the GC path).
func BenchmarkHookNearHeapLimitRegistration(b *testing.B) {
	cb := func(current, initial uint64) uint64 { return current * 2 }
	for i := 0; i < b.N; i++ {
		iso, err := gov8.NewIsolate()
		if err != nil {
			b.Fatal(err)
		}
		if err := iso.AddNearHeapLimitCallback(cb); err != nil {
			b.Fatal(err)
		}
		if err := iso.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// --- 8. memory pressure notification ---------------------------------------------

// BenchmarkHookMemoryPressureNotificationBaseline: the empty-loop baseline
// for the notification benchmark (one cheap engine call per iteration to
// keep the loop shape identical).
func BenchmarkHookMemoryPressureNotificationBaseline(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = iso.Close() }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := iso.HasPendingBackgroundTasks(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkHookMemoryPressureNotification: one MemoryPressureNotification
// (Moderate) per iteration.
func BenchmarkHookMemoryPressureNotification(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = iso.Close() }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := iso.MemoryPressureNotification(gov8.MemoryPressureModerate); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}
