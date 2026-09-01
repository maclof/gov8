//go:build windows && amd64

// Thin checked wrappers over the gov8 API used by the checks. Every helper
// fails the check loudly on an unexpected wrapper error so the normalized
// observations only ever diverge for real behavior differences.
package main

import (
	"syscall"
	"unsafe"

	gov8 "github.com/maclof/gov8"
)

func newIsolate(t tester) *gov8.Isolate {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	return iso
}

func closeIsolate(t tester, iso *gov8.Isolate) {
	t.Helper()
	if err := iso.Close(); err != nil {
		t.Errorf("Isolate.Close: %v", err)
	}
}

func newIsolateContext(t tester, iso *gov8.Isolate) *gov8.Context {
	t.Helper()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return ctx
}

func closeContext(t tester, ctx *gov8.Context) {
	t.Helper()
	if err := ctx.Close(); err != nil {
		t.Errorf("Context.Close: %v", err)
	}
}

func newIsolateScope(t tester, iso *gov8.Isolate) *gov8.Scope {
	t.Helper()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	return scope
}

func closeScope(t tester, scope *gov8.Scope) {
	t.Helper()
	if err := scope.Close(); err != nil {
		t.Errorf("Scope.Close: %v", err)
	}
}

func newTryCatch(t tester, iso *gov8.Isolate) *gov8.TryCatch {
	t.Helper()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	return tc
}

func closeTryCatch(t tester, tc *gov8.TryCatch) {
	t.Helper()
	if err := tc.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}
}

func closeShared(t tester, shared *gov8.SharedIsolate) {
	t.Helper()
	if err := shared.Close(); err != nil {
		t.Errorf("SharedIsolate.Close: %v", err)
	}
}

func closeWeak(t tester, w *gov8.Weak) {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Errorf("Weak.Close: %v", err)
	}
}

func closeUnbound(t tester, u *gov8.UnboundScript) {
	t.Helper()
	if err := u.Close(); err != nil {
		t.Errorf("UnboundScript.Close: %v", err)
	}
}

