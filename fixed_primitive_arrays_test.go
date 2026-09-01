//go:build windows && amd64

package gov8_test

import (
	"math"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func mustPrimitive(t *testing.T, makeValue func() (gov8.Value, error)) gov8.Value {
	t.Helper()
	v, err := makeValue()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPrimitiveArrayDefaultsKindsAndOverwrite(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	empty, err := gov8.NewPrimitiveArray(scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := empty.Length(); err != nil || n != 0 {
		t.Fatalf("empty length = %d, %v", n, err)
	}
	array, err := gov8.NewPrimitiveArray(scope, 8)
	if err != nil {
		t.Fatal(err)
	}
	desc := mustPrimitive(t, func() (gov8.Value, error) { return scope.NewString("token") })
	symbol, err := scope.NewSymbol(desc)
	if err != nil {
		t.Fatal(err)
	}
	values := []gov8.Value{
		mustPrimitive(t, scope.Undefined),
		mustPrimitive(t, scope.Null),
		mustPrimitive(t, func() (gov8.Value, error) { return scope.Boolean(true) }),
		mustPrimitive(t, func() (gov8.Value, error) { return scope.NewString("hello") }),
		symbol.Value,
		mustPrimitive(t, func() (gov8.Value, error) { return scope.Number(2.5) }),
		mustPrimitive(t, func() (gov8.Value, error) { return scope.Int32(-7) }),
		mustPrimitive(t, func() (gov8.Value, error) { return scope.BigIntFromInt64(9_007_199_254_740_993) }),
	}
	for i, v := range values {
		if ok, err := array.Set(scope, i, v); err != nil || !ok {
			t.Fatalf("Set(%d) = %v, %v", i, ok, err)
		}
	}
	wantText := []string{"undefined", "null", "true", "hello", "token", "2.5", "-7", "9007199254740993"}
	for i, want := range wantText {
		got, ok, err := array.Get(scope, i)
		if err != nil || !ok {
			t.Fatalf("Get(%d) = %v, %v", i, ok, err)
		}
		var text string
		if i == 4 {
			sym, castErr := gov8.AsSymbol(got)
			if castErr != nil {
				t.Fatalf("AsSymbol: %v", castErr)
			}
			description, descriptionErr := sym.Description(scope)
			if descriptionErr == nil {
				text, descriptionErr = description.StringValue()
			}
			err = descriptionErr
		} else {
			text, err = got.ToString(ctx)
		}
		if err != nil || text != want {
			t.Errorf("Get(%d) text = %q, %v; want %q", i, text, err, want)
		}
	}
	roundtrip, _, _ := array.Get(scope, 4)
	if same, err := roundtrip.StrictEquals(symbol.Value); err != nil || !same {
		t.Fatalf("symbol identity = %v, %v", same, err)
	}
	if ok, err := array.Set(scope, 6, mustPrimitive(t, func() (gov8.Value, error) { return scope.Int32(2) })); err != nil || !ok {
		t.Fatalf("overwrite = %v, %v", ok, err)
	}
	got, _, _ := array.Get(scope, 6)
	if text, _ := got.ToString(ctx); text != "2" {
		t.Fatalf("overwritten slot = %q", text)
	}
}

func TestPrimitiveArrayGlobalContextIndependence(t *testing.T) {
	iso := newIso(t)
	ctx1 := newCtx(t, iso)
	scope1 := newScope(t, iso)
	array, err := gov8.NewPrimitiveArray(scope1, 2)
	if err != nil {
		t.Fatal(err)
	}
	first := mustPrimitive(t, func() (gov8.Value, error) { return scope1.NewString("from-first-context") })
	if ok, err := array.Set(scope1, 0, first); err != nil || !ok {
		t.Fatalf("Set first = %v, %v", ok, err)
	}
	if ok, err := array.Set(scope1, 1, mustPrimitive(t, func() (gov8.Value, error) { return scope1.Int32(2) })); err != nil || !ok {
		t.Fatalf("Set second = %v, %v", ok, err)
	}
	global, err := gov8.NewPrimitiveArrayGlobal(scope1, array)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope1.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := array.Length(); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("local array after scope Close = %v", err)
	}
	if err := ctx1.Close(); err != nil {
		t.Fatal(err)
	}
	ctx2 := newCtx(t, iso)
	scope2 := newScope(t, iso)
	reopened, err := global.ToLocal(scope2)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"from-first-context", "2"} {
		v, ok, err := reopened.Get(scope2, i)
		if err != nil || !ok {
			t.Fatalf("Get(%d) = %v, %v", i, ok, err)
		}
		if got, _ := v.ToString(ctx2); got != want {
			t.Errorf("Get(%d) = %q, want %q", i, got, want)
		}
	}
	if err := global.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scope2.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx2.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrimitiveArraySafeBoundaries(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	array, err := gov8.NewPrimitiveArray(scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{-1, 1, math.MaxInt} {
		if _, ok, err := array.Get(scope, index); err != nil || ok {
			t.Errorf("Get(%d) = %v, %v; want safe miss", index, ok, err)
		}
		if ok, err := array.Set(scope, index, mustPrimitive(t, scope.Undefined)); err != nil || ok {
			t.Errorf("Set(%d) = %v, %v; want safe miss", index, ok, err)
		}
	}
	for _, length := range []int{-1, math.MaxInt32 + 1, math.MaxInt} {
		if _, err := gov8.NewPrimitiveArray(scope, length); err == nil || !strings.Contains(err.Error(), "negative C int") {
			t.Errorf("NewPrimitiveArray(%d) = %v", length, err)
		}
	}
	for _, tc := range []struct {
		requested int
		observed  int
	}{
		{int(uint64(1) << 32), 0},
		{int((uint64(1) << 32) + 1), 1},
	} {
		wrapped, err := gov8.NewPrimitiveArray(scope, tc.requested)
		if err != nil {
			t.Errorf("NewPrimitiveArray(%d): %v", tc.requested, err)
			continue
		}
		if got, err := wrapped.Length(); err != nil || got != tc.observed {
			t.Errorf("NewPrimitiveArray(%d).Length = %d, %v; want %d", tc.requested, got, err, tc.observed)
		}
		if tc.observed == 1 {
			value, ok, err := wrapped.Get(scope, 0)
			if err != nil || !ok {
				t.Errorf("wrapped Get(0) = %v, %v", ok, err)
			} else if undefined, err := value.IsUndefined(); err != nil || !undefined {
				t.Errorf("wrapped default IsUndefined = %v, %v", undefined, err)
			}
		}
	}
	obj, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := array.Set(scope, 0, obj.Value); err == nil || !strings.Contains(err.Error(), "not primitive") {
		t.Fatalf("object Set error = %v", err)
	}
	// Rejections do not poison the isolate.
	if ok, err := array.Set(scope, 0, mustPrimitive(t, func() (gov8.Value, error) { return scope.Int32(42) })); err != nil || !ok {
		t.Fatalf("valid Set after probes = %v, %v", ok, err)
	}
}

func TestFixedArrayModuleMetadataAndBounds(t *testing.T) {
	iso := newIso(t)
	moduleCtx := newCtx(t, iso)
	moduleScope := newScope(t, iso)
	m, err := moduleCtx.CompileModule(moduleScope,
		"import value from './data.json' with { kind: 'fixture' }; export default value;", "arrays.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	requests, err := m.ModuleRequests(moduleScope)
	if err != nil {
		t.Fatal(err)
	}
	if fixed, _ := requests.IsFixedArray(); !fixed {
		t.Fatal("requests IsFixedArray = false")
	}
	if n, _ := requests.Length(); n != 1 {
		t.Fatalf("request length = %d", n)
	}
	for _, index := range []int{-1, 1, math.MaxInt} {
		if _, ok, err := requests.Get(moduleScope, index); err != nil || ok {
			t.Errorf("requests.Get(%d) = %v, %v", index, ok, err)
		}
	}
	data, ok, err := requests.Get(moduleScope, 0)
	if err != nil || !ok {
		t.Fatalf("request Get = %v, %v", ok, err)
	}
	if is, _ := data.IsModuleRequest(); !is {
		t.Fatal("request IsModuleRequest = false")
	}
	if is, _ := data.IsPrimitive(); is {
		t.Fatal("request IsPrimitive = true")
	}
	request, ok, err := data.ModuleRequest()
	if err != nil || !ok {
		t.Fatalf("ModuleRequest = %v, %v", ok, err)
	}
	attrs, err := request.ImportAttributes(moduleScope)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := attrs.Length(); n != 3 {
		t.Fatalf("attribute length = %d", n)
	}
	for i, want := range []string{"kind", "fixture", "39"} {
		attribute, present, err := attrs.Get(moduleScope, i)
		if err != nil || !present {
			t.Fatalf("attribute %d = %v, %v", i, present, err)
		}
		value, isValue, err := attribute.Value()
		if err != nil || !isValue {
			t.Fatalf("attribute Value %d = %v, %v", i, isValue, err)
		}
		got, err := value.ToString(moduleCtx)
		if err != nil || got != want {
			t.Errorf("attribute %d = %q, %v; want %q", i, got, err, want)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := moduleScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := moduleCtx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrimitiveArrayCrossIsolateAndWrongThreadRejected(t *testing.T) {
	_, _, ctxA, _, scopeA, scopeB := twoIsolates(t)
	array, err := gov8.NewPrimitiveArray(scopeA, 1)
	if err != nil {
		t.Fatal(err)
	}
	wantForeignIsolateError(t, "PrimitiveArray.Get foreign scope", func() error {
		_, _, err := array.Get(scopeB, 0)
		return err
	})
	wantForeignIsolateError(t, "PrimitiveArray.Set foreign scope", func() error {
		_, err := array.Set(scopeB, 0, mustPrimitive(t, scopeB.Undefined))
		return err
	})
	wantForeignIsolateError(t, "PrimitiveArray.Set foreign item", func() error {
		foreign := mustPrimitive(t, scopeB.Undefined)
		_, err := array.Set(scopeA, 0, foreign)
		return err
	})
	global, err := gov8.NewPrimitiveArrayGlobal(scopeA, array)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = global.Close() }()
	wantForeignIsolateError(t, "PrimitiveArrayGlobal.ToLocal foreign scope", func() error {
		_, err := global.ToLocal(scopeB)
		return err
	})
	module, err := ctxA.CompileModule(scopeA, "export default 1;", "array-affinity.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = module.Close() }()
	requests, err := module.ModuleRequests(scopeA)
	if err != nil {
		t.Fatal(err)
	}
	wantForeignIsolateError(t, "FixedArray.Get foreign scope", func() error {
		_, _, err := requests.Get(scopeB, 0)
		return err
	})
	errCh := make(chan error, 1)
	go func() {
		_, _, err := array.Get(scopeA, 0)
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread Get = %v", err)
	}
}
