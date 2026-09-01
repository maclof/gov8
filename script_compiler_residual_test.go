//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"strings"
	"testing"

	gov8 "gov8"
)

func TestScriptCompilerConsumeWithoutCacheIsSafeError(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	source := gov8.NewScriptCompilerSource("40 + 2", nil)
	script, err := ctx.CompileScriptCompilerSource(scope, source,
		gov8.OptConsumeCodeCache, gov8.NoCacheNoReason, nil)
	if script != nil || err == nil || !strings.Contains(err.Error(), "requires cached data") {
		t.Fatalf("consume without cache = %v, %v", script, err)
	}
	if source.CachedData().Present {
		t.Fatal("failed consume invented cached data")
	}
}

func TestScriptCompilerSourceCopiesCacheAndValidatesReason(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	input := []byte{1, 2, 3}
	source, err := gov8.NewScriptCompilerSourceWithCachedData("40 + 2", nil, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 9
	state := source.CachedData()
	if !state.Present || state.Rejected || !bytes.Equal(state.Bytes, []byte{1, 2, 3}) {
		t.Fatalf("initial state = %+v", state)
	}
	state.Bytes[1] = 9
	if got := source.CachedData().Bytes; !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("CachedData exposed mutable storage: %v", got)
	}
	if _, err := ctx.CompileScriptCompilerSource(scope,
		gov8.NewScriptCompilerSource("1", nil), gov8.OptNoCompileOptions,
		gov8.NoCacheReason(99), nil); err == nil {
		t.Fatal("unknown NoCacheReason must fail")
	}
}

func TestCurrentHostDefinedOptionsOutsideRunAndWrongThread(t *testing.T) {
	iso, _, scope := newTestRuntime(t)
	options, present, err := iso.CurrentHostDefinedOptions(scope)
	if err != nil || present || options != nil {
		t.Fatalf("outside run = %v, %v, %v", options, present, err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, _, err := iso.CurrentHostDefinedOptions(scope)
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if _, _, err := (*gov8.Isolate)(nil).CurrentHostDefinedOptions(scope); err == nil {
		t.Fatal("nil isolate must fail")
	}
}

func TestScriptCompilerOriginLifecycleAndOwnership(t *testing.T) {
	isoA, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctxA, _ := isoA.NewContext()
	scopeA, _ := isoA.NewScope()
	isoB, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctxB, _ := isoB.NewContext()
	scopeB, _ := isoB.NewScope()
	nameB, _ := scopeB.NewString("foreign.js")
	_, err = ctxA.CompileScriptCompilerSource(scopeA,
		gov8.NewScriptCompilerSource("1", &gov8.ScriptCompilerOrigin{ResourceName: nameB}),
		gov8.OptNoCompileOptions, gov8.NoCacheNoReason, nil)
	if err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign origin error = %v", err)
	}
	if _, err := ctxA.CompileScriptCompilerSource(scopeB,
		gov8.NewScriptCompilerSource("1", nil), gov8.OptNoCompileOptions,
		gov8.NoCacheNoReason, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope error = %v", err)
	}
	if err := scopeB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctxB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := isoB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scopeA.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctxA.Close(); err != nil {
		t.Fatal(err)
	}
	if err := isoA.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScriptCompilerClosedOriginAndWrongThread(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := iso.NewContext()
	originScope, _ := iso.NewScope()
	name, _ := originScope.NewString("closed.js")
	if err := originScope.Close(); err != nil {
		t.Fatal(err)
	}
	scope, _ := iso.NewScope()
	source := gov8.NewScriptCompilerSource("1", &gov8.ScriptCompilerOrigin{ResourceName: name})
	if _, err := ctx.CompileScriptCompilerSource(scope, source,
		gov8.OptNoCompileOptions, gov8.NoCacheNoReason, nil); err == nil ||
		!strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("closed-origin error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := ctx.CompileScriptCompilerSource(scope,
			gov8.NewScriptCompilerSource("1", nil), gov8.OptNoCompileOptions,
			gov8.NoCacheNoReason, nil)
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if _, err := ctx.CompileScriptCompilerSource(scope, nil,
		gov8.OptNoCompileOptions, gov8.NoCacheNoReason, nil); err == nil {
		t.Fatal("nil source must fail")
	}
	_ = scope.Close()
	_ = ctx.Close()
	_ = iso.Close()
}

func TestScriptCompilerMalformedCacheBoundaries(t *testing.T) {
	const sourceText = "(function square(n) { return n * n; })(7) + 1"
	producer, producerContext, producerScope := newTestRuntime(t)
	unbound, err := producerContext.CompileUnbound(producerScope, sourceText,
		&gov8.Origin{ResourceName: "cache.js"}, gov8.OptNoCompileOptions, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := unbound.CreateCodeCache()
	if err != nil {
		t.Fatal(err)
	}
	if err := unbound.Close(); err != nil {
		t.Fatal(err)
	}
	_ = producer // cleanup is registered by newTestRuntime

	truncated := append([]byte(nil), cache[:len(cache)/2]...)
	corrupt := append([]byte(nil), cache...)
	corrupt[len(corrupt)/2] ^= 0xff
	cases := []struct {
		name         string
		cache        []byte
		wantRejected bool
	}{{"empty", []byte{}, true}, {"truncated", truncated, true}, {"midpoint corruption", corrupt, false}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, ctx, scope := newTestRuntime(t)
			source, err := gov8.NewScriptCompilerSourceWithCachedData(sourceText, nil, test.cache)
			if err != nil {
				t.Fatal(err)
			}
			script, err := ctx.CompileScriptCompilerSource(scope, source,
				gov8.OptConsumeCodeCache, gov8.NoCacheNoReason, nil)
			if err != nil {
				t.Fatal(err)
			}
			value, err := script.Run(scope, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, ok, err := value.IntegerValue(ctx)
			if err != nil || !ok || got != 50 {
				t.Fatalf("run = %d, %v, %v", got, ok, err)
			}
			if rejected := source.CachedData().Rejected; rejected != test.wantRejected {
				t.Fatalf("rejected = %v, want %v", rejected, test.wantRejected)
			}
			if err := script.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
