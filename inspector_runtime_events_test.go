//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func closeRuntimeEventInspector(t *testing.T, inspector *gov8.Inspector) {
	t.Helper()
	if err := inspector.Close(); err != nil {
		t.Error(err)
	}
}

func enableRuntimeEventInspector(t *testing.T, inspector *gov8.Inspector, ctx *gov8.Context) (*gov8.InspectorSession, *recordingInspectorChannel) {
	t.Helper()
	if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(),
		gov8.NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
		t.Fatal(err)
	}
	channel := &recordingInspectorChannel{}
	session, err := inspector.Connect(1, channel,
		gov8.NewInspectorStringView8([]byte(`{}`)), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{
		`{"id":1,"method":"Debugger.enable"}`,
		`{"id":2,"method":"Debugger.setAsyncCallStackDepth","params":{"maxDepth":8}}`,
		`{"id":3,"method":"Runtime.enable"}`,
	} {
		if err := session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(request))); err != nil {
			t.Fatal(err)
		}
	}
	return session, channel
}

func TestInspectorRuntimeEventTransitionsAndNullTask(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRuntimeEventInspector(t, inspector) })

	for name, call := range map[string]func() error{
		"finish before start": inspector.IdleFinished,
		"start":               inspector.IdleStarted,
		"repeated start":      inspector.IdleStarted,
		"finish":              inspector.IdleFinished,
		"repeated finish":     inspector.IdleFinished,
		"schedule zero": func() error {
			return inspector.AsyncTaskScheduled(gov8.EmptyInspectorStringView(), 0, false)
		},
		"start zero":  func() error { return inspector.AsyncTaskStarted(0) },
		"finish zero": func() error { return inspector.AsyncTaskFinished(0) },
		"cancel zero": func() error { return inspector.AsyncTaskCanceled(0) },
		"cancel all":  inspector.AllAsyncTasksCanceled,
	} {
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	errCh := make(chan error, 1)
	go func() { errCh <- inspector.AsyncTaskStarted(1) }()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread event = %v", err)
	}
}

func TestCreateInspectorStackTraceNilAndLifecycle(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}

	if trace, ok, err := inspector.CreateInspectorStackTrace(nil); err != nil || ok || trace != nil {
		t.Fatalf("CreateInspectorStackTrace(nil) = %v, %v, %v", trace, ok, err)
	}
	session, _ := enableRuntimeEventInspector(t, inspector, ctx)

	entered, err := ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	script, err := ctx.CompileWithOrigin(scope,
		"function outer(){return inner()} function inner(){return new Error('boom')} outer()",
		&gov8.Origin{ResourceName: "runtime-events.js"}, nil)
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
	stack, ok, err := gov8.ExceptionStackTrace(scope, exception)
	if err != nil || !ok {
		t.Fatalf("ExceptionStackTrace = %v, %v", ok, err)
	}
	owned, ok, err := inspector.CreateInspectorStackTrace(stack)
	if err != nil || !ok || owned == nil {
		t.Fatalf("CreateInspectorStackTrace = %v, %v, %v", owned, ok, err)
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
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}

	// The owned snapshot has copied frame state and may be released after its
	// Inspector, including from a foreign thread.
	errCh := make(chan error, 1)
	go func() { errCh <- owned.Close() }()
	if err := <-errCh; err != nil {
		t.Fatalf("foreign-thread Close after Inspector.Close = %v", err)
	}
	if err := owned.Close(); err == nil || !strings.Contains(err.Error(), "already closed") {
		t.Fatalf("second Close = %v", err)
	}
}

func TestInspectorExceptionThrownConsumesTraceWithoutRegistration(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := enableRuntimeEventInspector(t, inspector, ctx)
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if err := inspector.Close(); err != nil {
			t.Error(err)
		}
	})
	entered, err := ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := entered.Close(); err != nil {
			t.Error(err)
		}
	}()

	script, err := ctx.CompileWithOrigin(scope, "new Error('unregistered')",
		&gov8.Origin{ResourceName: "runtime-events-unregistered.js"}, nil)
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
	stack, ok, err := gov8.ExceptionStackTrace(scope, exception)
	if err != nil || !ok {
		t.Fatalf("ExceptionStackTrace = %v, %v", ok, err)
	}
	owned, ok, err := inspector.CreateInspectorStackTrace(stack)
	if err != nil || !ok {
		t.Fatalf("CreateInspectorStackTrace = %v, %v", ok, err)
	}
	if err := inspector.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := inspector.ExceptionThrown(scope, ctx,
		gov8.NewInspectorStringView8([]byte("after destroy")), exception,
		gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView(),
		1, 1, owned, 0)
	if err != nil || id != 0 {
		t.Fatalf("ExceptionThrown = %d, %v", id, err)
	}
	if err := owned.Close(); err == nil || !strings.Contains(err.Error(), "consumed") {
		t.Fatalf("Close consumed trace = %v", err)
	}
	if _, err := inspector.ExceptionThrown(scope, ctx,
		gov8.EmptyInspectorStringView(), exception,
		gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView(),
		0, 0, owned, 0); err == nil || !strings.Contains(err.Error(), "consumed") {
		t.Fatalf("reuse consumed trace = %v", err)
	}
	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	if id, err := inspector.ExceptionThrown(scope, ctx,
		gov8.NewInspectorStringView16([]uint16{'n', 'o', ' ', 's', 't', 'a', 'c', 'k'}), undefined,
		gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView(),
		0, 0, nil, 0); err != nil || id != 0 {
		t.Fatalf("ExceptionThrown(nil stack) = %d, %v", id, err)
	}
}

