//go:build windows && amd64

package gov8_test

import "testing"

func TestObjectCreationContextScopeLocalUsability(t *testing.T) {
	e := newObjectEnv(t)
	defer e.close()

	ctx2, err := e.iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	shareContextAccess(t, e.scope, e.ctx, ctx2)
	setContextMarker(t, e.scope, ctx2, "creation-two")

	entered, err := ctx2.Enter()
	if err != nil {
		t.Fatalf("ctx2.Enter: %v", err)
	}
	obj, err := e.scope.NewObject(ctx2)
	if err != nil {
		t.Fatalf("NewObject(ctx2): %v", err)
	}
	if err := entered.Close(); err != nil {
		t.Fatalf("ContextScope.Close: %v", err)
	}

	created, present, err := obj.CreationContext(e.scope)
	if err != nil || !present {
		t.Fatalf("CreationContext = present %v, err %v", present, err)
	}
	if same, err := created.SameAs(ctx2); err != nil || !same {
		t.Fatalf("CreationContext.SameAs(ctx2) = %v, %v", same, err)
	}
	if got := contextRefMarker(t, created, e.scope, e.ctx); got != "creation-two" {
		t.Fatalf("creation context marker = %q, want creation-two", got)
	}

	// The Local<Context> remains usable for the lifetime of its Scope even
	// after the independent persistent Go wrapper is closed.
	if err := ctx2.Close(); err != nil {
		t.Fatalf("ctx2.Close: %v", err)
	}
	if got := contextRefMarker(t, created, e.scope, e.ctx); got != "creation-two" {
		t.Fatalf("marker after persistent close = %q, want creation-two", got)
	}

	outer, err := e.ctx.Enter()
	if err != nil {
		t.Fatalf("outer Context.Enter: %v", err)
	}
	borrowed, err := created.Enter()
	if err != nil {
		t.Fatalf("creation ContextRef.Enter: %v", err)
	}
	if err := e.scope.Close(); err == nil {
		t.Fatal("source Scope.Close succeeded while borrowed ContextScope was active")
	}
	if err := outer.Close(); err == nil {
		t.Fatal("outer ContextScope.Close succeeded out of LIFO order")
	}
	current, err := e.iso.CurrentContext(e.scope)
	if err != nil {
		t.Fatalf("CurrentContext while borrowed entered: %v", err)
	}
	if same, err := current.SameAsRef(created); err != nil || !same {
		t.Fatalf("current.SameAsRef(created) = %v, %v", same, err)
	}
	if got := contextRefMarker(t, current, e.scope, e.ctx); got != "creation-two" {
		t.Fatalf("entered creation marker = %q, want creation-two", got)
	}
	if err := borrowed.Close(); err != nil {
		t.Fatalf("borrowed ContextScope.Close: %v", err)
	}
	restored, err := e.iso.CurrentContext(e.scope)
	if err != nil {
		t.Fatalf("restored CurrentContext: %v", err)
	}
	if same, err := restored.SameAs(e.ctx); err != nil || !same {
		t.Fatalf("restored.SameAs(outer) = %v, %v", same, err)
	}
	if err := outer.Close(); err != nil {
		t.Fatalf("outer ContextScope.Close: %v", err)
	}
}

func TestObjectCreationContextValidation(t *testing.T) {
	e := newObjectEnv(t)
	obj := e.mustObject()

	errCh := make(chan error, 1)
	go func() {
		_, _, err := obj.CreationContext(e.scope)
		errCh <- err
	}()
	if err := <-errCh; err == nil {
		t.Fatal("CreationContext on foreign thread succeeded")
	}

	foreignISO := newIso(t)
	foreignScope := newScope(t, foreignISO)
	if ref, present, err := obj.CreationContext(foreignScope); err == nil || ref != nil || present {
		t.Fatalf("CreationContext(foreign scope) = %v, %v, %v", ref, present, err)
	}
	if err := foreignScope.Close(); err != nil {
		t.Errorf("foreign Scope.Close: %v", err)
	}
	if err := foreignISO.Close(); err != nil {
		t.Errorf("foreign Isolate.Close: %v", err)
	}

	if err := e.scope.Close(); err != nil {
		t.Fatalf("Scope.Close: %v", err)
	}
	if ref, present, err := obj.CreationContext(e.scope); err == nil || ref != nil || present {
		t.Fatalf("CreationContext(stale) = %v, %v, %v", ref, present, err)
	}
	if err := e.ctx.Close(); err != nil {
		t.Errorf("Context.Close: %v", err)
	}
	if err := e.iso.Close(); err != nil {
		t.Errorf("Isolate.Close: %v", err)
	}
}
