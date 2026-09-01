//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	gov8 "gov8"
)

func requireRawString(v gov8.Value, ok bool, err error, want string) error {
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("value is absent")
	}
	isString, err := v.IsString()
	if err != nil {
		return err
	}
	if !isString {
		return fmt.Errorf("value is not a String")
	}
	got, err := v.StringValue()
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("text = %q, want %q", got, want)
	}
	return nil
}

func TestMessageRawLocalValuesAndPresence(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}

	const source = "function fail(){ throw new TypeError('raw-local'); }\nfail();"
	if compileRunTryCatch(t, ctx, scope, source,
		&gov8.Origin{ResourceName: "raw-local.js"}, tc) {
		t.Fatal("throwing script succeeded")
	}
	message, ok, err := tc.Message(scope)
	if err != nil || !ok {
		t.Fatalf("Message = %v, %v", ok, err)
	}

	textA, err := message.TextValue()
	if err != nil {
		t.Fatalf("TextValue: %v", err)
	}
	textB, err := message.TextValue()
	if err != nil {
		t.Fatalf("TextValue repeat: %v", err)
	}
	if err := requireRawString(textA, true, nil, "Uncaught TypeError: raw-local"); err != nil {
		t.Fatalf("TextValue: %v", err)
	}
	if equal, err := textA.StrictEquals(textB); err != nil || !equal {
		t.Fatalf("repeated TextValue equality = %v, %v", equal, err)
	}

	sourceA, sourceOK, err := message.SourceLineValue(ctx)
	if err := requireRawString(sourceA, sourceOK, err,
		"function fail(){ throw new TypeError('raw-local'); }"); err != nil {
		t.Fatalf("SourceLineValue: %v", err)
	}
	sourceB, sourceOK, err := message.SourceLineValue(ctx)
	if err != nil || !sourceOK {
		t.Fatalf("SourceLineValue repeat = %v, %v", sourceOK, err)
	}
	if equal, err := sourceA.StrictEquals(sourceB); err != nil || !equal {
		t.Fatalf("repeated SourceLineValue equality = %v, %v", equal, err)
	}

	resourceA, resourceOK, err := message.ResourceNameValue()
	if err := requireRawString(resourceA, resourceOK, err, "raw-local.js"); err != nil {
		t.Fatalf("ResourceNameValue: %v", err)
	}
	resourceB, resourceOK, err := message.ResourceNameValue()
	if err != nil || !resourceOK {
		t.Fatalf("ResourceNameValue repeat = %v, %v", resourceOK, err)
	}
	if equal, err := resourceA.StrictEquals(resourceB); err != nil || !equal {
		t.Fatalf("repeated ResourceNameValue equality = %v, %v", equal, err)
	}

	// Rust returns these locals with the parent-scope lifetime, not the
	// TryCatch lifetime. The Go handles must remain usable after Close too.
	if err := tc.Close(); err != nil {
		t.Fatalf("TryCatch.Close: %v", err)
	}
	if err := requireRawString(textA, true, nil, "Uncaught TypeError: raw-local"); err != nil {
		t.Fatalf("TextValue after TryCatch.Close: %v", err)
	}

	native, err := ctx.NewError(scope, "native")
	if err != nil {
		t.Fatalf("NewError: %v", err)
	}
	nativeMessage, err := ctx.CreateMessage(scope, native)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	emptyLine, emptyOK, err := nativeMessage.SourceLineValue(ctx)
	if err := requireRawString(emptyLine, emptyOK, err, ""); err != nil {
		t.Fatalf("empty SourceLineValue must be Some(String): %v", err)
	}
	undefinedResource, resourceOK, err := nativeMessage.ResourceNameValue()
	if err != nil || !resourceOK {
		t.Fatalf("native ResourceNameValue = %v, %v", resourceOK, err)
	}
	if isUndefined, err := undefinedResource.IsUndefined(); err != nil || !isUndefined {
		t.Fatalf("native resource IsUndefined = %v, %v", isUndefined, err)
	}

	if value, present, err := scope.CurrentScriptNameOrSourceURLValue(); err != nil || present || value != (gov8.Value{}) {
		t.Fatalf("idle CurrentScriptNameOrSourceURLValue = %#v, %v, %v", value, present, err)
	}
}

