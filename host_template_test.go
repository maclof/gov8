//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Template construction and lifecycle tests, mirroring the pinned Rust host
// oracle's template checks (rust-oracle/src/checks/host/templates.rs). The
// fixture-level mechanical comparison lives in conformance-host-templates/;
// these tests cover the same behaviors plus misuse and lifecycle cases.

func evalText(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, src string) (string, bool) {
	t.Helper()
	v, err := eval(t, ctx, scope, src)
	if err != nil {
		return "", false
	}
	txt, err := v.ToString(ctx)
	if err != nil {
		return "", false
	}
	return txt, true
}

// seedGlobal sets globalThis[name] = v via Object.SetByName on the global.
func seedGlobal(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, name string, v gov8.Value) bool {
	t.Helper()
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	ok, err := global.SetByName(scope, ctx, name, v)
	if err != nil {
		t.Fatalf("SetByName: %v", err)
	}
	return ok
}

func returnFive(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	_ = cs
	_ = rv.SetInt32(5)
}

func TestFunctionTemplateConstruction(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	ft, err := iso.NewFunctionTemplate(scope, returnFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	if err := ft.SetClassName("Gov8Base"); err != nil {
		t.Fatalf("SetClassName: %v", err)
	}

	f1, err := ft.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	f2, err := ft.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction 2: %v", err)
	}
	same, err := f1.Value.StrictEquals(f2.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	if !same {
		t.Errorf("GetFunction must return the same function within one context")
	}
	name, err := f1.Name()
	if err != nil || name != "Gov8Base" {
		t.Errorf("function name = %q, %v; want Gov8Base", name, err)
	}
	isFn, err := f1.IsFunction()
	if err != nil || !isFn {
		t.Errorf("is_function = %v, %v; want true", isFn, err)
	}
	res, ok, err := f1.Call(scope, mustUndefinedT(t, scope))
	if err != nil || !ok {
		t.Fatalf("Call: ok=%v err=%v", ok, err)
	}
	if txt, _ := res.ToString(ctx); txt != "5" {
		t.Errorf("call result = %q; want 5", txt)
	}

	// A second context in the same isolate instantiates its own function.
	scope2, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope 2: %v", err)
	}
	ctx2, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext 2: %v", err)
	}
	fCtx2, err := ft.GetFunction(scope2, ctx2)
	if err != nil {
		t.Fatalf("GetFunction ctx2: %v", err)
	}
	distinct, err := f1.Value.StrictEquals(fCtx2.Value)
	if err != nil {
		t.Fatalf("StrictEquals ctx2: %v", err)
	}
	if distinct {
		t.Errorf("functions from different contexts must be distinct")
	}
	if err := scope2.Close(); err != nil {
		t.Errorf("scope2.Close: %v", err)
	}
	if err := ctx2.Close(); err != nil {
		t.Errorf("ctx2.Close: %v", err)
	}
}

// constructSeeds mirrors the oracle's cb_construct_seeds_instance: seeds
// internal field 0 with the first argument on construct calls and records
// the call shape.
func constructSeeds(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	shape := callShapeString(args)
	if args.IsConstructCall() {
		this, err := args.This()
		if err != nil {
			panic(err)
		}
		first, err := args.Get(0)
		if err != nil {
			panic(err)
		}
		if _, err := this.SetInternalField(0, first); err != nil {
			panic(err)
		}
		shapeV, err := cs.NewString(shape)
		if err != nil {
			panic(err)
		}
		if _, err := cs.ObjectSet(this.Value, "call_shape", shapeV); err != nil {
			panic(err)
		}
	}
	shapeV, err := cs.NewString(shape)
	if err != nil {
		panic(err)
	}
	_ = rv.Set(shapeV)
}

