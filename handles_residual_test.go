//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestResidualEternalOverwriteReuseAndClose(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iso.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scope.Close() }()
	eternal, err := gov8.EmptyEternal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eternal.Close() }()
	if empty, err := eternal.IsEmpty(); err != nil || !empty {
		t.Fatalf("initial IsEmpty = %v, %v", empty, err)
	}
	first, _ := scope.NewString("first")
	second, _ := scope.NewString("second")
	if err := eternal.Set(scope, first); err != nil {
		t.Fatal(err)
	}
	// This is intentionally not preceded by Clear: the pinned V8 build
	// overwrites despite Eternal's set-once documentation.
	if err := eternal.Set(scope, second); err != nil {
		t.Fatal(err)
	}
	got, ok, err := eternal.Get(scope)
	if err != nil || !ok {
		t.Fatalf("Get = ok %v, %v", ok, err)
	}
	if text, err := got.StringValue(); err != nil || text != "second" {
		t.Fatalf("value = %q, %v", text, err)
	}
	if err := eternal.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := eternal.Get(scope); err != nil || ok {
		t.Fatalf("Get after Clear = ok %v, %v", ok, err)
	}
}

func TestResidualHandlesRejectCrossIsolateAndClosedScope(t *testing.T) {
	isoA, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	isoB, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scopeA, _ := isoA.NewScope()
	scopeB, _ := isoB.NewScope()
	valueA, _ := scopeA.NewString("a")
	valueB, _ := scopeB.NewString("b")
	eternal, _ := gov8.EmptyEternal()
	traced, _ := gov8.NewTracedReference(scopeA, valueA)
	if err := eternal.Set(scopeA, valueA); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"Eternal.Get": func() error { _, _, err := eternal.Get(scopeB); return err },
		"Eternal.Set": func() error { return eternal.Set(scopeB, valueB) },
		"Traced.Get":  func() error { _, _, err := traced.Get(scopeB); return err },
		"Traced.Reset": func() error {
			return traced.Reset(scopeB, &valueB)
		},
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "different isolate") {
			t.Errorf("%s error = %v, want different isolate", name, err)
		}
	}
	closedScope, _ := isoA.NewScope()
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := eternal.Get(closedScope); err == nil {
		t.Error("Eternal.Get with closed scope succeeded")
	}
	if err := traced.Reset(closedScope, nil); err == nil {
		t.Error("TracedReference.Reset with closed scope succeeded")
	}
	_ = traced.Reset(scopeA, nil)
	_ = eternal.Clear()
	_ = traced.Close()
	_ = eternal.Close()
	_ = scopeB.Close()
	_ = scopeA.Close()
	_ = isoB.Close()
	_ = isoA.Close()
}

func TestResidualHandlesWrongThread(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := iso.NewScope()
	value, _ := scope.NewString("thread")
	eternal, _ := gov8.EmptyEternal()
	traced, _ := gov8.NewTracedReference(scope, value)
	_ = eternal.Set(scope, value)
	errors := make(chan error, 4)
	go func() {
		_, _, err := eternal.Get(scope)
		errors <- err
		errors <- eternal.Clear()
		_, _, err = traced.Get(scope)
		errors <- err
		errors <- traced.Reset(scope, nil)
	}()
	for index := 0; index < 4; index++ {
		if err := <-errors; err == nil || !strings.Contains(err.Error(), "thread affinity") {
			t.Fatalf("wrong-thread operation %d = %v", index, err)
		}
	}
	_ = traced.Reset(scope, nil)
	_ = eternal.Clear()
	_ = traced.Close()
	_ = eternal.Close()
	_ = scope.Close()
	_ = iso.Close()
}

func TestResidualHandlesPostIsolateCleanup(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	value, _ := scope.NewString("former-isolate")
	eternal, _ := gov8.EmptyEternal()
	traced, _ := gov8.NewTracedReference(scope, value)
	if err := eternal.Set(scope, value); err != nil {
		t.Fatal(err)
	}
	_ = scope.Close()
	_ = ctx.Close()
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if empty, err := eternal.IsEmpty(); err != nil || empty {
		t.Fatalf("non-empty Eternal after isolate = %v, %v", empty, err)
	}
	if err := eternal.Clear(); err != nil {
		t.Fatalf("post-isolate Eternal.Clear: %v", err)
	}
	if empty, err := eternal.IsEmpty(); err != nil || !empty {
		t.Fatalf("cleared Eternal after isolate = %v, %v", empty, err)
	}
	// Pinned subprocess evidence proves non-empty TracedReference Drop is
	// safe after isolate disposal; Close is Go's explicit Drop equivalent.
	if err := traced.Close(); err != nil {
		t.Fatalf("post-isolate TracedReference.Close: %v", err)
	}
	if err := eternal.Close(); err != nil {
		t.Fatalf("post-isolate Eternal.Close: %v", err)
	}
	if err := eternal.Clear(); err == nil {
		t.Error("Eternal.Clear after Close succeeded")
	}
}
