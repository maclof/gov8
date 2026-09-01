//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Delegate-completion behavior tests for the serializer/deserializer slice
// (serializer_delegates.go), mirroring the observable contract pinned by
// rust-oracle/src/bin/conformance-serializer-delegates.rs with plain Go
// assertions. Byte-exact fixture coverage lives in
// conformance-serializer-delegates; panic/fatal boundaries in
// serializer_delegates_negative_test.go.

// --- shared counting delegates --------------------------------------------------

type sdCounts struct {
	hasCustom, isHost, writeHost, readHost int
	sabID, sabFromID                       int
	wasmID, wasmFromID                     int
	throwClone                             int
}

// sdBase is the minimum delegate: records the clone error, optionally
// rethrows (shim-side rebuild), no other hooks.
type sdBase struct {
	counts   *sdCounts
	rethrow  bool
	cloneErr *string
}

func (d sdBase) ThrowDataCloneError(message string) bool {
	d.counts.throwClone++
	if d.cloneErr != nil {
		*d.cloneErr = message
	}
	return d.rethrow
}

// sdRoundTrip writes uint32+raw+double for every host object and rebuilds
// {kind: "host", n: 42} on read.
type sdRoundTrip struct {
	counts *sdCounts
}

func (d sdRoundTrip) ThrowDataCloneError(string) bool { return true }

func (d sdRoundTrip) WriteHostObject(obj *gov8.Object, w *gov8.DelegateValueSerializer) (bool, bool) {
	d.counts.writeHost++
	if err := w.WriteUint32(42); err != nil {
		return false, false
	}
	if err := w.WriteRawBytes([]byte("host")); err != nil {
		return false, false
	}
	if err := w.WriteDouble(3.5); err != nil {
		return false, false
	}
	return true, true
}

func (d sdRoundTrip) ReadHostObject(r *gov8.DelegateValueDeserializer) (*gov8.Object, bool) {
	d.counts.readHost++
	magic, okU, err := r.ReadUint32()
	if err != nil || !okU || magic != 42 {
		return nil, false
	}
	raw, okR, err := r.ReadRawBytes(4)
	if err != nil || !okR || string(raw) != "host" {
		return nil, false
	}
	dd, okF, err := r.ReadDouble()
	if err != nil || !okF || dd != 3.5 {
		return nil, false
	}
	obj, err := r.Scope().NewObject(r.Context())
	if err != nil {
		return nil, false
	}
	kv, err := r.Scope().NewString("host")
	if err != nil {
		return nil, false
	}
	if _, err := obj.SetByName(r.Scope(), r.Context(), "kind", kv); err != nil {
		return nil, false
	}
	nv, err := r.Scope().Number(42)
	if err != nil {
		return nil, false
	}
	if _, err := obj.SetByName(r.Scope(), r.Context(), "n", nv); err != nil {
		return nil, false
	}
	return obj, true
}

// sdDenyWrite answers Some(false) without throwing or writing: the pinned
// build ignores the result and the write succeeds.
type sdDenyWrite struct {
	counts *sdCounts
}

func (d sdDenyWrite) ThrowDataCloneError(string) bool { return true }

func (d sdDenyWrite) WriteHostObject(*gov8.Object, *gov8.DelegateValueSerializer) (bool, bool) {
	d.counts.writeHost++
	return false, true
}

// sdCustomThrow throws its own RangeError from inside the hook.
type sdCustomThrow struct {
	counts *sdCounts
}

func (d sdCustomThrow) ThrowDataCloneError(string) bool { return true }

func (d sdCustomThrow) WriteHostObject(_ *gov8.Object, w *gov8.DelegateValueSerializer) (bool, bool) {
	d.counts.writeHost++
	msg, err := w.NewRangeError("host serialization refused")
	if err != nil {
		return false, false
	}
	if err := w.ThrowException(msg); err != nil {
		return false, false
	}
	return false, false
}

// sdSABID answers 42 for every SAB; sdSABFromID supplies a fresh SAB for 42.
type sdSABID struct{}

func (sdSABID) ThrowDataCloneError(string) bool { return true }

func (sdSABID) GetSharedArrayBufferID(*gov8.SharedArrayBuffer) (uint32, bool) {
	return 42, true
}

type sdSABFromID struct {
	counts *sdCounts
	iso    *gov8.Isolate
	scope  *gov8.Scope
	ctx    *gov8.Context
}

