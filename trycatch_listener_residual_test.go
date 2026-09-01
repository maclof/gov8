//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestTryCatchReThrowImmediatelyClosesInner(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	outer, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Close()
	inner, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	script, err := ctx.Compile(scope, "throw ({tag:'rethrown'})", inner)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	if _, err := script.Run(scope, inner); err == nil {
		t.Fatal("throw unexpectedly ran")
	}
	result, ok, err := inner.ReThrow(scope)
	if err != nil || !ok {
		t.Fatalf("ReThrow = %v, %v", ok, err)
	}
	if undefined, err := result.IsUndefined(); err != nil || !undefined {
		t.Fatalf("result undefined = %v, %v", undefined, err)
	}
	if _, err := inner.HasCaught(); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("inner remained usable: %v", err)
	}
	if caught, err := outer.HasCaught(); err != nil || !caught {
		t.Fatalf("outer caught = %v, %v", caught, err)
	}
}

func TestTryCatchReThrowRejectsForeignScopeAndWrongThread(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	_, _, foreignScope := newTestRuntime(t)
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	if _, _, err := tc.ReThrow(foreignScope); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope = %v", err)
	}
	errCh := make(chan error, 1)
	go func() { _, _, err := tc.ReThrow(foreignScope); errCh <- err }()
	if err := <-errCh; err == nil || (!strings.Contains(err.Error(), "affinity") && !strings.Contains(err.Error(), "wrong thread")) {
		t.Fatalf("wrong-thread ReThrow = %v", err)
	}
}

func TestCallbackMessageExpiresAfterListenerReturns(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var retained *gov8.CallbackMessage
	if ok, err := iso.AddMessageListener(func(message *gov8.CallbackMessage, _ gov8.Value) { retained = message }); err != nil || !ok {
		t.Fatalf("AddMessageListener = %v, %v", ok, err)
	}
	script, err := ctx.CompileUncaughtWithOrigin(scope, "throw 1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	if _, err := script.RunUncaught(scope); err == nil {
		t.Fatal("uncaught throw unexpectedly ran")
	}
	if retained == nil {
		t.Fatal("listener was not invoked")
	}
	if _, err := retained.AsMessage(); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("retained callback message = %v", err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
}

func TestCompileUncaughtWithOriginValidation(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	if _, err := ctx.CompileUncaughtWithOrigin(nil, "1", nil); err == nil || !strings.Contains(err.Error(), "nil scope") {
		t.Fatalf("nil scope = %v", err)
	}
	if _, err := ctx.CompileUncaughtWithOrigin(scope, "1", &gov8.Origin{IsModule: true}); err == nil || !strings.Contains(err.Error(), "module origins") {
		t.Fatalf("module origin = %v", err)
	}
	resource, err := scope.NewString("resource.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctx.CompileUncaughtWithOrigin(scope, "1", &gov8.Origin{ResourceNameValue: resource}); err == nil || !strings.Contains(err.Error(), "ResourceNameValue") {
		t.Fatalf("value resource origin = %v", err)
	}
}
