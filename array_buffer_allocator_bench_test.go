//go:build windows && amd64

package gov8_test

import (
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

func BenchmarkArrayBufferAllocatorBackingStore(b *testing.B) {
	var allocations atomic.Uint64
	var frees atomic.Uint64
	allocator, err := gov8.NewArrayBufferAllocator(gov8.ArrayBufferAllocatorCallbacks{
		Allocate: func(int) bool {
			allocations.Add(1)
			return true
		},
		Free: func(int, byte) { frees.Add(1) },
	})
	if err != nil {
		b.Fatal(err)
	}
	params := gov8.NewCreateParams()
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		b.Fatal(err)
	}
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = iso.Close()
		_ = allocator.Close()
	})
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		store, err := iso.NewBackingStore(64)
		if err != nil {
			b.Fatal(err)
		}
		if err := store.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if allocations.Load() != uint64(b.N) || frees.Load() != uint64(b.N) {
		b.Fatalf("callbacks allocations=%d frees=%d iterations=%d", allocations.Load(), frees.Load(), b.N)
	}
}
