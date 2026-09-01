//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Promise benchmarks aligned with rust-oracle/benches/promise.rs (criterion
// `promise/resolver_new_resolve` and `promise/resolve_then_checkpoint`):
// the isolate and context are created once per benchmark; every iteration
// opens a fresh nested Scope, mirroring the oracle's fresh inner
// HandleScope. Differences in harness (criterion warm-up/sampling versus
// `go test -bench` defaults) must be accounted for when comparing numbers.

// BenchmarkPromiseResolverNewResolve mirrors promise/resolver_new_resolve:
// one resolver per iteration, resolved with the number 42, settlement state
// read afterwards.
func BenchmarkPromiseResolverNewResolve(b *testing.B) {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		resolver, err := scope.NewPromiseResolver(ctx)
		if err != nil {
			b.Fatal(err)
		}
		promise, err := resolver.GetPromise(inner)
		if err != nil {
			b.Fatal(err)
		}
		n42, err := inner.Number(42)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := resolver.Resolve(ctx, n42); err != nil {
			b.Fatal(err)
		}
		if _, err := promise.State(); err != nil {
			b.Fatal(err)
		}
		_ = inner.Close()
	}
}

// BenchmarkPromiseResolveThenCheckpoint mirrors
// promise/resolve_then_checkpoint: the full native promise round-trip —
// resolver creation, then with a native handler, resolution, and the
// microtask checkpoint that runs the reaction job — under the Explicit
// microtasks policy. The handler is created once outside the loop like the
// oracle's Global-rooted handler.
func BenchmarkPromiseResolveThenCheckpoint(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		b.Fatalf("SetMicrotasksPolicy: %v", err)
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
	handler, err := scope.NewNativeFunction(ctx, func(args []gov8.Value) (gov8.Value, bool) {
		return gov8.Value{}, false
	})
	if err != nil {
		b.Fatalf("NewNativeFunction: %v", err)
	}
	defer func() { _ = handler.Close() }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		resolver, err := scope.NewPromiseResolver(ctx)
		if err != nil {
			b.Fatal(err)
		}
		promise, err := resolver.GetPromise(inner)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := promise.Then(ctx, handler.Value()); err != nil {
			b.Fatal(err)
		}
		n42, err := inner.Number(42)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := resolver.Resolve(ctx, n42); err != nil {
			b.Fatal(err)
		}
		if err := iso.PerformMicrotaskCheckpoint(); err != nil {
			b.Fatal(err)
		}
		_ = inner.Close()
	}
}
