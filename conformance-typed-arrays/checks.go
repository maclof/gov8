//go:build windows && amd64

// The 14 typed-array conformance checks, in the fixed oracle order
// (rust-oracle/src/bin/conformance-typed-arrays.rs CHECKS). Order is part of
// the observable contract: the fixture is ordered.
package main

import (
	"strconv"

	gov8 "gov8"
)

// typedArrayKey is the fixture's JSON key for a kind.
func typedArrayKey(k gov8.TypedArrayKind) string {
	switch k {
	case gov8.KindInt8:
		return "int8"
	case gov8.KindUint8:
		return "uint8"
	case gov8.KindUint8Clamped:
		return "uint8_clamped"
	case gov8.KindInt16:
		return "int16"
	case gov8.KindUint16:
		return "uint16"
	case gov8.KindInt32:
		return "int32"
	case gov8.KindUint32:
		return "uint32"
	case gov8.KindFloat16:
		return "float16"
	case gov8.KindFloat32:
		return "float32"
	case gov8.KindFloat64:
		return "float64"
	case gov8.KindBigInt64:
		return "bigint64"
	case gov8.KindBigUint64:
		return "biguint64"
	}
	return "unknown"
}

// mustConstruct builds a native view, failing the check on engine error.
func mustConstruct(t tester, r *runtime, ab *gov8.ArrayBuffer, kind gov8.TypedArrayKind, off, length int) *gov8.TypedArray {
	t.Helper()
	view, err := gov8.NewTypedArrayOfKind(r.scope, r.ctx, ab, kind, off, length)
	if err != nil {
		t.Fatalf("NewTypedArrayOfKind(%s, %d, %d): %v", kind, off, length, err)
	}
	return view
}

// tpred is a curried predicate adapter: tpred(t)(g()) keeps the multi-value
// engine call in sole-argument position so Go's f(g()) spread rule applies.
func tpred(t tester) func(bool, error) bool {
	t.Helper()
	return func(bv bool, err error) bool {
		if err != nil {
			t.Fatalf("predicate: %v", err)
		}
		return bv
	}
}

// --- 1. kind predicates ------------------------------------------------------

// Predicate/tag matrix over JS-created instances of all 12 kinds plus the
// DataView and ArrayBuffer contrast rows.
func checkKindPredicates(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	var actual, expected []jsonPair
	for _, kind := range gov8.TypedArrayKinds {
		ctor := kind.String()
		value, ok := r.eval(t, "new "+ctor+"(4)")
		if !ok {
			t.Fatalf("eval new %s(4) failed", ctor)
		}
		specific, err := kind.IsTypedArrayOfKind(value)
		if err != nil {
			t.Fatalf("IsTypedArrayOfKind(%s): %v", ctor, err)
		}
		actual = append(actual, kv(typedArrayKey(kind), obj(
			kv("is_typed_array", b(tpred(t)(value.IsTypedArray()))),
			kv("is_array_buffer_view", b(tpred(t)(value.IsArrayBufferView()))),
			kv("is_data_view", b(tpred(t)(value.IsDataView()))),
			kv("is_shared_array_buffer", b(tpred(t)(value.IsSharedArrayBuffer()))),
			kv("specific_predicate", b(specific)),
			kv("ctor_name", s(r.evalText(t, "(new "+ctor+"(4)).constructor.name"))),
			kv("bytes_per_element", s(r.evalText(t, "String("+ctor+".BYTES_PER_ELEMENT)"))),
			kv("type_of", s(evalTypeOfText(t, r, value))),
			kv("type_repr", s(typeRepr(value))),
		)))
		// The pinned upstream quirk: the crate's type_repr chain has no
		// is_float16_array branch, so a Float16Array reports the generic
		// "TypedArray" tag while every other kind reports its own name.
		typeReprTag := ctor
		if kind == gov8.KindFloat16 {
			typeReprTag = "TypedArray"
		}
		expected = append(expected, kv(typedArrayKey(kind), obj(
			kv("is_typed_array", b(true)),
			kv("is_array_buffer_view", b(true)),
			kv("is_data_view", b(false)),
			kv("is_shared_array_buffer", b(false)),
			kv("specific_predicate", b(true)),
			kv("ctor_name", s(ctor)),
			kv("bytes_per_element", s(strconv.FormatInt(int64(kind.ElementSize()), 10))),
			kv("type_of", s("object")),
			kv("type_repr", s(typeReprTag)),
		)))
	}

	// Contrast rows: a DataView is a view but not a typed array; an
	// ArrayBuffer is neither.
	dv, ok := r.eval(t, "new DataView(new ArrayBuffer(8))")
	if !ok {
		t.Fatal("eval DataView failed")
	}
	actual = append(actual, kv("data_view", obj(
		kv("is_typed_array", b(tpred(t)(dv.IsTypedArray()))),
		kv("is_array_buffer_view", b(tpred(t)(dv.IsArrayBufferView()))),
		kv("is_data_view", b(tpred(t)(dv.IsDataView()))),
		kv("ctor_name", s("DataView")),
		kv("type_of", s(evalTypeOfText(t, r, dv))),
		kv("type_repr", s(typeRepr(dv))),
	)))
	expected = append(expected, kv("data_view", obj(
		kv("is_typed_array", b(false)),
		kv("is_array_buffer_view", b(true)),
		kv("is_data_view", b(true)),
		kv("ctor_name", s("DataView")),
		kv("type_of", s("object")),
		kv("type_repr", s("DataView")),
	)))

	abVal, ok := r.eval(t, "new ArrayBuffer(8)")
	if !ok {
		t.Fatal("eval ArrayBuffer failed")
	}
	actual = append(actual, kv("array_buffer", obj(
		kv("is_typed_array", b(tpred(t)(abVal.IsTypedArray()))),
		kv("is_array_buffer_view", b(tpred(t)(abVal.IsArrayBufferView()))),
		kv("is_data_view", b(tpred(t)(abVal.IsDataView()))),
		kv("type_repr", s(typeRepr(abVal))),
	)))
	expected = append(expected, kv("array_buffer", obj(
		kv("is_typed_array", b(false)),
		kv("is_array_buffer_view", b(false)),
		kv("is_data_view", b(false)),
		kv("type_repr", s("ArrayBuffer")),
	)))

	return wantGot("typedarrays/kind_predicates", obj(expected...), obj(actual...))
}

// --- 2. constants --------------------------------------------------------------

