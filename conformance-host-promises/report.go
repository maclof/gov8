//go:build windows && amd64

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
		return "{\"check\":" + jsonString(s(o.id)) + ",\"ok\":true,\"value\":" + jsonString(o.got) + "}"
	}
	return "{\"check\":" + jsonString(s(o.id)) + ",\"ok\":false,\"expected\":" +
		jsonString(o.want) + ",\"actual\":" + jsonString(o.got) + "}"
}

// runtime is one isolate+context+scope triple under the Explicit microtasks
// policy, exactly like every oracle host promise check.
type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t tester) *runtime {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		_ = iso.Close()
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = iso.Close()
		t.Fatalf("NewScope: %v", err)
	}
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtime) close(t tester) {
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// eval mirrors oracle::checks::harness::eval (None on failure).
func (r *runtime) eval(t tester, source string) (gov8.Value, bool) {
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(r.scope, nil)
	if err != nil {
		return gov8.Value{}, false
	}
	return v, true
}

// evalText mirrors oracle::checks::harness::eval_text with
// unwrap_or_default semantics ("" on failure).
func (r *runtime) evalText(t tester, source string) string {
	v, ok := r.eval(t, source)
	if !ok {
		return ""
	}
	return r.valueText(t, v)
}

// valueText mirrors oracle::checks::harness::value_text: ECMAScript
// ToString of a value, "" on conversion failure.
func (r *runtime) valueText(t tester, v gov8.Value) string {
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// tester is the subset of *testing.T used by the checks.
type tester interface {
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Error(args ...interface{})
}
