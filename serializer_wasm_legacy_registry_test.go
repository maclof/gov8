//go:build windows && amd64

package gov8

import (
	"strings"
	"testing"
)

func TestSerializerDelegateCloseDrainsRegistry(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDelegateValueDeserializer(scope, ctx, []byte{0x54}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := d.delegateID
	if serDelLookup(id) == nil {
		t.Fatal("delegate missing from registry while live")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if serDelLookup(id) != nil {
		t.Fatal("delegate remained registered after Close")
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSerializerDelegateRegistryOverflowAndDrain(t *testing.T) {
	entry := &serDelEntry{}
	id, err := serDelRegister(entry)
	if err != nil || id == 0 {
		t.Fatalf("register = %d, %v", id, err)
	}
	if got := serDelLookup(id); got != entry {
		t.Fatalf("lookup = %p, want %p", got, entry)
	}
	serDelUnregister(id)
	if got := serDelLookup(id); got != nil {
		t.Fatalf("entry remained after unregister: %p", got)
	}

	serDelRegistry.mu.Lock()
	saved := serDelRegistry.next
	savedCount := len(serDelRegistry.entries)
	serDelRegistry.next = int64(^uint64(0) >> 1)
	serDelRegistry.mu.Unlock()
	defer func() {
		serDelRegistry.mu.Lock()
		serDelRegistry.next = saved
		serDelRegistry.mu.Unlock()
	}()
	_, err = serDelRegister(&serDelEntry{})
	if err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("max-id register = %v", err)
	}
	serDelRegistry.mu.Lock()
	if len(serDelRegistry.entries) != savedCount {
		t.Errorf("overflow register changed entry count: %d != %d", len(serDelRegistry.entries), savedCount)
	}
	serDelRegistry.next = -1
	serDelRegistry.mu.Unlock()
	_, err = serDelRegister(&serDelEntry{})
	if err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("corrupted-negative register = %v", err)
	}
}
