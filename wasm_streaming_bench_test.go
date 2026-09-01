//go:build windows && amd64

package gov8_test

import (
	"runtime"
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

func BenchmarkWasmModuleCompilationAnswerModule(b *testing.B) {
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
	defer func() {
		if err := scope.Close(); err != nil {
			b.Errorf("close scope: %v", err)
		}
		if err := ctx.Close(); err != nil {
			b.Errorf("close context: %v", err)
		}
		if err := gov8.ReleaseIsolateHostState(iso); err != nil {
			b.Errorf("release host state: %v", err)
		}
		if err := iso.Close(); err != nil {
			b.Errorf("close isolate: %v", err)
		}
	}()
	b.ReportAllocs()
	b.SetBytes(int64(len(answerWasmModule)))
	b.ResetTimer()
	for range b.N {
		compilation, err := gov8.NewWasmModuleCompilation()
		if err != nil {
			b.Fatal(err)
		}
		if err := compilation.OnBytesReceived(answerWasmModule); err != nil {
			b.Fatal(err)
		}
		var calls atomic.Int32
		if err := compilation.Finish(scope, ctx, nil, func(result *gov8.WasmModuleCompilationResult) {
			if result.Module == nil {
				panic("wasm benchmark compilation failed")
			}
			calls.Add(1)
		}); err != nil {
			b.Fatal(err)
		}
		for calls.Load() == 0 {
			ran, err := iso.PumpMessageLoop(false)
			if err != nil {
				b.Fatal(err)
			}
			if !ran {
				runtime.Gosched()
			}
		}
		if calls.Load() != 1 {
			b.Fatalf("resolution callback calls = %d", calls.Load())
		}
	}
}
