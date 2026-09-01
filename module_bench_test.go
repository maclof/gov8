//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const (
	moduleBenchmarkDependencySpecifier = "export const base = 40;"
	moduleBenchmarkDependencySource    = "export const base = 40;"
	moduleBenchmarkEntrySource         = "import { base } from 'export const base = 40;'; export const answer = base + 2;"
)

func instantiateModuleBenchmarkGraph(ctx *gov8.Context, scope *gov8.Scope,
	entry *gov8.Module) (dependency *gov8.Module, linked bool, err error) {
	linked, err = entry.Instantiate(scope, func(request gov8.ModuleResolveRequest) (*gov8.Module, error) {
		if request.Specifier != moduleBenchmarkDependencySpecifier {
			return nil, fmt.Errorf("unexpected benchmark dependency %q", request.Specifier)
		}
		if len(request.Attributes) != 0 {
			return nil, fmt.Errorf("unexpected benchmark import attributes: %v", request.Attributes)
		}
		dependency, err = ctx.CompileModule(scope, moduleBenchmarkDependencySource, "dependency.mjs", nil)
		return dependency, err
	}, nil)
	return dependency, linked, err
}

func probeModuleBenchmarkGraph(b *testing.B, iso *gov8.Isolate, ctx *gov8.Context) {
	b.Helper()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer scope.Close()
	entry, err := ctx.CompileModule(scope, moduleBenchmarkEntrySource, "entry.mjs", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer entry.Close()
	dependency, linked, err := instantiateModuleBenchmarkGraph(ctx, scope, entry)
	if dependency != nil {
		defer dependency.Close()
	}
	if err != nil || !linked {
		b.Fatalf("correctness probe Instantiate = %v, %v", linked, err)
	}
	if _, err := entry.Evaluate(scope, nil); err != nil {
		b.Fatal(err)
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		b.Fatal(err)
	}
	namespace, err := entry.Namespace(scope)
	if err != nil {
		b.Fatal(err)
	}
	object, err := gov8.AsObject(namespace)
	if err != nil {
		b.Fatal(err)
	}
	answer, ok, err := object.GetByName(scope, ctx, "answer")
	if err != nil || !ok {
		b.Fatalf("correctness probe namespace.answer = ok %v, err %v", ok, err)
	}
	value, ok, err := answer.IntegerValue(ctx)
	if err != nil || !ok || value != 42 {
		b.Fatalf("correctness probe answer = %d, ok %v, err %v", value, ok, err)
	}
}

func newModuleBenchmarkRuntime(b *testing.B) (*gov8.Isolate, *gov8.Context) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		b.Fatal(err)
	}
	probeModuleBenchmarkGraph(b, iso, ctx)
	return iso, ctx
}

func BenchmarkModuleCompile(b *testing.B) {
	iso, ctx := newModuleBenchmarkRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		module, err := ctx.CompileModule(scope, moduleBenchmarkEntrySource, "entry.mjs", nil)
		if err != nil {
			_ = scope.Close()
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

func BenchmarkModuleCompileInstantiate(b *testing.B) {
	iso, ctx := newModuleBenchmarkRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		entry, err := ctx.CompileModule(scope, moduleBenchmarkEntrySource, "entry.mjs", nil)
		if err != nil {
			_ = scope.Close()
			b.Fatal(err)
		}
		dependency, linked, err := instantiateModuleBenchmarkGraph(ctx, scope, entry)
		if err != nil || !linked {
			b.Fatalf("Instantiate = %v, %v", linked, err)
		}
		if err := dependency.Close(); err != nil {
			b.Fatal(err)
		}
		if err := entry.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkModuleCompileInstantiateEvaluate(b *testing.B) {
	iso, ctx := newModuleBenchmarkRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		entry, err := ctx.CompileModule(scope, moduleBenchmarkEntrySource, "entry.mjs", nil)
		if err != nil {
			_ = scope.Close()
			b.Fatal(err)
		}
		dependency, linked, err := instantiateModuleBenchmarkGraph(ctx, scope, entry)
		if err != nil || !linked {
			b.Fatalf("Instantiate = %v, %v", linked, err)
		}
		if _, err := entry.Evaluate(scope, nil); err != nil {
			b.Fatal(err)
		}
		if err := dependency.Close(); err != nil {
			b.Fatal(err)
		}
		if err := entry.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
