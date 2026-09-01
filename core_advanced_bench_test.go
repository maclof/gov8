//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// The eight core-advanced benchmarks specified by the pinned oracle's
// workload spec (rust-oracle/src/bin/conformance-core-advanced.rs, the
// "Benchmark workload spec" section): same sources, same shapes, one full
// operation per iteration, a fresh nested scope per iteration where locals
// are created. Harness differences (criterion: 1s warm-up, 3s measurement,
// 50 samples) must be accounted for when comparing against recorded oracle
// numbers; correctness is asserted once outside the timed loop.

func caBenchIsolate(b *testing.B) *gov8.Isolate {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	return iso
}

func caBenchContext(b *testing.B, iso *gov8.Isolate) *gov8.Context {
	b.Helper()
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	return ctx
}

// caBenchShared converts an isolate to shared once for a benchmark.
func caBenchShared(b *testing.B) *gov8.SharedIsolate {
	b.Helper()
	iso := caBenchIsolate(b)
	shared, err := iso.TryIntoShared()
	if err != nil {
		b.Fatalf("TryIntoShared: %v", err)
	}
	return shared
}

// BenchmarkLockerLockUnlockRoundtrip mirrors locker/lock_unlock_roundtrip:
// the shared isolate is held by the bench thread; per iteration Lock plus
// Locker.Close (no JS).
func BenchmarkLockerLockUnlockRoundtrip(b *testing.B) {
	shared := caBenchShared(b)
	defer func() { _ = shared.Close() }()
	// Correctness once, outside the timed loop.
	locker, err := shared.Lock()
	if err != nil {
		b.Fatalf("Lock: %v", err)
	}
	if err := locker.Close(); err != nil {
		b.Fatalf("Close: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		locker, err := shared.Lock()
		if err != nil {
			b.Fatal(err)
		}
		if err := locker.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLockerLockRunScript mirrors locker/lock_run_script: per iteration
// lock + fresh scope + context + run "40 + 2".
func BenchmarkLockerLockRunScript(b *testing.B) {
	shared := caBenchShared(b)
	defer func() { _ = shared.Close() }()
	// Correctness once.
	if got := lockedEvalBench(b, shared); got != 42 {
		b.Fatalf("run = %d, want 42", got)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := lockedEvalBench(b, shared); got != 42 {
			b.Fatalf("run = %d", got)
		}
	}
}

func lockedEvalBench(b *testing.B, shared *gov8.SharedIsolate) int64 {
	b.Helper()
	locker, err := shared.Lock()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = locker.Close() }()
	iso := locker.Isolate()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = ctx.Close() }()
	script, err := ctx.Compile(scope, "40 + 2", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(scope, nil)
	if err != nil {
		b.Fatal(err)
	}
	n, _, err := v.IntegerValue(ctx)
	if err != nil {
		b.Fatal(err)
	}
	return n
}

// BenchmarkScopeEscapableCreateEscape mirrors scope/escapable_create_escape:
// per iteration an escapable scope under the bench scope + one number +
// escape.
func BenchmarkScopeEscapableCreateEscape(b *testing.B) {
	iso := caBenchIsolate(b)
	defer func() { _ = iso.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	// Correctness once: the escaped value is usable and exact.
	func() {
		esc, err := scope.NewEscapableScope()
		if err != nil {
			b.Fatal(err)
		}
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		num, err := inner.Number(8)
		if err != nil {
			b.Fatal(err)
		}
		escaped, err := esc.Escape(num)
		if err != nil {
			b.Fatal(err)
		}
		_ = inner.Close()
		_ = esc.Close()
		if v, _ := escaped.NumberValueRaw(); v != 8 {
			b.Fatalf("escaped = %v", v)
		}
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		esc, err := scope.NewEscapableScope()
		if err != nil {
			b.Fatal(err)
		}
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		num, err := inner.Number(8)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := esc.Escape(num); err != nil {
			b.Fatal(err)
		}
		_ = inner.Close()
		_ = esc.Close()
	}
}

// BenchmarkContextNewWithSecurityToken mirrors
// context/new_with_security_token: per iteration Context::New plus a string
// security token set.
func BenchmarkContextNewWithSecurityToken(b *testing.B) {
	iso := caBenchIsolate(b)
	defer func() { _ = iso.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	ctx := caBenchContext(b, iso)
	token := func(ctx *gov8.Context) {
		b.Helper()
		tv, err := scope.NewString("shield")
		if err != nil {
			b.Fatal(err)
		}
		if err := ctx.SetSecurityToken(scope, tv); err != nil {
			b.Fatal(err)
		}
	}
	// Correctness once.
	token(ctx)
	got, err := ctx.GetSecurityToken(scope)
	if err != nil {
		b.Fatal(err)
	}
	want, err := scope.NewString("shield")
	if err != nil {
		b.Fatal(err)
	}
	if same, _ := got.SameValue(want); !same {
		b.Fatal("token roundtrip failed")
	}
	_ = ctx.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := caBenchContext(b, iso)
		token(ctx)
		_ = ctx.Close()
	}
}

// BenchmarkScriptCompileUnboundEager mirrors script/compile_unbound_eager:
// per iteration an eager unbound compile of the fib workload source.
func BenchmarkScriptCompileUnboundEager(b *testing.B) {
	iso := caBenchIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := caBenchContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	// Correctness once: the unbound script gets its own engine id.
	u, err := ctx.CompileUnbound(scope, benchFibIIFESource,
		&gov8.Origin{ResourceName: "eager.js"}, gov8.OptEagerCompile, nil)
	if err != nil {
		b.Fatal(err)
	}
	if id, _ := u.ID(); id == 0 {
		b.Fatal("unbound id = 0")
	}
	_ = u.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u, err := ctx.CompileUnbound(scope, benchFibIIFESource,
			&gov8.Origin{ResourceName: "eager.js"}, gov8.OptEagerCompile, nil)
		if err != nil {
			b.Fatal(err)
		}
		_ = u.Close()
	}
}

// BenchmarkScriptCodeCacheConsume mirrors script/code_cache_consume: the
// cache is produced once in setup; per iteration a byte copy +
// ConsumeCodeCache compile in a fresh context (the copy is part of the
// workload).
func BenchmarkScriptCodeCacheConsume(b *testing.B) {
	const source = benchFibIIFESource
	origin := &gov8.Origin{ResourceName: "cached.js"}

	producer := caBenchIsolate(b)
	pctx := caBenchContext(b, producer)
	pscope, err := producer.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	unbound, err := pctx.CompileUnbound(pscope, source, origin, gov8.OptNoCompileOptions, nil)
	if err != nil {
		b.Fatal(err)
	}
	cache, err := unbound.CreateCodeCache()
	if err != nil {
		b.Fatal(err)
	}
	_ = unbound.Close()
	_ = pscope.Close()
	_ = pctx.Close()
	_ = producer.Close()

	consumer := caBenchIsolate(b)
	defer func() { _ = consumer.Close() }()
	// Correctness once.
	func() {
		ctx := caBenchContext(b, consumer)
		defer func() { _ = ctx.Close() }()
		scope, err := consumer.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		defer func() { _ = scope.Close() }()
		script, rejected, err := ctx.CompileCached(scope, source, origin, cache, nil)
		if err != nil || rejected {
			b.Fatalf("consume failed (%v, rejected=%v)", err, rejected)
		}
		defer func() { _ = script.Close() }()
		v, err := script.Run(scope, nil)
		if err != nil {
			b.Fatal(err)
		}
		if n, _, _ := v.IntegerValue(ctx); n != 144 {
			b.Fatalf("run = %d, want 144", n)
		}
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		func() {
			ctx := caBenchContext(b, consumer)
			defer func() { _ = ctx.Close() }()
			scope, err := consumer.NewScope()
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = scope.Close() }()
			script, rejected, err := ctx.CompileCached(scope, source, origin, cache, nil)
			if err != nil || rejected {
				b.Fatalf("consume failed (%v, rejected=%v)", err, rejected)
			}
			_ = script.Close()
		}()
	}
}

// BenchmarkMessageCaptureStackTrace mirrors message/capture_stack_trace:
// per iteration StackTrace::current_stack_trace(16) inside a native
// callback invoked from JS (JS driver -> native fn -> capture, depth ~3).
func BenchmarkMessageCaptureStackTrace(b *testing.B) {
	iso := caBenchIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := caBenchContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()

	host, err := iso.NewFunction(scope, ctx, func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		trace, ok, err := cs.Scope().CurrentStackTrace(16)
		if err != nil || !ok {
			b.Error("no stack trace")
		} else if count, _ := trace.FrameCount(); count == 0 {
			b.Error("empty stack trace")
		}
		_ = rv.SetInt32(1)
	}, nil)
	if err != nil {
		b.Fatal(err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := global.SetByName(scope, ctx, "host", host.Value); err != nil {
		b.Fatal(err)
	}
	// A JS driver so the capture happens under real JS frames.
	script, cerr := ctx.Compile(scope, "function drive() { return host(); }", nil)
	if cerr != nil {
		b.Fatal(cerr)
	}
	if _, rerr := script.Run(scope, nil); rerr != nil {
		b.Fatal(rerr)
	}
	_ = script.Close()
	driveVal, found, gerr := global.GetByName(scope, ctx, "drive")
	if gerr != nil || !found {
		b.Fatalf("drive fn: %v/%v", found, gerr)
	}
	// Correctness once: the capture sees real frames.
	if _, err := gov8.CallFunction(ctx, scope, driveVal, mustUndef(b, scope), nil, nil); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := gov8.CallFunction(ctx, scope, driveVal, mustUndef(b, scope), nil, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func mustUndef(b *testing.B, scope *gov8.Scope) gov8.Value {
	b.Helper()
	v, err := scope.Undefined()
	if err != nil {
		b.Fatal(err)
	}
	return v
}

// BenchmarkHeapGetHeapStatistics mirrors heap/get_heap_statistics: per
// iteration GetHeapStatistics plus a read of UsedHeapSize.
func BenchmarkHeapGetHeapStatistics(b *testing.B) {
	iso := caBenchIsolate(b)
	defer func() { _ = iso.Close() }()
	// Correctness once.
	stats, err := iso.GetHeapStatistics()
	if err != nil {
		b.Fatal(err)
	}
	if stats.UsedHeapSize == 0 {
		b.Fatal("used heap size is zero")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stats, err := iso.GetHeapStatistics()
		if err != nil {
			b.Fatal(err)
		}
		if stats.UsedHeapSize == 0 {
			b.Fatal("used heap size is zero")
		}
	}
}
