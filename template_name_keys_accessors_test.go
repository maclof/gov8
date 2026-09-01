//go:build windows && amd64

package gov8_test

import (
	"runtime"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func exposeTemplateNameValue(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, name string, value gov8.Value) {
	t.Helper()
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(scope, ctx, name, value); err != nil || !ok {
		t.Fatalf("expose %s = %v, %v", name, ok, err)
	}
}

func templateNameEvalText(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, source string) string {
	t.Helper()
	value, err := eval(t, ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.ToString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestTemplateNameAccessorPropertySymbol(t *testing.T) {
	iso, ctx, outer := newTestRuntime(t)
	objectTemplate, err := iso.NewObjectTemplate(outer)
	if err != nil {
		t.Fatal(err)
	}
	getter, err := iso.NewFunctionTemplate(outer, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		_ = rv.SetInt32(41)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var setterValue int64
	setter, err := iso.NewFunctionTemplate(outer, func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		value, err := args.Get(0)
		if err != nil {
			return
		}
		setterValue, _, _ = cs.IntegerValue(value)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	keyScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	key := templateNameSymbol(t, keyScope, "object-accessor")
	if err := objectTemplate.SetAccessorPropertyName(key.Value, getter, setter, gov8.AttrDontEnum|gov8.AttrDontDelete); err != nil {
		t.Fatal(err)
	}
	if err := keyScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	object, ok, err := objectTemplate.NewInstance(outer, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance = %v, %v", ok, err)
	}
	exposeTemplateNameValue(t, outer, ctx, "nameAccessorObject", object.Value)
	if got := templateNameEvalText(t, outer, ctx, `(()=>{const s=Object.getOwnPropertySymbols(nameAccessorObject)[0];const d=Object.getOwnPropertyDescriptor(nameAccessorObject,s);const first=nameAccessorObject[s];nameAccessorObject[s]=88;return [s.description,first,d.enumerable,d.configurable].join("|")})()`); got != "object-accessor|41|false|false" {
		t.Fatalf("object accessor = %q", got)
	}
	if setterValue != 88 {
		t.Fatalf("setter value = %d", setterValue)
	}

	functionTemplate, err := iso.NewFunctionTemplate(outer, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var functionSetterValue int64
	functionSetter, err := iso.NewFunctionTemplate(outer, func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		value, err := args.Get(0)
		if err != nil {
			return
		}
		functionSetterValue, _, _ = cs.IntegerValue(value)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	functionKey := templateNameSymbol(t, outer, "function-accessor")
	if err := functionTemplate.SetAccessorPropertyName(functionKey.Value, getter, functionSetter, gov8.AttrDontDelete); err != nil {
		t.Fatal(err)
	}
	function, err := functionTemplate.GetFunction(outer, ctx)
	if err != nil {
		t.Fatal(err)
	}
	exposeTemplateNameValue(t, outer, ctx, "nameAccessorFunction", function.Value)
	exposeTemplateNameValue(t, outer, ctx, "nameAccessorFunctionKey", functionKey.Value)
	if got := templateNameEvalText(t, outer, ctx, `(()=>{const d=Object.getOwnPropertyDescriptor(nameAccessorFunction,nameAccessorFunctionKey);const first=nameAccessorFunction[nameAccessorFunctionKey];nameAccessorFunction[nameAccessorFunctionKey]=95;return [first,d.configurable].join("|")})()`); got != "41|false" {
		t.Fatalf("function accessor = %q", got)
	}
	if functionSetterValue != 95 {
		t.Fatalf("function setter value = %d", functionSetterValue)
	}
}

func TestTemplateNameNativeAccessorConfigurationSymbol(t *testing.T) {
	iso, ctx, outer := newTestRuntime(t)
	template, err := iso.NewObjectTemplate(outer)
	if err != nil {
		t.Fatal(err)
	}
	keyScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	key := templateNameSymbol(t, keyScope, "configured")
	data := templateNameInt(t, keyScope, 73)
	var getterCalls int
	var setterValue int64
	configuration := gov8.AccessorConfiguration{
		Getter: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			getterCalls++
			property, err := args.Property()
			if err != nil {
				t.Errorf("Property: %v", err)
				return
			}
			isSymbol, err := property.IsSymbol()
			if err != nil || !isSymbol {
				t.Errorf("property symbol = %v, %v", isSymbol, err)
				return
			}
			retained, err := args.Data()
			if err != nil {
				t.Errorf("Data: %v", err)
				return
			}
			value, ok, err := cs.IntegerValue(retained)
			if err != nil || !ok || value != 73 {
				t.Errorf("retained data = %d, %v, %v", value, ok, err)
				return
			}
			_ = rv.Set(retained)
		},
		Setter: func(cs *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, value gov8.Value) {
			setterValue, _, _ = cs.IntegerValue(value)
		},
		Data:      data,
		Attribute: gov8.AttrDontEnum | gov8.AttrDontDelete,
	}
	if err := template.SetAccessorWithConfigurationName(key.Value, configuration); err != nil {
		t.Fatal(err)
	}
	if err := keyScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	object, ok, err := template.NewInstance(outer, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance = %v, %v", ok, err)
	}
	exposeTemplateNameValue(t, outer, ctx, "configuredObject", object.Value)
	if got := templateNameEvalText(t, outer, ctx, `(()=>{const s=Object.getOwnPropertySymbols(configuredObject)[0];const d=Object.getOwnPropertyDescriptor(configuredObject,s);const first=configuredObject[s];configuredObject[s]=91;return [s.description,first,d.enumerable,d.configurable].join("|")})()`); got != "configured|73|false|false" {
		t.Fatalf("configured accessor = %q", got)
	}
	// V8 invokes a native-data-property getter once while materializing its
	// own descriptor and once for the explicit property read above.
	if getterCalls != 2 || setterValue != 91 {
		t.Fatalf("callbacks = getter:%d setter:%d", getterCalls, setterValue)
	}
}

func TestTemplateNameSetterOnlyNativeAccessorSymbol(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	template, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	key := templateNameSymbol(t, scope, "setter-only")
	var setterValue int64
	if err := template.SetAccessorWithSetterName(key.Value, nil, func(cs *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, value gov8.Value) {
		setterValue, _, _ = cs.IntegerValue(value)
	}); err != nil {
		t.Fatal(err)
	}
	object, ok, err := template.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance = %v, %v", ok, err)
	}
	exposeTemplateNameValue(t, scope, ctx, "setterOnlyObject", object.Value)
	exposeTemplateNameValue(t, scope, ctx, "setterOnlyKey", key.Value)
	if got := templateNameEvalText(t, scope, ctx, `String(setterOnlyObject[setterOnlyKey])`); got != "undefined" {
		t.Fatalf("setter-only read = %q", got)
	}
	if got := templateNameEvalText(t, scope, ctx, `setterOnlyObject[setterOnlyKey]=64`); got != "64" {
		t.Fatalf("setter-only write = %q", got)
	}
	if setterValue != 64 {
		t.Fatalf("setter value = %d", setterValue)
	}
}

func TestTemplateNameAccessorValidation(t *testing.T) {
	iso, _, scope := newTestRuntime(t)
	objectTemplate, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	functionTemplate, err := iso.NewFunctionTemplate(scope, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	number := templateNameInt(t, scope, 1)
	getter := func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) {}
	checks := []func() error{
		func() error {
			return objectTemplate.SetAccessorPropertyName(number, functionTemplate, nil, gov8.AttrNone)
		},
		func() error {
			return functionTemplate.SetAccessorPropertyName(number, functionTemplate, nil, gov8.AttrNone)
		},
		func() error {
			return objectTemplate.SetAccessorWithConfigurationName(number, gov8.AccessorConfiguration{Getter: getter})
		},
		func() error { return objectTemplate.SetAccessorWithSetterName(number, getter, nil) },
	}
	for i, check := range checks {
		if err := check(); err == nil || !strings.Contains(err.Error(), "not a Name") {
			t.Fatalf("wrong-kind check %d = %v", i, err)
		}
	}
	key := templateNameSymbol(t, scope, "valid")
	if err := objectTemplate.SetAccessorWithConfigurationName(key.Value, gov8.AccessorConfiguration{}); err == nil || !strings.Contains(err.Error(), "requires a getter") {
		t.Fatalf("nil configured getter = %v", err)
	}
	if err := objectTemplate.SetAccessorWithSetterName(key.Value, nil, nil); err == nil {
		t.Fatal("nil native accessor callbacks accepted")
	}
	if err := objectTemplate.SetAccessorPropertyName(key.Value, nil, nil, gov8.AttrNone); err == nil {
		t.Fatal("nil accessor templates accepted")
	}

	foreign, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	foreignScope, err := foreign.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	foreignKey := templateNameSymbol(t, foreignScope, "foreign")
	if err := objectTemplate.SetAccessorWithSetterName(foreignKey.Value, getter, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign key = %v", err)
	}
	foreignGetter, err := foreign.NewFunctionTemplate(foreignScope, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := objectTemplate.SetAccessorPropertyName(key.Value, foreignGetter, nil, gov8.AttrNone); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign accessor template = %v", err)
	}
	if err := foreignScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}

	closedScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedKey := templateNameSymbol(t, closedScope, "closed")
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := objectTemplate.SetAccessorWithConfigurationName(closedKey.Value, gov8.AccessorConfiguration{Getter: getter}); err == nil {
		t.Fatal("closed key accepted")
	}
}

func TestTemplateNameAccessorWrongThread(t *testing.T) {
	iso, _, scope := newTestRuntime(t)
	template, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	key := templateNameSymbol(t, scope, "thread")
	getter := func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) {}
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done <- template.SetAccessorWithSetterName(key.Value, getter, nil)
	}()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong-thread accessor = %v", err)
	}
}