func callShapeString(args gov8.FunctionCallbackArguments) string {
	nt, err := args.NewTarget()
	if err != nil {
		panic(err)
	}
	ntIsFn, _ := nt.IsFunction()
	ntIsUndef, _ := nt.IsUndefined()
	return "construct=" + b2s(args.IsConstructCall()) +
		";new_target_function=" + b2s(ntIsFn) +
		";new_target_undefined=" + b2s(ntIsUndef)
}

func b2s(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestInstancePrototypeAndConstructor(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	ft, err := iso.NewFunctionTemplate(scope, constructSeeds, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	if err := ft.SetClassName("Gov8Thing"); err != nil {
		t.Fatalf("SetClassName: %v", err)
	}
	it, err := ft.InstanceTemplate()
	if err != nil {
		t.Fatalf("InstanceTemplate: %v", err)
	}
	if ok, err := it.SetInternalFieldCount(2); err != nil || !ok {
		t.Fatalf("SetInternalFieldCount: ok=%v err=%v", ok, err)
	}
	if n, err := it.InternalFieldCount(); err != nil || n != 2 {
		t.Errorf("template field count = %d, %v; want 2", n, err)
	}
	pt, err := ft.PrototypeTemplate()
	if err != nil {
		t.Fatalf("PrototypeTemplate: %v", err)
	}
	mark, err := scope.NewString("on-proto")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if err := pt.Set("protoMark", mark); err != nil {
		t.Fatalf("pt.Set: %v", err)
	}

	f, err := ft.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "Gov8Thing", f.Value) {
		t.Fatal("seeding Gov8Thing failed")
	}

	protoCheck, ok := evalText(t, ctx, scope,
		"const t = new Gov8Thing(5); "+
			"[t instanceof Gov8Thing, t.protoMark, "+
			"Object.getPrototypeOf(t) === Gov8Thing.prototype].join('|')")
	if !ok || protoCheck != "true|on-proto|true" {
		t.Errorf("proto_check = %q (ok=%v); want true|on-proto|true", protoCheck, ok)
	}

	tV, err := eval(t, ctx, scope, "t")
	if err != nil {
		t.Fatalf("eval t: %v", err)
	}
	count, err := tV.InternalFieldCount()
	if err != nil || count != 2 {
		t.Errorf("instance field count = %d, %v; want 2", count, err)
	}
	seeded, has, err := tV.GetInternalField(0)
	if err != nil || !has {
		t.Fatalf("GetInternalField: has=%v err=%v", has, err)
	}
	if n, _ := seeded.NumberValueRaw(); n != 5 {
		t.Errorf("seeded value = %v; want 5", n)
	}

	plain, ok := evalText(t, ctx, scope, "Gov8Thing(3)")
	if !ok || plain != "construct=false;new_target_function=false;new_target_undefined=true" {
		t.Errorf("plain call shape = %q (ok=%v)", plain, ok)
	}
	shape, ok := evalText(t, ctx, scope, "t.call_shape")
	if !ok || shape != "construct=true;new_target_function=true;new_target_undefined=false" {
		t.Errorf("construct call shape = %q (ok=%v)", shape, ok)
	}

	nine, err := scope.Int32(9)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	inst, ok, err := f.NewInstance(scope, nine)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	if count, _ := inst.InternalFieldCount(); count != 2 {
		t.Errorf("host instance field count = %d; want 2", count)
	}
	hostMark, ok, err := inst.GetByName(scope, ctx, "protoMark")
	if err != nil || !ok {
		t.Fatalf("GetByName protoMark: ok=%v err=%v", ok, err)
	}
	if txt, _ := hostMark.ToString(ctx); txt != "on-proto" {
		t.Errorf("host instance protoMark = %q; want on-proto", txt)
	}
}

func TestObjectTemplateInstances(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	ot, err := iso.NewObjectTemplate(scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	one, err := scope.Int32(1)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	if err := ot.Set("a", one); err != nil {
		t.Fatalf("ot.Set: %v", err)
	}
	i1, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance i1: ok=%v err=%v", ok, err)
	}
	i2, ok, err := ot.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance i2: ok=%v err=%v", ok, err)
	}
	if distinct, _ := i1.Value.StrictEquals(i2.Value); distinct {
		t.Errorf("instances must be distinct objects")
	}
	for name, inst := range map[string]*gov8.Object{"i1": i1, "i2": i2} {
		a, ok, err := inst.GetByName(scope, ctx, "a")
		if err != nil || !ok {
			t.Fatalf("%s.GetByName a: ok=%v err=%v", name, ok, err)
		}
		if txt, _ := a.ToString(ctx); txt != "1" {
			t.Errorf("%s.a = %q; want 1", name, txt)
		}
	}
	two, err := scope.Int32(2)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	if _, err := i1.SetByName(scope, ctx, "b", two); err != nil {
		t.Fatalf("SetByName b: %v", err)
	}
	b2, ok, err := i2.GetByName(scope, ctx, "b")
	if err != nil || !ok {
		t.Fatalf("i2.GetByName b: ok=%v err=%v", ok, err)
	}
	if isU, _ := b2.IsUndefined(); !isU {
		t.Errorf("i2.b must be undefined (instances independent)")
	}

	// Instances derived from a function template inherit its prototype.
	ft, err := iso.NewFunctionTemplate(scope, returnFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	if err := ft.SetClassName("Gov8Base"); err != nil {
		t.Fatalf("SetClassName: %v", err)
	}
	fpt, err := ft.PrototypeTemplate()
	if err != nil {
		t.Fatalf("PrototypeTemplate: %v", err)
	}
	mark2, err := scope.NewString("on-proto")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if err := fpt.Set("protoMark", mark2); err != nil {
		t.Fatalf("fpt.Set: %v", err)
	}
	ctor, err := ft.GetFunction(scope, ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "Gov8Base", ctor.Value) {
		t.Fatal("seeding Gov8Base failed")
	}
	ot2, err := iso.NewObjectTemplateFromFunction(scope, ft)
	if err != nil {
		t.Fatalf("NewObjectTemplateFromFunction: %v", err)
	}
	o2, ok, err := ot2.NewInstance(scope, ctx)
	if err != nil || !ok {
		t.Fatalf("ot2.NewInstance: ok=%v err=%v", ok, err)
	}
	if !seedGlobal(t, ctx, scope, "o2", o2.Value) {
		t.Fatal("seeding o2 failed")
	}
	if got, ok := evalText(t, ctx, scope, "Object.getPrototypeOf(o2) === Gov8Base.prototype"); !ok || got != "true" {
		t.Errorf("proto identity = %q (ok=%v); want true", got, ok)
	}
	if got, ok := evalText(t, ctx, scope, "o2.protoMark"); !ok || got != "on-proto" {
		t.Errorf("inherited mark = %q (ok=%v); want on-proto", got, ok)
	}
}

func mustUndefinedT(t *testing.T, scope *gov8.Scope) gov8.Value {
	t.Helper()
	v, err := scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	return v
}

func TestTemplateMisuse(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	if _, err := iso.NewFunctionTemplate(scope, nil, nil); err == nil {
		t.Errorf("nil callback must be rejected")
	}

	// A closed scope invalidates the template created in it.
	s2, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	ft, err := iso.NewFunctionTemplate(s2, returnFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("s2.Close: %v", err)
	}
	if err := ft.SetClassName("X"); err == nil {
		t.Errorf("SetClassName on closed scope must fail")
	}
	if _, err := ft.GetFunction(scope, ctx); err == nil {
		t.Errorf("GetFunction with closed template scope must fail")
	}

	// Cross-isolate context must be rejected before touching the engine.
	isoB, _, scopeB := newTestRuntime(t)
	ftB, err := isoB.NewFunctionTemplate(scopeB, returnFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate isoB: %v", err)
	}
	if _, err := ftB.GetFunction(scopeB, ctx); err == nil {
		t.Errorf("GetFunction with foreign context must fail")
	}
}
