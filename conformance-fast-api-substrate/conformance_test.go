//go:build windows && amd64

package fastapisubstrateconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	gov8 "github.com/maclof/gov8"
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
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-fast-api-substrate-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	result := make(map[string]fixtureLine)
	scanner := bufio.NewScanner(f)
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
	if len(result) != 4 {
		t.Fatalf("fixture count = %d, want 4", len(result))
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

func testProc(t *testing.T, name string) *syscall.Proc {
	t.Helper()
	testOnce.Do(func() {
		var path string
		if path = os.Getenv("GOV8_SHIM_DLL"); path == "" {
			dir, err := os.Getwd()
			if err != nil {
				testErr = err
				return
			}
			for range 8 {
				candidate := filepath.Join(dir, "build", "shim", "gov8_shim.dll")
				if _, err := os.Stat(candidate); err == nil {
					path = candidate
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		}
		if path == "" {
			testErr = os.ErrNotExist
			return
		}
		testDLL, testErr = syscall.LoadDLL(path)
	})
	if testErr != nil {
		t.Fatal(testErr)
	}
	p, err := testDLL.FindProc(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func nativeAddress(t *testing.T, kind uintptr) uintptr {
	t.Helper()
	address, _, _ := testProc(t, "gov8_fast_api_test_address").Call(kind)
	if address == 0 {
		t.Fatal("zero native fast address")
	}
	return address
}

func resetCounters(t *testing.T) {
	t.Helper()
	status, _, _ := testProc(t, "gov8_fast_api_test_reset_counters").Call()
	if int64(status) < 0 {
		t.Fatalf("counter reset status %d", int64(status))
	}
}

func nativeCounter(t *testing.T, kind uintptr) int {
	t.Helper()
	count, _, _ := testProc(t, "gov8_fast_api_test_counter").Call(kind)
	return int(count)
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

func typeInfo(t *testing.T, kind gov8.FastType) gov8.FastTypeInfo {
	t.Helper()
	info, err := kind.Info()
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func cFunction(t *testing.T, addressKind uintptr, args ...gov8.FastType) gov8.CFunction {
	t.Helper()
	argumentInfo := make([]gov8.FastTypeInfo, len(args))
	for index, kind := range args {
		argumentInfo[index] = typeInfo(t, kind)
	}
	info, err := gov8.NewCFunctionInfo(typeInfo(t, gov8.FastTypeUint32), argumentInfo, gov8.FastInt64AsNumber)
	if err != nil {
		t.Fatal(err)
	}
	function, err := gov8.NewCFunction(nativeAddress(t, addressKind), info)
	if err != nil {
		t.Fatal(err)
	}
	return function
}

func (r *runtime) eval(t *testing.T, source string) gov8.Value {
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

func (r *runtime) integer(t *testing.T, source string) int64 {
	t.Helper()
	value, ok, err := r.eval(t, source).IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("integer = %d, %v, %v", value, ok, err)
	}
	return value
}

func (r *runtime) text(t *testing.T, source string) string {
	t.Helper()
	value, err := r.eval(t, source).ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (r *runtime) setFunction(t *testing.T, name string, function *gov8.Function) {
	t.Helper()
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, name, function.Value); err != nil || !ok {
		t.Fatalf("set %s = %v, %v", name, ok, err)
	}
}

func calls(slow int, fast ...int) map[string]any {
	return map[string]any{"slow": slow, "fast": fast}
}

func TestRustOracleFixture(t *testing.T) {
	fixtures := fixture(t)

	t.Run("native_descriptor_metadata", func(t *testing.T) {
		types := []struct {
			name string
			kind gov8.FastType
		}{
			{"Void", gov8.FastTypeVoid}, {"Bool", gov8.FastTypeBool}, {"Uint8", gov8.FastTypeUint8},
			{"Int32", gov8.FastTypeInt32}, {"Uint32", gov8.FastTypeUint32}, {"Int64", gov8.FastTypeInt64},
			{"Uint64", gov8.FastTypeUint64}, {"Float32", gov8.FastTypeFloat32}, {"Float64", gov8.FastTypeFloat64},
			{"Pointer", gov8.FastTypePointer}, {"V8Value", gov8.FastTypeV8Value},
			{"SeqOneByteString", gov8.FastTypeSeqOneByteString}, {"ApiObject", gov8.FastTypeAPIObject},
			{"Any", gov8.FastTypeAny}, {"CallbackOptions", gov8.FastTypeCallbackOptions},
		}
		typeValues := make([]any, len(types))
		for index, item := range types {
			typeValues[index] = map[string]any{"name": item.name, "discriminant": uint8(item.kind)}
		}
		flags := []struct {
			name string
			flag gov8.FastTypeFlags
		}{
			{"AllowShared", gov8.FastTypeAllowShared}, {"EnforceRange", gov8.FastTypeEnforceRange},
			{"Clamp", gov8.FastTypeClamp}, {"IsRestricted", gov8.FastTypeIsRestricted},
		}
		flagValues := make([]any, len(flags))
		for index, item := range flags {
			roundTrip, ok := gov8.FastTypeFlagsFromBits(uint8(item.flag))
			flagValues[index] = map[string]any{"name": item.name, "bits": uint8(item.flag), "round_trip": ok && roundTrip == item.flag}
		}
		info, err := gov8.NewCFunctionInfo(typeInfo(t, gov8.FastTypeUint32), []gov8.FastTypeInfo{
			typeInfo(t, gov8.FastTypeV8Value), typeInfo(t, gov8.FastTypeUint32), typeInfo(t, gov8.FastTypeUint32),
		}, gov8.FastInt64AsNumber)
		if err != nil {
			t.Fatal(err)
		}
		address := nativeAddress(t, 0)
		descriptor, err := gov8.NewCFunction(address, info)
		if err != nil {
			t.Fatal(err)
		}
		descriptorCopy := descriptor
		layout := func(kind uintptr) uintptr {
			value, _, _ := testProc(t, "gov8_fast_api_test_layout").Call(kind)
			return value
		}
		_, unknownRejected := gov8.FastTypeFlagsFromBits(0x10)
		compare(t, fixtures, "fast-api-substrate/native_descriptor_metadata", map[string]any{
			"types": typeValues,
			"int64_representations": []any{
				map[string]any{"name": "Number", "discriminant": uint8(gov8.FastInt64AsNumber)},
				map[string]any{"name": "BigInt", "discriminant": uint8(gov8.FastInt64AsBigInt)},
			},
			"flags": flagValues, "empty_flag_bits": 0, "all_flag_bits": 15,
			"unknown_flag_rejected":  !unknownRejected,
			"unknown_flag_truncated": uint8(gov8.FastTypeFlagsFromBitsTruncated(0x1f)),
			"layout": map[string]any{
				"type_size": unsafe.Sizeof(gov8.FastType(0)), "flags_size": unsafe.Sizeof(gov8.FastTypeFlags(0)),
				"ctype_info_size": layout(0), "cfunction_info_size": layout(1),
				"cfunction_size": layout(2), "cfunction_align": layout(3),
			},
			"address_identity":          descriptor.Address() == address,
			"copied_address_identity":   descriptorCopy.Address() == descriptor.Address(),
			"copied_type_info_identity": descriptorCopy.TypeInfo() == descriptor.TypeInfo(),
		})
	})

	t.Run("single_overload_execution_and_lifetime", func(t *testing.T) {
		resetCounters(t)
		slowCalls := 0
		r := newRuntime(t)
		fast := cFunction(t, 0, gov8.FastTypeV8Value, gov8.FastTypeUint32, gov8.FastTypeUint32)
		template, err := r.iso.FunctionBuilder(func(_ *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			slowCalls++
			a, b := uint32(0), uint32(0)
			if value, err := args.Get(0); err == nil {
				a, _, _ = value.Uint32Value(r.ctx)
			}
			if value, err := args.Get(1); err == nil {
				b, _, _ = value.Uint32Value(r.ctx)
			}
			_ = rv.SetUint32(a + b)
		}).BuildFast(r.scope, []gov8.CFunction{fast})
		if err != nil {
			t.Fatal(err)
		}
		function, err := template.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		global, err := gov8.NewGlobal(r.scope, function.Value)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.scope.Close(); err != nil {
			t.Fatal(err)
		}
		r.scope, err = r.iso.NewScope()
		if err != nil {
			t.Fatal(err)
		}
		value, err := global.ToLocal(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		function, ok, err := gov8.AsFunction(value, r.ctx)
		if err != nil || !ok {
			t.Fatalf("reopen function = %v, %v", ok, err)
		}
		r.setFunction(t, "fastAdd", function)
		cold := r.integer(t, "function addWrap(a,b){return fastAdd(a,b)}; %PrepareFunctionForOptimization(addWrap); addWrap(19,23)")
		afterCold := calls(slowCalls, nativeCounter(t, 0))
		optimized := r.integer(t, "%OptimizeFunctionOnNextCall(addWrap); addWrap(20,22)")
		afterOptimized := calls(slowCalls, nativeCounter(t, 0))
		incompatible := r.integer(t, "addWrap('19',23)")
		afterIncompatible := calls(slowCalls, nativeCounter(t, 0))
		compare(t, fixtures, "fast-api-substrate/single_overload_execution_and_lifetime", map[string]any{
			"creation_handle_scope_closed": true, "cold_result": cold, "after_cold": afterCold,
			"optimized_result": optimized, "after_optimized": afterOptimized,
			"incompatible_result": incompatible, "after_incompatible": afterIncompatible,
		})
		if err := global.Close(); err != nil {
			t.Fatal(err)
		}
		r.close(t)
	})

	t.Run("two_overload_arity_and_fallback", func(t *testing.T) {
		resetCounters(t)
		slowCalls := 0
		r := newRuntime(t)
		defer r.close(t)
		one := cFunction(t, 1, gov8.FastTypeV8Value, gov8.FastTypeUint32)
		two := cFunction(t, 2, gov8.FastTypeV8Value, gov8.FastTypeUint32, gov8.FastTypeUint32)
		template, err := r.iso.FunctionBuilder(func(_ *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			slowCalls++
			_ = rv.SetInt32(int32(900 + args.Length()))
		}).BuildFast(r.scope, []gov8.CFunction{one, two})
		if err != nil {
			t.Fatal(err)
		}
		function, err := template.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		r.setFunction(t, "overloaded", function)
		cold := r.text(t, "function f0(){return overloaded()} function f1(a){return overloaded(a)} function f2(a,b){return overloaded(a,b)} function f3(a,b,c){return overloaded(a,b,c)} %PrepareFunctionForOptimization(f0); %PrepareFunctionForOptimization(f1); %PrepareFunctionForOptimization(f2); %PrepareFunctionForOptimization(f3); [f0(),f1(1),f2(1,2),f3(1,2,3)].join(',')")
		afterCold := calls(slowCalls, nativeCounter(t, 1), nativeCounter(t, 2))
		cases := []struct{ name, source string }{
			{"one_arg", "%OptimizeFunctionOnNextCall(f1); f1(1)"},
			{"two_args", "%OptimizeFunctionOnNextCall(f2); f2(1,2)"},
			{"zero_args", "%OptimizeFunctionOnNextCall(f0); f0()"},
			{"three_args", "%OptimizeFunctionOnNextCall(f3); f3(1,2,3)"},
			{"type_mismatch", "f1('x')"},
		}
		observations := make([]any, 0, len(cases))
		for _, item := range cases {
			observations = append(observations, map[string]any{
				"case": item.name, "result": r.integer(t, item.source),
				"calls": calls(slowCalls, nativeCounter(t, 1), nativeCounter(t, 2)),
			})
		}
		compare(t, fixtures, "fast-api-substrate/two_overload_arity_and_fallback", map[string]any{
			"cold_results": cold, "after_cold": afterCold, "optimized_cases": observations,
		})
	})

	t.Run("empty_overloads_safe_boundary", func(t *testing.T) {
		slowCalls := 0
		r := newRuntime(t)
		defer r.close(t)
		template, err := r.iso.FunctionBuilder(func(_ *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			slowCalls++
			_ = rv.SetInt32(int32(700 + args.Length()))
		}).BuildFast(r.scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		function, err := template.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		r.setFunction(t, "emptyFast", function)
		compare(t, fixtures, "fast-api-substrate/empty_overloads_safe_boundary", map[string]any{
			"built": true, "direct_result": r.integer(t, "emptyFast(1,2)"),
			"slow_calls":       slowCalls,
			"construct_result": r.text(t, "try { new emptyFast(); 'constructed' } catch (e) { e.name + ':' + e.message }"),
		})
	})
}
