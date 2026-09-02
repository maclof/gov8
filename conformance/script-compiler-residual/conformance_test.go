//go:build windows && amd64

package scriptcompilerresidualconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const cacheSource = "(function square(n) { return n * n; })(7) + 1"

type fixtureLine struct {
	Check string          `json:"check"`
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
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

func (r *runtime) close(t *testing.T) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Error(err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Error(err)
	}
	if err := r.iso.Close(); err != nil {
		t.Error(err)
	}
}

func loadFixtures(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-script-compiler-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	result := map[string]json.RawMessage{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Check != "" {
			if !line.OK {
				t.Fatalf("fixture %s failed", line.Check)
			}
			result[line.Check] = line.Value
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) != 7 {
		t.Fatalf("fixture check count = %d", len(result))
	}
	return result
}

func compare(t *testing.T, fixtures map[string]json.RawMessage, id string, got any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok {
		t.Fatalf("missing fixture %s", id)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ = json.Marshal(gotValue)
	wantJSON, _ := json.Marshal(wantValue)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s differs\ngot  %s\nwant %s", id, gotJSON, wantJSON)
	}
}

func mustValue(t *testing.T, value gov8.Value, err error) gov8.Value {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustString(t *testing.T, scope *gov8.Scope, value string) gov8.Value {
	t.Helper()
	v, err := scope.NewString(value)
	return mustValue(t, v, err)
}

func mustInt32(t *testing.T, scope *gov8.Scope, value int32) gov8.Value {
	t.Helper()
	v, err := scope.Int32(value)
	return mustValue(t, v, err)
}

func mustNumber(t *testing.T, scope *gov8.Scope, value float64) gov8.Value {
	t.Helper()
	v, err := scope.Number(value)
	return mustValue(t, v, err)
}

func mustUndefined(t *testing.T, scope *gov8.Scope) gov8.Value {
	t.Helper()
	v, err := scope.Undefined()
	return mustValue(t, v, err)
}

func mustRun(t *testing.T, r *runtime, source *gov8.ScriptCompilerSource, option gov8.CompileOptions, reason gov8.NoCacheReason) (int64, bool) {
	t.Helper()
	script, err := r.ctx.CompileScriptCompilerSource(r.scope, source, option, reason, nil)
	if err != nil {
		return -1, false
	}
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := value.IntegerValue(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := script.Close(); err != nil {
		t.Fatal(err)
	}
	return result, ok
}

func TestExactFixture(t *testing.T) {
	fixtures := loadFixtures(t)

	t.Run("origin arbitrary values", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		nameObj, err := r.scope.NewObject(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		name := nameObj.Value
		sourceMap := mustNumber(t, r.scope, 17.5)
		origin := &gov8.ScriptCompilerOrigin{ResourceName: name, LineOffset: math.MinInt32, ColumnOffset: math.MaxInt32, IsSharedCrossOrigin: true, ScriptID: math.MinInt32, SourceMapURL: &sourceMap, IsOpaque: true}
		value, compiled := mustRun(t, r, gov8.NewScriptCompilerSource("6 * 7", origin), gov8.OptNoCompileOptions, gov8.NoCacheNoReason)
		nameObject, _ := name.IsObject()
		mapNumber, _ := sourceMap.IsNumber()
		nameSame, _ := name.SameValue(origin.ResourceName)
		mapSame, _ := sourceMap.SameValue(*origin.SourceMapURL)
		undefined := mustUndefined(t, r.scope)
		defaultOrigin := &gov8.ScriptCompilerOrigin{ResourceName: undefined, ScriptID: math.MaxInt32}
		compare(t, fixtures, "script-compiler-residual/origin_arbitrary_values", map[string]any{
			"script_id": math.MinInt32, "resource_name_present": true, "resource_name_kind": map[bool]string{true: "object", false: "other"}[nameObject], "resource_name_same_value": nameSame,
			"source_map_present": true, "source_map_kind": map[bool]string{true: "number", false: "other"}[mapNumber], "source_map_same_value": mapSame,
			"undefined_resource_name_is_some_undefined": func() bool { v, _ := defaultOrigin.ResourceName.IsUndefined(); return v }(), "absent_source_map_is_none": defaultOrigin.SourceMapURL == nil,
			"maximum_script_id": defaultOrigin.ScriptID, "compiled": compiled, "run_value": value,
		})
	})

	t.Run("host defined options", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		var seen bool
		var length, first int64 = -1, -1
		var callbackErr error
		fn, err := r.iso.NewFunction(r.scope, r.ctx, func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			options, present, err := cs.Isolate().CurrentHostDefinedOptions(cs.Scope())
			if err != nil || !present {
				callbackErr = err
				return
			}
			seen = true
			n, err := options.Length()
			if err != nil {
				callbackErr = err
				return
			}
			length = int64(n)
			v, ok, err := options.Get(cs.Scope(), 0)
			if err != nil || !ok {
				callbackErr = err
				return
			}
			first, _, callbackErr = cs.IntegerValue(v)
			if callbackErr == nil {
				callbackErr = rv.SetInt32(int32(first))
			}
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		global, _ := r.ctx.GlobalObject(r.scope)
		if ok, err := global.SetByName(r.scope, r.ctx, "observeHostOptions", fn.Value); err != nil || !ok {
			t.Fatalf("set callback = %v, %v", ok, err)
		}
		options, _ := gov8.NewPrimitiveArray(r.scope, 2)
		if ok, err := options.Set(r.scope, 0, mustInt32(t, r.scope, 73)); err != nil || !ok {
			t.Fatal(err)
		}
		if ok, err := options.Set(r.scope, 1, mustString(t, r.scope, "meta")); err != nil || !ok {
			t.Fatal(err)
		}
		name := mustString(t, r.scope, "host-options.js")
		origin := &gov8.ScriptCompilerOrigin{ResourceName: name, ScriptID: 123, HostDefinedOptions: options}
		result, compiled := mustRun(t, r, gov8.NewScriptCompilerSource("observeHostOptions()", origin), gov8.OptEagerCompile, gov8.NoCacheNoReason)
		if callbackErr != nil {
			t.Fatal(callbackErr)
		}
		_, outside, err := r.iso.CurrentHostDefinedOptions(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "script-compiler-residual/host_defined_options", map[string]any{
			"callback_seen": seen, "length": length, "first_value": first, "run_value": result, "outside_run_absent": !outside && compiled,
		})
	})

	t.Run("compile options", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		cases := []struct {
			name string
			opt  gov8.CompileOptions
		}{{"none", gov8.OptNoCompileOptions}, {"eager", gov8.OptEagerCompile}, {"produce_hints", gov8.OptProduceCompileHints}, {"consume_hints", gov8.OptConsumeCompileHints}, {"follow_magic_comment", gov8.OptFollowCompileHintsMagicComment}, {"follow_per_function_magic_comment", gov8.OptFollowCompileHintsPerFunctionMagicComment}}
		out := make([]map[string]any, 0, len(cases))
		for _, c := range cases {
			source := gov8.NewScriptCompilerSource("function hinted(a) { return a + 1; } hinted(41)", nil)
			value, compiled := mustRun(t, r, source, c.opt, gov8.NoCacheNoReason)
			out = append(out, map[string]any{"name": c.name, "bits": c.opt, "compiled": compiled, "run_value": value, "cached_data_absent": !source.CachedData().Present})
		}
		compare(t, fixtures, "script-compiler-residual/compile_options", out)
	})

	t.Run("no cache reasons", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		names := []string{"NoReason", "BecauseCachingDisabled", "BecauseNoResource", "BecauseInlineScript", "BecauseModule", "BecauseStreamingSource", "BecauseInspector", "BecauseScriptTooSmall", "BecauseCacheTooCold", "BecauseV8Extension", "BecauseExtensionModule", "BecausePacScript", "BecauseInDocumentWrite", "BecauseResourceWithNoCacheHandler", "BecauseDeferredProduceCodeCache"}
		out := make([]map[string]any, 0, len(names))
		for i, name := range names {
			value, compiled := mustRun(t, r, gov8.NewScriptCompilerSource("20 + 22", nil), gov8.OptNoCompileOptions, gov8.NoCacheReason(i))
			out = append(out, map[string]any{"name": name, "discriminant": i, "compiled": compiled, "run_value": value})
		}
		compare(t, fixtures, "script-compiler-residual/no_cache_reasons", out)
	})

	cache := produceCache(t)
	t.Run("cache origin and source mismatch", func(t *testing.T) {
		type config struct {
			name             string
			line, column, id int32
			smap             string
			shared, opaque   bool
			absent           bool
		}
		base := config{name: "cache-base.js", line: 3, column: 4, id: 101, smap: "base.map"}
		cases := []struct {
			name, source string
			config       config
		}{
			{"same", cacheSource, base}, {"resource_name", cacheSource, config{name: "other.js", line: 3, column: 4, id: 101, smap: "base.map"}},
			{"line", cacheSource, config{name: base.name, line: 30, column: 4, id: 101, smap: base.smap}}, {"column", cacheSource, config{name: base.name, line: 3, column: 40, id: 101, smap: base.smap}},
			{"script_id", cacheSource, config{name: base.name, line: 3, column: 4, id: 202, smap: base.smap}}, {"source_map", cacheSource, config{name: base.name, line: 3, column: 4, id: 101, smap: "other.map"}},
			{"origin_flags", cacheSource, config{name: base.name, line: 3, column: 4, id: 101, smap: base.smap, shared: true, opaque: true}}, {"no_origin", cacheSource, config{absent: true}}, {"changed_source", "40 + 3", base},
		}
		out := make([]map[string]any, 0, len(cases))
		for _, c := range cases {
			r := newRuntime(t)
			var origin *gov8.ScriptCompilerOrigin
			if !c.config.absent {
				name := mustString(t, r.scope, c.config.name)
				smap := mustString(t, r.scope, c.config.smap)
				origin = &gov8.ScriptCompilerOrigin{ResourceName: name, LineOffset: c.config.line, ColumnOffset: c.config.column, ScriptID: c.config.id, SourceMapURL: &smap, IsSharedCrossOrigin: c.config.shared, IsOpaque: c.config.opaque}
			}
			source, err := gov8.NewScriptCompilerSourceWithCachedData(c.source, origin, cache)
			if err != nil {
				t.Fatal(err)
			}
			before := source.CachedData()
			value, compiled := mustRun(t, r, source, gov8.OptConsumeCodeCache, gov8.NoCacheNoReason)
			after := source.CachedData()
			out = append(out, map[string]any{"case": c.name, "result": map[string]any{"input_len_positive": len(before.Bytes) > 0, "before_present": before.Present, "before_rejected": before.Rejected, "compiled": compiled, "after_rejected": after.Rejected, "bytes_preserved": bytes.Equal(after.Bytes, cache), "run_value": value}})
			r.close(t)
		}
		compare(t, fixtures, "script-compiler-residual/cache_origin_source_mismatch", map[string]any{"cache_produced": len(cache) > 0, "cases": out})
	})

	t.Run("syntax failure", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		name := mustString(t, r.scope, "syntax.js")
		smap := mustString(t, r.scope, "syntax.map")
		source := gov8.NewScriptCompilerSource("let = ;", &gov8.ScriptCompilerOrigin{ResourceName: name, LineOffset: 10, ColumnOffset: 20, ScriptID: 909, SourceMapURL: &smap})
		before := source.CachedData()
		tc, _ := r.iso.NewTryCatch()
		defer func() { _ = tc.Close() }()
		script, err := r.ctx.CompileScriptCompilerSource(r.scope, source, gov8.OptEagerCompile, gov8.NoCacheBecauseNoResource, tc)
		hasCaught, _ := tc.HasCaught()
		exception, _ := tc.ExceptionText(r.scope, r.ctx)
		message, present, _ := tc.Message(r.scope)
		resource, line, column := "", int32(-1), int64(-1)
		if present {
			resource, _ = message.ResourceName(r.ctx)
			line, _, _ = message.LineNumber(r.ctx)
			column, _ = message.StartColumn()
		}
		compare(t, fixtures, "script-compiler-residual/syntax_failure_source_state", map[string]any{"compile_none": script == nil && err != nil, "has_caught": hasCaught, "exception": exception, "resource_name": resource, "line_number": line, "start_column": column, "cache_absent_before": !before.Present, "cache_absent_after": !source.CachedData().Present})
	})

	t.Run("permissive boundaries", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		unknown := gov8.CompileOptions(1 << 20)
		unknownValue, unknownCompiled := mustRun(t, r, gov8.NewScriptCompilerSource("40 + 2", nil), unknown, gov8.NoCacheNoReason)
		name := mustString(t, r.scope, "wasm-marked-classic.js")
		wasmValue, wasmCompiled := mustRun(t, r, gov8.NewScriptCompilerSource("6 * 7", &gov8.ScriptCompilerOrigin{ResourceName: name, IsWasm: true}), gov8.OptNoCompileOptions, gov8.NoCacheNoReason)
		compare(t, fixtures, "script-compiler-residual/permissive_boundaries", map[string]any{"unknown_option_bits": unknown, "unknown_option_compiled": unknownCompiled, "unknown_option_run_value": unknownValue, "wasm_marked_classic_compiled": wasmCompiled, "wasm_marked_classic_run_value": wasmValue})
	})
}

func produceCache(t *testing.T) []byte {
	t.Helper()
	r := newRuntime(t)
	defer r.close(t)
	unbound, err := r.ctx.CompileUnbound(r.scope, cacheSource, &gov8.Origin{ResourceName: "cache-base.js", LineOffset: 3, ColumnOffset: 4, ScriptID: 101, SourceMapURL: "base.map"}, gov8.OptNoCompileOptions, nil)
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
	return cache
}
