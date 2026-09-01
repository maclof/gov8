//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"testing"

	gov8 "gov8"
)

// Serializer / deserializer behavior tests mirroring the pinned oracle's
// conformance-buffers binary. Byte-exact wire comparisons live in the
// conformance-buffers runner; these tests assert the same observations with
// plain Go assertions plus the delegate and lifetime cases.

func hexToBytes(s string) []byte {
	out := make([]byte, len(s)/2)
	for i := range out {
		hi := hexVal(s[2*i])
		lo := hexVal(s[2*i+1])
		out[i] = hi<<4 | lo
	}
	return out
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func serializeValue(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, tc *gov8.TryCatch, v gov8.Value, header bool) ([]byte, bool, string) {
	t.Helper()
	ser, err := gov8.NewValueSerializer(scope, ctx, reportingDelegate{})
	if err != nil {
		t.Fatalf("NewValueSerializer: %v", err)
	}
	defer func() { _ = ser.Close() }()
	if header {
		if err := ser.WriteHeader(); err != nil {
			t.Fatalf("WriteHeader: %v", err)
		}
	}
	ok, werr := ser.WriteValue(ctx, v, tc)
	if werr != nil {
		t.Logf("WriteValue error: %v", werr)
	}
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	cloneError := ""
	if tc != nil {
		if caught, _ := tc.HasCaught(); caught {
			if msg, merr := tc.MessageText(scope, ctx); merr == nil {
				cloneError = msg
			}
		}
	}
	_ = werr
	return wire, ok, cloneError
}

func TestSerializePrimitiveWires(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	cases := []struct {
		source string
		header bool
		wire   string
	}{
		{"undefined", false, "5f"},
		{"null", false, "30"},
		{"false", false, "46"},
		{"true", false, "54"},
		{"0", false, "4900"},
		{"1", false, "4902"},
		{"-1", false, "4901"},
		{"2.5", false, "4e0000000000000440"},
		{`"abc"`, false, "2203616263"},
		// write_value alone emits no header; the explicit header is version
		// 16 ("ff 10") for this build.
		{"true", true, "ff1054"},
		{`"abc"`, true, "ff102203616263"},
	}
	for _, c := range cases {
		v, ok := evalValue(t, ctx, scope, nil, c.source)
		if !ok {
			t.Fatalf("eval %s failed", c.source)
		}
		wire, ok2, cloneErr := serializeValue(t, ctx, scope, nil, v, c.header)
		if !ok2 {
			t.Errorf("%s: write failed (clone error %q)", c.source, cloneErr)
			continue
		}
		if cloneErr != "" {
			t.Errorf("%s: unexpected clone error %q", c.source, cloneErr)
		}
		if hexEncode(wire) != c.wire {
			t.Errorf("%s: wire = %s, want %s", c.source, hexEncode(wire), c.wire)
		}
	}
}

func TestSerializeObjectRoundtrip(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	src, ok := evalValue(t, ctx, scope, nil, "({a: 1, b: \"x\"})")
	if !ok {
		t.Fatal("eval object failed")
	}
	wire, ok2, _ := serializeValue(t, ctx, scope, nil, src, false)
	if !ok2 {
		t.Fatal("object serialize failed")
	}
	if got := hexEncode(wire); got != "6f22016149022201622201787b02" {
		t.Fatalf("object wire = %s", got)
	}

	vd, err := gov8.NewValueDeserializer(scope, ctx, wire)
	if err != nil {
		t.Fatalf("NewValueDeserializer: %v", err)
	}
	defer func() { _ = vd.Close() }()
	back, err := vd.ReadValue(ctx, nil)
	if err != nil {
		t.Fatalf("ReadValue: %v", err)
	}
	if is, _ := back.IsObject(); !is {
		t.Fatal("roundtrip value is not an object")
	}
	obj, err := gov8.AsObject(back)
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	aVal, found, err := obj.GetByName(scope, ctx, "a")
	if err != nil || !found {
		t.Fatalf("get a: %v %v", found, err)
	}
	if n, ok3, _ := aVal.IntegerValue(ctx); !ok3 || n != 1 {
		t.Errorf("a = %d %v", n, ok3)
	}
	bVal, found, err := obj.GetByName(scope, ctx, "b")
	if err != nil || !found {
		t.Fatalf("get b: %v %v", found, err)
	}
	if s, _ := bVal.ToString(ctx); s != "x" {
		t.Errorf("b = %q", s)
	}
}

func TestVersion16WireRejectedByDeserializer(t *testing.T) {
	const hostObject = "Uncaught Error: Deno deserializer: read_host_object not implemented"
	for _, wire := range []string{"ff1054", "ff102203616263", "ff"} {
		t.Run(wire, func(t *testing.T) {
			iso, ctx, scope := newTestRuntime(t)
			tc, err := newTryCatch(t, iso)
			if err != nil {
				t.Fatalf("NewTryCatch: %v", err)
			}
			defer func() { _ = tc.Close() }()
			vd, err := gov8.NewValueDeserializer(scope, ctx, hexToBytes(wire))
			if err != nil {
				t.Fatalf("NewValueDeserializer: %v", err)
			}
			defer func() { _ = vd.Close() }()
			if _, rerr := vd.ReadValue(ctx, tc); rerr == nil || !gov8.IsException(rerr) {
				t.Fatalf("ReadValue = %v, want an exception", rerr)
			}
			if msg, _ := tc.MessageText(scope, ctx); msg != hostObject {
				t.Errorf("message = %q, want %q", msg, hostObject)
			}
		})
	}
}

func TestDeserializeInvalidInputs(t *testing.T) {
	const unable = "Uncaught Error: Unable to deserialize cloned data."
	const hostObject = "Uncaught Error: Deno deserializer: read_host_object not implemented"
	cases := []struct {
		name    string
		data    []byte
		message string
	}{
		{"empty", nil, unable},
		{"truncated_header", []byte{0xFF}, hostObject},
		{"bad_header", []byte{0x00, 0x00}, unable},
		{"truncated_body", []byte{0xFF, 0x0D, 0x42}, hostObject},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			iso, ctx, scope := newTestRuntime(t)
			tc, err := newTryCatch(t, iso)
			if err != nil {
				t.Fatalf("NewTryCatch: %v", err)
			}
			defer func() { _ = tc.Close() }()
			vd, err := gov8.NewValueDeserializer(scope, ctx, c.data)
			if err != nil {
				t.Fatalf("NewValueDeserializer: %v", err)
			}
			defer func() { _ = vd.Close() }()
			_, rerr := vd.ReadValue(ctx, tc)
			if rerr == nil || !gov8.IsException(rerr) {
				t.Fatalf("ReadValue = %v, want an exception", rerr)
			}
			caught, _ := tc.HasCaught()
			if !caught {
				t.Fatal("TryCatch did not observe the failure")
			}
			msg, _ := tc.MessageText(scope, ctx)
			if msg != c.message {
				t.Errorf("message = %q, want %q", msg, c.message)
			}
		})
	}
}

