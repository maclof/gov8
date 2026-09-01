//go:build windows && amd64

package exceptionconstructorsconformance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	gov8 "gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-exception-constructors-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type outcome struct {
	check string
	value any
}

type reportLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

func line(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded) + "\n"
}

func report(t *testing.T) string {
	t.Helper()
	checks := []func(*testing.T) outcome{
		checkConstructorMatrix,
		checkMessageBoundaries,
		checkPrimitiveMessages,
		checkNativeDetails,
		checkScriptedReconstruction,
		checkCurrentStackFallback,
		checkCrossContextGlobal,
	}
	var output strings.Builder
	for _, check := range checks {
		result := check(t)
		output.WriteString(line(t, reportLine{Check: result.check, OK: true, Value: result.value}))
	}
	summary := struct {
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}{}
	summary.Summary.Total = len(checks)
	summary.Summary.Passed = len(checks)
	output.WriteString(line(t, summary))
	return output.String()
}

func TestPinnedFixtureByteForByte(t *testing.T) {
	got := report(t)
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		wantLines := strings.Split(strings.TrimRight(string(want), "\n"), "\n")
		gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		for index := 0; index < len(wantLines) || index < len(gotLines); index++ {
			var expected, actual string
			if index < len(wantLines) {
				expected = wantLines[index]
			}
			if index < len(gotLines) {
				actual = gotLines[index]
			}
			if expected != actual {
				t.Errorf("line %d:\nwant %s\n got %s", index+1, expected, actual)
			}
		}
		t.Fatal("Go report differs from pinned Rust exception-constructor fixture")
	}
}

func TestReportDeterministic(t *testing.T) {
	if first, second := report(t), report(t); first != second {
		t.Fatal("two exception-constructor reports differ")
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
	return &runtime{iso, ctx, scope}
}

func (r *runtime) close(t *testing.T) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Error(err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Error(err)
	}
	if err := r.iso.Close(); err != nil {
		t.Error(err)
	}
}

