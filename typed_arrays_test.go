//go:build windows && amd64

package gov8_test

import (
	"strings"
	"sync"
	"testing"

	gov8 "gov8"
)

// Behavior tests for the typed-array slice, mirroring the pinned oracle's
// characterization (rust-oracle/src/bin/conformance-typed-arrays.rs and
// tests/typed_arrays_negative.rs). The byte-exact conformance runner lives in
// conformance-typed-arrays/; these tests cover the same observations plus
// the lifecycle, affinity, and concurrency cases that must not abort the
// process.

// taSetGlobal stores v on the context global under name.
func taSetGlobal(t testing.TB, ctx *gov8.Context, scope *gov8.Scope, name string, v gov8.Value) {
	t.Helper()
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	ok, err := global.SetByName(scope, ctx, name, v)
	if err != nil || !ok {
		t.Fatalf("SetByName(%s) = %v, %v", name, ok, err)
	}
}

// --- kind predicates -----------------------------------------------------------

func TestTypedArrayKindPredicates(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	for _, kind := range gov8.TypedArrayKinds {
		ctor := kind.String()
		v, ok := evalValue(t, ctx, scope, nil, "new "+ctor+"(4)")
		if !ok {
			t.Fatalf("eval new %s(4) failed", ctor)
		}
		if is, _ := v.IsTypedArray(); !is {
			t.Errorf("%s: IsTypedArray = false", ctor)
		}
		if is, _ := v.IsArrayBufferView(); !is {
			t.Errorf("%s: IsArrayBufferView = false", ctor)
		}
		if is, _ := v.IsDataView(); is {
			t.Errorf("%s: IsDataView = true", ctor)
		}
		if is, _ := v.IsSharedArrayBuffer(); is {
			t.Errorf("%s: IsSharedArrayBuffer = true", ctor)
		}
		specific, err := kind.IsTypedArrayOfKind(v)
		if err != nil || !specific {
			t.Errorf("%s: specific predicate = %v, %v", ctor, specific, err)
		}
		// Every other kind's predicate must reject this value.
		for _, other := range gov8.TypedArrayKinds {
			if other == kind {
				continue
			}
			if is, err := other.IsTypedArrayOfKind(v); err != nil || is {
				t.Errorf("%s: %s predicate = %v, %v", ctor, other, is, err)
			}
		}
		// The 1-element geometry observes the element size.
		ab, err := gov8.NewArrayBuffer(scope, ctx, int(kind.ElementSize()))
		if err != nil {
			t.Fatalf("NewArrayBuffer: %v", err)
		}
		view, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, kind, 0, 1)
		if err != nil {
			t.Fatalf("NewTypedArrayOfKind(%s): %v", ctor, err)
		}
		if n, _ := view.ByteLength(); n != kind.ElementSize() {
			t.Errorf("%s: 1-element byte length = %d", ctor, n)
		}
	}

	// Contrast rows: a DataView is a view but not a typed array; an
	// ArrayBuffer is neither.
	dv, ok := evalValue(t, ctx, scope, nil, "new DataView(new ArrayBuffer(8))")
	if !ok {
		t.Fatal("eval DataView failed")
	}
	if is, _ := dv.IsTypedArray(); is {
		t.Error("DataView: IsTypedArray = true")
	}
	if is, _ := dv.IsArrayBufferView(); !is {
		t.Error("DataView: IsArrayBufferView = false")
	}
	if is, _ := dv.IsDataView(); !is {
		t.Error("DataView: IsDataView = false")
	}
	abVal, ok := evalValue(t, ctx, scope, nil, "new ArrayBuffer(8)")
	if !ok {
		t.Fatal("eval ArrayBuffer failed")
	}
	if is, _ := abVal.IsTypedArray(); is {
		t.Error("ArrayBuffer: IsTypedArray = true")
	}
	if is, _ := abVal.IsArrayBufferView(); is {
		t.Error("ArrayBuffer: IsArrayBufferView = true")
	}
}

// --- constants -----------------------------------------------------------------

