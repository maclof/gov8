//go:build windows && amd64

package gov8_test

import (
	"runtime"
	"testing"

	gov8 "github.com/maclof/gov8"
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
	// The pinned Rust workload holds a ContextScope around setup and every
	// timed nested HandleScope restoration. Keep the matching context entry
	// outside the timer; WasmModuleFromCompiled still preserves its standalone
	// behavior for callers that do not pre-enter the context.
	contextScope, err := ctx.Enter()
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
	defer func() {
		_ = compiled.Close()
		_ = contextScope.Close()
		_ = ctx.Close()
		_ = iso.Close()
	}()
	probeScope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	probe, err := ctx.WasmModuleFromCompiled(probeScope, compiled)
	if err != nil {
		b.Fatal(err)
	}
	isModule := false
	if probe != nil {
		isModule, err = probe.IsWasmModuleObject()
	}
	if err != nil {
		b.Fatal(err)
	}
	if !isModule {
		b.Fatal("restored value is not a WebAssembly.Module")
	}
	if err := probeScope.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		restored, err := ctx.WasmModuleFromCompiled(scope, compiled)
		if err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(restored)
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkWasmFromCompiledAnswerModuleCrossIsolate measures the same
// restoration operation after the producing isolate has been disposed. The
// compiled module is shareable, while each restored module remains local to a
// fresh scope in the consumer isolate.
func BenchmarkWasmFromCompiledAnswerModuleCrossIsolate(b *testing.B) {
	producer, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	producerContext, err := producer.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	producerScope, err := producer.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	module, err := producerContext.CompileWasmModule(producerScope, answerWasmModule, nil)
	if err != nil {
		b.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		b.Fatal(err)
	}
	if err := producerScope.Close(); err != nil {
		b.Fatal(err)
	}
	if err := producerContext.Close(); err != nil {
		b.Fatal(err)
	}
	if err := producer.Close(); err != nil {
		b.Fatal(err)
	}
	defer compiled.Close()

	consumer, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	consumerContext, err := consumer.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = consumerContext.Close(); _ = consumer.Close() }()

	probeScope, err := consumer.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	probe, err := consumerContext.WasmModuleFromCompiled(probeScope, compiled)
	if err != nil {
		b.Fatal(err)
	}
	isModule := false
	if probe != nil {
		isModule, err = probe.IsWasmModuleObject()
	}
	if err != nil {
		b.Fatal(err)
	}
	if !isModule {
		b.Fatal("cross-isolate restored value is not a WebAssembly.Module")
	}
	if err := probeScope.Close(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := consumer.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		restored, err := consumerContext.WasmModuleFromCompiled(scope, compiled)
		if err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(restored)
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}