func TestStackFrameRawLocalValues(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	if _, ok, err := scope.CurrentScriptNameOrSourceURLValue(); err != nil || ok {
		t.Fatalf("idle CurrentScriptNameOrSourceURLValue = %v, %v", ok, err)
	}

	var callbackErr error
	var escapedSource gov8.Value
	var escapedFrame *gov8.StackFrame
	host, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
			currentA, currentOK, err := cs.Scope().CurrentScriptNameOrSourceURLValue()
			if err := requireRawString(currentA, currentOK, err, "raw-frame.js"); err != nil {
				callbackErr = fmt.Errorf("current script: %w", err)
				return
			}
			currentB, currentOK, err := cs.Scope().CurrentScriptNameOrSourceURLValue()
			if err != nil || !currentOK {
				callbackErr = fmt.Errorf("current script repeat = %v, %v", currentOK, err)
				return
			}
			if equal, err := currentA.StrictEquals(currentB); err != nil || !equal {
				callbackErr = fmt.Errorf("current script equality = %v, %v", equal, err)
				return
			}

			trace, traceOK, err := cs.Scope().CurrentStackTrace(4)
			if err != nil || !traceOK {
				callbackErr = fmt.Errorf("CurrentStackTrace = %v, %v", traceOK, err)
				return
			}
			frame, err := trace.Frame(0)
			if err != nil {
				callbackErr = err
				return
			}
			escapedFrame = frame

			checks := []struct {
				name string
				want string
				get  func() (gov8.Value, bool, error)
			}{
				{"FunctionNameValue", "f", frame.FunctionNameValue},
				{"ScriptNameValue", "raw-frame.js", frame.ScriptNameValue},
				{"ScriptNameOrSourceURLValue", "raw-frame.js", frame.ScriptNameOrSourceURLValue},
				{"SourceMappingURLValue", "raw-frame.js.map", frame.SourceMappingURLValue},
			}
			for _, check := range checks {
				first, present, err := check.get()
				if err := requireRawString(first, present, err, check.want); err != nil {
					callbackErr = fmt.Errorf("%s: %w", check.name, err)
					return
				}
				second, present, err := check.get()
				if err != nil || !present {
					callbackErr = fmt.Errorf("%s repeat = %v, %v", check.name, present, err)
					return
				}
				if equal, err := first.StrictEquals(second); err != nil || !equal {
					callbackErr = fmt.Errorf("%s equality = %v, %v", check.name, equal, err)
					return
				}
			}

			scriptSource, sourceOK, err := frame.ScriptSourceValue()
			if err != nil || !sourceOK {
				callbackErr = fmt.Errorf("ScriptSourceValue = %v, %v", sourceOK, err)
				return
			}
			isString, err := scriptSource.IsString()
			if err != nil || !isString {
				callbackErr = fmt.Errorf("ScriptSourceValue IsString = %v, %v", isString, err)
				return
			}
			sourceText, err := scriptSource.StringValue()
			if err != nil || !strings.Contains(sourceText, "function f(){ host(); }") {
				callbackErr = fmt.Errorf("ScriptSourceValue = %q, %v", sourceText, err)
				return
			}
			escapedSource = scriptSource

			count, err := trace.FrameCount()
			if err != nil || count < 2 {
				callbackErr = fmt.Errorf("FrameCount = %d, %v", count, err)
				return
			}
			anonymous, err := trace.Frame(1)
			if err != nil {
				callbackErr = err
				return
			}
			if value, present, err := anonymous.FunctionNameValue(); err != nil || present || value != (gov8.Value{}) {
				callbackErr = fmt.Errorf("anonymous FunctionNameValue = %#v, %v, %v", value, present, err)
			}
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
	if !compileRunTryCatch(t, ctx, scope, "function f(){ host(); }\nf();",
		&gov8.Origin{ResourceName: "raw-frame.js", SourceMapURL: "raw-frame.js.map"}, nil) {
		t.Fatal("frame script failed")
	}
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	if _, err := escapedSource.IsString(); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("callback-local source after return error = %v", err)
	}
	if _, _, err := escapedFrame.ScriptNameValue(); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("callback-local frame after return error = %v", err)
	}
}

