//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"

	gov8 "gov8"
)

func TestReturnValueInt32Boundaries(t *testing.T) {
	for _, want := range []int32{42, math.MinInt32, math.MaxInt32} {
		t.Run(itoa(int(want)), func(t *testing.T) {
			iso, ctx, scope := newTestRuntime(t)
			var callbackErr error
			function, err := iso.NewFunction(scope, ctx,
				func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
					callbackErr = rv.SetInt32(want)
				}, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, ok, err := function.Call(scope, mustUndefinedT(t, scope))
			if err != nil || !ok || callbackErr != nil {
				t.Fatalf("Call = %v, %v; callback = %v", ok, err, callbackErr)
			}
			got, converted, err := result.IntegerValue(ctx)
			if err != nil || !converted || got != int64(want) {
				t.Fatalf("result = %d, %v, %v; want %d", got, converted, err, want)
			}
		})
	}
}

func TestReturnValueInt32ConstructCallReturnsReceiver(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var callbackErr error
	function, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			if !args.IsConstructCall() {
				callbackErr = fmt.Errorf("callback was not a construct call")
				return
			}
			receiver, err := args.This()
			if err != nil {
				callbackErr = err
				return
			}
			marker, err := cs.NewString("receiver")
			if err != nil {
				callbackErr = err
				return
			}
			if ok, err := cs.ObjectSet(receiver.Value, "constructed", marker); err != nil || !ok {
				callbackErr = fmt.Errorf("ObjectSet = %v, %v", ok, err)
				return
			}
			// A primitive constructor return is ignored by V8; the created
			// receiver, including the marker above, must be returned instead.
			callbackErr = rv.SetInt32(99)
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	instance, ok, err := function.NewInstance(scope)
	if err != nil || !ok || callbackErr != nil {
		t.Fatalf("NewInstance = %v, %v; callback = %v", ok, err, callbackErr)
	}
	marker, present, err := instance.GetByName(scope, ctx, "constructed")
	if err != nil || !present {
		t.Fatalf("constructed marker = %v, %v", present, err)
	}
	got, err := marker.ToString(ctx)
	if err != nil || got != "receiver" {
		t.Fatalf("constructed marker = %q, %v; want receiver", got, err)
	}
}

func TestReturnValueInt32LastSuccessfulWrite(t *testing.T) {
	type sequence struct {
		name string
		want string
		run  func(*gov8.CallbackScope, gov8.ReturnValue) error
	}
	sequences := []sequence{
		{name: "int32_twice", want: "8", run: func(_ *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetInt32(7); err != nil {
				return err
			}
			return rv.SetInt32(8)
		}},
		{name: "arbitrary", want: "value", run: func(cs *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetInt32(7); err != nil {
				return err
			}
			value, err := cs.NewString("value")
			if err != nil {
				return err
			}
			return rv.Set(value)
		}},
		{name: "uint32", want: "4294967295", run: func(_ *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetInt32(7); err != nil {
				return err
			}
			return rv.SetUint32(math.MaxUint32)
		}},
		{name: "float64", want: "2.5", run: func(_ *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetInt32(7); err != nil {
				return err
			}
			return rv.SetFloat64(2.5)
		}},
		{name: "bool", want: "true", run: func(_ *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetInt32(7); err != nil {
				return err
			}
			return rv.SetBool(true)
		}},
		{name: "null", want: "null", run: func(_ *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetInt32(7); err != nil {
				return err
			}
			return rv.SetNull()
		}},
		{name: "undefined", want: "undefined", run: func(_ *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetInt32(7); err != nil {
				return err
			}
			return rv.SetUndefined()
		}},
		{name: "empty_string", want: "", run: func(_ *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetInt32(7); err != nil {
				return err
			}
			return rv.SetEmptyString()
		}},
		{name: "int32_after_bool", want: "9", run: func(_ *gov8.CallbackScope, rv gov8.ReturnValue) error {
			if err := rv.SetBool(true); err != nil {
				return err
			}
			return rv.SetInt32(9)
		}},
	}

	for _, tc := range sequences {
		t.Run(tc.name, func(t *testing.T) {
			iso, ctx, scope := newTestRuntime(t)
			var callbackErr error
			function, err := iso.NewFunction(scope, ctx,
				func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
					callbackErr = tc.run(cs, rv)
				}, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, ok, err := function.Call(scope, mustUndefinedT(t, scope))
			if err != nil || !ok || callbackErr != nil {
				t.Fatalf("Call = %v, %v; callback = %v", ok, err, callbackErr)
			}
			got, err := result.ToString(ctx)
			if err != nil || got != tc.want {
				t.Fatalf("result = %q, %v; want %q", got, err, tc.want)
			}
		})
	}

	t.Run("invalid_write_preserves_int32", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		var invalidErr error
		function, err := iso.NewFunction(scope, ctx,
			func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
				_ = rv.SetInt32(11)
				invalidErr = rv.Set(gov8.Value{})
			}, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, ok, err := function.Call(scope, mustUndefinedT(t, scope))
		if err != nil || !ok {
			t.Fatalf("Call = %v, %v", ok, err)
		}
		if invalidErr == nil || !strings.Contains(invalidErr.Error(), "zero value handle") {
			t.Fatalf("invalid Set error = %v", invalidErr)
		}
		got, converted, err := result.IntegerValue(ctx)
		if err != nil || !converted || got != 11 {
			t.Fatalf("result = %d, %v, %v; want 11", got, converted, err)
		}
	})

	t.Run("get_materializes_int32", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		var callbackErr error
		function, err := iso.NewFunction(scope, ctx,
			func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
				if callbackErr = rv.SetInt32(12); callbackErr != nil {
					return
				}
				value, err := rv.Get()
				if err != nil {
					callbackErr = err
					return
				}
				got, converted, err := cs.IntegerValue(value)
				if err != nil || !converted || got != 12 {
					callbackErr = &callbackResultError{got: got, converted: converted, err: err}
					return
				}
				callbackErr = rv.SetInt32(13)
			}, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, ok, err := function.Call(scope, mustUndefinedT(t, scope))
		if err != nil || !ok || callbackErr != nil {
			t.Fatalf("Call = %v, %v; callback = %v", ok, err, callbackErr)
		}
		got, converted, err := result.IntegerValue(ctx)
		if err != nil || !converted || got != 13 {
			t.Fatalf("result = %d, %v, %v; want 13", got, converted, err)
		}
	})
}

