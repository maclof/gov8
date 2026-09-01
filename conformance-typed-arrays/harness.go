//go:build windows && amd64

// Shared harness for the conformance-typed-arrays checks: the runtime triple,
// the eval helpers, global-object stores, and the normalized value
// descriptions. Mirrors the local helpers of
// rust-oracle/src/bin/conformance-typed-arrays.rs one for one.
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

// eval compiles and runs source, returning the completion value (ok=false on
// compile or runtime failure; every eval in this slice is expected to
// succeed).
func (r *runtime) eval(t tester, source string) (gov8.Value, bool) {
	t.Helper()
	script, cerr := r.ctx.Compile(r.scope, source, nil)
	if cerr != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(r.scope, nil)
	if rerr != nil {
		return gov8.Value{}, false
	}
	return v, true
}

// evalText is the oracle's eval_text: ToString of the completion value
// ("" on failure).
func (r *runtime) evalText(t tester, source string) string {
	t.Helper()
	v, ok := r.eval(t, source)
	if !ok {
		return ""
	}
	txt, err := v.ToString(r.ctx)
	if err != nil {
		return ""
	}
	return txt
}

// probe wraps expr in the oracle's try/catch probe: "no-error" when the
// expression completed, otherwise "Type: message".
func (r *runtime) probe(t tester, expr string) string {
	t.Helper()
	return r.evalText(t,
		"try { "+expr+"; 'no-error' } catch (e) { e.constructor.name + ': ' + e.message }")
}

// setGlobal stores value on the context's global object under name and
// reports whether the store succeeded (the oracle's set_global).
func (r *runtime) setGlobal(t tester, name string, v gov8.Value) bool {
	t.Helper()
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Errorf("GlobalObject: %v", err)
		return false
	}
	ok, err := global.SetByName(r.scope, r.ctx, name, v)
	if err != nil {
		t.Errorf("SetByName(%s): %v", name, err)
		return false
	}
	return ok
}

// tester is the subset of *testing.T used by the checks.
type tester interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Fatal(args ...interface{})
	Errorf(format string, args ...interface{})
}

// ab16 is the oracle's ab16 helper: a fresh 16-byte ArrayBuffer.
func ab16(t tester, r *runtime) *gov8.ArrayBuffer {
	t.Helper()
	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer(16): %v", err)
	}
	return ab
}

// seedStore writes bytes at offset of the buffer's backing store (the
// oracle's store[i].set(...) loops).
func seedStore(t tester, r *runtime, ab *gov8.ArrayBuffer, at int, data []byte) {
	t.Helper()
	bs, err := ab.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	if _, err := bs.WriteAt(data, at); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
}

// readStore copies n bytes at offset of the buffer's backing store (the
// oracle's store[i].get() reads).
func readStore(t tester, r *runtime, ab *gov8.ArrayBuffer, at, n int) []byte {
	t.Helper()
	bs, err := ab.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	out := make([]byte, n)
	if _, err := bs.ReadAt(out, at); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	return out
}

// typeRepr reproduces the pinned crate's Value::type_repr chain
// (rust-oracle pins its output in the fixture) over the Go predicate set.
// The chain's leading branches (module-namespace / wasm objects) precede
// Proxy and are unreachable for the values this slice observes (JS-created
// typed arrays, DataViews and ArrayBuffers are never module or wasm
// objects), so the reproduction starts at Proxy and is exact for every
// observed value, including the pinned upstream quirk: Float16Array falls
// through the per-kind chain to the generic "TypedArray" tag because the
// crate's chain has no is_float16_array branch.
func typeRepr(v gov8.Value) string {
	chain := []struct {
		pred func(gov8.Value) (bool, error)
		tag  string
	}{
		{func(x gov8.Value) (bool, error) { return x.IsProxy() }, "Proxy"},
		{func(x gov8.Value) (bool, error) { return x.IsSharedArrayBuffer() }, "SharedArrayBuffer"},
		{func(x gov8.Value) (bool, error) { return x.IsDataView() }, "DataView"},
		{func(x gov8.Value) (bool, error) { return x.IsBigUint64Array() }, "BigUint64Array"},
		{func(x gov8.Value) (bool, error) { return x.IsBigInt64Array() }, "BigInt64Array"},
		{func(x gov8.Value) (bool, error) { return x.IsFloat64Array() }, "Float64Array"},
		{func(x gov8.Value) (bool, error) { return x.IsFloat32Array() }, "Float32Array"},
		{func(x gov8.Value) (bool, error) { return x.IsInt32Array() }, "Int32Array"},
		{func(x gov8.Value) (bool, error) { return x.IsUint32Array() }, "Uint32Array"},
		{func(x gov8.Value) (bool, error) { return x.IsInt16Array() }, "Int16Array"},
		{func(x gov8.Value) (bool, error) { return x.IsUint16Array() }, "Uint16Array"},
		{func(x gov8.Value) (bool, error) { return x.IsInt8Array() }, "Int8Array"},
		{func(x gov8.Value) (bool, error) { return x.IsUint8ClampedArray() }, "Uint8ClampedArray"},
		{func(x gov8.Value) (bool, error) { return x.IsUint8Array() }, "Uint8Array"},
		{func(x gov8.Value) (bool, error) { return x.IsTypedArray() }, "TypedArray"},
		{func(x gov8.Value) (bool, error) { return x.IsArrayBufferView() }, "ArrayBufferView"},
		{func(x gov8.Value) (bool, error) { return x.IsArrayBuffer() }, "ArrayBuffer"},
	}
	for _, c := range chain {
		if is, err := c.pred(v); err == nil && is {
			return c.tag
		}
	}
	return ""
}