func (d sdSABFromID) GetSharedArrayBufferFromID(id uint32) (*gov8.SharedArrayBuffer, bool) {
	d.counts.sabFromID++
	if id != 42 {
		return nil, false
	}
	bs, err := d.iso.NewSharedArrayBufferBackingStore(4)
	if err != nil {
		return nil, false
	}
	defer func() { _ = bs.Close() }()
	if _, err := bs.WriteAt([]byte{5, 6, 7, 8}, 0); err != nil {
		return nil, false
	}
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(d.scope, d.ctx, bs)
	if err != nil {
		return nil, false
	}
	return sab, true
}

// --- helpers ---------------------------------------------------------------

func sdTryCatch(t *testing.T, iso *gov8.Isolate) *gov8.TryCatch {
	t.Helper()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	return tc
}

func sdStore(t *testing.T, iso *gov8.Isolate, data []byte) *gov8.BackingStore {
	t.Helper()
	bs, err := iso.NewBackingStoreFromSlice(data)
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	return bs
}

func sdSharedStore(t *testing.T, iso *gov8.Isolate, n int) *gov8.BackingStore {
	t.Helper()
	bs, err := iso.NewSharedArrayBufferBackingStore(n)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStore: %v", err)
	}
	return bs
}

// --- roundtrip -------------------------------------------------------------

func TestSerDelHostObjectRoundtrip(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	counts := &sdCounts{}

	wire := func() []byte {
		tc := sdTryCatch(t, iso)
		defer func() { _ = tc.Close() }()
		bs := sdStore(t, iso, []byte{1, 2, 3, 4})
		defer func() { _ = bs.Close() }()
		ab, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
		if err != nil {
			t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
		}
		ta, err := gov8.NewUint8Array(scope, ctx, ab, 0, 4)
		if err != nil {
			t.Fatalf("NewUint8Array: %v", err)
		}
		ser, err := gov8.NewDelegateValueSerializer(scope, ctx, sdRoundTrip{counts})
		if err != nil {
			t.Fatalf("NewDelegateValueSerializer: %v", err)
		}
		defer func() { _ = ser.Close() }()
		if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
			t.Fatalf("SetTreatArrayBufferViewsAsHostObjects: %v", err)
		}
		ok, werr := ser.WriteValue(ctx, ta.Value, tc)
		if werr != nil || !ok {
			t.Fatalf("write failed: ok=%v err=%v", ok, werr)
		}
		out, rerr := ser.Release()
		if rerr != nil {
			t.Fatalf("Release: %v", rerr)
		}
		want := "5c2a686f73740000000000000c40"
		if got := hexEncode(out); got != want {
			t.Fatalf("wire = %s, want %s", got, want)
		}
		return out
	}()
	if counts.writeHost != 1 {
		t.Fatalf("writeHost = %d, want 1", counts.writeHost)
	}

	tc := sdTryCatch(t, iso)
	defer func() { _ = tc.Close() }()
	vd, err := gov8.NewDelegateValueDeserializer(scope, ctx, wire, sdRoundTrip{counts})
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	v, rerr := vd.ReadValue(ctx, tc)
	if rerr != nil {
		t.Fatalf("ReadValue: %v", rerr)
	}
	if err := vd.Close(); err != nil {
		t.Fatalf("vd Close: %v", err)
	}
	if counts.readHost != 1 {
		t.Fatalf("readHost = %d, want 1", counts.readHost)
	}
	obj, err := gov8.AsObject(v)
	if err != nil {
		t.Fatalf("AsObject: %v", err)
	}
	kv, found, gerr := obj.GetByName(scope, ctx, "kind")
	if gerr != nil || !found {
		t.Fatalf("kind: found=%v err=%v", found, gerr)
	}
	if txt, _ := kv.ToString(ctx); txt != "host" {
		t.Fatalf("kind = %q, want host", txt)
	}
}

// --- default-hook behavior -------------------------------------------------

