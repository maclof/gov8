//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

// Negative and boundary tests for the typed-array slice: every geometry the
// engine answers with a process-fatal V8 CHECK / ApiCheck
// (rust-oracle/tests/typed_arrays_negative.rs) is prevalidated at the shim
// boundary and becomes a Go error. These tests run IN-PROCESS and must leave
// the isolate healthy: a single one of them reaching the engine fatally would
// abort the whole test binary.

// taShimError reports whether err is a shim-reported error (negative status),
// i.e. a boundary rejection rather than a JS exception or a Go misuse error.
func taShimError(err error) bool {
	se, ok := err.(*gov8.ShimError)
	return ok && se.Code < 0
}

// wantDetail fails the test unless err is a shim error whose detail contains
// fragment.
func wantDetail(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got success", fragment)
	}
	if !taShimError(err) {
		t.Fatalf("error %v is not a shim boundary error", err)
	}
	if !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error %v does not mention %q", err, fragment)
	}
}

// 16-byte buffer for every probe (same prologue as the Rust child probes).
func taProbeBuffer(t *testing.T, ctx *gov8.Context, scope *gov8.Scope) *gov8.ArrayBuffer {
	t.Helper()
	ab, err := gov8.NewArrayBuffer(scope, ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	return ab
}

func TestTypedArrayPrevalidatesAlignment(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab := taProbeBuffer(t, ctx, scope)

	// byte_offset % element_size != 0 is a fatal engine CHECK
	// ("0 == byte_offset % element_size") for every kind with element size
	// greater than 1.
	cases := []struct {
		kind gov8.TypedArrayKind
		off  int
		len  int
	}{
		{gov8.KindInt16, 3, 2},
		{gov8.KindUint16, 1, 4},
		{gov8.KindInt32, 2, 1},
		{gov8.KindUint32, 3, 1},
		{gov8.KindFloat32, 2, 1},
		{gov8.KindFloat64, 4, 1},
		{gov8.KindBigInt64, 1, 1},
		{gov8.KindBigUint64, 4, 1},
		{gov8.KindFloat16, 3, 1}, // Float16 follows the 2-byte alignment class
	}
	for _, c := range cases {
		_, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, c.kind, c.off, c.len)
		wantDetail(t, err, "byte_offset not aligned to element size")
	}

	// Zero-length views are NOT exempt from the alignment CHECK.
	_, err := gov8.NewInt16Array(scope, ctx, ab, 1, 0)
	wantDetail(t, err, "byte_offset not aligned to element size")

	// 1-byte kinds have no alignment constraint: offset 3 is legal.
	if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindUint8, 3, 2); err != nil {
		t.Errorf("1-byte kind at odd offset: %v", err)
	}
}

func TestTypedArrayPrevalidatesBounds(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab := taProbeBuffer(t, ctx, scope)

	// offset > byte_length (even with length 0) is fatal
	// ("byte_offset <= buffer->GetByteLength()").
	_, err := gov8.NewUint8Array(scope, ctx, ab, 17, 0)
	wantDetail(t, err, "typed array view out of bounds")

	// length * element_size > byte_length is fatal
	// ("byte_length <= buffer->GetByteLength()").
	_, err = gov8.NewInt32Array(scope, ctx, ab, 0, 5) // 5 * 4 = 20 > 16
	wantDetail(t, err, "typed array view out of bounds")

	// offset + span > byte_length is fatal too.
	_, err = gov8.NewFloat64Array(scope, ctx, ab, 8, 2) // 8 + 16 = 24 > 16
	wantDetail(t, err, "typed array view out of bounds")

	// The exact end is still legal (zero-length, offset == byte_length).
	if _, err := gov8.NewUint8Array(scope, ctx, ab, 16, 0); err != nil {
		t.Errorf("zero-length view at the exact end: %v", err)
	}
}

