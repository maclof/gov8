//go:build windows && amd64

package functionsadvancedconformance

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gov8 "gov8"
)

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type fixtureLine struct {
	Check string         `json:"check"`
	OK    bool           `json:"ok"`
	Value map[string]any `json:"value"`
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-functions-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checked-in Rust advanced-function oracle fixture is missing: %s", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result := map[string]fixtureLine{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err == nil && line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok {
		t.Fatalf("fixture lacks %s", id)
	}
	if !want.OK {
		t.Fatalf("Rust oracle reports %s failed", id)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want.Value)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
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
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtime) close(t *testing.T) {
	t.Helper()
	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		t.Error(err)
	}
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

func (r *runtime) text(t *testing.T, source string) string {
	t.Helper()
	value := r.eval(t, source)
	text, err := value.ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func (r *runtime) function(t *testing.T, source string) *gov8.Function {
	t.Helper()
	function, ok, err := gov8.AsFunction(r.eval(t, source), r.ctx)
	if err != nil || !ok {
		t.Fatalf("AsFunction = %v, %v", ok, err)
	}
	return function
}

func (r *runtime) setGlobal(t *testing.T, name string, value gov8.Value) {
	t.Helper()
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := global.SetByName(r.scope, r.ctx, name, value)
	if err != nil || !ok {
		t.Fatalf("set %s = %v, %v", name, ok, err)
	}
}

func undefined(t *testing.T, scope *gov8.Scope) gov8.Value {
	t.Helper()
	value, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func noop(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}

func TestAdvancedFunctionConformanceFixture(t *testing.T) {
	fixtures := fixture(t)
	const prefix = "functions-advanced/"
	for _, name := range []string{
		"names_and_bound", "direct_builder_constructor_behavior", "script_metadata",
		"bound_construct_semantics", "side_effect_policies", "code_cache_roundtrip",
	} {
		line, ok := fixtures[prefix+name]
		if !ok || !line.OK {
			t.Fatalf("fixture lacks a passing %s%s check", prefix, name)
		}
	}

	t.Run("names_and_bound", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		native, err := r.iso.NewFunction(r.scope, r.ctx, noop, nil)
		if err != nil {
			t.Fatal(err)
		}
		initial, _ := native.Name()
		if err := native.SetName("native-renamed"); err != nil {
			t.Fatal(err)
		}
		r.setGlobal(t, "nativeFn", native.Value)
		declared := r.function(t, "(function declaredName(){})")
		inferred := r.function(t, "globalThis.inferredSlot=function(){}; inferredSlot")
		bound := r.function(t, "globalThis.boundTarget=function target(a,b){return this.tag+':'+a+':'+b}; boundTarget.bind({tag:'BOUND'},'A')")
		boundName, _ := bound.Name()
		if err := bound.SetName("ignored-on-bound"); err != nil {
			t.Fatal(err)
		}
		r.setGlobal(t, "boundFn", bound.Value)
		argument, _ := r.scope.NewString("B")
		hostResult, ok, err := bound.Call(r.scope, undefined(t, r.scope), argument)
		if err != nil || !ok {
			t.Fatalf("bound Call = %v, %v", ok, err)
		}
		hostText, _ := hostResult.ToString(r.ctx)
		nativeAfter, _ := native.Name()
		declaredName, _ := declared.Name()
		inferredName, _ := inferred.Name()
		boundAfter, _ := bound.Name()
		compare(t, fixtures, prefix+"names_and_bound", map[string]any{
			"native_initial": initial, "native_after_set_name": nativeAfter,
			"native_js_name": r.text(t, "nativeFn.name"), "declared_name": declaredName,
			"assignment_inferred_name": inferredName, "bound_name": boundName,
			"bound_set_name_is_noop": boundAfter, "bound_length": r.text(t, "boundFn.length"),
			"bound_call": r.text(t, "boundFn('B')"), "bound_host_call": hostText,
		})
	})

	t.Run("direct_builder_constructor_behavior", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		concise, err := r.iso.FunctionBuilder(noop).ConstructorBehavior(gov8.ConstructorBehaviorThrow).Build(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = concise.SetName("DirectConcise")
		regular, err := r.iso.NewFunction(r.scope, r.ctx, noop, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = regular.SetName("DirectRegular")
		r.setGlobal(t, "directConcise", concise.Value)
		r.setGlobal(t, "directRegular", regular.Value)
		compare(t, fixtures, prefix+"direct_builder_constructor_behavior", map[string]any{
			"concise_prototype": r.text(t, "typeof directConcise.prototype"),
			"concise_call":      r.text(t, "String(directConcise())"),
			"concise_construct": r.text(t, "try{new directConcise();'survived'}catch(e){e.toString()}"),
			"regular_prototype": r.text(t, "typeof directRegular.prototype"),
			"regular_construct": r.text(t, "new directRegular() instanceof directRegular"),
		})
	})

	t.Run("script_metadata", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		script, err := r.ctx.CompileWithOrigin(r.scope, "\n\n(function originFunction(){ return 1; })",
			&gov8.Origin{ResourceName: "origin-file.js", LineOffset: 10, ColumnOffset: 20, ScriptID: 777, SourceMapURL: "origin-file.map"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = script.Close() }()
		value, err := script.Run(r.scope, nil)
		if err != nil {
			t.Fatalf("origin script Run: %v", err)
		}
		function, ok, err := gov8.AsFunction(value, r.ctx)
		if err != nil || !ok {
			t.Fatalf("origin AsFunction = %v, %v", ok, err)
		}
		line, _, _ := function.ScriptLineNumber()
		column, _, _ := function.ScriptColumnNumber()
		functionID, _ := function.ScriptID()
		scriptID, _ := script.ID()
		origin, _ := function.ScriptOrigin()
		resource, _ := origin.ResourceName.ToString(r.ctx)
		sourceMap, _ := origin.SourceMapURL.ToString(r.ctx)

		sourceURLScript, err := r.ctx.Compile(r.scope, "(function sourceUrlFunction(){})\n//# sourceURL=virtual-source.js", nil)
		if err != nil {
			t.Fatalf("sourceURL Compile: %v", err)
		}
		defer func() { _ = sourceURLScript.Close() }()
		sourceURLValue, err := sourceURLScript.Run(r.scope, nil)
		if err != nil {
			t.Fatalf("sourceURL Run: %v", err)
		}
		sourceURLFunction, ok, err := gov8.AsFunction(sourceURLValue, r.ctx)
		if err != nil || !ok {
			t.Fatalf("sourceURL AsFunction = %v, %v", ok, err)
		}
		sourceURLOrigin, _ := sourceURLFunction.ScriptOrigin()
		sourceURLResource, _ := sourceURLOrigin.ResourceName.ToString(r.ctx)
		sourceURLID, _ := sourceURLScript.ID()

		native, _ := r.iso.NewFunction(r.scope, r.ctx, noop, nil)
		nativeLine, nativeLineOK, _ := native.ScriptLineNumber()
		nativeColumn, nativeColumnOK, _ := native.ScriptColumnNumber()
		nativeID, _ := native.ScriptID()
		nativeOrigin, _ := native.ScriptOrigin()
		nativeResource := "<none>"
		if nativeOrigin.HasResourceName {
			nativeResource, _ = nativeOrigin.ResourceName.ToString(r.ctx)
		}
		nativeSourceMap := "<none>"
		if nativeOrigin.HasSourceMapURL {
			nativeSourceMap, _ = nativeOrigin.SourceMapURL.ToString(r.ctx)
		}
		if nativeLineOK || nativeColumnOK {
			t.Fatal("native metadata unexpectedly present")
		}
		compare(t, fixtures, prefix+"script_metadata", map[string]any{
			"line": line, "column": column, "function_id_matches_script": functionID == scriptID,
			"origin_id_matches_function": origin.ScriptID == functionID, "resource_name": resource,
			"source_map_url": sourceMap, "source_url_resource_name": sourceURLResource,
			"source_url_id_matches_script": sourceURLOrigin.ScriptID == sourceURLID,
			"native_line":                  nativeLine, "native_column": nativeColumn, "native_script_id": nativeID,
			"native_resource_name": nativeResource, "native_source_map_url": nativeSourceMap,
		})
	})

	t.Run("bound_construct_semantics", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		bound := r.function(t, "globalThis.Base=function Base(a,b){this.args=a+':'+b;this.nt=new.target.name};globalThis.Bound=Base.bind({ignored:true},'A');Bound")
		argument, _ := r.scope.NewString("B")
		instance, ok, err := bound.NewInstance(r.scope, argument)
		if err != nil || !ok {
			t.Fatalf("NewInstance = %v, %v", ok, err)
		}
		r.setGlobal(t, "hostBoundInstance", instance.Value)
		compare(t, fixtures, prefix+"bound_construct_semantics", map[string]any{
			"js_construct":              r.text(t, "let x=new Bound('B');x.args+':'+x.nt"),
			"host_construct":            r.text(t, "hostBoundInstance.args+':'+hostBoundInstance.nt"),
			"instanceof_target":         r.text(t, "hostBoundInstance instanceof Base"),
			"instanceof_bound":          r.text(t, "hostBoundInstance instanceof Bound"),
			"reflect_custom_new_target": r.text(t, "function Alternate(){};let y=Reflect.construct(Bound,['B'],Alternate);y.args+':'+y.nt+':'+(Object.getPrototypeOf(y)===Alternate.prototype)"),
		})
	})

	t.Run("side_effect_policies_metadata", func(t *testing.T) {
		// The fixture's observable booleans require Runtime.evaluate with
		// throwOnSideEffect. gov8 has no Inspector family yet. Validate all three
		// metadata values are accepted without manufacturing an inspector shim.
		r := newRuntime(t)
		defer r.close(t)
		for _, sideEffectType := range []gov8.SideEffectType{
			gov8.SideEffectHasSideEffect,
			gov8.SideEffectHasNoSideEffect,
			gov8.SideEffectHasSideEffectToReceiver,
		} {
			if _, err := r.iso.FunctionBuilder(noop).SideEffectType(sideEffectType).Build(r.scope, r.ctx); err != nil {
				t.Fatalf("SideEffectType(%d): %v", sideEffectType, err)
			}
		}
		if _, err := r.iso.NewFunctionTemplate(r.scope, noop, &gov8.FunctionOptions{SideEffectType: gov8.SideEffectHasNoSideEffect}); err != nil {
			t.Fatal(err)
		}
		t.Log("oracle-only observation: throwOnSideEffect requires the not-yet-bound Inspector family")
	})

	t.Run("code_cache_roundtrip", func(t *testing.T) {
		const source = "return left * 10 + right;"
		params := []string{"left", "right"}
		producer := newRuntime(t)
		function, rejected, err := producer.ctx.CompileFunctionAdvanced(producer.scope, source, params, nil, nil)
		if err != nil || rejected {
			t.Fatalf("compile producer = %v, %v", rejected, err)
		}
		cache, err := function.CreateCodeCache()
		if err != nil {
			t.Fatal(err)
		}
		producer.close(t)

		consumer := newRuntime(t)
		defer consumer.close(t)
		function, rejected, err = consumer.ctx.CompileFunctionAdvanced(consumer.scope, source, params, cache, nil)
		compiled := err == nil
		value := int64(-1)
		if compiled {
			left, _ := consumer.scope.Int32(4)
			right, _ := consumer.scope.Int32(2)
			result, ok, callErr := function.Call(consumer.scope, undefined(t, consumer.scope), left, right)
			if callErr == nil && ok {
				value, _, _ = result.IntegerValue(consumer.ctx)
			}
		}
		compare(t, fixtures, prefix+"code_cache_roundtrip", map[string]any{
			"cache_non_empty": cache.Len() > 0, "consume_compiles": compiled,
			"cache_rejected": rejected, "call_value": value,
		})
	})
}
