//go:build windows && amd64

package gov8_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Focused conformance tests for the template-advanced surface: property
// interceptor families and flags, ReturnValue.Get/specials, signatures,
// intrinsic data properties, constructor behavior/prototype controls,
// template inheritance, accessor-shaped properties, internal-field and tag
// boundaries, security tokens, the call-as-function handler, immutable
// prototypes, and the misuse/lifecycle boundaries of every new API.
//
// The byte-for-byte fixture comparison against the pinned Rust oracle lives
// in conformance-template-advanced; these tests pin the individual API
// semantics and the error paths in-process.

func advNewIso(t *testing.T) *gov8.Isolate {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	return iso
}

func advNewCtx(t *testing.T, iso *gov8.Isolate) (*gov8.Scope, *gov8.Context) {
	t.Helper()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return scope, ctx
}

func advSeed(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, name string, v gov8.Value) {
	t.Helper()
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(scope, ctx, name, v); err != nil {
		t.Fatalf("SetByName %s: %v", name, err)
	}
}

func advEval(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, src string) gov8.Value {
	t.Helper()
	script, err := ctx.Compile(scope, src, nil)
	if err != nil {
		t.Fatalf("Compile %q: %v", src, err)
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("Run %q: %v", src, err)
	}
	return v
}

func advEvalText(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, src string) string {
	t.Helper()
	v := advEval(t, scope, ctx, src)
	txt, err := v.ToString(ctx)
	if err != nil {
		t.Fatalf("ToString %q: %v", src, err)
	}
	return txt
}

// evalOK evaluates src and reports success separately (for expressions the
// probe treats as optional).
func evalOK(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, src string) (gov8.Value, bool) {
	t.Helper()
	v, err := eval(t, ctx, scope, src)
	if err != nil {
		return gov8.Value{}, false
	}
	return v, true
}

// evalCaught runs src under a fresh TryCatch and returns either the
// completion value's text or the exception message ("Uncaught ..."),
// mirroring the oracle's eval_caught.
func advEvalCaught(t *testing.T, iso *gov8.Isolate, scope *gov8.Scope, ctx *gov8.Context, src string) string {
	t.Helper()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	text := ""
	script, cerr := ctx.Compile(scope, src, tc)
	if cerr == nil {
		v, rerr := script.Run(scope, tc)
		if rerr == nil {
			text, _ = v.ToString(ctx)
		}
		_ = script.Close()
	}
	caught, _ := tc.HasCaught()
	if caught {
		msg, merr := tc.MessageText(scope, ctx)
		if merr == nil {
			text = msg
		}
	}
	return text
}

// --- named interceptor: getter + setter + fall-through ----------------------------

func TestNamedInterceptorGetSetFallthrough(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	var log []string
	real, err := scope.NewString("R")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	data77, err := scope.Int32(77)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	getter := func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		name, _ := cs.ToString(key)
		if name == "in_a" || name == "in_b" {
			s, _ := cs.NewString(strings.TrimPrefix(strings.ToUpper(name), "IN_"))
			_ = rv.Set(s)
			holder, _ := args.Holder()
			var holderOK bool
			if holder != nil {
				hash, herr := holder.GetHash()
				holderOK = herr == nil && hash != 0
			}
			data, _ := args.Data()
			log = append(log, "get:"+name+":yes:holder="+b2s(holderOK)+":"+
				b2s(args.ShouldThrowOnError())+":data="+mustText(t, cs, data))
			return gov8.InterceptedYes
		}
		log = append(log, "get:"+name+":no")
		return gov8.InterceptedNo
	}
	setter := func(cs *gov8.CallbackScope, key, value gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		name, _ := cs.ToString(key)
		if strings.HasPrefix(name, "in_") {
			_ = rv.SetBool(true)
			n, _, _ := cs.IntegerValue(value)
			log = append(log, "set:"+name+":"+itoa(int(n))+":strict="+b2s(args.ShouldThrowOnError()))
			return gov8.InterceptedYes
		}
		return gov8.InterceptedNo
	}

	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.Set("real", real); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: getter, Setter: setter, Data: data77,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler: %v", err)
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	advSeed(t, scope, ctx, "o", obj.Value)

	if got := advEvalText(t, scope, ctx, "o.real"); got != "R" {
		t.Errorf("o.real = %q; want R", got)
	}
	if got := advEvalText(t, scope, ctx, "o.in_a"); got != "A" {
		t.Errorf("o.in_a = %q; want A", got)
	}
	if got := advEvalText(t, scope, ctx, "o.missing"); got != "undefined" {
		t.Errorf("o.missing = %q; want undefined", got)
	}
	if got := advEvalText(t, scope, ctx, "(o.in_a = 11)"); got != "11" {
		t.Errorf("assignment = %q; want 11", got)
	}
	if got := advEvalText(t, scope, ctx, "o.in_a"); got != "A" {
		t.Errorf("o.in_a after assignment = %q; want A", got)
	}
	if got := advEvalText(t, scope, ctx, "(o.plain_new = 42)"); got != "42" {
		t.Errorf("fallback assignment = %q; want 42", got)
	}
	if got := advEvalText(t, scope, ctx, "o.plain_new"); got != "42" {
		t.Errorf("fallback read = %q; want 42", got)
	}
	if got := advEvalCaught(t, iso, scope, ctx, "(() => { 'use strict'; o.in_b = 12; })()"); got != "undefined" {
		t.Errorf("strict assignment = %q; want undefined", got)
	}
	// The kNo setter fall-through created a real own property; the
	// intercepted setter did not.
	if got := advEvalText(t, scope, ctx, "Object.prototype.hasOwnProperty.call(o, 'plain_new')"); got != "true" {
		t.Errorf("own plain_new = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.prototype.hasOwnProperty.call(o, 'never_set')"); got != "false" {
		t.Errorf("own never_set = %q; want false", got)
	}
	joined := strings.Join(log, ";")
	for _, want := range []string{
		"get:real:no",
		"get:in_a:yes:holder=true:false:data=77",
		"get:missing:no",
		"set:in_a:11:strict=false",
		"set:in_b:12:strict=false",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("callback log missing %q; log:\n%s", want, joined)
		}
	}
}

// --- query / deleter / enumerator / definer / descriptor ---------------------------

