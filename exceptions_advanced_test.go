//go:build windows && amd64

package gov8_test

import (
	"runtime"
	"strings"
	"testing"

	gov8 "gov8"
)

func compileRunTryCatch(t *testing.T, c *gov8.Context, s *gov8.Scope, source string, origin *gov8.Origin, tc *gov8.TryCatch) bool {
	t.Helper()
	var script *gov8.Script
	var err error
	if origin == nil {
		script, err = c.Compile(s, source, tc)
	} else {
		script, err = c.CompileWithOrigin(s, source, origin, tc)
	}
	if err != nil {
		return false
	}
	defer func() { _ = script.Close() }()
	_, err = script.Run(s, tc)
	return err == nil
}

func TestAdvancedTryCatchLocalsAndCapture(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}

	if verbose, err := tc.IsVerbose(); err != nil || verbose {
		t.Fatalf("initial IsVerbose = %v, %v", verbose, err)
	}
	if err := tc.SetVerbose(true); err != nil {
		t.Fatalf("SetVerbose(true): %v", err)
	}
	if verbose, err := tc.IsVerbose(); err != nil || !verbose {
		t.Fatalf("IsVerbose after enable = %v, %v", verbose, err)
	}
	if err := tc.SetVerbose(false); err != nil {
		t.Fatalf("SetVerbose(false): %v", err)
	}
	if _, ok, err := tc.Exception(scope); err != nil || ok {
		t.Fatalf("empty Exception = ok %v, %v", ok, err)
	}
	if _, ok, err := tc.StackTrace(scope, ctx); err != nil || ok {
		t.Fatalf("empty StackTrace = ok %v, %v", ok, err)
	}
	if _, ok, err := tc.Rethrow(scope); err != nil || ok {
		t.Fatalf("empty Rethrow = ok %v, %v", ok, err)
	}

	origin := &gov8.Origin{ResourceName: "runtime.js", SourceMapURL: "runtime.js.map"}
	if compileRunTryCatch(t, ctx, scope,
		"function inner(){ null.value; }\ninner();", origin, tc) {
		t.Fatal("throwing run succeeded")
	}
	exception, ok, err := tc.Exception(scope)
	if err != nil || !ok {
		t.Fatalf("Exception = ok %v, %v", ok, err)
	}
	if native, err := exception.IsNativeError(); err != nil || !native {
		t.Fatalf("Exception.IsNativeError = %v, %v", native, err)
	}
	if text, _ := exception.ToString(ctx); !strings.HasPrefix(text, "TypeError: Cannot read properties of null") {
		t.Fatalf("exception text = %q", text)
	}
	stack, ok, err := tc.StackTrace(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("StackTrace = ok %v, %v", ok, err)
	}
	if text, _ := stack.ToString(ctx); !strings.Contains(text, "at inner (runtime.js:1:") {
		t.Fatalf("stack text = %q", text)
	}
	message, ok, err := tc.Message(scope)
	if err != nil || !ok {
		t.Fatalf("Message = ok %v, %v", ok, err)
	}
	if index, err := message.WasmFunctionIndex(); err != nil || index != -1 {
		t.Fatalf("WasmFunctionIndex = %d, %v", index, err)
	}

	// Locals are rooted in scope, not the TryCatch wrapper.
	if err := tc.Close(); err != nil {
		t.Fatalf("TryCatch.Close: %v", err)
	}
	if text, err := exception.ToString(ctx); err != nil || !strings.HasPrefix(text, "TypeError:") {
		t.Fatalf("exception after TryCatch.Close = %q, %v", text, err)
	}
	if text, err := message.Text(ctx); err != nil || !strings.HasPrefix(text, "Uncaught TypeError:") {
		t.Fatalf("message after TryCatch.Close = %q, %v", text, err)
	}
}

func TestAdvancedTryCatchCaptureMessageDisabled(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	if err := tc.SetCaptureMessage(false); err != nil {
		t.Fatalf("SetCaptureMessage(false): %v", err)
	}
	if compileRunTryCatch(t, ctx, scope, "function f(){ throw 17; } f();", nil, tc) {
		t.Fatal("throwing run succeeded")
	}
	exception, ok, err := tc.Exception(scope)
	if err != nil || !ok {
		t.Fatalf("Exception = ok %v, %v", ok, err)
	}
	if text, _ := exception.ToString(ctx); text != "17" {
		t.Fatalf("exception = %q", text)
	}
	if _, ok, err := tc.Message(scope); err != nil || ok {
		t.Fatalf("Message = ok %v, %v", ok, err)
	}
	if _, ok, err := tc.StackTrace(scope, ctx); err != nil || ok {
		t.Fatalf("StackTrace = ok %v, %v", ok, err)
	}
}

