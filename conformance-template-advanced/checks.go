//go:build windows && amd64

package main

// The 14 template-advanced checks of the pinned Rust oracle binary
// (rust-oracle/src/bin/conformance-template-advanced.rs), re-implemented on
// the Go binding in the binary's fixed order. Every value below is produced
// by live engine observation; the expectation is never hardcoded into the
// check bodies — the comparison target is the pinned fixture.
//
// The oracle's thread-local LOG/EXPECTED_HASH channels map to a per-check
// advLogger value captured by the callback closures (check execution is
// single-threaded, so this is deterministic the same way).

import (
	"strconv"
	"strings"
	"testing"

	gov8 "gov8"
)

// advLogger is the per-check callback log plus the object-identity hash the
// callbacks compare holder()/this() against (0 = no comparison configured).
type advLogger struct {
	log  []string
	hash uint32
}

func (l *advLogger) push(s string)        { l.log = append(l.log, s) }
func (l *advLogger) join() string         { return strings.Join(l.log, ";") }
func (l *advLogger) setHash(v gov8.Value) { h, _ := v.GetHash(); l.hash = h }
func (l *advLogger) setHashObj(o *gov8.Object) {
	if o == nil {
		return
	}
	h, _ := o.GetHash()
	l.hash = h
}

func (l *advLogger) matchesHash(h uint32, err error) bool {
	return err == nil && l.hash != 0 && h == l.hash
}

func (l *advLogger) matches(v gov8.Value) bool {
	h, err := v.GetHash()
	return l.matchesHash(h, err)
}

// advText mirrors the oracle's value_text for callback-scope values: the
// ToString ("" on failure).
func advText(cs *gov8.CallbackScope, v gov8.Value) string {
	txt, err := cs.ToString(v)
	if err != nil {
		return ""
	}
	return txt
}

// advIntOr is integer_value(scope).unwrap_or(fallback).
func advIntOr(cs *gov8.CallbackScope, v gov8.Value, fallback int64) int64 {
	n, ok, err := cs.IntegerValue(v)
	if err != nil || !ok {
		return fallback
	}
	return n
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// --- 1. named interceptor: getter + setter ---------------------------------------

func advNamedGetter(l *advLogger) gov8.NamedPropertyGetterCallback {
	return func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		name := advText(cs, key)
		if name == "in_a" || name == "in_b" {
			mark := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(name), "IN_"))
			s, err := cs.NewString(mark)
			if err != nil {
				return gov8.InterceptedNo
			}
			_ = rv.Set(s)
			holder, _ := args.Holder()
			var holderOK bool
			if holder != nil {
				holderOK = l.matches(holder.Value)
			}
			data, _ := args.Data()
			l.push("get:" + name + ":yes:holder=" + b2s(holderOK) + ":" +
				b2s(args.ShouldThrowOnError()) + ":data=" + advText(cs, data))
			return gov8.InterceptedYes
		}
		l.push("get:" + name + ":no")
		return gov8.InterceptedNo
	}
}

func advNamedSetter(l *advLogger) gov8.NamedPropertySetterCallback {
	return func(cs *gov8.CallbackScope, key, value gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		name := advText(cs, key)
		if strings.HasPrefix(name, "in_") {
			_ = rv.SetBool(true)
			l.push("set:" + name + ":" + itoa64(advIntOr(cs, value, -1)) +
				":strict=" + b2s(args.ShouldThrowOnError()))
			return gov8.InterceptedYes
		}
		return gov8.InterceptedNo
	}
}

func checkNamedInterceptorGetSet(t *testing.T, r *runtime) string {
	t.Helper()
	l := &advLogger{}
	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	real, _ := r.scope.NewString("R")
	if err := ot.Set("real", real); err != nil {
		t.Fatalf("Set real: %v", err)
	}
	data77, _ := r.scope.Int32(77)
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: advNamedGetter(l), Setter: advNamedSetter(l), Data: data77,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler: %v", err)
	}
	obj, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	l.setHash(obj.Value)
	r.seedGlobal(t, "o", obj.Value)

	real_ := r.evalText(t, "o.real")
	intercepted := r.evalText(t, "o.in_a")
	missing := r.evalText(t, "o.missing")
	assignmentValue := r.evalText(t, "(o.in_a = 11)")
	stillIntercepted := r.evalText(t, "o.in_a")
	ownInA := r.evalText(t, "Object.prototype.hasOwnProperty.call(o, 'in_a')")
	fallbackAssignment := r.evalText(t, "(o.plain_new = 42)")
	fallbackRead := r.evalText(t, "o.plain_new")
	ownFallback := r.evalText(t, "Object.prototype.hasOwnProperty.call(o, 'plain_new')")
	strictAssignment := r.evalCaught(t, "(() => { 'use strict'; o.in_b = 12; })()")
	ownNeverSet := r.evalText(t, "Object.prototype.hasOwnProperty.call(o, 'never_set')")
	callbackLog := l.join()

	return jsonString(jobj(
		kv("real", jstr(real_)),
		kv("intercepted", jstr(intercepted)),
		kv("missing", jstr(missing)),
		kv("assignment_value", jstr(assignmentValue)),
		kv("still_intercepted", jstr(stillIntercepted)),
		kv("own_in_a", jstr(ownInA)),
		kv("fallback_assignment", jstr(fallbackAssignment)),
		kv("fallback_read", jstr(fallbackRead)),
		kv("own_fallback", jstr(ownFallback)),
		kv("strict_assignment", jstr(strictAssignment)),
		kv("own_never_set", jstr(ownNeverSet)),
		kv("callback_log", jstr(callbackLog)),
	))
}

// --- 2. named interceptor: query / deleter / enumerator / definer / descriptor ---

func advNamedQuery(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
	name := advText(cs, key)
	if name == "q" {
		// READ_ONLY | DONT_ENUM == 3.
		_ = rv.SetInt32(int32(gov8.AttrReadOnly | gov8.AttrDontEnum))
		return gov8.InterceptedYes
	}
	return gov8.InterceptedNo
}

func advNamedDeleter(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
	name := advText(cs, key)
	if name == "del" {
		_ = rv.SetBool(false)
		return gov8.InterceptedYes
	}
	return gov8.InterceptedNo
}

func advNamedEnumerator(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
	// Non-Name elements are accepted on the NAMED path: V8 ToName-converts
	// each element (the Integer becomes "1").
	one, _ := cs.Scope().Int32(1)
	a, _ := cs.NewString("a")
	c, _ := cs.NewString("c")
	b, _ := cs.NewString("b")
	arr, err := cs.NewArrayWithElements([]gov8.Value{one, a, c, b})
	if err != nil {
		return
	}
	_ = rv.Set(arr)
}

func advNamedDefiner(l *advLogger) gov8.NamedPropertyDefinerCallback {
	return func(cs *gov8.CallbackScope, key gov8.Value, desc gov8.CallbackPropertyDescriptor, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		name := advText(cs, key)
		if name == "def" {
			valueText := ""
			if desc.HasValue() {
				v, ok, err := desc.Value()
				if err == nil && ok {
					valueText = advText(cs, v)
				}
			}
			l.push("define:" + name +
				":has_value=" + b2s(desc.HasValue()) +
				" value=" + valueText +
				" has_writable=" + b2s(desc.HasWritable()) +
				" writable=" + b2s(desc.Writable()) +
				" has_enum=" + b2s(desc.HasEnumerable()) +
				" enum=" + b2s(desc.Enumerable()) +
				" has_conf=" + b2s(desc.HasConfigurable()) +
				" conf=" + b2s(desc.Configurable()))
			_ = rv.SetBool(true)
			return gov8.InterceptedYes
		}
		return gov8.InterceptedNo
	}
}

