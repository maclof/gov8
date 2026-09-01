//go:build windows && amd64

package gov8_test

import (
	"math"
	"testing"

	gov8 "gov8"
)

// Benchmarks for the object-ops slice, following the workload spec in the
// pinned oracle's conformance-object-ops.rs benchmark notes: one isolate +
// context for the whole benchmark, fresh objects per iteration to keep GC
// pressure realistic, one full operation per iteration, and each workload
// asserted once for correctness outside the timed loop.
//
// Oracle workloads (object/*):
//   has_delete_cycle, get_with_receiver, get_identity_hash,
//   to_object_number, to_boolean_matrix, same_value_zero, typeof_all,
//   instance_of.

func benchObjectRuntime(b *testing.B) (*objEnv, *gov8.Object, *gov8.Object) {
	b.Helper()
	e := newObjectEnvTB(b)
	proto := e.mustObject()
	ctor := e.mustObject()
	return e, proto, ctor
}

func BenchmarkObjectHasDeleteCycle(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	key := e.mustString("k")
	v := e.mustInt(1)
	// Correctness assertion outside the timed loop.
	probe := e.mustObject()
	if _, err := probe.SetByKey(e.scope, e.ctx, key, v); err != nil {
		b.Fatal(err)
	}
	has, err := probe.Has(e.scope, e.ctx, key, nil)
	if err != nil || !has {
		b.Fatalf("has: %v err=%v", has, err)
	}
	if _, err := probe.Delete(e.scope, e.ctx, key, nil); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		obj, err := e.scope.NewObject(e.ctx)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := obj.SetByKey(e.scope, e.ctx, key, v); err != nil {
			b.Fatal(err)
		}
		if _, err := obj.Has(e.scope, e.ctx, key, nil); err != nil {
			b.Fatal(err)
		}
		if _, err := obj.Delete(e.scope, e.ctx, key, nil); err != nil {
			b.Fatal(err)
		}
		if _, err := obj.Has(e.scope, e.ctx, key, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObjectGetWithReceiver(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	// A JS accessor defined once on a prototype object.
	script, err := e.ctx.Compile(e.scope, `
		globalThis.proto = { get t() { return this.x; } };`, nil)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := script.Run(e.scope, nil); err != nil {
		b.Fatal(err)
	}
	_ = script.Close()
	proto := e.mustGlobalObject("globalThis.proto")
	key := e.mustString("t")
	probe := e.mustObject()
	if _, err := probe.SetByName(e.scope, e.ctx, "x", e.mustInt(7)); err != nil {
		b.Fatal(err)
	}
	if _, err := proto.GetWithReceiver(e.scope, e.ctx, key, probe); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		recv, err := e.scope.NewObject(e.ctx)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := recv.SetByName(e.scope, e.ctx, "x", e.mustInt(7)); err != nil {
			b.Fatal(err)
		}
		if _, err := proto.GetWithReceiver(e.scope, e.ctx, key, recv); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObjectGetIdentityHash(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	probe, err := e.scope.NewObject(e.ctx)
	if err != nil {
		b.Fatal(err)
	}
	h, err := probe.GetIdentityHash()
	if err != nil || h == 0 {
		b.Fatalf("identity hash: %v err=%v", h, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		obj, err := e.scope.NewObject(e.ctx)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := obj.GetIdentityHash(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObjectToObjectNumber(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	five, err := e.scope.Number(5)
	if err != nil {
		b.Fatal(err)
	}
	w, err := five.ToObject(e.scope, e.ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	if isNum, err := w.Value.IsNumberObject(); err != nil || !isNum {
		b.Fatalf("is number object: %v err=%v", isNum, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := five.ToObject(e.scope, e.ctx, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObjectToBooleanMatrix(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	nan, err := e.scope.Number(math.NaN())
	if err != nil {
		b.Fatal(err)
	}
	minusZero, err := e.scope.Number(math.Copysign(0, -1))
	if err != nil {
		b.Fatal(err)
	}
	empty, err := e.scope.NewString("")
	if err != nil {
		b.Fatal(err)
	}
	s0, err := e.scope.NewString("0")
	if err != nil {
		b.Fatal(err)
	}
	undef, err := e.scope.Undefined()
	if err != nil {
		b.Fatal(err)
	}
	nullV, err := e.scope.Null()
	if err != nil {
		b.Fatal(err)
	}
	fFalse, err := e.scope.Boolean(false)
	if err != nil {
		b.Fatal(err)
	}
	truth, err := e.scope.Boolean(true)
	if err != nil {
		b.Fatal(err)
	}
	matrix := []gov8.Value{undef, nullV, fFalse, minusZero, nan, empty, s0, truth}
	falsy := []bool{false, false, false, false, false, false, true, true}
	for i, v := range matrix {
		bv, err := v.ToBoolean(e.scope)
		if err != nil {
			b.Fatal(err)
		}
		got, err := bv.BooleanValue()
		if err != nil {
			b.Fatal(err)
		}
		if got != falsy[i] {
			b.Fatalf("matrix[%d] = %v, want %v", i, got, falsy[i])
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var fold bool
		for _, v := range matrix {
			bv, err := v.ToBoolean(e.scope)
			if err != nil {
				b.Fatal(err)
			}
			got, err := bv.BooleanValue()
			if err != nil {
				b.Fatal(err)
			}
			fold = fold != got
		}
		_ = fold
	}
}

func BenchmarkObjectSameValueZero(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	nan, err := e.scope.Number(math.NaN())
	if err != nil {
		b.Fatal(err)
	}
	minusZero, err := e.scope.Number(math.Copysign(0, -1))
	if err != nil {
		b.Fatal(err)
	}
	str, err := e.scope.NewString("ab")
	if err != nil {
		b.Fatal(err)
	}
	int7 := e.mustInt(7)
	float7, err := e.scope.Number(7)
	if err != nil {
		b.Fatal(err)
	}
	pairs := [][2]gov8.Value{
		{nan, nan}, {minusZero, int7}, {str, str}, {int7, float7},
	}
	if eq, err := nan.SameValueZero(e.scope, nan); err != nil || !eq {
		b.Fatalf("same value zero(NaN,NaN): %v err=%v", eq, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range pairs {
			if _, err := p[0].SameValueZero(e.scope, p[1]); err != nil {
				b.Fatal(err)
			}
			if _, err := p[0].StrictEquals(p[1]); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkObjectTypeOfAll(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	undef, err := e.scope.Undefined()
	if err != nil {
		b.Fatal(err)
	}
	nullV, err := e.scope.Null()
	if err != nil {
		b.Fatal(err)
	}
	truth := e.mustInt(1)
	str, err := e.scope.NewString("s")
	if err != nil {
		b.Fatal(err)
	}
	fn := e.evalValue("(function f() {})")
	obj := e.mustObject()
	samples := []struct {
		want string
		v    gov8.Value
	}{
		{"undefined", undef}, {"object", nullV}, {"number", truth},
		{"string", str}, {"function", fn}, {"object", obj.Value},
	}
	for _, s := range samples {
		tv, err := s.v.TypeOf(e.scope)
		if err != nil {
			b.Fatal(err)
		}
		got, err := tv.StringValue()
		if err != nil {
			b.Fatal(err)
		}
		if got != s.want {
			b.Fatalf("typeof = %q, want %q", got, s.want)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range samples {
			tv, err := s.v.TypeOf(e.scope)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := tv.StringValue(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkObjectInstanceOf(b *testing.B) {
	e := newObjectEnvTB(b)
	defer e.close()
	objectCtor := e.mustGlobalObject("Object")
	probe, err := e.scope.NewObject(e.ctx)
	if err != nil {
		b.Fatal(err)
	}
	in, err := probe.Value.InstanceOf(e.scope, e.ctx, objectCtor, nil)
	if err != nil || !in {
		b.Fatalf("instanceof Object: %v err=%v", in, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		obj, err := e.scope.NewObject(e.ctx)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := obj.Value.InstanceOf(e.scope, e.ctx, objectCtor, nil); err != nil {
			b.Fatal(err)
		}
	}
}