func TestNamedInterceptorQueryDeleteEnumerateDefineDescriptor(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	// query-only: hasOwnProperty consults the query handler.
	qt, _ := iso.NewObjectTemplate(scope)
	if err := qt.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Query: func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			name, _ := cs.ToString(key)
			if name == "q" {
				_ = rv.SetInt32(int32(gov8.AttrReadOnly | gov8.AttrDontEnum))
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler query: %v", err)
	}
	qObj, ok, _ := qt.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("query instance")
	}
	advSeed(t, scope, ctx, "q_o", qObj.Value)
	if got := advEvalText(t, scope, ctx, "Object.prototype.hasOwnProperty.call(q_o, 'q')"); got != "true" {
		t.Errorf("query hasOwnProperty = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.prototype.propertyIsEnumerable.call(q_o, 'q')"); got != "false" {
		t.Errorf("query propertyIsEnumerable = %q; want false", got)
	}

	// deleter-only.
	dt, _ := iso.NewObjectTemplate(scope)
	if err := dt.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Deleter: func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			name, _ := cs.ToString(key)
			if name == "del" {
				_ = rv.SetBool(false)
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler deleter: %v", err)
	}
	dObj, ok, _ := dt.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("deleter instance")
	}
	advSeed(t, scope, ctx, "d_o", dObj.Value)
	if got := advEvalText(t, scope, ctx, "(delete d_o.del)"); got != "false" {
		t.Errorf("delete intercepted = %q; want false", got)
	}
	if got := advEvalText(t, scope, ctx, "(delete d_o.other)"); got != "true" {
		t.Errorf("delete fallback = %q; want true", got)
	}

	// enumerator-only: keys come from the returned array in order.
	et, _ := iso.NewObjectTemplate(scope)
	if err := et.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Enumerator: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			one, _ := cs.Scope().Int32(1)
			a, _ := cs.NewString("a")
			c, _ := cs.NewString("c")
			b, _ := cs.NewString("b")
			arr, err := cs.NewArrayWithElements([]gov8.Value{one, a, c, b})
			if err != nil {
				t.Errorf("NewArrayWithElements: %v", err)
				return
			}
			_ = rv.Set(arr)
		},
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler enumerator: %v", err)
	}
	eObj, ok, _ := et.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("enumerator instance")
	}
	advSeed(t, scope, ctx, "e_o", eObj.Value)
	if got := advEvalText(t, scope, ctx, "Object.keys(e_o).join(',')"); got != "1,a,c,b" {
		t.Errorf("keys = %q; want 1,a,c,b", got)
	}

	// definer-only: descriptor view + intercepted vs fallback defines.
	var defineLog string
	ft, _ := iso.NewObjectTemplate(scope)
	if err := ft.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Definer: func(cs *gov8.CallbackScope, key gov8.Value, desc gov8.CallbackPropertyDescriptor, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			name, _ := cs.ToString(key)
			if name != "def" {
				return gov8.InterceptedNo
			}
			vText := ""
			if desc.HasValue() {
				v, ok, err := desc.Value()
				if err != nil || !ok {
					t.Errorf("descriptor value: ok=%v err=%v", ok, err)
					return gov8.InterceptedNo
				}
				vText = mustText(t, cs, v)
			}
			defineLog = "define:" + name +
				":has_value=" + b2s(desc.HasValue()) +
				" value=" + vText +
				" has_writable=" + b2s(desc.HasWritable()) +
				" writable=" + b2s(desc.Writable()) +
				" has_enum=" + b2s(desc.HasEnumerable()) +
				" enum=" + b2s(desc.Enumerable()) +
				" has_conf=" + b2s(desc.HasConfigurable()) +
				" conf=" + b2s(desc.Configurable())
			_ = rv.SetBool(true)
			return gov8.InterceptedYes
		},
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler definer: %v", err)
	}
	defObj, ok, _ := ft.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("definer instance")
	}
	advSeed(t, scope, ctx, "def_o", defObj.Value)
	if got := advEvalText(t, scope, ctx, "Object.defineProperty(def_o, 'def', {value: 42}) === def_o"); got != "true" {
		t.Errorf("define intercepted = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "def_o.def"); got != "undefined" {
		t.Errorf("intercepted define not stored: %q; want undefined", got)
	}
	if want := "define:def:has_value=true value=42 has_writable=false writable=false has_enum=false enum=false has_conf=false conf=false"; defineLog != want {
		t.Errorf("definer log = %q; want %q", defineLog, want)
	}

	// descriptor-only: object-shaped and value-shaped descriptors.
	st, _ := iso.NewObjectTemplate(scope)
	if err := st.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Descriptor: func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			name, _ := cs.ToString(key)
			if name == "desc" {
				obj, err := cs.NewObject()
				if err != nil {
					return gov8.InterceptedNo
				}
				dv, _ := cs.NewString("d-v")
				wb, _ := cs.Scope().Boolean(false)
				tr, _ := cs.Scope().Boolean(true)
				if _, err := cs.ObjectSet(obj, "value", dv); err != nil {
					return gov8.InterceptedNo
				}
				if _, err := cs.ObjectSet(obj, "writable", wb); err != nil {
					return gov8.InterceptedNo
				}
				if _, err := cs.ObjectSet(obj, "enumerable", tr); err != nil {
					return gov8.InterceptedNo
				}
				if _, err := cs.ObjectSet(obj, "configurable", tr); err != nil {
					return gov8.InterceptedNo
				}
				_ = rv.Set(obj)
				return gov8.InterceptedYes
			}
			if name == "descnum" {
				seven, _ := cs.Scope().Int32(7)
				_ = rv.Set(seven)
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler descriptor: %v", err)
	}
	descObj, ok, _ := st.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("descriptor instance")
	}
	advSeed(t, scope, ctx, "desc_o", descObj.Value)
	if got := advEvalText(t, scope, ctx, "JSON.stringify(Object.getOwnPropertyDescriptor(desc_o, 'desc'))"); got != `{"value":"d-v","writable":false,"enumerable":true,"configurable":true}` {
		t.Errorf("descriptor = %q", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.getOwnPropertyDescriptor(desc_o, 'nope')"); got != "undefined" {
		t.Errorf("missing descriptor = %q; want undefined", got)
	}
	if got := advEvalCaught(t, iso, scope, ctx, "JSON.stringify(Object.getOwnPropertyDescriptor(desc_o, 'descnum'))"); got != "Uncaught TypeError: Property description must be an object: 7" {
		t.Errorf("number descriptor = %q", got)
	}
}

// --- indexed interceptor: full family ------------------------------------------------

func TestIndexedInterceptorFullFamily(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	var setterLog []string
	ot, _ := iso.NewObjectTemplate(scope)
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
				setterLog = append(setterLog, "set:7:"+mustText(t, cs, value))
				return gov8.InterceptedYes
			}
			return gov8.InterceptedNo
		},
		Query: func(cs *gov8.CallbackScope, index uint32, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			if index == 9 {
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
			n9, _ := cs.Scope().Int32(9)
			n4, _ := cs.Scope().Int32(4)
			n0, _ := cs.Scope().Int32(0)
			arr, _ := cs.NewArrayWithElements([]gov8.Value{n9, n4, n0})
			_ = rv.Set(arr)
		},
	}); err != nil {
		t.Fatalf("SetIndexedPropertyHandler: %v", err)
	}
	obj, ok, _ := ot.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("instance")
	}
	advSeed(t, scope, ctx, "io", obj.Value)

	if got := advEvalText(t, scope, ctx, "io[42]"); got != "4242" {
		t.Errorf("io[42] = %q; want 4242", got)
	}
	// Numeric strings canonicalize to the indexed handler.
	if got := advEvalText(t, scope, ctx, "io['42']"); got != "4242" {
		t.Errorf("io['42'] = %q; want 4242", got)
	}
	if got := advEvalText(t, scope, ctx, "io['43x']"); got != "undefined" {
		t.Errorf("io['43x'] = %q; want undefined", got)
	}
	if got := advEvalText(t, scope, ctx, "(io[7] = 'x')"); got != "x" {
		t.Errorf("io[7] assignment = %q; want x", got)
	}
	if got := advEvalText(t, scope, ctx, "io[7]"); got != "undefined" {
		t.Errorf("io[7] after intercepted set = %q; want undefined", got)
	}
	if len(setterLog) != 1 || setterLog[0] != "set:7:x" {
		t.Errorf("setter log = %v; want [set:7:x]", setterLog)
	}
	if got := advEvalText(t, scope, ctx, "(io[8] = 8)"); got != "8" {
		t.Errorf("fallback assignment = %q; want 8", got)
	}
	if got := advEvalText(t, scope, ctx, "(delete io[5])"); got != "false" {
		t.Errorf("delete intercepted = %q; want false", got)
	}
	if got := advEvalText(t, scope, ctx, "(delete io[6])"); got != "true" {
		t.Errorf("delete fallback = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.prototype.hasOwnProperty.call(io, 9)"); got != "true" {
		t.Errorf("has 9 = %q; want true", got)
	}
	// Real element keys first, then enumerator keys that survive the query
	// filter (only index 9 has a query answer).
	if got := advEvalText(t, scope, ctx, "Object.keys(io).join(',')"); got != "8,9" {
		t.Errorf("keys = %q; want 8,9", got)
	}
}

// --- property handler flags -----------------------------------------------------------