func advNamedDescriptor(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
	name := advText(cs, key)
	if name == "desc" {
		obj, err := cs.NewObject()
		if err != nil {
			return gov8.InterceptedNo
		}
		dv, _ := cs.NewString("d-v")
		fb, _ := cs.Scope().Boolean(false)
		tb, _ := cs.Scope().Boolean(true)
		if _, err := cs.ObjectSet(obj, "value", dv); err != nil {
			return gov8.InterceptedNo
		}
		if _, err := cs.ObjectSet(obj, "writable", fb); err != nil {
			return gov8.InterceptedNo
		}
		if _, err := cs.ObjectSet(obj, "enumerable", tb); err != nil {
			return gov8.InterceptedNo
		}
		if _, err := cs.ObjectSet(obj, "configurable", tb); err != nil {
			return gov8.InterceptedNo
		}
		_ = rv.Set(obj)
		return gov8.InterceptedYes
	}
	if name == "descnum" {
		// A plain Number is legal to set; V8 converts it into a value-only
		// descriptor.
		seven, _ := cs.Scope().Int32(7)
		_ = rv.Set(seven)
		return gov8.InterceptedYes
	}
	return gov8.InterceptedNo
}

func checkNamedInterceptorQueryDeleteEnumDefine(t *testing.T, r *runtime) string {
	t.Helper()
	l := &advLogger{}

	// Query-only template: hasOwnProperty / propertyIsEnumerable consult it.
	qt, _ := r.iso.NewObjectTemplate(r.scope)
	if err := qt.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{Query: advNamedQuery}); err != nil {
		t.Fatalf("SetNamedPropertyHandler query: %v", err)
	}
	qObj, ok, _ := qt.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("query instance")
	}
	r.seedGlobal(t, "q_o", qObj.Value)
	hasIntercepted := r.evalText(t, "Object.prototype.hasOwnProperty.call(q_o, 'q')")
	enumerableIntercepted := r.evalText(t, "Object.prototype.propertyIsEnumerable.call(q_o, 'q')")
	hasMissing := r.evalText(t, "Object.prototype.hasOwnProperty.call(q_o, 'noq')")
	valueWithoutGetter := r.evalText(t, "q_o.q")

	// Deleter-only template.
	dt, _ := r.iso.NewObjectTemplate(r.scope)
	if err := dt.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{Deleter: advNamedDeleter}); err != nil {
		t.Fatalf("SetNamedPropertyHandler deleter: %v", err)
	}
	dObj, ok, _ := dt.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("deleter instance")
	}
	r.seedGlobal(t, "d_o", dObj.Value)
	deleteIntercepted := r.evalText(t, "(delete d_o.del)")
	deleteFallback := r.evalText(t, "(delete d_o.other)")

	// Enumerator-only template: no real properties, keys come from the
	// returned Array in its order.
	et, _ := r.iso.NewObjectTemplate(r.scope)
	if err := et.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{Enumerator: advNamedEnumerator}); err != nil {
		t.Fatalf("SetNamedPropertyHandler enumerator: %v", err)
	}
	eObj, ok, _ := et.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("enumerator instance")
	}
	r.seedGlobal(t, "e_o", eObj.Value)
	keys := r.evalText(t, "Object.keys(e_o).join(',')")
	ownNames := r.evalText(t, "Object.getOwnPropertyNames(e_o).join(',')")

	// Definer-only template.
	ft, _ := r.iso.NewObjectTemplate(r.scope)
	if err := ft.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{Definer: advNamedDefiner(l)}); err != nil {
		t.Fatalf("SetNamedPropertyHandler definer: %v", err)
	}
	defObj, ok, _ := ft.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("definer instance")
	}
	r.seedGlobal(t, "def_o", defObj.Value)
	defineIntercepted := r.evalText(t, "Object.defineProperty(def_o, 'def', {value: 42}) === def_o")
	defineFallback := r.evalText(t, "Object.defineProperty(def_o, 'other', {value: 1}) === def_o")
	fallbackStored := r.evalText(t, "def_o.other")
	interceptedNotStored := r.evalText(t, "def_o.def")
	definerLog := l.join()

	// Descriptor-only template.
	st, _ := r.iso.NewObjectTemplate(r.scope)
	if err := st.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{Descriptor: advNamedDescriptor}); err != nil {
		t.Fatalf("SetNamedPropertyHandler descriptor: %v", err)
	}
	descObj, ok, _ := st.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("descriptor instance")
	}
	r.seedGlobal(t, "desc_o", descObj.Value)
	descriptor := r.evalText(t, "JSON.stringify(Object.getOwnPropertyDescriptor(desc_o, 'desc'))")
	descriptorMissing := r.evalText(t, "Object.getOwnPropertyDescriptor(desc_o, 'nope')")
	descriptorType := r.evalText(t, "typeof Object.getOwnPropertyDescriptor(desc_o, 'desc')")
	descriptorNumber := r.evalCaught(t, "JSON.stringify(Object.getOwnPropertyDescriptor(desc_o, 'descnum'))")

	return jsonString(jobj(
		kv("has_intercepted", jstr(hasIntercepted)),
		kv("enumerable_intercepted", jstr(enumerableIntercepted)),
		kv("has_missing", jstr(hasMissing)),
		kv("value_without_getter", jstr(valueWithoutGetter)),
		kv("delete_intercepted", jstr(deleteIntercepted)),
		kv("delete_fallback", jstr(deleteFallback)),
		kv("keys", jstr(keys)),
		kv("own_names", jstr(ownNames)),
		kv("define_intercepted", jstr(defineIntercepted)),
		kv("define_fallback", jstr(defineFallback)),
		kv("fallback_stored", jstr(fallbackStored)),
		kv("intercepted_not_stored", jstr(interceptedNotStored)),
		kv("definer_log", jstr(definerLog)),
		kv("descriptor", jstr(descriptor)),
		kv("descriptor_missing", jstr(descriptorMissing)),
		kv("descriptor_type", jstr(descriptorType)),
		kv("descriptor_number", jstr(descriptorNumber)),
	))
}

// --- 3. indexed interceptor: full family -------------------------------------------

