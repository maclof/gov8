//go:build windows && amd64

package main

// The 25 serializer/deserializer DELEGATE checks of the pinned Rust oracle
// (rust-oracle/src/bin/conformance-serializer-delegates.rs), re-implemented
// on the Go binding in the same registry order. Every value is produced by
// live engine observation; the comparison target is the pinned fixture.

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// --- small helpers -----------------------------------------------------------

// newTC opens a TryCatch on the runtime's isolate.
func newTC(t tester, r *runtime) *gov8.TryCatch {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	return tc
}

// closeTC closes a TryCatch.
func closeTC(t tester, tc *gov8.TryCatch) {
	t.Helper()
	if err := tc.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}
}

// writeOnce runs one WriteValue and reports the Maybe<bool> as plain ok
// (write failures carry the exception through the caller's TryCatch).
func writeOnce(t tester, ser *gov8.DelegateValueSerializer, c *gov8.Context, v gov8.Value, tc *gov8.TryCatch) bool {
	t.Helper()
	ok, err := ser.WriteValue(c, v, tc)
	if err != nil {
		_ = err // exception path: ok=false, observable via tc
	}
	return ok
}

// readOnce runs one ReadValue and returns the value (ok=false on the
// exception path).
func readOnce(t tester, vd *gov8.DelegateValueDeserializer, c *gov8.Context, tc *gov8.TryCatch) (gov8.Value, bool) {
	t.Helper()
	v, err := vd.ReadValue(c, tc)
	if err != nil {
		_ = err
		return gov8.Value{}, false
	}
	return v, true
}

// --- 1. detection pipeline: denies all hosts ---------------------------------

// detectionDeniesAllHosts pins: has_custom_host_object consulted exactly
// once (constructor), is_host_object for every NEW plain object, and the
// object-id map short-circuit for a repeat write (no second consult, a
// '^' reference instead).
func detectionDeniesAllHosts(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	a, ok := r.eval(t, tc, "({marker: 1})")
	if !ok {
		t.Fatal("eval a failed")
	}
	bv, ok := r.eval(t, tc, "({other: 2})")
	if !ok {
		t.Fatal("eval b failed")
	}

	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, denyAllHosts{counts})
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	ok1 := writeOnce(t, ser, r.ctx, a, tc)
	ok1Again := writeOnce(t, ser, r.ctx, a, tc)
	ok2 := writeOnce(t, ser, r.ctx, bv, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	if err := ser.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := obj(
		kv("ok1", b(ok1)),
		kv("ok1_again", b(ok1Again)),
		kv("ok2", b(ok2)),
		kv("wire", s(lowerHex(wire))),
		kv("counts", countsJSON(counts)),
	)
	want := obj(
		kv("ok1", b(true)),
		kv("ok1_again", b(true)),
		kv("ok2", b(true)),
		// o "marker" I(1) { 1 | ^ id0 | o "other" I(2) { 1.
		kv("wire", s("6f22066d61726b657249027b015e006f22056f7468657249047b01")),
		kv("counts", obj(
			kv("has_custom_host_object", i(1)),
			kv("is_host_object", i(2)),
			kv("write_host_object", i(0)),
			kv("read_host_object", i(0)),
			kv("get_shared_array_buffer_id", i(0)),
			kv("get_shared_array_buffer_from_id", i(0)),
			kv("get_wasm_module_transfer_id", i(0)),
			kv("get_wasm_module_from_id", i(0)),
			kv("throw_data_clone_error", i(0)),
		)),
	)
	return wantGot("serdel/detection_denies_all_hosts", want, got)
}

// --- 2. embedder-field fallback without custom hooks --------------------------

// detectionEmbedderFieldsWithoutCustom pins the default-hook fallback: an
// instance of an ObjectTemplate with internal fields routes to the DEFAULT
// write_host_object (deterministic error, tag already on the partial
// wire), while a plain {} stays native. The observed routing (embedder
// fields -> host object, plain object -> native path) is the evidence that
// the default has_custom_host_object (false) was cached with zero Go
// calls.
func detectionEmbedderFieldsWithoutCustom(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}

	templ, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if _, err := templ.SetInternalFieldCount(2); err != nil {
		t.Fatalf("SetInternalFieldCount: %v", err)
	}
	withFields, built, err := templ.NewInstance(r.scope, r.ctx)
	if err != nil || !built {
		t.Fatalf("NewInstance: built=%v err=%v", built, err)
	}
	plain, ok := r.eval(t, nil, "({})")
	if !ok {
		t.Fatal("eval plain failed")
	}

	// Embedder-field write under its own TryCatch.
	embedderMsg, embedderOK, embedderWire := func() (string, bool, []byte) {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		delegate, _ := newSerBase(counts, true)
		ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
		if err != nil {
			t.Fatalf("NewDelegateValueSerializer: %v", err)
		}
		okW := writeOnce(t, ser, r.ctx, withFields.Value, tc)
		wire, rerr := ser.Release()
		if rerr != nil {
			t.Fatalf("Release: %v", rerr)
		}
		_ = ser.Close()
		return caughtMessage(t, r, tc), okW, wire
	}()

	// Plain object write under its own TryCatch.
	plainOK, plainWire := func() (bool, []byte) {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		delegate, _ := newSerBase(counts, true)
		ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
		if err != nil {
			t.Fatalf("NewDelegateValueSerializer 2: %v", err)
		}
		okW := writeOnce(t, ser, r.ctx, plain, tc)
		wire, rerr := ser.Release()
		if rerr != nil {
			t.Fatalf("Release 2: %v", rerr)
		}
		_ = ser.Close()
		return okW, wire
	}()

	got := obj(
		kv("embedder_ok", b(embedderOK)),
		kv("embedder_wire", s(lowerHex(embedderWire))),
		kv("embedder_caught_message", s(embedderMsg)),
		kv("plain_ok", b(plainOK)),
		kv("plain_wire", s(lowerHex(plainWire))),
		kv("counts", zeroCountsJSON()),
	)
	want := obj(
		kv("embedder_ok", b(false)),
		// The kHostObject tag is written before the default hook fails.
		kv("embedder_wire", s("5c")),
		kv("embedder_caught_message", s("Uncaught Error: Deno serializer: write_host_object not implemented")),
		kv("plain_ok", b(true)),
		kv("plain_wire", s("6f7b00")),
		kv("counts", zeroCountsJSON()),
	)
	return wantGot("serdel/detection_embedder_fields_without_custom", want, got)
}

