//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"
	"unsafe"

	gov8 "gov8"
)

func TestScriptRunScratchPreservesWrapperSize(t *testing.T) {
	if got := unsafe.Sizeof(gov8.Script{}); got != 32 {
		t.Fatalf("sizeof(Script) = %d, want 32", got)
	}
}

func TestScriptRunScratchNestedSameScript(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var script *gov8.Script
	nestedCalls := 0
	nestedResult := int32(0)

	reenter, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			nestedCalls++
			result, runErr := script.Run(cs.Scope(), nil)
			if runErr != nil {
				panic(runErr)
			}
			value, ok, valueErr := result.Int32Value(ctx)
			if valueErr != nil || !ok {
				panic("nested Script.Run did not return an Int32")
			}
			nestedResult = value
			if setErr := rv.Set(result); setErr != nil {
				panic(setErr)
			}
		}, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "__scriptScratchReenter", reenter.Value) {
		t.Fatal("seeding reentry callback failed")
	}

	script, err = ctx.Compile(scope,
		"globalThis.__scriptScratchDepth=(globalThis.__scriptScratchDepth||0)+1;"+
			"__scriptScratchDepth===1 ? __scriptScratchReenter()+1 : 40", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := script.Close(); closeErr != nil {
			t.Errorf("script.Close: %v", closeErr)
		}
	})

	result, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("outer Run: %v", err)
	}
	outer, ok, err := result.Int32Value(ctx)
	if err != nil || !ok || outer != 41 {
		t.Fatalf("outer result = %d/%v (%v), want 41/true", outer, ok, err)
	}
	if nestedCalls != 1 || nestedResult != 40 {
		t.Fatalf("nested calls/result = %d/%d, want 1/40", nestedCalls, nestedResult)
	}
}

func TestScriptRunScratchExceptionTryCatchRecovery(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	script, err := ctx.Compile(scope,
		"globalThis.__scriptScratchRuns=(globalThis.__scriptScratchRuns||0)+1;"+
			"if(__scriptScratchRuns===2){throw new Error('scratch-boom')} __scriptScratchRuns", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := script.Close(); closeErr != nil {
			t.Errorf("script.Close: %v", closeErr)
		}
	})

	first, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	firstValue, ok, err := first.Int32Value(ctx)
	if err != nil || !ok || firstValue != 1 {
		t.Fatalf("first result = %d/%v (%v), want 1/true", firstValue, ok, err)
	}

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := tc.Close(); closeErr != nil {
			t.Errorf("TryCatch.Close: %v", closeErr)
		}
	})
	failed, runErr := script.Run(scope, tc)
	if runErr == nil {
		t.Fatal("second Run unexpectedly succeeded")
	}
	if failed != (gov8.Value{}) {
		t.Fatal("failed Run returned a non-zero Value")
	}
	caught, err := tc.HasCaught()
	if err != nil || !caught {
		t.Fatalf("HasCaught = %v (%v), want true", caught, err)
	}
	exception, err := tc.ExceptionText(scope, ctx)
	if err != nil || !strings.Contains(exception, "scratch-boom") {
		t.Fatalf("ExceptionText = %q (%v), want scratch-boom", exception, err)
	}
	if err := tc.Reset(); err != nil {
		t.Fatalf("TryCatch.Reset: %v", err)
	}

	third, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("third Run after exception: %v", err)
	}
	thirdValue, ok, err := third.Int32Value(ctx)
	if err != nil || !ok || thirdValue != 3 {
		t.Fatalf("third result = %d/%v (%v), want 3/true", thirdValue, ok, err)
	}
}
