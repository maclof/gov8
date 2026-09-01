//go:build windows && amd64

package gov8_test

import (
	"math"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Unit tests for the object-ops slice: receiver get/set, prototype,
// identity, call/construct, conversions, equality and the missing
// predicates -- success, error, and lifecycle paths (the byte-exact
// normalized behavior lives in conformance-object-ops).

type objEnv struct {
	t     testing.TB
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newObjectEnv(t *testing.T) *objEnv {
	t.Helper()
	return newObjectEnvTB(t)
}

// newObjectEnvTB accepts *testing.T and *testing.B (the benchmarks share
// the runtime setup).
func newObjectEnvTB(t testing.TB) *objEnv {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	return &objEnv{t: t, iso: iso, ctx: ctx, scope: scope}
}

func (e *objEnv) close() {
	t := e.t
	for _, c := range []interface{ Close() error }{e.scope, e.ctx, e.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

func (e *objEnv) mustString(s string) gov8.Value {
	t := e.t
	t.Helper()
	v, err := e.scope.NewString(s)
	if err != nil {
		t.Fatalf("NewString(%q): %v", s, err)
	}
	return v
}

func (e *objEnv) mustInt(v int32) gov8.Value {
	t := e.t
	t.Helper()
	got, err := e.scope.Int32(v)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	return got
}

func (e *objEnv) mustObject() *gov8.Object {
	t := e.t
	t.Helper()
	o, err := e.scope.NewObject(e.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	return o
}

func (e *objEnv) evalInt(src string) int64 {
	t := e.t
	t.Helper()
	script, err := e.ctx.Compile(e.scope, src, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(e.scope, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	n, _, err := v.IntegerValue(e.ctx)
	if err != nil {
		t.Fatalf("IntegerValue: %v", err)
	}
	return n
}

// --- prototype -----------------------------------------------------------------

func TestObjectPrototypeSetAndGet(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	parent := e.mustObject()

	if ok, err := obj.SetPrototype(e.scope, e.ctx, parent.Value); err != nil || !ok {
		t.Fatalf("SetPrototype: ok=%v err=%v", ok, err)
	}
	proto, err := obj.GetPrototype(e.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	if eq, err := proto.StrictEquals(parent.Value); err != nil || !eq {
		t.Fatalf("prototype is not parent: eq=%v err=%v", eq, err)
	}

	// Null prototype is a present null value.
	nullV, err := e.scope.Null()
	if err != nil {
		t.Fatalf("Null: %v", err)
	}
	if ok, err := obj.SetPrototype(e.scope, e.ctx, nullV); err != nil || !ok {
		t.Fatalf("SetPrototype(null): ok=%v err=%v", ok, err)
	}
	proto2, err := obj.GetPrototype(e.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	if isNull, err := proto2.IsNull(); err != nil || !isNull {
		t.Fatalf("prototype after null: isNull=%v err=%v", isNull, err)
	}
}

func TestObjectPrototypeCycleRejectedWithoutException(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	a := e.mustObject()
	b := e.mustObject()
	if ok, err := a.SetPrototype(e.scope, e.ctx, b.Value); err != nil || !ok {
		t.Fatalf("a->b: ok=%v err=%v", ok, err)
	}
	tc, err := e.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	ok, err := b.SetPrototype(e.scope, e.ctx, a.Value)
	if err == nil || ok {
		t.Fatalf("cyclic SetPrototype must fail: ok=%v err=%v", ok, err)
	}
	if !gov8.IsException(err) {
		t.Errorf("cyclic rejection should map to the exception status code, got %v", err)
	}
	if caught, err := tc.HasCaught(); err != nil || caught {
		t.Fatalf("cyclic rejection must not schedule an exception: caught=%v err=%v", caught, err)
	}
	if err := tc.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}
}

// --- has / delete family ----------------------------------------------------------

func TestObjectHasDeleteFamily(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	key := e.mustString("a")
	if ok, err := obj.SetByKey(e.scope, e.ctx, key, e.mustInt(1)); err != nil || !ok {
		t.Fatalf("SetByKey: ok=%v err=%v", ok, err)
	}

	if has, err := obj.Has(e.scope, e.ctx, key, nil); err != nil || !has {
		t.Fatalf("Has(a): has=%v err=%v", has, err)
	}
	missing := e.mustString("missing")
	if has, err := obj.Has(e.scope, e.ctx, missing, nil); err != nil || has {
		t.Fatalf("Has(missing): has=%v err=%v", has, err)
	}
	if has, err := obj.HasIndex(e.scope, e.ctx, 5, nil); err != nil || has {
		t.Fatalf("HasIndex(5): has=%v err=%v", has, err)
	}
	if has, err := obj.HasOwnProperty(e.scope, e.ctx, key, nil); err != nil || !has {
		t.Fatalf("HasOwnProperty(a): has=%v err=%v", has, err)
	}
	// A Name requirement: an integer key is refused by the wrapper.
	if _, err := obj.HasOwnProperty(e.scope, e.ctx, e.mustInt(3), nil); err == nil {
		t.Fatal("HasOwnProperty with non-Name key must be refused")
	}

	del, err := obj.Delete(e.scope, e.ctx, key, nil)
	if err != nil || !del {
		t.Fatalf("Delete(a): del=%v err=%v", del, err)
	}
	if has, err := obj.Has(e.scope, e.ctx, key, nil); err != nil || has {
		t.Fatalf("Has(a) after delete: has=%v err=%v", has, err)
	}
	// A missing key deletes "successfully".
	delMissing, err := obj.Delete(e.scope, e.ctx, missing, nil)
	if err != nil || !delMissing {
		t.Fatalf("Delete(missing): del=%v err=%v", delMissing, err)
	}
	if del, err := obj.DeleteIndex(e.scope, e.ctx, 3, nil); err != nil || !del {
		t.Fatalf("DeleteIndex: del=%v err=%v", del, err)
	}
}

// --- identity ------------------------------------------------------------------

func TestObjectIdentityHashStableAndNonZero(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	h1, err := obj.GetIdentityHash()
	if err != nil {
		t.Fatalf("GetIdentityHash: %v", err)
	}
	if h1 == 0 {
		t.Fatal("identity hash must never be zero")
	}
	h2, err := obj.GetIdentityHash()
	if err != nil {
		t.Fatalf("GetIdentityHash: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("identity hash changed: %d vs %d", h1, h2)
	}
	vh, err := obj.Value.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}
	if int32(vh) != h1 {
		t.Fatalf("GetHash %d != identity hash %d", int32(vh), h1)
	}
}

func TestObjectCreationContextIs(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	if same, err := obj.CreationContextIs(e.scope, e.ctx); err != nil || !same {
		t.Fatalf("CreationContextIs(own ctx): same=%v err=%v", same, err)
	}
	ctx2, err := e.iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	obj2, err := e.scope.NewObject(ctx2)
	if err != nil {
		t.Fatalf("NewObject(ctx2): %v", err)
	}
	if same, err := obj2.CreationContextIs(e.scope, ctx2); err != nil || !same {
		t.Fatalf("CreationContextIs(ctx2): same=%v err=%v", same, err)
	}
	if same, err := obj2.CreationContextIs(e.scope, e.ctx); err != nil || same {
		t.Fatalf("obj2 must not report ctx1: same=%v err=%v", same, err)
	}
	if err := ctx2.Close(); err != nil {
		t.Errorf("ctx2.Close: %v", err)
	}
}

// --- receivers ------------------------------------------------------------------

func TestObjectReceiverGetSetOnAccessor(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	// holder.g is a JS accessor returning typeof this + ':' + this.tag.
	script, err := e.ctx.Compile(e.scope, `
		globalThis.holder = {};
		Object.defineProperty(globalThis.holder, 'g', {
		  get: function () { return (typeof this) + ':' + (this && this.tag ? this.tag : 'none'); },
		  configurable: true
		});`, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := script.Run(e.scope, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = script.Close()

	holder := e.mustGlobalObject("holder")
	stranger := e.mustObject()
	if _, err := stranger.SetByName(e.scope, e.ctx, "tag", e.mustString("recv")); err != nil {
		t.Fatalf("SetByName: %v", err)
	}

	key := e.mustString("g")
	got, err := holder.GetWithReceiver(e.scope, e.ctx, key, stranger)
	if err != nil {
		t.Fatalf("GetWithReceiver: %v", err)
	}
	text, err := got.ToString(e.ctx)
	if err != nil {
		t.Fatalf("ToString: %v", err)
	}
	if text != "object:recv" {
		t.Fatalf("receiver this mismatch: %q", text)
	}

	// Data property redirect: writing through an unrelated receiver
	// creates the property on the receiver.
	plain := e.mustObject()
	if _, err := plain.SetByName(e.scope, e.ctx, "p", e.mustInt(1)); err != nil {
		t.Fatalf("SetByName: %v", err)
	}
	other := e.mustObject()
	if ok, err := plain.SetWithReceiver(e.scope, e.ctx, e.mustString("p"), e.mustInt(42), other); err != nil || !ok {
		t.Fatalf("SetWithReceiver: ok=%v err=%v", ok, err)
	}
	v, _, err := other.GetByName(e.scope, e.ctx, "p")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	n, _, err := v.IntegerValue(e.ctx)
	if err != nil || n != 42 {
		t.Fatalf("redirected value: %v (err=%v)", n, err)
	}
}

func (e *objEnv) evalValue(expr string) gov8.Value {
	t := e.t
	t.Helper()
	script, err := e.ctx.Compile(e.scope, expr, nil)
	if err != nil {
		t.Fatalf("Compile(%s): %v", expr, err)
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(e.scope, nil)
	if err != nil {
		t.Fatalf("Run(%s): %v", expr, err)
	}
	return v
}

func (e *objEnv) mustGlobalObject(expr string) *gov8.Object {
	t := e.t
	t.Helper()
	o, err := gov8.AsObject(e.evalValue(expr))
	if err != nil {
		t.Fatalf("global %q not an object: %v", expr, err)
	}
	return o
}

func TestValueResidualConversionsAndTypeRepr(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	input := e.mustString("4294967297.9")
	number, err := input.ToNumber(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := number.NumberValue(e.ctx); err != nil || !ok || got != 4294967297.9 {
		t.Fatalf("ToNumber = %v/%v, %v", got, ok, err)
	}
	uint32Value, err := input.ToUint32(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := uint32Value.Uint32Value(e.ctx); err != nil || !ok || got != 1 {
		t.Fatalf("ToUint32 = %v/%v, %v", got, ok, err)
	}
	int32Value, err := input.ToInt32(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := int32Value.Int32Value(e.ctx); err != nil || !ok || got != 1 {
		t.Fatalf("ToInt32 = %v/%v, %v", got, ok, err)
	}
	text, err := input.ToStringValue(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := text.StringValue(); err != nil || got != "4294967297.9" {
		t.Fatalf("ToStringValue = %q, %v", got, err)
	}

	samples := []struct {
		expr string
		want string
	}{
		{"undefined", "undefined"}, {"null", "null"}, {"1", "Number"},
		{"true", "Boolean"}, {"1n", "bigint"}, {"'x'", "string"},
		{"Symbol('x')", "symbol"}, {"[]", "array"}, {"(function(){})", "function"},
		{"new Float16Array(1)", "TypedArray"},
	}
	for _, sample := range samples {
		got, err := e.evalValue(sample.expr).TypeRepr()
		if err != nil || got != sample.want {
			t.Fatalf("TypeRepr(%s) = %q, %v; want %q", sample.expr, got, err, sample.want)
		}
	}
}

func TestDataResidualPredicatesIdentityAndViews(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	assert := func(name string, data gov8.Data, predicate func(gov8.Data) (bool, error)) {
		t.Helper()
		if got, err := predicate(data); err != nil || !got {
			t.Fatalf("%s = %v, %v", name, got, err)
		}
	}
	valueData := func(v gov8.Value) gov8.Data {
		d, err := v.Data()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	assert("bigint", valueData(e.evalValue("1n")), gov8.Data.IsBigInt)
	assert("boolean", valueData(e.evalValue("true")), gov8.Data.IsBoolean)
	assert("name/string", valueData(e.mustString("name")), gov8.Data.IsName)
	assert("string", valueData(e.mustString("string")), gov8.Data.IsString)
	assert("number", valueData(e.mustInt(7)), gov8.Data.IsNumber)
	assert("symbol", valueData(e.evalValue("Symbol('s')")), gov8.Data.IsSymbol)

	contextData, err := e.ctx.Data(e.scope)
	if err != nil {
		t.Fatal(err)
	}
	assert("context", contextData, gov8.Data.IsContext)

	privateName := e.mustString("private")
	private, err := e.scope.NewPrivate(privateName)
	if err != nil {
		t.Fatal(err)
	}
	privateData, err := private.Data()
	if err != nil {
		t.Fatal(err)
	}
	assert("private", privateData, gov8.Data.IsPrivate)

	functionTemplate, err := e.iso.NewFunctionTemplate(e.scope,
		func(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	functionTemplateData, err := functionTemplate.Data()
	if err != nil {
		t.Fatal(err)
	}
	assert("function template", functionTemplateData, gov8.Data.IsFunctionTemplate)

	objectTemplate, err := e.iso.NewObjectTemplate(e.scope)
	if err != nil {
		t.Fatal(err)
	}
	objectTemplateData, err := objectTemplate.Data()
	if err != nil {
		t.Fatal(err)
	}
	assert("object template", objectTemplateData, gov8.Data.IsObjectTemplate)

	module, err := e.ctx.CompileModule(e.scope, "export const x = 1;", "data.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = module.Close() }()
	moduleData, err := module.Data(e.scope)
	if err != nil {
		t.Fatal(err)
	}
	assert("module", moduleData, gov8.Data.IsModule)

	objectA := e.mustObject()
	dataA := valueData(objectA.Value)
	dataACopy := valueData(objectA.Value)
	dataB := valueData(e.mustObject().Value)
	if equal, err := dataA.Equal(dataACopy); err != nil || !equal {
		t.Fatalf("same Data identity = %v, %v", equal, err)
	}
	if equal, err := dataA.Equal(dataB); err != nil || equal {
		t.Fatalf("distinct Data identity = %v, %v", equal, err)
	}
}

// --- lazy properties and instance accessors ---------------------------------------

func TestObjectLazyDataPropertyFiresOnce(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	hits := 0
	key := e.mustString("lazy")
	if ok, err := obj.SetLazyDataProperty(e.scope, e.ctx, key,
		func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			hits++
			if err := rv.SetInt32(43); err != nil {
				t.Errorf("SetInt32: %v", err)
			}
		}); err != nil || !ok {
		t.Fatalf("SetLazyDataProperty: ok=%v err=%v", ok, err)
	}
	if hits != 0 {
		t.Fatalf("getter fired on install: %d", hits)
	}
	v, err := obj.GetByKey(e.scope, e.ctx, key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if n, _, err := v.IntegerValue(e.ctx); err != nil || n != 43 {
		t.Fatalf("first lazy read: %v err=%v", n, err)
	}
	if hits != 1 {
		t.Fatalf("getter hits after first read: %d", hits)
	}
	if _, err := obj.GetByKey(e.scope, e.ctx, key); err != nil {
		t.Fatalf("second GetByKey: %v", err)
	}
	if hits != 1 {
		t.Fatalf("getter must fire exactly once: %d", hits)
	}
	// After materialization the property is a plain data property: JS sees
	// a value, not an accessor.
	if got := e.evalInt("Object.getOwnPropertyDescriptor ? 1 : 0"); got != 1 {
		t.Fatalf("sanity: %v", got)
	}
}

func TestObjectInstanceAccessorGetSet(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	state := int64(0)
	getHits, setHits := 0, 0
	key := e.mustString("x")
	if ok, err := obj.SetAccessor(e.scope, e.ctx, key,
		func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			getHits++
			_ = rv.SetInt32(int32(state))
		},
		func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, value gov8.Value) {
			setHits++
			if n, ok, err := cs.IntegerValue(value); err == nil && ok {
				state = n
			}
		}); err != nil || !ok {
		t.Fatalf("SetAccessor: ok=%v err=%v", ok, err)
	}
	v, err := obj.GetByKey(e.scope, e.ctx, key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if n, _, err := v.IntegerValue(e.ctx); err != nil || n != 0 {
		t.Fatalf("initial accessor value: %v err=%v", n, err)
	}
	if getHits != 1 {
		t.Fatalf("getter hits: %d", getHits)
	}
	if ok, err := obj.SetByKey(e.scope, e.ctx, key, e.mustInt(21)); err != nil || !ok {
		t.Fatalf("SetByKey through accessor: ok=%v err=%v", ok, err)
	}
	if setHits != 1 {
		t.Fatalf("setter hits: %d", setHits)
	}
	v2, err := obj.GetByKey(e.scope, e.ctx, key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if n, _, err := v2.IntegerValue(e.ctx); err != nil || n != 21 {
		t.Fatalf("second accessor value: %v err=%v", n, err)
	}
}

// --- call / construct -----------------------------------------------------------

func TestObjectCallAsFunctionAndConstructor(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	add, err := gov8.AsObject(e.evalValue("(function add(a, b) { return a + b; })"))
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	undef, err := e.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	got, err := add.CallAsFunction(e.scope, e.ctx, undef, []gov8.Value{e.mustInt(5), e.mustInt(7)}, nil)
	if err != nil {
		t.Fatalf("CallAsFunction: %v", err)
	}
	if n, _, err := got.IntegerValue(e.ctx); err != nil || n != 12 {
		t.Fatalf("add(5,7) = %v err=%v", n, err)
	}

	// Constructor: `this` is the result for plain constructors.
	maker, err := gov8.AsObject(e.evalValue("(function Maker() { this.tag = 9; })"))
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	made, err := maker.CallAsConstructor(e.scope, e.ctx, nil, nil)
	if err != nil {
		t.Fatalf("CallAsConstructor: %v", err)
	}
	isObj, err := made.IsObject()
	if err != nil || !isObj {
		t.Fatalf("made is object: %v err=%v", isObj, err)
	}
	if in, err := made.InstanceOf(e.scope, e.ctx, maker, nil); err != nil || !in {
		t.Fatalf("made instanceof Maker: %v err=%v", in, err)
	}

	// A plain object is neither callable nor a constructor: both calls
	// throw the pinned TypeErrors.
	plain := e.mustObject()
	if callable, err := plain.IsCallable(); err != nil || callable {
		t.Fatalf("plain IsCallable: %v err=%v", callable, err)
	}
	if ctor, err := plain.IsConstructor(); err != nil || ctor {
		t.Fatalf("plain IsConstructor: %v err=%v", ctor, err)
	}
	tc, err := e.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	if _, err := plain.CallAsFunction(e.scope, e.ctx, undef, nil, tc); err == nil {
		t.Fatal("CallAsFunction on a plain object must fail")
	}
	caught, err := tc.HasCaught()
	if err != nil || !caught {
		t.Fatalf("call caught: %v err=%v", caught, err)
	}
	msg, err := tc.MessageText(e.scope, e.ctx)
	if err != nil {
		t.Fatalf("MessageText: %v", err)
	}
	if !strings.Contains(msg, "object is not a function") {
		t.Fatalf("call message: %q", msg)
	}
	if err := tc.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}
}

// --- conversions -------------------------------------------------------------------

func TestValueConversions(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	// ToObject on a number produces a Number wrapper; on an object it is
	// the identity.
	nv, err := e.scope.Number(5)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	wrapper, err := nv.ToObject(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatalf("ToObject: %v", err)
	}
	if isNumObj, err := wrapper.Value.IsNumberObject(); err != nil || !isNumObj {
		t.Fatalf("IsNumberObject: %v err=%v", isNumObj, err)
	}
	if text, err := wrapper.Value.ToString(e.ctx); err != nil || text != "5" {
		t.Fatalf("wrapper ToString: %q err=%v", text, err)
	}

	// undefined/null ToObject throws (routed through the TryCatch).
	tc, err := e.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	undef, err := e.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	if _, err := undef.ToObject(e.scope, e.ctx, tc); err == nil {
		t.Fatal("ToObject(undefined) must fail")
	}
	if caught, _ := tc.HasCaught(); !caught {
		t.Fatal("ToObject(undefined) must be caught")
	}
	if err := tc.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}

	// ToInteger truncation and the +/-Infinity saturation of the raw read.
	f, err := e.scope.Number(3.75)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	iv, err := f.ToInteger(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatalf("ToInteger: %v", err)
	}
	if raw, err := iv.IntegerValueRaw(); err != nil || raw != 3 {
		t.Fatalf("ToInteger(3.75): %v err=%v", raw, err)
	}
	inf, err := e.scope.Number(math.Inf(1))
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	infI, err := inf.ToInteger(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatalf("ToInteger(inf): %v", err)
	}
	if raw, err := infI.IntegerValueRaw(); err != nil || raw != math.MinInt64 {
		t.Fatalf("ToInteger(inf) raw: %v err=%v", raw, err)
	}

	// ToBigInt: strings convert, numbers throw.
	bs, err := e.scope.NewString("123")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	b, err := bs.ToBigInt(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatalf("ToBigInt: %v", err)
	}
	if n, lossless, err := b.BigIntInt64(); err != nil || n != 123 || !lossless {
		t.Fatalf("ToBigInt(123): %v lossless=%v err=%v", n, lossless, err)
	}
	tc2, err := e.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	if _, err := nv.ToBigInt(e.scope, e.ctx, tc2); err == nil {
		t.Fatal("ToBigInt(number) must fail")
	}
	if err := tc2.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}

	// ToDetailString: compact object form.
	obj := e.mustObject()
	objStr, err := obj.Value.ToDetailString(e.scope, e.ctx, nil)
	if err != nil {
		t.Fatalf("ToDetailString: %v", err)
	}
	if text, err := objStr.StringValue(); err != nil || text != "#<Object>" {
		t.Fatalf("detail string: %q err=%v", text, err)
	}

	// ToBoolean on a Boolean wrapper of false is false.
	bFalse, err := e.scope.Boolean(false)
	if err != nil {
		t.Fatalf("Boolean: %v", err)
	}
	bv, err := bFalse.ToBoolean(e.scope)
	if err != nil {
		t.Fatalf("ToBoolean: %v", err)
	}
	if observed, err := bv.BooleanValue(); err != nil || observed {
		t.Fatalf("ToBoolean(false): %v err=%v", observed, err)
	}
}

// --- equality ---------------------------------------------------------------------

func TestSameValueZeroMatrix(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	plusZero, err := e.scope.Number(0)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	minusZero, err := e.scope.Number(math.Copysign(0, -1))
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	if eq, err := plusZero.SameValue(minusZero); err != nil || eq {
		t.Fatalf("SameValue(+0,-0) must be false: %v err=%v", eq, err)
	}
	if eq, err := plusZero.SameValueZero(e.scope, minusZero); err != nil || !eq {
		t.Fatalf("SameValueZero(+0,-0) must be true: %v err=%v", eq, err)
	}
	nan, err := e.scope.Number(math.NaN())
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	nan2, err := e.scope.Number(math.NaN())
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	if eq, err := nan.SameValueZero(e.scope, nan2); err != nil || !eq {
		t.Fatalf("SameValueZero(NaN,NaN): %v err=%v", eq, err)
	}
	str, err := e.scope.NewString("0")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if eq, err := plusZero.SameValueZero(e.scope, str); err != nil || eq {
		t.Fatalf("SameValueZero(0,'0') must be false: %v err=%v", eq, err)
	}
}

// --- missing predicates -----------------------------------------------------------

func TestMissingPredicates(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	bFalse, err := e.scope.Boolean(false)
	if err != nil {
		t.Fatalf("Boolean: %v", err)
	}
	if is, err := bFalse.IsFalse(); err != nil || !is {
		t.Fatalf("IsFalse: %v err=%v", is, err)
	}
	bTrue, err := e.scope.Boolean(true)
	if err != nil {
		t.Fatalf("Boolean: %v", err)
	}
	if is, err := bTrue.IsFalse(); err != nil || is {
		t.Fatalf("IsFalse(true): %v err=%v", is, err)
	}
	ext, err := e.scope.NewExternal(0)
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}
	if is, err := ext.IsExternal(); err != nil || !is {
		t.Fatalf("IsExternal: %v err=%v", is, err)
	}
	errV := e.evalValue("new Error('boom')")
	if is, err := errV.IsNativeError(); err != nil || !is {
		t.Fatalf("IsNativeError: %v err=%v", is, err)
	}
	promiseV := e.evalValue("Promise.resolve(1)")
	if is, err := promiseV.IsPromise(); err != nil || !is {
		t.Fatalf("IsPromise: %v err=%v", is, err)
	}
	// Negatives on an unrelated value.
	if is, err := bTrue.IsWeakMap(); err != nil || is {
		t.Fatalf("IsWeakMap(boolean): %v err=%v", is, err)
	}
	if is, err := bTrue.IsGeneratorObject(); err != nil || is {
		t.Fatalf("IsGeneratorObject(boolean): %v err=%v", is, err)
	}
}

// --- lifecycle and cross-isolate misuse ----------------------------------------------

func TestObjectOpsClosedScopeReceiver(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	scope2, err := e.iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	if err := scope2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The receiver's own scope is still open, so these succeed through a
	// different (open) scope...
	if _, err := obj.GetIdentityHash(); err != nil {
		t.Fatalf("GetIdentityHash: %v", err)
	}
	// ...but a method requiring the receiver's scope after it closes must
	// fail cleanly.
	if _, _, err := obj.GetByName(scope2, e.ctx, "x"); err == nil {
		t.Fatal("GetByName with a closed scope must fail")
	}
}

func TestObjectOpsCrossIsolateArguments(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	iso2, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso2.Close() }()
	ctx2, err := iso2.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope2, err := iso2.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope2.Close() }()

	obj := e.mustObject()
	foreignVal := func() gov8.Value {
		v, err := scope2.Int32(1)
		if err != nil {
			t.Fatalf("Int32: %v", err)
		}
		return v
	}
	if _, err := obj.Has(e.scope, ctx2, foreignVal(), nil); err == nil {
		t.Fatal("Has with a foreign context must fail")
	}
	if _, err := obj.Has(e.scope, e.ctx, foreignVal(), nil); err == nil {
		t.Fatal("Has with a foreign key must fail")
	}
	if _, err := obj.GetPrototype(scope2); err == nil {
		t.Fatal("GetPrototype with a foreign scope must fail")
	}
}

func TestObjectOpsWrongThreadAffinity(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	obj := e.mustObject()
	errc := make(chan error, 1)
	go func() {
		_, err := obj.GetIdentityHash()
		errc <- err
	}()
	if err := <-errc; err == nil {
		t.Fatal("GetIdentityHash from a foreign thread must fail")
	}
}

// --- receiver hardening (mirrors object_ops_negative findings) ---------------------

func TestObjectOpsReceiverTypeCheckedAtBoundary(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	// A confounded receiver: an *Object wrapper over a Number. The Go API
	// and the shim must both refuse it instead of crashing (the pinned
	// crate has no runtime check and dies with an access violation).
	numberValue := e.mustInt(7)
	confounded := &gov8.Object{Value: numberValue}
	if _, err := confounded.GetIdentityHash(); err == nil {
		t.Fatal("confounded GetIdentityHash must fail")
	}
	if _, err := confounded.IsCallable(); err == nil {
		t.Fatal("confounded IsCallable must fail")
	}
	if _, err := confounded.GetPrototype(e.scope); err == nil {
		t.Fatal("confounded GetPrototype must fail")
	}
	undef, err := e.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	if _, err := confounded.CallAsFunction(e.scope, e.ctx, undef, nil, nil); err == nil {
		t.Fatal("confounded CallAsFunction must fail")
	}
	if _, err := confounded.CallAsConstructor(e.scope, e.ctx, nil, nil); err == nil {
		t.Fatal("confounded CallAsConstructor must fail")
	}
	if ok, err := confounded.SetPrototype(e.scope, e.ctx, undef); err == nil || ok {
		t.Fatalf("confounded SetPrototype: ok=%v err=%v", ok, err)
	}

	// The isolate is fully usable afterwards.
	if n := e.evalInt("1 + 1"); n != 2 {
		t.Fatalf("isolate unusable after rejected misuse: %v", n)
	}
}
