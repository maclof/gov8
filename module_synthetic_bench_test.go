//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

func syntheticBenchmarkRuntime(b *testing.B) (*gov8.Isolate, *gov8.Context) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	return iso, ctx
}

func syntheticBenchmarkCallback(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
	value, err := e.Scope().Scope().Int32(42)
	if err != nil {
		return gov8.Value{}, err
	}
	if err := e.SetExport("answer", value); err != nil {
		return gov8.Value{}, err
	}
	return e.Scope().Scope().Undefined()
}

func BenchmarkSyntheticModuleCreate(b *testing.B) {
	iso, ctx := syntheticBenchmarkRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		module, err := ctx.NewSyntheticModule(scope, "benchmark-synthetic", []string{"answer"}, syntheticBenchmarkCallback)
		if err != nil {
			b.Fatal(err)
		}
		if err := module.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSyntheticModuleCreateInstantiateEvaluate(b *testing.B) {
	iso, ctx := syntheticBenchmarkRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		module, err := ctx.NewSyntheticModule(scope, "benchmark-synthetic", []string{"answer"}, syntheticBenchmarkCallback)
		if err != nil {
			b.Fatal(err)
		}
		linked, err := module.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil)
		if err != nil || !linked {
			b.Fatalf("Instantiate = %v, %v", linked, err)
		}
		if _, err := module.EvaluateValue(scope, nil); err != nil {
			b.Fatal(err)
		}
		if err := module.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