type callbackResultError struct {
	got       int64
	converted bool
	err       error
}

func (e *callbackResultError) Error() string {
	return "callback conversion mismatch: got=" + itoa(int(e.got)) +
		" converted=" + b2s(e.converted) + " err=" + errorText(e.err)
}

func errorText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func TestReturnValueInt32NestedFrames(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var callbackErr error
	inner, err := iso.NewFunction(scope, ctx,
		func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			callbackErr = rv.SetInt32(22)
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			if callbackErr = rv.SetInt32(11); callbackErr != nil {
				return
			}
			undefined, err := cs.Scope().Undefined()
			if err != nil {
				callbackErr = err
				return
			}
			innerResult, ok, err := inner.Call(cs.Scope(), undefined)
			if err != nil || !ok {
				callbackErr = &callbackCallError{ok: ok, err: err}
				return
			}
			innerValue, converted, err := cs.IntegerValue(innerResult)
			if err != nil || !converted || innerValue != 22 {
				callbackErr = &callbackResultError{got: innerValue, converted: converted, err: err}
				return
			}
			outerValue, err := rv.Get()
			if err != nil {
				callbackErr = err
				return
			}
			got, converted, err := cs.IntegerValue(outerValue)
			if err != nil || !converted || got != 11 {
				callbackErr = &callbackResultError{got: got, converted: converted, err: err}
				return
			}
			callbackErr = rv.SetInt32(33)
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := outer.Call(scope, mustUndefinedT(t, scope))
	if err != nil || !ok || callbackErr != nil {
		t.Fatalf("outer Call = %v, %v; callback = %v", ok, err, callbackErr)
	}
	got, converted, err := result.IntegerValue(ctx)
	if err != nil || !converted || got != 33 {
		t.Fatalf("result = %d, %v, %v; want 33", got, converted, err)
	}
}

type callbackCallError struct {
	ok  bool
	err error
}

func (e *callbackCallError) Error() string {
	return "callback call mismatch: ok=" + b2s(e.ok) + " err=" + errorText(e.err)
}

func TestReturnValueInt32WrongThreadAndRetained(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	var retained gov8.ReturnValue
	var wrongThreadErr error
	var ownerErr error
	function, err := iso.NewFunction(scope, ctx,
		func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			retained = rv
			done := make(chan error, 1)
			go func() {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				done <- rv.SetInt32(1)
			}()
			wrongThreadErr = <-done
			ownerErr = rv.SetInt32(42)
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := function.Call(scope, mustUndefinedT(t, scope))
	if err != nil || !ok || ownerErr != nil {
		t.Fatalf("Call = %v, %v; owner = %v", ok, err, ownerErr)
	}
	if wrongThreadErr == nil || !strings.Contains(wrongThreadErr.Error(), "thread affinity") {
		t.Fatalf("wrong-thread SetInt32 = %v", wrongThreadErr)
	}
	got, converted, err := result.IntegerValue(ctx)
	if err != nil || !converted || got != 42 {
		t.Fatalf("result = %d, %v, %v; want 42", got, converted, err)
	}
	if err := retained.SetInt32(2); err == nil || !strings.Contains(err.Error(), "used after Close") {
		t.Fatalf("retained SetInt32 = %v", err)
	}
}
