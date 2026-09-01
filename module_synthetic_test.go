//go:build windows && amd64

package gov8_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func syntheticInt(e *gov8.SyntheticModuleEvaluation, value int32) gov8.Value {
	result, err := e.Scope().Scope().Int32(value)
	if err != nil {
		panic(err)
	}
	return result
}

func TestSyntheticModuleCreateEvaluateAndUpdateExports(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var calls atomic.Int32
	var captured *gov8.SyntheticModuleEvaluation
	module, err := ctx.NewSyntheticModule(scope, "virtual:synthetic", []string{"a", "b"},
		func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
			captured = e
			calls.Add(1)
			if err := e.SetExport("a", syntheticInt(e, 1)); err != nil {
				return gov8.Value{}, err
			}
			if err := e.SetExport("b", syntheticInt(e, 2)); err != nil {
				return gov8.Value{}, err
			}
			return syntheticInt(e, 77), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close()
	if source, _ := module.IsSourceTextModule(); source {
		t.Fatal("synthetic module reports SourceTextModule")
	}
	if synthetic, err := module.IsSyntheticModule(); err != nil || !synthetic {
		t.Fatalf("IsSyntheticModule = %v, %v", synthetic, err)
	}
	if status, _ := module.Status(); status != gov8.ModuleUninstantiated {
		t.Fatalf("initial status = %v", status)
	}
	if _, err := module.ScriptID(); err == nil {
		t.Fatal("synthetic module exposed a script ID")
	}
	linked, err := module.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
		t.Fatal("synthetic module resolver was called")
		return nil, nil
	}, nil)
	if err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
	if graphAsync, err := module.IsGraphAsync(); err != nil || graphAsync {
		t.Fatalf("IsGraphAsync = %v, %v", graphAsync, err)
	}
	if _, err := module.Evaluate(scope, nil); err == nil || !strings.Contains(err.Error(), "EvaluateValue") {
		t.Fatalf("synthetic Evaluate error = %v", err)
	}
	completion, err := module.EvaluateValue(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, ok, err := completion.IntegerValue(ctx); err != nil || !ok || result != 77 {
		t.Fatalf("evaluation result = %d, %v, %v", result, ok, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("callback calls = %d", calls.Load())
	}
	second, err := module.EvaluateValue(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if isPromise, err := second.IsPromise(); err != nil || !isPromise || calls.Load() != 1 {
		t.Fatalf("second evaluation = promise %v, calls %d, err %v", isPromise, calls.Load(), err)
	}
	secondPromise := gov8.Promise{Value: second}
	if state, err := secondPromise.State(); err != nil || state != gov8.PromiseFulfilled {
		t.Fatalf("second evaluation promise state = %v, %v", state, err)
	}
	secondResult, err := secondPromise.Result(scope)
	if err != nil {
		t.Fatal(err)
	}
	if undefined, err := secondResult.IsUndefined(); err != nil || !undefined {
		t.Fatalf("second evaluation promise result = undefined %v, %v", undefined, err)
	}
	if err := captured.SetExport("a", gov8.Value{}); err == nil || !strings.Contains(err.Error(), "no longer active") {
		t.Fatalf("escaped callback state error = %v", err)
	}
	namespaceValue, err := module.Namespace(scope)
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := gov8.AsObject(namespaceValue)
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) int64 {
		value, ok, err := namespace.GetByName(scope, ctx, name)
		if err != nil || !ok {
			t.Fatalf("namespace.%s = %v, %v", name, ok, err)
		}
		integer, ok, err := value.IntegerValue(ctx)
		if err != nil || !ok {
			t.Fatalf("namespace.%s conversion = %v, %v", name, ok, err)
		}
		return integer
	}
	if read("a") != 1 || read("b") != 2 {
		t.Fatal("initial exports differ")
	}
	updated, err := module.SetSyntheticModuleExport(scope, "a", func() gov8.Value {
		v, valueErr := scope.Int32(9)
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		return v
	}(), nil)
	if err != nil || !updated || read("a") != 9 {
		t.Fatalf("export update = %v, %v, value %d", updated, err, read("a"))
	}
	_ = iso
}

func TestSyntheticModuleInvalidExportAndCallbackError(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	module, err := ctx.NewSyntheticModule(scope, "invalid-export", []string{"known"},
		func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
			return e.Scope().Scope().Undefined()
		})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close()
	if linked, err := module.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
	tc, _ := iso.NewTryCatch()
	defer tc.Close()
	value, _ := scope.Undefined()
	if updated, err := module.SetSyntheticModuleExport(scope, "missing", value, tc); err == nil || updated {
		t.Fatalf("invalid update = %v, %v", updated, err)
	}
	if caught, _ := tc.HasCaught(); !caught {
		t.Fatal("invalid export did not throw")
	}
	text, _ := tc.ExceptionText(scope, ctx)
	if !strings.Contains(text, "missing") {
		t.Fatalf("invalid export exception = %q", text)
	}

	errorModule, err := ctx.NewSyntheticModule(scope, "callback-error", nil,
		func(*gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
			return gov8.Value{}, errors.New("synthetic-evaluation-error")
		})
	if err != nil {
		t.Fatal(err)
	}
	defer errorModule.Close()
	if linked, err := errorModule.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err != nil || !linked {
		t.Fatalf("error Instantiate = %v, %v", linked, err)
	}
	errorTC, _ := iso.NewTryCatch()
	defer errorTC.Close()
	if _, err := errorModule.EvaluateValue(scope, errorTC); err == nil {
		t.Fatal("callback error did not fail evaluation")
	}
	if status, _ := errorModule.Status(); status != gov8.ModuleErrored {
		t.Fatalf("error status = %v", status)
	}
}

