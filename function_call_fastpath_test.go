//go:build windows && amd64

package gov8

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func closeFunctionCallRuntime(t *testing.T, iso *Isolate, ctx *Context, scope *Scope) {
	t.Helper()
	if err := scope.Close(); err != nil {
		t.Error(err)
	}
	if err := ctx.Close(); err != nil {
		t.Error(err)
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Error(err)
	}
	if err := iso.Close(); err != nil {
		t.Error(err)
	}
}

func compileFunctionCallTarget(t *testing.T, ctx *Context, scope *Scope, source string) *Function {
	t.Helper()
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := script.Run(scope, nil)
	if closeErr := script.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	function, ok, err := AsFunction(value, ctx)
	if err != nil || !ok {
		t.Fatalf("AsFunction = %v, %v", ok, err)
	}
	return function
}

func TestFunctionCallSameScopeShapesAndException(t *testing.T) {
	iso, ctx, scope := fastTestRuntime(t)
	defer closeFunctionCallRuntime(t, iso, ctx, scope)
	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	argument, err := scope.Number(7)
	if err != nil {
		t.Fatal(err)
	}

	length, err := iso.NewFunction(scope, ctx,
		func(_ *CallbackScope, args FunctionCallbackArguments, rv ReturnValue) {
			_ = rv.SetInt32(int32(args.Length()))
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []Value
		want int64
	}{
		{name: "zero", want: 0},
		{name: "one", args: []Value{argument}, want: 1},
		{name: "multiple", args: []Value{argument, argument}, want: 2},
	} {
		value, ok, err := length.Call(scope, undefined, test.args...)
		if err != nil || !ok {
			t.Fatalf("%s Call = %v, %v", test.name, ok, err)
		}
		got, converted, err := value.IntegerValue(ctx)
		if err != nil || !converted || got != test.want {
			t.Fatalf("%s result = %d, %v, %v; want %d", test.name, got, converted, err, test.want)
		}
	}
	if _, ok, err := length.Call(scope, Value{}, argument); err == nil || ok || !strings.Contains(err.Error(), "zero value") {
		t.Fatalf("zero receiver = %v, %v", ok, err)
	}
	if _, ok, err := length.Call(scope, undefined, Value{}); err == nil || ok || !strings.Contains(err.Error(), "zero value") {
		t.Fatalf("zero argument = %v, %v", ok, err)
	}

	thrower := compileFunctionCallTarget(t, ctx, scope, "(value)=>{throw new Error('function-call-fast-path')}")
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := thrower.Call(scope, undefined, argument); err != nil || ok {
		t.Fatalf("throwing Call = %v, %v", ok, err)
	}
	if caught, err := tc.HasCaught(); err != nil || !caught {
		t.Fatalf("TryCatch = %v, %v", caught, err)
	}
	if err := tc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionCallNestedCallbackReentry(t *testing.T) {
	iso, ctx, scope := fastTestRuntime(t)
	defer closeFunctionCallRuntime(t, iso, ctx, scope)
	inner := compileFunctionCallTarget(t, ctx, scope, "value=>value+1")
	var nestedErr error
	host, err := iso.NewFunction(scope, ctx,
		func(cs *CallbackScope, args FunctionCallbackArguments, rv ReturnValue) {
			argument, err := args.Get(0)
			if err != nil {
				nestedErr = err
				return
			}
			receiver, err := cs.Scope().Undefined()
			if err != nil {
				nestedErr = err
				return
			}
			result, ok, err := inner.Call(cs.Scope(), receiver, argument)
			if err != nil || !ok {
				nestedErr = fmt.Errorf("nested Call = %v, %w", ok, err)
				return
			}
			nestedErr = rv.Set(result)
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	argument, err := scope.Number(41)
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := host.Call(scope, undefined, argument)
	if err != nil || !ok || nestedErr != nil {
		t.Fatalf("outer Call = %v, %v; nested = %v", ok, err, nestedErr)
	}
	got, converted, err := result.IntegerValue(ctx)
	if err != nil || !converted || got != 42 {
		t.Fatalf("nested result = %d, %v, %v", got, converted, err)
	}
}

func TestFunctionCallValidationFastAndGeneralPaths(t *testing.T) {
	isoA, ctxA, scopeA := fastTestRuntime(t)
	isoB, ctxB, scopeB := fastTestRuntime(t)
	defer closeFunctionCallRuntime(t, isoA, ctxA, scopeA)
	defer closeFunctionCallRuntime(t, isoB, ctxB, scopeB)
	function := compileFunctionCallTarget(t, ctxA, scopeA, "value=>value")
	undefinedA, err := scopeA.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	argumentA, err := scopeA.Number(1)
	if err != nil {
		t.Fatal(err)
	}
	undefinedB, err := scopeB.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	argumentB, err := scopeB.Number(2)
	if err != nil {
		t.Fatal(err)
	}

	threadResult := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, _, err := function.Call(scopeA, undefinedA, argumentA)
		threadResult <- err
	}()
	if err := <-threadResult; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong-thread Call = %v", err)
	}
	if _, ok, err := function.Call(scopeB, undefinedB, argumentB); err == nil || ok || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope = %v, %v", ok, err)
	}
	if _, ok, err := function.Call(scopeA, undefinedA, argumentB); err == nil || ok || !strings.Contains(err.Error(), "argument belongs") {
		t.Fatalf("foreign argument = %v, %v", ok, err)
	}

	closedScope, err := isoA.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedFunction := compileFunctionCallTarget(t, ctxA, closedScope, "()=>1")
	closedReceiver, err := closedScope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := closedFunction.Call(closedScope, closedReceiver); err == nil || ok || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed scope = %v, %v", ok, err)
	}
}

func TestFunctionCallSameScopeAllocationCeiling(t *testing.T) {
	iso, ctx, scope := fastTestRuntime(t)
	defer closeFunctionCallRuntime(t, iso, ctx, scope)
	function := compileFunctionCallTarget(t, ctx, scope, "value=>value")
	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	argument, err := scope.Number(1)
	if err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(1000, func() {
		if _, ok, err := function.Call(scope, undefined, argument); err != nil || !ok {
			panic(fmt.Sprintf("Call = %v, %v", ok, err))
		}
	})
	if allocs > 2 {
		t.Fatalf("Function.Call allocations = %v, want <= 2", allocs)
	}
}
