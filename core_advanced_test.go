//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Unit tests for the core-advanced surface. Each test mirrors the
// mechanics of one oracle characterization check; the byte-exact
// normalized forms of the same observations live in the
// conformance-core-advanced runner.

func newIso(t *testing.T) *gov8.Isolate {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	return iso
}

func newCtx(t *testing.T, iso *gov8.Isolate) *gov8.Context {
	t.Helper()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return ctx
}

func newScope(t *testing.T, iso *gov8.Isolate) *gov8.Scope {
	t.Helper()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	return scope
}

func caNumber(t *testing.T, s *gov8.Scope, f float64) gov8.Value {
	t.Helper()
	v, err := s.Number(f)
	if err != nil {
		t.Fatalf("Number(%v): %v", f, err)
	}
	return v
}

func caString(t *testing.T, s *gov8.Scope, str string) gov8.Value {
	t.Helper()
	v, err := s.NewString(str)
	if err != nil {
		t.Fatalf("NewString(%q): %v", str, err)
	}
	return v
}

func caInt32(t *testing.T, s *gov8.Scope, v int32) gov8.Value {
	t.Helper()
	got, err := s.Int32(v)
	if err != nil {
		t.Fatalf("Int32(%v): %v", v, err)
	}
	return got
}

func evalInt(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, source string) (int64, bool) {
	t.Helper()
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		return 0, false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(scope, nil)
	if rerr != nil {
		return 0, false
	}
	n, _, nerr := v.IntegerValue(ctx)
	if nerr != nil {
		return 0, false
	}
	return n, true
}

// --- scopes ------------------------------------------------------------------

// TestEscapableScopeNestedAndEscaped mirrors
// core-advanced/scope/nested_and_escaped_scopes: a two-level escape chain,
// an intervening plain scope, and the outer value untouched.
func TestEscapableScopeNestedAndEscaped(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	outer := caNumber(t, scope, 7)

	// Level A: escape a number out of an escapable scope.
	escA, err := scope.NewEscapableScope()
	if err != nil {
		t.Fatalf("NewEscapableScope A: %v", err)
	}
	var escapedNumber gov8.Value
	func() {
		// A plain scope nested inside the escapable scope; its handles
		// die with it, but Escape copies the value into the escape slot
		// first (engine-equivalent of creating the value in the escapable
		// scope itself).
		inner := newScope(t, iso)
		defer func() { _ = inner.Close() }()
		escapedNumber, err = escA.Escape(caNumber(t, inner, 8))
		if err != nil {
			t.Fatalf("Escape(number): %v", err)
		}
	}()
	if err := escA.Close(); err != nil {
		t.Fatalf("Close A: %v", err)
	}

	// Two-level chain: B2 escapes a string into B1, B1 re-escapes it out.
	escB1, err := scope.NewEscapableScope()
	if err != nil {
		t.Fatalf("NewEscapableScope B1: %v", err)
	}
	var escapedString gov8.Value
	func() {
		escB2, err := scope.NewEscapableScope()
		if err != nil {
			t.Fatalf("NewEscapableScope B2: %v", err)
		}
		var deep gov8.Value
		func() {
			inner := newScope(t, iso)
			defer func() { _ = inner.Close() }()
			deep, err = escB2.Escape(caString(t, inner, "deep"))
			if err != nil {
				t.Fatalf("Escape(deep): %v", err)
			}
		}()
		if err := escB2.Close(); err != nil {
			t.Fatalf("Close B2: %v", err)
		}
		escapedString, err = escB1.Escape(deep)
		if err != nil {
			t.Fatalf("Escape(deep) via B1: %v", err)
		}
	}()
	if err := escB1.Close(); err != nil {
		t.Fatalf("Close B1: %v", err)
	}

	// An intervening plain scope must not disturb the escaped values.
	innerOK := false
	func() {
		inner := newScope(t, iso)
		defer func() { _ = inner.Close() }()
		probe := caNumber(t, inner, 1.5)
		v, err := probe.NumberValueRaw()
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		innerOK = v == 1.5
	}()

	if !innerOK {
		t.Error("inner scope probe failed")
	}
	if is, _ := escapedNumber.IsNumber(); !is {
		t.Error("escaped number is not a number")
	}
	if v, _ := escapedNumber.NumberValueRaw(); v != 8 {
		t.Errorf("escaped number value = %v, want 8", v)
	}
	if is, _ := escapedString.IsString(); !is {
		t.Error("escaped string is not a string")
	}
	if txt, err := escapedString.StringValue(); err != nil || txt != "deep" {
		t.Errorf("escaped string text = %q (%v), want deep", txt, err)
	}
	if v, _ := outer.NumberValueRaw(); v != 7 {
		t.Errorf("outer value changed: %v", v)
	}
}