// --- 3. detection admits host -> routes to write -------------------------------

// detectionAdmitsHostRoutesToWrite pins: is_host_object -> Some(true)
// routes a plain object to write_host_object, whose delegate bytes follow
// the 0x5c tag.
func detectionAdmitsHostRoutesToWrite(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	plain, ok := r.eval(t, tc, "({})")
	if !ok {
		t.Fatal("eval plain failed")
	}
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, admitAllHosts{counts})
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, plain, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("counts", countsJSON(counts)),
	)
	want := obj(
		kv("ok", b(true)),
		// kHostObject tag + the delegate's write_uint32(7) varint.
		kv("wire", s("5c07")),
		kv("counts", obj(
			kv("has_custom_host_object", i(1)),
			kv("is_host_object", i(1)),
			kv("write_host_object", i(1)),
			kv("read_host_object", i(0)),
			kv("get_shared_array_buffer_id", i(0)),
			kv("get_shared_array_buffer_from_id", i(0)),
			kv("get_wasm_module_transfer_id", i(0)),
			kv("get_wasm_module_from_id", i(0)),
			kv("throw_data_clone_error", i(0)),
		)),
	)
	return wantGot("serdel/detection_admits_host_routes_to_write", want, got)
}

// --- 4. host write/read roundtrip ----------------------------------------------

// hostWriteReadRoundtrip pins the full host-object write/read roundtrip
// through the treat-views flag and the helper read order, including the
// exact delegate-controlled wire bytes and the legacy wire version.
func hostWriteReadRoundtrip(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	sawTypedArray := new(bool)

	var wireHex string
	func() {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		bs, err := r.iso.NewBackingStoreFromSlice([]byte{1, 2, 3, 4})
		if err != nil {
			t.Fatalf("NewBackingStoreFromSlice: %v", err)
		}
		ab, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, bs)
		if err != nil {
			t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
		}
		ta, err := gov8.NewUint8Array(r.scope, r.ctx, ab, 0, 4)
		if err != nil {
			t.Fatalf("NewUint8Array: %v", err)
		}
		ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx,
			hostWriteCodec{counts: counts, sawTypedArray: sawTypedArray})
		if err != nil {
			t.Fatalf("NewDelegateValueSerializer: %v", err)
		}
		if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
			t.Fatalf("SetTreatArrayBufferViewsAsHostObjects: %v", err)
		}
		if !writeOnce(t, ser, r.ctx, ta.Value, tc) {
			t.Fatal("host write must succeed")
		}
		wire, rerr := ser.Release()
		if rerr != nil {
			t.Fatalf("Release: %v", rerr)
		}
		_ = ser.Close()
		_ = bs.Close()
		wireHex = lowerHex(wire)
	}()

	var read jsonValue
	var readCaught bool
	var u32Calls, rawCalls, f64Calls int64
	var version int64
	func() {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		bytes := hexDecode(wireHex) // retained by the deserializer until Close
		readU32, readRaw, readF64 := new(int), new(int), new(int)
		wireVersion := new(uint32)
		vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes,
			hostReadCodec{counts: counts, readU32: readU32, readRaw: readRaw,
				readF64: readF64, wireVersion: wireVersion})
		if err != nil {
			t.Fatalf("NewDelegateValueDeserializer: %v", err)
		}
		v, okV := readOnce(t, vd, r.ctx, tc)
		_ = vd.Close()
		if okV {
			read = describeValue(t, r, v)
		} else {
			read = jsonNull{}
		}
		readCaught, _ = tc.HasCaught()
		u32Calls = int64(*readU32)
		rawCalls = int64(*readRaw)
		f64Calls = int64(*readF64)
		version = int64(*wireVersion)
	}()

	undefined := obj(kv("type", s("undefined")))
	got := obj(
		kv("wire", s(wireHex)),
		kv("saw_typed_array", b(*sawTypedArray)),
		kv("read", read),
		kv("read_caught", b(readCaught)),
		kv("read_u32_calls", i(u32Calls)),
		kv("read_raw_calls", i(rawCalls)),
		kv("read_f64_calls", i(f64Calls)),
		kv("wire_version", i(version)),
		kv("counts", countsJSON(counts)),
	)
	want := obj(
		// 5c tag + varint(42) + raw "host" (NO length prefix) + LE double 3.5.
		kv("wire", s("5c2a686f73740000000000000c40")),
		kv("saw_typed_array", b(true)),
		kv("read", obj(
			kv("type", s("object")),
			kv("kind", obj(kv("type", s("string")), kv("value", s("host")))),
			kv("n", obj(kv("type", s("int32")), kv("value", i(42)))),
			kv("a", undefined),
			kv("b", undefined),
			kv("x", undefined),
		)),
		kv("read_caught", b(false)),
		kv("read_u32_calls", i(1)),
		kv("read_raw_calls", i(1)),
		kv("read_f64_calls", i(1)),
		// Header-less data reports legacy wire format version 0.
		kv("wire_version", i(0)),
		kv("counts", obj(
			kv("has_custom_host_object", i(0)),
			kv("is_host_object", i(0)),
			kv("write_host_object", i(1)),
			kv("read_host_object", i(1)),
			kv("get_shared_array_buffer_id", i(0)),
			kv("get_shared_array_buffer_from_id", i(0)),
			kv("get_wasm_module_transfer_id", i(0)),
			kv("get_wasm_module_from_id", i(0)),
			kv("throw_data_clone_error", i(0)),
		)),
	)
	return wantGot("serdel/host_write_read_roundtrip", want, got)
}

// --- 5. default write_host_object error leaves the tag on the wire -------------

