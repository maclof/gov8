//go:build windows && amd64

package gov8_test

import (
	"strconv"
	"strings"
	"testing"

	gov8 "gov8"
)

// promiseRT is one isolate + context + scope triple under the Explicit
// microtasks policy, mirroring every oracle host promise check
// (rust-oracle/src/checks/host/promises.rs).
type promiseRT struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newPromiseRT(t *testing.T) *promiseRT {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	rt := &promiseRT{iso: iso}
	if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		_ = iso.Close()
		t.Fatalf("SetMicrotasksPolicy: %v", err)
	}
	if rt.ctx, err = iso.NewContext(); err != nil {
		_ = iso.Close()
		t.Fatalf("NewContext: %v", err)
	}
	if rt.scope, err = iso.NewScope(); err != nil {
		_ = iso.Close()
		t.Fatalf("NewScope: %v", err)
	}
	return rt
}

func (r *promiseRT) close(t *testing.T) {
	t.Helper()
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

func (r *promiseRT) eval(t *testing.T, source string) (gov8.Value, bool) {
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

func (r *promiseRT) evalText(t *testing.T, source string) string {
	t.Helper()
	v, ok := r.eval(t, source)
	if !ok {
		return ""
	}
	return valueText(t, r, v)
}

// valueText mirrors oracle::checks::harness::value_text.
func valueText(t *testing.T, r *promiseRT, v gov8.Value) string {
	t.Helper()
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// mustState reads a promise state and fails the test on error.
func mustState(t *testing.T, p gov8.Promise) gov8.PromiseState {
	t.Helper()
	s, err := p.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	return s
}

// TestPromiseResolverSettlementSemantics is the Go-side characterization of
// promise/resolver_settlement_semantics: pending start, call-success
// semantics of resolve/reject after settlement, and mark-as-handled.
func TestPromiseResolverSettlementSemantics(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	resolver, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(rt.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}
	if s := mustState(t, promise); s != gov8.PromisePending {
		t.Fatalf("initial state = %v, want Pending", s)
	}
	if has, _ := promise.HasHandler(); has {
		t.Fatal("initial has_handler = true, want false")
	}

	n42, err := rt.scope.Number(42)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	resolveOK, err := resolver.Resolve(rt.ctx, n42)
	if err != nil || !resolveOK {
		t.Fatalf("Resolve = (%v, %v), want (true, nil)", resolveOK, err)
	}
	if s := mustState(t, promise); s != gov8.PromiseFulfilled {
		t.Fatalf("state after resolve = %v, want Fulfilled", s)
	}
	if got := valueText(t, rt, mustResult(t, promise, rt)); got != "42" {
		t.Fatalf("result = %q, want %q", got, "42")
	}

	// Both calls report call success; the settlement is silently ignored.
	n43, _ := rt.scope.Number(43)
	if ok, err := resolver.Resolve(rt.ctx, n43); err != nil || !ok {
		t.Fatalf("second Resolve = (%v, %v), want (true, nil)", ok, err)
	}
	late, _ := rt.scope.NewString("late")
	if ok, err := resolver.Reject(rt.ctx, late); err != nil || !ok {
		t.Fatalf("Reject after resolve = (%v, %v), want (true, nil)", ok, err)
	}
	if s := mustState(t, promise); s != gov8.PromiseFulfilled {
		t.Fatalf("state after ignored settle = %v, want Fulfilled", s)
	}
	if got := valueText(t, rt, mustResult(t, promise, rt)); got != "42" {
		t.Fatalf("result after ignored settle = %q, want %q", got, "42")
	}

	// Rejection path.
	resolver2, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver 2: %v", err)
	}
	promise2, err := resolver2.GetPromise(rt.scope)
	if err != nil {
		t.Fatalf("GetPromise 2: %v", err)
	}
	boom, _ := rt.scope.NewString("boom")
	if ok, err := resolver2.Reject(rt.ctx, boom); err != nil || !ok {
		t.Fatalf("Reject = (%v, %v), want (true, nil)", ok, err)
	}
	if s := mustState(t, promise2); s != gov8.PromiseRejected {
		t.Fatalf("state after reject = %v, want Rejected", s)
	}
	if got := valueText(t, rt, mustResult(t, promise2, rt)); got != "boom" {
		t.Fatalf("rejection result = %q, want %q", got, "boom")
	}
	if has, _ := promise2.HasHandler(); has {
		t.Fatal("has_handler after reject = true, want false")
	}
	if err := promise2.MarkAsHandled(); err != nil {
		t.Fatalf("MarkAsHandled: %v", err)
	}
}

func mustResult(t *testing.T, p gov8.Promise, rt *promiseRT) gov8.Value {
	t.Helper()
	v, err := p.Result(rt.scope)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	return v
}

// TestPromiseThenCheckpoint mirrors promise/native_then_checkpoint: the
// native handler is attached synchronously, the reaction job runs only at
// the checkpoint, and the derived promise is a distinct object settling to
// the handler's (implicit undefined) result.
func TestPromiseThenCheckpoint(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	if _, ok := rt.eval(t, "globalThis.__order = [];"); !ok {
		t.Fatal("seed eval failed")
	}

	resolver, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(rt.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}

	handler, err := rt.scope.NewNativeFunction(rt.ctx, pushOrderHandler(t, rt))
	if err != nil {
		t.Fatalf("NewNativeFunction: %v", err)
	}
	defer func() {
		if err := handler.Close(); err != nil {
			t.Errorf("handler Close: %v", err)
		}
	}()

	derived, err := promise.Then(rt.ctx, handler.Value())
	if err != nil {
		t.Fatalf("Then: %v", err)
	}
	if has, _ := promise.HasHandler(); !has {
		t.Fatal("has_handler after Then = false, want true")
	}
	if s := mustState(t, derived); s != gov8.PromisePending {
		t.Fatalf("derived initial state = %v, want Pending", s)
	}
	same, err := derived.StrictEquals(promise.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	if same {
		t.Fatal("derived promise is not distinct from the original")
	}

	n42, _ := rt.scope.Int32(42)
	if ok, err := resolver.Resolve(rt.ctx, n42); err != nil || !ok {
		t.Fatalf("Resolve = (%v, %v), want (true, nil)", ok, err)
	}
	if got := rt.evalText(t, "__order.join(',')"); got != "" {
		t.Fatalf("order before checkpoint = %q, want empty", got)
	}

	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("PerformMicrotaskCheckpoint: %v", err)
	}
	if got := rt.evalText(t, "__order.join(',')"); got != "42" {
		t.Fatalf("order after checkpoint = %q, want %q", got, "42")
	}
	if s := mustState(t, derived); s != gov8.PromiseFulfilled {
		t.Fatalf("derived final state = %v, want Fulfilled", s)
	}
	if got := valueText(t, rt, mustResult(t, derived, rt)); got != "undefined" {
		t.Fatalf("derived result = %q, want %q", got, "undefined")
	}
}

// pushOrderHandler is the Go port of the oracle's cb_push_order: it appends
// the received value's text to the __order script-global array through the
// object API. Array length and index writes go through canonical named
// properties ("length", "0", "1", ...), the observable equivalent of the
// oracle's Array::length/Array::set calls with the available object API.
func pushOrderHandler(t *testing.T, rt *promiseRT) gov8.NativePromiseHandler {
	return func(args []gov8.Value) (gov8.Value, bool) {
		global, err := rt.ctx.GlobalObject(rt.scope)
		if err != nil {
			t.Logf("pushOrder: GlobalObject: %v", err)
			return gov8.Value{}, false
		}
		order, ok, err := global.GetByName(rt.scope, rt.ctx, "__order")
		if err != nil || !ok {
			t.Logf("pushOrder: get __order: err=%v ok=%v", err, ok)
			return gov8.Value{}, false
		}
		arr := &gov8.Object{Value: order}
		lenVal, ok, err := arr.GetByName(rt.scope, rt.ctx, "length")
		if err != nil || !ok {
			t.Logf("pushOrder: get length: err=%v ok=%v", err, ok)
			return gov8.Value{}, false
		}
		idx, okInt, _ := lenVal.IntegerValue(rt.ctx)
		if !okInt {
			t.Log("pushOrder: length is not an integer")
			return gov8.Value{}, false
		}
		if len(args) == 0 {
			t.Log("pushOrder: no arguments")
			return gov8.Value{}, false
		}
		if _, err := arr.SetByName(rt.scope, rt.ctx,
			strconv.FormatInt(idx, 10), args[0]); err != nil {
			t.Logf("pushOrder: set: %v", err)
			return gov8.Value{}, false
		}
		return gov8.Value{}, false // leave the JS return value undefined
	}
}

// TestPromiseThen2RejectPath verifies the two-handler reaction: the
// rejection handler receives the reason and the derived promise fulfills
// with the handler's implicit undefined.
func TestPromiseThen2RejectPath(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	resolver, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(rt.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}

	var seen []string
	onFulfilled, err := rt.scope.NewNativeFunction(rt.ctx, func(args []gov8.Value) (gov8.Value, bool) {
		seen = append(seen, "fulfilled")
		return gov8.Value{}, false
	})
	if err != nil {
		t.Fatalf("NewNativeFunction fulfilled: %v", err)
	}
	defer func() { _ = onFulfilled.Close() }()
	onRejected, err := rt.scope.NewNativeFunction(rt.ctx, func(args []gov8.Value) (gov8.Value, bool) {
		txt := ""
		if len(args) > 0 {
			txt, _ = args[0].ToString(rt.ctx)
		}
		seen = append(seen, "rejected:"+txt)
		return gov8.Value{}, false
	})
	if err != nil {
		t.Fatalf("NewNativeFunction rejected: %v", err)
	}
	defer func() { _ = onRejected.Close() }()

	derived, err := promise.Then2(rt.ctx, onFulfilled.Value(), onRejected.Value())
	if err != nil {
		t.Fatalf("Then2: %v", err)
	}
	boom, _ := rt.scope.NewString("why")
	if ok, err := resolver.Reject(rt.ctx, boom); err != nil || !ok {
		t.Fatalf("Reject = (%v, %v), want (true, nil)", ok, err)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if len(seen) != 1 || seen[0] != "rejected:why" {
		t.Fatalf("handler calls = %v, want [rejected:why]", seen)
	}
	if s := mustState(t, derived); s != gov8.PromiseFulfilled {
		t.Fatalf("derived state = %v, want Fulfilled", s)
	}
	if got := valueText(t, rt, mustResult(t, derived, rt)); got != "undefined" {
		t.Fatalf("derived result = %q, want undefined", got)
	}
}

// TestPromiseCatchRejected verifies catch: the rejection reaction runs, the
// derived promise fulfills, and ok reports the derived promise's existence.
func TestPromiseCatchRejected(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	resolver, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(rt.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}
	var seen string
	handler, err := rt.scope.NewNativeFunction(rt.ctx, func(args []gov8.Value) (gov8.Value, bool) {
		if len(args) > 0 {
			seen, _ = args[0].ToString(rt.ctx)
		}
		return gov8.Value{}, false
	})
	if err != nil {
		t.Fatalf("NewNativeFunction: %v", err)
	}
	defer func() { _ = handler.Close() }()

	x, _ := rt.scope.NewString("x")
	if ok, err := resolver.Reject(rt.ctx, x); err != nil || !ok {
		t.Fatalf("Reject = (%v, %v), want (true, nil)", ok, err)
	}
	derived, ok, err := promise.Catch(rt.ctx, handler.Value())
	if err != nil || !ok {
		t.Fatalf("Catch = (%v, %v), want ok", ok, err)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if seen != "x" {
		t.Fatalf("catch handler saw %q, want %q", seen, "x")
	}
	if s := mustState(t, derived); s != gov8.PromiseFulfilled {
		t.Fatalf("derived state = %v, want Fulfilled", s)
	}
}

// TestPromiseNativeHandlerReturnValue verifies that a native handler's
// returned value becomes the JS return value, so the derived promise
// fulfills with it.
func TestPromiseNativeHandlerReturnValue(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	resolver, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(rt.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}
	mapped, err := rt.scope.NewString("mapped")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	handler, err := rt.scope.NewNativeFunction(rt.ctx, func(args []gov8.Value) (gov8.Value, bool) {
		return mapped, true
	})
	if err != nil {
		t.Fatalf("NewNativeFunction: %v", err)
	}
	defer func() { _ = handler.Close() }()

	derived, err := promise.Then(rt.ctx, handler.Value())
	if err != nil {
		t.Fatalf("Then: %v", err)
	}
	n1, _ := rt.scope.Int32(1)
	if ok, err := resolver.Resolve(rt.ctx, n1); err != nil || !ok {
		t.Fatalf("Resolve = (%v, %v), want (true, nil)", ok, err)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if s := mustState(t, derived); s != gov8.PromiseFulfilled {
		t.Fatalf("derived state = %v, want Fulfilled", s)
	}
	if got := valueText(t, rt, mustResult(t, derived, rt)); got != "mapped" {
		t.Fatalf("derived result = %q, want %q", got, "mapped")
	}
}

// TestPromiseJSFunctionHandler verifies that then accepts plain JS
// functions (any Local<Function> in the oracle), not only native ones.
func TestPromiseJSFunctionHandler(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	if _, ok := rt.eval(t, "globalThis.__seen = '';"+
		"globalThis.__js = function (v) { globalThis.__seen = String(v); };"); !ok {
		t.Fatal("seed eval failed")
	}
	global, err := rt.ctx.GlobalObject(rt.scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	jsHandler, ok, err := global.GetByName(rt.scope, rt.ctx, "__js")
	if err != nil || !ok {
		t.Fatalf("get __js: err=%v ok=%v", err, ok)
	}

	resolver, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise, _ := resolver.GetPromise(rt.scope)
	derived, err := promise.Then(rt.ctx, jsHandler)
	if err != nil {
		t.Fatalf("Then with JS function: %v", err)
	}
	n9, _ := rt.scope.Int32(9)
	if ok, err := resolver.Resolve(rt.ctx, n9); err != nil || !ok {
		t.Fatalf("Resolve = (%v, %v), want (true, nil)", ok, err)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if got := rt.evalText(t, "__seen"); got != "9" {
		t.Fatalf("__seen = %q, want %q", got, "9")
	}
	if s := mustState(t, derived); s != gov8.PromiseFulfilled {
		t.Fatalf("derived state = %v, want Fulfilled", s)
	}
}

// TestPromiseArgumentErrors covers the misuse errors detected in the Go
// wrapper before any engine call.
func TestPromiseArgumentErrors(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	foreign := newPromiseRT(t)
	defer foreign.close(t)

	resolver, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise, _ := resolver.GetPromise(rt.scope)

	foreignVal, err := foreign.scope.Int32(1)
	if err != nil {
		t.Fatalf("foreign Int32: %v", err)
	}
	if _, err := resolver.Resolve(rt.ctx, foreignVal); err == nil {
		t.Fatal("Resolve with foreign value: want error")
	}
	if _, err := resolver.Resolve(foreign.ctx, mustUndefined(t, rt)); err == nil {
		t.Fatal("Resolve with foreign context: want error")
	}

	str, _ := rt.scope.NewString("nope")
	if _, err := promise.Then(rt.ctx, str); err == nil || !strings.Contains(err.Error(), "not a function") {
		t.Fatalf("Then with string handler: err=%v, want not-a-function error", err)
	}
	if _, err := promise.Then(rt.ctx, gov8.Value{}); err == nil {
		t.Fatal("Then with zero value handler: want error")
	}
	if _, _, err := promise.Catch(foreign.ctx, str); err == nil {
		t.Fatal("Catch with foreign context: want error")
	}
	if _, err := promise.Result(foreign.scope); err == nil {
		t.Fatal("Result with foreign scope: want error")
	}
}

func mustUndefined(t *testing.T, rt *promiseRT) gov8.Value {
	t.Helper()
	v, err := rt.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	return v
}

// TestPromiseUseAfterScopeClose verifies scope-bound lifetime: after the
// creating scope closes, promise operations fail without touching the
// engine.
func TestPromiseUseAfterScopeClose(t *testing.T) {
	rt := newPromiseRT(t)
	// The scope is closed explicitly below; only ctx and iso remain.
	defer func() {
		if err := rt.ctx.Close(); err != nil {
			t.Errorf("Context.Close: %v", err)
		}
		if err := rt.iso.Close(); err != nil {
			t.Errorf("Isolate.Close: %v", err)
		}
	}()

	resolver, err := rt.scope.NewPromiseResolver(rt.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(rt.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}
	u := mustUndefined(t, rt)
	if err := rt.scope.Close(); err != nil {
		t.Fatalf("Scope.Close: %v", err)
	}
	if _, err := promise.State(); err == nil {
		t.Fatal("State after scope close: want error")
	}
	if _, err := promise.Result(rt.scope); err == nil {
		t.Fatal("Result after scope close: want error")
	}
	if _, err := resolver.Resolve(rt.ctx, u); err == nil {
		t.Fatal("Resolve after scope close: want error")
	}
	if _, err := promise.HasHandler(); err == nil {
		t.Fatal("HasHandler after scope close: want error")
	}
	if err := promise.MarkAsHandled(); err == nil {
		t.Fatal("MarkAsHandled after scope close: want error")
	}
}
