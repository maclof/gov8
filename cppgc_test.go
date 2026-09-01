//go:build windows && amd64

package gov8_test

import (
	"strings"
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

func cppgcAPIWrapper(t *testing.T, iso *gov8.Isolate, ctx *gov8.Context, scope *gov8.Scope) *gov8.Object {
	t.Helper()
	template, err := iso.NewFunctionTemplate(scope, func(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	function, err := template.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	wrapper, ok, err := function.NewInstance(scope)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	return wrapper
}

func TestCppGCWrapUnwrapIdentityAndTagBoundaries(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	t.Cleanup(func() {
		if err := gov8.ReleaseIsolateHostState(iso); err != nil {
			t.Errorf("ReleaseIsolateHostState: %v", err)
		}
	})
	present, err := iso.HasCppHeap()
	if err != nil || !present {
		t.Fatalf("HasCppHeap = %v, %v", present, err)
	}

	for _, tag := range []gov8.CppGCTag{0, 1, gov8.MaxCppGCTag} {
		wrapper := cppgcAPIWrapper(t, iso, ctx, scope)
		target, err := scope.NewObject(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := scope.WrapCppGCObject(wrapper, target.Value, int32(tag), tag, gov8.CppGCObjectCallbacks{})
		if err != nil {
			t.Fatalf("WrapCppGCObject(%d): %v", tag, err)
		}
		first, firstTarget, ok, err := scope.UnwrapCppGCObject(wrapper, tag)
		if err != nil || !ok {
			t.Fatalf("first Unwrap(%d): ok=%v err=%v", tag, ok, err)
		}
		second, secondTarget, ok, err := scope.UnwrapCppGCObject(wrapper, tag)
		if err != nil || !ok {
			t.Fatalf("second Unwrap(%d): ok=%v err=%v", tag, ok, err)
		}
		if id, err := wrapped.ID(); err != nil || id != int32(tag) {
			t.Fatalf("ID(%d) = %d, %v", tag, id, err)
		}
		if same, err := first.Same(second); err != nil || !same {
			t.Fatalf("Same(%d) = %v, %v", tag, same, err)
		}
		if same, err := firstTarget.StrictEquals(target.Value); err != nil || !same {
			t.Fatalf("traced target identity(%d) = %v, %v", tag, same, err)
		}
		if same, err := secondTarget.StrictEquals(target.Value); err != nil || !same {
			t.Fatalf("second traced target identity(%d) = %v, %v", tag, same, err)
		}
	}

	rewrapped := cppgcAPIWrapper(t, iso, ctx, scope)
	firstTarget, _ := scope.NewObject(ctx)
	first, err := scope.WrapCppGCObject(rewrapped, firstTarget.Value, 1, 1, gov8.CppGCObjectCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	secondTarget, _ := scope.NewObject(ctx)
	second, err := scope.WrapCppGCObject(rewrapped, secondTarget.Value, 2, 1, gov8.CppGCObjectCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	current, currentTarget, ok, err := scope.UnwrapCppGCObject(rewrapped, 1)
	if err != nil || !ok {
		t.Fatalf("unwrap after rewrap: ok=%v err=%v", ok, err)
	}
	if id, err := current.ID(); err != nil || id != 2 {
		t.Fatalf("rewrapped ID = %d, %v", id, err)
	}
	if same, err := current.Same(second); err != nil || !same {
		t.Fatalf("rewrapped identity = %v, %v", same, err)
	}
	if same, err := first.Same(second); err != nil || same {
		t.Fatalf("replaced identity = %v, %v", same, err)
	}
	if same, err := currentTarget.StrictEquals(secondTarget.Value); err != nil || !same {
		t.Fatalf("rewrapped traced target = %v, %v", same, err)
	}
}

func TestCppGCSafetyValidation(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	t.Cleanup(func() {
		if err := gov8.ReleaseIsolateHostState(iso); err != nil {
			t.Errorf("ReleaseIsolateHostState: %v", err)
		}
	})
	target, _ := scope.NewObject(ctx)
	plain, _ := scope.NewObject(ctx)
	if _, err := scope.WrapCppGCObject(plain, target.Value, 1, 1, gov8.CppGCObjectCallbacks{}); err == nil || !strings.Contains(err.Error(), "API wrapper") {
		t.Fatalf("plain wrapper error = %v", err)
	}
	wrapper := cppgcAPIWrapper(t, iso, ctx, scope)
	if _, err := scope.WrapCppGCObject(wrapper, target.Value, 1, gov8.MaxCppGCTag+1, gov8.CppGCObjectCallbacks{}); err == nil || !strings.Contains(err.Error(), "tag") {
		t.Fatalf("invalid tag error = %v", err)
	}
	if _, _, ok, err := scope.UnwrapCppGCObject(wrapper, 1); err != nil || ok {
		t.Fatalf("unwrap before wrap = %v, %v", ok, err)
	}
	if _, err := scope.WrapCppGCObject(wrapper, target.Value, 1, 1, gov8.CppGCObjectCallbacks{}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := scope.UnwrapCppGCObject(wrapper, 2); err != nil || ok {
		t.Fatalf("wrong-tag unwrap = %v, %v", ok, err)
	}

	foreign, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	foreignScope, _ := foreign.NewScope()
	foreignCtx, _ := foreign.NewContext()
	foreignTarget, _ := foreignScope.NewObject(foreignCtx)
	if _, err := scope.WrapCppGCObject(wrapper, foreignTarget.Value, 1, 1, gov8.CppGCObjectCallbacks{}); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign target error = %v", err)
	}
	if _, err := foreignScope.WrapCppGCObject(wrapper, target.Value, 1, 1, gov8.CppGCObjectCallbacks{}); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign wrapper error = %v", err)
	}
	_ = foreignScope.Close()
	_ = foreignCtx.Close()
	_ = foreign.Close()

	closed, _ := iso.NewScope()
	closedWrapper := cppgcAPIWrapper(t, iso, ctx, closed)
	closedTarget, _ := closed.NewObject(ctx)
	_ = closed.Close()
	if _, err := closed.WrapCppGCObject(closedWrapper, closedTarget.Value, 1, 1, gov8.CppGCObjectCallbacks{}); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("closed scope error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, _, _, err := scope.UnwrapCppGCObject(wrapper, 1)
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
}

func TestCppGCDestructionOnIsolateTeardown(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	wrapper := cppgcAPIWrapper(t, iso, ctx, scope)
	target, _ := scope.NewObject(ctx)
	var destroyed atomic.Int32
	view, err := scope.WrapCppGCObject(wrapper, target.Value, 7, 1, gov8.CppGCObjectCallbacks{
		Destroy: func() { destroyed.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if got := destroyed.Load(); got != 1 {
		t.Fatalf("destroy callback count = %d", got)
	}
	if _, err := view.ID(); err == nil {
		t.Fatal("view remained usable after its scope/isolate closed")
	}
}