// hostDefaultWriteErrorPartialWire pins the default write_host_object under
// the treat-views flag: deterministic "not implemented" Error, failed
// write, and the tag byte already on the buffer (no rollback). The
// exception comes from the hook itself; ThrowDataCloneError is NOT
// involved.
func hostDefaultWriteErrorPartialWire(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 4)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	ta, err := gov8.NewUint8Array(r.scope, r.ctx, ab, 0, 4)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}

	delegate, cloneError := newSerBase(counts, true)
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
		t.Fatalf("SetTreatArrayBufferViewsAsHostObjects: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, ta.Value, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("clone_error_called_with", s(*cloneError)),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("caught_message", s(caughtMessage(t, r, tc))),
		kv("counts", zeroCountsJSON()),
	)
	want := obj(
		kv("ok", b(false)),
		kv("wire", s("5c")),
		kv("clone_error_called_with", s("")),
		kv("caught", b(true)),
		kv("caught_message", s("Uncaught Error: Deno serializer: write_host_object not implemented")),
		kv("counts", zeroCountsJSON()),
	)
	return wantGot("serdel/host_default_write_error_partial_wire", want, got)
}

func mustHasCaught(t tester, tc *gov8.TryCatch) bool {
	t.Helper()
	caught, err := tc.HasCaught()
	if err != nil {
		t.Fatalf("HasCaught: %v", err)
	}
	return caught
}

// --- 6. default read_host_object error -------------------------------------------

// hostReadDefaultError pins the deterministic "not implemented" Error on a
// kHostObject-tagged payload with an empty delegate.
func hostReadDefaultError(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)
	bytes := hexDecode("5c2a04686f73740000000000000c40")
	vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes, nil)
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	v, okV := readOnce(t, vd, r.ctx, tc)
	_ = vd.Close()
	described := jsonValue(jsonNull{})
	if okV {
		described = describeValue(t, r, v)
	}
	got := obj(
		kv("read", described),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("message", s(caughtMessage(t, r, tc))),
	)
	want := obj(
		kv("read", jsonNull{}),
		kv("caught", b(true)),
		kv("message", s("Uncaught Error: Deno deserializer: read_host_object not implemented")),
	)
	return wantGot("serdel/host_read_default_error", want, got)
}

// readNone is a read_host_object completing as None WITHOUT throwing.
type readNone struct{}

func (readNone) ReadHostObject(*gov8.DelegateValueDeserializer) (*gov8.Object, bool) {
	return nil, false
}

// --- 7. read_host_object None -> engine error -------------------------------------

// hostReadNoneThrowsEngineError pins that a hook's silent None is never a
// clean read on this build: the engine throws "Unable to deserialize
// cloned data." and the TryCatch is NOT left empty.
func hostReadNoneThrowsEngineError(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)
	bytes := hexDecode("5c2a04686f73740000000000000c40")
	vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes, readNone{})
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	v, okV := readOnce(t, vd, r.ctx, tc)
	_ = vd.Close()
	described := jsonValue(jsonNull{})
	if okV {
		described = describeValue(t, r, v)
	}
	got := obj(
		kv("read", described),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("message", s(caughtMessage(t, r, tc))),
	)
	want := obj(
		kv("read", jsonNull{}),
		kv("caught", b(true)),
		kv("message", s("Uncaught Error: Unable to deserialize cloned data.")),
	)
	return wantGot("serdel/host_read_none_throws_engine_error", want, got)
}

// --- 8. write_host_object false result ignored -------------------------------------

// writeHostObjectFalseResultIgnored pins the release-build semantics: the
// delegate's bool result is ignored once no exception is pending, so the
// write "succeeds" with just the kHostObject tag on the wire.
func writeHostObjectFalseResultIgnored(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 4)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	ta, err := gov8.NewUint8Array(r.scope, r.ctx, ab, 0, 4)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}

	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, hostWriteDeny{counts})
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
		t.Fatalf("SetTreatArrayBufferViewsAsHostObjects: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, ta.Value, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("counts", countsJSON(counts)),
	)
	want := obj(
		kv("ok", b(true)),
		kv("wire", s("5c")),
		kv("caught", b(false)),
		kv("counts", obj(
			kv("has_custom_host_object", i(0)),
			kv("is_host_object", i(0)),
			kv("write_host_object", i(1)),
			kv("read_host_object", i(0)),
			kv("get_shared_array_buffer_id", i(0)),
			kv("get_shared_array_buffer_from_id", i(0)),
			kv("get_wasm_module_transfer_id", i(0)),
			kv("get_wasm_module_from_id", i(0)),
			kv("throw_data_clone_error", i(0)),
		)),
	)
	return wantGot("serdel/write_host_object_false_result_ignored", want, got)
}

// --- 9. clone-error delegate without rethrow ----------------------------------------

// cloneErrorDelegateWithoutRethrow pins the "rethrow=false" completion: the
// write fails but no exception is pending - the embedder decides whether a
// data-clone failure surfaces as a JS Error.
func cloneErrorDelegateWithoutRethrow(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	f, ok := r.eval(t, tc, "() => 1")
	if !ok {
		t.Fatal("eval function failed")
	}
	delegate, cloneError := newSerBase(counts, false)
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, f, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("clone_error_called_with", s(*cloneError)),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("caught_message", s(caughtMessage(t, r, tc))),
		kv("throw_calls", i(int64(counts.throwDataCloneError))),
	)
	want := obj(
		kv("ok", b(false)),
		kv("wire", s("")),
		kv("clone_error_called_with", s("() => 1 could not be cloned.")),
		kv("caught", b(false)),
		kv("caught_message", s("")),
		kv("throw_calls", i(1)),
	)
	return wantGot("serdel/clone_error_delegate_without_rethrow", want, got)
}

// --- 10. write_host_object throwing its own exception --------------------------------

