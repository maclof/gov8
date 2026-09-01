//go:build windows && amd64

package wasmstreamingconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
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
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-wasm-streaming-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open Rust fixture: %v", err)
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
			t.Fatalf("decode fixture: %v", err)
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

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok || !want.OK {
		t.Fatalf("missing or failed fixture %s", id)
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

func bytesJSON(values []byte) []int {
	result := make([]int, len(values))
	for i, value := range values {
		result[i] = int(value)
	}
	return result
}

type result[T any] struct {
	value T
	err   error
}

func resultOf[T any](value T, err error) result[T] { return result[T]{value, err} }
func (r result[T]) must(t *testing.T, operation string) T {
	t.Helper()
	if r.err != nil {
		t.Fatalf("%s: %v", operation, r.err)
	}
	return r.value
}

type runtime struct {
	iso       *gov8.Isolate
	ctx       *gov8.Context
	scope     *gov8.Scope
	streaming bool
}

func newRuntime(t *testing.T, callback gov8.WasmStreamingCallback) *runtime {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if callback != nil {
		if err := iso.SetWasmStreamingCallback(callback); err != nil {
			t.Fatal(err)
		}
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	return &runtime{iso: iso, ctx: ctx, scope: scope, streaming: callback != nil}
}

func (r *runtime) close(t *testing.T) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Errorf("close scope: %v", err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Errorf("close context: %v", err)
	}
	if r.streaming {
		if err := r.iso.ClearWasmStreamingCallback(); err != nil {
			t.Errorf("clear wasm streaming callback: %v", err)
		}
	}
	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		t.Errorf("release host state: %v", err)
	}
	if err := r.iso.Close(); err != nil {
		t.Errorf("close isolate: %v", err)
	}
}

func checkClose(t *testing.T, operation string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: %v", operation, err)
	}
}

func run(t *testing.T, r *runtime, source string) gov8.Value {
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
	return value
}

func compileStreaming(t *testing.T, r *runtime, source string) gov8.Promise {
	t.Helper()
	value := run(t, r, "WebAssembly.compileStreaming("+source+")")
	if !resultOf(value.IsPromise()).must(t, "promise predicate") {
		t.Fatal("compileStreaming did not return Promise")
	}
	return gov8.Promise{Value: value}
}

func pending(t *testing.T, r *runtime) bool {
	return resultOf(r.iso.HasPendingBackgroundTasks()).must(t, "pending background tasks")
}

func pump(t *testing.T, r *runtime) {
	t.Helper()
	for {
		ran := resultOf(r.iso.PumpMessageLoop(false)).must(t, "pump message loop")
		if !ran {
			break
		}
	}
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatal(err)
	}
}

func pumpUntil(t *testing.T, r *runtime, done func() bool) {
	t.Helper()
	for range 1000 {
		pump(t, r)
		if done() {
			return
		}
	}
	t.Fatal("async wasm callback did not run")
}

type streamCapture struct {
	streams  chan *gov8.WasmStreaming
	text     string
	isString bool
	isObject bool
}

func newStreamRuntime(t *testing.T) (*runtime, *streamCapture) {
	t.Helper()
	capture := &streamCapture{streams: make(chan *gov8.WasmStreaming, 1)}
	r := newRuntime(t, func(cs *gov8.CallbackScope, source gov8.Value, stream *gov8.WasmStreaming) {
		capture.text = resultOf(cs.ToString(source)).must(t, "source text")
		capture.isString = resultOf(source.IsString()).must(t, "source string predicate")
		capture.isObject = resultOf(source.IsObject()).must(t, "source object predicate")
		capture.streams <- stream
	})
	return r, capture
}

func compiledObservation(t *testing.T, module *gov8.WasmModuleObject) ([]byte, string) {
	t.Helper()
	compiled := resultOf(module.CompiledModule()).must(t, "compiled module")
	defer func() { checkClose(t, "close compiled module", compiled.Close()) }()
	return resultOf(compiled.WireBytes()).must(t, "wire bytes"),
		resultOf(compiled.SourceURL()).must(t, "source URL")
}

type compilationObservation struct {
	calls atomic.Int32
	ok    bool
	wire  []byte
	url   string
	error *gov8.Global
}

