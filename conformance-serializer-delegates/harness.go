//go:build windows && amd64

// Shared harness for the conformance-serializer-delegates checks: the
// runtime triple, the eval helper, the hook counters, the normalized value
// description, and the delegate implementations — one focused struct per
// hook-behavior variant, mirroring the local helpers and delegate structs
// of rust-oracle/src/bin/conformance-serializer-delegates.rs one for one.
package main

import (
	gov8 "gov8"
)

// obs packages one check's normalized observation (mirrors the oracle's
// report::CheckOutcome).
type obs struct {
	id   string
	val  jsonValue
	want jsonValue
	fail bool
}

// wantGot folds expectation and observation into one outcome: pass when
// the canonical encodings are byte-identical, otherwise a diffable
// failure.
func wantGot(id string, want, got jsonValue) obs {
	if jsonString(want) == jsonString(got) {
		return obs{id: id, val: got}
	}
	return obs{id: id, val: got, want: want, fail: true}
}

// line renders the normalized JSON-lines encoding of the outcome, exactly
// like rust-oracle/src/report.rs CheckOutcome::to_line.
func (o obs) line() string {
	if !o.fail {
		return "{\"check\":\"" + o.id + "\",\"ok\":true,\"value\":" + jsonString(o.val) + "}"
	}
	return "{\"check\":\"" + o.id + "\",\"ok\":false,\"expected\":" +
		jsonString(o.want) + ",\"actual\":" + jsonString(o.val) + "}"
}

// runtime is one isolate+context+scope triple, as used by every oracle
// check (fresh isolate per check; the oracle binary does the same).
type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t tester) *runtime {
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
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtime) close(t tester) {
	t.Helper()
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// eval compiles and runs source under the given TryCatch (nil allowed),
// returning the completion value (ok=false on compile or runtime failure).
func (r *runtime) eval(t tester, tc *gov8.TryCatch, source string) (gov8.Value, bool) {
	t.Helper()
	script, cerr := r.ctx.Compile(r.scope, source, tc)
	if cerr != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(r.scope, tc)
	if rerr != nil {
		return gov8.Value{}, false
	}
	return v, true
}

// caughtMessage is the oracle's caught_message! macro: the TryCatch message
// text ("" when nothing was caught).
func caughtMessage(t tester, r *runtime, tc *gov8.TryCatch) string {
	t.Helper()
	caught, err := tc.HasCaught()
	if err != nil || !caught {
		return ""
	}
	msg, err := tc.MessageText(r.scope, r.ctx)
	if err != nil {
		return ""
	}
	return msg
}

// fnv1aHex is FNV-1a (64-bit) rendered as 16 lowercase hex chars (big-endian
// byte order, matching Rust's hash.to_be_bytes() hex): the compact
// deterministic digest of the large-payload ownership check.
func fnv1aHex(bytes []byte) string {
	const offset64 = 0xcbf29ce484222325
	const prime64 = 0x00000100000001b3
	hash := uint64(offset64)
	for _, v := range bytes {
		hash ^= uint64(v)
		hash *= prime64
	}
	const digits = "0123456789abcdef"
	var out [16]byte
	for idx := 7; idx >= 0; idx-- {
		b := byte(hash)
		out[idx*2] = digits[b>>4]
		out[idx*2+1] = digits[b&0x0f]
		hash >>= 8
	}
	return string(out[:])
}

// tester is the subset of *testing.T used by the checks.
type tester interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
}

// --- hook counters -------------------------------------------------------------

// serdelCounts is the oracle's Counts: delegate hook counters shared
// between a check and its delegate structs.
type serdelCounts struct {
	hasCustomHostObject        int
	isHostObject               int
	writeHostObject            int
	readHostObject             int
	getSharedArrayBufferID     int
	getSharedArrayBufferFromID int
	getWasmModuleTransferID    int
	getWasmModuleFromID        int
	throwDataCloneError        int
}

