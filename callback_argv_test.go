//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"testing"

	gov8 "gov8"
)

// callbackArgInlineCapacity mirrors the native trampoline's small fixed
// argument buffer. These tests deliberately exercise both sides of that
// boundary; changing the native capacity requires changing this probe too.
const callbackArgInlineCapacity = 2

func callbackIdentityArguments(scope *gov8.Scope, ctx *gov8.Context, count int) ([]gov8.Value, error) {
	values := make([]gov8.Value, count)
	for i := range values {
		object, err := scope.NewObject(ctx)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		values[i] = object.Value
	}
	return values, nil
}

func checkCallbackArguments(args gov8.FunctionCallbackArguments, want []gov8.Value, construct bool) error {
	if got := args.Length(); got != len(want) {
		return fmt.Errorf("Length = %d, want %d", got, len(want))
	}
	if got := args.IsConstructCall(); got != construct {
		return fmt.Errorf("IsConstructCall = %v, want %v", got, construct)
	}
	for i := range want {
		first, err := args.Get(i)
		if err != nil {
			return fmt.Errorf("Get(%d): %w", i, err)
		}
		second, err := args.Get(i)
		if err != nil {
			return fmt.Errorf("repeated Get(%d): %w", i, err)
		}
		sameWant, err := first.StrictEquals(want[i])
		if err != nil || !sameWant {
			return fmt.Errorf("Get(%d) identity = %v, %v", i, sameWant, err)
		}
		sameRepeat, err := first.StrictEquals(second)
		if err != nil || !sameRepeat {
			return fmt.Errorf("repeated Get(%d) identity = %v, %v", i, sameRepeat, err)
		}
	}
	for _, index := range []int{-1, len(want)} {
		value, err := args.Get(index)
		if err != nil {
			return fmt.Errorf("out-of-bounds Get(%d): %w", index, err)
		}
		undefined, err := value.IsUndefined()
		if err != nil || !undefined {
			return fmt.Errorf("out-of-bounds Get(%d) undefined = %v, %v", index, undefined, err)
		}
	}
	return nil
}

func TestHostCallbackArgumentBufferBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		count     int
		construct bool
	}{
		{name: "zero", count: 0},
		{name: "one", count: 1},
		{name: "inline_capacity_construct", count: callbackArgInlineCapacity, construct: true},
		{name: "heap_fallback_construct", count: callbackArgInlineCapacity + 1, construct: true},
		{name: "large", count: 64},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			iso, ctx, scope := newTestRuntime(t)
			values, err := callbackIdentityArguments(scope, ctx, tc.count)
			if err != nil {
				t.Fatal(err)
			}
			var callbackErr error
			calls := 0
			function, err := iso.NewFunction(scope, ctx,
				func(_ *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
					calls++
					callbackErr = checkCallbackArguments(args, values, tc.construct)
					if callbackErr == nil && !tc.construct {
						callbackErr = rv.SetInt32(int32(args.Length()))
					}
				}, nil)
			if err != nil {
				t.Fatal(err)
			}

			if tc.construct {
				if _, ok, err := function.NewInstance(scope, values...); err != nil || !ok {
					t.Fatalf("NewInstance = %v, %v", ok, err)
				}
			} else {
				undefined, err := scope.Undefined()
				if err != nil {
					t.Fatal(err)
				}
				result, ok, err := function.Call(scope, undefined, values...)
				if err != nil || !ok {
					t.Fatalf("Call = %v, %v", ok, err)
				}
				got, converted, err := result.IntegerValue(ctx)
				if err != nil || !converted || got != int64(tc.count) {
					t.Fatalf("result = %d, %v, %v; want %d", got, converted, err, tc.count)
				}
			}
			if callbackErr != nil {
				t.Fatal(callbackErr)
			}
			if calls != 1 {
				t.Fatalf("callback calls = %d, want 1", calls)
			}
		})
	}
}

func TestHostCallbackArgumentBufferNestedReentry(t *testing.T) {
	for _, count := range []int{callbackArgInlineCapacity, callbackArgInlineCapacity + 1} {
		t.Run(fmt.Sprintf("argc_%d", count), func(t *testing.T) {
			iso, ctx, scope := newTestRuntime(t)
			values, err := callbackIdentityArguments(scope, ctx, count)
			if err != nil {
				t.Fatal(err)
			}
			var callbackErr error
			innerCalls := 0
			inner, err := iso.NewFunction(scope, ctx,
				func(_ *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
					innerCalls++
					callbackErr = checkCallbackArguments(args, values, false)
					if callbackErr == nil {
						callbackErr = rv.SetInt32(int32(args.Length()))
					}
				}, nil)
			if err != nil {
				t.Fatal(err)
			}

			outerCalls := 0
			outer, err := iso.NewFunction(scope, ctx,
				func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
					outerCalls++
					callbackErr = checkCallbackArguments(args, values, false)
					if callbackErr != nil {
						return
					}
					nestedArgs := make([]gov8.Value, args.Length())
					for i := range nestedArgs {
						nestedArgs[i], callbackErr = args.Get(i)
						if callbackErr != nil {
							return
						}
					}
					undefined, err := cs.Scope().Undefined()
					if err != nil {
						callbackErr = err
						return
					}
					result, ok, err := inner.Call(cs.Scope(), undefined, nestedArgs...)
					if err != nil || !ok {
						callbackErr = fmt.Errorf("nested Call = %v, %v", ok, err)
						return
					}
					callbackErr = rv.Set(result)
				}, nil)
			if err != nil {
				t.Fatal(err)
			}
			undefined, err := scope.Undefined()
			if err != nil {
				t.Fatal(err)
			}
			result, ok, err := outer.Call(scope, undefined, values...)
			if err != nil || !ok || callbackErr != nil {
				t.Fatalf("outer Call = %v, %v; callback = %v", ok, err, callbackErr)
			}
			got, converted, err := result.IntegerValue(ctx)
			if err != nil || !converted || got != int64(count) {
				t.Fatalf("result = %d, %v, %v; want %d", got, converted, err, count)
			}
			if outerCalls != 1 || innerCalls != 1 {
				t.Fatalf("callback calls = outer %d, inner %d; want 1 each", outerCalls, innerCalls)
			}
		})
	}
}
