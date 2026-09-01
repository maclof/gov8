//go:build windows && amd64

package main

import (
	"fmt"
	"math"
	"testing"

	gov8 "gov8"
)

// check is one oracle check: it builds both the fixed expectation (want) and
// the Go observation (got); wantGot folds them into an obs.
type check func(t *testing.T) obs

// checkR is a check that needs a fresh runtime; it keeps signatures uniform.
type checkR func(t *testing.T, r *runtime) (jsonValue, jsonValue)

func runCheck(id string, fn checkR) check {
	return func(t *testing.T) obs {
		r := newRuntime(t)
		defer r.close(t)
		want, got := fn(t, r)
		return wantGot(id, want, got)
	}
}

// --- platform ----------------------------------------------------------------

func checkVersionConstants(t *testing.T) obs {
	v, err := gov8.EngineVersion()
	if err != nil {
		t.Fatalf("EngineVersion: %v", err)
	}
	return wantGot("platform/version_constants",
		obj(kv("major", i(15)), kv("minor", i(2)), kv("build", i(124)), kv("patch", i(1))),
		obj(kv("major", i(int64(v.Major))), kv("minor", i(int64(v.Minor))),
			kv("build", i(int64(v.Build))), kv("patch", i(int64(v.Patch)))))
}

func checkVersionString(t *testing.T) obs {
	vs, err := gov8.VersionString()
	if err != nil {
		t.Fatalf("VersionString: %v", err)
	}
	rv, err := gov8.RuntimeVersionString()
	if err != nil {
		t.Fatalf("RuntimeVersionString: %v", err)
	}
	return wantGot("platform/version_string",
		obj(kv("version_string", s("15.2.124.1-rusty")), kv("get_version", s("15.2.124.1-rusty"))),
		obj(kv("version_string", s(vs)), kv("get_version", s(rv))))
}

func checkCurrentPlatformPresent(t *testing.T) obs {
	return pass("platform/current_platform_present", b(gov8.PlatformPresent()))
}

// --- isolate / context ---------------------------------------------------------

func checkContextScriptRoundtrip(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	v, ok := r.eval(t, "40 + 2")
	txt, isNumber := "", false
	var numberValue jsonValue = jsonNull{}
	if ok {
		txt = text(t, r, v)
		isNumber, _ = v.IsNumber()
		if n, nok, _ := v.NumberValue(r.ctx); nok {
			numberValue = f(n)
		}
	}
	return obj(kv("result_string", s("42")), kv("is_number", b(true)), kv("number_value", f(42))),
		obj(kv("result_string", s(txt)), kv("is_number", b(isNumber)), kv("number_value", numberValue))
}

func checkSequentialIsolates(t *testing.T) obs {
	var got []jsonValue
	for n := 1; n <= 3; n++ {
		r := newRuntime(t)
		txt, _ := r.evalText(t, fmt.Sprintf("'iso-' + %d", n))
		got = append(got, s(txt))
		r.close(t)
	}
	return wantGot("isolate/sequential_isolates",
		arr(s("iso-1"), s("iso-2"), s("iso-3")), arr(got...))
}

func checkGlobalObjectNativeAccess(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	if _, ok := r.eval(t, "globalThis.gv = 7;"); !ok {
		t.Fatal("seed eval failed")
	}
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	var readBack jsonValue = jsonNull{}
	if gv, ok, gerr := global.GetByName(r.scope, r.ctx, "gv"); gerr == nil && ok {
		if n, iok, _ := gv.IntegerValue(r.ctx); iok {
			readBack = i(n)
		}
	}
	nv, err := r.scope.Number(42)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	setResult := false
	if setOK, serr := global.SetByName(r.scope, r.ctx, "nv", nv); serr == nil {
		setResult = setOK
	}
	scriptRead, _ := r.evalText(t, "nv")
	return obj(kv("read_back_int", i(7)), kv("set_result", b(true)), kv("script_read", s("42"))),
		obj(kv("read_back_int", readBack), kv("set_result", b(setResult)), kv("script_read", s(scriptRead)))
}

