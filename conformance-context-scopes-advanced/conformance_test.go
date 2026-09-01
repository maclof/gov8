//go:build windows && amd64

package contextscopesadvancedconformance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	gov8 "gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-context-scopes-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

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

type summaryLine struct {
	Summary struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

func encodeLine(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded) + "\n"
}

func runReport(t *testing.T) string {
	t.Helper()
	checks := []func(*testing.T) outcome{
		checkGlobalTemplateAndExtras,
		checkGlobalObjectReuse,
		checkDistinctQueues,
		checkSharedQueue,
		checkContinuationData,
		checkQueueRunningAndDepth,
		checkPromiseHooks,
		checkJavascriptExecutionScopes,
	}
	var report strings.Builder
	for _, check := range checks {
		result := check(t)
		report.WriteString(encodeLine(t, reportLine{Check: result.check, OK: true, Value: result.value}))
	}
	var summary summaryLine
	summary.Summary.Total = len(checks)
	summary.Summary.Passed = len(checks)
	report.WriteString(encodeLine(t, summary))
	return report.String()
}

func TestPinnedFixtureByteForByte(t *testing.T) {
	got := runReport(t)
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		wantLines := strings.Split(strings.TrimRight(string(want), "\n"), "\n")
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
		t.Fatal("Go report differs from pinned Rust context/scope fixture")
	}
}

func TestReportDeterministic(t *testing.T) {
	first := runReport(t)
	second := runReport(t)
	if first != second {
		t.Fatal("two context/scope reports differ")
	}
}

func newIsolateScope(t *testing.T) (*gov8.Isolate, *gov8.Scope) {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = iso.Close()
		t.Fatal(err)
	}
	return iso, scope
}

func eval(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, source string) (gov8.Value, bool) {
	t.Helper()
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	value, err := script.Run(scope, nil)
	return value, err == nil
}

func evalText(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, source string) string {
	t.Helper()
	value, ok := eval(t, ctx, scope, source)
	if !ok {
		return ""
	}
	text, err := value.ToString(ctx)
	if err != nil {
		return ""
	}
	return text
}

func evalFunction(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, source string) *gov8.Function {
	t.Helper()
	value, ok := eval(t, ctx, scope, source)
	if !ok {
		t.Fatalf("eval function %q", source)
	}
	function, isFunction, err := gov8.AsFunction(value, ctx)
	if err != nil || !isFunction {
		t.Fatalf("AsFunction(%q) = %v, %v", source, isFunction, err)
	}
	return function
}

func checkGlobalTemplateAndExtras(t *testing.T) outcome {
	iso, scope := newIsolateScope(t)
	defer func() { _ = scope.Close(); _ = iso.Close() }()
	template, _ := iso.NewObjectTemplate(scope)
	value, _ := scope.Int32(73)
	if err := template.Set("fromTemplate", value); err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{GlobalTemplate: template})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctx.Close() }()
	extrasA, err := ctx.GetExtrasBindingObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	extrasB, err := ctx.GetExtrasBindingObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	extrasIsObject, _ := extrasA.Value.IsObject()
	extrasSame, _ := extrasA.Value.SameValue(extrasB.Value)
	names, err := extrasA.GetPropertyNames(scope, ctx, gov8.KeyCollectionOwnOnly,
		gov8.PropertyFilterOnlyEnumerable|gov8.PropertyFilterSkipSymbols,
		gov8.IndexFilterIncludeIndices, gov8.KeyConversionKeepNumbers)
	if err != nil {
		t.Fatal(err)
	}
	nameCount, err := names.Length()
	if err != nil {
		t.Fatal(err)
	}
	valueResult := struct {
		TemplateValue          string `json:"template_value"`
		TemplateValueIsOwn     bool   `json:"template_value_is_own"`
		ExtrasIsObject         bool   `json:"extras_is_object"`
		ExtrasIdentityStable   bool   `json:"extras_identity_stable"`
		ExtrasOwnPropertyCount int64  `json:"extras_own_property_count"`
	}{
		TemplateValue:          evalText(t, ctx, scope, "String(fromTemplate)"),
		TemplateValueIsOwn:     evalText(t, ctx, scope, "Object.hasOwn(globalThis, 'fromTemplate')") == "true",
		ExtrasIsObject:         extrasIsObject,
		ExtrasIdentityStable:   extrasSame,
		ExtrasOwnPropertyCount: nameCount,
	}
	return outcome{"context-scopes-advanced/context/options_global_template_and_extras", valueResult}
}

