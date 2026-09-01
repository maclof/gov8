//go:build windows && amd64

package conformance_array_buffer_allocator

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	gov8 "gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-array-buffer-allocator-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

var getCurrentThreadID = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

type events struct {
	mu            sync.Mutex
	initialized   []int
	uninitialized []int
	frees         []int
	first         []byte
	threads       map[uint32]struct{}
	drops         atomic.Int32
}

type eventSnapshot struct {
	initialized   []int
	uninitialized []int
	frees         []int
	first         []byte
	threads       int
}

func (e *events) observeThread() {
	id, _, _ := getCurrentThreadID.Call()
	if e.threads == nil {
		e.threads = make(map[uint32]struct{})
	}
	e.threads[uint32(id)] = struct{}{}
}

func (e *events) callbacks() gov8.ArrayBufferAllocatorCallbacks {
	return gov8.ArrayBufferAllocatorCallbacks{
		Allocate: func(length int) bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.initialized = append(e.initialized, length)
			e.observeThread()
			return true
		},
		AllocateUninitialized: func(length int) bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.uninitialized = append(e.uninitialized, length)
			e.observeThread()
			return true
		},
		Free: func(length int, first byte) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.frees = append(e.frees, length)
			e.first = append(e.first, first)
			e.observeThread()
		},
		Drop: func() { e.drops.Add(1) },
	}
}

func (e *events) snapshot() eventSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return eventSnapshot{
		initialized:   append([]int{}, e.initialized...),
		uninitialized: append([]int{}, e.uninitialized...),
		frees:         append([]int{}, e.frees...),
		first:         append([]byte{}, e.first...),
		threads:       len(e.threads),
	}
}

type runtimeState struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func openRuntime(t *testing.T, params *gov8.CreateParams) *runtimeState {
	t.Helper()
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = ctx.Close()
		_ = iso.Close()
		t.Fatal(err)
	}
	return &runtimeState{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtimeState) close(t *testing.T) {
	t.Helper()
	if r.scope != nil {
		if err := r.scope.Close(); err != nil {
			t.Fatal(err)
		}
		r.scope = nil
	}
	if r.ctx != nil {
		if err := r.ctx.Close(); err != nil {
			t.Fatal(err)
		}
		r.ctx = nil
	}
	if r.iso != nil {
		if err := r.iso.Close(); err != nil {
			t.Fatal(err)
		}
		r.iso = nil
	}
}

func customAllocator(t *testing.T, e *events) *gov8.ArrayBufferAllocator {
	t.Helper()
	allocator, err := gov8.NewArrayBufferAllocator(e.callbacks())
	if err != nil {
		t.Fatal(err)
	}
	return allocator
}

func paramsWith(t *testing.T, allocator *gov8.ArrayBufferAllocator) *gov8.CreateParams {
	t.Helper()
	params := gov8.NewCreateParams()
	if err := params.SetArrayBufferAllocator(allocator); err != nil {
		t.Fatal(err)
	}
	return params
}

func eval(t *testing.T, r *runtimeState, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func readAll(t *testing.T, store *gov8.BackingStore) []byte {
	t.Helper()
	length, err := store.ByteLength()
	if err != nil {
		t.Fatal(err)
	}
	result := make([]byte, length)
	if length != 0 {
		if n, err := store.ReadAt(result, 0); err != nil || n != length {
			t.Fatalf("ReadAt = %d, %v", n, err)
		}
	}
	return result
}

func octets(values []byte) []int {
	result := make([]int, len(values))
	for index, value := range values {
		result[index] = int(value)
	}
	return result
}

type outcome[T any] struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value T      `json:"value"`
}

func appendJSON[T any](t *testing.T, dst *bytes.Buffer, value T) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	dst.Write(encoded)
	dst.WriteByte('\n')
}

