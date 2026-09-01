//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestCallbackInt32ArgumentMetadataBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		values    []int32
		construct bool
	}{
		{name: "argc_zero"},
		{name: "argc_one", values: []int32{math.MinInt32}},
		{name: "argc_two", values: []int32{math.MinInt32, math.MaxInt32}},
		{name: "argc_three", values: []int32{11, 22, 33}},
		{name: "constructor", values: []int32{-7, 42}, construct: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			iso, ctx, scope := newTestRuntime(t)
			arguments := make([]gov8.Value, len(tc.values))
			for i, value := range tc.values {
				var err error
				arguments[i], err = scope.Int32(value)
				if err != nil {
					t.Fatal(err)
				}
			}
			var callbackErr error
			function, err := iso.NewFunction(scope, ctx,
				func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
					if args.Length() != len(tc.values) || args.IsConstructCall() != tc.construct {
						callbackErr = fmt.Errorf("shape = argc %d construct %v", args.Length(), args.IsConstructCall())
						return
					}
					for i, want := range tc.values {
						argument, getErr := args.Get(i)
						if getErr != nil {
							callbackErr = getErr
							return
						}
						for repeat := 0; repeat < 2; repeat++ {
							got, ok, conversionErr := cs.IntegerValue(argument)
							if conversionErr != nil || !ok || got != int64(want) {
								callbackErr = fmt.Errorf("arg %d repeat %d = %d, %v, %v; want %d", i, repeat, got, ok, conversionErr, want)
								return
							}
						}
					}
					callbackErr = rv.SetInt32(17)
				}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.construct {
				if _, ok, err := function.NewInstance(scope, arguments...); err != nil || !ok {
					t.Fatalf("NewInstance = %v, %v", ok, err)
				}
			} else {
				result, ok, err := function.Call(scope, mustUndefinedT(t, scope), arguments...)
				if err != nil || !ok {
					t.Fatalf("Call = %v, %v", ok, err)
				}
				got, converted, err := result.IntegerValue(ctx)
				if err != nil || !converted || got != 17 {
					t.Fatalf("result = %d, %v, %v", got, converted, err)
				}
			}
			if callbackErr != nil {
				t.Fatal(callbackErr)
			}
		})
	}
}

func TestCallbackInt32ArgumentNonInt32ConversionsStayNative(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   int64
	}{
		{name: "negative_zero", source: "-0", want: 0},
		{name: "string", source: "'20'", want: 20},
		{name: "fraction", source: "2.75", want: 2},
		{name: "large_uint32", source: "4294967295", want: 4294967295},
	} {
		t.Run(tc.name, func(t *testing.T) {
			iso, ctx, scope := newTestRuntime(t)
			var observed [2]int64
			var callbackErr error
			function, err := iso.NewFunction(scope, ctx,
				func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
					argument, err := args.Get(0)
					if err != nil {
						callbackErr = err
						return
					}
					for i := range observed {
						var ok bool
						observed[i], ok, err = cs.IntegerValue(argument)
						if err != nil || !ok {
							callbackErr = fmt.Errorf("conversion %d = %v, %v", i, ok, err)
							return
						}
					}
					callbackErr = rv.SetInt32(1)
				}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !seedGlobal(t, ctx, scope, "__int64Twice", function.Value) {
				t.Fatal("seed function")
			}
			if got, ok := evalText(t, ctx, scope, "__int64Twice("+tc.source+")"); !ok || got != "1" {
				t.Fatalf("call = %q, %v", got, ok)
			}
			if callbackErr != nil || observed != [2]int64{tc.want, tc.want} {
				t.Fatalf("observed = %v; callback = %v", observed, callbackErr)
			}
		})
	}

	t.Run("object_side_effects_repeat", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		var observed [2]int64
		var callbackErr error
		function, err := iso.NewFunction(scope, ctx,
			func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
				argument, err := args.Get(0)
				if err != nil {
					callbackErr = err
					return
				}
				for i := range observed {
					var ok bool
					observed[i], ok, err = cs.IntegerValue(argument)
					if err != nil || !ok {
						callbackErr = fmt.Errorf("conversion %d = %v, %v", i, ok, err)
						return
					}
				}
				callbackErr = rv.SetInt32(int32(observed[1]))
			}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !seedGlobal(t, ctx, scope, "__objectTwice", function.Value) {
			t.Fatal("seed function")
		}
		source := "globalThis.__argHits=0; __objectTwice({valueOf(){ return ++globalThis.__argHits; }})"
		if got, ok := evalText(t, ctx, scope, source); !ok || got != "2" {
			t.Fatalf("call = %q, %v", got, ok)
		}
		if hits, ok := evalText(t, ctx, scope, "__argHits"); !ok || hits != "2" {
			t.Fatalf("valueOf hits = %q, %v", hits, ok)
		}
		if callbackErr != nil || observed != [2]int64{1, 2} {
			t.Fatalf("observed = %v; callback = %v", observed, callbackErr)
		}
	})
}