func TestSerDelDefaultsRejectHostObjects(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	counts := &sdCounts{}

	// Default write_host_object under treat-views fails with the
	// deterministic error; the tag byte stays on the partial wire and the
	// clone-error hook is NOT involved.
	tc := sdTryCatch(t, iso)
	defer func() { _ = tc.Close() }()
	ab, err := gov8.NewArrayBuffer(scope, ctx, 4)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	ta, err := gov8.NewUint8Array(scope, ctx, ab, 0, 4)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	cloneErr := ""
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, sdBase{counts: counts, rethrow: true, cloneErr: &cloneErr})
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	defer func() { _ = ser.Close() }()
	if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
		t.Fatal(err)
	}
	ok, werr := ser.WriteValue(ctx, ta.Value, tc)
	if !gov8.IsException(werr) {
		t.Fatalf("WriteValue error = %v, want an exception", werr)
	}
	if ok {
		t.Fatal("write unexpectedly succeeded")
	}
	wireOut, rerr := ser.Release()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if hexEncode(wireOut) != "5c" {
		t.Fatalf("wire = %s, want 5c", hexEncode(wireOut))
	}
	if cloneErr != "" {
		t.Fatalf("clone error hook invoked with %q, want untouched", cloneErr)
	}
	caught, _ := tc.HasCaught()
	if !caught {
		t.Fatal("expected a caught exception")
	}
	msg, _ := tc.MessageText(scope, ctx)
	if !strings.Contains(msg, "write_host_object not implemented") {
		t.Fatalf("message = %q", msg)
	}

	// Default read_host_object on a tagged payload.
	bytes := hexToBytes("5c2a")
	vd, err := gov8.NewDelegateValueDeserializer(scope, ctx, bytes, nil)
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	defer func() { _ = vd.Close() }()
	if _, rerr := vd.ReadValue(ctx, tc); !gov8.IsException(rerr) {
		t.Fatalf("ReadValue error = %v, want an exception", rerr)
	}
	msg2, _ := tc.MessageText(scope, ctx)
	if !strings.Contains(msg2, "read_host_object not implemented") {
		t.Fatalf("message = %q", msg2)
	}
}

func TestSerDelDefaultSABWriteRejected(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	counts := &sdCounts{}
	tc := sdTryCatch(t, iso)
	defer func() { _ = tc.Close() }()

	bs := sdSharedStore(t, iso, 8)
	defer func() { _ = bs.Close() }()
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatal(err)
	}
	cloneErr := ""
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, sdBase{counts: counts, rethrow: true, cloneErr: &cloneErr})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser.Close() }()
	if ok, werr := ser.WriteValue(ctx, sab.Value, tc); ok || !gov8.IsException(werr) {
		t.Fatalf("write ok=%v err=%v, want a rejected write", ok, werr)
	}
	wireOut, rerr := ser.Release()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(wireOut) != 0 {
		t.Fatalf("wire = %s, want empty", hexEncode(wireOut))
	}
	// The rejection does NOT go through ThrowDataCloneError: the crate's
	// delegate glue forwards the silent None to V8's own throwing base
	// default, so the message surfaces directly in the TryCatch.
	if cloneErr != "" {
		t.Fatalf("clone error hook invoked with %q, want untouched", cloneErr)
	}
	msg, _ := tc.MessageText(scope, ctx)
	if !strings.Contains(msg, "#<SharedArrayBuffer> could not be cloned.") {
		t.Fatalf("message = %q", msg)
	}
}

func TestSerDelWriteHostObjectFalseResultIgnored(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	counts := &sdCounts{}
	tc := sdTryCatch(t, iso)
	defer func() { _ = tc.Close() }()
	ab, err := gov8.NewArrayBuffer(scope, ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	ta, err := gov8.NewUint8Array(scope, ctx, ab, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, sdDenyWrite{counts})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser.Close() }()
	if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
		t.Fatal(err)
	}
	ok, werr := ser.WriteValue(ctx, ta.Value, tc)
	if !ok || werr != nil {
		t.Fatalf("Some(false) without throwing must still succeed: ok=%v err=%v", ok, werr)
	}
	wireOut, rerr := ser.Release()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if hexEncode(wireOut) != "5c" {
		t.Fatalf("wire = %s, want 5c", hexEncode(wireOut))
	}
	if caught, _ := tc.HasCaught(); caught {
		t.Fatal("no exception expected")
	}
}

// --- custom throw -----------------------------------------------------------

func TestSerDelHookThrowsOwnException(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	counts := &sdCounts{}
	tc := sdTryCatch(t, iso)
	defer func() { _ = tc.Close() }()
	ab, err := gov8.NewArrayBuffer(scope, ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	ta, err := gov8.NewUint8Array(scope, ctx, ab, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, sdCustomThrow{counts})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser.Close() }()
	if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
		t.Fatal(err)
	}
	if ok, werr := ser.WriteValue(ctx, ta.Value, tc); ok || !gov8.IsException(werr) {
		t.Fatalf("thrown hook must fail the write: ok=%v err=%v", ok, werr)
	}
	msg, _ := tc.MessageText(scope, ctx)
	if !strings.Contains(msg, "RangeError: host serialization refused") {
		t.Fatalf("message = %q", msg)
	}
}

// --- SAB roundtrip ----------------------------------------------------------