func TestSerializeUnserializableFunction(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	tc, err := newTryCatch(t, iso)
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	v, ok := evalValue(t, ctx, scope, tc, "() => 1")
	if !ok {
		t.Fatal("eval function failed")
	}
	isFn, _ := v.IsFunction()
	if !isFn {
		t.Fatal("value is not a function")
	}

	captured := ""
	ser, err := gov8.NewValueSerializer(scope, ctx, captureDelegate{on: func(msg string) bool {
		captured = msg
		return true
	}})
	if err != nil {
		t.Fatalf("NewValueSerializer: %v", err)
	}
	defer func() { _ = ser.Close() }()
	ok2, werr := ser.WriteValue(ctx, v, tc)
	if ok2 || werr == nil || !gov8.IsException(werr) {
		t.Fatalf("WriteValue = %v %v, want failure with an exception", ok2, werr)
	}
	wire, err := ser.Release()
	if err != nil || len(wire) != 0 {
		t.Fatalf("Release = %d %v", len(wire), err)
	}
	if captured != "() => 1 could not be cloned." {
		t.Errorf("delegate message = %q", captured)
	}
	caught, _ := tc.HasCaught()
	if !caught {
		t.Fatal("TryCatch did not observe the re-thrown clone error")
	}
	msg, _ := tc.MessageText(scope, ctx)
	if msg != "Uncaught Error: () => 1 could not be cloned." {
		t.Errorf("message = %q", msg)
	}
}