func TestAdvancedTryCatchRethrow(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	outer, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("outer NewTryCatch: %v", err)
	}
	defer func() { _ = outer.Close() }()
	inner, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("inner NewTryCatch: %v", err)
	}
	if compileRunTryCatch(t, ctx, scope, "throw ({marker:'same-object'})", nil, inner) {
		t.Fatal("throwing run succeeded")
	}
	before, ok, err := inner.Exception(scope)
	if err != nil || !ok {
		t.Fatalf("inner Exception = ok %v, %v", ok, err)
	}
	returned, ok, err := inner.Rethrow(scope)
	if err != nil || !ok {
		t.Fatalf("Rethrow = ok %v, %v", ok, err)
	}
	if same, err := before.StrictEquals(returned); err != nil || same {
		t.Fatalf("Rethrow result StrictEquals exception = %v, %v", same, err)
	}
	if undef, err := returned.IsUndefined(); err != nil || !undef {
		t.Fatalf("Rethrow result IsUndefined = %v, %v", undef, err)
	}
	inner.Reset()
	if caught, _ := inner.HasCaught(); !caught {
		t.Error("Reset after Rethrow unexpectedly cleared the caught state")
	}
	if err := inner.Close(); err != nil {
		t.Fatalf("inner.Close: %v", err)
	}
	if caught, _ := outer.HasCaught(); !caught {
		t.Fatal("outer did not catch rethrown exception")
	}
	outerException, ok, err := outer.Exception(scope)
	if err != nil || !ok {
		t.Fatalf("outer Exception = ok %v, %v", ok, err)
	}
	obj, err := outerException.ToObject(scope, ctx, outer)
	if err != nil {
		t.Fatalf("ToObject: %v", err)
	}
	marker, ok, err := obj.GetByName(scope, ctx, "marker")
	if err != nil || !ok {
		t.Fatalf("marker = ok %v, %v", ok, err)
	}
	if text, _ := marker.ToString(ctx); text != "same-object" {
		t.Fatalf("marker = %q", text)
	}
}

func TestAdvancedMessageWasmAndFrameFields(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	wasm := "const b=new Uint8Array([0,97,115,109,1,0,0,0,1,4,1,96,0,0,3,2,1,0,7,5,1,1,102,0,0,10,5,1,3,0,0,11]);new WebAssembly.Instance(new WebAssembly.Module(b)).exports.f();"
	if compileRunTryCatch(t, ctx, scope, wasm, nil, tc) {
		t.Fatal("wasm trap succeeded")
	}
	message, ok, err := tc.Message(scope)
	if err != nil || !ok {
		t.Fatalf("Message = ok %v, %v", ok, err)
	}
	if index, err := message.WasmFunctionIndex(); err != nil || index != 0 {
		t.Fatalf("WasmFunctionIndex = %d, %v", index, err)
	}

	var captured struct {
		name, source, sourceMap string
		nameOK, sourceOK, mapOK bool
		err                     error
	}
	host, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
			trace, ok, err := cs.Scope().CurrentStackTrace(4)
			if err != nil || !ok {
				captured.err = err
				return
			}
			frame, err := trace.Frame(0)
			if err != nil {
				captured.err = err
				return
			}
			captured.name, captured.nameOK, captured.err = frame.ScriptNameOrSourceURL()
			if captured.err != nil {
				return
			}
			captured.sourceMap, captured.mapOK, captured.err = frame.SourceMappingURL()
			if captured.err != nil {
				return
			}
			captured.source, captured.sourceOK, captured.err = frame.ScriptSource()
		}, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if ok, err := global.SetByName(scope, ctx, "host", host.Value); err != nil || !ok {
		t.Fatalf("set host = %v, %v", ok, err)
	}
	origin := &gov8.Origin{ResourceName: "frame.js", SourceMapURL: "frame.js.map"}
	if !compileRunTryCatch(t, ctx, scope, "function f(){host()} f();", origin, nil) {
		t.Fatal("host callback run failed")
	}
	if captured.err != nil {
		t.Fatalf("capture: %v", captured.err)
	}
	if !captured.nameOK || captured.name != "frame.js" {
		t.Fatalf("ScriptNameOrSourceURL = %q, %v", captured.name, captured.nameOK)
	}
	if !captured.mapOK || captured.sourceMap != "frame.js.map" {
		t.Fatalf("SourceMappingURL = %q, %v", captured.sourceMap, captured.mapOK)
	}
	if !captured.sourceOK || !strings.Contains(captured.source, "function f()") {
		t.Fatalf("ScriptSource = %q, %v", captured.source, captured.sourceOK)
	}
}

func TestStackTraceFrameRejectsCountBoundary(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var boundaryErr error
	host, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
			trace, ok, err := cs.Scope().CurrentStackTrace(4)
			if err != nil || !ok {
				boundaryErr = err
				return
			}
			count, err := trace.FrameCount()
			if err != nil {
				boundaryErr = err
				return
			}
			_, boundaryErr = trace.Frame(count)
		}, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if ok, err := global.SetByName(scope, ctx, "host", host.Value); err != nil || !ok {
		t.Fatalf("set host = %v, %v", ok, err)
	}
	if !compileRunTryCatch(t, ctx, scope, "host()", nil, nil) {
		t.Fatal("host callback run failed")
	}
	if boundaryErr == nil || !strings.Contains(boundaryErr.Error(), "frame index out of range") {
		t.Fatalf("Frame(FrameCount) error = %v", boundaryErr)
	}
}

func TestAdvancedExceptionWrongThreadAndLifecycle(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	iso, _, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	ready := make(chan struct{})
	release := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		close(ready)
		<-release
		errs <- tc.SetVerbose(true)
		_, _, err := tc.Exception(scope)
		errs <- err
	}()
	<-ready
	close(release)
	for range 2 {
		if err := <-errs; err == nil || !strings.Contains(err.Error(), "thread affinity") {
			t.Fatalf("foreign-thread error = %v", err)
		}
	}
	if err := tc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := tc.IsVerbose(); err == nil {
		t.Fatal("IsVerbose after Close succeeded")
	}
	if err := tc.SetCaptureMessage(false); err == nil {
		t.Fatal("SetCaptureMessage after Close succeeded")
	}
}