func checkContextReportsDefaultMicrotaskQueue(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	p, err := r.iso.GetMicrotasksPolicy()
	if err != nil {
		t.Fatalf("GetMicrotasksPolicy: %v", err)
	}
	policy := "Explicit"
	if p == gov8.PolicyAuto {
		policy = "Auto"
	}
	raw, err := r.ctx.GetMicrotaskQueue()
	if err != nil {
		t.Fatalf("GetMicrotaskQueue: %v", err)
	}
	return obj(kv("default_policy", s("Auto")), kv("context_has_microtask_queue", b(true))),
		obj(kv("default_policy", s(policy)), kv("context_has_microtask_queue", b(raw != 0)))
}

// --- values -------------------------------------------------------------------

func checkUndefined(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	u, err := r.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	isU, _ := u.IsUndefined()
	isNU, _ := u.IsNullOrUndefined()
	return obj(kv("is_undefined", b(true)), kv("is_null_or_undefined", b(true)), kv("to_string", s("undefined"))),
		obj(kv("is_undefined", b(isU)), kv("is_null_or_undefined", b(isNU)), kv("to_string", s(text(t, r, u))))
}

func checkNull(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	n, err := r.scope.Null()
	if err != nil {
		t.Fatalf("Null: %v", err)
	}
	isN, _ := n.IsNull()
	isNU, _ := n.IsNullOrUndefined()
	return obj(kv("is_null", b(true)), kv("is_null_or_undefined", b(true)), kv("to_string", s("null"))),
		obj(kv("is_null", b(isN)), kv("is_null_or_undefined", b(isNU)), kv("to_string", s(text(t, r, n))))
}

func checkBooleans(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	truthy, err := r.scope.Boolean(true)
	if err != nil {
		t.Fatalf("Boolean: %v", err)
	}
	falsy, err := r.scope.Boolean(false)
	if err != nil {
		t.Fatalf("Boolean: %v", err)
	}
	tIs, _ := truthy.IsBoolean()
	tVal, _ := truthy.BooleanValue()
	fIs, _ := falsy.IsBoolean()
	fVal, _ := falsy.BooleanValue()
	return obj(kv("true_is_boolean", b(true)), kv("true_value", b(true)), kv("true_to_string", s("true")),
			kv("false_is_boolean", b(true)), kv("false_value", b(false)), kv("false_to_string", s("false"))),
		obj(kv("true_is_boolean", b(tIs)), kv("true_value", b(tVal)), kv("true_to_string", s(text(t, r, truthy))),
			kv("false_is_boolean", b(fIs)), kv("false_value", b(fVal)), kv("false_to_string", s(text(t, r, falsy))))
}

func checkIntegers(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	neg, err := r.scope.Int32(-42)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	uns, err := r.scope.Uint32(math.MaxUint32)
	if err != nil {
		t.Fatalf("Uint32: %v", err)
	}
	negVal, _ := neg.IntegerValueRaw()
	negInt32, _ := neg.IsInt32()
	negNum, _ := neg.IsNumber()
	unsVal, _ := uns.IntegerValueRaw()
	unsU32, _ := uns.IsUint32()
	unsI32, _ := uns.IsInt32()
	return obj(kv("negative", obj(kv("value", i(-42)), kv("is_int32", b(true)), kv("is_number", b(true)), kv("to_string", s("-42")))),
			kv("unsigned_max", obj(kv("value", i(4294967295)), kv("is_uint32", b(true)), kv("is_int32", b(false)), kv("to_string", s("4294967295"))))),
		obj(kv("negative", obj(kv("value", i(negVal)), kv("is_int32", b(negInt32)), kv("is_number", b(negNum)), kv("to_string", s(text(t, r, neg))))),
			kv("unsigned_max", obj(kv("value", i(unsVal)), kv("is_uint32", b(unsU32)), kv("is_int32", b(unsI32)), kv("to_string", s(text(t, r, uns))))))
}

func checkNumberF64(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	mk := func(sample float64, wantText string) (jsonValue, jsonValue) {
		n, err := r.scope.Number(sample)
		if err != nil {
			t.Fatalf("Number: %v", err)
		}
		val, _ := n.NumberValueRaw()
		isNum, _ := n.IsNumber()
		return obj(kv("value", f(sample)), kv("is_number", b(true)), kv("to_string", s(wantText))),
			obj(kv("value", f(val)), kv("is_number", b(isNum)), kv("to_string", s(text(t, r, n))))
	}
	w0, g0 := mk(2.5, "2.5")
	w1, g1 := mk(-1234.5, "-1234.5")
	w2, g2 := mk(0.5, "0.5")
	return arr(w0, w1, w2), arr(g0, g1, g2)
}