func TestSerializeDetachedSource(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	tc, err := newTryCatch(t, iso)
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	ab, err := gov8.NewArrayBuffer(scope, ctx, 4)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	if ok, derr := ab.Detach(ctx, gov8.Value{}); err != nil || !ok {
		t.Fatalf("detach = %v %v", ok, derr)
	}

	captured := ""
	ser, err := gov8.NewValueSerializer(scope, ctx, captureDelegate{on: func(msg string) bool {
		captured = msg
		return true
	}})
	if err != nil {
		t.Fatalf("NewValueSerializer: %v", err)
	}
	defer func() { _ = ser.Close() }()
	ok2, werr := ser.WriteValue(ctx, ab.Value, tc)
	if ok2 || werr == nil {
		t.Fatalf("WriteValue = %v %v, want failure", ok2, werr)
	}
	wire, rerr := ser.Release()
	if rerr != nil || len(wire) != 0 {
		t.Fatalf("Release = %d %v", len(wire), rerr)
	}
	if captured != "An ArrayBuffer is detached and could not be cloned." {
		t.Errorf("delegate message = %q", captured)
	}
}

func TestSerializeArrayBufferCloneAndTransfer(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	bs, err := iso.NewBackingStoreFromSlice([]byte{9, 8, 7, 6})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	defer func() { _ = bs.Close() }()
	ab, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
	}

	// Clone path: contents copied into the wire; source untouched.
	wire, ok, _ := serializeValue(t, ctx, scope, nil, ab.Value, false)
	if !ok {
		t.Fatal("serialize ArrayBuffer failed")
	}
	if got := hexEncode(wire); got != "420409080706" {
		t.Fatalf("clone wire = %s", got)
	}
	vd, err := gov8.NewValueDeserializer(scope, ctx, wire)
	if err != nil {
		t.Fatalf("NewValueDeserializer: %v", err)
	}
	back, err := vd.ReadValue(ctx, nil)
	if err != nil {
		t.Fatalf("ReadValue: %v", err)
	}
	if is, _ := back.IsArrayBuffer(); !is {
		t.Fatal("roundtrip value is not an ArrayBuffer")
	}
	backAB, err := gov8.AsArrayBuffer(back)
	if err != nil {
		t.Fatalf("AsArrayBuffer: %v", err)
	}
	if n, _ := backAB.ByteLength(); n != 4 {
		t.Errorf("roundtrip byte_length = %d", n)
	}
	backStore, err := backAB.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	got := make([]byte, 4)
	if n, _ := backStore.ReadAt(got, 0); n != 4 || !bytes.Equal(got, []byte{9, 8, 7, 6}) {
		t.Errorf("roundtrip contents = %d % x", n, got)
	}
	_ = backStore.Close()
	_ = vd.Close()

	// Transfer path: registering id 7 writes a two-byte transfer wire; this
	// build does NOT detach the source at write time.
	ser, err := gov8.NewValueSerializer(scope, ctx, reportingDelegate{})
	if err != nil {
		t.Fatalf("NewValueSerializer: %v", err)
	}
	if err := ser.TransferArrayBuffer(7, ab); err != nil {
		t.Fatalf("TransferArrayBuffer: %v", err)
	}
	ok2, werr := ser.WriteValue(ctx, ab.Value, nil)
	if !ok2 || werr != nil {
		t.Fatalf("transfer WriteValue = %v %v", ok2, werr)
	}
	twire, err := ser.Release()
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := ser.Close(); err != nil {
		t.Fatalf("ser.Close: %v", err)
	}
	if got := hexEncode(twire); got != "7407" {
		t.Fatalf("transfer wire = %s", got)
	}
	if n, _ := ab.ByteLength(); n != 4 {
		t.Errorf("source byte_length after transfer write = %d", n)
	}
	if d, _ := ab.WasDetached(); d {
		t.Error("source was detached at write time")
	}

	// With the id registered against a fresh zeroed buffer, the receiving
	// buffer's own store is reused as the transferred contents.
	targetBS, err := iso.NewBackingStoreFromSlice(make([]byte, 4))
	if err != nil {
		t.Fatalf("target store: %v", err)
	}
	defer func() { _ = targetBS.Close() }()
	target, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, targetBS)
	if err != nil {
		t.Fatalf("target buffer: %v", err)
	}
	vd7, err := gov8.NewValueDeserializer(scope, ctx, twire)
	if err != nil {
		t.Fatalf("NewValueDeserializer: %v", err)
	}
	defer func() { _ = vd7.Close() }()
	if err := vd7.TransferArrayBuffer(7, target); err != nil {
		t.Fatalf("TransferArrayBuffer: %v", err)
	}
	if _, err := vd7.ReadValue(ctx, nil); err != nil {
		t.Fatalf("ReadValue with transfer: %v", err)
	}
	if n, _ := target.ByteLength(); n != 4 {
		t.Errorf("target byte_length = %d", n)
	}
	tgt := make([]byte, 4)
	if n, _ := targetBS.ReadAt(tgt, 0); n != 4 || !bytes.Equal(tgt, []byte{0, 0, 0, 0}) {
		t.Errorf("target contents = %d % x", n, tgt)
	}

	// Without registering the id, deserialization fails deterministically.
	tc, err := newTryCatch(t, iso)
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	vdNo, err := gov8.NewValueDeserializer(scope, ctx, twire)
	if err != nil {
		t.Fatalf("NewValueDeserializer (no transfer): %v", err)
	}
	defer func() { _ = vdNo.Close() }()
	if _, rerr := vdNo.ReadValue(ctx, tc); rerr == nil {
		t.Fatal("ReadValue without transfer unexpectedly succeeded")
	}
	if msg, _ := tc.MessageText(scope, ctx); msg != "Uncaught Error: Unable to deserialize cloned data." {
		t.Errorf("message = %q", msg)
	}
}

