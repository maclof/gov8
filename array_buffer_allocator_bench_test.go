//go:build windows && amd64

package gov8_test

import (
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
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
	probe, err := iso.NewBackingStore(64)
	if err != nil {
		b.Fatal(err)
	}
	if err := probe.Close(); err != nil {
		b.Fatal(err)
	}
	if allocations.Load() != 1 || frees.Load() != 1 {
		b.Fatalf("probe callbacks allocations=%d frees=%d", allocations.Load(), frees.Load())
	}
	beforeAllocations := allocations.Load()
	beforeFrees := frees.Load()
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
	if allocations.Load()-beforeAllocations != uint64(b.N) || frees.Load()-beforeFrees != uint64(b.N) {
		b.Fatalf("callback deltas allocations=%d frees=%d iterations=%d",
			allocations.Load()-beforeAllocations, frees.Load()-beforeFrees, b.N)
	}
}

// BenchmarkArrayBufferAllocatorBackingStoreUnobserved preserves both
// native-to-Go allocator callbacks but removes the benchmark's atomic counter
// bodies. It is a diagnostic for callback-boundary cost, not a Rust comparison.
func BenchmarkArrayBufferAllocatorBackingStoreUnobserved(b *testing.B) {
	allocator, err := gov8.NewArrayBufferAllocator(gov8.ArrayBufferAllocatorCallbacks{})
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
}

// BenchmarkDefaultArrayBufferAllocatorBackingStore removes the two Go
// callbacks while retaining the public BackingStore wrapper, allocator
// reference cloning, and native create/dispose operations. It bounds the cost
// that can be improved without changing the safe callback contract.
func BenchmarkDefaultArrayBufferAllocatorBackingStore(b *testing.B) {
	allocator, err := gov8.NewDefaultArrayBufferAllocator()
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
}
