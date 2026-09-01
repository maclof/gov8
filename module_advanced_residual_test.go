//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestModuleAdvancedResidualValidation(t *testing.T) {
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	module, err := ctx.CompileModule(scope, "export const x=1", "validation.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.NamespaceWithPhase(scope, gov8.ModuleImportPhase(-1)); err == nil || !strings.Contains(err.Error(), "invalid module import phase") {
		t.Fatalf("invalid phase error = %v", err)
	}
	if _, _, err := module.EvaluateForImportDefer(nil); err == nil {
		t.Fatal("nil scope accepted")
	}
	if _, err := module.Instantiate2(scope, nil, func(gov8.ModuleSourceResolveRequest) (gov8.Value, error) { return gov8.Value{}, nil }, nil); err == nil {
		t.Fatal("nil module resolver accepted")
	}
	if _, err := module.Instantiate2(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil, nil); err == nil {
		t.Fatal("nil source resolver accepted")
	}
	if err := module.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := module.StalledTopLevelAwaitMessages(scope); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("closed module error = %v", err)
	}
	_ = gov8.ReleaseIsolateHostState(iso)
	_ = scope.Close()
	_ = ctx.Close()
	_ = iso.Close()
}

func TestModuleAdvancedResidualForeignIsolateAndDrain(t *testing.T) {
	isoA := newIso(t)
	ctxA := newCtx(t, isoA)
	scopeA := newScope(t, isoA)
	isoB := newIso(t)
	scopeB := newScope(t, isoB)
	module, err := ctxA.CompileModule(scopeA, "export const x=1", "foreign.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.NamespaceWithPhase(scopeB, gov8.ModuleImportEvaluation); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope error = %v", err)
	}
	if err := isoA.SetHostInitializeImportMetaObjectCallback(func(*gov8.CallbackScope, *gov8.Module, *gov8.Object) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := isoA.SetHostImportModuleDynamicallyCallback(func(gov8.DynamicImportRequest) (gov8.Promise, error) { return gov8.Promise{}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := gov8.ReleaseIsolateHostState(isoA); err != nil {
		t.Fatal(err)
	}
	if err := gov8.ReleaseIsolateHostState(isoA); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	_ = scopeB.Close()
	_ = isoB.Close()
	_ = module.Close()
	_ = scopeA.Close()
	_ = ctxA.Close()
	_ = isoA.Close()
}

func TestModuleAdvancedResidualWrongThread(t *testing.T) {
	iso := newIso(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- iso.SetHostInitializeImportMetaObjectCallback(func(*gov8.CallbackScope, *gov8.Module, *gov8.Object) error { return nil })
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread setter error = %v", err)
	}
	_ = gov8.ReleaseIsolateHostState(iso)
	_ = iso.Close()
}

func TestStalledTopLevelAwaitIdentityWithAnotherCurrentIsolate(t *testing.T) {
	isoA := newIso(t)
	ctxA := newCtx(t, isoA)
	scopeA := newScope(t, isoA)
	module, err := ctxA.CompileModule(scopeA, "await new Promise(() => {});", "interleaved-tla.mjs", nil)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := module.Instantiate(scopeA, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil)
	if err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
	if _, err := module.Evaluate(scopeA, nil); err != nil {
		t.Fatal(err)
	}
	if err := isoA.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatal(err)
	}

	// Keep B current while both the stalled query and its local-to-persistent
	// identity match operate on A.
	isoB := newIso(t)
	scopeB := newScope(t, isoB)
	stalled, err := module.StalledTopLevelAwaitMessages(scopeA)
	if err != nil {
		t.Fatal(err)
	}
	if len(stalled) != 1 || stalled[0].Module != module {
		t.Fatalf("stalled identity = %#v", stalled)
	}

	_ = gov8.ReleaseIsolateHostState(isoB)
	_ = scopeB.Close()
	_ = isoB.Close()
	_ = module.Close()
	_ = gov8.ReleaseIsolateHostState(isoA)
	_ = scopeA.Close()
	_ = ctxA.Close()
	_ = isoA.Close()
}
