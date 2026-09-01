//go:build windows && amd64

package gov8_test

import (
	"math"
	"strings"
	"testing"

	gov8 "gov8"
)

// Behavior tests for the runtime-values slice, mirroring the pinned oracle's
// characterization (rust-oracle/src/bin/conformance-runtime-values.rs). The
// byte-exact conformance runner lives in conformance-runtime-values/; these
// tests cover the same observations plus the lifecycle, negative, and
// concurrency cases that must not abort the process.

// setGlobal publishes a value on the global object.
func setGlobal(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, name string, v gov8.Value) {
	t.Helper()
	g, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if _, err := g.SetByName(scope, ctx, name, v); err != nil {
		t.Fatalf("SetByName(%s): %v", name, err)
	}
}

// evalCaught runs source under a fresh TryCatch and reports the ToString of
// the completion value ("" on failure), whether an exception was caught, and
// the formatted exception message.
func evalCaught(t *testing.T, iso *gov8.Isolate, ctx *gov8.Context, scope *gov8.Scope, source string) (text string, caught bool, message string) {
	t.Helper()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	text, _ = evalTextValue(t, ctx, scope, tc, source)
	caught, err = tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	if caught {
		message, err = tc.MessageText(scope, ctx)
		if err != nil {
			t.Fatalf("MessageText: %v", err)
		}
	}
	return text, caught, message
}

// jsonText is the oracle's stringify_text: JSONStringify flattened to "".
func jsonText(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, v gov8.Value) string {
	t.Helper()
	out, err := gov8.JSONStringify(ctx, scope, v, nil)
	if err != nil {
		return ""
	}
	return stringifyResult(t, ctx, out)
}

// stringifyResult reads a JSONStringify result value (already the JSON text)
// as a Go string.
func stringifyResult(t *testing.T, ctx *gov8.Context, out gov8.Value) string {
	t.Helper()
	s, err := out.ToString(ctx)
	if err != nil {
		return ""
	}
	return s
}

// newObject evaluates "({})" and returns it as an Object.
func newObject(t *testing.T, ctx *gov8.Context, scope *gov8.Scope) *gov8.Object {
	t.Helper()
	v, ok := evalValue(t, ctx, scope, nil, "({})")
	if !ok {
		t.Fatal("eval ({}) failed")
	}
	o, err := gov8.AsObject(v)
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	return o
}

// getName reads a named property, failing the test when the getter throws
// or reports failure.
func getName(t *testing.T, o *gov8.Object, scope *gov8.Scope, ctx *gov8.Context, name string) gov8.Value {
	t.Helper()
	v, ok, err := o.GetByName(scope, ctx, name)
	if err != nil || !ok {
		t.Fatalf("get %s: ok=%v err=%v", name, ok, err)
	}
	return v
}

// mustString creates a JS string or fails the test.
func mustString(t *testing.T, scope *gov8.Scope, s string) gov8.Value {
	t.Helper()
	v, err := scope.NewString(s)
	if err != nil {
		t.Fatalf("NewString(%q): %v", s, err)
	}
	return v
}

// mustInt32 creates a JS integer or fails the test.
func mustInt32(t *testing.T, scope *gov8.Scope, v int32) gov8.Value {
	t.Helper()
	out, err := scope.Int32(v)
	if err != nil {
		t.Fatalf("Int32(%d): %v", v, err)
	}
	return out
}

// --- Date -----------------------------------------------------------------------

