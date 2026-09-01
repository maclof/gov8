//go:build windows && amd64

package gov8

import "testing"

func TestWasmPolicyRegistryReplacementAndRelease(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	allow := func(*CallbackScope, Value) bool { return true }
	if err := iso.SetAllowWasmCodeGenerationCallback(allow); err != nil {
		t.Fatal(err)
	}
	first, _ := findWasmPolicyEntry(iso, false)
	if first == 0 {
		t.Fatal("allow callback not registered")
	}
	if err := iso.SetAllowWasmCodeGenerationCallback(allow); err != nil {
		t.Fatal(err)
	}
	second, _ := findWasmPolicyEntry(iso, false)
	if second == 0 || second == first {
		t.Fatalf("allow replacement ids = %d, %d", first, second)
	}
	hostCallbackRegistry.mu.Lock()
	_, oldRetained := hostCallbackRegistry.entries[first]
	hostCallbackRegistry.mu.Unlock()
	if oldRetained {
		t.Fatal("replaced callback retained")
	}
	if err := iso.SetWasmAsyncResolvePromiseCallback(func(*WasmAsyncResolution) {}); err != nil {
		t.Fatal(err)
	}
	if id, _ := findWasmPolicyEntry(iso, true); id == 0 {
		t.Fatal("async callback not registered")
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if id, _ := findWasmPolicyEntry(iso, false); id != 0 {
		t.Fatal("allow registry entry survived release")
	}
	if id, _ := findWasmPolicyEntry(iso, true); id != 0 {
		t.Fatal("async registry entry survived release")
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
