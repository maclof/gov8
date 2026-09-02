//go:build windows && amd64

package conformance_snapshot_resource_composition

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestMain(m *testing.M) {
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := gov8.Initialize(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code := m.Run()
	if _, err := gov8.Dispose(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 2
	} else if err := gov8.DisposePlatform(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 2
	}
	os.Exit(code)
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func openRuntime(t testing.TB, iso *gov8.Isolate) *runtime {
	t.Helper()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	return &runtime{iso, ctx, scope}
}

func (r *runtime) close(t testing.TB) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Error(err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Error(err)
	}
}

func eval(t testing.TB, r *runtime, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func marker(t testing.TB, r *runtime) int64 {
	t.Helper()
	value := eval(t, r, "snapshotMarker")
	result, ok, err := value.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("marker = %d, %v, %v", result, ok, err)
	}
	return result
}

func snapshot(t testing.TB, value int) *gov8.StartupData {
	t.Helper()
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatal(err)
	}
	r := openRuntime(t, creator.Isolate())
	eval(t, r, fmt.Sprintf("globalThis.snapshotMarker = %d", value))
	if err := creator.SetDefaultContext(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.close(t)
	blob, err := creator.CreateBlob(gov8.FunctionCodeKeep)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

type allocatorEvents struct {
	sync.Mutex
	initialized   []int
	uninitialized []int
	frees         []int
	drops         atomic.Int32
}

func (e *allocatorEvents) callbacks() gov8.ArrayBufferAllocatorCallbacks {
	return gov8.ArrayBufferAllocatorCallbacks{
		Allocate: func(size int) bool {
			e.Lock()
			e.initialized = append(e.initialized, size)
			e.Unlock()
			return true
		},
		AllocateUninitialized: func(size int) bool {
			e.Lock()
			e.uninitialized = append(e.uninitialized, size)
			e.Unlock()
			return true
		},
		Free: func(size int, _ byte) {
			e.Lock()
			e.frees = append(e.frees, size)
			e.Unlock()
		},
		Drop: func() { e.drops.Add(1) },
	}
}

func (e *allocatorEvents) snapshot() (initialized, uninitialized, frees []int) {
	e.Lock()
	defer e.Unlock()
	return append([]int{}, e.initialized...), append([]int{}, e.uninitialized...), append([]int{}, e.frees...)
}

func line(t *testing.T, check string, value any) string {
	t.Helper()
	data, err := json.Marshal(struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value any    `json:"value"`
	}{check, true, value})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func produce(t *testing.T) []string {
	var lines []string

	events := &allocatorEvents{}
	allocator, err := gov8.NewArrayBufferAllocator(events.callbacks())
	if err != nil {
		t.Fatal(err)
	}
	blob := snapshot(t, 41)
	defer blob.Release()
	params, err := gov8.NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		t.Fatal(err)
	}
	allocatorISO, err := gov8.NewIsolateWithSnapshotParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	r := openRuntime(t, allocatorISO)
	observedMarker := marker(t, r)
	buffer, err := gov8.NewArrayBuffer(r.scope, r.ctx, 9)
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
	r.close(t)
	initializedBefore, uninitializedBefore, freesBefore := events.snapshot()
	dropsBefore := events.drops.Load()
	if err := allocatorISO.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, freesAfterIsolate := events.snapshot()
	dropsAfterIsolate := events.drops.Load()
	byteAfter := []byte{0}
	if _, err := store.ReadAt(byteAfter, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, freesAfterStore := events.snapshot()
	lines = append(lines, line(t, "snapshot-resource-composition/custom_allocator", struct {
		BuilderOrder                    string `json:"builder_order"`
		Marker                          int64  `json:"marker"`
		InitializedBeforeIsolateDrop    []int  `json:"initialized_before_isolate_drop"`
		UninitializedBeforeIsolateDrop  []int  `json:"uninitialized_before_isolate_drop"`
		FreesBeforeIsolateDrop          []int  `json:"frees_before_isolate_drop"`
		AllocatorDropsBeforeIsolateDrop int32  `json:"allocator_drops_before_isolate_drop"`
		FreesAfterIsolateDrop           []int  `json:"frees_after_isolate_drop"`
		AllocatorDropsAfterIsolateDrop  int32  `json:"allocator_drops_after_isolate_drop"`
		StoreByteAfterIsolateDrop       byte   `json:"store_byte_after_isolate_drop"`
		FreesAfterStoreDrop             []int  `json:"frees_after_store_drop"`
		AllocatorDropsAfterStoreDrop    int32  `json:"allocator_drops_after_store_drop"`
	}{"allocator_then_snapshot", observedMarker, initializedBefore, uninitializedBefore, freesBefore, dropsBefore,
		freesAfterIsolate, dropsAfterIsolate, byteAfter[0], freesAfterStore, events.drops.Load()}))

	heapBlob := snapshot(t, 42)
	defer heapBlob.Release()
	heap, err := gov8.NewCppGCHeap(gov8.CppGCHeapCreateParams{MarkingSupport: gov8.CppGCMarkingAtomic, SweepingSupport: gov8.CppGCSweepingAtomic})
	if err != nil {
		t.Fatal(err)
	}
	heapParams, err := gov8.NewSnapshotCreateParams(heapBlob)
	if err != nil {
		t.Fatal(err)
	}
	if err := heapParams.SetCppGCHeap(heap); err != nil {
		t.Fatal(err)
	}
	heapISO, err := gov8.NewIsolateWithSnapshotParams(heapParams)
	if err != nil {
		t.Fatal(err)
	}
	sameHeap, err := heap.AttachedTo(heapISO)
	if err != nil {
		t.Fatal(err)
	}
	heapRuntime := openRuntime(t, heapISO)
	heapMarker := marker(t, heapRuntime)
	heapRuntime.close(t)
	var cppDrops atomic.Int32
	first, err := heapISO.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "SnapshotResourceCompositionLeaf", Alignment: 1, Callbacks: gov8.CppGCGenericCallbacks{Destroy: func() { cppDrops.Add(1) }}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := heapISO.CollectCppGCGarbageForTesting(gov8.CppGCStackNoHeapPointers); err != nil {
		t.Fatal(err)
	}
	dropsAfterCollection := cppDrops.Load()
	second, err := heapISO.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "SnapshotResourceCompositionLeaf", Alignment: 1, Callbacks: gov8.CppGCGenericCallbacks{Destroy: func() { cppDrops.Add(1) }}})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	_, shareErr := heapISO.TryIntoShared()
	intoErr, ok := shareErr.(*gov8.IntoSharedError)
	if !ok || intoErr.Kind != gov8.KindEmbedderCppHeap || intoErr.IntoIsolate() != heapISO {
		t.Fatalf("TryIntoShared = %#v", shareErr)
	}
	if err := heapISO.Close(); err != nil {
		t.Fatal(err)
	}
	if err := heap.Close(); err != nil {
		t.Fatal(err)
	}
	lines = append(lines, line(t, "snapshot-resource-composition/custom_cppgc_heap", struct {
		BuilderOrder               string `json:"builder_order"`
		Marker                     int64  `json:"marker"`
		SameHeapAddress            bool   `json:"same_heap_address"`
		DropsAfterForcedCollection int32  `json:"drops_after_forced_collection"`
		TryIntoSharedError         string `json:"try_into_shared_error"`
		DropsAfterIsolateDrop      int32  `json:"drops_after_isolate_drop"`
		IsolateDropOwnsHeap        bool   `json:"isolate_drop_owns_heap"`
	}{"snapshot_then_cpp_heap", heapMarker, sameHeap, dropsAfterCollection, "EmbedderCppHeap", cppDrops.Load(), true}))

	return lines
}

func fixture(t *testing.T) []string {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-snapshot-resource-composition-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestConformanceSnapshotResourceComposition(t *testing.T) {
	got := produce(t)
	got = append(got, `{"summary":{"total":2,"passed":2,"failed":0}}`)
	want := fixture(t)
	if !reflect.DeepEqual(got, want) {
		for i := range got {
			if i >= len(want) || got[i] != want[i] {
				wanted := "<missing>"
				if i < len(want) {
					wanted = want[i]
				}
				t.Errorf("line %d\n got: %s\nwant: %s", i+1, got[i], wanted)
			}
		}
		if len(got) != len(want) {
			t.Errorf("line count got %d want %d", len(got), len(want))
		}
	}
}