func TestTypedArrayConstants(t *testing.T) {
	limits, err := gov8.TypedArrayKindLimitsQuery()
	if err != nil {
		t.Fatalf("TypedArrayKindLimitsQuery: %v", err)
	}
	const max = 9_007_199_254_740_991 // 2^53 - 1
	if limits.MaxByteLength != max {
		t.Errorf("MaxByteLength = %d, want %d", limits.MaxByteLength, max)
	}
	if limits.MaxSizeInHeap != 0 {
		t.Errorf("MaxSizeInHeap = %d, want 0", limits.MaxSizeInHeap)
	}
	if len(limits.MaxLengths) != 12 {
		t.Fatalf("MaxLengths has %d entries, want 12", len(limits.MaxLengths))
	}
	for _, kind := range gov8.TypedArrayKinds {
		want := max / int64(kind.ElementSize())
		if got := limits.MaxLengths[kind]; got != want {
			t.Errorf("%s.MaxLength = %d, want %d", kind, got, want)
		}
	}

	// The legacy three-kind query must agree on the kinds it reports.
	legacy, err := gov8.TypedArrayLimitsQuery()
	if err != nil {
		t.Fatalf("TypedArrayLimitsQuery: %v", err)
	}
	if legacy.MaxByteLength != limits.MaxByteLength ||
		legacy.Uint8MaxLength != limits.MaxLengths[gov8.KindUint8] ||
		legacy.Float64MaxLength != limits.MaxLengths[gov8.KindFloat64] ||
		legacy.BigInt64MaxLength != limits.MaxLengths[gov8.KindBigInt64] ||
		legacy.MaxSizeInHeap != limits.MaxSizeInHeap {
		t.Errorf("legacy limits %+v disagree with per-kind limits %+v", legacy, limits)
	}

	// Kind metadata.
	if (gov8.TypedArrayKind(99)).IsValid() {
		t.Error("kind 99 reported valid")
	}
	if (gov8.TypedArrayKind(99)).ElementSize() != 0 {
		t.Error("kind 99 element size != 0")
	}
	if gov8.KindFloat16.ElementSize() != 2 || gov8.KindBigUint64.ElementSize() != 8 ||
		gov8.KindUint8Clamped.ElementSize() != 1 {
		t.Error("kind element sizes wrong")
	}
	if gov8.KindFloat16.String() != "Float16Array" {
		t.Errorf("KindFloat16.String() = %q", gov8.KindFloat16.String())
	}
}

// --- native geometry -------------------------------------------------------------

func TestTypedArrayNativeGeometry(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 32)
	if err != nil {
		t.Fatalf("NewArrayBuffer(32): %v", err)
	}

	for _, kind := range gov8.TypedArrayKinds {
		size := kind.ElementSize()
		view, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, kind, size, 3)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if n, _ := view.Length(); n != 3 {
			t.Errorf("%s: length = %d", kind, n)
		}
		if n, _ := view.ByteLength(); n != 3*size {
			t.Errorf("%s: byte length = %d", kind, n)
		}
		if n, _ := view.ByteOffset(); n != size {
			t.Errorf("%s: byte offset = %d", kind, n)
		}
		// JS agrees with the native geometry.
		taSetGlobal(t, ctx, scope, "ta", view.Value)
		js, ok := evalTextValue(t, ctx, scope, nil, "`${ta.length},${ta.byteLength},${ta.byteOffset}`")
		if !ok {
			t.Fatalf("%s: geometry eval failed", kind)
		}
		wantJS := "3," + taItoa(3*size) + "," + taItoa(size)
		if js != wantJS {
			t.Errorf("%s: js geometry = %q, want %q", kind, js, wantJS)
		}

		// Zero-length views at the start and at the exact end are legal.
		for _, geo := range [][2]int{{0, 0}, {32, 0}} {
			if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, kind, geo[0], geo[1]); err != nil {
				t.Errorf("%s: zero-length view at %d: %v", kind, geo[0], err)
			}
		}
	}
}

func taItoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for v > 0 {
		pos--
		digits[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(digits[pos:])
}

// --- bit patterns ----------------------------------------------------------------

func TestTypedArrayReadBitPatterns(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	cases := []struct {
		kind    gov8.TypedArrayKind
		bytes   []byte
		viewLen int
		want    string
	}{
		{gov8.KindInt8, []byte{0x80, 0x7F, 0xFF, 0x00}, 4, "-128,127,-1,0"},
		{gov8.KindUint8, []byte{0x80, 0x7F, 0xFF, 0x00}, 4, "128,127,255,0"},
		{gov8.KindUint8Clamped, []byte{0x80, 0x7F, 0xFF, 0x00}, 4, "128,127,255,0"},
		{gov8.KindInt16, []byte{0x00, 0x80, 0xFF, 0x7F, 0xFF, 0xFF, 0x00, 0x00}, 4, "-32768,32767,-1,0"},
		{gov8.KindUint16, []byte{0x00, 0x80, 0xFF, 0x7F, 0xFF, 0xFF, 0x00, 0x00}, 4, "32768,32767,65535,0"},
		{gov8.KindInt32, []byte{0x00, 0x00, 0x00, 0x80, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF}, 4, "-2147483648,2147483647,-1,0"},
		{gov8.KindUint32, []byte{0x00, 0x00, 0x00, 0x80, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF}, 4, "2147483648,2147483647,4294967295,0"},
		// IEEE half: 0x3C00=1.0, 0x3800=0.5, 0xC000=-2.0, 0x7BFF=65504.
		{gov8.KindFloat16, []byte{0x00, 0x3C, 0x00, 0x38, 0x00, 0xC0, 0xFF, 0x7B}, 4, "1,0.5,-2,65504"},
		{gov8.KindFloat32, []byte{0x00, 0x00, 0x80, 0x3F, 0x00, 0x00, 0x20, 0xC0, 0x00, 0x00, 0x00, 0x3F}, 4, "1,-2.5,0.5,0"},
		{gov8.KindFloat64, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF8, 0x3F, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xE0, 0xBF}, 2, "1.5,-0.5"},
		{gov8.KindBigInt64, []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 2, "1,-1"},
		{gov8.KindBigUint64, []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 2, "1,18446744073709551615"},
	}

	for _, c := range cases {
		ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
		if err != nil {
			t.Fatalf("NewArrayBuffer: %v", err)
		}
		bs, err := ab.GetBackingStore()
		if err != nil {
			t.Fatalf("GetBackingStore: %v", err)
		}
		if _, err := bs.WriteAt(c.bytes, 0); err != nil {
			t.Fatalf("WriteAt: %v", err)
		}
		if err := bs.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		view, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, c.kind, 0, c.viewLen)
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		taSetGlobal(t, ctx, scope, "ta", view.Value)
		got, ok := evalTextValue(t, ctx, scope, nil, "String(Array.from(ta))")
		if !ok {
			t.Fatalf("%s: read eval failed", c.kind)
		}
		if got != c.want {
			t.Errorf("%s: js read = %q, want %q", c.kind, got, c.want)
		}
	}
}

