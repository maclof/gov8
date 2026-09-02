//go:build windows && amd64

package main

import (
	gov8 "github.com/maclof/gov8"
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

// tc opens a fresh TryCatch on the runtime's isolate.
func (r *runtime) tc(t tester) *gov8.TryCatch {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	return tc
}

// eval compiles and runs source in the runtime (the oracle's eval; every
// eval in this slice runs under a surrounding TryCatch supplied by the
// check). A non-nil error means the script failed.
func (r *runtime) eval(t tester, tc *gov8.TryCatch, source string) (gov8.Value, error) {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, tc)
	if err != nil {
		return gov8.Value{}, err
	}
	defer func() { _ = script.Close() }()
	return script.Run(r.scope, tc)
}

// tester is the subset of *testing.T used by the checks.
type tester interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Error(args ...interface{})
}
