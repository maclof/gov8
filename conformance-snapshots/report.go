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
		return "{\"check\":\"" + o.id + "\",\"ok\":true,\"value\":" + jsonString(o.got) + "}"
	}
	return "{\"check\":\"" + o.id + "\",\"ok\":false,\"expected\":" +
		jsonString(o.want) + ",\"actual\":" + jsonString(o.got) + "}"
}

// runtime is one isolate+context+scope triple.
type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

// close releases the runtime in dependency order.
func (r *runtime) close(t tester) {
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// eval compiles and runs source in the runtime; a non-nil error means the
// script failed (including termination).
func (r *runtime) eval(t tester, source string) (gov8.Value, error) {
	return eval(t, r.ctx, r.scope, source)
}

// eval compiles and runs source in the given context/scope (the harness
// eval helper, mirroring oracle::checks::harness::eval).
func eval(t tester, ctx *gov8.Context, scope *gov8.Scope, src string) (gov8.Value, error) {
	script, err := ctx.Compile(scope, src, nil)
	if err != nil {
		return gov8.Value{}, err
	}
	defer func() { _ = script.Close() }()
	return script.Run(scope, nil)
}

// tester is the subset of *testing.T used by the checks.
type tester interface {
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Error(args ...interface{})
}