// Pinned size limits: per-kind MAX_LENGTH = TypedArray::MAX_BYTE_LENGTH /
// element size truncated (2^53-1 base, sandbox off), heap threshold 0.
func checkConstants(t tester) obs {
	limits, err := gov8.TypedArrayKindLimitsQuery()
	if err != nil {
		t.Fatalf("TypedArrayKindLimitsQuery: %v", err)
	}
	const max = 9_007_199_254_740_991 // 2^53 - 1
	var actual, expected []jsonPair
	for _, kind := range gov8.TypedArrayKinds {
		actual = append(actual, kv(typedArrayKey(kind), i(limits.MaxLengths[kind])))
		expected = append(expected, kv(typedArrayKey(kind), i(max/int64(kind.ElementSize()))))
	}
	actual = append(actual,
		kv("typed_array_max_byte_length", i(limits.MaxByteLength)),
		kv("typed_array_max_size_in_heap", i(limits.MaxSizeInHeap)))
	expected = append(expected,
		kv("typed_array_max_byte_length", i(max)),
		kv("typed_array_max_size_in_heap", i(0)))

	return wantGot("typedarrays/constants", obj(expected...), obj(actual...))
}

// --- 3. element sizes -----------------------------------------------------------

// Observed element size through 1-element view geometry, cross-checked
// against the constants; all 12 views share one 16-byte buffer.
func checkElementSizes(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	limits, err := gov8.TypedArrayKindLimitsQuery()
	if err != nil {
		t.Fatalf("TypedArrayKindLimitsQuery: %v", err)
	}
	ab := ab16(t, r)

	var actual, expected []jsonPair
	for _, kind := range gov8.TypedArrayKinds {
		view := mustConstruct(t, r, ab, kind, 0, 1)
		byteLength, err := view.ByteLength()
		if err != nil {
			t.Fatalf("ByteLength: %v", err)
		}
		byteOffset, err := view.ByteOffset()
		if err != nil {
			t.Fatalf("ByteOffset: %v", err)
		}
		actual = append(actual, kv(typedArrayKey(kind), obj(
			kv("observed_byte_length", i(int64(byteLength))),
			kv("derived_from_constants", i(limits.MaxByteLength/limits.MaxLengths[kind])),
			kv("byte_offset", i(int64(byteOffset))),
		)))
		expected = append(expected, kv(typedArrayKey(kind), obj(
			kv("observed_byte_length", i(int64(kind.ElementSize()))),
			kv("derived_from_constants", i(int64(kind.ElementSize()))),
			kv("byte_offset", i(0)),
		)))
	}

	return wantGot("typedarrays/element_sizes", obj(expected...), obj(actual...))
}

// --- 4. native geometry ----------------------------------------------------------

// Native construction geometry for aligned in-bounds arguments: a view of 3
// elements at offset = element_size over a 32-byte buffer, plus zero-length
// views at offset 0 and at the exact end (all legal; the out-of-bounds and
// misaligned equivalents are prevalidation errors, see the Go negative tests
// and rust-oracle/tests/typed_arrays_negative.rs).
func checkNativeGeometry(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 32)
	if err != nil {
		t.Fatalf("NewArrayBuffer(32): %v", err)
	}

	var actual, expected []jsonPair
	for _, kind := range gov8.TypedArrayKinds {
		size := kind.ElementSize()
		view := mustConstruct(t, r, ab, kind, size, 3)
		length, err := view.Length()
		if err != nil {
			t.Fatalf("Length: %v", err)
		}
		byteLength, err := view.ByteLength()
		if err != nil {
			t.Fatalf("ByteLength: %v", err)
		}
		byteOffset, err := view.ByteOffset()
		if err != nil {
			t.Fatalf("ByteOffset: %v", err)
		}
		if !r.setGlobal(t, "ta", view.Value) {
			t.Fatal("setGlobal ta failed")
		}
		js := r.evalText(t, "`${ta.length},${ta.byteLength},${ta.byteOffset}`")
		actual = append(actual, kv(typedArrayKey(kind), obj(
			kv("length", i(int64(length))),
			kv("byte_length", i(int64(byteLength))),
			kv("byte_offset", i(int64(byteOffset))),
			kv("js", s(js)),
		)))
		expected = append(expected, kv(typedArrayKey(kind), obj(
			kv("length", i(3)),
			kv("byte_length", i(int64(3*size))),
			kv("byte_offset", i(int64(size))),
			kv("js", s("3,"+strconv.FormatInt(int64(3*size), 10)+","+strconv.FormatInt(int64(size), 10))),
		)))

		// Zero-length views at the start and at the exact end are legal.
		_, startErr := gov8.NewTypedArrayOfKind(r.scope, r.ctx, ab, kind, 0, 0)
		_, endErr := gov8.NewTypedArrayOfKind(r.scope, r.ctx, ab, kind, 32, 0)
		actual = append(actual, kv(typedArrayKey(kind), obj(
			kv("zero_len_at_start_is_some", b(startErr == nil)),
			kv("zero_len_at_end_is_some", b(endErr == nil)),
		)))
		expected = append(expected, kv(typedArrayKey(kind), obj(
			kv("zero_len_at_start_is_some", b(true)),
			kv("zero_len_at_end_is_some", b(true)),
		)))
	}

	return wantGot("typedarrays/native_geometry", obj(expected...), obj(actual...))
}

// --- 5. bit patterns read from JS --------------------------------------------------

// readCase is one Go-written bit pattern read back from JS.
type readCase struct {
	kind       gov8.TypedArrayKind
	bytes      [16]byte
	viewLen    int
	expectedJS string
}

// The oracle's hand-pinned read cases; every value is exactly representable
// in its element type. IEEE half patterns: 0x3C00=1.0, 0x3800=0.5,
// 0xC000=-2.0, 0x7BFF=65504.
func readCases() []readCase {
	one := func(kind gov8.TypedArrayKind, bytes []byte, viewLen int, expected string) readCase {
		var c readCase
		c.kind = kind
		copy(c.bytes[:], bytes)
		c.viewLen = viewLen
		c.expectedJS = expected
		return c
	}
	return []readCase{
		one(gov8.KindInt8, []byte{0x80, 0x7F, 0xFF, 0x00}, 4, "-128,127,-1,0"),
		one(gov8.KindUint8, []byte{0x80, 0x7F, 0xFF, 0x00}, 4, "128,127,255,0"),
		one(gov8.KindUint8Clamped, []byte{0x80, 0x7F, 0xFF, 0x00}, 4, "128,127,255,0"),
		one(gov8.KindInt16, []byte{0x00, 0x80, 0xFF, 0x7F, 0xFF, 0xFF, 0x00, 0x00}, 4, "-32768,32767,-1,0"),
		one(gov8.KindUint16, []byte{0x00, 0x80, 0xFF, 0x7F, 0xFF, 0xFF, 0x00, 0x00}, 4, "32768,32767,65535,0"),
		one(gov8.KindInt32, []byte{0x00, 0x00, 0x00, 0x80, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00}, 4, "-2147483648,2147483647,-1,0"),
		one(gov8.KindUint32, []byte{0x00, 0x00, 0x00, 0x80, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00}, 4, "2147483648,2147483647,4294967295,0"),
		one(gov8.KindFloat16, []byte{0x00, 0x3C, 0x00, 0x38, 0x00, 0xC0, 0xFF, 0x7B, 0, 0, 0, 0, 0, 0, 0, 0}, 4, "1,0.5,-2,65504"),
		one(gov8.KindFloat32, []byte{0x00, 0x00, 0x80, 0x3F, 0x00, 0x00, 0x20, 0xC0, 0x00, 0x00, 0x00, 0x3F, 0x00, 0x00, 0x00, 0x00}, 4, "1,-2.5,0.5,0"),
		one(gov8.KindFloat64, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF8, 0x3F, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xE0, 0xBF}, 2, "1.5,-0.5"),
		one(gov8.KindBigInt64, []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 2, "1,-1"),
		one(gov8.KindBigUint64, []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 2, "1,18446744073709551615"),
	}
}