func checkNumberSpecial(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	nan, err := r.scope.Number(math.NaN())
	if err != nil {
		t.Fatalf("Number(NaN): %v", err)
	}
	inf, err := r.scope.Number(math.Inf(1))
	if err != nil {
		t.Fatalf("Number(Inf): %v", err)
	}
	ninf, err := r.scope.Number(math.Inf(-1))
	if err != nil {
		t.Fatalf("Number(-Inf): %v", err)
	}
	nanV, _ := nan.NumberValueRaw()
	infV, _ := inf.NumberValueRaw()
	ninfV, _ := ninf.NumberValueRaw()
	return obj(kv("nan", obj(kv("is_nan", b(true)), kv("to_string", s("NaN")))),
			kv("infinity", obj(kv("is_infinite", b(true)), kv("to_string", s("Infinity")))),
			kv("neg_infinity", obj(kv("is_infinite", b(true)), kv("to_string", s("-Infinity"))))),
		obj(kv("nan", obj(kv("is_nan", b(math.IsNaN(nanV))), kv("to_string", s(text(t, r, nan))))),
			kv("infinity", obj(kv("is_infinite", b(math.IsInf(infV, 0))), kv("to_string", s(text(t, r, inf))))),
			kv("neg_infinity", obj(kv("is_infinite", b(math.IsInf(ninfV, 0))), kv("to_string", s(text(t, r, ninf))))))
}

func checkStringRoundtrip(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	entry := func(src string, wantU16, wantU8 int64) (jsonValue, jsonValue) {
		str, err := r.scope.NewString(src)
		if err != nil {
			t.Fatalf("NewString: %v", err)
		}
		l16, _ := str.Length()
		l8, _ := str.Utf8Length()
		rt := text(t, r, str) == src
		return obj(kv("length_utf16", i(wantU16)), kv("utf8_length", i(wantU8)), kv("roundtrip", b(true))),
			obj(kv("length_utf16", i(int64(l16))), kv("utf8_length", i(int64(l8))), kv("roundtrip", b(rt)))
	}
	wA, gA := entry("hello oracle", 12, 12)
	wU, gU := entry("héllo 🦀 gov8", 13, 16)
	empty, err := r.scope.NewString("")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	l16e, _ := empty.Length()
	return obj(kv("ascii", wA), kv("unicode", wU), kv("empty", obj(kv("length_utf16", i(0)), kv("roundtrip", b(true))))),
		obj(kv("ascii", gA), kv("unicode", gU), kv("empty", obj(kv("length_utf16", i(int64(l16e))), kv("roundtrip", b(text(t, r, empty) == "")))))
}

func checkValueToStringConversions(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	type sample struct {
		name, wantText string
		mk             func() (gov8.Value, error)
	}
	samples := []sample{
		{"number_1234567_125", "1234567.125", func() (gov8.Value, error) { return r.scope.Number(1234567.125) }},
		{"integer_negative", "-987654", func() (gov8.Value, error) { return r.scope.Int32(-987654) }},
		{"boolean_false", "false", func() (gov8.Value, error) { return r.scope.Boolean(false) }},
		{"bigint_2p53", "9007199254740992", func() (gov8.Value, error) { return r.scope.BigIntFromInt64(1 << 53) }},
		{"string_abc", "abc", func() (gov8.Value, error) { return r.scope.NewString("abc") }},
		{"undefined", "undefined", r.scope.Undefined},
		{"null", "null", r.scope.Null},
	}
	var wantP, gotP jsonObj
	for _, sm := range samples {
		v, err := sm.mk()
		if err != nil {
			t.Fatalf("%s: %v", sm.name, err)
		}
		wantP = append(wantP, kv(sm.name, obj(kv("expected", s(sm.wantText)), kv("actual", s(sm.wantText)))))
		gotP = append(gotP, kv(sm.name, obj(kv("expected", s(sm.wantText)), kv("actual", s(text(t, r, v))))))
	}
	return wantP, gotP
}

