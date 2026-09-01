//go:build windows && amd64

// The 22 object-ops checks in the fixed oracle order (the order is part of
// the observable contract). Every check normalizes its observations with
// the rules of rust-oracle/src/json.rs: no addresses, no raw hash values
// (identity hashes are per-isolate seeded), exact engine strings for the
// pinned build. Mirrors rust-oracle/src/bin/conformance-object-ops.rs check
// by check.
package main

import (
	"math"

	gov8 "gov8"
)

// --- local helper closures over the runtime -------------------------------------

// caughtMessage is the exception message of the given TryCatch ("Uncaught "
// prefixed in this build); "" when nothing was caught.
func caughtMessage(t tester, r *runtime, tc *gov8.TryCatch) string {
	t.Helper()
	msg, err := tc.MessageText(r.scope, r.ctx)
	if err != nil {
		return ""
	}
	return msg
}

// nameKey builds a string Name value (the oracle's name_key).
func nameKey(t tester, r *runtime, name string) gov8.Value {
	t.Helper()
	return scopeString(t, r.scope, name)
}

// constructorNameOf is Object::get_constructor_name of the object produced
// by evaluating source ("" when it is not an object).
func constructorNameOf(t tester, r *runtime, source string) string {
	t.Helper()
	v, ok := r.globalValue(t, source)
	if !ok {
		return ""
	}
	o, err := gov8.AsObject(v)
	if err != nil {
		return ""
	}
	name, err := o.GetConstructorName(r.scope)
	if err != nil {
		t.Fatalf("GetConstructorName: %v", err)
	}
	return valueText(t, r, name)
}

// --- 1. proto ---------------------------------------------------------------------

