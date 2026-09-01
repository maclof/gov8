//go:build windows && amd64

package gov8

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func init() {
	// The root TestMain initializes V8 before benchmarks run. Enable native
	// optimization syntax only in benchmark invocations, while flags remain
	// mutable, so ordinary root tests retain their existing configuration.
	for _, argument := range os.Args {
		if strings.HasPrefix(argument, "-test.bench=") && argument != "-test.bench=" {
			if err := SetFlagsFromString("--allow-natives-syntax"); err != nil {
				panic(err)
			}
			break
		}
	}
}

func BenchmarkFastAPINativeOptimized(b *testing.B) {
	benchmarkFastAPIExecution(b, true)
}

func BenchmarkFastAPIGoSlowFallback(b *testing.B) {
	benchmarkFastAPIExecution(b, false)
}

func benchmarkFastAPIExecution(b *testing.B, useFast bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	iso, ctx, scope := fastTestRuntime(b)
	defer func() {
		if err := scope.Close(); err != nil {
			b.Error(err)
		}
		if err := ctx.Close(); err != nil {
			b.Error(err)
		}
		if err := ReleaseIsolateHostState(iso); err != nil {
			b.Error(err)
		}
		if err := iso.Close(); err != nil {
			b.Error(err)
		}
	}()

	u32 := fastInfo(b, FastTypeUint32)
	v8Value := fastInfo(b, FastTypeV8Value)
	options := fastInfo(b, FastTypeCallbackOptions)
	info, err := NewCFunctionInfo(u32, []FastTypeInfo{v8Value, u32, options}, FastInt64AsNumber)
	if err != nil {
		b.Fatal(err)
	}
	fast, err := NewCFunction(fastResidualTestAddress(b, 0), info)
	if err != nil {
		b.Fatal(err)
	}
	callback := func(_ *CallbackScope, args FunctionCallbackArguments, rv ReturnValue) {
		value, _ := args.Get(0)
		number, _, _ := value.Uint32Value(ctx)
		_ = rv.SetUint32(number + 1)
	}
	var benchmarkFunction *Function
	if useFast {
		template, err := iso.NewFastFunctionTemplate(scope, callback, nil, []CFunction{fast})
		if err != nil {
			b.Fatal(err)
		}
		benchmarkFunction, err = template.GetFunction(scope, ctx)
		if err != nil {
			b.Fatal(err)
		}
	} else {
		benchmarkFunction, err = iso.NewFunction(scope, ctx, callback, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		b.Fatal(err)
	}
	if ok, err := global.SetByName(scope, ctx, "benchTarget", benchmarkFunction.Value); err != nil || !ok {
		b.Fatalf("set benchTarget = %v, %v", ok, err)
	}
	script, err := ctx.Compile(scope, "function benchLoop(n){let value=0;for(let i=0;i<n;i++)value=benchTarget(i);return value}; benchLoop", nil)
	if err != nil {
		b.Fatal(err)
	}
	value, err := script.Run(scope, nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := script.Close(); err != nil {
		b.Fatal(err)
	}
	loop, ok, err := AsFunction(value, ctx)
	if err != nil || !ok {
		b.Fatalf("loop function = %v, %v", ok, err)
	}
	undefined, err := scope.Undefined()
	if err != nil {
		b.Fatal(err)
	}
	const callsPerIteration = 256
	count, err := scope.Number(callsPerIteration)
	if err != nil {
		b.Fatal(err)
	}
	callLoop := func() {
		if _, ok, err := loop.Call(scope, undefined, count); err != nil || !ok {
			b.Fatalf("loop call = %v, %v", ok, err)
		}
	}
	if status, _, _ := proc("gov8_fast_api_residual_reset").Call(); int64(status) < 0 {
		b.Fatalf("native reset = %d", int64(status))
	}
	warmScript, err := ctx.Compile(scope, "%PrepareFunctionForOptimization(benchLoop); benchLoop(1); %OptimizeFunctionOnNextCall(benchLoop); benchLoop(1)", nil)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := warmScript.Run(scope, nil); err != nil {
		b.Fatal(err)
	}
	if err := warmScript.Close(); err != nil {
		b.Fatal(err)
	}
	if count, _, _ := proc("gov8_fast_api_residual_counter").Call(0); useFast && count == 0 {
		b.Fatal("warm-up never selected the native fast path")
	}
	before, _, _ := proc("gov8_fast_api_residual_counter").Call(0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		callLoop()
	}
	b.StopTimer()
	after, _, _ := proc("gov8_fast_api_residual_counter").Call(0)
	if useFast {
		delta := after - before
		if delta == 0 {
			b.Fatal("measured workload never selected the native fast path")
		}
		b.ReportMetric(float64(delta)/float64(b.N*callsPerIteration), "fast/call")
	}
}
