//go:build windows && amd64

package serializerwasmlegacyconformance

import (
	"bufio"
	"bytes"
	"encoding/hex"
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
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-serializer-wasm-legacy-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open checked-in Rust fixture %s: %v", path, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	}()
	result := make(map[string]fixtureLine)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		if line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) != 4 {
		t.Fatalf("fixture check count = %d, want 4", len(result))
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok || !want.OK {
		t.Fatalf("missing or failed Rust fixture check %s", id)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
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

type writer struct {
	id      uint32
	lengths []int
}

func (*writer) ThrowDataCloneError(string) bool { return true }

func (w *writer) GetWasmModuleTransferID(module gov8.Value) (uint32, bool) {
	m, err := gov8.AsWasmModuleObject(module)
	if err != nil {
		panic(err)
	}
	compiled, err := m.CompiledModule()
	if err != nil {
		panic(err)
	}
	wire, err := compiled.WireBytes()
	if err != nil {
		panic(err)
	}
	if err := compiled.Close(); err != nil {
		panic(err)
	}
	w.lengths = append(w.lengths, len(wire))
	return w.id, true
}

type compiledReader struct {
	expected uint32
	modules  []*gov8.CompiledWasmModule
	ids      []uint32
	returned []gov8.Value
}

func (r *compiledReader) ResolveWasmModuleFromID(d *gov8.DelegateValueDeserializer, id uint32) (*gov8.WasmModuleObject, bool) {
	r.ids = append(r.ids, id)
	if id != r.expected || len(r.modules) == 0 {
		return nil, false
	}
	index := len(r.ids) - 1
	if index >= len(r.modules) {
		index = len(r.modules) - 1
	}
	module, err := d.Context().WasmModuleFromCompiled(d.Scope(), r.modules[index])
	if err != nil {
		panic(err)
	}
	r.returned = append(r.returned, module.Value)
	return module, true
}

type noneReader struct{ ids []uint32 }

func (r *noneReader) ResolveWasmModuleFromID(_ *gov8.DelegateValueDeserializer, uint32ID uint32) (*gov8.WasmModuleObject, bool) {
	r.ids = append(r.ids, uint32ID)
	return nil, false
}

func readProperty(t *testing.T, r *runtime, value gov8.Value, name string) gov8.Value {
	t.Helper()
	object, err := gov8.AsObject(value)
	if err != nil {
		t.Fatal(err)
	}
	property, found, err := object.GetByName(r.scope, r.ctx, name)
	if err != nil || !found {
		t.Fatalf("get %s: %v, %v", name, found, err)
	}
	return property
}

func isWasm(t *testing.T, value gov8.Value) bool {
	t.Helper()
	ok, err := value.IsWasmModuleObject()
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func runModule(t *testing.T, r *runtime, name string, module gov8.Value, expression string) gov8.Value {
	t.Helper()
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := global.SetByName(r.scope, r.ctx, name, module)
	if err != nil || !ok {
		t.Fatalf("set module global: %v, %v", ok, err)
	}
	script, err := r.ctx.Compile(r.scope, expression, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Errorf("close script: %v", err)
		}
	}()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func closeCompiled(t *testing.T, modules ...*gov8.CompiledWasmModule) {
	t.Helper()
	for _, module := range modules {
		if err := module.Close(); err != nil {
			t.Errorf("close compiled module: %v", err)
		}
	}
}

func TestRustOracleFixture(t *testing.T) {
	fs := fixtures(t)

	t.Run("wasm_cross_isolate_rehydration", func(t *testing.T) {
		producer := newRuntime(t)
		module, err := producer.ctx.CompileWasmModule(producer.scope, answerModule, nil)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := module.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		holder, err := producer.scope.NewObject(producer.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"a", "b"} {
			if ok, err := holder.SetByName(producer.scope, producer.ctx, name, module.Value); err != nil || !ok {
				t.Fatalf("set %s: %v, %v", name, ok, err)
			}
		}
		writer := &writer{id: 21}
		serializer, err := gov8.NewDelegateValueSerializer(producer.scope, producer.ctx, writer)
		if err != nil {
			t.Fatal(err)
		}
		if err := serializer.WriteHeader(); err != nil {
			t.Fatal(err)
		}
		if ok, err := serializer.WriteValue(producer.ctx, holder.Value, nil); err != nil || !ok {
			t.Fatalf("write: %v, %v", ok, err)
		}
		wire, err := serializer.Release()
		if err != nil {
			t.Fatal(err)
		}
		if err := serializer.Close(); err != nil {
			t.Fatal(err)
		}
		producer.close(t)
		defer closeCompiled(t, compiled)

		target := newRuntime(t)
		defer target.close(t)
		reader := &compiledReader{expected: 21, modules: []*gov8.CompiledWasmModule{compiled}}
		tc, err := target.iso.NewTryCatch()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tc.Close() }()
		deserializer, err := gov8.NewDelegateValueDeserializer(target.scope, target.ctx, wire, reader)
		if err != nil {
			t.Fatal(err)
		}
		header, err := deserializer.ReadHeader(target.ctx, tc)
		if err != nil {
			t.Fatal(err)
		}
		version, err := deserializer.GetWireFormatVersion()
		if err != nil {
			t.Fatal(err)
		}
		value, err := deserializer.ReadValue(target.ctx, tc)
		if err != nil {
			t.Fatal(err)
		}
		if err := deserializer.Close(); err != nil {
			t.Fatal(err)
		}
		a := readProperty(t, target, value, "a")
		b := readProperty(t, target, value, "b")
		sameAB, err := a.StrictEquals(b)
		if err != nil {
			t.Fatal(err)
		}
		sameReturned, err := a.StrictEquals(reader.returned[0])
		if err != nil {
			t.Fatal(err)
		}
		am, err := gov8.AsWasmModuleObject(a)
		if err != nil {
			t.Fatal(err)
		}
		ac, err := am.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		awire, err := ac.WireBytes()
		if err != nil {
			t.Fatal(err)
		}
		closeCompiled(t, ac)
		execution := runModule(t, target, "transferredModule", a, "new WebAssembly.Instance(transferredModule).exports.run()")
		executes, ok, err := execution.IntegerValue(target.ctx)
		if err != nil || !ok {
			t.Fatalf("integer: %d, %v, %v", executes, ok, err)
		}
		caught, err := tc.HasCaught()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fs, "serializer-wasm-legacy/wasm_cross_isolate_rehydration", map[string]any{
			"wire": hex.EncodeToString(wire), "writer_module_byte_lengths": writer.lengths,
			"header": header, "version": version, "caught": caught, "reader_ids": reader.ids,
			"a_is_module": isWasm(t, a), "b_is_module": isWasm(t, b),
			"repeated_identity": sameAB, "same_as_callback_return": sameReturned,
			"wire_bytes_equal": bytes.Equal(awire, answerModule), "executes_to": executes,
		})
	})

	t.Run("wasm_repeated_id_replacement", func(t *testing.T) {
		producer := newRuntime(t)
		answer, err := producer.ctx.CompileWasmModule(producer.scope, answerModule, nil)
		if err != nil {
			t.Fatal(err)
		}
		empty, err := producer.ctx.CompileWasmModule(producer.scope, emptyModule, nil)
		if err != nil {
			t.Fatal(err)
		}
		compiledA, err := answer.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		compiledB, err := empty.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		holder, err := producer.scope.NewObject(producer.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, property := range []struct {
			name  string
			value gov8.Value
		}{{"a", answer.Value}, {"b", empty.Value}} {
			if ok, err := holder.SetByName(producer.scope, producer.ctx, property.name, property.value); err != nil || !ok {
				t.Fatal(err)
			}
		}
		writer := &writer{id: 7}
		serializer, err := gov8.NewDelegateValueSerializer(producer.scope, producer.ctx, writer)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := serializer.WriteValue(producer.ctx, holder.Value, nil); err != nil || !ok {
			t.Fatalf("write: %v, %v", ok, err)
		}
		wire, err := serializer.Release()
		if err != nil {
			t.Fatal(err)
		}
		if err := serializer.Close(); err != nil {
			t.Fatal(err)
		}
		producer.close(t)
		defer closeCompiled(t, compiledA, compiledB)

		target := newRuntime(t)
		defer target.close(t)
		reader := &compiledReader{expected: 7, modules: []*gov8.CompiledWasmModule{compiledA, compiledB}}
		tc, err := target.iso.NewTryCatch()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tc.Close() }()
		deserializer, err := gov8.NewDelegateValueDeserializer(target.scope, target.ctx, wire, reader)
		if err != nil {
			t.Fatal(err)
		}
		value, err := deserializer.ReadValue(target.ctx, tc)
		if err != nil {
			t.Fatal(err)
		}
		if err := deserializer.Close(); err != nil {
			t.Fatal(err)
		}
		a := readProperty(t, target, value, "a")
		b := readProperty(t, target, value, "b")
		same, err := a.StrictEquals(b)
		if err != nil {
			t.Fatal(err)
		}
		aExecution := runModule(t, target, "moduleA", a, "new WebAssembly.Instance(moduleA).exports.run()")
		aAnswer, ok, err := aExecution.IntegerValue(target.ctx)
		if err != nil || !ok {
			t.Fatalf("answer: %d %v %v", aAnswer, ok, err)
		}
		bRun := runModule(t, target, "moduleB", b, "typeof new WebAssembly.Instance(moduleB).exports.run === 'function'")
		bHasRun, err := bRun.IsTrue()
		if err != nil {
			t.Fatal(err)
		}
		caught, err := tc.HasCaught()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fs, "serializer-wasm-legacy/wasm_repeated_id_replacement", map[string]any{
			"wire": hex.EncodeToString(wire), "writer_module_byte_lengths": writer.lengths,
			"reader_ids": reader.ids, "caught": caught, "a_is_module": isWasm(t, a),
			"b_is_module": isWasm(t, b), "same_identity": same,
			"a_executes_to": aAnswer, "b_has_run_export": bHasRun,
		})
	})

	t.Run("wasm_max_id_and_none_failure", func(t *testing.T) {
		producer := newRuntime(t)
		module, err := producer.ctx.CompileWasmModule(producer.scope, answerModule, nil)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := module.CompiledModule()
		if err != nil {
			t.Fatal(err)
		}
		writer := &writer{id: ^uint32(0)}
		serializer, err := gov8.NewDelegateValueSerializer(producer.scope, producer.ctx, writer)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := serializer.WriteValue(producer.ctx, module.Value, nil); err != nil || !ok {
			t.Fatalf("write max: %v, %v", ok, err)
		}
		wire, err := serializer.Release()
		if err != nil {
			t.Fatal(err)
		}
		if err := serializer.Close(); err != nil {
			t.Fatal(err)
		}
		producer.close(t)
		defer closeCompiled(t, compiled)

		target := newRuntime(t)
		defer target.close(t)
		successReader := &compiledReader{expected: ^uint32(0), modules: []*gov8.CompiledWasmModule{compiled}}
		successTC, err := target.iso.NewTryCatch()
		if err != nil {
			t.Fatal(err)
		}
		success, err := gov8.NewDelegateValueDeserializer(target.scope, target.ctx, wire, successReader)
		if err != nil {
			t.Fatal(err)
		}
		read, err := success.ReadValue(target.ctx, successTC)
		if err != nil {
			t.Fatal(err)
		}
		if err := success.Close(); err != nil {
			t.Fatal(err)
		}
		successCaught, err := successTC.HasCaught()
		if err != nil {
			t.Fatal(err)
		}
		_ = successTC.Close()

		failureReader := &noneReader{}
		failureTC, err := target.iso.NewTryCatch()
		if err != nil {
			t.Fatal(err)
		}
		failure, err := gov8.NewDelegateValueDeserializer(target.scope, target.ctx, []byte{0x77, 0x2a}, failureReader)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := failure.ReadValue(target.ctx, failureTC)
		if readErr == nil {
			t.Fatal("None resolver unexpectedly read a value")
		}
		failureCaught, err := failureTC.HasCaught()
		if err != nil {
			t.Fatal(err)
		}
		failureText, err := failureTC.ExceptionText(target.scope, target.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := failure.Close(); err != nil {
			t.Fatal(err)
		}
		_ = failureTC.Close()
		compare(t, fs, "serializer-wasm-legacy/wasm_max_id_and_none_failure", map[string]any{
			"max_id_success": map[string]any{
				"wire": hex.EncodeToString(wire), "writer_module_byte_lengths": writer.lengths,
				"read_is_module": isWasm(t, read), "caught": successCaught, "ids": successReader.ids,
			},
			"none_failure": map[string]any{
				"read_none": readErr != nil, "caught": failureCaught,
				"exception": failureText, "ids": failureReader.ids,
			},
		})
	})

	t.Run("legacy_wire_format_control", func(t *testing.T) {
		legacyCase := func(wire []byte, settings ...bool) map[string]any {
			r := newRuntime(t)
			defer r.close(t)
			tc, err := r.iso.NewTryCatch()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tc.Close() }()
			d, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, wire, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = d.Close() }()
			for _, enabled := range settings {
				if err := d.SetSupportsLegacyWireFormat(enabled); err != nil {
					t.Fatal(err)
				}
			}
			headerOK, headerErr := d.ReadHeader(r.ctx, tc)
			version, err := d.GetWireFormatVersion()
			if err != nil {
				t.Fatal(err)
			}
			var header any
			valueTrue := false
			if headerErr == nil {
				header = headerOK
				if headerOK {
					value, err := d.ReadValue(r.ctx, tc)
					if err != nil {
						t.Fatal(err)
					}
					valueTrue, err = value.IsTrue()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			caught, err := tc.HasCaught()
			if err != nil {
				t.Fatal(err)
			}
			var exception any
			if caught {
				text, err := tc.ExceptionText(r.scope, r.ctx)
				if err != nil {
					t.Fatal(err)
				}
				exception = text
			}
			return map[string]any{
				"header": header, "version": version, "value_is_true": valueTrue,
				"caught": caught, "exception": exception,
			}
		}
		compare(t, fs, "serializer-wasm-legacy/legacy_wire_format_control", map[string]any{
			"headerless_default":         legacyCase([]byte{0x54}),
			"headerless_false":           legacyCase([]byte{0x54}, false),
			"headerless_true":            legacyCase([]byte{0x54}, true),
			"headerless_true_then_false": legacyCase([]byte{0x54}, true, false),
			"headerless_false_then_true": legacyCase([]byte{0x54}, false, true),
			"version12_false":            legacyCase([]byte{0xff, 0x0c, 0x54}, false),
			"version12_true":             legacyCase([]byte{0xff, 0x0c, 0x54}, true),
			"version13_false":            legacyCase([]byte{0xff, 0x0d, 0x54}, false),
			"version16_true":             legacyCase([]byte{0xff, 0x10, 0x54}, true),
		})
	})
}