func TestCallbackInt32ArgumentNestedFrames(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var callbackErr error
	inner, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			left, err := callbackArgumentInteger(cs, args, 0)
			if err != nil {
				callbackErr = err
				return
			}
			right, err := callbackArgumentInteger(cs, args, 1)
			if err != nil {
				callbackErr = err
				return
			}
			callbackErr = rv.SetInt32(int32(left + right))
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			first, err := callbackArgumentInteger(cs, args, 0)
			if err != nil {
				callbackErr = err
				return
			}
			left, err := cs.Scope().Int32(20)
			if err != nil {
				callbackErr = err
				return
			}
			right, err := cs.Scope().Int32(22)
			if err != nil {
				callbackErr = err
				return
			}
			result, ok, err := inner.Call(cs.Scope(), mustUndefinedCallback(cs), left, right)
			if err != nil || !ok {
				callbackErr = fmt.Errorf("inner Call = %v, %v", ok, err)
				return
			}
			innerValue, converted, err := cs.IntegerValue(result)
			if err != nil || !converted {
				callbackErr = fmt.Errorf("inner result = %v, %v", converted, err)
				return
			}
			second, err := callbackArgumentInteger(cs, args, 1)
			if err != nil {
				callbackErr = err
				return
			}
			firstAgain, err := callbackArgumentInteger(cs, args, 0)
			if err != nil {
				callbackErr = err
				return
			}
			callbackErr = rv.SetInt32(int32(first + innerValue + second + firstAgain))
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := outer.Call(scope, mustUndefinedT(t, scope), mustInt32T(t, scope, 11), mustInt32T(t, scope, 12))
	if err != nil || !ok || callbackErr != nil {
		t.Fatalf("outer Call = %v, %v; callback = %v", ok, err, callbackErr)
	}
	got, converted, err := result.IntegerValue(ctx)
	if err != nil || !converted || got != 76 {
		t.Fatalf("result = %d, %v, %v; want 76", got, converted, err)
	}
}

