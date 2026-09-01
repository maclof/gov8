//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Promise benchmarks aligned with rust-oracle/benches/promise.rs. Setup,
// validation probes, and teardown are untimed; every measured iteration
// opens a fresh nested Scope and repeats the Rust workload's settlement
// assertions.

func benchRunAssertedResolverRoundtrip(b *testing.B, ctx *gov8.Context, scope *gov8.Scope) {
	resolver, err := scope.NewPromiseResolver(ctx)
	if err != nil {
		b.Fatalf("promise/resolver_new_resolve: NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(scope)
	if err != nil {
		b.Fatalf("promise/resolver_new_resolve: GetPromise: %v", err)
	}
	n42, err := scope.Number(42)
	if err != nil {
		b.Fatalf("promise/resolver_new_resolve: Number(42): %v", err)
	}
	resolved, err := resolver.Resolve(ctx, n42)
	if err != nil {
		b.Fatalf("promise/resolver_new_resolve: Resolve: %v", err)
	}
	if !resolved {
		b.Fatal("promise/resolver_new_resolve: Resolve did not report success")
	}
	state, err := promise.State()
	if err != nil {
		b.Fatalf("promise/resolver_new_resolve: State: %v", err)
	}
	if state != gov8.PromiseFulfilled {
		b.Fatalf("promise/resolver_new_resolve: state = %v, want Fulfilled", state)
	}
	result, err := promise.Result(scope)
	if err != nil {
		b.Fatalf("promise/resolver_new_resolve: Result: %v", err)
	}
	benchAssertNumber(b, ctx, result, 42, "promise/resolver_new_resolve")
}

func benchPromiseHandler(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
}

func benchRunAssertedThenCheckpoint(b *testing.B, iso *gov8.Isolate, ctx *gov8.Context, scope *gov8.Scope, handlerGlobal *gov8.Global) {
	resolver, err := scope.NewPromiseResolver(ctx)
	if err != nil {
		b.Fatalf("promise/resolve_then_checkpoint: NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(scope)
	if err != nil {
		b.Fatalf("promise/resolve_then_checkpoint: GetPromise: %v", err)
	}
	handler, err := handlerGlobal.ToLocal(scope)
	if err != nil {
		b.Fatalf("promise/resolve_then_checkpoint: Global.ToLocal: %v", err)
	}
	derived, err := promise.Then(ctx, handler)
	if err != nil {
		b.Fatalf("promise/resolve_then_checkpoint: Then: %v", err)
	}
	n42, err := scope.Int32(42)
	if err != nil {
		b.Fatalf("promise/resolve_then_checkpoint: Int32(42): %v", err)
	}
	resolved, err := resolver.Resolve(ctx, n42)
	if err != nil {
		b.Fatalf("promise/resolve_then_checkpoint: Resolve: %v", err)
	}
	if !resolved {
		b.Fatal("promise/resolve_then_checkpoint: Resolve did not report success")
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		b.Fatalf("promise/resolve_then_checkpoint: PerformMicrotaskCheckpoint: %v", err)
	}
	state, err := derived.State()
	if err != nil {
		b.Fatalf("promise/resolve_then_checkpoint: derived State: %v", err)
	}
	if state != gov8.PromiseFulfilled {
		b.Fatalf("promise/resolve_then_checkpoint: derived state = %v, want Fulfilled", state)
	}
}

func BenchmarkPromiseResolverNewResolve(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()

	benchRunAssertedResolverRoundtrip(b, ctx, scope)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		benchRunAssertedResolverRoundtrip(b, ctx, inner)
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
	b.StopTimer()
}

func BenchmarkPromiseResolveThenCheckpoint(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = gov8.ReleaseIsolateHostState(iso) }()
	if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		b.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	handler, err := iso.NewFunction(scope, ctx, benchPromiseHandler, nil)
	if err != nil {
		b.Fatalf("NewFunction: %v", err)
	}
	handlerGlobal, err := gov8.NewGlobal(scope, handler.Value)
	if err != nil {
		b.Fatalf("NewGlobal: %v", err)
	}
	defer func() { _ = handlerGlobal.Close() }()

	benchRunAssertedThenCheckpoint(b, iso, ctx, scope, handlerGlobal)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		benchRunAssertedThenCheckpoint(b, iso, ctx, inner, handlerGlobal)
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
	b.StopTimer()
}
