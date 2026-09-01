//go:build windows && amd64

package wasmcachepositiveconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"

	gov8 "github.com/maclof/gov8"
)

var answerModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
}

var emptyModule = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

type fixtureLine struct {
	Check   string         `json:"check"`
	OK      bool           `json:"ok"`
	Value   map[string]any `json:"value"`
	Summary *struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

func TestMain(m *testing.M) {
	if err := gov8.SetFlagsFromString("--no-liftoff --no-wasm-lazy-compilation"); err != nil {
		panic(err)
	}
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) ([]string, map[string]fixtureLine) {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-wasm-cache-positive-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open Rust fixture: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close Rust fixture: %v", err)
		}
	}()
	var order []string
	values := make(map[string]fixtureLine)
	var summary *fixtureLine
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("decode Rust fixture: %v", err)
		}
		if line.Check != "" {
			if _, duplicate := values[line.Check]; duplicate {
				t.Fatalf("duplicate Rust fixture check %q", line.Check)
			}
			order = append(order, line.Check)
			values[line.Check] = line
		} else if line.Summary != nil {
			copy := line
			summary = &copy
		} else {
			t.Fatalf("unrecognized Rust fixture line: %s", scanner.Bytes())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if summary == nil || summary.Summary.Total != 4 || summary.Summary.Passed != 4 || summary.Summary.Failed != 0 || len(order) != 4 {
		t.Fatalf("invalid Rust fixture summary/order: summary=%+v order=%v", summary, order)
	}
	wantOrder := []string{
		"wasm-cache-positive/producer/determinism",
		"wasm-cache-positive/streaming/accepted_cross_isolate",
		"wasm-cache-positive/streaming/rejection_fallback",
		"wasm-cache-positive/module_compilation/accepted",
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Fatalf("fixture order[%d] = %q, want %q", i, order[i], wantOrder[i])
		}
	}
	return order, values
}

func compare(t *testing.T, values map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := values[id]
	if !ok || !want.OK {
		t.Fatalf("missing or failed Rust fixture %q", id)
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

type testRuntime struct {
	iso       *gov8.Isolate
	ctx       *gov8.Context
	scope     *gov8.Scope
	streaming bool
}

func newRuntime(t *testing.T, callback gov8.WasmStreamingCallback) *testRuntime {
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
	return &testRuntime{iso: iso, ctx: ctx, scope: scope, streaming: callback != nil}
}

func (r *testRuntime) close(t *testing.T) {
	t.Helper()
	if r.scope != nil {
		if err := r.scope.Close(); err != nil {
			t.Errorf("close scope: %v", err)
		}
	}
	if r.ctx != nil {
		if err := r.ctx.Close(); err != nil {
			t.Errorf("close context: %v", err)
		}
	}
	if r.streaming {
		if err := r.iso.ClearWasmStreamingCallback(); err != nil {
			t.Errorf("clear streaming callback: %v", err)
		}
	}
	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		t.Errorf("release isolate host state: %v", err)
	}
	if err := r.iso.Close(); err != nil {
		t.Errorf("close isolate: %v", err)
	}
}

func eval(scope *gov8.Scope, ctx *gov8.Context, source string) (gov8.Value, error) {
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		return gov8.Value{}, err
	}
	defer script.Close()
	return script.Run(scope, nil)
}