func TestPropertyHandlerFlags(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	symFlagGetter := func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		isSym, _ := key.IsSymbol()
		if isSym {
			s, _ := cs.NewString("SYM")
			_ = rv.Set(s)
			return gov8.InterceptedYes
		}
		name, _ := cs.ToString(key)
		if name == "str" {
			s, _ := cs.NewString("S")
			_ = rv.Set(s)
			return gov8.InterceptedYes
		}
		return gov8.InterceptedNo
	}

	stringsOnly, _ := iso.NewObjectTemplate(scope)
	if err := stringsOnly.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: symFlagGetter, Flags: gov8.HandlerFlagOnlyInterceptStrings,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler strings-only: %v", err)
	}
	stringsOnlyObj, ok, _ := stringsOnly.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("strings-only instance")
	}
	advSeed(t, scope, ctx, "strings_only", stringsOnlyObj.Value)

	desc, err := scope.NewString("s")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	sym, err := scope.NewSymbol(desc)
	if err != nil {
		t.Fatalf("NewSymbol: %v", err)
	}
	advSeed(t, scope, ctx, "sym", sym.Value)

	// Symbol keys bypass ONLY_INTERCEPT_STRINGS handlers entirely.
	if got := advEvalText(t, scope, ctx, "strings_only[sym]"); got != "undefined" {
		t.Errorf("symbol with flag = %q; want undefined", got)
	}
	if got := advEvalText(t, scope, ctx, "strings_only.str"); got != "S" {
		t.Errorf("string with flag = %q; want S", got)
	}

	allKeys, _ := iso.NewObjectTemplate(scope)
	if err := allKeys.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: symFlagGetter,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler default flags: %v", err)
	}
	allKeysObj, ok, _ := allKeys.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("default-flags instance")
	}
	advSeed(t, scope, ctx, "all_keys", allKeysObj.Value)
	if got := advEvalText(t, scope, ctx, "all_keys[sym]"); got != "SYM" {
		t.Errorf("symbol without flag = %q; want SYM", got)
	}
	if got := advEvalText(t, scope, ctx, "all_keys.str"); got != "S" {
		t.Errorf("string without flag = %q; want S", got)
	}

	// NON_MASKING: an existing own data property wins; absent properties are
	// still intercepted.
	maskingGetter := func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		s, _ := cs.NewString("G")
		_ = rv.Set(s)
		return gov8.InterceptedYes
	}
	for _, tc := range []struct {
		name  string
		flags gov8.PropertyHandlerFlags
		seed  string
	}{
		{"masked", gov8.HandlerFlagNone, "masked"},
		{"unmasked", gov8.HandlerFlagNonMasking, "unmasked"},
	} {
		ot, _ := iso.NewObjectTemplate(scope)
		one, _ := scope.Int32(1)
		if err := ot.Set("dup", one); err != nil {
			t.Fatalf("Set dup: %v", err)
		}
		if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
			Getter: maskingGetter, Flags: tc.flags,
		}); err != nil {
			t.Fatalf("SetNamedPropertyHandler %s: %v", tc.name, err)
		}
		obj, ok, _ := ot.NewInstance(scope, ctx)
		if !ok {
			t.Fatalf("%s instance", tc.name)
		}
		advSeed(t, scope, ctx, tc.seed, obj.Value)
	}
	if got := advEvalText(t, scope, ctx, "masked.dup"); got != "G" {
		t.Errorf("masking real = %q; want G", got)
	}
	if got := advEvalText(t, scope, ctx, "masked.absent"); got != "G" {
		t.Errorf("masking absent = %q; want G", got)
	}
	if got := advEvalText(t, scope, ctx, "unmasked.dup"); got != "1" {
		t.Errorf("non-masking real = %q; want 1", got)
	}
	if got := advEvalText(t, scope, ctx, "unmasked.absent"); got != "G" {
		t.Errorf("non-masking absent = %q; want G", got)
	}

	// HAS_NO_SIDE_EFFECT: the handler still runs in normal execution.
	sfx, _ := iso.NewObjectTemplate(scope)
	if err := sfx.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: maskingGetter, Flags: gov8.HandlerFlagHasNoSideEffect,
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler no-side-effect: %v", err)
	}
	sfxObj, ok, _ := sfx.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("no-side-effect instance")
	}
	advSeed(t, scope, ctx, "sfx_o", sfxObj.Value)
	if got := advEvalText(t, scope, ctx, "sfx_o.anything"); got != "G" {
		t.Errorf("no-side-effect normal mode = %q; want G", got)
	}
}

// --- handler configuration misuse ------------------------------------------------------

func TestPropertyHandlerConfigValidation(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{}); err == nil {
		t.Error("empty named config must be rejected")
	}
	if err := ot.SetIndexedPropertyHandler(gov8.IndexedPropertyHandlerConfig{}); err == nil {
		t.Error("empty indexed config must be rejected")
	}
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Flags: gov8.PropertyHandlerFlags(255),
	}); err == nil {
		t.Error("out-of-range flags must be rejected")
	}
	if err := ot.SetIndexedPropertyHandler(gov8.IndexedPropertyHandlerConfig{
		Flags: gov8.HandlerFlagNonMasking,
	}); err != nil {
		t.Errorf("flags-only config must be accepted: %v", err)
	}
	// Foreign-isolate data is rejected before the engine is touched.
	iso2, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate 2: %v", err)
	}
	defer func() { _ = iso2.Close() }()
	scope2, ctx2 := advNewCtx(t, iso2)
	defer func() { _ = ctx2.Close() }()
	foreignData, err := scope2.Int32(1)
	if err != nil {
		t.Fatalf("Int32 foreign: %v", err)
	}
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			return gov8.InterceptedNo
		},
		Data: foreignData,
	}); err == nil {
		t.Error("foreign data must be rejected")
	}
}

// --- ReturnValue.Get and the special setters --------------------------------------------

func TestReturnValueGetAndSpecials(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	specials := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		modeV, _ := args.Get(0)
		mode, _, _ := cs.IntegerValue(modeV)
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
	f, err := iso.NewFunction(scope, ctx, specials, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	advSeed(t, scope, ctx, "rv_specials", f.Value)

	cases := map[string]string{
		"String(JSON.stringify(rv_specials(0)))": "undefined",
		"String(JSON.stringify(rv_specials(1)))": "null",
		`String(JSON.stringify(rv_specials(2)))`: `""`,
		"String(JSON.stringify(rv_specials(3)))": "true",
		"String(JSON.stringify(rv_specials(4)))": "4294967295",
		"String(JSON.stringify(rv_specials(5)))": "2.5",
		"String(JSON.stringify(rv_specials(9)))": "undefined",
	}
	for src, want := range cases {
		if got := advEvalText(t, scope, ctx, src); got != want {
			t.Errorf("%s = %q; want %q", src, got, want)
		}
	}

	// Get() reads back what was set; unset reads back undefined.
	var log []string
	probe := func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		before, err := rv.Get()
		if err != nil {
			t.Errorf("rv.Get: %v", err)
			return
		}
		bUndef, _ := before.IsUndefined()
		_ = rv.SetInt32(7)
		after, err := rv.Get()
		if err != nil {
			t.Errorf("rv.Get after: %v", err)
			return
		}
		aNum, _ := after.IsNumber()
		n, _ := after.NumberValueRaw()
		log = append(log, "before_undefined="+b2s(bUndef),
			"after_number="+b2s(aNum)+" value="+itoa(int(n)))
	}
	fGet, err := iso.NewFunction(scope, ctx, probe, nil)
	if err != nil {
		t.Fatalf("NewFunction probe: %v", err)
	}
	if _, ok, err := fGet.Call(scope, undefinedV(t, scope)); err != nil || !ok {
		t.Fatalf("probe call: ok=%v err=%v", ok, err)
	}
	if got, want := strings.Join(log, ";"), "before_undefined=true;after_number=true value=7"; got != want {
		t.Errorf("get probe log = %q; want %q", got, want)
	}

	// Get() inside an accessor getter and an interceptor getter.
	accGetter := func(cs *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
		before, _ := rv.Get()
		bUndef, _ := before.IsUndefined()
		v, _ := cs.NewString("acc-v")
		_ = rv.Set(v)
		after, _ := rv.Get()
		same, _ := after.StrictEquals(v)
		log = append(log, "acc_before_undefined="+b2s(bUndef)+" acc_after_same="+b2s(same))
	}
	ot, _ := iso.NewObjectTemplate(scope)
	if err := ot.SetAccessorWithSetter("p", accGetter, nil); err != nil {
		t.Fatalf("SetAccessorWithSetter: %v", err)
	}
	accObj, ok, _ := ot.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("acc instance")
	}
	advSeed(t, scope, ctx, "acc_o", accObj.Value)
	if got := advEvalText(t, scope, ctx, "acc_o.p"); got != "acc-v" {
		t.Errorf("acc_o.p = %q; want acc-v", got)
	}

	intGetter := func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		before, _ := rv.Get()
		bUndef, _ := before.IsUndefined()
		v, _ := cs.NewString("g")
		_ = rv.Set(v)
		after, _ := rv.Get()
		same, _ := after.StrictEquals(v)
		log = append(log, "int_before_undefined="+b2s(bUndef)+" int_after_same="+b2s(same))
		return gov8.InterceptedYes
	}
	ot2, _ := iso.NewObjectTemplate(scope)
	if err := ot2.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{Getter: intGetter}); err != nil {
		t.Fatalf("SetNamedPropertyHandler: %v", err)
	}
	intObj, ok, _ := ot2.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("interceptor instance")
	}
	advSeed(t, scope, ctx, "int_o", intObj.Value)
	if got := advEvalText(t, scope, ctx, "int_o.k"); got != "g" {
		t.Errorf("int_o.k = %q; want g", got)
	}

	want := "before_undefined=true;after_number=true value=7;acc_before_undefined=true acc_after_same=true;int_before_undefined=true int_after_same=true"
	if got := strings.Join(log, ";"); got != want {
		t.Errorf("full log =\n%q\nwant\n%q", got, want)
	}
}

