//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Internal field, External and isolate slot tests, mirroring the pinned Rust
// host oracle's external_data checks (rust-oracle/src/checks/host/
// external_data.rs) plus bounds-safety and release lifecycle cases. No raw
// addresses are asserted: only round-trip and identity booleans, per the
// oracle normalization rules.

// slotGuard mirrors the oracle's SlotGuard drop counter.
type slotGuard struct {
	id      int
	dropped *int
}

func (g slotGuard) ReleaseSlotValue() { *g.dropped++ }

func TestInternalFieldsAndExternals(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	countSet, err := ot.SetInternalFieldCount(2)
	if err != nil || !countSet {
		t.Fatalf("SetInternalFieldCount: ok=%v err=%v", countSet, err)
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	if count, _ := obj.InternalFieldCount(); count != 2 {
		t.Fatalf("field count = %d; want 2", count)
	}

	// Go-owned host data behind an integer token (never a raw Go pointer).
	token, err := iso.HostRefAdd(&map[string]int{"native": 1234})
	if err != nil {
		t.Fatalf("HostRefAdd: %v", err)
	}
	ext, err := scope.NewExternal(token)
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}
	if stored, err := obj.SetInternalField(0, ext); err != nil || !stored {
		t.Fatalf("SetInternalField(0): ok=%v err=%v", stored, err)
	}
	back, has, err := obj.GetInternalField(0)
	if err != nil || !has {
		t.Fatalf("GetInternalField(0): has=%v err=%v", has, err)
	}
	payload, err := back.ExternalValue()
	if err != nil {
		t.Fatalf("ExternalValue: %v", err)
	}
	if payload != token {
		t.Errorf("external roundtrip: payload=%x token=%x", payload, token)
	}
	if resolved, ok := iso.HostRefGet(payload); !ok || resolved == nil {
		t.Errorf("token must resolve to the registered host value")
	}

	// A JS integer can live in an internal field too.
	ninetyNine, err := scope.Int32(99)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	if stored, err := obj.SetInternalField(1, ninetyNine); err != nil || !stored {
		t.Fatalf("SetInternalField(1): ok=%v err=%v", stored, err)
	}
	intBack, has, err := obj.GetInternalField(1)
	if err != nil || !has {
		t.Fatalf("GetInternalField(1): has=%v err=%v", has, err)
	}
	if n, _ := intBack.IntegerValueRaw(); n != 99 {
		t.Errorf("integer field = %d; want 99", n)
	}

	// Out-of-bounds access is rejected by the wrapper before the engine.
	if stored, err := obj.SetInternalField(2, ext); err != nil || stored {
		t.Errorf("oob SetInternalField: ok=%v err=%v; want false", stored, err)
	}
	if _, has, err := obj.GetInternalField(2); err != nil || has {
		t.Errorf("oob GetInternalField: has=%v err=%v; want false", has, err)
	}

	// Aligned-pointer internal fields on a fresh instance, tag 7 (in range
	// 0..15; the engine aborts on out-of-range tags).
	obj2, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance obj2: ok=%v err=%v", ok, err)
	}
	if err := obj2.SetAlignedPointerInInternalField(0, token, 7); err != nil {
		t.Fatalf("SetAlignedPointerInInternalField: %v", err)
	}
	got, ok, err := obj2.GetAlignedPointerFromInternalField(0, 7)
	if err != nil || !ok {
		t.Fatalf("GetAlignedPointerFromInternalField: ok=%v err=%v", ok, err)
	}
	if got != token {
		t.Errorf("aligned roundtrip: got %x want %x", got, token)
	}

	// The host regains ownership and verifies the pointee "survived" (the
	// oracle reads through its Box; the Go analog reads the registry value).
	if v, ok := iso.HostRefGet(token); !ok || v == nil {
		t.Errorf("registered value must survive the roundtrips")
	}

	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Errorf("ReleaseIsolateHostState: %v", err)
	}
	if _, ok := iso.HostRefGet(token); ok {
		t.Errorf("token must stop resolving after release")
	}
}

