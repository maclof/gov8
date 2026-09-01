//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func compileStreaming(t *testing.T, iso *gov8.Isolate, ctx *gov8.Context, scope *gov8.Scope, source string) gov8.Promise {
	t.Helper()
	script, err := ctx.Compile(scope, "WebAssembly.compileStreaming("+source+")", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Errorf("close script: %v", err)
		}
	}()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tc.Close(); err != nil {
			t.Errorf("close try-catch: %v", err)
		}
	}()
	value, err := script.Run(scope, tc)
	if err != nil {
		text, textErr := tc.ExceptionText(scope, ctx)
		if textErr != nil {
			t.Fatalf("compileStreaming run: %v; read exception: %v", err, textErr)
		}
		t.Fatalf("compileStreaming run: %v: %s", err, text)
	}
	if promise, err := value.IsPromise(); err != nil || !promise {
		t.Fatalf("compileStreaming result promise = %v, %v", promise, err)
	}
	return gov8.Promise{Value: value}
}

func pumpWasmUntil(t *testing.T, iso *gov8.Isolate, done func() bool) {
	t.Helper()
	for range 1000 {
		for {
			ran, err := iso.PumpMessageLoop(false)
			if err != nil {
				t.Fatal(err)
			}
			if !ran {
				break
			}
		}
		if err := iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		if done() {
			return
		}
	}
	t.Fatal("wasm operation did not resolve")
}

