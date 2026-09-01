//go:build windows && amd64

package gov8

import "testing"

func TestFunctionCodeCacheEngineRejectionBoundaries(t *testing.T) {
	producer, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	producerContext, _ := producer.NewContext()
	producerScope, _ := producer.NewScope()
	compiled, rejected, err := producerContext.CompileFunctionAdvanced(
		producerScope, "return left * 10 + right;", []string{"left", "right"}, nil, nil)
	if err != nil || rejected {
		t.Fatalf("producer compile = rejected %v, err %v", rejected, err)
	}
	cache, err := compiled.CreateCodeCache()
	if err != nil {
		t.Fatal(err)
	}
	_ = producerScope.Close()
	_ = producerContext.Close()
	_ = producer.Close()

	consumer, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = consumer.Close() }()
	consumerContext, _ := consumer.NewContext()
	defer func() { _ = consumerContext.Close() }()
	consumerScope, _ := consumer.NewScope()
	defer func() { _ = consumerScope.Close() }()

	call := func(function *Function) int64 {
		left, _ := consumerScope.Int32(4)
		right, _ := consumerScope.Int32(2)
		receiver, _ := consumerScope.Undefined()
		result, ok, err := function.Call(consumerScope, receiver, left, right)
		if err != nil || !ok {
			t.Fatalf("Call = %v, %v", ok, err)
		}
		value, converted, err := result.IntegerValue(consumerContext)
		if err != nil || !converted {
			t.Fatalf("IntegerValue = %v, %v", converted, err)
		}
		return value
	}

	truncated := &FunctionCodeCache{data: append([]byte(nil), cache.data[:len(cache.data)/2]...)}
	function, rejected, err := consumerContext.CompileFunctionAdvanced(
		consumerScope, "return left * 10 + right;", []string{"left", "right"}, truncated, nil)
	if err != nil || !rejected || call(function) != 42 {
		t.Fatalf("truncated cache = rejected %v, err %v", rejected, err)
	}

	corrupted := &FunctionCodeCache{data: append([]byte(nil), cache.data...)}
	corrupted.data[len(corrupted.data)/2] ^= 0xff
	function, rejected, err = consumerContext.CompileFunctionAdvanced(
		consumerScope, "return left * 10 + right;", []string{"left", "right"}, corrupted, nil)
	if err != nil || rejected || call(function) != 42 {
		t.Fatalf("one-byte XOR cache = rejected %v, err %v", rejected, err)
	}

	// Ordinary callers cannot construct either payload: FunctionCodeCache.data
	// is private. This same-package test is the explicit engine boundary probe.
}