func TestTypedArrayWriteBitPatterns(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	cases := []struct {
		kind           gov8.TypedArrayKind
		script         string
		expectedPrefix []byte
	}{
		{gov8.KindInt8, "w[0]=-129;w[1]=128;w[2]=255;", []byte{0x7F, 0x80, 0xFF}},
		{gov8.KindUint8, "w[0]=256;w[1]=-1;", []byte{0x00, 0xFF}},
		{gov8.KindUint8Clamped, "w[0]=300;w[1]=-1;w[2]=1.5;w[3]=2.5;w[4]=0.5;", []byte{255, 0, 2, 2, 0}},
		{gov8.KindInt16, "w[0]=-32769;w[1]=32768;", []byte{0xFF, 0x7F, 0x00, 0x80}},
		{gov8.KindUint16, "w[0]=65536;w[1]=-1;", []byte{0x00, 0x00, 0xFF, 0xFF}},
		{gov8.KindInt32, "w[0]=-2147483649;w[1]=2147483648;", []byte{0xFF, 0xFF, 0xFF, 0x7F, 0x00, 0x00, 0x00, 0x80}},
		{gov8.KindUint32, "w[0]=4294967296;w[1]=-1;", []byte{0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF}},
		{gov8.KindFloat16, "w[0]=1.5;w[1]=-2;w[2]=0.5;", []byte{0x00, 0x3E, 0x00, 0xC0, 0x00, 0x38}},
		{gov8.KindFloat32, "w[0]=1e50;w[1]=0.1;", []byte{0x00, 0x00, 0x80, 0x7F, 0xCD, 0xCC, 0xCC, 0x3D}},
		{gov8.KindFloat64, "w[0]=1.5;w[1]=-0.5;", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF8, 0x3F, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xE0, 0xBF}},
		{gov8.KindBigInt64, "w[0]=9223372036854775808n;w[1]=-9223372036854775809n;", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F}},
		{gov8.KindBigUint64, "w[0]=-1n;w[1]=18446744073709551616n;", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
	}

	for _, c := range cases {
		viewLen := 16 / c.kind.ElementSize()
		ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
		if err != nil {
			t.Fatalf("NewArrayBuffer: %v", err)
		}
		view, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, c.kind, 0, viewLen)
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		taSetGlobal(t, ctx, scope, "w", view.Value)
		if _, ok := evalValue(t, ctx, scope, nil, c.script); !ok {
			t.Fatalf("%s: write eval %q failed", c.kind, c.script)
		}
		got := make([]byte, 16)
		n, err := view.CopyContents(got)
		if err != nil || n != 16 {
			t.Fatalf("%s: CopyContents = %d, %v", c.kind, n, err)
		}
		want := make([]byte, 16)
		copy(want, c.expectedPrefix)
		if string(got) != string(want) {
			t.Errorf("%s: readback = %s, want %s", c.kind, hexEncode(got), hexEncode(want))
		}
	}
}

// --- view surface -----------------------------------------------------------------