func TestTypedArrayPrevalidatesMaxLength(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab := taProbeBuffer(t, ctx, scope)

	// length > X::MAX_LENGTH hits the engine's ApiCheck
	// ("length exceeds max allowed value") before the factory CHECKs, so the
	// prevalidation must reject it with that detail — even when the geometry
	// is also misaligned or out of bounds (the engine's fatal order).
	_, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindFloat64, 0, 1<<62)
	wantDetail(t, err, "length exceeds max allowed value")
	_, err = gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindInt16, 1, 1<<62)
	wantDetail(t, err, "length exceeds max allowed value")

	// Per-kind max: a length one above the kind's max is rejected, the max
	// itself passes the prevalidation (and is rejected only by the bounds
	// check on this small buffer).
	limits, err := gov8.TypedArrayKindLimitsQuery()
	if err != nil {
		t.Fatalf("TypedArrayKindLimitsQuery: %v", err)
	}
	max := limits.MaxLengths[gov8.KindFloat16]
	_, err = gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindFloat16, 0, int(max)+1)
	wantDetail(t, err, "length exceeds max allowed value")
	_, err = gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindFloat16, 0, int(max))
	wantDetail(t, err, "typed array view out of bounds")
}

func TestTypedArrayNegativeGeometry(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab := taProbeBuffer(t, ctx, scope)

	// Negative geometry is rejected in the wrapper before any shim call.
	if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindUint8, -1, 0); err == nil {
		t.Error("negative byte offset must fail")
	}
	if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindUint8, 0, -1); err == nil {
		t.Error("negative length must fail")
	}

	// Unknown kinds are rejected without touching the engine.
	if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.TypedArrayKind(-1), 0, 1); err == nil {
		t.Error("unknown kind must fail")
	}
	if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.TypedArrayKind(12), 0, 1); err == nil {
		t.Error("unknown kind must fail")
	}

	// Nil buffers and nil context are misuse, not engine work.
	if _, err := gov8.NewTypedArrayOfKind(scope, ctx, nil, gov8.KindUint8, 0, 1); err == nil {
		t.Error("nil ArrayBuffer must fail")
	}
}

func TestDataViewPrevalidatesBounds(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab := taProbeBuffer(t, ctx, scope)

	// DataView has no alignment rule but the same bounds CHECKs.
	_, err := gov8.NewDataView(scope, ctx, ab, 17, 0)
	wantDetail(t, err, "data view out of bounds")
	_, err = gov8.NewDataView(scope, ctx, ab, 0, 100)
	wantDetail(t, err, "data view out of bounds")
	_, err = gov8.NewDataView(scope, ctx, ab, 2, 15)
	wantDetail(t, err, "data view out of bounds")

	// Odd offsets and odd lengths are legal (byte-granular).
	if _, err := gov8.NewDataView(scope, ctx, ab, 3, 9); err != nil {
		t.Errorf("odd DataView geometry: %v", err)
	}
}

func TestIsolateHealthyAfterPrevalidation(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	ab := taProbeBuffer(t, ctx, scope)

	// Hammer every fatal boundary in sequence; the process must survive with
	// a working isolate.
	for i := 0; i < 8; i++ {
		if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindInt32, 2, 1); err == nil {
			t.Fatal("misaligned probe unexpectedly succeeded")
		}
		if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindUint8, 17, 0); err == nil {
			t.Fatal("bounds probe unexpectedly succeeded")
		}
		if _, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindFloat64, 0, 1<<62); err == nil {
			t.Fatal("max probe unexpectedly succeeded")
		}
		if _, err := gov8.NewDataView(scope, ctx, ab, 17, 0); err == nil {
			t.Fatal("DataView probe unexpectedly succeeded")
		}
	}

	// The isolate is fully usable afterwards.
	view, err := gov8.NewTypedArrayOfKind(scope, ctx, ab, gov8.KindBigUint64, 8, 1)
	if err != nil {
		t.Fatalf("valid construction after probes: %v", err)
	}
	if n, _ := view.Length(); n != 1 {
		t.Errorf("length = %d", n)
	}
	taSetGlobal(t, ctx, scope, "ab", ab.Value)
	if got, ok := evalTextValue(t, ctx, scope, nil, "String(new BigUint64Array(ab)[0])"); !ok || got != "0" {
		t.Errorf("js readback = %q, %v", got, ok)
	}
}