func TestInspectorExceptionThrownNotificationSemantics(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	session, channel := enableRuntimeEventInspector(t, inspector, ctx)
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if err := inspector.ContextDestroyed(ctx); err != nil {
			t.Error(err)
		}
		if err := inspector.Close(); err != nil {
			t.Error(err)
		}
	})
	entered, err := ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := entered.Close(); err != nil {
			t.Error(err)
		}
	}()

	script, err := ctx.CompileWithOrigin(scope,
		"function outerEvent(){return innerEvent()} function innerEvent(){return new Error('oracle boom')} outerEvent()",
		&gov8.Origin{ResourceName: "event-source.js"}, nil)
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
	stack, ok, err := gov8.ExceptionStackTrace(scope, exception)
	if err != nil || !ok {
		t.Fatalf("ExceptionStackTrace = %v, %v", ok, err)
	}
	owned, ok, err := inspector.CreateInspectorStackTrace(stack)
	if err != nil || !ok {
		t.Fatalf("CreateInspectorStackTrace = %v, %v", ok, err)
	}
	before := len(channel.notifications)
	id, err := inspector.ExceptionThrown(scope, ctx,
		gov8.NewInspectorStringView8([]byte("oracle exception")), exception,
		gov8.NewInspectorStringView8([]byte("oracle detailed")),
		gov8.NewInspectorStringView8([]byte("embedder://event")),
		7, 9, owned, 42)
	if err != nil || id != 1 {
		t.Fatalf("ExceptionThrown = %d, %v", id, err)
	}
	if len(channel.notifications) != before+1 {
		t.Fatalf("notifications = %d, want %d", len(channel.notifications), before+1)
	}
	message := channel.notifications[len(channel.notifications)-1]
	for name, fragment := range map[string]string{
		"method":   `"method":"Runtime.exceptionThrown"`,
		"line":     `"lineNumber":6`,
		"column":   `"columnNumber":8`,
		"script":   `"scriptId":"42"`,
		"detailed": `"text":"oracle detailed"`,
		"url":      `"url":"embedder://event"`,
		"inner":    "innerEvent",
		"outer":    "outerEvent",
	} {
		if !strings.Contains(message, fragment) {
			t.Errorf("%s missing from notification %s", name, message)
		}
	}
	if strings.Contains(message, "oracle exception") {
		t.Errorf("original message unexpectedly visible: %s", message)
	}
	if strings.Contains(message, `"exception":`) {
		t.Errorf("exception object unexpectedly serialized: %s", message)
	}

	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	id, err = inspector.ExceptionThrown(scope, ctx,
		gov8.NewInspectorStringView8([]byte("without stack")), undefined,
		gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView(),
		0, 0, nil, 0)
	if err != nil || id != 2 {
		t.Fatalf("ExceptionThrown without stack = %d, %v", id, err)
	}
	message = channel.notifications[len(channel.notifications)-1]
	if !strings.Contains(message, `"text":"without stack"`) ||
		strings.Contains(message, `"stackTrace":`) ||
		!strings.Contains(message, `"lineNumber":0`) ||
		!strings.Contains(message, `"columnNumber":0`) {
		t.Fatalf("without-stack notification = %s", message)
	}
}

func TestInspectorRuntimeEventValidation(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRuntimeEventInspector(t, inspector) })
	exception, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	empty := gov8.EmptyInspectorStringView()
	if _, err := inspector.ExceptionThrown(nil, ctx, empty, exception, empty, empty, 0, 0, nil, 0); err == nil || !strings.Contains(err.Error(), "nil scope") {
		t.Fatalf("nil scope = %v", err)
	}
	if _, err := inspector.ExceptionThrown(scope, ctx, empty, exception, empty, empty, 0, 0, nil, 0); err == nil || !strings.Contains(err.Error(), "current entered context") {
		t.Fatalf("unentered context = %v", err)
	}

	entered, err := ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	inner, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.ExceptionThrown(scope, ctx, empty, exception, empty, empty, 0, 0, nil, 0); err == nil || !strings.Contains(err.Error(), "current innermost") {
		t.Fatalf("non-current scope = %v", err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := entered.Close(); err != nil {
		t.Fatal(err)
	}

	other, otherCtx, otherScope := newTestRuntime(t)
	_ = other
	otherValue, err := otherScope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.ExceptionThrown(otherScope, ctx, empty, exception, empty, empty, 0, 0, nil, 0); err == nil || !strings.Contains(err.Error(), "isolate") {
		t.Fatalf("foreign scope = %v", err)
	}
	entered, err = ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	defer entered.Close()
	if _, err := inspector.ExceptionThrown(scope, nil, empty, exception, empty, empty, 0, 0, nil, 0); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil context = %v", err)
	}
	if _, err := inspector.ExceptionThrown(scope, ctx, empty, gov8.Value{}, empty, empty, 0, 0, nil, 0); err == nil || !strings.Contains(err.Error(), "zero value") {
		t.Fatalf("zero exception = %v", err)
	}
	if _, err := inspector.ExceptionThrown(scope, otherCtx, empty, exception, empty, empty, 0, 0, nil, 0); err == nil || !strings.Contains(err.Error(), "isolate") {
		t.Fatalf("foreign context = %v", err)
	}
	if _, err := inspector.ExceptionThrown(scope, ctx, empty, otherValue, empty, empty, 0, 0, nil, 0); err == nil || !strings.Contains(err.Error(), "isolate") {
		t.Fatalf("foreign exception = %v", err)
	}
}
