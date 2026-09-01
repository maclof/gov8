//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestAdvancedContextOptionsExtrasAndContinuation(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iso.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	template, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	seventyThree, _ := scope.Int32(73)
	if err := template.Set("fromTemplate", seventyThree); err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{GlobalTemplate: template})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctx.Close() }()
	if got, ok := evalText(t, ctx, scope, "String(fromTemplate)"); !ok || got != "73" {
		t.Fatalf("template property = %q, %v", got, ok)
	}
	if got, ok := evalText(t, ctx, scope, "Object.hasOwn(globalThis, 'fromTemplate')"); !ok || got != "true" {
		t.Fatalf("template property own = %q, %v", got, ok)
	}
	extrasA, err := ctx.GetExtrasBindingObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	extrasB, err := ctx.GetExtrasBindingObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	same, err := extrasA.Value.SameValue(extrasB.Value)
	if err != nil || !same {
		t.Fatalf("extras identity = %v, %v", same, err)
	}
	names, err := extrasA.GetPropertyNames(scope, ctx, gov8.KeyCollectionOwnOnly,
		gov8.PropertyFilterOnlyEnumerable|gov8.PropertyFilterSkipSymbols,
		gov8.IndexFilterIncludeIndices, gov8.KeyConversionKeepNumbers)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := names.Length(); err != nil || count != 0 {
		t.Fatalf("extras own property count = %d, %v", count, err)
	}

	initial, err := scope.GetContinuationPreservedEmbedderData()
	if err != nil {
		t.Fatal(err)
	}
	if undefined, err := initial.IsUndefined(); err != nil || !undefined {
		t.Fatalf("initial continuation data undefined = %v, %v", undefined, err)
	}
	continuation, _ := scope.NewString("continuation-a")
	if err := scope.SetContinuationPreservedEmbedderData(continuation); err != nil {
		t.Fatal(err)
	}
	ctxB, err := iso.NewContextWithOptions(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctxB.Close() }()
	fromB, err := scope.GetContinuationPreservedEmbedderData()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fromB.ToString(ctxB); err != nil || got != "continuation-a" {
		t.Fatalf("continuation in second context = %q, %v", got, err)
	}
}

func TestAdvancedContextGlobalReuse(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iso.Close() }()
	scope, _ := iso.NewScope()
	defer func() { _ = scope.Close() }()
	template, _ := iso.NewObjectTemplate(scope)
	nine, _ := scope.Int32(9)
	if err := template.Set("templated", nine); err != nil {
		t.Fatal(err)
	}
	first, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{GlobalTemplate: template})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if _, err := eval(t, first, scope, "globalThis.transient = 41"); err != nil {
		t.Fatal(err)
	}
	reused, err := first.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	second, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{
		GlobalTemplate: template,
		GlobalObject:   reused,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	secondGlobal, _ := second.GlobalObject(scope)
	same, err := reused.Value.SameValue(secondGlobal.Value)
	if err != nil || !same {
		t.Fatalf("reused global identity = %v, %v", same, err)
	}
	for source, want := range map[string]string{
		"typeof transient":  "undefined",
		"String(templated)": "9",
		"typeof Object":     "function",
	} {
		if got, ok := evalText(t, second, scope, source); !ok || got != want {
			t.Fatalf("%s = %q, %v; want %q", source, got, ok, want)
		}
	}
}

