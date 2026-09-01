//go:build windows && amd64

package gov8_test

import (
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// TestPromiseNativeFunctionRegistryLifecycle verifies the explicit Close
// contract of NativeFunction and the observable effect of unregistering:
// after Close the function still exists (it is scope-owned) but its Go
// callback is inert, so the reaction job treats it as returning undefined.
func TestPromiseNativeFunctionRegistryLifecycle(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	if _, ok := rt.eval(t, "globalThis.__calls = 0;"); !ok {
		t.Fatal("seed eval failed")
	}
	resolver, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise, _ := resolver.GetPromise(rt.scope)

	handler, err := rt.scope.NewNativeFunction(rt.ctx, func(args []gov8.Value) (gov8.Value, bool) {
		rt.eval(t, "globalThis.__calls = __calls + 1;")
		return gov8.Value{}, false
	})
	if err != nil {
		t.Fatalf("NewNativeFunction: %v", err)
	}

	if _, err := promise.Then(rt.ctx, handler.Value()); err != nil {
		t.Fatalf("Then: %v", err)
	}
	n1, _ := rt.scope.Int32(1)
	if ok, err := resolver.Resolve(rt.ctx, n1); err != nil || !ok {
		t.Fatalf("Resolve = (%v, %v), want (true, nil)", ok, err)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if got := rt.evalText(t, "__calls"); got != "1" {
		t.Fatalf("__calls = %q, want 1", got)
	}

	if err := handler.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := handler.Close(); err == nil {
		t.Fatal("double Close: want error")
	}

	// The unregistered handler is inert: a second reaction job through a
	// fresh promise settles the derived promise with undefined instead.
	resolver2, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise2, _ := resolver2.GetPromise(rt.scope)
	derived2, err := promise2.Then(rt.ctx, handler.Value())
	if err != nil {
		t.Fatalf("Then after Close: %v", err)
	}
	if ok, err := resolver2.Resolve(rt.ctx, n1); err != nil || !ok {
		t.Fatalf("Resolve 2 = (%v, %v), want (true, nil)", ok, err)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint 2: %v", err)
	}
	if got := rt.evalText(t, "__calls"); got != "1" {
		t.Fatalf("__calls after inert dispatch = %q, want 1", got)
	}
	if s, _ := derived2.State(); s != gov8.PromiseFulfilled {
		t.Fatalf("derived2 state = %v, want Fulfilled", s)
	}
	if got := valueText(t, rt, mustResult(t, derived2, rt)); got != "undefined" {
		t.Fatalf("derived2 result = %q, want undefined", got)
	}

	// A freshly registered native function keeps working after another one
	// was closed (registry ids stay disjoint).
	handler2, err := rt.scope.NewNativeFunction(rt.ctx, func(args []gov8.Value) (gov8.Value, bool) {
		rt.eval(t, "globalThis.__calls = __calls + 10;")
		return gov8.Value{}, false
	})
	if err != nil {
		t.Fatalf("NewNativeFunction 2: %v", err)
	}
	defer func() { _ = handler2.Close() }()
	resolver3, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise3, _ := resolver3.GetPromise(rt.scope)
	if _, err := promise3.Then(rt.ctx, handler2.Value()); err != nil {
		t.Fatalf("Then 3: %v", err)
	}
	if ok, err := resolver3.Resolve(rt.ctx, n1); err != nil || !ok {
		t.Fatalf("Resolve 3 = (%v, %v), want (true, nil)", ok, err)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint 3: %v", err)
	}
	if got := rt.evalText(t, "__calls"); got != "11" {
		t.Fatalf("__calls = %q, want 11", got)
	}
}

// TestPromiseRejectCallbackEvents is the condensed Go-side check of the
// oracle's promise/reject_callback_events ordering; the byte-exact
// normalized version lives in conformance-host-promises.
func TestPromiseRejectCallbackEvents(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	var events []gov8.PromiseRejectEvent
	if err := rt.iso.SetPromiseRejectCallback(rt.scope, func(m gov8.PromiseRejectMessage) {
		events = append(events, m.Event)
	}); err != nil {
		t.Fatalf("SetPromiseRejectCallback: %v", err)
	}

	handler, err := rt.scope.NewNativeFunction(rt.ctx, func(args []gov8.Value) (gov8.Value, bool) {
		return gov8.Value{}, false
	})
	if err != nil {
		t.Fatalf("NewNativeFunction: %v", err)
	}
	defer func() { _ = handler.Close() }()

	// Case A: reject without handler, then attach one.
	resolver1, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise1, _ := resolver1.GetPromise(rt.scope)
	var valueSeen string
	var valueOK bool
	if err := rt.iso.SetPromiseRejectCallback(rt.scope, func(m gov8.PromiseRejectMessage) {
		events = append(events, m.Event)
		if v, ok := m.Value(); ok {
			valueOK = true
			valueSeen, _ = v.ToString(rt.ctx)
		}
	}); err != nil {
		t.Fatalf("re-set callback: %v", err)
	}
	x, _ := rt.scope.NewString("x")
	if ok, err := resolver1.Reject(rt.ctx, x); err != nil || !ok {
		t.Fatalf("Reject = (%v, %v), want (true, nil)", ok, err)
	}
	if len(events) != 1 || events[0] != gov8.PromiseRejectWithNoHandler {
		t.Fatalf("events after reject = %v, want [WithNoHandler]", events)
	}
	if !valueOK || valueSeen != "x" {
		t.Fatalf("reject value = (%q, %v), want (x, true)", valueSeen, valueOK)
	}
	if _, ok, err := promise1.Catch(rt.ctx, handler.Value()); err != nil || !ok {
		t.Fatalf("Catch = (%v, %v), want ok", ok, err)
	}
	if len(events) != 2 || events[1] != gov8.PromiseHandlerAddedAfterReject {
		t.Fatalf("events after catch = %v, want +HandlerAddedAfterReject", events)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint A: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events after checkpoint A = %v, want unchanged", events)
	}

	// Case B: handler attached before the reject produces no event at reject
	// time; the derived promise of a bare `then` is reported unhandled at
	// the checkpoint.
	resolver2, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise2, _ := resolver2.GetPromise(rt.scope)
	if _, err := promise2.Then(rt.ctx, handler.Value()); err != nil {
		t.Fatalf("Then: %v", err)
	}
	y, _ := rt.scope.NewString("y")
	if ok, err := resolver2.Reject(rt.ctx, y); err != nil || !ok {
		t.Fatalf("Reject 2 = (%v, %v), want (true, nil)", ok, err)
	}
	if len(events) != 2 {
		t.Fatalf("events after pre-handled reject = %v, want unchanged", events)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint B: %v", err)
	}
	if len(events) != 3 || events[2] != gov8.PromiseRejectWithNoHandler {
		t.Fatalf("events after checkpoint B = %v, want +WithNoHandler", events)
	}

	// Case C: rejecting an already fulfilled promise is ignored and fires no
	// event (AfterResolved events were removed from V8).
	resolver3, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise3, _ := resolver3.GetPromise(rt.scope)
	n1, _ := rt.scope.Int32(1)
	if ok, err := resolver3.Resolve(rt.ctx, n1); err != nil || !ok {
		t.Fatalf("Resolve 3 = (%v, %v), want (true, nil)", ok, err)
	}
	z, _ := rt.scope.NewString("z")
	if ok, err := resolver3.Reject(rt.ctx, z); err != nil || !ok {
		t.Fatalf("Reject 3 = (%v, %v), want (true, nil)", ok, err)
	}
	if len(events) != 3 {
		t.Fatalf("events after rejected-fulfilled = %v, want unchanged", events)
	}
	if s, _ := promise3.State(); s != gov8.PromiseFulfilled {
		t.Fatalf("promise3 state = %v, want Fulfilled", s)
	}

	// Clearing stops delivery entirely.
	if err := rt.iso.ClearPromiseRejectCallback(); err != nil {
		t.Fatalf("ClearPromiseRejectCallback: %v", err)
	}
	resolver4, _ := rt.scope.NewPromiseResolver(rt.ctx)
	promise4, _ := resolver4.GetPromise(rt.scope)
	w, _ := rt.scope.NewString("w")
	if ok, err := resolver4.Reject(rt.ctx, w); err != nil || !ok {
		t.Fatalf("Reject 4 = (%v, %v), want (true, nil)", ok, err)
	}
	if _, ok, err := promise4.Catch(rt.ctx, handler.Value()); err != nil || !ok {
		t.Fatalf("Catch 4 = (%v, %v), want ok", ok, err)
	}
	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("checkpoint D: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events after clear = %v, want unchanged", events)
	}
	if err := rt.iso.ClearPromiseRejectCallback(); err != nil {
		t.Fatalf("second Clear: %v", err)
	}
}

// TestPromiseRejectEventNames pins the oracle event name mapping used by
// the normalized conformance output.
func TestPromiseRejectEventNames(t *testing.T) {
	cases := map[gov8.PromiseRejectEvent]string{
		gov8.PromiseRejectWithNoHandler:     "WithNoHandler",
		gov8.PromiseHandlerAddedAfterReject: "HandlerAddedAfterReject",
		gov8.PromiseRejectAfterResolved:     "RejectAfterResolved",
		gov8.PromiseResolveAfterResolved:    "ResolveAfterResolved",
	}
	for e, want := range cases {
		if got := e.String(); got != want {
			t.Errorf("PromiseRejectEvent(%d).String() = %q, want %q", int(e), got, want)
		}
	}
}

// TestPromiseConcurrentIsolates runs two independent promise workloads on
// two isolates in parallel (each goroutine owns one isolate). The callback
// registries are shared process state, so this exercises their locking
// under -race as well as isolate affinity end to end.
func TestPromiseConcurrentIsolates(t *testing.T) {
	const workers = 2
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			errs[w] = promiseWorker(w)
		}(w)
	}
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", w, err)
		}
	}
}