func TestRawMessageValidationAndCaptureLimit(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	isoA, ctxA, scopeA := newTestRuntime(t)
	_, ctxB, scopeB := newTestRuntime(t)

	tc, err := isoA.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	if compileRunTryCatch(t, ctxA, scopeA, "throw new Error('validation')", nil, tc) {
		t.Fatal("throwing script succeeded")
	}
	message, ok, err := tc.Message(scopeA)
	if err != nil || !ok {
		t.Fatalf("Message = %v, %v", ok, err)
	}
	if _, _, err := message.SourceLineValue(ctxB); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign context SourceLineValue error = %v", err)
	}

	threadErrors := make(chan error, 2)
	go func() {
		_, err := message.TextValue()
		threadErrors <- err
		_, _, err = scopeA.CurrentScriptNameOrSourceURLValue()
		threadErrors <- err
	}()
	for range 2 {
		if err := <-threadErrors; err == nil || !strings.Contains(err.Error(), "thread affinity") {
			t.Fatalf("wrong-thread raw getter error = %v", err)
		}
	}

	foreignException, err := ctxB.NewError(scopeB, "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := gov8.ExceptionStackTrace(scopeA, foreignException); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign ExceptionStackTrace error = %v", err)
	}

	const maxInt32 = int(^uint32(0) >> 1)
	if err := isoA.SetCaptureStackTraceForUncaughtExceptions(true, maxInt32); err != nil {
		t.Fatalf("maximum capture limit rejected: %v", err)
	}
	if err := isoA.SetCaptureStackTraceForUncaughtExceptions(false, 0); err != nil {
		t.Fatalf("disable capture: %v", err)
	}
	if err := isoA.SetCaptureStackTraceForUncaughtExceptions(true, maxInt32+1); err == nil || !strings.Contains(err.Error(), "int32 range") {
		t.Fatalf("overflow capture limit error = %v", err)
	}

	if err := tc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMessageRawLocalClosedScope(t *testing.T) {
	iso, ctx, _ := newTestRuntime(t)
	localScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	if compileRunTryCatch(t, ctx, localScope, "throw 'closed-local'", nil, tc) {
		t.Fatal("throwing script succeeded")
	}
	message, ok, err := tc.Message(localScope)
	if err != nil || !ok {
		t.Fatalf("Message = %v, %v", ok, err)
	}
	value, err := message.TextValue()
	if err != nil {
		t.Fatal(err)
	}
	if err := tc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := localScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := message.TextValue(); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("Message.TextValue after Scope.Close error = %v", err)
	}
	if _, err := value.IsString(); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("raw Value after Scope.Close error = %v", err)
	}
}

func TestOriginResourceNameValueTypesAndIdentity(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	empty, err := scope.EmptyString()
	if err != nil {
		t.Fatal(err)
	}
	number, err := scope.Int32(42)
	if err != nil {
		t.Fatal(err)
	}
	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	object, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stringValue, err := scope.NewString("value-name.js")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		value gov8.Value
		check func(gov8.Value) (bool, error)
	}{
		{"string", stringValue, func(v gov8.Value) (bool, error) { return v.IsString() }},
		{"empty", empty, func(v gov8.Value) (bool, error) { return v.IsString() }},
		{"number", number, func(v gov8.Value) (bool, error) { return v.IsNumber() }},
		{"undefined", undefined, func(v gov8.Value) (bool, error) { return v.IsUndefined() }},
		{"object", object.Value, func(v gov8.Value) (bool, error) { return v.IsObject() }},
	}
	for _, test := range cases {
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatal(err)
		}
		origin := &gov8.Origin{
			ResourceName:      "ignored-string-name.js",
			ResourceNameValue: test.value,
		}
		if compileRunTryCatch(t, ctx, scope, "throw new Error('origin-value')", origin, tc) {
			t.Fatalf("%s throwing script succeeded", test.name)
		}
		message, ok, err := tc.Message(scope)
		if err != nil || !ok {
			t.Fatalf("%s Message = %v, %v", test.name, ok, err)
		}
		got, present, err := message.ResourceNameValue()
		if err != nil || !present {
			t.Fatalf("%s ResourceNameValue = %v, %v", test.name, present, err)
		}
		if typeOK, err := test.check(got); err != nil || !typeOK {
			t.Fatalf("%s resource type = %v, %v", test.name, typeOK, err)
		}
		if same, err := got.StrictEquals(test.value); err != nil || !same {
			t.Fatalf("%s resource identity = %v, %v", test.name, same, err)
		}
		if err := tc.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// The Rust fixture only characterizes Script::compile. ScriptCompiler's
	// code-cache path fatals with object resource names, so both broader paths
	// reject Value resources before FFI rather than exposing that boundary.
	valueOrigin := &gov8.Origin{ResourceNameValue: object.Value}
	if _, err := ctx.CompileUnbound(scope, "40 + 2", valueOrigin, gov8.OptNoCompileOptions, nil); err == nil {
		t.Fatal("CompileUnbound accepted a Value resource name")
	}
	if _, _, err := ctx.CompileCached(scope, "40 + 2", valueOrigin, nil, nil); err == nil {
		t.Fatal("CompileCached accepted a Value resource name")
	}
}