func TestAdvancedMicrotaskQueueObservationsAndLifetime(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iso.Close() }()
	scope, _ := iso.NewScope()
	defer func() { _ = scope.Close() }()
	queue, err := iso.NewMicrotaskQueue(gov8.PolicyExplicit)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContextWithOptions(scope, &gov8.ContextOptions{MicrotaskQueue: queue})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := queue.Raw()
	attached, _ := ctx.GetMicrotaskQueue()
	if attached != raw {
		t.Fatalf("construction queue = %#x; want %#x", attached, raw)
	}
	if err := queue.Close(); err == nil || !strings.Contains(err.Error(), "attached") {
		t.Fatalf("closing attached queue = %v", err)
	}

	type observation struct {
		runningBefore, runningAfter bool
		depthBefore, depthAfter     int32
	}
	var observed []observation
	callback, err := iso.NewFunction(scope, ctx,
		func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
			runningBefore, _ := queue.IsRunningMicrotasks()
			depthBefore, _ := queue.GetMicrotasksScopeDepth()
			_ = queue.PerformCheckpoint(ctx)
			runningAfter, _ := queue.IsRunningMicrotasks()
			depthAfter, _ := queue.GetMicrotasksScopeDepth()
			observed = append(observed, observation{runningBefore, runningAfter, depthBefore, depthAfter})
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if running, _ := queue.IsRunningMicrotasks(); running {
		t.Fatal("queue running before checkpoint")
	}
	if depth, _ := queue.GetMicrotasksScopeDepth(); depth != 0 {
		t.Fatalf("depth before = %d", depth)
	}
	if err := queue.Enqueue(ctx, callback.Value); err != nil {
		t.Fatal(err)
	}
	if err := queue.PerformCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0] != (observation{true, true, 0, 0}) {
		t.Fatalf("inside observation = %+v", observed)
	}
	if running, _ := queue.IsRunningMicrotasks(); running {
		t.Fatal("queue running after checkpoint")
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err == nil {
		t.Fatal("double context close must fail")
	}
	if err := queue.Close(); err != nil {
		t.Fatalf("queue after context close: %v", err)
	}
}

func TestAdvancedPromiseHooksRetainedAfterLocalScopeClose(t *testing.T) {
	iso, ctx, outer := newTestRuntime(t)
	if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		t.Fatal(err)
	}
	inner, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eval(t, ctx, inner, `globalThis.events=[];
initHook=function(_p,parent){events.push('init:'+(parent===undefined?'undefined':'promise'))};
beforeHook=function(_p){events.push('before')}; afterHook=function(_p){events.push('after')};
resolveHook=function(_p){events.push('resolve')}`); err != nil {
		t.Fatal(err)
	}
	hooks := gov8.ContextPromiseHooks{}
	for source, target := range map[string]**gov8.Function{
		"initHook": &hooks.Init, "beforeHook": &hooks.Before,
		"afterHook": &hooks.After, "resolveHook": &hooks.Resolve,
	} {
		value, err := eval(t, ctx, inner, source)
		if err != nil {
			t.Fatal(err)
		}
		function, ok, err := gov8.AsFunction(value, ctx)
		if err != nil || !ok {
			t.Fatalf("AsFunction(%s) = %v, %v", source, ok, err)
		}
		*target = function
	}
	if err := ctx.SetPromiseHooks(hooks); err != nil {
		t.Fatal(err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := eval(t, ctx, outer, "p=Promise.resolve(1); q=p.then(v=>v+1)"); err != nil {
		t.Fatal(err)
	}
	if got, ok := evalText(t, ctx, outer, "events.join(',')"); !ok || got != "init:undefined,resolve,init:promise" {
		t.Fatalf("synchronous hooks = %q, %v", got, ok)
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatal(err)
	}
	if got, ok := evalText(t, ctx, outer, "events.join(',')"); !ok || got != "init:undefined,resolve,init:promise,before,resolve,after" {
		t.Fatalf("checkpoint hooks = %q, %v", got, ok)
	}
	if err := ctx.SetPromiseHooks(gov8.ContextPromiseHooks{}); err != nil {
		t.Fatal(err)
	}
}

func TestAdvancedJavascriptExecutionScopes(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tc.Close() }()
	disallow, err := scope.NewDisallowJavascriptExecutionScope(gov8.ThrowOnFailure)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("Scope.Close with guard = %v", err)
	}
	script, err := ctx.Compile(scope, "43", tc)
	if err != nil {
		t.Fatalf("compile under disallow: %v", err)
	}
	defer func() { _ = script.Close() }()
	if _, err := script.Run(scope, tc); err == nil || !gov8.IsException(err) {
		t.Fatalf("run under disallow = %v", err)
	}
	if caught, _ := tc.HasCaught(); !caught {
		t.Fatal("ThrowOnFailure was not caught")
	}
	if text, err := tc.ExceptionText(scope, ctx); err != nil || text != "illegal access" {
		t.Fatalf("exception = %q, %v", text, err)
	}
	if err := tc.Reset(); err != nil {
		t.Fatal(err)
	}
	allow, err := disallow.NewAllowJavascriptExecutionScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := disallow.Close(); err == nil || !strings.Contains(err.Error(), "LIFO") {
		t.Fatalf("parent close before child = %v", err)
	}
	if got, ok := evalText(t, ctx, scope, "String(44)"); !ok || got != "44" {
		t.Fatalf("nested allow = %q, %v", got, ok)
	}
	if err := allow.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := script.Run(scope, tc); err == nil || !gov8.IsException(err) {
		t.Fatalf("restored disallow = %v", err)
	}
	if err := disallow.Close(); err != nil {
		t.Fatal(err)
	}
	if got, ok := evalText(t, ctx, scope, "String(46)"); !ok || got != "46" {
		t.Fatalf("after scope = %q, %v", got, ok)
	}
}

