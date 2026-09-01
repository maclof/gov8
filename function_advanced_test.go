//go:build windows && amd64

package gov8_test

import (
	"errors"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func advancedNoop(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}

func mustFunction(t *testing.T, ctx *gov8.Context, value gov8.Value) *gov8.Function {
	t.Helper()
	function, ok, err := gov8.AsFunction(value, ctx)
	if err != nil || !ok {
		t.Fatalf("AsFunction = ok %v, err %v", ok, err)
	}
	return function
}

func TestFunctionAdvancedNamesAndBound(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	native, err := iso.NewFunction(scope, ctx, advancedNoop, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := native.Name(); err != nil || got != "" {
		t.Fatalf("initial name = %q, %v", got, err)
	}
	if err := native.SetName("native-renamed"); err != nil {
		t.Fatal(err)
	}
	if got, err := native.Name(); err != nil || got != "native-renamed" {
		t.Fatalf("renamed = %q, %v", got, err)
	}
	if !seedGlobal(t, ctx, scope, "nativeFn", native.Value) {
		t.Fatal("set nativeFn")
	}
	if got, ok := evalText(t, ctx, scope, "nativeFn.name"); !ok || got != "native-renamed" {
		t.Fatalf("JS name = %q, %v", got, ok)
	}

	declaredValue, err := eval(t, ctx, scope, "(function declaredName() {})")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mustFunction(t, ctx, declaredValue).Name(); err != nil || got != "declaredName" {
		t.Fatalf("declared name = %q, %v", got, err)
	}
	inferredValue, err := eval(t, ctx, scope, "globalThis.inferredSlot = function() {}; inferredSlot")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mustFunction(t, ctx, inferredValue).Name(); err != nil || got != "" {
		t.Fatalf("assignment inferred GetName = %q, %v", got, err)
	}

	boundValue, err := eval(t, ctx, scope,
		"globalThis.boundTarget = function target(a,b){return this.tag+':'+a+':'+b}; boundTarget.bind({tag:'BOUND'}, 'A')")
	if err != nil {
		t.Fatal(err)
	}
	bound := mustFunction(t, ctx, boundValue)
	if got, err := bound.Name(); err != nil || got != "bound target" {
		t.Fatalf("bound name = %q, %v", got, err)
	}
	if err := bound.SetName("ignored-on-bound"); err != nil {
		t.Fatal(err)
	}
	if got, err := bound.Name(); err != nil || got != "bound target" {
		t.Fatalf("bound name after SetName = %q, %v", got, err)
	}
	target, err := bound.BoundTarget()
	if err != nil {
		t.Fatal(err)
	}
	targetValue, err := eval(t, ctx, scope, "boundTarget")
	if err != nil {
		t.Fatal(err)
	}
	if same, err := target.StrictEquals(targetValue); err != nil || !same {
		t.Fatalf("BoundTarget identity = %v, %v", same, err)
	}
	argument, _ := scope.NewString("B")
	result, ok, err := bound.Call(scope, mustUndefinedT(t, scope), argument)
	if err != nil || !ok {
		t.Fatalf("bound.Call = ok %v, err %v", ok, err)
	}
	if got, err := result.ToString(ctx); err != nil || got != "BOUND:A:B" {
		t.Fatalf("bound call result = %q, %v", got, err)
	}
	if !seedGlobal(t, ctx, scope, "boundFn", bound.Value) {
		t.Fatal("set boundFn")
	}
	if got, ok := evalText(t, ctx, scope, "boundFn.length + ':' + boundFn('B')"); !ok || got != "1:BOUND:A:B" {
		t.Fatalf("bound JS surface = %q, %v", got, ok)
	}
}

func TestFunctionAdvancedBuilderConstructorAndSideEffectMetadata(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	concise, err := iso.FunctionBuilder(advancedNoop).
		ConstructorBehavior(gov8.ConstructorBehaviorThrow).
		SideEffectType(gov8.SideEffectHasNoSideEffect).
		Build(scope, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := concise.SetName("DirectConcise"); err != nil {
		t.Fatal(err)
	}
	regular, err := iso.FunctionBuilder(advancedNoop).
		SideEffectType(gov8.SideEffectHasSideEffectToReceiver).
		Build(scope, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !seedGlobal(t, ctx, scope, "directConcise", concise.Value) ||
		!seedGlobal(t, ctx, scope, "directRegular", regular.Value) {
		t.Fatal("set globals")
	}
	got, ok := evalText(t, ctx, scope,
		"[typeof directConcise.prototype,String(directConcise()),(()=>{try{new directConcise();return 'survived'}catch(e){return e.toString()}})(),typeof directRegular.prototype,new directRegular() instanceof directRegular].join('|')")
	if !ok || got != "undefined|undefined|TypeError: directConcise is not a constructor|object|true" {
		t.Fatalf("constructor behavior = %q, %v", got, ok)
	}

	// FunctionTemplate receives the same metadata enum. Normal execution is
	// deliberately unaffected; throwOnSideEffect itself belongs to Inspector.
	template, err := iso.NewFunctionTemplate(scope, advancedNoop, &gov8.FunctionOptions{
		SideEffectType: gov8.SideEffectHasNoSideEffect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := template.GetFunction(scope, ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionAdvancedScriptMetadata(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	script, err := ctx.CompileWithOrigin(scope,
		"\n\n(function originFunction(){ return 1; })",
		&gov8.Origin{ResourceName: "origin-file.js", LineOffset: 10, ColumnOffset: 20, ScriptID: 777, SourceMapURL: "origin-file.map"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	value, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	function := mustFunction(t, ctx, value)
	line, lineOK, err := function.ScriptLineNumber()
	if err != nil || !lineOK || line != 12 {
		t.Fatalf("line = %d, %v, %v", line, lineOK, err)
	}
	column, columnOK, err := function.ScriptColumnNumber()
	if err != nil || !columnOK || column != 24 {
		t.Fatalf("column = %d, %v, %v", column, columnOK, err)
	}
	functionID, err := function.ScriptID()
	if err != nil {
		t.Fatal(err)
	}
	scriptID, err := script.ID()
	if err != nil || functionID != scriptID {
		t.Fatalf("ids: function=%d script=%d err=%v", functionID, scriptID, err)
	}
	origin, err := function.ScriptOrigin()
	if err != nil {
		t.Fatal(err)
	}
	resource, _ := origin.ResourceName.ToString(ctx)
	sourceMap, _ := origin.SourceMapURL.ToString(ctx)
	if origin.ScriptID != functionID || !origin.HasResourceName || !origin.HasSourceMapURL || resource != "origin-file.js" || sourceMap != "origin-file.map" {
		t.Fatalf("origin = id %d resource %q map %q", origin.ScriptID, resource, sourceMap)
	}

	sourceURLScript, err := ctx.Compile(scope, "(function sourceUrlFunction(){})\n//# sourceURL=virtual-source.js", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sourceURLScript.Close() }()
	sourceURLValue, err := sourceURLScript.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	sourceURLOrigin, err := mustFunction(t, ctx, sourceURLValue).ScriptOrigin()
	if err != nil {
		t.Fatal(err)
	}
	if resource, _ := sourceURLOrigin.ResourceName.ToString(ctx); resource != "virtual-source.js" {
		t.Fatalf("sourceURL resource = %q", resource)
	}

	native, err := iso.NewFunction(scope, ctx, advancedNoop, nil)
	if err != nil {
		t.Fatal(err)
	}
	if line, ok, err := native.ScriptLineNumber(); err != nil || ok || line != -1 {
		t.Fatalf("native line = %d, %v, %v", line, ok, err)
	}
	if column, ok, err := native.ScriptColumnNumber(); err != nil || ok || column != -1 {
		t.Fatalf("native column = %d, %v, %v", column, ok, err)
	}
	if id, err := native.ScriptID(); err != nil || id != 0 {
		t.Fatalf("native id = %d, %v", id, err)
	}
	nativeOrigin, err := native.ScriptOrigin()
	if err != nil {
		t.Fatal(err)
	}
	if nativeOrigin.HasResourceName || nativeOrigin.HasSourceMapURL {
		t.Fatalf("native origin presence = %v, %v", nativeOrigin.HasResourceName, nativeOrigin.HasSourceMapURL)
	}
}

func TestFunctionAdvancedBoundConstruct(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	boundValue, err := eval(t, ctx, scope,
		"globalThis.Base=function Base(a,b){this.args=a+':'+b;this.nt=new.target.name}; globalThis.Bound=Base.bind({ignored:true},'A'); Bound")
	if err != nil {
		t.Fatal(err)
	}
	bound := mustFunction(t, ctx, boundValue)
	argument, _ := scope.NewString("B")
	instance, ok, err := bound.NewInstance(scope, argument)
	if err != nil || !ok {
		t.Fatalf("NewInstance = %v, %v", ok, err)
	}
	if !seedGlobal(t, ctx, scope, "hostBoundInstance", instance.Value) {
		t.Fatal("set instance")
	}
	got, ok := evalText(t, ctx, scope,
		"[(()=>{let x=new Bound('B');return x.args+':'+x.nt})(),hostBoundInstance.args+':'+hostBoundInstance.nt,hostBoundInstance instanceof Base,hostBoundInstance instanceof Bound,(()=>{function Alternate(){};let y=Reflect.construct(Bound,['B'],Alternate);return y.args+':'+y.nt+':'+(Object.getPrototypeOf(y)===Alternate.prototype)})()].join('|')")
	if !ok || got != "A:B:Base|A:B:Base|true|true|A:B:Alternate:true" {
		t.Fatalf("bound construct = %q, %v", got, ok)
	}
}

func TestFunctionAdvancedCodeCacheCrossIsolate(t *testing.T) {
	const source = "return left * 10 + right;"
	params := []string{"left", "right"}

	producer, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	producerContext, _ := producer.NewContext()
	producerScope, _ := producer.NewScope()
	compiled, rejected, err := producerContext.CompileFunctionAdvanced(producerScope, source, params, nil, nil)
	if err != nil || rejected {
		t.Fatalf("producer compile = rejected %v, err %v", rejected, err)
	}
	cache, err := compiled.CreateCodeCache()
	if err != nil || cache.Len() == 0 {
		t.Fatalf("CreateCodeCache = len %d, err %v", cache.Len(), err)
	}
	_ = producerScope.Close()
	_ = producerContext.Close()
	_ = producer.Close()

	consumer, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = consumer.Close() }()
	consumerContext, _ := consumer.NewContext()
	defer func() { _ = consumerContext.Close() }()
	consumerScope, _ := consumer.NewScope()
	defer func() { _ = consumerScope.Close() }()
	consumed, rejected, err := consumerContext.CompileFunctionAdvanced(consumerScope, source, params, cache, nil)
	if err != nil || rejected {
		t.Fatalf("consumer compile = rejected %v, err %v", rejected, err)
	}
	left, _ := consumerScope.Int32(4)
	right, _ := consumerScope.Int32(2)
	result, ok, err := consumed.Call(consumerScope, mustUndefinedT(t, consumerScope), left, right)
	if err != nil || !ok {
		t.Fatalf("Call = ok %v, err %v", ok, err)
	}
	value, converted, err := result.IntegerValue(consumerContext)
	if err != nil || !converted || value != 42 {
		t.Fatalf("result = %d, %v, %v", value, converted, err)
	}
}

func TestFunctionAdvancedSafeCacheBoundary(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	native, err := iso.NewFunction(scope, ctx, advancedNoop, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.CreateCodeCache(); !errors.Is(err, gov8.ErrFunctionNotCacheable) {
		t.Fatalf("native cache error = %v", err)
	}
	for name, source := range map[string]string{
		"script": "(function scripted(){})",
		"bound":  "(function target(){}).bind(null)",
	} {
		value, err := eval(t, ctx, scope, source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mustFunction(t, ctx, value).CreateCodeCache(); !errors.Is(err, gov8.ErrFunctionNotCacheable) {
			t.Fatalf("%s cache error = %v", name, err)
		}
	}
	compiled, _, err := ctx.CompileFunctionAdvanced(scope,
		"return left * 10 + right;", []string{"left", "right"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := compiled.CreateCodeCache()
	if err != nil {
		t.Fatal(err)
	}
	call := func(function *gov8.Function) int64 {
		left, _ := scope.Int32(4)
		right, _ := scope.Int32(2)
		result, ok, err := function.Call(scope, mustUndefinedT(t, scope), left, right)
		if err != nil || !ok {
			t.Fatalf("cached function call = %v, %v", ok, err)
		}
		value, converted, err := result.IntegerValue(ctx)
		if err != nil || !converted {
			t.Fatalf("cached function result = %v, %v", converted, err)
		}
		return value
	}
	length := func(name string, function *gov8.Function) string {
		if !seedGlobal(t, ctx, scope, name, function.Value) {
			t.Fatalf("set %s", name)
		}
		value, ok := evalText(t, ctx, scope, name+".length")
		if !ok {
			t.Fatalf("read %s.length", name)
		}
		return value
	}

	changedSource, rejected, err := ctx.CompileFunctionAdvanced(scope,
		"return left * 10 + right + 1;", []string{"left", "right"}, cache, nil)
	if err != nil || !rejected || call(changedSource) != 43 || length("changedSourceFn", changedSource) != "2" {
		t.Fatalf("changed source = rejected %v, err %v", rejected, err)
	}
	changedNames, rejected, err := ctx.CompileFunctionAdvanced(scope,
		"return left * 10 + right;", []string{"x", "y"}, cache, nil)
	if err != nil || rejected || call(changedNames) != 42 || length("changedNamesFn", changedNames) != "2" {
		t.Fatalf("changed names = rejected %v, err %v", rejected, err)
	}
	changedCount, rejected, err := ctx.CompileFunctionAdvanced(scope,
		"return left * 10 + right;", []string{"left"}, cache, nil)
	if err != nil || rejected || call(changedCount) != 42 || length("changedCountFn", changedCount) != "2" {
		t.Fatalf("changed count = rejected %v, err %v", rejected, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		reused, rejected, err := ctx.CompileFunctionAdvanced(scope,
			"return left * 10 + right;", []string{"left", "right"}, cache, nil)
		if err != nil || rejected || call(reused) != 42 {
			t.Fatalf("reuse %d = rejected %v, err %v", attempt, rejected, err)
		}
	}
}