func checkIndexedInterceptorFullFamily(t *testing.T, r *runtime) string {
	t.Helper()
	l := &advLogger{}
	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetIndexedPropertyHandler(gov8.IndexedPropertyHandlerConfig{
		Getter: func(cs *gov8.CallbackScope, index uint32, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			if index == 42 {
				_ = rv.SetInt32(4242)
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
		Setter: func(cs *gov8.CallbackScope, index uint32, value gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			if index == 7 {
				_ = rv.SetBool(true)
				l.push("set:7:" + advText(cs, value))
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
		Query: func(cs *gov8.CallbackScope, index uint32, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			if index == 9 {
				// DONT_DELETE == 4.
				_ = rv.SetInt32(int32(gov8.AttrDontDelete))
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
		Deleter: func(cs *gov8.CallbackScope, index uint32, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			if index == 5 {
				_ = rv.SetBool(false)
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
		Enumerator: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			// Deliberately out of ascending index order to expose whether
			// V8 normalizes interceptor-provided element keys. Indexed
			// enumerator elements must be uint32-convertible values.
			n9, _ := cs.Scope().Int32(9)
			n4, _ := cs.Scope().Int32(4)
			n0, _ := cs.Scope().Int32(0)
			arr, err := cs.NewArrayWithElements([]gov8.Value{n9, n4, n0})
			if err != nil {
				return
			}
			_ = rv.Set(arr)
		},
	}); err != nil {
		t.Fatalf("SetIndexedPropertyHandler: %v", err)
	}
	obj, ok, _ := ot.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("indexed instance")
	}
	r.seedGlobal(t, "io", obj.Value)

	getIntercepted := r.evalText(t, "io[42]")
	getMissing := r.evalText(t, "io[43]")
	getNumericString := r.evalText(t, "io['42']")
	getNonIndexString := r.evalText(t, "io['43x']")
	setterIntercepted := r.evalText(t, "(io[7] = 'x')")
	setterNotStored := r.evalText(t, "io[7]")
	setterLog := l.join()
	fallbackAssignment := r.evalText(t, "(io[8] = 8)")
	fallbackRead := r.evalText(t, "io[8]")
	deleteIntercepted := r.evalText(t, "(delete io[5])")
	deleteFallback := r.evalText(t, "(delete io[6])")
	hasIntercepted := r.evalText(t, "Object.prototype.hasOwnProperty.call(io, 9)")
	hasMissing := r.evalText(t, "Object.prototype.hasOwnProperty.call(io, 10)")
	valueWithoutGetter := r.evalText(t, "io[9]")
	keys := r.evalText(t, "Object.keys(io).join(',')")

	return jsonString(jobj(
		kv("get_intercepted", jstr(getIntercepted)),
		kv("get_missing", jstr(getMissing)),
		kv("get_numeric_string", jstr(getNumericString)),
		kv("get_non_index_string", jstr(getNonIndexString)),
		kv("setter_intercepted", jstr(setterIntercepted)),
		kv("setter_not_stored", jstr(setterNotStored)),
		kv("setter_log", jstr(setterLog)),
		kv("fallback_assignment", jstr(fallbackAssignment)),
		kv("fallback_read", jstr(fallbackRead)),
		kv("delete_intercepted", jstr(deleteIntercepted)),
		kv("delete_fallback", jstr(deleteFallback)),
		kv("has_intercepted", jstr(hasIntercepted)),
		kv("has_missing", jstr(hasMissing)),
		kv("value_without_getter", jstr(valueWithoutGetter)),
		kv("keys", jstr(keys)),
	))
}

// --- 4. property handler flags --------------------------------------------------------

func advFlagGetter(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
	isSym, _ := key.IsSymbol()
	if isSym {
		s, _ := cs.NewString("SYM")
		_ = rv.Set(s)
		return gov8.InterceptedYes
	}
	if advText(cs, key) == "str" {
		s, _ := cs.NewString("S")
		_ = rv.Set(s)
		return gov8.InterceptedYes
	}
	return gov8.InterceptedNo
}

func advMaskingGetter(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
	s, _ := cs.NewString("G")
	_ = rv.Set(s)
	return gov8.InterceptedYes
}

func checkFlagInterceptors(t *testing.T, r *runtime) string {
	t.Helper()

	// ONLY_INTERCEPT_STRINGS: symbol-keyed lookups bypass the handler.
	ot, _ := r.iso.NewObjectTemplate(r.scope)
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: advFlagGetter,
		Flags:  gov8.HandlerFlagOnlyInterceptStrings,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler strings-only: %v", err)
	}
	stringsOnly, ok, _ := ot.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("strings-only instance")
	}
	r.seedGlobal(t, "strings_only", stringsOnly.Value)
	symDesc, _ := r.scope.NewString("s")
	symbol, err := r.scope.NewSymbol(symDesc)
	if err != nil {
		t.Fatalf("NewSymbol: %v", err)
	}
	r.seedGlobal(t, "sym", symbol.Value)

	// Default flags: the handler sees symbol keys too.
	ot2, _ := r.iso.NewObjectTemplate(r.scope)
	if err := ot2.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: advFlagGetter,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler default: %v", err)
	}
	allKeys, ok, _ := ot2.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("all-keys instance")
	}
	r.seedGlobal(t, "all_keys", allKeys.Value)

	symbolWithFlag := r.evalText(t, "strings_only[sym]")
	stringWithFlag := r.evalText(t, "strings_only.str")
	symbolWithoutFlag := r.evalText(t, "all_keys[sym]")
	stringWithoutFlag := r.evalText(t, "all_keys.str")

	// NON_MASKING: an existing own data property wins over the getter;
	// absent properties are still intercepted.
	masked, _ := r.iso.NewObjectTemplate(r.scope)
	one, _ := r.scope.Int32(1)
	if err := masked.Set("dup", one); err != nil {
		t.Fatalf("masked.Set: %v", err)
	}
	if err := masked.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: advMaskingGetter,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler masked: %v", err)
	}
	maskedObj, ok, _ := masked.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("masked instance")
	}
	r.seedGlobal(t, "masked", maskedObj.Value)

	nonMasking, _ := r.iso.NewObjectTemplate(r.scope)
	if err := nonMasking.Set("dup", one); err != nil {
		t.Fatalf("nonMasking.Set: %v", err)
	}
	if err := nonMasking.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: advMaskingGetter,
		Flags:  gov8.HandlerFlagNonMasking,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler non-masking: %v", err)
	}
	unmaskedObj, ok, _ := nonMasking.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("non-masking instance")
	}
	r.seedGlobal(t, "unmasked", unmaskedObj.Value)

	maskingReal := r.evalText(t, "masked.dup")
	maskingAbsent := r.evalText(t, "masked.absent")
	nonMaskingReal := r.evalText(t, "unmasked.dup")
	nonMaskingAbsent := r.evalText(t, "unmasked.absent")

	// HAS_NO_SIDE_EFFECT: the handler still runs in normal execution; the
	// allowlisting itself is only observable under debug-evaluate.
	sfx, _ := r.iso.NewObjectTemplate(r.scope)
	if err := sfx.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: advMaskingGetter,
		Flags:  gov8.HandlerFlagHasNoSideEffect,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler no-side-effect: %v", err)
	}
	sfxObj, ok, _ := sfx.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("no-side-effect instance")
	}
	r.seedGlobal(t, "sfx_o", sfxObj.Value)
	noSideEffectNormalMode := r.evalText(t, "sfx_o.anything")

	return jsonString(jobj(
		kv("symbol_with_flag", jstr(symbolWithFlag)),
		kv("string_with_flag", jstr(stringWithFlag)),
		kv("symbol_without_flag", jstr(symbolWithoutFlag)),
		kv("string_without_flag", jstr(stringWithoutFlag)),
		kv("masking_real", jstr(maskingReal)),
		kv("masking_absent", jstr(maskingAbsent)),
		kv("non_masking_real", jstr(nonMaskingReal)),
		kv("non_masking_absent", jstr(nonMaskingAbsent)),
		kv("no_side_effect_normal_mode", jstr(noSideEffectNormalMode)),
	))
}

// --- 5. ReturnValue.Get and the setter variants ---------------------------------------

func advRvSpecials(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	modeV, _ := args.Get(0)
	mode := advIntOr(cs, modeV, -1)
	switch mode {
	case 0:
		_ = rv.SetUndefined()
	case 1:
		_ = rv.SetNull()
	case 2:
		_ = rv.SetEmptyString()
	case 3:
		_ = rv.SetBool(true)
	case 4:
		_ = rv.SetUint32(4294967295)
	case 5:
		_ = rv.SetFloat64(2.5)
	}
}