// cloneErrorWithCustomHostException pins: a write_host_object that throws
// its OWN exception propagates it to the TryCatch verbatim;
// ThrowDataCloneError is NOT involved; the tag byte stays on the partial
// wire.
func cloneErrorWithCustomHostException(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 4)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	ta, err := gov8.NewUint8Array(r.scope, r.ctx, ab, 0, 4)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}

	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx,
		hostWriteCustomThrow{counts: counts})
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
		t.Fatalf("SetTreatArrayBufferViewsAsHostObjects: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, ta.Value, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("clone_error_called_with", s("")),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("caught_message", s(caughtMessage(t, r, tc))),
		kv("counts", countsJSON(counts)),
	)
	want := obj(
		kv("ok", b(false)),
		kv("wire", s("5c")),
		kv("clone_error_called_with", s("")),
		kv("caught", b(true)),
		kv("caught_message", s("Uncaught RangeError: host serialization refused")),
		kv("counts", obj(
			kv("has_custom_host_object", i(0)),
			kv("is_host_object", i(0)),
			kv("write_host_object", i(1)),
			kv("read_host_object", i(0)),
			kv("get_shared_array_buffer_id", i(0)),
			kv("get_shared_array_buffer_from_id", i(0)),
			kv("get_wasm_module_transfer_id", i(0)),
			kv("get_wasm_module_from_id", i(0)),
			kv("throw_data_clone_error", i(0)),
		)),
	)
	return wantGot("serdel/clone_error_with_custom_host_exception", want, got)
}

// --- 11. SAB write with a custom id --------------------------------------------------

// sabWriteCustomID pins: the id lands on the wire as 'u' + varint; the
// source SAB is untouched.
func sabWriteCustomID(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)

	bs, err := r.iso.NewSharedArrayBufferBackingStore(8)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	if _, err := bs.WriteAt([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(r.scope, r.ctx, bs)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
	}

	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, sabIDCustom{})
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, sab.Value, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	sourceLen, _ := sab.ByteLength()
	got := obj(
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("source_byte_length", i(int64(sourceLen))),
		kv("source_contents", s(lowerHex(backingStoreBytes(t, bs, 8)))),
	)
	want := obj(
		kv("ok", b(true)),
		// kSharedArrayBuffer 'u' + varint(42).
		kv("wire", s("752a")),
		kv("source_byte_length", i(8)),
		kv("source_contents", s("0102030405060708")),
	)
	return wantGot("serdel/sab_write_custom_id", want, got)
}

// --- 12. default SAB None is rejected -------------------------------------------------

// sabWriteDefaultNoneIsRejected pins: the pinned build REJECTS a
// Nothing-completion of get_shared_array_buffer_id - V8 throws its OWN
// kDataCloneError (interpolating the SAB) DIRECTLY, not routed through the
// delegate's ThrowDataCloneError (count stays 0).
func sabWriteDefaultNoneIsRejected(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	bs, err := r.iso.NewSharedArrayBufferBackingStore(8)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(r.scope, r.ctx, bs)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
	}

	delegate, _ := newSerBase(counts, true)
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, sab.Value, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("caught_message", s(caughtMessage(t, r, tc))),
		kv("counts", zeroCountsJSON()),
	)
	want := obj(
		kv("ok", b(false)),
		kv("wire", s("")),
		kv("caught", b(true)),
		kv("caught_message", s("Uncaught Error: #<SharedArrayBuffer> could not be cloned.")),
		kv("counts", zeroCountsJSON()),
	)
	return wantGot("serdel/sab_write_default_none_is_rejected", want, got)
}

// --- 13. SAB read roundtrip -------------------------------------------------------------

// sabReadRoundtrip pins: get_shared_array_buffer_from_id supplies the SAB
// registered under the wire id; the returned value IS a shared buffer.
func sabReadRoundtrip(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	observedID := new(uint32)
	tc := newTC(t, r)
	defer closeTC(t, tc)

	bytes := hexDecode("752a")
	vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes,
		sabFromIDRoundtrip{counts: counts, iso: r.iso, scope: r.scope,
			ctx: r.ctx, observedID: observedID})
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	v, okV := readOnce(t, vd, r.ctx, tc)
	_ = vd.Close()
	described := jsonValue(jsonNull{})
	if okV {
		described = describeValue(t, r, v)
	}

	got := obj(
		kv("read", described),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("message", s(caughtMessage(t, r, tc))),
		kv("observed_id", i(int64(*observedID))),
		kv("sab_hook_calls", i(int64(counts.getSharedArrayBufferFromID))),
	)
	want := obj(
		kv("read", obj(
			kv("type", s("sharedarraybuffer")),
			kv("byte_length", i(4)),
			kv("contents", s("05060708")),
		)),
		kv("caught", b(false)),
		kv("message", s("")),
		kv("observed_id", i(42)),
		kv("sab_hook_calls", i(1)),
	)
	return wantGot("serdel/sab_read_roundtrip", want, got)
}

// --- 14. default SAB-from-id error --------------------------------------------------------

// sabReadDefaultError pins the deterministic "not implemented" Error on a
// kSharedArrayBuffer payload with an empty delegate.
func sabReadDefaultError(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)
	bytes := hexDecode("752a")
	vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes, nil)
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	v, okV := readOnce(t, vd, r.ctx, tc)
	_ = vd.Close()
	described := jsonValue(jsonNull{})
	if okV {
		described = describeValue(t, r, v)
	}
	got := obj(
		kv("read", described),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("message", s(caughtMessage(t, r, tc))),
	)
	want := obj(
		kv("read", jsonNull{}),
		kv("caught", b(true)),
		kv("message", s("Uncaught Error: Deno deserializer: get_shared_array_buffer_from_id not implemented")),
	)
	return wantGot("serdel/sab_read_default_error", want, got)
}

// --- 15. SAB-from-id None -> engine error ---------------------------------------------------

// sabReadNoneThrowsEngineError pins the same engine error for a silent
// None from the SAB-from-id hook.
func sabReadNoneThrowsEngineError(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)
	bytes := hexDecode("752a")
	vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes,
		sabFromIDNone{counts: counts})
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	v, okV := readOnce(t, vd, r.ctx, tc)
	_ = vd.Close()
	described := jsonValue(jsonNull{})
	if okV {
		described = describeValue(t, r, v)
	}
	got := obj(
		kv("read", described),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("message", s(caughtMessage(t, r, tc))),
		kv("sab_hook_calls", i(int64(counts.getSharedArrayBufferFromID))),
	)
	want := obj(
		kv("read", jsonNull{}),
		kv("caught", b(true)),
		kv("message", s("Uncaught Error: Unable to deserialize cloned data.")),
		kv("sab_hook_calls", i(1)),
	)
	return wantGot("serdel/sab_read_none_throws_engine_error", want, got)
}

// --- 16. transfer registrations are not consulted ---------------------------------------------

