//go:build windows && amd64

package gov8

import (
	"strings"
	"testing"
)

func TestPendingWasmResolutionBlocksHostStateRelease(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	id, err := nextWasmCallbackID()
	if err != nil {
		t.Fatal(err)
	}
	wasmStreamingRegistry.Lock()
	wasmStreamingRegistry.resolutions[id] = wasmResolutionEntry{
		iso: iso, callback: func(*WasmModuleCompilationResult) {},
	}
	wasmStreamingRegistry.Unlock()
	if err := ReleaseIsolateHostState(iso); err == nil || !strings.Contains(err.Error(), "pending wasm compilation") {
		t.Fatalf("release with pending resolution = %v", err)
	}
	wasmStreamingRegistry.Lock()
	delete(wasmStreamingRegistry.resolutions, id)
	wasmStreamingRegistry.Unlock()
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatalf("release after resolution cleanup: %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