func TestSerializeNilDelegateRejected(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	if _, err := gov8.NewValueSerializer(scope, ctx, nil); err == nil {
		t.Fatal("nil delegate accepted")
	}
	if _, err := gov8.NewValueSerializer(scope, nil, reportingDelegate{}); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestDeserializerInputIsNotCopied(t *testing.T) {
	// Exercises the no-copy input contract: a one-byte legacy wire is read
	// directly from the caller's slice, and Close releases the engine's
	// reference to it.
	_, ctx, scope := newTestRuntime(t)
	vd, err := gov8.NewValueDeserializer(scope, ctx, []byte{0x54}) // 'true'
	if err != nil {
		t.Fatalf("NewValueDeserializer: %v", err)
	}
	defer func() { _ = vd.Close() }()
	v, err := vd.ReadValue(ctx, nil)
	if err != nil {
		t.Fatalf("ReadValue: %v", err)
	}
	if b, _ := v.BooleanValue(); !b {
		t.Error("deserialized true = false")
	}
}

// newTryCatch registers a TryCatch on the isolate.
func newTryCatch(t testing.TB, iso *gov8.Isolate) (*gov8.TryCatch, error) {
	t.Helper()
	return iso.NewTryCatch()
}

type captureDelegate struct {
	on func(message string) bool
}

func (d captureDelegate) ThrowDataCloneError(message string) bool { return d.on(message) }