func (r *runtime) eval(t *testing.T, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (r *runtime) evalOrigin(t *testing.T, source, resource string) gov8.Value {
	t.Helper()
	script, err := r.ctx.CompileWithOrigin(r.scope, source, &gov8.Origin{ResourceName: resource}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func traceFrames(t *testing.T, trace *gov8.StackTrace, ok bool, err error) int {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return 0
	}
	count, err := trace.FrameCount()
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func checkConstructorMatrix(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	type constructor func(*gov8.Scope, string) (gov8.Value, error)
	constructors := []struct {
		kind string
		new  constructor
	}{
		{"Error", r.ctx.NewError},
		{"RangeError", r.ctx.NewRangeError},
		{"ReferenceError", r.ctx.NewReferenceError},
		{"SyntaxError", r.ctx.NewSyntaxError},
		{"TypeError", r.ctx.NewTypeError},
	}
	type value struct {
		Kind               string `json:"kind"`
		ToString           string `json:"to_string"`
		MessageProperty    string `json:"message_property"`
		IsObject           bool   `json:"is_object"`
		IsNativeError      bool   `json:"is_native_error"`
		ConstructorName    string `json:"constructor_name"`
		PrototypeIsObject  bool   `json:"prototype_is_object"`
		InstanceOfMatching bool   `json:"instance_of_matching"`
		UncaughtMessage    string `json:"uncaught_message"`
		ExceptionStackNone bool   `json:"exception_stack_none"`
	}
	values := make([]value, 0, len(constructors))
	global, _ := r.ctx.GlobalObject(r.scope)
	for _, item := range constructors {
		exception, err := item.new(r.scope, "oracle-message")
		if err != nil {
			t.Fatal(err)
		}
		object, err := gov8.AsObject(exception)
		if err != nil {
			t.Fatal(err)
		}
		property, _, _ := object.GetByName(r.scope, r.ctx, "message")
		constructorName, _ := object.GetConstructorName(r.scope)
		prototype, _ := object.GetPrototype(r.scope)
		constructorValue, _, _ := global.GetByName(r.scope, r.ctx, item.kind)
		constructorObject, _ := gov8.AsObject(constructorValue)
		matching, _ := exception.InstanceOf(r.scope, r.ctx, constructorObject, nil)
		message, _ := r.ctx.CreateMessage(r.scope, exception)
		uncaught, _ := message.Text(r.ctx)
		_, hasTrace, _ := r.ctx.GetExceptionStackTrace(r.scope, exception)
		toString, _ := exception.ToString(r.ctx)
		messageProperty, _ := property.ToString(r.ctx)
		name, _ := constructorName.StringValue()
		isObject, _ := exception.IsObject()
		isNative, _ := exception.IsNativeError()
		prototypeObject, _ := prototype.IsObject()
		values = append(values, value{item.kind, toString, messageProperty, isObject, isNative, name, prototypeObject, matching, uncaught, !hasTrace})
	}
	return outcome{"exception-constructors/constructors/five_native_error_kinds", values}
}

func checkMessageBoundaries(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	empty, _ := r.ctx.NewTypeError(r.scope, "")
	multiline, _ := r.ctx.NewError(r.scope, "first\nsecond 🦀")
	emptyString, _ := empty.ToString(r.ctx)
	multilineString, _ := multiline.ToString(r.ctx)
	emptyMessage, _ := r.ctx.CreateMessage(r.scope, empty)
	multilineMessage, _ := r.ctx.CreateMessage(r.scope, multiline)
	emptyUncaught, _ := emptyMessage.Text(r.ctx)
	multilineUncaught, _ := multilineMessage.Text(r.ctx)
	value := struct {
		EmptyToString     string `json:"empty_to_string"`
		EmptyUncaught     string `json:"empty_uncaught"`
		MultilineToString string `json:"multiline_to_string"`
		MultilineUncaught string `json:"multiline_uncaught"`
	}{emptyString, emptyUncaught, multilineString, multilineUncaught}
	return outcome{"exception-constructors/constructors/message_boundaries", value}
}

func checkPrimitiveMessages(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	undefined, _ := r.scope.Undefined()
	null, _ := r.scope.Null()
	boolean, _ := r.scope.Boolean(true)
	number, _ := r.scope.Int32(42)
	stringValue, _ := r.scope.NewString("plain")
	bigint, _ := r.scope.BigIntFromInt64(99)
	description, _ := r.scope.NewString("sym")
	symbol, _ := r.scope.NewSymbol(description)
	primitives := []struct {
		kind  string
		value gov8.Value
	}{
		{"undefined", undefined}, {"null", null}, {"boolean", boolean},
		{"number", number}, {"string", stringValue}, {"bigint", bigint},
		{"symbol", symbol.Value},
	}
	type value struct {
		Kind      string `json:"kind"`
		Text      string `json:"text"`
		StackNone bool   `json:"stack_none"`
	}
	values := make([]value, 0, len(primitives))
	for _, primitive := range primitives {
		message, err := r.ctx.CreateMessage(r.scope, primitive.value)
		if err != nil {
			t.Fatal(err)
		}
		text, _ := message.Text(r.ctx)
		_, hasStack, _ := message.StackTrace()
		values = append(values, value{primitive.kind, text, !hasStack})
	}
	return outcome{"exception-constructors/create-message/primitive_values", values}
}

func checkNativeDetails(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	errorValue, _ := r.ctx.NewRangeError(r.scope, "native-details")
	message, _ := r.ctx.CreateMessage(r.scope, errorValue)
	text, _ := message.Text(r.ctx)
	sourceLine, _, _ := message.SourceLine(r.ctx)
	resource, _ := message.ResourceName(r.ctx)
	lineNumber, _, _ := message.LineNumber(r.ctx)
	startPosition, _ := message.StartPosition()
	endPosition, _ := message.EndPosition()
	startColumn, _ := message.StartColumn()
	endColumn, _ := message.EndColumn()
	wasmIndex, _ := message.WasmFunctionIndex()
	errorLevel, _ := message.ErrorLevel()
	shared, _ := message.IsSharedCrossOrigin()
	opaque, _ := message.IsOpaque()
	_, messageHasStack, _ := message.StackTrace()
	_, exceptionHasStack, _ := r.ctx.GetExceptionStackTrace(r.scope, errorValue)
	value := struct {
		Text               string `json:"text"`
		SourceLine         string `json:"source_line"`
		Resource           string `json:"resource"`
		Line               int32  `json:"line"`
		StartPosition      int64  `json:"start_position"`
		EndPosition        int64  `json:"end_position"`
		StartColumn        int64  `json:"start_column"`
		EndColumn          int64  `json:"end_column"`
		WasmFunctionIndex  int64  `json:"wasm_function_index"`
		ErrorLevel         int64  `json:"error_level"`
		SharedCrossOrigin  bool   `json:"shared_cross_origin"`
		Opaque             bool   `json:"opaque"`
		MessageStackNone   bool   `json:"message_stack_none"`
		ExceptionStackNone bool   `json:"exception_stack_none"`
	}{text, sourceLine, resource, lineNumber, startPosition, endPosition, startColumn, endColumn, wasmIndex, errorLevel, shared, opaque, !messageHasStack, !exceptionHasStack}
	return outcome{"exception-constructors/create-message/native_error_details", value}
}

func checkScriptedReconstruction(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	errorValue := r.evalOrigin(t, "function makeError(){ return new Error('scripted'); }\nmakeError();", "constructors.js")
	exceptionTrace, exceptionTraceOK, exceptionTraceErr := r.ctx.GetExceptionStackTrace(r.scope, errorValue)
	exceptionFrames := traceFrames(t, exceptionTrace, exceptionTraceOK, exceptionTraceErr)
	message, _ := r.ctx.CreateMessage(r.scope, errorValue)
	text, _ := message.Text(r.ctx)
	resource, _ := message.ResourceName(r.ctx)
	sourceLine, _, _ := message.SourceLine(r.ctx)
	lineNumber, _, _ := message.LineNumber(r.ctx)
	startColumn, _ := message.StartColumn()
	endColumn, _ := message.EndColumn()
	messageTrace, messageTraceOK, messageTraceErr := message.StackTrace()
	messageFrames := traceFrames(t, messageTrace, messageTraceOK, messageTraceErr)
	value := struct {
		Text            string `json:"text"`
		Resource        string `json:"resource"`
		SourceLine      string `json:"source_line"`
		Line            int32  `json:"line"`
		StartColumn     int64  `json:"start_column"`
		EndColumn       int64  `json:"end_column"`
		ExceptionFrames int    `json:"exception_frames"`
		MessageFrames   int    `json:"message_frames"`
	}{text, resource, sourceLine, lineNumber, startColumn, endColumn, exceptionFrames, messageFrames}
	return outcome{"exception-constructors/create-message/scripted_error_reconstruction", value}
}

func checkCurrentStackFallback(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	type value struct {
		Text       string `json:"text"`
		SourceLine string `json:"source_line"`
		Line       int32  `json:"line"`
		Frames     int    `json:"frames"`
	}
	var observed value
	var callbackErr error
	function, err := r.iso.NewFunction(r.scope, r.ctx,
		func(scope *gov8.CallbackScope, arguments gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
			argument, err := arguments.Get(0)
			if err != nil {
				callbackErr = err
				return
			}
			message, err := r.ctx.CreateMessage(scope.Scope(), argument)
			if err != nil {
				callbackErr = err
				return
			}
			observed.Text, callbackErr = message.Text(r.ctx)
			if callbackErr != nil {
				return
			}
			observed.SourceLine, _, callbackErr = message.SourceLine(r.ctx)
			if callbackErr != nil {
				return
			}
			observed.Line, _, callbackErr = message.LineNumber(r.ctx)
			if callbackErr != nil {
				return
			}
			trace, ok, err := message.StackTrace()
			if err != nil {
				callbackErr = err
				return
			}
			if ok {
				observed.Frames, callbackErr = trace.FrameCount()
			}
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	global, _ := r.ctx.GlobalObject(r.scope)
	if ok, err := global.SetByName(r.scope, r.ctx, "nativeMessage", function.Value); err != nil || !ok {
		t.Fatalf("set nativeMessage = %v, %v", ok, err)
	}
	_ = r.evalOrigin(t, "function outer(){ nativeMessage(17); }\nouter();", "callback.js")
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	return outcome{"exception-constructors/create-message/current_stack_fallback", observed}
}

func checkCrossContextGlobal(t *testing.T) outcome {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iso.Close() }()
	first, _ := iso.NewContext()
	firstScope, _ := iso.NewScope()
	errorValue, err := first.NewTypeError(firstScope, "cross-context")
	if err != nil {
		t.Fatal(err)
	}
	globalError, err := gov8.NewGlobal(firstScope, errorValue)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, _ := iso.NewContext()
	defer func() { _ = second.Close() }()
	secondScope, _ := iso.NewScope()
	defer func() { _ = secondScope.Close() }()
	errorValue, err = globalError.ToLocal(secondScope)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = globalError.Close() }()
	isNative, _ := errorValue.IsNativeError()
	toString, _ := errorValue.ToString(second)
	message, _ := second.CreateMessage(secondScope, errorValue)
	messageText, _ := message.Text(second)
	globalObject, _ := second.GlobalObject(secondScope)
	constructorValue, _, _ := globalObject.GetByName(secondScope, second, "TypeError")
	constructorObject, _ := gov8.AsObject(constructorValue)
	instanceOfSecond, _ := errorValue.InstanceOf(secondScope, second, constructorObject, nil)
	value := struct {
		IsNativeError           bool   `json:"is_native_error"`
		ToString                string `json:"to_string"`
		Message                 string `json:"message"`
		InstanceOfSecondContext bool   `json:"instance_of_second_context"`
	}{isNative, toString, messageText, instanceOfSecond}
	return outcome{"exception-constructors/lifecycle/cross_context_global", value}
}