// sabReadTransferRegistrationNotConsulted pins: transfer_shared_array_buffer
// registrations are NOT consulted by the SAB read path - the delegate hook
// is called even for a registered id, and its None surfaces the same
// deterministic engine error.
func sabReadTransferRegistrationNotConsulted(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}

	bs, err := r.iso.NewSharedArrayBufferBackingStore(4)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	if _, err := bs.WriteAt([]byte{9}, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	registered, err := gov8.NewSharedArrayBufferWithBackingStore(r.scope, r.ctx, bs)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
	}

	tc := newTC(t, r)
	defer closeTC(t, tc)
	bytes := hexDecode("752a")
	vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes,
		sabFromIDNone{counts: counts})
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	if err := vd.TransferSharedArrayBuffer(42, registered); err != nil {
		t.Fatalf("TransferSharedArrayBuffer: %v", err)
	}
	v, okV := readOnce(t, vd, r.ctx, tc)
	_ = vd.Close()
	described := jsonValue(jsonNull{})
	if okV {
		described = describeValue(t, r, v)
	}
	got := obj(
		kv("read", described),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("message", s(caughtMessage(t, r, tc))),
		kv("sab_hook_calls", i(int64(counts.getSharedArrayBufferFromID))),
	)
	want := obj(
		kv("read", jsonNull{}),
		kv("caught", b(true)),
		kv("message", s("Uncaught Error: Unable to deserialize cloned data.")),
		kv("sab_hook_calls", i(1)),
	)
	return wantGot("serdel/sab_read_transfer_registration_not_consulted", want, got)
}

// --- 17. wasm write with the default delegate --------------------------------------------

// wasmWriteDefaultDelegateError pins: serializing a WasmModuleObject with
// the DEFAULT delegate throws the deterministic "not implemented" error,
// which the write surfaces as a failure. Wasm itself is out of scope; this
// only proves the transfer-delegate hook's default path.
func wasmWriteDefaultDelegateError(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	module, ok := r.eval(t, tc, wasmEmptyModule)
	if !ok {
		// WebAssembly unavailable in this build: pin the availability fact
		// instead of the write behavior.
		got := obj(kv("wasm_available", b(false)))
		want := obj(kv("wasm_available", b(false)))
		return wantGot("serdel/wasm_write_default_delegate_error", want, got)
	}

	delegate, _ := newSerBase(counts, true)
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, module, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("wasm_available", b(true)),
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("caught_message", s(caughtMessage(t, r, tc))),
		// The wasm failure comes from the hook's own throw;
		// ThrowDataCloneError is not involved.
		kv("throw_calls", i(int64(counts.throwDataCloneError))),
	)
	want := obj(
		kv("wasm_available", b(true)),
		kv("ok", b(false)),
		kv("wire", s("")),
		kv("caught", b(true)),
		kv("caught_message", s("Uncaught Error: Deno serializer: get_wasm_module_transfer_id not implemented")),
		kv("throw_calls", i(0)),
	)
	return wantGot("serdel/wasm_write_default_delegate_error", want, got)
}

// --- 18. wasm write None silently drops the module -------------------------------------------

// wasmWriteNoneSilentlyDropsModule pins: get_wasm_module_transfer_id ->
// None (no throw) makes the module SILENTLY disappear from the wire while
// the enclosing object write succeeds.
func wasmWriteNoneSilentlyDropsModule(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	counts := &serdelCounts{}
	tc := newTC(t, r)
	defer closeTC(t, tc)

	module, ok := r.eval(t, tc, wasmEmptyModule)
	if !ok {
		got := obj(kv("wasm_available", b(false)))
		want := obj(kv("wasm_available", b(false)))
		return wantGot("serdel/wasm_write_none_silently_drops_module", want, got)
	}

	holder, ok := r.eval(t, tc, "({m: null})")
	if !ok {
		t.Fatal("eval holder failed")
	}
	if holderObj, err := gov8.AsObject(holder); err == nil {
		if _, err := holderObj.SetByName(r.scope, r.ctx, "m", module); err != nil {
			t.Fatalf("SetByName: %v", err)
		}
	}

	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, wasmIDNone{counts: counts})
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	okW := writeOnce(t, ser, r.ctx, holder, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("wasm_available", b(true)),
		kv("ok", b(okW)),
		kv("wire", s(lowerHex(wire))),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("wasm_hook_calls", i(int64(counts.getWasmModuleTransferID))),
	)
	want := obj(
		kv("wasm_available", b(true)),
		kv("ok", b(true)),
		// o "m" { 1 - key written, value ABSENT: the module was dropped.
		kv("wire", s("6f22016d7b01")),
		kv("caught", b(false)),
		kv("wasm_hook_calls", i(1)),
	)
	return wantGot("serdel/wasm_write_none_silently_drops_module", want, got)
}

// --- 19. wasm read default error -----------------------------------------------------------------

// wasmReadDefaultError pins the deterministic "not implemented" Error on a
// kWasmModuleTransfer payload ('w' + varint 21).
func wasmReadDefaultError(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)
	bytes := hexDecode("7715")
	vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes, nil)
	if err != nil {
		t.Fatalf("NewDelegateValueDeserializer: %v", err)
	}
	v, okV := readOnce(t, vd, r.ctx, tc)
	_ = vd.Close()
	described := jsonValue(jsonNull{})
	if okV {
		described = describeValue(t, r, v)
	}
	got := obj(
		kv("read", described),
		kv("caught", b(mustHasCaught(t, tc))),
		kv("message", s(caughtMessage(t, r, tc))),
	)
	want := obj(
		kv("read", jsonNull{}),
		kv("caught", b(true)),
		kv("message", s("Uncaught Error: Deno deserializer: get_wasm_module_from_id not implemented")),
	)
	return wantGot("serdel/wasm_read_default_error", want, got)
}

// --- 20. writer-side transfer re-registration ------------------------------------------------------

