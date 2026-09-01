//go:build windows && amd64

package gov8_test

import (
	"strings"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func snapshotResourceMarker(t testing.TB, iso *gov8.Isolate) int64 {
	t.Helper()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	defer ctx.Close()
	defer scope.Close()
	script, err := ctx.Compile(scope, "snapshotMarker", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	value, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	marker, ok, err := value.IntegerValue(ctx)
	if err != nil || !ok {
		t.Fatalf("marker: %d, %v, %v", marker, ok, err)
	}
	return marker
}

func TestSnapshotParamsCustomAllocatorBackingStoreLifetime(t *testing.T) {
	blob := cpsSnapshotBlob(t, 41)
	defer blob.Release()
	events := &allocatorTestEvents{}
	allocator, err := gov8.NewArrayBufferAllocator(events.callbacks())
	if err != nil {
		t.Fatal(err)
	}
	params, err := gov8.NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		t.Fatal(err)
	}
	iso, err := gov8.NewIsolateWithSnapshotParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if marker := snapshotResourceMarker(t, iso); marker != 41 {
		t.Fatalf("marker = %d", marker)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	buffer, err := gov8.NewArrayBuffer(scope, ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	store, err := buffer.GetBackingStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteAt([]byte{73}, 0); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if events.drops.Load() != 0 {
		t.Fatalf("allocator dropped with live store: %d", events.drops.Load())
	}
	read := []byte{0}
	if _, err := store.ReadAt(read, 0); err != nil || read[0] != 73 {
		t.Fatalf("post-isolate read = %v, %v", read, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	if len(events.initialized) != 1 || events.initialized[0] != 9 || len(events.freed) != 1 || events.freed[0] != 9 || events.drops.Load() != 1 {
		t.Fatalf("allocator events = %+v drops=%d", events, events.drops.Load())
	}
}

func TestSnapshotParamsCustomCppGCHeapOwnership(t *testing.T) {
	blob := cpsSnapshotBlob(t, 42)
	defer blob.Release()
	heap, err := gov8.NewCppGCHeap(gov8.CppGCHeapCreateParams{MarkingSupport: gov8.CppGCMarkingAtomic, SweepingSupport: gov8.CppGCSweepingAtomic})
	if err != nil {
		t.Fatal(err)
	}
	params, err := gov8.NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := params.SetCppGCHeap(heap); err != nil {
		t.Fatal(err)
	}
	iso, err := gov8.NewIsolateWithSnapshotParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := heap.AttachedTo(iso); err != nil || !same {
		t.Fatalf("AttachedTo = %v, %v", same, err)
	}
	if marker := snapshotResourceMarker(t, iso); marker != 42 {
		t.Fatalf("marker = %d", marker)
	}
	var drops atomic.Int32
	first, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "snapshot-first", Alignment: 1, Callbacks: gov8.CppGCGenericCallbacks{Destroy: func() { drops.Add(1) }}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if drops.Load() != 0 {
		t.Fatalf("root release destroyed synchronously = %d", drops.Load())
	}
	second, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "snapshot-second", Alignment: 1, Callbacks: gov8.CppGCGenericCallbacks{Destroy: func() { drops.Add(1) }}})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	_, shareErr := iso.TryIntoShared()
	intoErr, ok := shareErr.(*gov8.IntoSharedError)
	if !ok || intoErr.Kind != gov8.KindEmbedderCppHeap || intoErr.IntoIsolate() != iso {
		t.Fatalf("TryIntoShared = %#v", shareErr)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if drops.Load() != 2 {
		t.Fatalf("drops after isolate close = %d", drops.Load())
	}
	if err := heap.Close(); err != nil {
		t.Fatalf("transferred heap Close: %v", err)
	}
}

func TestSnapshotParamsResourceValidationAndWrongThread(t *testing.T) {
	t.Run("released snapshot preserves resources", func(t *testing.T) {
		blob := cpsSnapshotBlob(t, 1)
		params, err := gov8.NewSnapshotCreateParams(blob)
		if err != nil {
			t.Fatal(err)
		}
		heap, err := gov8.NewCppGCHeap(gov8.DefaultCppGCHeapCreateParams())
		if err != nil {
			t.Fatal(err)
		}
		if err := params.SetCppGCHeap(heap); err != nil {
			t.Fatal(err)
		}
		if err := blob.Release(); err != nil {
			t.Fatal(err)
		}
		if _, err := gov8.NewIsolateWithSnapshotParams(params); err == nil || !strings.Contains(err.Error(), "released") {
			t.Fatalf("released snapshot error = %v", err)
		}
		if err := heap.Close(); err != nil {
			t.Fatalf("unconsumed heap Close: %v", err)
		}
	})

	t.Run("closed allocator", func(t *testing.T) {
		blob := cpsSnapshotBlob(t, 1)
		defer blob.Release()
		allocator, err := gov8.NewDefaultArrayBufferAllocator()
		if err != nil {
			t.Fatal(err)
		}
		params, err := gov8.NewSnapshotCreateParams(blob)
		if err != nil {
			t.Fatal(err)
		}
		if err := params.SetArrayBufferAllocator(allocator); err != nil {
			t.Fatal(err)
		}
		if err := allocator.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := gov8.NewIsolateWithSnapshotParams(params); err == nil || !strings.Contains(err.Error(), "after Close") {
			t.Fatalf("closed allocator error = %v", err)
		}
	})

	t.Run("closed cppgc heap", func(t *testing.T) {
		blob := cpsSnapshotBlob(t, 1)
		defer blob.Release()
		heap, err := gov8.NewCppGCHeap(gov8.DefaultCppGCHeapCreateParams())
		if err != nil {
			t.Fatal(err)
		}
		params, err := gov8.NewSnapshotCreateParams(blob)
		if err != nil {
			t.Fatal(err)
		}
		if err := params.SetCppGCHeap(heap); err != nil {
			t.Fatal(err)
		}
		if err := heap.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := gov8.NewIsolateWithSnapshotParams(params); err == nil || !strings.Contains(err.Error(), "after Close or transfer") {
			t.Fatalf("closed cppgc heap error = %v", err)
		}
	})

	t.Run("wrong-thread heap", func(t *testing.T) {
		blob := cpsSnapshotBlob(t, 1)
		defer blob.Release()
		heap, err := gov8.NewCppGCHeap(gov8.DefaultCppGCHeapCreateParams())
		if err != nil {
			t.Fatal(err)
		}
		params, err := gov8.NewSnapshotCreateParams(blob)
		if err != nil {
			t.Fatal(err)
		}
		if err := params.SetCppGCHeap(heap); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := gov8.NewIsolateWithSnapshotParams(params); result <- err }()
		if err := <-result; err == nil || !strings.Contains(err.Error(), "thread affinity") {
			t.Fatalf("wrong-thread error = %v", err)
		}
		if err := heap.Close(); err != nil {
			t.Fatalf("unconsumed wrong-thread heap Close: %v", err)
		}
	})

}
