//go:build windows && amd64

// Shared harness for the conformance-object-ops checks: the check outcome
// type, the runtime triple, and the eval/option helpers. Mirrors the local
// helpers of rust-oracle/src/bin/conformance-object-ops.rs one for one.
package main

import (
	gov8 "gov8"
)

// obs packages one check's expectation/observation pair, mirroring
// rust-oracle/src/report.rs CheckOutcome.
type obs struct {
	id   string
	want jsonValue
	got  jsonValue
}

// wantGot mirrors report::expect_eq: the check passes when the canonical
// encodings are byte-identical.
func wantGot(id string, want, got jsonValue) obs {
	return obs{id: id, want: want, got: got}
}

// passed reports whether the canonical encodings match.
func (o obs) passed() bool {
	return jsonString(o.want) == jsonString(o.got)
}

// line renders the normalized JSON-lines encoding of the outcome, exactly
// like rust-oracle/src/report.rs CheckOutcome::to_line.
func (o obs) line() string {
	if o.passed() {
		return "{\"check\":\"" + o.id + "\",\"ok\":true,\"value\":" + jsonString(o.got) + "}"
	}
	return "{\"check\":\"" + o.id + "\",\"ok\":false,\"expected\":" +
		jsonString(o.want) + ",\"actual\":" + jsonString(o.got) + "}"
}

// runtime is one isolate+context+scope triple (the oracle's per-check
// `v8::scope!` + ContextScope).
type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t tester) *runtime {
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
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}

// close releases the runtime in dependency order.
func (r *runtime) close(t tester) {
	t.Helper()
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// tc opens a fresh TryCatch on the runtime's isolate. The caller closes it.
func (r *runtime) tc(t tester) *gov8.TryCatch {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	return tc
}

// eval compiles and runs source in the runtime, returning the completion
// value (ok=false on compile or runtime failure).
func (r *runtime) eval(t tester, tc *gov8.TryCatch, source string) (gov8.Value, bool) {
	t.Helper()
	script, cerr := r.ctx.Compile(r.scope, source, tc)
	if cerr != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(r.scope, tc)
	if rerr != nil {
		return gov8.Value{}, false
	}
	return v, true
}

// mustEval is eval with a loud failure (used where the oracle unwraps).
func (r *runtime) mustEval(t tester, source string) gov8.Value {
	t.Helper()
	v, ok := r.eval(t, nil, source)
	if !ok {
		t.Fatalf("eval %q failed", source)
	}
	return v
}

// globalValue fetches a global by name as a Value (the oracle's
// global_value: eval of the name).
func (r *runtime) globalValue(t tester, name string) (gov8.Value, bool) {
	t.Helper()
	return r.eval(t, nil, name)
}

// mustGlobalValue is globalValue with a loud failure.
func (r *runtime) mustGlobalValue(t tester, name string) gov8.Value {
	t.Helper()
	v, ok := r.globalValue(t, name)
	if !ok {
		t.Fatalf("global %q missing", name)
	}
	return v
}

// globalObject fetches a global by name as an *Object (functions and plain
// objects are objects; primitives fail, like try_cast::<v8::Object>).
func (r *runtime) globalObject(t tester, source string) *gov8.Object {
	t.Helper()
	v, ok := r.globalValue(t, source)
	if !ok {
		t.Fatalf("global %q missing", source)
	}
	o, err := gov8.AsObject(v)
	if err != nil {
		t.Fatalf("global %q is not an object: %v", source, err)
	}
	return o
}

// setGlobal sets a global property (the oracle's set_global; only used on
// plain globals where failure is impossible).
func (r *runtime) setGlobal(t tester, name string, value gov8.Value) {
	t.Helper()
	g := r.globalObject(t, "globalThis")
	ok, err := g.SetByName(r.scope, r.ctx, name, value)
	if err != nil || !ok {
		t.Fatalf("set global %q: ok=%v err=%v", name, ok, err)
	}
}

// evalText runs source and returns its completion value rendered via
// ToString ("" when it throws or does not convert).
func (r *runtime) evalText(t tester, source string) string {
	t.Helper()
	v, ok := r.eval(t, nil, source)
	if !ok {
		return ""
	}
	return valueText(t, r, v)
}

// valueText is the oracle's value_text: ECMAScript ToString of a value
// ("" on conversion failure).
func valueText(t tester, r *runtime, v gov8.Value) string {
	t.Helper()
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// intOf is the oracle's int_of: integer_value with a -1 fallback (both for
// an absent value and for a failed conversion). A zero Value encodes the
// crate's None.
func intOf(t tester, r *runtime, v gov8.Value) int64 {
	t.Helper()
	if v == (gov8.Value{}) {
		return -1
	}
	n, _, err := v.IntegerValue(r.ctx)
	if err != nil {
		return -1
	}
	return n
}

// optBool encodes Option<bool> (None -> JSON null). err non-nil means the
// engine produced an empty maybe.
func optBool(ok bool, err error) jsonValue {
	if err != nil {
		return jnull()
	}
	return jbool(ok)
}

// optFound encodes the (found bool, err error) pair of the real-named
// queries: a plain miss is Some-shaped (found false), only an error is None.
func optFound(found bool, err error) jsonValue {
	if err != nil {
		return jnull()
	}
	return jbool(found)
}

// tester is the subset of *testing.T used by the checks.
type tester interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Fatal(args ...interface{})
	Errorf(format string, args ...interface{})
	Error(args ...interface{})
}