func checkBigIntRoundtrip(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	fromI64, err := r.scope.BigIntFromInt64(-1234567890123456789)
	if err != nil {
		t.Fatalf("BigIntFromInt64: %v", err)
	}
	i64Back, lossless1, _ := fromI64.BigIntInt64()
	fromU64, err := r.scope.BigIntFromUint64(^uint64(0))
	if err != nil {
		t.Fatalf("BigIntFromUint64: %v", err)
	}
	i64OfMax, lossless2, _ := fromU64.BigIntInt64()
	return obj(kv("from_i64", obj(kv("value", i(-1234567890123456789)), kv("lossless", b(true)), kv("to_string", s("-1234567890123456789")))),
			kv("from_u64_max", obj(kv("i64_value", i(-1)), kv("lossless", b(false)), kv("to_string", s("18446744073709551615"))))),
		obj(kv("from_i64", obj(kv("value", i(i64Back)), kv("lossless", b(lossless1)), kv("to_string", s(text(t, r, fromI64))))),
			kv("from_u64_max", obj(kv("i64_value", i(i64OfMax)), kv("lossless", b(lossless2)), kv("to_string", s(text(t, r, fromU64))))))
}

func checkScriptNumberFormatting(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	const source = "[ String(0.1), String(1 / 3), String(1e21), String(1e-7)," +
		" String(-0), String(2 ** 53), String(100), String(0.5) ].join('|')"
	got, _ := r.evalText(t, source)
	return s("0.1|0.3333333333333333|1e+21|1e-7|0|9007199254740992|100|0.5"), s(got)
}

// --- scripts -------------------------------------------------------------------

func checkArithmetic(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	got, ok := r.evalText(t, "40 + 2")
	return obj(kv("value", s("42")), kv("succeeded", b(true))),
		obj(kv("value", s(got)), kv("succeeded", b(ok)))
}

func checkStringConcat(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	got, ok := r.evalText(t, "'go' + 'v8' + ' ' + 1")
	return obj(kv("value", s("gov8 1")), kv("succeeded", b(true))),
		obj(kv("value", s(got)), kv("succeeded", b(ok)))
}

func checkValueTypes(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	if _, ok := r.eval(t, "globalThis.__x = { b: 2, a: 1 };"); !ok {
		t.Fatal("eval obj failed")
	}
	objV, okObj := r.eval(t, "__x")
	objJSON, _ := r.evalText(t, "JSON.stringify(__x)")

	if _, ok := r.eval(t, "globalThis.__x = [1, 2, 3];"); !ok {
		t.Fatal("eval arr failed")
	}
	arrV, okArr := r.eval(t, "__x")
	arrJSON, _ := r.evalText(t, "JSON.stringify(__x)")

	if _, ok := r.eval(t, "globalThis.__f = function named(x) { return x; };"); !ok {
		t.Fatal("eval fn failed")
	}
	fnV, okFn := r.eval(t, "__f")

	objIs, _ := objV.IsObject()
	arrIsArray, _ := arrV.IsArray()
	arrIsObject, _ := arrV.IsObject()
	fnIsFn, _ := fnV.IsFunction()
	fnIsObj, _ := fnV.IsObject()
	return obj(kv("object", obj(kv("is_object", b(true)), kv("json", s("{\"b\":2,\"a\":1}")))),
			kv("array", obj(kv("is_array", b(true)), kv("is_object", b(true)), kv("json", s("[1,2,3]")))),
			kv("function", obj(kv("is_function", b(true)), kv("is_object", b(true))))),
		obj(kv("object", obj(kv("is_object", b(okObj && objIs)), kv("json", s(objJSON)))),
			kv("array", obj(kv("is_array", b(okArr && arrIsArray)), kv("is_object", b(okArr && arrIsObject)), kv("json", s(arrJSON)))),
			kv("function", obj(kv("is_function", b(okFn && fnIsFn)), kv("is_object", b(okFn && fnIsObj)))))
}

