//go:build windows && amd64

package main

import (
	"strconv"

	gov8 "gov8"
)

// The three checks below are mechanical ports of
// rust-oracle/src/checks/host/promises.rs, including JSON key order and the
// oracle's Option->null / unwrap_or_default mappings. Characterized
// contract (from the oracle's header comment):
//   - resolve/reject return the success of the CALL, not a settlement
//     change; repeat settlement attempts are silently ignored and still
//     return true.
//   - then attaches synchronously; under Explicit microtasks the reaction
//     job runs at perform_microtask_checkpoint; the derived promise is a
//     distinct object.
//   - the promise-reject callback fires synchronously at reject time
//     (WithNoHandler when unhandled), fires HandlerAddedAfterReject when a
//     handler is attached later, fires nothing when a handler preceded the
//     reject, and reports the derived promise of a bare `then` on a
//     rejected promise as a second WithNoHandler when the reaction job
//     runs. The AfterResolved events were removed from V8 and never fire.

// stateName mirrors state_name in promises.rs.
func stateName(t tester, st gov8.PromiseState, err error) string {
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	return st.String()
}

// promiseState reads a promise state as a name, failing the test on error.
func promiseState(t tester, p gov8.Promise) string {
	st, err := p.State()
	return stateName(t, st, err)
}

// pushOrder is the port of cb_push_order: it appends the received value to
// the __order script-global array through the object API. Length/index
// access uses canonical named properties ("length", "0", ...), the
// observable equivalent of the oracle's Array::length/Array::set with the
// object API available to this slice.
func pushOrder(t tester, r *runtime) gov8.NativePromiseHandler {
	return func(args []gov8.Value) (gov8.Value, bool) {
		global, err := r.ctx.GlobalObject(r.scope)
		if err != nil {
			t.Errorf("pushOrder GlobalObject: %v", err)
			return gov8.Value{}, false
		}
		order, ok, err := global.GetByName(r.scope, r.ctx, "__order")
		if err != nil || !ok {
			t.Errorf("pushOrder get __order: err=%v ok=%v", err, ok)
			return gov8.Value{}, false
		}
		array := &gov8.Object{Value: order}
		lenVal, ok, err := array.GetByName(r.scope, r.ctx, "length")
		if err != nil || !ok {
			t.Errorf("pushOrder get length: err=%v ok=%v", err, ok)
			return gov8.Value{}, false
		}
		idx, okInt, _ := lenVal.IntegerValue(r.ctx)
		if !okInt {
			t.Error("pushOrder: length not an integer")
			return gov8.Value{}, false
		}
		if len(args) == 0 {
			t.Error("pushOrder: no arguments")
			return gov8.Value{}, false
		}
		if _, err := array.SetByName(r.scope, r.ctx,
			strconv.FormatInt(idx, 10), args[0]); err != nil {
			t.Errorf("pushOrder set: %v", err)
			return gov8.Value{}, false
		}
		return gov8.Value{}, false // implicit undefined result, like the oracle
	}
}

// resolveBool maps Option<bool> to the normalized encoding: null when the
// call failed, the boolean otherwise (unwrap_or(Json::Null) in the oracle).
func resolveBool(ok bool, err error) jsonValue {
	if err != nil {
		return jsonNull{}
	}
	return b(ok)
}

