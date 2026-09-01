//go:build windows && amd64

package main

import (
	"math"

	gov8 "gov8"
)

type tester interface {
	Helper()
	Fatal(args ...any)
	Fatalf(string, ...any)
}

type checkOutcome struct {
	id    string
	value jsonValue
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t tester) *runtime {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	return &runtime{iso, ctx, scope}
}

func (r *runtime) close(t tester) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Fatalf("Scope.Close: %v", err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Fatalf("Context.Close: %v", err)
	}
	if err := r.iso.Close(); err != nil {
		t.Fatalf("Isolate.Close: %v", err)
	}
}

func newTC(t tester, r *runtime) *gov8.TryCatch {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	return tc
}

func run(t tester, r *runtime, source string, origin *gov8.Origin, tc *gov8.TryCatch) (gov8.Value, bool) {
	t.Helper()
	var script *gov8.Script
	var err error
	if origin == nil {
		script, err = r.ctx.Compile(r.scope, source, tc)
	} else {
		script, err = r.ctx.CompileWithOrigin(r.scope, source, origin, tc)
	}
	if err != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(r.scope, tc)
	return v, err == nil
}

func valueText(t tester, r *runtime, v gov8.Value) string {
	t.Helper()
	s, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return s
}

func checkEmptyToggleAndReset(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer func() { _ = tc.Close() }()
	hasCaught, _ := tc.HasCaught()
	canContinue, _ := tc.CanContinue()
	hasTerminated, _ := tc.HasTerminated()
	isVerbose, _ := tc.IsVerbose()
	_, exceptionSome, _ := tc.Exception(r.scope)
	_, messageSome, _ := tc.Message(r.scope)
	_, stackSome, _ := tc.StackTrace(r.scope, r.ctx)
	_, rethrowSome, _ := tc.Rethrow(r.scope)
	initial := jobj(
		kv("has_caught", jbool(hasCaught)),
		kv("can_continue", jbool(canContinue)),
		kv("has_terminated", jbool(hasTerminated)),
		kv("is_verbose", jbool(isVerbose)),
		kv("exception_none", jbool(!exceptionSome)),
		kv("message_none", jbool(!messageSome)),
		kv("stack_trace_none", jbool(!stackSome)),
		kv("rethrow_none", jbool(!rethrowSome)),
	)
	_ = tc.SetVerbose(true)
	verboseTrue, _ := tc.IsVerbose()
	_ = tc.SetVerbose(false)
	verboseFalse, _ := tc.IsVerbose()
	v, ran := run(t, r, "40 + 2", nil, tc)
	var successful int64 = -1
	if ran {
		successful, _, _ = v.IntegerValue(r.ctx)
	}
	_, _ = run(t, r, "throw 'reset-me'", nil, tc)
	beforeCaught, _ := tc.HasCaught()
	beforeExc, beforeSome, _ := tc.Exception(r.scope)
	beforeText := ""
	if beforeSome {
		beforeText = valueText(t, r, beforeExc)
	}
	_ = tc.Reset()
	afterCaught, _ := tc.HasCaught()
	_, afterException, _ := tc.Exception(r.scope)
	_, afterMessage, _ := tc.Message(r.scope)
	_, afterStack, _ := tc.StackTrace(r.scope, r.ctx)
	return checkOutcome{"exceptions-advanced/try-catch/empty_toggle_and_reset", jobj(
		kv("initial", initial),
		kv("verbose_true", jbool(verboseTrue)),
		kv("verbose_false", jbool(verboseFalse)),
		kv("successful_run", jint(successful)),
		kv("before_reset", jobj(kv("has_caught", jbool(beforeCaught)), kv("exception", jstr(beforeText)))),
		kv("after_reset", jobj(
			kv("has_caught", jbool(afterCaught)),
			kv("exception_none", jbool(!afterException)),
			kv("message_none", jbool(!afterMessage)),
			kv("stack_trace_none", jbool(!afterStack)),
		)),
	)}
}

