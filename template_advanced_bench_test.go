//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Template-advanced benchmarks, comparable with the pinned oracle's
// template/interceptor workloads:
//
//   - tpladv/named_interceptor_get_js:  one precompiled script reads an
//     interceptor-served property per iteration (named getter dispatch).
//   - tpladv/indexed_interceptor_get_js: same via io[42] (indexed getter).
//   - tpladv/object_template_new_instance: template instantiation cost.
//   - tpladv/call_as_function_js:       calling a call-as-function-handler
//     object from JS (function-shape dispatch on a non-Function).
//   - tpladv/signature_method_js:       a signature-checked method call
//     (`sd.m(5)`), i.e. receiver validation + native callback dispatch.
//   - tpladv/interceptor_set_js:        an intercepted property store.
//
// Raw command: go test -bench 'BenchmarkTplAdv' -benchmem -count 5 .
// Each iteration opens/closes a fresh Scope (HandleScope equivalent), like
// the oracle's bench harness.

func benchAdvSetup(b *testing.B) (*gov8.Isolate, *gov8.Context, *gov8.Scope) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	return iso, ctx, scope
}

func benchAdvCompile(b *testing.B, ctx *gov8.Context, scope *gov8.Scope, src string) *gov8.Script {
	b.Helper()
	script, err := ctx.Compile(scope, src, nil)
	if err != nil {
		b.Fatalf("Compile %q: %v", src, err)
	}
	return script
}

func benchInterceptorGet(b *testing.B, indexed bool) {
	iso, ctx, scope := benchAdvSetup(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = ctx.Close() }()
	defer func() { _ = scope.Close() }()

	getter := func(cs *gov8.CallbackScope, key gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		_ = rv.SetInt32(4242)
		return gov8.InterceptedYes
	}
	indexedGetter := func(cs *gov8.CallbackScope, index uint32, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
		_ = rv.SetInt32(4242)
		return gov8.InterceptedYes
	}
	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		b.Fatalf("NewObjectTemplate: %v", err)
	}
	if indexed {
		if err := ot.SetIndexedPropertyHandler(gov8.IndexedPropertyHandlerConfig{
			Getter: indexedGetter,
		}); err != nil {
			b.Fatalf("SetIndexedPropertyHandler: %v", err)
		}
	} else {
		if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
			Getter: getter,
		}); err != nil {
			b.Fatalf("SetNamedPropertyHandler: %v", err)
		}
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		b.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		b.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(scope, ctx, "o", obj.Value); err != nil {
		b.Fatalf("SetByName: %v", err)
	}
	src := "o.k"
	if indexed {
		src = "o[42]"
	}
	script := benchAdvCompile(b, ctx, scope, src)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		if _, err := script.Run(inner, nil); err != nil {
			b.Fatalf("Run: %v", err)
		}
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
}

func BenchmarkTplAdvNamedInterceptorGetJS(b *testing.B)   { benchInterceptorGet(b, false) }
func BenchmarkTplAdvIndexedInterceptorGetJS(b *testing.B) { benchInterceptorGet(b, true) }

func BenchmarkTplAdvInterceptorSetJS(b *testing.B) {
	iso, ctx, scope := benchAdvSetup(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = ctx.Close() }()
	defer func() { _ = scope.Close() }()

	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		b.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetNamedPropertyHandler(gov8.NamedPropertyHandlerConfig{
		Setter: func(cs *gov8.CallbackScope, key, value gov8.Value, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) gov8.Intercepted {
			_ = rv.SetBool(true)
			return gov8.InterceptedYes
		},
	}); err != nil {
		b.Fatalf("SetNamedPropertyHandler: %v", err)
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		b.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		b.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(scope, ctx, "o", obj.Value); err != nil {
		b.Fatalf("SetByName: %v", err)
	}
	script := benchAdvCompile(b, ctx, scope, "o.k = 42")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		if _, err := script.Run(inner, nil); err != nil {
			b.Fatalf("Run: %v", err)
		}
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
}

func BenchmarkTplAdvObjectTemplateNewInstance(b *testing.B) {
	iso, ctx, scope := benchAdvSetup(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = ctx.Close() }()
	defer func() { _ = scope.Close() }()

	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		b.Fatalf("NewObjectTemplate: %v", err)
	}
	mark, err := scope.Int32(7)
	if err != nil {
		b.Fatalf("Int32: %v", err)
	}
	if err := ot.Set("mark", mark); err != nil {
		b.Fatalf("Set: %v", err)
	}
	if _, err := ot.SetInternalFieldCount(1); err != nil {
		b.Fatalf("SetInternalFieldCount: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		ot2 := ot // the template wrapper stays scope-bound; the instance wire lands in inner
		if _, ok, err := ot2.NewInstance(inner, ctx); err != nil || !ok {
			b.Fatalf("NewInstance: ok=%v err=%v", ok, err)
		}
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
}

func BenchmarkTplAdvCallAsFunctionJS(b *testing.B) {
	iso, ctx, scope := benchAdvSetup(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = ctx.Close() }()
	defer func() { _ = scope.Close() }()

	handler := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		arg0, _ := args.Get(0)
		n, _, _ := cs.IntegerValue(arg0)
		_ = rv.SetInt32(int32(n * 2))
	}
	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		b.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetCallAsFunctionHandler(handler, gov8.Value{}); err != nil {
		b.Fatalf("SetCallAsFunctionHandler: %v", err)
	}
	obj, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		b.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		b.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(scope, ctx, "o", obj.Value); err != nil {
		b.Fatalf("SetByName: %v", err)
	}
	script := benchAdvCompile(b, ctx, scope, "o(4)")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		if _, err := script.Run(inner, nil); err != nil {
			b.Fatalf("Run: %v", err)
		}
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
}

func BenchmarkTplAdvSignatureMethodJS(b *testing.B) {
	iso, ctx, scope := benchAdvSetup(b)
	defer func() { _ = iso.Close() }()
	defer func() { _ = ctx.Close() }()
	defer func() { _ = scope.Close() }()

	method := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		v, _ := cs.NewString("ok")
		_ = rv.Set(v)
	}
	baseFT, err := iso.NewFunctionTemplate(scope, noopCB, nil)
	if err != nil {
		b.Fatalf("NewFunctionTemplate base: %v", err)
	}
	sig, err := iso.NewSignature(scope, baseFT)
	if err != nil {
		b.Fatalf("NewSignature: %v", err)
	}
	methodFT, err := iso.NewFunctionTemplate(scope, method, &gov8.FunctionOptions{
		Signature: sig,
	})
	if err != nil {
		b.Fatalf("NewFunctionTemplate method: %v", err)
	}
	proto, err := baseFT.PrototypeTemplate()
	if err != nil {
		b.Fatalf("PrototypeTemplate: %v", err)
	}
	if err := proto.SetData("m", methodFT); err != nil {
		b.Fatalf("SetData: %v", err)
	}
	baseFn, err := baseFT.GetFunction(scope, ctx)
	if err != nil {
		b.Fatalf("GetFunction: %v", err)
	}
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		b.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(scope, ctx, "Base", baseFn.Value); err != nil {
		b.Fatalf("SetByName: %v", err)
	}
	script := benchAdvCompile(b, ctx, scope, "var i = new Base(); i.m(5)")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inner, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		if _, err := script.Run(inner, nil); err != nil {
			b.Fatalf("Run: %v", err)
		}
		if err := inner.Close(); err != nil {
			b.Fatalf("inner.Close: %v", err)
		}
	}
}
