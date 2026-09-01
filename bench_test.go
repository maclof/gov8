//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Benchmarks aligned with rust-oracle/benches (criterion): the same workloads
// and shapes. Differences in harness (criterion: 1s warm-up, 3s measurement,
// 50 samples; go test -bench: defaults) must be accounted for when comparing
// against the recorded oracle numbers.
//
// Each iteration that creates locals opens a fresh Scope, mirroring the
// oracle's fresh nested HandleScope per iteration.

const (
	benchMinimalSource  = "1 + 1"
	benchWorkloadSource = "function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }" +
		"fib(12) + '|' + (2 + 3) + '|' + String(1.5).toUpperCase()"
	benchMinimalResult  = int32(2)
	benchWorkloadResult = "144|5|1.5"
)

func benchNewIsolate(b *testing.B) *gov8.Isolate {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	return iso
}

func benchNewContext(b *testing.B, iso *gov8.Isolate) *gov8.Context {
	b.Helper()
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	return ctx
}

type benchCloser interface {
	Close() error
}

func benchClosePersistent(b *testing.B, name string, closer benchCloser) {
	b.Helper()
	if err := closer.Close(); err != nil {
		b.Errorf("%s.Close: %v", name, err)
	}
}

// BenchmarkStartupIsolateNewDispose mirrors startup/isolate_new_dispose.
func BenchmarkStartupIsolateNewDispose(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		iso := benchNewIsolate(b)
		if err := iso.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStartupIsolateContextNewDispose mirrors
// startup/isolate_context_new_dispose.
func BenchmarkStartupIsolateContextNewDispose(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		iso := benchNewIsolate(b)
		ctx := benchNewContext(b, iso)
		if err := ctx.Close(); err != nil {
			b.Fatal(err)
		}
		if err := iso.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStartupContextNewDispose mirrors startup/context_new_dispose.
func BenchmarkStartupContextNewDispose(b *testing.B) {
	iso := benchNewIsolate(b)
	defer benchClosePersistent(b, "Isolate", iso)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := benchNewContext(b, iso)
		if err := ctx.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkScriptCompileMinimal mirrors script/compile_minimal.
func BenchmarkScriptCompileMinimal(b *testing.B) {
	iso := benchNewIsolate(b)
	defer benchClosePersistent(b, "Isolate", iso)
	ctx := benchNewContext(b, iso)
	defer benchClosePersistent(b, "Context", ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		script, err := ctx.Compile(scope, benchMinimalSource, nil)
		if err != nil {
			b.Fatal(err)
		}
		if err := script.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkScriptCompileWorkload mirrors script/compile_workload.
func BenchmarkScriptCompileWorkload(b *testing.B) {
	iso := benchNewIsolate(b)
	defer benchClosePersistent(b, "Isolate", iso)
	ctx := benchNewContext(b, iso)
	defer benchClosePersistent(b, "Context", ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		script, err := ctx.Compile(scope, benchWorkloadSource, nil)
		if err != nil {
			b.Fatal(err)
		}
		if err := script.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkScriptCompileAndRunMinimal mirrors script/compile_and_run_minimal.
func BenchmarkScriptCompileAndRunMinimal(b *testing.B) {
	iso := benchNewIsolate(b)
	defer benchClosePersistent(b, "Isolate", iso)
	ctx := benchNewContext(b, iso)
	defer benchClosePersistent(b, "Context", ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		script, err := ctx.Compile(scope, benchMinimalSource, nil)
		if err != nil {
			b.Fatal(err)
		}
		result, err := script.Run(scope, nil)
		if err != nil {
			b.Fatal(err)
		}
		got, ok, err := result.Int32Value(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if !ok || got != benchMinimalResult {
			b.Fatalf("result = %d, %v; want %d, true", got, ok, benchMinimalResult)
		}
		if err := script.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkScriptCompileAndRunWorkload mirrors
// script/compile_and_run_workload.
func BenchmarkScriptCompileAndRunWorkload(b *testing.B) {
	iso := benchNewIsolate(b)
	defer benchClosePersistent(b, "Isolate", iso)
	ctx := benchNewContext(b, iso)
	defer benchClosePersistent(b, "Context", ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		script, err := ctx.Compile(scope, benchWorkloadSource, nil)
		if err != nil {
			b.Fatal(err)
		}
		result, err := script.Run(scope, nil)
		if err != nil {
			b.Fatal(err)
		}
		got, err := result.ToString(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if got != benchWorkloadResult {
			b.Fatalf("result = %q; want %q", got, benchWorkloadResult)
		}
		if err := script.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkScriptRunPrecompiledWorkload mirrors
// script/run_precompiled_workload: the script is compiled once and rooted in
// a persistent handle; only execution is measured per iteration.
func BenchmarkScriptRunPrecompiledWorkload(b *testing.B) {
	iso := benchNewIsolate(b)
	defer benchClosePersistent(b, "Isolate", iso)
	ctx := benchNewContext(b, iso)
	defer benchClosePersistent(b, "Context", ctx)
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	script, err := ctx.Compile(scope, benchWorkloadSource, nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		b.Fatal(err)
	}
	defer benchClosePersistent(b, "Script", script)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		result, err := script.Run(scope, nil)
		if err != nil {
			b.Fatal(err)
		}
		got, err := result.ToString(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if got != benchWorkloadResult {
			b.Fatalf("result = %q; want %q", got, benchWorkloadResult)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkFFIStringRoundtrip measures one string construction plus a full
// ToString conversion through the shim (two transitions + engine work).
func BenchmarkFFIStringRoundtrip(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := scope.NewString("hello oracle")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.ToString(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