// --- signatures ---------------------------------------------------------------------------

func TestSignatureReceiverEnforcement(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	var expectedHash uint32
	method := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		this, _ := args.This()
		hash, _ := this.GetHash()
		dataText := ""
		if data, derr := args.Data(); derr == nil {
			dataText = mustText(t, cs, data)
		}
		_ = args.Length()
		v, _ := cs.NewString("ok")
		_ = rv.Set(v)
		expectedHashOK := expectedHash != 0 && hash == expectedHash
		if !expectedHashOK {
			t.Errorf("signature method called with wrong receiver (hash %d)", hash)
		}
		if dataText == "" {
			// data observed below via the JS log path; keep the callback
			// cheap and side-effect free.
		}
	}
	baseFT, err := iso.NewFunctionTemplate(scope, noopCB, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate base: %v", err)
	}
	if err := baseFT.SetClassName("Gov8SigBase"); err != nil {
		t.Fatalf("SetClassName: %v", err)
	}
	sig, err := iso.NewSignature(scope, baseFT)
	if err != nil {
		t.Fatalf("NewSignature: %v", err)
	}
	data, err := scope.NewString("sig-data")
	if err != nil {
		t.Fatalf("NewString data: %v", err)
	}
	methodFT, err := iso.NewFunctionTemplate(scope, method, &gov8.FunctionOptions{
		Length: 2, Data: data, Signature: sig,
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
	baseFn, err := baseFT.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction base: %v", err)
	}
	advSeed(t, scope, ctx, "Gov8SigBase", baseFn.Value)

	derivedFT, err := iso.NewFunctionTemplate(scope, noopCB, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate derived: %v", err)
	}
	if err := derivedFT.SetClassName("Gov8SigDerived"); err != nil {
		t.Fatalf("SetClassName derived: %v", err)
	}
	if err := derivedFT.Inherit(baseFT); err != nil {
		t.Fatalf("Inherit: %v", err)
	}
	derivedFn, err := derivedFT.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction derived: %v", err)
	}
	advSeed(t, scope, ctx, "Gov8SigDerived", derivedFn.Value)

	_ = advEvalText(t, scope, ctx, "var sd = new Gov8SigDerived(); var sb = new Gov8SigBase()")
	derivedInst, ok := evalOK(t, ctx, scope, "sd")
	if !ok {
		t.Fatalf("eval sd")
	}
	dHash, err := derivedInst.GetHash()
	if err != nil {
		t.Fatalf("GetHash derived: %v", err)
	}
	expectedHash = dHash
	if got := advEvalText(t, scope, ctx, "sd.m(5)"); got != "ok" {
		t.Errorf("sd.m(5) = %q; want ok", got)
	}
	if got := advEvalText(t, scope, ctx, "sd.m.length"); got != "2" {
		t.Errorf("sd.m.length = %q; want 2", got)
	}
	if got := advEvalCaught(t, iso, scope, ctx, "sd.m.call({}, 5)"); got != "Uncaught TypeError: Illegal invocation" {
		t.Errorf("wrong receiver = %q", got)
	}

	// Host-side call through the signature-checked method.
	hostObj, ok, err := derivedFn.NewInstance(scope)
	if err != nil || !ok {
		t.Fatalf("host construct: ok=%v err=%v", ok, err)
	}
	hostHash, err := hostObj.GetHash()
	if err != nil {
		t.Fatalf("GetHash host: %v", err)
	}
	expectedHash = hostHash
	methodV, ok, err := hostObj.GetByName(scope, ctx, "m")
	if err != nil || !ok {
		t.Fatalf("get m: ok=%v err=%v", ok, err)
	}
	methodFn, ok, err := gov8.AsFunction(methodV, ctx)
	if err != nil || !ok {
		t.Fatalf("AsFunction: ok=%v err=%v", ok, err)
	}
	five, _ := scope.Int32(5)
	if res, ok, err := methodFn.Call(scope, hostObj.Value, five); err != nil || !ok {
		t.Fatalf("host m call: ok=%v err=%v", ok, err)
	} else if txt, _ := res.ToString(ctx); txt != "ok" {
		t.Errorf("host m call result = %q; want ok", txt)
	}
}

// --- intrinsic data properties ---------------------------------------------------------------