func checkGlobalObjectReuse(t *testing.T) outcome {
	iso, scope := newIsolateScope(t)
	defer func() { _ = scope.Close(); _ = iso.Close() }()
	template, _ := iso.NewObjectTemplate(scope)
	nine, _ := scope.Int32(9)
	_ = template.Set("templated", nine)
	first, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{GlobalTemplate: template})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	_, _ = eval(t, first, scope, "globalThis.transient = 41")
	reused, _ := first.GlobalObject(scope)
	second, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{GlobalTemplate: template, GlobalObject: reused})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	secondGlobal, _ := second.GlobalObject(scope)
	same, _ := reused.Value.SameValue(secondGlobal.Value)
	valueResult := struct {
		GlobalIdentityReused    bool   `json:"global_identity_reused"`
		TransientTypeAfterReuse string `json:"transient_type_after_reuse"`
		TemplateValueAfterReuse string `json:"template_value_after_reuse"`
		BuiltinsAvailable       bool   `json:"builtins_available"`
	}{same, evalText(t, second, scope, "typeof transient"), evalText(t, second, scope, "String(templated)"), evalText(t, second, scope, "typeof Object") == "function"}
	return outcome{"context-scopes-advanced/context/options_global_object_reuse", valueResult}
}

func checkDistinctQueues(t *testing.T) outcome {
	iso, scope := newIsolateScope(t)
	defer func() { _ = scope.Close(); _ = iso.Close() }()
	queueA, _ := iso.NewMicrotaskQueue(gov8.PolicyExplicit)
	queueB, _ := iso.NewMicrotaskQueue(gov8.PolicyExplicit)
	defer func() { _ = queueB.Close(); _ = queueA.Close() }()
	contextA, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{MicrotaskQueue: queueA})
	if err != nil {
		t.Fatal(err)
	}
	contextB, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{MicrotaskQueue: queueB})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = contextB.Close(); _ = contextA.Close() }()
	attachedA, _ := contextA.GetMicrotaskQueue()
	attachedB, _ := contextB.GetMicrotaskQueue()
	rawA, _ := queueA.Raw()
	rawB, _ := queueB.Raw()
	_, _ = eval(t, contextA, scope, "globalThis.order = []; Promise.resolve().then(() => order.push('a'));")
	_, _ = eval(t, contextB, scope, "globalThis.order = []; Promise.resolve().then(() => order.push('b'));")
	_ = queueA.PerformCheckpoint(nil)
	afterA := evalText(t, contextA, scope, "order.join(',')")
	bBefore := evalText(t, contextB, scope, "order.join(',')")
	_ = queueB.PerformCheckpoint(nil)
	bAfter := evalText(t, contextB, scope, "order.join(',')")
	valueResult := struct {
		QueueAAttachedAtCreation bool   `json:"queue_a_attached_at_creation"`
		QueueBAttachedAtCreation bool   `json:"queue_b_attached_at_creation"`
		QueuesDistinct           bool   `json:"queues_distinct"`
		AAfterACheckpoint        string `json:"a_after_a_checkpoint"`
		BBeforeBCheckpoint       string `json:"b_before_b_checkpoint"`
		BAfterBCheckpoint        string `json:"b_after_b_checkpoint"`
	}{attachedA == rawA, attachedB == rawB, rawA != rawB, afterA, bBefore, bAfter}
	return outcome{"context-scopes-advanced/microtask/options_distinct_queues", valueResult}
}

func checkSharedQueue(t *testing.T) outcome {
	iso, scope := newIsolateScope(t)
	defer func() { _ = scope.Close(); _ = iso.Close() }()
	queue, _ := iso.NewMicrotaskQueue(gov8.PolicyExplicit)
	defer func() { _ = queue.Close() }()
	contextA, _ := iso.NewContextWithOptions(scope, &gov8.ContextOptions{MicrotaskQueue: queue})
	contextB, _ := iso.NewContextWithOptions(scope, &gov8.ContextOptions{MicrotaskQueue: queue})
	defer func() { _ = contextB.Close(); _ = contextA.Close() }()
	_, _ = eval(t, contextA, scope, "globalThis.done = ''; Promise.resolve().then(() => done = 'a');")
	_, _ = eval(t, contextB, scope, "globalThis.done = ''; Promise.resolve().then(() => done = 'b');")
	_ = queue.PerformCheckpoint(nil)
	attachedA, _ := contextA.GetMicrotaskQueue()
	attachedB, _ := contextB.GetMicrotaskQueue()
	valueResult := struct {
		ContextsShareQueue      bool   `json:"contexts_share_queue"`
		ContextAAfterCheckpoint string `json:"context_a_after_checkpoint"`
		ContextBAfterCheckpoint string `json:"context_b_after_checkpoint"`
	}{attachedA == attachedB, evalText(t, contextA, scope, "done"), evalText(t, contextB, scope, "done")}
	return outcome{"context-scopes-advanced/microtask/options_shared_queue", valueResult}
}