func produceFixture(t *testing.T) []byte {
	var output bytes.Buffer

	implicit := gov8.NewCreateParams()
	explicitAllocator, err := gov8.NewDefaultArrayBufferAllocator()
	if err != nil {
		t.Fatal(err)
	}
	explicit := gov8.NewCreateParams()
	if err := explicit.SetArrayBufferAllocator(explicitAllocator); err != nil {
		t.Fatal(err)
	}
	r := openRuntime(t, explicit)
	buffer, err := gov8.NewArrayBuffer(r.scope, r.ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	store, err := buffer.GetBackingStore()
	if err != nil {
		t.Fatal(err)
	}
	contents := readAll(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	zero, err := gov8.NewArrayBuffer(r.scope, r.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, zeroPresent, err := zero.Data()
	if err != nil {
		t.Fatal(err)
	}
	length, err := buffer.ByteLength()
	if err != nil {
		t.Fatal(err)
	}
	r.close(t)
	if err := explicitAllocator.Close(); err != nil {
		t.Fatal(err)
	}
	type defaultValue struct {
		RustCrate          string `json:"rust_crate"`
		V8                 string `json:"v8"`
		ImplicitHasSet     bool   `json:"implicit_has_set"`
		ExplicitHasSet     bool   `json:"explicit_has_set"`
		Length             int    `json:"length"`
		ZeroInitialized    bool   `json:"zero_initialized"`
		ZeroLengthDataNone bool   `json:"zero_length_data_none"`
	}
	version, err := gov8.VersionString()
	if err != nil {
		t.Fatal(err)
	}
	appendJSON(t, &output, outcome[defaultValue]{"array-buffer-allocator/pin_and_default_factory", true, defaultValue{
		"v8=152.2.0", version, implicit.HasSetArrayBufferAllocator(), explicit.HasSetArrayBufferAllocator(),
		length, bytes.Equal(contents, []byte{0, 0, 0, 0}), !zeroPresent,
	}})

	e := &events{}
	allocator := customAllocator(t, e)
	r = openRuntime(t, paramsWith(t, allocator))
	source, err := gov8.NewArrayBuffer(r.scope, r.ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	sourceStore, err := source.GetBackingStore()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := sourceStore.WriteAt([]byte{1, 2, 3, 4}, 0); err != nil || n != 4 {
		t.Fatalf("WriteAt = %d, %v", n, err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, "source", source.Value); err != nil || !ok {
		t.Fatalf("set source = %v, %v", ok, err)
	}
	zero, err = gov8.NewArrayBuffer(r.scope, r.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, zeroPresent, err = zero.Data()
	if err != nil {
		t.Fatal(err)
	}
	before := e.snapshot()
	transferredValue := eval(t, r, "globalThis.transferred=source.transfer(6); transferred")
	transferred, err := gov8.AsArrayBuffer(transferredValue)
	if err != nil {
		t.Fatal(err)
	}
	transferredStore, err := transferred.GetBackingStore()
	if err != nil {
		t.Fatal(err)
	}
	afterTransfer := e.snapshot()
	detached, err := source.WasDetached()
	if err != nil {
		t.Fatal(err)
	}
	transferredLength, err := transferred.ByteLength()
	if err != nil {
		t.Fatal(err)
	}
	transferredBytes := readAll(t, transferredStore)
	if err := transferredStore.Close(); err != nil {
		t.Fatal(err)
	}
	r.close(t)
	afterIsolate := e.snapshot()
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	type transferValue struct {
		BeforeInitialized          []int  `json:"before_initialized"`
		BeforeUninitialized        []int  `json:"before_uninitialized"`
		ZeroLengthBypassed         bool   `json:"zero_length_bypassed"`
		AfterTransferInitialized   []int  `json:"after_transfer_initialized"`
		AfterTransferUninitialized []int  `json:"after_transfer_uninitialized"`
		AfterTransferFrees         []int  `json:"after_transfer_frees"`
		SourceDetached             bool   `json:"source_detached"`
		TransferredLength          int    `json:"transferred_length"`
		TransferredBytes           string `json:"transferred_bytes"`
		AfterIsolateFrees          []int  `json:"after_isolate_frees"`
		FreedFirstBytes            []int  `json:"freed_first_bytes"`
		AllocatorDrops             int32  `json:"allocator_drops"`
	}
	appendJSON(t, &output, outcome[transferValue]{"array-buffer-allocator/callbacks_zero_and_transfer", true, transferValue{
		before.initialized, before.uninitialized, !zeroPresent, afterTransfer.initialized,
		afterTransfer.uninitialized, afterTransfer.frees, detached, transferredLength,
		hex.EncodeToString(transferredBytes), afterIsolate.frees, octets(afterIsolate.first), e.drops.Load(),
	}})

	e = &events{}
	allocator = customAllocator(t, e)
	r = openRuntime(t, paramsWith(t, allocator))
	standalone, err := r.iso.NewBackingStore(5)
	if err != nil {
		t.Fatal(err)
	}
	initialized := e.snapshot()
	standaloneContents := readAll(t, standalone)
	if err := standalone.Close(); err != nil {
		t.Fatal(err)
	}
	afterStore := e.snapshot()
	r.close(t)
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	type standaloneValue struct {
		Initialized    []int `json:"initialized"`
		Uninitialized  []int `json:"uninitialized"`
		ContentsZero   bool  `json:"contents_zero"`
		FreesAfterDrop []int `json:"frees_after_drop"`
		AllocatorDrops int32 `json:"allocator_drops"`
	}
	appendJSON(t, &output, outcome[standaloneValue]{"array-buffer-allocator/standalone_backing_store_free", true, standaloneValue{
		initialized.initialized, initialized.uninitialized, bytes.Equal(standaloneContents, make([]byte, 5)), afterStore.frees, e.drops.Load(),
	}})

	e = &events{}
	allocator = customAllocator(t, e)
	r = openRuntime(t, paramsWith(t, allocator))
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	kept, err := gov8.NewArrayBuffer(r.scope, r.ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	global, err = r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, "kept", kept.Value); err != nil || !ok {
		t.Fatalf("set kept = %v, %v", ok, err)
	}
	beforeTeardown := e.snapshot()
	dropsBeforeTeardown := e.drops.Load()
	r.close(t)
	afterTeardown := e.snapshot()
	type teardownValue struct {
		Initialized         []int `json:"initialized"`
		FreesBeforeTeardown []int `json:"frees_before_teardown"`
		DropsBeforeTeardown int32 `json:"drops_before_teardown"`
		FreesAfterTeardown  []int `json:"frees_after_teardown"`
		DropsAfterTeardown  int32 `json:"drops_after_teardown"`
	}
	appendJSON(t, &output, outcome[teardownValue]{"array-buffer-allocator/isolate_teardown_owns_allocator", true, teardownValue{
		beforeTeardown.initialized, beforeTeardown.frees, dropsBeforeTeardown, afterTeardown.frees, e.drops.Load(),
	}})

	e = &events{}
	allocator = customAllocator(t, e)
	r = openRuntime(t, paramsWith(t, allocator))
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	buffer, err = gov8.NewArrayBuffer(r.scope, r.ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	store, err = buffer.GetBackingStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteAt([]byte{77}, 0); err != nil {
		t.Fatal(err)
	}
	r.close(t)
	afterIsolate = e.snapshot()
	dropsAfterIsolate := e.drops.Load()
	readable := readAll(t, store)[0]
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	afterStore = e.snapshot()
	type outliveValue struct {
		FreesAfterIsolate         []int `json:"frees_after_isolate"`
		DropsAfterIsolate         int32 `json:"drops_after_isolate"`
		StoreReadableAfterIsolate byte  `json:"store_readable_after_isolate"`
		FreesAfterStore           []int `json:"frees_after_store"`
		FreedFirstBytes           []int `json:"freed_first_bytes"`
		DropsAfterStore           int32 `json:"drops_after_store"`
	}
	appendJSON(t, &output, outcome[outliveValue]{"array-buffer-allocator/backing_store_outlives_isolate", true, outliveValue{
		afterIsolate.frees, dropsAfterIsolate, readable, afterStore.frees, octets(afterStore.first), e.drops.Load(),
	}})

	e = &events{}
	allocator = customAllocator(t, e)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(2)
	errors := make(chan error, 2)
	for _, size := range []int{11, 13} {
		go func(length int) {
			params := gov8.NewCreateParams()
			if err := params.SetArrayBufferAllocator(allocator); err != nil {
				ready.Done()
				errors <- err
				return
			}
			iso, err := gov8.NewIsolateWithParams(params)
			ready.Done()
			if err != nil {
				errors <- err
				return
			}
			<-start
			ctx, err := iso.NewContext()
			if err != nil {
				errors <- err
				return
			}
			scope, err := iso.NewScope()
			if err != nil {
				errors <- err
				return
			}
			buffer, err := gov8.NewArrayBuffer(scope, ctx, length)
			if err == nil {
				var global *gov8.Object
				global, err = ctx.GlobalObject(scope)
				if err == nil {
					_, err = global.SetByName(scope, ctx, "threadBuffer", buffer.Value)
				}
			}
			if closeErr := scope.Close(); err == nil {
				err = closeErr
			}
			if closeErr := ctx.Close(); err == nil {
				err = closeErr
			}
			if closeErr := iso.Close(); err == nil {
				err = closeErr
			}
			errors <- err
		}(size)
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	threadEvents := e.snapshot()
	sort.Ints(threadEvents.initialized)
	sort.Ints(threadEvents.uninitialized)
	sort.Ints(threadEvents.frees)
	dropsBeforeOwner := e.drops.Load()
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	type threadsValue struct {
		InitializedSorted       []int `json:"initialized_sorted"`
		UninitializedSorted     []int `json:"uninitialized_sorted"`
		FreesSorted             []int `json:"frees_sorted"`
		DistinctCallbackThreads int   `json:"distinct_callback_threads"`
		DropsBeforeOwner        int32 `json:"drops_before_owner"`
		DropsAfterOwner         int32 `json:"drops_after_owner"`
	}
	appendJSON(t, &output, outcome[threadsValue]{"array-buffer-allocator/shared_allocator_across_isolate_threads", true, threadsValue{
		threadEvents.initialized, threadEvents.uninitialized, threadEvents.frees, threadEvents.threads, dropsBeforeOwner, e.drops.Load(),
	}})

	appendJSON(t, &output, struct {
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}{Summary: struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	}{6, 6, 0}})
	return output.Bytes()
}

func TestConformanceArrayBufferAllocatorFixture(t *testing.T) {
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	got := produceFixture(t)
	if !bytes.Equal(got, want) {
		t.Fatalf("fixture mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	lines := bytes.Split(bytes.TrimSuffix(got, []byte{'\n'}), []byte{'\n'})
	if len(lines) != 7 {
		t.Fatalf("fixture line count = %d", len(lines))
	}
}