func checkReturnValueGetAndSpecials(t *testing.T, r *runtime) string {
	t.Helper()
	l := &advLogger{}

	fSpecials, err := r.iso.NewFunction(r.scope, r.ctx, advRvSpecials, nil)
	if err != nil {
		t.Fatalf("NewFunction rv_specials: %v", err)
	}
	r.seedGlobal(t, "rv_specials", fSpecials.Value)
	undefinedOut := r.evalText(t, "String(JSON.stringify(rv_specials(0)))")
	nullOut := r.evalText(t, "String(JSON.stringify(rv_specials(1)))")
	emptyStringOut := r.evalText(t, "String(JSON.stringify(rv_specials(2)))")
	boolOut := r.evalText(t, "String(JSON.stringify(rv_specials(3)))")
	uint32Out := r.evalText(t, "String(JSON.stringify(rv_specials(4)))")
	doubleOut := r.evalText(t, "String(JSON.stringify(rv_specials(5)))")
	unsetOut := r.evalText(t, "String(JSON.stringify(rv_specials(9)))")

	// Get() reads back the value that was set (undefined when nothing was
	// set).
	fGet, err := r.iso.NewFunction(r.scope, r.ctx,
		func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			before, err := rv.Get()
			if err != nil {
				return
			}
			bUndef, _ := before.IsUndefined()
			l.push("before_undefined=" + b2s(bUndef))
			_ = rv.SetInt32(7)
			after, err := rv.Get()
			if err != nil {
				return
			}
			aNum, _ := after.IsNumber()
			n, _ := after.NumberValueRaw()
			l.push("after_number=" + b2s(aNum) + " value=" + itoa64(int64(n)))
		}, nil)
	if err != nil {
		t.Fatalf("NewFunction rv_get: %v", err)
	}
	r.seedGlobal(t, "rv_get", fGet.Value)
	getProbeValue := r.evalText(t, "rv_get()")

	ot, _ := r.iso.NewObjectTemplate(r.scope)
	if err := ot.SetAccessorWithSetter("p",
		func(cs *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			before, err := rv.Get()
			if err != nil {
				return
			}
			bUndef, _ := before.IsUndefined()
			l.push("acc_before_undefined=" + b2s(bUndef))
			v, _ := cs.NewString("acc-v")
			_ = rv.Set(v)
			after, err := rv.Get()
			if err != nil {
				return
			}
			same, _ := after.StrictEquals(v)
			l.push("acc_after_same=" + b2s(same))
		}, nil); err != nil {
		t.Fatalf("SetAccessorWithSetter: %v", err)
	}
	accObj, ok, _ := ot.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("accessor instance")
	}
	r.seedGlobal(t, "acc_o", accObj.Value)
	accessorValue := r.evalText(t, "acc_o.p")

	ot2, _ := r.iso.NewObjectTemplate(r.scope)
	if err := ot2.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			before, err := rv.Get()
			if err != nil {
				return gov8.InterceptedNo
			}
			bUndef, _ := before.IsUndefined()
			l.push("int_before_undefined=" + b2s(bUndef))
			v, _ := cs.NewString("g")
			_ = rv.Set(v)
			after, err := rv.Get()
			if err != nil {
				return gov8.InterceptedYes
			}
			same, _ := after.StrictEquals(v)
			l.push("int_after_same=" + b2s(same))
			return gov8.InterceptedYes
		},
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler: %v", err)
	}
	intObj, ok, _ := ot2.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("interceptor instance")
	}
	r.seedGlobal(t, "int_o", intObj.Value)
	interceptorValue := r.evalText(t, "int_o.k")

	callbackLog := l.join()

	return jsonString(jobj(
		kv("undefined_out", jstr(undefinedOut)),
		kv("null_out", jstr(nullOut)),
		kv("empty_string_out", jstr(emptyStringOut)),
		kv("bool_out", jstr(boolOut)),
		kv("uint32_out", jstr(uint32Out)),
		kv("double_out", jstr(doubleOut)),
		kv("unset_out", jstr(unsetOut)),
		kv("get_probe_value", jstr(getProbeValue)),
		kv("accessor_value", jstr(accessorValue)),
		kv("interceptor_value", jstr(interceptorValue)),
		kv("callback_log", jstr(callbackLog)),
	))
}

// --- 6. signatures: receiver enforcement ---------------------------------------------

func advNoop(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {}

func checkSignatureReceiverEnforcement(t *testing.T, r *runtime) string {
	t.Helper()
	l := &advLogger{}

	method := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		this, _ := args.This()
		data, _ := args.Data()
		var thisOK bool
		if this != nil {
			h, err := this.GetHash()
			thisOK = l.matchesHash(h, err)
		}
		l.push("call:args=" + itoa64(int64(args.Length())) +
			" data=" + advText(cs, data) +
			" this_ok=" + b2s(thisOK))
		okV, _ := cs.NewString("ok")
		_ = rv.Set(okV)
	}

	baseFT, err := r.iso.NewFunctionTemplate(r.scope, advNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate base: %v", err)
	}
	if err := baseFT.SetClassName("Gov8SigBase"); err != nil {
		t.Fatalf("SetClassName base: %v", err)
	}
	signature, err := r.iso.NewSignature(r.scope, baseFT)
	if err != nil {
		t.Fatalf("NewSignature: %v", err)
	}
	data, _ := r.scope.NewString("sig-data")
	methodFT, err := r.iso.NewFunctionTemplate(r.scope, method, &gov8.FunctionOptions{
		Signature: signature,
		Length:    2,
		Data:      data,
	})
	if err != nil {
		t.Fatalf("NewFunctionTemplate method: %v", err)
	}
	proto, err := baseFT.PrototypeTemplate()
	if err != nil {
		t.Fatalf("PrototypeTemplate: %v", err)
	}
	if err := proto.SetData("m", methodFT); err != nil {
		t.Fatalf("SetData m: %v", err)
	}
	baseCtor, err := baseFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction base: %v", err)
	}
	r.seedGlobal(t, "Gov8SigBase", baseCtor.Value)

	// A derived template inherits from the base: its instances remain valid
	// receivers for the base signature.
	derivedFT, err := r.iso.NewFunctionTemplate(r.scope, advNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate derived: %v", err)
	}
	if err := derivedFT.SetClassName("Gov8SigDerived"); err != nil {
		t.Fatalf("SetClassName derived: %v", err)
	}
	if err := derivedFT.Inherit(baseFT); err != nil {
		t.Fatalf("Inherit: %v", err)
	}
	derivedCtor, err := derivedFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction derived: %v", err)
	}
	r.seedGlobal(t, "Gov8SigDerived", derivedCtor.Value)

	// Bind stable instances so receiver-identity checks are meaningful.
	_ = r.evalText(t, "var sd = new Gov8SigDerived(); var sb = new Gov8SigBase()")
	derivedInstance, ok := r.eval(t, "sd")
	if !ok {
		t.Fatalf("eval sd")
	}
	derivedHash, err := derivedInstance.GetHash()
	if err != nil {
		t.Fatalf("GetHash derived: %v", err)
	}
	baseInstance, ok := r.eval(t, "sb")
	if !ok {
		t.Fatalf("eval sb")
	}
	baseHash, err := baseInstance.GetHash()
	if err != nil {
		t.Fatalf("GetHash base: %v", err)
	}

	l.hash = derivedHash
	derivedCall := r.evalText(t, "sd.m(5)")
	fnLength := r.evalText(t, "sd.m.length")
	wrongReceiver := r.evalCaught(t, "sd.m.call({}, 5)")

	l.hash = baseHash
	baseCall := r.evalText(t, "sb.m(1)")
	wrongReceiverBase := r.evalCaught(t, "sb.m.call({}, 5)")

	hostInstance, err := derivedFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction host: %v", err)
	}
	hostObject, ok, err := hostInstance.NewInstance(r.scope)
	if err != nil || !ok {
		t.Fatalf("host NewInstance: ok=%v err=%v", ok, err)
	}
	l.setHash(hostObject.Value)
	methodV, ok, err := hostObject.GetByName(r.scope, r.ctx, "m")
	if err != nil || !ok {
		t.Fatalf("host get m: ok=%v err=%v", ok, err)
	}
	methodFn, ok, err := gov8.AsFunction(methodV, r.ctx)
	if err != nil || !ok {
		t.Fatalf("AsFunction: ok=%v err=%v", ok, err)
	}
	five, _ := r.scope.Int32(5)
	hostCall := ""
	if res, ok, err := methodFn.Call(r.scope, hostObject.Value, five); err == nil && ok {
		hostCall = r.valueText(res)
	}
	callbackLog := l.join()

	return jsonString(jobj(
		kv("derived_call", jstr(derivedCall)),
		kv("base_call", jstr(baseCall)),
		kv("fn_length", jstr(fnLength)),
		kv("wrong_receiver", jstr(wrongReceiver)),
		kv("wrong_receiver_base", jstr(wrongReceiverBase)),
		kv("host_call", jstr(hostCall)),
		kv("callback_log", jstr(callbackLog)),
	))
}

// --- 7. intrinsic data properties -----------------------------------------------------

