//go:build windows && amd64

package gov8_test

import (
	"errors"
	"strings"
	"testing"

	gov8 "gov8"
)

func closeModuleRuntime(t *testing.T, modules []*gov8.Module, scope *gov8.Scope, ctx *gov8.Context, iso *gov8.Isolate) {
	t.Helper()
	for i := len(modules) - 1; i >= 0; i-- {
		if modules[i] != nil {
			if err := modules[i].Close(); err != nil {
				t.Errorf("Module.Close: %v", err)
			}
		}
	}
	for _, closer := range []interface{ Close() error }{scope, ctx, iso} {
		if err := closer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

func TestModuleCompileRequestsAndLocations(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	m, err := ctx.CompileModule(scope, "import './side.mjs';\nexport { value } from './dep.mjs' with { kind: 'fixture' };", "requests.mjs", nil)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	defer closeModuleRuntime(t, []*gov8.Module{m}, scope, ctx, iso)

	status, err := m.Status()
	if err != nil || status != gov8.ModuleUninstantiated {
		t.Fatalf("Status = %v, %v", status, err)
	}
	sourceText, err := m.IsSourceTextModule()
	if err != nil || !sourceText {
		t.Fatalf("IsSourceTextModule = %v, %v", sourceText, err)
	}
	synthetic, err := m.IsSyntheticModule()
	if err != nil || synthetic {
		t.Fatalf("IsSyntheticModule = %v, %v", synthetic, err)
	}
	if id, err := m.ScriptID(); err != nil || id <= 0 {
		t.Fatalf("ScriptID = %d, %v", id, err)
	}
	requests, err := m.Requests()
	if err != nil {
		t.Fatalf("Requests: %v", err)
	}
	if len(requests) != 2 || requests[0].Specifier != "./side.mjs" || requests[1].Specifier != "./dep.mjs" {
		t.Fatalf("requests = %#v", requests)
	}
	if requests[1].Phase != gov8.ModuleImportEvaluation || len(requests[1].Attributes) != 1 {
		t.Fatalf("second request = %#v", requests[1])
	}
	attr := requests[1].Attributes[0]
	if attr.Key != "kind" || attr.Value != "fixture" || attr.SourceOffset != 62 {
		t.Fatalf("attribute = %#v", attr)
	}
	first, err := m.SourceOffsetToLocation(requests[0].SourceOffset)
	if err != nil || first != (gov8.ModuleLocation{Line: 0, Column: 7}) {
		t.Fatalf("first location = %#v, %v", first, err)
	}
	second, err := m.SourceOffsetToLocation(requests[1].SourceOffset)
	if err != nil || second != (gov8.ModuleLocation{Line: 1, Column: 22}) {
		t.Fatalf("second location = %#v, %v", second, err)
	}
}

func TestModuleResolverReceivesImportAttributes(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	entry, err := ctx.CompileModule(scope,
		"import './dep.mjs' with { kind: 'fixture' }; export const ok = true;",
		"attributes.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := ctx.CompileModule(scope, "export {};", "dep.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeModuleRuntime(t, []*gov8.Module{entry, dep}, scope, ctx, iso)
	linked, err := entry.Instantiate(scope, func(request gov8.ModuleResolveRequest) (*gov8.Module, error) {
		if request.Specifier != "./dep.mjs" || len(request.Attributes) != 1 {
			t.Fatalf("resolver request = %#v", request)
		}
		attribute := request.Attributes[0]
		if attribute.Key != "kind" || attribute.Value != "fixture" || attribute.SourceOffset <= 0 {
			t.Fatalf("resolver attribute = %#v", attribute)
		}
		return dep, nil
	}, nil)
	if err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
}

func TestModuleLinkEvaluateNamespace(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	entry, err := ctx.CompileModule(scope,
		"import { base } from 'export const base = 40;';export let answer = base + 2; answer += 1;",
		"entry.mjs", nil)
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	modules := []*gov8.Module{entry}
	defer closeModuleRuntime(t, modules, scope, ctx, iso)

	linked, err := entry.Instantiate(scope, func(request gov8.ModuleResolveRequest) (*gov8.Module, error) {
		if request.Referrer != entry {
			t.Errorf("unexpected referrer: %p", request.Referrer)
		}
		dep, compileErr := ctx.CompileModule(scope, request.Specifier, "dependency.mjs", nil)
		if compileErr == nil {
			modules = append(modules, dep)
		}
		return dep, compileErr
	}, nil)
	if err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
	if linkedStatus, statusErr := entry.Status(); statusErr != nil || linkedStatus != gov8.ModuleInstantiated {
		t.Fatalf("linked status = %v, %v", linkedStatus, statusErr)
	}
	before, err := entry.Namespace(scope)
	if err != nil {
		t.Fatalf("Namespace before: %v", err)
	}
	promise, err := entry.Evaluate(scope, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state, err := promise.State(); err != nil || state != gov8.PromiseFulfilled {
		t.Fatalf("promise state = %v, %v", state, err)
	}
	after, err := entry.Namespace(scope)
	if err != nil {
		t.Fatalf("Namespace after: %v", err)
	}
	if same, err := before.StrictEquals(after); err != nil || !same {
		t.Fatalf("namespace identity = %v, %v", same, err)
	}
	namespace, err := gov8.AsObject(after)
	if err != nil {
		t.Fatalf("AsObject(namespace): %v", err)
	}
	answer, ok, err := namespace.GetByName(scope, ctx, "answer")
	if err != nil || !ok {
		t.Fatalf("namespace.answer: ok=%v err=%v", ok, err)
	}
	n, ok, err := answer.IntegerValue(ctx)
	if err != nil || !ok || n != 43 {
		t.Fatalf("answer = %d, %v, %v", n, ok, err)
	}
	status, _ := entry.Status()
	if status != gov8.ModuleEvaluated {
		t.Fatalf("status = %s", status)
	}
}

func TestModuleRepeatedEvaluateReturnsSamePromiseAndRunsOnce(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	m, err := ctx.CompileModule(scope,
		"globalThis.__module_runs = (globalThis.__module_runs || 0) + 1;export const answer = 43;",
		"repeat.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeModuleRuntime(t, []*gov8.Module{m}, scope, ctx, iso)
	linked, err := m.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
		t.Fatal("resolver called for request-free module")
		return nil, nil
	}, nil)
	if err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
	first, err := m.Evaluate(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatal(err)
	}
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	second, err := m.Evaluate(scope, tc)
	if err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}
	same, err := first.Value.StrictEquals(second.Value)
	if err != nil || !same {
		t.Fatalf("same promise = %v, %v", same, err)
	}
	firstState, _ := first.State()
	secondState, _ := second.State()
	status, _ := m.Status()
	caught, _ := tc.HasCaught()
	global, _ := ctx.GlobalObject(scope)
	runsValue, ok, err := global.GetByName(scope, ctx, "__module_runs")
	if err != nil || !ok {
		t.Fatalf("__module_runs: %v, %v", ok, err)
	}
	runs, ok, err := runsValue.IntegerValue(ctx)
	if err != nil || !ok || runs != 1 || caught || status != gov8.ModuleEvaluated ||
		firstState != gov8.PromiseFulfilled || secondState != gov8.PromiseFulfilled {
		t.Fatalf("repeat: runs=%d/%v err=%v caught=%v status=%v states=%v/%v",
			runs, ok, err, caught, status, firstState, secondState)
	}
}

func TestModuleNegativeOriginOffsetsAreAccepted(t *testing.T) {
	for _, test := range []struct {
		name              string
		line, column      int32
		wantLine          int32
		wantColumn        int64
		wantStartPosition int64
	}{
		{name: "line", line: -1, wantLine: 0, wantColumn: 13, wantStartPosition: 13},
		{name: "column", column: -1, wantLine: 1, wantColumn: 12, wantStartPosition: 13},
	} {
		t.Run(test.name, func(t *testing.T) {
			iso := newIso(t)
			ctx := newCtx(t, iso)
			scope := newScope(t, iso)
			tc, _ := iso.NewTryCatch()
			defer func() { _ = tc.Close(); closeModuleRuntime(t, nil, scope, ctx, iso) }()
			m, err := ctx.CompileModuleWithOptions(scope, "export const = 1;", gov8.ModuleCompileOptions{
				ResourceName: "negative-offset.mjs", LineOffset: test.line, ColumnOffset: test.column,
			}, tc)
			if m != nil || !gov8.IsException(err) {
				t.Fatalf("CompileModuleWithOptions = %v, %v", m, err)
			}
			line, ok, _ := tc.LineNumber(scope, ctx)
			column, _ := tc.StartColumn(scope)
			position, _ := tc.StartPosition(scope)
			if !ok || line != test.wantLine || column != test.wantColumn || position != test.wantStartPosition {
				t.Fatalf("diagnostic = line %d/%v column %d position %d", line, ok, column, position)
			}
		})
	}
}

func TestModuleSyntaxLinkAndEvaluationFailures(t *testing.T) {
	t.Run("syntax", func(t *testing.T) {
		iso := newIso(t)
		ctx := newCtx(t, iso)
		scope := newScope(t, iso)
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tc.Close(); closeModuleRuntime(t, nil, scope, ctx, iso) }()
		m, err := ctx.CompileModuleWithOptions(scope, "export const = 1;", gov8.ModuleCompileOptions{
			ResourceName: "syntax.mjs", LineOffset: 3, ColumnOffset: 5,
		}, tc)
		if m != nil || !gov8.IsException(err) {
			t.Fatalf("compile = %v, %v", m, err)
		}
		caught, _ := tc.HasCaught()
		exception, _ := tc.ExceptionText(scope, ctx)
		line, ok, _ := tc.LineNumber(scope, ctx)
		column, _ := tc.StartColumn(scope)
		if !caught || exception != "SyntaxError: Unexpected token '='" || !ok || line != 4 || column != 18 {
			t.Fatalf("syntax details: caught=%v exception=%q line=%d/%v column=%d", caught, exception, line, ok, column)
		}
	})

	t.Run("link", func(t *testing.T) {
		iso := newIso(t)
		ctx := newCtx(t, iso)
		scope := newScope(t, iso)
		m, err := ctx.CompileModule(scope, "import './missing.mjs';", "link.mjs", nil)
		if err != nil {
			t.Fatal(err)
		}
		tc, _ := iso.NewTryCatch()
		defer func() { _ = tc.Close(); closeModuleRuntime(t, []*gov8.Module{m}, scope, ctx, iso) }()
		ok, err := m.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
			return nil, errors.New("link boom")
		}, tc)
		caught, _ := tc.HasCaught()
		exception, _ := tc.ExceptionText(scope, ctx)
		status, _ := m.Status()
		if ok || err == nil || !caught || exception != "link boom" || status != gov8.ModuleUninstantiated {
			t.Fatalf("link failure: ok=%v err=%v caught=%v exception=%q status=%s", ok, err, caught, exception, status)
		}
	})

	t.Run("top-level-await-rejection", func(t *testing.T) {
		iso := newIso(t)
		if err := iso.SetMicrotasksPolicy(gov8.PolicyExplicit); err != nil {
			t.Fatal(err)
		}
		ctx := newCtx(t, iso)
		scope := newScope(t, iso)
		m, err := ctx.CompileModule(scope, "await Promise.reject(new RangeError('module rejected'));", "reject.mjs", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer closeModuleRuntime(t, []*gov8.Module{m}, scope, ctx, iso)
		linked, err := m.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
			return nil, errors.New("unexpected resolver call")
		}, nil)
		if err != nil || !linked {
			t.Fatalf("Instantiate = %v, %v", linked, err)
		}
		hasTLA, _ := m.HasTopLevelAwait()
		graphAsync, _ := m.IsGraphAsync()
		promise, err := m.Evaluate(scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := promise.State()
		if err := iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		after, _ := promise.State()
		reason, err := promise.Result(scope)
		if err != nil {
			t.Fatal(err)
		}
		text, _ := reason.ToString(ctx)
		status, _ := m.Status()
		stored, err := m.Exception(scope)
		if err != nil {
			t.Fatal(err)
		}
		same, _ := reason.StrictEquals(stored)
		if !hasTLA || !graphAsync || before != gov8.PromisePending || after != gov8.PromiseRejected ||
			status != gov8.ModuleErrored || !strings.Contains(text, "RangeError: module rejected") || !same {
			t.Fatalf("rejection: tla=%v graph=%v before=%v after=%v status=%v text=%q same=%v",
				hasTLA, graphAsync, before, after, status, text, same)
		}
	})
}
