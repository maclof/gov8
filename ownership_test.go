//go:build windows && amd64

package gov8_test

import (
	"strings"
	"sync"
	"testing"

	gov8 "gov8"
)

// twoIsolates creates two live isolates on the calling goroutine, each with
// a context and an open scope. Creating two isolates sequentially on one
// goroutine is legal (LockOSThread nests), and it is exactly the setup in
// which same-thread cross-isolate misuse would otherwise pass every
// affinity check and reach the engine with foreign handles.
func twoIsolates(t *testing.T) (isoA, isoB *gov8.Isolate, ctxA, ctxB *gov8.Context, scopeA, scopeB *gov8.Scope) {
	t.Helper()
	isoA, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate A: %v", err)
	}
	isoB, err = gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate B: %v", err)
	}
	ctxA, err = isoA.NewContext()
	if err != nil {
		t.Fatalf("NewContext A: %v", err)
	}
	ctxB, err = isoB.NewContext()
	if err != nil {
		t.Fatalf("NewContext B: %v", err)
	}
	scopeA, err = isoA.NewScope()
	if err != nil {
		t.Fatalf("NewScope A: %v", err)
	}
	scopeB, err = isoB.NewScope()
	if err != nil {
		t.Fatalf("NewScope B: %v", err)
	}
	t.Cleanup(func() {
		if err := scopeB.Close(); err != nil {
			t.Errorf("scopeB.Close: %v", err)
		}
		if err := scopeA.Close(); err != nil {
			t.Errorf("scopeA.Close: %v", err)
		}
		if err := ctxB.Close(); err != nil {
			t.Errorf("ctxB.Close: %v", err)
		}
		if err := ctxA.Close(); err != nil {
			t.Errorf("ctxA.Close: %v", err)
		}
		if err := isoB.Close(); err != nil {
			t.Errorf("isoB.Close: %v", err)
		}
		if err := isoA.Close(); err != nil {
			t.Errorf("isoA.Close: %v", err)
		}
	})
	return isoA, isoB, ctxA, ctxB, scopeA, scopeB
}

func wantForeignIsolateError(t *testing.T, name string, fn func() error) {
	t.Helper()
	err := fn()
	if err == nil {
		t.Errorf("%s: must fail with a cross-isolate ownership error", name)
		return
	}
	if !strings.Contains(err.Error(), "different isolate") {
		t.Errorf("%s: err = %v, want a different-isolate ownership error", name, err)
	}
}