func checkResolverSettlementSemantics(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	resolver, err := r.scope.NewPromiseResolver(r.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(r.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}
	initialState := promiseState(t, promise)
	initialHasHandler, err := promise.HasHandler()
	if err != nil {
		t.Fatalf("HasHandler: %v", err)
	}

	n42, err := r.scope.Number(42)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	resolveOK, resolveErr := resolver.Resolve(r.ctx, n42)
	fulfilledState := promiseState(t, promise)
	fulfilledResult := r.valueText(t, mustResult(t, r, promise))

	n43, err := r.scope.Number(43)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	resolveAgain, resolveAgainErr := resolver.Resolve(r.ctx, n43)
	late, err := r.scope.NewString("late")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	rejectAfter, rejectAfterErr := resolver.Reject(r.ctx, late)
	stillFulfilled := promiseState(t, promise)
	resultStill := r.valueText(t, mustResult(t, r, promise))

	resolver2, err := r.scope.NewPromiseResolver(r.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver 2: %v", err)
	}
	promise2, err := resolver2.GetPromise(r.scope)
	if err != nil {
		t.Fatalf("GetPromise 2: %v", err)
	}
	boom, err := r.scope.NewString("boom")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	rejectOK, rejectErr := resolver2.Reject(r.ctx, boom)
	rejectedState := promiseState(t, promise2)
	rejectedResult := r.valueText(t, mustResult(t, r, promise2))
	rejectedHasHandler, err := promise2.HasHandler()
	if err != nil {
		t.Fatalf("HasHandler 2: %v", err)
	}
	// The oracle records a literal true for mark_as_handled_ok; this port
	// additionally fails loudly if the call errors.
	markAsHandledOK := true
	if err := promise2.MarkAsHandled(); err != nil {
		t.Errorf("MarkAsHandled: %v", err)
		markAsHandledOK = false
	}

	want := obj(
		kv("initial_state", s("Pending")),
		kv("initial_has_handler", b(false)),
		kv("resolve_ok", b(true)),
		kv("fulfilled_state", s("Fulfilled")),
		kv("fulfilled_result", s("42")),
		// Both calls succeed (the settlement itself is silently ignored).
		kv("resolve_again", b(true)),
		kv("reject_after", b(true)),
		kv("still_fulfilled", s("Fulfilled")),
		kv("result_still", s("42")),
		kv("reject_ok", b(true)),
		kv("rejected_state", s("Rejected")),
		kv("rejected_result", s("boom")),
		kv("rejected_has_handler", b(false)),
		kv("mark_as_handled_ok", b(true)),
	)
	got := obj(
		kv("initial_state", s(initialState)),
		kv("initial_has_handler", b(initialHasHandler)),
		kv("resolve_ok", resolveBool(resolveOK, resolveErr)),
		kv("fulfilled_state", s(fulfilledState)),
		kv("fulfilled_result", s(fulfilledResult)),
		kv("resolve_again", resolveBool(resolveAgain, resolveAgainErr)),
		kv("reject_after", resolveBool(rejectAfter, rejectAfterErr)),
		kv("still_fulfilled", s(stillFulfilled)),
		kv("result_still", s(resultStill)),
		kv("reject_ok", resolveBool(rejectOK, rejectErr)),
		kv("rejected_state", s(rejectedState)),
		kv("rejected_result", s(rejectedResult)),
		kv("rejected_has_handler", b(rejectedHasHandler)),
		kv("mark_as_handled_ok", b(markAsHandledOK)),
	)
	return wantGot("promise/resolver_settlement_semantics", want, got)
}

func mustResult(t tester, r *runtime, p gov8.Promise) gov8.Value {
	v, err := p.Result(r.scope)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	return v
}

func checkNativeThenCheckpoint(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	if _, ok := r.eval(t, "globalThis.__order = [];"); !ok {
		t.Fatal("seed eval failed")
	}

	resolver, err := r.scope.NewPromiseResolver(r.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver: %v", err)
	}
	promise, err := resolver.GetPromise(r.scope)
	if err != nil {
		t.Fatalf("GetPromise: %v", err)
	}

	handler, err := r.scope.NewNativeFunction(r.ctx, pushOrder(t, r))
	if err != nil {
		t.Fatalf("NewNativeFunction: %v", err)
	}
	defer func() {
		if err := handler.Close(); err != nil {
			t.Errorf("handler Close: %v", err)
		}
	}()

	derived, err := promise.Then(r.ctx, handler.Value())
	if err != nil {
		t.Fatalf("Then: %v", err)
	}

	hasHandlerBeforeResolve, err := promise.HasHandler()
	if err != nil {
		t.Fatalf("HasHandler: %v", err)
	}
	derivedInitialState := promiseState(t, derived)
	same, err := derived.StrictEquals(promise.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	derivedIsDistinct := !same

	n42, err := r.scope.Int32(42)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	resolveOK, resolveErr := resolver.Resolve(r.ctx, n42)
	orderBefore := r.evalText(t, "__order.join(',')")

	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("PerformMicrotaskCheckpoint: %v", err)
	}
	orderAfter := r.evalText(t, "__order.join(',')")
	derivedFinalState := promiseState(t, derived)
	derivedResult := r.valueText(t, mustResult(t, r, derived))

	want := obj(
		kv("has_handler_before_resolve", b(true)),
		kv("derived_initial_state", s("Pending")),
		kv("derived_is_distinct", b(true)),
		kv("resolve_ok", b(true)),
		kv("order_before_checkpoint", s("")),
		kv("order_after_checkpoint", s("42")),
		kv("derived_final_state", s("Fulfilled")),
		kv("derived_result", s("undefined")),
	)
	got := obj(
		kv("has_handler_before_resolve", b(hasHandlerBeforeResolve)),
		kv("derived_initial_state", s(derivedInitialState)),
		kv("derived_is_distinct", b(derivedIsDistinct)),
		kv("resolve_ok", resolveBool(resolveOK, resolveErr)),
		kv("order_before_checkpoint", s(orderBefore)),
		kv("order_after_checkpoint", s(orderAfter)),
		kv("derived_final_state", s(derivedFinalState)),
		kv("derived_result", s(derivedResult)),
	)
	return wantGot("promise/native_then_checkpoint", want, got)
}

func checkRejectCallbackEvents(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	// Event names observed by the promise-reject callback, in order. The
	// callback is installed before any scope-local engine work, mirroring
	// the oracle's set_promise_reject_callback-before-scope ordering.
	var events []string
	snapshot := func() jsonValue {
		out := make([]jsonValue, 0, len(events))
		for _, name := range events {
			out = append(out, s(name))
		}
		return arr(out...)
	}
	observe := func(m gov8.PromiseRejectMessage) {
		events = append(events, m.Event.String())
	}
	if err := r.iso.SetPromiseRejectCallback(r.scope, observe); err != nil {
		t.Fatalf("SetPromiseRejectCallback: %v", err)
	}

	handler, err := r.scope.NewNativeFunction(r.ctx, func(args []gov8.Value) (gov8.Value, bool) {
		return gov8.Value{}, false
	})
	if err != nil {
		t.Fatalf("NewNativeFunction: %v", err)
	}
	defer func() {
		if err := handler.Close(); err != nil {
			t.Errorf("handler Close: %v", err)
		}
	}()

	// Case A: reject with no handler fires WithNoHandler synchronously;
	// attaching a handler afterwards fires HandlerAddedAfterReject.
	resolver1, err := r.scope.NewPromiseResolver(r.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver 1: %v", err)
	}
	promise1, err := resolver1.GetPromise(r.scope)
	if err != nil {
		t.Fatalf("GetPromise 1: %v", err)
	}
	x, err := r.scope.NewString("x")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	rejectOK, rejectErr := resolver1.Reject(r.ctx, x)
	afterReject := snapshot()
	_, catchAttached, catchErr := promise1.Catch(r.ctx, handler.Value())
	if catchErr != nil {
		t.Fatalf("Catch: %v", catchErr)
	}
	afterCatch := snapshot()
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint A: %v", err)
	}
	// The catch handler fulfills the derived promise: no new event.
	afterCheckpointA := snapshot()

	// Case B: a handler attached before the reject produces no event.
	resolver2, err := r.scope.NewPromiseResolver(r.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver 2: %v", err)
	}
	promise2, err := resolver2.GetPromise(r.scope)
	if err != nil {
		t.Fatalf("GetPromise 2: %v", err)
	}
	if _, err := promise2.Then(r.ctx, handler.Value()); err != nil {
		t.Fatalf("Then: %v", err)
	}
	y, err := r.scope.NewString("y")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	reject2OK, reject2Err := resolver2.Reject(r.ctx, y)
	afterPrehandledReject := snapshot()
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint B: %v", err)
	}
	// `then` registered no on_rejected, so when the reaction job runs the
	// derived promise is rejected with the same reason and reported as
	// unhandled.
	afterCheckpointB := snapshot()

	// Case C: rejecting an already fulfilled promise leaves the settlement
	// untouched; the AfterResolved events were removed from V8 and never
	// fire in this build.
	resolver3, err := r.scope.NewPromiseResolver(r.ctx)
	if err != nil {
		t.Fatalf("NewPromiseResolver 3: %v", err)
	}
	promise3, err := resolver3.GetPromise(r.scope)
	if err != nil {
		t.Fatalf("GetPromise 3: %v", err)
	}
	n1, err := r.scope.Int32(1)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	if ok, err := resolver3.Resolve(r.ctx, n1); err != nil || !ok {
		t.Fatalf("Resolve 3 = (%v, %v), want (true, nil)", ok, err)
	}
	z, err := r.scope.NewString("z")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	reject3OK, reject3Err := resolver3.Reject(r.ctx, z)
	afterRejectFulfilled := snapshot()
	promise3State := promiseState(t, promise3)
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint C: %v", err)
	}
	afterCheckpointC := snapshot()

	want := obj(
		kv("reject_ok", b(true)),
		kv("after_reject", arr(s("WithNoHandler"))),
		kv("catch_attached", b(true)),
		kv("after_catch", arr(s("WithNoHandler"), s("HandlerAddedAfterReject"))),
		// The catch handler fulfills the derived promise: no new event.
		kv("after_checkpoint_a", arr(s("WithNoHandler"), s("HandlerAddedAfterReject"))),
		// The boolean reports call success, not a settlement change.
		kv("reject2_ok", b(true)),
		// No event at reject time: promise2 already has a handler.
		kv("after_prehandled_reject", arr(s("WithNoHandler"), s("HandlerAddedAfterReject"))),
		kv("after_checkpoint_b", arr(s("WithNoHandler"), s("HandlerAddedAfterReject"), s("WithNoHandler"))),
		// Ignored (promise already fulfilled) but still reported as success.
		kv("reject3_ok", b(true)),
		// RejectAfterResolved / ResolveAfterResolved were removed from V8.
		kv("after_reject_fulfilled", arr(s("WithNoHandler"), s("HandlerAddedAfterReject"), s("WithNoHandler"))),
		kv("promise3_state", s("Fulfilled")),
		kv("after_checkpoint_c", arr(s("WithNoHandler"), s("HandlerAddedAfterReject"), s("WithNoHandler"))),
	)
	got := obj(
		kv("reject_ok", resolveBool(rejectOK, rejectErr)),
		kv("after_reject", afterReject),
		kv("catch_attached", b(catchAttached)),
		kv("after_catch", afterCatch),
		kv("after_checkpoint_a", afterCheckpointA),
		kv("reject2_ok", resolveBool(reject2OK, reject2Err)),
		kv("after_prehandled_reject", afterPrehandledReject),
		kv("after_checkpoint_b", afterCheckpointB),
		kv("reject3_ok", resolveBool(reject3OK, reject3Err)),
		kv("after_reject_fulfilled", afterRejectFulfilled),
		kv("promise3_state", s(promise3State)),
		kv("after_checkpoint_c", afterCheckpointC),
	)
	return wantGot("promise/reject_callback_events", want, got)
}

// promiseChecks is the fixed oracle order (src/checks/host/mod.rs).
func promiseChecks() []func(tester) obs {
	return []func(tester) obs{
		checkResolverSettlementSemantics,
		checkNativeThenCheckpoint,
		checkRejectCallbackEvents,
	}
}

// promiseCheckIDs is the registry order of this slice in the host fixture.
var promiseCheckIDs = []string{
	"promise/resolver_settlement_semantics",
	"promise/native_then_checkpoint",
	"promise/reject_callback_events",
}