func TestCallbackArgumentsWrongThreadAndRetainedFrameMethods(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	payload, err := scope.NewString("callback-data")
	if err != nil {
		t.Fatal(err)
	}
	var retainedArgs gov8.FunctionCallbackArguments
	var retainedScope *gov8.CallbackScope
	var retainedValue gov8.Value
	var wrongErrors [5]error
	var wrongLength int
	var wrongConstruct bool
	var callbackErr error
	function, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			retainedArgs, retainedScope = args, cs
			retainedValue, callbackErr = args.Get(0)
			if callbackErr != nil {
				return
			}
			if args.Length() != 1 || args.IsConstructCall() {
				callbackErr = fmt.Errorf("plain snapshots = %d, %v", args.Length(), args.IsConstructCall())
				return
			}
			if _, callbackErr = args.This(); callbackErr != nil {
				return
			}
			newTarget, err := args.NewTarget()
			if err != nil {
				callbackErr = err
				return
			}
			if undefined, err := newTarget.IsUndefined(); err != nil || !undefined {
				callbackErr = fmt.Errorf("plain NewTarget undefined = %v, %v", undefined, err)
				return
			}
			data, err := args.Data()
			if err != nil {
				callbackErr = err
				return
			}
			if same, err := data.StrictEquals(payload); err != nil || !same {
				callbackErr = fmt.Errorf("callback Data identity = %v, %v", same, err)
				return
			}
			done := make(chan struct{})
			go func() {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				wrongLength, wrongConstruct = args.Length(), args.IsConstructCall()
				_, wrongErrors[0] = args.Get(0)
				_, wrongErrors[1] = args.This()
				_, wrongErrors[2] = args.NewTarget()
				_, wrongErrors[3] = args.Data()
				_, _, wrongErrors[4] = cs.IntegerValue(retainedValue)
				close(done)
			}()
			<-done
			callbackErr = rv.SetInt32(42)
		}, &gov8.FunctionOptions{Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := function.Call(scope, mustUndefinedT(t, scope), mustInt32T(t, scope, 7))
	if err != nil || !ok || callbackErr != nil {
		t.Fatalf("Call = %v, %v; callback = %v", ok, err, callbackErr)
	}
	got, converted, err := result.IntegerValue(ctx)
	if err != nil || !converted || got != 42 {
		t.Fatalf("result = %d, %v, %v", got, converted, err)
	}
	if wrongLength != 1 || wrongConstruct {
		t.Fatalf("wrong-thread immutable snapshots = %d, %v", wrongLength, wrongConstruct)
	}
	for i, err := range wrongErrors {
		if err == nil || !strings.Contains(err.Error(), "thread affinity") {
			t.Fatalf("wrong-thread operation %d = %v", i, err)
		}
	}
	if retainedArgs.Length() != 1 || retainedArgs.IsConstructCall() {
		t.Fatalf("retained immutable snapshots = %d, %v", retainedArgs.Length(), retainedArgs.IsConstructCall())
	}
	for name, call := range map[string]func() error{
		"Get":       func() error { _, err := retainedArgs.Get(0); return err },
		"This":      func() error { _, err := retainedArgs.This(); return err },
		"NewTarget": func() error { _, err := retainedArgs.NewTarget(); return err },
		"Data":      func() error { _, err := retainedArgs.Data(); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "used after Close") {
			t.Fatalf("retained %s = %v", name, err)
		}
	}
	if _, _, err := retainedScope.IntegerValue(retainedValue); err == nil || !strings.Contains(err.Error(), "used after Close") {
		t.Fatalf("retained IntegerValue = %v", err)
	}
}

