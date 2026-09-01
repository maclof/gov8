//go:build windows && amd64

// Thin checked wrappers over the gov8 API used by the object-ops checks.
// Every helper fails the check loudly on an unexpected wrapper error so the
// normalized observations only ever diverge for real behavior differences.
package main

import (
	gov8 "gov8"
)

func newIsolateScope(t tester, iso *gov8.Isolate) *gov8.Scope {
	t.Helper()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	return scope
}

func closeTryCatch(t tester, tc *gov8.TryCatch) {
	t.Helper()
	if err := tc.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}
}

func closeContext(t tester, ctx *gov8.Context) {
	t.Helper()
	if err := ctx.Close(); err != nil {
		t.Errorf("Context.Close: %v", err)
	}
}

func scopeNumber(t tester, scope *gov8.Scope, f float64) gov8.Value {
	t.Helper()
	v, err := scope.Number(f)
	if err != nil {
		t.Fatalf("Number(%v): %v", f, err)
	}
	return v
}

func scopeString(t tester, scope *gov8.Scope, s string) gov8.Value {
	t.Helper()
	v, err := scope.NewString(s)
	if err != nil {
		t.Fatalf("NewString(%q): %v", s, err)
	}
	return v
}

func scopeBoolean(t tester, scope *gov8.Scope, b bool) gov8.Value {
	t.Helper()
	v, err := scope.Boolean(b)
	if err != nil {
		t.Fatalf("Boolean(%v): %v", b, err)
	}
	return v
}

func scopeNull(t tester, scope *gov8.Scope) gov8.Value {
	t.Helper()
	v, err := scope.Null()
	if err != nil {
		t.Fatalf("Null: %v", err)
	}
	return v
}

func scopeUndefined(t tester, scope *gov8.Scope) gov8.Value {
	t.Helper()
	v, err := scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	return v
}

// int32Val constructs an int32 value (named to avoid shadowing the builtin).
func int32Val(t tester, scope *gov8.Scope, v int32) gov8.Value {
	t.Helper()
	got, err := scope.Int32(v)
	if err != nil {
		t.Fatalf("Int32(%v): %v", v, err)
	}
	return got
}

func scopeBigIntI64(t tester, scope *gov8.Scope, v int64) gov8.Value {
	t.Helper()
	got, err := scope.BigIntFromInt64(v)
	if err != nil {
		t.Fatalf("BigIntFromInt64(%v): %v", v, err)
	}
	return got
}

func scopeSymbolNamed(t tester, r *runtime, description string) gov8.Value {
	t.Helper()
	desc := scopeString(t, r.scope, description)
	sym, err := r.scope.NewSymbol(desc)
	if err != nil {
		t.Fatalf("NewSymbol: %v", err)
	}
	return sym.Value
}

func mustSymbolValue(t tester, r *runtime) gov8.Value {
	t.Helper()
	sym, err := r.scope.NewSymbol(gov8.Value{})
	if err != nil {
		t.Fatalf("NewSymbol: %v", err)
	}
	return sym.Value
}

func mustNewObjectValue(t tester, r *runtime) gov8.Value {
	t.Helper()
	o, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	return o.Value
}

// mustPred evaluates one value predicate, failing loudly on wrapper errors.
func mustPred(t tester, f func() (bool, error)) bool {
	t.Helper()
	b, err := f()
	if err != nil {
		t.Fatalf("predicate: %v", err)
	}
	return b
}

func mustStrictEquals(t tester, a, b gov8.Value) bool {
	t.Helper()
	eq, err := a.StrictEquals(b)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	return eq
}

func mustSameValue(t tester, a, b gov8.Value) bool {
	t.Helper()
	eq, err := a.SameValue(b)
	if err != nil {
		t.Fatalf("SameValue: %v", err)
	}
	return eq
}

// mustSameValueZero is the slice's SameValueZero (crate-exact composition).
func mustSameValueZero(t tester, r *runtime, a, b gov8.Value) bool {
	t.Helper()
	eq, err := a.SameValueZero(r.scope, b)
	if err != nil {
		t.Fatalf("SameValueZero: %v", err)
	}
	return eq
}

func mustHash(t tester, v gov8.Value) uint32 {
	t.Helper()
	h, err := v.GetHash()
	if err != nil {
		t.Fatalf("GetHash: %v", err)
	}
	return h
}

// mustInstanceOf evaluates Value::InstanceOf without a TryCatch (the value
// must not throw for these operands); a thrown result fails the check.
func mustInstanceOf(t tester, r *runtime, v gov8.Value, ctor *gov8.Object) bool {
	t.Helper()
	eq, err := v.InstanceOf(r.scope, r.ctx, ctor, nil)
	if err != nil {
		t.Fatalf("InstanceOf: %v", err)
	}
	return eq
}

func mustHasCaught(t tester, tc *gov8.TryCatch) bool {
	t.Helper()
	caught, err := tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	return caught
}