func checkIntrinsicDataProperty(t *testing.T, r *runtime) string {
	t.Helper()

	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetIntrinsicDataProperty("arr", gov8.IntrinsicArrayPrototype, gov8.AttrNone); err != nil {
		t.Fatalf("SetIntrinsicDataProperty arr: %v", err)
	}
	if err := ot.SetIntrinsicDataProperty("ro", gov8.IntrinsicArrayPrototype, gov8.AttrReadOnly); err != nil {
		t.Fatalf("SetIntrinsicDataProperty ro: %v", err)
	}
	if err := ot.SetIntrinsicDataProperty("iter", gov8.IntrinsicIteratorPrototype, gov8.AttrNone); err != nil {
		t.Fatalf("SetIntrinsicDataProperty iter: %v", err)
	}
	obj, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	r.seedGlobal(t, "io", obj.Value)

	arrIsIntrinsic := r.evalText(t, "io.arr === Array.prototype")
	sameIntrinsicObject := r.evalText(t, "io.arr === io.ro")
	readOnlyAttr := r.evalText(t, "Object.getOwnPropertyDescriptor(io, 'ro').writable")
	plainAttr := r.evalText(t, "Object.getOwnPropertyDescriptor(io, 'arr').writable")
	iteratorIsIntrinsic := r.evalText(t, "io.iter[Symbol.iterator]() === io.iter")
	iteratorIdentity := r.evalText(t,
		"io.iter === Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]()))")

	// Intrinsics also work on an instance template: every `new C()` gets
	// the context's real Array.prototype.
	ft, err := r.iso.NewFunctionTemplate(r.scope, advNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	instTemplate, err := ft.InstanceTemplate()
	if err != nil {
		t.Fatalf("InstanceTemplate: %v", err)
	}
	if err := instTemplate.SetIntrinsicDataProperty("arr", gov8.IntrinsicArrayPrototype, gov8.AttrNone); err != nil {
		t.Fatalf("instance template intrinsic: %v", err)
	}
	ctor, err := ft.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	r.seedGlobal(t, "C", ctor.Value)
	instanceIntrinsic := r.evalText(t, "new C().arr === Array.prototype")

	return jsonString(jobj(
		kv("arr_is_intrinsic", jstr(arrIsIntrinsic)),
		kv("same_intrinsic_object", jstr(sameIntrinsicObject)),
		kv("read_only_attr", jstr(readOnlyAttr)),
		kv("plain_attr", jstr(plainAttr)),
		kv("iterator_is_intrinsic", jstr(iteratorIsIntrinsic)),
		kv("iterator_identity", jstr(iteratorIdentity)),
		kv("instance_intrinsic", jstr(instanceIntrinsic)),
	))
}

// --- 8. constructor behavior: Throw / Allow / read-only / removed prototype -----------

func advReturnInt(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	_ = rv.SetInt32(3)
}

func checkConstructorBehaviorAndPrototype(t *testing.T, r *runtime) string {
	t.Helper()

	// ConstructorBehavior::Throw: "concise" API function, no .prototype.
	conciseFT, err := r.iso.NewFunctionTemplate(r.scope, advReturnInt, &gov8.FunctionOptions{
		ConstructorBehavior: gov8.ConstructorBehaviorThrow,
	})
	if err != nil {
		t.Fatalf("NewFunctionTemplate concise: %v", err)
	}
	if err := conciseFT.SetClassName("Gov8Concise"); err != nil {
		t.Fatalf("SetClassName concise: %v", err)
	}
	conciseFn, err := conciseFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction concise: %v", err)
	}
	r.seedGlobal(t, "Concise", conciseFn.Value)
	concisePrototype := r.evalText(t, "typeof Concise.prototype")
	conciseCall := r.evalText(t, "Concise()")
	conciseName := r.evalText(t, "Concise.name")
	conciseNew := r.evalCaught(t, "new Concise()")

	// Default (Allow): full constructor with a writable prototype.
	plainFT, err := r.iso.NewFunctionTemplate(r.scope, advNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate plain: %v", err)
	}
	if err := plainFT.SetClassName("Gov8Plain"); err != nil {
		t.Fatalf("SetClassName plain: %v", err)
	}
	plainFn, err := plainFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction plain: %v", err)
	}
	r.seedGlobal(t, "Gov8Plain", plainFn.Value)
	plainPrototype := r.evalText(t, "typeof Gov8Plain.prototype")
	plainConstructorLink := r.evalText(t, "Gov8Plain.prototype.constructor === Gov8Plain")
	plainWritable := r.evalText(t,
		"Object.getOwnPropertyDescriptor(Gov8Plain, 'prototype').writable")

	// read_only_prototype: sloppy assignment silently fails.
	roFT, err := r.iso.NewFunctionTemplate(r.scope, advNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate ro: %v", err)
	}
	if err := roFT.SetClassName("Gov8RO"); err != nil {
		t.Fatalf("SetClassName ro: %v", err)
	}
	if err := roFT.ReadOnlyPrototype(); err != nil {
		t.Fatalf("ReadOnlyPrototype: %v", err)
	}
	roFn, err := roFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction ro: %v", err)
	}
	r.seedGlobal(t, "Gov8RO", roFn.Value)
	roAssignmentIgnored := r.evalText(t,
		"(Gov8RO.prototype = {}, Gov8RO.prototype.constructor === Gov8RO)")
	roWritable := r.evalText(t,
		"Object.getOwnPropertyDescriptor(Gov8RO, 'prototype').writable")

	// remove_prototype: like Throw, but retrofitted on a default template.
	removedFT, err := r.iso.NewFunctionTemplate(r.scope, advNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate removed: %v", err)
	}
	if err := removedFT.SetClassName("Gov8NoProto"); err != nil {
		t.Fatalf("SetClassName removed: %v", err)
	}
	if err := removedFT.RemovePrototype(); err != nil {
		t.Fatalf("RemovePrototype: %v", err)
	}
	removedFn, err := removedFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction removed: %v", err)
	}
	r.seedGlobal(t, "Gov8NoProto", removedFn.Value)
	removedPrototype := r.evalText(t, "typeof Gov8NoProto.prototype")
	removedCall := r.evalText(t, "Gov8NoProto()")
	removedNew := r.evalCaught(t, "new Gov8NoProto()")

	return jsonString(jobj(
		kv("concise_prototype", jstr(concisePrototype)),
		kv("concise_name", jstr(conciseName)),
		kv("concise_call", jstr(conciseCall)),
		kv("concise_new", jstr(conciseNew)),
		kv("plain_prototype", jstr(plainPrototype)),
		kv("plain_constructor_link", jstr(plainConstructorLink)),
		kv("plain_writable", jstr(plainWritable)),
		kv("ro_assignment_ignored", jstr(roAssignmentIgnored)),
		kv("ro_writable", jstr(roWritable)),
		kv("removed_prototype", jstr(removedPrototype)),
		kv("removed_call", jstr(removedCall)),
		kv("removed_new", jstr(removedNew)),
	))
}

// --- 9. template inheritance ----------------------------------------------------------

