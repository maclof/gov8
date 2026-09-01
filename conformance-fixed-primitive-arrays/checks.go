//go:build windows && amd64

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	gov8 "gov8"
)

type tester interface {
	Helper()
	Fatal(args ...any)
	Fatalf(string, ...any)
}

type outcome struct {
	id    string
	value string
}

func jstr(s string) string { b, _ := json.Marshal(s); return string(b) }
func jbool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
func jobj(fields ...string) string { return "{" + strings.Join(fields, ",") + "}" }
func jarr(values ...string) string { return "[" + strings.Join(values, ",") + "]" }
func kv(k, v string) string        { return jstr(k) + ":" + v }

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t tester) *runtime {
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

func (r *runtime) close(t tester) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustValue(t tester, makeValue func() (gov8.Value, error)) gov8.Value {
	t.Helper()
	v, err := makeValue()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func primitiveKind(t tester, v gov8.Value) string {
	t.Helper()
	checks := []struct {
		name string
		fn   func() (bool, error)
	}{
		{"undefined", v.IsUndefined}, {"null", v.IsNull}, {"boolean", v.IsBoolean},
		{"string", v.IsString}, {"symbol", v.IsSymbol}, {"bigint", v.IsBigInt},
		{"number", v.IsNumber},
	}
	for _, check := range checks {
		ok, err := check.fn()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return check.name
		}
	}
	return "unknown"
}

func primitiveText(t tester, r *runtime, v gov8.Value) string {
	t.Helper()
	if symbol, _ := v.IsSymbol(); symbol {
		sym, err := gov8.AsSymbol(v)
		if err != nil {
			t.Fatal(err)
		}
		description, err := sym.Description(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		text, err := description.StringValue()
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	text, err := v.ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func checkEmptyDefaults(t tester) outcome {
	r := newRuntime(t)
	defer r.close(t)
	empty, err := gov8.NewPrimitiveArray(r.scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := gov8.NewPrimitiveArray(r.scope, 3)
	if err != nil {
		t.Fatal(err)
	}
	emptyLen, _ := empty.Length()
	defaultLen, _ := defaults.Length()
	kinds := make([]string, defaultLen)
	for i := range kinds {
		v, ok, err := defaults.Get(r.scope, i)
		if err != nil || !ok {
			t.Fatalf("Get(%d) = %v, %v", i, ok, err)
		}
		kinds[i] = jstr(primitiveKind(t, v))
	}
	return outcome{"fixed-primitive-arrays/primitive_empty_and_defaults", jobj(
		kv("empty_length", fmt.Sprint(emptyLen)), kv("default_length", fmt.Sprint(defaultLen)),
		kv("default_kinds", jarr(kinds...)),
	)}
}

func checkSupportedKinds(t tester) outcome {
	r := newRuntime(t)
	defer r.close(t)
	array, err := gov8.NewPrimitiveArray(r.scope, 8)
	if err != nil {
		t.Fatal(err)
	}
	desc := mustValue(t, func() (gov8.Value, error) { return r.scope.NewString("token") })
	symbol, err := r.scope.NewSymbol(desc)
	if err != nil {
		t.Fatal(err)
	}
	values := []gov8.Value{
		mustValue(t, r.scope.Undefined), mustValue(t, r.scope.Null),
		mustValue(t, func() (gov8.Value, error) { return r.scope.Boolean(true) }),
		mustValue(t, func() (gov8.Value, error) { return r.scope.NewString("hello") }), symbol.Value,
		mustValue(t, func() (gov8.Value, error) { return r.scope.Number(2.5) }),
		mustValue(t, func() (gov8.Value, error) { return r.scope.Int32(-7) }),
		mustValue(t, func() (gov8.Value, error) { return r.scope.BigIntFromInt64(9_007_199_254_740_993) }),
	}
	for i, value := range values {
		if ok, err := array.Set(r.scope, i, value); err != nil || !ok {
			t.Fatalf("Set(%d) = %v, %v", i, ok, err)
		}
	}
	observed := make([]string, len(values))
	for i := range observed {
		value, _, _ := array.Get(r.scope, i)
		observed[i] = jobj(kv("kind", jstr(primitiveKind(t, value))), kv("text", jstr(primitiveText(t, r, value))))
	}
	roundtrip, _, _ := array.Get(r.scope, 4)
	identity, err := roundtrip.StrictEquals(symbol.Value)
	if err != nil {
		t.Fatal(err)
	}
	length, _ := array.Length()
	return outcome{"fixed-primitive-arrays/primitive_supported_kinds", jobj(
		kv("length", fmt.Sprint(length)), kv("values", jarr(observed...)), kv("symbol_identity", jbool(identity)),
	)}
}

func checkOverwriteContext(t tester) outcome {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx1, _ := iso.NewContext()
	scope1, _ := iso.NewScope()
	array, _ := gov8.NewPrimitiveArray(scope1, 2)
	first := mustValue(t, func() (gov8.Value, error) { return scope1.NewString("from-first-context") })
	_, _ = array.Set(scope1, 0, first)
	_, _ = array.Set(scope1, 1, mustValue(t, func() (gov8.Value, error) { return scope1.Int32(1) }))
	_, _ = array.Set(scope1, 1, mustValue(t, func() (gov8.Value, error) { return scope1.Int32(2) }))
	held, err := gov8.NewPrimitiveArrayGlobal(scope1, array)
	if err != nil {
		t.Fatal(err)
	}
	_ = scope1.Close()
	_ = ctx1.Close()
	ctx2, _ := iso.NewContext()
	scope2, _ := iso.NewScope()
	reopened, err := held.ToLocal(scope2)
	if err != nil {
		t.Fatal(err)
	}
	v0, _, _ := reopened.Get(scope2, 0)
	v1, _, _ := reopened.Get(scope2, 1)
	firstText, _ := v0.ToString(ctx2)
	secondText, _ := v1.ToString(ctx2)
	length, _ := reopened.Length()
	_ = held.Close()
	_ = scope2.Close()
	_ = ctx2.Close()
	_ = iso.Close()
	return outcome{"fixed-primitive-arrays/primitive_overwrite_and_context_independence", jobj(
		kv("length", fmt.Sprint(length)), kv("first_slot", jstr(firstText)), kv("overwritten_slot", jstr(secondText)),
	)}
}

func checkLengthConversion(t tester) outcome {
	r := newRuntime(t)
	defer r.close(t)
	zero := int(uint64(1) << 32)
	one := zero + 1
	zeroArray, err := gov8.NewPrimitiveArray(r.scope, zero)
	if err != nil {
		t.Fatal(err)
	}
	oneArray, err := gov8.NewPrimitiveArray(r.scope, one)
	if err != nil {
		t.Fatal(err)
	}
	zeroLength, err := zeroArray.Length()
	if err != nil {
		t.Fatal(err)
	}
	oneLength, err := oneArray.Length()
	if err != nil {
		t.Fatal(err)
	}
	value, ok, err := oneArray.Get(r.scope, 0)
	if err != nil || !ok {
		t.Fatalf("wrapped slot = %v, %v", ok, err)
	}
	return outcome{"fixed-primitive-arrays/primitive_length_conversion", jobj(
		kv("requested_zero", fmt.Sprint(zero)), kv("observed_zero", fmt.Sprint(zeroLength)),
		kv("requested_one", fmt.Sprint(one)), kv("observed_one", fmt.Sprint(oneLength)),
		kv("wrapped_slot_default", jstr(primitiveKind(t, value))),
	)}
}

func checkFixedEmpty(t tester) outcome {
	r := newRuntime(t)
	defer r.close(t)
	module, err := r.ctx.CompileModule(r.scope, "export const answer = 42;", "arrays.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = module.Close() }()
	fixed, err := module.ModuleRequests(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	isFixed, _ := fixed.IsFixedArray()
	length, _ := fixed.Length()
	_, zeroSome, zeroErr := fixed.Get(r.scope, 0)
	_, maxSome, maxErr := fixed.Get(r.scope, math.MaxInt)
	if zeroErr != nil || maxErr != nil {
		t.Fatal(zeroErr, maxErr)
	}
	return outcome{"fixed-primitive-arrays/fixed_empty_and_safe_bounds", jobj(
		kv("is_fixed_array", jbool(isFixed)), kv("length", fmt.Sprint(length)),
		kv("get_zero_none", jbool(!zeroSome)), kv("get_usize_max_none", jbool(!maxSome)),
	)}
}

func checkFixedKinds(t tester) outcome {
	r := newRuntime(t)
	defer r.close(t)
	module, err := r.ctx.CompileModule(r.scope,
		"import value from './data.json' with { kind: 'fixture' }; export default value;", "arrays.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = module.Close() }()
	requests, _ := module.ModuleRequests(r.scope)
	requestCount, _ := requests.Length()
	requestData, _, _ := requests.Get(r.scope, 0)
	isRequest, _ := requestData.IsModuleRequest()
	isPrimitive, _ := requestData.IsPrimitive()
	request, _, _ := requestData.ModuleRequest()
	attrs, _ := request.ImportAttributes(r.scope)
	attrsFixed, _ := attrs.IsFixedArray()
	attrLen, _ := attrs.Length()
	kinds := make([]string, attrLen)
	values := make([]string, attrLen)
	for i := 0; i < attrLen; i++ {
		data, _, _ := attrs.Get(r.scope, i)
		value, _, _ := data.Value()
		isString, _ := value.IsString()
		isNumber, _ := value.IsNumber()
		kind := "other"
		if isString {
			kind = "string"
		} else if isNumber {
			kind = "number"
		}
		kinds[i] = jstr(kind)
		text, _ := value.ToString(r.ctx)
		values[i] = jstr(text)
	}
	_, requestAtCount, _ := requests.Get(r.scope, requestCount)
	_, attrAtCount, _ := attrs.Get(r.scope, attrLen)
	return outcome{"fixed-primitive-arrays/fixed_data_kinds", jobj(
		kv("request_count", fmt.Sprint(requestCount)), kv("request_is_module_request", jbool(isRequest)),
		kv("request_is_primitive", jbool(isPrimitive)), kv("attributes_is_fixed_array", jbool(attrsFixed)),
		kv("attribute_length", fmt.Sprint(attrLen)), kv("attribute_kinds", jarr(kinds...)),
		kv("attribute_values", jarr(values...)), kv("request_at_count_none", jbool(!requestAtCount)),
		kv("attribute_at_count_none", jbool(!attrAtCount)),
	)}
}

var checks = []func(tester) outcome{
	checkEmptyDefaults, checkSupportedKinds, checkOverwriteContext,
	checkLengthConversion, checkFixedEmpty, checkFixedKinds,
}