func TestIntrinsicDataProperty(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	ot, err := iso.NewObjectTemplate(scope)
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
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	advSeed(t, scope, ctx, "io", obj.Value)

	if got := advEvalText(t, scope, ctx, "io.arr === Array.prototype"); got != "true" {
		t.Errorf("arr intrinsic = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "io.arr === io.ro"); got != "true" {
		t.Errorf("same intrinsic object = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.getOwnPropertyDescriptor(io, 'ro').writable"); got != "false" {
		t.Errorf("read-only attr = %q; want false", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.getOwnPropertyDescriptor(io, 'arr').writable"); got != "true" {
		t.Errorf("plain attr = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "io.iter[Symbol.iterator]() === io.iter"); got != "true" {
		t.Errorf("iterator intrinsic = %q; want true", got)
	}

	// Intrinsics also work on an instance template: every `new C()` gets
	// the context's real Array.prototype.
	ft, err := iso.NewFunctionTemplate(scope, noopCB, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	inst, err := ft.InstanceTemplate()
	if err != nil {
		t.Fatalf("InstanceTemplate: %v", err)
	}
	if err := inst.SetIntrinsicDataProperty("arr", gov8.IntrinsicArrayPrototype, gov8.AttrNone); err != nil {
		t.Fatalf("instance template intrinsic: %v", err)
	}
	ctor, err := ft.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	advSeed(t, scope, ctx, "C", ctor.Value)
	if got := advEvalText(t, scope, ctx, "new C().arr === Array.prototype"); got != "true" {
		t.Errorf("instance intrinsic = %q; want true", got)
	}
	if err := ot.SetIntrinsicDataProperty("bad", gov8.Intrinsic(99), gov8.AttrNone); err == nil {
		t.Error("out-of-range intrinsic must be rejected")
	}
}

// --- constructor behavior / prototype controls -------------------------------------------------

func TestConstructorBehaviorAndPrototypeControls(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	// ConstructorBehavior::Throw: no .prototype, `new` rejects.
	returnInt := func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		_ = rv.SetInt32(3)
	}
	conciseFT, err := iso.NewFunctionTemplate(scope, returnInt, &gov8.FunctionOptions{
		ConstructorBehavior: gov8.ConstructorBehaviorThrow,
	})
	if err != nil {
		t.Fatalf("NewFunctionTemplate concise: %v", err)
	}
	if err := conciseFT.SetClassName("Gov8Concise"); err != nil {
		t.Fatalf("SetClassName concise: %v", err)
	}
	conciseFn, err := conciseFT.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction concise: %v", err)
	}
	advSeed(t, scope, ctx, "Concise", conciseFn.Value)
	if got := advEvalText(t, scope, ctx, "typeof Concise.prototype"); got != "undefined" {
		t.Errorf("concise prototype = %q; want undefined", got)
	}
	if got := advEvalText(t, scope, ctx, "Concise()"); got != "3" {
		t.Errorf("concise call = %q; want 3", got)
	}
	if got := advEvalText(t, scope, ctx, "Concise.name"); got != "Gov8Concise" {
		t.Errorf("concise name = %q; want Gov8Concise", got)
	}
	if got := advEvalCaught(t, iso, scope, ctx, "new Concise()"); got != "Uncaught TypeError: Concise is not a constructor" {
		t.Errorf("concise new = %q", got)
	}

	// Default: full constructor with a writable prototype.
	plainFT, _ := iso.NewFunctionTemplate(scope, noopCB, nil)
	if err := plainFT.SetClassName("Gov8Plain"); err != nil {
		t.Fatalf("SetClassName plain: %v", err)
	}
	plainFn, _ := plainFT.GetFunction(scope, ctx)
	advSeed(t, scope, ctx, "Gov8Plain", plainFn.Value)
	if got := advEvalText(t, scope, ctx, "typeof Gov8Plain.prototype"); got != "object" {
		t.Errorf("plain prototype = %q; want object", got)
	}
	if got := advEvalText(t, scope, ctx, "Gov8Plain.prototype.constructor === Gov8Plain"); got != "true" {
		t.Errorf("plain constructor link = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.getOwnPropertyDescriptor(Gov8Plain, 'prototype').writable"); got != "true" {
		t.Errorf("plain writable = %q; want true", got)
	}

	// ReadOnlyPrototype: sloppy assignment silently fails.
	roFT, _ := iso.NewFunctionTemplate(scope, noopCB, nil)
	if err := roFT.SetClassName("Gov8RO"); err != nil {
		t.Fatalf("SetClassName ro: %v", err)
	}
	if err := roFT.ReadOnlyPrototype(); err != nil {
		t.Fatalf("ReadOnlyPrototype: %v", err)
	}
	roFn, _ := roFT.GetFunction(scope, ctx)
	advSeed(t, scope, ctx, "Gov8RO", roFn.Value)
	if got := advEvalText(t, scope, ctx, "(Gov8RO.prototype = {}, Gov8RO.prototype.constructor === Gov8RO)"); got != "true" {
		t.Errorf("ro assignment ignored = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.getOwnPropertyDescriptor(Gov8RO, 'prototype').writable"); got != "false" {
		t.Errorf("ro writable = %q; want false", got)
	}

	// RemovePrototype: retrofitted on a default template.
	removedFT, _ := iso.NewFunctionTemplate(scope, noopCB, nil)
	if err := removedFT.SetClassName("Gov8NoProto"); err != nil {
		t.Fatalf("SetClassName removed: %v", err)
	}
	if err := removedFT.RemovePrototype(); err != nil {
		t.Fatalf("RemovePrototype: %v", err)
	}
	removedFn, _ := removedFT.GetFunction(scope, ctx)
	advSeed(t, scope, ctx, "Gov8NoProto", removedFn.Value)
	if got := advEvalText(t, scope, ctx, "typeof Gov8NoProto.prototype"); got != "undefined" {
		t.Errorf("removed prototype = %q; want undefined", got)
	}
	if got := advEvalCaught(t, iso, scope, ctx, "new Gov8NoProto()"); got != "Uncaught TypeError: Gov8NoProto is not a constructor" {
		t.Errorf("removed new = %q", got)
	}
}

// --- template inheritance ------------------------------------------------------------------------

func TestInheritanceChain(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	baseFT, err := iso.NewFunctionTemplate(scope, noopCB, nil)
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
	markB, _ := scope.NewString("B")
	if err := baseProto.Set("baseMark", markB); err != nil {
		t.Fatalf("baseProto.Set: %v", err)
	}
	markS, _ := scope.NewString("s")
	if err := baseFT.Set("baseStatic", markS); err != nil {
		t.Fatalf("baseFT.Set static: %v", err)
	}

	derivedFT, err := iso.NewFunctionTemplate(scope, noopCB, nil)
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
	markD, _ := scope.NewString("D")
	if err := derivedProto.Set("derivedMark", markD); err != nil {
		t.Fatalf("derivedProto.Set: %v", err)
	}

	baseFn, _ := baseFT.GetFunction(scope, ctx)
	advSeed(t, scope, ctx, "Gov8Base", baseFn.Value)
	derivedFn, _ := derivedFT.GetFunction(scope, ctx)
	advSeed(t, scope, ctx, "Gov8Derived", derivedFn.Value)

	if got := advEvalText(t, scope, ctx, "Object.getPrototypeOf(Gov8Derived.prototype) === Gov8Base.prototype"); got != "true" {
		t.Errorf("proto chain = %q; want true", got)
	}
	if got := advEvalText(t, scope, ctx, "(new Gov8Derived() instanceof Gov8Derived) + '|' + (new Gov8Derived() instanceof Gov8Base)"); got != "true|true" {
		t.Errorf("instanceof = %q; want true|true", got)
	}
	if got := advEvalText(t, scope, ctx, "new Gov8Derived().baseMark + '|' + new Gov8Derived().derivedMark"); got != "B|D" {
		t.Errorf("marks = %q; want B|D", got)
	}
	// Template-level statics do NOT inherit.
	if got := advEvalText(t, scope, ctx, "Gov8Base.baseStatic + '|' + Gov8Derived.baseStatic"); got != "s|undefined" {
		t.Errorf("statics = %q; want s|undefined", got)
	}
	if got := advEvalText(t, scope, ctx, "new Gov8Derived().constructor === Gov8Derived"); got != "true" {
		t.Errorf("constructor identity = %q; want true", got)
	}
}

// --- accessor-shaped properties ---------------------------------------------------------------------

func TestAccessorPropertyShapes(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	var expectedHash uint32
	var setterLog string
	returnFive := func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		_ = rv.SetInt32(5)
	}
	setter := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		arg0, _ := args.Get(0)
		this, _ := args.This()
		hash, _ := this.GetHash()
		thisOK := expectedHash != 0 && hash == expectedHash
		setterLog = "set:args=" + itoa(args.Length()) + " arg0=" + mustText(t, cs, arg0) + " this_ok=" + b2s(thisOK)
	}
	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	getterFT, err := iso.NewFunctionTemplate(scope, returnFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate getter: %v", err)
	}
	setterFT, err := iso.NewFunctionTemplate(scope, setter, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate setter: %v", err)
	}
	if err := ot.SetAccessorProperty("acc", getterFT, setterFT, gov8.AttrNone); err != nil {
		t.Fatalf("SetAccessorProperty acc: %v", err)
	}
	hiddenFT, err := iso.NewFunctionTemplate(scope, returnFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate hidden: %v", err)
	}
	if err := ot.SetAccessorProperty("hidden", hiddenFT, nil, gov8.AttrDontEnum); err != nil {
		t.Fatalf("SetAccessorProperty hidden: %v", err)
	}
	if err := ot.SetAccessorProperty("both_nil", nil, nil, gov8.AttrNone); err == nil {
		t.Error("SetAccessorProperty with nil getter and setter must be rejected")
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	hash, err := obj.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}
	expectedHash = hash
	advSeed(t, scope, ctx, "ao", obj.Value)

	if got := advEvalText(t, scope, ctx, "typeof ao.acc"); got != "number" {
		t.Errorf("typeof ao.acc = %q; want number", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.getOwnPropertyDescriptor(ao, 'acc').get.call(ao)"); got != "5" {
		t.Errorf("getter call = %q; want 5", got)
	}
	if got := advEvalText(t, scope, ctx, "typeof Object.getOwnPropertyDescriptor(ao, 'acc').get"); got != "function" {
		t.Errorf("descriptor get = %q; want function", got)
	}
	if got := advEvalText(t, scope, ctx, "typeof Object.getOwnPropertyDescriptor(ao, 'acc').set"); got != "function" {
		t.Errorf("descriptor set = %q; want function", got)
	}
	if got := advEvalText(t, scope, ctx, "(ao.acc = 9)"); got != "9" {
		t.Errorf("setter assignment = %q; want 9", got)
	}
	if want := "set:args=1 arg0=9 this_ok=true"; setterLog != want {
		t.Errorf("setter log = %q; want %q", setterLog, want)
	}
	if got := advEvalText(t, scope, ctx, "Object.keys(ao).join(',')"); got != "acc" {
		t.Errorf("enumeration = %q; want acc (DONT_ENUM hides 'hidden')", got)
	}
}

// --- internal field boundaries -------------------------------------------------------------------------

func TestInternalFieldBoundaries(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	// Default template: zero internal fields.
	defaultOT, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if n, err := defaultOT.InternalFieldCount(); err != nil || n != 0 {
		t.Errorf("default count = %d err=%v; want 0", n, err)
	}
	if ok, err := defaultOT.SetInternalFieldCount(0); err != nil || !ok {
		t.Errorf("SetInternalFieldCount(0) = %v err=%v; want true", ok, err)
	}
	if n, _ := defaultOT.InternalFieldCount(); n != 0 {
		t.Errorf("count after zero = %d; want 0", n)
	}
	zeroInst, ok, err := defaultOT.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("zero instance: ok=%v err=%v", ok, err)
	}
	if n, _ := zeroInst.InternalFieldCount(); n != 0 {
		t.Errorf("zero instance count = %d; want 0", n)
	}
	if _, has, err := zeroInst.GetInternalField(0); err != nil || has {
		t.Errorf("zero instance GetInternalField(0): has=%v err=%v; want !has", has, err)
	}
	one, _ := scope.Int32(1)
	if ok, err := zeroInst.SetInternalField(0, one); err != nil || ok {
		t.Errorf("zero instance SetInternalField(0) = %v err=%v; want false", ok, err)
	}

	// The count is frozen by the FIRST instantiation: later template re-sets
	// affect neither existing nor future instances.
	growingOT, _ := iso.NewObjectTemplate(scope)
	if _, err := growingOT.SetInternalFieldCount(1); err != nil {
		t.Fatalf("SetInternalFieldCount 1: %v", err)
	}
	earlyInst, ok, _ := growingOT.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("early instance")
	}
	if _, err := growingOT.SetInternalFieldCount(3); err != nil {
		t.Fatalf("SetInternalFieldCount 3: %v", err)
	}
	lateInst, ok, _ := growingOT.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("late instance")
	}
	if n, _ := earlyInst.InternalFieldCount(); n != 1 {
		t.Errorf("early count = %d; want 1", n)
	}
	if n, _ := lateInst.InternalFieldCount(); n != 1 {
		t.Errorf("late count = %d; want 1", n)
	}
	// Impossible counts are rejected by the wrapper (crate-level rejection).
	if ok, err := growingOT.SetInternalFieldCount(int(^uint(0) >> 1)); err != nil || ok {
		t.Errorf("SetInternalFieldCount(max) = %v err=%v; want false", ok, err)
	}

	// Aligned pointers across the valid tag range (0 and 14), retargeting,
	// null pointers, and mixed aligned/Data usage — through integer HostRef
	// tokens, never raw Go pointers.
	tokenA, err := iso.HostRefAdd([]uint32{111})
	if err != nil {
		t.Fatalf("HostRefAdd a: %v", err)
	}
	tokenB, err := iso.HostRefAdd([]uint32{222})
	if err != nil {
		t.Fatalf("HostRefAdd b: %v", err)
	}
	tokenC, err := iso.HostRefAdd([]uint32{333})
	if err != nil {
		t.Fatalf("HostRefAdd c: %v", err)
	}
	alignedOT, _ := iso.NewObjectTemplate(scope)
	if _, err := alignedOT.SetInternalFieldCount(2); err != nil {
		t.Fatalf("aligned count: %v", err)
	}
	aligned, ok, _ := alignedOT.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("aligned instance")
	}
	if err := aligned.SetAlignedPointerInInternalField(0, tokenA, 0); err != nil {
		t.Fatalf("tag 0: %v", err)
	}
	if err := aligned.SetAlignedPointerInInternalField(1, tokenB, 14); err != nil {
		t.Fatalf("tag 14: %v", err)
	}
	if got, ok, err := aligned.GetAlignedPointerFromInternalField(0, 0); err != nil || !ok || got != tokenA {
		t.Errorf("tag 0 roundtrip = %x ok=%v err=%v", got, ok, err)
	}
	if got, ok, err := aligned.GetAlignedPointerFromInternalField(1, 14); err != nil || !ok || got != tokenB {
		t.Errorf("tag 14 roundtrip = %x ok=%v err=%v", got, ok, err)
	}
	if err := aligned.SetAlignedPointerInInternalField(0, tokenC, 5); err != nil {
		t.Fatalf("retag: %v", err)
	}
	if got, _, err := aligned.GetAlignedPointerFromInternalField(0, 5); err != nil || got != tokenC {
		t.Errorf("retarget roundtrip = %x err=%v", got, err)
	}
	// Out-of-range tags are rejected BEFORE the engine is touched (the raw
	// oracle characterizes a V8 fatal here; the Go wrapper prevalidates).
	if err := aligned.SetAlignedPointerInInternalField(0, tokenA, 99); err == nil {
		t.Error("tag 99 must be rejected by the wrapper")
	}
	if _, _, err := aligned.GetAlignedPointerFromInternalField(0, 99); err == nil {
		t.Error("tag 99 read must be rejected by the wrapper")
	}

	nullOT, _ := iso.NewObjectTemplate(scope)
	if _, err := nullOT.SetInternalFieldCount(1); err != nil {
		t.Fatalf("null count: %v", err)
	}
	nullInst, ok, _ := nullOT.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("null instance")
	}
	if err := nullInst.SetAlignedPointerInInternalField(0, 0, 3); err != nil {
		t.Fatalf("null pointer store: %v", err)
	}
	if got, ok, err := nullInst.GetAlignedPointerFromInternalField(0, 3); err != nil || !ok || got != 0 {
		t.Errorf("null roundtrip = %x ok=%v err=%v", got, ok, err)
	}

	mixedOT, _ := iso.NewObjectTemplate(scope)
	if _, err := mixedOT.SetInternalFieldCount(2); err != nil {
		t.Fatalf("mixed count: %v", err)
	}
	mixed, ok, _ := mixedOT.NewInstance(scope, ctx)
	if !ok {
		t.Fatalf("mixed instance")
	}
	if err := mixed.SetAlignedPointerInInternalField(0, tokenA, 7); err != nil {
		t.Fatalf("mixed aligned: %v", err)
	}
	fortyTwo, _ := scope.Int32(42)
	if ok, err := mixed.SetInternalField(1, fortyTwo); err != nil || !ok {
		t.Fatalf("mixed data store: ok=%v err=%v", ok, err)
	}
	if back, has, err := mixed.GetInternalField(1); err != nil || !has {
		t.Fatalf("mixed data read: has=%v err=%v", has, err)
	} else if n, err := back.IntegerValueRaw(); err != nil || n != 42 {
		t.Errorf("mixed data roundtrip = %d err=%v; want 42", n, err)
	}
	if got, _, err := mixed.GetAlignedPointerFromInternalField(0, 7); err != nil || got != tokenA {
		t.Errorf("mixed aligned roundtrip = %x err=%v", got, err)
	}

	// The host regains ownership and verifies the pointees survived.
	for _, tc := range []struct {
		token uintptr
		want  uint32
	}{{tokenA, 111}, {tokenB, 222}, {tokenC, 333}} {
		v, ok := iso.HostRefGet(tc.token)
		if !ok {
			t.Fatalf("HostRefGet %x missing", tc.token)
		}
		box, isSlice := v.([]uint32)
		if !isSlice || len(box) != 1 || box[0] != tc.want {
			t.Errorf("HostRefGet %x = %v; want [%d]", tc.token, v, tc.want)
		}
	}
}

