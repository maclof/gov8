//go:build windows && amd64

// Shared harness for the conformance-strings-bigint checks: the runtime
// triple, the eval helpers, and the normalized byte/word encodings. Mirrors
// the local helpers of rust-oracle/src/bin/conformance-strings-bigint.rs
// one for one.
package main

import (
	"fmt"
	"strings"
	"unicode/utf16"

	gov8 "gov8"
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

// withContext opens a fresh scope+context pair on an existing isolate for
// block-scoped oracle phases (external strings surviving GC, cross-isolate
// reuse), closing both afterwards.
func withContext(t tester, iso *gov8.Isolate, fn func(r *runtime)) {
	t.Helper()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	fn(&runtime{iso: iso, ctx: ctx, scope: scope})
	if err := scope.Close(); err != nil {
		t.Errorf("scope.Close: %v", err)
	}
	if err := ctx.Close(); err != nil {
		t.Errorf("ctx.Close: %v", err)
	}
}

func closeRuntime(t tester, r *runtime) {
	t.Helper()
	for _, c := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

// eval mirrors the oracle's eval: compile and run source, returning the
// completion value (ok=false on failure).
func (r *runtime) eval(t tester, source string) (gov8.Value, bool) {
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
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

// --- normalized byte/word encodings ------------------------------------------------

// lowerHex is the oracle's hex helper: lowercase hex without separators.
func lowerHex(bytes []byte) string {
	var out strings.Builder
	for _, by := range bytes {
		fmt.Fprintf(&out, "%02x", by)
	}
	return out.String()
}

// poison fills buf with the 0xAB tail-marker pattern so write checks can
// prove the engine left everything past the written range untouched.
func poison(buf []byte) {
	for i := range buf {
		buf[i] = 0xAB
	}
}

// poisonU16 is the UTF-16 counterpart of poison (0xABAB units).
func poisonU16(buf []uint16) {
	for i := range buf {
		buf[i] = 0xABAB
	}
}

// unitsJSON encodes the first n UTF-16 units as decimal integers.
func unitsJSON(buf []uint16, n int) jsonValue {
	items := make([]jsonValue, 0, n)
	for _, u := range buf[:n] {
		items = append(items, i(int64(u)))
	}
	return arr(items...)
}

// u64JSON encodes a u64 exactly like the oracle's u64_of: plain decimal
// when it fits i64::MAX, otherwise the high/low 32-bit half object.
func u64JSON(value uint64) jsonValue {
	if value <= 1<<63-1 {
		return i(int64(value))
	}
	return obj(kv("lo", i(int64(value&0xFFFF_FFFF))), kv("hi", i(int64(value>>32))))
}

// wordsJSON encodes words through the oracle's i64 cast (u64::MAX becomes
// -1, matching the pinned Json::i(*w as i64)).
func wordsJSON(words []uint64) jsonValue {
	items := make([]jsonValue, 0, len(words))
	for _, w := range words {
		items = append(items, i(int64(w)))
	}
	return arr(items...)
}

// decodeUTF16Lossy is the Go equivalent of the oracle's to_cow_lossy text
// rendering of two-byte view contents: WTF-16 units decoded with unpaired
// surrogates becoming U+FFFD.
func decodeUTF16Lossy(units []uint16) string {
	runes := utf16.Decode(units)
	var out strings.Builder
	for _, r := range runes {
		if r == 0xFFFD {
			out.WriteRune(0xFFFD)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
