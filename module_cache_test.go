//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

const moduleCacheSource = "export const answer = 42;\n//# sourceURL=virtual.mjs\n//# sourceMappingURL=virtual.map"

func produceModuleCache(t testing.TB, resource string) *gov8.ModuleCodeCache {
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
	module, rejected, err := ctx.CompileModuleCached(scope, moduleCacheSource,
		gov8.ModuleCompileOptions{ResourceName: resource}, nil, nil)
	if err != nil || rejected {
		t.Fatalf("producer compile = rejected %v, err %v", rejected, err)
	}
	unbound, err := module.GetUnboundModuleScript()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := unbound.CreateCodeCache()
	if err != nil {
		t.Fatal(err)
	}
	_ = unbound.Close()
	_ = module.Close()
	_ = scope.Close()
	_ = ctx.Close()
	_ = iso.Close()
	return cache
}

func moduleAnswer(t *testing.T, iso *gov8.Isolate, module *gov8.Module, scope *gov8.Scope, ctx *gov8.Context) int64 {
	t.Helper()
	linked, err := module.Instantiate(scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) {
		return nil, nil
	}, nil)
	if err != nil || !linked {
		t.Fatalf("Instantiate = %v, %v", linked, err)
	}
	if _, err := module.Evaluate(scope, nil); err != nil {
		t.Fatal(err)
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatal(err)
	}
	namespace, err := module.Namespace(scope)
	if err != nil {
		t.Fatal(err)
	}
	object, err := gov8.AsObject(namespace)
	if err != nil {
		t.Fatal(err)
	}
	answer, ok, err := object.GetByName(scope, ctx, "answer")
	if err != nil || !ok {
		t.Fatalf("namespace.answer = ok %v, err %v", ok, err)
	}
	value, ok, err := answer.IntegerValue(ctx)
	if err != nil || !ok {
		t.Fatalf("answer conversion = ok %v, err %v", ok, err)
	}
	return value
}

func TestUnboundModuleMetadataAndRepeatedCodeCache(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	module, rejected, err := ctx.CompileModuleCached(scope, moduleCacheSource,
		gov8.ModuleCompileOptions{ResourceName: "origin.mjs"}, nil, nil)
	if err != nil || rejected {
		t.Fatalf("compile = rejected %v, err %v", rejected, err)
	}
	defer module.Close()
	unbound, err := module.GetUnboundModuleScript()
	if err != nil {
		t.Fatal(err)
	}
	defer unbound.Close()
	sourceURL, err := unbound.SourceURL(scope)
	if err != nil {
		t.Fatal(err)
	}
	mappingURL, err := unbound.SourceMappingURL(scope)
	if err != nil {
		t.Fatal(err)
	}
	sourceText, sourceErr := sourceURL.StringValue()
	mappingText, mappingErr := mappingURL.StringValue()
	if sourceErr != nil || sourceText != "virtual.mjs" || mappingErr != nil || mappingText != "virtual.map" {
		t.Fatalf("metadata = %q/%q, errors %v/%v", sourceText, mappingText, sourceErr, mappingErr)
	}
	if id, err := unbound.ScriptID(); err != nil || id <= 0 {
		t.Fatalf("ScriptID = %d, %v", id, err)
	}
	first, err := unbound.CreateCodeCache()
	if err != nil {
		t.Fatal(err)
	}
	second, err := unbound.CreateCodeCache()
	if err != nil {
		t.Fatal(err)
	}
	if first.Len() == 0 || first.Len() != second.Len() || !first.Equal(second) {
		t.Fatalf("cache lengths = %d/%d", first.Len(), second.Len())
	}
	_ = iso
}

func TestModuleCodeCacheCrossIsolateAndChangedOrigin(t *testing.T) {
	cache := produceModuleCache(t, "first.mjs")
	iso, ctx, scope := newTestRuntime(t)
	module, rejected, err := ctx.CompileModuleCached(scope, moduleCacheSource,
		gov8.ModuleCompileOptions{ResourceName: "second.mjs"}, cache, nil)
	if err != nil || rejected {
		t.Fatalf("consume = rejected %v, err %v", rejected, err)
	}
	defer module.Close()
	if answer := moduleAnswer(t, iso, module, scope, ctx); answer != 42 {
		t.Fatalf("answer = %d", answer)
	}
	unbound, err := module.GetUnboundModuleScript()
	if err != nil {
		t.Fatal(err)
	}
	defer unbound.Close()
	value, err := unbound.SourceURL(scope)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := value.StringValue(); err != nil || text != "virtual.mjs" {
		t.Fatalf("sourceURL = %q, %v", text, err)
	}
}