func checkVerboseReporting(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	count := 0
	reported := ""
	_, err := r.iso.AddMessageListener(func(msg *gov8.CallbackMessage, _ gov8.Value) {
		count++
		reported, _ = msg.Text()
	})
	if err != nil {
		t.Fatalf("AddMessageListener: %v", err)
	}
	quiet := newTC(t, r)
	_, _ = run(t, r, "throw new Error('quiet')", nil, quiet)
	_ = quiet.Close()
	quietCount := count
	verbose := newTC(t, r)
	_ = verbose.SetVerbose(true)
	_, _ = run(t, r, "throw new Error('reported')", nil, verbose)
	_ = verbose.Close()
	return checkOutcome{"exceptions-advanced/try-catch/verbose_reporting", jobj(
		kv("quiet_listener_count", jint(int64(quietCount))),
		kv("verbose_listener_count", jint(int64(count))),
		kv("reported_text", jstr(reported)),
	)}
}

func checkRuntimeExceptionDetails(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer func() { _ = tc.Close() }()
	origin := &gov8.Origin{ResourceName: "runtime.js", SourceMapURL: "runtime.js.map"}
	source := "function outer() {\n  function inner() { null.value; }\n  inner();\n}\nouter();"
	_, ran := run(t, r, source, origin, tc)
	exc, excSome, _ := tc.Exception(r.scope)
	msg, msgSome, _ := tc.Message(r.scope)
	stack, stackSome, _ := tc.StackTrace(r.scope, r.ctx)
	hasCaught, _ := tc.HasCaught()
	canContinue, _ := tc.CanContinue()
	hasTerminated, _ := tc.HasTerminated()
	native := false
	if excSome {
		native, _ = exc.IsNativeError()
	}
	exceptionText := ""
	if excSome {
		exceptionText = valueText(t, r, exc)
	}
	stackText, stackOK := "", false
	if stackSome {
		stackText, stackOK = valueText(t, r, stack), true
	}
	messageText, resource, sourceLine := "", "", ""
	resourceOK, sourceOK := false, false
	var line int32
	lineOK := false
	startColumn, endColumn, wasmIndex := int64(0), int64(0), int64(0)
	messageStackNone := true
	if msgSome {
		messageText, _ = msg.Text(r.ctx)
		resource, _ = msg.ResourceName(r.ctx)
		resourceOK = resource != ""
		sourceLine, sourceOK, _ = msg.SourceLine(r.ctx)
		line, lineOK, _ = msg.LineNumber(r.ctx)
		startColumn, _ = msg.StartColumn()
		endColumn, _ = msg.EndColumn()
		wasmIndex, _ = msg.WasmFunctionIndex()
		_, messageStackSome, _ := msg.StackTrace()
		messageStackNone = !messageStackSome
	}
	return checkOutcome{"exceptions-advanced/try-catch/runtime_exception_details", jobj(
		kv("run_none", jbool(!ran)),
		kv("has_caught", jbool(hasCaught)),
		kv("can_continue", jbool(canContinue)),
		kv("has_terminated", jbool(hasTerminated)),
		kv("exception_is_error", jbool(native)),
		kv("exception", jstr(exceptionText)),
		kv("try_catch_stack", jopt(stackText, stackOK)),
		kv("message", jstr(messageText)),
		kv("resource_name", jopt(resource, resourceOK)),
		kv("source_line", jopt(sourceLine, sourceOK)),
		kv("line_number", func() jsonValue {
			if lineOK {
				return jint(int64(line))
			}
			return jnull()
		}()),
		kv("start_column", jint(startColumn)),
		kv("end_column", jint(endColumn)),
		kv("wasm_function_index", jint(wasmIndex)),
		kv("message_stack_none", jbool(messageStackNone)),
	)}
}

