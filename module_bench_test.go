//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

func BenchmarkModuleCompile(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close(); _ = ctx.Close(); _ = iso.Close() }()
	source := "import { x } from './dep.mjs'; export const answer = x + 1;"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, err := ctx.CompileModule(scope, source, "bench.mjs", nil)
		if err != nil {
			b.Fatal(err)
		}
		if err := m.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkModuleStatusAndIdentity(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	m, err := ctx.CompileModule(scope, "export const answer = 42;", "bench-status.mjs", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = m.Close(); _ = scope.Close(); _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Status(); err != nil {
			b.Fatal(err)
		}
		if _, err := m.IdentityHash(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkModuleCompileInstantiate(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	defer func() { _ = scope.Close(); _ = ctx.Close(); _ = iso.Close() }()
	const entrySource = "import { base } from './dep.mjs'; export const answer = base + 2;"
	const depSource = "export const base = 40;"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry, err := ctx.CompileModule(scope, entrySource, "entry.mjs", nil)
		if err != nil {
			b.Fatal(err)
		}
		dep, err := ctx.CompileModule(scope, depSource, "dep.mjs", nil)
		if err != nil {
			b.Fatal(err)
		}
		linked, err := entry.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
			return dep, nil
		}, nil)
		if err != nil || !linked {
			b.Fatalf("Instantiate = %v, %v", linked, err)
		}
		if err := dep.Close(); err != nil {
			b.Fatal(err)
		}
		if err := entry.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkModuleLinkEvaluateGraph(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	defer func() { _ = scope.Close(); _ = ctx.Close(); _ = iso.Close() }()
	const entrySource = "import { base } from './dep.mjs'; export const answer = base + 2;"
	const depSource = "export const base = 40;"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry, err := ctx.CompileModule(scope, entrySource, "entry.mjs", nil)
		if err != nil {
			b.Fatal(err)
		}
		dep, err := ctx.CompileModule(scope, depSource, "dep.mjs", nil)
		if err != nil {
			b.Fatal(err)
		}
		linked, err := entry.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
			return dep, nil
		}, nil)
		if err != nil || !linked {
			b.Fatalf("Instantiate = %v, %v", linked, err)
		}
		if _, err := entry.Evaluate(scope, nil); err != nil {
			b.Fatal(err)
		}
		if err := dep.Close(); err != nil {
			b.Fatal(err)
		}
		if err := entry.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
