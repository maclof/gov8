//go:build windows && amd64

package gov8

import (
	"errors"
	"math"
	"strings"
	"testing"
)

const internalModuleCacheSource = "export const answer = 42;"

func internalModuleCache(t *testing.T) *ModuleCodeCache {
	t.Helper()
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	module, rejected, err := ctx.CompileModuleCached(scope, internalModuleCacheSource,
		ModuleCompileOptions{ResourceName: "producer.mjs"}, nil, nil)
	if err != nil || rejected {
		t.Fatalf("producer = rejected %v, err %v", rejected, err)
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

func internalModuleAnswer(t *testing.T, iso *Isolate, ctx *Context, scope *Scope, module *Module) int64 {
	t.Helper()
	linked, err := module.Instantiate(scope, func(ModuleResolveRequest) (*Module, error) { return nil, nil }, nil)
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
	object, err := AsObject(namespace)
	if err != nil {
		t.Fatal(err)
	}
	value, ok, err := object.GetByName(scope, ctx, "answer")
	if err != nil || !ok {
		t.Fatalf("answer property = ok %v, err %v", ok, err)
	}
	answer, ok, err := value.IntegerValue(ctx)
	if err != nil || !ok {
		t.Fatalf("answer conversion = ok %v, err %v", ok, err)
	}
	return answer
}

func TestModuleCodeCacheEngineRejectionBoundaries(t *testing.T) {
	cache := internalModuleCache(t)
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer iso.Close()
	ctx, _ := iso.NewContext()
	defer ctx.Close()
	scope, _ := iso.NewScope()
	defer scope.Close()

	truncated := &ModuleCodeCache{
		data: append([]byte(nil), cache.data[:len(cache.data)/2]...), provenance: true,
	}
	module, rejected, err := ctx.CompileModuleCached(scope, internalModuleCacheSource,
		ModuleCompileOptions{ResourceName: "truncated.mjs"}, truncated, nil)
	if err != nil || !rejected || internalModuleAnswer(t, iso, ctx, scope, module) != 42 {
		t.Fatalf("truncated = rejected %v, err %v", rejected, err)
	}
	_ = module.Close()

	corrupted := &ModuleCodeCache{data: append([]byte(nil), cache.data...), provenance: true}
	corrupted.data[len(corrupted.data)/2] ^= 0xff
	module, rejected, err = ctx.CompileModuleCached(scope, internalModuleCacheSource,
		ModuleCompileOptions{ResourceName: "corrupt.mjs"}, corrupted, nil)
	if err != nil || rejected || internalModuleAnswer(t, iso, ctx, scope, module) != 42 {
		t.Fatalf("corrupted = rejected %v, err %v", rejected, err)
	}
	_ = module.Close()

	module, rejected, err = ctx.CompileModuleCached(scope, "export const answer = 43;",
		ModuleCompileOptions{ResourceName: "changed.mjs"}, cache, nil)
	if err != nil || rejected || internalModuleAnswer(t, iso, ctx, scope, module) != 42 {
		t.Fatalf("changed source = rejected %v, err %v", rejected, err)
	}
	_ = module.Close()
}

func TestModuleCodeCacheSafeBoundaries(t *testing.T) {
	if err := validateModuleCodeCacheLength(math.MaxInt32 + 1); err == nil || !strings.Contains(err.Error(), "int32") {
		t.Fatalf("oversize error = %v", err)
	}
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	if _, _, err := ctx.CompileModuleCached(scope, "", ModuleCompileOptions{}, &ModuleCodeCache{}, nil); !errors.Is(err, ErrModuleNotCacheable) {
		t.Fatalf("unproven cache error = %v", err)
	}
	module, _, err := ctx.CompileModuleCached(scope, internalModuleCacheSource, ModuleCompileOptions{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	unbound, err := module.GetUnboundModuleScript()
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Close(); err != nil {
		t.Fatal(err)
	}
	if id, err := unbound.ScriptID(); err != nil || id <= 0 {
		t.Fatalf("unbound after module close = %d, %v", id, err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := unbound.ScriptID()
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if err := unbound.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := unbound.CreateCodeCache(); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed unbound error = %v", err)
	}
	_ = scope.Close()
	_ = ctx.Close()
	_ = iso.Close()
}

func TestUnboundModuleForeignScope(t *testing.T) {
	isoA, _ := NewIsolate()
	isoB, _ := NewIsolate()
	ctxA, _ := isoA.NewContext()
	scopeA, _ := isoA.NewScope()
	scopeB, _ := isoB.NewScope()
	module, _, err := ctxA.CompileModuleCached(scopeA, internalModuleCacheSource, ModuleCompileOptions{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	unbound, _ := module.GetUnboundModuleScript()
	if _, err := unbound.SourceURL(scopeB); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign-scope error = %v", err)
	}
	_ = unbound.Close()
	_ = module.Close()
	_ = scopeB.Close()
	_ = scopeA.Close()
	_ = ctxA.Close()
	_ = isoB.Close()
	_ = isoA.Close()
}
