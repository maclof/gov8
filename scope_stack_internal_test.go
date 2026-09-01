//go:build windows && amd64

package gov8

import "testing"

func TestHandleScopeStackLIFOReuseAndDrain(t *testing.T) {
	iso := &Isolate{}
	outer := &Scope{iso: iso}
	inner := &EscapableScope{iso: iso, outer: outer}

	pushHandleScope(iso, outer)
	pushHandleScope(iso, inner)
	if !currentHandleScope(iso, inner) || currentHandleScopeToken(iso) != inner {
		t.Fatal("inner scope is not current")
	}
	capacity := cap(iso.handleScopeStack)
	if err := popHandleScope(iso, outer); err == nil {
		t.Fatal("out-of-order pop succeeded")
	}
	if len(iso.handleScopeStack) != 2 || currentHandleScopeToken(iso) != inner {
		t.Fatal("out-of-order pop changed the stack")
	}

	if err := popHandleScope(iso, inner); err != nil {
		t.Fatalf("pop inner: %v", err)
	}
	if slot := iso.handleScopeStack[:capacity][1]; slot != nil {
		t.Fatalf("popped slot retained %T", slot)
	}
	if err := popHandleScope(iso, outer); err != nil {
		t.Fatalf("pop outer: %v", err)
	}
	if len(iso.handleScopeStack) != 0 || cap(iso.handleScopeStack) != capacity {
		t.Fatalf("empty stack = len %d cap %d, want len 0 cap %d",
			len(iso.handleScopeStack), cap(iso.handleScopeStack), capacity)
	}
	for index, slot := range iso.handleScopeStack[:capacity] {
		if slot != nil {
			t.Fatalf("empty stack slot %d retained %T", index, slot)
		}
	}

	pushHandleScope(iso, outer)
	if cap(iso.handleScopeStack) != capacity {
		t.Fatalf("reused stack cap = %d, want %d", cap(iso.handleScopeStack), capacity)
	}
	iso.clearHandleScopeStack()
	if iso.handleScopeStack != nil {
		t.Fatalf("drained stack = %#v, want nil", iso.handleScopeStack)
	}
}

func TestHandleScopeStackSteadyStateAllocations(t *testing.T) {
	iso := &Isolate{}
	scope := &Scope{iso: iso}
	pushHandleScope(iso, scope)
	if err := popHandleScope(iso, scope); err != nil {
		t.Fatal(err)
	}

	allocations := testing.AllocsPerRun(1000, func() {
		pushHandleScope(iso, scope)
		if !currentHandleScope(iso, scope) {
			panic("scope is not current")
		}
		if err := popHandleScope(iso, scope); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("steady-state stack allocations = %v, want 0", allocations)
	}
}

func TestIsolateCloseDrainsRetainedHandleScopeCapacity(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = iso.Close()
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		_ = iso.Close()
		t.Fatal(err)
	}
	if len(iso.handleScopeStack) != 0 || cap(iso.handleScopeStack) == 0 {
		_ = iso.Close()
		t.Fatalf("closed scope stack = len %d cap %d, want retained capacity",
			len(iso.handleScopeStack), cap(iso.handleScopeStack))
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if iso.handleScopeStack != nil {
		t.Fatalf("isolate teardown retained stack capacity %d", cap(iso.handleScopeStack))
	}
}

func BenchmarkHandleScopeStackOwnerThread(b *testing.B) {
	iso := &Isolate{}
	scope := &Scope{iso: iso}
	pushHandleScope(iso, scope)
	if err := popHandleScope(iso, scope); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		pushHandleScope(iso, scope)
		if !currentHandleScope(iso, scope) {
			b.Fatal("scope is not current")
		}
		if err := popHandleScope(iso, scope); err != nil {
			b.Fatal(err)
		}
	}
}