// Go-written bit patterns read from JS through each element type.
func checkReadBitPatterns(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	var actual, expected []jsonPair
	for _, c := range readCases() {
		ab := ab16(t, r)
		seedStore(t, r, ab, 0, c.bytes[:])
		view := mustConstruct(t, r, ab, c.kind, 0, c.viewLen)
		if !r.setGlobal(t, "ta", view.Value) {
			t.Fatal("setGlobal ta failed")
		}
		actual = append(actual, kv(typedArrayKey(c.kind), s(r.evalText(t, "String(Array.from(ta))"))))
		expected = append(expected, kv(typedArrayKey(c.kind), s(c.expectedJS)))
	}

	return wantGot("typedarrays/read_bit_patterns", obj(expected...), obj(actual...))
}

// --- 6. bit patterns written from JS -------------------------------------------------

// writeCase is one JS element write read back as bytes through CopyContents.
type writeCase struct {
	kind           gov8.TypedArrayKind
	script         string
	expectedPrefix []byte
}

// The oracle's hand-pinned write cases: ECMAScript conversion semantics
// (modular wrapping, Uint8Clamped clamp with round-half-to-even, float
// overflow to Infinity, BigInt64/BigUint64 modular wraparound).
func writeCases() []writeCase {
	return []writeCase{
		{gov8.KindInt8, "w[0]=-129;w[1]=128;w[2]=255;", []byte{0x7F, 0x80, 0xFF}},
		{gov8.KindUint8, "w[0]=256;w[1]=-1;", []byte{0x00, 0xFF}},
		{gov8.KindUint8Clamped, "w[0]=300;w[1]=-1;w[2]=1.5;w[3]=2.5;w[4]=0.5;", []byte{255, 0, 2, 2, 0}},
		{gov8.KindInt16, "w[0]=-32769;w[1]=32768;", []byte{0xFF, 0x7F, 0x00, 0x80}},
		{gov8.KindUint16, "w[0]=65536;w[1]=-1;", []byte{0x00, 0x00, 0xFF, 0xFF}},
		{gov8.KindInt32, "w[0]=-2147483649;w[1]=2147483648;", []byte{0xFF, 0xFF, 0xFF, 0x7F, 0x00, 0x00, 0x00, 0x80}},
		{gov8.KindUint32, "w[0]=4294967296;w[1]=-1;", []byte{0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF}},
		{gov8.KindFloat32, "w[0]=1e50;w[1]=0.1;", []byte{0x00, 0x00, 0x80, 0x7F, 0xCD, 0xCC, 0xCC, 0x3D}},
		{gov8.KindFloat64, "w[0]=1.5;w[1]=-0.5;", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF8, 0x3F, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xE0, 0xBF}},
		// The oracle's write_cases table orders Float16 after Float64 (its
		// position differs from ALL_KINDS here); the fixture key order pins
		// that.
		{gov8.KindFloat16, "w[0]=1.5;w[1]=-2;w[2]=0.5;", []byte{0x00, 0x3E, 0x00, 0xC0, 0x00, 0x38}},
		{gov8.KindBigInt64, "w[0]=9223372036854775808n;w[1]=-9223372036854775809n;", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F}},
		{gov8.KindBigUint64, "w[0]=-1n;w[1]=18446744073709551616n;", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
	}
}

// JS-written values read back as bytes through CopyContents.
func checkWriteBitPatterns(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	var actual, expected []jsonPair
	for _, c := range writeCases() {
		// The view spans the whole 16-byte buffer (16/size elements); that
		// keeps `copied` deterministic per element size.
		viewLen := 16 / c.kind.ElementSize()
		ab := ab16(t, r)
		view := mustConstruct(t, r, ab, c.kind, 0, viewLen)
		if !r.setGlobal(t, "w", view.Value) {
			t.Fatal("setGlobal w failed")
		}
		if _, ok := r.eval(t, c.script); !ok {
			t.Fatalf("eval %q failed", c.script)
		}
		bytes := make([]byte, 16)
		copied, err := view.CopyContents(bytes)
		if err != nil {
			t.Fatalf("CopyContents: %v", err)
		}
		actual = append(actual, kv(typedArrayKey(c.kind), obj(
			kv("copied", i(int64(copied))),
			kv("readback", s(lowerHex(bytes))),
		)))
		want := make([]byte, 16)
		copy(want, c.expectedPrefix)
		expected = append(expected, kv(typedArrayKey(c.kind), obj(
			kv("copied", i(int64(viewLen*c.kind.ElementSize()))),
			kv("readback", s(lowerHex(want))),
		)))
	}

	return wantGot("typedarrays/write_bit_patterns", obj(expected...), obj(actual...))
}

// --- 7. JS-created view geometry --------------------------------------------------