func newStreamingRuntime(t *testing.T, callback gov8.WasmStreamingCallback) (*gov8.Isolate, *gov8.Context, *gov8.Scope) {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if err := iso.SetWasmStreamingCallback(callback); err != nil {
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
	t.Cleanup(func() {
		if err := scope.Close(); err != nil {
			t.Errorf("close scope: %v", err)
		}
		if err := ctx.Close(); err != nil {
			t.Errorf("close context: %v", err)
		}
		if err := gov8.ReleaseIsolateHostState(iso); err != nil {
			t.Errorf("release isolate host state: %v", err)
		}
		if err := iso.Close(); err != nil {
			t.Errorf("close isolate: %v", err)
		}
	})
	return iso, ctx, scope
}

func TestWasmStreamingFinishCacheAbortAndDrop(t *testing.T) {
	var sourceText string
	var sourceString, sourceObject bool
	streams := make(chan *gov8.WasmStreaming, 1)
	iso, ctx, scope := newStreamingRuntime(t, func(cs *gov8.CallbackScope, source gov8.Value, stream *gov8.WasmStreaming) {
		var err error
		sourceText, err = cs.ToString(source)
		if err != nil {
			panic(err)
		}
		sourceString, err = source.IsString()
		if err != nil {
			panic(err)
		}
		sourceObject, err = source.IsObject()
		if err != nil {
			panic(err)
		}
		streams <- stream
	})

	promise := compileStreaming(t, iso, ctx, scope, "'https://input.example/module.wasm'")
	stream := <-streams
	if sourceText != "https://input.example/module.wasm" || !sourceString || sourceObject {
		t.Fatalf("source = %q, %v, %v", sourceText, sourceString, sourceObject)
	}
	if state, err := promise.State(); err != nil || state != gov8.PromisePending {
		t.Fatalf("state before finish = %v, %v", state, err)
	}
	for _, chunk := range [][]byte{nil, answerWasmModule[:9], answerWasmModule[9:]} {
		if err := stream.OnBytesReceived(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.SetURL("https://compiled.example/chunked.wasm"); err != nil {
		t.Fatal(err)
	}
	if err := stream.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err == nil {
		t.Fatal("completed stream accepted Close")
	}
	if state, err := promise.State(); err != nil || state != gov8.PromiseFulfilled {
		t.Fatalf("state after finish = %v, %v", state, err)
	}
	result, err := promise.Result(scope)
	if err != nil {
		t.Fatal(err)
	}
	module, err := gov8.AsWasmModuleObject(result)
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
	if wire, err := compiled.WireBytes(); err != nil || !bytes.Equal(wire, answerWasmModule) {
		t.Fatalf("streamed wire = %v, %v", wire, err)
	}
	if url, err := compiled.SourceURL(); err != nil || url != "https://compiled.example/chunked.wasm" {
		t.Fatalf("streamed URL = %q, %v", url, err)
	}

	cachePromise := compileStreaming(t, iso, ctx, scope, "'cache'")
	cacheStream := <-streams
	if err := cacheStream.SetHasCompiledModuleBytes(); err != nil {
		t.Fatal(err)
	}
	if err := cacheStream.OnBytesReceived(emptyWasmModule); err != nil {
		t.Fatal(err)
	}
	var cacheInterface *gov8.ModuleCachingInterface
	var reentrantStreamErr error
	if err := cacheStream.Finish(func(cache *gov8.ModuleCachingInterface) {
		reentrantStreamErr = cacheStream.Close()
		cacheInterface = cache
		wire, err := cache.WireBytes()
		if err != nil || !bytes.Equal(wire, emptyWasmModule) {
			panic("unexpected cache wire bytes")
		}
		accepted, err := cache.SetCachedCompiledModuleBytes(nil)
		if err != nil || accepted {
			panic("empty cache unexpectedly accepted")
		}
		if _, err := cache.SetCachedCompiledModuleBytes(nil); err == nil {
			panic("repeated cache setter accepted")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheInterface.WireBytes(); err == nil {
		t.Fatal("cache interface survived callback")
	}
	if reentrantStreamErr == nil || !strings.Contains(reentrantStreamErr.Error(), "completed wasm stream") {
		t.Fatalf("reentrant stream Close = %v", reentrantStreamErr)
	}
	if state, err := cachePromise.State(); err != nil || state != gov8.PromiseFulfilled {
		t.Fatalf("cache promise = %v, %v", state, err)
	}

	abortPromise := compileStreaming(t, iso, ctx, scope, "'abort'")
	abortStream := <-streams
	exception, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exceptionValue := exception.Value
	if err := abortStream.Abort(&exceptionValue); err != nil {
		t.Fatal(err)
	}
	if state, err := abortPromise.State(); err != nil || state != gov8.PromiseRejected {
		t.Fatalf("abort promise = %v, %v", state, err)
	}

	dropPromise := compileStreaming(t, iso, ctx, scope, "'drop'")
	if err := (<-streams).Close(); err != nil {
		t.Fatal(err)
	}
	if state, err := dropPromise.State(); err != nil || state != gov8.PromisePending {
		t.Fatalf("drop promise = %v, %v", state, err)
	}
}

type wasmCompilationObservation struct {
	calls atomic.Int32
	wire  []byte
	url   string
	err   *gov8.Global
}

func finishWasmCompilation(t *testing.T, compilation *gov8.WasmModuleCompilation,
	context *gov8.Context, scope *gov8.Scope, cache gov8.ModuleCachingCallback) *wasmCompilationObservation {
	t.Helper()
	observation := &wasmCompilationObservation{}
	if err := compilation.Finish(scope, context, cache, func(result *gov8.WasmModuleCompilationResult) {
		observation.calls.Add(1)
		if result.Module != nil {
			compiled, err := result.Module.CompiledModule()
			if err != nil {
				panic(err)
			}
			defer func() {
				if err := compiled.Close(); err != nil {
					panic(err)
				}
			}()
			observation.wire, err = compiled.WireBytes()
			if err != nil {
				panic(err)
			}
			observation.url, err = compiled.SourceURL()
			if err != nil {
				panic(err)
			}
			return
		}
		var err error
		observation.err, err = gov8.NewGlobal(result.CallbackScope.Scope(), result.Error)
		if err != nil {
			panic(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	return observation
}

func TestWasmModuleCompilationSuccessFailureAndLifecycle(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	success, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := success.OnBytesReceived(answerWasmModule[:3]); err != nil {
		t.Fatal(err)
	}
	if err := success.OnBytesReceived(answerWasmModule[3:]); err != nil {
		t.Fatal(err)
	}
	if err := success.SetURL("https://async.example/answer.wasm"); err != nil {
		t.Fatal(err)
	}
	successResult := finishWasmCompilation(t, success, ctx, scope, nil)
	pumpWasmUntil(t, iso, func() bool { return successResult.calls.Load() != 0 })
	if successResult.calls.Load() != 1 || !bytes.Equal(successResult.wire, answerWasmModule) || successResult.url != "https://async.example/answer.wasm" {
		t.Fatalf("success = %d, %v, %q", successResult.calls.Load(), successResult.wire, successResult.url)
	}
	if err := success.Close(); err == nil {
		t.Fatal("finished compilation accepted Close")
	}

	failure, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := failure.OnBytesReceived([]byte{0, 1, 2, 3, 4, 5, 6, 7}); err != nil {
		t.Fatal(err)
	}
	failureResult := finishWasmCompilation(t, failure, ctx, scope, nil)
	pumpWasmUntil(t, iso, func() bool { return failureResult.calls.Load() != 0 })
	if failureResult.err == nil {
		t.Fatal("invalid compilation did not return an error")
	}
	errorValue, err := failureResult.err.ToLocal(scope)
	if err != nil {
		t.Fatal(err)
	}
	errorText, err := errorValue.ToString(ctx)
	if err != nil || !strings.Contains(errorText, "expected magic word") {
		t.Fatalf("compilation error = %q, %v", errorText, err)
	}
	if err := failureResult.err.Close(); err != nil {
		t.Fatal(err)
	}

	crossThread := make(chan *gov8.WasmModuleCompilation, 1)
	go func() {
		compilation, err := gov8.NewWasmModuleCompilation()
		if err == nil {
			err = compilation.OnBytesReceived(answerWasmModule)
		}
		if err != nil {
			crossThread <- nil
			return
		}
		crossThread <- compilation
	}()
	moved := <-crossThread
	if moved == nil {
		t.Fatal("cross-thread compilation setup failed")
	}
	if err := moved.SetURL("https://async.example/cross-thread.wasm"); err != nil {
		t.Fatal(err)
	}
	movedResult := finishWasmCompilation(t, moved, ctx, scope, nil)
	pumpWasmUntil(t, iso, func() bool { return movedResult.calls.Load() != 0 })
	if movedResult.calls.Load() != 1 || !bytes.Equal(movedResult.wire, answerWasmModule) {
		t.Fatalf("cross-thread result = %d, %v", movedResult.calls.Load(), movedResult.wire)
	}

	cacheMarked, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := cacheMarked.SetHasCompiledModuleBytes(); err != nil {
		t.Fatal(err)
	}
	if err := cacheMarked.OnBytesReceived(emptyWasmModule); err != nil {
		t.Fatal(err)
	}
	var reentrantCompilationErr error
	cacheResult := finishWasmCompilation(t, cacheMarked, ctx, scope, func(cache *gov8.ModuleCachingInterface) {
		reentrantCompilationErr = cacheMarked.Close()
		accepted, err := cache.SetCachedCompiledModuleBytes(nil)
		if err != nil || accepted {
			panic("empty cache accepted")
		}
	})
	pumpWasmUntil(t, iso, func() bool { return cacheResult.calls.Load() != 0 })
	if !bytes.Equal(cacheResult.wire, emptyWasmModule) {
		t.Fatalf("cache fallback wire = %v", cacheResult.wire)
	}
	if reentrantCompilationErr == nil || !strings.Contains(reentrantCompilationErr.Error(), "completed wasm module compilation") {
		t.Fatalf("reentrant compilation Close = %v", reentrantCompilationErr)
	}

	aborted, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := aborted.OnBytesReceived(emptyWasmModule); err != nil {
		t.Fatal(err)
	}
	abortDone := make(chan error, 1)
	go func() { abortDone <- aborted.Abort() }()
	if err := <-abortDone; err != nil {
		t.Fatal(err)
	}
	unstarted, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := unstarted.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWasmCacheMarkOrderAndStreamingAffinity(t *testing.T) {
	streams := make(chan *gov8.WasmStreaming, 1)
	iso, ctx, scope := newStreamingRuntime(t, func(_ *gov8.CallbackScope, _ gov8.Value, stream *gov8.WasmStreaming) {
		streams <- stream
	})
	_ = compileStreaming(t, iso, ctx, scope, "'order'")
	stream := <-streams
	if err := stream.OnBytesReceived(nil); err != nil {
		t.Fatal(err)
	}
	if err := stream.SetHasCompiledModuleBytes(); err == nil || !strings.Contains(err.Error(), "before OnBytesReceived") {
		t.Fatalf("stream cache order = %v", err)
	}
	wrongThread := make(chan error, 1)
	go func() { wrongThread <- stream.SetURL("wrong-thread") }()
	if err := <-wrongThread; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("stream wrong-thread = %v", err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err == nil || !strings.Contains(err.Error(), "active wasm streams") {
		t.Fatalf("release with active stream = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	compilation, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := compilation.OnBytesReceived(nil); err != nil {
		t.Fatal(err)
	}
	if err := compilation.SetHasCompiledModuleBytes(); err == nil || !strings.Contains(err.Error(), "before OnBytesReceived") {
		t.Fatalf("compilation cache order = %v", err)
	}
	if err := compilation.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWasmStreamingRegistrationAndActiveCallbackSafety(t *testing.T) {
	late, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	lateContext, err := late.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := late.SetWasmStreamingCallback(func(*gov8.CallbackScope, gov8.Value, *gov8.WasmStreaming) {}); err == nil || !strings.Contains(err.Error(), "before creating a context") {
		t.Fatalf("late streaming callback error = %v", err)
	}
	if err := lateContext.Close(); err != nil {
		t.Fatal(err)
	}
	if err := late.Close(); err != nil {
		t.Fatal(err)
	}

	lateOptions, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	lateOptionsScope, err := lateOptions.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	lateOptionsContext, err := lateOptions.NewContextWithOptions(lateOptionsScope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := lateOptions.SetWasmStreamingCallback(func(*gov8.CallbackScope, gov8.Value, *gov8.WasmStreaming) {}); err == nil || !strings.Contains(err.Error(), "before creating a context") {
		t.Fatalf("late options streaming callback error = %v", err)
	}
	if err := lateOptionsContext.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lateOptionsScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lateOptions.Close(); err != nil {
		t.Fatal(err)
	}

	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	var callbackReleaseErr error
	if err := iso.SetWasmStreamingCallback(func(_ *gov8.CallbackScope, _ gov8.Value, stream *gov8.WasmStreaming) {
		if err := stream.OnBytesReceived(emptyWasmModule); err != nil {
			panic(err)
		}
		if err := stream.Finish(nil); err != nil {
			panic(err)
		}
		callbackReleaseErr = gov8.ReleaseIsolateHostState(iso)
	}); err != nil {
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
	_ = compileStreaming(t, iso, ctx, scope, "'active-callback'")
	if callbackReleaseErr == nil || !strings.Contains(callbackReleaseErr.Error(), "active wasm callback") {
		t.Fatalf("release inside streaming callback = %v", callbackReleaseErr)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWasmResolutionCallbackAndForeignContextSafety(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	_, foreignContext, _ := newTestRuntime(t)
	foreign, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := foreign.Finish(scope, foreignContext, nil, func(*gov8.WasmModuleCompilationResult) {}); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign context finish = %v", err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}

	compilation, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := compilation.OnBytesReceived(emptyWasmModule); err != nil {
		t.Fatal(err)
	}
	var callbackReleaseErr error
	if err := compilation.Finish(scope, ctx, nil, func(*gov8.WasmModuleCompilationResult) {
		callbackReleaseErr = gov8.ReleaseIsolateHostState(iso)
	}); err != nil {
		t.Fatal(err)
	}
	pumpWasmUntil(t, iso, func() bool { return callbackReleaseErr != nil })
	if !strings.Contains(callbackReleaseErr.Error(), "active wasm callback") {
		t.Fatalf("release inside resolution callback = %v", callbackReleaseErr)
	}
}
