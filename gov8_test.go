//go:build windows && amd64

package gov8_test

import (
	"errors"
	"math"
	"testing"

	gov8 "gov8"
)

// newTestRuntime returns an isolate+context+scope for one test. The returned
// cleanup closes scope, context, and isolate in dependency order. V8 itself
// is initialized once per process by TestMain.
func newTestRuntime(t *testing.T) (*gov8.Isolate, *gov8.Context, *gov8.Scope) {
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
	t.Cleanup(func() {
		if err := scope.Close(); err != nil {
			t.Errorf("scope.Close: %v", err)
		}
		if err := ctx.Close(); err != nil {
			t.Errorf("ctx.Close: %v", err)
		}
		if err := iso.Close(); err != nil {
			t.Errorf("iso.Close: %v", err)
		}
	})
	return iso, ctx, scope
}

func TestVersionIdentity(t *testing.T) {
	v, err := gov8.EngineVersion()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	want := gov8.Version{Major: 15, Minor: 2, Build: 124, Patch: 1}
	if v != want {
		t.Fatalf("version = %+v, want %+v", v, want)
	}
	vs, err := gov8.VersionString()
	if err != nil || vs != "15.2.124.1-rusty" {
		t.Fatalf("VersionString = %q, %v", vs, err)
	}
	rt, err := gov8.RuntimeVersionString()
	if err != nil || rt != "15.2.124.1-rusty" {
		t.Fatalf("RuntimeVersionString = %q, %v", rt, err)
	}
}

func TestScriptRoundtrip(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	script, err := ctx.Compile(scope, "40 + 2", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Errorf("script.Close: %v", err)
		}
	}()

	res, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	text, err := res.ToString(ctx)
	if err != nil {
		t.Fatalf("ToString: %v", err)
	}
	if text != "42" {
		t.Fatalf("result = %q, want %q", text, "42")
	}
	isNum, err := res.IsNumber()
	if err != nil || !isNum {
		t.Fatalf("IsNumber = %v, %v", isNum, err)
	}
	num, ok, err := res.NumberValue(ctx)
	if err != nil || !ok || num != 42 {
		t.Fatalf("NumberValue = %v, %v, %v", num, ok, err)
	}
}

func TestPrimitivesAndConversions(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	cases := []struct {
		name string
		make func() (gov8.Value, error)
		text string
	}{
		{"undefined", scope.Undefined, "undefined"},
		{"null", scope.Null, "null"},
		{"true", func() (gov8.Value, error) { return scope.Boolean(true) }, "true"},
		{"false", func() (gov8.Value, error) { return scope.Boolean(false) }, "false"},
		{"int", func() (gov8.Value, error) { return scope.Int32(-42) }, "-42"},
		{"uint", func() (gov8.Value, error) { return scope.Uint32(math.MaxUint32) }, "4294967295"},
		{"f64", func() (gov8.Value, error) { return scope.Number(2.5) }, "2.5"},
		{"negf64", func() (gov8.Value, error) { return scope.Number(-1234.5) }, "-1234.5"},
		{"ascii", func() (gov8.Value, error) { return scope.NewString("hello oracle") }, "hello oracle"},
		{"unicode", func() (gov8.Value, error) { return scope.NewString("héllo 🦀 gov8") }, "héllo 🦀 gov8"},
		{"empty", func() (gov8.Value, error) { return scope.NewString("") }, ""},
		{"bigint", func() (gov8.Value, error) { return scope.BigIntFromInt64(-1234567890123456789) }, "-1234567890123456789"},
		{"bigint_u64max", func() (gov8.Value, error) { return scope.BigIntFromUint64(^uint64(0)) }, "18446744073709551615"},
	}
	for _, tc := range cases {
		v, err := tc.make()
		if err != nil {
			t.Fatalf("%s: construct: %v", tc.name, err)
		}
		got, err := v.ToString(ctx)
		if err != nil {
			t.Fatalf("%s: ToString: %v", tc.name, err)
		}
		if got != tc.text {
			t.Errorf("%s: ToString = %q, want %q", tc.name, got, tc.text)
		}
	}

	// NaN / Infinity
	nan, err := scope.Number(math.NaN())
	if err != nil {
		t.Fatalf("Number(NaN): %v", err)
	}
	nv, _, err := nan.NumberValue(ctx)
	if err != nil {
		t.Fatalf("NaN NumberValue: %v", err)
	}
	if !math.IsNaN(nv) {
		t.Fatalf("NaN value = %v", nv)
	}
	if txt, _ := nan.ToString(ctx); txt != "NaN" {
		t.Fatalf("NaN text = %q", txt)
	}
	inf, err := scope.Number(math.Inf(1))
	if err != nil {
		t.Fatalf("Number(Inf): %v", err)
	}
	if txt, _ := inf.ToString(ctx); txt != "Infinity" {
		t.Fatalf("Infinity text = %q", txt)
	}

	// string metrics
	uni, err := scope.NewString("héllo 🦀 gov8")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if l, _ := uni.Length(); l != 13 {
		t.Fatalf("utf16 length = %d, want 13", l)
	}
	if l, _ := uni.Utf8Length(); l != 16 {
		t.Fatalf("utf8 length = %d, want 16", l)
	}

	// BigInt i64 round-trip and lossless flag
	b, err := scope.BigIntFromInt64(-1234567890123456789)
	if err != nil {
		t.Fatalf("BigIntFromInt64: %v", err)
	}
	if v, lossless, _ := b.BigIntInt64(); v != -1234567890123456789 || !lossless {
		t.Fatalf("bigint i64 = %d, %v", v, lossless)
	}
	bu, err := scope.BigIntFromUint64(^uint64(0))
	if err != nil {
		t.Fatalf("BigIntFromUint64: %v", err)
	}
	if v, lossless, _ := bu.BigIntInt64(); v != -1 || lossless {
		t.Fatalf("u64max i64 = %d, %v; want -1, false", v, lossless)
	}
}

