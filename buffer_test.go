//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"errors"
	"runtime"
	"testing"
	"time"
	"unsafe"

	gov8 "gov8"
)

// Behavior tests for the buffers slice, mirroring the pinned oracle's
// characterization (rust-oracle/src/bin/conformance-buffers.rs and
// tests/buffers_negative.rs). The byte-exact conformance runner lives in
// conformance-buffers/; these tests cover the same observations plus the
// lifecycle, negative, and concurrency cases that must not abort the process.

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}

// evalValue compiles and runs source, returning the completion value. When tc
// is non-nil compile/run failures are recorded there; ok reports success. The
// script is closed synchronously: a deferred/t.Cleanup close would run after
// the runtime cleanup disposed the isolate (use after free).
func evalValue(t testing.TB, ctx *gov8.Context, scope *gov8.Scope, tc *gov8.TryCatch, source string) (gov8.Value, bool) {
	t.Helper()
	script, cerr := ctx.Compile(scope, source, tc)
	if cerr != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(scope, tc)
	if rerr != nil {
		return gov8.Value{}, false
	}
	return v, true
}

// evalTextValue is the oracle's eval_text: ToString of the completion value.
func evalTextValue(t testing.TB, ctx *gov8.Context, scope *gov8.Scope, tc *gov8.TryCatch, source string) (string, bool) {
	t.Helper()
	v, ok := evalValue(t, ctx, scope, tc, source)
	if !ok {
		return "", false
	}
	s, err := v.ToString(ctx)
	if err != nil {
		return "", false
	}
	return s, true
}

// newManualRuntime is newTestRuntime without automatic cleanup: the caller
// closes scope/context/isolate explicitly (needed when a test must close
// the scope early and observe engine-side effects afterwards).
func newManualRuntime(t *testing.T) (*gov8.Isolate, *gov8.Context, *gov8.Scope, func(*testing.T)) {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	return iso, ctx, scope, func(t2 *testing.T) {
		_ = scope.Close() // may already be closed by the test; ignore
		_ = ctx.Close()
		if err := iso.Close(); err != nil {
			t2.Errorf("iso.Close: %v", err)
		}
	}
}

// --- construction -------------------------------------------------------------

func TestArrayBufferNewBasics(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	ab16, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer(16): %v", err)
	}
	ab0, err := gov8.NewArrayBuffer(scope, ctx, 0)
	if err != nil {
		t.Fatalf("NewArrayBuffer(0): %v", err)
	}
	v8, ok := evalValue(t, ctx, scope, nil, "new ArrayBuffer(8)")
	if !ok {
		t.Fatal("eval new ArrayBuffer(8) failed")
	}
	isAB, _ := v8.IsArrayBuffer()
	if !isAB {
		t.Fatal("JS-created value is not an ArrayBuffer")
	}
	jsAB, err := gov8.AsArrayBuffer(v8)
	if err != nil {
		t.Fatalf("AsArrayBuffer: %v", err)
	}

	if n, _ := ab16.ByteLength(); n != 16 {
		t.Errorf("len16 byte_length = %d", n)
	}
	if d, _ := ab16.IsDetachable(); !d {
		t.Error("len16 is_detachable = false")
	}
	if d, _ := ab16.WasDetached(); d {
		t.Error("len16 was_detached = true")
	}
	if _, some, _ := ab16.Data(); !some {
		t.Error("len16 data is none")
	}

	if n, _ := ab0.ByteLength(); n != 0 {
		t.Errorf("len0 byte_length = %d", n)
	}
	// Pinned nuance: a zero-length buffer consults the real WasDetached bit.
	if d, _ := ab0.WasDetached(); d {
		t.Error("len0 was_detached = true")
	}
	if _, some, _ := ab0.Data(); some {
		t.Error("len0 data is some")
	}

	if n, _ := jsAB.ByteLength(); n != 8 {
		t.Errorf("js byte_length = %d", n)
	}
	if d, _ := jsAB.IsDetachable(); !d {
		t.Error("js is_detachable = false")
	}
	if _, some, _ := jsAB.Data(); !some {
		t.Error("js data is none")
	}
}

// --- backing store ownership ---------------------------------------------------

