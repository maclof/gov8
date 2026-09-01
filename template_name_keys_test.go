//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	gov8 "gov8"
)

func templateNameSymbol(t *testing.T, scope *gov8.Scope, description string) *gov8.Symbol {
	t.Helper()
	value, err := scope.NewString(description)
	if err != nil {
		t.Fatal(err)
	}
	symbol, err := scope.NewSymbol(value)
	if err != nil {
		t.Fatal(err)
	}
	return symbol
}

func templateNameInt(t *testing.T, scope *gov8.Scope, value int32) gov8.Value {
	t.Helper()
	result, err := scope.Int32(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTemplateNameKeyObjectFunctionAndIntrinsic(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	objectTemplate, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	symbol := templateNameSymbol(t, scope, "symbol")
	if err := objectTemplate.SetNameWithAttr(symbol.Value, templateNameInt(t, scope, 22), gov8.AttrReadOnly|gov8.AttrDontEnum); err != nil {
		t.Fatal(err)
	}
	intrinsic := templateNameSymbol(t, scope, "intrinsic")
	if err := objectTemplate.SetIntrinsicDataPropertyName(intrinsic.Value, gov8.IntrinsicArrayPrototype, gov8.AttrDontEnum); err != nil {
		t.Fatal(err)
	}
	instance, ok, err := objectTemplate.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance = %v, %v", ok, err)
	}
	got, err := instance.GetByKey(scope, ctx, symbol.Value)
	if err != nil {
		t.Fatal(err)
	}
	integer, ok, err := got.IntegerValue(ctx)
	if err != nil || !ok || integer != 22 {
		t.Fatalf("symbol value = %d, %v, %v", integer, ok, err)
	}
	arrayPrototype, err := instance.GetByKey(scope, ctx, intrinsic.Value)
	if err != nil {
		t.Fatal(err)
	}
	wantPrototype, err := eval(t, ctx, scope, "Array.prototype")
	if err != nil {
		t.Fatal(err)
	}
	equal, err := arrayPrototype.StrictEquals(wantPrototype)
	if err != nil || !equal {
		t.Fatalf("intrinsic identity = %v, %v", equal, err)
	}

	functionTemplate, err := iso.NewFunctionTemplate(scope, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		_ = rv.SetInt32(7)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	functionSymbol := templateNameSymbol(t, scope, "static")
	if err := functionTemplate.SetName(functionSymbol.Value, templateNameInt(t, scope, 52)); err != nil {
		t.Fatal(err)
	}
	functionDataSymbol := templateNameSymbol(t, scope, "static-data")
	functionData, err := templateNameInt(t, scope, 53).Data()
	if err != nil {
		t.Fatal(err)
	}
	if err := functionTemplate.SetDataNameWithAttr(functionDataSymbol.Value, functionData, gov8.AttrDontEnum); err != nil {
		t.Fatal(err)
	}
	functionIntrinsicSymbol := templateNameSymbol(t, scope, "static-intrinsic")
	if err := functionTemplate.SetIntrinsicDataPropertyName(functionIntrinsicSymbol.Value, gov8.IntrinsicArrayPrototype, gov8.AttrReadOnly); err != nil {
		t.Fatal(err)
	}
	function, err := functionTemplate.GetFunction(scope, ctx)
	if err != nil {
		t.Fatal(err)
	}
	functionObject, err := gov8.AsObject(function.Value)
	if err != nil {
		t.Fatal(err)
	}
	got, err = functionObject.GetByKey(scope, ctx, functionSymbol.Value)
	if err != nil {
		t.Fatal(err)
	}
	integer, ok, err = got.IntegerValue(ctx)
	if err != nil || !ok || integer != 52 {
		t.Fatalf("function symbol = %d, %v, %v", integer, ok, err)
	}
	got, err = functionObject.GetByKey(scope, ctx, functionDataSymbol.Value)
	if err != nil {
		t.Fatal(err)
	}
	integer, ok, err = got.IntegerValue(ctx)
	if err != nil || !ok || integer != 53 {
		t.Fatalf("function data symbol = %d, %v, %v", integer, ok, err)
	}
	got, err = functionObject.GetByKey(scope, ctx, functionIntrinsicSymbol.Value)
	if err != nil {
		t.Fatal(err)
	}
	equal, err = got.StrictEquals(wantPrototype)
	if err != nil || !equal {
		t.Fatalf("function intrinsic identity = %v, %v", equal, err)
	}
}

func TestTemplateNameKeyRetainedAfterKeyScopeClose(t *testing.T) {
	iso, ctx, outer := newTestRuntime(t)
	template, err := iso.NewObjectTemplate(outer)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	symbol := templateNameSymbol(t, inner, "retained-after-scope")
	if err := template.SetName(symbol.Value, templateNameInt(t, inner, 71)); err != nil {
		t.Fatal(err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	instance, ok, err := template.NewInstance(outer, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance = %v, %v", ok, err)
	}
	global, err := ctx.GlobalObject(outer)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(outer, ctx, "instance", instance.Value); err != nil || !ok {
		t.Fatalf("set instance = %v, %v", ok, err)
	}
	value, err := eval(t, ctx, outer, "(()=>{const s=Object.getOwnPropertySymbols(instance)[0];return `${s.description}|${instance[s]}`})()")
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.ToString(ctx)
	if err != nil || text != "retained-after-scope|71" {
		t.Fatalf("retained key = %q, %v", text, err)
	}
}

func TestTemplateNameKeyNestedTemplateData(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	root, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := iso.NewFunctionTemplate(scope, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		_ = rv.SetInt32(42)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := nested.Data()
	if err != nil {
		t.Fatal(err)
	}
	key := templateNameSymbol(t, scope, "nested")
	if err := root.SetDataNameWithAttr(key.Value, data, gov8.AttrDontEnum); err != nil {
		t.Fatal(err)
	}
	instance, ok, err := root.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance = %v, %v", ok, err)
	}
	value, err := instance.GetByKey(scope, ctx, key.Value)
	if err != nil {
		t.Fatal(err)
	}
	function, ok, err := gov8.AsFunction(value, ctx)
	if err != nil || !ok {
		t.Fatalf("AsFunction = %v, %v", ok, err)
	}
	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := function.Call(scope, undefined)
	if err != nil || !ok {
		t.Fatalf("Call = %v, %v", ok, err)
	}
	integer, ok, err := result.IntegerValue(ctx)
	if err != nil || !ok || integer != 42 {
		t.Fatalf("result = %d, %v, %v", integer, ok, err)
	}
}

func TestTemplateNameKeyValidation(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	template, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	value := templateNameInt(t, scope, 1)
	if err := template.SetName(value, value); err == nil || !strings.Contains(err.Error(), "not a Name") {
		t.Fatalf("wrong-kind key = %v", err)
	}
	key, err := scope.NewString("key")
	if err != nil {
		t.Fatal(err)
	}
	object, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := template.SetName(key, object.Value); err == nil || !strings.Contains(err.Error(), "JSReceiver") {
		t.Fatalf("unsafe value = %v", err)
	}
	if err := template.SetNameWithAttr(key, value, gov8.PropertyAttribute(8)); err == nil {
		t.Fatal("unknown attributes accepted")
	}
	if err := template.SetIntrinsicDataPropertyName(key, gov8.Intrinsic(255), gov8.AttrNone); err == nil {
		t.Fatal("unknown intrinsic accepted")
	}

	foreignIso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	foreignScope, err := foreignIso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	foreignKey, err := foreignScope.NewString("foreign")
	if err != nil {
		t.Fatal(err)
	}
	if err := template.SetName(foreignKey, value); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign key = %v", err)
	}
	if err := foreignScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := foreignIso.Close(); err != nil {
		t.Fatal(err)
	}

	closedScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedKey, err := closedScope.NewString("closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := template.SetName(closedKey, value); err == nil {
		t.Fatal("closed key accepted")
	}
	templateScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedTemplate, err := iso.NewObjectTemplate(templateScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := templateScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closedTemplate.SetName(key, value); err == nil {
		t.Fatal("closed template accepted")
	}
}

func TestTemplateNameKeyWrongThread(t *testing.T) {
	iso, _, scope := newTestRuntime(t)
	template, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	key, err := scope.NewString("key")
	if err != nil {
		t.Fatal(err)
	}
	value := templateNameInt(t, scope, 1)
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done <- template.SetName(key, value)
	}()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong-thread SetName = %v", err)
	}
}

func TestTemplateNameKeyDuplicateFatal(t *testing.T) {
	for _, probe := range []string{"TestProbeTemplateDuplicateStringName", "TestProbeTemplateDuplicateSymbolName"} {
		out, code := runCHProbe(t, probe)
		if code != exitStatusBreakpoint {
			t.Fatalf("%s exit = %d, want %d; output:\n%s", probe, code, exitStatusBreakpoint, out)
		}
		if !strings.Contains(out, "MARK:before-instantiation") || strings.Contains(out, "MARK:after-instantiation") {
			t.Fatalf("%s markers; output:\n%s", probe, out)
		}
		if !strings.Contains(out, "Check failed: LinearSearch(*desc->GetKey(), descriptor_number) == InternalIndex::NotFound().") {
			t.Fatalf("%s diagnostic; output:\n%s", probe, out)
		}
	}
}

func TestProbeTemplateDuplicateStringName(t *testing.T) {
	templateDuplicateProbe(t, "TestProbeTemplateDuplicateStringName", false)
}

func TestProbeTemplateDuplicateSymbolName(t *testing.T) {
	templateDuplicateProbe(t, "TestProbeTemplateDuplicateSymbolName", true)
}

func templateDuplicateProbe(t *testing.T, probe string, useSymbol bool) {
	t.Helper()
	if !chProbe(t, probe) {
		t.Skip("probe body")
	}
	iso, ctx, scope := newTestRuntime(t)
	template, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatal(err)
	}
	var first, second gov8.Value
	if useSymbol {
		first = templateNameSymbol(t, scope, "duplicate").Value
		second = first
	} else {
		first, err = scope.NewString("duplicate")
		if err != nil {
			t.Fatal(err)
		}
		second, err = scope.NewString("duplicate")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := template.SetName(first, templateNameInt(t, scope, 1)); err != nil {
		t.Fatal(err)
	}
	if err := template.SetNameWithAttr(second, templateNameInt(t, scope, 2), gov8.AttrDontEnum); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "MARK:before-instantiation")
	_, _, _ = template.NewInstance(scope, ctx)
	fmt.Fprintln(os.Stderr, "MARK:after-instantiation")
}
