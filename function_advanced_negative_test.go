//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestFunctionAdvancedLifecycleAndWrongThread(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	function, err := iso.NewFunction(scope, ctx, advancedNoop, nil)
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- function.SetName("wrong-thread") }()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := function.Name(); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("use-after-scope error = %v", err)
	}
	_ = ctx.Close()
	_ = iso.Close()
}

func TestFunctionAdvancedCrossIsolateRejections(t *testing.T) {
	isoA, ctxA, scopeA := newTestRuntime(t)
	isoB, ctxB, scopeB := newTestRuntime(t)
	_ = ctxA
	_ = isoB
	if _, err := isoA.FunctionBuilder(advancedNoop).Build(scopeB, ctxB); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign builder error = %v", err)
	}
	if _, _, err := ctxB.CompileFunctionAdvanced(scopeA, "return 1", nil, nil, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope error = %v", err)
	}
}

func TestFunctionAdvancedOptionBoundaries(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	for index, length := range []int{-1, 65535, int(^uint32(0) >> 1)} {
		function, err := iso.NewFunction(scope, ctx, advancedNoop, &gov8.FunctionOptions{Length: length})
		if err != nil {
			t.Fatalf("length %d: %v", length, err)
		}
		name := "lengthBoundary" + string(rune('A'+index))
		if !seedGlobal(t, ctx, scope, name, function.Value) {
			t.Fatalf("set %s", name)
		}
		if got, ok := evalText(t, ctx, scope, name+".length"); !ok || got != "65535" {
			t.Fatalf("length %d observed = %q, %v; want 65535", length, got, ok)
		}
	}
	if int64(^uint32(0)>>1)+1 <= int64(^uint(0)>>1) {
		tooLarge := int(int64(^uint32(0)>>1) + 1)
		if _, err := iso.NewFunction(scope, ctx, advancedNoop, &gov8.FunctionOptions{Length: tooLarge}); err == nil || !strings.Contains(err.Error(), "int32 range") {
			t.Fatalf("out-of-range length error = %v", err)
		}
	}
	if _, err := iso.NewFunction(scope, ctx, advancedNoop, &gov8.FunctionOptions{
		ConstructorBehavior: gov8.ConstructorBehavior(255),
	}); err == nil || !strings.Contains(err.Error(), "constructor behavior") {
		t.Fatalf("invalid constructor behavior error = %v", err)
	}
	if _, err := iso.NewFunction(scope, ctx, advancedNoop, &gov8.FunctionOptions{
		SideEffectType: gov8.SideEffectType(255),
	}); err == nil || !strings.Contains(err.Error(), "side-effect type") {
		t.Fatalf("invalid side-effect type error = %v", err)
	}
	if _, err := iso.NewFunction(scope, ctx, nil, nil); err == nil || !strings.Contains(err.Error(), "nil callback") {
		t.Fatalf("nil callback error = %v", err)
	}
}
