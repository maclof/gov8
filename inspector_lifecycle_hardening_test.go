//go:build windows && amd64

package gov8

import (
	"strings"
	"sync"
	"testing"
)

func TestInspectorLifecycleRejectsPrematureContextIsolateAndHostClose(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, EmptyInspectorStringView(), EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}

	if err := ctx.Close(); err == nil || !strings.Contains(err.Error(), "active Inspector registration") {
		t.Fatalf("Context.Close with Inspector registration = %v", err)
	}
	if err := iso.Close(); err == nil || !strings.Contains(err.Error(), "live Inspector state") {
		t.Fatalf("Isolate.Close with Inspector = %v", err)
	}
	if err := ReleaseIsolateHostState(iso); err == nil || !strings.Contains(err.Error(), "live Inspector state") {
		t.Fatalf("ReleaseIsolateHostState with Inspector = %v", err)
	}

	wrongThread := make(chan error, 3)
	go func() {
		wrongThread <- ctx.Close()
		wrongThread <- ReleaseIsolateHostState(iso)
		wrongThread <- iso.Close()
	}()
	for operation := 0; operation < 3; operation++ {
		if err := <-wrongThread; err == nil || (!strings.Contains(err.Error(), "thread") && !strings.Contains(err.Error(), "affinity")) {
			t.Fatalf("wrong-thread operation %d = %v", operation, err)
		}
	}

	if err := inspector.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatalf("ReleaseIsolateHostState after Inspector teardown: %v", err)
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatalf("second ReleaseIsolateHostState: %v", err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectorLifecycleCountsSessionsAndRepeatedContextRegistrations(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	for _, inspector := range []*Inspector{first, second} {
		if err := inspector.ContextCreated(ctx, 1, EmptyInspectorStringView(), EmptyInspectorStringView()); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.ContextCreated(ctx, 1, EmptyInspectorStringView(), EmptyInspectorStringView()); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate context registration = %v", err)
	}
	session, err := first.Connect(1, &drainingInspectorChannel{}, EmptyInspectorStringView(), InspectorUntrusted)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(iso); err == nil || !strings.Contains(err.Error(), "sessions=1") {
		t.Fatalf("ReleaseIsolateHostState with live session = %v", err)
	}

	if err := first.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err == nil || !strings.Contains(err.Error(), "1 active Inspector registration") {
		t.Fatalf("Context.Close with second registration = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err == nil || !strings.Contains(err.Error(), "inspectors=1") {
		t.Fatalf("Isolate.Close with second Inspector = %v", err)
	}
	if err := second.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectorLifecycleRegistryConcurrentAccounting(t *testing.T) {
	iso := &Isolate{handle: 0x87654321}
	context := &Context{iso: iso, handle: 1}
	registerInspectorLifecycle(iso)
	t.Cleanup(func() { unregisterInspectorLifecycle(iso) })

	const iterations = 500
	var readers sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				_ = inspectorIsolateCloseError(iso)
				_ = inspectorContextCloseError(context)
			}
		}()
	}
	for iteration := 0; iteration < iterations; iteration++ {
		registerInspectorSessionLifecycle(iso)
		registerInspectorContextLifecycle(iso, context)
		unregisterInspectorContextLifecycle(iso, context)
		unregisterInspectorSessionLifecycle(iso)
	}
	readers.Wait()
	if err := inspectorContextCloseError(context); err != nil {
		t.Fatalf("context accounting did not drain: %v", err)
	}
	if err := inspectorIsolateCloseError(iso); err == nil || !strings.Contains(err.Error(), "inspectors=1") {
		t.Fatalf("base Inspector accounting was lost: %v", err)
	}
}

func TestInspectorLifecycleUsesIsolateIdentityNotNativeAddress(t *testing.T) {
	const reusedNativeAddress = uintptr(0x12345678)
	oldIsolate := &Isolate{handle: reusedNativeAddress}
	newIsolate := &Isolate{handle: reusedNativeAddress}
	oldContext := &Context{iso: oldIsolate, handle: 1}
	newContext := &Context{iso: newIsolate, handle: 1}

	registerInspectorLifecycle(oldIsolate)
	registerInspectorContextLifecycle(oldIsolate, oldContext)
	registerInspectorLifecycle(newIsolate)
	registerInspectorSessionLifecycle(newIsolate)
	registerInspectorContextLifecycle(newIsolate, newContext)
	t.Cleanup(func() {
		unregisterInspectorContextLifecycle(oldIsolate, oldContext)
		unregisterInspectorLifecycle(oldIsolate)
		unregisterInspectorContextLifecycle(newIsolate, newContext)
		unregisterInspectorSessionLifecycle(newIsolate)
		unregisterInspectorLifecycle(newIsolate)
	})

	unregisterInspectorContextLifecycle(oldIsolate, oldContext)
	unregisterInspectorLifecycle(oldIsolate)
	if err := inspectorIsolateCloseError(oldIsolate); err != nil {
		t.Fatalf("old isolate retained stale lifecycle state: %v", err)
	}
	if err := inspectorIsolateCloseError(newIsolate); err == nil || !strings.Contains(err.Error(), "sessions=1") {
		t.Fatalf("new isolate state was removed by reused-address cleanup: %v", err)
	}
	if err := inspectorContextCloseError(newContext); err == nil || !strings.Contains(err.Error(), "active Inspector registration") {
		t.Fatalf("new context state was removed by reused-address cleanup: %v", err)
	}
}
