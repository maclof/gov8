//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestExceptionConstructorsAndMessages(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	constructors := []struct {
		name string
		new  func(*gov8.Scope, string) (gov8.Value, error)
	}{
		{"Error", ctx.NewError},
		{"RangeError", ctx.NewRangeError},
		{"ReferenceError", ctx.NewReferenceError},
		{"SyntaxError", ctx.NewSyntaxError},
		{"TypeError", ctx.NewTypeError},
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, constructor := range constructors {
		exception, err := constructor.new(scope, "oracle-message")
		if err != nil {
			t.Fatalf("%s: %v", constructor.name, err)
		}
		if got, err := exception.ToString(ctx); err != nil || got != constructor.name+": oracle-message" {
			t.Fatalf("ToString = %q, %v", got, err)
		}
		if native, err := exception.IsNativeError(); err != nil || !native {
			t.Fatalf("IsNativeError = %v, %v", native, err)
		}
		object, err := gov8.AsObject(exception)
		if err != nil {
			t.Fatal(err)
		}
		messageProperty, ok, err := object.GetByName(scope, ctx, "message")
		if err != nil || !ok {
			t.Fatalf("message property = %v, %v", ok, err)
		}
		if got, _ := messageProperty.ToString(ctx); got != "oracle-message" {
			t.Fatalf("message property = %q", got)
		}
		constructorValue, ok, err := global.GetByName(scope, ctx, constructor.name)
		if err != nil || !ok {
			t.Fatalf("global constructor = %v, %v", ok, err)
		}
		constructorObject, err := gov8.AsObject(constructorValue)
		if err != nil {
			t.Fatal(err)
		}
		if matches, err := exception.InstanceOf(scope, ctx, constructorObject, nil); err != nil || !matches {
			t.Fatalf("InstanceOf = %v, %v", matches, err)
		}
		message, err := ctx.CreateMessage(scope, exception)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := message.Text(ctx); err != nil || got != "Uncaught "+constructor.name+": oracle-message" {
			t.Fatalf("message text = %q, %v", got, err)
		}
		if trace, ok, err := ctx.GetExceptionStackTrace(scope, exception); err != nil || ok || trace != nil {
			t.Fatalf("native trace = %v, %v, %v", trace, ok, err)
		}
	}
}

func TestExceptionConstructorMessageBoundaries(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	empty, err := ctx.NewTypeError(scope, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := empty.ToString(ctx); got != "TypeError" {
		t.Fatalf("empty TypeError = %q", got)
	}
	emptyMessage, _ := ctx.CreateMessage(scope, empty)
	if got, _ := emptyMessage.Text(ctx); got != "Uncaught TypeError" {
		t.Fatalf("empty message = %q", got)
	}
	multiline, err := ctx.NewError(scope, "first\nsecond 🦀")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := multiline.ToString(ctx); got != "Error: first\nsecond 🦀" {
		t.Fatalf("multiline Error = %q", got)
	}
	multilineMessage, _ := ctx.CreateMessage(scope, multiline)
	if got, _ := multilineMessage.Text(ctx); got != "Uncaught Error: first\nsecond 🦀" {
		t.Fatalf("multiline message = %q", got)
	}
}

func TestExceptionConstructorValidation(t *testing.T) {
	isoA, ctxA, scopeA := newTestRuntime(t)
	_, ctxB, scopeB := newTestRuntime(t)
	if _, err := ctxA.NewError(scopeB, "foreign"); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope error = %v", err)
	}
	foreign, err := ctxB.NewError(scopeB, "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.CreateMessage(scopeA, foreign); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign value error = %v", err)
	}
	if _, _, err := ctxA.GetExceptionStackTrace(scopeA, foreign); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign stack value error = %v", err)
	}
	if _, err := ctxA.NewError(nil, "nil"); err == nil {
		t.Fatal("nil scope accepted")
	}
	var nilContext *gov8.Context
	if _, err := nilContext.NewError(scopeA, "nil"); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := scopeA.NewError("no-entered-context"); err == nil || !strings.Contains(err.Error(), "requires an entered context") {
		t.Fatalf("legacy no-context constructor error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := ctxA.NewError(scopeA, "wrong-thread")
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	closedScope, err := isoA.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.NewTypeError(closedScope, "closed"); err == nil {
		t.Fatal("closed scope accepted")
	}
	closedContext, err := isoA.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedContext.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closedContext.NewError(scopeA, "closed"); err == nil {
		t.Fatal("closed context accepted")
	}
	local, err := ctxA.NewError(scopeA, "local")
	if err != nil {
		t.Fatal(err)
	}
	message, err := ctxA.CreateMessage(scopeA, local)
	if err != nil {
		t.Fatal(err)
	}
	for name, getter := range map[string]func() (int64, error){
		"start position": message.StartPosition, "end position": message.EndPosition,
		"start column": message.StartColumn, "end column": message.EndColumn,
	} {
		if got, err := getter(); err != nil || got != -1 {
			t.Fatalf("native message %s = %d, %v; want -1", name, got, err)
		}
	}
	if _, ok, err := message.StackTrace(); err != nil || ok {
		t.Fatalf("message stack = %v, %v", ok, err)
	}
}
