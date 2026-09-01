//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Native callback benchmarks, workload-for-workload comparable with the
// pinned oracle's benches/callback.rs:
//
//   - callback/native_call_from_js:  one precompiled script per iteration
//     calls a native add(a, b) twice; the callback returns via
//     ReturnValue.SetInt32.
//   - callback/native_call_from_host: the host calls the same-shaped native
//     function once per iteration with an undefined receiver and two number
//     arguments.
//   - callback/function_new_call:    Function creation plus a single host
//     call per iteration.
//
// The isolate and context are created once per benchmark; each iteration
// opens and closes a fresh Scope (the HandleScope equivalent). Unlike the
// oracle, the function_new_call workload must periodically release the
// per-function native callback registrations (the Go dispatch design keeps
// one small shim context per created function); the release amortizes to
// O(1) per iteration and is part of the measured workload.

func benchAddCb(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	a := cbIntOrZero(cs, args, 0)
	b := cbIntOrZero(cs, args, 1)
	_ = rv.SetInt32(int32(a + b))
}

func BenchmarkCallbackNativeCallFromJS(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()

	f, err := iso.NewFunction(scope, ctx, benchAddCb, nil)
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		if _, err := script.Run(inner, nil); err != nil {
			b.Fatalf("Run: %v", err)
		}
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
}

func BenchmarkCallbackNativeCallFromHost(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = setup.Close() }()

	f, err := iso.NewFunction(setup, ctx, benchAddCb, nil)
	if err != nil {
		b.Fatalf("NewFunction: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		undef, err := inner.Undefined()
		if err != nil {
			b.Fatalf("Undefined: %v", err)
		}
		a, err := inner.Number(20)
		if err != nil {
			b.Fatalf("Number: %v", err)
		}
		c, err := inner.Number(22)
		if err != nil {
			b.Fatalf("Number: %v", err)
		}
		if _, _, err := f.Call(inner, undef, a, c); err != nil {
			b.Fatalf("Call: %v", err)
		}
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
}

func BenchmarkCallbackFunctionNewCall(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()

	// Bound the growth of per-function dispatch registrations.
	const releaseEvery = 512

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		f, err := iso.NewFunction(inner, ctx, benchAddCb, nil)
		if err != nil {
			b.Fatalf("NewFunction: %v", err)
		}
		undef, err := inner.Undefined()
		if err != nil {
			b.Fatalf("Undefined: %v", err)
		}
		a, err := inner.Number(20)
		if err != nil {
			b.Fatalf("Number: %v", err)
		}
		c, err := inner.Number(22)
		if err != nil {
			b.Fatalf("Number: %v", err)
		}
		if _, _, err := f.Call(inner, undef, a, c); err != nil {
			b.Fatalf("Call: %v", err)
		}
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
		if (i+1)%releaseEvery == 0 {
			if err := gov8.ReleaseIsolateHostState(iso); err != nil {
				b.Fatalf("ReleaseIsolateHostState: %v", err)
			}
		}
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		b.Fatalf("final ReleaseIsolateHostState: %v", err)
	}
}