// countsJSON renders the counters with the oracle's fixed key order.
func countsJSON(c *serdelCounts) jsonValue {
	return obj(
		kv("has_custom_host_object", i(int64(c.hasCustomHostObject))),
		kv("is_host_object", i(int64(c.isHostObject))),
		kv("write_host_object", i(int64(c.writeHostObject))),
		kv("read_host_object", i(int64(c.readHostObject))),
		kv("get_shared_array_buffer_id", i(int64(c.getSharedArrayBufferID))),
		kv("get_shared_array_buffer_from_id", i(int64(c.getSharedArrayBufferFromID))),
		kv("get_wasm_module_transfer_id", i(int64(c.getWasmModuleTransferID))),
		kv("get_wasm_module_from_id", i(int64(c.getWasmModuleFromID))),
		kv("throw_data_clone_error", i(int64(c.throwDataCloneError))),
	)
}

// zeroCountsJSON is the all-zero counter object several default-path checks
// pin (trait defaults are un-instrumentable).
func zeroCountsJSON() jsonValue {
	return countsJSON(&serdelCounts{})
}

// --- delegate implementations --------------------------------------------------

// serBase is the minimum delegate: ThrowDataCloneError records the message
// and optionally re-throws it as a JS Error (the shim-side rebuild; the
// canonical structured-clone behavior). All other hooks keep their trait
// defaults.
type serBase struct {
	counts     *serdelCounts
	rethrow    bool
	cloneError *string
}

func newSerBase(counts *serdelCounts, rethrow bool) (serBase, *string) {
	slot := new(string)
	return serBase{counts: counts, rethrow: rethrow, cloneError: slot}, slot
}

func (d serBase) ThrowDataCloneError(message string) bool {
	d.counts.throwDataCloneError++
	*d.cloneError = message
	return d.rethrow
}

// denyAllHosts is detection variant A: claims custom host objects, denies
// them all (is_host_object -> Some(false)); everything takes the native
// path.
type denyAllHosts struct {
	counts *serdelCounts
}

func (d denyAllHosts) ThrowDataCloneError(string) bool { return true }

func (d denyAllHosts) HasCustomHostObject() bool {
	d.counts.hasCustomHostObject++
	return true
}

func (d denyAllHosts) IsHostObject(*gov8.Object) (bool, bool) {
	d.counts.isHostObject++
	return false, true
}

// admitAllHosts is detection variant B: claims every object as a host
// object and writes a single varint byte from inside write_host_object.
type admitAllHosts struct {
	counts *serdelCounts
}

func (d admitAllHosts) ThrowDataCloneError(string) bool { return true }

func (d admitAllHosts) HasCustomHostObject() bool {
	d.counts.hasCustomHostObject++
	return true
}

func (d admitAllHosts) IsHostObject(*gov8.Object) (bool, bool) {
	d.counts.isHostObject++
	return true, true
}

func (d admitAllHosts) WriteHostObject(_ *gov8.Object, w *gov8.DelegateValueSerializer) (bool, bool) {
	d.counts.writeHostObject++
	if err := w.WriteUint32(7); err != nil {
		return false, false
	}
	return true, true
}

// hostWriteCodec is the host-object codec, write side (used with the
// treat-views flag): writes uint32(42) | raw("host") | double(3.5) and
// records whether the deferred object was a typed array.
type hostWriteCodec struct {
	counts        *serdelCounts
	sawTypedArray *bool
}

func (d hostWriteCodec) ThrowDataCloneError(string) bool { return true }

