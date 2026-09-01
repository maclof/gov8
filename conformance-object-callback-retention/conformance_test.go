//go:build windows && amd64

package objectcallbackretentionconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gov8 "gov8"
)

type fixtureLine struct {
	Check string          `json:"check"`
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
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

func fixtures(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-object-callback-retention-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	result := map[string]json.RawMessage{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(s.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Check != "" {
			if !line.OK {
				t.Fatalf("Rust fixture check %s failed", line.Check)
			}
			result[line.Check] = line.Value
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) != 6 {
		t.Fatalf("fixture checks=%d, want 6", len(result))
	}
	return result
}

func compare(t *testing.T, fixture map[string]json.RawMessage, check string, got any) {
	t.Helper()
	want, ok := fixture[check]
	if !ok {
		t.Fatalf("missing fixture %s", check)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gv, wv any
	if err := json.Unmarshal(gotJSON, &gv); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wv); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ = json.Marshal(gv)
	wantJSON, _ := json.Marshal(wv)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s differs\ngot  %s\nwant %s", check, gotJSON, wantJSON)
	}
}

func mustString(t *testing.T, r *runtime, text string) gov8.Value {
	t.Helper()
	v, err := r.scope.NewString(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustInt(t *testing.T, r *runtime, n int32) gov8.Value {
	t.Helper()
	v, err := r.scope.Int32(n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustObject(t *testing.T, r *runtime) *gov8.Object {
	t.Helper()
	o, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func setGlobal(t *testing.T, r *runtime, name string, value gov8.Value) {
	t.Helper()
	g, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := g.SetByName(r.scope, r.ctx, name, value); err != nil || !ok {
		t.Fatalf("set global %s: ok=%v err=%v", name, ok, err)
	}
}

func eval(t *testing.T, r *runtime, source string, tc *gov8.TryCatch) (gov8.Value, bool) {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, tc)
	if err != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(r.scope, tc)
	return v, err == nil
}

func evalText(t *testing.T, r *runtime, source string) string {
	t.Helper()
	v, ok := eval(t, r, source, nil)
	if !ok {
		t.Fatalf("eval %q failed", source)
	}
	s, err := v.ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func evalInt(t *testing.T, r *runtime, source string) int64 {
	t.Helper()
	v, ok := eval(t, r, source, nil)
	if !ok {
		t.Fatalf("eval %q failed", source)
	}
	n, ok, err := v.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("IntegerValue: ok=%v err=%v", ok, err)
	}
	return n
}

func attributes(a gov8.PropertyAttribute) map[string]any {
	return map[string]any{
		"bits":        int(a),
		"read_only":   a&gov8.AttrReadOnly != 0,
		"dont_enum":   a&gov8.AttrDontEnum != 0,
		"dont_delete": a&gov8.AttrDontDelete != 0,
	}
}

func dataObject(t *testing.T, r *runtime, scope *gov8.Scope, marker int32) gov8.Value {
	t.Helper()
	o, err := scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	m, err := scope.Int32(marker)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := o.SetByName(scope, r.ctx, "marker", m); err != nil || !ok {
		t.Fatal(err)
	}
	if ok, err := o.SetByName(scope, r.ctx, "self", o.Value); err != nil || !ok {
		t.Fatal(err)
	}
	return o.Value
}

func callbackObservations(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, wantKey string, wantHolder int64) (marker int64, self, key, holder bool) {
	data, err := args.Data()
	if err != nil {
		panic(err)
	}
	markerValue, ok, err := cs.ObjectGet(data, "marker")
	if err != nil || !ok {
		panic("marker")
	}
	marker, ok, err = cs.IntegerValue(markerValue)
	if err != nil || !ok {
		panic("marker integer")
	}
	selfValue, ok, err := cs.ObjectGet(data, "self")
	if err != nil || !ok {
		panic("self")
	}
	self, err = data.StrictEquals(selfValue)
	if err != nil {
		panic(err)
	}
	property, err := args.Property()
	if err != nil {
		panic(err)
	}
	propertyText, err := cs.ToString(property)
	if err != nil {
		panic(err)
	}
	key = propertyText == wantKey
	holderValue, err := args.Holder()
	if err != nil {
		panic(err)
	}
	holderTag, present, err := cs.ObjectGet(holderValue.Value, "holderTag")
	if err != nil || !present {
		return marker, self, key, wantHolder == 0
	}
	holderInt, present, err := cs.IntegerValue(holderTag)
	if err != nil || !present {
		panic("holder tag")
	}
	holder = holderInt == wantHolder
	return
}

func TestConformanceObjectCallbackRetention(t *testing.T) {
	fixture := fixtures(t)

	t.Run("accessor configuration", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		object := mustObject(t, r)
		_, _ = object.SetByName(r.scope, r.ctx, "holderTag", mustInt(t, r, 99))
		setGlobal(t, r, "configuredObject", object.Value)
		state := int64(5)
		getHits, setHits := 0, 0
		getThrow, setThrow := []bool{}, []bool{}
		dataSelf, keyMatches, holderMatches := true, true, true
		inner, _ := r.iso.NewScope()
		data := dataObject(t, r, inner, 73)
		install, err := object.SetAccessorWithConfiguration(r.scope, r.ctx, mustString(t, r, "configured"), gov8.AccessorConfiguration{
			Getter: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
				getHits++
				marker, self, key, holder := callbackObservations(cs, args, "configured", 99)
				dataSelf = dataSelf && self && marker == 73
				keyMatches = keyMatches && key
				holderMatches = holderMatches && holder
				getThrow = append(getThrow, args.ShouldThrowOnError())
				_ = rv.SetInt32(int32(state))
			},
			Setter: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, value gov8.Value) {
				setHits++
				marker, self, key, holder := callbackObservations(cs, args, "configured", 99)
				dataSelf = dataSelf && self && marker == 73
				keyMatches = keyMatches && key
				holderMatches = holderMatches && holder
				setThrow = append(setThrow, args.ShouldThrowOnError())
				state, _, _ = cs.IntegerValue(value)
			}, Data: data, Attribute: gov8.AttrDontEnum | gov8.AttrDontDelete,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := inner.Close(); err != nil {
			t.Fatal(err)
		}
		if err := r.iso.LowMemoryNotification(); err != nil {
			t.Fatal(err)
		}
		first := evalInt(t, r, "configuredObject.configured")
		reflectSet := evalText(t, r, "Reflect.set(configuredObject, 'configured', 11)")
		strictSet := evalInt(t, r, "(() => { 'use strict'; return (configuredObject.configured = 12); })()")
		final := evalInt(t, r, "configuredObject.configured")
		attr, _, _ := object.GetPropertyAttributes(r.scope, r.ctx, mustString(t, r, "configured"))
		compare(t, fixture, "object-callback-retention/accessor_configuration", map[string]any{
			"install": install, "first": first, "reflect_set": reflectSet, "strict_set": strictSet,
			"final_value": final, "get_hits": getHits, "set_hits": setHits,
			"getter_should_throw": getThrow, "setter_should_throw": setThrow,
			"data_self_identity": dataSelf, "key_matches": keyMatches, "holder_matches": holderMatches,
			"attributes": attributes(attr), "enumerable": evalText(t, r, "Object.keys(configuredObject).includes('configured')"),
			"delete_result": evalText(t, r, "Reflect.deleteProperty(configuredObject, 'configured')"),
		})
	})

	t.Run("accessor replacement read only", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		object := mustObject(t, r)
		setGlobal(t, r, "replacementObject", object.Value)
		markers := []int64{}
		getter := func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			marker, _, _, _ := callbackObservations(cs, args, "replaceable", 0)
			markers = append(markers, marker)
			_ = rv.SetInt32(int32(marker))
		}
		key := mustString(t, r, "replaceable")
		first, err := object.SetAccessorWithConfiguration(r.scope, r.ctx, key, gov8.AccessorConfiguration{Getter: getter, Data: dataObject(t, r, r.scope, 1)})
		if err != nil {
			t.Fatal(err)
		}
		second, err := object.SetAccessorWithConfiguration(r.scope, r.ctx, key, gov8.AccessorConfiguration{Getter: getter, Data: dataObject(t, r, r.scope, 2)})
		if err != nil {
			t.Fatal(err)
		}
		afterReinstall := evalInt(t, r, "replacementObject.replaceable")
		markersAfterReinstall := append([]int64(nil), markers...)
		ordinary, err := object.DefineOwnProperty(r.scope, r.ctx, key, mustInt(t, r, 88), gov8.AttrNone)
		if err != nil {
			t.Fatal(err)
		}
		markersAfterDefine := append([]int64(nil), markers...)
		afterOrdinary := evalInt(t, r, "replacementObject.replaceable")
		markersAfterOrdinary := append([]int64(nil), markers...)

		readOnly := mustObject(t, r)
		setGlobal(t, r, "readOnlyObject", readOnly.Value)
		setterHits := 0
		ro, err := readOnly.SetAccessorWithConfiguration(r.scope, r.ctx, mustString(t, r, "ro"), gov8.AccessorConfiguration{
			Getter: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
				_ = rv.SetInt32(3)
			},
			Setter: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, value gov8.Value) { setterHits++ },
			Data:   dataObject(t, r, r.scope, 3), Attribute: gov8.AttrReadOnly,
		})
		if err != nil {
			t.Fatal(err)
		}
		reflectWrite := evalText(t, r, "Reflect.set(readOnlyObject, 'ro', 10)")
		tc, _ := r.iso.NewTryCatch()
		strict, strictPresent := eval(t, r, "'use strict'; readOnlyObject.ro = 10", tc)
		_ = strict
		caught, _ := tc.HasCaught()
		exception, _ := tc.ExceptionText(r.scope, r.ctx)
		_ = tc.Close()
		compare(t, fixture, "object-callback-retention/accessor_replacement_read_only", map[string]any{
			"first_install": first, "second_install": second, "after_reinstall": afterReinstall,
			"markers_after_reinstall": markersAfterReinstall, "ordinary_replacement": ordinary,
			"markers_after_define": markersAfterDefine, "after_ordinary": afterOrdinary,
			"markers_after_ordinary": markersAfterOrdinary, "read_only_install": ro,
			"read_only_reflect_set": reflectWrite, "read_only_setter_hits": setterHits,
			"strict_result_none": !strictPresent, "strict_caught": caught, "strict_exception": exception,
		})
	})

	t.Run("lazy data attributes", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		object := mustObject(t, r)
		_, _ = object.SetByName(r.scope, r.ctx, "holderTag", mustInt(t, r, 44))
		setGlobal(t, r, "lazyObject", object.Value)
		hits := 0
		dataSelf, keyMatches, holderMatches := true, true, true
		throwFlags := []bool{}
		key := mustString(t, r, "retained")
		inner, _ := r.iso.NewScope()
		data := dataObject(t, r, inner, 55)
		install, err := object.SetLazyDataPropertyWithData(r.scope, r.ctx, key,
			func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
				hits++
				marker, self, named, holder := callbackObservations(cs, args, "retained", 44)
				dataSelf = dataSelf && self && marker == 55
				keyMatches = keyMatches && named
				holderMatches = holderMatches && holder
				throwFlags = append(throwFlags, args.ShouldThrowOnError())
				value, _ := args.Data()
				_ = rv.Set(value)
			}, data, gov8.AttrReadOnly|gov8.AttrDontEnum|gov8.AttrDontDelete,
			gov8.SideEffectHasNoSideEffect, gov8.SideEffectHasSideEffectToReceiver)
		if err != nil {
			t.Fatal(err)
		}
		_ = inner.Close()
		beforeHits := hits
		before, _, _ := object.GetPropertyAttributes(r.scope, r.ctx, key)
		afterAttributeHits := hits
		_ = r.iso.LowMemoryNotification()
		first, _ := object.GetByKey(r.scope, r.ctx, key)
		second, _ := object.GetByKey(r.scope, r.ctx, key)
		firstObj, _ := gov8.AsObject(first)
		markerValue, _, _ := firstObj.GetByName(r.scope, r.ctx, "marker")
		marker, _, _ := markerValue.IntegerValue(r.ctx)
		same, _ := first.StrictEquals(second)
		after, _, _ := object.GetPropertyAttributes(r.scope, r.ctx, key)
		compare(t, fixture, "object-callback-retention/lazy_data_attributes", map[string]any{
			"install": install, "hits_before_attributes": beforeHits, "hits_after_attributes": afterAttributeHits,
			"attributes_before": attributes(before), "marker": marker, "first_second_identity": same,
			"hits_after_reads": hits, "data_self_identity": dataSelf, "key_matches": keyMatches,
			"holder_matches": holderMatches, "should_throw": throwFlags, "attributes_after": attributes(after),
			"descriptor_writable":     evalText(t, r, "Object.getOwnPropertyDescriptor(lazyObject, 'retained').writable"),
			"descriptor_enumerable":   evalText(t, r, "Object.getOwnPropertyDescriptor(lazyObject, 'retained').enumerable"),
			"descriptor_configurable": evalText(t, r, "Object.getOwnPropertyDescriptor(lazyObject, 'retained').configurable"),
			"reflect_set":             evalText(t, r, "Reflect.set(lazyObject, 'retained', 9)"),
			"reflect_delete":          evalText(t, r, "Reflect.deleteProperty(lazyObject, 'retained')"),
		})
	})

	t.Run("lazy side effect matrix", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		types := []struct {
			name string
			kind gov8.SideEffectType
		}{{"HasSideEffect", gov8.SideEffectHasSideEffect}, {"HasNoSideEffect", gov8.SideEffectHasNoSideEffect}, {"HasSideEffectToReceiver", gov8.SideEffectHasSideEffectToReceiver}}
		setters := []struct {
			name string
			kind gov8.SideEffectType
		}{{"HasSideEffect", gov8.SideEffectHasSideEffect}, {"HasSideEffectToReceiver", gov8.SideEffectHasSideEffectToReceiver}}
		cases := []map[string]any{}
		hits := 0
		allIdentity := true
		index := int32(0)
		for _, getter := range types {
			for _, setter := range setters {
				index++
				object := mustObject(t, r)
				key := mustString(t, r, "value")
				marker := index + (index - 1) // 1,3,5? Corrected below from oracle's enum encoding.
				marker = int32(getter.kind)*3 + int32(setter.kind) + 1
				data := dataObject(t, r, r.scope, marker)
				install, err := object.SetLazyDataPropertyWithData(r.scope, r.ctx, key,
					func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
						hits++
						m, self, _, _ := callbackObservations(cs, args, "value", 0)
						allIdentity = allIdentity && self
						_ = rv.SetInt32(int32(m))
					}, data, gov8.AttrNone, getter.kind, setter.kind)
				if err != nil {
					t.Fatal(err)
				}
				first, _ := object.GetByKey(r.scope, r.ctx, key)
				second, _ := object.GetByKey(r.scope, r.ctx, key)
				fv, _, _ := first.IntegerValue(r.ctx)
				sv, _, _ := second.IntegerValue(r.ctx)
				cases = append(cases, map[string]any{"getter": getter.name, "setter": setter.name, "install": install, "first": fv, "second": sv})
			}
		}
		compare(t, fixture, "object-callback-retention/lazy_side_effect_matrix", map[string]any{"cases": cases, "callback_hits": hits, "all_data_self_identity": allIdentity})
	})

	t.Run("lazy throw empty failure", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		object := mustObject(t, r)
		throwHits := 0
		throwKey := mustString(t, r, "throws")
		throwInstall, err := object.SetLazyDataProperty(r.scope, r.ctx, throwKey,
			func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
				throwHits++
				ex, err := cs.NewError("lazy boom")
				if err != nil || cs.ThrowException(ex) != nil {
					panic("throw")
				}
			})
		if err != nil {
			t.Fatal(err)
		}
		readThrow := func() (bool, bool, string) {
			tc, _ := r.iso.NewTryCatch()
			_, err := object.GetByKey(r.scope, r.ctx, throwKey)
			caught, _ := tc.HasCaught()
			text, _ := tc.ExceptionText(r.scope, r.ctx)
			_ = tc.Close()
			return err == nil, caught, text
		}
		firstPresent, firstCaught, firstException := readThrow()
		hitsAfterFirst := throwHits
		secondPresent, secondCaught, secondException := readThrow()
		hitsAfterSecond := throwHits
		tc, _ := r.iso.NewTryCatch()
		replace, replaceErr := object.DefineOwnProperty(r.scope, r.ctx, throwKey, mustInt(t, r, 9), gov8.AttrNone)
		replaceCaught, _ := tc.HasCaught()
		replaceException, _ := tc.ExceptionText(r.scope, r.ctx)
		_ = tc.Close()
		hitsAfterReplace := throwHits
		readAfterPresent, readAfterCaught, readAfterException := readThrow()

		emptyHits := 0
		emptyKey := mustString(t, r, "empty")
		emptyInstall, err := object.SetLazyDataProperty(r.scope, r.ctx, emptyKey,
			func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) { emptyHits++ })
		if err != nil {
			t.Fatal(err)
		}
		emptyFirst, _ := object.GetByKey(r.scope, r.ctx, emptyKey)
		emptySecond, _ := object.GetByKey(r.scope, r.ctx, emptyKey)
		firstUndefined, _ := emptyFirst.IsUndefined()
		secondUndefined, _ := emptySecond.IsUndefined()

		nonextensible := mustObject(t, r)
		setGlobal(t, r, "nonextensible", nonextensible.Value)
		_ = evalText(t, r, "Object.preventExtensions(nonextensible); 'ok'")
		accessorTC, _ := r.iso.NewTryCatch()
		accessorFailure, _ := nonextensible.SetAccessorWithConfiguration(r.scope, r.ctx, mustString(t, r, "a"), gov8.AccessorConfiguration{Getter: func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) {}})
		accessorCaught, _ := accessorTC.HasCaught()
		_ = accessorTC.Close()
		lazyTC, _ := r.iso.NewTryCatch()
		lazyFailure, _ := nonextensible.SetLazyDataProperty(r.scope, r.ctx, mustString(t, r, "l"), func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) {})
		lazyCaught, _ := lazyTC.HasCaught()
		_ = lazyTC.Close()

		var replaceValue any
		if replaceErr == nil {
			replaceValue = replace
		}
		compare(t, fixture, "object-callback-retention/lazy_throw_empty_failure", map[string]any{
			"throw_install": throwInstall, "first_present": firstPresent, "first_caught": firstCaught,
			"first_exception": firstException, "second_present": secondPresent, "second_caught": secondCaught,
			"second_exception": secondException, "hits_after_first": hitsAfterFirst, "hits_after_second": hitsAfterSecond,
			"hits_after_replace_attempt": hitsAfterReplace, "throw_hits": throwHits, "replace_after_throw": replaceValue,
			"replace_caught": replaceCaught, "replace_exception": replaceException,
			"read_after_replace_present": readAfterPresent, "read_after_replace_caught": readAfterCaught,
			"read_after_replace_exception": readAfterException, "empty_install": emptyInstall,
			"empty_first_undefined": firstUndefined, "empty_second_undefined": secondUndefined, "empty_hits": emptyHits,
			"accessor_nonextensible": accessorFailure, "accessor_nonextensible_caught": accessorCaught,
			"lazy_nonextensible": lazyFailure, "lazy_nonextensible_caught": lazyCaught,
		})
	})

	t.Run("template set with attr", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		returnValue := func(n int32) gov8.FunctionCallback {
			return func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) { _ = rv.SetInt32(n) }
		}
		nestedFunction, _ := r.iso.NewFunctionTemplate(r.scope, returnValue(42), nil)
		nestedObject, _ := r.iso.NewObjectTemplate(r.scope)
		_ = nestedObject.Set("nestedValue", mustInt(t, r, 81))
		objectTemplate, _ := r.iso.NewObjectTemplate(r.scope)
		_ = objectTemplate.SetWithAttr("primitive", mustString(t, r, "template-value"), gov8.AttrReadOnly|gov8.AttrDontEnum|gov8.AttrDontDelete)
		nestedFunctionData, _ := nestedFunction.Data()
		nestedObjectData, _ := nestedObject.Data()
		_ = objectTemplate.SetDataWithAttr("nestedFunction", nestedFunctionData, gov8.AttrDontEnum)
		_ = objectTemplate.SetDataWithAttr("nestedObject", nestedObjectData, gov8.AttrNone)
		rootFunctionTemplate, _ := r.iso.NewFunctionTemplate(r.scope, returnValue(10), nil)
		_ = rootFunctionTemplate.SetWithAttr("primitive", mustInt(t, r, 9), gov8.AttrDontEnum)
		_ = rootFunctionTemplate.SetDataWithAttr("nestedFunction", nestedFunctionData, gov8.AttrReadOnly|gov8.AttrDontEnum|gov8.AttrDontDelete)

		first, _, _ := objectTemplate.NewInstance(r.scope, r.ctx)
		second, _, _ := objectTemplate.NewInstance(r.scope, r.ctx)
		firstPrimitive, _, _ := first.GetByName(r.scope, r.ctx, "primitive")
		secondPrimitive, _, _ := second.GetByName(r.scope, r.ctx, "primitive")
		primitiveText, _ := firstPrimitive.ToString(r.ctx)
		primitiveShared, _ := firstPrimitive.StrictEquals(secondPrimitive)
		primitiveAttr, _, _ := first.GetPropertyAttributes(r.scope, r.ctx, mustString(t, r, "primitive"))
		nestedFnAttr, _, _ := first.GetPropertyAttributes(r.scope, r.ctx, mustString(t, r, "nestedFunction"))
		firstFnValue, _, _ := first.GetByName(r.scope, r.ctx, "nestedFunction")
		secondFnValue, _, _ := second.GetByName(r.scope, r.ctx, "nestedFunction")
		firstFn, _, _ := gov8.AsFunction(firstFnValue, r.ctx)
		undefined, _ := r.scope.Undefined()
		fnResult, _, _ := firstFn.Call(r.scope, undefined)
		fnInt, _, _ := fnResult.IntegerValue(r.ctx)
		fnShared, _ := firstFnValue.StrictEquals(secondFnValue)
		firstObjValue, _, _ := first.GetByName(r.scope, r.ctx, "nestedObject")
		secondObjValue, _, _ := second.GetByName(r.scope, r.ctx, "nestedObject")
		firstNestedObj, _ := gov8.AsObject(firstObjValue)
		nestedValue, _, _ := firstNestedObj.GetByName(r.scope, r.ctx, "nestedValue")
		nestedInt, _, _ := nestedValue.IntegerValue(r.ctx)
		objectsSame, _ := firstObjValue.StrictEquals(secondObjValue)
		write, _ := first.SetByName(r.scope, r.ctx, "primitive", mustString(t, r, "changed"))
		firstAfter, _, _ := first.GetByName(r.scope, r.ctx, "primitive")
		firstAfterText, _ := firstAfter.ToString(r.ctx)
		secondAfterText, _ := secondPrimitive.ToString(r.ctx)

		rootFunction, _ := rootFunctionTemplate.GetFunction(r.scope, r.ctx)
		rootFunctionAgain, _ := rootFunctionTemplate.GetFunction(r.scope, r.ctx)
		rootResult, _, _ := rootFunction.Call(r.scope, undefined)
		rootCall, _, _ := rootResult.IntegerValue(r.ctx)
		rootSame, _ := rootFunction.Value.StrictEquals(rootFunctionAgain.Value)
		rootObject, _ := gov8.AsObject(rootFunction.Value)
		rootPrimitive, _, _ := rootObject.GetByName(r.scope, r.ctx, "primitive")
		rootPrimitiveInt, _, _ := rootPrimitive.IntegerValue(r.ctx)
		rootPrimitiveAttr, _, _ := rootObject.GetPropertyAttributes(r.scope, r.ctx, mustString(t, r, "primitive"))
		rootNested, _, _ := rootObject.GetByName(r.scope, r.ctx, "nestedFunction")
		rootNestedFunction, _, _ := gov8.AsFunction(rootNested, r.ctx)
		rootNestedResult, _, _ := rootNestedFunction.Call(r.scope, undefined)
		rootNestedInt, _, _ := rootNestedResult.IntegerValue(r.ctx)
		rootNestedAttr, _, _ := rootObject.GetPropertyAttributes(r.scope, r.ctx, mustString(t, r, "nestedFunction"))
		compare(t, fixture, "object-callback-retention/template_set_with_attr", map[string]any{
			"object_primitive": primitiveText, "primitive_shared_value": primitiveShared,
			"primitive_attributes": attributes(primitiveAttr), "nested_function_attributes": attributes(nestedFnAttr),
			"nested_function_call": fnInt, "nested_function_shared_across_instances": fnShared,
			"nested_object_value": nestedInt, "nested_object_distinct_across_instances": !objectsSame,
			"read_only_write": write, "first_primitive_after_write": firstAfterText,
			"second_primitive_after_first_write": secondAfterText, "function_call": rootCall,
			"function_same_context_identity": rootSame, "function_primitive": rootPrimitiveInt,
			"function_primitive_attributes": attributes(rootPrimitiveAttr), "function_nested_call": rootNestedInt,
			"function_nested_attributes": attributes(rootNestedAttr),
		})
	})
}