func TestAdvancedContextScopeNegativeValidation(t *testing.T) {
	isoA, ctxA, scopeA := newTestRuntime(t)
	isoB, ctxB, scopeB := newTestRuntime(t)
	_ = ctxA
	if _, err := isoA.NewMicrotaskQueue(gov8.MicrotasksPolicy(255)); err == nil {
		t.Fatal("invalid queue policy accepted")
	}
	if err := isoA.SetMicrotasksPolicy(gov8.MicrotasksPolicy(255)); err == nil {
		t.Fatal("invalid isolate policy accepted")
	}
	if _, err := scopeA.NewDisallowJavascriptExecutionScope(gov8.JavascriptExecutionFailure(255)); err == nil {
		t.Fatal("invalid failure mode accepted")
	}
	templateB, _ := isoB.NewObjectTemplate(scopeB)
	if _, err := isoA.NewContextWithOptions(scopeA, &gov8.ContextOptions{GlobalTemplate: templateB}); err == nil {
		t.Fatal("foreign template accepted")
	}
	globalB, _ := ctxB.GlobalObject(scopeB)
	if _, err := isoA.NewContextWithOptions(scopeA, &gov8.ContextOptions{GlobalObject: globalB}); err == nil {
		t.Fatal("foreign global accepted")
	}
	queueB, _ := isoB.NewMicrotaskQueue(gov8.PolicyExplicit)
	defer func() { _ = queueB.Close() }()
	if _, err := isoA.NewContextWithOptions(scopeA, &gov8.ContextOptions{MicrotaskQueue: queueB}); err == nil {
		t.Fatal("foreign queue accepted")
	}
	foreignData, _ := scopeB.NewString("foreign")
	if err := scopeA.SetContinuationPreservedEmbedderData(foreignData); err == nil {
		t.Fatal("foreign continuation data accepted")
	}
	foreignHookValue, err := eval(t, ctxB, scopeB, "function foreignHook(){}; foreignHook")
	if err != nil {
		t.Fatal(err)
	}
	foreignHook, ok, err := gov8.AsFunction(foreignHookValue, ctxB)
	if err != nil || !ok {
		t.Fatalf("foreign AsFunction = %v, %v", ok, err)
	}
	if err := ctxA.SetPromiseHooks(gov8.ContextPromiseHooks{Init: foreignHook}); err == nil {
		t.Fatal("foreign promise hook accepted")
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := scopeA.GetContinuationPreservedEmbedderData()
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread continuation error = %v", err)
	}
	queueA, _ := isoA.NewMicrotaskQueue(gov8.PolicyExplicit)
	defer func() { _ = queueA.Close() }()
	go func() {
		_, err := queueA.IsRunningMicrotasks()
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread queue error = %v", err)
	}
	disallow, err := scopeA.NewDisallowJavascriptExecutionScope(gov8.ThrowOnFailure)
	if err != nil {
		t.Fatal(err)
	}
	go func() { errCh <- disallow.Close() }()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread guard close error = %v", err)
	}
	if err := disallow.Close(); err != nil {
		t.Fatal(err)
	}
	closedScope, _ := isoA.NewScope()
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closedScope.GetContinuationPreservedEmbedderData(); err == nil {
		t.Fatal("continuation read through closed scope accepted")
	}
	closedQueue, _ := isoA.NewMicrotaskQueue(gov8.PolicyExplicit)
	if err := closedQueue.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closedQueue.GetMicrotasksScopeDepth(); err == nil {
		t.Fatal("closed queue depth read accepted")
	}
}

