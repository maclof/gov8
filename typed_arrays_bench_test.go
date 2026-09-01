//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Six typed-array benchmark workloads mirroring the oracle's benchmark
// specification (rust-oracle/src/bin/conformance-typed-arrays.rs module docs;
// the benches/typed_array.rs file is pending upstream in the oracle):
//
//   - typedarray/new_buffer_and_view
//   - typedarray/new_view_only
//   - typedarray/copy_contents_256
//   - typedarray/host_element_roundtrip
//   - typedarray/js_element_roundtrip
//   - typedarray/sab_view_copy_contents
//
// Harness conventions mirror rust-oracle/benches: a fresh Scope per iteration
// stands in for the oracle's fresh nested HandleScope; isolate and context
// are created once per benchmark except where creation is itself measured
// (it is not here). Criterion vs `go test -bench` methodology differences
// (1s warm-up / 3s measurement / 50 samples vs Go's defaults) must be
// accounted for when comparing against oracle numbers.

// 1. ArrayBuffer allocation + view construction: NewArrayBuffer(64) and
// NewUint8Array(0, 64) per iteration.
func BenchmarkTypedArrayNewBufferAndView(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		ab, err := gov8.NewArrayBuffer(scope, ctx, 64)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := gov8.NewUint8Array(scope, ctx, ab, 0, 64); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// 2. View construction only: a fixed 64-byte ArrayBuffer; NewUint8Array per
// iteration (the shim's boundary prevalidation is included).
func BenchmarkTypedArrayNewViewOnly(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	ab, err := gov8.NewArrayBuffer(scope, ctx, 64)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := gov8.NewUint8Array(scope, ctx, ab, 0, 64); err != nil {
			b.Fatal(err)
		}
	}
}

// 3. Contents copy: CopyContents of a fixed 256-byte Uint8Array per
// iteration (the engine-to-Go byte path).
func BenchmarkTypedArrayCopyContents256(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	ab, err := gov8.NewArrayBuffer(scope, ctx, 256)
	if err != nil {
		b.Fatal(err)
	}
	ta, err := gov8.NewUint8Array(scope, ctx, ab, 0, 256)
	if err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n, err := ta.CopyContents(dst); err != nil || n != 256 {
			b.Fatalf("CopyContents = %d, %v", n, err)
		}
	}
}

// 4. Host element roundtrip: a fixed Uint16Array over 256 bytes; per
// iteration write 128 u16 values through the backing store and read them
// back with CopyContents. (The oracle writes element-by-element through
// bs[i].set, a raw memory store; the Go backing-store surface copies through
// the boundary, so one WriteAt of the 256 encoded bytes is the equivalent
// unit of work: same bytes written, one transition.)
func BenchmarkTypedArrayHostElementRoundtrip256(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	ab, err := gov8.NewArrayBuffer(scope, ctx, 256)
	if err != nil {
		b.Fatal(err)
	}
	ta, err := gov8.NewUint16Array(scope, ctx, ab, 0, 128)
	if err != nil {
		b.Fatal(err)
	}
	bs, err := ab.GetBackingStore()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = bs.Close() }()
	enc := make([]byte, 256)
	for i := 0; i < 128; i++ {
		binary.LittleEndian.PutUint16(enc[i*2:], uint16(i))
	}
	dst := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n, err := bs.WriteAt(enc, 0); err != nil || n != 256 {
			b.Fatalf("WriteAt = %d, %v", n, err)
		}
		if n, err := ta.CopyContents(dst); err != nil || n != 256 {
			b.Fatalf("CopyContents = %d, %v", n, err)
		}
	}
}

// 5. JS element roundtrip: a fixed Uint16Array stored in a global; the
// precompiled script `ta[0] = ta[0] + 1; ta[0]` runs per iteration
// (JS element access + number conversion; the script is compiled once and
// run N times, so this measures steady-state execution).
func BenchmarkTypedArrayJSElementRoundtrip(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	ab, err := gov8.NewArrayBuffer(scope, ctx, 256)
	if err != nil {
		b.Fatal(err)
	}
	ta, err := gov8.NewUint16Array(scope, ctx, ab, 0, 128)
	if err != nil {
		b.Fatal(err)
	}
	taSetGlobal(b, ctx, scope, "ta", ta.Value)
	script, err := ctx.Compile(scope, "ta[0] = ta[0] + 1; ta[0];", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := script.Run(scope, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// 6. SharedArrayBuffer view copy: a fixed Uint16Array over a 256-byte
// SharedArrayBuffer; CopyContents per iteration (the relaxed-atomic copy
// path in ArrayBufferView::CopyContents).
func BenchmarkTypedArraySABViewCopyContents256(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	bs, err := iso.NewSharedArrayBufferBackingStore(256)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = bs.Close() }()
	if _, err := bs.WriteAt(bytes.Repeat([]byte{7}, 256), 0); err != nil {
		b.Fatal(err)
	}
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		b.Fatal(err)
	}
	taSetGlobal(b, ctx, scope, "sab", sab.Value)
	// The pinned crate binds no native SAB view construction (upstream gap),
	// so the fixed view is created from JS in the long-lived setup scope,
	// exactly as the oracle's checks do. The Go-side SAB wrapper is checked
	// once here as setup sanity; the loop only exercises the view.
	if isSAB, err := sab.IsSharedArrayBuffer(); err != nil || !isSAB {
		b.Fatalf("sab predicate = %v, %v", isSAB, err)
	}
	v, ok := evalValueB(b, ctx, scope, "new Uint16Array(sab, 0, 128)")
	if !ok {
		b.Fatal("eval SAB view failed")
	}
	ta, err := gov8.AsTypedArray(v)
	if err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n, err := ta.CopyContents(dst); err != nil || n != 256 {
			b.Fatalf("CopyContents = %d, %v", n, err)
		}
	}
}
