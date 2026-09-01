//go:build windows && amd64

package gov8

import (
	"sync/atomic"
	"syscall"
	"testing"
)

// BenchmarkArrayBufferAllocatorBackingStoreNativeFloor retains the exact host
// allocator and its two Go callbacks while keeping the short-lived store in
// native handles. It excludes the public BackingStore wrapper allocation and
// the extra allocator reference that permits a public store to outlive its
// isolate. The benchmark is diagnostic; public ownership semantics are not
// weakened.
func BenchmarkArrayBufferAllocatorBackingStoreNativeFloor(b *testing.B) {
	var allocations atomic.Uint64
	var frees atomic.Uint64
	allocator, err := NewArrayBufferAllocator(ArrayBufferAllocatorCallbacks{
		Allocate: func(int) bool {
			allocations.Add(1)
			return true
		},
		Free: func(int, byte) { frees.Add(1) },
	})
	if err != nil {
		b.Fatal(err)
	}
	params := NewCreateParams()
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		b.Fatal(err)
	}
	iso, err := NewIsolateWithParams(params)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = iso.Close()
		_ = allocator.Close()
	})
	ensureBackingStoreHotProcs()

	probe, _, _ := syscall.Syscall(backingStoreNewAddr, 2, iso.handle, 64, 0)
	if probe == 0 {
		b.Fatal(shimError("native-floor.NewBackingStore", 0))
	}
	status, _, _ := syscall.Syscall(backingStoreDisposeAddr, 1, probe, 0, 0)
	if int64(status) < 0 {
		b.Fatal(shimError("native-floor.BackingStore.Close", status))
	}
	if allocations.Load() != 1 || frees.Load() != 1 {
		b.Fatalf("probe callbacks allocations=%d frees=%d", allocations.Load(), frees.Load())
	}
	beforeAllocations := allocations.Load()
	beforeFrees := frees.Load()

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		handle, _, _ := syscall.Syscall(backingStoreNewAddr, 2, iso.handle, 64, 0)
		if handle == 0 {
			b.Fatal(shimError("native-floor.NewBackingStore", 0))
		}
		status, _, _ := syscall.Syscall(backingStoreDisposeAddr, 1, handle, 0, 0)
		if int64(status) < 0 {
			b.Fatal(shimError("native-floor.BackingStore.Close", status))
		}
	}
	b.StopTimer()
	if allocations.Load()-beforeAllocations != uint64(b.N) || frees.Load()-beforeFrees != uint64(b.N) {
		b.Fatalf("callback deltas allocations=%d frees=%d iterations=%d",
			allocations.Load()-beforeAllocations, frees.Load()-beforeFrees, b.N)
	}
}

// BenchmarkDefaultArrayBufferAllocatorBackingStoreNativeFloor removes both
// Go callbacks and public wrapper/reference bookkeeping. It measures the two
// required native create/dispose crossings and V8's allocation work.
func BenchmarkDefaultArrayBufferAllocatorBackingStoreNativeFloor(b *testing.B) {
	allocator, err := NewDefaultArrayBufferAllocator()
	if err != nil {
		b.Fatal(err)
	}
	params := NewCreateParams()
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		b.Fatal(err)
	}
	iso, err := NewIsolateWithParams(params)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = iso.Close()
		_ = allocator.Close()
	})
	ensureBackingStoreHotProcs()

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		handle, _, _ := syscall.Syscall(backingStoreNewAddr, 2, iso.handle, 64, 0)
		if handle == 0 {
			b.Fatal(shimError("default-native-floor.NewBackingStore", 0))
		}
		status, _, _ := syscall.Syscall(backingStoreDisposeAddr, 1, handle, 0, 0)
		if int64(status) < 0 {
			b.Fatal(shimError("default-native-floor.BackingStore.Close", status))
		}
	}
}
