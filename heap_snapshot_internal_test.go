//go:build windows && amd64

package gov8

import "testing"

func TestHeapSnapshotRegistryDrains(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if err := iso.TakeHeapSnapshot(func([]byte) bool { return false }); err != nil {
		t.Fatal(err)
	}
	assertHeapSnapshotRegistryEmpty(t, "abort")
	if err := iso.TakeHeapSnapshot(func([]byte) bool { return true }); err != nil {
		t.Fatal(err)
	}
	assertHeapSnapshotRegistryEmpty(t, "complete")
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertHeapSnapshotRegistryEmpty(t *testing.T, phase string) {
	t.Helper()
	heapSnapshotRegistry.Lock()
	entries := len(heapSnapshotRegistry.entries)
	active := len(heapSnapshotRegistry.active)
	heapSnapshotRegistry.Unlock()
	if entries != 0 || active != 0 {
		t.Fatalf("registry after %s: entries=%d active=%d", phase, entries, active)
	}
}
