//go:build windows && amd64

// Shared harness for the conformance-controls-hooks checks: the check
// outcome type, the runtime triple, and the eval helpers. Mirrors the local
// helpers of rust-oracle/src/bin/conformance-controls-hooks.rs one for one.
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

// tester is the subset of *testing.T used by the checks.
type tester interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// fatalf is tester.Fatalf for plain helper funcs.
func fatalf(t tester, format string, args ...interface{}) {
	t.Fatalf(format, args...)
}

// newRuntime builds a runtime, failing the check loudly on wrapper errors
// so the normalized observations only ever diverge for real behavior
// differences.
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

// evalText is the oracle's eval_text: compile, run, ECMAScript ToString
// (lossy UTF-8); ok=false on any failure.
func (r *runtime) evalText(t tester, source string) (string, bool) {
	t.Helper()
	script, cerr := r.ctx.Compile(r.scope, source, nil)
	if cerr != nil {
		return "", false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(r.scope, nil)
	if rerr != nil {
		return "", false
	}
	text, terr := v.ToString(r.ctx)
	if terr != nil {
		return "", false
	}
	return text, true
}

// evalTextCaught is the oracle's tc_scope! + eval_text + caught_text!:
// evaluates under a fresh TryCatch and returns the completion text with the
// caught exception text ("" when nothing was caught).
func (r *runtime) evalTextCaught(t tester, source string) (result string, caught string, ok bool) {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	caughtOf := func() string {
		has, herr := tc.HasCaught()
		if herr != nil || !has {
			return ""
		}
		text, terr := tc.ExceptionText(r.scope, r.ctx)
		if terr != nil {
			return ""
		}
		return text
	}
	script, cerr := r.ctx.Compile(r.scope, source, tc)
	if cerr != nil {
		return "", caughtOf(), false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(r.scope, tc)
	if rerr != nil {
		return "", caughtOf(), false
	}
	text, terr := v.ToString(r.ctx)
	if terr != nil {
		return "", caughtOf(), false
	}
	return text, caughtOf(), true
}
