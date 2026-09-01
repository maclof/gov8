//go:build windows && amd64

package gov8

import (
	"math"
	"strings"
	"testing"
)

func TestModuleSourceResolverRegistryOverflowDoesNotInsert(t *testing.T) {
	moduleAdvancedRegistry.Lock()
	originalNext := moduleAdvancedRegistry.next
	originalCount := len(moduleAdvancedRegistry.entries)
	moduleAdvancedRegistry.next = math.MaxUint64
	moduleAdvancedRegistry.Unlock()
	t.Cleanup(func() {
		moduleAdvancedRegistry.Lock()
		moduleAdvancedRegistry.next = originalNext
		moduleAdvancedRegistry.Unlock()
	})

	_, err := registerModuleSourceResolver(nil, func(ModuleSourceResolveRequest) (Value, error) { return Value{}, nil })
	if err == nil || !strings.Contains(err.Error(), "registry exhausted") {
		t.Fatalf("overflow error = %v", err)
	}
	moduleAdvancedRegistry.Lock()
	count := len(moduleAdvancedRegistry.entries)
	moduleAdvancedRegistry.Unlock()
	if count != originalCount {
		t.Fatalf("overflow changed registry size: got %d, want %d", count, originalCount)
	}
}