func promiseWorker(w int) error {
	iso, err := gov8.NewIsolate()
	if err != nil {
		return err
	}
	defer func() { _ = iso.Close() }()
	if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
		return err
	}
	ctx, err := iso.NewContext()
	if err != nil {
		return err
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		return err
	}
	defer func() { _ = scope.Close() }()

	resolver, err := scope.NewPromiseResolver(ctx)
	if err != nil {
		return err
	}
	promise, err := resolver.GetPromise(scope)
	if err != nil {
		return err
	}
	handler, err := scope.NewNativeFunction(ctx, func(args []gov8.Value) (gov8.Value, bool) {
		return gov8.Value{}, false
	})
	if err != nil {
		return err
	}
	defer func() { _ = handler.Close() }()
	if _, err := promise.Then(ctx, handler.Value()); err != nil {
		return err
	}
	n42, err := scope.Int32(int32(42 + w))
	if err != nil {
		return err
	}
	if ok, err := resolver.Resolve(ctx, n42); err != nil || !ok {
		return err
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		return err
	}
	s, err := promise.State()
	if err != nil {
		return err
	}
	if s != gov8.PromiseFulfilled {
		return errPromiseState{s}
	}
	return nil
}

type errPromiseState struct{ s gov8.PromiseState }

func (e errPromiseState) Error() string {
	return "promise not fulfilled after checkpoint"
}
