//go:build windows && amd64

package modulesconformance

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	gov8 "gov8"
)

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type runtime struct {
	iso     *gov8.Isolate
	ctx     *gov8.Context
	scope   *gov8.Scope
	modules []*gov8.Module
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

func (r *runtime) compile(t *testing.T, name, source string) *gov8.Module {
	t.Helper()
	m, err := r.ctx.CompileModule(r.scope, source, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.modules = append(r.modules, m)
	return m
}

func (r *runtime) close(t *testing.T) {
	t.Helper()
	for i := len(r.modules) - 1; i >= 0; i-- {
		if err := r.modules[i].Close(); err != nil {
			t.Error(err)
		}
	}
	for _, closer := range []interface{ Close() error }{r.scope, r.ctx, r.iso} {
		if err := closer.Close(); err != nil {
			t.Error(err)
		}
	}
}

type fixtureLine struct {
	Check string         `json:"check"`
	OK    bool           `json:"ok"`
	Value map[string]any `json:"value"`
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-modules-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checked-in Rust module oracle fixture is missing: %s", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]fixtureLine{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue // summary line has no check/value fields
		}
		if line.Check != "" {
			out[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func normalized(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func compare(t *testing.T, all map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := all[id]
	if !ok {
		t.Fatalf("fixture lacks %s", id)
	}
	gotJSON, _ := json.Marshal(normalized(got))
	wantJSON, _ := json.Marshal(want.Value)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

func phaseName(phase gov8.ModuleImportPhase) string {
	switch phase {
	case gov8.ModuleImportSource:
		return "Source"
	case gov8.ModuleImportDefer:
		return "Defer"
	case gov8.ModuleImportEvaluation:
		return "Evaluation"
	default:
		return fmt.Sprintf("Unknown(%d)", phase)
	}
}

func TestModuleConformanceFixture(t *testing.T) {
	all := fixture(t)
	t.Run("compile_requests", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		m := r.compile(t, "requests.mjs", "import './side.mjs';\nexport { value } from './dep.mjs' with { kind: 'fixture' };")
		status, _ := m.Status()
		sourceText, _ := m.IsSourceTextModule()
		synthetic, _ := m.IsSyntheticModule()
		id, _ := m.ScriptID()
		requests, err := m.Requests()
		if err != nil {
			t.Fatal(err)
		}
		normalizedRequests := make([]any, len(requests))
		for i, request := range requests {
			loc, err := m.SourceOffsetToLocation(request.SourceOffset)
			if err != nil {
				t.Fatal(err)
			}
			attributes := make([]any, 0, len(request.Attributes)*3)
			for _, attr := range request.Attributes {
				attributes = append(attributes, attr.Key, attr.Value, strconv.Itoa(int(attr.SourceOffset)))
			}
			normalizedRequests[i] = map[string]any{
				"specifier": request.Specifier, "phase": phaseName(request.Phase),
				"line": loc.Line, "column": loc.Column, "attributes": attributes,
			}
		}
		compare(t, all, "modules/compile_requests", map[string]any{
			"status": status.String(), "source_text": sourceText, "synthetic": synthetic,
			"script_id_positive": id > 0, "request_count": len(requests), "requests": normalizedRequests,
		})
	})

	t.Run("link_evaluate_namespace", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		m := r.compile(t, "entry.mjs", "import { base } from 'export const base = 40;';export let answer = base + 2; answer += 1;")
		before, _ := m.Status()
		linked, err := m.Instantiate(r.scope, func(request gov8.ModuleResolveRequest) (*gov8.Module, error) {
			return r.compile(t, "dependency.mjs", request.Specifier), nil
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		afterLink, _ := m.Status()
		nsBefore, _ := m.Namespace(r.scope)
		promise, err := m.Evaluate(r.scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.iso.PerformMicrotaskCheckpoint()
		nsAfter, _ := m.Namespace(r.scope)
		stable, _ := nsBefore.StrictEquals(nsAfter)
		state, _ := promise.State()
		afterEval, _ := m.Status()
		ns, _ := gov8.AsObject(nsAfter)
		answerValue, _, _ := ns.GetByName(r.scope, r.ctx, "answer")
		answer, _, _ := answerValue.IntegerValue(r.ctx)
		compare(t, all, "modules/link_evaluate_namespace", map[string]any{
			"before": before.String(), "instantiate_result": linked, "after_link": afterLink.String(),
			"namespace_stable": stable, "evaluate_returns_promise": true, "promise_state": state.String(),
			"after_evaluate": afterEval.String(), "answer": answer,
		})
	})

	t.Run("syntax_failure", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		tc, _ := r.iso.NewTryCatch()
		defer tc.Close()
		m, err := r.ctx.CompileModuleWithOptions(r.scope, "export const = 1;", gov8.ModuleCompileOptions{
			ResourceName: "syntax.mjs", LineOffset: 3, ColumnOffset: 5,
		}, tc)
		caught, _ := tc.HasCaught()
		exception, _ := tc.ExceptionText(r.scope, r.ctx)
		message, _ := tc.MessageText(r.scope, r.ctx)
		line, _, _ := tc.LineNumber(r.scope, r.ctx)
		column, _ := tc.StartColumn(r.scope)
		resource := ""
		if caughtMessage, present, messageErr := tc.Message(r.scope); messageErr != nil {
			t.Fatal(messageErr)
		} else if present {
			resource, _ = caughtMessage.ResourceName(r.ctx)
		}
		compare(t, all, "modules/syntax_failure", map[string]any{
			"compiled": m != nil && err == nil, "caught": caught, "exception": exception,
			"message": message, "line": line, "start_column": column, "resource": resource,
		})
	})

	t.Run("negative_origin_offsets", func(t *testing.T) {
		observe := func(lineOffset, columnOffset int32) map[string]any {
			r := newRuntime(t)
			tc, err := r.iso.NewTryCatch()
			if err != nil {
				t.Fatal(err)
			}
			m, compileErr := r.ctx.CompileModuleWithOptions(r.scope, "export const = 1;", gov8.ModuleCompileOptions{
				ResourceName: "negative-offset.mjs", LineOffset: lineOffset, ColumnOffset: columnOffset,
			}, tc)
			caught, _ := tc.HasCaught()
			exception, _ := tc.ExceptionText(r.scope, r.ctx)
			message, _ := tc.MessageText(r.scope, r.ctx)
			line, _, _ := tc.LineNumber(r.scope, r.ctx)
			column, _ := tc.StartColumn(r.scope)
			position, _ := tc.StartPosition(r.scope)
			resource := ""
			if caughtMessage, present, messageErr := tc.Message(r.scope); messageErr != nil {
				t.Fatal(messageErr)
			} else if present {
				resource, _ = caughtMessage.ResourceName(r.ctx)
			}
			_ = tc.Close()
			r.close(t)
			return map[string]any{
				"compiled": m != nil && compileErr == nil, "caught": caught,
				"exception": exception, "message": message, "line": line,
				"start_column": column, "start_position": position, "resource": resource,
			}
		}
		compare(t, all, "modules/negative_origin_offsets", map[string]any{
			"line_minus_one": observe(-1, 0), "column_minus_one": observe(0, -1),
		})
	})

	t.Run("repeated_evaluate", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		m := r.compile(t, "repeat.mjs",
			"globalThis.__module_runs = (globalThis.__module_runs || 0) + 1;export const answer = 43;")
		linked, err := m.Instantiate(r.scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
			return nil, errors.New("unexpected resolver call")
		}, nil)
		if err != nil || !linked {
			t.Fatalf("Instantiate = %v, %v", linked, err)
		}
		first, err := m.Evaluate(r.scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.iso.PerformMicrotaskCheckpoint()
		firstState, _ := first.State()
		namespaceBefore, _ := m.Namespace(r.scope)
		namespaceObject, _ := gov8.AsObject(namespaceBefore)
		answerValue, _, _ := namespaceObject.GetByName(r.scope, r.ctx, "answer")
		answerBefore, _, _ := answerValue.IntegerValue(r.ctx)
		tc, _ := r.iso.NewTryCatch()
		defer tc.Close()
		second, secondErr := m.Evaluate(r.scope, tc)
		secondState := "None"
		secondIsPromise := false
		samePromise := false
		if secondErr == nil {
			secondIsPromise, _ = second.Value.IsPromise()
			state, _ := second.State()
			secondState = state.String()
			samePromise, _ = first.Value.StrictEquals(second.Value)
		}
		caught, _ := tc.HasCaught()
		_ = r.iso.PerformMicrotaskCheckpoint()
		namespaceAfter, _ := m.Namespace(r.scope)
		namespaceStable, _ := namespaceBefore.StrictEquals(namespaceAfter)
		namespaceAfterObject, _ := gov8.AsObject(namespaceAfter)
		answerValue, _, _ = namespaceAfterObject.GetByName(r.scope, r.ctx, "answer")
		answerAfter, _, _ := answerValue.IntegerValue(r.ctx)
		global, _ := r.ctx.GlobalObject(r.scope)
		runsValue, _, _ := global.GetByName(r.scope, r.ctx, "__module_runs")
		runs, _, _ := runsValue.IntegerValue(r.ctx)
		status, _ := m.Status()
		var caughtException any
		if caught {
			caughtException, _ = tc.ExceptionText(r.scope, r.ctx)
		}
		compare(t, all, "modules/repeated_evaluate", map[string]any{
			"second_is_some": secondErr == nil, "second_is_promise": secondIsPromise,
			"status": status.String(), "first_promise_state": firstState.String(),
			"second_promise_state": secondState, "same_promise": samePromise,
			"namespace_stable": namespaceStable, "answer_before": answerBefore,
			"answer_after": answerAfter, "evaluation_count": runs, "caught": caught,
			"exception": caughtException,
		})
	})

	t.Run("link_failure", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		m := r.compile(t, "link.mjs", "import './missing.mjs';")
		tc, _ := r.iso.NewTryCatch()
		defer tc.Close()
		linked, err := m.Instantiate(r.scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
			return nil, errors.New("link boom")
		}, tc)
		caught, _ := tc.HasCaught()
		exception, _ := tc.ExceptionText(r.scope, r.ctx)
		status, _ := m.Status()
		compare(t, all, "modules/link_failure", map[string]any{
			"result_is_none": !linked && err != nil, "caught": caught, "exception": exception, "status": status.String(),
		})
	})

	t.Run("evaluation_rejection", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		if err := r.iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
			t.Fatal(err)
		}
		m := r.compile(t, "reject.mjs", "await Promise.reject(new RangeError('module rejected'));")
		linked, err := m.Instantiate(r.scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
			return nil, errors.New("unexpected resolver call")
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		hasTLA, _ := m.HasTopLevelAwait()
		graphAsync, _ := m.IsGraphAsync()
		promise, err := m.Evaluate(r.scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := promise.State()
		_ = r.iso.PerformMicrotaskCheckpoint()
		after, _ := promise.State()
		reason, _ := promise.Result(r.scope)
		exception, _ := reason.ToString(r.ctx)
		status, _ := m.Status()
		stored, _ := m.Exception(r.scope)
		same, _ := reason.StrictEquals(stored)
		compare(t, all, "modules/evaluation_rejection", map[string]any{
			"linked": linked, "has_top_level_await": hasTLA, "graph_async": graphAsync,
			"state_before_checkpoint": before.String(), "state_after_checkpoint": after.String(),
			"status": status.String(), "exception": exception, "stored_exception_same": same,
		})
	})
}
