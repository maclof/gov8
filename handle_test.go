//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Public Global/Weak handle tests, mirroring the pinned Rust oracle's
// handle checks (registry prefix handle/).

// hndNewObject creates a plain JS object via the engine (v8::Object::new
// analog) by evaluating an object literal.
func hndNewObject(t *testing.T, ctx *gov8.Context, scope *gov8.Scope) gov8.Value {
	t.Helper()
	v, err := eval(t, ctx, scope, "({})")
	if err != nil {
		t.Fatalf("new object: %v", err)
	}
	if is, _ := v.IsObject(); !is {
		t.Fatal("object literal did not produce an object")
	}
	return v
}

// hndNewObjectGlobal roots a fresh plain object in a strong global and
// closes the object's local scope, so the global is the only strong
// reference afterwards (mirroring the pinned checks, which drop the
// HandleScope before forcing collection).
func hndNewObjectGlobal(t *testing.T, iso *gov8.Isolate, ctx *gov8.Context) *gov8.Global {
	t.Helper()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	v := hndNewObject(t, ctx, scope)
	g, err := gov8.NewGlobal(scope, v)
	if err != nil {
		t.Fatalf("NewGlobal: %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("scope.Close: %v", err)
	}
	return g
}

func TestGlobalRoundtripAndIdentity(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	original, err := gov8.NewGlobal(scope, hndNewObject(t, ctx, scope))
	if err != nil {
		t.Fatalf("NewGlobal: %v", err)
	}
	keeper, err := original.Clone() // new global cell, same object
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	distinct, err := gov8.NewGlobal(scope, hndNewObject(t, ctx, scope))
	if err != nil {
		t.Fatalf("NewGlobal distinct: %v", err)
	}
	raw, err := original.IntoRaw()
	if err != nil {
		t.Fatalf("IntoRaw: %v", err)
	}
	if err := original.Close(); err == nil {
		t.Error("consumed global must not be closable")
	}
	// Re-adopt the raw cell outside any scope.
	restored, err := gov8.GlobalFromRaw(iso, raw)
	if err != nil {
		t.Fatalf("GlobalFromRaw: %v", err)
	}

	roundtripEqual, err := restored.Equal(keeper)
	if err != nil || !roundtripEqual {
		t.Errorf("roundtrip equal = %v, %v; want true", roundtripEqual, err)
	}
	distinctUnequal, err := restored.Equal(distinct)
	if err != nil {
		t.Errorf("distinct Equal: %v", err)
	} else if distinctUnequal {
		// fixture: distinct_unequal=true means distinct objects compare
		// unequal (Equal returns false).
		t.Errorf("distinct Equal = %v; want false (objects differ)", distinctUnequal)
	}

	for _, g := range []*gov8.Global{restored, keeper, distinct} {
		if err := g.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

func TestGlobalEqCrossIsolate(t *testing.T) {
	isoA, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate A: %v", err)
	}
	ctxA, err := isoA.NewContext()
	if err != nil {
		t.Fatalf("NewContext A: %v", err)
	}
	scopeA, err := isoA.NewScope()
	if err != nil {
		t.Fatalf("NewScope A: %v", err)
	}
	globalA, err := gov8.NewGlobal(scopeA, hndNewObject(t, ctxA, scopeA))
	if err != nil {
		t.Fatalf("NewGlobal A: %v", err)
	}

	// The cross-isolate comparison runs while both isolates are alive.
	crossIsolateEqual := false
	func() {
		isoB, err := gov8.NewIsolate()
		if err != nil {
			t.Fatalf("NewIsolate B: %v", err)
		}
		defer func() { _ = isoB.Close() }()
		ctxB, err := isoB.NewContext()
		if err != nil {
			t.Fatalf("NewContext B: %v", err)
		}
		scopeB, err := isoB.NewScope()
		if err != nil {
			t.Fatalf("NewScope B: %v", err)
		}
		globalB, err := gov8.NewGlobal(scopeB, hndNewObject(t, ctxB, scopeB))
		if err != nil {
			t.Fatalf("NewGlobal B: %v", err)
		}
		crossIsolateEqual, err = globalA.Equal(globalB)
		if err != nil {
			t.Fatalf("cross-isolate Equal: %v", err)
		}
	}()

	ownLocalEqual := false
	func() {
		scope, err := isoA.NewScope()
		if err != nil {
			t.Fatalf("reopen scope: %v", err)
		}
		defer func() { _ = scope.Close() }()
		reopened, err := globalA.ToLocal(scope)
		if err != nil {
			t.Fatalf("ToLocal: %v", err)
		}
		ownLocalEqual, err = globalA.Equal(mustGlobal(t, scope, ctxA, reopened))
		if err != nil {
			t.Fatalf("own-local Equal: %v", err)
		}
	}()

	if crossIsolateEqual {
		t.Error("cross-isolate globals must compare unequal")
	}
	if !ownLocalEqual {
		t.Error("global must equal locals reopened from it")
	}

	_ = scopeA.Close()
	_ = ctxA.Close()
	_ = globalA.Close()
	_ = isoA.Close()
}

// mustGlobal roots a value for an equality check and releases it via t.Cleanup.
func mustGlobal(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, v gov8.Value) *gov8.Global {
	t.Helper()
	g, err := gov8.NewGlobal(scope, v)
	if err != nil {
		t.Fatalf("NewGlobal: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g
}

// TestGlobalDropAfterIsolateDispose mirrors the pinned
// handle/global_drop_after_isolate_dispose: closing the global after its
// host isolate was disposed must be a silent no-op, not a panic, and the
// wrapper must never touch the dead engine. Equal after dispose returns an
// error instead of the pinned crate's panic (documented deviation).
func TestGlobalDropAfterIsolateDispose(t *testing.T) {
	var global *gov8.Global
	func() {
		iso, err := gov8.NewIsolate()
		if err != nil {
			t.Fatalf("NewIsolate: %v", err)
		}
		defer func() { _ = iso.Close() }()
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		global, err = gov8.NewGlobal(scope, hndNewObject(t, ctx, scope))
		if err != nil {
			t.Fatalf("NewGlobal: %v", err)
		}
		_ = scope.Close()
		_ = ctx.Close()
	}() // isolate disposed here, global outlives it

	if err := global.Close(); err != nil { // must be a silent no-op
		t.Errorf("Close after isolate dispose = %v; want nil", err)
	}
	if err := global.Close(); err == nil {
		t.Error("second Close must report the closed global")
	}
}

func TestWeakFinalizerFiresAfterForcedGC(t *testing.T) {
	iso, ctx, _ := newTestRuntime(t)

	strong := hndNewObjectGlobal(t, iso, ctx)
	events := []string(nil)
	weak, err := strong.NewWeakWithFinalizer(func(i *gov8.Isolate) {
		events = append(events, "finalizer")
	})
	if err != nil {
		t.Fatalf("NewWeakWithFinalizer: %v", err)
	}

	aliveBeforeGC := false
	if empty, _ := weak.IsEmpty(); !empty {
		aliveBeforeGC = true
	}
	if err := strong.Close(); err != nil {
		t.Fatalf("strong.Close: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification 2: %v", err)
	}

	collectedAfterGC, _ := weak.IsEmpty()
	_, resurrectOK, _ := weak.ToGlobal()
	finalizerFired := false
	for _, e := range events {
		if e == "finalizer" {
			finalizerFired = true
		}
	}
	if err := weak.Close(); err != nil {
		t.Errorf("weak.Close: %v", err)
	}

	if !aliveBeforeGC {
		t.Error("weak must be alive while the strong global holds the object")
	}
	if !collectedAfterGC {
		t.Error("weak must be collected after forced GCs")
	}
	if resurrectOK {
		t.Error("to_global must yield none after collection")
	}
	if !finalizerFired {
		t.Error("finalizer must have fired")
	}
}

func TestWeakGuaranteedFinalizerRunsByTeardown(t *testing.T) {
	events := []string(nil)
	func() {
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
		strong := hndNewObjectGlobal(t, iso, ctx)
		weak, err := strong.NewWeakWithGuaranteedFinalizer(func() {
			events = append(events, "guaranteed")
		})
		if err != nil {
			t.Fatalf("NewWeakWithGuaranteedFinalizer: %v", err)
		}
		// The weak is deliberately never closed in the usual order: the
		// drain below consumes it instead.
		_ = weak
		// Last strong reference gone; the weak keeps its cell.
		if err := strong.Close(); err != nil {
			t.Errorf("strong.Close: %v", err)
		}
		// The pinned crate drains guaranteed finalizers inside the isolate
		// Drop; the Go analog is the explicit drain before Isolate.Close
		// (documented deviation: same guarantee, explicit step).
		_ = scope.Close()
		_ = ctx.Close()
		if err := gov8.DrainGuaranteedWeakFinalizers(iso); err != nil {
			t.Errorf("DrainGuaranteedWeakFinalizers: %v", err)
		}
		if err := iso.Close(); err != nil {
			t.Fatalf("iso.Close: %v", err)
		}
	}()
	firedAfterTeardown := false
	for _, e := range events {
		if e == "guaranteed" {
			firedAfterTeardown = true
		}
	}
	if !firedAfterTeardown {
		t.Fatal("guaranteed finalizer must have run by isolate teardown")
	}
}

func TestWeakDropCancelsFinalizer(t *testing.T) {
	iso, ctx, _ := newTestRuntime(t)

	strong := hndNewObjectGlobal(t, iso, ctx)
	var events []string
	weak, err := strong.NewWeakWithFinalizer(func(i *gov8.Isolate) {
		events = append(events, "cancelled-should-not-run")
	})
	if err != nil {
		t.Fatalf("NewWeakWithFinalizer: %v", err)
	}
	// Cancels the finalizer; the object is still strongly held.
	if err := weak.Close(); err != nil {
		t.Fatalf("weak.Close: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification 2: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("cancelled finalizer fired: %v", events)
	}
	if err := strong.Close(); err != nil {
		t.Fatalf("strong.Close: %v", err)
	}
}

func TestWeakEqualityAndClone(t *testing.T) {
	iso, ctx, _ := newTestRuntime(t)

	strong := hndNewObjectGlobal(t, iso, ctx)
	weak, err := strong.NewWeak()
	if err != nil {
		t.Fatalf("NewWeak: %v", err)
	}
	weakClone, err := weak.Clone() // documented: clone carries no finalizer
	if err != nil {
		t.Fatalf("Weak.Clone: %v", err)
	}

	weakEqualsClone, err := weak.EqualWeak(weakClone)
	if err != nil || !weakEqualsClone {
		t.Errorf("weak == clone = %v, %v; want true", weakEqualsClone, err)
	}
	weakEqualsGlobal, err := weak.EqualGlobal(strong)
	if err != nil || !weakEqualsGlobal {
		t.Errorf("weak == global = %v, %v; want true", weakEqualsGlobal, err)
	}
	toLocalSome := false
	func() {
		s, err := iso.NewScope()
		if err != nil {
			t.Fatalf("scope: %v", err)
		}
		defer func() { _ = s.Close() }()
		if _, ok, err := weak.ToLocal(s); err == nil && ok {
			toLocalSome = true
		}
	}()

	if err := strong.Close(); err != nil {
		t.Fatalf("strong.Close: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification 2: %v", err)
	}

	collectedEmpty, _ := weak.IsEmpty()
	collectedCloneEmpty, _ := weakClone.IsEmpty()
	collectedToLocalNone := false
	func() {
		s, err := iso.NewScope()
		if err != nil {
			t.Fatalf("scope 2: %v", err)
		}
		defer func() { _ = s.Close() }()
		if _, ok, err := weak.ToLocal(s); err == nil && !ok {
			collectedToLocalNone = true
		}
	}()

	empty1, err := iso.EmptyWeak()
	if err != nil {
		t.Fatalf("EmptyWeak: %v", err)
	}
	empty2, err := iso.EmptyWeak()
	if err != nil {
		t.Fatalf("EmptyWeak 2: %v", err)
	}
	emptyEqualsEmpty, err := empty1.EqualWeak(empty2)
	if err != nil || !emptyEqualsEmpty {
		t.Errorf("empty == empty = %v, %v; want true", emptyEqualsEmpty, err)
	}
	collectedEqualsEmpty, err := weak.EqualWeak(empty1)
	if err != nil || !collectedEqualsEmpty {
		t.Errorf("collected == empty = %v, %v; want true", collectedEqualsEmpty, err)
	}

	if !toLocalSome {
		t.Error("weak.to_local must be some while alive")
	}
	if !collectedEmpty || !collectedCloneEmpty {
		t.Error("weak and clone must be empty after collection")
	}
	if !collectedToLocalNone {
		t.Error("weak.to_local must be none after collection")
	}
	_ = weak.Close()
	_ = weakClone.Close()
}

// TestGlobalUseAfterClose pins the wrapper error behavior (the pinned crate
// panics in these spots; the Go module's documented deviation is errors).
func TestGlobalUseAfterClose(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	g, err := gov8.NewGlobal(scope, hndNewObject(t, ctx, scope))
	if err != nil {
		t.Fatalf("NewGlobal: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := g.Clone(); err == nil {
		t.Error("Clone after Close must fail")
	}
	if _, err := g.ToLocal(scope); err == nil {
		t.Error("ToLocal after Close must fail")
	}
	if _, err := g.IntoRaw(); err == nil {
		t.Error("IntoRaw after Close must fail")
	}
	if err := g.Close(); err == nil {
		t.Error("double Close must fail")
	}
	// The isolate stays usable.
	nested, err := iso.NewScope()
	if err != nil {
		t.Errorf("isolate unusable after global misuse: %v", err)
	} else if err := nested.Close(); err != nil {
		t.Errorf("close usability-probe scope: %v", err)
	}
}
