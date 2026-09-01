//go:build windows && amd64

package trycatchlistenerresidualconformance

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gov8 "github.com/maclof/gov8"
)

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
	p := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-trycatch-listener-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result := map[string]fixtureLine{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		var line fixtureLine
		if json.Unmarshal(s.Bytes(), &line) == nil && line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, fs map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fs[id]
	if !ok || !want.OK {
		t.Fatalf("fixture lacks passing %s", id)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want.Value)
	if string(a) != string(b) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, a, b)
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
	_ = gov8.ReleaseIsolateHostState(r.iso)
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
func run(t *testing.T, r *runtime, source string, tc *gov8.TryCatch) (gov8.Value, bool) {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, tc)
	if err != nil {
		return gov8.Value{}, false
	}
	defer script.Close()
	v, err := script.Run(r.scope, tc)
	return v, err == nil
}
func text(t *testing.T, r *runtime, v gov8.Value) string {
	t.Helper()
	s, err := v.ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func caught(t *testing.T, tc *gov8.TryCatch) bool {
	t.Helper()
	v, err := tc.HasCaught()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func structural(t *testing.T) map[string]any {
	r := newRuntime(t)
	defer r.close(t)
	outer, _ := r.iso.NewTryCatch()
	defer outer.Close()
	middle, _ := r.iso.NewTryCatch()
	inner, _ := r.iso.NewTryCatch()
	_, ran := run(t, r, "const e = new Error('three-level'); e.marker = 'three-level'; throw e", inner)
	excA, _, _ := inner.Exception(r.scope)
	excB, _, _ := inner.Exception(r.scope)
	excSame, _ := excA.StrictEquals(excB)
	msgA, _, _ := inner.Message(r.scope)
	msgB, _, _ := inner.Message(r.scope)
	msgSame, _ := msgA.SameIdentity(msgB)
	stackA, _, _ := inner.StackTrace(r.scope, r.ctx)
	stackB, _, _ := inner.StackTrace(r.scope, r.ctx)
	stackSame, _ := stackA.StrictEquals(stackB)
	wasCaught := caught(t, inner)
	rethrown, _, err := inner.ReThrow(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	undef, _ := rethrown.IsUndefined()
	innerObs := map[string]any{"run_none": !ran, "has_caught_before_rethrow": wasCaught, "exception_repeat_identity": excSame, "message_repeat_identity": msgSame, "stack_repeat_identity": stackSame, "rethrow_is_undefined": undef}
	middleExc, _, _ := middle.Exception(r.scope)
	sameOriginal, _ := middleExc.StrictEquals(excA)
	_, middleMessage, _ := middle.Message(r.scope)
	_, middleStack, _ := middle.StackTrace(r.scope, r.ctx)
	middleBefore := map[string]any{"has_caught": caught(t, middle), "same_exception": sameOriginal, "message_some": middleMessage, "stack_some": middleStack}
	if err := middle.Reset(); err != nil {
		t.Fatal(err)
	}
	_, middleExceptionAfter, _ := middle.Exception(r.scope)
	middleAfter := map[string]any{"has_caught": caught(t, middle), "exception_none": !middleExceptionAfter}
	if err := middle.Close(); err != nil {
		t.Fatal(err)
	}
	_, outerExceptionBefore, _ := outer.Exception(r.scope)
	outerBefore := map[string]any{"has_caught": caught(t, outer), "exception_none": !outerExceptionBefore}
	_, ran = run(t, r, "throw 'outer-reuse'", outer)
	outerExc, _, _ := outer.Exception(r.scope)
	return map[string]any{"inner": innerObs, "middle": map[string]any{"before_reset": middleBefore, "after_reset": middleAfter}, "outer_after_middle_reset": outerBefore, "outer_reuse": map[string]any{"run_none": !ran, "has_caught": caught(t, outer), "exception": text(t, r, outerExc)}}
}

func configuration(t *testing.T) map[string]any {
	r := newRuntime(t)
	defer r.close(t)
	_, _ = r.iso.AddMessageListener(func(*gov8.CallbackMessage, gov8.Value) {})
	outer, _ := r.iso.NewTryCatch()
	defer outer.Close()
	_ = outer.SetVerbose(true)
	_ = outer.SetCaptureMessage(false)
	inner, _ := r.iso.NewTryCatch()
	verbose, _ := inner.IsVerbose()
	_, ran := run(t, r, "throw new Error('inner-default')", inner)
	_, msg, _ := inner.Message(r.scope)
	_, stack, _ := inner.StackTrace(r.scope, r.ctx)
	defaults := map[string]any{"verbose": verbose, "run_none": !ran, "message_some": msg, "stack_some": stack}
	_ = inner.Reset()
	_ = inner.SetVerbose(false)
	_ = inner.SetCaptureMessage(false)
	verbose, _ = inner.IsVerbose()
	_, ran = run(t, r, "throw new Error('inner-no-message')", inner)
	_, msg, _ = inner.Message(r.scope)
	_, stack, _ = inner.StackTrace(r.scope, r.ctx)
	disabled := map[string]any{"verbose": verbose, "run_none": !ran, "message_none": !msg, "stack_none": !stack}
	_ = inner.Reset()
	_ = inner.SetVerbose(true)
	_ = inner.SetCaptureMessage(false)
	verbose, _ = inner.IsVerbose()
	_, ran = run(t, r, "throw new Error('inner-verbose')", inner)
	_, msg, _ = inner.Message(r.scope)
	_, stack, _ = inner.StackTrace(r.scope, r.ctx)
	forced := map[string]any{"verbose": verbose, "run_none": !ran, "message_some": msg, "stack_some": stack}
	_ = inner.Reset()
	_ = inner.Close()
	outerVerbose, _ := outer.IsVerbose()
	outerBefore := map[string]any{"verbose": outerVerbose, "has_caught": caught(t, outer)}
	_, ran = run(t, r, "throw new Error('outer-no-message')", outer)
	_, msg, _ = outer.Message(r.scope)
	_, stack, _ = outer.StackTrace(r.scope, r.ctx)
	return map[string]any{"inner_defaults": defaults, "inner_capture_disabled": disabled, "inner_verbose_forces_message": forced, "outer_before_throw": outerBefore, "outer_after_throw": map[string]any{"run_none": !ran, "has_caught": caught(t, outer), "message_none": !msg, "stack_none": !stack}}
}

func termination(t *testing.T) map[string]any {
	r := newRuntime(t)
	defer r.close(t)
	handle := r.iso.ThreadSafeHandle()
	fn, err := r.iso.NewFunction(r.scope, r.ctx, func(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {
		if err := r.iso.TerminateExecution(); err != nil {
			panic(err)
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	global, _ := r.ctx.GlobalObject(r.scope)
	ok, err := global.SetByName(r.scope, r.ctx, "terminateNow", fn.Value)
	if err != nil || !ok {
		t.Fatal("set terminateNow")
	}
	outer, _ := r.iso.NewTryCatch()
	defer outer.Close()
	middle, _ := r.iso.NewTryCatch()
	inner, _ := r.iso.NewTryCatch()
	_, ran := run(t, r, "terminateNow(); while (true) {}", inner)
	can, _ := inner.CanContinue()
	term, _ := inner.HasTerminated()
	exc, excSome, _ := inner.Exception(r.scope)
	isNull := false
	if excSome {
		isNull, _ = exc.IsNull()
	}
	_, msg, _ := inner.Message(r.scope)
	before := map[string]any{"run_none": !ran, "has_caught": caught(t, inner), "can_continue": can, "has_terminated": term, "exception_none": !excSome, "exception_is_null": isNull, "message_none": !msg}
	cancel := handle.CancelTerminateExecution()
	_ = inner.Reset()
	afterCaught := caught(t, inner)
	afterTerm, _ := inner.HasTerminated()
	value, reused := run(t, r, "6 * 7", inner)
	reuse := int64(0)
	if reused {
		reuse, _, _ = value.IntegerValue(r.ctx)
	}
	_ = inner.Close()
	middleEmpty := !caught(t, middle)
	_ = middle.Close()
	outerEmpty := !caught(t, outer)
	return map[string]any{"nested": map[string]any{"inner": map[string]any{"before_cancel": before, "cancel_ok": cancel, "after_reset_has_caught": afterCaught, "after_reset_terminated": afterTerm, "reuse": reuse}, "middle_empty": middleEmpty}, "outer_empty": outerEmpty}
}

func listenerRecord(t *testing.T, r *runtime, cm *gov8.CallbackMessage, exception gov8.Value) map[string]any {
	m, err := cm.AsMessage()
	if err != nil {
		t.Fatal(err)
	}
	textA, _ := m.TextValue()
	textB, _ := m.TextValue()
	textSame, _ := textA.StrictEquals(textB)
	textValue, _ := textA.ToString(r.ctx)
	sourceA, sourceSome, _ := m.SourceLineValue(r.ctx)
	sourceB, sourceSomeB, _ := m.SourceLineValue(r.ctx)
	sourceSame := sourceSome == sourceSomeB
	sourceText := any(nil)
	if sourceSome {
		sourceSame, _ = sourceA.StrictEquals(sourceB)
		s, _ := sourceA.ToString(r.ctx)
		sourceText = s
	}
	resourceA, resourceSome, _ := m.ResourceNameValue()
	resourceB, resourceSomeB, _ := m.ResourceNameValue()
	resourceSame := resourceSome == resourceSomeB
	resourceText := any(nil)
	resourceString := false
	if resourceSome {
		resourceSame, _ = resourceA.StrictEquals(resourceB)
		resourceString, _ = resourceA.IsString()
		s, _ := resourceA.ToString(r.ctx)
		resourceText = s
	}
	line, lineSome, _ := m.LineNumber(r.ctx)
	var lineValue any
	if lineSome {
		lineValue = line
	}
	start, _ := m.StartPosition()
	end, _ := m.EndPosition()
	startColumn, _ := m.StartColumn()
	endColumn, _ := m.EndColumn()
	wasm, _ := m.WasmFunctionIndex()
	level, _ := m.ErrorLevel()
	shared, _ := m.IsSharedCrossOrigin()
	opaque, _ := m.IsOpaque()
	stack, stackSome, _ := m.StackTrace()
	stackObj := map[string]any{"present": stackSome, "frame_count": nil, "first": nil}
	if stackSome {
		count, _ := stack.FrameCount()
		stackObj["frame_count"] = count
		if count > 0 {
			frame, _ := stack.Frame(0)
			function, _, _ := frame.FunctionName()
			script, _, _ := frame.ScriptName()
			scriptURL, _, _ := frame.ScriptNameOrSourceURL()
			fl, _ := frame.LineNumber()
			col, _ := frame.Column()
			eval, _ := frame.IsEval()
			wasmFrame, _ := frame.IsWasm()
			user, _ := frame.IsUserJavaScript()
			stackObj["first"] = map[string]any{"line": fl, "column": col, "function": function, "script": script, "script_or_url": scriptURL, "is_eval": eval, "is_wasm": wasmFrame, "is_user_js": user}
		}
	}
	exceptionText, _ := cm.ValueText(exception)
	native, _ := exception.IsNativeError()
	number, _ := exception.IsNumber()
	recreated, _ := cm.CreateMessage(r.ctx, exception)
	recreatedSame, _ := m.SameIdentity(recreated)
	return map[string]any{"text": textValue, "text_repeat_identity": textSame, "source_line": sourceText, "source_repeat_identity": sourceSame, "resource": map[string]any{"present": resourceSome, "is_string": resourceString, "text": resourceText, "repeat_identity": resourceSame}, "line": lineValue, "start_position": start, "end_position": end, "start_column": startColumn, "end_column": endColumn, "wasm_function_index": wasm, "error_level": level, "shared": shared, "opaque": opaque, "stack": stackObj, "exception_text": exceptionText, "exception_is_native_error": native, "exception_is_number": number, "recreated_message_identity": recreatedSame}
}

func listenerCase(t *testing.T, source string, compileOnly, capture bool, origin *gov8.Origin, verbose bool) map[string]any {
	r := newRuntime(t)
	defer r.close(t)
	_ = r.iso.SetCaptureStackTraceForUncaughtExceptions(capture, 3)
	records := []any{}
	added, err := r.iso.AddMessageListener(func(m *gov8.CallbackMessage, e gov8.Value) { records = append(records, listenerRecord(t, r, m, e)) })
	if err != nil {
		t.Fatal(err)
	}
	operationNone := false
	if verbose {
		tc, _ := r.iso.NewTryCatch()
		_ = tc.SetVerbose(true)
		_, ran := run(t, r, source, tc)
		operationNone = !ran
		_ = tc.Close()
	} else {
		script, err := r.ctx.CompileUncaughtWithOrigin(r.scope, source, origin)
		if err != nil {
			operationNone = true
		} else {
			defer script.Close()
			if !compileOnly {
				_, err = script.RunUncaught(r.scope)
				operationNone = err != nil
			}
		}
	}
	return map[string]any{"registration_ok": added, "operation_none": operationNone, "records": records}
}

func listenerFidelity(t *testing.T) map[string]any {
	return map[string]any{
		"runtime_with_stack":      listenerCase(t, "function fail(){ throw new TypeError('listener-boom'); }\nfail();", false, true, &gov8.Origin{ResourceName: "listener-rich.js", LineOffset: 4, ColumnOffset: 6, IsSharedCrossOrigin: true, SourceMapURL: "listener-rich.js.map"}, false),
		"primitive_without_stack": listenerCase(t, "throw 17", false, false, nil, false),
		"syntax_compile":          listenerCase(t, "function broken(", true, false, &gov8.Origin{ResourceName: "syntax-listener.js", IsOpaque: true}, false),
		"caught_verbose":          listenerCase(t, "throw new Error('verbose-listener')", false, false, nil, true),
	}
}

func TestTryCatchListenerResidualFixture(t *testing.T) {
	fs := fixtures(t)
	checks := []struct {
		id string
		fn func(*testing.T) map[string]any
	}{
		{"trycatch-listener-residual/structural_nesting_identity", structural},
		{"trycatch-listener-residual/nested_configuration", configuration},
		{"trycatch-listener-residual/nested_termination_recovery", termination},
		{"trycatch-listener-residual/listener_full_message_fidelity", listenerFidelity},
	}
	for _, check := range checks {
		t.Run(check.id, func(t *testing.T) { compare(t, fs, check.id, check.fn(t)) })
	}
}