func checkJSViewGeometry(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	ab := ab16(t, r)
	if !r.setGlobal(t, "ab", ab.Value) {
		t.Fatal("setGlobal ab failed")
	}
	if _, ok := r.eval(t,
		"globalThis.base = new Uint16Array(ab, 4, 4); "+
			"globalThis.sub = base.subarray(1, 3); "+
			"globalThis.sliced = base.slice(1, 3); "+
			"globalThis.tracking = new Uint8Array(ab, 8); "+
			"globalThis.tracking_dv = new DataView(ab, 4);"); !ok {
		t.Fatal("eval geometry setup failed")
	}
	fetch := func(expr string) gov8.Value {
		t.Helper()
		v, ok := r.eval(t, expr)
		if !ok {
			t.Fatalf("eval %s failed", expr)
		}
		return v
	}
	base := fetch("base")
	sub := fetch("sub")
	sliced := fetch("sliced")
	tracking := fetch("tracking")
	trackingDv := fetch("tracking_dv")
	own := fetch("new Int8Array(4)")
	if _, ok := r.eval(t, "globalThis.from_iterable = new Int16Array([1, 2, 3]);"); !ok {
		t.Fatal("eval from_iterable failed")
	}
	fromIterable := fetch("from_iterable")

	// geometry is the oracle's geometry closure: normalize one JS-created
	// typed-array view value.
	geometry := func(v gov8.Value, sharesAB bool) jsonValue {
		isTA, _ := v.IsTypedArray()
		view, err := gov8.AsTypedArray(v)
		if err != nil {
			t.Fatalf("value is not a typed array: %v", err)
		}
		bo, _ := view.ByteOffset()
		bl, _ := view.ByteLength()
		l, _ := view.Length()
		shares := false
		if buf, berr := view.Buffer(); berr == nil {
			if same, serr := gov8.Same(buf.Value, ab.Value); serr == nil {
				shares = same
			}
		}
		return obj(
			kv("is_typed_array", b(isTA)),
			kv("byte_offset", i(int64(bo))),
			kv("byte_length", i(int64(bl))),
			kv("length", i(int64(l))),
			kv("shares_ab", b(shares)),
			kv("expected_shares_ab", b(sharesAB)),
		)
	}

	actual := obj(
		kv("base", geometry(base, true)),
		kv("subarray", geometry(sub, true)),
		kv("slice", geometry(sliced, false)),
		kv("length_tracking_ta", geometry(tracking, true)),
		kv("length_tracking_dv", func() jsonValue {
			dvv, err := gov8.AsDataView(trackingDv)
			if err != nil {
				t.Fatalf("tracking_dv not a DataView: %v", err)
			}
			bo, _ := dvv.ByteOffset()
			bl, _ := dvv.ByteLength()
			isTA, _ := trackingDv.IsTypedArray()
			return obj(
				kv("is_typed_array", b(isTA)),
				kv("byte_offset", i(int64(bo))),
				kv("byte_length", i(int64(bl))),
			)
		}()),
		kv("own_buffer_ta", geometry(own, false)),
		kv("from_iterable", geometry(fromIterable, false)),
		kv("from_iterable_elements", s(r.evalText(t, "String(globalThis.from_iterable)"))),
		kv("subarray_buffer_identity_via_js", b(r.evalText(t, "String(sub.buffer === ab)") == "true")),
		kv("slice_buffer_not_ab_via_js", b(r.evalText(t, "String(sliced.buffer === ab)") == "false")),
	)

	expected := obj(
		kv("base", obj(
			kv("is_typed_array", b(true)),
			kv("byte_offset", i(4)),
			kv("byte_length", i(8)),
			kv("length", i(4)),
			kv("shares_ab", b(true)),
			kv("expected_shares_ab", b(true)))),
		kv("subarray", obj(
			kv("is_typed_array", b(true)),
			kv("byte_offset", i(6)),
			kv("byte_length", i(4)),
			kv("length", i(2)),
			kv("shares_ab", b(true)),
			kv("expected_shares_ab", b(true)))),
		kv("slice", obj(
			kv("is_typed_array", b(true)),
			kv("byte_offset", i(0)),
			kv("byte_length", i(4)),
			kv("length", i(2)),
			kv("shares_ab", b(false)),
			kv("expected_shares_ab", b(false)))),
		kv("length_tracking_ta", obj(
			kv("is_typed_array", b(true)),
			kv("byte_offset", i(8)),
			kv("byte_length", i(8)),
			kv("length", i(8)),
			kv("shares_ab", b(true)),
			kv("expected_shares_ab", b(true)))),
		kv("length_tracking_dv", obj(
			kv("is_typed_array", b(false)),
			kv("byte_offset", i(4)),
			kv("byte_length", i(12)))),
		kv("own_buffer_ta", obj(
			kv("is_typed_array", b(true)),
			kv("byte_offset", i(0)),
			kv("byte_length", i(4)),
			kv("length", i(4)),
			kv("shares_ab", b(false)),
			kv("expected_shares_ab", b(false)))),
		kv("from_iterable", obj(
			kv("is_typed_array", b(true)),
			kv("byte_offset", i(0)),
			kv("byte_length", i(6)),
			kv("length", i(3)),
			kv("shares_ab", b(false)),
			kv("expected_shares_ab", b(false)))),
		kv("from_iterable_elements", s("1,2,3")),
		kv("subarray_buffer_identity_via_js", b(true)),
		kv("slice_buffer_not_ab_via_js", b(true)),
	)

	return wantGot("typedarrays/js_view_geometry", expected, actual)
}

// --- 8. view surface ---------------------------------------------------------------