// transferWriterReregisterSameBuffer pins: re-registering the SAME buffer
// under a new id replaces its mapping (last registration wins on write).
func transferWriterReregisterSameBuffer(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)

	bs, err := r.iso.NewBackingStoreFromSlice([]byte{1, 1, 1, 1})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	defer func() { _ = bs.Close() }()
	ab, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, bs)
	if err != nil {
		t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
	}

	delegate, _ := newSerBase(&serdelCounts{}, true)
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	if err := ser.TransferArrayBuffer(7, ab); err != nil {
		t.Fatalf("TransferArrayBuffer 7: %v", err)
	}
	if err := ser.TransferArrayBuffer(9, ab); err != nil {
		t.Fatalf("TransferArrayBuffer 9: %v", err)
	}
	holder, ok := r.eval(t, tc, "({x: null})")
	if !ok {
		t.Fatal("eval holder failed")
	}
	if holderObj, err := gov8.AsObject(holder); err == nil {
		if _, err := holderObj.SetByName(r.scope, r.ctx, "x", ab.Value); err != nil {
			t.Fatalf("SetByName: %v", err)
		}
	}
	okW := writeOnce(t, ser, r.ctx, holder, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(kv("ok", b(okW)), kv("wire", s(lowerHex(wire))))
	want := obj(
		kv("ok", b(true)),
		// o "x" t varint(9) { 1 - id 9 replaced id 7 for the same buffer.
		kv("wire", s("6f22017874097b01")),
	)
	return wantGot("serdel/transfer_writer_reregister_same_buffer", want, got)
}

// --- 21. reader-side transfer collisions --------------------------------------------------------------

// transferCollisionReaderAliasLastWins pins: two DIFFERENT buffers written
// under the SAME id alias to the one registered target; re-registering the
// id on the reader replaces the target (last registration wins on read).
func transferCollisionReaderAliasLastWins(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	var wireHex string
	func() {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		bs1, err := r.iso.NewBackingStoreFromSlice([]byte{1, 1, 1, 1})
		if err != nil {
			t.Fatalf("bs1: %v", err)
		}
		ab1, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, bs1)
		if err != nil {
			t.Fatalf("ab1: %v", err)
		}
		bs2, err := r.iso.NewBackingStoreFromSlice([]byte{2, 2, 2, 2})
		if err != nil {
			t.Fatalf("bs2: %v", err)
		}
		ab2, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, bs2)
		if err != nil {
			t.Fatalf("ab2: %v", err)
		}
		delegate, _ := newSerBase(&serdelCounts{}, true)
		ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
		if err != nil {
			t.Fatalf("NewDelegateValueSerializer: %v", err)
		}
		// Collision: both buffers share transfer id 7.
		if err := ser.TransferArrayBuffer(7, ab1); err != nil {
			t.Fatalf("TransferArrayBuffer ab1: %v", err)
		}
		if err := ser.TransferArrayBuffer(7, ab2); err != nil {
			t.Fatalf("TransferArrayBuffer ab2: %v", err)
		}
		holder, ok := r.eval(t, tc, "({a: null, b: null})")
		if !ok {
			t.Fatal("eval holder failed")
		}
		if holderObj, err := gov8.AsObject(holder); err == nil {
			if _, err := holderObj.SetByName(r.scope, r.ctx, "a", ab1.Value); err != nil {
				t.Fatalf("SetByName a: %v", err)
			}
			if _, err := holderObj.SetByName(r.scope, r.ctx, "b", ab2.Value); err != nil {
				t.Fatalf("SetByName b: %v", err)
			}
		}
		if !writeOnce(t, ser, r.ctx, holder, tc) {
			t.Fatal("collision wire must serialize")
		}
		wire, rerr := ser.Release()
		if rerr != nil {
			t.Fatalf("Release: %v", rerr)
		}
		_ = ser.Close()
		_ = bs1.Close()
		_ = bs2.Close()
		wireHex = lowerHex(wire)
	}()

	// Read with TWO registrations for id 7 (t1 then t2): the last must win
	// and both properties must alias that one target.
	var read jsonValue
	var readCaught bool
	func() {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		tbs, err := r.iso.NewBackingStoreFromSlice(make([]byte, 4))
		if err != nil {
			t.Fatalf("t1 store: %v", err)
		}
		t1, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, tbs)
		if err != nil {
			t.Fatalf("t1: %v", err)
		}
		t2bs, err := r.iso.NewBackingStoreFromSlice([]byte{3, 3, 3, 3})
		if err != nil {
			t.Fatalf("t2 store: %v", err)
		}
		t2, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, t2bs)
		if err != nil {
			t.Fatalf("t2: %v", err)
		}

		bytes := hexDecode(wireHex)
		vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes, nil)
		if err != nil {
			t.Fatalf("NewDelegateValueDeserializer: %v", err)
		}
		if err := vd.TransferArrayBuffer(7, t1); err != nil {
			t.Fatalf("TransferArrayBuffer t1: %v", err)
		}
		if err := vd.TransferArrayBuffer(7, t2); err != nil {
			t.Fatalf("TransferArrayBuffer t2: %v", err)
		}
		v, okV := readOnce(t, vd, r.ctx, tc)
		_ = vd.Close()

		if !okV {
			read = jsonNull{}
		} else {
			a, aOK := propArrayBuffer(t, r, v, "a")
			bv, bOK := propArrayBuffer(t, r, v, "b")
			var aIsT2, bIsT2, aIsT1 bool
			if aOK {
				aIsT2, _ = gov8.Same(a.Value, t2.Value)
				aIsT1, _ = gov8.Same(a.Value, t1.Value)
			}
			if bOK {
				bIsT2, _ = gov8.Same(bv.Value, t2.Value)
			}
			t2Len, _ := t2.ByteLength()
			read = obj(
				kv("a_is_t2", b(aOK && aIsT2)),
				kv("b_is_t2", b(bOK && bIsT2)),
				kv("a_is_t1", b(aOK && aIsT1)),
				kv("t2_byte_length", i(int64(t2Len))),
			)
		}
		readCaught = mustHasCaught(t, tc)
		_ = tbs.Close()
		_ = t2bs.Close()
	}()

	got := obj(
		kv("wire", s(wireHex)),
		kv("read", read),
		kv("read_caught", b(readCaught)),
	)
	want := obj(
		// o "a" t 7 "b" t 7 { 2.
		kv("wire", s("6f220161740722016274077b02")),
		kv("read", obj(
			kv("a_is_t2", b(true)),
			kv("b_is_t2", b(true)),
			kv("a_is_t1", b(false)),
			kv("t2_byte_length", i(4)),
		)),
		kv("read_caught", b(false)),
	)
	return wantGot("serdel/transfer_collision_reader_alias_last_wins", want, got)
}