func checkSyntaxExceptionDetails(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer func() { _ = tc.Close() }()
	origin := &gov8.Origin{ResourceName: "syntax.js"}
	_, compiled := run(t, r, "const answer = ;", origin, tc)
	exc, _, _ := tc.Exception(r.scope)
	stack, stackSome, _ := tc.StackTrace(r.scope, r.ctx)
	msg, _, _ := tc.Message(r.scope)
	hasCaught, _ := tc.HasCaught()
	stackText, stackOK := "", false
	if stackSome {
		stackText, stackOK = valueText(t, r, stack), true
	}
	messageText, _ := msg.Text(r.ctx)
	resource, _ := msg.ResourceName(r.ctx)
	sourceLine, sourceOK, _ := msg.SourceLine(r.ctx)
	line, lineOK, _ := msg.LineNumber(r.ctx)
	startPos, _ := msg.StartPosition()
	endPos, _ := msg.EndPosition()
	startColumn, _ := msg.StartColumn()
	endColumn, _ := msg.EndColumn()
	wasmIndex, _ := msg.WasmFunctionIndex()
	return checkOutcome{"exceptions-advanced/try-catch/syntax_exception_details", jobj(
		kv("compile_none", jbool(!compiled)),
		kv("has_caught", jbool(hasCaught)),
		kv("exception", jstr(valueText(t, r, exc))),
		kv("stack_trace", jopt(stackText, stackOK)),
		kv("message", jstr(messageText)),
		kv("resource_name", jopt(resource, resource != "")),
		kv("source_line", jopt(sourceLine, sourceOK)),
		kv("line_number", func() jsonValue {
			if lineOK {
				return jint(int64(line))
			}
			return jnull()
		}()),
		kv("start_position", jint(startPos)),
		kv("end_position", jint(endPos)),
		kv("start_column", jint(startColumn)),
		kv("end_column", jint(endColumn)),
		kv("wasm_function_index", jint(wasmIndex)),
	)}
}

func checkCaptureMessageDisabled(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer func() { _ = tc.Close() }()
	_ = tc.SetCaptureMessage(false)
	_, ran := run(t, r, "function f(){ throw 17; } f();", nil, tc)
	hasCaught, _ := tc.HasCaught()
	exc, _, _ := tc.Exception(r.scope)
	_, msgSome, _ := tc.Message(r.scope)
	_, stackSome, _ := tc.StackTrace(r.scope, r.ctx)
	return checkOutcome{"exceptions-advanced/try-catch/capture_message_disabled", jobj(
		kv("run_none", jbool(!ran)),
		kv("has_caught", jbool(hasCaught)),
		kv("exception", jstr(valueText(t, r, exc))),
		kv("message_none", jbool(!msgSome)),
		kv("stack_trace_none", jbool(!stackSome)),
	)}
}

func checkRethrowPropagation(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	outer := newTC(t, r)
	defer func() { _ = outer.Close() }()
	inner := newTC(t, r)
	_, _ = run(t, r, "throw ({marker: 'same-object'})", nil, inner)
	before, _, _ := inner.Exception(r.scope)
	returned, returnedSome, _ := inner.Rethrow(r.scope)
	same, _ := before.StrictEquals(returned)
	returnedText := valueText(t, r, returned)
	returnedUndefined, _ := returned.IsUndefined()
	_ = inner.Reset()
	innerCaught, _ := inner.HasCaught()
	_, innerException, _ := inner.Exception(r.scope)
	_ = inner.Close()
	outerCaught, _ := outer.HasCaught()
	outerExc, _, _ := outer.Exception(r.scope)
	obj, _ := outerExc.ToObject(r.scope, r.ctx, outer)
	marker, _, _ := obj.GetByName(r.scope, r.ctx, "marker")
	return checkOutcome{"exceptions-advanced/try-catch/rethrow_propagation", jobj(
		kv("inner_rethrow", jobj(
			kv("returned_value", jbool(returnedSome)),
			kv("same_value", jbool(same)),
			kv("returned_text", jstr(returnedText)),
			kv("returned_is_undefined", jbool(returnedUndefined)),
		)),
		kv("inner_after_reset", jobj(
			kv("has_caught", jbool(innerCaught)),
			kv("exception_some", jbool(innerException)),
		)),
		kv("outer_has_caught", jbool(outerCaught)),
		kv("outer_marker", jstr(valueText(t, r, marker))),
	)}
}

func checkCaughtLocalLifetime(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	_, _ = run(t, r, "throw new TypeError('local-lifetime')", nil, tc)
	exc, _, _ := tc.Exception(r.scope)
	msg, _, _ := tc.Message(r.scope)
	_ = tc.Close()
	messageText, _ := msg.Text(r.ctx)
	return checkOutcome{"exceptions-advanced/try-catch/caught_local_lifetime", jobj(
		kv("exception", jstr(valueText(t, r, exc))),
		kv("message", jstr(messageText)),
	)}
}