// The ArrayBufferView data/backing-store/copy surface on a natively-built
// Uint8Array at offset 3, length 5. The Go shape of copy_contents_uninit is
// a second CopyContents into a fresh 0xEE-filled destination (Go has no
// MaybeUninit; CopyContents observes the same bytes regardless of
// destination initialization state).
func checkViewSurface(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	ab := ab16(t, r)
	view, err := gov8.NewUint8Array(r.scope, r.ctx, ab, 3, 5)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}

	// Seed the store so every copy below has deterministic content.
	seedStore(t, r, ab, 3, []byte{1, 2, 3, 4, 5})

	bufferVal, err := view.Buffer()
	if err != nil {
		t.Fatalf("Buffer: %v", err)
	}
	bufferIdentity, err := gov8.Same(bufferVal.Value, ab.Value)
	if err != nil {
		t.Fatalf("Same: %v", err)
	}
	storeViaView, err := view.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	storeLen, err := storeViaView.ByteLength()
	if err != nil {
		t.Fatalf("ByteLength: %v", err)
	}
	storeShared, err := storeViaView.IsShared()
	if err != nil {
		t.Fatalf("IsShared: %v", err)
	}
	if err := storeViaView.Close(); err != nil {
		t.Fatalf("store Close: %v", err)
	}
	if !r.setGlobal(t, "ta", view.Value) {
		t.Fatal("setGlobal ta failed")
	}
	jsRead := r.evalText(t, "String(ta[0])")
	seedStore(t, r, ab, 3, []byte{42})
	jsSeesStoreWrite := r.evalText(t, "String(ta[0])")
	if _, ok := r.eval(t, "ta[0] = 7;"); !ok {
		t.Fatal("eval ta[0] = 7 failed")
	}
	storeSeesJSWrite := int64(readStore(t, r, ab, 3, 1)[0])

	basePtr, baseSome, err := ab.Data()
	if err != nil {
		t.Fatalf("ab.Data: %v", err)
	}
	viewPtr, viewSome, err := view.Data()
	if err != nil {
		t.Fatalf("view.Data: %v", err)
	}
	dataDeltaIsByteOffset := baseSome && viewSome && viewPtr-basePtr == 3

	dest := fill(8, 0xEE)
	copied, err := view.CopyContents(dest)
	if err != nil {
		t.Fatalf("CopyContents: %v", err)
	}
	uninitDest := fill(8, 0xEE)
	copiedUninit, err := view.CopyContents(uninitDest)
	if err != nil {
		t.Fatalf("CopyContents(uninit): %v", err)
	}
	uninitMatch := copiedUninit == copied && string(uninitDest[:copiedUninit]) == string(dest[:copied])

	storage := make([]byte, 8)
	contents, err := view.GetContents(storage)
	if err != nil {
		t.Fatalf("GetContents: %v", err)
	}
	viewData, viewDataSome, err := view.Data()
	if err != nil {
		t.Fatalf("view.Data: %v", err)
	}
	contentsLen := contents.Length
	if contentsLen > len(storage) {
		contentsLen = len(storage)
	}
	tiny := make([]byte, 1)
	tinyContents, err := view.GetContents(tiny)
	if err != nil {
		t.Fatalf("GetContents(tiny): %v", err)
	}
	tinyMatches := tinyContents.Length == contents.Length &&
		contents.Length > 0 && tiny[0] == storage[0]

	actual := obj(
		kv("buffer_identity", b(bufferIdentity)),
		kv("has_buffer", b(tpred(t)(view.HasBuffer()))),
		kv("get_backing_store_is_some", b(true)),
		kv("store_byte_length", i(int64(storeLen))),
		kv("store_is_shared", b(storeShared)),
		kv("js_read", s(jsRead)),
		kv("js_sees_store_write", s(jsSeesStoreWrite)),
		kv("store_sees_js_write", i(storeSeesJSWrite)),
		kv("data_delta_is_byte_offset", b(dataDeltaIsByteOffset)),
		kv("copy", obj(
			kv("copied", i(int64(copied))),
			kv("bytes", s(lowerHex(dest))),
			kv("sentinel_tail_intact", b(allAre(dest[5:], 0xEE))),
		)),
		kv("copy_uninit", obj(
			kv("copied", i(int64(copiedUninit))),
			kv("matches_copy_contents", b(uninitMatch)),
		)),
		kv("get_contents", obj(
			kv("len", i(int64(contents.Length))),
			kv("bytes", s(lowerHex(storage[:contentsLen]))),
			kv("ptr_is_data", b(viewDataSome && contents.SourceIsData(viewData))),
			kv("len_with_tiny_storage", i(int64(tinyContents.Length))),
			kv("tiny_storage_matches", b(tinyMatches)),
		)),
	)

	expected := obj(
		kv("buffer_identity", b(true)),
		kv("has_buffer", b(true)),
		kv("get_backing_store_is_some", b(true)),
		kv("store_byte_length", i(16)),
		kv("store_is_shared", b(false)),
		kv("js_read", s("1")),
		kv("js_sees_store_write", s("42")),
		kv("store_sees_js_write", i(7)),
		kv("data_delta_is_byte_offset", b(true)),
		kv("copy", obj(
			kv("copied", i(5)),
			// Snapshot taken AFTER the `ta[0] = 7` JS write above: the first
			// byte is 0x07, not the seeded 0x2A.
			kv("bytes", s("0702030405eeeeee")),
			kv("sentinel_tail_intact", b(true)),
		)),
		kv("copy_uninit", obj(
			kv("copied", i(5)),
			kv("matches_copy_contents", b(true)),
		)),
		kv("get_contents", obj(
			kv("len", i(5)),
			kv("bytes", s("0702030405")),
			kv("ptr_is_data", b(true)),
			// Off-heap views ignore the caller's storage size entirely:
			// GetContents reports a live span over the backing store.
			kv("len_with_tiny_storage", i(5)),
			kv("tiny_storage_matches", b(true)),
		)),
	)

	return wantGot("typedarrays/view_surface", expected, actual)
}

// --- 9. copy_contents bounds --------------------------------------------------------

func checkCopyContentsBounds(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	ab := ab16(t, r)
	seq := make([]byte, 16)
	for i := range seq {
		seq[i] = byte(i)
	}
	seedStore(t, r, ab, 0, seq)

	full := mustConstruct(t, r, ab, gov8.KindUint8, 0, 16)
	tail := mustConstruct(t, r, ab, gov8.KindUint8, 8, 8)
	zero := mustConstruct(t, r, ab, gov8.KindUint8, 16, 0)
	dv, err := gov8.NewDataView(r.scope, r.ctx, ab, 3, 9)
	if err != nil {
		t.Fatalf("NewDataView: %v", err)
	}

	big := fill(24, 0xEE)
	fullCopied, err := full.CopyContents(big)
	if err != nil {
		t.Fatalf("CopyContents: %v", err)
	}
	small := fill(4, 0xEE)
	tailCopied, err := tail.CopyContents(small)
	if err != nil {
		t.Fatalf("CopyContents: %v", err)
	}
	untouched := fill(4, 0xEE)
	zeroCopied, err := zero.CopyContents(untouched)
	if err != nil {
		t.Fatalf("CopyContents: %v", err)
	}
	dvDest := fill(16, 0xEE)
	dvCopied, err := dv.CopyContents(dvDest)
	if err != nil {
		t.Fatalf("DataView CopyContents: %v", err)
	}

	actual := obj(
		kv("dest_larger", obj(
			kv("copied", i(int64(fullCopied))),
			kv("bytes", s(lowerHex(big))))),
		kv("dest_smaller", obj(
			kv("copied", i(int64(tailCopied))),
			kv("bytes", s(lowerHex(small))))),
		kv("zero_len_view", obj(
			kv("copied", i(int64(zeroCopied))),
			kv("bytes", s(lowerHex(untouched))))),
		kv("data_view", obj(
			kv("copied", i(int64(dvCopied))),
			kv("bytes", s(lowerHex(dvDest))))),
	)

	bigExpected := fill(24, 0xEE)
	for i := 0; i < 16; i++ {
		bigExpected[i] = byte(i)
	}
	smallExpected := []byte{8, 9, 10, 11}
	dvExpected := fill(16, 0xEE)
	for i, byteVal := range []byte{3, 4, 5, 6, 7, 8, 9, 10, 11} {
		dvExpected[i] = byteVal
	}

	expected := obj(
		kv("dest_larger", obj(
			kv("copied", i(16)),
			kv("bytes", s(lowerHex(bigExpected))))),
		kv("dest_smaller", obj(
			kv("copied", i(4)),
			kv("bytes", s(lowerHex(smallExpected))))),
		kv("zero_len_view", obj(
			kv("copied", i(0)),
			kv("bytes", s("eeeeeeee")))),
		kv("data_view", obj(
			kv("copied", i(9)),
			kv("bytes", s(lowerHex(dvExpected))))),
	)

	return wantGot("typedarrays/copy_contents_bounds", expected, actual)
}

