//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

func BenchmarkWasmSyncCompileAnswerModule(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := ctx.CompileWasmModule(scope, answerWasmModule, nil); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWasmFromCompiledAnswerModule(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	setupScope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	module, err := ctx.CompileWasmModule(setupScope, answerWasmModule, nil)
	if err != nil {
		b.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		b.Fatal(err)
	}
	if err := setupScope.Close(); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = compiled.Close(); _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := ctx.WasmModuleFromCompiled(scope, compiled); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
