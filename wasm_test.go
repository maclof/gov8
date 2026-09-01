//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

var emptyWasmModule = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
var answerWasmModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
}

func TestWasmModuleCompileCompiledRoundTrip(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	module, err := ctx.CompileWasmModule(scope, answerWasmModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := module.IsWasmModuleObject(); err != nil || !ok {
		t.Fatalf("IsWasmModuleObject = %v, %v", ok, err)
	}
	if ok, err := module.IsWasmMemoryObject(); err != nil || ok {
		t.Fatalf("IsWasmMemoryObject = %v, %v", ok, err)
	}
	empty, err := ctx.CompileWasmModule(scope, emptyWasmModule, nil)
	if err != nil {
		t.Fatalf("empty MVP module: %v", err)
	}
	if ok, err := empty.IsWasmModuleObject(); err != nil || !ok {
		t.Fatalf("empty MVP predicate = %v, %v", ok, err)
	}
	emptyCompiled, err := empty.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	defer emptyCompiled.Close()
	if wire, err := emptyCompiled.WireBytes(); err != nil || !bytes.Equal(wire, emptyWasmModule) {
		t.Fatalf("empty MVP wire = %v, %v", wire, err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Close()
	wire, err := compiled.WireBytes()
	if err != nil || !bytes.Equal(wire, answerWasmModule) {
		t.Fatalf("WireBytes = %v, %v", wire, err)
	}
	if url, err := compiled.SourceURL(); err != nil || url != "wasm://wasm/604c99b2" {
		t.Fatalf("SourceURL = %q, %v", url, err)
	}
	compiledRepeat, err := module.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	defer compiledRepeat.Close()
	repeatWire, repeatWireErr := compiledRepeat.WireBytes()
	repeatURL, repeatURLErr := compiledRepeat.SourceURL()
	if repeatWireErr != nil || !bytes.Equal(repeatWire, wire) || repeatURLErr != nil || repeatURL != "wasm://wasm/604c99b2" {
		t.Fatalf("repeated compiled representation = %v, %v, %q, %v", repeatWire, repeatWireErr, repeatURL, repeatURLErr)
	}
	recreated, err := ctx.WasmModuleFromCompiled(scope, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := module.StrictEquals(recreated.Value); err != nil || same {
		t.Fatalf("recreated identity = %v, %v", same, err)
	}
	if ok, err := recreated.IsWasmModuleObject(); err != nil || !ok {
		t.Fatalf("recreated predicate = %v, %v", ok, err)
	}
	recreatedAgain, err := ctx.WasmModuleFromCompiled(scope, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := recreated.StrictEquals(recreatedAgain.Value); err != nil || same {
		t.Fatalf("two recreated identities = %v, %v", same, err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(scope, ctx, "__gov8WasmModule", recreated.Value); err != nil || !ok {
		t.Fatalf("seed wasm module = %v, %v", ok, err)
	}
	script, err := ctx.Compile(scope, "new WebAssembly.Instance(__gov8WasmModule).exports.run()", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	runResult, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if answer, ok, err := runResult.IntegerValue(ctx); err != nil || !ok || answer != 42 {
		t.Fatalf("wasm run result = %d, %v, %v", answer, ok, err)
	}
}

func TestWasmCompileFailureAndMemoryBuffer(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	if _, err := ctx.CompileWasmModule(scope, []byte{0, 1, 2, 3, 4, 5, 6, 7}, tc); err == nil || !gov8.IsException(err) {
		t.Fatalf("malformed compile error = %v", err)
	}
	if caught, err := tc.HasCaught(); err != nil || !caught {
		t.Fatalf("TryCatch caught = %v, %v", caught, err)
	}
	if text, err := tc.ExceptionText(scope, ctx); err != nil || !strings.Contains(text, "CompileError") {
		t.Fatalf("compile exception = %q, %v", text, err)
	}
	tc.Reset()
	for _, malformed := range [][]byte{nil, append(append([]byte{}, emptyWasmModule...), 0xff)} {
		if _, err := ctx.CompileWasmModule(scope, malformed, tc); err == nil || !gov8.IsException(err) {
			t.Fatalf("malformed %v error = %v", malformed, err)
		}
		if caught, err := tc.HasCaught(); err != nil || !caught {
			t.Fatalf("malformed TryCatch = %v, %v", caught, err)
		}
		if text, err := tc.ExceptionText(scope, ctx); err != nil || !strings.Contains(text, "CompileError") {
			t.Fatalf("malformed exception = %q, %v", text, err)
		}
		tc.Reset()
	}

	script, err := ctx.Compile(scope, "globalThis.__gov8WasmMemory = new WebAssembly.Memory({initial: 1, maximum: 2}); __gov8WasmMemory", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	value, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := gov8.AsWasmMemoryObject(value)
	if err != nil {
		t.Fatal(err)
	}
	buffer, err := memory.Buffer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if length, err := buffer.ByteLength(); err != nil || length != 65536 {
		t.Fatalf("memory buffer length = %d, %v", length, err)
	}
	bufferAgain, err := memory.Buffer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := buffer.StrictEquals(bufferAgain.Value); err != nil || !same {
		t.Fatalf("buffer identity before growth = %v, %v", same, err)
	}
	grow, err := ctx.Compile(scope, "__gov8WasmMemory.grow(1)", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer grow.Close()
	growth, err := grow.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pages, ok, err := growth.IntegerValue(ctx); err != nil || !ok || pages != 1 {
		t.Fatalf("memory grow result = %d, %v, %v", pages, ok, err)
	}
	newBuffer, err := memory.Buffer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := buffer.StrictEquals(newBuffer.Value); err != nil || same {
		t.Fatalf("buffer identity after growth = %v, %v", same, err)
	}
	if length, err := newBuffer.ByteLength(); err != nil || length != 131072 {
		t.Fatalf("grown buffer length = %d, %v", length, err)
	}
	if length, err := buffer.ByteLength(); err != nil || length != 0 {
		t.Fatalf("old buffer length = %d, %v", length, err)
	}
	if detached, err := buffer.WasDetached(); err != nil || !detached {
		t.Fatalf("old buffer detached = %v, %v", detached, err)
	}
	newBufferAgain, err := memory.Buffer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := newBuffer.StrictEquals(newBufferAgain.Value); err != nil || !same {
		t.Fatalf("buffer identity after growth = %v, %v", same, err)
	}
	if _, err := gov8.AsWasmModuleObject(value); err == nil {
		t.Fatal("memory converted to module")
	}
}

func TestCompiledWasmModuleSurvivesProducerIsolateAndIsConcurrent(t *testing.T) {
	producer, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	producerContext, err := producer.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	producerScope, err := producer.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	module, err := producerContext.CompileWasmModule(producerScope, emptyWasmModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	if err := producerScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := producerContext.Close(); err != nil {
		t.Fatal(err)
	}
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wire, err := compiled.WireBytes()
			if err != nil {
				errors <- err
				return
			}
			if !bytes.Equal(wire, emptyWasmModule) {
				errors <- &wireMismatchError{}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}

	consumer, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	consumerContext, err := consumer.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	consumerScope, err := consumer.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	recreated, err := consumerContext.WasmModuleFromCompiled(consumerScope, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := recreated.IsWasmModuleObject(); err != nil || !ok {
		t.Fatalf("consumer module = %v, %v", ok, err)
	}
	if err := compiled.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.WireBytes(); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("closed handle error = %v", err)
	}
	if err := consumerScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := consumerContext.Close(); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
}

type wireMismatchError struct{}

func (*wireMismatchError) Error() string { return "compiled wasm wire bytes changed" }

func TestWasmLocalAffinity(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	module, err := ctx.CompileWasmModule(scope, emptyWasmModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	errors := make(chan error, 1)
	go func() { _, err := module.IsWasmModuleObject(); errors <- err }()
	if err := <-errors; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread local error = %v", err)
	}
}

func TestWasmForeignContextAndScope(t *testing.T) {
	_, ctxA, scopeA := newTestRuntime(t)
	_, ctxB, scopeB := newTestRuntime(t)
	module, err := ctxA.CompileWasmModule(scopeA, emptyWasmModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.CompileWasmModule(scopeB, emptyWasmModule, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope error = %v", err)
	}
	script, err := ctxA.Compile(scopeA, "new WebAssembly.Memory({initial: 1})", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	value, err := script.Run(scopeA, nil)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := gov8.AsWasmMemoryObject(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Buffer(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := memory.Buffer(ctxB); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign context error = %v", err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Close()
	if _, err := ctxB.WasmModuleFromCompiled(scopeA, compiled); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope recreation error = %v", err)
	}
	wrongModule := &gov8.WasmModuleObject{Value: value}
	if _, err := wrongModule.CompiledModule(); err == nil || !strings.Contains(err.Error(), "not a WasmModuleObject") {
		t.Fatalf("forged module wrapper error = %v", err)
	}
	wrongMemory := &gov8.WasmMemoryObject{Value: module.Value}
	if _, err := wrongMemory.Buffer(ctxA); err == nil || !strings.Contains(err.Error(), "not a WasmMemoryObject") {
		t.Fatalf("forged memory wrapper error = %v", err)
	}
}

func TestWasmLocalLifecycle(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	module, err := ctx.CompileWasmModule(scope, emptyWasmModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := module.CompiledModule(); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("module after scope close error = %v", err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