// --- 10. detached view ----------------------------------------------------------------

// Full view-side detach contract: geometry collapses to zero (byte_offset
// pinned to 0), data() null, buffer identity retained, copies yield 0 bytes,
// and JS indexing yields undefined.
func checkDetachedView(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	ab := ab16(t, r)
	ta, err := gov8.NewUint8Array(r.scope, r.ctx, ab, 3, 5)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	seedStore(t, r, ab, 3, []byte{1, 2, 3, 4, 5})
	if !r.setGlobal(t, "ab", ab.Value) || !r.setGlobal(t, "ta", ta.Value) {
		t.Fatal("setGlobal failed")
	}

	detachOK, err := ab.Detach(r.ctx, gov8.Value{})
	if err != nil {
		t.Fatalf("Detach: %v", err)
	}

	bufferVal, err := ta.Buffer()
	if err != nil {
		t.Fatalf("Buffer: %v", err)
	}
	bufferIdentity, err := gov8.Same(bufferVal.Value, ab.Value)
	if err != nil {
		t.Fatalf("Same: %v", err)
	}
	length, _ := ta.Length()
	byteLength, _ := ta.ByteLength()
	byteOffset, _ := ta.ByteOffset()
	hasBuffer, _ := ta.HasBuffer()
	_, dataSome, _ := ta.Data()

	dest := fill(8, 0xEE)
	copied, err := ta.CopyContents(dest)
	if err != nil {
		t.Fatalf("CopyContents: %v", err)
	}
	contents, err := ta.GetContents(make([]byte, 8))
	if err != nil {
		t.Fatalf("GetContents: %v", err)
	}

	actual := obj(
		kv("detach_ok", b(detachOK)),
		kv("length", i(int64(length))),
		kv("byte_length", i(int64(byteLength))),
		kv("byte_offset", i(int64(byteOffset))),
		kv("has_buffer", b(hasBuffer)),
		kv("buffer_identity", b(bufferIdentity)),
		kv("data_is_null", b(!dataSome)),
		kv("copy", obj(
			kv("copied", i(int64(copied))),
			kv("sentinel_intact", b(allAre(dest, 0xEE))),
		)),
		kv("get_contents_len", i(int64(contents.Length))),
		kv("js_length", s(r.evalText(t, "String(ta.length)"))),
		kv("js_element", s(r.evalText(t, "String(ta[0])"))),
		kv("js_byte_offset", s(r.evalText(t, "String(ta.byteOffset)"))),
		kv("js_byte_length", s(r.evalText(t, "String(ta.byteLength)"))),
	)

	expected := obj(
		kv("detach_ok", b(true)),
		kv("length", i(0)),
		kv("byte_length", i(0)),
		// Pinned: the engine's ArrayBufferView::ByteOffset()/ByteLength()
		// return 0 for detached views rather than the stored geometry.
		kv("byte_offset", i(0)),
		kv("has_buffer", b(true)),
		kv("buffer_identity", b(true)),
		kv("data_is_null", b(true)),
		kv("copy", obj(
			kv("copied", i(0)),
			kv("sentinel_intact", b(true)),
		)),
		kv("get_contents_len", i(0)),
		kv("js_length", s("0")),
		kv("js_element", s("undefined")),
		kv("js_byte_offset", s("0")),
		kv("js_byte_length", s("0")),
	)

	return wantGot("typedarrays/detached_view", expected, actual)
}

// --- 11. DataView surface ---------------------------------------------------------------

func checkDataViewSurface(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	ab := ab16(t, r)
	seed := make([]byte, 16)
	for i := range seed {
		seed[i] = byte((i*16 + 1) % 256)
	}
	seedStore(t, r, ab, 0, seed)

	dv, err := gov8.NewDataView(r.scope, r.ctx, ab, 3, 9)
	if err != nil {
		t.Fatalf("NewDataView: %v", err)
	}
	bufferVal, err := dv.Buffer()
	if err != nil {
		t.Fatalf("Buffer: %v", err)
	}
	bufferIdentity, err := gov8.Same(bufferVal.Value, ab.Value)
	if err != nil {
		t.Fatalf("Same: %v", err)
	}
	byteOffset, _ := dv.ByteOffset()
	byteLength, _ := dv.ByteLength()

	storage := make([]byte, 16)
	contents, err := dv.GetContents(storage)
	if err != nil {
		t.Fatalf("GetContents: %v", err)
	}
	viewData, viewDataSome, err := dv.Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	preLen := contents.Length
	if preLen > len(storage) {
		preLen = len(storage)
	}
	preWrite := lowerHex(storage[:preLen])

	dest := make([]byte, 16)
	copied, err := dv.CopyContents(dest)
	if err != nil {
		t.Fatalf("CopyContents: %v", err)
	}

	if !r.setGlobal(t, "dv", dv.Value) {
		t.Fatal("setGlobal dv failed")
	}
	jsGet := r.evalText(t, "String(dv.getUint8(0))")
	jsSetGet := r.evalText(t, "dv.setUint16(0, 0xBEEF); String(dv.getUint16(0))")
	jsGeometry := r.evalText(t, "`${dv.byteOffset},${dv.byteLength}`")

	// Re-read of the live contents after the JS writes: the same span
	// reflects them (the oracle re-reads its live slice).
	post, err := dv.GetContents(storage)
	if err != nil {
		t.Fatalf("GetContents(post): %v", err)
	}
	postLen := post.Length
	if postLen > len(storage) {
		postLen = len(storage)
	}
	postWrite := lowerHex(storage[:postLen])

	storeSeen := fill(16, 0x00)
	copy(storeSeen, readStore(t, r, ab, 3, 5))

	actual := obj(
		kv("byte_offset", i(int64(byteOffset))),
		kv("byte_length", i(int64(byteLength))),
		kv("buffer_identity", b(bufferIdentity)),
		kv("get_contents", obj(
			kv("len", i(int64(contents.Length))),
			kv("pre_write", s(preWrite)),
			kv("post_write", s(postWrite)),
			kv("ptr_is_data", b(viewDataSome && contents.SourceIsData(viewData))),
		)),
		kv("copy", obj(
			kv("copied", i(int64(copied))),
			kv("bytes", s(lowerHex(dest))),
		)),
		kv("js_get", s(jsGet)),
		kv("js_set_get", s(jsSetGet)),
		kv("js_geometry", s(jsGeometry)),
		kv("store_sees_js_write", s(lowerHex(storeSeen))),
	)

	contentsExpected := make([]byte, 9)
	for i := range contentsExpected {
		contentsExpected[i] = byte(((3+i)*16 + 1) % 256)
	}
	copyExpected := fill(16, 0x00)
	copy(copyExpected, contentsExpected)
	// store[3..8] after `dv.setUint16(0, 0xBEEF)`: DataView set/get default
	// to BIG-endian, so offsets 3,4 hold BE EF, then the seeded 0x51, 0x61,
	// 0x71.
	expected := obj(
		kv("byte_offset", i(3)),
		kv("byte_length", i(9)),
		kv("buffer_identity", b(true)),
		kv("get_contents", obj(
			kv("len", i(9)),
			kv("pre_write", s(lowerHex(contentsExpected))),
			kv("post_write", s("beef5161718191a1b1")),
			kv("ptr_is_data", b(true)),
		)),
		kv("copy", obj(
			kv("copied", i(9)),
			kv("bytes", s(lowerHex(copyExpected))),
		)),
		kv("js_get", s("49")),
		kv("js_set_get", s("48879")),
		kv("js_geometry", s("3,9")),
		kv("store_sees_js_write", s("beef5161710000000000000000000000")),
	)

	return wantGot("typedarrays/data_view_surface", expected, actual)
}

