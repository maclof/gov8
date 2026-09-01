//go:build windows && amd64

package modulecacheconformance

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const code = "export const answer = 42;\n//# sourceURL=virtual.mjs\n//# sourceMappingURL=virtual.map"

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type fixtureLine struct {
	Check string         `json:"check"`
	OK    bool           `json:"ok"`
	Value map[string]any `json:"value"`
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-module-cache-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checked-in Rust module-cache fixture is missing: %s", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result := map[string]fixtureLine{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err == nil && line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok || !want.OK {
		t.Fatalf("missing or failed fixture check %s", id)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want.Value)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
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

func (r *runtime) close() {
	_ = r.scope.Close()
	_ = r.ctx.Close()
	_ = r.iso.Close()
}

func (r *runtime) compile(t *testing.T, source, resource string, cache *gov8.ModuleCodeCache) (*gov8.Module, bool) {
	t.Helper()
	module, rejected, err := r.ctx.CompileModuleCached(r.scope, source,
		gov8.ModuleCompileOptions{ResourceName: resource}, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	return module, rejected
}

func produce(t *testing.T, resource string) *gov8.ModuleCodeCache {
	t.Helper()
	r := newRuntime(t)
	module, _ := r.compile(t, code, resource, nil)
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
	r.close()
	return cache
}

func answer(t *testing.T, r *runtime, module *gov8.Module) (linked, evaluated bool, value int64) {
	t.Helper()
	linked, err := module.Instantiate(r.scope, func(gov8.ModuleResolveRequest) (*gov8.Module, error) { return nil, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.Evaluate(r.scope, nil); err == nil {
		evaluated = true
	} else {
		t.Fatal(err)
	}
	_ = r.iso.PerformMicrotaskCheckpoint()
	namespace, err := module.Namespace(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	object, err := gov8.AsObject(namespace)
	if err != nil {
		t.Fatal(err)
	}
	answer, ok, err := object.GetByName(r.scope, r.ctx, "answer")
	if err != nil || !ok {
		t.Fatalf("namespace answer = %v, %v", ok, err)
	}
	value, ok, err = answer.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("answer = %v, %v", ok, err)
	}
	return
}

func TestRustOracleFixture(t *testing.T) {
	fixtures := fixture(t)
	t.Run("metadata_and_repeated_cache", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close()
		module, _ := r.compile(t, code, "origin.mjs", nil)
		defer module.Close()
		unbound, err := module.GetUnboundModuleScript()
		if err != nil {
			t.Fatal(err)
		}
		defer unbound.Close()
		sourceURL, _ := unbound.SourceURL(r.scope)
		mappingURL, _ := unbound.SourceMappingURL(r.scope)
		sourceText, _ := sourceURL.StringValue()
		mappingText, _ := mappingURL.StringValue()
		first, err := unbound.CreateCodeCache()
		if err != nil {
			t.Fatal(err)
		}
		second, err := unbound.CreateCodeCache()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "module-cache/unbound_metadata_and_repeated_cache", map[string]any{
			"source_url": sourceText, "source_mapping_url": mappingText,
			"cache_non_empty": first.Len() > 0, "repeated_same_length": first.Len() == second.Len(),
			"repeated_same_bytes": first.Equal(second),
		})
	})

	t.Run("cross_isolate_roundtrip", func(t *testing.T) {
		cache := produce(t, "origin.mjs")
		r := newRuntime(t)
		defer r.close()
		module, rejected := r.compile(t, code, "origin.mjs", cache)
		defer module.Close()
		linked, evaluated, got := answer(t, r, module)
		compare(t, fixtures, "module-cache/cross_isolate_roundtrip", map[string]any{
			"producer_dropped": true, "cache_rejected": rejected, "linked": linked,
			"evaluated": evaluated, "answer": got,
		})
	})

	t.Run("changed_origin", func(t *testing.T) {
		cache := produce(t, "first.mjs")
		r := newRuntime(t)
		defer r.close()
		module, rejected := r.compile(t, code, "second.mjs", cache)
		defer module.Close()
		unbound, err := module.GetUnboundModuleScript()
		if err != nil {
			t.Fatal(err)
		}
		defer unbound.Close()
		value, err := unbound.SourceURL(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.StringValue()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "module-cache/changed_origin", map[string]any{"cache_rejected": rejected, "source_url": text})
	})
}