func checkContinuationData(t *testing.T) outcome {
	iso, scope := newIsolateScope(t)
	defer func() { _ = scope.Close(); _ = iso.Close() }()
	_ = iso.SetMicrotasksPolicy(gov8.PolicyExplicit)
	contextA, _ := iso.NewContext()
	contextB, _ := iso.NewContext()
	defer func() { _ = contextB.Close(); _ = contextA.Close() }()
	initial, _ := scope.GetContinuationPreservedEmbedderData()
	initialUndefined, _ := initial.IsUndefined()
	continuation, _ := scope.NewString("continuation-a")
	_ = scope.SetContinuationPreservedEmbedderData(continuation)
	_, _ = eval(t, contextA, scope, "Promise.resolve().then(() => 1); 6 * 7")
	scriptCompleted := evalText(t, contextA, scope, "String(6 * 7)")
	visible, _ := scope.GetContinuationPreservedEmbedderData()
	visibleText, _ := visible.ToString(contextB)
	_ = iso.PerformMicrotaskCheckpoint()
	after, _ := scope.GetContinuationPreservedEmbedderData()
	afterText, _ := after.ToString(contextA)
	undefined, _ := scope.Undefined()
	_ = scope.SetContinuationPreservedEmbedderData(undefined)
	reset, _ := scope.GetContinuationPreservedEmbedderData()
	resetUndefined, _ := reset.IsUndefined()
	valueResult := struct {
		InitialUndefined            bool   `json:"initial_undefined"`
		ScriptCompleted             string `json:"script_completed"`
		VisibleInSecondContext      string `json:"visible_in_second_context"`
		SurvivesMicrotaskCheckpoint string `json:"survives_microtask_checkpoint"`
		ResetInSecondVisibleInFirst bool   `json:"reset_in_second_visible_in_first"`
	}{initialUndefined, scriptCompleted, visibleText, afterText, resetUndefined}
	return outcome{"context-scopes-advanced/context/continuation_preserved_data", valueResult}
}