// --- 22. realloc growth with a large payload -------------------------------------------------------------

// reallocGrowthLargePayloadHashed pins output-buffer growth through the
// delegate's ReallocateBufferMemory: a 256 KiB payload forces several
// reallocations; Release hands back exactly the written bytes (ownership
// transfer, contents intact, FNV-1a digest).
func reallocGrowthLargePayloadHashed(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)

	payload := make([]byte, 262144)
	for i := uint32(0); i < 262144; i++ {
		payload[i] = byte((i*31 + 7) & 0xff)
	}

	delegate, _ := newSerBase(&serdelCounts{}, true)
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	if err := ser.WriteUint32(1); err != nil {
		t.Fatalf("WriteUint32: %v", err)
	}
	if err := ser.WriteRawBytes(payload); err != nil {
		t.Fatalf("WriteRawBytes: %v", err)
	}
	if err := ser.WriteUint32(2); err != nil {
		t.Fatalf("WriteUint32 2: %v", err)
	}
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("len", i(int64(len(wire)))),
		kv("fnv1a", s(fnv1aHex(wire))),
		kv("first16", s(lowerHex(wire[:16]))),
		kv("last16", s(lowerHex(wire[len(wire)-16:]))),
	)
	want := obj(
		// varint(1) + 262144 payload bytes + varint(2).
		kv("len", i(262_146)),
		kv("fnv1a", s("879dea323af0902a")),
		kv("first16", s("010726456483a2c1e0ff1e3d5c7b9ab9")),
		kv("last16", s("36557493b2d1f00f2e4d6c8baac9e802")),
	)
	return wantGot("serdel/realloc_growth_large_payload_hashed", want, got)
}

// --- 23. release ownership and drop paths -----------------------------------------------------------------

// releaseOwnershipDropPaths pins: Release consumes the buffer (a second
// release is empty); dropping (Close) WITHOUT releasing frees through the
// delegate's FreeBufferMemory; both paths leave the engine healthy.
func releaseOwnershipDropPaths(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)

	delegate, _ := newSerBase(&serdelCounts{}, true)
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	if err := ser.WriteUint32(7); err != nil {
		t.Fatalf("WriteUint32: %v", err)
	}
	first, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	second, rerr := ser.Release() // fixture-pinned: empty, no error
	if rerr != nil {
		t.Fatalf("second Release: %v", rerr)
	}
	if err := ser.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Close WITHOUT release: ~1 KiB pending, freed by the destructor path.
	func() {
		delegate2, _ := newSerBase(&serdelCounts{}, true)
		ser2, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate2)
		if err != nil {
			t.Fatalf("NewDelegateValueSerializer 2: %v", err)
		}
		if err := ser2.WriteRawBytes(make([]byte, 1024)); err != nil {
			t.Fatalf("WriteRawBytes: %v", err)
		}
		_ = ser2.Close()
	}()

	// Engine still healthy afterwards.
	afterHex := func() string {
		delegate3, _ := newSerBase(&serdelCounts{}, true)
		ser3, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate3)
		if err != nil {
			t.Fatalf("NewDelegateValueSerializer 3: %v", err)
		}
		if err := ser3.WriteUint32(5); err != nil {
			t.Fatalf("WriteUint32: %v", err)
		}
		wire, rerr := ser3.Release()
		if rerr != nil {
			t.Fatalf("Release 3: %v", rerr)
		}
		_ = ser3.Close()
		return lowerHex(wire)
	}()

	got := obj(
		kv("first_len", i(int64(len(first)))),
		kv("first_hex", s(lowerHex(first))),
		kv("second_len", i(int64(len(second)))),
		kv("after_hex", s(afterHex)),
	)
	want := obj(
		kv("first_len", i(1)),
		kv("first_hex", s("07")),
		kv("second_len", i(0)),
		kv("after_hex", s("05")),
	)
	return wantGot("serdel/release_ownership_drop_paths", want, got)
}

// --- 24. serializer state after a clone error ---------------------------------------------------------------

// serializerStateAfterCloneError pins: after a data-clone error the failed
// object already occupies id-map space, so a LATER write of the same
// object emits a bare '^' reference to a never-written id (a serialization
// hazard to mirror), while fresh objects keep working.
func serializerStateAfterCloneError(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	tc := newTC(t, r)
	defer closeTC(t, tc)

	f, ok := r.eval(t, tc, "() => 1")
	if !ok {
		t.Fatal("eval function failed")
	}
	fresh, ok := r.eval(t, tc, "({})")
	if !ok {
		t.Fatal("eval fresh failed")
	}

	delegate, _ := newSerBase(&serdelCounts{}, true)
	ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
	if err != nil {
		t.Fatalf("NewDelegateValueSerializer: %v", err)
	}
	okFunction := writeOnce(t, ser, r.ctx, f, tc)
	okFreshObject := writeOnce(t, ser, r.ctx, fresh, tc)
	okFunctionAgain := writeOnce(t, ser, r.ctx, f, tc)
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	_ = ser.Close()

	got := obj(
		kv("ok_function", b(okFunction)),
		kv("ok_fresh_object", b(okFreshObject)),
		kv("ok_function_again", b(okFunctionAgain)),
		kv("wire", s(lowerHex(wire))),
	)
	want := obj(
		kv("ok_function", b(false)),
		kv("ok_fresh_object", b(true)),
		kv("ok_function_again", b(true)),
		// {} wire + '^' reference (id 1 -> varint 0) for the failed object.
		kv("wire", s("6f7b005e00")),
	)
	return wantGot("serdel/serializer_state_after_clone_error", want, got)
}

// --- 25. read_header and wire format version ------------------------------------------------------------------

