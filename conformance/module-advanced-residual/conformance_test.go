//go:build windows && amd64

package moduleadvancedresidualconformance

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestResolveSourcePanicBoundary(t *testing.T) {
	if os.Getenv("GOV8_MODULE_SOURCE_PANIC_CHILD") == "1" {
		r := newRuntime(t)
		m := r.compile(t, "panic-source.mjs", "import source x from 'bad';")
		_, _ = m.Instantiate2(r.scope, noModule, func(gov8.ModuleSourceResolveRequest) (gov8.Value, error) { panic("resolve source panic boundary") }, nil)
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestResolveSourcePanicBoundary$")
	cmd.Env = append(os.Environ(), "GOV8_MODULE_SOURCE_PANIC_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("panic child succeeded: %s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || uint32(exitErr.ExitCode()) != 0xC0000409 {
		t.Fatalf("panic child exit = %v, want 0xC0000409", err)
	}
	if !strings.Contains(string(out), "panic in module source resolver callback") || !strings.Contains(string(out), "resolve source panic boundary") {
		t.Fatalf("panic output: %s", out)
	}
}

func TestMain(m *testing.M) {
	if err := gov8.SetFlagsFromString("--js-source-phase-imports --harmony-shadow-realm"); err != nil {
		panic(err)
	}
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type fixtureLine struct {
	Check string         `json:"check"`
	Value map[string]any `json:"value"`
}

func fixture(t *testing.T) map[string]fixtureLine {
	f, err := os.Open(filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-module-advanced-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result := map[string]fixtureLine{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if json.Unmarshal(scanner.Bytes(), &line) == nil && line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, all map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := all[id]
	if !ok {
		t.Fatalf("fixture lacks %s", id)
	}
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want.Value)
	if string(gb) != string(wb) {
		t.Fatalf("%s mismatch\n got %s\nwant %s", id, gb, wb)
	}
}

type runtime struct {
	iso           *gov8.Isolate
	ctx           *gov8.Context
	scope         *gov8.Scope
	modules       []*gov8.Module
	scripts       []*gov8.Script
	trycatches    []*gov8.TryCatch
	extraContexts []*gov8.Context
}

func newRuntime(t *testing.T) *runtime {
	t.Helper()
	iso, e := gov8.NewIsolate()
	if e != nil {
		t.Fatal(e)
	}
	ctx, e := iso.NewContext()
	if e != nil {
		t.Fatal(e)
	}
	scope, e := iso.NewScope()
	if e != nil {
		t.Fatal(e)
	}
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}
func (r *runtime) compile(t *testing.T, name, text string) *gov8.Module {
	t.Helper()
	m, e := r.ctx.CompileModule(r.scope, text, name, nil)
	if e != nil {
		t.Fatal(e)
	}
	r.modules = append(r.modules, m)
	return m
}
func (r *runtime) eval(t *testing.T, text string, tc *gov8.TryCatch) (gov8.Value, error) {
	t.Helper()
	sc, e := r.ctx.Compile(r.scope, text, tc)
	if e != nil {
		return gov8.Value{}, e
	}
	r.scripts = append(r.scripts, sc)
	return sc.Run(r.scope, tc)
}
func (r *runtime) close(t *testing.T) {
	t.Helper()
	for i := len(r.trycatches) - 1; i >= 0; i-- {
		_ = r.trycatches[i].Close()
	}
	for i := len(r.scripts) - 1; i >= 0; i-- {
		_ = r.scripts[i].Close()
	}
	for i := len(r.modules) - 1; i >= 0; i-- {
		_ = r.modules[i].Close()
	}
	for _, c := range r.extraContexts {
		_ = c.Close()
	}
	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		t.Error(err)
	}
	_ = r.scope.Close()
	_ = r.ctx.Close()
	_ = r.iso.Close()
}
func noModule(gov8.ModuleResolveRequest) (*gov8.Module, error) {
	return nil, errors.New("unexpected evaluation-phase resolve")
}
func object(t *testing.T, v gov8.Value) *gov8.Object {
	t.Helper()
	o, e := gov8.AsObject(v)
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func integer(t *testing.T, v gov8.Value, c *gov8.Context) int64 {
	t.Helper()
	n, ok, e := v.IntegerValue(c)
	if e != nil || !ok {
		t.Fatalf("integer=%d/%v %v", n, ok, e)
	}
	return n
}

func TestAdvancedModuleResidualFixture(t *testing.T) {
	all := fixture(t)
	t.Run("instantiate2_source_phase", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		m := r.compile(t, "source-entry.mjs", "import source mod from 'source-dep'; export default mod;")
		reqs, _ := m.Requests()
		loc, _ := m.SourceOffsetToLocation(reqs[0].SourceOffset)
		wasm, e := r.eval(t, "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))", nil)
		if e != nil {
			t.Fatal(e)
		}
		calls := 0
		linked, e := m.Instantiate2(r.scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
			t.Fatal("module resolver called")
			return nil, nil
		}, func(req gov8.ModuleSourceResolveRequest) (gov8.Value, error) {
			calls++
			if req.Specifier != "source-dep" || req.Phase != gov8.ModuleImportSource ||
				req.SourceOffset != reqs[0].SourceOffset || req.Location != loc || len(req.Attributes) != 0 {
				t.Fatalf("request=%#v", req)
			}
			return wasm, nil
		}, nil)
		if e != nil {
			t.Fatal(e)
		}
		p, e := m.Evaluate(r.scope, nil)
		if e != nil {
			t.Fatal(e)
		}
		_ = r.iso.PerformMicrotaskCheckpoint()
		ns, _ := m.Namespace(r.scope)
		exported, _, _ := object(t, ns).GetByName(r.scope, r.ctx, "default")
		same, _ := exported.StrictEquals(wasm)
		state, _ := p.State()
		status, _ := m.Status()
		compare(t, all, "module-advanced-residual/instantiate2_source_phase", map[string]any{"request_phase": "Source", "request_line": loc.Line, "request_column": loc.Column, "linked": linked, "module_resolves": 0, "source_resolves": calls, "promise_state": state.String(), "status": status.String(), "export_same": same})
	})

	t.Run("instantiate2_source_exception", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		m := r.compile(t, "source-error.mjs", "import source x from 'bad';")
		tc, _ := r.iso.NewTryCatch()
		r.trycatches = append(r.trycatches, tc)
		calls := 0
		linked, e := m.Instantiate2(r.scope, noModule, func(req gov8.ModuleSourceResolveRequest) (gov8.Value, error) {
			calls++
			exc, x := req.Scope.NewTypeError("source link boom")
			if x != nil {
				return gov8.Value{}, x
			}
			if x = req.Scope.ThrowException(exc); x != nil {
				return gov8.Value{}, x
			}
			return gov8.Value{}, nil
		}, tc)
		caught, _ := tc.HasCaught()
		text, _ := tc.ExceptionText(r.scope, r.ctx)
		status, _ := m.Status()
		compare(t, all, "module-advanced-residual/instantiate2_source_exception", map[string]any{"linked_none": !linked && e != nil, "caught": caught, "exception": text, "status": status.String(), "source_resolves": calls})
	})

	t.Run("deferred_namespace", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		_, _ = r.eval(t, "globalThis.deferHits=0", nil)
		m := r.compile(t, "deferred.mjs", "globalThis.deferHits++; export const answer=42;")
		linked, e := m.Instantiate(r.scope, noModule, nil)
		if e != nil {
			t.Fatal(e)
		}
		before, _ := m.Status()
		g, ok, e := m.EvaluateForImportDefer(r.scope)
		if e != nil || !ok {
			t.Fatalf("gather %v %v", ok, e)
		}
		isPromise, _ := g.IsPromise()
		gp := gov8.Promise{Value: g}
		gb, _ := gp.State()
		_ = r.iso.PerformMicrotaskCheckpoint()
		ga, _ := gp.State()
		afterGather, _ := m.Status()
		hitsV, _ := r.eval(t, "deferHits", nil)
		hitsBefore := integer(t, hitsV, r.ctx)
		d, _ := m.NamespaceWithPhase(r.scope, gov8.ModuleImportDefer)
		d2, _ := m.NamespaceWithPhase(r.scope, gov8.ModuleImportDefer)
		src, _ := m.NamespaceWithPhase(r.scope, gov8.ModuleImportSource)
		stable, _ := d.StrictEquals(d2)
		srcObj, _ := src.IsObject()
		srcU, _ := src.IsUndefined()
		srcSame, _ := src.StrictEquals(d)
		hitsV, _ = r.eval(t, "deferHits", nil)
		hitsNS := integer(t, hitsV, r.ctx)
		answerV, _, _ := object(t, d).GetByName(r.scope, r.ctx, "answer")
		answer := integer(t, answerV, r.ctx)
		_ = r.iso.PerformMicrotaskCheckpoint()
		hitsV, _ = r.eval(t, "deferHits", nil)
		hitsAccess := integer(t, hitsV, r.ctx)
		afterAccess, _ := m.Status()
		evalNS, _ := m.NamespaceWithPhase(r.scope, gov8.ModuleImportEvaluation)
		evalSame, _ := d.StrictEquals(evalNS)
		compare(t, all, "module-advanced-residual/deferred_namespace", map[string]any{"linked": linked, "before_status": before.String(), "gathered_is_promise": isPromise, "gather_state_before": gb.String(), "gather_state_after": ga.String(), "after_gather_status": afterGather.String(), "hits_before_namespace": hitsBefore, "deferred_namespace_stable": stable, "source_phase_is_object": srcObj, "source_phase_is_undefined": srcU, "source_phase_same_deferred": srcSame, "hits_after_namespace": hitsNS, "answer": answer, "hits_after_access": hitsAccess, "after_access_status": afterAccess.String(), "evaluation_namespace_same": evalSame})
	})

	t.Run("stalled_top_level_await", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		source := "await new Promise((_resolve, _reject) => {});"
		m := r.compile(t, "stalled-fixture.mjs", source)
		linked, _ := m.Instantiate(r.scope, noModule, nil)
		before, _ := m.StalledTopLevelAwaitMessages(r.scope)
		p, _ := m.Evaluate(r.scope, nil)
		_ = r.iso.PerformMicrotaskCheckpoint()
		stalled, e := m.StalledTopLevelAwaitMessages(r.scope)
		if e != nil || len(stalled) != 1 {
			t.Fatalf("stalled=%d %v", len(stalled), e)
		}
		msg := stalled[0].Message
		text, _ := msg.Text(r.ctx)
		line, _, _ := msg.LineNumber(r.ctx)
		resource, _ := msg.ResourceName(r.ctx)
		sourceLine, _, _ := msg.SourceLine(r.ctx)
		sp, _ := msg.StartPosition()
		ep, _ := msg.EndPosition()
		sc, _ := msg.StartColumn()
		ec, _ := msg.EndColumn()
		wi, _ := msg.WasmFunctionIndex()
		el, _ := msg.ErrorLevel()
		shared, _ := msg.IsSharedCrossOrigin()
		opaque, _ := msg.IsOpaque()
		status, _ := m.Status()
		tla, _ := m.HasTopLevelAwait()
		async, _ := m.IsGraphAsync()
		ps, _ := p.State()
		same := stalled[0].Module == m
		compare(t, all, "module-advanced-residual/stalled_top_level_await", map[string]any{"linked": linked, "before_count": len(before), "after_count": len(stalled), "module_same": same, "status": status.String(), "has_tla": tla, "graph_async": async, "promise_state": ps.String(), "message": text, "line": line, "resource": resource, "source_line": sourceLine, "start_position": sp, "end_position": ep, "start_column": sc, "end_column": ec, "wasm_function_index": wi, "error_level": el, "shared_cross_origin": shared, "opaque": opaque})
	})

	t.Run("deferred_exception", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		m := r.compile(t, "deferred-error.mjs", "throw new RangeError('deferred boom'); export const answer=1;")
		linked, _ := m.Instantiate(r.scope, noModule, nil)
		g, _, _ := m.EvaluateForImportDefer(r.scope)
		gp := gov8.Promise{Value: g}
		gs, _ := gp.State()
		ns, _ := m.NamespaceWithPhase(r.scope, gov8.ModuleImportDefer)
		tc, _ := r.iso.NewTryCatch()
		r.trycatches = append(r.trycatches, tc)
		_, ok, _ := object(t, ns).GetByName(r.scope, r.ctx, "answer")
		caught, _ := tc.HasCaught()
		text, _ := tc.ExceptionText(r.scope, r.ctx)
		status, _ := m.Status()
		caughtV, _, _ := tc.Exception(r.scope)
		stored, _ := m.Exception(r.scope)
		same, _ := caughtV.StrictEquals(stored)
		compare(t, all, "module-advanced-residual/deferred_exception", map[string]any{"linked": linked, "gather_state": gs.String(), "property_none": !ok, "caught": caught, "exception": text, "status": status.String(), "stored_exception_same": same})
	})

	t.Run("stalled_tla_resolution_lifecycle", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		resolver, e := r.scope.NewPromiseResolver(r.ctx)
		if e != nil {
			t.Fatal(e)
		}
		pending, _ := resolver.GetPromise(r.scope)
		global, _ := r.ctx.GlobalObject(r.scope)
		_, _ = global.SetByName(r.scope, r.ctx, "pendingFixture", pending.Value)
		m := r.compile(t, "settled-tla.mjs", "await globalThis.pendingFixture; export const done=1;")
		_, _ = m.Instantiate(r.scope, noModule, nil)
		evaluation, _ := m.Evaluate(r.scope, nil)
		_ = r.iso.PerformMicrotaskCheckpoint()
		before, _ := m.StalledTopLevelAwaitMessages(r.scope)
		u, _ := r.scope.Undefined()
		_, _ = resolver.Resolve(r.ctx, u)
		_ = r.iso.PerformMicrotaskCheckpoint()
		after, _ := m.StalledTopLevelAwaitMessages(r.scope)
		state, _ := evaluation.State()
		status, _ := m.Status()
		ns, _ := m.Namespace(r.scope)
		doneV, _, _ := object(t, ns).GetByName(r.scope, r.ctx, "done")
		compare(t, all, "module-advanced-residual/stalled_tla_resolution_lifecycle", map[string]any{"stalled_before": len(before), "promise_after": state.String(), "stalled_after": len(after), "status": status.String(), "done": integer(t, doneV, r.ctx)})
	})

	t.Run("import_meta_callback", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		calls := 0
		if e := r.iso.SetHostInitializeImportMetaObjectCallback(func(cs *gov8.CallbackScope, _ *gov8.Module, meta *gov8.Object) error {
			calls++
			v, e := cs.Scope().Int32(42)
			if e != nil {
				return e
			}
			_, e = cs.ObjectSet(meta.Value, "oracle", v)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		m := r.compile(t, "meta.mjs", "export const value=import.meta.oracle; export const same=import.meta===import.meta;")
		_, _ = m.Instantiate(r.scope, noModule, nil)
		_, _ = m.Evaluate(r.scope, nil)
		_ = r.iso.PerformMicrotaskCheckpoint()
		ns, _ := m.Namespace(r.scope)
		o := object(t, ns)
		value, _, _ := o.GetByName(r.scope, r.ctx, "value")
		sameV, _, _ := o.GetByName(r.scope, r.ctx, "same")
		same, _ := sameV.IsTrue()
		status, _ := m.Status()
		compare(t, all, "module-advanced-residual/import_meta_callback", map[string]any{"calls": calls, "value": integer(t, value, r.ctx), "same_within_module": same, "status": status.String()})
	})

	t.Run("dynamic_import_callbacks", func(t *testing.T) {
		legacy := newRuntime(t)
		legacyCalls := 0
		_ = legacy.iso.SetHostImportModuleDynamicallyCallback(func(req gov8.DynamicImportRequest) (gov8.Promise, error) {
			legacyCalls++
			resolver, p, e := req.Scope.NewCallbackPromiseResolver()
			if e != nil {
				return gov8.Promise{}, e
			}
			v, e := req.Scope.NewString("dynamic-result")
			if e != nil {
				return gov8.Promise{}, e
			}
			_, e = req.Scope.SettleCallbackPromise(resolver, v, false)
			return p, e
		})
		old, _ := legacy.eval(t, "import('dynamic-dep')", nil)
		_ = legacy.iso.PerformMicrotaskCheckpoint()
		op := gov8.Promise{Value: old}
		os, _ := op.State()
		or, _ := op.Result(legacy.scope)
		ort, _ := or.ToString(legacy.ctx)
		legacy.close(t)
		phase := newRuntime(t)
		evalCalls, sourceCalls := 0, 0
		wasmValue, wasmErr := phase.eval(t, "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))", nil)
		if wasmErr != nil {
			t.Fatal(wasmErr)
		}
		_ = phase.iso.SetHostImportModuleDynamicallyCallback(func(req gov8.DynamicImportRequest) (gov8.Promise, error) {
			evalCalls++
			resolver, p, e := req.Scope.NewCallbackPromiseResolver()
			if e != nil {
				return p, e
			}
			v, e := req.Scope.NewString("phase-evaluation-result")
			if e == nil {
				_, e = req.Scope.SettleCallbackPromise(resolver, v, false)
			}
			return p, e
		})
		_ = phase.iso.SetHostImportModuleWithPhaseDynamicallyCallback(func(req gov8.DynamicImportRequest) (gov8.Promise, error) {
			sourceCalls++
			resolver, p, e := req.Scope.NewCallbackPromiseResolver()
			if e != nil {
				return p, e
			}
			if e == nil {
				_, e = req.Scope.SettleCallbackPromise(resolver, wasmValue, false)
			}
			return p, e
		})
		ev, _ := phase.eval(t, "import('phase-evaluation')", nil)
		src, _ := phase.eval(t, "import.source('source-dynamic')", nil)
		_ = phase.iso.PerformMicrotaskCheckpoint()
		ep := gov8.Promise{Value: ev}
		sp := gov8.Promise{Value: src}
		es, _ := ep.State()
		ss, _ := sp.State()
		erv, _ := ep.Result(phase.scope)
		ert, _ := erv.ToString(phase.ctx)
		srv, _ := sp.Result(phase.scope)
		wasm, _ := srv.IsWasmModuleObject()
		compare(t, all, "module-advanced-residual/dynamic_import_callbacks", map[string]any{"old_callback_calls": legacyCalls, "old_state": os.String(), "old_result": ort, "phase_evaluation_calls": evalCalls, "phase_evaluation_state": es.String(), "phase_evaluation_result": ert, "phase_source_calls": sourceCalls, "phase_source_state": ss.String(), "phase_source_is_wasm": wasm})
		phase.close(t)
	})

	t.Run("shadow_realm_callback", func(t *testing.T) {
		without := newRuntime(t)
		tc, _ := without.iso.NewTryCatch()
		without.trycatches = append(without.trycatches, tc)
		_, e := without.eval(t, "new ShadowRealm()", tc)
		caught, _ := tc.HasCaught()
		text, _ := tc.ExceptionText(without.scope, without.ctx)
		without.close(t)
		r := newRuntime(t)
		calls := 0
		var shadow *gov8.Context
		_ = r.iso.SetHostCreateShadowRealmContextCallback(func(cs *gov8.CallbackScope) (*gov8.Context, error) {
			calls++
			var e error
			shadow, e = r.iso.NewContext()
			if e != nil {
				return nil, e
			}
			global, e := shadow.GlobalObject(cs.Scope())
			if e != nil {
				return nil, e
			}
			v, e := cs.Scope().Int32(42)
			if e == nil {
				_, e = global.SetByName(cs.Scope(), shadow, "answer", v)
			}
			return shadow, e
		})
		value, e2 := r.eval(t, "new ShadowRealm().evaluate('globalThis.answer')", nil)
		if e2 != nil {
			t.Fatal(e2)
		}
		r.extraContexts = append(r.extraContexts, shadow)
		compare(t, all, "module-advanced-residual/shadow_realm_callback", map[string]any{"without_callback_none": e != nil, "without_callback_caught": caught, "without_callback_exception": text, "calls": calls, "evaluated_value": integer(t, value, r.ctx)})
		r.close(t)
	})
	if len(all) != 9 {
		t.Fatalf("fixture count=%d", len(all))
	}
}