func checkQueueRunningAndDepth(t *testing.T) outcome {
	iso, scope := newIsolateScope(t)
	defer func() { _ = scope.Close(); _ = iso.Close() }()
	queue, _ := iso.NewMicrotaskQueue(gov8.PolicyExplicit)
	defer func() { _ = queue.Close() }()
	ctx, _ := iso.NewContextWithOptions(scope, &gov8.ContextOptions{MicrotaskQueue: queue})
	defer func() { _ = ctx.Close() }()
	type observation struct {
		runningBefore bool
		depthBefore   int32
		runningAfter  bool
		depthAfter    int32
	}
	var observations []observation
	function, err := iso.NewFunction(scope, ctx, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		runningBefore, _ := queue.IsRunningMicrotasks()
		depthBefore, _ := queue.GetMicrotasksScopeDepth()
		_ = queue.PerformCheckpoint(nil)
		runningAfter, _ := queue.IsRunningMicrotasks()
		depthAfter, _ := queue.GetMicrotasksScopeDepth()
		observations = append(observations, observation{runningBefore, depthBefore, runningAfter, depthAfter})
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outsideBeforeRunning, _ := queue.IsRunningMicrotasks()
	outsideBeforeDepth, _ := queue.GetMicrotasksScopeDepth()
	_ = queue.Enqueue(ctx, function.Value)
	_ = queue.PerformCheckpoint(nil)
	outsideAfterRunning, _ := queue.IsRunningMicrotasks()
	outsideAfterDepth, _ := queue.GetMicrotasksScopeDepth()
	inside := observation{depthBefore: -1, depthAfter: -1}
	if len(observations) != 0 {
		inside = observations[0]
	}
	valueResult := struct {
		OutsideBeforeRunning         bool  `json:"outside_before_running"`
		OutsideBeforeDepth           int32 `json:"outside_before_depth"`
		CallbackCount                int   `json:"callback_count"`
		InsideRunning                bool  `json:"inside_running"`
		InsideDepth                  int32 `json:"inside_depth"`
		AfterNestedCheckpointRunning bool  `json:"after_nested_checkpoint_running"`
		AfterNestedCheckpointDepth   int32 `json:"after_nested_checkpoint_depth"`
		OutsideAfterRunning          bool  `json:"outside_after_running"`
		OutsideAfterDepth            int32 `json:"outside_after_depth"`
	}{outsideBeforeRunning, outsideBeforeDepth, len(observations), inside.runningBefore, inside.depthBefore, inside.runningAfter, inside.depthAfter, outsideAfterRunning, outsideAfterDepth}
	return outcome{"context-scopes-advanced/microtask/running_and_scope_depth", valueResult}
}

func checkPromiseHooks(t *testing.T) outcome {
	iso, scope := newIsolateScope(t)
	defer func() { _ = scope.Close(); _ = iso.Close() }()
	_ = iso.SetMicrotasksPolicy(gov8.PolicyExplicit)
	ctx, _ := iso.NewContext()
	defer func() { _ = ctx.Close() }()
	_, _ = eval(t, ctx, scope, `globalThis.events = [];
globalThis.initHook = function(_promise, parent) { events.push('init:' + (parent === undefined ? 'undefined' : 'promise')); };
globalThis.beforeHook = function(_promise) { events.push('before'); };
globalThis.afterHook = function(_promise) { events.push('after'); };
globalThis.resolveHook = function(_promise) { events.push('resolve'); };`)
	_ = ctx.SetPromiseHooks(gov8.ContextPromiseHooks{
		Init: evalFunction(t, ctx, scope, "initHook"), Before: evalFunction(t, ctx, scope, "beforeHook"),
		After: evalFunction(t, ctx, scope, "afterHook"), Resolve: evalFunction(t, ctx, scope, "resolveHook"),
	})
	_, _ = eval(t, ctx, scope, "globalThis.p = Promise.resolve(1); globalThis.q = p.then(v => v + 1);")
	synchronous := evalText(t, ctx, scope, "events.join(',')")
	_ = iso.PerformMicrotaskCheckpoint()
	afterCheckpoint := evalText(t, ctx, scope, "events.join(',')")
	_ = ctx.SetPromiseHooks(gov8.ContextPromiseHooks{})
	_, _ = eval(t, ctx, scope, "Promise.resolve(3)")
	afterDisable := evalText(t, ctx, scope, "events.join(',')")
	valueResult := struct {
		Synchronous       string `json:"synchronous"`
		AfterCheckpoint   string `json:"after_checkpoint"`
		AfterDisable      string `json:"after_disable"`
		DisableStopsHooks bool   `json:"disable_stops_hooks"`
	}{synchronous, afterCheckpoint, afterDisable, afterDisable == afterCheckpoint}
	return outcome{"context-scopes-advanced/context/promise_hooks", valueResult}
}

func checkJavascriptExecutionScopes(t *testing.T) outcome {
	iso, scope := newIsolateScope(t)
	defer func() { _ = scope.Close(); _ = iso.Close() }()
	ctx, _ := iso.NewContext()
	defer func() { _ = ctx.Close() }()
	baseline := evalText(t, ctx, scope, "String(40 + 2)")

	tc, _ := iso.NewTryCatch()
	disallow, _ := scope.NewDisallowJavascriptExecutionScope(gov8.ThrowOnFailure)
	script, compileErr := ctx.Compile(scope, "43", tc)
	runNone := true
	if compileErr == nil {
		_, runErr := script.Run(scope, tc)
		runNone = runErr != nil
		_ = script.Close()
	}
	_ = disallow.Close()
	hasCaught, _ := tc.HasCaught()
	exception, _ := tc.ExceptionText(scope, ctx)
	_ = tc.Close()

	tcNested, _ := iso.NewTryCatch()
	disallowNested, _ := scope.NewDisallowJavascriptExecutionScope(gov8.ThrowOnFailure)
	allow, _ := disallowNested.NewAllowJavascriptExecutionScope()
	allowedValue := evalText(t, ctx, scope, "String(44)")
	_ = allow.Close()
	disallowedAgain := true
	script45, compile45Err := ctx.Compile(scope, "45", tcNested)
	if compile45Err == nil {
		_, run45Err := script45.Run(scope, tcNested)
		disallowedAgain = run45Err != nil
		_ = script45.Close()
	}
	_ = disallowNested.Close()
	caughtAfterRestore, _ := tcNested.HasCaught()
	_ = tcNested.Close()

	valueResult := struct {
		BeforeScope    string `json:"before_scope"`
		ThrowOnFailure struct {
			RunNone   bool   `json:"run_none"`
			HasCaught bool   `json:"has_caught"`
			Exception string `json:"exception"`
		} `json:"throw_on_failure"`
		NestedAllow struct {
			AllowedValue          string `json:"allowed_value"`
			DisallowedAgain       bool   `json:"disallowed_again"`
			HasCaughtAfterRestore bool   `json:"has_caught_after_restore"`
		} `json:"nested_allow"`
		AfterScope string `json:"after_scope"`
	}{BeforeScope: baseline, AfterScope: evalText(t, ctx, scope, "String(46)")}
	valueResult.ThrowOnFailure.RunNone = runNone
	valueResult.ThrowOnFailure.HasCaught = hasCaught
	valueResult.ThrowOnFailure.Exception = exception
	valueResult.NestedAllow.AllowedValue = allowedValue
	valueResult.NestedAllow.DisallowedAgain = disallowedAgain
	valueResult.NestedAllow.HasCaughtAfterRestore = caughtAfterRestore
	return outcome{"context-scopes-advanced/scope/disallow_allow_nesting", valueResult}
}
