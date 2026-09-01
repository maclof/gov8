//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestObjectResidualSafetyAndLifecycle(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	null, _ := scope.Null()
	name, _ := scope.NewString("x")
	value, _ := scope.Int32(1)

	if _, err := scope.NewObjectWithPrototypeAndProperties(ctx, null, []gov8.Value{name}, nil); err == nil {
		t.Fatal("constructor accepted mismatched property slices")
	}
	if _, err := scope.NewObjectWithPrototypeAndProperties(ctx, null, []gov8.Value{value}, []gov8.Value{value}); err == nil {
		t.Fatal("constructor accepted non-Name property")
	}
	if _, err := scope.NewObjectWithPrototypeAndProperties(ctx, null, nil, nil); err != nil {
		t.Fatalf("empty property constructor: %v", err)
	}

	object, _ := scope.NewObject(ctx)
	if _, err := object.GetOwnPropertyNames(scope, ctx, gov8.PropertyFilter(0x80), gov8.KeyConversionKeepNumbers); err == nil {
		t.Fatal("GetOwnPropertyNames accepted invalid property filter")
	}
	if _, err := object.GetOwnPropertyNames(scope, ctx, gov8.PropertyFilterAllProperties, gov8.KeyConversionMode(99)); err == nil {
		t.Fatal("GetOwnPropertyNames accepted invalid conversion")
	}

	foreign, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	foreignCtx, _ := foreign.NewContext()
	foreignScope, _ := foreign.NewScope()
	foreignValue, _ := foreignScope.Int32(2)
	if _, err := scope.NewObjectWithPrototypeAndProperties(ctx, foreignValue, nil, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign prototype error = %v", err)
	}
	if _, _, _, err := object.PreviewEntries(foreignScope, foreignCtx); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign preview scope error = %v", err)
	}
	_ = foreignScope.Close()
	_ = foreignCtx.Close()
	_ = foreign.Close()

	errCh := make(chan error, 1)
	go func() {
		_, _, _, err := object.PreviewEntries(scope, ctx)
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Fatalf("wrong-thread PreviewEntries error = %v", err)
	}

	closed, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedObject, err := closed.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closedObject.IsAPIWrapper(); err == nil {
		t.Fatal("IsAPIWrapper accepted receiver from closed scope")
	}
	if _, _, _, err := object.PreviewEntries(closed, ctx); err == nil {
		t.Fatal("PreviewEntries accepted closed result scope")
	}
}
