//go:build windows && amd64

package templateaccessornamekeysconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gov8 "github.com/maclof/gov8"
)

var expectedIDs = []string{
	"template-accessor-name-keys/function/accessor_property",
	"template-accessor-name-keys/object/accessor_property",
	"template-accessor-name-keys/object/native_data_property_wrappers",
	"template-accessor-name-keys/lifecycle/retention_post_publication",
	"template-accessor-name-keys/duplicate/replacement",
}

type fixtureLine struct {
	Check   string         `json:"check"`
	OK      bool           `json:"ok"`
	Value   map[string]any `json:"value"`
	Summary *struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) []fixtureLine {
	t.Helper()
	path := filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-template-accessor-name-keys-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	var lines []fixtureLine
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != len(expectedIDs)+1 {
		t.Fatalf("fixture lines = %d, want %d", len(lines), len(expectedIDs)+1)
	}
	for index, id := range expectedIDs {
		if lines[index].Check != id || !lines[index].OK || lines[index].Summary != nil {
			t.Fatalf("fixture line %d = %+v, want successful %s", index, lines[index], id)
		}
	}
	summary := lines[len(expectedIDs)].Summary
	if summary == nil || summary.Total != 5 || summary.Passed != 5 || summary.Failed != 0 || lines[len(expectedIDs)].Check != "" {
		t.Fatalf("fixture summary = %+v", lines[len(expectedIDs)])
	}
	return lines[:len(expectedIDs)]
}

func compare(t *testing.T, line fixtureLine, got map[string]any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(line.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", line.Check, gotJSON, wantJSON)
	}
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

func mustString(t *testing.T, scope *gov8.Scope, text string) gov8.Value {
	t.Helper()
	value, err := scope.NewString(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustInt(t *testing.T, scope *gov8.Scope, value int32) gov8.Value {
	t.Helper()
	result, err := scope.Int32(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustSymbol(t *testing.T, scope *gov8.Scope, text string) *gov8.Symbol {
	t.Helper()
	description := mustString(t, scope, text)
	symbol, err := scope.NewSymbol(description)
	if err != nil {
		t.Fatal(err)
	}
	return symbol
}

func attributes(attr gov8.PropertyAttribute) map[string]any {
	return map[string]any{
		"bits":        int(attr),
		"read_only":   attr&gov8.AttrReadOnly != 0,
		"dont_enum":   attr&gov8.AttrDontEnum != 0,
		"dont_delete": attr&gov8.AttrDontDelete != 0,
	}
}

func evalValue(t *testing.T, r *runtimeState, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Error(err)
		}
	}()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func evalText(t *testing.T, r *runtimeState, source string) string {
	t.Helper()
	text, err := evalValue(t, r, source).ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func expose(t *testing.T, r *runtimeState, name string, value gov8.Value) {
	t.Helper()
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, name, value); err != nil || !ok {
		t.Fatalf("set global %s = %v, %v", name, ok, err)
	}
}

func propertyAttributes(t *testing.T, r *runtimeState, object *gov8.Object, key gov8.Value) gov8.PropertyAttribute {
	t.Helper()
	attr, present, err := object.GetPropertyAttributes(r.scope, r.ctx, key)
	if err != nil || !present {
		t.Fatalf("attributes = %d, %v, %v", attr, present, err)
	}
	return attr
}

type functionCounters struct {
	gets   int
	sets   int
	last   int64
	getAlt bool
}

func newAccessorGetter(counters *functionCounters) gov8.FunctionCallback {
	return func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		counters.gets++
		value := int32(41)
		if counters.getAlt {
			value = 42
		}
		_ = rv.SetInt32(value)
	}
}

func newAccessorSetter(counters *functionCounters) gov8.FunctionCallback {
	return func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		counters.sets++
		value, err := args.Get(0)
		if err != nil {
			panic(err)
		}
		counters.last, _, err = cs.IntegerValue(value)
		if err != nil {
			panic(err)
		}
	}
}

func callbackKey(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments) (string, string) {
	property, err := args.Property()
	if err != nil {
		panic(err)
	}
	isSymbol, err := property.IsSymbol()
	if err != nil {
		panic(err)
	}
	if !isSymbol {
		text, err := cs.ToString(property)
		if err != nil {
			panic(err)
		}
		return "string", text
	}
	symbol, err := gov8.AsSymbol(property)
	if err != nil {
		panic(err)
	}
	description, err := symbol.Description(cs.Scope())
	if err != nil {
		panic(err)
	}
	text, err := cs.ToString(description)
	if err != nil {
		panic(err)
	}
	return "symbol", text
}

type nativeState struct {
	log   []string
	value int64
}

func nativeGetter(state *nativeState) gov8.AccessorGetterCallback {
	return func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
		kind, text := callbackKey(cs, args)
		data, err := args.Data()
		if err != nil {
			panic(err)
		}
		undefined, err := data.IsUndefined()
		if err != nil {
			panic(err)
		}
		dataKind := "undefined"
		result := state.value
		if text == "native-simple" {
			result = 61
		}
		if !undefined {
			dataKind = "int32"
			result, _, err = cs.IntegerValue(data)
			if err != nil {
				panic(err)
			}
		}
		state.log = append(state.log, fmt.Sprintf("get:%s:%s:data=%s", kind, text, dataKind))
		_ = rv.SetFloat64(float64(result))
	}
}

func nativeSetter(state *nativeState) gov8.AccessorSetterCallback {
	return func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, value gov8.Value) {
		kind, text := callbackKey(cs, args)
		data, err := args.Data()
		if err != nil {
			panic(err)
		}
		undefined, err := data.IsUndefined()
		if err != nil {
			panic(err)
		}
		dataKind := "undefined"
		if !undefined {
			dataKind = "int32"
		}
		integer, _, err := cs.IntegerValue(value)
		if err != nil {
			panic(err)
		}
		state.log = append(state.log, fmt.Sprintf("set:%s:%s:%d:data=%s", kind, text, integer, dataKind))
		state.value = integer
	}
}

