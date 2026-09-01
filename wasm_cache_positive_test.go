//go:build windows && amd64

package gov8

import (
	"bytes"
	"sync"
	"testing"
)

func TestSerializedWasmModuleCacheCopiesAndNil(t *testing.T) {
	var nilCache *SerializedWasmModuleCache
	if nilCache.Len() != 0 || nilCache.SerializedBytes() != nil {
		t.Fatal("nil serialized cache must be empty")
	}
	cache := &SerializedWasmModuleCache{serialized: []byte{1, 2, 3}, wire: []byte{4}}
	copyBytes := cache.SerializedBytes()
	copyBytes[0] = 9
	if got := cache.SerializedBytes(); !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("cache was mutated through returned bytes: %v", got)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if cache.Len() != 3 || len(cache.SerializedBytes()) != 3 {
					t.Error("concurrent immutable cache read changed size")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestSerializedWasmModuleCacheInvalidReceivers(t *testing.T) {
	if _, err := (*CompiledWasmModule)(nil).Serialize(); err == nil {
		t.Fatal("nil compiled module Serialize succeeded")
	}
	closed := &CompiledWasmModule{closed: true}
	if _, err := closed.Serialize(); err == nil {
		t.Fatal("closed compiled module Serialize succeeded")
	}
	if _, err := (*ModuleCachingInterface)(nil).SetCachedCompiledModule(nil); err == nil {
		t.Fatal("nil caching interface setter succeeded")
	}
	inactive := &ModuleCachingInterface{}
	if _, err := inactive.SetCachedCompiledModule(&SerializedWasmModuleCache{serialized: []byte{1}, wire: []byte{1}}); err == nil {
		t.Fatal("inactive caching interface setter succeeded")
	}
}
