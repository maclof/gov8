//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

type stringValueExceptionConstructor func(*gov8.Scope, gov8.Value) (gov8.Value, error)

type exceptionStringInput struct {
	name            string
	text            string
	units           []uint16
	value           gov8.Value
	external        bool
	externalOneByte bool
	externalTwoByte bool
}

func exceptionStringInputs(t *testing.T, scope *gov8.Scope) []exceptionStringInput {
	t.Helper()
	ordinary, err := scope.NewStringFromUTF8([]byte("héllo 🦀"), gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	embeddedNUL, err := scope.NewStringFromUTF8([]byte("left\x00right"), gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	loneSurrogate, err := scope.NewStringFromTwoByte([]uint16{0xd800, 'X'}, gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	internalized, err := scope.NewStringFromUTF8([]byte("internalized-value"), gov8.StringInternalized)
	if err != nil {
		t.Fatal(err)
	}
	externalOneByte, err := scope.NewExternalOneByteString([]byte{'e', 'x', 't', '-', 0xa9})
	if err != nil {
		t.Fatal(err)
	}
	externalTwoByte, err := scope.NewExternalTwoByteString([]uint16{'e', 'x', 't', 0x20ac})
	if err != nil {
		t.Fatal(err)
	}
	return []exceptionStringInput{
		{name: "ordinary_utf8", text: "héllo 🦀", units: []uint16{104, 233, 108, 108, 111, 32, 55358, 56704}, value: ordinary},
		{name: "embedded_nul", text: "left\x00right", units: []uint16{108, 101, 102, 116, 0, 114, 105, 103, 104, 116}, value: embeddedNUL},
		{name: "utf16_lone_surrogate", text: "�X", units: []uint16{0xd800, 'X'}, value: loneSurrogate},
		{name: "internalized", text: "internalized-value", units: []uint16{105, 110, 116, 101, 114, 110, 97, 108, 105, 122, 101, 100, 45, 118, 97, 108, 117, 101}, value: internalized},
		{name: "external_one_byte", text: "ext-©", units: []uint16{'e', 'x', 't', '-', 0xa9}, value: externalOneByte, external: true, externalOneByte: true},
		{name: "external_two_byte", text: "ext€", units: []uint16{'e', 'x', 't', 0x20ac}, value: externalTwoByte, external: true, externalTwoByte: true},
	}
}

func exceptionStringConstructors(ctx *gov8.Context) []struct {
	name string
	fn   stringValueExceptionConstructor
} {
	return []struct {
		name string
		fn   stringValueExceptionConstructor
	}{
		{"Error", ctx.NewErrorFromStringValue},
		{"RangeError", ctx.NewRangeErrorFromStringValue},
		{"ReferenceError", ctx.NewReferenceErrorFromStringValue},
		{"SyntaxError", ctx.NewSyntaxErrorFromStringValue},
		{"TypeError", ctx.NewTypeErrorFromStringValue},
	}
}

func exceptionMessageValue(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, exception gov8.Value) gov8.Value {
	t.Helper()
	object, err := exception.ToObject(scope, ctx, nil)
	if err != nil {
		t.Fatalf("exception.ToObject: %v", err)
	}
	key, err := scope.NewString("message")
	if err != nil {
		t.Fatalf("message key: %v", err)
	}
	message, found, err := object.GetRealNamedProperty(scope, ctx, key, nil)
	if err != nil || !found {
		t.Fatalf("error.message: found=%v err=%v", found, err)
	}
	return message
}

// TestExceptionStringLocalFiveConstructorsByStringKind mirrors
// exception-string-local/five_constructors_by_string_kind.
func TestExceptionStringLocalFiveConstructorsByStringKind(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	for _, input := range exceptionStringInputs(t, scope) {
		for _, constructor := range exceptionStringConstructors(ctx) {
			exception, err := constructor.fn(scope, input.value)
			if err != nil {
				t.Fatalf("%s/%s constructor: %v", input.name, constructor.name, err)
			}
			if object, err := exception.IsObject(); err != nil || !object {
				t.Fatalf("%s/%s IsObject = %v, %v", input.name, constructor.name, object, err)
			}
			if native, err := exception.IsNativeError(); err != nil || !native {
				t.Fatalf("%s/%s IsNativeError = %v, %v", input.name, constructor.name, native, err)
			}
			if stringValue, err := exception.IsString(); err != nil || stringValue {
				t.Fatalf("%s/%s IsString = %v, %v", input.name, constructor.name, stringValue, err)
			}
			if got, err := exception.ToString(ctx); err != nil || got != constructor.name+": "+input.text {
				t.Fatalf("%s/%s ToString = %q, %v", input.name, constructor.name, got, err)
			}
			message := exceptionMessageValue(t, ctx, scope, exception)
			if got, err := message.StringValue(); err != nil || got != input.text {
				t.Fatalf("%s/%s message = %q, %v", input.name, constructor.name, got, err)
			}
			createdMessage, err := ctx.CreateMessage(scope, exception)
			if err != nil {
				t.Fatalf("%s/%s CreateMessage: %v", input.name, constructor.name, err)
			}
			if got, err := createdMessage.Text(ctx); err != nil || got != "Uncaught "+constructor.name+": "+input.text {
				t.Fatalf("%s/%s uncaught message = %q, %v", input.name, constructor.name, got, err)
			}
			if trace, ok, err := ctx.GetExceptionStackTrace(scope, exception); err != nil || ok || trace != nil {
				t.Fatalf("%s/%s exception trace = %v, %v, %v", input.name, constructor.name, trace, ok, err)
			}
			if trace, ok, err := createdMessage.StackTrace(); err != nil || ok || trace != nil {
				t.Fatalf("%s/%s message trace = %v, %v, %v", input.name, constructor.name, trace, ok, err)
			}
		}
	}
}

// TestExceptionStringLocalInputAndMessageIdentity mirrors
// exception-string-local/input_and_message_identity. Strict equality, exact
// UTF-16 units, external flags, and external resource identity are preserved
// and asserted here.
func TestExceptionStringLocalInputAndMessageIdentity(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	for _, input := range exceptionStringInputs(t, scope) {
		inputResource, _, inputResourceOK, err := input.value.GetExternalStringResourceBase()
		if err != nil {
			t.Fatalf("%s input resource: %v", input.name, err)
		}
		for _, constructor := range exceptionStringConstructors(ctx) {
			exception, err := constructor.fn(scope, input.value)
			if err != nil {
				t.Fatalf("%s/%s constructor: %v", input.name, constructor.name, err)
			}
			message := exceptionMessageValue(t, ctx, scope, exception)
			if same, err := message.StrictEquals(input.value); err != nil || !same {
				t.Fatalf("%s/%s StrictEquals = %v, %v", input.name, constructor.name, same, err)
			}
			gotUnits := make([]uint16, len(input.units))
			if n, err := message.WriteTwoByte(0, gotUnits, 0); err != nil || n != len(input.units) {
				t.Fatalf("%s/%s WriteTwoByte = %d, %v", input.name, constructor.name, n, err)
			}
			for i := range input.units {
				if gotUnits[i] != input.units[i] {
					t.Fatalf("%s/%s UTF-16 unit %d = %#x, want %#x", input.name, constructor.name, i, gotUnits[i], input.units[i])
				}
			}
			external, err := message.IsExternalString()
			if err != nil || external != input.external {
				t.Fatalf("%s/%s IsExternalString = %v, %v", input.name, constructor.name, external, err)
			}
			externalOneByte, err := message.IsExternalOneByte()
			if err != nil || externalOneByte != input.externalOneByte {
				t.Fatalf("%s/%s IsExternalOneByte = %v, %v", input.name, constructor.name, externalOneByte, err)
			}
			externalTwoByte, err := message.IsExternalTwoByte()
			if err != nil || externalTwoByte != input.externalTwoByte {
				t.Fatalf("%s/%s IsExternalTwoByte = %v, %v", input.name, constructor.name, externalTwoByte, err)
			}
			propertyResource, _, propertyResourceOK, err := message.GetExternalStringResourceBase()
			if err != nil {
				t.Fatalf("%s/%s property resource: %v", input.name, constructor.name, err)
			}
			if input.external {
				if !inputResourceOK || !propertyResourceOK || propertyResource != inputResource {
					t.Fatalf("%s/%s external resource = %#x/%v, want %#x/%v", input.name, constructor.name, propertyResource, propertyResourceOK, inputResource, inputResourceOK)
				}
			} else if propertyResourceOK {
				t.Fatalf("%s/%s non-external property exposed resource %#x", input.name, constructor.name, propertyResource)
			}
		}
	}
}

func TestExceptionConstructorFromOuterStringIntoInnerScope(t *testing.T) {
	iso, ctx, outer := newTestRuntime(t)
	message, err := outer.NewStringFromTwoByte([]uint16{'o', 'u', 't', 'e', 'r'}, gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	exception, err := ctx.NewErrorFromStringValue(inner, message)
	if err != nil {
		t.Fatalf("outer String in inner constructor scope: %v", err)
	}
	if got, err := exception.ToString(ctx); err != nil || got != "Error: outer" {
		t.Fatalf("ToString = %q, %v", got, err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := exception.ToString(ctx); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("returned local after constructor scope close = %v", err)
	}
}

func TestExceptionConstructorFromStringValueValidation(t *testing.T) {
	isoA, ctxA, scopeA := newTestRuntime(t)
	_, _, scopeB := newTestRuntime(t)
	message, err := scopeA.NewString("valid")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ctxA.NewErrorFromStringValue(scopeA, gov8.Value{}); err == nil || !strings.Contains(err.Error(), "zero exception message") {
		t.Fatalf("zero message error = %v", err)
	}
	number, err := scopeA.Int32(42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.NewErrorFromStringValue(scopeA, number); err == nil || !strings.Contains(err.Error(), "not a String") {
		t.Fatalf("non-String error = %v", err)
	}
	foreign, err := scopeB.NewString("foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.NewErrorFromStringValue(scopeA, foreign); err == nil || !strings.Contains(err.Error(), "exception message belongs to a different isolate") {
		t.Fatalf("foreign message error = %v", err)
	}

	closedMessageScope, err := isoA.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedMessage, err := closedMessageScope.NewString("closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := closedMessageScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.NewErrorFromStringValue(scopeA, closedMessage); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("closed message error = %v", err)
	}

	closedDestination, err := isoA.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedDestination.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ctxA.NewErrorFromStringValue(closedDestination, message); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("closed destination scope error = %v", err)
	}

	closedContext, err := isoA.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedContext.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closedContext.NewErrorFromStringValue(scopeA, message); err == nil || !strings.Contains(err.Error(), "context used after Close") {
		t.Fatalf("closed context error = %v", err)
	}

	threadResult := make(chan error, 1)
	go func() {
		_, err := ctxA.NewErrorFromStringValue(scopeA, message)
		threadResult <- err
	}()
	if err := <-threadResult; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}

}