func accessorPropertyCheck(t *testing.T, line fixtureLine, function bool) {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counters := &functionCounters{last: -1}
	getter, err := r.iso.NewFunctionTemplate(r.scope, newAccessorGetter(counters), nil)
	if err != nil {
		t.Fatal(err)
	}
	setter, err := r.iso.NewFunctionTemplate(r.scope, newAccessorSetter(counters), nil)
	if err != nil {
		t.Fatal(err)
	}
	stringKey := mustString(t, r.scope, "string-accessor")
	symbolKey := mustSymbol(t, r.scope, "symbol-accessor")
	var object *gov8.Object
	if function {
		template, err := r.iso.NewFunctionTemplate(r.scope, newAccessorGetter(counters), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorPropertyName(stringKey, getter, setter, gov8.AttrNone); err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorPropertyName(symbolKey.Value, getter, setter, gov8.AttrDontEnum|gov8.AttrDontDelete); err != nil {
			t.Fatal(err)
		}
		fn, err := template.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		object, err = gov8.AsObject(fn.Value)
		if err != nil {
			t.Fatal(err)
		}
		expose(t, r, "fnValue", fn.Value)
		expose(t, r, "fnSymbol", symbolKey.Value)
	} else {
		template, err := r.iso.NewObjectTemplate(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorPropertyName(stringKey, getter, setter, gov8.AttrNone); err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorPropertyName(symbolKey.Value, getter, setter, gov8.AttrDontEnum|gov8.AttrDontDelete); err != nil {
			t.Fatal(err)
		}
		var ok bool
		object, ok, err = template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("NewInstance = %v, %v", ok, err)
		}
		expose(t, r, "objectValue", object.Value)
		expose(t, r, "objectSymbol", symbolKey.Value)
	}
	prefix := "object"
	lastSet := int64(63)
	if function {
		prefix = "fn"
		lastSet = 53
	}
	reads := evalText(t, r, fmt.Sprintf("`${%sValue['string-accessor']}|${%sValue[%sSymbol]}`", prefix, prefix, prefix))
	writes := evalText(t, r, fmt.Sprintf("`${Reflect.set(%sValue,'string-accessor',%d)}|${Reflect.set(%sValue,%sSymbol,%d)}`", prefix, lastSet-1, prefix, prefix, lastSet))
	descriptor := evalText(t, r, fmt.Sprintf("(()=>{const d=Object.getOwnPropertyDescriptor(%sValue,%sSymbol);return `${typeof d.get}|${typeof d.set}|${d.enumerable}|${d.configurable}`})()", prefix, prefix))
	compare(t, line, map[string]any{
		"reads": reads, "writes": writes, "descriptor": descriptor,
		"attributes":  attributes(propertyAttributes(t, r, object, symbolKey.Value)),
		"getter_hits": counters.gets, "setter_hits": counters.sets, "last_set": counters.last,
	})
}

func TestRustOracleFixture(t *testing.T) {
	fixtures := fixture(t)

	t.Run("function_accessor_property", func(t *testing.T) {
		accessorPropertyCheck(t, fixtures[0], true)
	})
	t.Run("object_accessor_property", func(t *testing.T) {
		accessorPropertyCheck(t, fixtures[1], false)
	})

	t.Run("native_data_property_wrappers", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		state := &nativeState{value: 17}
		getter := nativeGetter(state)
		setter := nativeSetter(state)
		template, err := r.iso.NewObjectTemplate(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		simpleSymbol := mustSymbol(t, r.scope, "native-simple")
		controlKey := mustString(t, r.scope, "native-control")
		dataSymbol := mustSymbol(t, r.scope, "native-data")
		if err := template.SetAccessorWithSetterName(simpleSymbol.Value, getter, nil); err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorWithSetterName(controlKey, getter, setter); err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorWithConfigurationName(dataSymbol.Value, gov8.AccessorConfiguration{
			Getter: getter, Setter: setter, Data: mustInt(t, r.scope, 73),
			Attribute: gov8.AttrDontEnum | gov8.AttrDontDelete,
		}); err != nil {
			t.Fatal(err)
		}
		object, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("NewInstance = %v, %v", ok, err)
		}
		expose(t, r, "nativeObject", object.Value)
		expose(t, r, "simpleSymbol", simpleSymbol.Value)
		expose(t, r, "dataSymbol", dataSymbol.Value)
		reads := evalText(t, r, "`${nativeObject[simpleSymbol]}|${nativeObject['native-control']}|${nativeObject[dataSymbol]}`")
		writes := evalText(t, r, "`${Reflect.set(nativeObject,'native-control',81)}|${Reflect.set(nativeObject,dataSymbol,82)}`")
		afterWrite := evalText(t, r, "`${nativeObject['native-control']}|${nativeObject[dataSymbol]}`")
		descriptors := evalText(t, r, "(()=>{const a=Object.getOwnPropertyDescriptor(nativeObject,simpleSymbol),b=Object.getOwnPropertyDescriptor(nativeObject,dataSymbol);return `${typeof a.get}|${typeof a.set}|${typeof b.get}|${typeof b.set}`})()")
		compare(t, fixtures[2], map[string]any{
			"reads": reads, "writes": writes, "after_write": afterWrite, "descriptors": descriptors,
			"data_attributes": attributes(propertyAttributes(t, r, object, dataSymbol.Value)), "callback_log": state.log,
		})
	})

	t.Run("retention_post_publication", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		state := &nativeState{value: 17}
		counters := &functionCounters{last: -1}
		template, err := r.iso.NewObjectTemplate(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		keyScope, err := r.iso.NewScope()
		if err != nil {
			t.Fatal(err)
		}
		retainedNative := mustSymbol(t, keyScope, "retained-native")
		if err := template.SetAccessorWithConfigurationName(retainedNative.Value, gov8.AccessorConfiguration{
			Getter: nativeGetter(state), Data: mustInt(t, keyScope, 91),
		}); err != nil {
			t.Fatal(err)
		}
		retainedAccessor := mustSymbol(t, keyScope, "retained-accessor")
		getter, err := r.iso.NewFunctionTemplate(keyScope, newAccessorGetter(counters), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorPropertyName(retainedAccessor.Value, getter, nil, gov8.AttrNone); err != nil {
			t.Fatal(err)
		}
		if err := keyScope.Close(); err != nil {
			t.Fatal(err)
		}
		if err := r.iso.LowMemoryNotification(); err != nil {
			t.Fatal(err)
		}
		first, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("first = %v, %v", ok, err)
		}
		lateNative := mustSymbol(t, r.scope, "late-native")
		if err := template.SetAccessorWithSetterName(lateNative.Value, nativeGetter(state), nil); err != nil {
			t.Fatal(err)
		}
		lateAccessor := mustSymbol(t, r.scope, "late-accessor")
		lateGetter, err := r.iso.NewFunctionTemplate(r.scope, newAccessorGetter(counters), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorPropertyName(lateAccessor.Value, lateGetter, nil, gov8.AttrNone); err != nil {
			t.Fatal(err)
		}
		second, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("second = %v, %v", ok, err)
		}
		expose(t, r, "retainedObject", second.Value)
		retained := evalText(t, r, "(()=>Object.getOwnPropertySymbols(retainedObject).sort((a,b)=>a.description.localeCompare(b.description)).map(s=>`${s.description}:${retainedObject[s]}`).join('|'))()")

		functionTemplate, err := r.iso.NewFunctionTemplate(r.scope, newAccessorGetter(counters), nil)
		if err != nil {
			t.Fatal(err)
		}
		function, err := functionTemplate.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		lateFunction := mustSymbol(t, r.scope, "late-function")
		if err := functionTemplate.SetAccessorPropertyName(lateFunction.Value, lateGetter, nil, gov8.AttrNone); err != nil {
			t.Fatal(err)
		}
		repeated, err := functionTemplate.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		functionObject, err := gov8.AsObject(function.Value)
		if err != nil {
			t.Fatal(err)
		}
		repeatedSame, err := function.Value.StrictEquals(repeated.Value)
		if err != nil {
			t.Fatal(err)
		}
		firstHasNative, err := first.HasOwnProperty(r.scope, r.ctx, lateNative.Value, nil)
		if err != nil {
			t.Fatal(err)
		}
		secondHasNative, err := second.HasOwnProperty(r.scope, r.ctx, lateNative.Value, nil)
		if err != nil {
			t.Fatal(err)
		}
		firstHasAccessor, err := first.HasOwnProperty(r.scope, r.ctx, lateAccessor.Value, nil)
		if err != nil {
			t.Fatal(err)
		}
		secondHasAccessor, err := second.HasOwnProperty(r.scope, r.ctx, lateAccessor.Value, nil)
		if err != nil {
			t.Fatal(err)
		}
		functionHasAccessor, err := functionObject.HasOwnProperty(r.scope, r.ctx, lateFunction.Value, nil)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures[3], map[string]any{
			"retained": retained, "first_has_late_native": firstHasNative,
			"second_has_late_native": secondHasNative, "first_has_late_accessor": firstHasAccessor,
			"second_has_late_accessor": secondHasAccessor, "function_has_late_accessor": functionHasAccessor,
			"repeated_function_same": repeatedSame,
		})
	})

	t.Run("duplicate_replacement", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		state := &nativeState{value: 17}
		base := &functionCounters{last: -1}
		alt := &functionCounters{last: -1, getAlt: true}
		getter, err := r.iso.NewFunctionTemplate(r.scope, newAccessorGetter(base), nil)
		if err != nil {
			t.Fatal(err)
		}
		getterAlt, err := r.iso.NewFunctionTemplate(r.scope, newAccessorGetter(alt), nil)
		if err != nil {
			t.Fatal(err)
		}
		template, err := r.iso.NewObjectTemplate(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		accessorKey := mustSymbol(t, r.scope, "duplicate-accessor")
		if err := template.SetAccessorPropertyName(accessorKey.Value, getter, nil, gov8.AttrDontEnum); err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorPropertyName(accessorKey.Value, getterAlt, nil, gov8.AttrDontDelete); err != nil {
			t.Fatal(err)
		}
		nativeKey := mustSymbol(t, r.scope, "duplicate-native")
		if err := template.SetAccessorWithSetterName(nativeKey.Value, nativeGetter(state), nil); err != nil {
			t.Fatal(err)
		}
		if err := template.SetAccessorWithConfigurationName(nativeKey.Value, gov8.AccessorConfiguration{
			Getter: nativeGetter(state), Data: mustInt(t, r.scope, 99), Attribute: gov8.AttrDontEnum,
		}); err != nil {
			t.Fatal(err)
		}
		functionTemplate, err := r.iso.NewFunctionTemplate(r.scope, newAccessorGetter(base), nil)
		if err != nil {
			t.Fatal(err)
		}
		functionKey := mustSymbol(t, r.scope, "duplicate-function")
		if err := functionTemplate.SetAccessorPropertyName(functionKey.Value, getter, nil, gov8.AttrDontEnum); err != nil {
			t.Fatal(err)
		}
		if err := functionTemplate.SetAccessorPropertyName(functionKey.Value, getterAlt, nil, gov8.AttrDontDelete); err != nil {
			t.Fatal(err)
		}
		object, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("NewInstance = %v, %v", ok, err)
		}
		function, err := functionTemplate.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		functionObject, err := gov8.AsObject(function.Value)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures[4], map[string]any{
			"object_accessor_value":        integerProperty(t, r, object, accessorKey.Value),
			"object_accessor_attributes":   attributes(propertyAttributes(t, r, object, accessorKey.Value)),
			"object_native_value":          integerProperty(t, r, object, nativeKey.Value),
			"object_native_attributes":     attributes(propertyAttributes(t, r, object, nativeKey.Value)),
			"function_accessor_value":      integerProperty(t, r, functionObject, functionKey.Value),
			"function_accessor_attributes": attributes(propertyAttributes(t, r, functionObject, functionKey.Value)),
		})
	})
}

func integerProperty(t *testing.T, r *runtimeState, object *gov8.Object, key gov8.Value) int64 {
	t.Helper()
	value, err := object.GetByKey(r.scope, r.ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	integer, ok, err := value.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("IntegerValue = %d, %v, %v", integer, ok, err)
	}
	return integer
}

func TestNilAccessorTemplatesRejectedSafely(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)
	key := mustSymbol(t, r.scope, "none")
	objectTemplate, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := objectTemplate.SetAccessorPropertyName(key.Value, nil, nil, gov8.AttrNone); err == nil {
		t.Fatal("ObjectTemplate nil/nil accessor accepted")
	}
	functionTemplate, err := r.iso.NewFunctionTemplate(r.scope, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := functionTemplate.SetAccessorPropertyName(key.Value, nil, nil, gov8.AttrNone); err == nil {
		t.Fatal("FunctionTemplate nil/nil accessor accepted")
	}
}
