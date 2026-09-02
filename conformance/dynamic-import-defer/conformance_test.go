//go:build windows && amd64

package dynamicimportdeferconformance

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	gov8 "github.com/maclof/gov8"
)

const createNoWindow = 0x08000000

func TestMain(m *testing.M) {
	if err := gov8.SetFlagsFromString("--js-defer-import-eval --harmony-import-attributes"); err != nil {
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

func loadFixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-dynamic-import-defer-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result := make(map[string]fixtureLine)
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

func compare(t *testing.T, fixture map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fixture[id]
	if !ok {
		t.Fatalf("fixture lacks %s", id)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want.Value)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s mismatch\n got %s\nwant %s", id, gotJSON, wantJSON)
	}
}

type callbackObservation struct {
	resourceName string
	specifier    string
	phase        string
	attributes   []string
}

type pendingImport struct {
	resolver    *gov8.Global
	promise     *gov8.Global
	namespace   *gov8.Global
	preparation *gov8.Global
	module      *gov8.Module
}

func (p *pendingImport) close() {
	if p == nil {
		return
	}
	for _, global := range []*gov8.Global{p.preparation, p.namespace, p.promise, p.resolver} {
		if global != nil {
			_ = global.Close()
		}
	}
	if p.module != nil {
		_ = p.module.Close()
	}
}

type callbackState struct {
	legacyCalls int
	observed    []callbackObservation
	pending     []*pendingImport
}

type runtimeState struct {
	iso     *gov8.Isolate
	ctx     *gov8.Context
	scope   *gov8.Scope
	scripts []*gov8.Script
	state   callbackState
}

func newRuntime(t *testing.T) *runtimeState {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = ctx.Close()
		_ = iso.Close()
		t.Fatal(err)
	}
	r := &runtimeState{iso: iso, ctx: ctx, scope: scope}
	if err := iso.SetHostImportModuleDynamicallyCallback(func(req gov8.DynamicImportRequest) (gov8.Promise, error) {
		r.state.legacyCalls++
		resolver, promise, err := req.Scope.NewCallbackPromiseResolver()
		if err != nil {
			return gov8.Promise{}, err
		}
		value, err := req.Scope.NewString("unexpected legacy callback")
		if err != nil {
			return gov8.Promise{}, err
		}
		if _, err := req.Scope.SettleCallbackPromise(resolver, value, false); err != nil {
			return gov8.Promise{}, err
		}
		return promise, nil
	}); err != nil {
		r.close(t)
		t.Fatal(err)
	}
	if err := iso.SetHostImportModuleWithPhaseDynamicallyCallback(r.phaseCallback); err != nil {
		r.close(t)
		t.Fatal(err)
	}
	return r
}

func (r *runtimeState) close(t *testing.T) {
	t.Helper()
	for _, pending := range r.state.pending {
		pending.close()
	}
	r.state.pending = nil
	for i := len(r.scripts) - 1; i >= 0; i-- {
		_ = r.scripts[i].Close()
	}
	if r.iso != nil {
		if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
			t.Errorf("ReleaseIsolateHostState: %v", err)
		}
	}
	if r.scope != nil {
		_ = r.scope.Close()
	}
	if r.ctx != nil {
		_ = r.ctx.Close()
	}
	if r.iso != nil {
		_ = r.iso.Close()
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
		return fmt.Sprintf("ModuleImportPhase(%d)", phase)
	}
}

func noImports(gov8.ModuleResolveRequest) (*gov8.Module, error) {
	return nil, errors.New("unexpected module dependency")
}