func TestSerDelSABRoundtrip(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	counts := &sdCounts{}
	tc := sdTryCatch(t, iso)
	defer func() { _ = tc.Close() }()

	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, sdSABID{})
	if err != nil {
		t.Fatal(err)
	}
	bs := sdSharedStore(t, iso, 8)
	defer func() { _ = bs.Close() }()
	if _, err := bs.WriteAt([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 0); err != nil {
		t.Fatal(err)
	}
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatal(err)
	}
	if ok, werr := ser.WriteValue(ctx, sab.Value, tc); !ok || werr != nil {
		t.Fatalf("SAB write failed: ok=%v err=%v", ok, werr)
	}
	wireOut, rerr := ser.Release()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if err := ser.Close(); err != nil {
		t.Fatal(err)
	}
	if hexEncode(wireOut) != "752a" {
		t.Fatalf("wire = %s, want 752a", hexEncode(wireOut))
	}

	vd, err := gov8.NewDelegateValueDeserializer(scope, ctx, wireOut, sdSABFromID{
		counts: counts, iso: iso, scope: scope, ctx: ctx,
	})
	if err != nil {
		t.Fatal(err)
	}
	v, rerr := vd.ReadValue(ctx, tc)
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	if err := vd.Close(); err != nil {
		t.Fatal(err)
	}
	isSAB, _ := v.IsSharedArrayBuffer()
	if !isSAB {
		t.Fatal("deserialized value is not a SharedArrayBuffer")
	}
	sab2, err := gov8.AsSharedArrayBuffer(v)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := sab2.ByteLength(); n != 4 {
		t.Fatalf("byte length = %d, want 4", n)
	}
	bs2, err := sab2.GetBackingStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bs2.Close() }()
	got := make([]byte, 4)
	if _, err := bs2.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	want := []byte{5, 6, 7, 8}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("contents = %v, want %v", got, want)
		}
	}
	if counts.sabFromID != 1 {
		t.Fatalf("sabFromID = %d, want 1", counts.sabFromID)
	}
}

// --- lifecycle and guards ---------------------------------------------------

