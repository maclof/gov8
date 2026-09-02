//go:build windows && amd64

// Shared harness for the conformance-buffers checks: the runtime triple,
// the eval helpers, the serializer outcome capture, and the normalized
// value descriptions. Mirrors the local helpers of
// rust-oracle/src/bin/conformance-buffers.rs one for one.
package main

import (
	gov8 "github.com/maclof/gov8"
)

// obs packages one check's normalized observation.
type obs struct {
	id   string
	val  jsonValue
	want jsonValue
	fail bool
}

// wantGot folds expectation and observation into one outcome: pass when the
// canonical encodings are byte-identical, otherwise a diffable failure.
func wantGot(id string, want, got jsonValue) obs {
	if jsonString(want) == jsonString(got) {
		return obs{id: id, val: got}
	}
	return obs{id: id, val: got, want: want, fail: true}
}

// runtime is one isolate+context+scope triple, as used by every oracle check.
type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t tester) *runtime {
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
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// eval compiles and runs source under an optional TryCatch, returning the
// completion value (ok=false on compile or runtime failure).
func (r *runtime) eval(t tester, tc *gov8.TryCatch, source string) (gov8.Value, bool) {
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

// evalText is the oracle's eval_text: ToString of the completion value
// ("" on failure).
func (r *runtime) evalText(t tester, tc *gov8.TryCatch, source string) (string, bool) {
	v, ok := r.eval(t, tc, source)
	if !ok {
		return "", false
	}
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return "", false
	}
	return txt, true
}

// valueText is the oracle's value_text: ECMAScript ToString of a value
// ("" on conversion failure).
func valueText(t tester, r *runtime, v gov8.Value) string {
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// tester is the subset of *testing.T used by the checks.
type tester interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Fatal(args ...interface{})
	Errorf(format string, args ...interface{})
}

// serOutcome is one serialize attempt: ok reports write_value success, wire
// holds the released bytes (partial output on failure), cloneError the
// message forwarded to ThrowDataCloneError ("" if never called).
type serOutcome struct {
	ok         bool
	wire       []byte
	cloneError string
}

// cloneErrorReporter is the oracle's DataCloneErrorReporter: capture the
// message handed to the delegate and re-throw it as a regular Error so a
// surrounding TryCatch observes the failure.
type cloneErrorReporter struct {
	slot *string
}

func (r cloneErrorReporter) ThrowDataCloneError(message string) bool {
	*r.slot = message
	return true
}

// serializeWith serializes value with a fresh ValueSerializer inside the
// caller's TryCatch; prep runs after construction and before write_value
// (used to register ArrayBuffer transfers and to write headers).
func serializeWith(t tester, r *runtime, tc *gov8.TryCatch, v gov8.Value, prep func(*gov8.ValueSerializer)) serOutcome {
	t.Helper()
	captured := ""
	ser, err := gov8.NewValueSerializer(r.scope, r.ctx, cloneErrorReporter{slot: &captured})
	if err != nil {
		t.Fatalf("NewValueSerializer: %v", err)
	}
	defer func() { _ = ser.Close() }()
	if prep != nil {
		prep(ser)
	}
	ok, werr := ser.WriteValue(r.ctx, v, tc)
	_ = werr
	wire, rerr := ser.Release()
	if rerr != nil {
		t.Fatalf("Release: %v", rerr)
	}
	return serOutcome{ok: ok, wire: wire, cloneError: captured}
}

func serialize(t tester, r *runtime, tc *gov8.TryCatch, v gov8.Value) serOutcome {
	return serializeWith(t, r, tc, v, nil)
}

// deserDescribe is the oracle's deser_describe! macro: deserializes bytes
// inside the caller's TryCatch and normalizes the outcome (described value
// or null, plus caught and message). bytes MUST reference a binding that
// outlives the deserializer: the engine stores the raw data pointer without
// copying (the oracle pins the same lifetime contract).
func deserDescribe(t tester, r *runtime, tc *gov8.TryCatch, data []byte, keys []string) (jsonValue, bool) {
	t.Helper()
	vd, err := gov8.NewValueDeserializer(r.scope, r.ctx, data)
	if err != nil {
		t.Fatalf("NewValueDeserializer: %v", err)
	}
	defer func() { _ = vd.Close() }()

	var described jsonValue = jsonNull{}
	v, rerr := vd.ReadValue(r.ctx, tc)
	if rerr == nil {
		described = describeValue(t, r, v, keys)
	}
	caught, _ := tc.HasCaught()
	message, _ := tc.MessageText(r.scope, r.ctx)
	return obj(
		kv("read", described),
		kv("caught", b(caught)),
		kv("message", s(message)),
	), rerr == nil
}

// describeValue is the oracle's describe_value: a type/shape description of
// a deserialized value, normalized for JSONL. keys are probed only when the
// value is a plain object.
func describeValue(t tester, r *runtime, v gov8.Value, keys []string) jsonValue {
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
		if n == float64(int64(n)) && n < 9_000_000_000.0 && n > -9_000_000_000.0 {
			return obj(kv("type", s("number")), kv("value", i(int64(n))))
		}
		return obj(kv("type", s("number")), kv("value", f(n)))
	}
	if is, _ := v.IsString(); is {
		return obj(kv("type", s("string")), kv("value", s(valueText(t, r, v))))
	}
	if is, _ := v.IsArrayBuffer(); is {
		if ab, err := gov8.AsArrayBuffer(v); err == nil {
			length, _ := ab.ByteLength()
			bs, err := ab.GetBackingStore()
			if err != nil {
				t.Fatalf("GetBackingStore: %v", err)
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
			for _, key := range keys {
				observed := jsonValue(jsonNull{})
				if val, found, gerr := o.GetByName(r.scope, r.ctx, key); gerr == nil && found {
					observed = describeValue(t, r, val, nil)
				}
				fields = append(fields, kv(key, observed))
			}
			return obj(fields...)
		}
	}
	return obj(kv("type", s("other")))
}

// useCountIs reports whether the live reference count of the store equals n
// (the readable form of the crate's assert_use_count_eq polling assertion).
func useCountIs(t tester, bs *gov8.BackingStore, n int) bool {
	t.Helper()
	got, err := bs.UseCount()
	if err != nil {
		t.Errorf("UseCount: %v", err)
		return false
	}
	return got == n
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