func (r *runtimeState) phaseCallback(req gov8.DynamicImportRequest) (gov8.Promise, error) {
	resourceName, err := req.Scope.ToString(req.ResourceName)
	if err != nil {
		return gov8.Promise{}, err
	}
	specifier, err := req.Scope.ToString(req.Specifier)
	if err != nil {
		return gov8.Promise{}, err
	}
	length, err := req.Attributes.Length()
	if err != nil {
		return gov8.Promise{}, err
	}
	attributes := make([]string, length)
	for index := range attributes {
		data, ok, err := req.Attributes.Get(req.Scope.Scope(), index)
		if err != nil || !ok {
			return gov8.Promise{}, fmt.Errorf("attribute %d: ok=%v err=%w", index, ok, err)
		}
		value, ok, err := data.Value()
		if err != nil || !ok {
			return gov8.Promise{}, fmt.Errorf("attribute value %d: ok=%v err=%w", index, ok, err)
		}
		attributes[index], err = req.Scope.ToString(value)
		if err != nil {
			return gov8.Promise{}, err
		}
	}
	r.state.observed = append(r.state.observed, callbackObservation{
		resourceName: resourceName,
		specifier:    specifier,
		phase:        phaseName(req.Phase),
		attributes:   attributes,
	})

	resolver, promise, err := req.Scope.NewCallbackPromiseResolver()
	if err != nil {
		return gov8.Promise{}, err
	}
	if specifier == "reject-me" {
		reason, err := req.Scope.NewString("host rejected deferred import")
		if err != nil {
			return gov8.Promise{}, err
		}
		if _, err := req.Scope.SettleCallbackPromise(resolver, reason, true); err != nil {
			return gov8.Promise{}, err
		}
		return promise, nil
	}

	ordinal := len(r.state.pending) + 1
	source := fmt.Sprintf("globalThis.deferBodyHits++; export const answer=42; export const ordinal=%d;", ordinal)
	module, err := r.ctx.CompileModule(req.Scope.Scope(), source, specifier+".mjs", nil)
	if err != nil {
		return gov8.Promise{}, err
	}
	closeModule := true
	defer func() {
		if closeModule {
			_ = module.Close()
		}
	}()
	instantiated, err := module.Instantiate(req.Scope.Scope(), noImports, nil)
	if err != nil || !instantiated {
		return gov8.Promise{}, fmt.Errorf("instantiate deferred module: ok=%v err=%w", instantiated, err)
	}
	preparation, ok, err := module.EvaluateForImportDefer(req.Scope.Scope())
	if err != nil || !ok {
		return gov8.Promise{}, fmt.Errorf("prepare deferred module: ok=%v err=%w", ok, err)
	}
	namespace, err := module.NamespaceWithPhase(req.Scope.Scope(), gov8.ModuleImportDefer)
	if err != nil {
		return gov8.Promise{}, err
	}
	globals := make([]*gov8.Global, 0, 4)
	root := func(value gov8.Value) (*gov8.Global, error) {
		global, err := gov8.NewGlobal(req.Scope.Scope(), value)
		if err == nil {
			globals = append(globals, global)
		}
		return global, err
	}
	resolverGlobal, err := root(resolver.Value)
	if err != nil {
		return gov8.Promise{}, err
	}
	promiseGlobal, err := root(promise.Value)
	if err != nil {
		for _, global := range globals {
			_ = global.Close()
		}
		return gov8.Promise{}, err
	}
	namespaceGlobal, err := root(namespace)
	if err != nil {
		for _, global := range globals {
			_ = global.Close()
		}
		return gov8.Promise{}, err
	}
	preparationGlobal, err := root(preparation)
	if err != nil {
		for _, global := range globals {
			_ = global.Close()
		}
		return gov8.Promise{}, err
	}
	r.state.pending = append(r.state.pending, &pendingImport{
		resolver: resolverGlobal, promise: promiseGlobal, namespace: namespaceGlobal,
		preparation: preparationGlobal, module: module,
	})
	closeModule = false
	return promise, nil
}

func (r *runtimeState) runScript(t *testing.T, name, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.CompileWithOrigin(r.scope, source, &gov8.Origin{ResourceName: name}, nil)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	r.scripts = append(r.scripts, script)
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatalf("run %s: %v", name, err)
	}
	return value
}

func (r *runtimeState) promise(t *testing.T, source string) gov8.Promise {
	t.Helper()
	return gov8.Promise{Value: r.runScript(t, "probe.js", source)}
}