func TestSerDelLifecycleGuards(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	counts := &sdCounts{}

	if _, err := gov8.NewDelegateValueSerializer(scope, ctx, nil); err == nil {
		t.Fatal("nil delegate accepted")
	}

	cloneErr := ""
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, sdBase{counts: counts, rethrow: true, cloneErr: &cloneErr})
	if err != nil {
		t.Fatal(err)
	}
	if err := ser.WriteUint32(7); err != nil {
		t.Fatal(err)
	}
	first, rerr := ser.Release()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if hexEncode(first) != "07" {
		t.Fatalf("first release = %s", hexEncode(first))
	}
	// The crate's release() is empty on the second call (fixture-pinned).
	second, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("second release: %v", rerr)
	}
	if len(second) != 0 {
		t.Fatalf("second release = %s, want empty", hexEncode(second))
	}
	if err := ser.WriteUint32(1); err == nil {
		t.Fatal("write after Release accepted")
	}
	if err := ser.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ser.Close(); err == nil {
		t.Fatal("double Close accepted")
	}
	if err := ser.WriteHeader(); err == nil {
		t.Fatal("use after Close accepted")
	}

	// Close without release frees through the delegate; the engine stays
	// healthy.
	ser2, err := gov8.NewDelegateValueSerializer(scope, ctx, sdBase{counts: counts, rethrow: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ser2.WriteRawBytes(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	if err := ser2.Close(); err != nil {
		t.Fatal(err)
	}
	ser3, err := gov8.NewDelegateValueSerializer(scope, ctx, sdBase{counts: counts, rethrow: true})
	if err != nil {
		t.Fatalf("engine unusable after drop-without-release: %v", err)
	}
	if err := ser3.WriteUint32(5); err != nil {
		t.Fatal(err)
	}
	wireOut, rerr := ser3.Release()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if hexEncode(wireOut) != "05" {
		t.Fatalf("after drop wire = %s, want 05", hexEncode(wireOut))
	}
	_ = ser3.Close()
}

func TestSerDelForeignIsolateRejected(t *testing.T) {
	_, ctxA, scopeA := newTestRuntime(t)
	_, ctxB, scopeB := newTestRuntime(t)

	ser, err := gov8.NewDelegateValueSerializer(scopeA, ctxA, sdBase{counts: &sdCounts{}, rethrow: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser.Close() }()
	v, err := scopeB.NewString("x")
	if err != nil {
		t.Fatal(err)
	}
	if _, werr := ser.WriteValue(ctxB, v, nil); werr == nil {
		t.Fatal("foreign-isolate write accepted")
	}
	abB, err := gov8.NewArrayBuffer(scopeB, ctxB, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := ser.TransferArrayBuffer(1, abB); err == nil {
		t.Fatal("foreign-isolate transfer accepted")
	}

	// Deserializer guards: foreign context and nil-input misuse.
	if _, err := gov8.NewDelegateValueDeserializer(scopeB, ctxA, []byte{0x54}, nil); err == nil {
		t.Fatal("foreign context accepted")
	}
	vd, err := gov8.NewDelegateValueDeserializer(scopeA, ctxA, []byte{0x54}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vd.Close() }()
	if _, rerr := vd.ReadValue(ctxB, nil); rerr == nil {
		t.Fatal("foreign-isolate read accepted")
	}
	if err := vd.TransferSharedArrayBuffer(1, nil); err == nil {
		t.Fatal("nil SAB accepted")
	}
}

// --- reader helpers and wire version ------------------------------------------

func TestSerDelReadHeaderAndWireVersion(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	cloneErr := ""
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, sdBase{counts: &sdCounts{}, rethrow: true, cloneErr: &cloneErr})
	if err != nil {
		t.Fatal(err)
	}
	if err := ser.WriteHeader(); err != nil {
		t.Fatal(err)
	}
	tv, err := scope.Boolean(true)
	if err != nil {
		t.Fatal(err)
	}
	if ok, werr := ser.WriteValue(ctx, tv, nil); !ok || werr != nil {
		t.Fatalf("write: ok=%v err=%v", ok, werr)
	}
	wireOut, rerr := ser.Release()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if err := ser.Close(); err != nil {
		t.Fatal(err)
	}
	if hexEncode(wireOut) != "ff1054" {
		t.Fatalf("wire = %s, want ff1054", hexEncode(wireOut))
	}

	tc := sdTryCatch(t, iso)
	defer func() { _ = tc.Close() }()
	vd, err := gov8.NewDelegateValueDeserializer(scope, ctx, wireOut, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vd.Close() }()
	header, herr := vd.ReadHeader(ctx, tc)
	if herr != nil || !header {
		t.Fatalf("ReadHeader = %v, %v", header, herr)
	}
	version, verr := vd.GetWireFormatVersion()
	if verr != nil || version != 16 {
		t.Fatalf("wire version = %d, %v; want 16", version, verr)
	}
	v, rerr := vd.ReadValue(ctx, tc)
	if rerr != nil {
		t.Fatalf("ReadValue: %v", rerr)
	}
	bl, _ := v.BooleanValue()
	if !bl {
		t.Fatal("read value is not true")
	}

	// Reading the versioned bytes WITHOUT ReadHeader consumes the header
	// bytes as tags and reaches the host-object path (pinned default
	// error).
	tc2 := sdTryCatch(t, iso)
	defer func() { _ = tc2.Close() }()
	vd2, err := gov8.NewDelegateValueDeserializer(scope, ctx, wireOut, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vd2.Close() }()
	if _, rerr := vd2.ReadValue(ctx, tc2); !gov8.IsException(rerr) {
		t.Fatalf("header-less read = %v, want an exception", rerr)
	}
	msg, _ := tc2.MessageText(scope, ctx)
	if !strings.Contains(msg, "read_host_object not implemented") {
		t.Fatalf("message = %q", msg)
	}
}

func TestSerDelRawReader(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	// The helper reads operate on the reader's CURRENT position: outside a
	// hook that is the raw wire start (inside ReadHostObject the engine
	// has already consumed the 0x5c tag). Wire: varint(42) | 4 raw bytes |
	// LE double 3.5.
	vd, err := gov8.NewDelegateValueDeserializer(scope, ctx,
		hexToBytes("2a686f73740000000000000c40"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vd.Close() }()
	magic, ok, err := vd.ReadUint32()
	if err != nil || !ok || magic != 42 {
		t.Fatalf("ReadUint32 = %d, %v, %v", magic, ok, err)
	}
	raw, ok, err := vd.ReadRawBytes(4)
	if err != nil || !ok || string(raw) != "host" {
		t.Fatalf("ReadRawBytes = %q, %v, %v", raw, ok, err)
	}
	dd, ok, err := vd.ReadDouble()
	if err != nil || !ok || dd != 3.5 {
		t.Fatalf("ReadDouble = %v, %v, %v", dd, ok, err)
	}
	// A read past the end reports ok=false instead of throwing.
	_, ok, err = vd.ReadUint32()
	if err != nil || ok {
		t.Fatalf("exhausted ReadUint32 = %v, %v; want ok=false", ok, err)
	}
	if version, err := vd.GetWireFormatVersion(); err != nil || version != 0 {
		t.Fatalf("legacy wire version = %d, %v; want 0", version, err)
	}
}