func runContextScopesProbe(t *testing.T, name string) (string, int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run", "^"+name+"$", "-test.v=false")
	cmd.Env = append(os.Environ(), "GOV8_CONTEXT_SCOPES_PROBE="+name)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return string(out), exit.ExitCode()
	}
	t.Fatalf("probe: %v", err)
	return "", -1
}

func TestCrashOnFailureIsSubprocessFatal(t *testing.T) {
	out, code := runContextScopesProbe(t, "TestProbeCrashOnFailure")
	for _, marker := range []string{"mode=crash", "scope=entered", "script=compiled"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("missing %q; output:\n%s", marker, out)
		}
	}
	if strings.Contains(out, "run_some=") {
		t.Fatalf("CrashOnFailure survived; output:\n%s", out)
	}
	if !strings.Contains(out, "Fatal error") || !strings.Contains(out, "Invoke in DisallowJavascriptExecutionScope") {
		t.Fatalf("fatal diagnostic mismatch; output:\n%s", out)
	}
	if code != exitStatusBreakpoint {
		t.Fatalf("exit code = %d, want %d; output:\n%s", code, exitStatusBreakpoint, out)
	}
}

func TestDumpOnFailurePermitsExecution(t *testing.T) {
	out, code := runContextScopesProbe(t, "TestProbeDumpOnFailure")
	if code != 0 || !strings.Contains(out, "run_some=true") {
		t.Fatalf("dump probe code=%d output:\n%s", code, out)
	}
	if strings.Contains(out, "Fatal error") {
		t.Fatalf("DumpOnFailure emitted fatal diagnostic:\n%s", out)
	}
}

func contextScopesProbe(t *testing.T, name string, mode gov8.JavascriptExecutionFailure) {
	if os.Getenv("GOV8_CONTEXT_SCOPES_PROBE") != name {
		t.Skip("subprocess probe")
	}
	chRawAbortExit()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iso.Close() }()
	scope, _ := iso.NewScope()
	defer func() { _ = scope.Close() }()
	ctx, _ := iso.NewContext()
	defer func() { _ = ctx.Close() }()
	label := "dump"
	if mode == gov8.CrashOnFailure {
		label = "crash"
	}
	fmt.Printf("mode=%s\n", label)
	guard, err := scope.NewDisallowJavascriptExecutionScope(mode)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guard.Close() }()
	fmt.Println("scope=entered")
	script, err := ctx.Compile(scope, "42", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	fmt.Println("script=compiled")
	_, err = script.Run(scope, nil)
	fmt.Printf("run_some=%v\n", err == nil)
}

func TestProbeCrashOnFailure(t *testing.T) {
	contextScopesProbe(t, "TestProbeCrashOnFailure", gov8.CrashOnFailure)
}

func TestProbeDumpOnFailure(t *testing.T) {
	contextScopesProbe(t, "TestProbeDumpOnFailure", gov8.DumpOnFailure)
}