func (r *runtimeState) stateName(t *testing.T, promise gov8.Promise) string {
	t.Helper()
	state, err := promise.State()
	if err != nil {
		t.Fatal(err)
	}
	return state.String()
}

func (r *runtimeState) integer(t *testing.T, source string) int64 {
	t.Helper()
	value := r.runScript(t, "probe.js", source)
	result, ok, err := value.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("integer %q: value=%d ok=%v err=%v", source, result, ok, err)
	}
	return result
}

func (r *runtimeState) boolean(t *testing.T, source string) bool {
	t.Helper()
	value := r.runScript(t, "probe.js", source)
	result, err := value.IsTrue()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (r *runtimeState) resolvePending(t *testing.T, index int) bool {
	t.Helper()
	pending := r.state.pending[index]
	resolverValue, err := pending.resolver.ToLocal(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := pending.namespace.ToLocal(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := (gov8.PromiseResolver{Value: resolverValue}).Resolve(r.ctx, namespace)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func (r *runtimeState) pendingStatus(t *testing.T, index int) (string, string) {
	t.Helper()
	pending := r.state.pending[index]
	status, err := pending.module.Status()
	if err != nil {
		t.Fatal(err)
	}
	preparationValue, err := pending.preparation.ToLocal(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	preparationState, err := (gov8.Promise{Value: preparationValue}).State()
	if err != nil {
		t.Fatal(err)
	}
	return status.String(), preparationState.String()
}

func TestDynamicImportDeferFixture(t *testing.T) {
	fixture := loadFixture(t)
	t.Run("pin", func(t *testing.T) {
		version, err := gov8.RuntimeVersionString()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixture, "dynamic-import-defer/pin", map[string]any{
			"rust_crate": "v8=152.2.0", "v8": version,
			"flag": "--js-defer-import-eval", "syntax": "import.defer(...)"})
	})

	t.Run("phase_payload_and_lazy_evaluation", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		outer := gov8.Promise{Value: r.runScript(t, "defer-entry.js",
			"globalThis.deferBodyHits=0; globalThis.deferFulfilled=false; globalThis.deferNS=undefined; "+
				"globalThis.deferOuter=import.defer('deferred-ok',{with:{type:'json',mode:'lazy'}}); "+
				"deferOuter.then(ns=>{deferFulfilled=true; deferNS=ns;}); deferOuter")}
		moduleBefore, preparationInitial := r.pendingStatus(t, 0)
		outerInitial := r.stateName(t, outer)
		callbackPromise, err := r.state.pending[0].promise.ToLocal(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		returnedSame, err := outer.Value.StrictEquals(callbackPromise)
		if err != nil {
			t.Fatal(err)
		}
		bodyBefore := r.integer(t, "deferBodyHits")
		resolved := r.resolvePending(t, 0)
		immediate := r.stateName(t, outer)
		if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		afterCheckpoint := r.stateName(t, outer)
		thenRan := r.boolean(t, "deferFulfilled")
		bodyAfterFulfillment := r.integer(t, "deferBodyHits")
		moduleAfterFulfillment, _ := r.pendingStatus(t, 0)
		answer := r.integer(t, "deferNS.answer")
		bodyAfterAccess := r.integer(t, "deferBodyHits")
		answerAgain := r.integer(t, "deferNS.answer")
		bodyAfterSecondAccess := r.integer(t, "deferBodyHits")
		moduleAfterAccess, _ := r.pendingStatus(t, 0)
		observation := r.state.observed[0]
		compare(t, fixture, "dynamic-import-defer/phase_payload_and_lazy_evaluation", map[string]any{
			"phase_calls": len(r.state.observed), "legacy_calls": r.state.legacyCalls,
			"resource_name": observation.resourceName, "specifier": observation.specifier,
			"phase": observation.phase, "attributes": observation.attributes,
			"outer_initial": outerInitial, "returned_is_callback_promise": returnedSame,
			"preparation_initial": preparationInitial, "module_before_settlement": moduleBefore,
			"body_before_settlement": bodyBefore, "resolver_resolved": resolved,
			"outer_immediately_after_resolve": immediate, "outer_after_checkpoint": afterCheckpoint,
			"then_ran": thenRan, "body_after_fulfillment": bodyAfterFulfillment,
			"module_after_fulfillment": moduleAfterFulfillment, "answer": answer,
			"body_after_access": bodyAfterAccess, "answer_again": answerAgain,
			"body_after_second_access": bodyAfterSecondAccess, "module_after_access": moduleAfterAccess,
		})
	})

	t.Run("rejected_callback_promise", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		promise := gov8.Promise{Value: r.runScript(t, "reject-entry.js",
			"globalThis.rejectSeen='unset'; globalThis.rejectOuter=import.defer('reject-me'); "+
				"rejectOuter.catch(e=>{rejectSeen=String(e);}); rejectOuter")}
		before := r.stateName(t, promise)
		reasonValue, err := promise.Result(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		reason, err := reasonValue.ToString(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		after := r.stateName(t, promise)
		caught, err := r.runScript(t, "probe.js", "rejectSeen").ToString(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		observation := r.state.observed[0]
		compare(t, fixture, "dynamic-import-defer/rejected_callback_promise", map[string]any{
			"phase_calls": len(r.state.observed), "legacy_calls": r.state.legacyCalls,
			"specifier": observation.specifier, "phase": observation.phase,
			"before_checkpoint": before, "after_checkpoint": after,
			"reason": reason, "caught": caught,
		})
	})

	t.Run("invalid_attributes_before_callback", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		promise := gov8.Promise{Value: r.runScript(t, "invalid-attributes.js",
			"globalThis.invalidSeen='unset'; globalThis.invalidOuter=import.defer('must-not-call',{with:{type:1}}); "+
				"invalidOuter.catch(e=>{invalidSeen=String(e);}); invalidOuter")}
		before := r.stateName(t, promise)
		reasonValue, err := promise.Result(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		reason, err := reasonValue.ToString(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		after := r.stateName(t, promise)
		caught, err := r.runScript(t, "probe.js", "invalidSeen").ToString(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixture, "dynamic-import-defer/invalid_attributes_before_callback", map[string]any{
			"phase_calls": len(r.state.observed), "legacy_calls": r.state.legacyCalls,
			"before_checkpoint": before, "after_checkpoint": after,
			"reason": reason, "caught": caught,
		})
	})

	t.Run("repeated_delayed_settlement", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		r.runScript(t, "repeat-entry.js",
			"globalThis.deferBodyHits=0; globalThis.repeatNS1=undefined; globalThis.repeatNS2=undefined; "+
				"globalThis.repeatP1=import.defer('repeat'); globalThis.repeatP2=import.defer('repeat'); "+
				"repeatP1.then(ns=>{repeatNS1=ns;}); repeatP2.then(ns=>{repeatNS2=ns;});")
		p1 := r.promise(t, "repeatP1")
		p2 := r.promise(t, "repeatP2")
		same, err := p1.Value.StrictEquals(p2.Value)
		if err != nil {
			t.Fatal(err)
		}
		resolvedSecond := r.resolvePending(t, 1)
		if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		afterSecondP1 := r.stateName(t, p1)
		afterSecondP2 := r.stateName(t, p2)
		bodyAfterSecond := r.integer(t, "deferBodyHits")
		resolvedFirst := r.resolvePending(t, 0)
		if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
			t.Fatal(err)
		}
		afterBothP1 := r.stateName(t, p1)
		afterBothP2 := r.stateName(t, p2)
		namespacesDistinct := r.boolean(t, "repeatNS1!==repeatNS2")
		firstOrdinal := r.integer(t, "repeatNS1.ordinal")
		hitsAfterFirst := r.integer(t, "deferBodyHits")
		secondOrdinal := r.integer(t, "repeatNS2.ordinal")
		hitsAfterSecond := r.integer(t, "deferBodyHits")
		status1, _ := r.pendingStatus(t, 0)
		status2, _ := r.pendingStatus(t, 1)
		specifiers := make([]string, len(r.state.observed))
		for index, observation := range r.state.observed {
			specifiers[index] = observation.specifier
		}
		compare(t, fixture, "dynamic-import-defer/repeated_delayed_settlement", map[string]any{
			"phase_calls": len(r.state.observed), "legacy_calls": r.state.legacyCalls,
			"specifiers": specifiers, "promises_distinct": !same,
			"resolved_second": resolvedSecond, "after_second_p1": afterSecondP1,
			"after_second_p2": afterSecondP2, "body_after_second_settlement": bodyAfterSecond,
			"resolved_first": resolvedFirst, "after_both_p1": afterBothP1,
			"after_both_p2": afterBothP2, "namespaces_distinct": namespacesDistinct,
			"first_ordinal": firstOrdinal, "hits_after_first_access": hitsAfterFirst,
			"second_ordinal": secondOrdinal, "hits_after_second_access": hitsAfterSecond,
			"module_statuses": []string{status1, status2},
		})
	})
}

func TestFixtureShape(t *testing.T) {
	fixture := loadFixture(t)
	if len(fixture) != 5 {
		t.Fatalf("fixture checks = %d, want 5", len(fixture))
	}
}

func TestLegacyOnlyImportDeferFatalBoundary(t *testing.T) {
	if os.Getenv("GOV8_DYNAMIC_IMPORT_DEFER_LEGACY_FATAL") == "1" {
		installBreakpointExit()
		iso, err := gov8.NewIsolate()
		if err != nil {
			panic(err)
		}
		ctx, err := iso.NewContext()
		if err != nil {
			panic(err)
		}
		scope, err := iso.NewScope()
		if err != nil {
			panic(err)
		}
		if err := iso.SetHostImportModuleDynamicallyCallback(func(req gov8.DynamicImportRequest) (gov8.Promise, error) {
			panic("legacy callback must not receive import.defer")
		}); err != nil {
			panic(err)
		}
		script, err := ctx.CompileWithOrigin(scope, "import.defer('legacy-only')", &gov8.Origin{ResourceName: "legacy-only.js"}, nil)
		if err != nil {
			panic(err)
		}
		_, _ = script.Run(scope, nil)
		os.Exit(0)
	}
	for run := 0; run < 2; run++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLegacyOnlyImportDeferFatalBoundary$")
		cmd.Env = append(os.Environ(), "GOV8_DYNAMIC_IMPORT_DEFER_LEGACY_FATAL=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("run %d: legacy-only child succeeded: %s", run, output)
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok || uint32(exitErr.ExitCode()) != 0x80000003 {
			t.Fatalf("run %d: exit=%v output=%s", run, err, output)
		}
		text := string(output)
		if !strings.Contains(text, "Fatal error") ||
			!strings.Contains(text, "Check failed: (host_import_module_with_phase_dynamically_callback_) != nullptr") {
			t.Fatalf("run %d: fatal output=%s", run, text)
		}
	}
}

// installBreakpointExit makes the subprocess expose V8's raw
// STATUS_BREAKPOINT, matching the plain Rust executable. Without this
// first-chance handler the Go runtime intercepts the breakpoint, prints its
// own crash dump, and exits with code 2.
func installBreakpointExit() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	exitProcess := kernel32.NewProc("ExitProcess")
	filter := syscall.NewCallback(func(exceptionPointers uintptr) uintptr {
		record := *(*uintptr)(wordToPointer(exceptionPointers))
		code := *(*uint32)(wordToPointer(record))
		if code == 0x80000003 {
			exitProcess.Call(uintptr(code))
		}
		return 0
	})
	kernel32.NewProc("AddVectoredExceptionHandler").Call(1, filter)
}

func wordToPointer(word uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&word))
}
