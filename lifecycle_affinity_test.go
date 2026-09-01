//go:build windows && amd64

package gov8_test

import (
	"sync"
	"testing"

	gov8 "gov8"
)

func TestIsolateThreadAffinity(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()

	// Operations from a foreign goroutine must fail with an affinity error,
	// not crash or succeed.
	errCh := make(chan error, 1)
	go func() {
		_, err := iso.NewContext()
		errCh <- err
	}()
	if err := <-errCh; err == nil {
		t.Fatal("NewContext from foreign goroutine must fail")
	} else if !syncIsAffinity(err) {
		t.Fatalf("error = %v, want affinity violation", err)
	}

	// Close from a foreign goroutine must fail too.
	go func() { errCh <- iso.Close() }()
	if err := <-errCh; err == nil {
		t.Fatal("Close from foreign goroutine must fail")
	}
}

func syncIsAffinity(err error) bool {
	return err != nil && syncContains(err.Error(), "thread affinity")
}

func syncContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestScopeCloseInvalidatesValues(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	v, err := scope.NewString("scoped")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("scope.Close: %v", err)
	}
	// Values from the closed scope must error instead of touching the engine.
	if _, err := v.ToString(ctx); err == nil {
		t.Fatal("ToString on value from closed scope must fail")
	}
	if _, err := v.IsString(); err == nil {
		t.Fatal("IsString on value from closed scope must fail")
	}

	// Reopening a scope and doing fresh work must be fine.
	scope2, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope2.Close() }()
	s2, err := scope2.NewString("ok")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if txt, err := s2.ToString(ctx); err != nil || txt != "ok" {
		t.Fatalf("ToString = %q, %v", txt, err)
	}
}

func TestUseAfterCloseErrors(t *testing.T) {
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
	for _, c := range []interface{ Close() error }{scope, ctx, iso} {
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	if _, err := iso.NewScope(); err == nil {
		t.Fatal("NewScope on closed isolate must fail")
	}
	if _, err := iso.NewContext(); err == nil {
		t.Fatal("NewContext on closed isolate must fail")
	}
	if err := iso.Close(); err == nil {
		t.Fatal("double Close must fail")
	}
}

func TestGlobalObjectAccess(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	if _, err := eval(t, ctx, scope, "globalThis.gv = 7;"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	gv, ok, err := global.GetByName(scope, ctx, "gv")
	if err != nil || !ok {
		t.Fatalf("Get(gv): ok=%v err=%v", ok, err)
	}
	n, iok, err := gv.IntegerValue(ctx)
	if err != nil || !iok || n != 7 {
		t.Fatalf("IntegerValue(gv) = %d, %v, %v", n, iok, err)
	}
	nv, err := scope.Number(42)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	setOK, err := global.SetByName(scope, ctx, "nv", nv)
	if err != nil || !setOK {
		t.Fatalf("Set(nv) = %v, %v", setOK, err)
	}
	v, err := eval(t, ctx, scope, "nv")
	if err != nil {
		t.Fatalf("eval nv: %v", err)
	}
	if txt, _ := v.ToString(ctx); txt != "42" {
		t.Fatalf("script read of nv = %q", txt)
	}
}

func TestScriptIDs(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	s1, err := ctx.Compile(scope, "1 + 1", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() { _ = s1.Close() }()
	id1, err := s1.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}

	s2, err := ctx.Compile(scope, "1 + 1", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() { _ = s2.Close() }()
	id2, err := s2.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}

	s3, err := ctx.Compile(scope, "2 + 2", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer func() { _ = s3.Close() }()
	id3, err := s3.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}

	if id1 != id2 {
		t.Fatalf("same source ids differ: %d != %d", id1, id2)
	}
	if id1 == id3 {
		t.Fatal("distinct source ids equal")
	}
	if id3 <= id1 {
		t.Fatalf("ids not increasing: %d then %d", id1, id3)
	}
}

func TestDefaultMicrotaskPolicyIsAuto(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	p, err := iso.GetMicrotasksPolicy()
	if err != nil || p != gov8.PolicyAuto {
		t.Fatalf("default policy = %v, %v; want Auto", p, err)
	}

	mq, err := ctx.GetMicrotaskQueue()
	if err != nil || mq == 0 {
		t.Fatalf("context default microtask queue = %x, %v; want non-zero", mq, err)
	}

	if _, err := eval(t, ctx, scope, "globalThis.__x = 0; Promise.resolve().then(() => __x = 1);"); err != nil {
		t.Fatalf("eval: %v", err)
	}
	v, err := eval(t, ctx, scope, "__x")
	if err != nil {
		t.Fatalf("eval __x: %v", err)
	}
	if txt, _ := v.ToString(ctx); txt != "1" {
		t.Fatalf("Auto policy did not run jobs before Script::run returned: __x=%s", txt)
	}
}

func TestNativeMicrotaskQueue(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	mq, err := iso.NewMicrotaskQueue(gov8.PolicyExplicit)
	if err != nil {
		t.Fatalf("NewMicrotaskQueue: %v", err)
	}
	defer func() { _ = mq.Close() }()

	if err := ctx.SetMicrotaskQueue(mq); err != nil {
		t.Fatalf("SetMicrotaskQueue: %v", err)
	}
	raw, err := ctx.GetMicrotaskQueue()
	if err != nil {
		t.Fatalf("GetMicrotaskQueue: %v", err)
	}
	wantRaw, _ := mq.Raw()
	if raw != wantRaw {
		t.Fatalf("attached queue = %x, want %x", raw, wantRaw)
	}

	if _, err := eval(t, ctx, scope, "globalThis.__order = [];"+
		"Promise.resolve().then(() => __order.push('n1'));"+
		"Promise.resolve().then(() => __order.push('n2'));"); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := orderString(t, ctx, scope); got != "" {
		t.Fatalf("after run = %q, want empty", got)
	}
	if err := mq.PerformCheckpoint(ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if got := orderString(t, ctx, scope); got != "n1,n2" {
		// Diagnostic: did the jobs land on the isolate default queue instead?
		if err := iso.PerformMicrotaskCheckpoint(); err == nil {
			t.Logf("DIAG: after isolate checkpoint order=%q", orderString(t, ctx, scope))
		}
		t.Fatalf("after checkpoint = %q, want n1,n2", got)
	}

	fn, err := eval(t, ctx, scope, "() => __order.push('native')")
	if err != nil {
		t.Fatalf("eval fn: %v", err)
	}
	if err := mq.Enqueue(ctx, fn); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := mq.PerformCheckpoint(ctx); err != nil {
		t.Fatalf("checkpoint 2: %v", err)
	}
	if got := orderString(t, ctx, scope); got != "n1,n2,native" {
		t.Fatalf("after native enqueue = %q, want n1,n2,native", got)
	}
}

func TestParallelIsolates(t *testing.T) {
	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			iso, err := gov8.NewIsolate()
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = iso.Close() }()
			ctx, err := iso.NewContext()
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = ctx.Close() }()
			scope, err := iso.NewScope()
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = scope.Close() }()
			script, err := ctx.Compile(scope, "1 + 1", nil)
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = script.Close() }()
			res, err := script.Run(scope, nil)
			if err != nil {
				errs[i] = err
				return
			}
			if txt, _ := res.ToString(ctx); txt != "2" {
				t.Errorf("goroutine %d: result = %q", i, txt)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
}