func TestTypedArrayViewSurface(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	view, err := gov8.NewUint8Array(scope, ctx, ab, 3, 5)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}

	// Seed bytes 1..5 at offset 3.
	bs, err := ab.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	if _, err := bs.WriteAt([]byte{1, 2, 3, 4, 5}, 3); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	bufferVal, err := view.Buffer()
	if err != nil {
		t.Fatalf("Buffer: %v", err)
	}
	if same, err := gov8.Same(bufferVal.Value, ab.Value); err != nil || !same {
		t.Errorf("buffer identity = %v, %v", same, err)
	}
	if has, _ := view.HasBuffer(); !has {
		t.Error("HasBuffer = false")
	}

	storeViaView, err := view.GetBackingStore()
	if err != nil {
		t.Fatalf("view GetBackingStore: %v", err)
	}
	if n, err := storeViaView.ByteLength(); err != nil || n != 16 {
		t.Errorf("store byte length = %d, %v", n, err)
	}
	if shared, err := storeViaView.IsShared(); err != nil || shared {
		t.Errorf("store is shared = %v, %v", shared, err)
	}

	// JS read of the seeded bytes, then live visibility in both directions.
	taSetGlobal(t, ctx, scope, "ta", view.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "String(ta[0])"); !ok || got != "1" {
		t.Errorf("js read = %q, %v", got, ok)
	}
	if _, err := bs.WriteAt([]byte{42}, 3); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if got, ok := evalTextValue(t, ctx, scope, nil, "String(ta[0])"); !ok || got != "42" {
		t.Errorf("js sees store write = %q, %v", got, ok)
	}
	if _, ok := evalValue(t, ctx, scope, nil, "ta[0] = 7;"); !ok {
		t.Fatal("js write failed")
	}
	after := make([]byte, 1)
	if _, err := bs.ReadAt(after, 3); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if after[0] != 7 {
		t.Errorf("store sees js write = %d", after[0])
	}

	// data() = buffer data + byte offset.
	basePtr, baseSome, err := ab.Data()
	if err != nil || !baseSome {
		t.Fatalf("ab data = %d, %v, %v", basePtr, baseSome, err)
	}
	viewPtr, viewSome, err := view.Data()
	if err != nil || !viewSome {
		t.Fatalf("view data = %d, %v, %v", viewPtr, viewSome, err)
	}
	if viewPtr-basePtr != 3 {
		t.Errorf("data delta = %d, want 3", viewPtr-basePtr)
	}

	// Copies observe the current (JS-written) contents.
	dest := []byte{0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE}
	n, err := view.CopyContents(dest)
	if err != nil || n != 5 {
		t.Fatalf("CopyContents = %d, %v", n, err)
	}
	if hexEncode(dest) != "0702030405eeeeee" {
		t.Errorf("copy bytes = %s", hexEncode(dest))
	}

	// GetContents: live span, storage-size independent, base == data().
	storage := make([]byte, 8)
	contents, err := view.GetContents(storage)
	if err != nil {
		t.Fatalf("GetContents: %v", err)
	}
	if contents.Length != 5 {
		t.Errorf("contents length = %d", contents.Length)
	}
	if hexEncode(storage[:5]) != "0702030405" {
		t.Errorf("contents bytes = %s", hexEncode(storage[:5]))
	}
	if !contents.SourceIsData(viewPtr) {
		t.Error("contents source is not the view data pointer")
	}
	tiny := make([]byte, 1)
	tinyContents, err := view.GetContents(tiny)
	if err != nil {
		t.Fatalf("GetContents(tiny): %v", err)
	}
	if tinyContents.Length != 5 {
		t.Errorf("tiny-storage contents length = %d, want 5 (storage size ignored)", tinyContents.Length)
	}
	if tiny[0] != storage[0] {
		t.Errorf("tiny copy = %#x, want %#x", tiny[0], storage[0])
	}

	// A JS write after the first GetContents is observed by a re-read.
	if _, ok := evalValue(t, ctx, scope, nil, "ta[1] = 99;"); !ok {
		t.Fatal("js write failed")
	}
	re, err := view.GetContents(storage)
	if err != nil {
		t.Fatalf("GetContents(re): %v", err)
	}
	if re.Length != 5 || storage[1] != 99 {
		t.Errorf("live contents not refreshed: len %d, byte %d", re.Length, storage[1])
	}

	if err := storeViaView.Close(); err != nil {
		t.Errorf("store Close: %v", err)
	}
	if err := bs.Close(); err != nil {
		t.Errorf("bs Close: %v", err)
	}
}

// --- detached view -----------------------------------------------------------------

func TestTypedArrayDetachedView(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	view, err := gov8.NewUint8Array(scope, ctx, ab, 3, 5)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	taSetGlobal(t, ctx, scope, "ta", view.Value)

	if ok, err := ab.Detach(ctx, gov8.Value{}); err != nil || !ok {
		t.Fatalf("Detach = %v, %v", ok, err)
	}

	if n, _ := view.Length(); n != 0 {
		t.Errorf("length after detach = %d", n)
	}
	if n, _ := view.ByteLength(); n != 0 {
		t.Errorf("byte length after detach = %d", n)
	}
	// Pinned: byte_offset is pinned to 0 for detached views, not clamped.
	if n, _ := view.ByteOffset(); n != 0 {
		t.Errorf("byte offset after detach = %d", n)
	}
	if has, _ := view.HasBuffer(); !has {
		t.Error("detached view lost its buffer identity")
	}
	if _, some, _ := view.Data(); some {
		t.Error("detached view data pointer is not null")
	}
	dest := []byte{0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE}
	if n, err := view.CopyContents(dest); err != nil || n != 0 {
		t.Errorf("copy after detach = %d, %v", n, err)
	}
	if !allBytes(dest, 0xEE) {
		t.Error("copy after detach touched the destination")
	}
	contents, err := view.GetContents(make([]byte, 8))
	if err != nil || contents.Length != 0 {
		t.Errorf("contents after detach = %d, %v", contents.Length, err)
	}
	if got, _ := evalTextValue(t, ctx, scope, nil, "String(ta.length)"); got != "0" {
		t.Errorf("js length = %q", got)
	}
	if got, _ := evalTextValue(t, ctx, scope, nil, "String(ta[0])"); got != "undefined" {
		t.Errorf("js element = %q", got)
	}
}