func checkInheritanceChain(t *testing.T, r *runtime) string {
	t.Helper()

	baseFT, err := r.iso.NewFunctionTemplate(r.scope, advNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate base: %v", err)
	}
	if err := baseFT.SetClassName("Gov8Base"); err != nil {
		t.Fatalf("SetClassName base: %v", err)
	}
	baseProto, err := baseFT.PrototypeTemplate()
	if err != nil {
		t.Fatalf("PrototypeTemplate base: %v", err)
	}
	markB, _ := r.scope.NewString("B")
	if err := baseProto.Set("baseMark", markB); err != nil {
		t.Fatalf("baseProto.Set: %v", err)
	}
	// Template-level statics: properties on the function itself.
	markS, _ := r.scope.NewString("s")
	if err := baseFT.Set("baseStatic", markS); err != nil {
		t.Fatalf("baseFT.Set baseStatic: %v", err)
	}

	derivedFT, err := r.iso.NewFunctionTemplate(r.scope, advNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate derived: %v", err)
	}
	if err := derivedFT.SetClassName("Gov8Derived"); err != nil {
		t.Fatalf("SetClassName derived: %v", err)
	}
	if err := derivedFT.Inherit(baseFT); err != nil {
		t.Fatalf("Inherit: %v", err)
	}
	derivedProto, err := derivedFT.PrototypeTemplate()
	if err != nil {
		t.Fatalf("PrototypeTemplate derived: %v", err)
	}
	markD, _ := r.scope.NewString("D")
	if err := derivedProto.Set("derivedMark", markD); err != nil {
		t.Fatalf("derivedProto.Set: %v", err)
	}

	baseCtor, err := baseFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction base: %v", err)
	}
	r.seedGlobal(t, "Gov8Base", baseCtor.Value)
	derivedCtor, err := derivedFT.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction derived: %v", err)
	}
	r.seedGlobal(t, "Gov8Derived", derivedCtor.Value)

	protoChain := r.evalText(t,
		"Object.getPrototypeOf(Gov8Derived.prototype) === Gov8Base.prototype")
	instanceOf := r.evalText(t,
		"(new Gov8Derived() instanceof Gov8Derived) + '|' + (new Gov8Derived() instanceof Gov8Base)")
	marks := r.evalText(t,
		"new Gov8Derived().baseMark + '|' + new Gov8Derived().derivedMark")
	statics := r.evalText(t, "Gov8Base.baseStatic + '|' + Gov8Derived.baseStatic")
	constructorIdentity := r.evalText(t, "new Gov8Derived().constructor === Gov8Derived")
	derivedConstructorLink := r.evalText(t, "Gov8Derived.prototype.constructor === Gov8Derived")
	baseProtoStaticNotInherited := r.evalText(t,
		"Object.prototype.hasOwnProperty.call(Gov8Derived.prototype, 'baseMark')")

	return jsonString(jobj(
		kv("proto_chain", jstr(protoChain)),
		kv("instance_of", jstr(instanceOf)),
		kv("marks", jstr(marks)),
		kv("statics", jstr(statics)),
		kv("constructor_identity", jstr(constructorIdentity)),
		kv("derived_constructor_link", jstr(derivedConstructorLink)),
		kv("base_proto_static_not_inherited", jstr(baseProtoStaticNotInherited)),
	))
}

// --- 10. accessor properties (accessor-shaped) on object templates ---------------------

func checkAccessorPropertyShapes(t *testing.T, r *runtime) string {
	t.Helper()
	l := &advLogger{}

	returnFive := func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		_ = rv.SetInt32(5)
	}
	setter := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		arg0, _ := args.Get(0)
		this, _ := args.This()
		var thisOK bool
		if this != nil {
			h, err := this.GetHash()
			thisOK = l.matchesHash(h, err)
		}
		l.push("set:args=" + itoa64(int64(args.Length())) +
			" arg0=" + advText(cs, arg0) +
			" this_ok=" + b2s(thisOK))
	}

	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	getterFT, err := r.iso.NewFunctionTemplate(r.scope, returnFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate getter: %v", err)
	}
	setterFT, err := r.iso.NewFunctionTemplate(r.scope, setter, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate setter: %v", err)
	}
	if err := ot.SetAccessorProperty("acc", getterFT, setterFT, gov8.AttrNone); err != nil {
		t.Fatalf("SetAccessorProperty acc: %v", err)
	}
	hiddenGetterFT, err := r.iso.NewFunctionTemplate(r.scope, returnFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate hidden: %v", err)
	}
	if err := ot.SetAccessorProperty("hidden", hiddenGetterFT, nil, gov8.AttrDontEnum); err != nil {
		t.Fatalf("SetAccessorProperty hidden: %v", err)
	}
	obj, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	l.setHash(obj.Value)
	r.seedGlobal(t, "ao", obj.Value)

	readInvokesGetter := r.evalText(t, "typeof ao.acc")
	getterCall := r.evalText(t,
		"Object.getOwnPropertyDescriptor(ao, 'acc').get.call(ao)")
	descriptorGet := r.evalText(t,
		"typeof Object.getOwnPropertyDescriptor(ao, 'acc').get")
	descriptorSet := r.evalText(t,
		"typeof Object.getOwnPropertyDescriptor(ao, 'acc').set")
	setterSeen := r.evalText(t, "(ao.acc = 9)")
	hiddenReadable := r.evalText(t, "ao.hidden")
	enumeration := r.evalText(t, "Object.keys(ao).join(',')")

	return jsonString(jobj(
		kv("read_invokes_getter", jstr(readInvokesGetter)),
		kv("getter_call", jstr(getterCall)),
		kv("descriptor_get", jstr(descriptorGet)),
		kv("descriptor_set", jstr(descriptorSet)),
		kv("setter_seen", jstr(setterSeen)),
		kv("hidden_readable", jstr(hiddenReadable)),
		kv("enumeration", jstr(enumeration)),
		kv("callback_log", jstr(l.join())),
	))
}

// --- 11. internal-field / aligned-pointer boundaries ------------------------------------