func TestOriginResourceNameValueValidation(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	_, ctxA, scopeA := newTestRuntime(t)
	isoB, _, scopeB := newTestRuntime(t)
	foreign, err := scopeB.NewString("foreign.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.CompileWithOrigin(scopeA, "1", &gov8.Origin{ResourceNameValue: foreign}, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign resource error = %v", err)
	}

	closedScope, err := isoB.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedValue, err := closedScope.NewString("closed.js")
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.CompileWithOrigin(scopeA, "1", &gov8.Origin{ResourceNameValue: closedValue}, nil); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("closed resource error = %v", err)
	}

	local, err := scopeA.NewString("thread.js")
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := ctxA.CompileWithOrigin(scopeA, "1", &gov8.Origin{ResourceNameValue: local}, nil)
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread resource error = %v", err)
	}
}

func TestOriginResourceNameValueRetainedByCompiledScript(t *testing.T) {
	iso, ctx, outer := newTestRuntime(t)
	inner, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	resource, err := inner.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := inner.NewString("retained")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := resource.SetByName(inner, ctx, "marker", marker); err != nil || !ok {
		t.Fatalf("set marker = %v, %v", ok, err)
	}
	script, err := ctx.CompileWithOrigin(inner, "throw new Error('retained-origin')",
		&gov8.Origin{ResourceNameValue: resource.Value}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tc.Close() }()
	if _, err := script.Run(outer, tc); err == nil {
		t.Fatal("throwing retained-origin script succeeded")
	}
	message, ok, err := tc.Message(outer)
	if err != nil || !ok {
		t.Fatalf("Message = %v, %v", ok, err)
	}
	retained, present, err := message.ResourceNameValue()
	if err != nil || !present {
		t.Fatalf("ResourceNameValue = %v, %v", present, err)
	}
	retainedObject, err := gov8.AsObject(retained)
	if err != nil {
		t.Fatal(err)
	}
	gotMarker, ok, err := retainedObject.GetByName(outer, ctx, "marker")
	if err != nil || !ok {
		t.Fatalf("get retained marker = %v, %v", ok, err)
	}
	if got, err := gotMarker.ToString(ctx); err != nil || got != "retained" {
		t.Fatalf("retained marker = %q, %v", got, err)
	}
}

func TestTryCatchOverwriteResetAndCaptureReenable(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tc.Close() }()

	if compileRunTryCatch(t, ctx, scope, "throw new Error('first')", nil, tc) {
		t.Fatal("first throw succeeded")
	}
	first, ok, err := tc.Exception(scope)
	if err != nil || !ok {
		t.Fatalf("first exception = %v, %v", ok, err)
	}
	firstMessage, ok, err := tc.Message(scope)
	if err != nil || !ok {
		t.Fatalf("first message = %v, %v", ok, err)
	}
	if compileRunTryCatch(t, ctx, scope, "throw new TypeError('second')", nil, tc) {
		t.Fatal("second throw succeeded")
	}
	second, ok, err := tc.Exception(scope)
	if err != nil || !ok {
		t.Fatalf("second exception = %v, %v", ok, err)
	}
	if same, err := first.StrictEquals(second); err != nil || same {
		t.Fatalf("overwritten exception equality = %v, %v", same, err)
	}
	if text, err := firstMessage.Text(ctx); err != nil || text != "Uncaught Error: first" {
		t.Fatalf("first message after overwrite = %q, %v", text, err)
	}

	if err := tc.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := tc.SetCaptureMessage(false); err != nil {
		t.Fatal(err)
	}
	if compileRunTryCatch(t, ctx, scope, "throw 31", nil, tc) {
		t.Fatal("disabled-capture throw succeeded")
	}
	if message, ok, err := tc.Message(scope); err != nil || ok || message != nil {
		t.Fatalf("disabled Message = %v, %v, %v", message, ok, err)
	}
	if err := tc.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := tc.SetCaptureMessage(true); err != nil {
		t.Fatal(err)
	}
	if compileRunTryCatch(t, ctx, scope, "throw new Error('enabled')", nil, tc) {
		t.Fatal("reenabled throw succeeded")
	}
	message, ok, err := tc.Message(scope)
	if err != nil || !ok {
		t.Fatalf("reenabled Message = %v, %v", ok, err)
	}
	if text, err := message.Text(ctx); err != nil || text != "Uncaught Error: enabled" {
		t.Fatalf("reenabled message = %q, %v", text, err)
	}
}
