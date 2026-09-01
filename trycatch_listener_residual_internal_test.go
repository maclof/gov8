//go:build windows && amd64

package gov8

import "testing"

func TestResidualListenerRegistryDrainsWithIsolateHostState(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := iso.AddMessageListener(func(*CallbackMessage, Value) {}); err != nil || !ok {
		t.Fatalf("AddMessageListener = %v, %v", ok, err)
	}
	key := chKey{kind: chKindMessageListener, engine: iso.handle}
	chRegistry.mu.Lock()
	before := chRegistry.entries[key]
	chRegistry.mu.Unlock()
	if before == nil {
		t.Fatal("listener registry entry missing")
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	chRegistry.mu.Lock()
	after := chRegistry.entries[key]
	chRegistry.mu.Unlock()
	if after != nil {
		t.Fatal("listener registry entry retained after release")
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMessageListenerRegistrationReplacesStaleIsolateIdentity(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	key := chKey{kind: chKindMessageListener, engine: iso.handle}
	stale := &Isolate{handle: iso.handle, tid: iso.tid}
	chRegistry.mu.Lock()
	chRegistry.entries[key] = &chEntry{
		iso:            stale,
		listeners:      []MessageListenerCallback{func(*CallbackMessage, Value) {}},
		listenerLevels: []uint32{MsgError},
	}
	chRegistry.mu.Unlock()
	if ok, err := iso.AddMessageListener(func(*CallbackMessage, Value) {}); err != nil || !ok {
		t.Fatalf("AddMessageListener = %v, %v", ok, err)
	}
	chRegistry.mu.Lock()
	entry := chRegistry.entries[key]
	chRegistry.mu.Unlock()
	if entry == nil || entry.iso != iso || len(entry.listeners) != 1 {
		t.Fatal("stale listener registration was not replaced")
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