// --- 12. SharedArrayBuffer-backed views --------------------------------------------------

// SharedArrayBuffer-backed views (created from JS; the pinned crate binds no
// native SAB path): geometry, the masquerading buffer() result, shared
// backing-store access, byte-level visibility in both directions, and the JS
// RangeError path for invalid SAB view construction.
func checkSABViews(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	bs, err := r.iso.NewSharedArrayBufferBackingStore(16)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(r.scope, r.ctx, bs)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
	}
	if !r.setGlobal(t, "sab", sab.Value) {
		t.Fatal("setGlobal sab failed")
	}
	viewValue, ok := r.eval(t, "new Uint16Array(sab, 4, 4)")
	if !ok {
		t.Fatal("eval new Uint16Array(sab, 4, 4) failed")
	}
	if !r.setGlobal(t, "ta", viewValue) {
		t.Fatal("setGlobal ta failed")
	}

	ta, err := gov8.AsTypedArray(viewValue)
	if err != nil {
		t.Fatalf("AsTypedArray: %v", err)
	}
	length, _ := ta.Length()
	byteOffset, _ := ta.ByteOffset()
	byteLength, _ := ta.ByteLength()

	// The masquerade: view.buffer() hands back a Local<ArrayBuffer> for a
	// SharedArrayBuffer object. Only the value predicates reveal what it
	// really is (is_shared_array_buffer, and NOT is_array_buffer).
	bufferVal, err := ta.Buffer()
	bufferIsSome := err == nil
	bufIsSAB, _ := bufferVal.IsSharedArrayBuffer()
	bufIsAB, _ := bufferVal.IsArrayBuffer()
	bufLen := int64(0)
	if sabBuf, serr := gov8.AsSharedArrayBuffer(bufferVal.Value); serr == nil {
		if l, lerr := sabBuf.ByteLength(); lerr == nil {
			bufLen = int64(l)
		}
	}
	// The pinned wrapper rule (crate and Go wrapper agree): a non-zero-length
	// buffer reports was_detached=false without consulting the engine; this
	// 16-byte buffer is therefore not detached.
	bufDetached := false

	storeViaView, err := ta.GetBackingStore()
	viewStoreIsSome := err == nil
	viewStoreShared, viewStoreLen := false, int64(0)
	if err == nil {
		viewStoreShared, _ = storeViaView.IsShared()
		if l, lerr := storeViaView.ByteLength(); lerr == nil {
			viewStoreLen = int64(l)
		}
		_ = storeViaView.Close()
	}

	// Go -> JS through the shared store.
	if _, werr := bs.WriteAt([]byte{0x34, 0x12}, 4); werr != nil {
		t.Fatalf("WriteAt: %v", werr)
	}
	jsViewRead := r.evalText(t, "String(ta[0])")
	// Pinned quirk: in this build a SharedArrayBuffer does NOT expose
	// integer-indexed element access from script (`sab[4]` is undefined);
	// reads go through views.
	sabDirectIndex := r.evalText(t, "String(sab[4])")
	jsViewByteRead := r.evalText(t, "String(new Uint8Array(sab)[4])")
	// JS -> Go through the view.
	if _, ok := r.eval(t, "ta[1] = 48879;"); !ok {
		t.Fatal("eval ta[1] = 48879 failed")
	}
	bytes := make([]byte, 8)
	copied, err := ta.CopyContents(bytes)
	if err != nil {
		t.Fatalf("CopyContents: %v", err)
	}

	// JS construction errors over a SAB (RangeError, never a native abort).
	misaligned := r.evalText(t,
		"try { new Float64Array(sab, 4, 1); 'no-error' } "+
			"catch (e) { e.constructor.name + ': ' + e.message }")
	outOfBounds := r.evalText(t,
		"try { new Uint8Array(sab, 0, 100); 'no-error' } "+
			"catch (e) { e.constructor.name + ': ' + e.message }")
	offsetPastEnd := r.evalText(t,
		"try { new Uint8Array(sab, 17, 0); 'no-error' } "+
			"catch (e) { e.constructor.name + ': ' + e.message }")

	actual := obj(
		kv("is_typed_array", b(tpred(t)(viewValue.IsTypedArray()))),
		kv("length", i(int64(length))),
		kv("byte_offset", i(int64(byteOffset))),
		kv("byte_length", i(int64(byteLength))),
		kv("buffer_is_some", b(bufferIsSome)),
		kv("buffer_is_shared_array_buffer", b(bufIsSAB)),
		kv("buffer_is_plain_array_buffer", b(bufIsAB)),
		kv("buffer_byte_length", i(bufLen)),
		kv("buffer_was_detached", b(bufDetached)),
		kv("view_store_is_some", b(viewStoreIsSome)),
		kv("view_store_is_shared", b(viewStoreShared)),
		kv("view_store_byte_length", i(viewStoreLen)),
		kv("js_view_read", s(jsViewRead)),
		kv("sab_direct_index", s(sabDirectIndex)),
		kv("js_view_byte_read", s(jsViewByteRead)),
		kv("rust_sees_js_write", obj(
			kv("copied", i(int64(copied))),
			kv("bytes", s(lowerHex(bytes))),
		)),
		kv("js_misaligned_error", s(misaligned)),
		kv("js_out_of_bounds_error", s(outOfBounds)),
		kv("js_offset_past_end_error", s(offsetPastEnd)),
	)

	expected := obj(
		kv("is_typed_array", b(true)),
		kv("length", i(4)),
		kv("byte_offset", i(4)),
		kv("byte_length", i(8)),
		kv("buffer_is_some", b(true)),
		kv("buffer_is_shared_array_buffer", b(true)),
		kv("buffer_is_plain_array_buffer", b(false)),
		kv("buffer_byte_length", i(16)),
		kv("buffer_was_detached", b(false)),
		kv("view_store_is_some", b(true)),
		kv("view_store_is_shared", b(true)),
		kv("view_store_byte_length", i(16)),
		kv("js_view_read", s("4660")),
		kv("sab_direct_index", s("undefined")),
		kv("js_view_byte_read", s("52")),
		kv("rust_sees_js_write", obj(
			kv("copied", i(8)),
			kv("bytes", s("3412efbe00000000")),
		)),
		kv("js_misaligned_error", s("RangeError: start offset of Float64Array should be a multiple of 8")),
		kv("js_out_of_bounds_error", s("RangeError: Invalid typed array length: 100")),
		kv("js_offset_past_end_error", s("RangeError: Invalid typed array length: 0")),
	)

	return wantGot("typedarrays/sab_views", expected, actual)
}