func TestDateConstructionAndValueOf(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	epoch, err := scope.NewDate(ctx, 0)
	if err != nil {
		t.Fatalf("NewDate(0): %v", err)
	}
	if is, err := epoch.IsDate(); err != nil || !is {
		t.Fatalf("IsDate = %v, %v; want true", is, err)
	}
	if is, err := epoch.IsObject(); err != nil || !is {
		t.Fatalf("IsObject = %v, %v; want true", is, err)
	}
	vo, err := epoch.ValueOf()
	if err != nil || vo != 0 {
		t.Fatalf("ValueOf = %v, %v; want 0", vo, err)
	}

	setGlobal(t, ctx, scope, "d", epoch.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "d.getTime()"); !ok || got != "0" {
		t.Fatalf("d.getTime() = %q, %v; want 0", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "d.toISOString()"); !ok || got != "1970-01-01T00:00:00.000Z" {
		t.Fatalf("toISOString = %q, %v", got, ok)
	}
	if _, ok := evalValue(t, ctx, scope, nil, "d.setUTCSeconds(30)"); !ok {
		t.Fatal("setUTCSeconds failed")
	}
	// JS mutation is reflected natively.
	if vo, err = epoch.ValueOf(); err != nil || vo != 30000 {
		t.Fatalf("ValueOf after mutation = %v, %v; want 30000", vo, err)
	}

	later, err := scope.NewDate(ctx, 1.5e12)
	if err != nil {
		t.Fatalf("NewDate(1.5e12): %v", err)
	}
	if vo, err = later.ValueOf(); err != nil || vo != 1.5e12 {
		t.Fatalf("ValueOf = %v, %v; want 1.5e12", vo, err)
	}

	invalid, err := scope.NewDate(ctx, math.NaN())
	if err != nil {
		t.Fatalf("NewDate(NaN): %v", err)
	}
	if vo, err = invalid.ValueOf(); err != nil || !math.IsNaN(vo) {
		t.Fatalf("ValueOf(NaN date) = %v, %v", vo, err)
	}
	setGlobal(t, ctx, scope, "di", invalid.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "Number.isNaN(di.getTime())"); !ok || got != "true" {
		t.Fatalf("Number.isNaN(di.getTime()) = %q, %v", got, ok)
	}

	jsCreated, ok := evalValue(t, ctx, scope, nil, "new Date(86400500)")
	if !ok {
		t.Fatal("eval new Date(86400500) failed")
	}
	if is, err := jsCreated.IsDate(); err != nil || !is {
		t.Fatalf("js-created IsDate = %v, %v", is, err)
	}
	d, err := gov8.AsDate(jsCreated)
	if err != nil {
		t.Fatalf("AsDate(js date): %v", err)
	}
	if vo, err = d.ValueOf(); err != nil || vo != 86400500 {
		t.Fatalf("js-created ValueOf = %v, %v; want 86400500", vo, err)
	}
}

func TestDateInvalidTimeValueError(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	_, caught, message := evalCaught(t, iso, ctx, scope, "new Date(NaN).toISOString()")
	if !caught {
		t.Fatal("expected RangeError")
	}
	if message != "Uncaught RangeError: Invalid time value" {
		t.Fatalf("message = %q", message)
	}
}

func TestDateNegativeCases(t *testing.T) {
	_, _, scope := newTestRuntime(t)

	if _, err := gov8.AsDate(gov8.Value{}); err == nil {
		t.Fatal("AsDate(zero) must fail")
	}
	b, err := scope.Boolean(true)
	if err != nil {
		t.Fatalf("Boolean: %v", err)
	}
	if _, err := gov8.AsDate(b); err == nil || !strings.Contains(err.Error(), "not a Date") {
		t.Fatalf("AsDate(boolean) = %v; want not-a-Date error", err)
	}
}

// --- RegExp -----------------------------------------------------------------------

func TestRegExpNewFlagsAndSource(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	pattern := mustString(t, scope, "a(b)c")
	re, err := scope.NewRegExp(ctx, pattern, gov8.RegExpGlobal|gov8.RegExpIgnoreCase, nil)
	if err != nil {
		t.Fatalf("NewRegExp: %v", err)
	}
	if is, err := re.IsRegExp(); err != nil || !is {
		t.Fatalf("IsRegExp = %v, %v", is, err)
	}
	source, err := re.GetSource(scope)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if s, err := source.StringValue(); err != nil || s != "a(b)c" {
		t.Fatalf("source = %q, %v", s, err)
	}
	setGlobal(t, ctx, scope, "re", re.Value)
	for _, tc := range [][2]string{
		{"re.flags", "gi"},
		{"re.global", "true"},
		{"re.ignoreCase", "true"},
		{"re.sticky", "false"},
		{"re.multiline", "false"},
		{"typeof re", "object"},
	} {
		if got, ok := evalTextValue(t, ctx, scope, nil, tc[0]); !ok || got != tc[1] {
			t.Errorf("%s = %q, %v; want %s", tc[0], got, ok, tc[1])
		}
	}
}

func TestRegExpExecAndLastIndex(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	pattern := mustString(t, scope, "a(b)c")
	subject := mustString(t, scope, "xxabcXXabc")
	re, err := scope.NewRegExp(ctx, pattern, gov8.RegExpGlobal, nil)
	if err != nil {
		t.Fatalf("NewRegExp: %v", err)
	}

	describe := func(m *gov8.Object) (match string, index int32, input string) {
		s := jsonText(t, ctx, scope, m.Value)
		idxV := getName(t, m, scope, ctx, "index")
		if err != nil {
			t.Fatalf("get index: %v", err)
		}
		idx, _, err := idxV.Int32Value(ctx)
		if err != nil {
			t.Fatalf("Int32Value: %v", err)
		}
		inV := getName(t, m, scope, ctx, "input")
		if err != nil {
			t.Fatalf("get input: %v", err)
		}
		in, err := inV.ToString(ctx)
		if err != nil {
			t.Fatalf("input ToString: %v", err)
		}
		return s, idx, in
	}

	m1, err := re.Exec(scope, ctx, subject)
	if err != nil {
		t.Fatalf("Exec 1: %v", err)
	}
	match, index, input := describe(m1)
	if match != "[\"abc\",\"b\"]" || index != 2 || input != "xxabcXXabc" {
		t.Fatalf("first = %q, %d, %q", match, index, input)
	}
	setGlobal(t, ctx, scope, "g", re.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "g.lastIndex"); !ok || got != "5" {
		t.Fatalf("lastIndex after first = %q, %v", got, ok)
	}

	m2, err := re.Exec(scope, ctx, subject)
	if err != nil {
		t.Fatalf("Exec 2: %v", err)
	}
	match, index, _ = describe(m2)
	if match != "[\"abc\",\"b\"]" || index != 7 {
		t.Fatalf("second = %q, %d", match, index)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "g.lastIndex"); !ok || got != "10" {
		t.Fatalf("lastIndex after second = %q, %v", got, ok)
	}

	// Pinned nuance: a failed global exec returns a non-nil result wrapping
	// the null value and resets lastIndex to 0; only a throw yields an error.
	m3, err := re.Exec(scope, ctx, subject)
	if err != nil {
		t.Fatalf("Exec 3: %v", err)
	}
	isNull, err := m3.IsNull()
	if err != nil || !isNull {
		t.Fatalf("third exec = %v, %v; want null", isNull, err)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "g.lastIndex"); !ok || got != "0" {
		t.Fatalf("lastIndex after fail = %q, %v", got, ok)
	}

	// Sticky: exec is anchored at lastIndex; a failed match resets it.
	spattern := mustString(t, scope, "x")
	ssubject := mustString(t, scope, "axxa")
	sticky, err := scope.NewRegExp(ctx, spattern, gov8.RegExpSticky, nil)
	if err != nil {
		t.Fatalf("NewRegExp sticky: %v", err)
	}
	m0, err := sticky.Exec(scope, ctx, ssubject)
	if err != nil {
		t.Fatalf("sticky exec at 0: %v", err)
	}
	if isNull, _ := m0.IsNull(); !isNull {
		t.Fatal("sticky exec at 0 must miss")
	}
	setGlobal(t, ctx, scope, "s", sticky.Value)
	if _, ok := evalValue(t, ctx, scope, nil, "s.lastIndex = 2"); !ok {
		t.Fatal("set lastIndex failed")
	}
	ms, err := sticky.Exec(scope, ctx, ssubject)
	if err != nil {
		t.Fatalf("sticky exec at 2: %v", err)
	}
	if isNull, _ := ms.IsNull(); isNull {
		t.Fatal("sticky exec at 2 must match")
	}
	idxV := getName(t, ms, scope, ctx, "index")
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	idx, _, err := idxV.Int32Value(ctx)
	if err != nil || idx != 2 {
		t.Fatalf("sticky index = %d, %v; want 2", idx, err)
	}
	me, err := sticky.Exec(scope, ctx, ssubject)
	if err != nil {
		t.Fatalf("sticky exec exhausted: %v", err)
	}
	if isNull, _ := me.IsNull(); !isNull {
		t.Fatal("sticky exec exhausted must miss")
	}
}

func TestRegExpInvalidPatternError(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	pattern := mustString(t, scope, "(")
	re, err := scope.NewRegExp(ctx, pattern, 0, tc)
	if err == nil || re != nil {
		t.Fatal("NewRegExp('(') must fail with nil result")
	}
	caught, err := tc.HasCaught()
	if err != nil || !caught {
		t.Fatalf("HasCaught = %v, %v; want true", caught, err)
	}
	nativeMessage, err := tc.MessageText(scope, ctx)
	if err != nil {
		t.Fatalf("MessageText: %v", err)
	}
	const want = "Uncaught SyntaxError: Invalid regular expression: /(/: Unterminated group"
	if nativeMessage != want {
		t.Fatalf("native message = %q", nativeMessage)
	}
	// The same TryCatch observes the JS constructor's error for the same
	// pattern with the identical message.
	if _, ok := evalTextValue(t, ctx, scope, tc, `new RegExp("(")`); ok {
		t.Fatal("JS new RegExp('(') must fail")
	}
	caught, err = tc.HasCaught()
	if err != nil || !caught {
		t.Fatalf("JS HasCaught = %v, %v", caught, err)
	}
	jsMessage, err := tc.MessageText(scope, ctx)
	if err != nil || jsMessage != want {
		t.Fatalf("js message = %q, %v", jsMessage, err)
	}
}

func TestRegExpNegativeCases(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	pattern := mustString(t, scope, "a")
	// Non-string pattern is refused before the engine is entered.
	number, err := scope.Number(1)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	if _, err := scope.NewRegExp(ctx, number, 0, nil); err == nil {
		t.Fatal("NewRegExp(number) must fail")
	}
	// Undefined flag bits are refused.
	if _, err := scope.NewRegExp(ctx, pattern, gov8.RegExpFlags(0x10000), nil); err == nil {
		t.Fatal("NewRegExp(bad flags) must fail")
	}
	re, err := scope.NewRegExp(ctx, pattern, 0, nil)
	if err != nil {
		t.Fatalf("NewRegExp: %v", err)
	}
	if _, err := re.Exec(scope, ctx, number); err == nil || !strings.Contains(err.Error(), "not a String") {
		t.Fatalf("Exec(non-string subject) = %v", err)
	}
	arr, err := scope.NewArray(ctx, 0)
	if err != nil {
		t.Fatalf("NewArray: %v", err)
	}
	if _, err := gov8.AsRegExp(arr.Value); err == nil {
		t.Fatal("AsRegExp(array) must fail")
	}
}

// --- JSON -----------------------------------------------------------------------

func TestJSONParseCanonical(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	roundtrip := func(source string) string {
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		text := mustString(t, scope, source)
		parsed, err := gov8.JSONParse(ctx, scope, text, tc)
		if err != nil {
			caught, _ := tc.HasCaught()
			t.Fatalf("JSONParse(%q): %v (caught=%v)", source, err, caught)
		}
		return jsonText(t, ctx, scope, parsed)
	}
	for _, c := range [][2]string{
		{`{"a":[1,2.5,"s",true,null],"b":{"c":1}}`, `{"a":[1,2.5,"s",true,null],"b":{"c":1}}`},
		{"[ 1 , 2 ]", "[1,2]"},
		{"-0", "0"},
		{"1e999", "null"},
		{"9007199254740993", "9007199254740992"},
		{`"\ud800"`, `"\ud800"`},
		{`"a\/\b\f\n\r\t\u0041"`, `"a/\b\f\n\r\tA"`},
	} {
		if got := roundtrip(c[0]); got != c[1] {
			t.Errorf("roundtrip(%q) = %q; want %q", c[0], got, c[1])
		}
	}
}

func TestJSONParseErrors(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	for _, c := range [][2]string{
		{"", "Uncaught SyntaxError: Unexpected end of JSON input"},
		{"{", "Uncaught SyntaxError: Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{"{'a':1}", "Uncaught SyntaxError: Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{"[1,2],3", "Uncaught SyntaxError: Unexpected non-whitespace character after JSON at position 5 (line 1 column 6)"},
		{"undefined", `Uncaught SyntaxError: "undefined" is not valid JSON`},
	} {
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		text := mustString(t, scope, c[0])
		_, perr := gov8.JSONParse(ctx, scope, text, tc)
		if perr == nil {
			t.Errorf("JSONParse(%q) must fail", c[0])
		}
		caught, err := tc.HasCaught()
		if err != nil || !caught {
			t.Errorf("JSONParse(%q): HasCaught = %v, %v", c[0], caught, err)
		}
		message, err := tc.MessageText(scope, ctx)
		if err != nil || message != c[1] {
			t.Errorf("JSONParse(%q) message = %q, %v; want %q", c[0], message, err, c[1])
		}
		_ = tc.Close()
	}
}

func TestJSONStringifyObjects(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	stringify := func(source string) string {
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		v, ok := evalValue(t, ctx, scope, tc, source)
		if !ok {
			t.Fatalf("eval %q failed", source)
		}
		out, err := gov8.JSONStringify(ctx, scope, v, tc)
		if err != nil {
			t.Fatalf("stringify %q: %v", source, err)
		}
		return stringifyResult(t, ctx, out)
	}
	for _, c := range [][2]string{
		{"({a: undefined, b: () => 1, c: [1, undefined, 2], d: null, e: 0})",
			`{"c":[1,null,2],"d":null,"e":0}`},
		{"const s = Symbol('k'); ({[s]: 1, ok: 2})", `{"ok":2}`},
		{"(function(){ const a = [1]; a[3] = 4; return a; })()", "[1,null,null,4]"},
		{`({q: "a\"b\\c\nd\te", f: "\u0001"})`, `{"q":"a\"b\\c\nd\te","f":"\u0001"}`},
		{"new Date(0)", `"1970-01-01T00:00:00.000Z"`},
		{"({toJSON: () => ({replaced: true}), ignored: 1})", `{"replaced":true}`},
		{`({o: {a: [[1, {b: "x"}]]}})`, `{"o":{"a":[[1,{"b":"x"}]]}}`},
	} {
		if got := stringify(c[0]); got != c[1] {
			t.Errorf("stringify(%q) = %q; want %q", c[0], got, c[1])
		}
	}
}

func TestJSONStringifyBoundaries(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	stringify := func(source string) (isNone bool, text string) {
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		v, ok := evalValue(t, ctx, scope, tc, source)
		if !ok {
			t.Fatalf("eval %q failed", source)
		}
		out, err := gov8.JSONStringify(ctx, scope, v, tc)
		if err != nil {
			caught, _ := tc.HasCaught()
			if !caught {
				t.Fatalf("stringify %q failed without a caught exception", source)
			}
			message, _ := tc.MessageText(scope, ctx)
			return true, "<caught> " + message
		}
		return false, stringifyResult(t, ctx, out)
	}
	// Pinned nuance: the C++ stringify renders top-level undefined /
	// functions / symbols as the literal string "undefined", not an empty
	// maybe.
	for _, c := range []struct {
		source string
		isNone bool
		want   string
	}{
		{"undefined", false, "undefined"},
		{"() => 1", false, "undefined"},
		{"NaN", false, "null"},
		{"Infinity", false, "null"},
		{"-Infinity", false, "null"},
		{"new Number(5)", false, "5"},
		{"new Boolean(false)", false, "false"},
		{`new String("ab")`, false, `"ab"`},
		{"Symbol('s')", false, "undefined"},
	} {
		isNone, got := stringify(c.source)
		if isNone != c.isNone || got != c.want {
			t.Errorf("stringify(%q) = (%v, %q); want (%v, %q)",
				c.source, isNone, got, c.isNone, c.want)
		}
	}
	isNone, got := stringify("const c = {}; c.self = c; c")
	if !isNone {
		t.Fatal("circular stringify must fail")
	}
	wantCircular := "<caught> Uncaught TypeError: Converting circular structure to JSON\n" +
		"    --> starting at object with constructor 'Object'\n" +
		"    --- property 'self' closes the circle"
	if got != wantCircular {
		t.Fatalf("circular = %q; want %q", got, wantCircular)
	}
}

// --- Array -----------------------------------------------------------------------

func TestArrayNewAndElements(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	three, err := scope.NewArray(ctx, 3)
	if err != nil {
		t.Fatalf("NewArray(3): %v", err)
	}
	if is, err := three.IsArray(); err != nil || !is {
		t.Fatalf("IsArray = %v, %v", is, err)
	}
	length, err := three.Length()
	if err != nil || length != 3 {
		t.Fatalf("Length = %d, %v", length, err)
	}
	hasZero, err := three.HasIndex(scope, ctx, 0)
	if err != nil || hasZero {
		t.Fatalf("HasIndex(0) = %v, %v; want false (holey)", hasZero, err)
	}
	if got := jsonText(t, ctx, scope, three.Value); got != "[null,null,null]" {
		t.Fatalf("stringify = %q", got)
	}

	// Pinned boundary: the native constructor collapses negative lengths to
	// an empty array instead of throwing.
	negative, err := scope.NewArray(ctx, -5)
	if err != nil {
		t.Fatalf("NewArray(-5): %v", err)
	}
	if length, err = negative.Length(); err != nil || length != 0 {
		t.Fatalf("negative Length = %d, %v; want 0", length, err)
	}

	elements, err := scope.NewArrayWithElements(ctx, []gov8.Value{
		mustInt32(t, scope, 1), mustInt32(t, scope, 2),
	})
	if err != nil {
		t.Fatalf("NewArrayWithElements: %v", err)
	}
	if length, err = elements.Length(); err != nil || length != 2 {
		t.Fatalf("elements Length = %d, %v", length, err)
	}
	if got := jsonText(t, ctx, scope, elements.Value); got != "[1,2]" {
		t.Fatalf("elements stringify = %q", got)
	}

	_, caught, message := evalCaught(t, iso, ctx, scope, "new Array(-1)")
	if !caught || message != "Uncaught RangeError: Invalid array length" {
		t.Fatalf("JS negative = %v, %q", caught, message)
	}
}

func TestArrayIndexSemantics(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	arr, err := scope.NewArray(ctx, 0)
	if err != nil {
		t.Fatalf("NewArray(0): %v", err)
	}
	setGlobal(t, ctx, scope, "a", arr.Value)
	b := mustString(t, scope, "b")
	if ok, err := arr.SetIndex(scope, ctx, 1, b); err != nil || !ok {
		t.Fatalf("SetIndex = %v, %v", ok, err)
	}
	length, err := arr.Length()
	if err != nil || length != 2 {
		t.Fatalf("length after set = %d, %v; want 2", length, err)
	}
	got, err := arr.GetIndex(scope, ctx, 1)
	if err != nil {
		t.Fatalf("GetIndex: %v", err)
	}
	if s, err := got.ToString(ctx); err != nil || s != "b" {
		t.Fatalf("got[1] = %q, %v", s, err)
	}
	if ok, err := arr.HasIndex(scope, ctx, 1); err != nil || !ok {
		t.Fatalf("HasIndex(1) = %v, %v", ok, err)
	}
	if ok, err := arr.HasIndex(scope, ctx, 2); err != nil || ok {
		t.Fatalf("HasIndex(2) = %v, %v", ok, err)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "a.push('pushed'); a.length"); !ok || got != "3" {
		t.Fatalf("push = %q, %v", got, ok)
	}
	if length, err = arr.Length(); err != nil || length != 3 {
		t.Fatalf("native length after push = %d, %v", length, err)
	}
	// Negative subscripts are plain named properties: not indices.
	want := "3|true|[null,\"b\",\"pushed\"]"
	if got, ok := evalTextValue(t, ctx, scope, nil, "a[-1] = 'neg'; [a.length, a.hasOwnProperty(-1), JSON.stringify(a)].join('|')"); !ok || got != want {
		t.Fatalf("negative subscript = %q, %v; want %q", got, ok, want)
	}
	if length, err = arr.Length(); err != nil || length != 3 {
		t.Fatalf("length after negative subscript = %d, %v", length, err)
	}
	if _, ok := evalValue(t, ctx, scope, nil, "(function(){ const mx = []; mx[4294967294] = 7; globalThis.mx = mx; return mx.length; })()"); !ok {
		t.Fatal("max index setup failed")
	}
	mxV, ok := evalValue(t, ctx, scope, nil, "mx")
	if !ok {
		t.Fatal("eval mx failed")
	}
	mx, err := gov8.AsArray(mxV)
	if err != nil {
		t.Fatalf("AsArray(mx): %v", err)
	}
	if length, err = mx.Length(); err != nil || length != 4294967295 {
		t.Fatalf("max length = %d, %v; want 4294967295", length, err)
	}
}

// --- Map / Set -------------------------------------------------------------------

func TestMapNativeOps(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	m, err := scope.NewMap(ctx)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	if is, err := m.IsMap(); err != nil || !is {
		t.Fatalf("IsMap = %v, %v", is, err)
	}
	size, err := m.Size()
	if err != nil || size != 0 {
		t.Fatalf("initial size = %d, %v", size, err)
	}
	keyA := mustString(t, scope, "a")
	one := mustInt32(t, scope, 1)
	returned, err := m.Set(scope, ctx, keyA, one)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if same, err := gov8.Same(returned.Value, m.Value); err != nil || !same {
		t.Fatalf("returned-is-same = %v, %v", same, err)
	}
	if ok, err := m.Has(scope, ctx, keyA); err != nil || !ok {
		t.Fatalf("Has(a) = %v, %v", ok, err)
	}
	got, err := m.Get(scope, ctx, keyA)
	if err != nil {
		t.Fatalf("Get(a): %v", err)
	}
	if s, err := got.ToString(ctx); err != nil || s != "1" {
		t.Fatalf("get a = %q, %v", s, err)
	}
	if size, err = m.Size(); err != nil || size != 1 {
		t.Fatalf("size one = %d, %v", size, err)
	}

	// NaN is a legal SameValueZero key.
	nan, err := scope.Number(math.NaN())
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	two := mustInt32(t, scope, 2)
	if _, err := m.Set(scope, ctx, nan, two); err != nil {
		t.Fatalf("Set(NaN): %v", err)
	}
	if ok, err := m.Has(scope, ctx, nan); err != nil || !ok {
		t.Fatalf("Has(NaN) = %v, %v", ok, err)
	}
	got, err = m.Get(scope, ctx, nan)
	if err != nil {
		t.Fatalf("Get(NaN): %v", err)
	}
	if s, err := got.ToString(ctx); err != nil || s != "2" {
		t.Fatalf("get NaN = %q, %v", s, err)
	}

	// Distinct objects are distinct keys; re-setting overwrites in place.
	k1 := newObject(t, ctx, scope)
	k2 := newObject(t, ctx, scope)
	three := mustInt32(t, scope, 3)
	four := mustInt32(t, scope, 4)
	nine := mustInt32(t, scope, 9)
	if _, err := m.Set(scope, ctx, k1.Value, three); err != nil {
		t.Fatalf("Set(k1): %v", err)
	}
	if _, err := m.Set(scope, ctx, k2.Value, four); err != nil {
		t.Fatalf("Set(k2): %v", err)
	}
	if size, err = m.Size(); err != nil || size != 4 {
		t.Fatalf("size with objects = %d, %v", size, err)
	}
	if _, err := m.Set(scope, ctx, k1.Value, nine); err != nil {
		t.Fatalf("overwrite k1: %v", err)
	}
	if size, err = m.Size(); err != nil || size != 4 {
		t.Fatalf("size after overwrite = %d, %v", size, err)
	}
	got, err = m.Get(scope, ctx, k1.Value)
	if err != nil {
		t.Fatalf("Get(k1): %v", err)
	}
	if s, err := got.ToString(ctx); err != nil || s != "9" {
		t.Fatalf("get k1 after overwrite = %q, %v", s, err)
	}

	deleted, err := m.Delete(scope, ctx, keyA)
	if err != nil || !deleted {
		t.Fatalf("Delete(a) = %v, %v", deleted, err)
	}
	if deleted, err = m.Delete(scope, ctx, keyA); err != nil || deleted {
		t.Fatalf("Delete(a) again = %v, %v", deleted, err)
	}

	ordered, err := scope.NewMap(ctx)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	bs := mustString(t, scope, "b")
	if _, err := ordered.Set(scope, ctx, keyA, one); err != nil {
		t.Fatalf("ordered set a: %v", err)
	}
	if _, err := ordered.Set(scope, ctx, bs, two); err != nil {
		t.Fatalf("ordered set b: %v", err)
	}
	arr, err := ordered.AsArray(scope, ctx)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if got := jsonText(t, ctx, scope, arr.Value); got != "[\"a\",1,\"b\",2]" {
		t.Fatalf("as_array = %q", got)
	}

	if err := m.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if size, err = m.Size(); err != nil || size != 0 {
		t.Fatalf("size after clear = %d, %v", size, err)
	}
	if ok, err := m.Has(scope, ctx, keyA); err != nil || ok {
		t.Fatalf("Has(a) after clear = %v, %v", ok, err)
	}
}

func TestSetNativeOps(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	s, err := scope.NewSet(ctx)
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	if is, err := s.IsSet(); err != nil || !is {
		t.Fatalf("IsSet = %v, %v", is, err)
	}
	size, err := s.Size()
	if err != nil || size != 0 {
		t.Fatalf("initial size = %d, %v", size, err)
	}
	x := mustString(t, scope, "x")
	returned, err := s.Add(scope, ctx, x)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if same, err := gov8.Same(returned.Value, s.Value); err != nil || !same {
		t.Fatalf("returned-is-same = %v, %v", same, err)
	}
	if _, err := s.Add(scope, ctx, x); err != nil {
		t.Fatalf("Add dup: %v", err)
	}
	if size, err = s.Size(); err != nil || size != 1 {
		t.Fatalf("size after dup = %d, %v", size, err)
	}
	nan, err := scope.Number(math.NaN())
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	if _, err := s.Add(scope, ctx, nan); err != nil {
		t.Fatalf("Add(NaN): %v", err)
	}
	if _, err := s.Add(scope, ctx, nan); err != nil {
		t.Fatalf("Add(NaN) dup: %v", err)
	}
	if size, err = s.Size(); err != nil || size != 2 {
		t.Fatalf("size after NaN dedup = %d, %v", size, err)
	}
	if ok, err := s.Has(scope, ctx, nan); err != nil || !ok {
		t.Fatalf("Has(NaN) = %v, %v", ok, err)
	}
	posZero, err := scope.Number(0)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	negZero, err := scope.Number(math.Copysign(0, -1))
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	if _, err := s.Add(scope, ctx, negZero); err != nil {
		t.Fatalf("Add(-0): %v", err)
	}
	if ok, err := s.Has(scope, ctx, posZero); err != nil || !ok {
		t.Fatalf("Has(+0) after Add(-0) = %v, %v", ok, err)
	}
	arr, err := s.AsArray(scope, ctx)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if got := jsonText(t, ctx, scope, arr.Value); got != "[\"x\",null,0]" {
		t.Fatalf("as_array = %q", got)
	}
	deleted, err := s.Delete(scope, ctx, x)
	if err != nil || !deleted {
		t.Fatalf("Delete(x) = %v, %v", deleted, err)
	}
	if deleted, err = s.Delete(scope, ctx, x); err != nil || deleted {
		t.Fatalf("Delete(x) again = %v, %v", deleted, err)
	}
	if size, err = s.Size(); err != nil || size != 2 {
		t.Fatalf("size after delete = %d, %v", size, err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if size, err = s.Size(); err != nil || size != 0 {
		t.Fatalf("size after clear = %d, %v", size, err)
	}
}

func TestMapSetJsInterop(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	jsMapV, ok := evalValue(t, ctx, scope, nil, `new Map([["a", 1], ["b", 2]])`)
	if !ok {
		t.Fatal("eval js map failed")
	}
	if is, err := jsMapV.IsMap(); err != nil || !is {
		t.Fatalf("is_map = %v, %v", is, err)
	}
	m, err := gov8.AsMap(jsMapV)
	if err != nil {
		t.Fatalf("AsMap: %v", err)
	}
	size, err := m.Size()
	if err != nil || size != 2 {
		t.Fatalf("size = %d, %v", size, err)
	}
	keyB := mustString(t, scope, "b")
	got, err := m.Get(scope, ctx, keyB)
	if err != nil {
		t.Fatalf("Get(b): %v", err)
	}
	if s, err := got.ToString(ctx); err != nil || s != "2" {
		t.Fatalf("get b = %q, %v", s, err)
	}
	typeofV, err := jsMapV.TypeOf(scope)
	if err != nil {
		t.Fatalf("TypeOf: %v", err)
	}
	if s, err := typeofV.ToString(ctx); err != nil || s != "object" {
		t.Fatalf("typeof = %q, %v", s, err)
	}

	native, err := scope.NewMap(ctx)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	tenStr := mustString(t, scope, "ten")
	twentyStr := mustString(t, scope, "twenty")
	if _, err := native.Set(scope, ctx, mustInt32(t, scope, 10), tenStr); err != nil {
		t.Fatalf("set 10: %v", err)
	}
	if _, err := native.Set(scope, ctx, mustInt32(t, scope, 20), twentyStr); err != nil {
		t.Fatalf("set 20: %v", err)
	}
	setGlobal(t, ctx, scope, "nm", native.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "JSON.stringify([...nm.entries()])"); !ok || got != `[[10,"ten"],[20,"twenty"]]` {
		t.Fatalf("js entries = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "nm instanceof Map"); !ok || got != "true" {
		t.Fatalf("instanceof = %q, %v", got, ok)
	}

	jsSetV, ok := evalValue(t, ctx, scope, nil, "(function(){ const s = new Set([1,2]); s.add(3); return s; })()")
	if !ok {
		t.Fatal("eval js set failed")
	}
	s, err := gov8.AsSet(jsSetV)
	if err != nil {
		t.Fatalf("AsSet: %v", err)
	}
	if size, err = s.Size(); err != nil || size != 3 {
		t.Fatalf("set size = %d, %v", size, err)
	}
	three := mustInt32(t, scope, 3)
	if ok, err := s.Has(scope, ctx, three); err != nil || !ok {
		t.Fatalf("set has 3 = %v, %v", ok, err)
	}
	arr, err := s.AsArray(scope, ctx)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if got := jsonText(t, ctx, scope, arr.Value); got != "[1,2,3]" {
		t.Fatalf("set as_array = %q", got)
	}
}

// --- Proxy -----------------------------------------------------------------------

func TestProxyIdentityAndDefaultTraps(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	targetV, ok := evalValue(t, ctx, scope, nil, "({x: 1})")
	if !ok {
		t.Fatal("eval target failed")
	}
	target, err := gov8.AsObject(targetV)
	if err != nil {
		t.Fatalf("AsObject(target): %v", err)
	}
	handler := newObject(t, ctx, scope)

	proxy, err := scope.NewProxy(ctx, target, handler)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	if is, err := proxy.IsProxy(); err != nil || !is {
		t.Fatalf("is_proxy = %v, %v", is, err)
	}
	if is, err := proxy.IsObject(); err != nil || !is {
		t.Fatalf("is_object = %v, %v", is, err)
	}
	gotTarget, err := proxy.GetTarget(scope)
	if err != nil {
		t.Fatalf("GetTarget: %v", err)
	}
	if same, err := gov8.Same(gotTarget, target.Value); err != nil || !same {
		t.Fatalf("target_same = %v, %v", same, err)
	}
	gotHandler, err := proxy.GetHandler(scope)
	if err != nil {
		t.Fatalf("GetHandler: %v", err)
	}
	if same, err := gov8.Same(gotHandler, handler.Value); err != nil || !same {
		t.Fatalf("handler_same = %v, %v", same, err)
	}
	if revoked, err := proxy.IsRevoked(); err != nil || revoked {
		t.Fatalf("not_revoked = %v, %v", revoked, err)
	}

	setGlobal(t, ctx, scope, "p", proxy.Value)
	gotX := getName(t, target, scope, ctx, "x")
	if s, err := gotX.ToString(ctx); err != nil || s != "1" {
		t.Fatalf("target_get_x = %q, %v", s, err)
	}
	proxyObj, err := gov8.AsObject(proxy.Value)
	if err != nil {
		t.Fatalf("AsObject(proxy): %v", err)
	}
	proxyGotX := getName(t, proxyObj, scope, ctx, "x")
	if s, err := proxyGotX.ToString(ctx); err != nil || s != "1" {
		t.Fatalf("proxy_get_x = %q, %v", s, err)
	}
	two := mustInt32(t, scope, 2)
	if ok, err := proxyObj.SetByName(scope, ctx, "y", two); err != nil || !ok {
		t.Fatalf("proxy set y = %v, %v", ok, err)
	}
	want := "1|2|true|{\"x\":1,\"y\":2}"
	if got, ok := evalTextValue(t, ctx, scope, nil, "[p.x, p.y, 'x' in p, JSON.stringify(p)].join('|')"); !ok || got != want {
		t.Fatalf("js_sees = %q, %v; want %q", got, ok, want)
	}
}

func TestProxyRevokeSemantics(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	targetV, ok := evalValue(t, ctx, scope, tc, "({x: 1})")
	if !ok {
		t.Fatal("eval target failed")
	}
	target, err := gov8.AsObject(targetV)
	if err != nil {
		t.Fatalf("AsObject(target): %v", err)
	}
	handler, err := gov8.AsObject(mustEval(t, ctx, scope, tc, "({})"))
	if err != nil {
		t.Fatalf("AsObject(handler): %v", err)
	}
	proxy, err := scope.NewProxy(ctx, target, handler)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	gotTarget, err := proxy.GetTarget(scope)
	if err != nil {
		t.Fatalf("GetTarget before revoke: %v", err)
	}
	if same, err := gov8.Same(gotTarget, target.Value); err != nil || !same {
		t.Fatalf("target_same_before = %v, %v", same, err)
	}
	if revoked, err := proxy.IsRevoked(); err != nil || revoked {
		t.Fatalf("not_revoked_before = %v, %v", revoked, err)
	}

	setGlobal(t, ctx, scope, "rp", proxy.Value)
	if err := proxy.Revoke(); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked, err := proxy.IsRevoked(); err != nil || !revoked {
		t.Fatalf("revoked_after = %v, %v", revoked, err)
	}

	proxyObj, err := gov8.AsObject(proxy.Value)
	if err != nil {
		t.Fatalf("AsObject(proxy): %v", err)
	}
	if _, ok, err := proxyObj.GetByName(scope, ctx, "x"); ok || err != nil {
		t.Fatalf("get after revoke = %v, %v; want failed", ok, err)
	}
	caught, err := tc.HasCaught()
	if err != nil || !caught {
		t.Fatalf("native_caught = %v, %v", caught, err)
	}
	message, err := tc.MessageText(scope, ctx)
	if err != nil || message != "Uncaught TypeError: Cannot perform 'get' on a proxy that has been revoked" {
		t.Fatalf("native_message = %q, %v", message, err)
	}

	// Pinned nuance: get_target still resolves after revoke, but to the
	// JavaScript null value.
	targetAfter, err := proxy.GetTarget(scope)
	if err != nil {
		t.Fatalf("GetTarget after revoke: %v", err)
	}
	if isUndef, err := targetAfter.IsUndefined(); err != nil || isUndef {
		t.Fatalf("target undefined after revoke = %v, %v", isUndef, err)
	}
	if isNull, err := targetAfter.IsNull(); err != nil || !isNull {
		t.Fatalf("target null after revoke = %v, %v", isNull, err)
	}
	_ = iso
}

func TestProxyTrapInvariantError(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	targetV, ok := evalValue(t, ctx, scope, tc, "({x: 1})")
	if !ok {
		t.Fatal("eval target failed")
	}
	target, err := gov8.AsObject(targetV)
	if err != nil {
		t.Fatalf("AsObject(target): %v", err)
	}
	handler, err := gov8.AsObject(mustEval(t, ctx, scope, tc, "({get: 1})"))
	if err != nil {
		t.Fatalf("AsObject(handler): %v", err)
	}
	proxy, err := scope.NewProxy(ctx, target, handler)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	proxyObj, err := gov8.AsObject(proxy.Value)
	if err != nil {
		t.Fatalf("AsObject(proxy): %v", err)
	}
	if _, ok, err := proxyObj.GetByName(scope, ctx, "x"); ok || err != nil {
		t.Fatalf("native get = %v, %v; want failed", ok, err)
	}
	caught, err := tc.HasCaught()
	if err != nil || !caught {
		t.Fatalf("caught = %v, %v", caught, err)
	}
	message, err := tc.MessageText(scope, ctx)
	if err != nil {
		t.Fatalf("MessageText: %v", err)
	}
	const want = "Uncaught TypeError: '1' returned for property 'get' of object '#<Object>' is not a function"
	if message != want {
		t.Fatalf("message = %q", message)
	}

	if got, ok := evalTextValue(t, ctx, scope, tc, "(function(){ const r = Proxy.revocable({a: 1}, {}); globalThis.rpr = r; r.revoke(); return 'revoked'; })()"); !ok || got != "revoked" {
		t.Fatalf("js_revocable = %q, %v", got, ok)
	}
	rprV, ok := evalValue(t, ctx, scope, nil, "rpr.proxy")
	if !ok {
		t.Fatal("eval rpr.proxy failed")
	}
	rp, err := gov8.AsProxy(rprV)
	if err != nil {
		t.Fatalf("AsProxy(rpr.proxy): %v", err)
	}
	if revoked, err := rp.IsRevoked(); err != nil || !revoked {
		t.Fatalf("js_revocable_is_revoked = %v, %v", revoked, err)
	}
}

// --- Symbol -----------------------------------------------------------------------

func TestSymbolIdentityAndDescription(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	desc := mustString(t, scope, "gov8")
	s1, err := scope.NewSymbol(desc)
	if err != nil {
		t.Fatalf("NewSymbol: %v", err)
	}
	s2, err := scope.NewSymbol(gov8.Value{})
	if err != nil {
		t.Fatalf("NewSymbol(anonymous): %v", err)
	}
	if is, err := s1.IsSymbol(); err != nil || !is {
		t.Fatalf("is_symbol = %v, %v", is, err)
	}
	descV, err := s1.Description(scope)
	if err != nil {
		t.Fatalf("Description: %v", err)
	}
	if s, err := descV.ToString(ctx); err != nil || s != "gov8" {
		t.Fatalf("description = %q, %v", s, err)
	}
	s2desc, err := s2.Description(scope)
	if err != nil {
		t.Fatalf("s2 Description: %v", err)
	}
	if isUndef, err := s2desc.IsUndefined(); err != nil || !isUndef {
		t.Fatalf("anonymous description undefined = %v, %v", isUndef, err)
	}
	typeofV, err := s1.TypeOf(scope)
	if err != nil {
		t.Fatalf("TypeOf: %v", err)
	}
	if s, err := typeofV.ToString(ctx); err != nil || s != "symbol" {
		t.Fatalf("typeof = %q, %v", s, err)
	}

	// Pinned nuance: ToString of a symbol throws; the exception is observed
	// by the active TryCatch and the value converts to "".
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	toStr, err := s1.ToStringTC(scope, ctx, tc)
	caught, err2 := tc.HasCaught()
	if err2 != nil {
		t.Fatalf("HasCaught: %v", err2)
	}
	if err == nil || !caught || toStr != "" {
		t.Fatalf("ToString(symbol) = %q, err=%v, caught=%v; want throw", toStr, err, caught)
	}
	_ = tc.Close()

	fresh, err := scope.NewSymbol(mustString(t, scope, "gov8"))
	if err != nil {
		t.Fatalf("NewSymbol(fresh): %v", err)
	}
	if same, err := s1.StrictEquals(fresh.Value); err != nil || same {
		t.Fatalf("fresh_symbols_differ = %v, %v", same, err)
	}

	setGlobal(t, ctx, scope, "sym1", s1.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "typeof sym1"); !ok || got != "symbol" {
		t.Fatalf("js typeof = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "String(sym1)"); !ok || got != "Symbol(gov8)" {
		t.Fatalf("js String = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "sym1.description"); !ok || got != "gov8" {
		t.Fatalf("js description = %q, %v", got, ok)
	}
}

func TestSymbolRegistry(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	d1 := mustString(t, scope, "gov8.slice")
	d2 := mustString(t, scope, "gov8.other")
	k1a, err := scope.SymbolForKey(d1)
	if err != nil {
		t.Fatalf("ForKey: %v", err)
	}
	k1b, err := scope.SymbolForKey(mustString(t, scope, "gov8.slice"))
	if err != nil {
		t.Fatalf("ForKey again: %v", err)
	}
	k2, err := scope.SymbolForKey(d2)
	if err != nil {
		t.Fatalf("ForKey other: %v", err)
	}
	a1, err := scope.SymbolForApi(mustString(t, scope, "gov8.slice"))
	if err != nil {
		t.Fatalf("ForApi: %v", err)
	}
	a1b, err := scope.SymbolForApi(mustString(t, scope, "gov8.slice"))
	if err != nil {
		t.Fatalf("ForApi again: %v", err)
	}

	same, err := k1a.StrictEquals(k1b.Value)
	if err != nil || !same {
		t.Fatalf("for_key_idempotent = %v, %v", same, err)
	}
	same, err = k1a.StrictEquals(k2.Value)
	if err != nil || same {
		t.Fatalf("different descriptions differ = %v, %v", same, err)
	}
	same, err = a1.StrictEquals(a1b.Value)
	if err != nil || !same {
		t.Fatalf("for_api_idempotent = %v, %v", same, err)
	}
	same, err = a1.StrictEquals(k1a.Value)
	if err != nil || same {
		t.Fatalf("for_api differs from for_key = %v, %v", same, err)
	}

	jsSym, ok := evalValue(t, ctx, scope, nil, "Symbol.for('gov8.slice')")
	if !ok {
		t.Fatal("eval Symbol.for failed")
	}
	setGlobal(t, ctx, scope, "symk", k1a.Value)
	same, err = k1a.StrictEquals(jsSym)
	if err != nil || !same {
		t.Fatalf("registry_matches_js = %v, %v", same, err)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "Symbol.keyFor(symk)"); !ok || got != "gov8.slice" {
		t.Fatalf("js keyFor = %q, %v", got, ok)
	}
	freshJS, ok := evalValue(t, ctx, scope, nil, "Symbol('gov8.slice')")
	if !ok {
		t.Fatal("eval fresh Symbol failed")
	}
	same, err = k1a.StrictEquals(freshJS)
	if err != nil || same {
		t.Fatalf("fresh js symbol differs = %v, %v", same, err)
	}
}

func TestSymbolWellKnownInterop(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	tagTarget, err := gov8.AsObject(mustEval(t, ctx, scope, nil, "({})"))
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	tag := mustString(t, scope, "Gov8")
	tagSym, err := scope.GetToStringTagSymbol()
	if err != nil {
		t.Fatalf("GetToStringTagSymbol: %v", err)
	}
	if ok, err := tagTarget.SetByKey(scope, ctx, tagSym.Value, tag); err != nil || !ok {
		t.Fatalf("set toStringTag = %v, %v", ok, err)
	}
	setGlobal(t, ctx, scope, "tagged", tagTarget.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "Object.prototype.toString.call(tagged)"); !ok || got != "[object Gov8]" {
		t.Fatalf("js_to_string_tag = %q, %v", got, ok)
	}

	iterable, err := gov8.AsObject(mustEval(t, ctx, scope, nil, "({length: 2, 0: 'a', 1: 'b'})"))
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	generator, ok := evalValue(t, ctx, scope, nil, "(function*(){ yield 1; yield 2; })")
	if !ok {
		t.Fatal("eval generator failed")
	}
	iterSym, err := scope.GetIteratorSymbol()
	if err != nil {
		t.Fatalf("GetIteratorSymbol: %v", err)
	}
	if ok, err := iterable.SetByKey(scope, ctx, iterSym.Value, generator); err != nil || !ok {
		t.Fatalf("set iterator = %v, %v", ok, err)
	}
	setGlobal(t, ctx, scope, "it", iterable.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "[...it].join('-')"); !ok || got != "1-2" {
		t.Fatalf("js_spread = %q, %v", got, ok)
	}

	// Symbol.hasInstance is non-writable on Function.prototype, so a plain
	// set is silently ignored; define_own_property creates the own property.
	ctor, err := gov8.AsObject(mustEval(t, ctx, scope, nil, "function C(){}; C"))
	if err != nil {
		t.Fatalf("AsObject(ctor): %v", err)
	}
	alwaysTrue, ok := evalValue(t, ctx, scope, nil, "() => true")
	if !ok {
		t.Fatal("eval predicate failed")
	}
	hiSym, err := scope.GetHasInstanceSymbol()
	if err != nil {
		t.Fatalf("GetHasInstanceSymbol: %v", err)
	}
	if ok, err := ctor.SetByKey(scope, ctx, hiSym.Value, alwaysTrue); err != nil || !ok {
		t.Fatalf("plain set hasInstance = %v, %v", ok, err)
	}
	gotAfterSet, err := ctor.GetByKey(scope, ctx, hiSym.Value)
	if err != nil {
		t.Fatalf("get hasInstance: %v", err)
	}
	if same, err := gotAfterSet.StrictEquals(alwaysTrue); err != nil || same {
		t.Fatalf("plain_set_ignored = %v, %v", same, err)
	}
	defined, err := ctor.DefineOwnProperty(scope, ctx, hiSym.Value, alwaysTrue, gov8.AttrNone)
	if err != nil || !defined {
		t.Fatalf("define hasInstance = %v, %v", defined, err)
	}
	setGlobal(t, ctx, scope, "C", ctor.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "({}) instanceof C"); !ok || got != "true" {
		t.Fatalf("js_instanceof = %q, %v", got, ok)
	}

	jsIter, ok := evalValue(t, ctx, scope, nil, "Symbol.iterator")
	if !ok {
		t.Fatal("eval Symbol.iterator failed")
	}
	same, err := iterSym.StrictEquals(jsIter)
	if err != nil || !same {
		t.Fatalf("iterator_identity = %v, %v", same, err)
	}
}

// --- Private -----------------------------------------------------------------------

func TestPrivateSymbolInvisibility(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	priv1, err := scope.NewPrivate(mustString(t, scope, "gov8.secret"))
	if err != nil {
		t.Fatalf("NewPrivate: %v", err)
	}
	nameV, err := priv1.Name(scope)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if s, err := nameV.ToString(ctx); err != nil || s != "gov8.secret" {
		t.Fatalf("name = %q, %v", s, err)
	}
	anonymous, err := scope.NewPrivate(gov8.Value{})
	if err != nil {
		t.Fatalf("NewPrivate(anonymous): %v", err)
	}
	anonName, err := anonymous.Name(scope)
	if err != nil {
		t.Fatalf("anonymous Name: %v", err)
	}
	if isUndef, err := anonName.IsUndefined(); err != nil || !isUndef {
		t.Fatalf("anonymous name undefined = %v, %v", isUndef, err)
	}

	obj, err := gov8.AsObject(mustEval(t, ctx, scope, nil, "({visible: 1})"))
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	value := mustInt32(t, scope, 42)
	if ok, err := obj.SetPrivate(scope, ctx, priv1, value); err != nil || !ok {
		t.Fatalf("SetPrivate = %v, %v", ok, err)
	}
	if ok, err := obj.HasPrivate(scope, ctx, priv1); err != nil || !ok {
		t.Fatalf("HasPrivate = %v, %v", ok, err)
	}
	got, err := obj.GetPrivate(scope, ctx, priv1)
	if err != nil {
		t.Fatalf("GetPrivate: %v", err)
	}
	if s, err := got.ToString(ctx); err != nil || s != "42" {
		t.Fatalf("get_private = %q, %v", s, err)
	}

	p2, err := scope.PrivateForApi(mustString(t, scope, "gov8.api"))
	if err != nil {
		t.Fatalf("PrivateForApi: %v", err)
	}
	p2b, err := scope.PrivateForApi(mustString(t, scope, "gov8.api"))
	if err != nil {
		t.Fatalf("PrivateForApi again: %v", err)
	}
	fresh, err := scope.PrivateForApi(mustString(t, scope, "gov8.api2"))
	if err != nil {
		t.Fatalf("PrivateForApi fresh: %v", err)
	}
	if same, err := gov8.Same(p2.Value, p2b.Value); err != nil || !same {
		t.Fatalf("for_api_idempotent = %v, %v", same, err)
	}
	if same, err := gov8.Same(p2.Value, fresh.Value); err != nil || same {
		t.Fatalf("for_api_distinct = %v, %v", same, err)
	}

	setGlobal(t, ctx, scope, "po", obj.Value)
	want := "{\"visible\":1}|1|false"
	if got, ok := evalTextValue(t, ctx, scope, nil, "[JSON.stringify(po), Object.keys(po).length, 'gov8.secret' in po].join('|')"); !ok || got != want {
		t.Fatalf("js_sees = %q, %v; want %q", got, ok, want)
	}

	deleteOK, err := obj.DeletePrivate(scope, ctx, priv1)
	if err != nil || !deleteOK {
		t.Fatalf("DeletePrivate = %v, %v", deleteOK, err)
	}
	if ok, err := obj.HasPrivate(scope, ctx, priv1); err != nil || ok {
		t.Fatalf("has after delete = %v, %v", ok, err)
	}
}

// --- primitive wrapper objects -------------------------------------------------------

func TestPrimitiveWrapperObjects(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	eval := func(src string) gov8.Value {
		v, ok := evalValue(t, ctx, scope, nil, src)
		if !ok {
			t.Fatalf("eval %q failed", src)
		}
		return v
	}
	numberWrapper := eval("new Number(5)")
	booleanWrapper := eval("new Boolean(false)")
	stringWrapper := eval("new String('ab')")
	bigintWrapper := eval("Object(123n)")
	primitive := eval("5")

	checkPred := func(name string, v gov8.Value, want bool, is func() (bool, error)) {
		t.Helper()
		got, err := is()
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", name, got, err, want)
		}
	}
	checkPred("number_is_number", numberWrapper, false, numberWrapper.IsNumber)
	checkPred("number_is_number_object", numberWrapper, true, numberWrapper.IsNumberObject)
	checkPred("number_is_object", numberWrapper, true, numberWrapper.IsObject)
	checkPred("boolean_is_boolean", booleanWrapper, false, booleanWrapper.IsBoolean)
	checkPred("boolean_is_boolean_object", booleanWrapper, true, booleanWrapper.IsBooleanObject)
	// Wrappers are always truthy for ToBoolean, but new Boolean(false) is
	// not the primitive true.
	checkPred("boolean_object_is_true", booleanWrapper, false, booleanWrapper.IsTrue)
	if b, err := booleanWrapper.BooleanValue(); err != nil || !b {
		t.Fatalf("ToBoolean(new Boolean(false)) = %v, %v; want true", b, err)
	}
	checkPred("string_is_string", stringWrapper, false, stringWrapper.IsString)
	checkPred("string_is_string_object", stringWrapper, true, stringWrapper.IsStringObject)
	checkPred("string_is_name", stringWrapper, false, stringWrapper.IsName)
	checkPred("bigint_is_big_int", bigintWrapper, false, bigintWrapper.IsBigInt)
	checkPred("bigint_is_big_int_object", bigintWrapper, true, bigintWrapper.IsBigIntObject)

	for _, c := range [][2]string{
		{"number_to_string", "5"},
		{"boolean_to_string", "false"},
		{"string_to_string", "ab"},
		{"bigint_to_string", "123"},
	} {
		var v gov8.Value
		switch c[0] {
		case "number_to_string":
			v = numberWrapper
		case "boolean_to_string":
			v = booleanWrapper
		case "string_to_string":
			v = stringWrapper
		default:
			v = bigintWrapper
		}
		s, err := v.ToString(ctx)
		if err != nil || s != c[1] {
			t.Errorf("%s = %q, %v; want %s", c[0], s, err, c[1])
		}
	}
	if same, err := numberWrapper.StrictEquals(primitive); err != nil || same {
		t.Fatalf("strict_wrapper_primitive = %v, %v", same, err)
	}
	want := "object|6|5|1|2|object"
	if got, ok := evalTextValue(t, ctx, scope, nil,
		"const nw = new Number(5), bw = new Boolean(false), sw = new String('ab'); "+
			"[typeof nw, nw + 1, nw.valueOf(), bw ? 1 : 0, sw.length, typeof sw].join('|')"); !ok || got != want {
		t.Fatalf("js = %q, %v; want %q", got, ok, want)
	}
}

// --- property attributes and integrity ------------------------------------------------

func TestPropertyAttributesBits(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	obj, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	setGlobal(t, ctx, scope, "oa", obj.Value)

	plain := mustString(t, scope, "plain")
	one := mustInt32(t, scope, 1)
	created, err := obj.CreateDataProperty(scope, ctx, plain, one)
	if err != nil || !created {
		t.Fatalf("CreateDataProperty = %v, %v", created, err)
	}
	attrs, present, err := obj.GetPropertyAttributes(scope, ctx, plain)
	if err != nil || !present || attrs != gov8.AttrNone {
		t.Fatalf("plain attrs = %v, %v, %v; want 0", attrs, present, err)
	}

	ro := mustString(t, scope, "ro")
	two := mustInt32(t, scope, 2)
	defined, err := obj.DefineOwnProperty(scope, ctx, ro, two, gov8.AttrReadOnly)
	if err != nil || !defined {
		t.Fatalf("DefineOwnProperty(ro) = %v, %v", defined, err)
	}
	attrs, present, err = obj.GetPropertyAttributes(scope, ctx, ro)
	if err != nil || !present || attrs != gov8.AttrReadOnly {
		t.Fatalf("ro attrs = %v, %v, %v; want 1", attrs, present, err)
	}

	locked := mustString(t, scope, "locked")
	three := mustInt32(t, scope, 3)
	defined, err = obj.DefineOwnProperty(scope, ctx, locked, three,
		gov8.AttrReadOnly|gov8.AttrDontEnum|gov8.AttrDontDelete)
	if err != nil || !defined {
		t.Fatalf("DefineOwnProperty(locked) = %v, %v", defined, err)
	}
	attrs, present, err = obj.GetPropertyAttributes(scope, ctx, locked)
	if err != nil || !present || attrs != gov8.AttrReadOnly|gov8.AttrDontEnum|gov8.AttrDontDelete {
		t.Fatalf("locked attrs = %v, %v, %v; want 7", attrs, present, err)
	}

	// Pinned nuance: a missing property yields Just(NONE), not an error.
	attrs, present, err = obj.GetPropertyAttributes(scope, ctx, mustString(t, scope, "missing"))
	if err != nil || !present || attrs != gov8.AttrNone {
		t.Fatalf("missing = %v, %v, %v; want (0, true, nil)", attrs, present, err)
	}

	wantDesc := `{"value":3,"writable":false,"enumerable":false,"configurable":false}`
	if got, ok := evalTextValue(t, ctx, scope, nil, "JSON.stringify(Object.getOwnPropertyDescriptor(oa, 'locked'))"); !ok || got != wantDesc {
		t.Fatalf("js_descriptor = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "(function(){ oa.locked = 99; return oa.locked; })()"); !ok || got != "3" {
		t.Fatalf("js_write = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "delete oa.locked"); !ok || got != "false" {
		t.Fatalf("js_delete = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "JSON.stringify(Object.keys(oa))"); !ok || got != `["plain","ro"]` {
		t.Fatalf("js_keys = %q, %v", got, ok)
	}
}

func TestIntegrityLevels(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	sealed, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	a := mustString(t, scope, "a")
	if ok, err := sealed.CreateDataProperty(scope, ctx, a, mustInt32(t, scope, 1)); err != nil || !ok {
		t.Fatalf("create a = %v, %v", ok, err)
	}
	if ok, err := sealed.SetIntegrityLevel(scope, ctx, gov8.IntegritySealed); err != nil || !ok {
		t.Fatalf("seal = %v, %v", ok, err)
	}
	attrs, present, err := sealed.GetPropertyAttributes(scope, ctx, a)
	if err != nil || !present || attrs != gov8.AttrDontDelete {
		t.Fatalf("sealed attrs = %v, %v, %v; want 4", attrs, present, err)
	}
	setGlobal(t, ctx, scope, "sl", sealed.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "Object.isSealed(sl)"); !ok || got != "true" {
		t.Fatalf("js_is_sealed = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "(function(){ sl.newProp = 1; return sl.newProp === undefined; })()"); !ok || got != "true" {
		t.Fatalf("js_add = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "delete sl.a"); !ok || got != "false" {
		t.Fatalf("js_delete = %q, %v", got, ok)
	}

	frozen, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	b := mustString(t, scope, "b")
	if ok, err := frozen.CreateDataProperty(scope, ctx, b, mustInt32(t, scope, 2)); err != nil || !ok {
		t.Fatalf("create b = %v, %v", ok, err)
	}
	if ok, err := frozen.SetIntegrityLevel(scope, ctx, gov8.IntegrityFrozen); err != nil || !ok {
		t.Fatalf("freeze = %v, %v", ok, err)
	}
	attrs, present, err = frozen.GetPropertyAttributes(scope, ctx, b)
	if err != nil || !present || attrs != gov8.AttrReadOnly|gov8.AttrDontDelete {
		t.Fatalf("frozen attrs = %v, %v, %v; want 5", attrs, present, err)
	}
	setGlobal(t, ctx, scope, "fz", frozen.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "Object.isFrozen(fz)"); !ok || got != "true" {
		t.Fatalf("js_is_frozen = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "(function(){ fz.b = 99; return fz.b; })()"); !ok || got != "2" {
		t.Fatalf("js_write = %q, %v", got, ok)
	}
}

// --- property descriptors ----------------------------------------------------------------

func TestNativePropertyDescriptors(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	value := mustInt32(t, scope, 5)

	defaultPD, err := scope.NewPropertyDescriptor()
	if err != nil {
		t.Fatalf("NewPropertyDescriptor: %v", err)
	}
	defer func() { _ = defaultPD.Close() }()
	for name, want := range map[string]bool{
		"has_value": false, "has_writable": false, "has_enumerable": false,
		"has_configurable": false, "has_get": false, "has_set": false,
	} {
		got, err := boolPDFlag(defaultPD, name)
		if err != nil || got != want {
			t.Errorf("default %s = %v, %v; want %v", name, got, err, want)
		}
	}

	valuePD, err := scope.NewPropertyDescriptorFromValue(value)
	if err != nil {
		t.Fatalf("FromValue: %v", err)
	}
	defer func() { _ = valuePD.Close() }()
	if has, err := valuePD.HasValue(); err != nil || !has {
		t.Fatalf("from_value has_value = %v, %v", has, err)
	}
	pdValue, err := valuePD.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if same, err := pdValue.StrictEquals(value); err != nil || !same {
		t.Fatalf("from_value_value_is_five = %v, %v", same, err)
	}

	writablePD, err := scope.NewPropertyDescriptorFromValueWritable(value, true)
	if err != nil {
		t.Fatalf("FromValueWritable: %v", err)
	}
	defer func() { _ = writablePD.Close() }()
	if has, err := writablePD.HasWritable(); err != nil || !has {
		t.Fatalf("writable has_writable = %v, %v", has, err)
	}
	if w, err := writablePD.Writable(); err != nil || !w {
		t.Fatalf("writable = %v, %v", w, err)
	}

	openPD, err := scope.NewPropertyDescriptorFromValueWritable(value, false)
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
	if e, err := openPD.Enumerable(); err != nil || !e {
		t.Fatalf("open enumerable = %v, %v", e, err)
	}
	if c, err := openPD.Configurable(); err != nil || !c {
		t.Fatalf("open configurable = %v, %v", c, err)
	}

	getter, ok := evalValue(t, ctx, scope, nil, "(() => 7)")
	if !ok {
		t.Fatal("eval getter failed")
	}
	setter, ok := evalValue(t, ctx, scope, nil, "(() => {})")
	if !ok {
		t.Fatal("eval setter failed")
	}
	accessorPD, err := scope.NewPropertyDescriptorFromGetSet(getter, setter)
	if err != nil {
		t.Fatalf("FromGetSet: %v", err)
	}
	defer func() { _ = accessorPD.Close() }()
	if has, err := accessorPD.HasValue(); err != nil || has {
		t.Fatalf("accessor has_value = %v, %v", has, err)
	}
	if has, err := accessorPD.HasGet(); err != nil || !has {
		t.Fatalf("accessor has_get = %v, %v", has, err)
	}
	if has, err := accessorPD.HasSet(); err != nil || !has {
		t.Fatalf("accessor has_set = %v, %v", has, err)
	}
	pdGet, err := accessorPD.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if same, err := pdGet.StrictEquals(getter); err != nil || !same {
		t.Fatalf("get_same = %v, %v", same, err)
	}
	pdSet, err := accessorPD.Set()
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if same, err := pdSet.StrictEquals(setter); err != nil || !same {
		t.Fatalf("set_same = %v, %v", same, err)
	}

	// Effect through DefineProperty: a descriptor with only value+writable
	// leaves enumerable/configurable at their spec defaults (false).
	target, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	defined, err := target.DefineProperty(scope, ctx, mustString(t, scope, "d"), writablePD)
	if err != nil || !defined {
		t.Fatalf("DefineProperty(d) = %v, %v", defined, err)
	}
	roPD, err := scope.NewPropertyDescriptorFromValueWritable(value, false)
	if err != nil {
		t.Fatalf("ro descriptor: %v", err)
	}
	defer func() { _ = roPD.Close() }()
	defined, err = target.DefineProperty(scope, ctx, mustString(t, scope, "ro"), roPD)
	if err != nil || !defined {
		t.Fatalf("DefineProperty(ro) = %v, %v", defined, err)
	}
	setGlobal(t, ctx, scope, "dt", target.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "JSON.stringify(Object.getOwnPropertyDescriptor(dt, 'ro'))"); !ok || got != `{"value":5,"writable":false,"enumerable":false,"configurable":false}` {
		t.Fatalf("js_descriptor = %q, %v", got, ok)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "(function(){ dt.ro = 50; return dt.ro; })()"); !ok || got != "5" {
		t.Fatalf("js_write = %q, %v", got, ok)
	}
}

func boolPDFlag(pd *gov8.PropertyDescriptor, name string) (bool, error) {
	switch name {
	case "has_value":
		return pd.HasValue()
	case "has_writable":
		return pd.HasWritable()
	case "has_enumerable":
		return pd.HasEnumerable()
	case "has_configurable":
		return pd.HasConfigurable()
	case "has_get":
		return pd.HasGet()
	case "has_set":
		return pd.HasSet()
	}
	return false, nil
}

func TestJsPropertyDescriptorView(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	obj, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	setGlobal(t, ctx, scope, "od", obj.Value)

	if ok, err := obj.CreateDataProperty(scope, ctx, mustString(t, scope, "data"), mustInt32(t, scope, 1)); err != nil || !ok {
		t.Fatalf("create data = %v, %v", ok, err)
	}
	if ok, err := obj.DefineOwnProperty(scope, ctx, mustString(t, scope, "hidden"), mustInt32(t, scope, 2), gov8.AttrDontEnum); err != nil || !ok {
		t.Fatalf("define hidden = %v, %v", ok, err)
	}
	if _, ok := evalValue(t, ctx, scope, nil, "Object.defineProperty(od, 'acc', {get(){ return 7; }, configurable: true})"); !ok {
		t.Fatal("define acc failed")
	}

	descriptor := func(key string) string {
		d, err := obj.GetOwnPropertyDescriptor(scope, ctx, mustString(t, scope, key))
		if err != nil {
			t.Fatalf("descriptor(%s): %v", key, err)
		}
		return jsonText(t, ctx, scope, d)
	}
	if got := descriptor("data"); got != `{"value":1,"writable":true,"enumerable":true,"configurable":true}` {
		t.Fatalf("data = %q", got)
	}
	if got := descriptor("hidden"); got != `{"value":2,"writable":true,"enumerable":false,"configurable":true}` {
		t.Fatalf("hidden = %q", got)
	}
	accJSON := descriptor("acc")
	if accJSON != `{"enumerable":false,"configurable":true}` {
		t.Fatalf("accessor = %q", accJSON)
	}

	acc, err := obj.GetOwnPropertyDescriptor(scope, ctx, mustString(t, scope, "acc"))
	if err != nil {
		t.Fatalf("descriptor(acc): %v", err)
	}
	accObj, err := gov8.AsObject(acc)
	if err != nil {
		t.Fatalf("AsObject(acc descriptor): %v", err)
	}
	names, err := accObj.GetPropertyNames(scope, ctx,
		gov8.KeyCollectionOwnOnly, gov8.PropertyFilterAllProperties,
		gov8.IndexFilterSkipIndices, gov8.KeyConversionConvertToString)
	if err != nil {
		t.Fatalf("GetPropertyNames(acc): %v", err)
	}
	parts := descriptorNames(t, ctx, scope, names)
	if got := "[" + strings.Join(parts, ",") + "]"; got != "[get,set,enumerable,configurable]" {
		t.Fatalf("accessor_keys = %q", got)
	}
	getV := getName(t, accObj, scope, ctx, "get")
	if err != nil {
		t.Fatalf("get 'get': %v", err)
	}
	if is, err := getV.IsFunction(); err != nil || !is {
		t.Fatalf("accessor_get_is_function = %v, %v", is, err)
	}

	// Pinned nuance: a missing key resolves to the undefined value (not an
	// error).
	missing, err := obj.GetOwnPropertyDescriptor(scope, ctx, mustString(t, scope, "missing"))
	if err != nil {
		t.Fatalf("descriptor(missing): %v", err)
	}
	if isUndef, err := missing.IsUndefined(); err != nil || !isUndef {
		t.Fatalf("missing descriptor undefined = %v, %v", isUndef, err)
	}
	if got := jsonText(t, ctx, scope, missing); got != "undefined" {
		t.Fatalf("missing stringify = %q; want undefined", got)
	}

	mixed, err := gov8.AsObject(mustEval(t, ctx, scope, nil, "({s: 1, [Symbol('y')]: 2, 42: 3})"))
	if err != nil {
		t.Fatalf("AsObject(mixed): %v", err)
	}

	defaultNames, err := mixed.GetPropertyNames(scope, ctx,
		gov8.KeyCollectionOwnOnly,
		gov8.PropertyFilterOnlyEnumerable|gov8.PropertyFilterSkipSymbols,
		gov8.IndexFilterIncludeIndices, gov8.KeyConversionKeepNumbers)
	if err != nil {
		t.Fatalf("names default: %v", err)
	}
	withSymbols, err := mixed.GetPropertyNames(scope, ctx,
		gov8.KeyCollectionOwnOnly, gov8.PropertyFilterOnlyEnumerable,
		gov8.IndexFilterIncludeIndices, gov8.KeyConversionKeepNumbers)
	if err != nil {
		t.Fatalf("names with symbols: %v", err)
	}
	stringsConverted, err := mixed.GetPropertyNames(scope, ctx,
		gov8.KeyCollectionOwnOnly,
		gov8.PropertyFilterOnlyEnumerable|gov8.PropertyFilterSkipSymbols,
		gov8.IndexFilterIncludeIndices, gov8.KeyConversionConvertToString)
	if err != nil {
		t.Fatalf("names converted: %v", err)
	}

	render := func(names *gov8.Array) string {
		t.Helper()
		n, err := names.Length()
		if err != nil {
			t.Fatalf("names length: %v", err)
		}
		var parts []string
		for i := int64(0); i < n; i++ {
			name, err := names.GetIndex(scope, ctx, uint32(i))
			if err != nil {
				t.Fatalf("names[%d]: %v", i, err)
			}
			parts = append(parts, jsonText(t, ctx, scope, name))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	if got := render(defaultNames); got != "[42,\"s\"]" {
		t.Fatalf("names_default = %q", got)
	}
	if got := render(withSymbols); got != "[42,\"s\",undefined]" {
		t.Fatalf("names_with_symbols = %q", got)
	}
	if got := render(stringsConverted); got != "[\"42\",\"s\"]" {
		t.Fatalf("names_keys_converted = %q", got)
	}
}

func descriptorNames(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, names *gov8.Array) []string {
	t.Helper()
	n, err := names.Length()
	if err != nil {
		t.Fatalf("names length: %v", err)
	}
	var parts []string
	for i := int64(0); i < n; i++ {
		name, err := names.GetIndex(scope, ctx, uint32(i))
		if err != nil {
			t.Fatalf("names[%d]: %v", i, err)
		}
		s, err := name.ToString(ctx)
		if err != nil {
			t.Fatalf("name ToString: %v", err)
		}
		parts = append(parts, s)
	}
	return parts
}

// --- lifecycle / negative / concurrency ---------------------------------------------

func TestRuntimeValuesUseAfterScopeClose(t *testing.T) {
	iso, ctx, scope, cleanup := newManualRuntime(t)
	defer cleanup(t)

	re, err := scope.NewRegExp(ctx, mustString(t, scope, "a"), 0, nil)
	if err != nil {
		t.Fatalf("NewRegExp: %v", err)
	}
	m, err := scope.NewMap(ctx)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("scope.Close: %v", err)
	}

	if _, err := re.GetSource(scope); err == nil {
		t.Fatal("GetSource after scope close must fail")
	}
	if _, err := m.Size(); err == nil {
		t.Fatal("Map.Size after scope close must fail")
	}
	_ = iso
}

func TestRuntimeValuesForeignIsolate(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	_, ctx2, scope2 := newTestRuntime(t)

	m, err := scope.NewMap(ctx)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	key2 := mustString(t, scope2, "a")
	if _, err := m.Has(scope, ctx, key2); err == nil {
		t.Fatal("cross-isolate key must fail")
	}
	if _, err := scope2.NewMap(ctx2); err != nil {
		t.Fatalf("NewMap on other scope: %v", err)
	}
	if _, err := m.Get(scope2, ctx2, key2); err == nil {
		t.Fatal("cross-isolate scope must fail")
	}
	pattern := mustString(t, scope, "a")
	if _, err := scope.NewRegExp(ctx2, pattern, 0, nil); err == nil {
		t.Fatal("cross-isolate context must fail")
	}
}

func TestRuntimeValuesFatalInputPrevalidation(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	// RegExp: non-string pattern refused before the engine.
	number, err := scope.Number(3)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	if _, err := scope.NewRegExp(ctx, number, 0, nil); err == nil {
		t.Fatal("non-string pattern must fail")
	}
	// JSON: non-string text refused.
	if _, err := gov8.JSONParse(ctx, scope, number, nil); err == nil {
		t.Fatal("non-string JSON text must fail")
	}
	// Symbol registry: non-string description refused.
	if _, err := scope.SymbolForKey(number); err == nil {
		t.Fatal("non-string SymbolForKey description must fail")
	}
	// Private: non-string name refused.
	if _, err := scope.PrivateForApi(number); err == nil {
		t.Fatal("non-string PrivateForApi name must fail")
	}
	// Name-keyed operations: non-Name key refused.
	obj, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	if _, err := obj.CreateDataProperty(scope, ctx, number, number); err == nil {
		t.Fatal("non-Name CreateDataProperty key must fail")
	}
	if _, err := obj.GetOwnPropertyDescriptor(scope, ctx, number); err == nil {
		t.Fatal("non-Name GetOwnPropertyDescriptor key must fail")
	}
	// Invalid enum values refused.
	if _, err := obj.SetIntegrityLevel(scope, ctx, gov8.IntegrityLevel(9)); err == nil {
		t.Fatal("invalid integrity level must fail")
	}
}

func TestPropertyDescriptorLifecycle(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	pd, err := scope.NewPropertyDescriptorFromValue(mustInt32(t, scope, 1))
	if err != nil {
		t.Fatalf("NewPropertyDescriptorFromValue: %v", err)
	}
	if _, err := pd.Value(); err != nil {
		t.Fatalf("Value: %v", err)
	}
	if err := pd.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Every accessor must refuse a closed descriptor.
	if _, err := pd.HasValue(); err == nil {
		t.Fatal("HasValue after Close must fail")
	}
	if _, err := pd.Value(); err == nil {
		t.Fatal("Value after Close must fail")
	}
	if err := pd.Close(); err == nil {
		t.Fatal("double Close must fail")
	}
	// DefineProperty with a closed descriptor must fail without touching
	// the engine.
	obj, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	if _, err := obj.DefineProperty(scope, ctx, mustString(t, scope, "k"), pd); err == nil {
		t.Fatal("DefineProperty with closed descriptor must fail")
	}
	_ = ctx
}

func TestRuntimeValuesWrongThreadAffinity(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	m, err := scope.NewMap(ctx)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	key := mustString(t, scope, "k")

	errs := make(chan error, 4)
	probe := func(name string, fn func() error) {
		t.Helper()
		go func() { errs <- fn() }()
		if err := <-errs; err == nil {
			t.Errorf("%s from foreign goroutine must fail", name)
		} else if !strings.Contains(err.Error(), "affinity") &&
			!strings.Contains(err.Error(), "wrong thread") {
			t.Errorf("%s from foreign goroutine = %v, want affinity error", name, err)
		}
	}
	probe("Map.Size", func() error {
		_, err := m.Size()
		return err
	})
	probe("Map.Clear", m.Clear)
	probe("Array of ops", func() error {
		_, err := m.Has(scope, ctx, key)
		return err
	})
}

// mustEval evaluates source and fails the test on error.
func mustEval(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, tc *gov8.TryCatch, source string) gov8.Value {
	t.Helper()
	v, ok := evalValue(t, ctx, scope, tc, source)
	if !ok {
		t.Fatalf("eval %q failed", source)
	}
	return v
}
