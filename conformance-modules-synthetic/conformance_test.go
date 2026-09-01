//go:build windows && amd64

package modulessyntheticconformance

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type fixtureLine struct {
	Check string         `json:"check"`
	OK    bool           `json:"ok"`
	Value map[string]any `json:"value"`
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-modules-synthetic-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checked-in Rust synthetic-module fixture is missing: %s", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result := map[string]fixtureLine{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err == nil && line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok || !want.OK {
		t.Fatalf("missing or failed fixture check %s", id)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want.Value)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t *testing.T) *runtime {
	t.Helper()
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
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtime) close() {
	_ = r.scope.Close()
	_ = r.ctx.Close()
	_ = r.iso.Close()
}

func callbackInt(e *gov8.SyntheticModuleEvaluation, value int32) gov8.Value {
	result, err := e.Scope().Scope().Int32(value)
	if err != nil {
		panic(err)
	}
	return result
}

func namespaceValue(t *testing.T, r *runtime, module *gov8.Module, name string) gov8.Value {
	t.Helper()
	namespace, err := module.Namespace(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	object, err := gov8.AsObject(namespace)
	if err != nil {
		t.Fatal(err)
	}
	value, ok, err := object.GetByName(r.scope, r.ctx, name)
	if err != nil || !ok {
		t.Fatalf("namespace.%s = %v, %v", name, ok, err)
	}
	return value
}

func integer(t *testing.T, ctx *gov8.Context, value gov8.Value) int64 {
	t.Helper()
	result, ok, err := value.IntegerValue(ctx)
	if err != nil || !ok {
		t.Fatalf("IntegerValue = %v, %v", ok, err)
	}
	return result
}

func instantiate(t *testing.T, r *runtime, module *gov8.Module, resolverCalls *atomic.Int32) bool {
	t.Helper()
	linked, err := module.Instantiate(r.scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
		resolverCalls.Add(1)
		return nil, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return linked
}

func TestRustOracleFixture(t *testing.T) {
	fixtures := fixture(t)
	t.Run("creation_and_pre_set_exports", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close()
		var evaluationCalls atomic.Int32
		module, err := r.ctx.NewSyntheticModule(r.scope, "synthetic-fixture", []string{"b", "a"},
			func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
				evaluationCalls.Add(1)
				return callbackInt(e, 77), nil
			})
		if err != nil {
			t.Fatal(err)
		}
		defer module.Close()
		before, _ := module.Status()
		sourceText, _ := module.IsSourceTextModule()
		synthetic, _ := module.IsSyntheticModule()
		_, scriptIDErr := module.ScriptID()
		identity, _ := module.IdentityHash()
		requests, err := module.Requests()
		if err != nil {
			t.Fatal(err)
		}
		preTC, _ := r.iso.NewTryCatch()
		preValue, _ := r.scope.Int32(10)
		preSet, preErr := module.SetSyntheticModuleExport(r.scope, "a", preValue, preTC)
		preCaught, _ := preTC.HasCaught()
		preException, _ := preTC.ExceptionText(r.scope, r.ctx)
		_ = preTC.Close()
		var resolverCalls atomic.Int32
		linked := instantiate(t, r, module, &resolverCalls)
		postValue, _ := r.scope.Int32(20)
		postSet, postErr := module.SetSyntheticModuleExport(r.scope, "b", postValue, nil)
		namespaceBefore, _ := module.Namespace(r.scope)
		first, firstErr := module.EvaluateValue(r.scope, nil)
		if firstErr != nil {
			t.Fatal(firstErr)
		}
		firstPromise, _ := first.IsPromise()
		firstResult := integer(t, r.ctx, first)
		second, secondErr := module.EvaluateValue(r.scope, nil)
		if secondErr != nil {
			t.Fatal(secondErr)
		}
		secondIsPromise, _ := second.IsPromise()
		_ = r.iso.PerformMicrotaskCheckpoint()
		secondPromise := gov8.Promise{Value: second}
		secondState, _ := secondPromise.State()
		secondResult, _ := secondPromise.Result(r.scope)
		secondUndefined, _ := secondResult.IsUndefined()
		sameEvaluation, _ := first.StrictEquals(second)
		namespaceAfter, _ := module.Namespace(r.scope)
		namespaceStable, _ := namespaceBefore.StrictEquals(namespaceAfter)
		aUndefined, _ := namespaceValue(t, r, module, "a").IsUndefined()
		status, _ := module.Status()
		compare(t, fixtures, "modules-synthetic/creation_and_pre_set_exports", map[string]any{
			"before": before.String(), "source_text": sourceText, "synthetic": synthetic,
			"script_id_none": scriptIDErr != nil, "identity_hash_nonzero": identity != 0,
			"requests": len(requests), "pre_set_is_none": preErr != nil && !preSet,
			"pre_set_caught": preCaught, "pre_set_exception": preException,
			"instantiate": linked, "post_link_set": postErr == nil && postSet,
			"resolver_calls": resolverCalls.Load(), "evaluation_calls": evaluationCalls.Load(),
			"status": status.String(), "evaluation_is_promise": firstPromise,
			"evaluation_result": firstResult, "second_evaluate_some": secondErr == nil,
			"second_is_promise": secondIsPromise, "second_promise_state": secondState.String(),
			"second_result_is_undefined": secondUndefined, "second_evaluation_same": sameEvaluation,
			"namespace_stable": namespaceStable, "a_is_undefined": aUndefined,
			"b": integer(t, r.ctx, namespaceValue(t, r, module, "b")),
		})
	})

	t.Run("evaluation_sets_and_invalid_export", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close()
		var invalidCaught atomic.Bool
		var invalidException atomic.Value
		invalidException.Store("")
		module, err := r.ctx.NewSyntheticModule(r.scope, "synthetic-fixture", []string{"a", "b"},
			func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
				for _, pair := range []struct {
					name  string
					value int32
				}{{"a", 1}, {"b", 2}} {
					if err := e.SetExport(pair.name, callbackInt(e, pair.value)); err != nil {
						return gov8.Value{}, err
					}
				}
				err := e.SetExport("missing", callbackInt(e, 0))
				invalidCaught.Store(err != nil)
				if err != nil {
					invalidException.Store(err.Error())
				}
				return callbackInt(e, 99), nil
			})
		if err != nil {
			t.Fatal(err)
		}
		defer module.Close()
		var resolverCalls atomic.Int32
		instantiate(t, r, module, &resolverCalls)
		result, err := module.EvaluateValue(r.scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		isPromise, _ := result.IsPromise()
		status, _ := module.Status()
		missingUndefined, _ := namespaceValue(t, r, module, "missing").IsUndefined()
		script, compileErr := r.ctx.Compile(r.scope, "6*7", nil)
		isolateRecovers := false
		if compileErr == nil {
			value, runErr := script.Run(r.scope, nil)
			if runErr == nil {
				isolateRecovers = integer(t, r.ctx, value) == 42
			}
			_ = script.Close()
		}
		exception, _ := invalidException.Load().(string)
		compare(t, fixtures, "modules-synthetic/evaluation_sets_and_invalid_export", map[string]any{
			"invalid_export_caught": invalidCaught.Load(), "invalid_export_exception": exception,
			"status": status.String(), "evaluation_is_promise": isPromise,
			"evaluation_result":    integer(t, r.ctx, result),
			"a":                    integer(t, r.ctx, namespaceValue(t, r, module, "a")),
			"b":                    integer(t, r.ctx, namespaceValue(t, r, module, "b")),
			"missing_is_undefined": missingUndefined, "isolate_recovers": isolateRecovers,
		})
	})

	t.Run("thrown_evaluation", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close()
		module, err := r.ctx.NewSyntheticModule(r.scope, "synthetic-fixture", nil,
			func(e *gov8.SyntheticModuleEvaluation) (gov8.Value, error) {
				exception, err := e.NewTypeError("synthetic boom")
				if err != nil {
					return gov8.Value{}, err
				}
				if err := e.Throw(exception); err != nil {
					return gov8.Value{}, err
				}
				return gov8.Value{}, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		defer module.Close()
		var resolverCalls atomic.Int32
		instantiate(t, r, module, &resolverCalls)
		tc, _ := r.iso.NewTryCatch()
		defer tc.Close()
		_, evalErr := module.EvaluateValue(r.scope, tc)
		caught, _ := tc.HasCaught()
		exceptionText, _ := tc.ExceptionText(r.scope, r.ctx)
		caughtException, _, _ := tc.Exception(r.scope)
		storedException, _ := module.Exception(r.scope)
		same, _ := caughtException.StrictEquals(storedException)
		status, _ := module.Status()
		compare(t, fixtures, "modules-synthetic/thrown_evaluation", map[string]any{
			"evaluate_some": evalErr == nil, "trycatch_caught": caught,
			"status": status.String(), "exception": exceptionText,
			"stored_exception_same": same,
		})
	})
}