func TestExternalPredicates(t *testing.T) {
	iso, _, scope := newTestRuntime(t)

	token, err := iso.HostRefAdd(42)
	if err != nil {
		t.Fatalf("HostRefAdd: %v", err)
	}
	ext, err := scope.NewExternal(token)
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}
	if is, err := ext.IsExternal(); err != nil || !is {
		t.Errorf("IsExternal = %v, %v; want true", is, err)
	}
	str, err := scope.NewString("nope")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if is, err := str.IsExternal(); err != nil || is {
		t.Errorf("string IsExternal = %v, %v; want false", is, err)
	}
	// Cross-isolate token resolution must fail.
	isoB, _, _ := newTestRuntime(t)
	if _, ok := isoB.HostRefGet(token); ok {
		t.Errorf("token from another isolate must not resolve")
	}
	// Unaligned / zero tokens are rejected.
	if _, ok := iso.HostRefGet(0); ok {
		t.Errorf("zero token must not resolve")
	}
	if _, ok := iso.HostRefGet(token + 1); ok {
		t.Errorf("unaligned token must not resolve")
	}
}

func TestIsolateSlotOwnership(t *testing.T) {
	// A dedicated isolate whose teardown mirrors the oracle's isolate drop.
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	dropped := 0

	firstSet := iso.SetSlot("guard", slotGuard{id: 1, dropped: &dropped})
	v, ok := iso.GetSlot("guard")
	readBackID := 0
	if g, isGuard := v.(slotGuard); ok && isGuard {
		readBackID = g.id
	}

	// Replacing releases the old value immediately (oracle: replace drops).
	replaced := iso.SetSlot("guard", slotGuard{id: 2, dropped: &dropped})
	dropsAfterReplace := dropped

	removed, ok := iso.RemoveSlot("guard")
	removedID := 0
	if g, isGuard := removed.(slotGuard); ok && isGuard {
		removedID = g.id
	}
	_, getAfterRemove := iso.GetSlot("guard")
	// Ownership handed back: the caller releases.
	if g, isGuard := removed.(slotGuard); ok && isGuard {
		g.ReleaseSlotValue()
	}
	dropsBeforeIsolateDrop := dropped

	// A value still stored when the isolate goes away is released with it.
	iso.SetSlot("guard", slotGuard{id: 3, dropped: &dropped})

	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Fatalf("ReleaseIsolateHostState: %v", err)
	}
	dropsAfterIsolateDrop := dropped

	if err := iso.Close(); err != nil {
		t.Fatalf("iso.Close: %v", err)
	}

	if !firstSet {
		t.Errorf("first set must report an empty slot")
	}
	if readBackID != 1 {
		t.Errorf("read back id = %d; want 1", readBackID)
	}
	if replaced {
		t.Errorf("replacing must report a non-empty slot")
	}
	if dropsAfterReplace != 1 {
		t.Errorf("drops after replace = %d; want 1", dropsAfterReplace)
	}
	if removedID != 2 {
		t.Errorf("removed id = %d; want 2", removedID)
	}
	if getAfterRemove {
		t.Errorf("get after remove must be empty")
	}
	if dropsBeforeIsolateDrop != 2 {
		t.Errorf("drops before isolate drop = %d; want 2", dropsBeforeIsolateDrop)
	}
	if dropsAfterIsolateDrop != 3 {
		t.Errorf("drops after isolate drop = %d; want 3", dropsAfterIsolateDrop)
	}
}

func TestReleaseIsolateHostStateIsIdempotent(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	if _, err := iso.NewFunction(scope, ctx, cbAdd, nil); err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := gov8.ReleaseIsolateHostState(iso); err != nil {
			t.Fatalf("ReleaseIsolateHostState #%d: %v", i+1, err)
		}
	}
	// The engine stays usable after release.
	if _, err := iso.NewFunction(scope, ctx, cbAdd, nil); err != nil {
		t.Errorf("NewFunction after release: %v", err)
	}
}

func TestSlotReplaceWithoutReleaser(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()

	if iso.SetSlot("plain", 1) != true {
		t.Errorf("first set must be empty")
	}
	// Values without slotReleaser are just replaced (Go GC owns them).
	if iso.SetSlot("plain", "two") != false {
		t.Errorf("second set must report replacement")
	}
	if v, ok := iso.GetSlot("plain"); !ok || v != "two" {
		t.Errorf("get after replace = %v, %v", v, ok)
	}
	if v, ok := iso.RemoveSlot("plain"); !ok || v != "two" {
		t.Errorf("remove = %v, %v", v, ok)
	}
	// Release on an isolate without host state must succeed.
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Errorf("ReleaseIsolateHostState: %v", err)
	}
}