func checkScriptIDs(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	s1, err := r.ctx.Compile(r.scope, "1 + 1", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() { _ = s1.Close() }()
	id1, err := s1.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	s2, err := r.ctx.Compile(r.scope, "1 + 1", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() { _ = s2.Close() }()
	id2, err := s2.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	s3, err := r.ctx.Compile(r.scope, "2 + 2", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() { _ = s3.Close() }()
	id3, err := s3.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	return obj(kv("same_source_same_id", b(true)), kv("different_source_different_id", b(true)), kv("increasing", b(true))),
		obj(kv("same_source_same_id", b(id1 == id2)), kv("different_source_different_id", b(id1 != id3)), kv("increasing", b(id3 > id1)))
}

func checkEmptySource(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	v, ok := r.eval(t, "")
	isU := false
	if ok {
		isU, _ = v.IsUndefined()
	}
	return obj(kv("compiles", b(true)), kv("result_is_undefined", b(true))),
		obj(kv("compiles", b(ok)), kv("result_is_undefined", b(isU)))
}

// --- exceptions ------------------------------------------------------------------

type observedException struct {
	compileOK, runOK, hasCaught, canContinue, exceptionIsString bool
	message, exceptionText                                      string
}

func (o observedException) json() jsonValue {
	return obj(kv("compile_ok", b(o.compileOK)), kv("run_ok", b(o.runOK)),
		kv("has_caught", b(o.hasCaught)), kv("can_continue", b(o.canContinue)),
		kv("message", s(o.message)), kv("exception_text", s(o.exceptionText)),
		kv("exception_is_string", b(o.exceptionIsString)))
}

func observe(t *testing.T, r *runtime, source string) observedException {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	var o observedException
	script, cerr := r.ctx.Compile(r.scope, source, tc)
	o.compileOK = cerr == nil
	if script != nil {
		defer func() { _ = script.Close() }()
		_, rerr := script.Run(r.scope, tc)
		o.runOK = rerr == nil
	}
	o.hasCaught, _ = tc.HasCaught()
	o.canContinue, _ = tc.CanContinue()
	o.message, _ = tc.MessageText(r.scope, r.ctx)
	o.exceptionText, _ = tc.ExceptionText(r.scope, r.ctx)
	o.exceptionIsString, _ = tc.ExceptionIsString()
	return o
}

func checkException(id, source string, want observedException) check {
	return func(t *testing.T) obs {
		r := newRuntime(t)
		defer r.close(t)
		got := observe(t, r, source)
		return wantGot(id, want.json(), got.json())
	}
}

func checkSyntaxErrorMessagePosition(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	const source = "const a = 1;\nconst const b = 2;\nconst c = 3;\n"
	compileOK := false
	if script, cerr := r.ctx.Compile(r.scope, source, tc); cerr == nil {
		compileOK = true
		_ = script.Close()
	}
	startPos, _ := tc.StartPosition(r.scope)
	line, lineOK, _ := tc.LineNumber(r.scope, r.ctx)
	startCol, _ := tc.StartColumn(r.scope)
	msg, _ := tc.MessageText(r.scope, r.ctx)

	var lineVal jsonValue = jsonNull{}
	if lineOK {
		lineVal = i(int64(line))
	}
	return wantGot("exceptions/syntax_error_message_position",
		obj(kv("compile_ok", b(false)), kv("start_position", i(19)), kv("line_number", i(2)),
			kv("start_column", i(6)), kv("message", s("Uncaught SyntaxError: Unexpected token 'const'"))),
		obj(kv("compile_ok", b(compileOK)), kv("start_position", i(startPos)), kv("line_number", lineVal),
			kv("start_column", i(startCol)), kv("message", s(msg))))
}

func checkTryCatchResetAllowsContinue(t *testing.T, r *runtime) (jsonValue, jsonValue) {
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	script, cerr := r.ctx.Compile(r.scope, "throw 'reset-me';", tc)
	if cerr == nil && script != nil {
		_, _ = script.Run(r.scope, tc)
		_ = script.Close()
	}
	caughtBefore, _ := tc.HasCaught()
	if err := tc.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	caughtAfter, _ := tc.HasCaught()
	exceptionAfter, _ := tc.ExceptionText(r.scope, r.ctx)
	next, _ := r.evalText(t, "40 + 2")
	return obj(kv("caught_before_reset", b(true)), kv("caught_after_reset", b(false)),
			kv("exception_after_reset", s("")), kv("next_script_value", s("42"))),
		obj(kv("caught_before_reset", b(caughtBefore)), kv("caught_after_reset", b(caughtAfter)),
			kv("exception_after_reset", s(exceptionAfter)), kv("next_script_value", s(next)))
}

// --- microtasks --------------------------------------------------------------------

const microtaskScript = "Promise.resolve().then(() => __order.push('p1'));" +
	"Promise.resolve().then(() => __order.push('p2')).then(() => __order.push('p2b'));" +
	"new Promise(function (resolve) { resolve(); }).then(() => __order.push('p3'));" +
	"Promise.resolve().then(() => { __order.push('p4'); " +
	"Promise.resolve().then(() => __order.push('p4b')); });"

func seedOrder(t *testing.T, r *runtime) {
	t.Helper()
	if _, ok := r.eval(t, "globalThis.__order = [];"); !ok {
		t.Fatal("seed failed")
	}
}

func orderOf(t *testing.T, r *runtime) string {
	t.Helper()
	got, _ := r.evalText(t, "__order.join(',')")
	return got
}

func checkExplicitPolicyOrdering(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)
	if err := r.iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	seedOrder(t, r)
	if _, ok := r.eval(t, microtaskScript); !ok {
		t.Fatal("eval microtask script failed")
	}
	afterRun := orderOf(t, r)
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	afterCheckpoint := orderOf(t, r)
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("second checkpoint: %v", err)
	}
	afterSecond := orderOf(t, r)
	const want = "p1,p2,p3,p4,p2b,p4b"
	return wantGot("microtasks/explicit_policy_ordering",
		obj(kv("after_run", s("")), kv("after_checkpoint", s(want)), kv("after_second_checkpoint", s(want))),
		obj(kv("after_run", s(afterRun)), kv("after_checkpoint", s(afterCheckpoint)),
			kv("after_second_checkpoint", s(afterSecond))))
}

func checkAutoPolicyOrdering(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)
	p, _ := r.iso.GetMicrotasksPolicy()
	policy := "Explicit"
	if p == gov8.PolicyAuto {
		policy = "Auto"
	}
	seedOrder(t, r)
	if _, ok := r.eval(t, microtaskScript); !ok {
		t.Fatal("eval microtask script failed")
	}
	afterRun := orderOf(t, r)
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	afterCheckpoint := orderOf(t, r)
	const want = "p1,p2,p3,p4,p2b,p4b"
	return wantGot("microtasks/auto_policy_ordering",
		obj(kv("default_policy", s("Auto")), kv("after_run", s(want)), kv("after_checkpoint", s(want))),
		obj(kv("default_policy", s(policy)), kv("after_run", s(afterRun)), kv("after_checkpoint", s(afterCheckpoint))))
}

