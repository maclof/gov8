//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

func setContextMarker(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, marker string) {
	t.Helper()
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	value, err := scope.NewString(marker)
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if ok, err := global.SetByName(scope, ctx, "__context_ref_marker", value); err != nil || !ok {
		t.Fatalf("SetByName marker = %v, %v", ok, err)
	}
}

func shareContextAccess(t *testing.T, scope *gov8.Scope, contexts ...*gov8.Context) {
	t.Helper()
	token, err := scope.NewString("context-ref-shared-token")
	if err != nil {
		t.Fatalf("NewString security token: %v", err)
	}
	for _, ctx := range contexts {
		if err := ctx.SetSecurityToken(scope, token); err != nil {
			t.Fatalf("SetSecurityToken: %v", err)
		}
	}
}

func contextRefMarker(t *testing.T, ref *gov8.ContextRef, scope *gov8.Scope, operationContext *gov8.Context) string {
	t.Helper()
	global, err := ref.GlobalObject(scope)
	if err != nil {
		t.Fatalf("ContextRef.GlobalObject: %v", err)
	}
	value, present, err := global.GetByName(scope, operationContext, "__context_ref_marker")
	if err != nil || !present {
		t.Fatalf("GetByName marker = present %v, err %v", present, err)
	}
	marker, err := value.StringValue()
	if err != nil {
		t.Fatalf("StringValue marker: %v", err)
	}
	return marker
}