func checkInternalFieldBoundaries(t *testing.T, r *runtime) string {
	t.Helper()

	// Default template: zero internal fields.
	defaultOT, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	defaultCount, err := defaultOT.InternalFieldCount()
	if err != nil {
		t.Fatalf("InternalFieldCount: %v", err)
	}
	zeroSet, err := defaultOT.SetInternalFieldCount(0)
	if err != nil {
		t.Fatalf("SetInternalFieldCount(0): %v", err)
	}
	defaultCountAfterZero, _ := defaultOT.InternalFieldCount()
	zeroInstance, ok, err := defaultOT.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("zero instance: ok=%v err=%v", ok, err)
	}
	zeroInstanceCount, _ := zeroInstance.InternalFieldCount()
	_, zeroHas, err := zeroInstance.GetInternalField(0)
	if err != nil {
		t.Fatalf("zero GetInternalField: %v", err)
	}
	one, _ := r.scope.Int32(1)
	zeroSetField, err := zeroInstance.SetInternalField(0, one)
	if err != nil {
		t.Fatalf("zero SetInternalField: %v", err)
	}

	// The count is frozen by the FIRST instantiation: instances created
	// before and after a template re-set both carry the original count
	// (later set_internal_field_count calls are silently inert).
	growingOT, _ := r.iso.NewObjectTemplate(r.scope)
	if _, err := growingOT.SetInternalFieldCount(1); err != nil {
		t.Fatalf("growing count 1: %v", err)
	}
	earlyInstance, ok, _ := growingOT.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("early instance")
	}
	if _, err := growingOT.SetInternalFieldCount(3); err != nil {
		t.Fatalf("growing count 3: %v", err)
	}
	lateInstance, ok, _ := growingOT.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("late instance")
	}
	earlyCount, _ := earlyInstance.InternalFieldCount()
	lateCount, _ := lateInstance.InternalFieldCount()

	// Impossible counts are rejected at the wrapper boundary (no V8 call).
	hugeCountSet, err := growingOT.SetInternalFieldCount(int(^uint(0) >> 1))
	if err != nil {
		t.Fatalf("huge count: %v", err)
	}

	// Aligned pointers across the valid tag range 0..15 and mixed usage.
	// The oracle stores raw Box pointers; the Go binding's ownership rule
	// requires integer registry tokens instead (same observable round-trip).
	tokenA, err := r.iso.HostRefAdd([]uint32{111})
	if err != nil {
		t.Fatalf("HostRefAdd a: %v", err)
	}
	tokenB, err := r.iso.HostRefAdd([]uint32{222})
	if err != nil {
		t.Fatalf("HostRefAdd b: %v", err)
	}
	tokenC, err := r.iso.HostRefAdd([]uint32{333})
	if err != nil {
		t.Fatalf("HostRefAdd c: %v", err)
	}

	alignedOT, _ := r.iso.NewObjectTemplate(r.scope)
	if _, err := alignedOT.SetInternalFieldCount(2); err != nil {
		t.Fatalf("aligned count: %v", err)
	}
	aligned, ok, _ := alignedOT.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("aligned instance")
	}
	if err := aligned.SetAlignedPointerInInternalField(0, tokenA, 0); err != nil {
		t.Fatalf("aligned tag 0: %v", err)
	}
	if err := aligned.SetAlignedPointerInInternalField(1, tokenB, 14); err != nil {
		t.Fatalf("aligned tag 14: %v", err)
	}
	tagZeroRoundtrip := false
	if got, ok, err := aligned.GetAlignedPointerFromInternalField(0, 0); err == nil && ok {
		tagZeroRoundtrip = got == tokenA
	}
	tagMaxRoundtrip := false
	if got, ok, err := aligned.GetAlignedPointerFromInternalField(1, 14); err == nil && ok {
		tagMaxRoundtrip = got == tokenB
	}

	// Re-targeting the same field with a different tag: the last write wins.
	if err := aligned.SetAlignedPointerInInternalField(0, tokenC, 5); err != nil {
		t.Fatalf("aligned retag: %v", err)
	}
	retargetRoundtrip := false
	if got, ok, err := aligned.GetAlignedPointerFromInternalField(0, 5); err == nil && ok {
		retargetRoundtrip = got == tokenC
	}

	// A null aligned pointer round-trips as null.
	nullOT, _ := r.iso.NewObjectTemplate(r.scope)
	if _, err := nullOT.SetInternalFieldCount(1); err != nil {
		t.Fatalf("null count: %v", err)
	}
	nullInstance, ok, _ := nullOT.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("null instance")
	}
	if err := nullInstance.SetAlignedPointerInInternalField(0, 0, 3); err != nil {
		t.Fatalf("null pointer store: %v", err)
	}
	nullRoundtrip := false
	if got, ok, err := nullInstance.GetAlignedPointerFromInternalField(0, 3); err == nil && ok {
		nullRoundtrip = got == 0
	}

	// Aligned and Data fields coexist on one object when used consistently.
	mixedOT, _ := r.iso.NewObjectTemplate(r.scope)
	if _, err := mixedOT.SetInternalFieldCount(2); err != nil {
		t.Fatalf("mixed count: %v", err)
	}
	mixed, ok, _ := mixedOT.NewInstance(r.scope, r.ctx)
	if !ok {
		t.Fatalf("mixed instance")
	}
	if err := mixed.SetAlignedPointerInInternalField(0, tokenA, 7); err != nil {
		t.Fatalf("mixed aligned: %v", err)
	}
	fortyTwo, _ := r.scope.Int32(42)
	dataStored, err := mixed.SetInternalField(1, fortyTwo)
	if err != nil {
		t.Fatalf("mixed data store: %v", err)
	}
	dataRoundtrip := int64(-1)
	if back, has, err := mixed.GetInternalField(1); err == nil && has {
		if n, nerr := back.IntegerValueRaw(); nerr == nil {
			dataRoundtrip = n
		}
	}
	alignedSideRoundtrip := false
	if got, ok, err := mixed.GetAlignedPointerFromInternalField(0, 7); err == nil && ok {
		alignedSideRoundtrip = got == tokenA
	}

	// Reclaim the host allocations and verify the payloads survived.
	nativeRoundtrip := false
	checkPayload := func(token uintptr, want uint32) bool {
		v, ok := r.iso.HostRefGet(token)
		if !ok {
			return false
		}
		box, isSlice := v.([]uint32)
		return isSlice && len(box) == 1 && box[0] == want
	}
	nativeRoundtrip = checkPayload(tokenA, 111) && checkPayload(tokenB, 222) &&
		checkPayload(tokenC, 333)

	return jsonString(jobj(
		kv("default_count", jint(int64(defaultCount))),
		kv("zero_set", jbool(zeroSet)),
		kv("default_count_after_zero", jint(int64(defaultCountAfterZero))),
		kv("zero_instance_count", jint(int64(zeroInstanceCount))),
		kv("zero_get_is_none", jbool(!zeroHas)),
		kv("zero_set_field", jbool(zeroSetField)),
		kv("early_count", jint(int64(earlyCount))),
		kv("late_count", jint(int64(lateCount))),
		kv("huge_count_set", jbool(hugeCountSet)),
		kv("tag_zero_roundtrip", jbool(tagZeroRoundtrip)),
		kv("tag_max_roundtrip", jbool(tagMaxRoundtrip)),
		kv("retarget_roundtrip", jbool(retargetRoundtrip)),
		kv("null_roundtrip", jbool(nullRoundtrip)),
		kv("data_stored", jbool(dataStored)),
		kv("data_roundtrip", jint(dataRoundtrip)),
		kv("aligned_side_roundtrip", jbool(alignedSideRoundtrip)),
		kv("native_roundtrip", jbool(nativeRoundtrip)),
	))
}

// --- 12. security tokens (the crate's whole access-check surface) -----------------------

func strictEq(a, b gov8.Value) bool {
	// Zero values fail the StrictEquals prechecks inside gov8; just route
	// through and treat any error as "not equal".
	same, err := a.StrictEquals(b)
	return err == nil && same
}