func checkSourceURLFallback(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer func() { _ = tc.Close() }()
	source := "function named(){ throw new Error('url-only'); }\nnamed();\n//# sourceURL=fallback.js"
	_, _ = run(t, r, source, nil, tc)
	msg, _, _ := tc.Message(r.scope)
	resource, _ := msg.ResourceName(r.ctx)
	exc, _, _ := tc.Exception(r.scope)
	trace, traceSome, _ := gov8.ExceptionStackTrace(r.scope, exc)
	name, nameOK := "", false
	nameOrURL, nameOrURLOK := "", false
	if traceSome {
		frame, err := trace.Frame(0)
		if err == nil {
			name, nameOK, _ = frame.ScriptName()
			nameOrURL, nameOrURLOK, _ = frame.ScriptNameOrSourceURL()
		}
	}
	return checkOutcome{"exceptions-advanced/message/source_url_fallback", jobj(
		kv("resource_name", jopt(resource, resource != "")),
		kv("frame_script_name", jopt(name, nameOK)),
		kv("frame_script_name_or_source_url", jopt(nameOrURL, nameOrURLOK)),
	)}
}

type stackCapture struct {
	value jsonValue
	err   error
}

func stackCallbackCapture(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments) stackCapture {
	arg, err := args.Get(0)
	if err != nil {
		return stackCapture{err: err}
	}
	label, err := cs.ToString(arg)
	if err != nil {
		return stackCapture{err: err}
	}
	countFor := func(limit int) (int64, bool, error) {
		st, ok, err := cs.Scope().CurrentStackTrace(limit)
		if err != nil || !ok {
			return 0, false, err
		}
		count, err := st.FrameCount()
		return int64(count), err == nil, err
	}
	zero, zeroOK, err := countFor(0)
	if err != nil {
		return stackCapture{err: err}
	}
	one, oneOK, err := countFor(1)
	if err != nil {
		return stackCapture{err: err}
	}
	two, twoOK, err := countFor(2)
	if err != nil {
		return stackCapture{err: err}
	}
	_, overflowSome, err := cs.Scope().CurrentStackTrace(math.MaxInt)
	if err != nil {
		return stackCapture{err: err}
	}
	currentName, currentNameOK, err := cs.Scope().CurrentScriptNameOrSourceURL()
	if err != nil {
		return stackCapture{err: err}
	}
	trace, ok, err := cs.Scope().CurrentStackTrace(16)
	if err != nil || !ok {
		return stackCapture{err: err}
	}
	count, err := trace.FrameCount()
	if err != nil {
		return stackCapture{err: err}
	}
	_, equalCountErr := trace.Frame(count)
	frames := make([]jsonValue, 0, count)
	var firstID int64
	for index := 0; index < count; index++ {
		frame, err := trace.Frame(index)
		if err != nil {
			return stackCapture{err: err}
		}
		functionName, functionOK, err := frame.FunctionName()
		if err != nil {
			return stackCapture{err: err}
		}
		isWasm, err := frame.IsWasm()
		if err != nil {
			return stackCapture{err: err}
		}
		scriptName, scriptOK, err := frame.ScriptName()
		if err != nil {
			return stackCapture{err: err}
		}
		scriptNameOrURL, scriptNameOrURLOK, err := frame.ScriptNameOrSourceURL()
		if err != nil {
			return stackCapture{err: err}
		}
		if isWasm {
			scriptName, scriptOK = "<wasm-url>", true
			scriptNameOrURL, scriptNameOrURLOK = "<wasm-url>", true
		}
		sourceMap, sourceMapOK, err := frame.SourceMappingURL()
		if err != nil {
			return stackCapture{err: err}
		}
		line, _ := frame.LineNumber()
		column, _ := frame.Column()
		scriptID, _ := frame.ScriptID()
		if index == 0 {
			firstID = scriptID
		}
		isEval, _ := frame.IsEval()
		isConstructor, _ := frame.IsConstructor()
		isUserJS, _ := frame.IsUserJavaScript()
		frames = append(frames, jobj(
			kv("function_name", jopt(functionName, functionOK)),
			kv("script_name", jopt(scriptName, scriptOK)),
			kv("script_name_or_source_url", jopt(scriptNameOrURL, scriptNameOrURLOK)),
			kv("source_map_url", jopt(sourceMap, sourceMapOK)),
			kv("line", jint(line)),
			kv("column", jint(column)),
			kv("script_id_positive", jbool(scriptID > 0)),
			kv("same_script_as_first", jbool(scriptID == firstID)),
			kv("is_eval", jbool(isEval)),
			kv("is_constructor", jbool(isConstructor)),
			kv("is_wasm", jbool(isWasm)),
			kv("is_user_javascript", jbool(isUserJS)),
		))
	}
	optCount := func(v int64, ok bool) jsonValue {
		if ok {
			return jint(v)
		}
		return jnull()
	}
	return stackCapture{value: jobj(
		kv("label", jstr(label)),
		kv("limit_zero", optCount(zero, zeroOK)),
		kv("limit_one", optCount(one, oneOK)),
		kv("limit_two", optCount(two, twoOK)),
		kv("overflow_none", jbool(!overflowSome)),
		kv("frame_count", jint(int64(count))),
		kv("index_equal_count_some", jbool(equalCountErr == nil)),
		kv("current_script_name", jopt(currentName, currentNameOK)),
		kv("frames", jarr(frames...)),
	)}
}