// --- 13. JS error paths -------------------------------------------------------------

// JS construction errors over a plain ArrayBuffer: deterministic RangeErrors
// with pinned messages. The native equivalents abort in the engine (the Go
// wrapper prevalidates them into errors instead) and are never mapped onto
// these JS-shaped failures.
func checkJSErrorPaths(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	ab := ab16(t, r)
	if !r.setGlobal(t, "ab", ab.Value) {
		t.Fatal("setGlobal ab failed")
	}

	actual := obj(
		kv("misaligned_offset", s(r.probe(t, "new Float64Array(ab, 4, 1)"))),
		kv("misaligned_zero_length", s(r.probe(t, "new Int16Array(ab, 1, 0)"))),
		kv("out_of_bounds_length", s(r.probe(t, "new Uint8Array(ab, 0, 100)"))),
		kv("offset_past_end_zero_length", s(r.probe(t, "new Uint8Array(ab, 17, 0)"))),
		kv("data_view_out_of_bounds", s(r.probe(t, "new DataView(ab, 2, 100)"))),
		// Contrast: byte-granular odd offsets are legal from JS too.
		kv("data_view_odd_offset_ok", s(r.evalText(t, "String(new DataView(ab, 3, 9).byteLength)"))),
	)

	expected := obj(
		kv("misaligned_offset", s("RangeError: start offset of Float64Array should be a multiple of 8")),
		kv("misaligned_zero_length", s("RangeError: start offset of Int16Array should be a multiple of 2")),
		kv("out_of_bounds_length", s("RangeError: Invalid typed array length: 100")),
		// Pinned: an explicit zero length past the end reports the
		// length-based message, not the offset message.
		kv("offset_past_end_zero_length", s("RangeError: Invalid typed array length: 0")),
		kv("data_view_out_of_bounds", s("RangeError: Invalid DataView length 100")),
		kv("data_view_odd_offset_ok", s("9")),
	)

	return wantGot("typedarrays/js_error_paths", expected, actual)
}

// --- 14. Float16 availability --------------------------------------------------------

// Float16Array availability in this build: js_float16array ships on, so the
// native constructor passes its ApiCheck, the JS constructor exists, and the
// f16 helpers work.
func checkFloat16Availability(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)
	ab := ab16(t, r)
	if !r.setGlobal(t, "ab", ab.Value) {
		t.Fatal("setGlobal ab failed")
	}

	native, nativeErr := gov8.NewTypedArrayOfKind(r.scope, r.ctx, ab, gov8.KindFloat16, 0, 2)
	nativeIsSome := nativeErr == nil
	nativeGeometry := jsonValue(jsonNull{})
	if nativeErr == nil {
		length, _ := native.Length()
		byteLength, _ := native.ByteLength()
		byteOffset, _ := native.ByteOffset()
		nativeGeometry = obj(
			kv("length", i(int64(length))),
			kv("byte_length", i(int64(byteLength))),
			kv("byte_offset", i(int64(byteOffset))),
		)
	}

	nativeJSRead := ""
	if nativeErr == nil && r.setGlobal(t, "h", native.Value) {
		nativeJSRead = r.evalText(t, "h[0] = 1.5; String(h[0])")
	}

	actual := obj(
		kv("native_new_is_some", b(nativeIsSome)),
		kv("native_geometry", nativeGeometry),
		kv("typeof_ctor", s(r.evalText(t, "typeof Float16Array"))),
		kv("ctor_name", s(r.evalText(t, "Float16Array.name"))),
		kv("js_built_length", s(r.evalText(t, "String(new Float16Array(3).length)"))),
		kv("f16round_1_5", s(r.evalText(t, "String(Math.f16round(1.5))"))),
		kv("f16round_2", s(r.evalText(t, "String(Math.f16round(2))"))),
		kv("data_view_set_get_float16", s(r.evalText(t,
			"const d = new DataView(ab); d.setFloat16(0, 1.5); String(d.getFloat16(0))"))),
		kv("native_view_js_roundtrip", s(nativeJSRead)),
	)

	expected := obj(
		kv("native_new_is_some", b(true)),
		kv("native_geometry", obj(
			kv("length", i(2)),
			kv("byte_length", i(4)),
			kv("byte_offset", i(0)),
		)),
		kv("typeof_ctor", s("function")),
		kv("ctor_name", s("Float16Array")),
		kv("js_built_length", s("3")),
		kv("f16round_1_5", s("1.5")),
		kv("f16round_2", s("2")),
		kv("data_view_set_get_float16", s("1.5")),
		kv("native_view_js_roundtrip", s("1.5")),
	)

	return wantGot("typedarrays/float16_availability", expected, actual)
}

// --- helpers ---------------------------------------------------------------------------

// evalTypeOfText is the oracle's type_of observation: ECMAScript ToString of
// Value::type_of.
func evalTypeOfText(t tester, r *runtime, v gov8.Value) string {
	t.Helper()
	typeOf, err := v.TypeOf(r.scope)
	if err != nil {
		t.Fatalf("TypeOf: %v", err)
		return ""
	}
	txt, err := typeOf.ToString(r.ctx)
	if err != nil {
		t.Fatalf("type_of ToString: %v", err)
		return ""
	}
	return txt
}

func fill(n int, v byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// allAre reports whether every byte equals v.
func allAre(data []byte, v byte) bool {
	for _, x := range data {
		if x != v {
			return false
		}
	}
	return true
}
