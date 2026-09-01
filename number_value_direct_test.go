//go:build windows && amd64

package gov8_test

import (
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestNumberValueDirectSpecialValues(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	cases := []struct {
		name  string
		value float64
	}{
		{"positive-zero", 0},
		{"negative-zero", math.Copysign(0, -1)},
		{"smallest-subnormal", math.SmallestNonzeroFloat64},
		{"negative-smallest-subnormal", -math.SmallestNonzeroFloat64},
		{"maximum", math.MaxFloat64},
		{"positive-infinity", math.Inf(1)},
		{"negative-infinity", math.Inf(-1)},
		{"nan", math.Float64frombits(0x7ff8000000000042)},
	}
	for _, test := range cases {
		value, err := scope.Number(test.value)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		got, ok, err := value.NumberValue(ctx)
		if err != nil || !ok {
			t.Fatalf("%s: NumberValue = %v, %v, %v", test.name, got, ok, err)
		}
		if math.IsNaN(test.value) {
			if !math.IsNaN(got) {
				t.Fatalf("%s: NumberValue = %v, want NaN", test.name, got)
			}
			continue
		}
		if math.Float64bits(got) != math.Float64bits(test.value) {
			t.Fatalf("%s: NumberValue bits = %#x, want %#x", test.name, math.Float64bits(got), math.Float64bits(test.value))
		}
	}
}

func TestNumberValueDirectCoercionAndEmptyMaybe(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	for _, test := range []struct {
		source string
		want   float64
	}{
		{`"42.5"`, 42.5},
		{`true`, 1},
		{`null`, 0},
		{`undefined`, math.NaN()},
	} {
		value, err := eval(t, ctx, scope, test.source)
		if err != nil {
			t.Fatalf("eval %s: %v", test.source, err)
		}
		got, ok, err := value.NumberValue(ctx)
		if err != nil || !ok || (!math.IsNaN(test.want) && got != test.want) || (math.IsNaN(test.want) && !math.IsNaN(got)) {
			t.Fatalf("NumberValue(%s) = %v, %v, %v", test.source, got, ok, err)
		}
	}

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	throwing, err := eval(t, ctx, scope, `({valueOf(){throw new Error("number-value-direct")}})`)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := throwing.NumberValue(ctx); err != nil || ok || got != 0 {
		t.Fatalf("throwing NumberValue = %v, %v, %v", got, ok, err)
	}
	if caught, err := tc.HasCaught(); err != nil || !caught {
		t.Fatalf("throwing conversion caught = %v, %v", caught, err)
	}
	if err := tc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNumberValueDirectNestedCoercionTLS(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	t.Cleanup(func() { _ = gov8.ReleaseIsolateHostState(iso) })
	inner, err := scope.Number(math.Inf(1))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	valueOf, err := iso.NewFunction(scope, ctx, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		got, ok, conversionErr := inner.NumberValue(ctx)
		if conversionErr != nil || !ok || !math.IsInf(got, 1) {
			panic("nested NumberValue did not preserve its TLS result")
		}
		calls.Add(1)
		_ = rv.SetFloat64(math.Copysign(0, -1))
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !seedGlobal(t, ctx, scope, "numberValueDirectValueOf", valueOf.Value) {
		t.Fatal("seed valueOf callback")
	}
	outer, err := eval(t, ctx, scope, `({valueOf:numberValueDirectValueOf})`)
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		got, ok, err := outer.NumberValue(ctx)
		if err != nil || !ok || math.Float64bits(got) != math.Float64bits(math.Copysign(0, -1)) {
			t.Fatalf("outer NumberValue = %v (%#x), %v, %v", got, math.Float64bits(got), ok, err)
		}
	}
	if calls.Load() != 50 {
		t.Fatalf("valueOf calls = %d, want 50", calls.Load())
	}
}

func TestNumberValueDirectValidationOrder(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	value, err := scope.Number(42)
	if err != nil {
		t.Fatal(err)
	}
	errChannel := make(chan error, 1)
	go func() {
		_, _, err := value.NumberValue(ctx)
		errChannel <- err
	}()
	if err := <-errChannel; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread NumberValue error = %v", err)
	}
	_, otherContext, _ := newTestRuntime(t)
	if _, _, err := value.NumberValue(otherContext); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("wrong-context NumberValue error = %v", err)
	}
	inner, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedValue, err := inner.Number(7)
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := closedValue.NumberValue(ctx); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("closed-scope NumberValue error = %v", err)
	}
}

func TestNumberValueDirectAllocationCeiling(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	value, err := scope.Number(42)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := value.NumberValue(ctx); err != nil || !ok {
		t.Fatalf("warm-up NumberValue = %v, %v", ok, err)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		got, ok, err := value.NumberValue(ctx)
		if err != nil || !ok || got != 42 {
			panic("NumberValue allocation probe failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("NumberValue allocations = %v, want 0", allocations)
	}
	runtime.KeepAlive(value)
}
