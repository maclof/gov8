//go:build windows && amd64

package main

// The 27 runtime-values checks of the pinned Rust oracle
// (rust-oracle/src/bin/conformance-runtime-values.rs), re-implemented on the
// Go binding in the same registry order. Every value is produced by live
// engine observation; the comparison target is the pinned fixture.

import (
	"math"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// --- harness (mirrors the runner's eval_text/caught_message/value_text/
// json_stringify/stringify_text/is_null_result helpers) ----------------------

// evalText is the oracle's eval_text: ToString of the completion value
// ("" on failure).
func evalText(t tester, r *runtime, tc *gov8.TryCatch, source string) string {
	t.Helper()
	v, err := r.eval(t, tc, source)
	if err != nil {
		return ""
	}
	s, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return s
}

// caughtMessage mirrors the caught_message! macro: (has_caught, message).
func caughtMessage(t tester, r *runtime, tc *gov8.TryCatch) (bool, string) {
	t.Helper()
	caught, err := tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	message := ""
	if caught {
		message, err = tc.MessageText(r.scope, r.ctx)
		if err != nil {
			t.Fatalf("MessageText: %v", err)
		}
	}
	return caught, message
}

// valueText is the oracle's value_text: ToString of an arbitrary value
// ("" when conversion fails). Uses the TryCatch-routed ToString so a
// throwing conversion stays observable in tc (symbols).
func valueText(t tester, r *runtime, tc *gov8.TryCatch, v gov8.Value) string {
	t.Helper()
	s, err := v.ToStringTC(r.scope, r.ctx, tc)
	if err != nil {
		return ""
	}
	return s
}

// stringifyText is the oracle's json_stringify rendered as text (None keeps
// undefined distinguishable from "": flattened to "" here by callers that
// mirror unwrap_or_default).
func jsonOrNone(t tester, r *runtime, tc *gov8.TryCatch, v gov8.Value) (gov8.Value, bool) {
	t.Helper()
	out, err := gov8.JSONStringify(r.ctx, r.scope, v, tc)
	if err != nil {
		return gov8.Value{}, false
	}
	return out, true
}

// stringifyText is json_stringify + unwrap_or_default.
func stringifyText(t tester, r *runtime, tc *gov8.TryCatch, v gov8.Value) string {
	t.Helper()
	out, ok := jsonOrNone(t, r, tc, v)
	if !ok {
		return ""
	}
	s, err := out.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return s
}

// evalValue compiles and runs source, failing the check on error (the
// oracle's `.unwrap()` on successful evals).
func evalValue(t tester, r *runtime, tc *gov8.TryCatch, source string) gov8.Value {
	t.Helper()
	v, err := r.eval(t, tc, source)
	if err != nil {
		t.Fatalf("eval %q: %v", source, err)
	}
	return v
}

// evalValueOK compiles and runs source, reporting success like Option.
func evalValueOK(t tester, r *runtime, tc *gov8.TryCatch, source string) (gov8.Value, bool) {
	t.Helper()
	v, err := r.eval(t, tc, source)
	return v, err == nil
}

// str creates a JS string, failing the check on error.
func str(t tester, r *runtime, s string) gov8.Value {
	t.Helper()
	v, err := r.scope.NewString(s)
	if err != nil {
		t.Fatalf("NewString(%q): %v", s, err)
	}
	return v
}

// int32 creates a JS integer, failing the check on error.
func int32Val(t tester, r *runtime, v int32) gov8.Value {
	t.Helper()
	out, err := r.scope.Int32(v)
	if err != nil {
		t.Fatalf("Int32(%d): %v", v, err)
	}
	return out
}

// num creates a JS number, failing the check on error.
func num(t tester, r *runtime, v float64) gov8.Value {
	t.Helper()
	out, err := r.scope.Number(v)
	if err != nil {
		t.Fatalf("Number(%v): %v", v, err)
	}
	return out
}

// publish sets a global property.
func publish(t tester, r *runtime, name string, v gov8.Value) {
	t.Helper()
	g, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if _, err := g.SetByName(r.scope, r.ctx, name, v); err != nil {
		t.Fatalf("set global %s: %v", name, err)
	}
}

// getObject casts a value to an object view, failing the check on error
// (the oracle's try_cast::<v8::Object>().unwrap()).
func getObject(t tester, v gov8.Value) *gov8.Object {
	t.Helper()
	o, err := gov8.AsObject(v)
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	return o
}

// getByName reads a named property, failing the check when the getter fails
// (the oracle's `.unwrap()` on Some results).
func getByName(t tester, o *gov8.Object, r *runtime, name string) gov8.Value {
	t.Helper()
	v, ok, err := o.GetByName(r.scope, r.ctx, name)
	if err != nil || !ok {
		t.Fatalf("get %s: ok=%v err=%v", name, ok, err)
	}
	return v
}

// int32Of converts a value through Int32Value with a default of 0 on
// failure (the oracle's and_then(...).unwrap_or_default()).
func int32Of(t tester, r *runtime, v gov8.Value) int32 {
	t.Helper()
	n, ok, err := v.Int32Value(r.ctx)
	if err != nil || !ok {
		return 0
	}
	return n
}

// textOf is value_text for values that always convert (no TryCatch needed).
func textOf(t tester, r *runtime, v gov8.Value) string {
	t.Helper()
	s, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return s
}

// --- 1. Date --------------------------------------------------------------------

func checkDateConstructionAndValueOf(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	epoch, err := r.scope.NewDate(r.ctx, 0)
	if err != nil {
		t.Fatalf("NewDate(0): %v", err)
	}
	isDate, err := epoch.IsDate()
	if err != nil {
		t.Fatalf("IsDate: %v", err)
	}
	isObject, err := epoch.IsObject()
	if err != nil {
		t.Fatalf("IsObject: %v", err)
	}
	nativeValueOf, err := epoch.ValueOf()
	if err != nil {
		t.Fatalf("ValueOf: %v", err)
	}

	publish(t, r, "d", epoch.Value)
	jsGetTime := evalText(t, r, nil, "d.getTime()")
	jsToISO := evalText(t, r, nil, "d.toISOString()")
	evalValue(t, r, nil, "d.setUTCSeconds(30)")
	nativeAfterMutation := false
	if vo, err := epoch.ValueOf(); err == nil {
		nativeAfterMutation = vo == 30000
	}
	jsToISOAfter := evalText(t, r, nil, "d.toISOString()")

	later, err := r.scope.NewDate(r.ctx, 1.5e12)
	if err != nil {
		t.Fatalf("NewDate(1.5e12): %v", err)
	}
	laterExact := false
	if vo, err := later.ValueOf(); err == nil {
		laterExact = vo == 1.5e12
	}

	invalid, err := r.scope.NewDate(r.ctx, math.NaN())
	if err != nil {
		t.Fatalf("NewDate(NaN): %v", err)
	}
	invalidValueOf, err := invalid.ValueOf()
	if err != nil {
		t.Fatalf("invalid ValueOf: %v", err)
	}
	publish(t, r, "di", invalid.Value)
	jsInvalidIsNaN := evalText(t, r, nil, "Number.isNaN(di.getTime())")

	jsCreated := evalValue(t, r, nil, "new Date(86400500)")
	jsCreatedIsDate, err := jsCreated.IsDate()
	if err != nil {
		t.Fatalf("js IsDate: %v", err)
	}
	jsCreatedNativeValue := false
	if d, err := gov8.AsDate(jsCreated); err == nil {
		if vo, err := d.ValueOf(); err == nil {
			jsCreatedNativeValue = vo == 86400500
		}
	}

	return wantGot("runtime-values/date_construction_and_value_of",
		jobj(
			kv("is_date", jbool(true)),
			kv("is_object", jbool(true)),
			kv("native_value_of_is_zero", jbool(true)),
			kv("js_get_time", jstr("0")),
			kv("js_to_iso", jstr("1970-01-01T00:00:00.000Z")),
			kv("native_after_mutation", jbool(true)),
			kv("js_to_iso_after", jstr("1970-01-01T00:00:30.000Z")),
			kv("later_exact", jbool(true)),
			kv("invalid_value_of_is_nan", jbool(true)),
			kv("js_invalid_is_nan", jstr("true")),
			kv("js_created_is_date", jbool(true)),
			kv("js_created_native_value", jbool(true)),
		),
		jobj(
			kv("is_date", jbool(isDate)),
			kv("is_object", jbool(isObject)),
			kv("native_value_of_is_zero", jbool(nativeValueOf == 0)),
			kv("js_get_time", jstr(jsGetTime)),
			kv("js_to_iso", jstr(jsToISO)),
			kv("native_after_mutation", jbool(nativeAfterMutation)),
			kv("js_to_iso_after", jstr(jsToISOAfter)),
			kv("later_exact", jbool(laterExact)),
			kv("invalid_value_of_is_nan", jbool(math.IsNaN(invalidValueOf))),
			kv("js_invalid_is_nan", jstr(jsInvalidIsNaN)),
			kv("js_created_is_date", jbool(jsCreatedIsDate)),
			kv("js_created_native_value", jbool(jsCreatedNativeValue)),
		))
}

func checkDateInvalidTimeValueError(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	tc := r.tc(t)
	defer func() { _ = tc.Close() }()
	jsSees := evalText(t, r, tc, "new Date(NaN).toISOString()")
	caught, message := caughtMessage(t, r, tc)

	return wantGot("runtime-values/date_invalid_time_value_error",
		jobj(
			kv("result", jstr("")),
			kv("caught", jbool(true)),
			kv("message", jstr("Uncaught RangeError: Invalid time value")),
		),
		jobj(
			kv("result", jstr(jsSees)),
			kv("caught", jbool(caught)),
			kv("message", jstr(message)),
		))
}

// --- 2. RegExp ------------------------------------------------------------------

func checkRegExpNewFlagsAndSource(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	pattern := str(t, r, "a(b)c")
	re, err := r.scope.NewRegExp(r.ctx, pattern, gov8.RegExpGlobal|gov8.RegExpIgnoreCase, nil)
	if err != nil {
		t.Fatalf("NewRegExp: %v", err)
	}
	isRegExp, err := re.IsRegExp()
	if err != nil {
		t.Fatalf("IsRegExp: %v", err)
	}
	sourceV, err := re.GetSource(r.scope)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	source := textOf(t, r, sourceV)
	publish(t, r, "re", re.Value)

	jsFlags := evalText(t, r, nil, "re.flags")
	jsGlobal := evalText(t, r, nil, "re.global")
	jsIgnoreCase := evalText(t, r, nil, "re.ignoreCase")
	jsSticky := evalText(t, r, nil, "re.sticky")
	jsMultiline := evalText(t, r, nil, "re.multiline")
	jsTypeof := evalText(t, r, nil, "typeof re")

	return wantGot("runtime-values/regexp_new_flags_and_source",
		jobj(
			kv("is_reg_exp", jbool(true)),
			kv("source", jstr("a(b)c")),
			kv("js_flags", jstr("gi")),
			kv("js_global", jstr("true")),
			kv("js_ignore_case", jstr("true")),
			kv("js_sticky", jstr("false")),
			kv("js_multiline", jstr("false")),
			kv("js_typeof", jstr("object")),
		),
		jobj(
			kv("is_reg_exp", jbool(isRegExp)),
			kv("source", jstr(source)),
			kv("js_flags", jstr(jsFlags)),
			kv("js_global", jstr(jsGlobal)),
			kv("js_ignore_case", jstr(jsIgnoreCase)),
			kv("js_sticky", jstr(jsSticky)),
			kv("js_multiline", jstr(jsMultiline)),
			kv("js_typeof", jstr(jsTypeof)),
		))
}

// describeExec is the oracle's describe_exec: the JSON view of the match
// object, its index, and its input. A thrown exec (err) maps to None
// ({"match": null}); a miss is a Some wrapping the null value.
func describeExec(t *testing.T, r *runtime, m *gov8.Object, err error) jsonValue {
	if err != nil {
		return jobj(kv("match", jnull()))
	}
	match := stringifyText(t, r, nil, m.Value)
	indexV := getByName(t, m, r, "index")
	index := int32Of(t, r, indexV)
	inputV := getByName(t, m, r, "input")
	input := textOf(t, r, inputV)
	return jobj(
		kv("match", jstr(match)),
		kv("index", jint(int64(index))),
		kv("input", jstr(input)),
	)
}

// execIsNull mirrors is_null_result: false for a thrown exec (None), true
// only when the result wraps the null value.
func execIsNull(m *gov8.Object, err error) (isNull, threw bool) {
	if err != nil {
		return false, true
	}
	null, err := m.IsNull()
	if err != nil {
		return false, true
	}
	return null, false
}

func checkRegExpExecAndLastIndex(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	pattern := str(t, r, "a(b)c")
	re, err := r.scope.NewRegExp(r.ctx, pattern, gov8.RegExpGlobal, nil)
	if err != nil {
		t.Fatalf("NewRegExp: %v", err)
	}
	subject := str(t, r, "xxabcXXabc")

	firstM, firstErr := re.Exec(r.scope, r.ctx, subject)
	first := describeExec(t, r, firstM, firstErr)
	publish(t, r, "g", re.Value)
	lastIndexAfterFirst := evalText(t, r, nil, "g.lastIndex")

	secondM, secondErr := re.Exec(r.scope, r.ctx, subject)
	second := describeExec(t, r, secondM, secondErr)
	lastIndexAfterSecond := evalText(t, r, nil, "g.lastIndex")

	thirdM, thirdErr := re.Exec(r.scope, r.ctx, subject)
	thirdIsNull, _ := execIsNull(thirdM, thirdErr)
	lastIndexAfterFail := evalText(t, r, nil, "g.lastIndex")

	// Sticky: exec is anchored at lastIndex (a failed match resets it).
	spattern := str(t, r, "x")
	sticky, err := r.scope.NewRegExp(r.ctx, spattern, gov8.RegExpSticky, nil)
	if err != nil {
		t.Fatalf("NewRegExp sticky: %v", err)
	}
	ssubject := str(t, r, "axxa")
	s0M, s0Err := sticky.Exec(r.scope, r.ctx, ssubject)
	stickyAt0IsNull, _ := execIsNull(s0M, s0Err)
	publish(t, r, "s", sticky.Value)
	evalText(t, r, nil, "s.lastIndex = 2")
	stickyMatch, stickyErr := sticky.Exec(r.scope, r.ctx, ssubject)
	stickyAt2IsNull, stickyThrew := execIsNull(stickyMatch, stickyErr)
	stickyAt2IsMatch := !stickyAt2IsNull && !stickyThrew
	stickyIndex := int32(0)
	if stickyMatch != nil && !stickyThrew {
		stickyIndex = int32Of(t, r, getByName(t, stickyMatch, r, "index"))
	}
	seM, seErr := sticky.Exec(r.scope, r.ctx, ssubject)
	stickyExhaustedIsNull, _ := execIsNull(seM, seErr)

	return wantGot("runtime-values/regexp_exec_and_last_index",
		jobj(
			kv("first", jobj(
				kv("match", jstr("[\"abc\",\"b\"]")),
				kv("index", jint(2)),
				kv("input", jstr("xxabcXXabc")),
			)),
			kv("last_index_after_first", jstr("5")),
			kv("second", jobj(
				kv("match", jstr("[\"abc\",\"b\"]")),
				kv("index", jint(7)),
				kv("input", jstr("xxabcXXabc")),
			)),
			kv("last_index_after_second", jstr("10")),
			kv("third_is_null", jbool(true)),
			kv("last_index_after_fail", jstr("0")),
			kv("sticky_at_0_is_null", jbool(true)),
			kv("sticky_at_2_is_match", jbool(true)),
			kv("sticky_index", jint(2)),
			kv("sticky_exhausted_is_null", jbool(true)),
		),
		jobj(
			kv("first", first),
			kv("last_index_after_first", jstr(lastIndexAfterFirst)),
			kv("second", second),
			kv("last_index_after_second", jstr(lastIndexAfterSecond)),
			kv("third_is_null", jbool(thirdIsNull)),
			kv("last_index_after_fail", jstr(lastIndexAfterFail)),
			kv("sticky_at_0_is_null", jbool(stickyAt0IsNull)),
			kv("sticky_at_2_is_match", jbool(stickyAt2IsMatch)),
			kv("sticky_index", jint(int64(stickyIndex))),
			kv("sticky_exhausted_is_null", jbool(stickyExhaustedIsNull)),
		))
}

func checkRegExpInvalidPatternError(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	tc := r.tc(t)
	defer func() { _ = tc.Close() }()
	pattern := str(t, r, "(")
	_, nativeErr := r.scope.NewRegExp(r.ctx, pattern, 0, tc)
	nativeIsNone := nativeErr != nil
	nativeCaught, nativeMessage := caughtMessage(t, r, tc)

	jsText := evalText(t, r, tc, `new RegExp("(")`)
	jsFailed := jsText == ""
	jsCaught, jsMessage := caughtMessage(t, r, tc)

	return wantGot("runtime-values/regexp_invalid_pattern_error",
		jobj(
			kv("native_is_none", jbool(true)),
			kv("native_caught", jbool(true)),
			kv("native_message", jstr("Uncaught SyntaxError: Invalid regular expression: /(/: Unterminated group")),
			kv("js_failed", jbool(true)),
			kv("js_caught", jbool(true)),
			kv("js_message", jstr("Uncaught SyntaxError: Invalid regular expression: /(/: Unterminated group")),
		),
		jobj(
			kv("native_is_none", jbool(nativeIsNone)),
			kv("native_caught", jbool(nativeCaught)),
			kv("native_message", jstr(nativeMessage)),
			kv("js_failed", jbool(jsFailed)),
			kv("js_caught", jbool(jsCaught)),
			kv("js_message", jstr(jsMessage)),
		))
}

func checkRegExpJsCreatedSource(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	value := evalValue(t, r, nil, "/ab+c/gi")
	isRegExp, err := value.IsRegExp()
	if err != nil {
		t.Fatalf("IsRegExp: %v", err)
	}
	re, err := gov8.AsRegExp(value)
	if err != nil {
		t.Fatalf("AsRegExp: %v", err)
	}
	sourceV, err := re.GetSource(r.scope)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	source := textOf(t, r, sourceV)

	subject := str(t, r, "xAbcXABBC")
	execText := func() string {
		m, err := re.Exec(r.scope, r.ctx, subject)
		if err != nil {
			return ""
		}
		return stringifyText(t, r, nil, m.Value)
	}
	first := execText()
	second := execText()

	return wantGot("runtime-values/regexp_js_created_source",
		jobj(
			kv("is_reg_exp", jbool(true)),
			kv("source", jstr("ab+c")),
			kv("first", jstr("[\"Abc\"]")),
			kv("second", jstr("[\"ABBC\"]")),
		),
		jobj(
			kv("is_reg_exp", jbool(isRegExp)),
			kv("source", jstr(source)),
			kv("first", jstr(first)),
			kv("second", jstr(second)),
		))
}

// --- 3. JSON --------------------------------------------------------------------

func checkJSONParseCanonical(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	roundtrip := func(source string) string {
		tc := r.tc(t)
		defer func() { _ = tc.Close() }()
		text := str(t, r, source)
		parsed, err := gov8.JSONParse(r.ctx, r.scope, text, tc)
		if err != nil {
			caught, _ := tc.HasCaught()
			return "<caught:" + map[bool]string{true: "true", false: "false"}[caught] + ">"
		}
		out, ok := jsonOrNone(t, r, tc, parsed)
		if !ok {
			return ""
		}
		s, err := out.ToString(r.ctx)
		if err != nil {
			return ""
		}
		return s
	}

	return wantGot("runtime-values/json_parse_canonical",
		jobj(
			kv("object", jstr(`{"a":[1,2.5,"s",true,null],"b":{"c":1}}`)),
			kv("whitespace", jstr("[1,2]")),
			kv("negative_zero", jstr("0")),
			kv("overflow_number", jstr("null")),
			kv("precision", jstr("9007199254740992")),
			kv("lone_surrogate", jstr("\"\\ud800\"")),
			kv("escapes", jstr("\"a/\\b\\f\\n\\r\\tA\"")),
		),
		jobj(
			kv("object", jstr(roundtrip(`{"a":[1,2.5,"s",true,null],"b":{"c":1}}`))),
			kv("whitespace", jstr(roundtrip("[ 1 , 2 ]"))),
			kv("negative_zero", jstr(roundtrip("-0"))),
			kv("overflow_number", jstr(roundtrip("1e999"))),
			kv("precision", jstr(roundtrip("9007199254740993"))),
			kv("lone_surrogate", jstr(roundtrip(`"\ud800"`))),
			kv("escapes", jstr(roundtrip(`"a\/\b\f\n\r\t\u0041"`))),
		))
}

func checkJSONParseErrors(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	entries := func() (out []jsonPair) {
		for _, c := range []struct{ name, input string }{
			{"empty", ""},
			{"truncated", "{"},
			{"single_quotes", "{'a':1}"},
			{"trailing", "[1,2],3"},
			{"bare_word", "undefined"},
		} {
			tc := r.tc(t)
			text := str(t, r, c.input)
			_, err := gov8.JSONParse(r.ctx, r.scope, text, tc)
			caught, message := caughtMessage(t, r, tc)
			out = append(out, kv(c.name, jobj(
				kv("is_none", jbool(err != nil)),
				kv("caught", jbool(caught)),
				kv("message", jstr(message)),
			)))
			_ = tc.Close()
		}
		return out
	}()

	rejected := func(message string) jsonValue {
		return jobj(
			kv("is_none", jbool(true)),
			kv("caught", jbool(true)),
			kv("message", jstr(message)),
		)
	}

	return wantGot("runtime-values/json_parse_errors",
		jobj(
			kv("empty", rejected("Uncaught SyntaxError: Unexpected end of JSON input")),
			kv("truncated", rejected("Uncaught SyntaxError: Expected property name or '}' in JSON at position 1 (line 1 column 2)")),
			kv("single_quotes", rejected("Uncaught SyntaxError: Expected property name or '}' in JSON at position 1 (line 1 column 2)")),
			kv("trailing", rejected("Uncaught SyntaxError: Unexpected non-whitespace character after JSON at position 5 (line 1 column 6)")),
			kv("bare_word", rejected(`Uncaught SyntaxError: "undefined" is not valid JSON`)),
		),
		jobj(entries...))
}

func checkJSONStringifyObjects(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	stringify := func(source string) string {
		tc := r.tc(t)
		defer func() { _ = tc.Close() }()
		value := evalValue(t, r, tc, source)
		out, ok := jsonOrNone(t, r, tc, value)
		if !ok {
			return ""
		}
		s, err := out.ToString(r.ctx)
		if err != nil {
			return ""
		}
		return s
	}

	return wantGot("runtime-values/json_stringify_objects",
		jobj(
			kv("omissions", jstr(`{"c":[1,null,2],"d":null,"e":0}`)),
			kv("symbol_keys_skipped", jstr(`{"ok":2}`)),
			kv("holes", jstr("[1,null,null,4]")),
			kv("escapes", jstr(`{"q":"a\"b\\c\nd\te","f":"\u0001"}`)),
			kv("date", jstr(`"1970-01-01T00:00:00.000Z"`)),
			kv("to_json", jstr(`{"replaced":true}`)),
			kv("nested", jstr(`{"o":{"a":[[1,{"b":"x"}]]}}`)),
		),
		jobj(
			kv("omissions", jstr(stringify("({a: undefined, b: () => 1, c: [1, undefined, 2], d: null, e: 0})"))),
			kv("symbol_keys_skipped", jstr(stringify("const s = Symbol('k'); ({[s]: 1, ok: 2})"))),
			kv("holes", jstr(stringify("(function(){ const a = [1]; a[3] = 4; return a; })()"))),
			kv("escapes", jstr(stringify(`({q: "a\"b\\c\nd\te", f: "\u0001"})`))),
			kv("date", jstr(stringify("new Date(0)"))),
			kv("to_json", jstr(stringify("({toJSON: () => ({replaced: true}), ignored: 1})"))),
			kv("nested", jstr(stringify(`({o: {a: [[1, {b: "x"}]]}})`))),
		))
}

func checkJSONStringifyBoundaries(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	stringify := func(source string) (bool, string) {
		tc := r.tc(t)
		defer func() { _ = tc.Close() }()
		value := evalValue(t, r, tc, source)
		out, err := gov8.JSONStringify(r.ctx, r.scope, value, tc)
		isNone := err != nil
		text := ""
		if !isNone {
			if s, serr := out.ToString(r.ctx); serr == nil {
				text = s
			}
		}
		caught, message := caughtMessage(t, r, tc)
		if caught {
			text = "<caught> " + message
		}
		return isNone, text
	}

	undefinedIsNone, undefinedText := stringify("undefined")
	functionIsNone, functionText := stringify("() => 1")
	_, nanText := stringify("NaN")
	_, infinityText := stringify("Infinity")
	_, negInfinityText := stringify("-Infinity")
	wrapperIsNone, wrapperText := stringify("new Number(5)")
	booleanWrapperIsNone, booleanWrapperText := stringify("new Boolean(false)")
	stringWrapperIsNone, stringWrapperText := stringify(`new String("ab")`)
	symbolIsNone, symbolText := stringify("Symbol('s')")
	circularIsNone, circularText := stringify("const c = {}; c.self = c; c")

	return wantGot("runtime-values/json_stringify_boundaries",
		jobj(
			kv("undefined_is_none", jbool(false)),
			kv("undefined", jstr("undefined")),
			kv("function_is_none", jbool(false)),
			kv("function", jstr("undefined")),
			kv("nan", jstr("null")),
			kv("infinity", jstr("null")),
			kv("neg_infinity", jstr("null")),
			kv("number_wrapper", jstr("5")),
			kv("number_wrapper_is_none", jbool(false)),
			kv("boolean_wrapper", jstr("false")),
			kv("boolean_wrapper_is_none", jbool(false)),
			kv("string_wrapper", jstr("\"ab\"")),
			kv("string_wrapper_is_none", jbool(false)),
			kv("symbol_is_none", jbool(false)),
			kv("symbol", jstr("undefined")),
			kv("circular_is_none", jbool(true)),
			kv("circular", jstr("<caught> Uncaught TypeError: Converting circular structure to JSON\n    --> starting at object with constructor 'Object'\n    --- property 'self' closes the circle")),
		),
		jobj(
			kv("undefined_is_none", jbool(undefinedIsNone)),
			kv("undefined", jstr(undefinedText)),
			kv("function_is_none", jbool(functionIsNone)),
			kv("function", jstr(functionText)),
			kv("nan", jstr(nanText)),
			kv("infinity", jstr(infinityText)),
			kv("neg_infinity", jstr(negInfinityText)),
			kv("number_wrapper", jstr(wrapperText)),
			kv("number_wrapper_is_none", jbool(wrapperIsNone)),
			kv("boolean_wrapper", jstr(booleanWrapperText)),
			kv("boolean_wrapper_is_none", jbool(booleanWrapperIsNone)),
			kv("string_wrapper", jstr(stringWrapperText)),
			kv("string_wrapper_is_none", jbool(stringWrapperIsNone)),
			kv("symbol_is_none", jbool(symbolIsNone)),
			kv("symbol", jstr(symbolText)),
			kv("circular_is_none", jbool(circularIsNone)),
			kv("circular", jstr(circularText)),
		))
}

// --- 4. Array -------------------------------------------------------------------

func checkArrayNewAndElements(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	three, err := r.scope.NewArray(r.ctx, 3)
	if err != nil {
		t.Fatalf("NewArray(3): %v", err)
	}
	isArray, err := three.IsArray()
	if err != nil {
		t.Fatalf("IsArray: %v", err)
	}
	length, err := three.Length()
	if err != nil {
		t.Fatalf("Length: %v", err)
	}
	hasZero, err := three.HasIndex(r.scope, r.ctx, 0)
	if err != nil {
		hasZero = false
	}
	if got := stringifyText(t, r, nil, three.Value); got != "[null,null,null]" {
		t.Logf("three stringify = %q", got)
	}

	negative, err := r.scope.NewArray(r.ctx, -5)
	if err != nil {
		t.Fatalf("NewArray(-5): %v", err)
	}
	negativeLength, err := negative.Length()
	if err != nil {
		t.Fatalf("negative Length: %v", err)
	}

	elements, err := r.scope.NewArrayWithElements(r.ctx, []gov8.Value{
		int32Val(t, r, 1), int32Val(t, r, 2),
	})
	if err != nil {
		t.Fatalf("NewArrayWithElements: %v", err)
	}
	elementsLength, err := elements.Length()
	if err != nil {
		t.Fatalf("elements Length: %v", err)
	}
	elementsJSON := stringifyText(t, r, nil, elements.Value)

	// The JS constructor throws a deterministic RangeError for negative
	// lengths (unlike the native constructor).
	jsNegativeCaught, jsNegativeMessage := func() (bool, string) {
		tc := r.tc(t)
		defer func() { _ = tc.Close() }()
		evalText(t, r, tc, "new Array(-1)")
		return caughtMessage(t, r, tc)
	}()

	threeStringify := stringifyText(t, r, nil, three.Value)

	return wantGot("runtime-values/array_new_and_elements",
		jobj(
			kv("is_array", jbool(true)),
			kv("length", jint(3)),
			kv("has_index_zero", jbool(false)),
			kv("stringify", jstr("[null,null,null]")),
			kv("negative_length", jint(0)),
			kv("elements_length", jint(2)),
			kv("elements_json", jstr("[1,2]")),
			kv("js_negative_caught", jbool(true)),
			kv("js_negative_message", jstr("Uncaught RangeError: Invalid array length")),
		),
		jobj(
			kv("is_array", jbool(isArray)),
			kv("length", jint(length)),
			kv("has_index_zero", jbool(hasZero)),
			kv("stringify", jstr(threeStringify)),
			kv("negative_length", jint(negativeLength)),
			kv("elements_length", jint(elementsLength)),
			kv("elements_json", jstr(elementsJSON)),
			kv("js_negative_caught", jbool(jsNegativeCaught)),
			kv("js_negative_message", jstr(jsNegativeMessage)),
		))
}

func checkArrayIndexSemantics(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	arr, err := r.scope.NewArray(r.ctx, 0)
	if err != nil {
		t.Fatalf("NewArray(0): %v", err)
	}
	publish(t, r, "a", arr.Value)

	b := str(t, r, "b")
	if _, err := arr.SetIndex(r.scope, r.ctx, 1, b); err != nil {
		t.Fatalf("SetIndex: %v", err)
	}
	lengthAfterSet, err := arr.Length()
	if err != nil {
		t.Fatalf("Length: %v", err)
	}
	gotOne, err := arr.GetIndex(r.scope, r.ctx, 1)
	if err != nil {
		t.Fatalf("GetIndex: %v", err)
	}
	getOne := textOf(t, r, gotOne)
	hasOne, err := arr.HasIndex(r.scope, r.ctx, 1)
	if err != nil {
		t.Fatalf("HasIndex(1): %v", err)
	}
	hasTwo, err := arr.HasIndex(r.scope, r.ctx, 2)
	if err != nil {
		t.Fatalf("HasIndex(2): %v", err)
	}

	push := evalText(t, r, nil, "a.push('pushed'); a.length")
	pushNative, err := arr.Length()
	if err != nil {
		t.Fatalf("push native: %v", err)
	}
	negativeSub := evalText(t, r, nil, "a[-1] = 'neg'; [a.length, a.hasOwnProperty(-1), JSON.stringify(a)].join('|')")
	negativeSubNative, err := arr.Length()
	if err != nil {
		t.Fatalf("negative native: %v", err)
	}

	evalText(t, r, nil, "(function(){ const mx = []; mx[4294967294] = 7; globalThis.mx = mx; return mx.length; })()")
	maxIndexNative := int64(0)
	if mxV, ok := evalValueOK(t, r, nil, "mx"); ok {
		if mx, err := gov8.AsArray(mxV); err == nil {
			if n, err := mx.Length(); err == nil {
				maxIndexNative = n
			}
		}
	}

	return wantGot("runtime-values/array_index_semantics",
		jobj(
			kv("length_after_set", jint(2)),
			kv("get_one", jstr("b")),
			kv("has_one", jbool(true)),
			kv("has_two", jbool(false)),
			kv("push_js", jstr("3")),
			kv("push_native", jint(3)),
			kv("negative_subscript", jstr("3|true|[null,\"b\",\"pushed\"]")),
			kv("negative_subscript_native_length", jint(3)),
			kv("max_index_native", jint(4294967295)),
		),
		jobj(
			kv("length_after_set", jint(lengthAfterSet)),
			kv("get_one", jstr(getOne)),
			kv("has_one", jbool(hasOne)),
			kv("has_two", jbool(hasTwo)),
			kv("push_js", jstr(push)),
			kv("push_native", jint(pushNative)),
			kv("negative_subscript", jstr(negativeSub)),
			kv("negative_subscript_native_length", jint(negativeSubNative)),
			kv("max_index_native", jint(maxIndexNative)),
		))
}

// --- 5. Map / Set ---------------------------------------------------------------

func checkMapNativeOps(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	m, err := r.scope.NewMap(r.ctx)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	isMap, err := m.IsMap()
	if err != nil {
		t.Fatalf("IsMap: %v", err)
	}
	initialSize, err := m.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}

	keyA := str(t, r, "a")
	one := int32Val(t, r, 1)
	returned, err := m.Set(r.scope, r.ctx, keyA, one)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	returnedIsSame := false
	if same, err := gov8.Same(returned.Value, m.Value); err == nil {
		returnedIsSame = same
	}

	hasA, err := m.Has(r.scope, r.ctx, keyA)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	gotA, err := m.Get(r.scope, r.ctx, keyA)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	getA := textOf(t, r, gotA)
	sizeOne, err := m.Size()
	if err != nil {
		t.Fatalf("Size one: %v", err)
	}

	nan := num(t, r, math.NaN())
	two := int32Val(t, r, 2)
	if _, err := m.Set(r.scope, r.ctx, nan, two); err != nil {
		t.Fatalf("Set(NaN): %v", err)
	}
	hasNan, err := m.Has(r.scope, r.ctx, nan)
	if err != nil {
		t.Fatalf("Has(NaN): %v", err)
	}
	gotNan, err := m.Get(r.scope, r.ctx, nan)
	if err != nil {
		t.Fatalf("Get(NaN): %v", err)
	}
	getNan := textOf(t, r, gotNan)

	k1 := getObject(t, evalValue(t, r, nil, "({})"))
	k2 := getObject(t, evalValue(t, r, nil, "({})"))
	three := int32Val(t, r, 3)
	four := int32Val(t, r, 4)
	nine := int32Val(t, r, 9)
	if _, err := m.Set(r.scope, r.ctx, k1.Value, three); err != nil {
		t.Fatalf("Set(k1): %v", err)
	}
	if _, err := m.Set(r.scope, r.ctx, k2.Value, four); err != nil {
		t.Fatalf("Set(k2): %v", err)
	}
	sizeWithObjects, err := m.Size()
	if err != nil {
		t.Fatalf("Size objects: %v", err)
	}
	if _, err := m.Set(r.scope, r.ctx, k1.Value, nine); err != nil {
		t.Fatalf("overwrite k1: %v", err)
	}
	sizeAfterOverwrite, err := m.Size()
	if err != nil {
		t.Fatalf("Size overwrite: %v", err)
	}
	gotK1, err := m.Get(r.scope, r.ctx, k1.Value)
	if err != nil {
		t.Fatalf("Get(k1): %v", err)
	}
	getK1AfterOverwrite := textOf(t, r, gotK1)

	deleted, err := m.Delete(r.scope, r.ctx, keyA)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	deletedMissing, err := m.Delete(r.scope, r.ctx, keyA)
	if err != nil {
		t.Fatalf("Delete again: %v", err)
	}

	ordered, err := r.scope.NewMap(r.ctx)
	if err != nil {
		t.Fatalf("NewMap ordered: %v", err)
	}
	if _, err := ordered.Set(r.scope, r.ctx, str(t, r, "a"), one); err != nil {
		t.Fatalf("ordered set a: %v", err)
	}
	if _, err := ordered.Set(r.scope, r.ctx, str(t, r, "b"), two); err != nil {
		t.Fatalf("ordered set b: %v", err)
	}
	asArray, err := ordered.AsArray(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	asArrayJSON := stringifyText(t, r, nil, asArray.Value)

	if err := m.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	sizeAfterClear, err := m.Size()
	if err != nil {
		t.Fatalf("Size clear: %v", err)
	}
	hasAAfterClear, err := m.Has(r.scope, r.ctx, keyA)
	if err != nil {
		t.Fatalf("Has after clear: %v", err)
	}

	return wantGot("runtime-values/map_native_ops",
		jobj(
			kv("is_map", jbool(true)),
			kv("initial_size", jint(0)),
			kv("returned_is_same", jbool(true)),
			kv("has_a", jbool(true)),
			kv("get_a", jstr("1")),
			kv("size_one", jint(1)),
			kv("has_nan", jbool(true)),
			kv("get_nan", jstr("2")),
			kv("size_with_objects", jint(4)),
			kv("size_after_overwrite", jint(4)),
			kv("get_k1_after_overwrite", jstr("9")),
			kv("deleted", jbool(true)),
			kv("deleted_missing", jbool(false)),
			kv("as_array", jstr("[\"a\",1,\"b\",2]")),
			kv("size_after_clear", jint(0)),
			kv("has_a_after_clear", jbool(false)),
		),
		jobj(
			kv("is_map", jbool(isMap)),
			kv("initial_size", jint(initialSize)),
			kv("returned_is_same", jbool(returnedIsSame)),
			kv("has_a", jbool(hasA)),
			kv("get_a", jstr(getA)),
			kv("size_one", jint(sizeOne)),
			kv("has_nan", jbool(hasNan)),
			kv("get_nan", jstr(getNan)),
			kv("size_with_objects", jint(sizeWithObjects)),
			kv("size_after_overwrite", jint(sizeAfterOverwrite)),
			kv("get_k1_after_overwrite", jstr(getK1AfterOverwrite)),
			kv("deleted", jbool(deleted)),
			kv("deleted_missing", jbool(deletedMissing)),
			kv("as_array", jstr(asArrayJSON)),
			kv("size_after_clear", jint(sizeAfterClear)),
			kv("has_a_after_clear", jbool(hasAAfterClear)),
		))
}

func checkSetNativeOps(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	s, err := r.scope.NewSet(r.ctx)
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	isSet, err := s.IsSet()
	if err != nil {
		t.Fatalf("IsSet: %v", err)
	}
	initialSize, err := s.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}

	x := str(t, r, "x")
	returned, err := s.Add(r.scope, r.ctx, x)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	returnedIsSame := false
	if same, err := gov8.Same(returned.Value, s.Value); err == nil {
		returnedIsSame = same
	}
	if _, err := s.Add(r.scope, r.ctx, x); err != nil {
		t.Fatalf("Add dup: %v", err)
	}
	sizeAfterDup, err := s.Size()
	if err != nil {
		t.Fatalf("Size dup: %v", err)
	}

	nan := num(t, r, math.NaN())
	if _, err := s.Add(r.scope, r.ctx, nan); err != nil {
		t.Fatalf("Add(NaN): %v", err)
	}
	if _, err := s.Add(r.scope, r.ctx, nan); err != nil {
		t.Fatalf("Add(NaN) dup: %v", err)
	}
	sizeAfterNan, err := s.Size()
	if err != nil {
		t.Fatalf("Size NaN: %v", err)
	}
	hasNan, err := s.Has(r.scope, r.ctx, nan)
	if err != nil {
		t.Fatalf("Has(NaN): %v", err)
	}

	posZero := num(t, r, 0)
	negZero := num(t, r, math.Copysign(0, -1))
	if _, err := s.Add(r.scope, r.ctx, negZero); err != nil {
		t.Fatalf("Add(-0): %v", err)
	}
	hasPosZero, err := s.Has(r.scope, r.ctx, posZero)
	if err != nil {
		t.Fatalf("Has(+0): %v", err)
	}

	asArray, err := s.AsArray(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	asArrayJSON := stringifyText(t, r, nil, asArray.Value)

	deleted, err := s.Delete(r.scope, r.ctx, x)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	deletedMissing, err := s.Delete(r.scope, r.ctx, x)
	if err != nil {
		t.Fatalf("Delete again: %v", err)
	}
	sizeAfterDelete, err := s.Size()
	if err != nil {
		t.Fatalf("Size delete: %v", err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	sizeAfterClear, err := s.Size()
	if err != nil {
		t.Fatalf("Size clear: %v", err)
	}

	return wantGot("runtime-values/set_native_ops",
		jobj(
			kv("is_set", jbool(true)),
			kv("initial_size", jint(0)),
			kv("returned_is_same", jbool(true)),
			kv("size_after_dup", jint(1)),
			kv("size_after_nan", jint(2)),
			kv("has_nan", jbool(true)),
			kv("has_pos_zero_after_neg_zero", jbool(true)),
			kv("as_array", jstr("[\"x\",null,0]")),
			kv("deleted", jbool(true)),
			kv("deleted_missing", jbool(false)),
			kv("size_after_delete", jint(2)),
			kv("size_after_clear", jint(0)),
		),
		jobj(
			kv("is_set", jbool(isSet)),
			kv("initial_size", jint(initialSize)),
			kv("returned_is_same", jbool(returnedIsSame)),
			kv("size_after_dup", jint(sizeAfterDup)),
			kv("size_after_nan", jint(sizeAfterNan)),
			kv("has_nan", jbool(hasNan)),
			kv("has_pos_zero_after_neg_zero", jbool(hasPosZero)),
			kv("as_array", jstr(asArrayJSON)),
			kv("deleted", jbool(deleted)),
			kv("deleted_missing", jbool(deletedMissing)),
			kv("size_after_delete", jint(sizeAfterDelete)),
			kv("size_after_clear", jint(sizeAfterClear)),
		))
}

func checkMapSetJsInterop(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	jsMap := evalValue(t, r, nil, `new Map([["a", 1], ["b", 2]])`)
	isMap, err := jsMap.IsMap()
	if err != nil {
		t.Fatalf("IsMap: %v", err)
	}
	m, err := gov8.AsMap(jsMap)
	if err != nil {
		t.Fatalf("AsMap: %v", err)
	}
	size, err := m.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	keyB := str(t, r, "b")
	gotB, err := m.Get(r.scope, r.ctx, keyB)
	if err != nil {
		t.Fatalf("Get(b): %v", err)
	}
	getB := textOf(t, r, gotB)
	mapTypeof := textOf(t, r, mustTypeOf(t, r, jsMap))

	nativeMap, err := r.scope.NewMap(r.ctx)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	tenStr := str(t, r, "ten")
	twentyStr := str(t, r, "twenty")
	if _, err := nativeMap.Set(r.scope, r.ctx, int32Val(t, r, 10), tenStr); err != nil {
		t.Fatalf("set 10: %v", err)
	}
	if _, err := nativeMap.Set(r.scope, r.ctx, int32Val(t, r, 20), twentyStr); err != nil {
		t.Fatalf("set 20: %v", err)
	}
	publish(t, r, "nm", nativeMap.Value)
	jsEntries := evalText(t, r, nil, "JSON.stringify([...nm.entries()])")
	jsInstanceof := evalText(t, r, nil, "nm instanceof Map")

	jsSet := evalValue(t, r, nil, "(function(){ const s = new Set([1,2]); s.add(3); return s; })()")
	s, err := gov8.AsSet(jsSet)
	if err != nil {
		t.Fatalf("AsSet: %v", err)
	}
	setSize, err := s.Size()
	if err != nil {
		t.Fatalf("Set size: %v", err)
	}
	three := int32Val(t, r, 3)
	setHasThree, err := s.Has(r.scope, r.ctx, three)
	if err != nil {
		t.Fatalf("Set has 3: %v", err)
	}
	setArray, err := s.AsArray(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("Set AsArray: %v", err)
	}
	setAsArray := stringifyText(t, r, nil, setArray.Value)

	return wantGot("runtime-values/map_set_js_interop",
		jobj(
			kv("js_map_is_map", jbool(true)),
			kv("size", jint(2)),
			kv("get_b", jstr("2")),
			kv("map_typeof", jstr("object")),
			kv("native_map_js_entries", jstr("[[10,\"ten\"],[20,\"twenty\"]]")),
			kv("native_map_instanceof", jstr("true")),
			kv("set_size", jint(3)),
			kv("set_has_three", jbool(true)),
			kv("set_as_array", jstr("[1,2,3]")),
		),
		jobj(
			kv("js_map_is_map", jbool(isMap)),
			kv("size", jint(size)),
			kv("get_b", jstr(getB)),
			kv("map_typeof", jstr(mapTypeof)),
			kv("native_map_js_entries", jstr(jsEntries)),
			kv("native_map_instanceof", jstr(jsInstanceof)),
			kv("set_size", jint(setSize)),
			kv("set_has_three", jbool(setHasThree)),
			kv("set_as_array", jstr(setAsArray)),
		))
}

func mustTypeOf(t tester, r *runtime, v gov8.Value) gov8.Value {
	t.Helper()
	out, err := v.TypeOf(r.scope)
	if err != nil {
		t.Fatalf("TypeOf: %v", err)
	}
	return out
}

// --- 6. Proxy -------------------------------------------------------------------

func checkProxyIdentityAndDefaultTraps(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	target := getObject(t, evalValue(t, r, nil, "({x: 1})"))
	handler := getObject(t, evalValue(t, r, nil, "({})"))

	proxy, err := r.scope.NewProxy(r.ctx, target, handler)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	isProxy, err := proxy.IsProxy()
	if err != nil {
		t.Fatalf("IsProxy: %v", err)
	}
	isObject, err := proxy.IsObject()
	if err != nil {
		t.Fatalf("IsObject: %v", err)
	}
	gotTarget, err := proxy.GetTarget(r.scope)
	if err != nil {
		t.Fatalf("GetTarget: %v", err)
	}
	targetSame := false
	if same, err := gov8.Same(gotTarget, target.Value); err == nil {
		targetSame = same
	}
	gotHandler, err := proxy.GetHandler(r.scope)
	if err != nil {
		t.Fatalf("GetHandler: %v", err)
	}
	handlerSame := false
	if same, err := gov8.Same(gotHandler, handler.Value); err == nil {
		handlerSame = same
	}
	revoked, err := proxy.IsRevoked()
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	notRevoked := !revoked

	publish(t, r, "p", proxy.Value)

	gotX := getByName(t, target, r, "x")
	nativeGetX := textOf(t, r, gotX)
	proxyObj := getObject(t, proxy.Value)
	proxyGotX := getByName(t, proxyObj, r, "x")
	proxyGetX := textOf(t, r, proxyGotX)
	two := int32Val(t, r, 2)
	nativeSetY := false
	if ok, err := proxyObj.SetByName(r.scope, r.ctx, "y", two); err == nil {
		nativeSetY = ok
	}
	jsSees := evalText(t, r, nil, "[p.x, p.y, 'x' in p, JSON.stringify(p)].join('|')")

	return wantGot("runtime-values/proxy_identity_and_default_traps",
		jobj(
			kv("is_proxy", jbool(true)),
			kv("is_object", jbool(true)),
			kv("target_same", jbool(true)),
			kv("handler_same", jbool(true)),
			kv("not_revoked", jbool(true)),
			kv("target_get_x", jstr("1")),
			kv("proxy_get_x", jstr("1")),
			kv("proxy_set_y", jbool(true)),
			kv("js_sees", jstr("1|2|true|{\"x\":1,\"y\":2}")),
		),
		jobj(
			kv("is_proxy", jbool(isProxy)),
			kv("is_object", jbool(isObject)),
			kv("target_same", jbool(targetSame)),
			kv("handler_same", jbool(handlerSame)),
			kv("not_revoked", jbool(notRevoked)),
			kv("target_get_x", jstr(nativeGetX)),
			kv("proxy_get_x", jstr(proxyGetX)),
			kv("proxy_set_y", jbool(nativeSetY)),
			kv("js_sees", jstr(jsSees)),
		))
}

func checkProxyRevokeSemantics(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	tc := r.tc(t)
	defer func() { _ = tc.Close() }()

	target := getObject(t, evalValue(t, r, tc, "({x: 1})"))
	handler := getObject(t, evalValue(t, r, tc, "({})"))
	proxy, err := r.scope.NewProxy(r.ctx, target, handler)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	gotTarget, err := proxy.GetTarget(r.scope)
	if err != nil {
		t.Fatalf("GetTarget: %v", err)
	}
	targetSameBefore := false
	if same, err := gov8.Same(gotTarget, target.Value); err == nil {
		targetSameBefore = same
	}
	revokedBefore, err := proxy.IsRevoked()
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	notRevokedBefore := !revokedBefore

	publish(t, r, "rp", proxy.Value)
	if err := proxy.Revoke(); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	revokedAfter, err := proxy.IsRevoked()
	if err != nil {
		t.Fatalf("IsRevoked after: %v", err)
	}

	proxyObj := getObject(t, proxy.Value)
	_, _, getErr := proxyObj.GetByName(r.scope, r.ctx, "x")
	nativeGetAfterRevoke := getErr == nil
	nativeCaught, nativeMessage := caughtMessage(t, r, tc)

	jsErrorName := evalText(t, r, tc, "(function(){ try { return String(rp.x); } catch (e) { return e.name; } })()")
	// Pinned nuance: a revoked proxy's native get_target still resolves,
	// but to the JavaScript null value (V8 clears the target to null).
	targetAfter, err := proxy.GetTarget(r.scope)
	if err != nil {
		t.Fatalf("GetTarget after revoke: %v", err)
	}
	targetUndefinedAfter, err := targetAfter.IsUndefined()
	if err != nil {
		t.Fatalf("IsUndefined: %v", err)
	}
	targetNullAfter, err := targetAfter.IsNull()
	if err != nil {
		t.Fatalf("IsNull: %v", err)
	}

	return wantGot("runtime-values/proxy_revoke_semantics",
		jobj(
			kv("target_same_before_revoke", jbool(true)),
			kv("not_revoked_before", jbool(true)),
			kv("revoked_after", jbool(true)),
			kv("native_get_after_revoke_is_none", jbool(true)),
			kv("native_caught", jbool(true)),
			kv("native_message", jstr("Uncaught TypeError: Cannot perform 'get' on a proxy that has been revoked")),
			kv("js_error_name", jstr("TypeError")),
			kv("target_undefined_after_revoke", jbool(false)),
			kv("target_null_after_revoke", jbool(true)),
		),
		jobj(
			kv("target_same_before_revoke", jbool(targetSameBefore)),
			kv("not_revoked_before", jbool(notRevokedBefore)),
			kv("revoked_after", jbool(revokedAfter)),
			kv("native_get_after_revoke_is_none", jbool(nativeGetAfterRevoke)),
			kv("native_caught", jbool(nativeCaught)),
			kv("native_message", jstr(nativeMessage)),
			kv("js_error_name", jstr(jsErrorName)),
			kv("target_undefined_after_revoke", jbool(targetUndefinedAfter)),
			kv("target_null_after_revoke", jbool(targetNullAfter)),
		))
}

func checkProxyTrapInvariantError(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	tc := r.tc(t)
	defer func() { _ = tc.Close() }()

	target := getObject(t, evalValue(t, r, tc, "({x: 1})"))
	handler := getObject(t, evalValue(t, r, tc, "({get: 1})"))
	proxy, err := r.scope.NewProxy(r.ctx, target, handler)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	proxyObj := getObject(t, proxy.Value)
	_, _, getErr := proxyObj.GetByName(r.scope, r.ctx, "x")
	nativeGetIsNone := getErr == nil
	caught, message := caughtMessage(t, r, tc)

	jsRevocable := evalText(t, r, tc, "(function(){ const r = Proxy.revocable({a: 1}, {}); globalThis.rpr = r; r.revoke(); return 'revoked'; })()")
	revokedViaNative := false
	if pV, ok := evalValueOK(t, r, nil, "rpr.proxy"); ok {
		if p, err := gov8.AsProxy(pV); err == nil {
			if revoked, err := p.IsRevoked(); err == nil {
				revokedViaNative = revoked
			}
		}
	}

	return wantGot("runtime-values/proxy_trap_invariant_error",
		jobj(
			kv("native_get_is_none", jbool(true)),
			kv("caught", jbool(true)),
			kv("message", jstr("Uncaught TypeError: '1' returned for property 'get' of object '#<Object>' is not a function")),
			kv("js_revocable", jstr("revoked")),
			kv("js_revocable_is_revoked", jbool(true)),
		),
		jobj(
			kv("native_get_is_none", jbool(nativeGetIsNone)),
			kv("caught", jbool(caught)),
			kv("message", jstr(message)),
			kv("js_revocable", jstr(jsRevocable)),
			kv("js_revocable_is_revoked", jbool(revokedViaNative)),
		))
}

// --- 7. Symbol / Private --------------------------------------------------------

func checkSymbolIdentityAndDescription(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	s1, err := r.scope.NewSymbol(str(t, r, "gov8"))
	if err != nil {
		t.Fatalf("NewSymbol: %v", err)
	}
	s2, err := r.scope.NewSymbol(gov8.Value{})
	if err != nil {
		t.Fatalf("NewSymbol anonymous: %v", err)
	}
	isSymbol, err := s1.IsSymbol()
	if err != nil {
		t.Fatalf("IsSymbol: %v", err)
	}
	descriptionValue, err := s1.Description(r.scope)
	if err != nil {
		t.Fatalf("Description: %v", err)
	}
	descriptionText := textOf(t, r, descriptionValue)
	s2Description, err := s2.Description(r.scope)
	if err != nil {
		t.Fatalf("s2 Description: %v", err)
	}
	s2DescriptionIsUndefined, err := s2Description.IsUndefined()
	if err != nil {
		t.Fatalf("IsUndefined: %v", err)
	}
	typeofText := textOf(t, r, mustTypeOf(t, r, s1.Value))

	// Pinned nuance: ToString of a symbol throws (it is not a string
	// primitive conversion); the exception is observed by the TryCatch.
	tc := r.tc(t)
	to_string_text := valueText(t, r, tc, s1.Value)
	to_string_caught, _ := tc.HasCaught()
	_ = tc.Close()

	fresh, err := r.scope.NewSymbol(str(t, r, "gov8"))
	if err != nil {
		t.Fatalf("NewSymbol fresh: %v", err)
	}
	strictDifferent := true
	if same, err := s1.StrictEquals(fresh.Value); err == nil {
		strictDifferent = !same
	}

	publish(t, r, "sym1", s1.Value)
	jsTypeof := evalText(t, r, nil, "typeof sym1")
	jsString := evalText(t, r, nil, "String(sym1)")
	jsDescription := evalText(t, r, nil, "sym1.description")

	return wantGot("runtime-values/symbol_identity_and_description",
		jobj(
			kv("is_symbol", jbool(true)),
			kv("description", jstr("gov8")),
			kv("no_description_is_undefined", jbool(true)),
			kv("typeof", jstr("symbol")),
			kv("to_string", jstr("")),
			kv("to_string_throws", jbool(true)),
			kv("fresh_symbols_differ", jbool(true)),
			kv("js_typeof", jstr("symbol")),
			kv("js_string", jstr("Symbol(gov8)")),
			kv("js_description", jstr("gov8")),
		),
		jobj(
			kv("is_symbol", jbool(isSymbol)),
			kv("description", jstr(descriptionText)),
			kv("no_description_is_undefined", jbool(s2DescriptionIsUndefined)),
			kv("typeof", jstr(typeofText)),
			kv("to_string", jstr(to_string_text)),
			kv("to_string_throws", jbool(to_string_caught)),
			kv("fresh_symbols_differ", jbool(strictDifferent)),
			kv("js_typeof", jstr(jsTypeof)),
			kv("js_string", jstr(jsString)),
			kv("js_description", jstr(jsDescription)),
		))
}

func checkSymbolRegistry(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	k1a, err := r.scope.SymbolForKey(str(t, r, "gov8.slice"))
	if err != nil {
		t.Fatalf("ForKey: %v", err)
	}
	k1b, err := r.scope.SymbolForKey(str(t, r, "gov8.slice"))
	if err != nil {
		t.Fatalf("ForKey again: %v", err)
	}
	k2, err := r.scope.SymbolForKey(str(t, r, "gov8.other"))
	if err != nil {
		t.Fatalf("ForKey other: %v", err)
	}
	a1, err := r.scope.SymbolForApi(str(t, r, "gov8.slice"))
	if err != nil {
		t.Fatalf("ForApi: %v", err)
	}
	a1b, err := r.scope.SymbolForApi(str(t, r, "gov8.slice"))
	if err != nil {
		t.Fatalf("ForApi again: %v", err)
	}

	jsRegistrySymbol := evalValue(t, r, nil, "Symbol.for('gov8.slice')")
	publish(t, r, "symk", k1a.Value)
	jsKeyFor := evalText(t, r, nil, "Symbol.keyFor(symk)")
	registryMatchesJS := false
	if same, err := k1a.StrictEquals(jsRegistrySymbol); err == nil {
		registryMatchesJS = same
	}
	freshJSSymbolDiffers := true
	if fresh, ok := evalValueOK(t, r, nil, "Symbol('gov8.slice')"); ok {
		if same, err := k1a.StrictEquals(fresh); err == nil {
			freshJSSymbolDiffers = !same
		}
	}

	forKeyIDE, err := k1a.StrictEquals(k1b.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	forKeyDifferentDiffer := true
	if same, err := k1a.StrictEquals(k2.Value); err == nil {
		forKeyDifferentDiffer = !same
	}
	forApiIDE, err := a1.StrictEquals(a1b.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	forApiDiffers := true
	if same, err := a1.StrictEquals(k1a.Value); err == nil {
		forApiDiffers = !same
	}

	return wantGot("runtime-values/symbol_registry",
		jobj(
			kv("for_key_idempotent", jbool(true)),
			kv("for_key_different_descriptions_differ", jbool(true)),
			kv("for_api_idempotent", jbool(true)),
			kv("for_api_differs_from_for_key", jbool(true)),
			kv("registry_matches_js_symbol_for", jbool(true)),
			kv("fresh_js_symbol_differs", jbool(true)),
			kv("js_key_for", jstr("gov8.slice")),
		),
		jobj(
			kv("for_key_idempotent", jbool(forKeyIDE)),
			kv("for_key_different_descriptions_differ", jbool(forKeyDifferentDiffer)),
			kv("for_api_idempotent", jbool(forApiIDE)),
			kv("for_api_differs_from_for_key", jbool(forApiDiffers)),
			kv("registry_matches_js_symbol_for", jbool(registryMatchesJS)),
			kv("fresh_js_symbol_differs", jbool(freshJSSymbolDiffers)),
			kv("js_key_for", jstr(jsKeyFor)),
		))
}

func checkSymbolWellKnownInterop(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	// toStringTag
	tagTarget := getObject(t, evalValue(t, r, nil, "({})"))
	tag := str(t, r, "Gov8")
	tagSym, err := r.scope.GetToStringTagSymbol()
	if err != nil {
		t.Fatalf("GetToStringTagSymbol: %v", err)
	}
	if _, err := tagTarget.SetByKey(r.scope, r.ctx, tagSym.Value, tag); err != nil {
		t.Fatalf("set toStringTag: %v", err)
	}
	publish(t, r, "tagged", tagTarget.Value)
	jsToStringTag := evalText(t, r, nil, "Object.prototype.toString.call(tagged)")

	// iterator
	iterable := getObject(t, evalValue(t, r, nil, "({length: 2, 0: 'a', 1: 'b'})"))
	generator := evalValue(t, r, nil, "(function*(){ yield 1; yield 2; })")
	iterSym, err := r.scope.GetIteratorSymbol()
	if err != nil {
		t.Fatalf("GetIteratorSymbol: %v", err)
	}
	if _, err := iterable.SetByKey(r.scope, r.ctx, iterSym.Value, generator); err != nil {
		t.Fatalf("set iterator: %v", err)
	}
	publish(t, r, "it", iterable.Value)
	jsSpread := evalText(t, r, nil, "[...it].join('-')")

	// hasInstance: non-writable on Function.prototype, so a plain set is
	// silently ignored; define_own_property creates the own property.
	ctor := getObject(t, evalValue(t, r, nil, "function C(){}; C"))
	alwaysTrue := evalValue(t, r, nil, "() => true")
	hasInstanceSym, err := r.scope.GetHasInstanceSymbol()
	if err != nil {
		t.Fatalf("GetHasInstanceSymbol: %v", err)
	}
	if _, err := ctor.SetByKey(r.scope, r.ctx, hasInstanceSym.Value, alwaysTrue); err != nil {
		t.Fatalf("set hasInstance: %v", err)
	}
	gotHI, err := ctor.GetByKey(r.scope, r.ctx, hasInstanceSym.Value)
	if err != nil {
		t.Fatalf("get hasInstance: %v", err)
	}
	plainSetIgnored := true
	if same, err := gotHI.StrictEquals(alwaysTrue); err == nil {
		plainSetIgnored = !same
	}
	definedHI := false
	if ok, err := ctor.DefineOwnProperty(r.scope, r.ctx, hasInstanceSym.Value, alwaysTrue, gov8.AttrNone); err == nil {
		definedHI = ok
	}
	publish(t, r, "C", ctor.Value)
	jsInstanceof := evalText(t, r, nil, "({}) instanceof C")

	// JS Symbol.iterator identity with the native getter.
	jsIterator := evalValue(t, r, nil, "Symbol.iterator")
	iteratorIdentity := false
	if same, err := iterSym.StrictEquals(jsIterator); err == nil {
		iteratorIdentity = same
	}

	return wantGot("runtime-values/symbol_wellknown_interop",
		jobj(
			kv("js_to_string_tag", jstr("[object Gov8]")),
			kv("js_spread", jstr("1-2")),
			kv("plain_set_ignored", jbool(true)),
			kv("defined_has_instance", jbool(true)),
			kv("js_instanceof", jstr("true")),
			kv("iterator_identity", jbool(true)),
		),
		jobj(
			kv("js_to_string_tag", jstr(jsToStringTag)),
			kv("js_spread", jstr(jsSpread)),
			kv("plain_set_ignored", jbool(plainSetIgnored)),
			kv("defined_has_instance", jbool(definedHI)),
			kv("js_instanceof", jstr(jsInstanceof)),
			kv("iterator_identity", jbool(iteratorIdentity)),
		))
}

func checkPrivateSymbolInvisibility(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	priv1, err := r.scope.NewPrivate(str(t, r, "gov8.secret"))
	if err != nil {
		t.Fatalf("NewPrivate: %v", err)
	}
	priv1NameValue, err := priv1.Name(r.scope)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	priv1Name := textOf(t, r, priv1NameValue)
	anonymous, err := r.scope.NewPrivate(gov8.Value{})
	if err != nil {
		t.Fatalf("NewPrivate anonymous: %v", err)
	}
	anonymousName, err := anonymous.Name(r.scope)
	if err != nil {
		t.Fatalf("anonymous Name: %v", err)
	}
	anonymousNameIsUndefined, err := anonymousName.IsUndefined()
	if err != nil {
		t.Fatalf("IsUndefined: %v", err)
	}

	obj := getObject(t, evalValue(t, r, nil, "({visible: 1})"))
	value := int32Val(t, r, 42)
	setOK := false
	if ok, err := obj.SetPrivate(r.scope, r.ctx, priv1, value); err == nil {
		setOK = ok
	}
	hasPrivate := false
	if ok, err := obj.HasPrivate(r.scope, r.ctx, priv1); err == nil {
		hasPrivate = ok
	}
	gotPrivateV, err := obj.GetPrivate(r.scope, r.ctx, priv1)
	if err != nil {
		t.Fatalf("GetPrivate: %v", err)
	}
	getPrivate := textOf(t, r, gotPrivateV)

	p2, err := r.scope.PrivateForApi(str(t, r, "gov8.api"))
	if err != nil {
		t.Fatalf("ForApi: %v", err)
	}
	p2b, err := r.scope.PrivateForApi(str(t, r, "gov8.api"))
	if err != nil {
		t.Fatalf("ForApi again: %v", err)
	}
	fresh, err := r.scope.PrivateForApi(str(t, r, "gov8.api2"))
	if err != nil {
		t.Fatalf("ForApi fresh: %v", err)
	}
	forApiIdempotent := false
	if same, err := gov8.Same(p2.Value, p2b.Value); err == nil {
		forApiIdempotent = same
	}
	forApiDistinct := true
	if same, err := gov8.Same(p2.Value, fresh.Value); err == nil {
		forApiDistinct = !same
	}

	publish(t, r, "po", obj.Value)
	jsSees := evalText(t, r, nil, "[JSON.stringify(po), Object.keys(po).length, 'gov8.secret' in po].join('|')")

	deleteOK := false
	if ok, err := obj.DeletePrivate(r.scope, r.ctx, priv1); err == nil {
		deleteOK = ok
	}
	hasAfterDelete := true
	if ok, err := obj.HasPrivate(r.scope, r.ctx, priv1); err == nil {
		hasAfterDelete = ok
	}

	return wantGot("runtime-values/private_symbol_invisibility",
		jobj(
			kv("name", jstr("gov8.secret")),
			kv("anonymous_name_is_undefined", jbool(true)),
			kv("set_ok", jbool(true)),
			kv("has_private", jbool(true)),
			kv("get_private", jstr("42")),
			kv("for_api_idempotent", jbool(true)),
			kv("for_api_distinct", jbool(true)),
			kv("js_sees", jstr("{\"visible\":1}|1|false")),
			kv("delete_ok", jbool(true)),
			kv("has_after_delete", jbool(false)),
		),
		jobj(
			kv("name", jstr(priv1Name)),
			kv("anonymous_name_is_undefined", jbool(anonymousNameIsUndefined)),
			kv("set_ok", jbool(setOK)),
			kv("has_private", jbool(hasPrivate)),
			kv("get_private", jstr(getPrivate)),
			kv("for_api_idempotent", jbool(forApiIdempotent)),
			kv("for_api_distinct", jbool(forApiDistinct)),
			kv("js_sees", jstr(jsSees)),
			kv("delete_ok", jbool(deleteOK)),
			kv("has_after_delete", jbool(hasAfterDelete)),
		))
}

// --- 8. Wrappers ----------------------------------------------------------------

func checkPrimitiveWrapperObjects(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	numberWrapper := evalValue(t, r, nil, "new Number(5)")
	booleanWrapper := evalValue(t, r, nil, "new Boolean(false)")
	stringWrapper := evalValue(t, r, nil, "new String('ab')")
	bigintWrapper := evalValue(t, r, nil, "Object(123n)")
	primitive := evalValue(t, r, nil, "5")

	numberToString := textOf(t, r, numberWrapper)
	booleanToString := textOf(t, r, booleanWrapper)
	stringToString := textOf(t, r, stringWrapper)
	bigintToString := textOf(t, r, bigintWrapper)

	strictWrapperPrimitive := false
	if same, err := numberWrapper.StrictEquals(primitive); err == nil {
		strictWrapperPrimitive = same
	}

	pred := func(v gov8.Value, f func() (bool, error)) bool {
		b, err := f()
		if err != nil {
			t.Fatalf("predicate: %v", err)
		}
		return b
	}
	numberIsNumber := pred(numberWrapper, numberWrapper.IsNumber)
	numberIsNumberObject := pred(numberWrapper, numberWrapper.IsNumberObject)
	numberIsObject := pred(numberWrapper, numberWrapper.IsObject)
	booleanIsBoolean := pred(booleanWrapper, booleanWrapper.IsBoolean)
	booleanIsBooleanObject := pred(booleanWrapper, booleanWrapper.IsBooleanObject)
	booleanObjectIsTrue := pred(booleanWrapper, booleanWrapper.IsTrue)
	stringIsString := pred(stringWrapper, stringWrapper.IsString)
	stringIsStringObject := pred(stringWrapper, stringWrapper.IsStringObject)
	stringIsName := pred(stringWrapper, stringWrapper.IsName)
	bigintIsBigInt := pred(bigintWrapper, bigintWrapper.IsBigInt)
	bigintIsBigIntObject := pred(bigintWrapper, bigintWrapper.IsBigIntObject)

	tc := r.tc(t)
	js := evalText(t, r, tc,
		"const nw = new Number(5), bw = new Boolean(false), sw = new String('ab'); "+
			"[typeof nw, nw + 1, nw.valueOf(), bw ? 1 : 0, sw.length, typeof sw].join('|')")
	_ = tc.Close()

	return wantGot("runtime-values/primitive_wrapper_objects",
		jobj(
			kv("number_is_number", jbool(false)),
			kv("number_is_number_object", jbool(true)),
			kv("number_is_object", jbool(true)),
			kv("boolean_is_boolean", jbool(false)),
			kv("boolean_is_boolean_object", jbool(true)),
			kv("boolean_object_is_true", jbool(false)),
			kv("string_is_string", jbool(false)),
			kv("string_is_string_object", jbool(true)),
			kv("string_is_name", jbool(false)),
			kv("bigint_is_big_int", jbool(false)),
			kv("bigint_is_big_int_object", jbool(true)),
			kv("number_to_string", jstr("5")),
			kv("boolean_to_string", jstr("false")),
			kv("string_to_string", jstr("ab")),
			kv("bigint_to_string", jstr("123")),
			kv("strict_wrapper_primitive", jbool(false)),
			kv("js", jstr("object|6|5|1|2|object")),
		),
		jobj(
			kv("number_is_number", jbool(numberIsNumber)),
			kv("number_is_number_object", jbool(numberIsNumberObject)),
			kv("number_is_object", jbool(numberIsObject)),
			kv("boolean_is_boolean", jbool(booleanIsBoolean)),
			kv("boolean_is_boolean_object", jbool(booleanIsBooleanObject)),
			kv("boolean_object_is_true", jbool(booleanObjectIsTrue)),
			kv("string_is_string", jbool(stringIsString)),
			kv("string_is_string_object", jbool(stringIsStringObject)),
			kv("string_is_name", jbool(stringIsName)),
			kv("bigint_is_big_int", jbool(bigintIsBigInt)),
			kv("bigint_is_big_int_object", jbool(bigintIsBigIntObject)),
			kv("number_to_string", jstr(numberToString)),
			kv("boolean_to_string", jstr(booleanToString)),
			kv("string_to_string", jstr(stringToString)),
			kv("bigint_to_string", jstr(bigintToString)),
			kv("strict_wrapper_primitive", jbool(strictWrapperPrimitive)),
			kv("js", jstr(js)),
		))
}

// --- 9. Property attributes / integrity / descriptors ---------------------------

func checkPropertyAttributesBits(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	obj, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	publish(t, r, "oa", obj.Value)

	plain := str(t, r, "plain")
	one := int32Val(t, r, 1)
	created := false
	if ok, err := obj.CreateDataProperty(r.scope, r.ctx, plain, one); err == nil {
		created = ok
	}
	plainAttrs, _, err := obj.GetPropertyAttributes(r.scope, r.ctx, plain)
	if err != nil {
		t.Fatalf("attributes(plain): %v", err)
	}

	ro := str(t, r, "ro")
	two := int32Val(t, r, 2)
	definedRO := false
	if ok, err := obj.DefineOwnProperty(r.scope, r.ctx, ro, two, gov8.AttrReadOnly); err == nil {
		definedRO = ok
	}
	roAttrs, _, err := obj.GetPropertyAttributes(r.scope, r.ctx, ro)
	if err != nil {
		t.Fatalf("attributes(ro): %v", err)
	}

	locked := str(t, r, "locked")
	three := int32Val(t, r, 3)
	if _, err := obj.DefineOwnProperty(r.scope, r.ctx, locked, three,
		gov8.AttrReadOnly|gov8.AttrDontEnum|gov8.AttrDontDelete); err != nil {
		t.Fatalf("define locked: %v", err)
	}
	lockedAttrs, _, err := obj.GetPropertyAttributes(r.scope, r.ctx, locked)
	if err != nil {
		t.Fatalf("attributes(locked): %v", err)
	}

	// Pinned nuance: a missing property yields Just(NONE), not an error.
	_, missingIsSome, err := obj.GetPropertyAttributes(r.scope, r.ctx, str(t, r, "missing"))
	if err != nil {
		t.Fatalf("attributes(missing): %v", err)
	}

	jsDescriptor := evalText(t, r, nil, "JSON.stringify(Object.getOwnPropertyDescriptor(oa, 'locked'))")
	jsWrite := evalText(t, r, nil, "(function(){ oa.locked = 99; return oa.locked; })()")
	jsDelete := evalText(t, r, nil, "delete oa.locked")
	jsKeys := evalText(t, r, nil, "JSON.stringify(Object.keys(oa))")

	return wantGot("runtime-values/property_attributes_bits",
		jobj(
			kv("create_ok", jbool(true)),
			kv("plain_attrs", jint(0)),
			kv("defined_ro", jbool(true)),
			kv("ro_attrs", jint(1)),
			kv("locked_attrs", jint(7)),
			kv("missing_is_some", jbool(true)),
			kv("js_descriptor", jstr(`{"value":3,"writable":false,"enumerable":false,"configurable":false}`)),
			kv("js_write_result", jstr("3")),
			kv("js_delete", jstr("false")),
			kv("js_keys", jstr("[\"plain\",\"ro\"]")),
		),
		jobj(
			kv("create_ok", jbool(created)),
			kv("plain_attrs", jint(int64(plainAttrs))),
			kv("defined_ro", jbool(definedRO)),
			kv("ro_attrs", jint(int64(roAttrs))),
			kv("locked_attrs", jint(int64(lockedAttrs))),
			kv("missing_is_some", jbool(missingIsSome)),
			kv("js_descriptor", jstr(jsDescriptor)),
			kv("js_write_result", jstr(jsWrite)),
			kv("js_delete", jstr(jsDelete)),
			kv("js_keys", jstr(jsKeys)),
		))
}

func checkIntegrityLevels(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	sealed, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	a := str(t, r, "a")
	one := int32Val(t, r, 1)
	if _, err := sealed.CreateDataProperty(r.scope, r.ctx, a, one); err != nil {
		t.Fatalf("create a: %v", err)
	}
	sealedOK := false
	if ok, err := sealed.SetIntegrityLevel(r.scope, r.ctx, gov8.IntegritySealed); err == nil {
		sealedOK = ok
	}
	sealedAttrs, _, err := sealed.GetPropertyAttributes(r.scope, r.ctx, a)
	if err != nil {
		t.Fatalf("sealed attrs: %v", err)
	}
	publish(t, r, "sl", sealed.Value)
	jsSealed := evalText(t, r, nil, "Object.isSealed(sl)")
	jsAdd := evalText(t, r, nil, "(function(){ sl.newProp = 1; return sl.newProp === undefined; })()")
	jsDelete := evalText(t, r, nil, "delete sl.a")

	frozen, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	b := str(t, r, "b")
	two := int32Val(t, r, 2)
	if _, err := frozen.CreateDataProperty(r.scope, r.ctx, b, two); err != nil {
		t.Fatalf("create b: %v", err)
	}
	frozenOK := false
	if ok, err := frozen.SetIntegrityLevel(r.scope, r.ctx, gov8.IntegrityFrozen); err == nil {
		frozenOK = ok
	}
	frozenAttrs, _, err := frozen.GetPropertyAttributes(r.scope, r.ctx, b)
	if err != nil {
		t.Fatalf("frozen attrs: %v", err)
	}
	publish(t, r, "fz", frozen.Value)
	jsFrozen := evalText(t, r, nil, "Object.isFrozen(fz)")
	jsWrite := evalText(t, r, nil, "(function(){ fz.b = 99; return fz.b; })()")

	return wantGot("runtime-values/integrity_levels",
		jobj(
			kv("sealed_ok", jbool(true)),
			kv("sealed_attrs", jint(4)),
			kv("js_is_sealed", jstr("true")),
			kv("js_add_silently_fails", jstr("true")),
			kv("js_delete", jstr("false")),
			kv("frozen_ok", jbool(true)),
			kv("frozen_attrs", jint(5)),
			kv("js_is_frozen", jstr("true")),
			kv("js_write_result", jstr("2")),
		),
		jobj(
			kv("sealed_ok", jbool(sealedOK)),
			kv("sealed_attrs", jint(int64(sealedAttrs))),
			kv("js_is_sealed", jstr(jsSealed)),
			kv("js_add_silently_fails", jstr(jsAdd)),
			kv("js_delete", jstr(jsDelete)),
			kv("frozen_ok", jbool(frozenOK)),
			kv("frozen_attrs", jint(int64(frozenAttrs))),
			kv("js_is_frozen", jstr(jsFrozen)),
			kv("js_write_result", jstr(jsWrite)),
		))
}

// describePD mirrors the oracle's describe closure: the presence flags and
// flag values of a descriptor.
func describePD(t *testing.T, pd *gov8.PropertyDescriptor) jsonValue {
	flags := func() map[string]bool {
		out := make(map[string]bool, 9)
		for name, fn := range map[string]func() (bool, error){
			"has_value":        pd.HasValue,
			"has_writable":     pd.HasWritable,
			"has_enumerable":   pd.HasEnumerable,
			"has_configurable": pd.HasConfigurable,
			"has_get":          pd.HasGet,
			"has_set":          pd.HasSet,
		} {
			b, err := fn()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			out[name] = b
		}
		return out
	}()
	writable, err := pd.Writable()
	if err != nil {
		t.Fatalf("Writable: %v", err)
	}
	enumerable, err := pd.Enumerable()
	if err != nil {
		t.Fatalf("Enumerable: %v", err)
	}
	configurable, err := pd.Configurable()
	if err != nil {
		t.Fatalf("Configurable: %v", err)
	}
	return jobj(
		kv("has_value", jbool(flags["has_value"])),
		kv("has_writable", jbool(flags["has_writable"])),
		kv("has_enumerable", jbool(flags["has_enumerable"])),
		kv("has_configurable", jbool(flags["has_configurable"])),
		kv("has_get", jbool(flags["has_get"])),
		kv("has_set", jbool(flags["has_set"])),
		kv("writable", jbool(writable)),
		kv("enumerable", jbool(enumerable)),
		kv("configurable", jbool(configurable)),
	)
}

func checkNativePropertyDescriptors(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	defaultPD, err := r.scope.NewPropertyDescriptor()
	if err != nil {
		t.Fatalf("NewPropertyDescriptor: %v", err)
	}
	defer func() { _ = defaultPD.Close() }()

	value := int32Val(t, r, 5)
	valuePD, err := r.scope.NewPropertyDescriptorFromValue(value)
	if err != nil {
		t.Fatalf("FromValue: %v", err)
	}
	defer func() { _ = valuePD.Close() }()
	valueIsFive := false
	if pdValue, err := valuePD.Value(); err == nil {
		if same, err := pdValue.StrictEquals(value); err == nil {
			valueIsFive = same
		}
	}

	writablePD, err := r.scope.NewPropertyDescriptorFromValueWritable(value, true)
	if err != nil {
		t.Fatalf("FromValueWritable: %v", err)
	}
	defer func() { _ = writablePD.Close() }()
	// Snapshot eagerly: Object::DefineProperty may backfill the descriptor's
	// fields when it is consumed (the pinned runner describes each flavor
	// before any define_property call).
	writableDescribed := describePD(t, writablePD)
	openPD, err := r.scope.NewPropertyDescriptorFromValueWritable(value, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = openPD.Close() }()
	if err := openPD.SetEnumerable(true); err != nil {
		t.Fatalf("SetEnumerable: %v", err)
	}
	if err := openPD.SetConfigurable(true); err != nil {
		t.Fatalf("SetConfigurable: %v", err)
	}
	openDescribed := describePD(t, openPD)

	getter := evalValue(t, r, nil, "(() => 7)")
	setter := evalValue(t, r, nil, "(() => {})")
	accessorPD, err := r.scope.NewPropertyDescriptorFromGetSet(getter, setter)
	if err != nil {
		t.Fatalf("FromGetSet: %v", err)
	}
	defer func() { _ = accessorPD.Close() }()
	accessorHasValue, err := accessorPD.HasValue()
	if err != nil {
		t.Fatalf("HasValue: %v", err)
	}
	accessorHasGet, err := accessorPD.HasGet()
	if err != nil {
		t.Fatalf("HasGet: %v", err)
	}
	accessorHasSet, err := accessorPD.HasSet()
	if err != nil {
		t.Fatalf("HasSet: %v", err)
	}
	pdGet, err := accessorPD.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	getSame := false
	if same, err := pdGet.StrictEquals(getter); err == nil {
		getSame = same
	}
	pdSet, err := accessorPD.Set()
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	setSame := false
	if same, err := pdSet.StrictEquals(setter); err == nil {
		setSame = same
	}

	// Effect through define_property: a descriptor with only value+writable
	// leaves enumerable/configurable at their spec defaults (false).
	target, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	defined := false
	if ok, err := target.DefineProperty(r.scope, r.ctx, str(t, r, "d"), writablePD); err == nil {
		defined = ok
	}
	roPD, err := r.scope.NewPropertyDescriptorFromValueWritable(value, false)
	if err != nil {
		t.Fatalf("ro descriptor: %v", err)
	}
	defer func() { _ = roPD.Close() }()
	definedRO := false
	if ok, err := target.DefineProperty(r.scope, r.ctx, str(t, r, "ro"), roPD); err == nil {
		definedRO = ok
	}
	publish(t, r, "dt", target.Value)
	jsDescriptor := evalText(t, r, nil, "JSON.stringify(Object.getOwnPropertyDescriptor(dt, 'ro'))")
	jsWrite := evalText(t, r, nil, "(function(){ dt.ro = 50; return dt.ro; })()")

	return wantGot("runtime-values/native_property_descriptors",
		jobj(
			kv("default", jobj(
				kv("has_value", jbool(false)), kv("has_writable", jbool(false)),
				kv("has_enumerable", jbool(false)), kv("has_configurable", jbool(false)),
				kv("has_get", jbool(false)), kv("has_set", jbool(false)),
				kv("writable", jbool(false)), kv("enumerable", jbool(false)),
				kv("configurable", jbool(false))),
			),
			kv("from_value", jobj(
				kv("has_value", jbool(true)), kv("has_writable", jbool(false)),
				kv("has_enumerable", jbool(false)), kv("has_configurable", jbool(false)),
				kv("has_get", jbool(false)), kv("has_set", jbool(false)),
				kv("writable", jbool(false)), kv("enumerable", jbool(false)),
				kv("configurable", jbool(false))),
			),
			kv("from_value_value_is_five", jbool(true)),
			kv("from_value_writable_true", jobj(
				kv("has_value", jbool(true)), kv("has_writable", jbool(true)),
				kv("has_enumerable", jbool(false)), kv("has_configurable", jbool(false)),
				kv("has_get", jbool(false)), kv("has_set", jbool(false)),
				kv("writable", jbool(true)), kv("enumerable", jbool(false)),
				kv("configurable", jbool(false))),
			),
			kv("after_setters", jobj(
				kv("has_value", jbool(true)), kv("has_writable", jbool(true)),
				kv("has_enumerable", jbool(true)), kv("has_configurable", jbool(true)),
				kv("has_get", jbool(false)), kv("has_set", jbool(false)),
				kv("writable", jbool(false)), kv("enumerable", jbool(true)),
				kv("configurable", jbool(true))),
			),
			kv("accessor", jobj(
				kv("has_value", jbool(false)),
				kv("has_get", jbool(true)),
				kv("has_set", jbool(true)),
				kv("get_same", jbool(true)),
				kv("set_same", jbool(true)),
			)),
			kv("defined", jbool(true)),
			kv("defined_ro", jbool(true)),
			kv("js_descriptor", jstr(`{"value":5,"writable":false,"enumerable":false,"configurable":false}`)),
			kv("js_write_result", jstr("5")),
		),
		jobj(
			kv("default", describePD(t, defaultPD)),
			kv("from_value", describePD(t, valuePD)),
			kv("from_value_value_is_five", jbool(valueIsFive)),
			kv("from_value_writable_true", writableDescribed),
			kv("after_setters", openDescribed),
			kv("accessor", jobj(
				kv("has_value", jbool(accessorHasValue)),
				kv("has_get", jbool(accessorHasGet)),
				kv("has_set", jbool(accessorHasSet)),
				kv("get_same", jbool(getSame)),
				kv("set_same", jbool(setSame)),
			)),
			kv("defined", jbool(defined)),
			kv("defined_ro", jbool(definedRO)),
			kv("js_descriptor", jstr(jsDescriptor)),
			kv("js_write_result", jstr(jsWrite)),
		))
}

func checkJsPropertyDescriptorView(t *testing.T) obs {
	r := newRuntime(t)
	defer r.close(t)

	obj, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	publish(t, r, "od", obj.Value)

	if _, err := obj.CreateDataProperty(r.scope, r.ctx, str(t, r, "data"), int32Val(t, r, 1)); err != nil {
		t.Fatalf("create data: %v", err)
	}
	if _, err := obj.DefineOwnProperty(r.scope, r.ctx, str(t, r, "hidden"), int32Val(t, r, 2), gov8.AttrDontEnum); err != nil {
		t.Fatalf("define hidden: %v", err)
	}
	evalText(t, r, nil, "Object.defineProperty(od, 'acc', {get(){ return 7; }, configurable: true})")

	ownDescriptor := func(key string) (gov8.Value, bool) {
		d, err := obj.GetOwnPropertyDescriptor(r.scope, r.ctx, str(t, r, key))
		if err != nil {
			return gov8.Value{}, false
		}
		return d, true
	}
	descriptorJSON := func(key string) string {
		d, ok := ownDescriptor(key)
		if !ok {
			return ""
		}
		return stringifyText(t, r, nil, d)
	}

	dataJSON := descriptorJSON("data")
	hiddenJSON := descriptorJSON("hidden")
	acc, accOK := ownDescriptor("acc")
	accJSON := ""
	if accOK {
		accJSON = stringifyText(t, r, nil, acc)
	}
	accessorKeys := ""
	accessorGetIsFunction := false
	if accOK {
		accObj := getObject(t, acc)
		names, err := accObj.GetPropertyNames(r.scope, r.ctx,
			gov8.KeyCollectionOwnOnly, gov8.PropertyFilterAllProperties,
			gov8.IndexFilterSkipIndices, gov8.KeyConversionConvertToString)
		if err != nil {
			t.Fatalf("GetPropertyNames(acc): %v", err)
		}
		var parts []string
		n, err := names.Length()
		if err != nil {
			t.Fatalf("Length: %v", err)
		}
		for i := int64(0); i < n; i++ {
			name, err := names.GetIndex(r.scope, r.ctx, uint32(i))
			if err != nil {
				t.Fatalf("names[%d]: %v", i, err)
			}
			parts = append(parts, textOf(t, r, name))
		}
		accessorKeys = "[" + strings.Join(parts, ",") + "]"
		getV, ok, err := accObj.GetByName(r.scope, r.ctx, "get")
		if err == nil && ok {
			if is, err := getV.IsFunction(); err == nil {
				accessorGetIsFunction = is
			}
		}
	}
	// Pinned nuance: a missing key resolves to the undefined value (a Some
	// result), not None.
	missingJSON := ""
	if d, ok := ownDescriptor("missing"); ok {
		missingJSON = stringifyText(t, r, nil, d)
	}

	// Property-name filters on an object with string, symbol and index keys.
	mixed := getObject(t, evalValue(t, r, nil, "({s: 1, [Symbol('y')]: 2, 42: 3})"))
	namesOf := func(filter gov8.PropertyFilter, conversion gov8.KeyConversionMode) string {
		names, err := mixed.GetPropertyNames(r.scope, r.ctx,
			gov8.KeyCollectionOwnOnly, filter,
			gov8.IndexFilterIncludeIndices, conversion)
		if err != nil {
			t.Fatalf("GetPropertyNames(mixed): %v", err)
		}
		var parts []string
		n, err := names.Length()
		if err != nil {
			t.Fatalf("Length: %v", err)
		}
		for i := int64(0); i < n; i++ {
			name, err := names.GetIndex(r.scope, r.ctx, uint32(i))
			if err != nil {
				t.Fatalf("names[%d]: %v", i, err)
			}
			parts = append(parts, stringifyText(t, r, nil, name))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	defaultNames := namesOf(gov8.PropertyFilterOnlyEnumerable|gov8.PropertyFilterSkipSymbols, gov8.KeyConversionKeepNumbers)
	withSymbols := namesOf(gov8.PropertyFilterOnlyEnumerable, gov8.KeyConversionKeepNumbers)
	stringsConverted := namesOf(gov8.PropertyFilterOnlyEnumerable|gov8.PropertyFilterSkipSymbols, gov8.KeyConversionConvertToString)

	return wantGot("runtime-values/js_property_descriptor_view",
		jobj(
			kv("data", jstr(`{"value":1,"writable":true,"enumerable":true,"configurable":true}`)),
			kv("hidden", jstr(`{"value":2,"writable":true,"enumerable":false,"configurable":true}`)),
			kv("accessor", jstr(`{"enumerable":false,"configurable":true}`)),
			kv("accessor_keys", jstr("[get,set,enumerable,configurable]")),
			kv("accessor_get_is_function", jbool(true)),
			kv("missing_stringify", jstr("undefined")),
			kv("names_default", jstr("[42,\"s\"]")),
			kv("names_with_symbols", jstr("[42,\"s\",undefined]")),
			kv("names_keys_converted", jstr("[\"42\",\"s\"]")),
		),
		jobj(
			kv("data", jstr(dataJSON)),
			kv("hidden", jstr(hiddenJSON)),
			kv("accessor", jstr(accJSON)),
			kv("accessor_keys", jstr(accessorKeys)),
			kv("accessor_get_is_function", jbool(accessorGetIsFunction)),
			kv("missing_stringify", jstr(missingJSON)),
			kv("names_default", jstr(defaultNames)),
			kv("names_with_symbols", jstr(withSymbols)),
			kv("names_keys_converted", jstr(stringsConverted)),
		))
}

// --- registry -------------------------------------------------------------------

type checkFn func(t *testing.T) obs

type runtimeValuesCheck struct {
	id string
	fn checkFn
}

// allRuntimeValuesChecks is the fixed oracle registry order
// (conformance-runtime-values.rs CHECKS), all 27 checks.
func allRuntimeValuesChecks() []runtimeValuesCheck {
	return []runtimeValuesCheck{
		// Date
		{"runtime-values/date_construction_and_value_of", checkDateConstructionAndValueOf},
		{"runtime-values/date_invalid_time_value_error", checkDateInvalidTimeValueError},
		// RegExp
		{"runtime-values/regexp_new_flags_and_source", checkRegExpNewFlagsAndSource},
		{"runtime-values/regexp_exec_and_last_index", checkRegExpExecAndLastIndex},
		{"runtime-values/regexp_invalid_pattern_error", checkRegExpInvalidPatternError},
		{"runtime-values/regexp_js_created_source", checkRegExpJsCreatedSource},
		// JSON
		{"runtime-values/json_parse_canonical", checkJSONParseCanonical},
		{"runtime-values/json_parse_errors", checkJSONParseErrors},
		{"runtime-values/json_stringify_objects", checkJSONStringifyObjects},
		{"runtime-values/json_stringify_boundaries", checkJSONStringifyBoundaries},
		// Array
		{"runtime-values/array_new_and_elements", checkArrayNewAndElements},
		{"runtime-values/array_index_semantics", checkArrayIndexSemantics},
		// Map / Set
		{"runtime-values/map_native_ops", checkMapNativeOps},
		{"runtime-values/set_native_ops", checkSetNativeOps},
		{"runtime-values/map_set_js_interop", checkMapSetJsInterop},
		// Proxy
		{"runtime-values/proxy_identity_and_default_traps", checkProxyIdentityAndDefaultTraps},
		{"runtime-values/proxy_revoke_semantics", checkProxyRevokeSemantics},
		{"runtime-values/proxy_trap_invariant_error", checkProxyTrapInvariantError},
		// Symbol / private keys
		{"runtime-values/symbol_identity_and_description", checkSymbolIdentityAndDescription},
		{"runtime-values/symbol_registry", checkSymbolRegistry},
		{"runtime-values/symbol_wellknown_interop", checkSymbolWellKnownInterop},
		{"runtime-values/private_symbol_invisibility", checkPrivateSymbolInvisibility},
		// primitive wrappers
		{"runtime-values/primitive_wrapper_objects", checkPrimitiveWrapperObjects},
		// property attributes / integrity / descriptors
		{"runtime-values/property_attributes_bits", checkPropertyAttributesBits},
		{"runtime-values/integrity_levels", checkIntegrityLevels},
		{"runtime-values/native_property_descriptors", checkNativePropertyDescriptors},
		{"runtime-values/js_property_descriptor_view", checkJsPropertyDescriptorView},
	}
}

// runtimeValuesCheckIDs lists the registry ids in the same order.
func runtimeValuesCheckIDs() []string {
	ids := make([]string, 0, 27)
	for _, c := range allRuntimeValuesChecks() {
		ids = append(ids, c.id)
	}
	return ids
}