// --- security tokens -------------------------------------------------------------------------------------

func TestSecurityTokenContexts(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	ctx1, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext 1: %v", err)
	}
	defer func() { _ = ctx1.Close() }()
	ctx2, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext 2: %v", err)
	}
	defer func() { _ = ctx2.Close() }()
	ctx3, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext 3: %v", err)
	}
	defer func() { _ = ctx3.Close() }()

	// The DEFAULT security token of a context is the context's own global
	// object, so every fresh context carries a distinct token.
	token1, err := ctx1.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken 1: %v", err)
	}
	token2, err := ctx2.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken 2: %v", err)
	}
	token3, err := ctx3.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken 3: %v", err)
	}
	if same, _ := token1.StrictEquals(token2); same {
		t.Error("default tokens of fresh contexts must differ (1 vs 2)")
	}
	if same, _ := token2.StrictEquals(token3); same {
		t.Error("default tokens of fresh contexts must differ (2 vs 3)")
	}

	// A plain object created in ctx1; plain objects carry no access-check
	// info, so once bridged they are readable from any context.
	shared := createSharedObject(t, iso, scope, ctx1)

	// Bridging into the receiving context's own global always works.
	global2, err := ctx2.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject 2: %v", err)
	}
	if ok, err := global2.SetByName(scope, ctx2, "o1", shared); err != nil || !ok {
		t.Fatalf("bridge into ctx2: ok=%v err=%v", ok, err)
	}
	if got := advEvalCaught(t, iso, scope, ctx2, "o1.mark"); got != "m1" {
		t.Errorf("read from ctx2 = %q; want m1", got)
	}

	// Setting a property on ANOTHER context's global proxy while tokens
	// differ is denied: the set throws "no access". The context ARGUMENT of
	// the property operation is the accessing context (the oracle's entered
	// context, ctx2 here); the target global proxy belongs to ctx3.
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	global3, err := ctx3.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject 3: %v", err)
	}
	ok3, setErr := global3.SetByName(scope, ctx2, "o1", shared)
	caught, _ := tc.HasCaught()
	msg := ""
	if caught {
		msg, _ = tc.MessageText(scope, ctx3)
	}
	_ = tc.Close()
	if ok3 {
		t.Error("cross-token set must be denied")
	}
	_ = setErr
	if !caught {
		t.Error("cross-token set must be caught by the TryCatch")
	}
	if msg != "Uncaught TypeError: no access" {
		t.Errorf("denial message = %q; want Uncaught TypeError: no access", msg)
	}

	// Sharing a token re-enables cross-context global access.
	if err := ctx3.SetSecurityToken(scope, token2); err != nil {
		t.Fatalf("SetSecurityToken: %v", err)
	}
	sharedToken, err := ctx3.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken after share: %v", err)
	}
	if same, _ := sharedToken.StrictEquals(token2); !same {
		t.Error("token must now equal token2")
	}
	if ok, err := global3.SetByName(scope, ctx2, "o1", shared); err != nil || !ok {
		t.Fatalf("bridge into ctx3 after share: ok=%v err=%v", ok, err)
	}
	if got := advEvalCaught(t, iso, scope, ctx3, "o1.mark"); got != "m1" {
		t.Errorf("read from ctx3 after share = %q; want m1", got)
	}

	// Resetting restores the context's own global object as the token.
	if err := ctx3.UseDefaultSecurityToken(); err != nil {
		t.Fatalf("UseDefaultSecurityToken: %v", err)
	}
	resetToken, _ := ctx3.GetSecurityToken(scope)
	if same, _ := resetToken.StrictEquals(token2); same {
		t.Error("reset token must differ from token2")
	}
	tc2, _ := iso.NewTryCatch()
	global1, err := ctx1.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject 1: %v", err)
	}
	ok1, _ := global1.SetByName(scope, ctx2, "o1", shared)
	caught2, _ := tc2.HasCaught()
	_ = tc2.Close()
	if ok1 {
		t.Error("set on ctx1's global must be denied after reset")
	}
	if !caught2 {
		t.Error("denied set must be caught again")
	}
}