func allBytes(data []byte, v byte) bool {
	for _, b := range data {
		if b != v {
			return false
		}
	}
	return true
}

// --- DataView surface ----------------------------------------------------------------

func TestTypedArrayDataViewSurface(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	bs, err := ab.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	seed := make([]byte, 16)
	for i := range seed {
		seed[i] = byte(i*16 + 1)
	}
	if _, err := bs.WriteAt(seed, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	// Odd offsets are legal for DataViews (byte-granular).
	dv, err := gov8.NewDataView(scope, ctx, ab, 3, 9)
	if err != nil {
		t.Fatalf("NewDataView: %v", err)
	}
	if n, _ := dv.ByteOffset(); n != 3 {
		t.Errorf("byte offset = %d", n)
	}
	if n, _ := dv.ByteLength(); n != 9 {
		t.Errorf("byte length = %d", n)
	}

	storage := make([]byte, 16)
	contents, err := dv.GetContents(storage)
	if err != nil {
		t.Fatalf("GetContents: %v", err)
	}
	if contents.Length != 9 {
		t.Errorf("contents length = %d", contents.Length)
	}
	wantPre := make([]byte, 9)
	for i := range wantPre {
		wantPre[i] = byte(((3+i)*16 + 1) % 256)
	}
	if hexEncode(storage[:9]) != hexEncode(wantPre) {
		t.Errorf("pre-write contents = %s, want %s", hexEncode(storage[:9]), hexEncode(wantPre))
	}

	dest := make([]byte, 16)
	if n, err := dv.CopyContents(dest); err != nil || n != 9 {
		t.Fatalf("CopyContents = %d, %v", n, err)
	}
	if hexEncode(dest[:9]) != hexEncode(wantPre) {
		t.Errorf("copy includes offset: %s", hexEncode(dest[:9]))
	}

	// JS get/set (big-endian by default) are seen by GetContents and the store.
	taSetGlobal(t, ctx, scope, "dv", dv.Value)
	if got, _ := evalTextValue(t, ctx, scope, nil, "String(dv.getUint8(0))"); got != "49" {
		t.Errorf("js get = %q", got)
	}
	if _, ok := evalValue(t, ctx, scope, nil, "dv.setUint16(0, 0xBEEF);"); !ok {
		t.Fatal("js set failed")
	}
	post, err := dv.GetContents(storage)
	if err != nil || post.Length != 9 {
		t.Fatalf("post contents = %d, %v", post.Length, err)
	}
	if hexEncode(storage[:9]) != "beef5161718191a1b1" {
		t.Errorf("post-write contents = %s", hexEncode(storage[:9]))
	}
	after := make([]byte, 5)
	if _, err := bs.ReadAt(after, 3); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if hexEncode(after) != "beef516171" {
		t.Errorf("store sees js write = %s", hexEncode(after))
	}
}

// --- SharedArrayBuffer-backed views ------------------------------------------------

func TestTypedArraySABViews(t *testing.T) {
	iso, ctx, scope, cleanup := newManualRuntime(t)
	defer cleanup(t)

	bs, err := iso.NewSharedArrayBufferBackingStore(16)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
	}
	taSetGlobal(t, ctx, scope, "sab", sab.Value)

	v, ok := evalValue(t, ctx, scope, nil, "new Uint16Array(sab, 4, 4)")
	if !ok {
		t.Fatal("eval SAB view failed")
	}
	ta, err := gov8.AsTypedArray(v)
	if err != nil {
		t.Fatalf("AsTypedArray: %v", err)
	}
	if n, _ := ta.Length(); n != 4 {
		t.Errorf("length = %d", n)
	}
	if n, _ := ta.ByteOffset(); n != 4 {
		t.Errorf("byte offset = %d", n)
	}
	if n, _ := ta.ByteLength(); n != 8 {
		t.Errorf("byte length = %d", n)
	}

	// The masquerade: buffer() is a Local<ArrayBuffer> whose value is really
	// a SharedArrayBuffer.
	bufVal, err := ta.Buffer()
	if err != nil {
		t.Fatalf("Buffer: %v", err)
	}
	if is, _ := bufVal.IsSharedArrayBuffer(); !is {
		t.Error("masquerading buffer is not a SharedArrayBuffer")
	}
	if is, _ := bufVal.IsArrayBuffer(); is {
		t.Error("masquerading buffer is a plain ArrayBuffer")
	}
	sabBuf, err := gov8.AsSharedArrayBuffer(bufVal.Value)
	if err != nil {
		t.Fatalf("AsSharedArrayBuffer: %v", err)
	}
	if n, err := sabBuf.ByteLength(); err != nil || n != 16 {
		t.Errorf("masquerade byte length = %d, %v", n, err)
	}

	storeViaView, err := ta.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	if shared, _ := storeViaView.IsShared(); !shared {
		t.Error("SAB view store is not shared")
	}
	if n, _ := storeViaView.ByteLength(); n != 16 {
		t.Errorf("SAB view store byte length = %d", n)
	}
	_ = storeViaView.Close()

	// Byte-level cross-boundary visibility in both directions.
	if _, err := bs.WriteAt([]byte{0x34, 0x12}, 4); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	taSetGlobal(t, ctx, scope, "ta", v)
	if got, _ := evalTextValue(t, ctx, scope, nil, "String(ta[0])"); got != "4660" {
		t.Errorf("js view read = %q", got)
	}
	if _, ok := evalValue(t, ctx, scope, nil, "ta[1] = 48879;"); !ok {
		t.Fatal("js write failed")
	}
	got := make([]byte, 8)
	if n, err := ta.CopyContents(got); err != nil || n != 8 {
		t.Fatalf("CopyContents = %d, %v", n, err)
	}
	if hexEncode(got) != "3412efbe00000000" {
		t.Errorf("rust/go sees js write = %s", hexEncode(got))
	}
}