// checkProtoGetAndSet mirrors obj-ops/proto/get_and_set.
func checkProtoGetAndSet(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	obj, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	objectPrototype := r.mustEval(t, "Object.prototype")
	protoOfPlain, err := obj.GetPrototype(r.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	protoPresent := protoOfPlain != (gov8.Value{}) // the engine always produces a value
	protoMatchesObjectPrototype, err := protoOfPlain.StrictEquals(objectPrototype)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}

	// The crate reports a null prototype as a *present* null value.
	objectPrototypeObject, err := gov8.AsObject(objectPrototype)
	if err != nil {
		t.Fatalf("AsObject(Object.prototype): %v", err)
	}
	opProto, err := objectPrototypeObject.GetPrototype(r.scope)
	if err != nil {
		t.Fatalf("GetPrototype(Object.prototype): %v", err)
	}
	objectPrototypeProtoIsNull, err := opProto.IsNull()
	if err != nil {
		t.Fatalf("IsNull: %v", err)
	}

	parent, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	setOk, err := obj.SetPrototype(r.scope, r.ctx, parent.Value)
	if err != nil {
		t.Fatalf("SetPrototype: %v", err)
	}
	objProto, err := obj.GetPrototype(r.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	protoIsParent, err := objProto.StrictEquals(parent.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}

	nullV := scopeNull(t, r.scope)
	setNullOk, err := obj.SetPrototype(r.scope, r.ctx, nullV)
	if err != nil {
		t.Fatalf("SetPrototype(null): %v", err)
	}
	objProto2, err := obj.GetPrototype(r.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	protoNullAfterNull, err := objProto2.IsNull()
	if err != nil {
		t.Fatalf("IsNull: %v", err)
	}

	// Cycle: a -> b is fine, then b -> a is refused. The API-level
	// SetPrototype does NOT raise "Cyclic __proto__ value": the attempt
	// yields an empty result without a pending exception and leaves both
	// prototypes untouched.
	a, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	b, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	chainOk, err := a.SetPrototype(r.scope, r.ctx, b.Value)
	if err != nil {
		t.Fatalf("SetPrototype chain: %v", err)
	}
	aProto, err := a.GetPrototype(r.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	aProtoIsB, err := aProto.StrictEquals(b.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}

	tc := r.tc(t)
	cyclicResult, cyclicErr := b.SetPrototype(r.scope, r.ctx, a.Value)
	cyclicCaught, err := tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	closeTryCatch(t, tc)
	bProtoStill, err := b.GetPrototype(r.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	bProtoIsObjectPrototype, err := bProtoStill.StrictEquals(objectPrototype)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	aProtoStill, err := a.GetPrototype(r.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	aProtoStillB, err := aProtoStill.StrictEquals(b.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}

	got := jobj(
		kv("proto_present", jbool(protoPresent)),
		kv("proto_matches_object_prototype", jbool(protoMatchesObjectPrototype)),
		kv("object_prototype_proto_is_null", jbool(objectPrototypeProtoIsNull)),
		kv("set_ok", optBool(setOk, errNil())),
		kv("proto_is_parent", jbool(protoIsParent)),
		kv("set_null_ok", optBool(setNullOk, errNil())),
		kv("proto_null_after_null", jbool(protoNullAfterNull)),
		kv("chain_ok", optBool(chainOk, errNil())),
		kv("a_proto_is_b", jbool(aProtoIsB)),
		kv("cyclic_result", optBool(cyclicResult, cyclicErr)),
		kv("cyclic_caught", jbool(cyclicCaught)),
		kv("b_proto_is_object_prototype", jbool(bProtoIsObjectPrototype)),
		kv("a_proto_still_b", jbool(aProtoStillB)),
	)
	want := jobj(
		kv("proto_present", jbool(true)),
		kv("proto_matches_object_prototype", jbool(true)),
		kv("object_prototype_proto_is_null", jbool(true)),
		kv("set_ok", jbool(true)),
		kv("proto_is_parent", jbool(true)),
		kv("set_null_ok", jbool(true)),
		kv("proto_null_after_null", jbool(true)),
		kv("chain_ok", jbool(true)),
		kv("a_proto_is_b", jbool(true)),
		kv("cyclic_result", jnull()),
		kv("cyclic_caught", jbool(false)),
		kv("b_proto_is_object_prototype", jbool(true)),
		kv("a_proto_still_b", jbool(true)),
	)
	return wantGot("obj-ops/proto/get_and_set", want, got)
}

// errNil is a nil error value for optBool at call sites (readability).
func errNil() error { return nil }

// --- 2. property ------------------------------------------------------------------

// checkHasDeleteFamily mirrors obj-ops/property/has_delete_family.
func checkHasDeleteFamily(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	if _, ok := r.eval(t, nil, `globalThis.o = {a: 1, 5: 'five'};
         Object.defineProperty(globalThis.o, 'fixed', {value: 1, configurable: false});
         function Base() {} Base.prototype.inherited = 1;
         globalThis.child = new Base();
         globalThis.arr = [1, 2, 3];
         globalThis.frozen = Object.freeze({x: 9});`); !ok {
		t.Fatal("setup eval failed")
	}
	o := r.globalObject(t, "o")
	child := r.globalObject(t, "child")
	arr := r.globalObject(t, "arr")
	frozen := r.globalObject(t, "frozen")

	hasA, err := o.Has(r.scope, r.ctx, nameKey(t, r, "a"), nil)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	hasMissing, err := o.Has(r.scope, r.ctx, nameKey(t, r, "missing"), nil)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	hasIndex5, err := o.HasIndex(r.scope, r.ctx, 5, nil)
	if err != nil {
		t.Fatalf("HasIndex: %v", err)
	}
	hasIndex7, err := o.HasIndex(r.scope, r.ctx, 7, nil)
	if err != nil {
		t.Fatalf("HasIndex: %v", err)
	}
	childHasInherited, err := child.Has(r.scope, r.ctx, nameKey(t, r, "inherited"), nil)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	childOwnInherited, err := child.HasOwnProperty(r.scope, r.ctx, nameKey(t, r, "inherited"), nil)
	if err != nil {
		t.Fatalf("HasOwnProperty: %v", err)
	}
	oOwnA, err := o.HasOwnProperty(r.scope, r.ctx, nameKey(t, r, "a"), nil)
	if err != nil {
		t.Fatalf("HasOwnProperty: %v", err)
	}

	delA, err := o.Delete(r.scope, r.ctx, nameKey(t, r, "a"), nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	hasAAfter, err := o.Has(r.scope, r.ctx, nameKey(t, r, "a"), nil)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	delMissing, err := o.Delete(r.scope, r.ctx, nameKey(t, r, "missing"), nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	delFixed, err := o.Delete(r.scope, r.ctx, nameKey(t, r, "fixed"), nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	delFrozenX, err := frozen.Delete(r.scope, r.ctx, nameKey(t, r, "x"), nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	delArr1, err := arr.DeleteIndex(r.scope, r.ctx, 1, nil)
	if err != nil {
		t.Fatalf("DeleteIndex: %v", err)
	}
	arrHoleUndefined := r.evalText(t, "arr[1] === undefined")
	arrHoleNotIn := r.evalText(t, "!(1 in arr)")
	arrLengthIntact := r.evalText(t, "arr.length")

	// Symbol-keyed delete through the same API.
	sym, err := r.scope.NewSymbol(gov8.Value{})
	if err != nil {
		t.Fatalf("NewSymbol: %v", err)
	}
	one := int32Val(t, r.scope, 1)
	symSet, err := o.SetByKey(r.scope, r.ctx, sym.Value, one)
	if err != nil {
		t.Fatalf("SetByKey: %v", err)
	}
	symHas, err := o.Has(r.scope, r.ctx, sym.Value, nil)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	symDel, err := o.Delete(r.scope, r.ctx, sym.Value, nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	symHasAfter, err := o.Has(r.scope, r.ctx, sym.Value, nil)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}

	// Key conversion: a plain object converts to "[object Object]" (no
	// throw, plain miss), while a null-prototype object cannot convert and
	// the API reports an empty maybe under a TryCatch.
	plainKeyValue := r.mustEval(t, "({})")
	plainKeyLookup, err := o.Has(r.scope, r.ctx, plainKeyValue, nil)
	if err != nil {
		t.Fatalf("Has(object key): %v", err)
	}
	plainKeyMiss := r.evalText(t, "o['[object Object]']")

	tc := r.tc(t)
	unconvertible, ok := r.eval(t, tc, "Object.create(null)")
	if !ok {
		t.Fatal("eval Object.create(null) failed")
	}
	badKeyHas, badKeyErr := o.Has(r.scope, r.ctx, unconvertible, tc)
	badKeyCaught, err := tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	badKeyMessage := caughtMessage(t, r, tc)
	closeTryCatch(t, tc)

	got := jobj(
		kv("has_a", jbool(hasA)),
		kv("has_missing", jbool(hasMissing)),
		kv("has_index_5", jbool(hasIndex5)),
		kv("has_index_7", jbool(hasIndex7)),
		kv("child_has_inherited", jbool(childHasInherited)),
		kv("child_own_inherited", jbool(childOwnInherited)),
		kv("o_own_a", jbool(oOwnA)),
		kv("del_a", jbool(delA)),
		kv("has_a_after", jbool(hasAAfter)),
		kv("del_missing", jbool(delMissing)),
		kv("del_fixed", jbool(delFixed)),
		kv("del_frozen_x", jbool(delFrozenX)),
		kv("del_arr_1", jbool(delArr1)),
		kv("arr_hole_undefined", jstr(arrHoleUndefined)),
		kv("arr_hole_not_in", jstr(arrHoleNotIn)),
		kv("arr_length_intact", jstr(arrLengthIntact)),
		kv("sym_set", jbool(symSet)),
		kv("sym_has", jbool(symHas)),
		kv("sym_del", jbool(symDel)),
		kv("sym_has_after", jbool(symHasAfter)),
		kv("plain_key_lookup", jbool(plainKeyLookup)),
		kv("plain_key_miss", jstr(plainKeyMiss)),
		kv("bad_key_has", optBool(badKeyHas, badKeyErr)),
		kv("bad_key_caught", jbool(badKeyCaught)),
		kv("bad_key_message", jstr(badKeyMessage)),
	)
	want := jobj(
		kv("has_a", jbool(true)),
		kv("has_missing", jbool(false)),
		kv("has_index_5", jbool(true)),
		kv("has_index_7", jbool(false)),
		kv("child_has_inherited", jbool(true)),
		kv("child_own_inherited", jbool(false)),
		kv("o_own_a", jbool(true)),
		kv("del_a", jbool(true)),
		kv("has_a_after", jbool(false)),
		kv("del_missing", jbool(true)),
		kv("del_fixed", jbool(false)),
		kv("del_frozen_x", jbool(false)),
		kv("del_arr_1", jbool(true)),
		kv("arr_hole_undefined", jstr("true")),
		kv("arr_hole_not_in", jstr("true")),
		kv("arr_length_intact", jstr("3")),
		kv("sym_set", jbool(true)),
		kv("sym_has", jbool(true)),
		kv("sym_del", jbool(true)),
		kv("sym_has_after", jbool(false)),
		kv("plain_key_lookup", jbool(false)),
		kv("plain_key_miss", jstr("undefined")),
		kv("bad_key_has", jnull()),
		kv("bad_key_caught", jbool(true)),
		kv("bad_key_message", jstr("Uncaught TypeError: Cannot convert object to primitive value")),
	)
	return wantGot("obj-ops/property/has_delete_family", want, got)
}

// --- 3. real-named interceptor bypass ------------------------------------------------

// objOpsCounters carries the per-check hit counters the oracle keeps in
// thread-local cells (check execution is single-threaded, so plain locals
// captured by the closures are deterministic).
type objOpsCounters struct {
	intercepted int
	lazy        int
	accGet      int
	accSet      int
	accState    int64
}

// checkRealNamedInterceptorBypass mirrors
// obj-ops/property/real_named_interceptor_bypass.
func checkRealNamedInterceptorBypass(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	hits := &objOpsCounters{}

	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			text, err := cs.ToString(key)
			if err != nil {
				return gov8.InterceptedNo
			}
			if text == "in_a" {
				hits.intercepted++
				if err := rv.SetInt32(10); err != nil {
					_ = err
				}
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler: %v", err)
	}
	io, _, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}

	// A real own property, created through the API (the setter interceptor
	// is absent, so plain set creates a real property).
	realSet, err := io.SetByKey(r.scope, r.ctx, nameKey(t, r, "real"), int32Val(t, r.scope, 3))
	if err != nil {
		t.Fatalf("SetByKey: %v", err)
	}

	// An inherited real property on a plain parent.
	parent, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	if _, err := parent.SetByKey(r.scope, r.ctx, nameKey(t, r, "inh"), int32Val(t, r.scope, 9)); err != nil {
		t.Fatalf("SetByKey: %v", err)
	}
	r.setGlobal(t, "parent", parent.Value)
	child := r.globalObject(t, "Object.create(parent)")

	// A real own read-only property.
	r.setGlobal(t, "io", io.Value)
	if _, ok := r.eval(t, nil, "Object.defineProperty(io, 'ro', {value: 4, writable: false})"); !ok {
		t.Fatal("defineProperty eval failed")
	}

	keyInA := nameKey(t, r, "in_a")
	interceptedGet, err := io.GetByKey(r.scope, r.ctx, keyInA)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	interceptedGetHits := hits.intercepted
	interceptedHas, err := io.Has(r.scope, r.ctx, keyInA, nil)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	interceptedHasHits := hits.intercepted
	interceptedOwn, err := io.HasOwnProperty(r.scope, r.ctx, keyInA, nil)
	if err != nil {
		t.Fatalf("HasOwnProperty: %v", err)
	}
	interceptedOwnHits := hits.intercepted

	// The real-named family never consults the interceptor.
	realGet, realGetFound, err := io.GetRealNamedProperty(r.scope, r.ctx, keyInA, nil)
	if err != nil {
		t.Fatalf("GetRealNamedProperty: %v", err)
	}
	_ = realGetFound
	realGetIsNone := realGet == (gov8.Value{})
	realGetHits := hits.intercepted
	realHasIntercepted, err := io.HasRealNamedProperty(r.scope, r.ctx, keyInA)
	if err != nil {
		t.Fatalf("HasRealNamedProperty: %v", err)
	}
	realHasHits := hits.intercepted

	keyReal := nameKey(t, r, "real")
	realGetReal, realFound, err := io.GetRealNamedProperty(r.scope, r.ctx, keyReal, nil)
	if err != nil {
		t.Fatalf("GetRealNamedProperty: %v", err)
	}
	realHasReal, err := io.HasRealNamedProperty(r.scope, r.ctx, keyReal)
	if err != nil {
		t.Fatalf("HasRealNamedProperty: %v", err)
	}
	realAttrs, realAttrsPresent, err := io.GetRealNamedPropertyAttributes(r.scope, r.ctx, keyReal)
	if err != nil {
		t.Fatalf("GetRealNamedPropertyAttributes: %v", err)
	}
	keyRo := nameKey(t, r, "ro")
	roAttrs, roAttrsPresent, err := io.GetRealNamedPropertyAttributes(r.scope, r.ctx, keyRo)
	if err != nil {
		t.Fatalf("GetRealNamedPropertyAttributes: %v", err)
	}

	keyInh := nameKey(t, r, "inh")
	childRealGet, childRealFound, err := child.GetRealNamedProperty(r.scope, r.ctx, keyInh, nil)
	if err != nil {
		t.Fatalf("GetRealNamedProperty: %v", err)
	}
	childRealHas, err := child.HasRealNamedProperty(r.scope, r.ctx, keyInh)
	if err != nil {
		t.Fatalf("HasRealNamedProperty: %v", err)
	}
	childRealAttrs, childRealAttrsPresent, err := child.GetRealNamedPropertyAttributes(r.scope, r.ctx, keyInh)
	if err != nil {
		t.Fatalf("GetRealNamedPropertyAttributes: %v", err)
	}

	keyMissing := nameKey(t, r, "missing")
	missingRealGet, missingRealGetFound, err := io.GetRealNamedProperty(r.scope, r.ctx, keyMissing, nil)
	if err != nil {
		t.Fatalf("GetRealNamedProperty: %v", err)
	}
	_ = missingRealGetFound
	missingRealGetIsNone := missingRealGet == (gov8.Value{})
	missingRealHas, err := io.HasRealNamedProperty(r.scope, r.ctx, keyMissing)
	if err != nil {
		t.Fatalf("HasRealNamedProperty: %v", err)
	}
	missingRealAttrs, missingRealAttrsPresent, err := io.GetRealNamedPropertyAttributes(r.scope, r.ctx, keyMissing)
	if err != nil {
		t.Fatalf("GetRealNamedPropertyAttributes: %v", err)
	}
	_ = realGet
	finalHits := hits.intercepted

	// Attribute encoding helper (None -> JSON null for a missing property).
	attrsJSON := func(attr gov8.PropertyAttribute, present bool, err error) jsonValue {
		if err != nil {
			t.Fatalf("attributes: %v", err)
		}
		if !present {
			return jnull()
		}
		return jint(int64(attr))
	}
	_ = realAttrsPresent
	_ = roAttrsPresent
	_ = childRealAttrsPresent
	_ = childRealFound
	_ = realFound

	got := jobj(
		kv("real_set", jbool(realSet)),
		kv("intercepted_get", jint(intOf(t, r, interceptedGet))),
		kv("intercepted_get_hits", jint(int64(interceptedGetHits))),
		kv("intercepted_has", optBool(interceptedHas, errNil())),
		kv("intercepted_has_hits", jint(int64(interceptedHasHits))),
		kv("intercepted_own", optBool(interceptedOwn, errNil())),
		kv("intercepted_own_hits", jint(int64(interceptedOwnHits))),
		kv("real_get_intercepted_is_none", jbool(realGetIsNone)),
		kv("real_get_hits_unchanged", jbool(realGetHits == interceptedOwnHits)),
		kv("real_has_intercepted", optBool(realHasIntercepted, errNil())),
		kv("real_has_hits_unchanged", jbool(realHasHits == interceptedOwnHits)),
		kv("real_get_real", jint(intOf(t, r, realGetReal))),
		kv("real_has_real", optBool(realHasReal, errNil())),
		kv("real_attrs", attrsJSON(realAttrs, realAttrsPresent, nil)),
		// defineProperty defaults: {value: 4, writable: false} leaves
		// enumerable=false and configurable=false, so the real attributes
		// are READ_ONLY | DONT_ENUM | DONT_DELETE = 7.
		kv("ro_attrs", attrsJSON(roAttrs, roAttrsPresent, nil)),
		kv("child_real_get", jint(intOf(t, r, childRealGet))),
		// HasRealNamedProperty is own-only in this build: the inherited
		// real property is found by GetRealNamedProperty (9) but not by
		// HasRealNamedProperty.
		kv("child_real_has", optBool(childRealHas, errNil())),
		kv("child_real_attrs", attrsJSON(childRealAttrs, childRealAttrsPresent, nil)),
		kv("missing_real_get_is_none", jbool(missingRealGetIsNone)),
		kv("missing_real_has", optBool(missingRealHas, errNil())),
		kv("missing_real_attrs", attrsJSON(missingRealAttrs, missingRealAttrsPresent, nil)),
		kv("final_hits", jint(int64(finalHits))),
	)
	want := jobj(
		kv("real_set", jbool(true)),
		kv("intercepted_get", jint(10)),
		kv("intercepted_get_hits", jint(1)),
		kv("intercepted_has", jbool(true)),
		kv("intercepted_has_hits", jint(2)),
		kv("intercepted_own", jbool(true)),
		kv("intercepted_own_hits", jint(3)),
		kv("real_get_intercepted_is_none", jbool(true)),
		kv("real_get_hits_unchanged", jbool(true)),
		kv("real_has_intercepted", jbool(false)),
		kv("real_has_hits_unchanged", jbool(true)),
		kv("real_get_real", jint(3)),
		kv("real_has_real", jbool(true)),
		kv("real_attrs", jint(0)),
		kv("ro_attrs", jint(7)),
		kv("child_real_get", jint(9)),
		kv("child_real_has", jbool(false)),
		kv("child_real_attrs", jint(0)),
		kv("missing_real_get_is_none", jbool(true)),
		kv("missing_real_has", jbool(false)),
		kv("missing_real_attrs", jnull()),
		kv("final_hits", jint(3)),
	)
	return wantGot("obj-ops/property/real_named_interceptor_bypass", want, got)
}

// --- 4. identity ---------------------------------------------------------------------

// checkIdentityHash mirrors obj-ops/identity/identity_hash.
func checkIdentityHash(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	obj, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	hash1, err := obj.GetIdentityHash()
	if err != nil {
		t.Fatalf("GetIdentityHash: %v", err)
	}
	hash2, err := obj.GetIdentityHash()
	if err != nil {
		t.Fatalf("GetIdentityHash: %v", err)
	}
	valueHash, err := obj.Value.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}

	got := jobj(
		kv("nonzero", jbool(hash1 != 0)),
		kv("stable", jbool(hash1 == hash2)),
		kv("matches_value_get_hash", jbool(int32(valueHash) == hash1)),
	)
	want := jobj(
		kv("nonzero", jbool(true)),
		kv("stable", jbool(true)),
		kv("matches_value_get_hash", jbool(true)),
	)
	return wantGot("obj-ops/identity/identity_hash", want, got)
}

// checkCreationContext mirrors obj-ops/identity/creation_context.
func checkCreationContext(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	plain, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	jsLiteralObj, err := gov8.AsObject(r.mustEval(t, "({made: 'in js'})"))
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	objectPrototypeObj, err := gov8.AsObject(r.mustEval(t, "Object.prototype"))
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	globalProxy := r.globalObject(t, "globalThis")

	plainIsCtx1, err := plain.CreationContextIs(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("CreationContextIs: %v", err)
	}
	jsLiteralIsCtx1, err := jsLiteralObj.CreationContextIs(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("CreationContextIs: %v", err)
	}
	objectPrototypeIsCtx1, err := objectPrototypeObj.CreationContextIs(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("CreationContextIs: %v", err)
	}
	globalIsCtx1, err := globalProxy.CreationContextIs(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("CreationContextIs: %v", err)
	}

	// An object created in a second context keeps that context even while
	// ctx1 remains the runtime's entered context.
	ctx2, err := r.iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	obj2, err := r.scope.NewObject(ctx2)
	if err != nil {
		t.Fatalf("NewObject(ctx2): %v", err)
	}
	obj2IsCtx2, err := obj2.CreationContextIs(r.scope, ctx2)
	if err != nil {
		t.Fatalf("CreationContextIs: %v", err)
	}
	closeContext(t, ctx2)

	got := jobj(
		kv("plain_is_ctx1", jbool(plainIsCtx1)),
		kv("js_literal_is_ctx1", jbool(jsLiteralIsCtx1)),
		kv("object_prototype_is_ctx1", jbool(objectPrototypeIsCtx1)),
		kv("global_is_ctx1", jbool(globalIsCtx1)),
		kv("obj2_is_ctx2", jbool(obj2IsCtx2)),
	)
	want := jobj(
		kv("plain_is_ctx1", jbool(true)),
		kv("js_literal_is_ctx1", jbool(true)),
		kv("object_prototype_is_ctx1", jbool(true)),
		kv("global_is_ctx1", jbool(true)),
		kv("obj2_is_ctx2", jbool(true)),
	)
	return wantGot("obj-ops/identity/creation_context", want, got)
}

// checkConstructorName mirrors obj-ops/identity/constructor_name.
func checkConstructorName(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	if _, ok := r.eval(t, nil, `function Foo() {}
         class Bar {}
         class Sub extends Bar {}
         globalThis.fooI = new Foo();
         globalThis.barI = new Bar();
         globalThis.subI = new Sub();
         globalThis.weirdI = Reflect.construct(Array, [], function Weird() {});`); !ok {
		t.Fatal("setup eval failed")
	}

	apiObject, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	apiObjectName, err := apiObject.GetConstructorName(r.scope)
	if err != nil {
		t.Fatalf("GetConstructorName: %v", err)
	}

	got := jobj(
		kv("api_object", jstr(valueText(t, r, apiObjectName))),
		kv("js_literal", jstr(constructorNameOf(t, r, "({})"))),
		kv("foo_instance", jstr(constructorNameOf(t, r, "fooI"))),
		kv("bar_instance", jstr(constructorNameOf(t, r, "barI"))),
		kv("sub_instance", jstr(constructorNameOf(t, r, "subI"))),
		kv("array_literal", jstr(constructorNameOf(t, r, "[]"))),
		kv("error_instance", jstr(constructorNameOf(t, r, "new Error('e')"))),
		kv("function_itself", jstr(constructorNameOf(t, r, "(function f() {})"))),
		kv("class_itself", jstr(constructorNameOf(t, r, "Bar"))),
		kv("reflect_construct_target", jstr(constructorNameOf(t, r, "weirdI"))),
	)
	want := jobj(
		kv("api_object", jstr("Object")),
		kv("js_literal", jstr("Object")),
		kv("foo_instance", jstr("Foo")),
		kv("bar_instance", jstr("Bar")),
		kv("sub_instance", jstr("Sub")),
		kv("array_literal", jstr("Array")),
		kv("error_instance", jstr("Error")),
		kv("function_itself", jstr("Function")),
		kv("class_itself", jstr("Function")),
		kv("reflect_construct_target", jstr("Weird")),
	)
	return wantGot("obj-ops/identity/constructor_name", want, got)
}

// --- 5. receiver ----------------------------------------------------------------------

// checkGetSetWithReceiver mirrors obj-ops/receiver/get_set_with_receiver.
func checkGetSetWithReceiver(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	if _, ok := r.eval(t, nil, `globalThis.holder = {};
         Object.defineProperty(globalThis.holder, 'g', {
           get: function () {
             return (typeof this) + ':' + ((this && this.tag) ? this.tag : 'none');
           },
           configurable: true
         });
         globalThis.proto = {
           get t() { return this.x; },
           set s(v) { this.saved = v; }
         };
         globalThis.child = Object.create(globalThis.proto);
         globalThis.child.x = 7;
         globalThis.stranger = {tag: 'recv'};
         globalThis.plain = {p: 1};`); !ok {
		t.Fatal("setup eval failed")
	}
	holder := r.globalObject(t, "holder")
	proto := r.globalObject(t, "proto")
	child := r.globalObject(t, "child")
	stranger := r.globalObject(t, "stranger")
	plain := r.globalObject(t, "plain")
	other, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}

	keyG := nameKey(t, r, "g")
	keyT := nameKey(t, r, "t")
	keyS := nameKey(t, r, "s")
	keyP := nameKey(t, r, "p")

	getDefault, err := holder.GetByKey(r.scope, r.ctx, keyG)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	getWithRecv, err := holder.GetWithReceiver(r.scope, r.ctx, keyG, stranger)
	if err != nil {
		t.Fatalf("GetWithReceiver: %v", err)
	}
	protoTDefault, err := proto.GetByKey(r.scope, r.ctx, keyT)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	protoTDefaultIsUndefined, err := protoTDefault.IsUndefined()
	if err != nil {
		t.Fatalf("IsUndefined: %v", err)
	}
	protoTChild, err := proto.GetWithReceiver(r.scope, r.ctx, keyT, child)
	if err != nil {
		t.Fatalf("GetWithReceiver: %v", err)
	}

	five := int32Val(t, r.scope, 5)
	setterViaReceiver, err := proto.SetWithReceiver(r.scope, r.ctx, keyS, five, child)
	if err != nil {
		t.Fatalf("SetWithReceiver: %v", err)
	}
	childSaved := r.evalText(t, "child.saved")
	six := int32Val(t, r.scope, 6)
	setterOnProto, err := proto.SetByKey(r.scope, r.ctx, keyS, six)
	if err != nil {
		t.Fatalf("SetByKey: %v", err)
	}
	protoSaved := r.evalText(t, "proto.saved")
	childSavedAfter := r.evalText(t, "child.saved")

	// Getter-only accessor: the write is silently dropped (sloppy mode).
	one := int32Val(t, r.scope, 1)
	setGetterOnly, err := holder.SetWithReceiver(r.scope, r.ctx, keyG, one, stranger)
	if err != nil {
		t.Fatalf("SetWithReceiver: %v", err)
	}
	gotUnchanged, err := holder.GetByKey(r.scope, r.ctx, keyG)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	getterUnchanged := valueText(t, r, gotUnchanged)

	// Data property redirect: writing through an unrelated receiver creates
	// the property on the receiver.
	fortyTwo := int32Val(t, r.scope, 42)
	redirect, err := plain.SetWithReceiver(r.scope, r.ctx, keyP, fortyTwo, other)
	if err != nil {
		t.Fatalf("SetWithReceiver: %v", err)
	}
	otherGot, err := other.GetByKey(r.scope, r.ctx, keyP)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	otherP := intOf(t, r, otherGot)
	plainGot, err := plain.GetByKey(r.scope, r.ctx, keyP)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	plainP := intOf(t, r, plainGot)

	got := jobj(
		kv("get_default", jstr(valueText(t, r, getDefault))),
		kv("get_with_receiver", jstr(valueText(t, r, getWithRecv))),
		kv("proto_t_default_is_undefined", jbool(protoTDefaultIsUndefined)),
		kv("proto_t_child", jint(intOf(t, r, protoTChild))),
		kv("setter_via_receiver", jbool(setterViaReceiver)),
		kv("child_saved", jstr(childSaved)),
		kv("setter_on_proto", jbool(setterOnProto)),
		kv("proto_saved", jstr(protoSaved)),
		kv("child_saved_after", jstr(childSavedAfter)),
		kv("set_getter_only", jbool(setGetterOnly)),
		kv("getter_unchanged", jstr(getterUnchanged)),
		kv("redirect", jbool(redirect)),
		kv("other_p", jint(otherP)),
		kv("plain_p", jint(plainP)),
	)
	want := jobj(
		kv("get_default", jstr("object:none")),
		kv("get_with_receiver", jstr("object:recv")),
		kv("proto_t_default_is_undefined", jbool(true)),
		kv("proto_t_child", jint(7)),
		kv("setter_via_receiver", jbool(true)),
		kv("child_saved", jstr("5")),
		kv("setter_on_proto", jbool(true)),
		kv("proto_saved", jstr("6")),
		kv("child_saved_after", jstr("5")),
		kv("set_getter_only", jbool(true)),
		kv("getter_unchanged", jstr("object:none")),
		kv("redirect", jbool(true)),
		kv("other_p", jint(42)),
		kv("plain_p", jint(1)),
	)
	return wantGot("obj-ops/receiver/get_set_with_receiver", want, got)
}

// --- 6. lazy / instance accessor ---------------------------------------------------------

// checkLazyDataProperty mirrors obj-ops/lazy/lazy_data_property.
func checkLazyDataProperty(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	hits := &objOpsCounters{}

	obj, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	r.setGlobal(t, "lo", obj.Value)
	key := nameKey(t, r, "lazy")

	install, err := obj.SetLazyDataProperty(r.scope, r.ctx, key,
		func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			hits.lazy++
			if err := rv.SetInt32(43); err != nil {
				t.Errorf("rv.SetInt32: %v", err)
			}
		})
	if err != nil {
		t.Fatalf("SetLazyDataProperty: %v", err)
	}
	ownBeforeRead, err := obj.HasOwnProperty(r.scope, r.ctx, key, nil)
	if err != nil {
		t.Fatalf("HasOwnProperty: %v", err)
	}
	hitsBeforeRead := hits.lazy

	firstGot, err := obj.GetByKey(r.scope, r.ctx, key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	first := intOf(t, r, firstGot)
	hitsAfterFirst := hits.lazy
	secondGot, err := obj.GetByKey(r.scope, r.ctx, key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	second := intOf(t, r, secondGot)
	hitsAfterSecond := hits.lazy

	// After materialization the property is a plain data property.
	descriptorGet := r.evalText(t, "typeof Object.getOwnPropertyDescriptor(lo, 'lazy').get")
	descriptorValue := r.evalText(t, "Object.getOwnPropertyDescriptor(lo, 'lazy').value")
	jsRead := r.evalText(t, "lo.lazy")
	hitsAfterJS := hits.lazy

	got := jobj(
		kv("install", optBool(install, errNil())),
		kv("own_before_read", jbool(ownBeforeRead)),
		kv("hits_before_read", jint(int64(hitsBeforeRead))),
		kv("first", jint(first)),
		kv("hits_after_first", jint(int64(hitsAfterFirst))),
		kv("second", jint(second)),
		kv("hits_after_second", jint(int64(hitsAfterSecond))),
		kv("descriptor_get", jstr(descriptorGet)),
		kv("descriptor_value", jstr(descriptorValue)),
		kv("js_read", jstr(jsRead)),
		kv("hits_after_js", jint(int64(hitsAfterJS))),
	)
	want := jobj(
		kv("install", jbool(true)),
		kv("own_before_read", jbool(true)),
		kv("hits_before_read", jint(0)),
		kv("first", jint(43)),
		kv("hits_after_first", jint(1)),
		kv("second", jint(43)),
		kv("hits_after_second", jint(1)),
		kv("descriptor_get", jstr("undefined")),
		kv("descriptor_value", jstr("43")),
		kv("js_read", jstr("43")),
		kv("hits_after_js", jint(1)),
	)
	return wantGot("obj-ops/lazy/lazy_data_property", want, got)
}

// checkInstanceAccessor mirrors obj-ops/lazy/instance_accessor.
func checkInstanceAccessor(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	hits := &objOpsCounters{}

	obj, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	r.setGlobal(t, "ia", obj.Value)
	key := nameKey(t, r, "x")

	install, err := obj.SetAccessor(r.scope, r.ctx, key,
		func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			hits.accGet++
			if err := rv.SetInt32(int32(hits.accState)); err != nil {
				t.Errorf("rv.SetInt32: %v", err)
			}
		},
		func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, value gov8.Value) {
			hits.accSet++
			if n, ok, err := cs.IntegerValue(value); err == nil && ok {
				hits.accState = n
			}
		})
	if err != nil {
		t.Fatalf("SetAccessor: %v", err)
	}

	firstGot, err := obj.GetByKey(r.scope, r.ctx, key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	first := intOf(t, r, firstGot)
	getHitsAfterFirst := hits.accGet

	write, err := obj.SetByKey(r.scope, r.ctx, key, int32Val(t, r.scope, 21))
	if err != nil {
		t.Fatalf("SetByKey: %v", err)
	}
	setHits := hits.accSet
	secondGot, err := obj.GetByKey(r.scope, r.ctx, key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	second := intOf(t, r, secondGot)
	getHitsAfterSecond := hits.accGet

	descHasGet := r.evalText(t, "typeof Object.getOwnPropertyDescriptor(ia, 'x').get === 'function'")
	descHasSet := r.evalText(t, "typeof Object.getOwnPropertyDescriptor(ia, 'x').set === 'function'")
	descValueIsUndefined := r.evalText(t, "Object.getOwnPropertyDescriptor(ia, 'x').value === undefined")
	// API-level accessors are AccessorInfo-backed: to JS descriptors they
	// appear as plain data properties carrying the current value, not as
	// get/set functions (unlike template-level set_accessor_property).
	descValueText := r.evalText(t, "String(Object.getOwnPropertyDescriptor(ia, 'x').value)")
	jsWrite := r.evalText(t, "(ia.x = 33) === 33")
	jsRead := r.evalText(t, "ia.x")

	got := jobj(
		kv("install", optBool(install, errNil())),
		kv("first", jint(first)),
		kv("get_hits_after_first", jint(int64(getHitsAfterFirst))),
		kv("write", jbool(write)),
		kv("set_hits", jint(int64(setHits))),
		kv("second", jint(second)),
		kv("get_hits_after_second", jint(int64(getHitsAfterSecond))),
		kv("desc_has_get", jstr(descHasGet)),
		kv("desc_has_set", jstr(descHasSet)),
		kv("desc_value_is_undefined", jstr(descValueIsUndefined)),
		kv("desc_value_text", jstr(descValueText)),
		kv("js_write", jstr(jsWrite)),
		kv("js_read", jstr(jsRead)),
	)
	want := jobj(
		kv("install", jbool(true)),
		kv("first", jint(0)),
		kv("get_hits_after_first", jint(1)),
		kv("write", jbool(true)),
		kv("set_hits", jint(1)),
		kv("second", jint(21)),
		kv("get_hits_after_second", jint(2)),
		kv("desc_has_get", jstr("false")),
		kv("desc_has_set", jstr("false")),
		kv("desc_value_is_undefined", jstr("false")),
		kv("desc_value_text", jstr("21")),
		kv("js_write", jstr("true")),
		kv("js_read", jstr("33")),
	)
	return wantGot("obj-ops/lazy/instance_accessor", want, got)
}

// --- 7. call -----------------------------------------------------------------------------

// checkCallPlainObjectNotCallable mirrors obj-ops/call/plain_object_not_callable.
func checkCallPlainObjectNotCallable(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	obj, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	isCallable, err := obj.IsCallable()
	if err != nil {
		t.Fatalf("IsCallable: %v", err)
	}
	isConstructor, err := obj.IsConstructor()
	if err != nil {
		t.Fatalf("IsConstructor: %v", err)
	}

	tc := r.tc(t)
	undef := scopeUndefined(t, r.scope)
	_, callErr := obj.CallAsFunction(r.scope, r.ctx, undef, nil, tc)
	callCaught, err := tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	callMessage := caughtMessage(t, r, tc)
	closeTryCatch(t, tc)

	tc2 := r.tc(t)
	_, ctorErr := obj.CallAsConstructor(r.scope, r.ctx, nil, tc2)
	ctorCaught, err := tc2.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	ctorMessage := caughtMessage(t, r, tc2)
	closeTryCatch(t, tc2)

	got := jobj(
		kv("is_callable", jbool(isCallable)),
		kv("is_constructor", jbool(isConstructor)),
		kv("call_result", jbool(callErr == nil)),
		kv("call_caught", jbool(callCaught)),
		kv("call_message", jstr(callMessage)),
		kv("ctor_result", jbool(ctorErr == nil)),
		kv("ctor_caught", jbool(ctorCaught)),
		kv("ctor_message", jstr(ctorMessage)),
	)
	want := jobj(
		kv("is_callable", jbool(false)),
		kv("is_constructor", jbool(false)),
		kv("call_result", jbool(false)),
		kv("call_caught", jbool(true)),
		kv("call_message", jstr("Uncaught TypeError: object is not a function")),
		kv("ctor_result", jbool(false)),
		kv("ctor_caught", jbool(true)),
		kv("ctor_message", jstr("Uncaught TypeError: object is not a constructor")),
	)
	return wantGot("obj-ops/call/plain_object_not_callable", want, got)
}

// checkCallFunctionCallAndConstruct mirrors
// obj-ops/call/function_call_and_construct.
func checkCallFunctionCallAndConstruct(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	add := r.globalObject(t, "(function add(a, b) { return a + b; })")
	what := r.globalObject(t, "(function what() { return this; })")
	maker := r.globalObject(t, "(function Maker() { this.tag = 'made'; })")
	returner := r.globalObject(t, "(function Returner() { return {custom: 1}; })")
	klass := r.globalObject(t, "(class K { constructor(v) { this.v = v; } })")
	arrow := r.globalObject(t, "((a) => a * 2)")

	context2, err := r.iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { closeContext(t, context2) }()
	global := r.globalObject(t, "globalThis")

	undef := scopeUndefined(t, r.scope)
	addGot, err := add.CallAsFunction(r.scope, r.ctx, undef, []gov8.Value{int32Val(t, r.scope, 5), int32Val(t, r.scope, 7)}, nil)
	if err != nil {
		t.Fatalf("CallAsFunction: %v", err)
	}
	addResult := intOf(t, r, addGot)

	ctxGot, err := add.CallAsFunction(r.scope, context2, undef, []gov8.Value{int32Val(t, r.scope, 20), int32Val(t, r.scope, 22)}, nil)
	if err != nil {
		t.Fatalf("CallAsFunction(context2): %v", err)
	}
	withContextResult := intOf(t, r, ctxGot)

	receiver, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	receiverGot, err := what.CallAsFunction(r.scope, r.ctx, receiver.Value, nil, nil)
	if err != nil {
		t.Fatalf("CallAsFunction: %v", err)
	}
	boundReceiver, err := receiverGot.StrictEquals(receiver.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	globalGot, err := what.CallAsFunction(r.scope, r.ctx, undef, nil, nil)
	if err != nil {
		t.Fatalf("CallAsFunction: %v", err)
	}
	undefinedReceiverIsGlobal, err := globalGot.StrictEquals(global.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}

	made, err := maker.CallAsConstructor(r.scope, r.ctx, []gov8.Value{int32Val(t, r.scope, 0)}, nil)
	if err != nil {
		t.Fatalf("CallAsConstructor: %v", err)
	}
	madeIsObject, err := made.IsObject()
	if err != nil {
		t.Fatalf("IsObject: %v", err)
	}
	madeObj, err := gov8.AsObject(made)
	if err != nil {
		t.Fatalf("AsObject(made): %v", err)
	}
	madeTagVal, err := madeObj.GetByKey(r.scope, r.ctx, nameKey(t, r, "tag"))
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	madeTag := valueText(t, r, madeTagVal)
	madeInstanceofMaker, err := made.InstanceOf(r.scope, r.ctx, maker, nil)
	if err != nil {
		t.Fatalf("InstanceOf: %v", err)
	}
	madeWithContext, err := maker.CallAsConstructor(r.scope, context2, nil, nil)
	if err != nil {
		t.Fatalf("CallAsConstructor(context2): %v", err)
	}
	madeWithIsObject, err := madeWithContext.IsObject()
	if err != nil {
		t.Fatalf("IsObject: %v", err)
	}

	returned, err := returner.CallAsConstructor(r.scope, r.ctx, nil, nil)
	if err != nil {
		t.Fatalf("CallAsConstructor: %v", err)
	}
	returnedObj, err := gov8.AsObject(returned)
	if err != nil {
		t.Fatalf("AsObject(returned): %v", err)
	}
	returnedCustomVal, err := returnedObj.GetByKey(r.scope, r.ctx, nameKey(t, r, "custom"))
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	returnedCustom := intOf(t, r, returnedCustomVal)
	returnedInstanceofReturner, err := returned.InstanceOf(r.scope, r.ctx, returner, nil)
	if err != nil {
		t.Fatalf("InstanceOf: %v", err)
	}

	klassConstructed, err := klass.CallAsConstructor(r.scope, r.ctx, []gov8.Value{int32Val(t, r.scope, 9)}, nil)
	if err != nil {
		t.Fatalf("CallAsConstructor: %v", err)
	}
	klassObj, err := gov8.AsObject(klassConstructed)
	if err != nil {
		t.Fatalf("AsObject(klass): %v", err)
	}
	klassVVal, err := klassObj.GetByKey(r.scope, r.ctx, nameKey(t, r, "v"))
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	klassConstructV := intOf(t, r, klassVVal)

	tc := r.tc(t)
	_, klassCallErr := klass.CallAsFunction(r.scope, r.ctx, undef, nil, tc)
	klassCallCaught, err := tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	klassCallMessage := caughtMessage(t, r, tc)
	closeTryCatch(t, tc)

	arrowGot, err := arrow.CallAsFunction(r.scope, r.ctx, undef, []gov8.Value{int32Val(t, r.scope, 21)}, nil)
	if err != nil {
		t.Fatalf("CallAsFunction: %v", err)
	}
	arrowCall := intOf(t, r, arrowGot)

	tc2 := r.tc(t)
	_, arrowConstructErr := arrow.CallAsConstructor(r.scope, r.ctx, nil, tc2)
	arrowConstructCaught, err := tc2.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	closeTryCatch(t, tc2)

	got := jobj(
		kv("add_result", jint(addResult)),
		kv("with_context_result", jint(withContextResult)),
		kv("bound_receiver", jbool(boundReceiver)),
		kv("undefined_receiver_is_global", jbool(undefinedReceiverIsGlobal)),
		kv("made_is_object", jbool(madeIsObject)),
		kv("made_tag", jstr(madeTag)),
		kv("made_instanceof_maker", jbool(madeInstanceofMaker)),
		kv("made_with_context", jbool(madeWithIsObject)),
		kv("returned_custom", jint(returnedCustom)),
		kv("returned_instanceof_returner", jbool(returnedInstanceofReturner)),
		kv("klass_construct_v", jint(klassConstructV)),
		kv("klass_call", jbool(klassCallErr == nil)),
		kv("klass_call_caught", jbool(klassCallCaught)),
		kv("klass_call_message", jstr(klassCallMessage)),
		kv("arrow_call", jint(arrowCall)),
		kv("arrow_construct", jbool(arrowConstructErr == nil)),
		kv("arrow_construct_caught", jbool(arrowConstructCaught)),
	)
	want := jobj(
		kv("add_result", jint(12)),
		kv("with_context_result", jint(42)),
		kv("bound_receiver", jbool(true)),
		kv("undefined_receiver_is_global", jbool(true)),
		kv("made_is_object", jbool(true)),
		kv("made_tag", jstr("made")),
		kv("made_instanceof_maker", jbool(true)),
		kv("made_with_context", jbool(true)),
		kv("returned_custom", jint(1)),
		kv("returned_instanceof_returner", jbool(false)),
		kv("klass_construct_v", jint(9)),
		kv("klass_call", jbool(false)),
		kv("klass_call_caught", jbool(true)),
		kv("klass_call_message", jstr("Uncaught TypeError: Class constructor K cannot be invoked without 'new'")),
		kv("arrow_call", jint(42)),
		kv("arrow_construct", jbool(false)),
		kv("arrow_construct_caught", jbool(true)),
	)
	return wantGot("obj-ops/call/function_call_and_construct", want, got)
}

// checkCallableConstructorPredicates mirrors
// obj-ops/call/callable_constructor_predicates.
func checkCallableConstructorPredicates(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	samples := []struct {
		name   string
		source string
	}{
		{"plain_object", "({})"},
		{"function", "(function f() {})"},
		{"arrow", "((a) => a)"},
		{"generator_function", "(function* g() {})"},
		{"async_function", "(async function af() {})"},
		{"class_constructor", "(class K {})"},
		{"method", "({ m() {} }).m"},
		{"bound_function", "(function g() {}.bind({}))"},
		{"proxy_of_function", "(new Proxy(function () {}, {}))"},
		{"proxy_of_object", "(new Proxy({}, {}))"},
		{"builtin_nonconstructable", "Math.max"},
		{"builtin_constructable", "Date"},
	}

	gotPairs := make(jsonObj, 0, len(samples))
	wantPairs := make(jsonObj, 0, len(samples))
	expectations := map[string][2]bool{
		"plain_object":             {false, false},
		"function":                 {true, true},
		"arrow":                    {true, false},
		"generator_function":       {true, false},
		"async_function":           {true, false},
		"class_constructor":        {true, true},
		"method":                   {true, false},
		"bound_function":           {true, true},
		"proxy_of_function":        {true, true},
		"proxy_of_object":          {false, false},
		"builtin_nonconstructable": {true, false},
		"builtin_constructable":    {true, true},
	}
	for _, s := range samples {
		o := r.globalObject(t, s.source)
		callable, err := o.IsCallable()
		if err != nil {
			t.Fatalf("IsCallable(%s): %v", s.name, err)
		}
		constructor, err := o.IsConstructor()
		if err != nil {
			t.Fatalf("IsConstructor(%s): %v", s.name, err)
		}
		gotPairs = append(gotPairs, kv(s.name, jobj(
			kv("is_callable", jbool(callable)),
			kv("is_constructor", jbool(constructor)),
		)))
		wantPairs = append(wantPairs, kv(s.name, jobj(
			kv("is_callable", jbool(expectations[s.name][0])),
			kv("is_constructor", jbool(expectations[s.name][1])),
		)))
	}
	return wantGot("obj-ops/call/callable_constructor_predicates", jobj(wantPairs...), jobj(gotPairs...))
}

// --- 8. convert ---------------------------------------------------------------------------

// checkToObjectMatrix mirrors obj-ops/convert/to_object.
func checkToObjectMatrix(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	numberProto := r.mustEval(t, "Number.prototype")
	plain, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}

	undefinedResult := false
	undefinedCaught := false
	{
		tc := r.tc(t)
		undef := scopeUndefined(t, r.scope)
		_, undefinedErr := undef.ToObject(r.scope, r.ctx, tc)
		undefinedResult = undefinedErr == nil
		undefinedCaught = mustHasCaught(t, tc)
		closeTryCatch(t, tc)
	}
	nullResult := false
	nullCaught := false
	{
		tc := r.tc(t)
		_, nullErr := scopeNull(t, r.scope).ToObject(r.scope, r.ctx, tc)
		nullResult = nullErr == nil
		nullCaught = mustHasCaught(t, tc)
		closeTryCatch(t, tc)
	}

	number := scopeNumber(t, r.scope, 5.0)
	wrapperNumber, err := number.ToObject(r.scope, r.ctx, nil)
	if err != nil {
		t.Fatalf("ToObject(number): %v", err)
	}
	isNumberObject, err := wrapperNumber.Value.IsNumberObject()
	if err != nil {
		t.Fatalf("IsNumberObject: %v", err)
	}
	typeOfValue, err := wrapperNumber.Value.TypeOf(r.scope)
	if err != nil {
		t.Fatalf("TypeOf: %v", err)
	}
	typeOfText := valueText(t, r, typeOfValue)
	toStringText, err := wrapperNumber.Value.ToString(r.ctx)
	if err != nil {
		t.Fatalf("ToString: %v", err)
	}
	protoOfWrapper, err := wrapperNumber.GetPrototype(r.scope)
	if err != nil {
		t.Fatalf("GetPrototype: %v", err)
	}
	protoMatches, err := protoOfWrapper.StrictEquals(numberProto)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}

	stringWrapped := false
	if so, serr := scopeString(t, r.scope, "hi").ToObject(r.scope, r.ctx, nil); serr == nil {
		stringWrapped = mustPred(t, so.Value.IsStringObject)
	}
	booleanWrapped := false
	if bo, berr := scopeBoolean(t, r.scope, true).ToObject(r.scope, r.ctx, nil); berr == nil {
		booleanWrapped = mustPred(t, bo.Value.IsBooleanObject)
	}
	symbolWrapped := false
	sym, serr := r.scope.NewSymbol(gov8.Value{})
	if serr != nil {
		t.Fatalf("NewSymbol: %v", err)
	}
	if so, oerr := sym.Value.ToObject(r.scope, r.ctx, nil); oerr == nil {
		symbolWrapped = mustPred(t, so.Value.IsSymbolObject)
	}
	bigintWrapped := false
	if bo, oerr := scopeBigIntI64(t, r.scope, 7).ToObject(r.scope, r.ctx, nil); oerr == nil {
		bigintWrapped = mustPred(t, bo.Value.IsBigIntObject)
	}
	identity := false
	if po, oerr := plain.Value.ToObject(r.scope, r.ctx, nil); oerr == nil {
		identity = mustStrictEquals(t, po.Value, plain.Value)
	}

	got := jobj(
		kv("undefined_result", jbool(undefinedResult)),
		kv("undefined_caught", jbool(undefinedCaught)),
		kv("null_result", jbool(nullResult)),
		kv("null_caught", jbool(nullCaught)),
		kv("number_wrapper", jobj(
			kv("is_number_object", jbool(isNumberObject)),
			kv("type_of", jstr(typeOfText)),
			kv("to_string", jstr(toStringText)),
			kv("proto_is_number_prototype", jbool(protoMatches)),
		)),
		kv("string_wrapper", jbool(stringWrapped)),
		kv("boolean_wrapper", jbool(booleanWrapped)),
		kv("symbol_wrapper", jbool(symbolWrapped)),
		kv("bigint_wrapper", jbool(bigintWrapped)),
		kv("object_identity", jbool(identity)),
	)
	want := jobj(
		kv("undefined_result", jbool(false)),
		kv("undefined_caught", jbool(true)),
		kv("null_result", jbool(false)),
		kv("null_caught", jbool(true)),
		kv("number_wrapper", jobj(
			kv("is_number_object", jbool(true)),
			kv("type_of", jstr("object")),
			kv("to_string", jstr("5")),
			kv("proto_is_number_prototype", jbool(true)),
		)),
		kv("string_wrapper", jbool(true)),
		kv("boolean_wrapper", jbool(true)),
		kv("symbol_wrapper", jbool(true)),
		kv("bigint_wrapper", jbool(true)),
		kv("object_identity", jbool(true)),
	)
	return wantGot("obj-ops/convert/to_object", want, got)
}

// checkToBooleanMatrix mirrors obj-ops/convert/to_boolean.
func checkToBooleanMatrix(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	negZero := math.Copysign(0, -1)
	type sample struct {
		name string
		v    gov8.Value
	}
	samples := []sample{
		{"undefined", scopeUndefined(t, r.scope)},
		{"null", scopeNull(t, r.scope)},
		{"false", scopeBoolean(t, r.scope, false)},
		{"zero", int32Val(t, r.scope, 0)},
		{"neg_zero", scopeNumber(t, r.scope, negZero)},
		{"nan", scopeNumber(t, r.scope, math.NaN())},
		{"empty_string", scopeString(t, r.scope, "")},
		{"string_zero", scopeString(t, r.scope, "0")},
		{"int_42", int32Val(t, r.scope, 42)},
		{"float_1p5", scopeNumber(t, r.scope, 1.5)},
		{"true", scopeBoolean(t, r.scope, true)},
		{"empty_object", mustNewObjectValue(t, r)},
		{"empty_array", r.mustEval(t, "[]")},
		{"string_false", scopeString(t, r.scope, "false")},
	}
	falsy := map[string]bool{
		"undefined": true, "null": true, "false": true, "zero": true,
		"neg_zero": true, "nan": true, "empty_string": true,
	}
	gotPairs := make(jsonObj, 0, len(samples))
	wantPairs := make(jsonObj, 0, len(samples))
	for _, s := range samples {
		b, err := s.v.ToBoolean(r.scope)
		if err != nil {
			t.Fatalf("ToBoolean(%s): %v", s.name, err)
		}
		observed := mustPred(t, b.BooleanValue)
		gotPairs = append(gotPairs, kv(s.name, jbool(observed)))
		wantPairs = append(wantPairs, kv(s.name, jbool(!falsy[s.name])))
	}
	return wantGot("obj-ops/convert/to_boolean", jobj(wantPairs...), jobj(gotPairs...))
}

// toIntegerI64 is the oracle's to_integer_i64: ToInteger read as a raw
// Integer value (-1 when the conversion throws).
func toIntegerI64(t tester, r *runtime, tc *gov8.TryCatch, v gov8.Value) int64 {
	t.Helper()
	i, err := v.ToInteger(r.scope, r.ctx, tc)
	if err != nil {
		return -1
	}
	n, err := i.IntegerValueRaw()
	if err != nil {
		t.Fatalf("IntegerValueRaw: %v", err)
	}
	return n
}

// checkToIntegerMatrix mirrors obj-ops/convert/to_integer.
func checkToIntegerMatrix(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	nan := scopeNumber(t, r.scope, math.NaN())
	infinity := scopeNumber(t, r.scope, math.Inf(1))
	negInfinity := scopeNumber(t, r.scope, math.Inf(-1))

	samples := []struct {
		name string
		v    gov8.Value
	}{
		{"float_3p75", scopeNumber(t, r.scope, 3.75)},
		{"float_neg_3p75", scopeNumber(t, r.scope, -3.75)},
		{"string_42", scopeString(t, r.scope, "42")},
		{"string_empty", scopeString(t, r.scope, "")},
		{"null", scopeNull(t, r.scope)},
		{"undefined", scopeUndefined(t, r.scope)},
		{"true", scopeBoolean(t, r.scope, true)},
		{"nan_truncates_to_zero", nan},
		{"object_empty", r.mustEval(t, "({})")},
	}
	gotSamples := make(jsonObj, 0, len(samples)+1)
	wantSamples := []jsonPair{
		kv("float_3p75", jint(3)),
		kv("float_neg_3p75", jint(-3)),
		kv("string_42", jint(42)),
		kv("string_empty", jint(0)),
		kv("null", jint(0)),
		kv("undefined", jint(0)),
		kv("true", jint(1)),
		kv("nan_truncates_to_zero", jint(0)),
		kv("object_empty", jint(0)),
		kv("big_int_throws", jint(-1)),
	}
	for _, s := range samples {
		gotSamples = append(gotSamples, kv(s.name, jint(toIntegerI64(t, r, nil, s.v))))
	}
	{
		tc := r.tc(t)
		bigIntThrows := toIntegerI64(t, r, tc, scopeBigIntI64(t, r.scope, 1))
		gotSamples = append(gotSamples, kv("big_int_throws", jint(bigIntThrows)))
		closeTryCatch(t, tc)
	}

	infinityRaw := toIntegerI64(t, r, nil, infinity)
	negInfinityRaw := toIntegerI64(t, r, nil, negInfinity)
	{
		tc := r.tc(t)
		_ = toIntegerI64(t, r, tc, infinity)
		infinityCaught := mustHasCaught(t, tc)
		closeTryCatch(t, tc)

		got := jobj(
			kv("samples", jobj(gotSamples...)),
			kv("infinity", jint(infinityRaw)),
			kv("neg_infinity", jint(negInfinityRaw)),
			kv("infinity_caught", jbool(infinityCaught)),
		)
		want := jobj(
			kv("samples", jobj(wantSamples...)),
			// ToInteger keeps the double at +/-Infinity; the raw read
			// saturates through the C++ double -> int64 cast on x86_64
			// (cvttsd2si returns 0x8000000000000000 for out-of-range).
			kv("infinity", jint(math.MinInt64)),
			kv("neg_infinity", jint(math.MinInt64)),
			kv("infinity_caught", jbool(false)),
		)
		return wantGot("obj-ops/convert/to_integer", want, got)
	}
}

// toBigIntTriple is the oracle's to_big_int_triple: (present, i64 value or
// -1, caught).
func toBigIntTriple(t tester, r *runtime, tc *gov8.TryCatch, v gov8.Value) (bool, int64, bool) {
	t.Helper()
	b, err := v.ToBigInt(r.scope, r.ctx, tc)
	if err != nil {
		return false, -1, mustHasCaught(t, tc)
	}
	n, _, err := b.BigIntInt64()
	if err != nil {
		t.Fatalf("BigIntInt64: %v", err)
	}
	return true, n, false
}

func bigIntTripleJSON(present bool, value int64, caught bool) jsonValue {
	return jobj(
		kv("present", jbool(present)),
		kv("value", jint(value)),
		kv("caught", jbool(caught)),
	)
}

// checkToBigIntMatrix mirrors obj-ops/convert/to_big_int.
func checkToBigIntMatrix(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	read := func(v gov8.Value) jsonValue {
		tc := r.tc(t)
		present, value, caught := toBigIntTriple(t, r, tc, v)
		closeTryCatch(t, tc)
		return bigIntTripleJSON(present, value, caught)
	}

	numberMessage := ""
	{
		tc := r.tc(t)
		_, merr := int32Val(t, r.scope, 42).ToBigInt(r.scope, r.ctx, tc)
		if merr == nil {
			t.Fatal("ToBigInt(42) unexpectedly succeeded")
		}
		numberMessage = caughtMessage(t, r, tc)
		closeTryCatch(t, tc)
	}

	got := jobj(
		kv("number_42", read(int32Val(t, r.scope, 42))),
		kv("float_1p5", read(scopeNumber(t, r.scope, 1.5))),
		kv("bool_true", read(scopeBoolean(t, r.scope, true))),
		kv("string_123", read(scopeString(t, r.scope, "123"))),
		kv("string_1p5", read(scopeString(t, r.scope, "1.5"))),
		kv("string_hex", read(scopeString(t, r.scope, "0x10"))),
		kv("undefined", read(scopeUndefined(t, r.scope))),
		kv("bigint_identity", read(scopeBigIntI64(t, r.scope, -9))),
		kv("number_message", jstr(numberMessage)),
	)
	want := jobj(
		kv("number_42", bigIntTripleJSON(false, -1, true)),
		kv("float_1p5", bigIntTripleJSON(false, -1, true)),
		kv("bool_true", bigIntTripleJSON(true, 1, false)),
		kv("string_123", bigIntTripleJSON(true, 123, false)),
		kv("string_1p5", bigIntTripleJSON(false, -1, true)),
		kv("string_hex", bigIntTripleJSON(true, 16, false)),
		kv("undefined", bigIntTripleJSON(false, -1, true)),
		kv("bigint_identity", bigIntTripleJSON(true, -9, false)),
		// The message embeds the concrete number, not the type name.
		kv("number_message", jstr("Uncaught TypeError: Cannot convert 42 to a BigInt")),
	)
	return wantGot("obj-ops/convert/to_big_int", want, got)
}

// toDetailPair is the oracle's to_detail_pair: (present, text).
func toDetailPair(t tester, r *runtime, tc *gov8.TryCatch, v gov8.Value) (bool, string) {
	t.Helper()
	s, err := v.ToDetailString(r.scope, r.ctx, tc)
	if err != nil {
		return false, ""
	}
	return true, valueText(t, r, s)
}

func detailPairJSON(present bool, text string) jsonValue {
	return jobj(
		kv("present", jbool(present)),
		kv("text", jstr(text)),
	)
}

// checkToDetailStringMatrix mirrors obj-ops/convert/to_detail_string.
func checkToDetailStringMatrix(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	read := func(v gov8.Value) jsonValue {
		tc := r.tc(t)
		present, text := toDetailPair(t, r, tc, v)
		closeTryCatch(t, tc)
		return detailPairJSON(present, text)
	}

	got := jobj(
		kv("undefined", read(scopeUndefined(t, r.scope))),
		kv("null", read(scopeNull(t, r.scope))),
		kv("int_42", read(int32Val(t, r.scope, 42))),
		kv("float_2p5", read(scopeNumber(t, r.scope, 2.5))),
		kv("true", read(scopeBoolean(t, r.scope, true))),
		kv("string", read(scopeString(t, r.scope, "plain"))),
		kv("symbol", read(scopeSymbolNamed(t, r, "gov8"))),
		kv("error", read(r.mustEval(t, "new TypeError('bad')"))),
		kv("plain_object", read(mustNewObjectValue(t, r))),
	)
	want := jobj(
		kv("undefined", detailPairJSON(true, "undefined")),
		kv("null", detailPairJSON(true, "null")),
		kv("int_42", detailPairJSON(true, "42")),
		kv("float_2p5", detailPairJSON(true, "2.5")),
		kv("true", detailPairJSON(true, "true")),
		kv("string", detailPairJSON(true, "plain")),
		kv("symbol", detailPairJSON(true, "Symbol(gov8)")),
		// ToDetailString renders errors via ToString WITHOUT the "Uncaught"
		// prefix (that prefix belongs to Message::get), and non-string
		// JSReceiver objects as V8's compact "#<Object>" form rather than
		// the JS ToString "[object Object]".
		kv("error", detailPairJSON(true, "TypeError: bad")),
		kv("plain_object", detailPairJSON(true, "#<Object>")),
	)
	return wantGot("obj-ops/convert/to_detail_string", want, got)
}

// --- 9. instanceof ------------------------------------------------------------------------

// checkAPIInstanceOf mirrors obj-ops/instanceof/api_instance_of.
func checkAPIInstanceOf(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	if _, ok := r.eval(t, nil, `function P() {}
         globalThis.pi = Object.create(P.prototype);`); !ok {
		t.Fatal("setup eval failed")
	}

	plain, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	objectCtor := r.globalObject(t, "Object")
	pi := r.mustGlobalValue(t, "pi")
	pCtor := r.globalObject(t, "P")
	functionCtor := r.globalObject(t, "Function")
	numberCtor := r.globalObject(t, "Number")
	functionValue := r.mustGlobalValue(t, "(function f() {})")
	arrow := r.mustGlobalValue(t, "((a) => a)")
	five := scopeNumber(t, r.scope, 5.0)
	nullV := scopeNull(t, r.scope)

	plainIsObject := mustInstanceOf(t, r, plain.Value, objectCtor)
	piIsP := mustInstanceOf(t, r, pi, pCtor)
	numberIsNumberCtor := mustInstanceOf(t, r, five, numberCtor)
	functionIsFunction := mustInstanceOf(t, r, functionValue, functionCtor)
	arrowIsFunction := mustInstanceOf(t, r, arrow, functionCtor)
	nullIsObject := mustInstanceOf(t, r, nullV, objectCtor)

	tc := r.tc(t)
	rhs, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	_, rhsErr := plain.InstanceOf(r.scope, r.ctx, rhs, tc)
	rhsCaught := mustHasCaught(t, tc)
	rhsMessage := caughtMessage(t, r, tc)
	closeTryCatch(t, tc)

	got := jobj(
		kv("plain_is_object", jbool(plainIsObject)),
		kv("proto_linked_is_p", jbool(piIsP)),
		kv("number_is_number_ctor", jbool(numberIsNumberCtor)),
		kv("function_is_function", jbool(functionIsFunction)),
		kv("arrow_is_function", jbool(arrowIsFunction)),
		kv("null_is_object", jbool(nullIsObject)),
		kv("rhs_non_callable_is_none", jbool(rhsErr != nil)),
		kv("rhs_caught", jbool(rhsCaught)),
		kv("rhs_message", jstr(rhsMessage)),
	)
	want := jobj(
		kv("plain_is_object", jbool(true)),
		kv("proto_linked_is_p", jbool(true)),
		kv("number_is_number_ctor", jbool(false)),
		kv("function_is_function", jbool(true)),
		kv("arrow_is_function", jbool(true)),
		kv("null_is_object", jbool(false)),
		kv("rhs_non_callable_is_none", jbool(true)),
		kv("rhs_caught", jbool(true)),
		kv("rhs_message", jstr("Uncaught TypeError: Right-hand side of 'instanceof' is not callable")),
	)
	return wantGot("obj-ops/instanceof/api_instance_of", want, got)
}

// --- 10. equality ---------------------------------------------------------------------------

// checkEqualitySameValueZero mirrors obj-ops/equality/same_value_zero.
func checkEqualitySameValueZero(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	nan := scopeNumber(t, r.scope, math.NaN())
	nan2 := scopeNumber(t, r.scope, math.NaN())
	plusZero := scopeNumber(t, r.scope, 0.0)
	minusZero := scopeNumber(t, r.scope, math.Copysign(0, -1))
	stringA := scopeString(t, r.scope, "ab")
	stringACopy := scopeString(t, r.scope, "ab")
	intSeven := int32Val(t, r.scope, 7)
	floatSeven := scopeNumber(t, r.scope, 7.0)
	bigintOne := scopeBigIntI64(t, r.scope, 1)
	numberOne := int32Val(t, r.scope, 1)
	obj := mustNewObjectValue(t, r)
	objOther := mustNewObjectValue(t, r)
	undefinedV := scopeUndefined(t, r.scope)
	nullV := scopeNull(t, r.scope)
	trueV := scopeBoolean(t, r.scope, true)

	type triple struct {
		same, zero, strict bool
	}
	read := func(a, b gov8.Value) triple {
		same := mustSameValue(t, a, b)
		zero := mustSameValueZero(t, r, a, b)
		strict := mustStrictEquals(t, a, b)
		return triple{same, zero, strict}
	}

	cases := []struct {
		name string
		t    triple
		want triple
	}{
		{"nan_nan", read(nan, nan2), triple{true, true, false}},
		{"plus_minus_zero", read(plusZero, minusZero), triple{false, true, true}},
		{"string_copies", read(stringA, stringACopy), triple{true, true, true}},
		{"int_vs_float_seven", read(intSeven, floatSeven), triple{true, true, true}},
		{"bigint_vs_number_one", read(bigintOne, numberOne), triple{false, false, false}},
		{"same_object", read(obj, obj), triple{true, true, true}},
		{"distinct_objects", read(obj, objOther), triple{false, false, false}},
		{"undefined_vs_null", read(undefinedV, nullV), triple{false, false, false}},
		{"true_vs_one", read(trueV, numberOne), triple{false, false, false}},
	}

	gotPairs := make(jsonObj, 0, len(cases))
	wantPairs := make(jsonObj, 0, len(cases))
	for _, c := range cases {
		gotPairs = append(gotPairs, kv(c.name, jobj(
			kv("same_value", jbool(c.t.same)),
			kv("same_value_zero", jbool(c.t.zero)),
			kv("strict_equals", jbool(c.t.strict)),
		)))
		wantPairs = append(wantPairs, kv(c.name, jobj(
			kv("same_value", jbool(c.want.same)),
			kv("same_value_zero", jbool(c.want.zero)),
			kv("strict_equals", jbool(c.want.strict)),
		)))
	}
	return wantGot("obj-ops/equality/same_value_zero", jobj(wantPairs...), jobj(gotPairs...))
}

// checkValueHashSemantics mirrors obj-ops/equality/value_hash.
func checkValueHashSemantics(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	obj, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	identity, err := obj.GetIdentityHash()
	if err != nil {
		t.Fatalf("GetIdentityHash: %v", err)
	}
	objectValueHash, err := obj.Value.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}
	objectValueHashAgain, err := obj.Value.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}

	smi42 := int32Val(t, r.scope, 42)
	smi41 := int32Val(t, r.scope, 41)
	smiHash, err := smi42.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}
	smiHashAgain, err := smi42.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}
	smi41Hash, err := smi41.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}

	undefinedHashFirst := mustHash(t, scopeUndefined(t, r.scope))
	undefinedHashAgain := mustHash(t, scopeUndefined(t, r.scope))
	stringHashFirst := mustHash(t, scopeString(t, r.scope, "seeded"))
	stringHashAgain := mustHash(t, scopeString(t, r.scope, "seeded"))
	bigintHashFirst := mustHash(t, scopeBigIntI64(t, r.scope, 5))
	bigintHashAgain := mustHash(t, scopeBigIntI64(t, r.scope, 5))

	got := jobj(
		kv("identity_nonzero", jbool(identity != 0)),
		kv("object_value_hash_matches_identity", jbool(int32(objectValueHash) == identity)),
		kv("object_value_hash_stable", jbool(objectValueHash == objectValueHashAgain)),
		kv("smi_hash_stable", jbool(smiHash == smiHashAgain)),
		kv("smi_41_differs", jbool(smi41Hash != smiHash)),
		kv("undefined_hash_stable", jbool(undefinedHashFirst == undefinedHashAgain)),
		kv("string_hash_stable", jbool(stringHashFirst == stringHashAgain)),
		kv("bigint_hash_stable", jbool(bigintHashFirst == bigintHashAgain)),
	)
	want := jobj(
		kv("identity_nonzero", jbool(true)),
		kv("object_value_hash_matches_identity", jbool(true)),
		kv("object_value_hash_stable", jbool(true)),
		// Integer hashes are NOT the raw value in this build; they are only
		// stable within the isolate and differ between distinct integers.
		kv("smi_hash_stable", jbool(true)),
		kv("smi_41_differs", jbool(true)),
		kv("undefined_hash_stable", jbool(true)),
		kv("string_hash_stable", jbool(true)),
		kv("bigint_hash_stable", jbool(true)),
	)
	return wantGot("obj-ops/equality/value_hash", want, got)
}

// --- 11. typeof -----------------------------------------------------------------------------

// checkTypeRepresentation mirrors obj-ops/typeof/type_representation.
func checkTypeRepresentation(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	type sample struct {
		name string
		v    gov8.Value
	}
	samples := []sample{
		{"undefined", scopeUndefined(t, r.scope)},
		{"null", scopeNull(t, r.scope)},
		{"boolean", scopeBoolean(t, r.scope, true)},
		{"integer", int32Val(t, r.scope, 7)},
		{"float", scopeNumber(t, r.scope, 2.5)},
		{"string", scopeString(t, r.scope, "s")},
		{"symbol", mustSymbolValue(t, r)},
		{"bigint", scopeBigIntI64(t, r.scope, 1)},
		{"function", r.mustEval(t, "(function f() {})")},
		{"plain_object", mustNewObjectValue(t, r)},
		{"array", r.mustEval(t, "[]")},
		{"error", r.mustEval(t, "new Error('e')")},
		{"proxy_of_function", r.mustEval(t, "new Proxy(function () {}, {})")},
	}
	expectations := map[string]string{
		"undefined": "undefined",
		"null":      "object", // V8 reports null as "object" (like JS typeof)
		"boolean":   "boolean",
		"integer":   "number",
		"float":     "number",
		"string":    "string",
		"symbol":    "symbol",
		"bigint":    "bigint",
		"function":  "function",
		// V8's Value::TypeOf reflects callability: a proxy of a function
		// reports "function".
		"proxy_of_function": "function",
	}
	gotPairs := make(jsonObj, 0, len(samples))
	wantPairs := make(jsonObj, 0, len(samples))
	for _, s := range samples {
		tv, err := s.v.TypeOf(r.scope)
		if err != nil {
			t.Fatalf("TypeOf(%s): %v", s.name, err)
		}
		text := valueText(t, r, tv)
		gotPairs = append(gotPairs, kv(s.name, jstr(text)))
		wantText, ok := expectations[s.name]
		if !ok {
			wantText = "object"
		}
		wantPairs = append(wantPairs, kv(s.name, jstr(wantText)))
	}
	return wantGot("obj-ops/typeof/type_representation", jobj(wantPairs...), jobj(gotPairs...))
}

// --- 12. predicates ---------------------------------------------------------------------------

// checkPredicatesMissingInventory mirrors obj-ops/predicates/missing_inventory.
func checkPredicatesMissingInventory(t tester) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	if _, ok := r.eval(t, nil, `globalThis.argsObj = (function () { return arguments; })(1, 2);
         globalThis.symbolObj = Object(Symbol('s'));
         globalThis.nativeError = new Error('e');
         globalThis.asyncFn = async function () {};
         globalThis.genFn = function* () {};
         globalThis.genObj = (function* () { yield 1; })();
         globalThis.promiseVal = Promise.resolve(1);
         globalThis.mapIter = new Map().keys();
         globalThis.setIter = new Set().values();
         globalThis.weakMap = new WeakMap();
         globalThis.weakSet = new WeakSet();
         globalThis.u8 = new Uint8Array(2);
         globalThis.u8c = new Uint8ClampedArray(2);
         globalThis.i8 = new Int8Array(2);
         globalThis.u16 = new Uint16Array(2);
         globalThis.i16 = new Int16Array(2);
         globalThis.u32 = new Uint32Array(2);
         globalThis.i32 = new Int32Array(2);
         globalThis.f32 = new Float32Array(2);
         globalThis.f64 = new Float64Array(2);
         globalThis.bi64 = new BigInt64Array(2);
         globalThis.bu64 = new BigUint64Array(2);`); !ok {
		t.Fatal("setup eval failed")
	}

	float16Constructs := r.evalText(t, "typeof Float16Array") == "function"
	if float16Constructs {
		if _, ok := r.eval(t, nil, "globalThis.f16 = new Float16Array(2);"); !ok {
			t.Fatal("Float16Array construction failed")
		}
	}

	globalOrUndefined := func(name string) gov8.Value {
		v, ok := r.globalValue(t, name)
		if !ok {
			return scopeUndefined(t, r.scope)
		}
		return v
	}

	external, err := r.scope.NewExternal(0)
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}

	u8 := globalOrUndefined("u8")
	u8IsNotI8 := !mustPred(t, u8.IsInt8Array)

	got := jobj(
		kv("is_false", jbool(mustPred(t, scopeBoolean(t, r.scope, false).IsFalse))),
		kv("is_external", jbool(mustPred(t, external.IsExternal))),
		kv("is_arguments_object", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("argsObj").IsArgumentsObject() }))),
		kv("is_symbol_object", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("symbolObj").IsSymbolObject() }))),
		kv("is_native_error", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("nativeError").IsNativeError() }))),
		kv("is_async_function", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("asyncFn").IsAsyncFunction() }))),
		kv("is_generator_function", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("genFn").IsGeneratorFunction() }))),
		kv("is_promise", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("promiseVal").IsPromise() }))),
		kv("is_map_iterator", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("mapIter").IsMapIterator() }))),
		kv("is_set_iterator", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("setIter").IsSetIterator() }))),
		kv("is_generator_object", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("genObj").IsGeneratorObject() }))),
		// Script-level WeakMap/WeakSet objects report true: they are
		// JSWeakMap/JSWeakSet instances in this V8.
		kv("is_weak_map", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("weakMap").IsWeakMap() }))),
		kv("is_weak_set", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("weakSet").IsWeakSet() }))),
		kv("is_uint8_array", jbool(mustPred(t, u8.IsUint8Array))),
		kv("is_uint8_clamped_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("u8c").IsUint8ClampedArray() }))),
		kv("is_int8_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("i8").IsInt8Array() }))),
		kv("is_uint16_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("u16").IsUint16Array() }))),
		kv("is_int16_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("i16").IsInt16Array() }))),
		kv("is_uint32_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("u32").IsUint32Array() }))),
		kv("is_int32_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("i32").IsInt32Array() }))),
		kv("float16_constructs", jbool(float16Constructs)),
		kv("is_float16_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("f16").IsFloat16Array() }))),
		kv("is_float32_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("f32").IsFloat32Array() }))),
		kv("is_float64_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("f64").IsFloat64Array() }))),
		kv("is_big_int64_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("bi64").IsBigInt64Array() }))),
		kv("is_big_uint64_array", jbool(mustPred(t, func() (bool, error) { return globalOrUndefined("bu64").IsBigUint64Array() }))),
		// Cross-controls: each typed-array predicate answers false on a
		// plain Uint8Array except its own.
		kv("u8_is_not_i8", jbool(u8IsNotI8)),
		kv("u8_is_typed_array", jbool(mustPred(t, u8.IsTypedArray))),
	)
	want := jobj(
		kv("is_false", jbool(true)),
		kv("is_external", jbool(true)),
		kv("is_arguments_object", jbool(true)),
		kv("is_symbol_object", jbool(true)),
		kv("is_native_error", jbool(true)),
		kv("is_async_function", jbool(true)),
		kv("is_generator_function", jbool(true)),
		kv("is_promise", jbool(true)),
		kv("is_map_iterator", jbool(true)),
		kv("is_set_iterator", jbool(true)),
		kv("is_generator_object", jbool(true)),
		kv("is_weak_map", jbool(true)),
		kv("is_weak_set", jbool(true)),
		kv("is_uint8_array", jbool(true)),
		kv("is_uint8_clamped_array", jbool(true)),
		kv("is_int8_array", jbool(true)),
		kv("is_uint16_array", jbool(true)),
		kv("is_int16_array", jbool(true)),
		kv("is_uint32_array", jbool(true)),
		kv("is_int32_array", jbool(true)),
		kv("float16_constructs", jbool(true)),
		kv("is_float16_array", jbool(true)),
		kv("is_float32_array", jbool(true)),
		kv("is_float64_array", jbool(true)),
		kv("is_big_int64_array", jbool(true)),
		kv("is_big_uint64_array", jbool(true)),
		kv("u8_is_not_i8", jbool(true)),
		kv("u8_is_typed_array", jbool(true)),
	)
	return wantGot("obj-ops/predicates/missing_inventory", want, got)
}

// --- registry (order is the observable contract) -------------------------------------------------

type checkFn func(t tester) obs

var checks = []checkFn{
	checkProtoGetAndSet,
	checkHasDeleteFamily,
	checkRealNamedInterceptorBypass,
	checkIdentityHash,
	checkCreationContext,
	checkConstructorName,
	checkGetSetWithReceiver,
	checkLazyDataProperty,
	checkInstanceAccessor,
	checkCallPlainObjectNotCallable,
	checkCallFunctionCallAndConstruct,
	checkCallableConstructorPredicates,
	checkToObjectMatrix,
	checkToBooleanMatrix,
	checkToIntegerMatrix,
	checkToBigIntMatrix,
	checkToDetailStringMatrix,
	checkAPIInstanceOf,
	checkEqualitySameValueZero,
	checkValueHashSemantics,
	checkTypeRepresentation,
	checkPredicatesMissingInventory,
}
