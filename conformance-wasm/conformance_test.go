//go:build windows && amd64

package wasmconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gov8 "gov8"
)

var emptyModule = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
var answerModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
}

type fixtureLine struct {
	Check string         `json:"check"`
	OK    bool           `json:"ok"`
	Value map[string]any `json:"value"`
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixtures(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-wasm-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open checked-in Rust wasm fixture %s: %v", path, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	}()
	result := map[string]fixtureLine{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("decode Rust fixture line: %v", err)
		}
		if line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, fs map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fs[id]
	if !ok || !want.OK {
		t.Fatalf("missing or failed Rust fixture check %s", id)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("encode Go result for %s: %v", id, err)
	}
	wantJSON, err := json.Marshal(want.Value)
	if err != nil {
		t.Fatalf("encode Rust result for %s: %v", id, err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

func bytesJSON(values []byte) []int {
	result := make([]int, len(values))
	for i, value := range values {
		result[i] = int(value)
	}
	return result
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t *testing.T) *runtime {
	t.Helper()
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
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtime) close(t *testing.T) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Errorf("close scope: %v", err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Errorf("close context: %v", err)
	}
	if err := r.iso.Close(); err != nil {
		t.Errorf("close isolate: %v", err)
	}
}

type apiResult[T any] struct {
	value T
	err   error
}

func resultOf[T any](value T, err error) apiResult[T] {
	return apiResult[T]{value: value, err: err}
}

func (r apiResult[T]) must(t *testing.T, operation string) T {
	t.Helper()
	if r.err != nil {
		t.Fatalf("%s: %v", operation, r.err)
	}
	return r.value
}

func checkClose(t *testing.T, operation string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: %v", operation, err)
	}
}

func compileFailure(t *testing.T, r *runtime, wire []byte) map[string]any {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { checkClose(t, "close TryCatch", tc.Close()) }()
	module, compileErr := r.ctx.CompileWasmModule(r.scope, wire, tc)
	if compileErr == nil || module != nil {
		t.Fatalf("invalid wasm compile unexpectedly returned module=%v, err=%v", module != nil, compileErr)
	}
	caught, caughtErr := tc.HasCaught()
	if caughtErr != nil {
		t.Fatal(caughtErr)
	}
	text, textErr := tc.ExceptionText(r.scope, r.ctx)
	if textErr != nil {
		t.Fatal(textErr)
	}
	exception, hasException, exceptionErr := tc.Exception(r.scope)
	if exceptionErr != nil {
		t.Fatal(exceptionErr)
	}
	native := false
	if hasException {
		native, err = exception.IsNativeError()
		if err != nil {
			t.Fatal(err)
		}
	}
	return map[string]any{
		"module_none": module == nil && compileErr != nil,
		"caught":      caught, "exception": text, "is_native_error": native,
	}
}

func runInteger(t *testing.T, r *runtime, source string) int64 {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { checkClose(t, "close script", script.Close()) }()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	integer, ok, err := value.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("IntegerValue = %d, %v, %v", integer, ok, err)
	}
	return integer
}

func producerLifetime(t *testing.T) map[string]any {
	t.Helper()
	producer := newRuntime(t)
	module, err := producer.ctx.CompileWasmModule(producer.scope, answerModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	producer.close(t)
	defer func() { checkClose(t, "close compiled module", compiled.Close()) }()
	wire, wireErr := compiled.WireBytes()
	url, urlErr := compiled.SourceURL()
	if wireErr != nil || urlErr != nil {
		t.Fatalf("compiled after producer close: %v, %v", wireErr, urlErr)
	}
	consumer := newRuntime(t)
	defer consumer.close(t)
	restored, restoreErr := consumer.ctx.WasmModuleFromCompiled(consumer.scope, compiled)
	if restoreErr != nil || restored == nil {
		t.Fatalf("restore compiled module = %v, %v", restored, restoreErr)
	}
	global, err := consumer.ctx.GlobalObject(consumer.scope)
	if err != nil {
		t.Fatal(err)
	}
	set, err := global.SetByName(consumer.scope, consumer.ctx, "module", restored.Value)
	if err != nil || !set {
		t.Fatalf("set restored module = %v, %v", set, err)
	}
	works := runInteger(t, consumer, "new WebAssembly.Instance(module).exports.run()") == 42
	return map[string]any{
		"wire_bytes_equal": bytes.Equal(wire, answerModule), "source_url": url,
		"from_compiled_some": true, "executes": works,
	}
}

func TestRustOracleFixture(t *testing.T) {
	fs := fixtures(t)
	t.Run("sync_compile_and_compiled_module", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		empty, err := r.ctx.CompileWasmModule(r.scope, emptyModule, nil)
		if err != nil {
			t.Fatal(err)
		}
		module, err := r.ctx.CompileWasmModule(r.scope, answerModule, nil)
		if err != nil {
			t.Fatal(err)
		}
		compiledA, err := module.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { checkClose(t, "close compiled A", compiledA.Close()) }()
		compiledB, err := module.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { checkClose(t, "close compiled B", compiledB.Close()) }()
		emptyCompiled, err := empty.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { checkClose(t, "close empty compiled module", emptyCompiled.Close()) }()
		restoredA, err := r.ctx.WasmModuleFromCompiled(r.scope, compiledA)
		if err != nil {
			t.Fatal(err)
		}
		restoredB, err := r.ctx.WasmModuleFromCompiled(r.scope, compiledA)
		if err != nil {
			t.Fatal(err)
		}
		global, err := r.ctx.GlobalObject(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		if set, err := global.SetByName(r.scope, r.ctx, "module", module.Value); err != nil || !set {
			t.Fatalf("set module = %v, %v", set, err)
		}
		isWasm := resultOf(module.IsWasmModuleObject()).must(t, "module wasm predicate")
		isObject := resultOf(module.IsObject()).must(t, "module object predicate")
		emptyIsWasm := resultOf(empty.IsWasmModuleObject()).must(t, "empty module wasm predicate")
		wireA := resultOf(compiledA.WireBytes()).must(t, "compiled A wire bytes")
		wireB := resultOf(compiledB.WireBytes()).must(t, "compiled B wire bytes")
		urlA := resultOf(compiledA.SourceURL()).must(t, "compiled A source URL")
		urlB := resultOf(compiledB.SourceURL()).must(t, "compiled B source URL")
		emptyWire := resultOf(emptyCompiled.WireBytes()).must(t, "empty compiled wire bytes")
		restoredDistinct := resultOf(restoredA.StrictEquals(module.Value)).must(t, "restored A identity")
		restoredMutuallyDistinct := resultOf(restoredB.StrictEquals(restoredA.Value)).must(t, "restored B identity")
		restoredCompiled, err := restoredA.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { checkClose(t, "close restored compiled module", restoredCompiled.Close()) }()
		restoredWire := resultOf(restoredCompiled.WireBytes()).must(t, "restored compiled wire bytes")
		trailing := append(append([]byte{}, emptyModule...), 0xff)
		compare(t, fs, "wasm/sync_compile_and_compiled_module", map[string]any{
			"empty_module_is_wasm": emptyIsWasm,
			"module_predicates":    map[string]any{"is_wasm_module": isWasm, "is_object": isObject},
			"executes_to":          runInteger(t, r, "new WebAssembly.Instance(module).exports.run()"),
			"compiled": map[string]any{
				"wire_bytes": bytesJSON(wireA), "wire_bytes_repeat_equal": bytes.Equal(wireA, wireB),
				"source_url": urlA, "source_url_repeat": urlB, "empty_wire_bytes": bytesJSON(emptyWire),
			},
			"restored": map[string]any{
				"a_distinct_from_original": !restoredDistinct, "b_distinct_from_a": !restoredMutuallyDistinct,
				"wire_bytes_equal": bytes.Equal(restoredWire, answerModule),
			},
			"invalid_empty":             compileFailure(t, r, nil),
			"invalid_magic":             compileFailure(t, r, []byte{0, 1, 2, 3, 4, 5, 6, 7}),
			"invalid_trailing":          compileFailure(t, r, trailing),
			"producer_isolate_lifetime": producerLifetime(t),
		})
	})

	t.Run("memory_buffer", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		script, err := r.ctx.Compile(r.scope, "globalThis.memory = new WebAssembly.Memory({initial:1, maximum:2}); memory", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { checkClose(t, "close memory script", script.Close()) }()
		value, err := script.Run(r.scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		memory, err := gov8.AsWasmMemoryObject(value)
		if err != nil {
			t.Fatal(err)
		}
		beforeA := resultOf(memory.Buffer(r.ctx)).must(t, "first memory buffer")
		beforeB := resultOf(memory.Buffer(r.ctx)).must(t, "repeated memory buffer")
		beforeBytes := resultOf(beforeA.ByteLength()).must(t, "initial memory byte length")
		beforeDetached := resultOf(beforeA.WasDetached()).must(t, "initial memory detached state")
		beforeSame := resultOf(beforeA.StrictEquals(beforeB.Value)).must(t, "initial buffer identity")
		growResult := runInteger(t, r, "memory.grow(1)")
		afterA := resultOf(memory.Buffer(r.ctx)).must(t, "grown memory buffer")
		afterB := resultOf(memory.Buffer(r.ctx)).must(t, "repeated grown memory buffer")
		afterBytes := resultOf(afterA.ByteLength()).must(t, "grown memory byte length")
		afterSame := resultOf(afterA.StrictEquals(afterB.Value)).must(t, "grown buffer identity")
		bufferSame := resultOf(afterA.StrictEquals(beforeA.Value)).must(t, "old/new buffer identity")
		oldBytes := resultOf(beforeA.ByteLength()).must(t, "old buffer byte length")
		oldDetached := resultOf(beforeA.WasDetached()).must(t, "old buffer detached state")
		isMemory := resultOf(value.IsWasmMemoryObject()).must(t, "memory predicate")
		isObject := resultOf(value.IsObject()).must(t, "memory object predicate")
		compare(t, fs, "wasm/memory_buffer", map[string]any{
			"is_wasm_memory": isMemory, "is_object": isObject,
			"before_bytes": beforeBytes, "before_detached": beforeDetached,
			"before_repeat_same": beforeSame, "grow_return": growResult,
			"after_bytes": afterBytes, "after_repeat_same": afterSame,
			"buffer_replaced": !bufferSame, "old_bytes_after_grow": oldBytes,
			"old_detached": oldDetached,
		})
	})
}