// --- Float16 -------------------------------------------------------------------------

func TestTypedArrayFloat16(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	view, err := gov8.NewFloat16Array(scope, ctx, ab, 0, 2)
	if err != nil {
		t.Fatalf("NewFloat16Array: %v", err)
	}
	if n, _ := view.Length(); n != 2 {
		t.Errorf("length = %d", n)
	}
	if n, _ := view.ByteLength(); n != 4 {
		t.Errorf("byte length = %d", n)
	}
	if v, ok := evalValue(t, ctx, scope, nil, "new Float16Array(3)"); !ok {
		t.Fatal("js Float16Array constructor missing")
	} else if _, err := gov8.AsTypedArray(v); err != nil {
		t.Errorf("js-created Float16Array: %v", err)
	}
	if got, _ := evalTextValue(t, ctx, scope, nil, "String(Math.f16round(1.5))"); got != "1.5" {
		t.Errorf("Math.f16round(1.5) = %q", got)
	}
	taSetGlobal(t, ctx, scope, "ab", ab.Value)
	if got, _ := evalTextValue(t, ctx, scope, nil,
		"const d = new DataView(ab); d.setFloat16(0, 1.5); String(d.getFloat16(0))"); got != "1.5" {
		t.Errorf("DataView f16 roundtrip = %q", got)
	}
	// Native-built view, JS-written, Go-read.
	taSetGlobal(t, ctx, scope, "h", view.Value)
	if _, ok := evalValue(t, ctx, scope, nil, "h[0] = 1.5;"); !ok {
		t.Fatal("js f16 write failed")
	}
	got := make([]byte, 4)
	if _, err := view.CopyContents(got); err != nil {
		t.Fatalf("CopyContents: %v", err)
	}
	if got[0] != 0x00 || got[1] != 0x3E { // f16(1.5) = 0x3E00 little-endian
		t.Errorf("f16 bytes = %s, want 003e...", hexEncode(got))
	}
}

