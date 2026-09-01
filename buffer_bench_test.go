//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Nine buffer/serialization benchmark workloads covering the slice's
// performance-sensitive paths: buffer allocation, backing-store construction
// and copy-in, store reads (the wire to Go path), view construction with
// boundary prevalidation, detach, serializer production (small and bulk),
// and full serialize/deserialize roundtrips.
//
// Harness conventions mirror rust-oracle/benches (see README.md): a fresh
// Scope per iteration stands in for the oracle's fresh nested HandleScope;
// isolate and context are created once per benchmark except where creation
// is itself measured (it is not here). Criterion vs `go test -bench`
// methodology differences (1s warm-up / 3s measurement / 50 samples vs Go's
// defaults) must be accounted for when comparing against oracle numbers.

// 1. ArrayBuffer allocation: NewArrayBuffer(16) plus scope teardown.
func BenchmarkBufferArrayBufferNew16(b *testing.B) {
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
		if _, err := gov8.NewArrayBuffer(scope, ctx, 16); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// 2. Backing-store construction with copy-in and attachment: a 4 KiB Go
// slice becomes an engine-backed store aliased by an ArrayBuffer.
func BenchmarkBufferBackingStoreFromSliceAttach4K(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	data := bytes.Repeat([]byte{7}, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		bs, err := iso.NewBackingStoreFromSlice(data)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
		if err := bs.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// 3. Store read: 4 KiB copied out of a backing store per iteration (the
// engine-to-Go byte path used to drain buffer contents).
func BenchmarkBufferBackingStoreRead4K(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	bs, err := iso.NewBackingStoreFromSlice(bytes.Repeat([]byte{9}, 4096))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = bs.Close() }()
	dst := make([]byte, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n, err := bs.ReadAt(dst, 0); err != nil || n != 4096 {
			b.Fatalf("ReadAt = %d, %v", n, err)
		}
	}
}

// 4. View construction + contents copy: fresh 4 KiB buffer and Uint8Array
// per iteration (including the shim's boundary prevalidation) and
// CopyContents out.
func BenchmarkBufferTypedArrayNewCopy4K(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	dst := make([]byte, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		ab, err := gov8.NewArrayBuffer(scope, ctx, 4096)
		if err != nil {
			b.Fatal(err)
		}
		ta, err := gov8.NewUint8Array(scope, ctx, ab, 0, 4096)
		if err != nil {
			b.Fatal(err)
		}
		if n, err := ta.CopyContents(dst); err != nil || n != 4096 {
			b.Fatalf("CopyContents = %d, %v", n, err)
		}
		_ = scope.Close()
	}
}

// 5. Detach: allocate, detach, and observe the detached state per iteration.
func BenchmarkBufferDetachAndObserve(b *testing.B) {
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
		if ok, err := ab.Detach(ctx, gov8.Value{}); err != nil || !ok {
			b.Fatalf("Detach = %v, %v", ok, err)
		}
		if n, _ := ab.ByteLength(); n != 0 {
			b.Fatalf("length after detach = %d", n)
		}
		_ = scope.Close()
	}
}

// 6. Serialize a primitive: full serializer lifecycle (construct, write,
// release, destroy) per iteration -- the small-payload wire path.
func BenchmarkBufferSerializePrimitive(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	v, ok := evalValueB(b, ctx, scope, "true")
	if !ok {
		b.Fatal("eval true failed")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ser, err := gov8.NewValueSerializer(scope, ctx, reportingDelegate{})
		if err != nil {
			b.Fatal(err)
		}
		if ok, err := ser.WriteValue(ctx, v, nil); err != nil || !ok {
			b.Fatalf("WriteValue = %v, %v", ok, err)
		}
		wire, err := ser.Release()
		if err != nil {
			b.Fatal(err)
		}
		if len(wire) != 1 {
			b.Fatalf("wire = %d bytes", len(wire))
		}
		if err := ser.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// 7. Serialize an ArrayBuffer with 4 KiB contents: the bulk copy-in path
// through the structured-clone writer, including Release.
func BenchmarkBufferSerializeArrayBuffer4K(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	bs, err := iso.NewBackingStoreFromSlice(bytes.Repeat([]byte{3}, 4096))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = bs.Close() }()
	ab, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ser, err := gov8.NewValueSerializer(scope, ctx, reportingDelegate{})
		if err != nil {
			b.Fatal(err)
		}
		if ok, err := ser.WriteValue(ctx, ab.Value, nil); err != nil || !ok {
			b.Fatalf("WriteValue = %v, %v", ok, err)
		}
		wire, err := ser.Release()
		if err != nil {
			b.Fatal(err)
		}
		if len(wire) != 4099 { // 1 tag + 2-byte varint(4096) + 4096 bytes
			b.Fatalf("wire = %d bytes", len(wire))
		}
		if err := ser.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// 8. Full object roundtrip: serialize {a:1,b:"x"} and deserialize it back,
// including both wrapper lifecycles -- the structured-clone steady state.
func BenchmarkBufferObjectRoundtrip(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	src, ok := evalValueB(b, ctx, scope, "({a: 1, b: \"x\"})")
	if !ok {
		b.Fatal("eval failed")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ser, err := gov8.NewValueSerializer(scope, ctx, reportingDelegate{})
		if err != nil {
			b.Fatal(err)
		}
		if ok, err := ser.WriteValue(ctx, src, nil); err != nil || !ok {
			b.Fatalf("WriteValue = %v, %v", ok, err)
		}
		wire, err := ser.Release()
		if err != nil {
			b.Fatal(err)
		}
		if err := ser.Close(); err != nil {
			b.Fatal(err)
		}
		vd, err := gov8.NewValueDeserializer(scope, ctx, wire)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := vd.ReadValue(ctx, nil); err != nil {
			b.Fatal(err)
		}
		if err := vd.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// 9. Deserialize a prebuilt 4 KiB ArrayBuffer wire: the bulk read path of
// the deserializer, including its input-retention lifecycle.
func BenchmarkBufferDeserializeArrayBufferWire4K(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	bs, err := iso.NewBackingStoreFromSlice(bytes.Repeat([]byte{5}, 4096))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = bs.Close() }()
	ab, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		b.Fatal(err)
	}
	ser, err := gov8.NewValueSerializer(scope, ctx, reportingDelegate{})
	if err != nil {
		b.Fatal(err)
	}
	if ok, err := ser.WriteValue(ctx, ab.Value, nil); err != nil || !ok {
		b.Fatalf("WriteValue = %v, %v", ok, err)
	}
	wire, err := ser.Release()
	if err != nil {
		b.Fatal(err)
	}
	if err := ser.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vd, err := gov8.NewValueDeserializer(scope, ctx, wire)
		if err != nil {
			b.Fatal(err)
		}
		back, err := vd.ReadValue(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		backAB, err := gov8.AsArrayBuffer(back)
		if err != nil {
			b.Fatal(err)
		}
		if n, _ := backAB.ByteLength(); n != 4096 {
			b.Fatalf("byte_length = %d", n)
		}
		if err := vd.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// --- helpers -----------------------------------------------------------------

// evalValueB is evalValue for benchmarks (no cleanup churn).
func evalValueB(b *testing.B, ctx *gov8.Context, scope *gov8.Scope, source string) (gov8.Value, bool) {
	b.Helper()
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, err := script.Run(scope, nil)
	if err != nil {
		return gov8.Value{}, false
	}
	return v, true
}
