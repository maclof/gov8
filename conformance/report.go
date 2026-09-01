//go:build windows && amd64

package main

import (
	gov8 "github.com/maclof/gov8"
)

// obs packages one check's normalized observation.
type obs struct {
	id   string
	val  jsonValue
	want jsonValue
	fail bool
}

func pass(id string, v jsonValue) obs { return obs{id: id, val: v} }

// wantGot folds expectation and observation into one outcome: pass when the
// canonical encodings are byte-identical, otherwise a diffable failure.
func wantGot(id string, want, got jsonValue) obs {
	if jsonString(want) == jsonString(got) {
		return obs{id: id, val: got}
	}
	return obs{id: id, val: got, want: want, fail: true}
}

// runtime is one isolate+context+scope triple, as used by every oracle check.
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

func (r *runtime) close(t tester) {
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// eval mirrors oracle::checks::harness::eval.
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

func (r *runtime) evalText(t tester, source string) (string, bool) {
	v, ok := r.eval(t, source)
	if !ok {
		return "", false
	}
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return "", false
	}
	return txt, true
}

// text is oracle::checks::harness::value_text: ECMAScript ToString of a
// value as a Go string ("" on conversion failure).
func text(t tester, r *runtime, v gov8.Value) string {
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// tester is the subset of *testing.T used by checks (keeps helpers usable
// from both test and non-test contexts).
type tester interface {
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}