func TestBackingStoreOwnershipLifecycle(t *testing.T) {
	iso, ctx, scope, closeRuntime := newManualRuntime(t)
	defer closeRuntime(t)

	bs, err := iso.NewBackingStoreFromSlice([]byte{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}

	if n, _ := bs.UseCount(); n != 1 {
		t.Fatalf("standalone use count = %d, want 1", n)
	}
	if sh, _ := bs.IsShared(); sh {
		t.Error("standalone is_shared = true")
	}
	if rz, _ := bs.IsResizableByUserJavaScript(); rz {
		t.Error("standalone is_resizable = true")
	}
	buf := make([]byte, 4)
	if n, _ := bs.ReadAt(buf, 0); n != 4 || !bytes.Equal(buf, []byte{1, 2, 3, 4}) {
		t.Fatalf("standalone read = %d % x", n, buf)
	}

	ab, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
	}
	if n, _ := bs.UseCount(); n != 2 {
		t.Fatalf("attached use count = %d, want 2", n)
	}
	if n, _ := ab.ByteLength(); n != 4 {
		t.Fatalf("buffer byte_length = %d", n)
	}
	view, err := gov8.NewUint8Array(scope, ctx, ab, 0, 4)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	out := make([]byte, 4)
	if n, _ := view.CopyContents(out); n != 4 || !bytes.Equal(out, []byte{1, 2, 3, 4}) {
		t.Fatalf("typed array contents = %d % x", n, out)
	}

	// Close the scope so the engine drops the JS-side reference, force a full
	// GC, and observe the count fall back to 1 while the standalone Go
	// reference keeps the bytes alive and reusable.
	if err := scope.Close(); err != nil {
		t.Fatalf("scope.Close: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	if n, _ := bs.UseCount(); n != 1 {
		t.Fatalf("collected use count = %d, want 1", n)
	}
	got := make([]byte, 4)
	if n, _ := bs.ReadAt(got, 0); n != 4 || !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("post-GC bytes = %d % x", n, got)
	}

	scope2, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope2.Close() }()
	rebuilt, err := gov8.NewArrayBufferWithBackingStore(scope2, ctx, bs)
	if err != nil {
		t.Fatalf("rebuild after GC: %v", err)
	}
	if n, _ := rebuilt.ByteLength(); n != 4 {
		t.Fatalf("rebuilt byte_length = %d", n)
	}
}

func TestBackingStoreAliasAndDetachIndependence(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	bs, err := iso.NewBackingStoreFromSlice([]byte{10, 20, 30, 40})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	defer func() {
		if err := bs.Close(); err != nil {
			t.Errorf("bs.Close: %v", err)
		}
	}()

	ab1, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("buffer1: %v", err)
	}
	ab2, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("buffer2: %v", err)
	}
	if n, _ := bs.UseCount(); n != 3 {
		t.Fatalf("two-buffer use count = %d, want 3", n)
	}

	// Interior-mutable write through the store, observed through ab2's view.
	if n, err := bs.WriteAt([]byte{99}, 1); err != nil || n != 1 {
		t.Fatalf("WriteAt: %d %v", n, err)
	}
	ta2, err := gov8.NewUint8Array(scope, ctx, ab2, 0, 4)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	view := make([]byte, 4)
	if n, _ := ta2.CopyContents(view); n != 4 || hexEncode(view) != "0a631e28" {
		t.Fatalf("seen by ab2 = %d %s", n, hexEncode(view))
	}

	if ok, err := ab1.Detach(ctx, gov8.Value{}); err != nil || !ok {
		t.Fatalf("detach ab1 = %v %v", ok, err)
	}
	if n, _ := ab1.ByteLength(); n != 0 {
		t.Errorf("ab1 length after detach = %d", n)
	}
	if n, _ := ab2.ByteLength(); n != 4 {
		t.Errorf("ab2 length after sibling detach = %d", n)
	}
	after := make([]byte, 4)
	if n, _ := ta2.CopyContents(after); n != 4 || hexEncode(after) != "0a631e28" {
		t.Fatalf("ab2 after detach = %d %s", n, hexEncode(after))
	}
}

