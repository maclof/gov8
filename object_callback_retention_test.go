//go:build windows && amd64

package gov8_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestAccessorConfigurationRetainsDataAndAttributes(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	key := e.mustString("configured")
	dataScope, err := e.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	data := func() gov8.Value {
		o, err := dataScope.NewObject(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		marker, _ := dataScope.Int32(73)
		if ok, err := o.SetByName(dataScope, e.ctx, "marker", marker); err != nil || !ok {
			t.Fatalf("data marker: ok=%v err=%v", ok, err)
		}
		if ok, err := o.SetByName(dataScope, e.ctx, "self", o.Value); err != nil || !ok {
			t.Fatalf("data self: ok=%v err=%v", ok, err)
		}
		return o.Value
	}()
	state, getHits, setHits := int64(5), 0, 0
	ok, err := obj.SetAccessorWithConfiguration(e.scope, e.ctx, key, gov8.AccessorConfiguration{
		Getter: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			getHits++
			got, err := args.Data()
			if err != nil {
				panic(err)
			}
			self, present, err := cs.ObjectGet(got, "self")
			if err != nil || !present {
				panic("missing retained self")
			}
			equal, err := got.StrictEquals(self)
			if err != nil || !equal {
				panic("retained data identity changed")
			}
			_ = rv.SetInt32(int32(state))
		},
		Setter: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, value gov8.Value) {
			setHits++
			state, _, _ = cs.IntegerValue(value)
		},
		Data:      data,
		Attribute: gov8.AttrDontEnum | gov8.AttrDontDelete,
	})
	if err != nil || !ok {
		t.Fatalf("install: ok=%v err=%v", ok, err)
	}
	if err := dataScope.Close(); err != nil {
		t.Fatal(err)
	}
	if got := e.evalInt("configuredObject = undefined; 0"); got != 0 { // keep script machinery warm
		t.Fatal(got)
	}
	v, err := obj.GetByKey(e.scope, e.ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if n, _, err := v.IntegerValue(e.ctx); err != nil || n != 5 {
		t.Fatalf("get=%d err=%v", n, err)
	}
	if ok, err := obj.SetByKey(e.scope, e.ctx, key, e.mustInt(12)); err != nil || !ok {
		t.Fatalf("set: ok=%v err=%v", ok, err)
	}
	attr, present, err := obj.GetPropertyAttributes(e.scope, e.ctx, key)
	if err != nil || !present || attr != gov8.AttrDontEnum|gov8.AttrDontDelete {
		t.Fatalf("attributes=%#x present=%v err=%v", attr, present, err)
	}
	if getHits != 1 || setHits != 1 || state != 12 {
		t.Fatalf("hits/state=%d/%d/%d", getHits, setHits, state)
	}
}

func TestLazyDataPropertyThrowRetryAndEmptyCaching(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()
	obj := e.mustObject()
	throwKey := e.mustString("throws")
	hits := 0
	ok, err := obj.SetLazyDataPropertyWithConfiguration(e.scope, e.ctx, throwKey, gov8.LazyDataPropertyConfiguration{
		Getter: func(cs *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) {
			hits++
			exception, err := cs.NewError("lazy boom")
			if err != nil {
				panic(err)
			}
			if err := cs.ThrowException(exception); err != nil {
				panic(err)
			}
		},
	})
	if err != nil || !ok {
		t.Fatalf("throwing install: ok=%v err=%v", ok, err)
	}
	for want := 1; want <= 2; want++ {
		tc, err := e.iso.NewTryCatch()
		if err != nil {
			t.Fatal(err)
		}
		_, err = obj.GetByKey(e.scope, e.ctx, throwKey)
		caught, _ := tc.HasCaught()
		text, _ := tc.ExceptionText(e.scope, e.ctx)
		_ = tc.Close()
		if err == nil || !caught || text != "Error: lazy boom" || hits != want {
			t.Fatalf("attempt %d: err=%v caught=%v text=%q hits=%d", want, err, caught, text, hits)
		}
	}

	emptyKey := e.mustString("empty")
	emptyHits := 0
	ok, err = obj.SetLazyDataProperty(e.scope, e.ctx, emptyKey,
		func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) { emptyHits++ })
	if err != nil || !ok {
		t.Fatalf("empty install: ok=%v err=%v", ok, err)
	}
	for range 2 {
		value, err := obj.GetByKey(e.scope, e.ctx, emptyKey)
		if err != nil {
			t.Fatal(err)
		}
		if undefined, err := value.IsUndefined(); err != nil || !undefined {
			t.Fatalf("empty value undefined=%v err=%v", undefined, err)
		}
	}
	if emptyHits != 1 {
		t.Fatalf("empty callback hits=%d", emptyHits)
	}
}