func TestCallbackArgumentsRetainedConstructorAndNestedFrames(t *testing.T) {
	t.Run("constructor", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		payload, err := scope.NewString("constructor-data")
		if err != nil {
			t.Fatal(err)
		}
		var retained gov8.FunctionCallbackArguments
		var callbackErr error
		function, err := iso.NewFunction(scope, ctx,
			func(_ *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
				retained = args
				if args.Length() != 2 || !args.IsConstructCall() {
					callbackErr = fmt.Errorf("constructor snapshots = %d, %v", args.Length(), args.IsConstructCall())
					return
				}
				oob, err := args.Get(2)
				if err != nil {
					callbackErr = err
					return
				}
				if undefined, err := oob.IsUndefined(); err != nil || !undefined {
					callbackErr = fmt.Errorf("constructor OOB undefined = %v, %v", undefined, err)
					return
				}
				if _, callbackErr = args.This(); callbackErr != nil {
					return
				}
				newTarget, err := args.NewTarget()
				if err != nil {
					callbackErr = err
					return
				}
				if isFunction, err := newTarget.IsFunction(); err != nil || !isFunction {
					callbackErr = fmt.Errorf("NewTarget function = %v, %v", isFunction, err)
					return
				}
				data, err := args.Data()
				if err != nil {
					callbackErr = err
					return
				}
				if same, err := data.StrictEquals(payload); err != nil || !same {
					callbackErr = fmt.Errorf("constructor Data identity = %v, %v", same, err)
					return
				}
				callbackErr = rv.SetInt32(9)
			}, &gov8.FunctionOptions{Data: payload})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := function.NewInstance(scope, mustInt32T(t, scope, 1), mustInt32T(t, scope, 2)); err != nil || !ok || callbackErr != nil {
			t.Fatalf("NewInstance = %v, %v; callback = %v", ok, err, callbackErr)
		}
		if retained.Length() != 2 || !retained.IsConstructCall() {
			t.Fatalf("retained constructor snapshots = %d, %v", retained.Length(), retained.IsConstructCall())
		}
		if _, err := retained.NewTarget(); err == nil || !strings.Contains(err.Error(), "used after Close") {
			t.Fatalf("retained constructor NewTarget = %v", err)
		}
	})

	t.Run("nested_outer_inner", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		var outerRetained, innerRetained gov8.FunctionCallbackArguments
		var callbackErr error
		inner, err := iso.NewFunction(scope, ctx,
			func(_ *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
				innerRetained = args
				if args.Length() != 2 || args.IsConstructCall() {
					callbackErr = fmt.Errorf("inner snapshots = %d, %v", args.Length(), args.IsConstructCall())
					return
				}
				callbackErr = rv.SetInt32(22)
			}, nil)
		if err != nil {
			t.Fatal(err)
		}
		outer, err := iso.NewFunction(scope, ctx,
			func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
				outerRetained = args
				result, ok, err := inner.Call(cs.Scope(), mustUndefinedCallback(cs), mustInt32Callback(cs, 20), mustInt32Callback(cs, 2))
				if err != nil || !ok {
					callbackErr = fmt.Errorf("inner Call = %v, %v", ok, err)
					return
				}
				if _, err := innerRetained.Get(0); err == nil || !strings.Contains(err.Error(), "used after Close") {
					callbackErr = fmt.Errorf("retained inner Get = %v", err)
					return
				}
				if args.Length() != 1 || args.IsConstructCall() {
					callbackErr = fmt.Errorf("outer snapshots after inner = %d, %v", args.Length(), args.IsConstructCall())
					return
				}
				if _, err := args.Get(0); err != nil {
					callbackErr = fmt.Errorf("outer Get after inner = %v", err)
					return
				}
				callbackErr = rv.Set(result)
			}, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, ok, err := outer.Call(scope, mustUndefinedT(t, scope), mustInt32T(t, scope, 1))
		if err != nil || !ok || callbackErr != nil {
			t.Fatalf("outer Call = %v, %v; callback = %v", ok, err, callbackErr)
		}
		got, converted, err := result.IntegerValue(ctx)
		if err != nil || !converted || got != 22 {
			t.Fatalf("result = %d, %v, %v", got, converted, err)
		}
		for name, args := range map[string]gov8.FunctionCallbackArguments{"outer": outerRetained, "inner": innerRetained} {
			if _, err := args.Get(0); err == nil || !strings.Contains(err.Error(), "used after Close") {
				t.Fatalf("retained %s Get = %v", name, err)
			}
		}
	})
}

func TestFunctionCallbackArgumentsZeroValue(t *testing.T) {
	var args gov8.FunctionCallbackArguments
	if args.Length() != 0 || args.IsConstructCall() {
		t.Fatalf("zero snapshots = %d, %v", args.Length(), args.IsConstructCall())
	}
	for name, call := range map[string]func() error{
		"Get":       func() error { _, err := args.Get(0); return err },
		"This":      func() error { _, err := args.This(); return err },
		"NewTarget": func() error { _, err := args.NewTarget(); return err },
		"Data":      func() error { _, err := args.Data(); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "invalid callback arguments") {
			t.Fatalf("zero %s = %v", name, err)
		}
	}
}

func callbackArgumentInteger(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, index int) (int64, error) {
	value, err := args.Get(index)
	if err != nil {
		return 0, err
	}
	got, ok, err := cs.IntegerValue(value)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("argument %d conversion failed", index)
	}
	return got, nil
}

func mustUndefinedCallback(cs *gov8.CallbackScope) gov8.Value {
	value, err := cs.Scope().Undefined()
	if err != nil {
		panic(err)
	}
	return value
}

func mustInt32Callback(cs *gov8.CallbackScope, value int32) gov8.Value {
	result, err := cs.Scope().Int32(value)
	if err != nil {
		panic(err)
	}
	return result
}

func mustInt32T(t *testing.T, scope *gov8.Scope, value int32) gov8.Value {
	t.Helper()
	result, err := scope.Int32(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