func closeLocker(t tester, l *gov8.Locker) {
	t.Helper()
	if err := l.Close(); err != nil {
		t.Errorf("Locker.Close: %v", err)
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

// int32Val constructs an int32 value (named to avoid shadowing the builtin).
func int32Val(t tester, scope *gov8.Scope, v int32) gov8.Value {
	t.Helper()
	got, err := scope.Int32(v)
	if err != nil {
		t.Fatalf("Int32(%v): %v", v, err)
	}
	return got
}

func newEscapable(t tester, scope *gov8.Scope) *gov8.EscapableScope {
	t.Helper()
	esc, err := scope.NewEscapableScope()
	if err != nil {
		t.Fatalf("NewEscapableScope: %v", err)
	}
	return esc
}

func escapeValue(t tester, esc *gov8.EscapableScope, v gov8.Value) gov8.Value {
	t.Helper()
	out, err := esc.Escape(v)
	if err != nil {
		t.Fatalf("Escape: %v", err)
	}
	return out
}

func closeEscapable(t tester, esc *gov8.EscapableScope) {
	t.Helper()
	if err := esc.Close(); err != nil {
		t.Errorf("EscapableScope.Close: %v", err)
	}
}

func enterContext(t tester, ctx *gov8.Context) *gov8.ContextScope {
	t.Helper()
	cs, err := ctx.Enter()
	if err != nil {
		t.Fatalf("Context.Enter: %v", err)
	}
	return cs
}

func closeContextScope(t tester, cs *gov8.ContextScope) {
	t.Helper()
	if err := cs.Close(); err != nil {
		t.Errorf("ContextScope.Close: %v", err)
	}
}

func currentContext(t tester, iso *gov8.Isolate, scope *gov8.Scope) *gov8.ContextRef {
	t.Helper()
	ref, err := iso.CurrentContext(scope)
	if err != nil {
		t.Fatalf("CurrentContext: %v", err)
	}
	return ref
}

func enteredOrMicrotask(t tester, iso *gov8.Isolate, scope *gov8.Scope) *gov8.ContextRef {
	t.Helper()
	ref, err := iso.EnteredOrMicrotaskContext(scope)
	if err != nil {
		t.Fatalf("EnteredOrMicrotaskContext: %v", err)
	}
	return ref
}

func sameContext(t tester, ref *gov8.ContextRef, ctx *gov8.Context) bool {
	t.Helper()
	eq, err := ref.SameAs(ctx)
	if err != nil {
		t.Fatalf("SameAs: %v", err)
	}
	return eq
}

func globalObject(t tester, ctx *gov8.Context, scope *gov8.Scope) gov8.Value {
	t.Helper()
	g, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	return g.Value
}

func sameValue(t tester, a, b gov8.Value) bool {
	t.Helper()
	eq, err := a.SameValue(b)
	if err != nil {
		t.Fatalf("SameValue: %v", err)
	}
	return eq
}

func tokenOf(t tester, ctx *gov8.Context, scope *gov8.Scope) gov8.Value {
	t.Helper()
	v, err := ctx.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	return v
}

func setToken(t tester, ctx *gov8.Context, scope *gov8.Scope, token gov8.Value) {
	t.Helper()
	if err := ctx.SetSecurityToken(scope, token); err != nil {
		t.Fatalf("SetSecurityToken: %v", err)
	}
}

func useDefaultToken(t tester, ctx *gov8.Context) {
	t.Helper()
	if err := ctx.UseDefaultSecurityToken(); err != nil {
		t.Fatalf("UseDefaultSecurityToken: %v", err)
	}
}

func setEmbedder(t tester, ctx *gov8.Context, scope *gov8.Scope, slot int, v gov8.Value) {
	t.Helper()
	if err := ctx.SetEmbedderData(scope, slot, v); err != nil {
		t.Fatalf("SetEmbedderData(%d): %v", slot, err)
	}
}

func setAlignedPointer(t tester, ctx *gov8.Context, slot int, p uintptr) {
	t.Helper()
	if err := ctx.SetAlignedPointerInEmbedderData(slot, p); err != nil {
		t.Fatalf("SetAlignedPointerInEmbedderData(%d): %v", slot, err)
	}
}

func alignedPointer(t tester, ctx *gov8.Context, slot int) uintptr {
	t.Helper()
	p, err := ctx.GetAlignedPointerFromEmbedderData(slot)
	if err != nil {
		t.Fatalf("GetAlignedPointerFromEmbedderData(%d): %v", slot, err)
	}
	return p
}

func mustGetData(t tester, iso *gov8.Isolate, slot int) uintptr {
	t.Helper()
	p, err := iso.GetData(slot)
	if err != nil {
		t.Fatalf("GetData(%d): %v", slot, err)
	}
	return p
}

func setIsolateData(t tester, iso *gov8.Isolate, slot int, p uintptr) {
	t.Helper()
	if err := iso.SetData(slot, p); err != nil {
		t.Fatalf("SetData(%d): %v", slot, err)
	}
}

func newObject(t tester, scope *gov8.Scope, ctx *gov8.Context) gov8.Value {
	t.Helper()
	o, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	return o.Value
}

func asObject(t tester, v gov8.Value) *gov8.Object {
	t.Helper()
	o, err := gov8.AsObject(v)
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	return o
}

func newError(t tester, scope *gov8.Scope, message string) gov8.Value {
	t.Helper()
	v, err := scope.NewError(message)
	if err != nil {
		t.Fatalf("NewError: %v", err)
	}
	return v
}

// evalIn compiles and runs source in the runtime under an optional
// TryCatch, returning the int64 completion value.
func evalIn(t tester, r *runtime, tc *gov8.TryCatch, source string) (int64, bool) {
	t.Helper()
	script, cerr := r.ctx.Compile(r.scope, source, tc)
	if cerr != nil {
		return 0, false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(r.scope, tc)
	if rerr != nil {
		return 0, false
	}
	n, _, nerr := v.IntegerValue(r.ctx)
	if nerr != nil {
		return 0, false
	}
	return n, true
}

func mustStats(t tester, iso *gov8.Isolate) *gov8.HeapStatistics {
	t.Helper()
	stats, err := iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	return stats
}

// pred evaluates one value predicate, failing loudly on wrapper errors.
func pred(t tester, f func() (bool, error)) bool {
	t.Helper()
	b, err := f()
	if err != nil {
		t.Fatalf("predicate: %v", err)
	}
	return b
}

// newThreadIDFn returns a kernel32.GetCurrentThreadId binding for the
// interrupt-callback thread comparison (the Go analog of the oracle's
// std::thread::id equality).
func newThreadIDFn() func() uint32 {
	dll := syscall.NewLazyDLL("kernel32.dll")
	proc := dll.NewProc("GetCurrentThreadId")
	return func() uint32 {
		r, _, _ := proc.Call()
		return uint32(r)
	}
}

var _ = unsafe.Pointer(nil)