func TestCrossIsolateOwnershipRejections(t *testing.T) {
	isoA, isoB, ctxA, ctxB, scopeA, scopeB := twoIsolates(t)

	scriptA, err := ctxA.Compile(scopeA, "1 + 1", nil)
	if err != nil {
		t.Fatalf("Compile A: %v", err)
	}
	defer func() { _ = scriptA.Close() }()

	tcA, err := isoA.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch A: %v", err)
	}
	defer func() { _ = tcA.Close() }()
	tcB, err := isoB.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch B: %v", err)
	}
	defer func() { _ = tcB.Close() }()

	tcClosed, err := isoA.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch closed-probe: %v", err)
	}
	if err := tcClosed.Close(); err != nil {
		t.Fatalf("tcClosed.Close: %v", err)
	}

	mqA, err := isoA.NewMicrotaskQueue(gov8.PolicyExplicit)
	if err != nil {
		t.Fatalf("NewMicrotaskQueue A: %v", err)
	}
	defer func() { _ = mqA.Close() }()
	mqB, err := isoB.NewMicrotaskQueue(gov8.PolicyExplicit)
	if err != nil {
		t.Fatalf("NewMicrotaskQueue B: %v", err)
	}
	defer func() { _ = mqB.Close() }()

	fnA, err := eval(t, ctxA, scopeA, "() => 1")
	if err != nil {
		t.Fatalf("eval fnA: %v", err)
	}
	fnB, err := eval(t, ctxB, scopeB, "() => 1")
	if err != nil {
		t.Fatalf("eval fnB: %v", err)
	}
	valB, err := scopeB.Number(11)
	if err != nil {
		t.Fatalf("Number B: %v", err)
	}
	objA, err := ctxA.GlobalObject(scopeA)
	if err != nil {
		t.Fatalf("GlobalObject A: %v", err)
	}

	// --- foreign scope / TryCatch on compile and run ---------------------
	wantForeignIsolateError(t, "GlobalObject(scopeB)", func() error {
		_, err := ctxA.GlobalObject(scopeB)
		return err
	})
	wantForeignIsolateError(t, "Compile(scopeB)", func() error {
		_, err := ctxA.Compile(scopeB, "1 + 1", nil)
		return err
	})
	wantForeignIsolateError(t, "Compile(tcB)", func() error {
		_, err := ctxA.Compile(scopeA, "1 + 1", tcB)
		return err
	})
	wantForeignIsolateError(t, "Run(scopeB)", func() error {
		_, err := scriptA.Run(scopeB, nil)
		return err
	})
	wantForeignIsolateError(t, "Run(tcB)", func() error {
		_, err := scriptA.Run(scopeA, tcB)
		return err
	})

	// --- closed TryCatch must not reach the shim -------------------------
	if _, err := scriptA.Run(scopeA, tcClosed); err == nil ||
		!strings.Contains(err.Error(), "trycatch used after Close") {
		t.Errorf("Run(closed tc) = %v, want closed-trycatch error", err)
	}
	if _, err := ctxA.Compile(scopeA, "1 + 1", tcClosed); err == nil ||
		!strings.Contains(err.Error(), "trycatch used after Close") {
		t.Errorf("Compile(closed tc) = %v, want closed-trycatch error", err)
	}

	// --- foreign scope / context on TryCatch readers ----------------------
	wantForeignIsolateError(t, "MessageText(scopeB, ctxA)", func() error {
		_, err := tcA.MessageText(scopeB, ctxA)
		return err
	})
	wantForeignIsolateError(t, "MessageText(scopeA, ctxB)", func() error {
		_, err := tcA.MessageText(scopeA, ctxB)
		return err
	})
	wantForeignIsolateError(t, "ExceptionText(scopeB, ctxA)", func() error {
		_, err := tcA.ExceptionText(scopeB, ctxA)
		return err
	})
	wantForeignIsolateError(t, "StartPosition(scopeB)", func() error {
		_, err := tcA.StartPosition(scopeB)
		return err
	})
	wantForeignIsolateError(t, "StartColumn(scopeB)", func() error {
		_, err := tcA.StartColumn(scopeB)
		return err
	})
	wantForeignIsolateError(t, "LineNumber(scopeB, ctxA)", func() error {
		_, _, err := tcA.LineNumber(scopeB, ctxA)
		return err
	})
	wantForeignIsolateError(t, "LineNumber(scopeA, ctxB)", func() error {
		_, _, err := tcA.LineNumber(scopeA, ctxB)
		return err
	})

	// --- microtask queue ownership ----------------------------------------
	wantForeignIsolateError(t, "SetMicrotaskQueue(mqB)", func() error {
		return ctxA.SetMicrotaskQueue(mqB)
	})
	wantForeignIsolateError(t, "mqA.PerformCheckpoint(ctxB)", func() error {
		return mqA.PerformCheckpoint(ctxB)
	})
	wantForeignIsolateError(t, "mqA.Enqueue(fnB)", func() error {
		return mqA.Enqueue(ctxA, fnB)
	})
	wantForeignIsolateError(t, "mqA.Enqueue(ctxB, fnA)", func() error {
		return mqA.Enqueue(ctxB, fnA)
	})

	// --- object property ownership -----------------------------------------
	wantForeignIsolateError(t, "GetByName(scopeB, ctxA)", func() error {
		_, _, err := objA.GetByName(scopeB, ctxA, "missing")
		return err
	})
	wantForeignIsolateError(t, "SetByName(value from B)", func() error {
		_, err := objA.SetByName(scopeA, ctxA, "foreign", valB)
		return err
	})
	wantForeignIsolateError(t, "SetByName(scopeB, ctxA)", func() error {
		v, err := scopeA.Number(1)
		if err != nil {
			return err
		}
		_, err = objA.SetByName(scopeB, ctxA, "foreign", v)
		return err
	})

	// --- context conversions on foreign contexts ---------------------------
	wantForeignIsolateError(t, "ToString(ctxB)", func() error {
		_, err := fnA.ToString(ctxB)
		return err
	})
	wantForeignIsolateError(t, "NumberValue(ctxB)", func() error {
		_, _, err := valB.NumberValue(ctxA)
		return err
	})
	wantForeignIsolateError(t, "IntegerValue(ctxB)", func() error {
		_, _, err := valB.IntegerValue(ctxA)
		return err
	})
	wantForeignIsolateError(t, "Int32Value(ctxB)", func() error {
		_, _, err := valB.Int32Value(ctxA)
		return err
	})
	wantForeignIsolateError(t, "Uint32Value(ctxB)", func() error {
		_, _, err := valB.Uint32Value(ctxA)
		return err
	})

	// --- positive control: same-isolate paths still work --------------------
	if _, err := scriptA.Run(scopeA, nil); err != nil {
		t.Errorf("same-isolate Run: %v", err)
	}
	own, err := scopeA.Number(3)
	if err != nil {
		t.Fatalf("Number A: %v", err)
	}
	if ok, err := objA.SetByName(scopeA, ctxA, "own", own); err != nil || !ok {
		t.Errorf("same-isolate SetByName = %v, %v", ok, err)
	}
	got, ok, err := objA.GetByName(scopeA, ctxA, "own")
	if err != nil || !ok {
		t.Fatalf("same-isolate GetByName = %v, %v", ok, err)
	}
	if n, _, err := got.IntegerValue(ctxA); err != nil || n != 3 {
		t.Errorf("same-isolate roundtrip = %d, %v", n, err)
	}
}