func TestSyntheticModuleBoundariesAndAffinity(t *testing.T) {
	isoA, isoB, ctxA, ctxB, scopeA, scopeB := twoIsolates(t)
	_ = isoB
	if _, err := ctxA.NewSyntheticModule(scopeA, "duplicate", []string{"x", "x"},
		func(*gov8.SyntheticModuleEvaluation) (gov8.Value, error) { return gov8.Value{}, nil }); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate exports error = %v", err)
	}
	if _, err := ctxA.NewSyntheticModule(scopeA, "invalid-utf8", []string{"\xff"},
		func(*gov8.SyntheticModuleEvaluation) (gov8.Value, error) { return gov8.Value{}, nil }); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8 export error = %v", err)
	}
	if _, err := ctxA.NewSyntheticModule(scopeA, "nil", nil, nil); err == nil || !strings.Contains(err.Error(), "callback") {
		t.Fatalf("nil callback error = %v", err)
	}
	if _, err := ctxA.NewSyntheticModule(scopeB, "foreign", nil,
		func(*gov8.SyntheticModuleEvaluation) (gov8.Value, error) { return gov8.Value{}, nil }); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope error = %v", err)
	}
	module, err := ctxA.NewSyntheticModule(scopeA, "affinity", []string{"x"},
		func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) { return e.Scope().Scope().Undefined() })
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close()
	foreignValue, _ := scopeB.Int32(1)
	if _, err := module.SetSyntheticModuleExport(scopeA, "x", foreignValue, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign value error = %v", err)
	}
	localValue, _ := scopeA.Int32(1)
	if _, err := module.SetSyntheticModuleExport(scopeB, "x", localValue, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope update error = %v", err)
	}
	foreignResultModule, err := ctxA.NewSyntheticModule(scopeA, "foreign-result", nil,
		func(*gov8.SyntheticModuleEvaluation) (gov8.Value, error) { return foreignValue, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer foreignResultModule.Close()
	if linked, err := foreignResultModule.Instantiate(scopeA, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err != nil || !linked {
		t.Fatalf("foreign result Instantiate = %v, %v", linked, err)
	}
	if _, err := foreignResultModule.EvaluateValue(scopeA, nil); err == nil {
		t.Fatal("foreign callback result was accepted")
	}
	zeroResultModule, err := ctxA.NewSyntheticModule(scopeA, "zero-result", nil,
		func(*gov8.SyntheticModuleEvaluation) (gov8.Value, error) { return gov8.Value{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer zeroResultModule.Close()
	if linked, err := zeroResultModule.Instantiate(scopeA, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err != nil || !linked {
		t.Fatalf("zero result Instantiate = %v, %v", linked, err)
	}
	zeroResult, err := zeroResultModule.EvaluateValue(scopeA, nil)
	if err != nil {
		t.Fatal(err)
	}
	if undefined, err := zeroResult.IsUndefined(); err != nil || !undefined {
		t.Fatalf("zero result normalization = undefined %v, %v", undefined, err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := module.IsSyntheticModule()
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	_ = isoA
	_ = ctxB
}

func TestSyntheticModuleCallbackPanicAbortsProcess(t *testing.T) {
	if os.Getenv("GOV8_SYNTHETIC_PANIC_PROBE") == "1" {
		iso := newIso(t)
		ctx := newCtx(t, iso)
		scope := newScope(t, iso)
		module, err := ctx.NewSyntheticModule(scope, "panic", nil,
			func(*gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
				fmt.Fprintln(os.Stderr, "marker:synthetic-entered")
				panic("synthetic-callback-panic")
			})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = module.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil)
		fmt.Fprintln(os.Stderr, "marker:synthetic-before")
		_, _ = module.EvaluateValue(scope, nil)
		fmt.Fprintln(os.Stderr, "marker:synthetic-after")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestSyntheticModuleCallbackPanicAbortsProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOV8_SYNTHETIC_PANIC_PROBE=1")
	out, err := cmd.CombinedOutput()
	text := string(out)
	for _, marker := range []string{"marker:synthetic-before", "marker:synthetic-entered", "synthetic-callback-panic"} {
		if !strings.Contains(text, marker) {
			t.Errorf("missing %q; output:\n%s", marker, text)
		}
	}
	if strings.Contains(text, "marker:synthetic-after") {
		t.Errorf("panic returned; output:\n%s", text)
	}
	exitErr, ok := err.(*exec.ExitError)
	if err == nil || !ok || exitErr.ExitCode() != 3221226505 {
		t.Fatalf("exit = %v; want 0xC0000409; output:\n%s", err, text)
	}
}

func TestSyntheticModuleRejectsReentrantClose(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	var closeErr error
	module, err := ctx.NewSyntheticModule(scope, "reentrant-close", nil,
		func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
			closeErr = e.Module().Close()
			return e.Scope().Scope().Undefined()
		})
	if err != nil {
		t.Fatal(err)
	}
	if linked, err := module.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
	if _, err := module.EvaluateValue(scope, nil); err != nil {
		t.Fatal(err)
	}
	if closeErr == nil || !strings.Contains(closeErr.Error(), "active evaluation callback") {
		t.Fatalf("reentrant Close error = %v", closeErr)
	}
	if status, err := module.Status(); err != nil || status != gov8.ModuleEvaluated {
		t.Fatalf("module after rejected Close = %v, %v", status, err)
	}
	if err := module.Close(); err != nil {
		t.Fatalf("Close after callback = %v", err)
	}
}

func TestSyntheticModuleNestedEvaluationScopes(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var innerCalls, outerCalls atomic.Int32
	inner, err := ctx.NewSyntheticModule(scope, "nested-inner", nil,
		func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
			innerCalls.Add(1)
			return syntheticInt(e, 41), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	if linked, err := inner.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err != nil || !linked {
		t.Fatalf("inner Instantiate = %v, %v", linked, err)
	}

	outer, err := ctx.NewSyntheticModule(scope, "nested-outer", nil,
		func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
			outerCalls.Add(1)
			nestedScope, err := iso.NewScope()
			if err != nil {
				return gov8.Value{}, err
			}
			innerResult, err := inner.EvaluateValue(nestedScope, nil)
			if err != nil {
				_ = nestedScope.Close()
				return gov8.Value{}, err
			}
			integer, ok, err := innerResult.IntegerValue(ctx)
			if err != nil || !ok || integer != 41 {
				_ = nestedScope.Close()
				return gov8.Value{}, fmt.Errorf("nested result = %d, %v, %v", integer, ok, err)
			}
			if err := nestedScope.Close(); err != nil {
				return gov8.Value{}, err
			}
			// This value belongs to the restored outer callback scope, not the
			// nested scope that has just been closed.
			return syntheticInt(e, 42), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Close()
	if linked, err := outer.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err != nil || !linked {
		t.Fatalf("outer Instantiate = %v, %v", linked, err)
	}
	result, err := outer.EvaluateValue(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	integer, ok, err := result.IntegerValue(ctx)
	if err != nil || !ok || integer != 42 {
		t.Fatalf("outer result = %d, %v, %v", integer, ok, err)
	}
	if innerCalls.Load() != 1 || outerCalls.Load() != 1 {
		t.Fatalf("callback calls = inner %d, outer %d", innerCalls.Load(), outerCalls.Load())
	}
}

func TestSyntheticModuleExplicitCleanupOrder(t *testing.T) {
	iso, err := gov8.NewIsolate()
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
	module, err := ctx.NewSyntheticModule(scope, "cleanup", nil,
		func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) { return e.Scope().Scope().Undefined() })
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Close(); err != nil {
		t.Fatalf("module.Close before isolate.Close = %v", err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatalf("isolate.Close after module cleanup = %v", err)
	}
}

func TestSyntheticModuleManyExportsAndBindingIdentity(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	exports := []string{"e0", "e1", "e2", "e3", "e4", "e5", "e6", "e7", "e8", "nul\x00key"}
	modules := make([]*gov8.Module, 32)
	for index := range modules {
		index := index
		module, err := ctx.NewSyntheticModule(scope, fmt.Sprintf("many-%d\x00suffix", index), exports,
			func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
				value := syntheticInt(e, int32(index))
				if err := e.SetExport("e8", value); err != nil {
					return gov8.Value{}, err
				}
				return value, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		modules[index] = module
	}
	for index := len(modules) - 1; index >= 0; index-- {
		module := modules[index]
		if linked, err := module.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil); err != nil || !linked {
			t.Fatalf("module %d Instantiate = %v, %v", index, linked, err)
		}
		result, err := module.EvaluateValue(scope, nil)
		if err != nil {
			t.Fatalf("module %d EvaluateValue: %v", index, err)
		}
		integer, ok, err := result.IntegerValue(ctx)
		if err != nil || !ok || integer != int64(index) {
			t.Fatalf("module %d result = %d, %v, %v", index, integer, ok, err)
		}
		if err := module.Close(); err != nil {
			t.Fatalf("module %d Close: %v", index, err)
		}
	}
}