// createSharedObject creates a plain host object with `mark: "m1"` in ctx1
// and stores it on ctx1's global as `o1` (the Go analog of bridging a
// v8::Global across the check; the engine object identity is what matters).
func createSharedObject(t *testing.T, iso *gov8.Isolate, scope *gov8.Scope, ctx *gov8.Context) gov8.Value {
	t.Helper()
	script, err := ctx.Compile(scope, "globalThis.o1 = ({mark: 'm1'}); o1", nil)
	if err != nil {
		t.Fatalf("Compile shared: %v", err)
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("Run shared: %v", err)
	}
	return v
}

// --- call-as-function handler -------------------------------------------------------------------------------

func TestCallAsFunctionHandler(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	var expectedHash uint32
	var log []string
	handler := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		arg0, _ := args.Get(0)
		this, _ := args.This()
		hash, _ := this.GetHash()
		thisOK := expectedHash != 0 && hash == expectedHash
		data, _ := args.Data()
		n, _, _ := cs.IntegerValue(arg0)
		_ = rv.SetInt32(int32(n * 2))
		log = append(log, "caf:construct="+b2s(args.IsConstructCall())+
			" data="+mustText(t, cs, data)+
			" this_ok="+b2s(thisOK)+
			" arg0="+mustText(t, cs, arg0))
	}
	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	data, _ := scope.NewString("caf-data")
	if err := ot.SetCallAsFunctionHandler(handler, data); err != nil {
		t.Fatalf("SetCallAsFunctionHandler: %v", err)
	}
	if err := ot.SetCallAsFunctionHandler(nil, gov8.Value{}); err == nil {
		t.Error("nil handler must be rejected")
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	hash, _ := obj.GetHash()
	expectedHash = hash
	advSeed(t, scope, ctx, "co", obj.Value)

	if got := advEvalText(t, scope, ctx, "co(4)"); got != "8" {
		t.Errorf("co(4) = %q; want 8", got)
	}
	if got := advEvalText(t, scope, ctx, "typeof co"); got != "function" {
		t.Errorf("typeof co = %q; want function", got)
	}
	if got := advEvalText(t, scope, ctx, "Object.prototype.toString.call(co)"); got != "[object Object]" {
		t.Errorf("toString tag = %q", got)
	}
	// Construct calls dispatch to the SAME handler and even a primitive
	// return value is delivered as the construct result.
	if got := advEvalCaught(t, iso, scope, ctx, "new co(1)"); got != "2" {
		t.Errorf("new co(1) = %q; want 2", got)
	}
	want := "caf:construct=false data=caf-data this_ok=true arg0=4;caf:construct=true data=caf-data this_ok=true arg0=1"
	if got := strings.Join(log, ";"); got != want {
		t.Errorf("callback log =\n%q\nwant\n%q", got, want)
	}
}

// --- immutable proto ------------------------------------------------------------------------------------------

