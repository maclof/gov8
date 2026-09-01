//go:build windows && amd64

package gov8

import (
	"math"
	"testing"
)

func TestCppGCGenericRegistryWrapAndIsolateDrain(t *testing.T) {
	cppgcGenericRegistry.Lock()
	baseline := len(cppgcGenericRegistry.entries)
	oldNext := cppgcGenericRegistry.next
	cppgcGenericRegistry.next = math.MaxUint64
	cppgcGenericRegistry.Unlock()

	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	object, err := iso.NewCppGCGenericObject(CppGCGenericOptions{Name: "registry", Alignment: 1})
	if err != nil {
		_ = iso.Close()
		t.Fatal(err)
	}
	if object.genericID == 0 {
		t.Fatal("registry wrapped to reserved zero")
	}
	cppgcGenericRegistry.Lock()
	if len(cppgcGenericRegistry.entries) != baseline+1 {
		t.Fatalf("registry entries after allocation = %d, baseline %d", len(cppgcGenericRegistry.entries), baseline)
	}
	cppgcGenericRegistry.Unlock()
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if err := object.Close(); err != nil {
		t.Fatalf("post-isolate Close: %v", err)
	}
	cppgcGenericRegistry.Lock()
	remaining := len(cppgcGenericRegistry.entries)
	cppgcGenericRegistry.next = oldNext
	cppgcGenericRegistry.Unlock()
	if remaining != baseline {
		t.Fatalf("registry entries after isolate close = %d, baseline %d", remaining, baseline)
	}
}