func TestExceptionDetails(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() {
		if err := tc.Close(); err != nil {
			t.Errorf("tc.Close: %v", err)
		}
	}()

	script, err := ctx.Compile(scope, "1 +", tc)
	if err == nil {
		_ = script.Close()
		t.Fatalf("compile of '1 +' unexpectedly succeeded")
	}
	if !errors.Is(err, err) { // trivially true; keeps err referenced
		t.Fatal("unreachable")
	}
	caught, err := tc.HasCaught()
	if err != nil || !caught {
		t.Fatalf("HasCaught = %v, %v", caught, err)
	}
	can, err := tc.CanContinue()
	if err != nil || !can {
		t.Fatalf("CanContinue = %v, %v", can, err)
	}
	msg, err := tc.MessageText(scope, ctx)
	if err != nil || msg != "Uncaught SyntaxError: Unexpected end of input" {
		t.Fatalf("MessageText = %q, %v", msg, err)
	}
	ext, err := tc.ExceptionText(scope, ctx)
	if err != nil || ext != "SyntaxError: Unexpected end of input" {
		t.Fatalf("ExceptionText = %q, %v", ext, err)
	}
	isStr, err := tc.ExceptionIsString()
	if err != nil || isStr {
		t.Fatalf("ExceptionIsString = %v, %v", isStr, err)
	}

	// Reset allows continuing.
	if err := tc.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if caught, _ := tc.HasCaught(); caught {
		t.Fatal("HasCaught after Reset = true")
	}
	script2, err := ctx.Compile(scope, "40 + 2", nil)
	if err != nil {
		t.Fatalf("Compile after reset: %v", err)
	}
	defer func() { _ = script2.Close() }()
	res, err := script2.Run(scope, nil)
	if err != nil {
		t.Fatalf("Run after reset: %v", err)
	}
	if txt, _ := res.ToString(ctx); txt != "42" {
		t.Fatalf("result after reset = %q", txt)
	}
}

func TestMicrotasksExplicitPolicy(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	p, err := iso.GetMicrotasksPolicy()
	if err != nil || p != gov8.PolicyExplicit {
		t.Fatalf("GetMicrotasksPolicy = %v, %v", p, err)
	}

	const scriptSrc = "globalThis.__order = [];" +
		"Promise.resolve().then(() => __order.push('p1'));" +
		"Promise.resolve().then(() => __order.push('p2')).then(() => __order.push('p2b'));" +
		"new Promise(function (resolve) { resolve(); }).then(() => __order.push('p3'));" +
		"Promise.resolve().then(() => { __order.push('p4'); " +
		"Promise.resolve().then(() => __order.push('p4b')); });"

	if _, err := eval(t, ctx, scope, scriptSrc); err != nil {
		t.Fatalf("eval microtask script: %v", err)
	}
	if got := orderString(t, ctx, scope); got != "" {
		t.Fatalf("order after run = %q, want empty", got)
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	want := "p1,p2,p3,p4,p2b,p4b"
	if got := orderString(t, ctx, scope); got != want {
		t.Fatalf("order after checkpoint = %q, want %q", got, want)
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("second checkpoint: %v", err)
	}
	if got := orderString(t, ctx, scope); got != want {
		t.Fatalf("order after second checkpoint = %q, want %q", got, want)
	}
}

func orderString(t *testing.T, ctx *gov8.Context, scope *gov8.Scope) string {
	t.Helper()
	v, err := eval(t, ctx, scope, "__order.join(',')")
	if err != nil {
		t.Fatalf("order eval: %v", err)
	}
	txt, err := v.ToString(ctx)
	if err != nil {
		t.Fatalf("order ToString: %v", err)
	}
	return txt
}

func eval(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, src string) (gov8.Value, error) {
	t.Helper()
	script, err := ctx.Compile(scope, src, nil)
	if err != nil {
		return gov8.Value{}, err
	}
	defer func() { _ = script.Close() }()
	return script.Run(scope, nil)
}