func TestImmutableProto(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetImmutableProto(); err != nil {
		t.Fatalf("SetImmutableProto: %v", err)
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	advSeed(t, scope, ctx, "ip", obj.Value)

	const wantMsg = "Uncaught TypeError: Immutable prototype object '#<Object>' cannot have their prototype set"
	if got := advEvalCaught(t, iso, scope, ctx, "Object.setPrototypeOf(ip, {x: 1})"); got != wantMsg {
		t.Errorf("setPrototypeOf = %q; want %q", got, wantMsg)
	}
	if got := advEvalText(t, scope, ctx, "ip.x"); got != "undefined" {
		t.Errorf("ip.x = %q; want undefined", got)
	}
	if got := advEvalCaught(t, iso, scope, ctx, "(ip.__proto__ = {y: 2})"); got != wantMsg {
		t.Errorf("__proto__ assignment = %q; want %q", got, wantMsg)
	}
	if got := advEvalText(t, scope, ctx, "ip.toString === Object.prototype.toString"); got != "true" {
		t.Errorf("default proto intact = %q; want true", got)
	}
}

// --- GetHash identity -----------------------------------------------------------------------------------------

func TestGetHashIdentity(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)
	defer func() { _ = ctx.Close() }()

	script, err := ctx.Compile(scope, "globalThis.a = {}; globalThis.b = {}", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := script.Run(scope, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = script.Close()
	a, ok := evalOK(t, ctx, scope, "a")
	if !ok {
		t.Fatalf("eval a")
	}
	b, ok := evalOK(t, ctx, scope, "b")
	if !ok {
		t.Fatalf("eval b")
	}
	// The same engine object yields the same hash from a fresh local.
	a2, _ := evalOK(t, ctx, scope, "a")
	ha, err := a.GetHash()
	if err != nil {
		t.Fatalf("GetHash a: %v", err)
	}
	ha2, err := a2.GetHash()
	if err != nil {
		t.Fatalf("GetHash a2: %v", err)
	}
	hb, err := b.GetHash()
	if err != nil {
		t.Fatalf("GetHash b: %v", err)
	}
	if ha == 0 || ha != ha2 {
		t.Errorf("hash stability: %d vs %d", ha, ha2)
	}
	if ha == hb {
		t.Errorf("distinct objects must have distinct hashes (both %d)", ha)
	}
}

// --- lifecycle / misuse boundaries --------------------------------------------------------------------------------

func TestTemplateAdvancedScopeLifecycle(t *testing.T) {
	iso := advNewIso(t)
	defer func() { _ = iso.Close() }()
	scope, ctx := advNewCtx(t, iso)

	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("scope.Close: %v", err)
	}

	// Values and templates from a closed scope refuse to operate.
	if _, err := obj.GetHash(); err == nil {
		t.Error("GetHash must refuse a closed scope")
	}
	if err := ot.SetImmutableProto(); err == nil {
		t.Error("SetImmutableProto must refuse a closed scope")
	}
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{}); err == nil {
		t.Error("SetNamedPropertyHandler on a closed scope must fail")
	}
	if _, err := ctx.GetSecurityToken(scope); err == nil {
		t.Error("GetSecurityToken must refuse a closed scope")
	}

	// Signature creation from a foreign template isolate is rejected.
	iso2 := advNewIso(t)
	defer func() { _ = iso2.Close() }()
	scope2, ctx2 := advNewCtx(t, iso2)
	defer func() { _ = ctx2.Close() }()
	ft2, err := iso2.NewFunctionTemplate(scope2, noopCB, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate iso2: %v", err)
	}
	if _, err := iso.NewSignature(scope, ft2); err == nil {
		t.Error("NewSignature must reject a foreign template")
	}
}

// TestInterceptorPanicAbortsProcess characterizes the panic boundary on the
// interceptor dispatch path: a panic inside a property handler must
// terminate the process fail-fast, exactly like the function-callback path
// (the observable equivalent of the oracle's extern "C" unwinding rule).
func TestInterceptorPanicAbortsProcess(t *testing.T) {
	if os.Getenv("GOV8_HOST_TEST_INTERCEPTOR_PANIC_CHILD") == "1" {
		interceptorPanicChild(t)
		return // never reached: the child aborts
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=TestInterceptorPanicAbortsProcess", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOV8_HOST_TEST_INTERCEPTOR_PANIC_CHILD=1")
	out, err := cmd.CombinedOutput()
	stdoutStderr := string(out)

	if !strings.Contains(stdoutStderr, "marker:interceptor-entered") {
		t.Errorf("the interceptor must be entered; output:\n%s", stdoutStderr)
	}
	if !strings.Contains(stdoutStderr, "interceptor-panic-marker") {
		t.Errorf("the panic message must be printed; output:\n%s", stdoutStderr)
	}
	if strings.Contains(stdoutStderr, "marker:after-read") {
		t.Errorf("host code after the read must never run; output:\n%s", stdoutStderr)
	}
	if err == nil {
		t.Fatalf("the process must not exit cleanly; output:\n%s", stdoutStderr)
	}
	var ee *exec.ExitError
	if !asExitError(err, &ee) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if got := ee.ExitCode(); got != 3221226505 {
		t.Errorf("exit code = %d; want 3221226505 (0xC0000409); output:\n%s", got, stdoutStderr)
	}
}

func interceptorPanicChild(t *testing.T) {
	iso := advNewIso(t)
	scope, ctx := advNewCtx(t, iso)
	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Getter: func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			println("marker:interceptor-entered")
			panic("interceptor-panic-marker")
		},
	}); err != nil {
		t.Fatalf("SetNamedPropertyHandler: %v", err)
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	advSeed(t, scope, ctx, "o", obj.Value)
	script, err := ctx.Compile(scope, "o.k", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, _ = script.Run(scope, nil)
	println("marker:after-read") // never reached
}

// TestIndexedEnumeratorStringElementsIsFatal characterizes the oracle's
// out-of-process negative probe: an indexed enumerator returning
// non-uint32-convertible (String) elements aborts inside V8's
// `Object::ToUint32` CHECK when the keys are consumed (e.g. Object.keys) in
// the full-family configuration. The wrapper deliberately does not
// prevalidate element types — matching the pinned crate, which reaches V8
// verbatim — so this must be observed in a child process.
func TestIndexedEnumeratorStringElementsIsFatal(t *testing.T) {
	if os.Getenv("GOV8_HOST_TEST_ENUMERATOR_FATAL_CHILD") == "1" {
		enumeratorFatalChild(t)
		return // never reached: the child aborts through V8's fatal path
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=TestIndexedEnumeratorStringElementsIsFatal", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOV8_HOST_TEST_ENUMERATOR_FATAL_CHILD=1")
	out, err := cmd.CombinedOutput()
	stdoutStderr := string(out)

	if err == nil {
		t.Fatalf("the process must not survive the bad enumerator; output:\n%s", stdoutStderr)
	}
	if !strings.Contains(stdoutStderr, "Check failed") && !strings.Contains(stdoutStderr, "Fatal") {
		t.Errorf("expected a V8 fatal on the output; output:\n%s", stdoutStderr)
	}
	if strings.Contains(stdoutStderr, "marker:enumerator-fatal-survived") {
		t.Errorf("the probe must not survive; output:\n%s", stdoutStderr)
	}
}

func enumeratorFatalChild(t *testing.T) {
	iso := advNewIso(t)
	scope, ctx := advNewCtx(t, iso)
	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	noIntercept := func(cs *gov8.CallbackScope, index uint32, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		return gov8.InterceptedNo
	}
	if err := ot.SetIndexedPropertyHandler(gov8.IndexedPropertyHandlerConfig{
		Getter: noIntercept,
		Setter: func(cs *gov8.CallbackScope, index uint32, value gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			return gov8.InterceptedNo
		},
		Query: noIntercept,
		Deleter: func(cs *gov8.CallbackScope, index uint32, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			return gov8.InterceptedNo
		},
		Enumerator: func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
			// Strings are not uint32-convertible through Object::ToUint32.
			n9, _ := cs.NewString("9")
			n4, _ := cs.NewString("4")
			arr, aerr := cs.NewArrayWithElements([]gov8.Value{n9, n4})
			if aerr != nil {
				return
			}
			_ = rv.Set(arr)
		},
	}); err != nil {
		t.Fatalf("SetIndexedPropertyHandler: %v", err)
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	advSeed(t, scope, ctx, "io", obj.Value)
	// Consuming the keys drives the fatal ToUint32 CHECK.
	_ = advEvalText(t, scope, ctx, "Object.keys(io)")
	println("marker:enumerator-fatal-survived")
}

func noopCB(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {}

func mustText(t *testing.T, cs *gov8.CallbackScope, v gov8.Value) string {
	t.Helper()
	txt, err := cs.ToString(v)
	if err != nil {
		t.Fatalf("ToString: %v", err)
	}
	return txt
}

func undefinedV(t *testing.T, scope *gov8.Scope) gov8.Value {
	t.Helper()
	v, err := scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	return v
}
