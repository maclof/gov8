//go:build windows && amd64

package gov8_test

import (
	"strconv"
	"testing"
)

func BenchmarkFunctionAdvancedCompileCold(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	sources := make([]string, b.N)
	for index := range sources {
		sources[index] = "return left * 10 + right; // " + strconv.Itoa(index)
	}

	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		if _, rejected, err := ctx.CompileFunctionAdvanced(
			scope, sources[index], []string{"left", "right"}, nil, nil,
		); err != nil || rejected {
			b.Fatalf("compile = rejected %v, err %v", rejected, err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFunctionAdvancedCodeCacheConsume(b *testing.B) {
	const source = "return left * 10 + right;"
	params := []string{"left", "right"}

	producer := benchNewIsolate(b)
	producerContext := benchNewContext(b, producer)
	producerScope, err := producer.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	compiled, rejected, err := producerContext.CompileFunctionAdvanced(
		producerScope, source, params, nil, nil)
	if err != nil || rejected {
		b.Fatalf("producer compile = rejected %v, err %v", rejected, err)
	}
	cache, err := compiled.CreateCodeCache()
	if err != nil {
		b.Fatal(err)
	}
	_ = producerScope.Close()
	_ = producerContext.Close()
	_ = producer.Close()

	consumer := benchNewIsolate(b)
	defer func() { _ = consumer.Close() }()
	consumerContext := benchNewContext(b, consumer)
	defer func() { _ = consumerContext.Close() }()
	// Correctness once before timing.
	checkScope, err := consumer.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	checkFunction, rejected, err := consumerContext.CompileFunctionAdvanced(
		checkScope, source, params, cache, nil)
	if err != nil || rejected {
		b.Fatalf("consumer compile = rejected %v, err %v", rejected, err)
	}
	left, _ := checkScope.Int32(4)
	right, _ := checkScope.Int32(2)
	result, ok, err := checkFunction.Call(checkScope, mustUndef(b, checkScope), left, right)
	if err != nil || !ok {
		b.Fatalf("call = ok %v, err %v", ok, err)
	}
	if value, converted, err := result.IntegerValue(consumerContext); err != nil || !converted || value != 42 {
		b.Fatalf("result = %d, %v, %v", value, converted, err)
	}
	_ = checkScope.Close()

	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		scope, err := consumer.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		if _, rejected, err := consumerContext.CompileFunctionAdvanced(
			scope, source, params, cache, nil,
		); err != nil || rejected {
			b.Fatalf("consume = rejected %v, err %v", rejected, err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
