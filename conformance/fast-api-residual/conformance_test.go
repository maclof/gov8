//go:build windows && amd64

package fastapiresidualconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	gov8 "github.com/maclof/gov8"
	"github.com/maclof/gov8/internal/prebuilt"
)

type fixtureLine struct {
	Check string         `json:"check"`
	OK    bool           `json:"ok"`
	Value map[string]any `json:"value"`
}

var (
	testDLL  *syscall.DLL
	testOnce sync.Once
	testErr  error
)

func TestMain(m *testing.M) {
	if err := gov8.SetFlagsFromString("--allow-natives-syntax"); err != nil {
		panic(err)
	}
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-fast-api-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	result := make(map[string]fixtureLine)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) != 8 {
		t.Fatalf("fixture count = %d, want 8", len(result))
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok || !want.OK {
		t.Fatalf("missing/failed fixture %s", id)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

func testProc(t testing.TB, name string) *syscall.Proc {
	t.Helper()
	testOnce.Do(func() {
		path := os.Getenv("GOV8_SHIM_DLL")
		if path == "" {
			path, testErr = prebuilt.Path()
			if testErr != nil {
				return
			}
		}
		testDLL, testErr = syscall.LoadDLL(path)
	})
	if testErr != nil {
		t.Fatal(testErr)
	}
	proc, err := testDLL.FindProc(name)
	if err != nil {
		t.Fatal(err)
	}
	return proc
}

func nativeCall(t testing.TB, name string, args ...uintptr) uintptr {
	t.Helper()
	result, _, _ := testProc(t, name).Call(args...)
	return result
}

func nativeAddress(t testing.TB, kind uintptr) uintptr {
	t.Helper()
	address := nativeCall(t, "gov8_fast_api_residual_address", kind)
	if address == 0 {
		t.Fatalf("native address %d is zero", kind)
	}
	return address
}

func resetNative(t testing.TB) {
	t.Helper()
	if status := int64(nativeCall(t, "gov8_fast_api_residual_reset")); status < 0 {
		t.Fatalf("reset status = %d", status)
	}
}

func counter(t testing.TB, kind uintptr) int {
	t.Helper()
	return int(nativeCall(t, "gov8_fast_api_residual_counter", kind))
}

type runtimeState struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t testing.TB) *runtimeState {
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
	return &runtimeState{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtimeState) close(t testing.TB) {
	t.Helper()
	if r.scope != nil {
		if err := r.scope.Close(); err != nil {
			t.Error(err)
		}
	}
	if err := r.ctx.Close(); err != nil {
		t.Error(err)
	}
	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		t.Error(err)
	}
	if err := r.iso.Close(); err != nil {
		t.Error(err)
	}
}

func (r *runtimeState) eval(t testing.TB, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (r *runtimeState) text(t testing.TB, source string) string {
	t.Helper()
	text, err := r.eval(t, source).ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func (r *runtimeState) setFunction(t testing.TB, name string, function *gov8.Function) {
	t.Helper()
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, name, function.Value); err != nil || !ok {
		t.Fatalf("set %s = %v, %v", name, ok, err)
	}
}

func typeInfo(t testing.TB, kind gov8.FastType, flags gov8.FastTypeFlags) gov8.FastTypeInfo {
	t.Helper()
	info, err := gov8.NewFastTypeInfo(kind, flags)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func nativeFunction(t testing.TB, addressKind uintptr, returnType gov8.FastTypeInfo, arguments ...gov8.FastTypeInfo) gov8.CFunction {
	t.Helper()
	info, err := gov8.NewCFunctionInfo(returnType, arguments, gov8.FastInt64AsNumber)
	if err != nil {
		t.Fatal(err)
	}
	function, err := gov8.NewCFunction(nativeAddress(t, addressKind), info)
	if err != nil {
		t.Fatal(err)
	}
	return function
}

func (r *runtimeState) buildFunction(t testing.TB, slow gov8.FunctionCallback, opts *gov8.FunctionOptions, fast gov8.CFunction) *gov8.Function {
	t.Helper()
	template, err := r.iso.NewFastFunctionTemplate(r.scope, slow, opts, []gov8.CFunction{fast})
	if err != nil {
		t.Fatal(err)
	}
	function, err := template.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return function
}

func TestRustOracleFixture(t *testing.T) {
	fixtures := fixture(t)
	u32 := typeInfo(t, gov8.FastTypeUint32, 0)
	v8Value := typeInfo(t, gov8.FastTypeV8Value, 0)

	t.Run("pin_and_public_surface", func(t *testing.T) {
		options := typeInfo(t, gov8.FastTypeCallbackOptions, 0)
		info, err := gov8.NewCFunctionInfo(u32, []gov8.FastTypeInfo{v8Value, u32, options}, gov8.FastInt64AsNumber)
		if err != nil {
			t.Fatal(err)
		}
		version, err := gov8.RuntimeVersionString()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "fast-api-residual/pin_and_public_surface", map[string]any{
			"crate": "v8=152.2.0", "v8": version, "target": "x86_64-pc-windows-msvc",
			"callback_options_size":        nativeCall(t, "gov8_fast_api_residual_layout", 0),
			"callback_options_align":       nativeCall(t, "gov8_fast_api_residual_layout", 1),
			"callback_options_has_isolate": true, "callback_options_has_data": true,
			"callback_options_has_fallback":  false,
			"supported_callback_abis":        []any{"mutable_pointer", "shared_reference"},
			"options_excluded_from_js_arity": info.ArgumentCount() == 2 && info.HasOptions(),
		})
	})

	t.Run("callback_options", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		options := typeInfo(t, gov8.FastTypeCallbackOptions, 0)
		args := []gov8.FastTypeInfo{v8Value, u32, options}
		slowCalls := 0
		slow := func(_ *gov8.CallbackScope, values gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			slowCalls++
			value, _ := values.Get(0)
			number, _, _ := value.Uint32Value(r.ctx)
			_ = rv.SetUint32(number + 1000)
		}
		external, err := r.scope.NewExternal(nativeCall(t, "gov8_fast_api_residual_external_sentinel"))
		if err != nil {
			t.Fatal(err)
		}
		withData := r.buildFunction(t, slow, &gov8.FunctionOptions{Data: external}, nativeFunction(t, 0, u32, args...))
		withoutData := r.buildFunction(t, slow, nil, nativeFunction(t, 1, u32, args...))
		withGlobal, err := gov8.NewGlobal(r.scope, withData.Value)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = withGlobal.Close() }()
		withoutGlobal, err := gov8.NewGlobal(r.scope, withoutData.Value)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = withoutGlobal.Close() }()
		if err := r.scope.Close(); err != nil {
			t.Fatal(err)
		}
		r.scope, err = r.iso.NewScope()
		if err != nil {
			t.Fatal(err)
		}
		local, err := withGlobal.ToLocal(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		withData, ok, err := gov8.AsFunction(local, r.ctx)
		if err != nil || !ok {
			t.Fatalf("withData local = %v, %v", ok, err)
		}
		r.setFunction(t, "withData", withData)
		resetNative(t)
		slowCalls = 0
		results := r.text(t, "function od(x){return withData(x)}; %PrepareFunctionForOptimization(od); const cold=od(10); %OptimizeFunctionOnNextCall(od); const fast=od(20); [cold,fast].join(',')")
		observations := nativeCall(t, "gov8_fast_api_residual_observations")
		compare(t, fixtures, "fast-api-residual/options/external_data_and_callback_scope", map[string]any{
			"creation_handle_scope_closed": true, "results": results,
			"slow_calls": slowCalls, "fast_calls": counter(t, 0),
			"data_is_external": (observations & 1) != 0, "external_pointer_matches": (observations & 2) != 0,
			"unchecked_isolate_accessors_match": (observations & 4) != 0,
			"callback_scope_has_context":        (observations & 8) != 0,
		})

		local, err = withoutGlobal.ToLocal(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		withoutData, ok, err = gov8.AsFunction(local, r.ctx)
		if err != nil || !ok {
			t.Fatalf("withoutData local = %v, %v", ok, err)
		}
		r.setFunction(t, "withoutData", withoutData)
		resetNative(t)
		slowCalls = 0
		results = r.text(t, "function ou(x){return withoutData(x)}; %PrepareFunctionForOptimization(ou); ou(1); %OptimizeFunctionOnNextCall(ou); const fast2=ou(2); const mismatch2=ou('x'); [fast2,mismatch2].join(',')")
		observations = nativeCall(t, "gov8_fast_api_residual_observations")
		compare(t, fixtures, "fast-api-residual/options/undefined_data_and_type_fallback", map[string]any{
			"results": results, "data_is_undefined": (observations & 16) != 0,
			"callback_abi": "shared_reference", "slow_calls": slowCalls, "fast_calls": counter(t, 0),
		})
	})

	t.Run("one_byte", func(t *testing.T) {
		direct := make([]any, 4)
		for index := range direct {
			direct[index] = nativeCall(t, "gov8_fast_api_residual_direct_byte", uintptr(index))
		}
		compare(t, fixtures, "fast-api-residual/one_byte/direct_as_bytes_boundaries", map[string]any{
			"direct_bytes":     direct,
			"null_zero_len":    nativeCall(t, "gov8_fast_api_residual_null_length", 0),
			"null_nonzero_len": nativeCall(t, "gov8_fast_api_residual_null_length", 7),
			"layout":           map[string]any{"size": nativeCall(t, "gov8_fast_api_residual_layout", 2), "align": nativeCall(t, "gov8_fast_api_residual_layout", 3)},
		})

		resetNative(t)
		r := newRuntime(t)
		defer r.close(t)
		slowCalls := 0
		fast := nativeFunction(t, 2, u32, v8Value, typeInfo(t, gov8.FastTypeSeqOneByteString, 0))
		function := r.buildFunction(t, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			slowCalls++
			_ = rv.SetUint32(^uint32(0) - 1)
		}, nil, fast)
		r.setFunction(t, "stringBytes", function)
		results := r.text(t, `function os(x){return stringBytes(x)}; %PrepareFunctionForOptimization(os); const cold=os("hello"); %OptimizeFunctionOnNextCall(os); const ascii=os("hello"); const nulLatin=os("\0A\xFF"); const latin=os("\xFF"); const empty=os(""); const twoByte=os("é雪"); [cold,ascii,nulLatin,latin,empty,twoByte].join(',')`)
		compare(t, fixtures, "fast-api-residual/one_byte/optimized_input_matrix", map[string]any{
			"results": results, "slow_calls": slowCalls, "fast_calls": counter(t, 1),
		})
	})

	t.Run("ctype_info", func(t *testing.T) {
		pairs := []struct {
			name  string
			kind  gov8.FastType
			flags gov8.FastTypeFlags
		}{
			{"Uint8+EnforceRange", gov8.FastTypeUint8, gov8.FastTypeEnforceRange},
			{"Int32+EnforceRange", gov8.FastTypeInt32, gov8.FastTypeEnforceRange},
			{"Uint32+EnforceRange", gov8.FastTypeUint32, gov8.FastTypeEnforceRange},
			{"Int64+EnforceRange", gov8.FastTypeInt64, gov8.FastTypeEnforceRange},
			{"Uint64+EnforceRange", gov8.FastTypeUint64, gov8.FastTypeEnforceRange},
			{"Uint8+Clamp", gov8.FastTypeUint8, gov8.FastTypeClamp},
			{"Int32+Clamp", gov8.FastTypeInt32, gov8.FastTypeClamp},
			{"Uint32+Clamp", gov8.FastTypeUint32, gov8.FastTypeClamp},
			{"Int64+Clamp", gov8.FastTypeInt64, gov8.FastTypeClamp},
			{"Uint64+Clamp", gov8.FastTypeUint64, gov8.FastTypeClamp},
			{"Float32+IsRestricted", gov8.FastTypeFloat32, gov8.FastTypeIsRestricted},
			{"Float64+IsRestricted", gov8.FastTypeFloat64, gov8.FastTypeIsRestricted},
			{"V8Value+AllowShared", gov8.FastTypeV8Value, gov8.FastTypeAllowShared},
		}
		values := make([]any, len(pairs))
		for index, pair := range pairs {
			info := typeInfo(t, pair.kind, pair.flags)
			values[index] = map[string]any{"name": pair.name, "type_byte": uint8(info.Type()), "flags_byte": uint8(info.Flags()), "identifier": info.Identifier()}
		}
		compare(t, fixtures, "fast-api-residual/ctype_info/constructor_flag_matrix", map[string]any{
			"pairs": values, "rust_get_type_exposed": false, "rust_get_flags_exposed": false, "rust_get_id_exposed": false,
		})

		resetNative(t)
		r := newRuntime(t)
		defer r.close(t)
		slowCalls := 0
		slow := func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			slowCalls++
			_ = rv.SetInt32(-1000)
		}
		for _, item := range []struct {
			name        string
			addressKind uintptr
			argument    gov8.FastTypeInfo
		}{
			{"enforce", 3, typeInfo(t, gov8.FastTypeInt32, gov8.FastTypeEnforceRange)},
			{"clamp", 3, typeInfo(t, gov8.FastTypeInt32, gov8.FastTypeClamp)},
			{"restricted", 4, typeInfo(t, gov8.FastTypeFloat64, gov8.FastTypeIsRestricted)},
		} {
			function := r.buildFunction(t, slow, nil, nativeFunction(t, item.addressKind, typeInfo(t, gov8.FastTypeInt32, 0), v8Value, item.argument))
			r.setFunction(t, item.name, function)
		}
		results := r.text(t, `function e0(x){return enforce(x)} function e1(x){return enforce(x)} function e2(x){return enforce(x)} function e3(x){return enforce(x)} function c0(x){return clamp(x)} function c1(x){return clamp(x)} function c2(x){return clamp(x)} function c3(x){return clamp(x)} function r0(x){return restricted(x)} function r1(x){return restricted(x)} function r2(x){return restricted(x)} function exercise(f,value){%PrepareFunctionForOptimization(f); f(value); %OptimizeFunctionOnNextCall(f); return f(value)} [exercise(e0,42),exercise(e1,3.5),exercise(e2,2147483648),exercise(e3,NaN),exercise(c0,3.7),exercise(c1,1e100),exercise(c2,-1e100),exercise(c3,NaN),exercise(r0,1.25),exercise(r1,Infinity),exercise(r2,NaN)].join(',')`)
		compare(t, fixtures, "fast-api-residual/ctype_info/optimized_flag_semantics", map[string]any{
			"results": results, "slow_calls": slowCalls, "fast_calls": counter(t, 2),
		})
	})

	t.Run("allow_shared", func(t *testing.T) {
		resetNative(t)
		r := newRuntime(t)
		defer r.close(t)
		slowCalls := 0
		argument := typeInfo(t, gov8.FastTypeV8Value, gov8.FastTypeAllowShared)
		function := r.buildFunction(t, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			slowCalls++
			_ = rv.SetUint32(1000)
		}, nil, nativeFunction(t, 5, u32, v8Value, argument))
		r.setFunction(t, "allowShared", function)
		results := r.text(t, `function a0(x){return allowShared(x)} function a1(x){return allowShared(x)} function a2(x){return allowShared(x)} function exerciseShared(f,value){%PrepareFunctionForOptimization(f); f(value); %OptimizeFunctionOnNextCall(f); return f(value)} [exerciseShared(a0,new ArrayBuffer(4)),exerciseShared(a1,new SharedArrayBuffer(4)),exerciseShared(a2,{})].join(',')`)
		compare(t, fixtures, "fast-api-residual/ctype_info/allow_shared_v8value_semantics", map[string]any{
			"results": results, "slow_calls": slowCalls, "fast_calls": counter(t, 3), "generic_v8value_restricts_input": false,
		})
	})
}