// readHeaderAndWireFormatVersion pins: a version-16 payload read
// header-first succeeds; the same bytes read WITHOUT ReadHeader are parsed
// as legacy version 0 and fail deterministically (the header bytes are
// consumed as tags instead, reaching the host-object path).
func readHeaderAndWireFormatVersion(t *testing.T) obs {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)

	var wireHex string
	func() {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		value, ok := r.eval(t, tc, "true")
		if !ok {
			t.Fatal("eval true failed")
		}
		delegate, _ := newSerBase(&serdelCounts{}, true)
		ser, err := gov8.NewDelegateValueSerializer(r.scope, r.ctx, delegate)
		if err != nil {
			t.Fatalf("NewDelegateValueSerializer: %v", err)
		}
		if err := ser.WriteHeader(); err != nil {
			t.Fatalf("WriteHeader: %v", err)
		}
		if !writeOnce(t, ser, r.ctx, value, tc) {
			t.Fatal("write must succeed")
		}
		wire, rerr := ser.Release()
		if rerr != nil {
			t.Fatalf("Release: %v", rerr)
		}
		_ = ser.Close()
		wireHex = lowerHex(wire)
	}()

	var headerOK bool
	var version int64
	var headerRead jsonValue
	func() {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		bytes := hexDecode(wireHex)
		vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes, nil)
		if err != nil {
			t.Fatalf("NewDelegateValueDeserializer: %v", err)
		}
		header, herr := vd.ReadHeader(r.ctx, tc)
		if herr != nil {
			headerOK = false
		} else {
			headerOK = header
		}
		if ver, verr := vd.GetWireFormatVersion(); verr == nil {
			version = int64(ver)
		}
		v, okV := readOnce(t, vd, r.ctx, tc)
		_ = vd.Close()
		if okV {
			headerRead = describeValue(t, r, v)
		} else {
			headerRead = jsonNull{}
		}
	}()

	var noHeaderRead jsonValue
	var noHeaderCaught bool
	var noHeaderMessage string
	func() {
		tc := newTC(t, r)
		defer closeTC(t, tc)
		bytes := hexDecode(wireHex)
		vd, err := gov8.NewDelegateValueDeserializer(r.scope, r.ctx, bytes, nil)
		if err != nil {
			t.Fatalf("NewDelegateValueDeserializer 2: %v", err)
		}
		v, okV := readOnce(t, vd, r.ctx, tc)
		_ = vd.Close()
		if okV {
			noHeaderRead = describeValue(t, r, v)
		} else {
			noHeaderRead = jsonNull{}
		}
		noHeaderCaught = mustHasCaught(t, tc)
		noHeaderMessage = caughtMessage(t, r, tc)
	}()

	got := obj(
		kv("wire", s(wireHex)),
		kv("with_header_ok", b(headerOK)),
		kv("with_header_version", i(version)),
		kv("with_header_read", headerRead),
		kv("without_header_read", noHeaderRead),
		kv("without_header_caught", b(noHeaderCaught)),
		kv("without_header_message", s(noHeaderMessage)),
	)
	want := obj(
		// kVersion tag + varint(16) + 'T'.
		kv("wire", s("ff1054")),
		kv("with_header_ok", b(true)),
		kv("with_header_version", i(16)),
		kv("with_header_read", obj(kv("type", s("boolean")), kv("value", b(true)))),
		kv("without_header_read", jsonNull{}),
		kv("without_header_caught", b(true)),
		kv("without_header_message", s("Uncaught Error: Deno deserializer: read_host_object not implemented")),
	)
	return wantGot("serdel/read_header_and_wire_format_version", want, got)
}

// --- registry ---------------------------------------------------------------------------------------------

type serdelCheck struct {
	id string
	fn func(t *testing.T) obs
}

// allSerdelChecks is the fixed oracle registry order
// (conformance-serializer-delegates.rs CHECKS), all 25 checks.
func allSerdelChecks() []serdelCheck {
	return []serdelCheck{
		{"serdel/detection_denies_all_hosts", detectionDeniesAllHosts},
		{"serdel/detection_embedder_fields_without_custom", detectionEmbedderFieldsWithoutCustom},
		{"serdel/detection_admits_host_routes_to_write", detectionAdmitsHostRoutesToWrite},
		{"serdel/host_write_read_roundtrip", hostWriteReadRoundtrip},
		{"serdel/host_default_write_error_partial_wire", hostDefaultWriteErrorPartialWire},
		{"serdel/host_read_default_error", hostReadDefaultError},
		{"serdel/host_read_none_throws_engine_error", hostReadNoneThrowsEngineError},
		{"serdel/write_host_object_false_result_ignored", writeHostObjectFalseResultIgnored},
		{"serdel/clone_error_delegate_without_rethrow", cloneErrorDelegateWithoutRethrow},
		{"serdel/clone_error_with_custom_host_exception", cloneErrorWithCustomHostException},
		{"serdel/sab_write_custom_id", sabWriteCustomID},
		{"serdel/sab_write_default_none_is_rejected", sabWriteDefaultNoneIsRejected},
		{"serdel/sab_read_roundtrip", sabReadRoundtrip},
		{"serdel/sab_read_default_error", sabReadDefaultError},
		{"serdel/sab_read_none_throws_engine_error", sabReadNoneThrowsEngineError},
		{"serdel/sab_read_transfer_registration_not_consulted", sabReadTransferRegistrationNotConsulted},
		{"serdel/wasm_write_default_delegate_error", wasmWriteDefaultDelegateError},
		{"serdel/wasm_write_none_silently_drops_module", wasmWriteNoneSilentlyDropsModule},
		{"serdel/wasm_read_default_error", wasmReadDefaultError},
		{"serdel/transfer_writer_reregister_same_buffer", transferWriterReregisterSameBuffer},
		{"serdel/transfer_collision_reader_alias_last_wins", transferCollisionReaderAliasLastWins},
		{"serdel/realloc_growth_large_payload_hashed", reallocGrowthLargePayloadHashed},
		{"serdel/release_ownership_drop_paths", releaseOwnershipDropPaths},
		{"serdel/serializer_state_after_clone_error", serializerStateAfterCloneError},
		{"serdel/read_header_and_wire_format_version", readHeaderAndWireFormatVersion},
	}
}

// serdelCheckIDs lists the registry ids in the same order.
func serdelCheckIDs() []string {
	ids := make([]string, 0, 25)
	for _, c := range allSerdelChecks() {
		ids = append(ids, c.id)
	}
	return ids
}
