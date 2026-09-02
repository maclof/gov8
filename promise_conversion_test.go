//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func mustEvalPromiseValue(t *testing.T, rt *promiseRT, source string) gov8.Value {
	t.Helper()
	v, ok := rt.eval(t, source)
	if !ok {
		t.Fatalf("eval %q failed", source)
	}
	return v
}

func mustPromiseResultText(t *testing.T, rt *promiseRT, p gov8.Promise) string {
	t.Helper()
	v, err := p.Result(rt.scope)
	if err != nil {
		t.Fatalf("Promise.Result: %v", err)
	}
	return valueText(t, rt, v)
}

func TestAsPromiseJSValuesAndTypedOperations(t *testing.T) {
	rt := newPromiseRT(t)
	defer rt.close(t)

	resolved, err := gov8.AsPromise(mustEvalPromiseValue(t, rt, "Promise.resolve(42)"))
	if err != nil {
		t.Fatalf("AsPromise(resolved): %v", err)
	}
	if state := mustState(t, *resolved); state != gov8.PromiseFulfilled {
		t.Fatalf("resolved state = %v, want Fulfilled", state)
	}
	if got := mustPromiseResultText(t, rt, *resolved); got != "42" {
		t.Fatalf("resolved result = %q, want 42", got)
	}

	rejected, err := gov8.AsPromise(mustEvalPromiseValue(t, rt, "Promise.reject('boom')"))
	if err != nil {
		t.Fatalf("AsPromise(rejected): %v", err)
	}
	if state := mustState(t, *rejected); state != gov8.PromiseRejected {
		t.Fatalf("rejected state = %v, want Rejected", state)
	}
	if got := mustPromiseResultText(t, rt, *rejected); got != "boom" {
		t.Fatalf("rejected result = %q, want boom", got)
	}

	thenHandler := mustEvalPromiseValue(t, rt, "x => x + 1")
	derivedThen, err := resolved.Then(rt.ctx, thenHandler)
	if err != nil {
		t.Fatalf("Promise.Then: %v", err)
	}
	if state := mustState(t, derivedThen); state != gov8.PromisePending {
		t.Fatalf("Then initial state = %v, want Pending", state)
	}

	catchHandler := mustEvalPromiseValue(t, rt, "x => 'caught:' + x")
	derivedCatch, attached, err := rejected.Catch(rt.ctx, catchHandler)
	if err != nil || !attached {
		t.Fatalf("Promise.Catch = attached %v, err %v", attached, err)
	}
	if state := mustState(t, derivedCatch); state != gov8.PromisePending {
		t.Fatalf("Catch initial state = %v, want Pending", state)
	}

	if err := rt.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatalf("PerformMicrotaskCheckpoint: %v", err)
	}
	if state := mustState(t, derivedThen); state != gov8.PromiseFulfilled {
		t.Fatalf("Then final state = %v, want Fulfilled", state)
	}
	if got := mustPromiseResultText(t, rt, derivedThen); got != "43" {
		t.Fatalf("Then result = %q, want 43", got)
	}
	if state := mustState(t, derivedCatch); state != gov8.PromiseFulfilled {
		t.Fatalf("Catch final state = %v, want Fulfilled", state)
	}
	if got := mustPromiseResultText(t, rt, derivedCatch); got != "caught:boom" {
		t.Fatalf("Catch result = %q, want caught:boom", got)
	}
}

func TestAsPromiseValidationLifecycleAndAffinity(t *testing.T) {
	t.Run("wrong type", func(t *testing.T) {
		rt := newPromiseRT(t)
		defer rt.close(t)
		if p, err := gov8.AsPromise(mustEvalPromiseValue(t, rt, "42")); err == nil || p != nil {
			t.Fatalf("AsPromise(number) = %v, %v; want nil, error", p, err)
		}
		if p, err := gov8.AsPromise(gov8.Value{}); err == nil || p != nil {
			t.Fatalf("AsPromise(zero) = %v, %v; want nil, error", p, err)
		}
	})

	t.Run("stale scope", func(t *testing.T) {
		rt := newPromiseRT(t)
		v := mustEvalPromiseValue(t, rt, "Promise.resolve(1)")
		if err := rt.scope.Close(); err != nil {
			t.Fatalf("Scope.Close: %v", err)
		}
		if p, err := gov8.AsPromise(v); err == nil || p != nil {
			t.Fatalf("AsPromise(stale) = %v, %v; want nil, error", p, err)
		}
		if err := rt.ctx.Close(); err != nil {
			t.Errorf("Context.Close: %v", err)
		}
		if err := rt.iso.Close(); err != nil {
			t.Errorf("Isolate.Close: %v", err)
		}
	})

	t.Run("wrong thread and foreign context", func(t *testing.T) {
		rt := newPromiseRT(t)
		defer rt.close(t)
		v := mustEvalPromiseValue(t, rt, "Promise.resolve(1)")
		errCh := make(chan error, 1)
		go func() {
			_, err := gov8.AsPromise(v)
			errCh <- err
		}()
		if err := <-errCh; err == nil {
			t.Fatal("AsPromise on foreign thread succeeded")
		}

		promise, err := gov8.AsPromise(v)
		if err != nil {
			t.Fatalf("AsPromise on owner thread: %v", err)
		}
		foreign := newPromiseRT(t)
		defer foreign.close(t)
		handler := mustEvalPromiseValue(t, rt, "x => x")
		if _, err := promise.Then(foreign.ctx, handler); err == nil || !strings.Contains(err.Error(), "different isolate") {
			t.Fatalf("Then(foreign context) error = %v", err)
		}
	})
}
