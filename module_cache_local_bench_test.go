//go:build windows && amd64

package gov8

import (
	"errors"
	"math"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"unsafe"
)

const (
	scopeLocalModuleCacheCode     = "export const answer = 42;"
	scopeLocalModuleCacheResource = "module-cache-bench.mjs"
)

var (
	moduleCacheLocalOnce        sync.Once
	moduleCacheCompileLocalAddr uintptr
)

func ensureModuleCacheLocalProc() {
	moduleCacheLocalOnce.Do(func() {
		moduleCacheCompileLocalAddr = proc("gov8_module_cache_compile_local").Addr()
	})
}

// compileModuleCachedLocal is a benchmark-only scope-local counterpart to the
// public persistent Module path. The returned wire handle is valid only until
// s closes and is deliberately not exposed as a Go value.
func (c *Context) compileModuleCachedLocal(s *Scope, source string,
	options ModuleCompileOptions, cache *ModuleCodeCache,
	tc *TryCatch) (local uintptr, rejected bool, err error) {
	if err = c.check(); err != nil {
		return 0, false, err
	}
	if s == nil || s.iso != c.iso {
		return 0, false, foreignIsolate("scope")
	}
	scopeHandle, err := s.checkedHandleAssumingIsolate()
	if err != nil {
		return 0, false, err
	}
	if tc != nil {
		if tc.iso != c.iso {
			return 0, false, foreignIsolate("trycatch")
		}
		if err = tc.check(); err != nil {
			return 0, false, err
		}
	}
	if len(source) > math.MaxInt32 || len(options.ResourceName) > math.MaxInt32 {
		return 0, false, errors.New("gov8: module source or resource name exceeds int32")
	}
	var cacheBytes []byte
	consume := uintptr(0)
	if cache != nil {
		if !cache.provenance {
			return 0, false, ErrModuleNotCacheable
		}
		if err = validateModuleCodeCacheLength(len(cache.data)); err != nil {
			return 0, false, err
		}
		cacheBytes = cache.data
		consume = 1
	}
	var tryCatchHandle uintptr
	if tc != nil {
		tryCatchHandle = tc.handle
	}
	sourceBytes := []byte(source)
	nameBytes := []byte(options.ResourceName)
	var rejectedInt int32
	ensureModuleCacheLocalProc()
	r1, _, _ := syscall.Syscall15(moduleCacheCompileLocalAddr, 14,
		c.iso.handleAssumingCheck(), c.handle, scopeHandle, tryCatchHandle,
		bytesArg(sourceBytes), uintptr(len(sourceBytes)),
		bytesArg(nameBytes), uintptr(len(nameBytes)),
		uintptr(options.LineOffset), uintptr(options.ColumnOffset),
		bytesArg(cacheBytes), uintptr(len(cacheBytes)), consume,
		uintptr(unsafe.Pointer(&rejectedInt)), 0)
	runtime.KeepAlive(sourceBytes)
	runtime.KeepAlive(nameBytes)
	runtime.KeepAlive(cacheBytes)
	if r1 == 0 {
		return 0, false, shimError("compileModuleCachedLocal", r1)
	}
	return r1, rejectedInt != 0, nil
}

func produceScopeLocalModuleCache(b *testing.B) *ModuleCodeCache {
	b.Helper()
	iso, err := NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	defer iso.Close()
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	defer ctx.Close()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer scope.Close()
	module, rejected, err := ctx.CompileModuleCached(scope,
		scopeLocalModuleCacheCode,
		ModuleCompileOptions{ResourceName: scopeLocalModuleCacheResource}, nil, nil)
	if err != nil || rejected {
		b.Fatalf("producer compile = rejected %v, err %v", rejected, err)
	}
	defer module.Close()
	unbound, err := module.GetUnboundModuleScript()
	if err != nil {
		b.Fatal(err)
	}
	defer unbound.Close()
	cache, err := unbound.CreateCodeCache()
	if err != nil || cache.Len() == 0 {
		b.Fatalf("CreateCodeCache = length %d, err %v", cache.Len(), err)
	}
	return cache
}

func probeScopeLocalModuleCache(b *testing.B, iso *Isolate, ctx *Context,
	cache *ModuleCodeCache) {
	b.Helper()
	localScope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	local, rejected, err := ctx.compileModuleCachedLocal(localScope,
		scopeLocalModuleCacheCode,
		ModuleCompileOptions{ResourceName: scopeLocalModuleCacheResource}, cache, nil)
	if err != nil || rejected || local == 0 {
		b.Fatalf("scope-local probe = handle %x, rejected %v, err %v", local, rejected, err)
	}
	if err := localScope.Close(); err != nil {
		b.Fatal(err)
	}

	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer scope.Close()
	module, rejected, err := ctx.CompileModuleCached(scope,
		scopeLocalModuleCacheCode,
		ModuleCompileOptions{ResourceName: scopeLocalModuleCacheResource}, cache, nil)
	if err != nil || rejected {
		b.Fatalf("correctness compile = rejected %v, err %v", rejected, err)
	}
	defer module.Close()
	linked, err := module.Instantiate(scope,
		func(ModuleResolveRequest) (*Module, error) { return nil, nil }, nil)
	if err != nil || !linked {
		b.Fatalf("Instantiate = %v, %v", linked, err)
	}
	if _, err := module.Evaluate(scope, nil); err != nil {
		b.Fatal(err)
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		b.Fatal(err)
	}
	namespace, err := module.Namespace(scope)
	if err != nil {
		b.Fatal(err)
	}
	object, err := AsObject(namespace)
	if err != nil {
		b.Fatal(err)
	}
	answer, ok, err := object.GetByName(scope, ctx, "answer")
	if err != nil || !ok {
		b.Fatalf("namespace.answer = ok %v, err %v", ok, err)
	}
	value, ok, err := answer.IntegerValue(ctx)
	if err != nil || !ok || value != 42 {
		b.Fatalf("answer = %d, ok %v, err %v", value, ok, err)
	}
}

func BenchmarkModuleCacheConsumeCompile(b *testing.B) {
	cache := produceScopeLocalModuleCache(b)
	iso, err := NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		b.Fatal(err)
	}
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	probeScopeLocalModuleCache(b, iso, ctx, cache)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		local, rejected, err := ctx.compileModuleCachedLocal(scope,
			scopeLocalModuleCacheCode,
			ModuleCompileOptions{ResourceName: scopeLocalModuleCacheResource}, cache, nil)
		if err != nil || rejected || local == 0 {
			b.Fatalf("consume compile = handle %x, rejected %v, err %v", local, rejected, err)
		}
		runtime.KeepAlive(local)
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