// --- lifecycle / affinity / concurrency -------------------------------------------------

func TestTypedArrayScopeInvalidation(t *testing.T) {
	_, ctx, scope, cleanup := newManualRuntime(t)
	defer cleanup(t)

	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	view, err := gov8.NewUint8Array(scope, ctx, ab, 0, 8)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("scope Close: %v", err)
	}
	// Every operation on the closed scope's values must fail cleanly (never
	// touch the engine).
	if _, err := view.Length(); err == nil {
		t.Error("Length after scope close must fail")
	}
	if _, _, err := view.Data(); err == nil {
		t.Error("Data after scope close must fail")
	}
	if _, err := view.GetBackingStore(); err == nil {
		t.Error("GetBackingStore after scope close must fail")
	}
	if _, err := view.GetContents(make([]byte, 4)); err == nil {
		t.Error("GetContents after scope close must fail")
	}
	// scope (closed above) and context are closed by the manual-runtime
	// cleanup; the isolate close must still succeed.
}

func TestTypedArrayForeignContext(t *testing.T) {
	_, ctx, scope, cleanup := newManualRuntime(t)
	defer cleanup(t)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}

	// A context of a DIFFERENT isolate is wrapper misuse and must be
	// rejected before touching the engine. (A same-isolate context pairing
	// is engine-coherent: the engine allocates against the context passed
	// in, so it is accepted, matching the existing buffer surface.)
	iso2, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate (second): %v", err)
	}
	ctx2, err := iso2.NewContext()
	if err != nil {
		t.Fatalf("NewContext (second isolate): %v", err)
	}
	if _, err := gov8.NewTypedArrayOfKind(scope, ctx2, ab, gov8.KindUint8, 0, 1); err == nil {
		t.Fatal("foreign-isolate context must be rejected")
	} else if !strings.Contains(err.Error(), "isolate") {
		t.Errorf("foreign context error = %v", err)
	}
	_ = ctx2.Close()
	if err := iso2.Close(); err != nil {
		t.Errorf("iso2 Close: %v", err)
	}

	// The isolate is still healthy afterwards.
	if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindUint8, 0, 1); err != nil {
		t.Errorf("construction after rejection: %v", err)
	}
}

func TestTypedArrayWrongThread(t *testing.T) {
	_, ctx, scope, cleanup := newManualRuntime(t)
	defer cleanup(t)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindUint16, 0, 2)
		errCh <- err
	}()
	err = <-errCh
	if err == nil {
		t.Fatal("constructor from a foreign goroutine must fail")
	}
	if !strings.Contains(err.Error(), "thread affinity") {
		t.Errorf("error = %v, want affinity violation", err)
	}
}

func TestTypedArrayConcurrentIsolates(t *testing.T) {
	const goroutines = 4
	const iterations = 20
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			iso, err := gov8.NewIsolate()
			if err != nil {
				errs[g] = err
				return
			}
			defer func() { _ = iso.Close() }()
			ctx, err := iso.NewContext()
			if err != nil {
				errs[g] = err
				return
			}
			defer func() { _ = ctx.Close() }()
			for i := 0; i < iterations; i++ {
				scope, err := iso.NewScope()
				if err != nil {
					errs[g] = err
					return
				}
				ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
				if err != nil {
					errs[g] = err
					return
				}
				view, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindFloat16, 2, 4)
				if err != nil {
					errs[g] = err
					return
				}
				dst := make([]byte, 8)
				if _, err := view.CopyContents(dst); err != nil {
					errs[g] = err
					return
				}
				if _, err := view.GetContents(dst); err != nil {
					errs[g] = err
					return
				}
				if ok, err := ab.Detach(ctx, gov8.Value{}); err != nil || !ok {
					errs[g] = err
					return
				}
				_ = scope.Close()
			}
		}(g)
	}
	wg.Wait()
	for g, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", g, err)
		}
	}
}