func TestWrongThreadChildWrapperMisuse(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	script, err := ctx.Compile(scope, "1 + 1", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() { _ = script.Close() }()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	mq, err := iso.NewMicrotaskQueue(gov8.PolicyExplicit)
	if err != nil {
		t.Fatalf("NewMicrotaskQueue: %v", err)
	}
	defer func() { _ = mq.Close() }()

	// Every engine-touching entry point must fail with an affinity error on
	// a foreign thread BEFORE reading any wrapper state owned by the
	// isolate's thread.
	errs := make(chan error, 16)
	probe := func(name string, fn func() error) {
		go func() { errs <- fn() }()
		if err := <-errs; err == nil {
			t.Errorf("%s from foreign goroutine must fail", name)
		} else if !strings.Contains(err.Error(), "affinity") &&
			!strings.Contains(err.Error(), "wrong thread") {
			t.Errorf("%s from foreign goroutine = %v, want affinity error", name, err)
		}
	}

	probe("Scope.Close", scope.Close)
	probe("Context.Close", ctx.Close)
	probe("Script.Close", script.Close)
	probe("TryCatch.Close", tc.Close)
	probe("MicrotaskQueue.Close", mq.Close)
	probe("Isolate.Close", iso.Close)
	probe("Script.ID", func() error { _, err := script.ID(); return err })
	probe("TryCatch.HasCaught", func() error { _, err := tc.HasCaught(); return err })
	probe("MicrotaskQueue.Raw", func() error { _, err := mq.Raw(); return err })
	probe("Scope.Undefined", func() error { _, err := scope.Undefined(); return err })
	probe("Context.GetMicrotaskQueue", func() error { _, err := ctx.GetMicrotaskQueue(); return err })
	probe("Isolate.GetMicrotasksPolicy", func() error { _, err := iso.GetMicrotasksPolicy(); return err })

	// The isolate must still be fully usable on its owning thread after all
	// that rejected misuse.
	res, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("owning-thread Run after misuse probes: %v", err)
	}
	if txt, err := res.ToString(ctx); err != nil || txt != "2" {
		t.Fatalf("owning-thread result = %q, %v", txt, err)
	}
}

func TestConcurrentIsolateLifecycleStorm(t *testing.T) {
	// Concurrent create/use/close of isolates must not wedge the process
	// lifecycle: after every isolate is closed, the wrapper's teardown
	// accounting must be exactly empty (verified by Dispose succeeding in
	// internal/lifecycle on the same invariant, and here by all operations
	// succeeding under the race detector).
	const workers = 6
	const perWorker = 3
	var wg sync.WaitGroup
	errs := make([][]error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				errs[w] = append(errs[w], stormOnce())
			}
		}(w)
	}
	wg.Wait()
	for w, list := range errs {
		for j, err := range list {
			if err != nil {
				t.Fatalf("worker %d iteration %d: %v", w, j, err)
			}
		}
	}
}

func stormOnce() error {
	iso, err := gov8.NewIsolate()
	if err != nil {
		return err
	}
	defer func() { _ = iso.Close() }()
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
	script, err := ctx.Compile(scope, "21 * 2", nil)
	if err != nil {
		return err
	}
	defer func() { _ = script.Close() }()
	res, err := script.Run(scope, nil)
	if err != nil {
		return err
	}
	if txt, err := res.ToString(ctx); err != nil || txt != "42" {
		return err
	}
	return nil
}