func checkSecurityTokenContexts(t *testing.T, r *runtime) string {
	t.Helper()

	ctx1, err := r.iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext 1: %v", err)
	}
	defer func() { _ = ctx1.Close() }()
	ctx2, err := r.iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext 2: %v", err)
	}
	defer func() { _ = ctx2.Close() }()
	ctx3, err := r.iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext 3: %v", err)
	}
	defer func() { _ = ctx3.Close() }()

	// The DEFAULT security token of a context is the context's own global
	// object, so every fresh context carries a distinct token.
	token1, err := ctx1.GetSecurityToken(r.scope)
	if err != nil {
		t.Fatalf("GetSecurityToken 1: %v", err)
	}
	token2, err := ctx2.GetSecurityToken(r.scope)
	if err != nil {
		t.Fatalf("GetSecurityToken 2: %v", err)
	}
	token3, err := ctx3.GetSecurityToken(r.scope)
	if err != nil {
		t.Fatalf("GetSecurityToken 3: %v", err)
	}
	defaultsEqual12 := strictEq(token1, token2)
	defaultsEqual23 := strictEq(token2, token3)

	// A plain host object created in ctx1. Plain objects carry no
	// access-check info, so once bridged they are readable from any context
	// regardless of tokens.
	sharedObj, err := r.scope.NewObject(ctx1)
	if err != nil {
		t.Fatalf("NewObject shared: %v", err)
	}
	m1, _ := r.scope.NewString("m1")
	if _, err := sharedObj.SetByName(r.scope, ctx1, "mark", m1); err != nil {
		t.Fatalf("shared SetByName mark: %v", err)
	}
	global1, err := ctx1.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject 1: %v", err)
	}
	if _, err := global1.SetByName(r.scope, ctx1, "o1", sharedObj.Value); err != nil {
		t.Fatalf("seed ctx1 o1: %v", err)
	}
	// The bridge value: a live local fetched through ctx1's own global (the
	// Go analog of the oracle's v8::Global handle across the check).
	shared, okShared, err := global1.GetByName(r.scope, ctx1, "o1")
	if err != nil || !okShared {
		t.Fatalf("fetch shared o1: ok=%v err=%v", okShared, err)
	}

	// Bridging into the receiving context's own global always works.
	global2, err := ctx2.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject 2: %v", err)
	}
	ownGlobalSet, err := global2.SetByName(r.scope, ctx2, "o1", shared)
	if err != nil {
		t.Fatalf("bridge ctx2: %v", err)
	}

	// Run `o1.mark` in ctx2.
	readFrom := func(target *gov8.Context, source string) string {
		sc, err := r.iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope read: %v", err)
		}
		defer func() { _ = sc.Close() }()
		script, cerr := target.Compile(sc, source, nil)
		if cerr != nil {
			return ""
		}
		defer func() { _ = script.Close() }()
		v, rerr := script.Run(sc, nil)
		if rerr != nil {
			return ""
		}
		txt, terr := v.ToString(target)
		if terr != nil {
			return ""
		}
		return txt
	}
	readFromCtx2 := readFrom(ctx2, "o1.mark")

	// Setting a property on ANOTHER context's global proxy while tokens
	// differ is denied: the set throws and the exception carries V8's
	// SecurityError text. The context ARGUMENT of the operation is the
	// accessing context — ctx2, the context the oracle keeps entered.
	global3, err := ctx3.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject 3: %v", err)
	}
	crossTokenSetDenied, denialCaught, denialMessage := func() (bool, bool, string) {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		okSet, _ := global3.SetByName(r.scope, ctx2, "o1", shared)
		caught, _ := tc.HasCaught()
		msg := ""
		if caught {
			msg, _ = tc.MessageText(r.scope, ctx3)
		}
		return !okSet, caught, msg
	}()
	readFromCtx3Before := readFrom(ctx3, "typeof o1")

	// Sharing a token (any Value) re-enables cross-context global access.
	if err := ctx3.SetSecurityToken(r.scope, token2); err != nil {
		t.Fatalf("SetSecurityToken: %v", err)
	}
	token3After, err := ctx3.GetSecurityToken(r.scope)
	if err != nil {
		t.Fatalf("GetSecurityToken 3 after: %v", err)
	}
	tokensShared := strictEq(token3After, token2)
	sharedTokenSet, err := global3.SetByName(r.scope, ctx2, "o1", shared)
	if err != nil {
		t.Fatalf("bridge ctx3: %v", err)
	}
	readFromCtx3After := readFrom(ctx3, "o1.mark")

	// Resetting restores the context's own global object as the token.
	if err := ctx3.UseDefaultSecurityToken(); err != nil {
		t.Fatalf("UseDefaultSecurityToken: %v", err)
	}
	token3Reset, err := ctx3.GetSecurityToken(r.scope)
	if err != nil {
		t.Fatalf("GetSecurityToken 3 reset: %v", err)
	}
	resetTokenIsCtx3Own := !strictEq(token3Reset, token2)
	deniedAgain, denialCaughtAgain := func() (bool, bool) {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch 2: %v", err)
		}
		defer func() { _ = tc.Close() }()
		global1b, err := ctx1.GlobalObject(r.scope)
		if err != nil {
			t.Fatalf("GlobalObject 1b: %v", err)
		}
		okSet, _ := global1b.SetByName(r.scope, ctx2, "o1", shared)
		caught, _ := tc.HasCaught()
		return !okSet, caught
	}()

	return jsonString(jobj(
		kv("defaults_equal_1_2", jbool(defaultsEqual12)),
		kv("defaults_equal_2_3", jbool(defaultsEqual23)),
		kv("own_global_set", jbool(ownGlobalSet)),
		kv("read_from_ctx2", jstr(readFromCtx2)),
		kv("cross_token_set_denied", jbool(crossTokenSetDenied)),
		kv("denial_caught", jbool(denialCaught)),
		kv("denial_message", jstr(denialMessage)),
		kv("read_from_ctx3_before", jstr(readFromCtx3Before)),
		kv("tokens_shared", jbool(tokensShared)),
		kv("shared_token_set", jbool(sharedTokenSet)),
		kv("read_from_ctx3_after", jstr(readFromCtx3After)),
		kv("reset_token_is_ctx3_own", jbool(resetTokenIsCtx3Own)),
		kv("denied_again", jbool(deniedAgain)),
		kv("denial_caught_again", jbool(denialCaughtAgain)),
	))
}

// --- 13. call-as-function handler on object templates -------------------------------------

func checkCallAsFunctionHandler(t *testing.T, r *runtime) string {
	t.Helper()
	l := &advLogger{}

	handler := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		arg0, _ := args.Get(0)
		this, _ := args.This()
		data, _ := args.Data()
		var thisOK bool
		if this != nil {
			h, err := this.GetHash()
			thisOK = l.matchesHash(h, err)
		}
		l.push("caf:construct=" + b2s(args.IsConstructCall()) +
			" data=" + advText(cs, data) +
			" this_ok=" + b2s(thisOK) +
			" arg0=" + advText(cs, arg0))
		doubled := advIntOr(cs, arg0, 0) * 2
		_ = rv.SetInt32(int32(doubled))
	}

	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	data, _ := r.scope.NewString("caf-data")
	if err := ot.SetCallAsFunctionHandler(handler, data); err != nil {
		t.Fatalf("SetCallAsFunctionHandler: %v", err)
	}
	obj, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	l.setHash(obj.Value)
	r.seedGlobal(t, "co", obj.Value)

	callResult := r.evalText(t, "co(4)")
	typeOf := r.evalText(t, "typeof co")
	toStringTag := r.evalText(t, "Object.prototype.toString.call(co)")
	// Construct calls dispatch to the SAME handler: is_construct_call() is
	// true, `this` is the instance, and even a primitive return value is
	// delivered as the construct result.
	constructAttempt := r.evalCaught(t, "new co(1)")
	callbackLog := l.join()

	return jsonString(jobj(
		kv("call_result", jstr(callResult)),
		kv("type_of", jstr(typeOf)),
		kv("to_string_tag", jstr(toStringTag)),
		kv("construct_attempt", jstr(constructAttempt)),
		kv("callback_log", jstr(callbackLog)),
	))
}

// --- 14. immutable prototype object templates -----------------------------------------------

func checkImmutableProto(t *testing.T, r *runtime) string {
	t.Helper()

	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetImmutableProto(); err != nil {
		t.Fatalf("SetImmutableProto: %v", err)
	}
	obj, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	r.seedGlobal(t, "ip", obj.Value)

	// Immutable-prototype instances REJECT prototype mutations by throwing,
	// while ordinary property reads keep working through the default chain.
	setProtoThrows := r.evalCaught(t, "Object.setPrototypeOf(ip, {x: 1})")
	newPropMissing := r.evalText(t, "ip.x")
	dunderThrows := r.evalCaught(t, "(ip.__proto__ = {y: 2})")
	defaultProtoOk := r.evalText(t, "ip.toString === Object.prototype.toString")

	return jsonString(jobj(
		kv("set_proto_throws", jstr(setProtoThrows)),
		kv("new_prop_missing", jstr(newPropMissing)),
		kv("dunder_throws", jstr(dunderThrows)),
		kv("default_proto_ok", jstr(defaultProtoOk)),
	))
}

// allAdvChecks is the fixed oracle order
// (rust-oracle/src/bin/conformance-template-advanced.rs CHECKS).
func allAdvChecks() []advCheck {
	return []advCheck{
		{"tpladv/named_interceptor_get_set", checkNamedInterceptorGetSet},
		{"tpladv/named_interceptor_query_delete_enum_define", checkNamedInterceptorQueryDeleteEnumDefine},
		{"tpladv/indexed_interceptor_full_family", checkIndexedInterceptorFullFamily},
		{"tpladv/flag_interceptors", checkFlagInterceptors},
		{"tpladv/return_value_get_and_specials", checkReturnValueGetAndSpecials},
		{"tpladv/signature_receiver_enforcement", checkSignatureReceiverEnforcement},
		{"tpladv/intrinsic_data_property", checkIntrinsicDataProperty},
		{"tpladv/constructor_behavior_and_prototype", checkConstructorBehaviorAndPrototype},
		{"tpladv/inheritance_chain", checkInheritanceChain},
		{"tpladv/accessor_property_shapes", checkAccessorPropertyShapes},
		{"tpladv/internal_field_boundaries", checkInternalFieldBoundaries},
		{"tpladv/security_token_contexts", checkSecurityTokenContexts},
		{"tpladv/call_as_function_handler", checkCallAsFunctionHandler},
		{"tpladv/immutable_proto", checkImmutableProto},
	}
}