func TestTemplateSetDataWithAttrNestedTemplates(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()
	return42 := func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		_ = rv.SetInt32(42)
	}
	nestedFunction, err := e.iso.NewFunctionTemplate(e.scope, return42, nil)
	if err != nil {
		t.Fatal(err)
	}
	nestedObject, err := e.iso.NewObjectTemplate(e.scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := nestedObject.Set("value", e.mustInt(81)); err != nil {
		t.Fatal(err)
	}
	root, err := e.iso.NewObjectTemplate(e.scope)
	if err != nil {
		t.Fatal(err)
	}
	functionData, _ := nestedFunction.Data()
	objectData, _ := nestedObject.Data()
	if err := root.SetDataWithAttr("fn", functionData, gov8.AttrDontEnum); err != nil {
		t.Fatal(err)
	}
	if err := root.SetDataWithAttr("obj", objectData, gov8.AttrNone); err != nil {
		t.Fatal(err)
	}
	first, present, err := root.NewInstance(e.scope, e.ctx)
	if err != nil || !present {
		t.Fatalf("first instance: present=%v err=%v", present, err)
	}
	second, present, err := root.NewInstance(e.scope, e.ctx)
	if err != nil || !present {
		t.Fatalf("second instance: present=%v err=%v", present, err)
	}
	fn1, _, _ := first.GetByName(e.scope, e.ctx, "fn")
	fn2, _, _ := second.GetByName(e.scope, e.ctx, "fn")
	if same, err := fn1.StrictEquals(fn2); err != nil || !same {
		t.Fatalf("nested function shared=%v err=%v", same, err)
	}
	o1, _, _ := first.GetByName(e.scope, e.ctx, "obj")
	o2, _, _ := second.GetByName(e.scope, e.ctx, "obj")
	if same, err := o1.StrictEquals(o2); err != nil || same {
		t.Fatalf("nested objects distinct=%v err=%v", !same, err)
	}
}