func checkCurrentFramesAndLimits(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	captures := make([]jsonValue, 0, 4)
	var callbackErr error
	host, err := r.iso.NewFunction(r.scope, r.ctx,
		func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
			capture := stackCallbackCapture(cs, args)
			if capture.err != nil {
				callbackErr = capture.err
				return
			}
			captures = append(captures, capture.value)
		}, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, "host", host.Value); err != nil || !ok {
		t.Fatalf("set host = %v, %v", ok, err)
	}
	origin := &gov8.Origin{ResourceName: "stack.js", SourceMapURL: "stack.js.map"}
	source := `function HostCtor(){ host('constructor'); }
eval("function evalCaller(){ host('eval'); }\n//# sourceURL=eval-source.js");
function normal(){ host('normal'); }
new HostCtor();
evalCaller();
normal();
const wasmBytes = new Uint8Array([0,97,115,109,1,0,0,0,1,4,1,96,0,0,2,12,1,3,101,110,118,4,104,111,115,116,0,0,3,2,1,0,7,5,1,1,102,0,1,10,6,1,4,0,16,0,11]);
new WebAssembly.Instance(new WebAssembly.Module(wasmBytes), {env:{host(){ host('wasm'); }}}).exports.f();
//# sourceMappingURL=stack.js.map`
	_, ran := run(t, r, source, origin, nil)
	if callbackErr != nil {
		t.Fatalf("stack callback: %v", callbackErr)
	}
	return checkOutcome{"exceptions-advanced/stack/current_frames_and_limits", jobj(
		kv("run_succeeded", jbool(ran)),
		kv("captures", jarr(captures...)),
	)}
}

func checkWasmTrap(t tester) checkOutcome {
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer func() { _ = tc.Close() }()
	source := `const b = new Uint8Array([0,97,115,109,1,0,0,0,1,4,1,96,0,0,3,2,1,0,7,5,1,1,102,0,0,10,5,1,3,0,0,11]);
new WebAssembly.Instance(new WebAssembly.Module(b)).exports.f();`
	_, ran := run(t, r, source, nil, tc)
	msg, _, _ := tc.Message(r.scope)
	messageText, _ := msg.Text(r.ctx)
	wasmIndex, _ := msg.WasmFunctionIndex()
	exc, _, _ := tc.Exception(r.scope)
	_, traceSome, _ := gov8.ExceptionStackTrace(r.scope, exc)
	return checkOutcome{"exceptions-advanced/message/wasm_trap", jobj(
		kv("run_none", jbool(!ran)),
		kv("message", jstr(messageText)),
		kv("wasm_function_index", jint(wasmIndex)),
		kv("exception_stack_trace_none", jbool(!traceSome)),
	)}
}

var checks = []func(tester) checkOutcome{
	checkEmptyToggleAndReset,
	checkVerboseReporting,
	checkRuntimeExceptionDetails,
	checkSyntaxExceptionDetails,
	checkCaptureMessageDisabled,
	checkRethrowPropagation,
	checkCaughtLocalLifetime,
	checkSourceURLFallback,
	checkCurrentFramesAndLimits,
	checkWasmTrap,
}
