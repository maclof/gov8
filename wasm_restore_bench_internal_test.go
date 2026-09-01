//go:build windows && amd64

package gov8

import (
	"syscall"
	"testing"
	"unsafe"
)

var wasmRestoreBenchmarkModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
}

func setupWasmRestoreBenchmark(b *testing.B) (*Isolate, *Context, *Scope, *CompiledWasmModule) {
	b.Helper()
	iso, err := NewIsolate()
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
	module, err := ctx.CompileWasmModule(scope, wasmRestoreBenchmarkModule, nil)
	if err != nil {
		b.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		b.Fatal(err)
	}
	return iso, ctx, scope, compiled
}

// BenchmarkWasmFromCompiledExistingScope separates the public restoration
// call from the fresh nested HandleScope enter/exit required by the paired
// rusty_v8 benchmark. It is diagnostic rather than a cross-language result.
func BenchmarkWasmFromCompiledExistingScope(b *testing.B) {
	iso, ctx, scope, compiled := setupWasmRestoreBenchmark(b)
	defer func() { _ = compiled.Close(); _ = scope.Close(); _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ctx.WasmModuleFromCompiled(scope, compiled); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWasmFromCompiledNativeFloor measures the fixed-arity DLL/V8 call
// alone. It deliberately excludes Go validation, ownership locking, wrapper
// construction, and HandleScope transitions, so it is a lower bound rather
// than a public-API alternative.
func BenchmarkWasmFromCompiledNativeFloor(b *testing.B) {
	iso, ctx, scope, compiled := setupWasmRestoreBenchmark(b)
	defer func() { _ = compiled.Close(); _ = scope.Close(); _ = ctx.Close(); _ = iso.Close() }()
	wasmFromCompiledProcOnce.Do(resolveWasmFromCompiledProc)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out uintptr
		r1, _, _ := syscall.Syscall6(wasmFromCompiledProcAddr, 5,
			iso.handle, ctx.handle, scope.handle, compiled.handle,
			uintptr(unsafe.Pointer(&out)), 0)
		if int64(r1) < 0 || out == 0 {
			b.Fatalf("native restore status=%d output=%#x", int64(r1), out)
		}
	}
}
