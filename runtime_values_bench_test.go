//go:build windows && amd64

package gov8_test

import (
	"math"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Benchmarks for the runtime-values slice: construction, steady-state
// collection operations, RegExp execution, JSON round trips, and the
// property-descriptor surface. These complement the oracle's script/startup
// workloads with the cross-boundary cost profile of the built-ins this
// slice adds. Each iteration that creates locals opens a fresh Scope,
// mirroring the oracle's fresh nested HandleScope per iteration.

// benchRuntimeValuesRuntime prepares an isolate/context pair shared by the
// benchmarks (the scope is opened per iteration by the benchmarks).
func benchRuntimeValuesRuntime(b *testing.B) (*gov8.Isolate, *gov8.Context) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	return iso, ctx
}

// BenchmarkDateNewValueOf measures Date construction plus the raw value read
// (two shim crossings per iteration).
func BenchmarkDateNewValueOf(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		d, err := scope.NewDate(ctx, 1.5e12)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := d.ValueOf(); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// BenchmarkRegExpExec measures a global-pattern exec cycle on a small
// subject (the engine allocates the match object per iteration).
func BenchmarkRegExpExec(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	pattern, err := scope.NewString("a(b)c")
	if err != nil {
		b.Fatal(err)
	}
	re, err := scope.NewRegExp(ctx, pattern, gov8.RegExpGlobal, nil)
	if err != nil {
		b.Fatal(err)
	}
	subject, err := scope.NewString("xxabcXXabc")
	if err != nil {
		b.Fatal(err)
	}
	// Keep lastIndex cycling: reset it from Go every other match via a
	// plain index rewrite of the subject is unnecessary — a global regexp
	// that exhausts resets lastIndex to 0 by itself, so consecutive Execs
	// cycle matches forever.
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := re.Exec(scope, ctx, subject); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJSONParseStringify measures a full parse+stringify round trip of
// a small canonical object.
func BenchmarkJSONParseStringify(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	const src = `{"a":[1,2.5,"s",true,null],"b":{"c":1}}`
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		text, err := scope.NewString(src)
		if err != nil {
			b.Fatal(err)
		}
		parsed, err := gov8.JSONParse(ctx, scope, text, nil)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := gov8.JSONStringify(ctx, scope, parsed, nil); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// BenchmarkArrayNewWithElements8 measures element-array construction with
// eight pre-built elements (one elements copy across the boundary).
func BenchmarkArrayNewWithElements8(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = setup.Close() }()
	elements := make([]gov8.Value, 8)
	for i := range elements {
		v, err := setup.Int32(int32(i))
		if err != nil {
			b.Fatal(err)
		}
		elements[i] = v
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := scope.NewArrayWithElements(ctx, elements); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// BenchmarkArrayIndexSetGet measures a SetIndex/GetIndex pair (MaybeBool +
// MaybeLocal crossings with context entry per call).
func BenchmarkArrayIndexSetGet(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	arr, err := setup.NewArray(ctx, 16)
	if err != nil {
		b.Fatal(err)
	}
	v, err := setup.Int32(7)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		idx := uint32(i % 16)
		if _, err := arr.SetIndex(scope, ctx, idx, v); err != nil {
			b.Fatal(err)
		}
		if _, err := arr.GetIndex(scope, ctx, idx); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// BenchmarkMapSetGetDelete measures the Map write/read/delete cycle with
// integer keys (SameValueZero key lookup per operation).
func BenchmarkMapSetGetDelete(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = setup.Close() }()
	m, err := setup.NewMap(ctx)
	if err != nil {
		b.Fatal(err)
	}
	value, err := setup.Int32(1)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		key, err := scope.Int32(int32(i % 1024))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := m.Set(scope, ctx, key, value); err != nil {
			b.Fatal(err)
		}
		if _, err := m.Get(scope, ctx, key); err != nil {
			b.Fatal(err)
		}
		if _, err := m.Delete(scope, ctx, key); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// BenchmarkSetAddHas measures the Set insert/probe cycle with NaN-style
// SameValueZero dedup pressure (repeated same key: insert is a no-op after
// the first).
func BenchmarkSetAddHas(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = setup.Close() }()
	s, err := setup.NewSet(ctx)
	if err != nil {
		b.Fatal(err)
	}
	nan, err := setup.Number(math.NaN())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Add(scope, ctx, nan); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Has(scope, ctx, nan); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// BenchmarkSymbolForKey measures the registry lookup path (symbol
// interning) against a fresh symbol for comparison.
func BenchmarkSymbolForKey(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		desc, err := scope.NewString("gov8.bench")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := scope.SymbolForKey(desc); err != nil {
			b.Fatal(err)
		}
		if _, err := scope.NewSymbol(desc); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}

// BenchmarkProxyGet measures native property reads through default proxy
// trap forwarding (each get crosses the trap machinery).
func BenchmarkProxyGet(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = setup.Close() }()
	target, err := setup.NewObject(ctx)
	if err != nil {
		b.Fatal(err)
	}
	one, err := setup.Int32(1)
	if err != nil {
		b.Fatal(err)
	}
	name, err := setup.NewString("x")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := target.CreateDataProperty(setup, ctx, name, one); err != nil {
		b.Fatal(err)
	}
	handler, err := setup.NewObject(ctx)
	if err != nil {
		b.Fatal(err)
	}
	proxy, err := setup.NewProxy(ctx, target, handler)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		proxyObj, err := gov8.AsObject(proxy.Value)
		if err != nil {
			b.Fatal(err)
		}
		if _, ok, err := proxyObj.GetByName(scope, ctx, "x"); err != nil || !ok {
			b.Fatalf("proxy get: ok=%v err=%v", ok, err)
		}
		_ = scope.Close()
	}
}

// BenchmarkDefinePropertyAndAttributes measures the descriptor surface: a
// PropertyDescriptor construction, a define through it, and an attribute
// read-back.
func BenchmarkDefinePropertyAndAttributes(b *testing.B) {
	iso, ctx := benchRuntimeValuesRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = setup.Close() }()
	value, err := setup.Int32(5)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		obj, err := scope.NewObject(ctx)
		if err != nil {
			b.Fatal(err)
		}
		key, err := scope.NewString("k")
		if err != nil {
			b.Fatal(err)
		}
		pd, err := scope.NewPropertyDescriptorFromValueWritable(value, true)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := obj.DefineProperty(scope, ctx, key, pd); err != nil {
			b.Fatal(err)
		}
		if err := pd.Close(); err != nil {
			b.Fatal(err)
		}
		if _, _, err := obj.GetPropertyAttributes(scope, ctx, key); err != nil {
			b.Fatal(err)
		}
		_ = scope.Close()
	}
}
