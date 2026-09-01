//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

func BenchmarkModuleCodeCacheProduce(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	scope, _ := iso.NewScope()
	module, _, err := ctx.CompileModuleCached(scope, moduleCacheSource,
		gov8.ModuleCompileOptions{ResourceName: "module-cache-bench.mjs"}, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	unbound, err := module.GetUnboundModuleScript()
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		_ = unbound.Close()
		_ = module.Close()
		_ = scope.Close()
		_ = ctx.Close()
		_ = iso.Close()
	}()
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		iterationScope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		cache, err := unbound.CreateCodeCache()
		if err != nil || cache.Len() == 0 {
			b.Fatalf("CreateCodeCache = length %d, %v", cache.Len(), err)
		}
		if err := iterationScope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkModuleCodeCacheConsume(b *testing.B) {
	cache := produceModuleCache(b, "module-cache-bench.mjs")
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		module, rejected, err := ctx.CompileModuleCached(scope, moduleCacheSource,
			gov8.ModuleCompileOptions{ResourceName: "module-cache-bench.mjs"}, cache, nil)
		if err != nil || rejected {
			b.Fatalf("CompileModuleCached = rejected %v, %v", rejected, err)
		}
		if err := module.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