func checkNativeMicrotaskQueue(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	mq, err := r.iso.NewMicrotaskQueue(gov8.PolicyExplicit)
	if err != nil {
		t.Fatalf("NewMicrotaskQueue: %v", err)
	}
	defer func() { _ = mq.Close() }()
	if err := r.ctx.SetMicrotaskQueue(mq); err != nil {
		t.Fatalf("SetMicrotaskQueue: %v", err)
	}
	wantRaw, err := mq.Raw()
	if err != nil {
		t.Fatalf("Raw: %v", err)
	}
	gotRaw, err := r.ctx.GetMicrotaskQueue()
	if err != nil {
		t.Fatalf("GetMicrotaskQueue: %v", err)
	}
	attached := gotRaw == wantRaw

	if _, ok := r.eval(t, "globalThis.__order = [];"+
		"Promise.resolve().then(() => __order.push('n1'));"+
		"Promise.resolve().then(() => __order.push('n2'));"); !ok {
		t.Fatal("eval failed")
	}
	afterRun := orderOf(t, r)
	if err := mq.PerformCheckpoint(r.ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	afterCheckpoint := orderOf(t, r)

	fn, ok := r.eval(t, "() => __order.push('native')")
	if !ok {
		t.Fatal("eval fn failed")
	}
	if err := mq.Enqueue(r.ctx, fn); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := mq.PerformCheckpoint(r.ctx); err != nil {
		t.Fatalf("checkpoint 2: %v", err)
	}
	afterNative := orderOf(t, r)

	return wantGot("microtasks/native_microtask_queue",
		obj(kv("queue_attached", b(true)), kv("after_run", s("")),
			kv("after_checkpoint", s("n1,n2")), kv("after_native_enqueue", s("n1,n2,native"))),
		obj(kv("queue_attached", b(attached)), kv("after_run", s(afterRun)),
			kv("after_checkpoint", s(afterCheckpoint)), kv("after_native_enqueue", s(afterNative))))
}

// --- platform shutdown (must stay last; consumes process state) ----------------------

func checkDisposeReturnsTrue(t *testing.T) obs {
	disposed, err := gov8.Dispose()
	if err != nil {
		t.Fatalf("Dispose: %v", err)
	}
	return wantGot("platform/dispose_returns_true", b(true), b(disposed))
}

func checkDisposePlatformNoPanic(t *testing.T) obs {
	if err := gov8.DisposePlatform(); err != nil {
		t.Fatalf("DisposePlatform: %v", err)
	}
	return pass("platform/dispose_platform_no_panic", b(true))
}

// allChecks is the fixed oracle order (rust-oracle/src/checks/mod.rs).
func allChecks() []check {
	return []check{
		// platform: version identity and init state
		checkVersionConstants,
		checkVersionString,
		checkCurrentPlatformPresent,
		// isolates and contexts
		runCheck("isolate/context_script_roundtrip", checkContextScriptRoundtrip),
		checkSequentialIsolates,
		runCheck("isolate/global_object_native_access", checkGlobalObjectNativeAccess),
		runCheck("isolate/context_reports_default_microtask_queue", checkContextReportsDefaultMicrotaskQueue),
		// primitive values and conversions
		runCheck("values/undefined", checkUndefined),
		runCheck("values/null", checkNull),
		runCheck("values/booleans", checkBooleans),
		runCheck("values/integers", checkIntegers),
		runCheck("values/number_f64", checkNumberF64),
		runCheck("values/number_special", checkNumberSpecial),
		runCheck("values/string_roundtrip", checkStringRoundtrip),
		runCheck("values/value_to_string_conversions", checkValueToStringConversions),
		runCheck("values/bigint_roundtrip", checkBigIntRoundtrip),
		runCheck("values/script_number_formatting", checkScriptNumberFormatting),
		// script compile/run success
		runCheck("script/arithmetic", checkArithmetic),
		runCheck("script/string_concat", checkStringConcat),
		runCheck("script/value_types", checkValueTypes),
		runCheck("script/script_ids_distinct_and_increasing", checkScriptIDs),
		runCheck("script/empty_source", checkEmptySource),
		// exceptions
		checkException("exceptions/syntax_error_compile_fails", "1 +", observedException{
			message:       "Uncaught SyntaxError: Unexpected end of input",
			exceptionText: "SyntaxError: Unexpected end of input",
			hasCaught:     true, canContinue: true,
		}),
		checkSyntaxErrorMessagePosition,
		checkException("exceptions/runtime_reference_error", "missing_thing();", observedException{
			compileOK:     true,
			message:       "Uncaught ReferenceError: missing_thing is not defined",
			exceptionText: "ReferenceError: missing_thing is not defined",
			hasCaught:     true, canContinue: true,
		}),
		checkException("exceptions/runtime_type_error", "null.f();", observedException{
			compileOK:     true,
			message:       "Uncaught TypeError: Cannot read properties of null (reading 'f')",
			exceptionText: "TypeError: Cannot read properties of null (reading 'f')",
			hasCaught:     true, canContinue: true,
		}),
		checkException("exceptions/throw_string", "throw 'boom';", observedException{
			compileOK: true, hasCaught: true, canContinue: true,
			message: "Uncaught boom", exceptionText: "boom", exceptionIsString: true,
		}),
		checkException("exceptions/throw_error_object", "throw new Error('oops');", observedException{
			compileOK: true, hasCaught: true, canContinue: true,
			message: "Uncaught Error: oops", exceptionText: "Error: oops",
		}),
		runCheck("exceptions/trycatch_reset_allows_continue", checkTryCatchResetAllowsContinue),
		// microtasks
		checkExplicitPolicyOrdering,
		checkAutoPolicyOrdering,
		checkNativeMicrotaskQueue,
		// platform shutdown (must stay last)
		checkDisposeReturnsTrue,
		checkDisposePlatformNoPanic,
	}
}
