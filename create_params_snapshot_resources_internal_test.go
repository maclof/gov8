//go:build windows && amd64

package gov8

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func snapshotResourceInternalBlob(t *testing.T) *StartupData {
	t.Helper()
	creator, err := NewSnapshotCreator()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := creator.Isolate().NewContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.SetDefaultContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(FunctionCodeKeep)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestSnapshotResourceBeginFailureRollsBackWithoutHeapConsumption(t *testing.T) {
	blob := snapshotResourceInternalBlob(t)
	defer blob.Release()
	var allocatorDrops atomic.Int32
	allocator, err := NewArrayBufferAllocator(ArrayBufferAllocatorCallbacks{Drop: func() { allocatorDrops.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	params, err := NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		t.Fatal(err)
	}

	type result struct {
		err      error
		heapErr  error
		prepared chan struct{}
		proceed  chan struct{}
	}
	state := result{prepared: make(chan struct{}), proceed: make(chan struct{})}
	done := make(chan result, 1)
	go func() {
		heap, heapErr := NewCppGCHeap(DefaultCppGCHeapCreateParams())
		if heapErr == nil {
			heapErr = params.SetCppGCHeap(heap)
		}
		params.SetCounterLookupCallback(func(string) {})
		close(state.prepared)
		<-state.proceed
		_, constructorErr := NewIsolateWithSnapshotParams(params)
		if heap != nil {
			if closeErr := heap.Close(); heapErr == nil {
				heapErr = closeErr
			}
		}
		done <- result{err: constructorErr, heapErr: heapErr}
	}()
	<-state.prepared

	isolateCounterRegistry.Lock()
	target := isolateCounterRegistry.next + 1
	isolateCounterRegistry.Unlock()
	lifecycleMu.Lock()
	close(state.proceed)
	deadline := time.Now().Add(5 * time.Second)
	for {
		isolateCounterRegistry.Lock()
		_, registered := isolateCounterRegistry.entries[target]
		isolateCounterRegistry.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			lifecycleMu.Unlock()
			t.Fatal("counter callback was not registered")
		}
		runtime.Gosched()
	}
	storePlatform(stateDisposed)
	lifecycleMu.Unlock()
	got := <-done
	lifecycleMu.Lock()
	storePlatform(stateInitialized)
	lifecycleMu.Unlock()
	if !errors.Is(got.err, ErrNotInitialized) {
		t.Fatalf("constructor error = %v", got.err)
	}
	if got.heapErr != nil {
		t.Fatalf("unconsumed heap cleanup = %v", got.heapErr)
	}
	isolateCounterRegistry.Lock()
	_, counterLeaked := isolateCounterRegistry.entries[target]
	isolateCounterRegistry.Unlock()
	if counterLeaked {
		t.Fatal("constructor failure leaked counter registration")
	}
	if count, err := allocator.UseCount(); err != nil || count != 1 {
		t.Fatalf("allocator use count after rollback = %d, %v", count, err)
	}
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	if allocatorDrops.Load() != 1 {
		t.Fatalf("allocator drops = %d", allocatorDrops.Load())
	}
}