func TestObjectTemplateAccessorConfigurationRetainsData(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()
	template, err := e.iso.NewObjectTemplate(e.scope)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := e.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	dataObject, err := inner.NewObject(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	marker, _ := inner.Int32(91)
	_, _ = dataObject.SetByName(inner, e.ctx, "marker", marker)
	hits := 0
	err = template.SetAccessorWithConfiguration("retained", gov8.AccessorConfiguration{
		Getter: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			hits++
			data, err := args.Data()
			if err != nil {
				panic(err)
			}
			value, present, err := cs.ObjectGet(data, "marker")
			if err != nil || !present {
				panic("retained template data missing")
			}
			_ = rv.Set(value)
		},
		Data: dataObject.Value, Attribute: gov8.AttrDontEnum,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	instance, present, err := template.NewInstance(e.scope, e.ctx)
	if err != nil || !present {
		t.Fatalf("NewInstance: present=%v err=%v", present, err)
	}
	value, present, err := instance.GetByName(e.scope, e.ctx, "retained")
	if err != nil || !present {
		t.Fatalf("GetByName: present=%v err=%v", present, err)
	}
	if n, ok, err := value.IntegerValue(e.ctx); err != nil || !ok || n != 91 || hits != 1 {
		t.Fatalf("value=%d ok=%v hits=%d err=%v", n, ok, hits, err)
	}
	attr, present, err := instance.GetPropertyAttributes(e.scope, e.ctx, e.mustString("retained"))
	if err != nil || !present || attr != gov8.AttrDontEnum {
		t.Fatalf("attributes=%#x present=%v err=%v", attr, present, err)
	}
}

func TestLegacySetterOnlyAccessorsReadUndefined(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()
	setter := func(cs *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, value gov8.Value) {
		if _, ok, err := cs.IntegerValue(value); err != nil || !ok {
			panic("setter value")
		}
	}
	object := e.mustObject()
	key := e.mustString("only")
	if ok, err := object.SetAccessor(e.scope, e.ctx, key, nil, setter); err != nil || !ok {
		t.Fatalf("instance setter-only: ok=%v err=%v", ok, err)
	}
	before, _ := object.GetByKey(e.scope, e.ctx, key)
	if undefined, _ := before.IsUndefined(); !undefined {
		t.Fatal("instance read before write was not undefined")
	}
	if ok, err := object.SetByKey(e.scope, e.ctx, key, e.mustInt(4)); err != nil || !ok {
		t.Fatalf("instance write: ok=%v err=%v", ok, err)
	}
	after, _ := object.GetByKey(e.scope, e.ctx, key)
	if undefined, _ := after.IsUndefined(); !undefined {
		t.Fatal("instance read after write was not undefined")
	}

	template, _ := e.iso.NewObjectTemplate(e.scope)
	if err := template.SetAccessorWithSetter("only", nil, setter); err != nil {
		t.Fatal(err)
	}
	instance, present, err := template.NewInstance(e.scope, e.ctx)
	if err != nil || !present {
		t.Fatalf("template instance: present=%v err=%v", present, err)
	}
	before, _, _ = instance.GetByName(e.scope, e.ctx, "only")
	if undefined, _ := before.IsUndefined(); !undefined {
		t.Fatal("template read before write was not undefined")
	}
	if ok, err := instance.SetByName(e.scope, e.ctx, "only", e.mustInt(5)); err != nil || !ok {
		t.Fatalf("template write: ok=%v err=%v", ok, err)
	}
	after, _, _ = instance.GetByName(e.scope, e.ctx, "only")
	if undefined, _ := after.IsUndefined(); !undefined {
		t.Fatal("template read after write was not undefined")
	}
}

func TestObjectCallbackRetentionSafetyBoundaries(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()
	obj := e.mustObject()
	key := e.mustString("x")
	getter := func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) {}
	if _, err := obj.SetAccessorWithConfiguration(e.scope, e.ctx, key, gov8.AccessorConfiguration{}); err == nil {
		t.Fatal("missing configured getter accepted")
	}
	if _, err := obj.SetLazyDataPropertyWithData(e.scope, e.ctx, key, getter, gov8.Value{}, 0, gov8.SideEffectHasSideEffect, gov8.SideEffectHasNoSideEffect); err == nil {
		t.Fatal("fatal setter side-effect combination accepted")
	}
	root, _ := e.iso.NewObjectTemplate(e.scope)
	objectData, _ := obj.Value.Data()
	if err := root.SetDataWithAttr("receiver", objectData, 0); err == nil || !strings.Contains(err.Error(), "JSReceiver") {
		t.Fatalf("JSReceiver template data error=%v", err)
	}
	contextData, err := e.ctx.Data(e.scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.SetDataWithAttr("context", contextData, 0); err == nil {
		t.Fatal("Context metadata template data accepted")
	}
	array, err := gov8.NewPrimitiveArray(e.scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.SetDataWithAttr("primitive-array", array.Data, 0); err == nil {
		t.Fatal("PrimitiveArray metadata template data accepted")
	}

	closedScope, _ := e.iso.NewScope()
	closedTemplate, _ := e.iso.NewObjectTemplate(closedScope)
	closedPrimitive, _ := closedScope.Int32(1)
	closedData, _ := closedPrimitive.Data()
	_ = closedScope.Close()
	if err := closedTemplate.SetDataWithAttr("closed", closedData, 0); err == nil {
		t.Fatal("closed template/data accepted")
	}

	other, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	otherScope, _ := other.NewScope()
	foreign, _ := otherScope.Int32(1)
	foreignData, _ := foreign.Data()
	if _, err := obj.SetAccessorWithConfiguration(e.scope, e.ctx, key, gov8.AccessorConfiguration{Getter: getter, Data: foreign}); err == nil {
		t.Fatal("foreign accessor callback data accepted")
	}
	if err := root.SetDataWithAttr("foreign", foreignData, 0); err == nil {
		t.Fatal("foreign template data accepted")
	}
	_ = otherScope.Close()
	_ = other.Close()

	errs := make(chan error, 1)
	go func() {
		_, err := obj.SetLazyDataProperty(e.scope, e.ctx, key, getter)
		errs <- err
	}()
	if err := <-errs; err == nil || (!strings.Contains(err.Error(), "affinity") && !strings.Contains(err.Error(), "wrong thread")) {
		t.Fatalf("wrong-thread error=%v", err)
	}
}

func TestObjectCallbackPanicsAbort(t *testing.T) {
	mode := os.Getenv("GOV8_OCR_PANIC_CHILD")
	if mode != "" {
		objectCallbackPanicChild(t, mode)
		return
	}
	for _, mode := range []string{"accessor-getter", "accessor-setter", "lazy-getter"} {
		cmd := exec.Command(os.Args[0], "-test.run=TestObjectCallbackPanicsAbort", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOV8_OCR_PANIC_CHILD="+mode)
		out, err := cmd.CombinedOutput()
		text := string(out)
		if err == nil || !strings.Contains(text, "marker:entered-"+mode) || strings.Contains(text, "marker:after-"+mode) {
			t.Fatalf("%s boundary: err=%v output=%s", mode, err, text)
		}
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3221226505 {
			t.Fatalf("%s exit=%v", mode, err)
		}
	}
}

func objectCallbackPanicChild(t *testing.T, mode string) {
	e := newObjectEnv(t)
	obj := e.mustObject()
	key := e.mustString("p")
	getter := func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.ReturnValue) {
		_, _ = os.Stderr.WriteString("marker:entered-" + mode + "\n")
		panic(mode)
	}
	setter := func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, _ gov8.Value) {
		_, _ = os.Stderr.WriteString("marker:entered-" + mode + "\n")
		panic(mode)
	}
	switch mode {
	case "accessor-getter":
		_, _ = obj.SetAccessor(e.scope, e.ctx, key, getter, nil)
		_, _ = obj.GetByKey(e.scope, e.ctx, key)
	case "accessor-setter":
		_, _ = obj.SetAccessor(e.scope, e.ctx, key, func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, rv gov8.ReturnValue) { _ = rv.SetInt32(1) }, setter)
		_, _ = obj.SetByKey(e.scope, e.ctx, key, e.mustInt(2))
	case "lazy-getter":
		_, _ = obj.SetLazyDataProperty(e.scope, e.ctx, key, getter)
		_, _ = obj.GetByKey(e.scope, e.ctx, key)
	}
	_, _ = os.Stderr.WriteString("marker:after-" + mode + "\n")
}

func TestLazyDataPropertyBorrowedCallbackScopeInvalidated(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()
	obj := e.mustObject()
	key := e.mustString("borrowed-lazy")
	var borrowed *gov8.Scope
	var callbackValue gov8.Value
	var closeErr error
	getter := func(cs *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
		borrowed = cs.Scope()
		callbackValue, _ = borrowed.Int32(42)
		closeErr = borrowed.Close()
		_ = rv.Set(callbackValue)
	}
	if ok, err := obj.SetLazyDataProperty(e.scope, e.ctx, key, getter); err != nil || !ok {
		t.Fatalf("install: ok=%v err=%v", ok, err)
	}
	value, err := obj.GetByKey(e.scope, e.ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := value.Int32Value(e.ctx); err != nil || !ok || got != 42 {
		t.Fatalf("value: got=%d ok=%v err=%v", got, ok, err)
	}
	if closeErr == nil || !strings.Contains(closeErr.Error(), "borrowed callback scope") {
		t.Fatalf("Close during callback = %v", closeErr)
	}
	if _, err := borrowed.Int32(1); err == nil || !strings.Contains(err.Error(), "used after Close") {
		t.Fatalf("borrowed scope after callback = %v", err)
	}
	if _, err := callbackValue.IsInt32(); err == nil || !strings.Contains(err.Error(), "used after Close") {
		t.Fatalf("callback value after callback = %v", err)
	}
}

func TestLazyDataPropertySharedGetterSurvivesFailedSiblingInstallation(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	hits := 0
	getter := func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
		hits++
		_ = rv.SetInt32(42)
	}
	first := e.mustObject()
	firstKey := e.mustString("first")
	if ok, err := first.SetLazyDataProperty(e.scope, e.ctx, firstKey, getter); err != nil || !ok {
		t.Fatalf("first install: ok=%v err=%v", ok, err)
	}

	sealed := e.mustObject()
	if ok, err := sealed.SetIntegrityLevel(e.scope, e.ctx, gov8.IntegritySealed); err != nil || !ok {
		t.Fatalf("seal sibling: ok=%v err=%v", ok, err)
	}
	failedKey := e.mustString("failed")
	if ok, _ := sealed.SetLazyDataProperty(e.scope, e.ctx, failedKey, getter); ok {
		t.Fatal("lazy property installation on sealed sibling succeeded")
	}

	value, err := first.GetByKey(e.scope, e.ctx, firstKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := value.Int32Value(e.ctx); err != nil || !ok || got != 42 {
		t.Fatalf("first value after sibling failure: got=%d ok=%v err=%v", got, ok, err)
	}
	if hits != 1 {
		t.Fatalf("getter hits = %d, want 1", hits)
	}
}

func BenchmarkLazyDataPropertyFirstRead(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	getter := func(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
		_ = rv.SetInt32(42)
	}
	// Validate the matched workload before timing it. This also creates the
	// reusable dispatch context for this exact getter value, mirroring Rust's
	// untimed probe and repeated use of one static callback pointer.
	probe := e.mustObject()
	probeKey := e.mustString("lazy")
	if ok, err := probe.SetLazyDataProperty(e.scope, e.ctx, probeKey, getter); err != nil || !ok {
		b.Fatalf("probe install: ok=%v err=%v", ok, err)
	}
	probeValue, err := probe.GetByKey(e.scope, e.ctx, probeKey)
	if err != nil {
		b.Fatalf("probe first read: %v", err)
	}
	if value, ok, err := probeValue.Int32Value(e.ctx); err != nil || !ok || value != 42 {
		b.Fatalf("probe value: value=%d ok=%v err=%v", value, ok, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := e.iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		obj, err := inner.NewObject(e.ctx)
		if err != nil {
			b.Fatal(err)
		}
		key, err := inner.NewString("lazy")
		if err != nil {
			b.Fatal(err)
		}
		if ok, err := obj.SetLazyDataProperty(inner, e.ctx, key, getter); err != nil || !ok {
			b.Fatalf("install: ok=%v err=%v", ok, err)
		}
		value, err := obj.GetByKey(inner, e.ctx, key)
		if err != nil {
			b.Fatal(err)
		}
		if got, ok, err := value.Int32Value(e.ctx); err != nil || !ok || got != 42 {
			b.Fatalf("first read: value=%d ok=%v err=%v", got, ok, err)
		}
		if err := inner.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLazyDataPropertyEagerControl keeps the same scope, object, key,
// read, conversion, and teardown shape without callback registration or lazy
// materialization. It distinguishes lazy-path changes from machine-wide or
// ordinary object-operation movement during paired measurements.
func BenchmarkLazyDataPropertyEagerControl(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	want := e.mustInt(42)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := e.iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		obj, err := inner.NewObject(e.ctx)
		if err != nil {
			b.Fatal(err)
		}
		key, err := inner.NewString("lazy")
		if err != nil {
			b.Fatal(err)
		}
		if ok, err := obj.SetByKey(inner, e.ctx, key, want); err != nil || !ok {
			b.Fatalf("set: ok=%v err=%v", ok, err)
		}
		value, err := obj.GetByKey(inner, e.ctx, key)
		if err != nil {
			b.Fatal(err)
		}
		if got, ok, err := value.Int32Value(e.ctx); err != nil || !ok || got != 42 {
			b.Fatalf("read: value=%d ok=%v err=%v", got, ok, err)
		}
		if err := inner.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