func (d hostWriteCodec) WriteHostObject(object *gov8.Object, w *gov8.DelegateValueSerializer) (bool, bool) {
	d.counts.writeHostObject++
	isTA, err := object.IsTypedArray()
	if err != nil {
		return false, false
	}
	*d.sawTypedArray = isTA
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

// hostReadCodec is the host-object codec, read side: consumes exactly the
// bytes written by hostWriteCodec via the helper read primitives and
// rebuilds a host object {kind: "host", n: 42}.
type hostReadCodec struct {
	counts      *serdelCounts
	readU32     *int
	readRaw     *int
	readF64     *int
	wireVersion *uint32
}

func (d hostReadCodec) ReadHostObject(r *gov8.DelegateValueDeserializer) (*gov8.Object, bool) {
	d.counts.readHostObject++
	if ver, err := r.GetWireFormatVersion(); err == nil {
		*d.wireVersion = ver
	}
	magic, gotU32, err := r.ReadUint32()
	if err != nil {
		return nil, false
	}
	*d.readU32++
	raw, gotRaw, err := r.ReadRawBytes(4)
	if err != nil {
		return nil, false
	}
	*d.readRaw++
	dd, gotF64, err := r.ReadDouble()
	if err != nil {
		return nil, false
	}
	*d.readF64++
	if !gotU32 || magic != 42 || !gotF64 || dd != 3.5 {
		return nil, false
	}
	if !gotRaw || string(raw) != "host" {
		return nil, false
	}
	obj, err := r.Scope().NewObject(r.Context())
	if err != nil {
		return nil, false
	}
	kindVal, err := r.Scope().NewString("host")
	if err != nil {
		return nil, false
	}
	if _, err := obj.SetByName(r.Scope(), r.Context(), "kind", kindVal); err != nil {
		return nil, false
	}
	nVal, err := r.Scope().Number(42)
	if err != nil {
		return nil, false
	}
	if _, err := obj.SetByName(r.Scope(), r.Context(), "n", nVal); err != nil {
		return nil, false
	}
	return obj, true
}

// hostWriteDeny is write_host_object returning Some(false) WITHOUT
// throwing and without writing anything (pins the release-build "result
// ignored" semantics).
type hostWriteDeny struct {
	counts *serdelCounts
}

func (d hostWriteDeny) ThrowDataCloneError(string) bool { return true }

func (d hostWriteDeny) WriteHostObject(*gov8.Object, *gov8.DelegateValueSerializer) (bool, bool) {
	d.counts.writeHostObject++
	return false, true
}

// hostWriteCustomThrow is write_host_object that throws its OWN RangeError
// and completes as None (the delegate-drives-the-exception path).
type hostWriteCustomThrow struct {
	counts *serdelCounts
}

func (d hostWriteCustomThrow) ThrowDataCloneError(string) bool { return true }

func (d hostWriteCustomThrow) WriteHostObject(_ *gov8.Object, w *gov8.DelegateValueSerializer) (bool, bool) {
	d.counts.writeHostObject++
	msg, err := w.NewRangeError("host serialization refused")
	if err != nil {
		return false, false
	}
	if err := w.ThrowException(msg); err != nil {
		return false, false
	}
	return false, false
}

// sabIDCustom is the SAB id hook returning a fixed id (write roundtrip).
type sabIDCustom struct{}

func (sabIDCustom) ThrowDataCloneError(string) bool { return true }

func (sabIDCustom) GetSharedArrayBufferID(*gov8.SharedArrayBuffer) (uint32, bool) {
	return 42, true
}

// wasmIDNone is the wasm transfer-id hook completing with None WITHOUT
// throwing (the "module silently dropped from the wire" path).
type wasmIDNone struct {
	counts *serdelCounts
}

func (d wasmIDNone) ThrowDataCloneError(string) bool { return true }

func (d wasmIDNone) GetWasmModuleTransferID(gov8.Value) (uint32, bool) {
	d.counts.getWasmModuleTransferID++
	return 0, false
}

// sabFromIDRoundtrip is the SAB-from-id hook returning a fresh SAB for
// id 42 (read roundtrip path). The runtime triple is captured by the check
// before the read; the hook's engine scope is still open while it runs.
type sabFromIDRoundtrip struct {
	counts     *serdelCounts
	iso        *gov8.Isolate
	scope      *gov8.Scope
	ctx        *gov8.Context
	observedID *uint32
}

func (d sabFromIDRoundtrip) GetSharedArrayBufferFromID(transferID uint32) (*gov8.SharedArrayBuffer, bool) {
	d.counts.getSharedArrayBufferFromID++
	*d.observedID = transferID
	if transferID != 42 {
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

// sabFromIDNone is the SAB-from-id hook completing with None WITHOUT
// throwing (clean read failure path; also proves registrations are not
// consulted).
type sabFromIDNone struct {
	counts *serdelCounts
}

func (d sabFromIDNone) GetSharedArrayBufferFromID(uint32) (*gov8.SharedArrayBuffer, bool) {
	d.counts.getSharedArrayBufferFromID++
	return nil, false
}

// --- normalized value description ------------------------------------------------

// describeValue is the oracle's describe_value: a type/shape description
// of a value, normalized for JSONL. Objects are probed for the fixed key
// list ["kind", "n", "a", "b", "x"].
func describeValue(t tester, r *runtime, v gov8.Value) jsonValue {
	t.Helper()
	if is, _ := v.IsUndefined(); is {
		return obj(kv("type", s("undefined")))
	}
	if is, _ := v.IsNull(); is {
		return obj(kv("type", s("null")))
	}
	if is, _ := v.IsBoolean(); is {
		bl, _ := v.BooleanValue()
		return obj(kv("type", s("boolean")), kv("value", b(bl)))
	}
	if is, _ := v.IsInt32(); is {
		n, _, _ := v.Int32Value(r.ctx)
		return obj(kv("type", s("int32")), kv("value", i(int64(n))))
	}
	if is, _ := v.IsNumber(); is {
		n, _, _ := v.NumberValue(r.ctx)
		if n == float64(int64(n)) && n < 9_000_000_000.0 {
			return obj(kv("type", s("number")), kv("value", i(int64(n))))
		}
		return obj(kv("type", s("number")), kv("value", f(n)))
	}
	if is, _ := v.IsString(); is {
		return obj(kv("type", s("string")), kv("value", s(valueText(t, r, v))))
	}
	if is, _ := v.IsSharedArrayBuffer(); is {
		if sab, err := gov8.AsSharedArrayBuffer(v); err == nil {
			length, _ := sab.ByteLength()
			bs, berr := sab.GetBackingStore()
			if berr != nil {
				t.Fatalf("GetBackingStore: %v", berr)
			}
			contents := make([]byte, length)
			if _, err := bs.ReadAt(contents, 0); err != nil {
				t.Fatalf("ReadAt: %v", err)
			}
			_ = bs.Close()
			return obj(
				kv("type", s("sharedarraybuffer")),
				kv("byte_length", i(int64(length))),
				kv("contents", s(lowerHex(contents))))
		}
	}
	if is, _ := v.IsArrayBuffer(); is {
		if ab, err := gov8.AsArrayBuffer(v); err == nil {
			length, _ := ab.ByteLength()
			bs, berr := ab.GetBackingStore()
			if berr != nil {
				t.Fatalf("GetBackingStore: %v", berr)
			}
			contents := make([]byte, length)
			if _, err := bs.ReadAt(contents, 0); err != nil {
				t.Fatalf("ReadAt: %v", err)
			}
			_ = bs.Close()
			return obj(
				kv("type", s("arraybuffer")),
				kv("byte_length", i(int64(length))),
				kv("contents", s(lowerHex(contents))))
		}
	}
	if is, _ := v.IsObject(); is {
		if o, err := gov8.AsObject(v); err == nil {
			fields := []jsonPair{kv("type", s("object"))}
			for _, key := range []string{"kind", "n", "a", "b", "x"} {
				observed := jsonValue(jsonNull{})
				if val, found, gerr := o.GetByName(r.scope, r.ctx, key); gerr == nil && found {
					observed = describeValue(t, r, val)
				}
				fields = append(fields, kv(key, observed))
			}
			return obj(fields...)
		}
	}
	return obj(kv("type", s("other")))
}

func valueText(t tester, r *runtime, v gov8.Value) string {
	t.Helper()
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// propArrayBuffer reads a property of an object value and casts it to an
// ArrayBuffer (identity-compared by the caller via gov8.Same).
func propArrayBuffer(t tester, r *runtime, v gov8.Value, key string) (*gov8.ArrayBuffer, bool) {
	t.Helper()
	obj, err := gov8.AsObject(v)
	if err != nil {
		return nil, false
	}
	prop, found, gerr := obj.GetByName(r.scope, r.ctx, key)
	if gerr != nil || !found {
		return nil, false
	}
	ab, aerr := gov8.AsArrayBuffer(prop)
	if aerr != nil {
		return nil, false
	}
	return ab, true
}

// backingStoreBytes reads a store's full contents.
func backingStoreBytes(t tester, bs *gov8.BackingStore, length int) []byte {
	t.Helper()
	contents := make([]byte, length)
	if _, err := bs.ReadAt(contents, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	return contents
}

// wasmEmptyModule is the minimal empty wasm module source: proves
// WebAssembly availability and produces a module for the transfer-hook
// checks only (Wasm itself is out of scope; no Wasm API is exposed).
const wasmEmptyModule = "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))"