func TestBackingStoreOutOfRangeCopies(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	bs, err := iso.NewBackingStoreFromSlice([]byte{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	defer func() { _ = bs.Close() }()
	if _, err := bs.ReadAt(make([]byte, 4), 2); err == nil {
		t.Error("read past end accepted")
	}
	if _, err := bs.WriteAt([]byte{1, 2, 3}, 2); err == nil {
		t.Error("write past end accepted")
	}
	if _, err := bs.ReadAt(make([]byte, 2), -1); err == nil {
		t.Error("negative offset accepted")
	}
}

// --- shared array buffers -------------------------------------------------------

func TestSharedArrayBufferBasics(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	bs, err := iso.NewSharedArrayBufferBackingStore(8)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	if sh, _ := bs.IsShared(); !sh {
		t.Error("store is_shared = false")
	}
	if n, _ := bs.ByteLength(); n != 8 {
		t.Errorf("store byte_length = %d", n)
	}

	sab, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
	}
	if n, _ := bs.UseCount(); n != 2 {
		t.Errorf("use count with SAB = %d, want 2", n)
	}
	if n, _ := sab.ByteLength(); n != 8 {
		t.Errorf("sab byte_length = %d", n)
	}
	fromSAB, err := sab.GetBackingStore()
	if err != nil {
		t.Fatalf("sab.GetBackingStore: %v", err)
	}
	if sh, _ := fromSAB.IsShared(); !sh {
		t.Error("SAB backing store is_shared = false")
	}
	// Dropping the extra reference restores the expected count.
	if err := fromSAB.Close(); err != nil {
		t.Errorf("fromSAB.Close: %v", err)
	}
	if n, _ := bs.UseCount(); n != 2 {
		t.Errorf("use count after closing extra ref = %d, want 2", n)
	}

	v := sab.Value
	if is, _ := v.IsSharedArrayBuffer(); !is {
		t.Error("is_shared_array_buffer = false")
	}
	if is, _ := v.IsArrayBuffer(); is {
		t.Error("SAB reported as plain ArrayBuffer")
	}
}

// --- detach ---------------------------------------------------------------------

func TestDetachBasic(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	if _, ok := evalValue(t, ctx, scope, nil, "globalThis.ab = new ArrayBuffer(8)"); !ok {
		t.Fatal("seed eval failed")
	}
	v, ok := evalValue(t, ctx, scope, nil, "ab")
	if !ok {
		t.Fatal("eval ab failed")
	}
	ab, err := gov8.AsArrayBuffer(v)
	if err != nil {
		t.Fatalf("AsArrayBuffer: %v", err)
	}

	if n, _ := ab.ByteLength(); n != 8 {
		t.Fatalf("before: byte_length = %d", n)
	}
	ok2, err := ab.Detach(ctx, gov8.Value{})
	if err != nil || !ok2 {
		t.Fatalf("detach = %v %v", ok2, err)
	}
	if n, _ := ab.ByteLength(); n != 0 {
		t.Errorf("after: byte_length = %d", n)
	}
	if d, _ := ab.WasDetached(); !d {
		t.Error("after: was_detached = false")
	}
	if _, some, _ := ab.Data(); some {
		t.Error("after: data is some")
	}
	jsSees, ok3 := evalTextValue(t, ctx, scope, nil, "`${ab.byteLength},${ab.detached}`")
	if !ok3 || jsSees != "0,true" {
		t.Errorf("js sees = %q %v", jsSees, ok3)
	}
	// A second detach is a no-op success.
	ok4, err := ab.Detach(ctx, gov8.Value{})
	if err != nil || !ok4 {
		t.Errorf("second detach = %v %v", ok4, err)
	}
}

func TestDetachKeyGate(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	ab, err := gov8.NewArrayBuffer(scope, ctx, 8)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	key, err := scope.NewString("owner")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	wrong, err := scope.NewString("other")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if err := ab.SetDetachKey(key); err != nil {
		t.Fatalf("SetDetachKey: %v", err)
	}

	ok, err := ab.Detach(ctx, wrong)
	if err != nil || ok {
		t.Fatalf("wrong key detach = %v %v, want false", ok, err)
	}
	if n, _ := ab.ByteLength(); n != 8 {
		t.Errorf("untouched byte_length = %d", n)
	}
	if d, _ := ab.WasDetached(); d {
		t.Error("untouched was_detached = true")
	}

	// A set detach key also rejects a detach attempt WITHOUT a key.
	ok, err = ab.Detach(ctx, gov8.Value{})
	if err != nil || ok {
		t.Fatalf("none-key detach = %v %v, want false", ok, err)
	}
	if n, _ := ab.ByteLength(); n != 8 {
		t.Errorf("state after none-key byte_length = %d", n)
	}

	ok, err = ab.Detach(ctx, key)
	if err != nil || !ok {
		t.Fatalf("right key detach = %v %v", ok, err)
	}
	if n, _ := ab.ByteLength(); n != 0 {
		t.Errorf("final byte_length = %d", n)
	}
	if d, _ := ab.WasDetached(); !d {
		t.Error("final was_detached = false")
	}
}

func TestDetachViewsFollow(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	if _, ok := evalValue(t, ctx, scope, nil,
		"globalThis.ab = new ArrayBuffer(8); globalThis.ta = new Uint8Array(ab, 2, 4)"); !ok {
		t.Fatal("seed eval failed")
	}
	abV, ok := evalValue(t, ctx, scope, nil, "ab")
	if !ok {
		t.Fatal("eval ab failed")
	}
	ab, err := gov8.AsArrayBuffer(abV)
	if err != nil {
		t.Fatalf("AsArrayBuffer: %v", err)
	}
	taV, ok := evalValue(t, ctx, scope, nil, "ta")
	if !ok {
		t.Fatal("eval ta failed")
	}
	ta, err := gov8.AsTypedArray(taV)
	if err != nil {
		t.Fatalf("AsTypedArray: %v", err)
	}

	if n, _ := ta.Length(); n != 4 {
		t.Fatalf("before length = %d", n)
	}
	if n, _ := ta.ByteOffset(); n != 2 {
		t.Errorf("before byte_offset = %d", n)
	}
	if n, _ := ta.ByteLength(); n != 4 {
		t.Errorf("before byte_length = %d", n)
	}
	jsBefore, ok2 := evalTextValue(t, ctx, scope, nil, "`${ta.length},${ta.byteLength},${ta[0]}`")
	if !ok2 || jsBefore != "4,4,0" {
		t.Fatalf("js before = %q %v", jsBefore, ok2)
	}

	ok3, err := ab.Detach(ctx, gov8.Value{})
	if err != nil || !ok3 {
		t.Fatalf("detach = %v %v", ok3, err)
	}
	if n, _ := ta.Length(); n != 0 {
		t.Errorf("after length = %d", n)
	}
	if n, _ := ta.ByteLength(); n != 0 {
		t.Errorf("after byte_length = %d", n)
	}
	viewBuf, err := ta.Buffer()
	if err != nil {
		t.Fatalf("ta.Buffer: %v", err)
	}
	same, err := gov8.Same(viewBuf.Value, ab.Value)
	if err != nil || !same {
		t.Fatalf("view buffer identity = %v %v", same, err)
	}
	jsAfter, ok4 := evalTextValue(t, ctx, scope, nil, "`${ta.length},${ta.byteLength},${ta[0]}`")
	if !ok4 || jsAfter != "0,0,undefined" {
		t.Fatalf("js after = %q %v", jsAfter, ok4)
	}
	// A zero-length view over the now-detached (zero-length) buffer works.
	if _, err := gov8.NewUint8Array(scope, ctx, ab, 0, 0); err != nil {
		t.Errorf("view after detach: %v", err)
	}
}

func TestDetachJSTransfer(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	jsSees, ok := evalTextValue(t, ctx, scope, nil,
		"const src = new ArrayBuffer(8); const dst = src.transfer(); "+
			"`${src.detached},${src.byteLength},${dst.byteLength}`")
	if !ok {
		t.Fatal("eval transfer failed")
	}
	if jsSees != "true,0,8" {
		t.Fatalf("js transfer = %q", jsSees)
	}
}

// --- typed arrays / DataView ------------------------------------------------------

func TestTypedArrayBounds(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	ta, err := gov8.NewUint8Array(scope, ctx, ab, 4, 8)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	if n, _ := ta.Length(); n != 8 {
		t.Errorf("length = %d", n)
	}
	if n, _ := ta.ByteOffset(); n != 4 {
		t.Errorf("byte_offset = %d", n)
	}
	if n, _ := ta.ByteLength(); n != 8 {
		t.Errorf("byte_length = %d", n)
	}
	if hb, _ := ta.HasBuffer(); !hb {
		t.Error("has_buffer = false")
	}
	buf, err := ta.Buffer()
	if err != nil {
		t.Fatalf("Buffer: %v", err)
	}
	if same, _ := gov8.Same(buf.Value, ab.Value); !same {
		t.Error("buffer identity mismatch")
	}
	contents := make([]byte, 8)
	if n, _ := ta.CopyContents(contents); n != 8 || hexEncode(contents) != "0000000000000000" {
		t.Errorf("contents = %d %s", n, hexEncode(contents))
	}

	// In-bounds zero-length views at the start and exactly at the end work.
	if _, err := gov8.NewUint8Array(scope, ctx, ab, 16, 0); err != nil {
		t.Errorf("end zero-length view: %v", err)
	}
	if _, err := gov8.NewUint8Array(scope, ctx, ab, 0, 0); err != nil {
		t.Errorf("zero-length view: %v", err)
	}
}

// TestTypedArrayFatalBoundariesAreErrors pins the intentional deviation from
// the oracle: the engine CHECK-fatals (process aborts) on out-of-bounds and
// misaligned view construction; gov8 rejects the same geometry with an error
// before entering the engine.
func TestTypedArrayFatalBoundariesAreErrors(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	if _, err := gov8.NewUint8Array(scope, ctx, ab, 8, 16); err == nil {
		t.Error("out-of-bounds Uint8Array accepted")
	}
	if _, err := gov8.NewFloat64Array(scope, ctx, ab, 4, 1); err == nil {
		t.Error("misaligned Float64Array accepted")
	}
	if _, err := gov8.NewDataView(scope, ctx, ab, 2, 100); err == nil {
		t.Error("out-of-bounds DataView accepted")
	}
	if _, err := gov8.NewUint8Array(scope, ctx, ab, -1, 1); err == nil {
		t.Error("negative offset accepted")
	}
}

func TestDataViewBounds(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	dv, err := gov8.NewDataView(scope, ctx, ab, 2, 8)
	if err != nil {
		t.Fatalf("NewDataView: %v", err)
	}
	v := dv.Value
	if is, _ := v.IsDataView(); !is {
		t.Error("is_data_view = false")
	}
	if is, _ := v.IsArrayBufferView(); !is {
		t.Error("is_array_buffer_view = false")
	}
	if is, _ := v.IsTypedArray(); is {
		t.Error("DataView reported as TypedArray")
	}
	if n, _ := dv.ByteOffset(); n != 2 {
		t.Errorf("byte_offset = %d", n)
	}
	if n, _ := dv.ByteLength(); n != 8 {
		t.Errorf("byte_length = %d", n)
	}
}

func TestTypedArrayLimits(t *testing.T) {
	limits, err := gov8.TypedArrayLimitsQuery()
	if err != nil {
		t.Fatalf("TypedArrayLimitsQuery: %v", err)
	}
	if limits.MaxByteLength != 9_007_199_254_740_991 {
		t.Errorf("max byte length = %d", limits.MaxByteLength)
	}
	if limits.Uint8MaxLength != 9_007_199_254_740_991 {
		t.Errorf("uint8 max length = %d", limits.Uint8MaxLength)
	}
	if limits.Float64MaxLength != 1_125_899_906_842_623 {
		t.Errorf("float64 max length = %d", limits.Float64MaxLength)
	}
	if limits.BigInt64MaxLength != 1_125_899_906_842_623 {
		t.Errorf("bigint64 max length = %d", limits.BigInt64MaxLength)
	}
	if limits.MaxSizeInHeap != 0 {
		t.Errorf("max size in heap = %d", limits.MaxSizeInHeap)
	}
}

// --- external backing store with deleter -----------------------------------------

func TestExternalBackingStoreDeleter(t *testing.T) {
	iso, ctx, scope, closeRuntime := newManualRuntime(t)
	defer closeRuntime(t)

	invocations := 0
	observedLen := 0
	observedData := uintptr(0)
	registered := uintptr(0xA5A5A5A5) // the deleterData the callback must see
	memory := make([]byte, 12)
	for i := range memory {
		memory[i] = 7
	}

	bs, err := iso.NewBackingStoreFromPtr(unsafe.Pointer(&memory[0]), len(memory),
		func(data unsafe.Pointer, byteLength int, deleterData uintptr) {
			_ = data // the memory is Go-owned here; nothing to free
			invocations++
			observedLen = byteLength
			observedData = deleterData
		}, registered)
	if err != nil {
		t.Fatalf("NewBackingStoreFromPtr: %v", err)
	}

	// The store is readable through its normal surface.
	got := make([]byte, 12)
	if n, _ := bs.ReadAt(got, 0); n != 12 || !allByte(got, 7) {
		t.Fatalf("store bytes = %d % x", n, got)
	}

	func() {
		ab, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
		if err != nil {
			t.Fatalf("buffer over external store: %v", err)
		}
		if n, _ := ab.ByteLength(); n != 12 {
			t.Errorf("external buffer length = %d", n)
		}
	}()

	// Drop the JS-side reference, then the Go reference: the deleter must run
	// exactly once, with the registered length and deleterData. KeepAlive
	// pins the registered memory through the deleter's synchronous run.
	if err := scope.Close(); err != nil {
		t.Fatalf("scope.Close: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	if err := bs.Close(); err != nil {
		t.Fatalf("bs.Close: %v", err)
	}
	runtime.KeepAlive(memory)

	if invocations != 1 {
		t.Errorf("deleter invocations = %d, want 1", invocations)
	}
	if observedLen != 12 {
		t.Errorf("observed byte length = %d", observedLen)
	}
	if observedData != registered {
		t.Errorf("deleterData = %#x, want %#x", observedData, registered)
	}
}

func allByte(b []byte, v byte) bool {
	for _, x := range b {
		if x != v {
			return false
		}
	}
	return true
}

// --- lifecycle / misuse --------------------------------------------------------

func TestBackingStoreDoubleCloseAndUseAfterClose(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	bs, err := iso.NewBackingStoreFromSlice([]byte{1})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	if err := bs.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := bs.Close(); err == nil {
		t.Error("second Close accepted")
	}
	if _, err := bs.ByteLength(); err == nil {
		t.Error("ByteLength after Close accepted")
	}
	if _, err := bs.UseCount(); err == nil {
		t.Error("UseCount after Close accepted")
	}
}

func TestBufferWrongThreadAffinity(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 8)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ab.ByteLength()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("foreign-thread ArrayBuffer access accepted")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("foreign-thread probe timed out")
	}
}

