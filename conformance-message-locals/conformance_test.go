//go:build windows && amd64

package messagelocalsconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-message-locals-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type runtimeState struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t *testing.T) *runtimeState {
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
	return &runtimeState{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtimeState) close(t *testing.T) {
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

func strptr(value string) *string { return &value }
func boolptr(value bool) *bool    { return &value }

type stringObservation struct {
	Present     bool    `json:"present"`
	IsString    bool    `json:"is_string"`
	Text        *string `json:"text"`
	RepeatEqual bool    `json:"repeat_equal"`
}

type valueObservation struct {
	Present     bool    `json:"present"`
	IsString    bool    `json:"is_string"`
	IsNumber    bool    `json:"is_number"`
	IsUndefined bool    `json:"is_undefined"`
	IsObject    bool    `json:"is_object"`
	Text        *string `json:"text"`
	RepeatEqual bool    `json:"repeat_equal"`
	SameOrigin  *bool   `json:"same_as_origin"`
}

func observeString(get func() (gov8.Value, bool, error)) (stringObservation, error) {
	first, firstOK, err := get()
	if err != nil {
		return stringObservation{}, err
	}
	second, secondOK, err := get()
	if err != nil {
		return stringObservation{}, err
	}
	result := stringObservation{Present: firstOK, RepeatEqual: !firstOK && !secondOK}
	if !firstOK {
		return result, nil
	}
	result.IsString, err = first.IsString()
	if err != nil {
		return stringObservation{}, err
	}
	text, err := first.StringValue()
	if err != nil {
		return stringObservation{}, err
	}
	result.Text = strptr(text)
	if secondOK {
		result.RepeatEqual, err = first.StrictEquals(second)
	}
	return result, err
}

func observeMessageText(message *gov8.Message) (stringObservation, error) {
	return observeString(func() (gov8.Value, bool, error) {
		value, err := message.TextValue()
		return value, err == nil, err
	})
}

func observeValue(ctx *gov8.Context, original *gov8.Value,
	get func() (gov8.Value, bool, error)) (valueObservation, error) {
	first, firstOK, err := get()
	if err != nil {
		return valueObservation{}, err
	}
	second, secondOK, err := get()
	if err != nil {
		return valueObservation{}, err
	}
	result := valueObservation{Present: firstOK, RepeatEqual: !firstOK && !secondOK}
	if !firstOK {
		return result, nil
	}
	if result.IsString, err = first.IsString(); err != nil {
		return valueObservation{}, err
	}
	if result.IsNumber, err = first.IsNumber(); err != nil {
		return valueObservation{}, err
	}
	if result.IsUndefined, err = first.IsUndefined(); err != nil {
		return valueObservation{}, err
	}
	if result.IsObject, err = first.IsObject(); err != nil {
		return valueObservation{}, err
	}
	text, err := first.ToString(ctx)
	if err != nil {
		return valueObservation{}, err
	}
	result.Text = strptr(text)
	if secondOK {
		result.RepeatEqual, err = first.StrictEquals(second)
		if err != nil {
			return valueObservation{}, err
		}
	}
	if original != nil {
		same, err := first.StrictEquals(*original)
		if err != nil {
			return valueObservation{}, err
		}
		result.SameOrigin = boolptr(same)
	}
	return result, nil
}

type messageCase struct {
	Case       string            `json:"case"`
	RunNone    *bool             `json:"run_none,omitempty"`
	Message    stringObservation `json:"message"`
	SourceLine stringObservation `json:"source_line"`
	Resource   valueObservation  `json:"resource"`
	Line       int64             `json:"line"`
	Shared     bool              `json:"shared"`
	Opaque     bool              `json:"opaque"`
}

func caughtMessageCase(t *testing.T, r *runtimeState, label, source string,
	resource *gov8.Value, sourceMap string, shared, opaque bool) messageCase {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tc.Close() }()

	var script *gov8.Script
	if resource == nil {
		script, err = r.ctx.Compile(r.scope, source, tc)
	} else {
		script, err = r.ctx.CompileWithOrigin(r.scope, source, &gov8.Origin{
			ResourceNameValue:   *resource,
			SourceMapURL:        sourceMap,
			IsSharedCrossOrigin: shared,
			IsOpaque:            opaque,
		}, tc)
	}
	if err != nil {
		t.Fatalf("%s compile: %v", label, err)
	}
	defer func() { _ = script.Close() }()
	_, runErr := script.Run(r.scope, tc)
	runNone := runErr != nil
	message, ok, err := tc.Message(r.scope)
	if err != nil || !ok {
		t.Fatalf("%s Message = %v, %v", label, ok, err)
	}
	messageObs, err := observeMessageText(message)
	if err != nil {
		t.Fatal(err)
	}
	sourceObs, err := observeString(func() (gov8.Value, bool, error) {
		return message.SourceLineValue(r.ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceObs, err := observeValue(r.ctx, resource, message.ResourceNameValue)
	if err != nil {
		t.Fatal(err)
	}
	line, lineOK, err := message.LineNumber(r.ctx)
	if err != nil || !lineOK {
		t.Fatalf("%s line = %d, %v, %v", label, line, lineOK, err)
	}
	isShared, err := message.IsSharedCrossOrigin()
	if err != nil {
		t.Fatal(err)
	}
	isOpaque, err := message.IsOpaque()
	if err != nil {
		t.Fatal(err)
	}
	return messageCase{
		Case: label, RunNone: boolptr(runNone), Message: messageObs,
		SourceLine: sourceObs, Resource: resourceObs, Line: int64(line),
		Shared: isShared, Opaque: isOpaque,
	}
}

func checkMessageMatrix(t *testing.T) any {
	r := newRuntime(t)
	defer r.close(t)
	stringResource, _ := r.scope.NewString("normal.js")
	emptyResource, _ := r.scope.EmptyString()
	numberResource, _ := r.scope.Int32(42)
	undefinedResource, _ := r.scope.Undefined()
	object, _ := r.scope.NewObject(r.ctx)
	mapValue := "normal.js.map"
	source := "function fail(){ null.member; }\nfail();"
	cases := []messageCase{
		caughtMessageCase(t, r, "string", source, &stringResource, mapValue, false, false),
		caughtMessageCase(t, r, "empty", source, &emptyResource, "", false, false),
		caughtMessageCase(t, r, "number", source, &numberResource, "", false, false),
		caughtMessageCase(t, r, "undefined", source, &undefinedResource, "", false, false),
		caughtMessageCase(t, r, "object", source, &object.Value, "", false, false),
		caughtMessageCase(t, r, "absent", source, nil, "", false, false),
		caughtMessageCase(t, r, "source_url",
			"function fail(){ null.member; }\nfail();\n//# sourceURL=fallback.js",
			nil, "", false, false),
		caughtMessageCase(t, r, "eval_source_url",
			"eval(\"function fail(){ null.member; }\\nfail();\\n//# sourceURL=eval-message.js\");",
			nil, "", false, false),
	}
	primitive, _ := r.scope.Int32(17)
	message, err := r.ctx.CreateMessage(r.scope, primitive)
	if err != nil {
		t.Fatal(err)
	}
	messageObs, _ := observeMessageText(message)
	sourceObs, _ := observeString(func() (gov8.Value, bool, error) {
		return message.SourceLineValue(r.ctx)
	})
	resourceObs, _ := observeValue(r.ctx, nil, message.ResourceNameValue)
	line, lineOK, err := message.LineNumber(r.ctx)
	if err != nil || !lineOK {
		t.Fatalf("primitive line = %d, %v, %v", line, lineOK, err)
	}
	shared, _ := message.IsSharedCrossOrigin()
	opaque, _ := message.IsOpaque()
	cases = append(cases, messageCase{
		Case: "primitive_created", Message: messageObs, SourceLine: sourceObs,
		Resource: resourceObs, Line: int64(line), Shared: shared, Opaque: opaque,
	})
	return cases
}

func checkOriginFlags(t *testing.T) any {
	r := newRuntime(t)
	defer r.close(t)
	var cases []messageCase
	for _, test := range []struct {
		label          string
		shared, opaque bool
	}{
		{"false_true", false, true},
		{"true_true", true, true},
		{"true_false", true, false},
	} {
		resource, _ := r.scope.NewString(test.label)
		cases = append(cases, caughtMessageCase(t, r, test.label,
			"\n\nthrow new Error('flags');", &resource, "", test.shared, test.opaque))
	}
	return cases
}

type frameCase struct {
	Case               string            `json:"case"`
	CurrentNameOrURL   stringObservation `json:"current_name_or_url"`
	FrameCountPositive bool              `json:"frame_count_positive"`
	Line               int64             `json:"line"`
	Column             int64             `json:"column"`
	ScriptIDPositive   bool              `json:"script_id_positive"`
	ScriptName         stringObservation `json:"script_name"`
	ScriptNameOrURL    stringObservation `json:"script_name_or_url"`
	ScriptSource       stringObservation `json:"script_source"`
	SourceMapURL       stringObservation `json:"source_map_url"`
	FunctionName       stringObservation `json:"function_name"`
	IsEval             bool              `json:"is_eval"`
	IsConstructor      bool              `json:"is_constructor"`
	IsWasm             bool              `json:"is_wasm"`
	IsUserJavaScript   bool              `json:"is_user_javascript"`
}

func captureFrame(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments) (frameCase, error) {
	label := "wasm"
	if args.Length() > 0 {
		arg, err := args.Get(0)
		if err != nil {
			return frameCase{}, err
		}
		label, err = cs.ToString(arg)
		if err != nil {
			return frameCase{}, err
		}
	}
	current, err := observeString(cs.Scope().CurrentScriptNameOrSourceURLValue)
	if err != nil {
		return frameCase{}, err
	}
	trace, ok, err := cs.Scope().CurrentStackTrace(8)
	if err != nil || !ok {
		return frameCase{}, fmt.Errorf("CurrentStackTrace = %v, %v", ok, err)
	}
	count, err := trace.FrameCount()
	if err != nil || count == 0 {
		return frameCase{}, fmt.Errorf("FrameCount = %d, %v", count, err)
	}
	frame, err := trace.Frame(0)
	if err != nil {
		return frameCase{}, err
	}
	observe := func(get func() (gov8.Value, bool, error)) stringObservation {
		value, obsErr := observeString(get)
		if obsErr != nil && err == nil {
			err = obsErr
		}
		return value
	}
	line, lineErr := frame.LineNumber()
	column, columnErr := frame.Column()
	scriptID, scriptIDErr := frame.ScriptID()
	isEval, evalErr := frame.IsEval()
	isConstructor, constructorErr := frame.IsConstructor()
	isWasm, wasmErr := frame.IsWasm()
	isUserJS, userErr := frame.IsUserJavaScript()
	for _, candidate := range []error{lineErr, columnErr, scriptIDErr, evalErr, constructorErr, wasmErr, userErr} {
		if candidate != nil {
			return frameCase{}, candidate
		}
	}
	result := frameCase{
		Case: label, CurrentNameOrURL: current, FrameCountPositive: count > 0,
		Line: line, Column: column, ScriptIDPositive: scriptID > 0,
		ScriptName:      observe(frame.ScriptNameValue),
		ScriptNameOrURL: observe(frame.ScriptNameOrSourceURLValue),
		ScriptSource:    observe(frame.ScriptSourceValue),
		SourceMapURL:    observe(frame.SourceMappingURLValue),
		FunctionName:    observe(frame.FunctionNameValue),
		IsEval:          isEval, IsConstructor: isConstructor, IsWasm: isWasm,
		IsUserJavaScript: isUserJS,
	}
	return result, err
}

func runStackSource(t *testing.T, r *runtimeState, source, resource, sourceMap string) {
	t.Helper()
	var script *gov8.Script
	var err error
	if resource == "" {
		script, err = r.ctx.Compile(r.scope, source, nil)
	} else {
		script, err = r.ctx.CompileWithOrigin(r.scope, source, &gov8.Origin{
			ResourceName: resource, SourceMapURL: sourceMap,
		}, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	if _, err := script.Run(r.scope, nil); err != nil {
		t.Fatal(err)
	}
}

func checkStackFrames(t *testing.T) any {
	r := newRuntime(t)
	defer r.close(t)
	var captures []frameCase
	var callbackErr error
	probe, err := r.iso.NewFunction(r.scope, r.ctx,
		func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
			capture, err := captureFrame(cs, args)
			if err != nil {
				callbackErr = err
				return
			}
			captures = append(captures, capture)
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	global, _ := r.ctx.GlobalObject(r.scope)
	if ok, err := global.SetByName(r.scope, r.ctx, "probe", probe.Value); err != nil || !ok {
		t.Fatalf("set probe = %v, %v", ok, err)
	}
	named := "function named(){ probe('named'); }\nnamed();\n(function(){ probe('anonymous'); })();\n//# sourceMappingURL=named.map"
	runStackSource(t, r, named, "named.js", "named.map")
	evalSource := "eval(\"function evalNamed(){ probe('eval_source_url'); }\\nevalNamed();\\n//# sourceURL=eval-local.js\\n//# sourceMappingURL=eval-local.map\");"
	runStackSource(t, r, evalSource, "eval-host.js", "")
	runStackSource(t, r, "function sourced(){ probe('source_url_only'); }\nsourced();\n//# sourceURL=source-only.js", "", "")
	runStackSource(t, r, "(function(){ probe('anonymous_no_origin'); })();", "", "")
	wasm := "const bytes=new Uint8Array([0,97,115,109,1,0,0,0,1,4,1,96,0,0,2,12,1,3,101,110,118,4,104,111,115,116,0,0,3,2,1,0,7,5,1,1,102,0,1,10,6,1,4,0,16,0,11]);new WebAssembly.Instance(new WebAssembly.Module(bytes),{env:{host:probe}}).exports.f();"
	runStackSource(t, r, wasm, "wasm-host.js", "")
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	return captures
}

type overwriteObservation struct {
	FirstNone           bool   `json:"first_none"`
	FirstException      string `json:"first_exception"`
	FirstMessage        string `json:"first_message"`
	SecondNone          bool   `json:"second_none"`
	HasCaught           bool   `json:"has_caught"`
	SecondException     string `json:"second_exception"`
	SecondMessage       string `json:"second_message"`
	ExceptionChanged    bool   `json:"exception_changed"`
	MessageChanged      bool   `json:"message_changed"`
	FirstExceptionAfter string `json:"first_exception_after"`
	FirstMessageAfter   string `json:"first_message_after"`
}

type resetObservation struct {
	HasCaught     bool `json:"has_caught"`
	ExceptionNone bool `json:"exception_none"`
	MessageNone   bool `json:"message_none"`
}

type disabledObservation struct {
	RunNone     bool   `json:"run_none"`
	HasCaught   bool   `json:"has_caught"`
	Exception   string `json:"exception"`
	MessageNone bool   `json:"message_none"`
}

type enabledObservation struct {
	RunNone     bool   `json:"run_none"`
	HasCaught   bool   `json:"has_caught"`
	MessageSome bool   `json:"message_some"`
	Message     string `json:"message"`
}

type mutationObservation struct {
	Overwrite       overwriteObservation `json:"overwrite_without_reset"`
	FirstReset      resetObservation     `json:"first_reset"`
	CaptureDisabled disabledObservation  `json:"capture_disabled"`
	DisabledReset   resetObservation     `json:"disabled_reset"`
	ResetReenabled  enabledObservation   `json:"reset_reenabled"`
}

func runThrow(t *testing.T, r *runtimeState, tc *gov8.TryCatch, source string) bool {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	_, err = script.Run(r.scope, tc)
	return err != nil
}

func resetState(t *testing.T, tc *gov8.TryCatch, scope *gov8.Scope) resetObservation {
	t.Helper()
	hasCaught, err := tc.HasCaught()
	if err != nil {
		t.Fatal(err)
	}
	_, exceptionOK, err := tc.Exception(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, messageOK, err := tc.Message(scope)
	if err != nil {
		t.Fatal(err)
	}
	return resetObservation{HasCaught: hasCaught, ExceptionNone: !exceptionOK, MessageNone: !messageOK}
}

func checkMutation(t *testing.T) any {
	r := newRuntime(t)
	defer r.close(t)
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tc.Close() }()

	firstNone := runThrow(t, r, tc, "throw new Error('first')")
	firstException, _, _ := tc.Exception(r.scope)
	firstMessage, _, _ := tc.Message(r.scope)
	firstExceptionText, _ := firstException.ToString(r.ctx)
	firstMessageText, _ := firstMessage.Text(r.ctx)
	firstMessageValue, _ := firstMessage.TextValue()
	secondNone := runThrow(t, r, tc, "throw new TypeError('second')")
	secondException, _, _ := tc.Exception(r.scope)
	secondMessage, _, _ := tc.Message(r.scope)
	secondExceptionText, _ := secondException.ToString(r.ctx)
	secondMessageText, _ := secondMessage.Text(r.ctx)
	secondMessageValue, _ := secondMessage.TextValue()
	hasCaught, _ := tc.HasCaught()
	exceptionSame, _ := firstException.StrictEquals(secondException)
	messageSame, _ := firstMessageValue.StrictEquals(secondMessageValue)
	firstExceptionAfter, _ := firstException.ToString(r.ctx)
	firstMessageAfter, _ := firstMessage.Text(r.ctx)
	overwrite := overwriteObservation{
		FirstNone: firstNone, FirstException: firstExceptionText,
		FirstMessage: firstMessageText, SecondNone: secondNone, HasCaught: hasCaught,
		SecondException: secondExceptionText, SecondMessage: secondMessageText,
		ExceptionChanged: !exceptionSame, MessageChanged: !messageSame,
		FirstExceptionAfter: firstExceptionAfter, FirstMessageAfter: firstMessageAfter,
	}
	if err := tc.Reset(); err != nil {
		t.Fatal(err)
	}
	firstReset := resetState(t, tc, r.scope)
	if err := tc.SetCaptureMessage(false); err != nil {
		t.Fatal(err)
	}
	disabledNone := runThrow(t, r, tc, "throw 31")
	disabledCaught, _ := tc.HasCaught()
	disabledException, _, _ := tc.Exception(r.scope)
	disabledText, _ := disabledException.ToString(r.ctx)
	_, disabledMessageOK, _ := tc.Message(r.scope)
	disabled := disabledObservation{
		RunNone: disabledNone, HasCaught: disabledCaught,
		Exception: disabledText, MessageNone: !disabledMessageOK,
	}
	if err := tc.Reset(); err != nil {
		t.Fatal(err)
	}
	disabledReset := resetState(t, tc, r.scope)
	if err := tc.SetCaptureMessage(true); err != nil {
		t.Fatal(err)
	}
	enabledNone := runThrow(t, r, tc, "throw new Error('enabled')")
	enabledCaught, _ := tc.HasCaught()
	enabledMessage, enabledOK, _ := tc.Message(r.scope)
	enabledText := ""
	if enabledOK {
		enabledText, _ = enabledMessage.Text(r.ctx)
	}
	enabled := enabledObservation{
		RunNone: enabledNone, HasCaught: enabledCaught,
		MessageSome: enabledOK, Message: enabledText,
	}
	return mutationObservation{
		Overwrite: overwrite, FirstReset: firstReset, CaptureDisabled: disabled,
		DisabledReset: disabledReset, ResetReenabled: enabled,
	}
}

type reportLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

func report(t *testing.T) string {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T) any
	}{
		{"message-locals/message_value_matrix", checkMessageMatrix},
		{"message-locals/message_origin_flags", checkOriginFlags},
		{"message-locals/stack_frame_string_getters", checkStackFrames},
		{"message-locals/try_catch_mutation", checkMutation},
	}
	var output strings.Builder
	for _, check := range checks {
		encoded, err := json.Marshal(reportLine{Check: check.name, OK: true, Value: check.run(t)})
		if err != nil {
			t.Fatal(err)
		}
		output.Write(encoded)
		output.WriteByte('\n')
	}
	output.WriteString(`{"summary":{"total":4,"passed":4,"failed":0}}` + "\n")
	return output.String()
}

func TestPinnedFixtureByteForByte(t *testing.T) {
	got := report(t)
	wantBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}
	wantLines := strings.Split(strings.TrimRight(want, "\n"), "\n")
	gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var expected, actual string
		if i < len(wantLines) {
			expected = wantLines[i]
		}
		if i < len(gotLines) {
			actual = gotLines[i]
		}
		if expected != actual {
			t.Errorf("line %d:\nwant %s\n got %s", i+1, expected, actual)
		}
	}
	t.Fatal("Go report differs from pinned Rust message-locals fixture")
}

func TestReportDeterministic(t *testing.T) {
	if first, second := report(t), report(t); first != second {
		t.Fatal("message-locals reports differ")
	}
}