func TestContextRefNestedCurrentEnteredAndGlobal(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx1 := newCtx(t, iso)
	defer func() { _ = ctx1.Close() }()
	ctx2 := newCtx(t, iso)
	defer func() { _ = ctx2.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	shareContextAccess(t, scope, ctx1, ctx2)
	setContextMarker(t, scope, ctx1, "one")
	setContextMarker(t, scope, ctx2, "two")

	outer, err := ctx1.Enter()
	if err != nil {
		t.Fatalf("ctx1.Enter: %v", err)
	}
	current, err := iso.CurrentContext(scope)
	if err != nil {
		t.Fatalf("CurrentContext: %v", err)
	}
	entered, err := iso.EnteredOrMicrotaskContext(scope)
	if err != nil {
		t.Fatalf("EnteredOrMicrotaskContext: %v", err)
	}
	if empty, err := current.IsEmpty(); err != nil || empty {
		t.Fatalf("current IsEmpty = %v, %v", empty, err)
	}
	if same, err := current.SameAsRef(entered); err != nil || !same {
		t.Fatalf("current.SameAsRef(entered) = %v, %v", same, err)
	}
	if got := contextRefMarker(t, current, scope, ctx1); got != "one" {
		t.Fatalf("outer current marker = %q, want one", got)
	}

	inner, err := ctx2.Enter()
	if err != nil {
		t.Fatalf("ctx2.Enter: %v", err)
	}
	innerCurrent, err := iso.CurrentContext(scope)
	if err != nil {
		t.Fatalf("inner CurrentContext: %v", err)
	}
	innerEntered, err := iso.EnteredOrMicrotaskContext(scope)
	if err != nil {
		t.Fatalf("inner EnteredOrMicrotaskContext: %v", err)
	}
	if same, err := innerCurrent.SameAsRef(innerEntered); err != nil || !same {
		t.Fatalf("inner current.SameAsRef(entered) = %v, %v", same, err)
	}
	if got := contextRefMarker(t, innerEntered, scope, ctx1); got != "two" {
		t.Fatalf("inner entered marker = %q, want two", got)
	}
	if err := inner.Close(); err != nil {
		t.Fatalf("inner.Close: %v", err)
	}

	restored, err := iso.EnteredOrMicrotaskContext(scope)
	if err != nil {
		t.Fatalf("restored EnteredOrMicrotaskContext: %v", err)
	}
	if same, err := restored.SameAs(ctx1); err != nil || !same {
		t.Fatalf("restored.SameAs(ctx1) = %v, %v", same, err)
	}
	if got := contextRefMarker(t, restored, scope, ctx1); got != "one" {
		t.Fatalf("restored marker = %q, want one", got)
	}
	if err := outer.Close(); err != nil {
		t.Fatalf("outer.Close: %v", err)
	}

	emptyCurrent, err := iso.CurrentContext(scope)
	if err != nil {
		t.Fatalf("empty CurrentContext: %v", err)
	}
	emptyEntered, err := iso.EnteredOrMicrotaskContext(scope)
	if err != nil {
		t.Fatalf("empty EnteredOrMicrotaskContext: %v", err)
	}
	for name, ref := range map[string]*gov8.ContextRef{"current": emptyCurrent, "entered": emptyEntered} {
		if empty, err := ref.IsEmpty(); err != nil || !empty {
			t.Fatalf("%s IsEmpty = %v, %v", name, empty, err)
		}
		if _, err := ref.GlobalObject(scope); err == nil {
			t.Fatalf("%s empty GlobalObject succeeded", name)
		}
		if _, err := ref.Enter(); err == nil {
			t.Fatalf("%s empty Enter succeeded", name)
		}
	}
	if same, err := emptyCurrent.SameAsRef(emptyEntered); err != nil || !same {
		t.Fatalf("empty refs SameAsRef = %v, %v", same, err)
	}
}

func TestContextRefLifecycleAffinityAndForeignScope(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	entered, err := ctx.Enter()
	if err != nil {
		t.Fatalf("Context.Enter: %v", err)
	}
	ref, err := iso.CurrentContext(scope)
	if err != nil {
		t.Fatalf("CurrentContext: %v", err)
	}
	borrowed, err := ref.Enter()
	if err != nil {
		t.Fatalf("ContextRef.Enter: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- borrowed.Close()
	}()
	if err := <-errCh; err == nil {
		t.Fatal("borrowed ContextScope.Close on foreign thread succeeded")
	}
	if err := borrowed.Close(); err != nil {
		t.Fatalf("borrowed ContextScope.Close: %v", err)
	}
	go func() {
		_, err := ref.Enter()
		errCh <- err
	}()
	if err := <-errCh; err == nil {
		t.Fatal("ContextRef.Enter on foreign thread succeeded")
	}

	foreignISO := newIso(t)
	foreignCtx := newCtx(t, foreignISO)
	foreignScope := newScope(t, foreignISO)
	foreignEntered, err := foreignCtx.Enter()
	if err != nil {
		t.Fatalf("foreign Context.Enter: %v", err)
	}
	foreignRef, err := foreignISO.CurrentContext(foreignScope)
	if err != nil {
		t.Fatalf("foreign CurrentContext: %v", err)
	}
	if same, err := ref.SameAsRef(foreignRef); err != nil || same {
		t.Fatalf("SameAsRef(foreign) = %v, %v", same, err)
	}
	if _, err := ref.GlobalObject(foreignScope); err == nil {
		t.Fatal("ContextRef.GlobalObject with foreign scope succeeded")
	}
	if err := foreignEntered.Close(); err != nil {
		t.Errorf("foreign ContextScope.Close: %v", err)
	}
	if err := foreignScope.Close(); err != nil {
		t.Errorf("foreign Scope.Close: %v", err)
	}
	if err := foreignCtx.Close(); err != nil {
		t.Errorf("foreign Context.Close: %v", err)
	}
	if err := foreignISO.Close(); err != nil {
		t.Errorf("foreign Isolate.Close: %v", err)
	}

	if err := entered.Close(); err != nil {
		t.Fatalf("ContextScope.Close: %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("Scope.Close: %v", err)
	}
	if _, err := ref.IsEmpty(); err == nil {
		t.Fatal("ContextRef.IsEmpty after Scope.Close succeeded")
	}
	if _, err := ref.Enter(); err == nil {
		t.Fatal("ContextRef.Enter after Scope.Close succeeded")
	}
	if err := ctx.Close(); err != nil {
		t.Errorf("Context.Close: %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Errorf("Isolate.Close: %v", err)
	}
}

func TestContextRefRejectsNonCurrentResultScope(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	outerScope := newScope(t, iso)
	defer func() { _ = outerScope.Close() }()
	entered, err := ctx.Enter()
	if err != nil {
		t.Fatalf("Context.Enter: %v", err)
	}
	defer func() { _ = entered.Close() }()
	outerRef, err := iso.CurrentContext(outerScope)
	if err != nil {
		t.Fatalf("CurrentContext(outer): %v", err)
	}
	obj, err := outerScope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}

	innerScope := newScope(t, iso)
	borrowed, err := outerRef.Enter()
	if err != nil {
		t.Fatalf("ContextRef.Enter from non-current source Scope: %v", err)
	}
	borrowedCurrent, err := iso.CurrentContext(innerScope)
	if err != nil {
		t.Fatalf("CurrentContext(inner while borrowed entered): %v", err)
	}
	if same, err := borrowedCurrent.SameAsRef(outerRef); err != nil || !same {
		t.Fatalf("borrowed current.SameAsRef(source) = %v, %v", same, err)
	}
	if err := borrowed.Close(); err != nil {
		t.Fatalf("borrowed ContextScope.Close with non-current source Scope: %v", err)
	}
	if ref, err := iso.CurrentContext(outerScope); err == nil || ref != nil {
		t.Fatalf("CurrentContext(non-current outer) = %v, %v", ref, err)
	}
	if ref, err := iso.EnteredOrMicrotaskContext(outerScope); err == nil || ref != nil {
		t.Fatalf("EnteredOrMicrotaskContext(non-current outer) = %v, %v", ref, err)
	}
	if global, err := outerRef.GlobalObject(outerScope); err == nil || global != nil {
		t.Fatalf("GlobalObject(non-current outer) = %v, %v", global, err)
	}
	if ref, present, err := obj.CreationContext(outerScope); err == nil || ref != nil || present {
		t.Fatalf("CreationContext(non-current outer) = %v, %v, %v", ref, present, err)
	}

	innerRef, err := iso.CurrentContext(innerScope)
	if err != nil {
		t.Fatalf("CurrentContext(inner): %v", err)
	}
	if _, err := innerRef.GlobalObject(innerScope); err != nil {
		t.Fatalf("GlobalObject(inner): %v", err)
	}
	if err := innerScope.Close(); err != nil {
		t.Fatalf("inner Scope.Close: %v", err)
	}
	if _, err := innerRef.IsEmpty(); err == nil {
		t.Fatal("inner ContextRef survived inner Scope.Close")
	}
	if _, err := outerRef.GlobalObject(outerScope); err != nil {
		t.Fatalf("outer ContextRef unusable after inner close: %v", err)
	}
}