func finishCompilation(t *testing.T, r *runtime, compilation *gov8.WasmModuleCompilation,
	cache gov8.ModuleCachingCallback) *compilationObservation {
	t.Helper()
	observation := &compilationObservation{}
	err := compilation.Finish(r.scope, r.ctx, cache, func(resolved *gov8.WasmModuleCompilationResult) {
		observation.calls.Add(1)
		if resolved.Module != nil {
			observation.ok = true
			observation.wire, observation.url = compiledObservation(t, resolved.Module)
			return
		}
		observation.error = resultOf(gov8.NewGlobal(resolved.CallbackScope.Scope(), resolved.Error)).must(t, "persist compilation error")
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func materializeError(t *testing.T, r *runtime, observation *compilationObservation) string {
	t.Helper()
	if observation.error == nil {
		return ""
	}
	defer func() { checkClose(t, "close error Global", observation.error.Close()) }()
	local := resultOf(observation.error.ToLocal(r.scope)).must(t, "local compilation error")
	return resultOf(local.ToString(r.ctx)).must(t, "compilation error text")
}

func compilationJSON(t *testing.T, r *runtime, observation *compilationObservation) map[string]any {
	t.Helper()
	return map[string]any{
		"callback_calls": observation.calls.Load(), "ok": observation.ok,
		"wire_bytes": bytesJSON(observation.wire), "source_url": observation.url,
		"error": materializeError(t, r, observation),
	}
}

func TestRustOracleFixture(t *testing.T) {
	fs := fixtures(t)

	t.Run("streaming_finish_and_url", func(t *testing.T) {
		r, capture := newStreamRuntime(t)
		defer r.close(t)
		promise := compileStreaming(t, r, "'https://input.example/module.wasm'")
		before := resultOf(promise.State()).must(t, "promise state")
		pendingBefore := pending(t, r)
		stream := <-capture.streams
		for _, chunk := range [][]byte{nil, answerModule[:9], answerModule[9:]} {
			if err := stream.OnBytesReceived(chunk); err != nil {
				t.Fatal(err)
			}
		}
		if err := stream.SetURL("https://compiled.example/chunked.wasm"); err != nil {
			t.Fatal(err)
		}
		beforeFinish := resultOf(promise.State()).must(t, "state before finish")
		if err := stream.Finish(nil); err != nil {
			t.Fatal(err)
		}
		afterFinish := resultOf(promise.State()).must(t, "state after finish")
		pendingAfter := pending(t, r)
		pump(t, r)
		afterPump := resultOf(promise.State()).must(t, "state after pump")
		value := resultOf(promise.Result(r.scope)).must(t, "promise result")
		module := resultOf(gov8.AsWasmModuleObject(value)).must(t, "wasm module result")
		wire, url := compiledObservation(t, module)
		global := resultOf(r.ctx.GlobalObject(r.scope)).must(t, "global object")
		if set, err := global.SetByName(r.scope, r.ctx, "module", module.Value); err != nil || !set {
			t.Fatalf("set module = %v, %v", set, err)
		}
		answer, ok, err := run(t, r, "new WebAssembly.Instance(module).exports.run()").IntegerValue(r.ctx)
		if err != nil || !ok {
			t.Fatalf("execute module = %d, %v, %v", answer, ok, err)
		}
		compare(t, fs, "wasm/streaming_finish_and_url", map[string]any{
			"source":         map[string]any{"text": capture.text, "is_string": capture.isString, "is_object": capture.isObject},
			"promise_before": before.String(), "pending_before": pendingBefore,
			"promise_before_finish": beforeFinish.String(), "promise_after_finish": afterFinish.String(),
			"pending_after_finish": pendingAfter, "promise_after_pump": afterPump.String(),
			"result_is_wasm_module": resultOf(value.IsWasmModuleObject()).must(t, "result predicate"),
			"wire_bytes":            bytesJSON(wire), "source_url": url, "executes_to": answer,
		})
	})

	t.Run("streaming_abort_and_drop", func(t *testing.T) {
		r, capture := newStreamRuntime(t)
		defer r.close(t)
		withValue := compileStreaming(t, r, "'abort-with-value'")
		stream := <-capture.streams
		exception := resultOf(r.scope.NewObject(r.ctx)).must(t, "abort exception")
		exceptionValue := exception.Value
		if err := stream.Abort(&exceptionValue); err != nil {
			t.Fatal(err)
		}
		pump(t, r)
		withValueResult := resultOf(withValue.Result(r.scope)).must(t, "abort result")
		withValueSame := resultOf(withValueResult.StrictEquals(exceptionValue)).must(t, "abort exception identity")
		withValueState := resultOf(withValue.State()).must(t, "abort state")
		pendingAfterValue := pending(t, r)

		withoutValue := compileStreaming(t, r, "'abort-without-value'")
		if err := (<-capture.streams).Abort(nil); err != nil {
			t.Fatal(err)
		}
		pump(t, r)
		withoutValueState := resultOf(withoutValue.State()).must(t, "nil abort state")
		pendingAfterNone := pending(t, r)

		dropped := compileStreaming(t, r, "'drop-stream'")
		if err := (<-capture.streams).Close(); err != nil {
			t.Fatal(err)
		}
		pump(t, r)
		dropState := resultOf(dropped.State()).must(t, "drop state")
		pendingAfterDrop := pending(t, r)
		compare(t, fs, "wasm/streaming_abort_and_drop", map[string]any{
			"abort_with_exception":    map[string]any{"state": withValueState.String(), "same_exception": withValueSame, "pending_tasks": pendingAfterValue},
			"abort_without_exception": map[string]any{"state": withoutValueState.String(), "pending_tasks": pendingAfterNone},
			"drop_without_finish":     map[string]any{"state": dropState.String(), "pending_tasks": pendingAfterDrop},
		})
	})

	t.Run("streaming_cache_rejection", func(t *testing.T) {
		r, capture := newStreamRuntime(t)
		defer r.close(t)
		promise := compileStreaming(t, r, "'cached-module'")
		stream := <-capture.streams
		if err := stream.SetHasCompiledModuleBytes(); err != nil {
			t.Fatal(err)
		}
		if err := stream.OnBytesReceived(emptyModule); err != nil {
			t.Fatal(err)
		}
		var cacheWire []byte
		cacheAccepted := false
		if err := stream.Finish(func(cache *gov8.ModuleCachingInterface) {
			cacheWire = resultOf(cache.WireBytes()).must(t, "cache wire")
			cacheAccepted = resultOf(cache.SetCachedCompiledModuleBytes(nil)).must(t, "empty cache candidate")
		}); err != nil {
			t.Fatal(err)
		}
		afterFinish := resultOf(promise.State()).must(t, "cache state after finish")
		pump(t, r)
		afterPump := resultOf(promise.State()).must(t, "cache state after pump")
		value := resultOf(promise.Result(r.scope)).must(t, "cache result")
		compare(t, fs, "wasm/streaming_cache_rejection", map[string]any{
			"wire_bytes": bytesJSON(cacheWire), "empty_cache_accepted": cacheAccepted,
			"state_after_finish": afterFinish.String(), "state_after_pump": afterPump.String(),
			"result_text":     resultOf(value.ToString(r.ctx)).must(t, "cache result text"),
			"result_is_error": resultOf(value.IsNativeError()).must(t, "cache result error predicate"),
			"pending_tasks":   pending(t, r),
		})
	})

	t.Run("module_compilation_success_failure", func(t *testing.T) {
		r := newRuntime(t, nil)
		defer r.close(t)
		success := resultOf(gov8.NewWasmModuleCompilation()).must(t, "new compilation")
		if err := success.OnBytesReceived(answerModule[:3]); err != nil {
			t.Fatal(err)
		}
		if err := success.OnBytesReceived(answerModule[3:]); err != nil {
			t.Fatal(err)
		}
		if err := success.SetURL("https://async.example/answer.wasm"); err != nil {
			t.Fatal(err)
		}
		successResult := finishCompilation(t, r, success, nil)
		pumpUntil(t, r, func() bool { return successResult.calls.Load() != 0 })

		failure := resultOf(gov8.NewWasmModuleCompilation()).must(t, "new failed compilation")
		if err := failure.OnBytesReceived([]byte{0, 1, 2}); err != nil {
			t.Fatal(err)
		}
		if err := failure.OnBytesReceived([]byte{3, 4, 5, 6, 7}); err != nil {
			t.Fatal(err)
		}
		if err := failure.SetURL("https://async.example/bad.wasm"); err != nil {
			t.Fatal(err)
		}
		failureResult := finishCompilation(t, r, failure, nil)
		pumpUntil(t, r, func() bool { return failureResult.calls.Load() != 0 })
		compare(t, fs, "wasm/module_compilation_success_failure", map[string]any{
			"success": compilationJSON(t, r, successResult), "failure": compilationJSON(t, r, failureResult),
			"pending_tasks": pending(t, r),
		})
	})

	t.Run("module_compilation_lifecycle", func(t *testing.T) {
		r := newRuntime(t, nil)
		defer r.close(t)
		movedCh := make(chan *gov8.WasmModuleCompilation, 1)
		go func() {
			compilation, err := gov8.NewWasmModuleCompilation()
			if err == nil {
				err = compilation.OnBytesReceived(answerModule[:11])
			}
			if err == nil {
				err = compilation.OnBytesReceived(answerModule[11:])
			}
			if err != nil {
				movedCh <- nil
				return
			}
			movedCh <- compilation
		}()
		moved := <-movedCh
		if moved == nil {
			t.Fatal("cross-thread construction failed")
		}
		if err := moved.OnBytesReceived(nil); err != nil {
			t.Fatal(err)
		}
		if err := moved.SetURL("https://async.example/cross-thread.wasm"); err != nil {
			t.Fatal(err)
		}
		cross := finishCompilation(t, r, moved, nil)
		pumpUntil(t, r, func() bool { return cross.calls.Load() != 0 })

		cached := resultOf(gov8.NewWasmModuleCompilation()).must(t, "new cached compilation")
		if err := cached.SetHasCompiledModuleBytes(); err != nil {
			t.Fatal(err)
		}
		if err := cached.OnBytesReceived(emptyModule); err != nil {
			t.Fatal(err)
		}
		var cacheWire []byte
		cacheAccepted := false
		cachedResult := finishCompilation(t, r, cached, func(cache *gov8.ModuleCachingInterface) {
			cacheWire = resultOf(cache.WireBytes()).must(t, "async cache wire")
			cacheAccepted = resultOf(cache.SetCachedCompiledModuleBytes(nil)).must(t, "async cache candidate")
		})
		pumpUntil(t, r, func() bool { return cachedResult.calls.Load() != 0 })

		var serializationCalls atomic.Int32
		serialization := resultOf(gov8.NewWasmModuleCompilation()).must(t, "new serialization compilation")
		if err := serialization.SetMoreFunctionsCanBeSerializedCallback(func(module *gov8.CompiledWasmModule) {
			serializationCalls.Add(1)
			if err := module.Close(); err != nil {
				panic(err)
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := serialization.OnBytesReceived(answerModule); err != nil {
			t.Fatal(err)
		}
		if err := serialization.SetURL("https://async.example/cross-thread.wasm"); err != nil {
			t.Fatal(err)
		}
		serialized := finishCompilation(t, r, serialization, nil)
		pumpUntil(t, r, func() bool { return serialized.calls.Load() != 0 })

		abortResult := make(chan bool, 1)
		go func() {
			compilation, err := gov8.NewWasmModuleCompilation()
			if err == nil {
				err = compilation.OnBytesReceived(emptyModule)
			}
			if err == nil {
				err = compilation.Abort()
			}
			abortResult <- err == nil
		}()
		abortCompleted := <-abortResult
		unfinished := resultOf(gov8.NewWasmModuleCompilation()).must(t, "new unfinished compilation")
		dropCompleted := unfinished.Close() == nil

		compare(t, fs, "wasm/module_compilation_lifecycle", map[string]any{
			"cross_thread": compilationJSON(t, r, cross),
			"cache_rejection": map[string]any{
				"wire_bytes": bytesJSON(cacheWire), "empty_cache_accepted": cacheAccepted,
				"resolution": compilationJSON(t, r, cachedResult),
			},
			"serialization": map[string]any{
				"resolution": compilationJSON(t, r, serialized), "callback_calls": serializationCalls.Load(),
			},
			"abort_on_worker_completed": abortCompleted, "drop_unfinished_completed": dropCompleted,
		})
	})
}