// TestEscapableScopeEscapeTwice mirrors
// core-advanced/scope/escapable_escape_twice_panics: the second escape
// fails with the pinned message and the first value stays usable.
func TestEscapableScopeEscapeTwice(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	esc, err := scope.NewEscapableScope()
	if err != nil {
		t.Fatalf("NewEscapableScope: %v", err)
	}
	func() {
		inner := newScope(t, iso)
		defer func() { _ = inner.Close() }()
		first, err := esc.Escape(caNumber(t, inner, 1))
		if err != nil {
			t.Fatalf("first escape: %v", err)
		}
		_, secondErr := esc.Escape(caNumber(t, inner, 2))
		if secondErr == nil {
			t.Fatal("second escape succeeded")
		}
		if secondErr.Error() != "EscapableHandleScope::escape() called twice" {
			t.Fatalf("second escape message = %q", secondErr.Error())
		}
		if v, _ := first.NumberValueRaw(); v != 1 {
			t.Fatalf("first escaped value changed: %v", v)
		}
	}()
	if err := esc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// --- context ------------------------------------------------------------------

// TestContextEnterExitNesting mirrors
// core-advanced/context/enter_exit_nesting.
func TestContextEnterExitNesting(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx1 := newCtx(t, iso)
	defer func() { _ = ctx1.Close() }()
	ctx2 := newCtx(t, iso)
	defer func() { _ = ctx2.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	outer, err := ctx1.Enter()
	if err != nil {
		t.Fatalf("Enter ctx1: %v", err)
	}
	cur, err := iso.CurrentContext(scope)
	if err != nil {
		t.Fatalf("CurrentContext: %v", err)
	}
	outerCurrentIsCtx1, err := cur.SameAs(ctx1)
	if err != nil {
		t.Fatalf("SameAs: %v", err)
	}
	entered, err := iso.EnteredOrMicrotaskContext(scope)
	if err != nil {
		t.Fatalf("EnteredOrMicrotaskContext: %v", err)
	}
	outerEnteredIsCtx1, err := entered.SameAs(ctx1)
	if err != nil {
		t.Fatalf("SameAs: %v", err)
	}

	g1a, err := ctx1.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	g1b, err := ctx1.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	g2, err := ctx2.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	globalIdentity, err := g1a.Value.SameValue(g1b.Value)
	if err != nil {
		t.Fatalf("SameValue: %v", err)
	}
	sameG2, err := g1a.Value.SameValue(g2.Value)
	if err != nil {
		t.Fatalf("SameValue: %v", err)
	}
	globalsDistinct := !sameG2
	if err != nil {
		t.Fatalf("SameValue: %v", err)
	}
	_ = g1a
	_ = g1b
	_ = g2

	innerCurrentIsCtx2 := false
	innerEnteredIsCtx2 := false
	innerCurrentNotCtx1 := false
	func() {
		inner, err := ctx2.Enter()
		if err != nil {
			t.Fatalf("Enter ctx2: %v", err)
		}
		defer func() { _ = inner.Close() }()
		cur2, err := iso.CurrentContext(scope)
		if err != nil {
			t.Fatalf("CurrentContext: %v", err)
		}
		if innerCurrentIsCtx2, err = cur2.SameAs(ctx2); err != nil {
			t.Fatalf("SameAs: %v", err)
		}
		entered2, err := iso.EnteredOrMicrotaskContext(scope)
		if err != nil {
			t.Fatalf("EnteredOrMicrotaskContext: %v", err)
		}
		if innerEnteredIsCtx2, err = entered2.SameAs(ctx2); err != nil {
			t.Fatalf("SameAs: %v", err)
		}
		not1, err := cur2.SameAs(ctx1)
		if err != nil {
			t.Fatalf("SameAs: %v", err)
		}
		innerCurrentNotCtx1 = !not1
	}()

	curAfter, err := iso.CurrentContext(scope)
	if err != nil {
		t.Fatalf("CurrentContext: %v", err)
	}
	restoredIsCtx1, err := curAfter.SameAs(ctx1)
	if err != nil {
		t.Fatalf("SameAs: %v", err)
	}
	if err := outer.Close(); err != nil {
		t.Fatalf("Close ctx1 scope: %v", err)
	}

	for name, got := range map[string]bool{
		"outer_current_is_ctx1":  outerCurrentIsCtx1,
		"outer_entered_is_ctx1":  outerEnteredIsCtx1,
		"global_identity_stable": globalIdentity,
		"globals_distinct":       globalsDistinct,
		"inner_current_is_ctx2":  innerCurrentIsCtx2,
		"inner_entered_is_ctx2":  innerEnteredIsCtx2,
		"inner_current_not_ctx1": innerCurrentNotCtx1,
		"restored_is_ctx1":       restoredIsCtx1,
	} {
		if !got {
			t.Errorf("%s = false", name)
		}
	}
}

// TestContextSecurityTokens mirrors core-advanced/context/security_tokens.
func TestContextSecurityTokens(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctxA := newCtx(t, iso)
	defer func() { _ = ctxA.Close() }()
	ctxB := newCtx(t, iso)
	defer func() { _ = ctxB.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	same := func(a, b gov8.Value) bool {
		t.Helper()
		eq, err := a.SameValue(b)
		if err != nil {
			t.Fatalf("SameValue: %v", err)
		}
		return eq
	}

	// Distinct default tokens.
	sa, err := ctxA.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	ta, err := ctxA.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	sb, err := ctxB.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	tb, err := ctxB.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	defaultEqual := same(ta, tb)
	if err := sb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sa.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A custom token diverges from B's default.
	tokenA := caString(t, scope, "shield-a")
	if err := ctxA.SetSecurityToken(scope, tokenA); err != nil {
		t.Fatalf("SetSecurityToken: %v", err)
	}
	sa, err = ctxA.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	ta, err = ctxA.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	sb, err = ctxB.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	tb, err = ctxB.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	diverges := !same(ta, tb)
	if err := sb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sa.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Equal-content distinct string tokens compare equal under SameValue.
	tokenACopy := caString(t, scope, "shield-a")
	if err := ctxB.SetSecurityToken(scope, tokenACopy); err != nil {
		t.Fatalf("SetSecurityToken: %v", err)
	}
	sa, err = ctxA.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	ta, err = ctxA.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	sb, err = ctxB.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	tb, err = ctxB.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	equalContent := same(ta, tb)
	if err := sb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sa.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reset to the default diverges again.
	if err := ctxB.UseDefaultSecurityToken(); err != nil {
		t.Fatalf("UseDefaultSecurityToken: %v", err)
	}
	sa, err = ctxA.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	ta, err = ctxA.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	sb, err = ctxB.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	tb, err = ctxB.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	resetDiverges := !same(ta, tb)
	if err := sb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sa.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The exact same string object makes them equal again.
	if err := ctxB.SetSecurityToken(scope, tokenA); err != nil {
		t.Fatalf("SetSecurityToken: %v", err)
	}
	sa, err = ctxA.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	ta, err = ctxA.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	sb, err = ctxB.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	tb, err = ctxB.GetSecurityToken(scope)
	if err != nil {
		t.Fatalf("GetSecurityToken: %v", err)
	}
	sameObjectEqual := same(ta, tb)
	if err := sb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sa.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if defaultEqual {
		t.Error("default tokens compared equal")
	}
	if !diverges {
		t.Error("custom token did not diverge from default")
	}
	if !equalContent {
		t.Error("equal-content tokens did not compare equal")
	}
	if !resetDiverges {
		t.Error("reset token did not diverge")
	}
	if !sameObjectEqual {
		t.Error("same-object tokens did not compare equal")
	}
}

// TestContextEmbedderDataAndSlots mirrors
// core-advanced/context/embedder_data_and_slots.
func TestContextEmbedderDataAndSlots(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	predicates := func(v gov8.Value, present bool) map[string]bool {
		t.Helper()
		if !present {
			t.Fatal("embedder data absent")
		}
		out := make(map[string]bool, 6)
		for name, f := range map[string]func() (bool, error){
			"null":      v.IsNull,
			"undefined": v.IsUndefined,
			"int32":     v.IsInt32,
			"string":    v.IsString,
			"number":    v.IsNumber,
			"object":    v.IsObject,
		} {
			b, err := f()
			if err != nil {
				t.Fatalf("predicate %s: %v", name, err)
			}
			out[name] = b
		}
		return out
	}

	// The default slot content is an engine-internal oddball: no
	// predicate of the pinned set is true (ToString on it is not
	// meaningful, mirroring the oracle's comment).
	before := predicates(mustGetEmbedder(t, ctx, scope, 0), true)
	for name, b := range before {
		if b {
			t.Errorf("default embedder predicate %s = true", name)
		}
	}

	if err := ctx.SetEmbedderData(scope, 0, caInt32(t, scope, 11)); err != nil {
		t.Fatalf("SetEmbedderData: %v", err)
	}
	if v := mustEmbedderInt(t, ctx, scope, 0); v != 11 {
		t.Errorf("read0 = %d", v)
	}
	if err := ctx.SetEmbedderData(scope, 1, caInt32(t, scope, 12)); err != nil {
		t.Fatalf("SetEmbedderData: %v", err)
	}
	if v := mustEmbedderInt(t, ctx, scope, 1); v != 12 {
		t.Errorf("read1 = %d", v)
	}
	if v := mustEmbedderInt(t, ctx, scope, 0); v != 11 {
		t.Errorf("read0 after slot1 = %d", v)
	}
	if err := ctx.SetEmbedderData(scope, 0, caInt32(t, scope, 13)); err != nil {
		t.Fatalf("SetEmbedderData: %v", err)
	}
	if v := mustEmbedderInt(t, ctx, scope, 0); v != 13 {
		t.Errorf("read0 overwritten = %d", v)
	}

	// Aligned pointer round-trip in slot 2.
	sentinel := uintptr(0xABCD000)
	if err := ctx.SetAlignedPointerInEmbedderData(2, sentinel); err != nil {
		t.Fatalf("SetAlignedPointer: %v", err)
	}
	got, err := ctx.GetAlignedPointerFromEmbedderData(2)
	if err != nil {
		t.Fatalf("GetAlignedPointer: %v", err)
	}
	if got != sentinel {
		t.Errorf("aligned pointer = %x, want %x", got, sentinel)
	}

	// Rc-style slots (host-side, explicitly keyed).
	if _, first := ctx.SetSlot("u32", uint32(7)); !first {
		t.Error("first slot set was not empty")
	}
	if v, ok := ctx.GetSlot("u32"); !ok || v.(uint32) != 7 {
		t.Errorf("slot read = %v, %v", v, ok)
	}
	if prev, ok := ctx.SetSlot("u32", uint32(8)); ok || prev.(uint32) != 7 {
		t.Errorf("second slot set = %v, %v", prev, ok)
	}
	if v, ok := ctx.GetSlot("u32"); !ok || v.(uint32) != 8 {
		t.Errorf("slot read = %v, %v", v, ok)
	}
	if removed, ok := ctx.RemoveSlot("u32"); !ok || removed.(uint32) != 8 {
		t.Errorf("remove = %v, %v", removed, ok)
	}
	if _, ok := ctx.RemoveSlot("u32"); ok {
		t.Error("second remove returned a value")
	}
	if _, first := ctx.SetSlot("u64", uint64(99)); !first {
		t.Error("other-type slot set was not empty")
	}
	if v, ok := ctx.GetSlot("u64"); !ok || v.(uint64) != 99 {
		t.Errorf("other-type read = %v, %v", v, ok)
	}
	if _, gone := ctx.GetSlot("u32"); gone {
		t.Error("u32 slot survived removal")
	}

	ctx.ClearAllSlots()
	if _, ok := ctx.GetSlot("u64"); ok {
		t.Error("u64 survived clear")
	}
	if _, ok := ctx.GetSlot("u32"); ok {
		t.Error("u32 survived clear")
	}
	if v := mustEmbedderInt(t, ctx, scope, 0); v != 13 {
		t.Errorf("embedder data did not survive clear: %d", v)
	}
	if _, first := ctx.SetSlot("u32", uint32(5)); !first {
		t.Error("slot set after clear was not empty")
	}
	if v, ok := ctx.GetSlot("u32"); !ok || v.(uint32) != 5 {
		t.Errorf("slot re-init read = %v, %v", v, ok)
	}
}

func mustGetEmbedder(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, slot int) gov8.Value {
	t.Helper()
	v, ok, err := ctx.GetEmbedderData(scope, slot)
	if err != nil {
		t.Fatalf("GetEmbedderData(%d): %v", slot, err)
	}
	if !ok {
		t.Fatalf("GetEmbedderData(%d): absent", slot)
	}
	return v
}

func mustEmbedderInt(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, slot int) int64 {
	t.Helper()
	v, _, err := mustGetEmbedder(t, ctx, scope, slot).IntegerValue(ctx)
	if err != nil {
		t.Fatalf("embedder int %d: %v", slot, err)
	}
	return v
}

// --- slots ---------------------------------------------------------------------

// TestIsolateRawDataSlots mirrors core-advanced/slots/isolate_raw_data_slots
// and the Go-side bound checks the upstream lacks.
func TestIsolateRawDataSlots(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()

	count, err := iso.DataSlotCount()
	if err != nil {
		t.Fatalf("DataSlotCount: %v", err)
	}
	if count != 3 {
		t.Errorf("slot count = %d, want 3", count)
	}

	v0, err := iso.GetData(0)
	if err != nil {
		t.Fatalf("GetData(0): %v", err)
	}
	if v0 != 0 {
		t.Errorf("initial slot 0 = %x, want null", v0)
	}

	sentinelA := uintptr(0x1111)
	sentinelB := uintptr(0x2222)
	if err := iso.SetData(0, sentinelA); err != nil {
		t.Fatalf("SetData(0): %v", err)
	}
	if got, _ := iso.GetData(0); got != sentinelA {
		t.Errorf("roundtrip a = %x", got)
	}
	if err := iso.SetData(1, sentinelB); err != nil {
		t.Fatalf("SetData(1): %v", err)
	}
	if got, _ := iso.GetData(1); got != sentinelB {
		t.Errorf("roundtrip b = %x", got)
	}
	if got, _ := iso.GetData(0); got != sentinelA {
		t.Errorf("slot 0 affected: %x", got)
	}
	if err := iso.SetData(0, 0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, _ := iso.GetData(0); got != 0 {
		t.Errorf("cleared slot 0 = %x", got)
	}
	if got, _ := iso.GetData(1); got != sentinelB {
		t.Errorf("slot 1 did not survive: %x", got)
	}

	// Upstream the slot index is unchecked; the Go wrapper rejects OOB.
	if err := iso.SetData(count, 1); err == nil {
		t.Error("SetData accepted an out-of-range slot")
	}
	if _, err := iso.GetData(-1); err == nil {
		t.Error("GetData accepted a negative slot")
	}
	if _, err := iso.GetData(count); err == nil {
		t.Error("GetData accepted an out-of-range slot")
	}
}

// TestIsolateTypedSlots mirrors core-advanced/slots/isolate_multiple_types
// (crate type-keyed slots map to explicit Go keys, matching the existing
// isolate-slot surface) plus the context-slot clear semantics.
func TestIsolateTypedSlots(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()

	type alpha struct{ value uint32 }
	type beta struct{ label string }

	if first := iso.SetSlot("alpha", alpha{value: 1}); !first {
		t.Error("alpha first set not empty")
	}
	if first := iso.SetSlot("beta", beta{label: "beta"}); !first {
		t.Error("beta first set not empty")
	}
	if a, ok := iso.GetSlot("alpha"); !ok || a.(alpha).value != 1 {
		t.Errorf("alpha read = %v", a)
	}
	if b, ok := iso.GetSlot("beta"); !ok || b.(beta).label != "beta" {
		t.Errorf("beta read = %v", b)
	}
	if removed, ok := iso.RemoveSlot("alpha"); !ok || removed.(alpha).value != 1 {
		t.Errorf("alpha remove = %v", removed)
	}
	if _, ok := iso.RemoveSlot("alpha"); ok {
		t.Error("alpha second remove returned a value")
	}
	if b, ok := iso.GetSlot("beta"); !ok || b.(beta).label != "beta" {
		t.Error("beta did not survive alpha removal")
	}
	if first := iso.SetSlot("alpha", alpha{value: 2}); !first {
		t.Error("alpha re-set not empty")
	}
	if a, ok := iso.GetSlot("alpha"); !ok || a.(alpha).value != 2 {
		t.Errorf("alpha re-read = %v", a)
	}
}