func mustEval(t *testing.T, r *testRuntime, source string) gov8.Value {
	t.Helper()
	value, err := eval(r.scope, r.ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func compileStreaming(t *testing.T, r *testRuntime) gov8.Promise {
	t.Helper()
	value := mustEval(t, r, "WebAssembly.compileStreaming('https://cache-input.example/module.wasm')")
	isPromise, err := value.IsPromise()
	if err != nil || !isPromise {
		t.Fatalf("compileStreaming promise = %v, %v", isPromise, err)
	}
	return gov8.Promise{Value: value}
}

func pump(t *testing.T, r *testRuntime) {
	t.Helper()
	for range 10000 {
		_, err := r.iso.PumpMessageLoop(false)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
}

func pumpPromise(t *testing.T, r *testRuntime, promise gov8.Promise) {
	t.Helper()
	for range 10000 {
		state, err := promise.State()
		if err != nil {
			t.Fatal(err)
		}
		if state != gov8.PromisePending {
			return
		}
		if _, err := r.iso.PumpMessageLoop(false); err != nil {
			t.Fatal(err)
		}
		if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
	t.Fatal("wasm cache promise did not settle")
}

func fnv1a64(data []byte) string {
	hash := uint64(0xcbf29ce484222325)
	for _, value := range data {
		hash ^= uint64(value)
		hash *= 0x100000001b3
	}
	return fmt.Sprintf("%016x", hash)
}

func produceCache(t *testing.T) (*gov8.SerializedWasmModuleCache, bool) {
	t.Helper()
	r := newRuntime(t, nil)
	defer r.close(t)
	module, err := r.ctx.CompileWasmModule(r.scope, answerModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := compiled.Close(); err != nil {
			t.Errorf("close compiled module: %v", err)
		}
	}()
	first, err := compiled.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiled.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return first, bytes.Equal(first.SerializedBytes(), second.SerializedBytes())
}

type streamAttemptResult struct {
	callbackWireMatches     bool
	cacheAccepted           bool
	stateAfterFinish        string
	stateAfterPump          string
	resultIsModule          bool
	executesTo              any
	reserializedEqualsInput bool
	restoredObjectDistinct  bool
	restoredExecutesTo      any
	sourceURL               string
}

func (o streamAttemptResult) json() map[string]any {
	return map[string]any{
		"callback_wire_matches":     o.callbackWireMatches,
		"cache_accepted":            o.cacheAccepted,
		"state_after_finish":        o.stateAfterFinish,
		"state_after_pump":          o.stateAfterPump,
		"result_is_module":          o.resultIsModule,
		"executes_to":               o.executesTo,
		"reserialized_equals_input": o.reserializedEqualsInput,
		"restored_object_distinct":  o.restoredObjectDistinct,
		"restored_executes_to":      o.restoredExecutesTo,
		"source_url":                o.sourceURL,
	}
}

func streamingAttempt(t *testing.T, wire, input []byte, typed *gov8.SerializedWasmModuleCache, url string) streamAttemptResult {
	t.Helper()
	streams := make(chan *gov8.WasmStreaming, 1)
	var streamingErr error
	r := newRuntime(t, func(_ *gov8.CallbackScope, _ gov8.Value, stream *gov8.WasmStreaming) {
		select {
		case streams <- stream:
		default:
			streamingErr = errors.New("duplicate streaming callback")
		}
	})
	defer r.close(t)
	promise := compileStreaming(t, r)
	stream := <-streams
	if streamingErr != nil {
		t.Fatal(streamingErr)
	}
	if err := stream.SetHasCompiledModuleBytes(); err != nil {
		t.Fatal(err)
	}
	cut := min(5, len(wire))
	if err := stream.OnBytesReceived(wire[:cut]); err != nil {
		t.Fatal(err)
	}
	if err := stream.OnBytesReceived(wire[cut:]); err != nil {
		t.Fatal(err)
	}
	if err := stream.SetURL(url); err != nil {
		t.Fatal(err)
	}
	var callbackWire []byte
	var accepted bool
	var cacheErr error
	if err := stream.Finish(func(cache *gov8.ModuleCachingInterface) {
		callbackWire, cacheErr = cache.WireBytes()
		if cacheErr != nil {
			return
		}
		if typed != nil {
			accepted, cacheErr = cache.SetCachedCompiledModule(typed)
		} else {
			accepted, cacheErr = cache.SetCachedCompiledModuleBytes(input)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if cacheErr != nil {
		t.Fatalf("cache callback: %v", cacheErr)
	}
	afterFinish, err := promise.State()
	if err != nil {
		t.Fatal(err)
	}
	pumpPromise(t, r, promise)
	afterPump, err := promise.State()
	if err != nil {
		t.Fatal(err)
	}
	result, err := promise.Result(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	isModule, err := result.IsWasmModuleObject()
	if err != nil {
		t.Fatal(err)
	}
	observation := streamAttemptResult{
		callbackWireMatches: bytes.Equal(callbackWire, wire), cacheAccepted: accepted,
		stateAfterFinish: afterFinish.String(), stateAfterPump: afterPump.String(), resultIsModule: isModule,
	}
	if !isModule {
		return observation
	}
	module, err := gov8.AsWasmModuleObject(result)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Close()
	reserialized, err := compiled.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	observation.reserializedEqualsInput = bytes.Equal(reserialized.SerializedBytes(), input)
	observation.sourceURL, err = compiled.SourceURL()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := r.ctx.WasmModuleFromCompiled(r.scope, compiled)
	if err != nil {
		t.Fatal(err)
	}
	same, err := module.Value.StrictEquals(restored.Value)
	if err != nil {
		t.Fatal(err)
	}
	observation.restoredObjectDistinct = !same
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, "cachedModule", module.Value); err != nil || !ok {
		t.Fatalf("set cachedModule = %v, %v", ok, err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, "restoredModule", restored.Value); err != nil || !ok {
		t.Fatalf("set restoredModule = %v, %v", ok, err)
	}
	observation.executesTo = integerResult(t, r.scope, r.ctx, "new WebAssembly.Instance(cachedModule).exports.run()")
	observation.restoredExecutesTo = integerResult(t, r.scope, r.ctx, "new WebAssembly.Instance(restoredModule).exports.run()")
	return observation
}

func integerResult(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, source string) int64 {
	t.Helper()
	value, err := eval(scope, ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	integer, ok, err := value.IntegerValue(ctx)
	if err != nil || !ok {
		t.Fatalf("integer result = %d, %v, %v", integer, ok, err)
	}
	return integer
}

func TestRustOracleFixture(t *testing.T) {
	_, values := fixture(t)
	cache, repeated := produceCache(t)
	independent, independentRepeated := produceCache(t)
	cacheBytes := cache.SerializedBytes()
	compare(t, values, "wasm-cache-positive/producer/determinism", map[string]any{
		"serialized_size":                   cache.Len(),
		"fnv1a64":                           fnv1a64(cacheBytes),
		"repeat_same_compiled_equal":        repeated,
		"independent_producer_repeat_equal": independentRepeated,
		"independent_isolate_bytes_equal":   bytes.Equal(cacheBytes, independent.SerializedBytes()),
	})

	accepted := streamingAttempt(t, answerModule, cacheBytes, cache, "https://cache.example/accepted.wasm")
	compare(t, values, "wasm-cache-positive/streaming/accepted_cross_isolate", accepted.json())

	header := append([]byte(nil), cacheBytes...)
	header[0] ^= 0x5a
	tail := append([]byte(nil), cacheBytes...)
	tail[len(tail)-1] ^= 0x5a
	compare(t, values, "wasm-cache-positive/streaming/rejection_fallback", map[string]any{
		"header_corruption": streamingAttempt(t, answerModule, header, nil, "https://cache.example/header-corruption.wasm").json(),
		"tail_corruption":   streamingAttempt(t, answerModule, tail, nil, "https://cache.example/tail-corruption.wasm").json(),
	})

	compare(t, values, "wasm-cache-positive/module_compilation/accepted", moduleCompilationAttempt(t, cache))
}

func moduleCompilationAttempt(t *testing.T, cache *gov8.SerializedWasmModuleCache) map[string]any {
	t.Helper()
	r := newRuntime(t, nil)
	defer r.close(t)
	compilation, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := compilation.SetHasCompiledModuleBytes(); err != nil {
		t.Fatal(err)
	}
	if err := compilation.OnBytesReceived(answerModule); err != nil {
		t.Fatal(err)
	}
	if err := compilation.SetURL("https://cache.example/module-compilation.wasm"); err != nil {
		t.Fatal(err)
	}
	var callbackWire []byte
	var cacheAccepted bool
	var callbackErr error
	var resolutionCalls atomic.Int32
	resolvedModule := false
	resolvedError := false
	var executesTo int64
	var reserializedEqual bool
	var sourceURL string
	err = compilation.Finish(r.scope, r.ctx, func(iface *gov8.ModuleCachingInterface) {
		callbackWire, callbackErr = iface.WireBytes()
		if callbackErr == nil {
			cacheAccepted, callbackErr = iface.SetCachedCompiledModule(cache)
		}
	}, func(resolved *gov8.WasmModuleCompilationResult) {
		resolutionCalls.Add(1)
		if resolved.Module == nil {
			resolvedError = true
			return
		}
		resolvedModule = true
		compiled, err := resolved.Module.CompiledModule()
		if err != nil {
			callbackErr = err
			return
		}
		defer compiled.Close()
		serialized, err := compiled.Serialize()
		if err != nil {
			callbackErr = err
			return
		}
		reserializedEqual = bytes.Equal(serialized.SerializedBytes(), cache.SerializedBytes())
		sourceURL, callbackErr = compiled.SourceURL()
		if callbackErr != nil {
			return
		}
		global, err := r.ctx.GlobalObject(resolved.CallbackScope.Scope())
		if err != nil {
			callbackErr = err
			return
		}
		if ok, err := global.SetByName(resolved.CallbackScope.Scope(), r.ctx, "compiledModule", resolved.Module.Value); err != nil || !ok {
			callbackErr = fmt.Errorf("set compiledModule = %v, %w", ok, err)
			return
		}
		value, err := eval(resolved.CallbackScope.Scope(), r.ctx, "new WebAssembly.Instance(compiledModule).exports.run()")
		if err != nil {
			callbackErr = err
			return
		}
		executesTo, _, callbackErr = value.IntegerValue(r.ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 10000 {
		if resolutionCalls.Load() != 0 {
			break
		}
		if _, err := r.iso.PumpMessageLoop(false); err != nil {
			t.Fatal(err)
		}
		if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
	if resolutionCalls.Load() != 1 {
		t.Fatalf("resolution calls = %d", resolutionCalls.Load())
	}
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	return map[string]any{
		"callback_wire_matches":     bytes.Equal(callbackWire, answerModule),
		"cache_accepted":            cacheAccepted,
		"resolution_calls":          resolutionCalls.Load(),
		"resolved_module":           resolvedModule,
		"resolved_error":            resolvedError,
		"executes_to":               executesTo,
		"reserialized_equals_input": reserializedEqual,
		"source_url":                sourceURL,
	}
}

func TestTypedCacheSafetyAndLifetime(t *testing.T) {
	cache, _ := produceCache(t)
	original := cache.SerializedBytes()
	copyBytes := cache.SerializedBytes()
	copyBytes[0] ^= 0xff
	if !bytes.Equal(original, cache.SerializedBytes()) {
		t.Fatal("serialized cache was mutable through its copy")
	}

	// A compiled wrapper remains shareable after its producer isolate is gone,
	// and Serialize itself has no isolate/thread affinity.
	r := newRuntime(t, nil)
	module, err := r.ctx.CompileWasmModule(r.scope, answerModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		t.Fatal(err)
	}
	r.close(t)
	done := make(chan error, 1)
	go func() {
		_, err := compiled.Serialize()
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatalf("cross-thread Serialize after producer disposal: %v", err)
	}
	if err := compiled.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.Serialize(); err == nil {
		t.Fatal("Serialize after Close succeeded")
	}

	testCachePrevalidation(t, cache)
}

func testCachePrevalidation(t *testing.T, cache *gov8.SerializedWasmModuleCache) {
	t.Helper()
	streams := make(chan *gov8.WasmStreaming, 1)
	r := newRuntime(t, func(_ *gov8.CallbackScope, _ gov8.Value, stream *gov8.WasmStreaming) { streams <- stream })
	defer r.close(t)
	promise := compileStreaming(t, r)
	stream := <-streams
	if err := stream.SetHasCompiledModuleBytes(); err != nil {
		t.Fatal(err)
	}
	if err := stream.OnBytesReceived(emptyModule); err != nil {
		t.Fatal(err)
	}
	var mismatchErr, retryErr, expiredErr, expiredCrossThreadErr error
	var retryAccepted bool
	var iface *gov8.ModuleCachingInterface
	if err := stream.Finish(func(value *gov8.ModuleCachingInterface) {
		iface = value
		_, mismatchErr = value.SetCachedCompiledModule(cache)
		retryAccepted, retryErr = value.SetCachedCompiledModuleBytes(nil)
	}); err != nil {
		t.Fatal(err)
	}
	if mismatchErr == nil || retryErr != nil || retryAccepted {
		t.Fatalf("typed mismatch/retry = %v / %v, %v", mismatchErr, retryAccepted, retryErr)
	}
	_, expiredErr = iface.SetCachedCompiledModule(cache)
	done := make(chan struct{})
	go func() {
		_, expiredCrossThreadErr = iface.SetCachedCompiledModule(cache)
		close(done)
	}()
	<-done
	if expiredErr == nil || expiredCrossThreadErr == nil {
		t.Fatalf("expired callback interface errors = %v, %v", expiredErr, expiredCrossThreadErr)
	}
	pumpPromise(t, r, promise)

	// Native consumption is one-shot even when the first candidate is valid;
	// Go turns rusty_v8's second-set fatal CHECK into a deterministic error.
	streams2 := make(chan *gov8.WasmStreaming, 1)
	r2 := newRuntime(t, func(_ *gov8.CallbackScope, _ gov8.Value, stream *gov8.WasmStreaming) { streams2 <- stream })
	defer r2.close(t)
	promise2 := compileStreaming(t, r2)
	stream2 := <-streams2
	if err := stream2.SetHasCompiledModuleBytes(); err != nil {
		t.Fatal(err)
	}
	if err := stream2.OnBytesReceived(answerModule); err != nil {
		t.Fatal(err)
	}
	var first bool
	var firstErr, secondErr error
	if err := stream2.Finish(func(value *gov8.ModuleCachingInterface) {
		first, firstErr = value.SetCachedCompiledModule(cache)
		_, secondErr = value.SetCachedCompiledModuleBytes(cache.SerializedBytes())
	}); err != nil {
		t.Fatal(err)
	}
	if !first || firstErr != nil || secondErr == nil {
		t.Fatalf("one-shot result = %v/%v, second=%v", first, firstErr, secondErr)
	}
	pumpPromise(t, r2, promise2)
}

func TestRawFatalCacheBoundaries(t *testing.T) {
	for _, mode := range []string{"mismatched-wire", "truncated-cache"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestRawFatalCacheBoundaryChild$", "-test.v=false")
			cmd.Env = append(os.Environ(), "GOV8_WASM_CACHE_FATAL="+mode)
			output, err := cmd.CombinedOutput()
			exitErr, ok := err.(*exec.ExitError)
			if !ok || exitErr.ExitCode() != 2147483651 {
				t.Fatalf("%s exit=%v, want STATUS_BREAKPOINT; output:\n%s", mode, err, output)
			}
			if !bytes.Contains(output, []byte("marker:before-attempt:"+mode)) || bytes.Contains(output, []byte("marker:after-attempt:"+mode)) {
				t.Fatalf("%s markers incorrect:\n%s", mode, output)
			}
		})
	}
}

func TestRawFatalCacheBoundaryChild(t *testing.T) {
	mode := os.Getenv("GOV8_WASM_CACHE_FATAL")
	if mode == "" {
		t.Skip("fatal subprocess only")
	}
	installBreakpointExit()
	cache, _ := produceCache(t)
	candidate := cache.SerializedBytes()
	wire := answerModule
	if mode == "mismatched-wire" {
		wire = emptyModule
	} else if mode == "truncated-cache" {
		candidate = candidate[:len(candidate)-1]
	} else {
		t.Fatalf("unknown fatal mode %q", mode)
	}
	streams := make(chan *gov8.WasmStreaming, 1)
	r := newRuntime(t, func(_ *gov8.CallbackScope, _ gov8.Value, stream *gov8.WasmStreaming) { streams <- stream })
	defer r.close(t)
	_ = compileStreaming(t, r)
	stream := <-streams
	if err := stream.SetHasCompiledModuleBytes(); err != nil {
		t.Fatal(err)
	}
	if err := stream.OnBytesReceived(wire); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "marker:before-attempt:"+mode)
	if err := stream.Finish(func(value *gov8.ModuleCachingInterface) {
		_, _ = value.SetCachedCompiledModuleBytes(candidate)
	}); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "marker:after-attempt:"+mode)
}

func installBreakpointExit() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	_, _, _ = kernel32.NewProc("SetErrorMode").Call(0x0001 | 0x0002 | 0x8000)
	exitProcess := kernel32.NewProc("ExitProcess")
	filter := syscall.NewCallback(func(exceptionPointers uintptr) uintptr {
		record := *(*uintptr)(wordToPointer(exceptionPointers))
		code := *(*uint32)(wordToPointer(record))
		if code == 0x80000003 {
			exitProcess.Call(uintptr(code))
		}
		return 0
	})
	_, _, _ = kernel32.NewProc("AddVectoredExceptionHandler").Call(1, filter)
	runtime.KeepAlive(filter)
}

func wordToPointer(word uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&word))
}

func TestFixtureHasNoUncomparedChecks(t *testing.T) {
	order, values := fixture(t)
	if len(order) != len(values) {
		t.Fatalf("fixture order/value sizes differ: %d/%d", len(order), len(values))
	}
	if strings.Join(order, "\n") == "" {
		t.Fatal("fixture is empty")
	}
}
