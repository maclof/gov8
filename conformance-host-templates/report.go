//go:build windows && amd64

package main

import (
	"testing"

	gov8 "gov8"
)

// runtime is one isolate+context+scope triple, as used by every oracle
// host check. Cleanup order is scope, context, isolate.
type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t *testing.T) *runtime {
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

func (r *runtime) close(t *testing.T) {
	t.Helper()
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// eval mirrors oracle::checks::harness::eval: (value, true) or (zero, false).
func (r *runtime) eval(t *testing.T, source string) (gov8.Value, bool) {
	t.Helper()
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

// evalText is harness::eval_text: ToString of the completion value, ""
// (with ok=false) when the script failed.
func (r *runtime) evalText(t *testing.T, source string) (string, bool) {
	t.Helper()
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

// valueText is harness::value_text.
func (r *runtime) valueText(t *testing.T, v gov8.Value) string {
	t.Helper()
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// seedGlobal sets globalThis[name] = v and fails the test on error.
func (r *runtime) seedGlobal(t *testing.T, name string, v gov8.Value) {
	t.Helper()
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(r.scope, r.ctx, name, v); err != nil {
		t.Fatalf("SetByName %s: %v", name, err)
	}
}

func (r *runtime) undefined(t *testing.T) gov8.Value {
	t.Helper()
	v, err := r.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	return v
}

func b2s(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
