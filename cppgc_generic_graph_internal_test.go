//go:build windows && amd64

package gov8

import (
	"math"
	"sync/atomic"
	"testing"
)

func TestCppGCGenericGraphRegistryIDWrapSkipsZero(t *testing.T) {
	next := uint64(math.MaxUint64)
	id, err := nextCppGCGraphID(&next, func(candidate uint64) bool { return candidate == 1 })
	if err != nil {
		t.Fatal(err)
	}
	if id != 2 || next != 2 {
		t.Fatalf("wrapped id/next = %d/%d, want 2/2", id, next)
	}
}

func TestDiscardCppGCGenericGraphStateDropsOwnedCloneOnce(t *testing.T) {
	var drops atomic.Int32
	id, err := registerCppGCGraphState(99, 42,
		func(value int) (int, error) { return value, nil },
		func(int) { drops.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	discardCppGCGraphState(id)
	discardCppGCGraphState(id)
	if drops.Load() != 1 {
		t.Fatalf("owned clone drops = %d, want 1", drops.Load())
	}
}
