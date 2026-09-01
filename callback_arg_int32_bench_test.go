//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// BenchmarkCallbackNonInt32CoerciveControl measures the native IsInt32 tax
// when neither argument is eligible for metadata. Both string arguments must
// still take V8's coercive IntegerValue path and produce the asserted result.
func BenchmarkCallbackNonInt32CoerciveControl(b *testing.B) {
	iso := benchNewIsolate(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = gov8.ReleaseIsolateHostState(iso) }()
	ctx := benchNewContext(b, iso)
	defer func() { _ = ctx.Close() }()
	setup, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = setup.Close() }()

	function, err := iso.NewFunction(setup, ctx, benchAddCb, &gov8.FunctionOptions{Length: 2})
	if err != nil {
		b.Fatal(err)
	}
	global, err := ctx.GlobalObject(setup)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := global.SetByName(setup, ctx, "__coerciveAdd", function.Value); err != nil {
		b.Fatal(err)
	}
	script, err := ctx.Compile(setup, "__coerciveAdd('20', '22')", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = script.Close() }()

	probe, err := script.Run(setup, nil)
	if err != nil {
		b.Fatal(err)
	}
	benchAssertNumber(b, ctx, probe, benchCallbackExpectedHostResult, "callback/non_int32_coercive_control")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		result, err := script.Run(scope, nil)
		if err != nil {
			b.Fatal(err)
		}
		benchAssertNumber(b, ctx, result, benchCallbackExpectedHostResult, "callback/non_int32_coercive_control")
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}
