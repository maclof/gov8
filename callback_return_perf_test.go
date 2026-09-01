//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

func BenchmarkCallbackReturnValueBoolFromHost(b *testing.B) {
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

	function, err := iso.NewFunction(setup, ctx,
		func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			_ = rv.SetBool(true)
		}, nil)
	if err != nil {
		b.Fatal(err)
	}
	global, err := gov8.NewGlobal(setup, function.Value)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = global.Close() }()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		local, err := global.ToLocal(scope)
		if err != nil {
			b.Fatal(err)
		}
		undefined, err := scope.Undefined()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := gov8.CallFunction(ctx, scope, local, undefined, nil, nil); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
