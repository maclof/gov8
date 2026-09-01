//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Native callback benchmarks, workload-for-workload comparable with the
// pinned oracle's benches/callback.rs. Setup, validation probes, and teardown
// are untimed; every measured iteration opens and closes a fresh Scope and
// performs the same result validation as the Rust workload.

const (
	benchCallbackExpectedJSResult   = 342.0
	benchCallbackExpectedHostResult = 42.0
)

func benchAddCb(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	a := cbIntOrZero(cs, args, 0)
	b := cbIntOrZero(cs, args, 1)
	_ = rv.SetInt32(int32(a + b))
}

func benchAssertNumber(b *testing.B, ctx *gov8.Context, value gov8.Value, expected float64, name string) {
	got, ok, err := value.NumberValue(ctx)
	if err != nil {
		b.Fatalf("%s: NumberValue: %v", name, err)
	}
	if !ok {
		b.Fatalf("%s: workload result is not a number", name)
	}
	if got != expected {
		b.Fatalf("%s: workload result = %v, want %v", name, got, expected)
	}
}

func benchRunAssertedJSCallback(b *testing.B, ctx *gov8.Context, scope *gov8.Scope, script *gov8.Script) {
	value, err := script.Run(scope, nil)
	if err != nil {
		b.Fatalf("callback/native_call_from_js: Run: %v", err)
	}
	benchAssertNumber(b, ctx, value, benchCallbackExpectedJSResult, "callback/native_call_from_js")
}

func benchRunAssertedHostCallback(b *testing.B, ctx *gov8.Context, scope *gov8.Scope, function gov8.Value) {
	a, err := scope.Int32(20)
	if err != nil {
		b.Fatalf("callback/native_call_from_host: Int32(20): %v", err)
	}
	c, err := scope.Int32(22)
	if err != nil {
		b.Fatalf("callback/native_call_from_host: Int32(22): %v", err)
	}
	undefined, err := scope.Undefined()
	if err != nil {
		b.Fatalf("callback/native_call_from_host: Undefined: %v", err)
	}
	value, err := gov8.CallFunction(ctx, scope, function, undefined, []gov8.Value{a, c}, nil)
	if err != nil {
		b.Fatalf("callback/native_call_from_host: CallFunction: %v", err)
	}
	benchAssertNumber(b, ctx, value, benchCallbackExpectedHostResult, "callback/native_call_from_host")
}

func BenchmarkCallbackNativeCallFromJS(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = gov8.ReleaseIsolateHostState(iso) }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()

	f, err := iso.NewFunction(scope, ctx, benchAddCb, &gov8.FunctionOptions{Length: 2})
	if err != nil {
		b.Fatalf("NewFunction: %v", err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		b.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(scope, ctx, "add", f.Value); err != nil {
		b.Fatalf("SetByName: %v", err)
	}
	script, err := ctx.Compile(scope, "add(20, 22) + add(100, 200)", nil)
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	defer func() { _ = script.Close() }()

	benchRunAssertedJSCallback(b, ctx, scope, script)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		benchRunAssertedJSCallback(b, ctx, inner, script)
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
	b.StopTimer()
}

func BenchmarkCallbackNativeCallFromHost(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = gov8.ReleaseIsolateHostState(iso) }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = setup.Close() }()

	f, err := iso.NewFunction(setup, ctx, benchAddCb, &gov8.FunctionOptions{Length: 2})
	if err != nil {
		b.Fatalf("NewFunction: %v", err)
	}
	fGlobal, err := gov8.NewGlobal(setup, f.Value)
	if err != nil {
		b.Fatalf("NewGlobal: %v", err)
	}
	defer func() { _ = fGlobal.Close() }()
	probeFunction, err := fGlobal.ToLocal(setup)
	if err != nil {
		b.Fatalf("probe Global.ToLocal: %v", err)
	}
	benchRunAssertedHostCallback(b, ctx, setup, probeFunction)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		function, err := fGlobal.ToLocal(inner)
		if err != nil {
			b.Fatalf("Global.ToLocal: %v", err)
		}
		benchRunAssertedHostCallback(b, ctx, inner, function)
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
	b.StopTimer()
}

func BenchmarkCallbackFunctionNewCall(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()

	// Validate the exact workload once before measurement.
	probeScope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("probe NewScope: %v", err)
	}
	probeFunction, err := iso.NewFunction(probeScope, ctx, benchAddCb, &gov8.FunctionOptions{Length: 2})
	if err != nil {
		b.Fatalf("probe NewFunction: %v", err)
	}
	benchRunAssertedHostCallback(b, ctx, probeScope, probeFunction.Value)
	if err := probeScope.Close(); err != nil {
		b.Fatalf("probeScope.Close: %v", err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		b.Fatalf("probe ReleaseIsolateHostState: %v", err)
	}

	// Go keeps one callback registration per created function until explicit
	// host-state release. Bound that storage outside the measured intervals;
	// it is lifecycle maintenance, not part of the Rust workload.
	const releaseEvery = 512

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		f, err := iso.NewFunction(inner, ctx, benchAddCb, &gov8.FunctionOptions{Length: 2})
		if err != nil {
			b.Fatalf("NewFunction: %v", err)
		}
		benchRunAssertedHostCallback(b, ctx, inner, f.Value)
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
		if (i+1)%releaseEvery == 0 {
			b.StopTimer()
			if err := gov8.ReleaseIsolateHostState(iso); err != nil {
				b.Fatalf("ReleaseIsolateHostState: %v", err)
			}
			b.StartTimer()
		}
	}
	b.StopTimer()
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		b.Fatalf("final ReleaseIsolateHostState: %v", err)
	}
}
