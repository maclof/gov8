//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"testing"

	gov8 "gov8"
)

func TestModuleInstantiatePriorStatusErrorsAreExact(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	module, err := ctx.CompileModule(scope, "export const answer = 42;", "status.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeModuleRuntime(t, []*gov8.Module{module}, scope, ctx, iso)

	resolver := func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
		return nil, fmt.Errorf("resolver unexpectedly called")
	}
	linked, err := module.Instantiate(scope, resolver, nil)
	if err != nil || !linked {
		t.Fatalf("first Instantiate = %v, %v", linked, err)
	}
	linked, err = module.Instantiate(scope, resolver, nil)
	if linked || err == nil || err.Error() != "gov8: Instantiate requires Uninstantiated module, got Instantiated" {
		t.Fatalf("second Instantiate = %v, %q", linked, err)
	}
	if _, err := module.Evaluate(scope, nil); err != nil {
		t.Fatal(err)
	}
	linked, err = module.Instantiate(scope, resolver, nil)
	if linked || err == nil || err.Error() != "gov8: Instantiate requires Uninstantiated module, got Evaluated" {
		t.Fatalf("post-evaluate Instantiate = %v, %q", linked, err)
	}
}

func TestModuleInstantiateResolverMayReenterInstantiate(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	outer, err := ctx.CompileModule(scope, "import 'outer-dependency';", "outer.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	outerDependency, err := ctx.CompileModule(scope, "export const outer = 1;", "outer-dependency.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := ctx.CompileModule(scope, "import 'inner-dependency';", "inner.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	innerDependency, err := ctx.CompileModule(scope, "export const inner = 2;", "inner-dependency.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	modules := []*gov8.Module{outer, outerDependency, inner, innerDependency}
	defer closeModuleRuntime(t, modules, scope, ctx, iso)

	innerCalls := 0
	outerCalls := 0
	linked, err := outer.Instantiate(scope, func(request gov8.ModuleResolveRequest) (*gov8.Module, error) {
		outerCalls++
		if request.Specifier != "outer-dependency" {
			return nil, fmt.Errorf("outer resolver specifier = %q", request.Specifier)
		}
		innerLinked, innerErr := inner.Instantiate(scope, func(request gov8.ModuleResolveRequest) (*gov8.Module, error) {
			innerCalls++
			if request.Specifier != "inner-dependency" {
				return nil, fmt.Errorf("inner resolver specifier = %q", request.Specifier)
			}
			return innerDependency, nil
		}, nil)
		if innerErr != nil || !innerLinked {
			return nil, fmt.Errorf("nested Instantiate = %v, %v", innerLinked, innerErr)
		}
		return outerDependency, nil
	}, nil)
	if err != nil || !linked || outerCalls != 1 || innerCalls != 1 {
		t.Fatalf("outer Instantiate = %v, %v; calls outer=%d inner=%d",
			linked, err, outerCalls, innerCalls)
	}
	outerStatus, outerErr := outer.Status()
	innerStatus, innerErr := inner.Status()
	if outerErr != nil || innerErr != nil || outerStatus != gov8.ModuleInstantiated || innerStatus != gov8.ModuleInstantiated {
		t.Fatalf("statuses outer=%v/%v inner=%v/%v", outerStatus, outerErr, innerStatus, innerErr)
	}
}