// --- concurrency ------------------------------------------------------------------

// TestParallelIsolateSerialization runs two isolates on two OS threads, each
// serializing and deserializing on its own thread through the shared
// delegate registry (thread-affine resources used concurrently, never
// shared). Every resource is created and closed on its own goroutine.
func TestParallelIsolateSerialization(t *testing.T) {
	run := func() error {
		iso, err := gov8.NewIsolate()
		if err != nil {
			return err
		}
		defer func() { _ = iso.Close() }()
		ctx, err := iso.NewContext()
		if err != nil {
			return err
		}
		defer func() { _ = ctx.Close() }()
		scope, err := iso.NewScope()
		if err != nil {
			return err
		}
		defer func() { _ = scope.Close() }()

		v, ok := evalValue(t, ctx, scope, nil, "({n: 7})")
		if !ok {
			return errors.New("eval failed")
		}
		ser, err := gov8.NewValueSerializer(scope, ctx, reportingDelegate{})
		if err != nil {
			return err
		}
		if ok, err := ser.WriteValue(ctx, v, nil); err != nil || !ok {
			return errors.Join(err, errors.New("WriteValue failed"))
		}
		wire, err := ser.Release()
		if err != nil {
			return err
		}
		if err := ser.Close(); err != nil {
			return err
		}
		vd, err := gov8.NewValueDeserializer(scope, ctx, wire)
		if err != nil {
			return err
		}
		if _, err := vd.ReadValue(ctx, nil); err != nil {
			return err
		}
		return vd.Close()
	}
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- run() }()
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("parallel run: %v", err)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("parallel serialization timed out")
		}
	}
}

// reportingDelegate is the oracle's DataCloneErrorReporter: capture the
// message and re-throw it as a JS Error.
type reportingDelegate struct{}

func (reportingDelegate) ThrowDataCloneError(message string) bool { return true }
