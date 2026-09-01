//go:build windows && amd64

package gov8

import (
	"strings"
	"testing"
)

// TestInspectorStackTraceCloseAfterIsolate pins the lifecycle exception for
// this owned type: V8StackTraceImpl::~V8StackTraceImpl is defaulted and only
// releases copied/shared frame state, so deletion does not enter a live V8
// isolate and remains safe after Inspector and isolate disposal.
func TestInspectorStackTraceCloseAfterIsolate(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
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
	session, err := inspector.Connect(1, iowTestChannel{}, EmptyInspectorStringView(), InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	entered, err := ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	var owned *InspectorStackTrace
	t.Cleanup(func() {
		if owned != nil && !owned.closed && !owned.consumed {
			_ = owned.Close()
		}
		if entered != nil && !entered.closed {
			_ = entered.Close()
		}
		if session != nil && !session.closed {
			_ = session.Close()
		}
		if inspector != nil && ctx != nil {
			if _, registered := inspector.contexts[ctx]; registered {
				_ = inspector.ContextDestroyed(ctx)
			}
		}
		if scope != nil && !scope.closed {
			_ = scope.Close()
		}
		if ctx != nil && !ctx.closed {
			_ = ctx.Close()
		}
		if inspector != nil && !inspector.closed {
			_ = inspector.Close()
		}
		if iso != nil && !iso.closed {
			_ = ReleaseIsolateHostState(iso)
			_ = iso.Close()
		}
	})

	for _, request := range []string{
		`{"id":1,"method":"Debugger.enable"}`,
		`{"id":2,"method":"Debugger.setAsyncCallStackDepth","params":{"maxDepth":8}}`,
		`{"id":3,"method":"Runtime.enable"}`,
	} {
		if err := session.DispatchProtocolMessage(NewInspectorStringView8([]byte(request))); err != nil {
			t.Fatal(err)
		}
	}
	script, err := ctx.CompileWithOrigin(scope, "new Error('late close')",
		&Origin{ResourceName: "late-close.js"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	exception, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := script.Close(); err != nil {
		t.Fatal(err)
	}
	stack, ok, err := ExceptionStackTrace(scope, exception)
	if err != nil || !ok {
		t.Fatalf("ExceptionStackTrace = %v, %v", ok, err)
	}
	owned, ok, err = inspector.CreateInspectorStackTrace(stack)
	if err != nil || !ok {
		t.Fatalf("CreateInspectorStackTrace = %v, %v", ok, err)
	}

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := entered.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- owned.Close() }()
	if err := <-errCh; err != nil {
		t.Fatalf("Close after isolate disposal = %v", err)
	}
	if err := owned.Close(); err == nil || !strings.Contains(err.Error(), "already closed") {
		t.Fatalf("second Close = %v", err)
	}
}

func TestInspectorRuntimeEventForgedLifecycleGuards(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	local := &StackTrace{iso: iso, sc: scope, h: 1}
	if _, _, err := inspector.CreateInspectorStackTrace(local); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("closed local stack trace = %v", err)
	}
	other, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	otherScope, err := other.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	foreign := &StackTrace{iso: other, sc: otherScope, h: 1}
	if _, _, err := inspector.CreateInspectorStackTrace(foreign); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign local stack trace = %v", err)
	}
	if err := otherScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(other); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
