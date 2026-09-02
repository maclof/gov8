//go:build windows && amd64

package main

// The 13 template/callback/accessor/external checks of the pinned Rust host
// oracle (rust-oracle/src/checks/host/{templates,callbacks,accessors,
// external_data}.rs), re-implemented on the Go binding. Every value below is
// produced by live engine observation; the expectation is never hardcoded
// into the check bodies — the comparison target is the pinned fixture.

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// --- callbacks (mirroring rust-oracle/src/checks/host/callbacks.rs) ----------

func cbConstantFive(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	_ = rv.SetInt32(5)
}

// cbConstructSeedsInstance seeds internal field 0 of the created instance
// with its first argument and records the call shape.
func cbConstructSeedsInstance(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	encoding := callShapeEncoding(args)
	encoded, err := cs.NewString(encoding)
	if err != nil {
		panic(err)
	}
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
		if _, err := cs.ObjectSet(this.Value, "call_shape", encoded); err != nil {
			panic(err)
		}
	}
	_ = rv.Set(encoded)
}

func callShapeEncoding(args gov8.FunctionCallbackArguments) string {
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

func cbAdd(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	a := intOrZero(cs, args, 0)
	b := intOrZero(cs, args, 1)
	_ = rv.SetInt32(int32(a + b))
}

func intOrZero(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, i int) int64 {
	v, err := args.Get(i)
	if err != nil {
		return 0
	}
	n, ok, err := cs.IntegerValue(v)
	if err != nil || !ok {
		return 0
	}
	return n
}

func cbArity(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	oob, err := args.Get(3)
	if err != nil {
		panic(err)
	}
	oobUndef, _ := oob.IsUndefined()
	encoding := "len=" + itoa(args.Length()) + ";oob3_undefined=" + b2s(oobUndef)
	encoded, err := cs.NewString(encoding)
	if err != nil {
		panic(err)
	}
	_ = rv.Set(encoded)
}

func cbReceiverMark(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	this, err := args.This()
	if err != nil {
		panic(err)
	}
	mark, ok, err := cs.ObjectGet(this.Value, "mark")
	if err != nil || !ok {
		panic(err)
	}
	txt, err := cs.ToString(mark)
	if err != nil {
		panic(err)
	}
	txtV, err := cs.NewString(txt)
	if err != nil {
		panic(err)
	}
	_ = rv.Set(txtV)
}

func cbEchoData(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	data, err := args.Data()
	if err != nil {
		panic(err)
	}
	txt, err := cs.ToString(data)
	if err != nil {
		panic(err)
	}
	txtV, err := cs.NewString(txt)
	if err != nil {
		panic(err)
	}
	_ = rv.Set(txtV)
}

func cbConstructShape(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	encoding := callShapeEncoding(args)
	encoded, err := cs.NewString(encoding)
	if err != nil {
		panic(err)
	}
	if args.IsConstructCall() {
		this, err := args.This()
		if err != nil {
			panic(err)
		}
		first, err := args.Get(0)
		if err != nil {
			panic(err)
		}
		if _, err := cs.ObjectSet(this.Value, "seeded", first); err != nil {
			panic(err)
		}
		if _, err := cs.ObjectSet(this.Value, "call_shape", encoded); err != nil {
			panic(err)
		}
	}
	_ = rv.Set(encoded)
}

func cbCallIt(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	fn, err := args.Get(0)
	if err != nil {
		panic(err)
	}
	arg, err := args.Get(1)
	if err != nil {
		panic(err)
	}
	res, ok, err := cs.CallFunction(fn, csMustUndefined(cs), []gov8.Value{arg})
	if err != nil || !ok {
		return
	}
	_ = rv.Set(res)
}

func csMustUndefined(cs *gov8.CallbackScope) gov8.Value {
	v, err := cs.Scope().Undefined()
	if err != nil {
		panic(err)
	}
	return v
}

func cbThrowError(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
	exc, err := cs.NewError("native-boom")
	if err != nil {
		panic(err)
	}
	if err := cs.ThrowException(exc); err != nil {
		panic(err)
	}
}

func cbThrowString(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
	msg, err := cs.NewString("native-string-boom")
	if err != nil {
		panic(err)
	}
	if err := cs.ThrowException(msg); err != nil {
		panic(err)
	}
}

// observeThrown mirrors the oracle's observe_thrown: run `source` in a fresh
// TryCatch and render the normalized exception observation.
func observeThrown(t *testing.T, r *runtime, source string) string {
	t.Helper()
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	compileOK := false
	script, cerr := r.ctx.Compile(r.scope, source, tc)
	if cerr == nil {
		compileOK = true
	}
	runOK := false
	if compileOK {
		_, rerr := script.Run(r.scope, tc)
		runOK = rerr == nil
		_ = script.Close()
	}
	hasCaught, _ := tc.HasCaught()
	canContinue, _ := tc.CanContinue()
	message, _ := tc.MessageText(r.scope, r.ctx)
	exceptionText, _ := tc.ExceptionText(r.scope, r.ctx)
	exceptionIsString, _ := tc.ExceptionIsString()

	return jsonString(jobj(
		kv("compile_ok", jbool(compileOK)),
		kv("run_ok", jbool(runOK)),
		kv("has_caught", jbool(hasCaught)),
		kv("can_continue", jbool(canContinue)),
		kv("message", jstr(message)),
		kv("exception_text", jstr(exceptionText)),
		kv("exception_is_string", jbool(exceptionIsString)),
	))
}

// --- accessors (mirroring rust-oracle/src/checks/host/accessors.rs) ----------

func accGetter(_ *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {
	_ = rv.SetInt32(7)
}

func staticGetterFn(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	_ = rv.SetInt32(33)
}

func cbNoop(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {}

// --- checks -------------------------------------------------------------------

func checkFunctionTemplateConstruction(t *testing.T, r *runtime) string {
	t.Helper()
	template, err := r.iso.NewFunctionTemplate(r.scope, cbConstantFive, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	if err := template.SetClassName("Gov8Base"); err != nil {
		t.Fatalf("SetClassName: %v", err)
	}

	f1, err := template.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	f2, err := template.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction 2: %v", err)
	}
	sameInContext, err := f1.Value.StrictEquals(f2.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	name, err := f1.Name()
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	isFunction, _ := f1.IsFunction()
	called, ok, err := f1.Call(r.scope, r.undefined(t))
	if err != nil || !ok {
		t.Fatalf("Call: ok=%v err=%v", ok, err)
	}
	callResult := r.valueText(t, called)

	// A second context in the same isolate instantiates its own function.
	scope2, err := r.iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope 2: %v", err)
	}
	ctx2, err := r.iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext 2: %v", err)
	}
	fCtx2, err := template.GetFunction(scope2, ctx2)
	if err != nil {
		t.Fatalf("GetFunction ctx2: %v", err)
	}
	distinct, err := f1.Value.StrictEquals(fCtx2.Value)
	if err != nil {
		t.Fatalf("StrictEquals ctx2: %v", err)
	}
	if err := scope2.Close(); err != nil {
		t.Errorf("scope2.Close: %v", err)
	}
	if err := ctx2.Close(); err != nil {
		t.Errorf("ctx2.Close: %v", err)
	}

	return jsonString(jobj(
		kv("same_in_context", jbool(sameInContext)),
		kv("name", jstr(name)),
		kv("is_function", jbool(isFunction)),
		kv("call_result", jstr(callResult)),
		kv("distinct_across_contexts", jbool(!distinct)),
	))
}

func checkInstancePrototypeAndConstructor(t *testing.T, r *runtime) string {
	t.Helper()
	template, err := r.iso.NewFunctionTemplate(r.scope, cbConstructSeedsInstance, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	if err := template.SetClassName("Gov8Thing"); err != nil {
		t.Fatalf("SetClassName: %v", err)
	}
	instanceTemplate, err := template.InstanceTemplate()
	if err != nil {
		t.Fatalf("InstanceTemplate: %v", err)
	}
	countSet, err := instanceTemplate.SetInternalFieldCount(2)
	if err != nil || !countSet {
		t.Fatalf("SetInternalFieldCount: ok=%v err=%v", countSet, err)
	}
	templateFieldCount, err := instanceTemplate.InternalFieldCount()
	if err != nil {
		t.Fatalf("InternalFieldCount: %v", err)
	}

	prototypeTemplate, err := template.PrototypeTemplate()
	if err != nil {
		t.Fatalf("PrototypeTemplate: %v", err)
	}
	mark, err := r.scope.NewString("on-proto")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if err := prototypeTemplate.Set("protoMark", mark); err != nil {
		t.Fatalf("prototypeTemplate.Set: %v", err)
	}

	fnV, err := template.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	r.seedGlobal(t, "Gov8Thing", fnV.Value)

	protoCheck, _ := r.evalText(t,
		"const t = new Gov8Thing(5); "+
			"[t instanceof Gov8Thing, t.protoMark, "+
			"Object.getPrototypeOf(t) === Gov8Thing.prototype].join('|')")

	seededField := "null"
	tV, ok := r.eval(t, "t")
	if ok {
		count, err := tV.InternalFieldCount()
		if err != nil {
			t.Fatalf("InternalFieldCount: %v", err)
		}
		seeded := "null"
		if field, has, err := tV.GetInternalField(0); err == nil && has {
			if n, nerr := field.NumberValueRaw(); nerr == nil {
				seeded = jsonString(jfloat(n))
			}
		}
		seededField = jsonString(jobj(
			kv("field_count", jint(int64(count))),
			kv("seeded_value", jsonRaw(seeded)),
		))
	}

	plainCall, _ := r.evalText(t, "Gov8Thing(3)")
	callShape, _ := r.evalText(t, "t.call_shape")

	nine, err := r.scope.Int32(9)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	hostInstance := "null"
	if inst, ok, err := fnV.NewInstance(r.scope, nine); err == nil && ok {
		count, _ := inst.InternalFieldCount()
		protoMark, ok, err := inst.GetByName(r.scope, r.ctx, "protoMark")
		markText := ""
		if err == nil && ok {
			markText = r.valueText(t, protoMark)
		}
		hostInstance = jsonString(jobj(
			kv("field_count", jint(int64(count))),
			kv("proto_mark", jstr(markText)),
		))
	}

	return jsonString(jobj(
		kv("template_field_count", jint(int64(templateFieldCount))),
		kv("proto_check", jstr(protoCheck)),
		kv("constructed", jsonRaw(seededField)),
		kv("plain_call", jstr(plainCall)),
		kv("call_shape", jstr(callShape)),
		kv("host_instance", jsonRaw(hostInstance)),
	))
}

func checkObjectTemplateInstances(t *testing.T, r *runtime) string {
	t.Helper()
	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	one, err := r.scope.Int32(1)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	if err := ot.Set("a", one); err != nil {
		t.Fatalf("Set: %v", err)
	}
	i1, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance i1: ok=%v err=%v", ok, err)
	}
	i2, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance i2: ok=%v err=%v", ok, err)
	}
	distinct, err := i1.Value.StrictEquals(i2.Value)
	if err != nil {
		t.Fatalf("StrictEquals: %v", err)
	}
	a1V, ok, err := i1.GetByName(r.scope, r.ctx, "a")
	if err != nil || !ok {
		t.Fatalf("i1.a: ok=%v err=%v", ok, err)
	}
	a2V, ok, err := i2.GetByName(r.scope, r.ctx, "a")
	if err != nil || !ok {
		t.Fatalf("i2.a: ok=%v err=%v", ok, err)
	}
	two, err := r.scope.Int32(2)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	if _, err := i1.SetByName(r.scope, r.ctx, "b", two); err != nil {
		t.Fatalf("i1.SetByName b: %v", err)
	}
	bOnI2, ok, err := i2.GetByName(r.scope, r.ctx, "b")
	if err != nil || !ok {
		t.Fatalf("i2.b: ok=%v err=%v", ok, err)
	}
	bIsUndef, _ := bOnI2.IsUndefined()

	// Instances derived from a function template inherit its prototype.
	ft, err := r.iso.NewFunctionTemplate(r.scope, cbConstantFive, nil)
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
	mark2, err := r.scope.NewString("on-proto")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if err := fpt.Set("protoMark", mark2); err != nil {
		t.Fatalf("fpt.Set: %v", err)
	}
	ctor, err := ft.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	r.seedGlobal(t, "Gov8Base", ctor.Value)
	ot2, err := r.iso.NewObjectTemplateFromFunction(r.scope, ft)
	if err != nil {
		t.Fatalf("NewObjectTemplateFromFunction: %v", err)
	}
	o2, ok, err := ot2.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("ot2.NewInstance: ok=%v err=%v", ok, err)
	}
	r.seedGlobal(t, "o2", o2.Value)
	protoIsFTPrototype, _ := r.evalText(t, "Object.getPrototypeOf(o2) === Gov8Base.prototype")
	inheritedMark, _ := r.evalText(t, "o2.protoMark")

	return jsonString(jobj(
		kv("instances_distinct", jbool(!distinct)),
		kv("i1_a", jstr(r.valueText(t, a1V))),
		kv("i2_a", jstr(r.valueText(t, a2V))),
		kv("i2_b_is_undefined", jbool(bIsUndef)),
		kv("proto_is_ft_prototype", jstr(protoIsFTPrototype)),
		kv("inherited_mark", jstr(inheritedMark)),
	))
}

func checkArgumentsAndReturn(t *testing.T, r *runtime) string {
	t.Helper()
	f, err := r.iso.NewFunction(r.scope, r.ctx, cbAdd, &gov8.FunctionOptions{Length: 2})
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	r.seedGlobal(t, "add", f.Value)

	jsTwoArgs, _ := r.evalText(t, "add(20, 22)")
	jsOneArg, _ := r.evalText(t, "add(7)")
	fnLength, _ := r.evalText(t, "add.length")
	resultIsNumber := false
	if v, ok := r.eval(t, "add(1, 2)"); ok {
		resultIsNumber, _ = v.IsNumber()
	}
	a, err := r.scope.Int32(20)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	b, err := r.scope.Int32(22)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	hostCall := ""
	if res, ok, err := f.Call(r.scope, r.undefined(t), a, b); err == nil && ok {
		hostCall = r.valueText(t, res)
	}

	return jsonString(jobj(
		kv("js_two_args", jstr(jsTwoArgs)),
		kv("js_one_arg", jstr(jsOneArg)),
		kv("fn_length", jstr(fnLength)),
		kv("result_is_number", jbool(resultIsNumber)),
		kv("host_call", jstr(hostCall)),
	))
}

func checkArityAndOutOfBounds(t *testing.T, r *runtime) string {
	t.Helper()
	f, err := r.iso.NewFunction(r.scope, r.ctx, cbArity, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	r.seedGlobal(t, "__arity", f.Value)
	oneArg, _ := r.evalText(t, "__arity(1)")
	threeArgs, _ := r.evalText(t, "__arity(1, 2, 3)")
	return jsonString(jobj(
		kv("one_arg", jstr(oneArg)),
		kv("three_args", jstr(threeArgs)),
	))
}

func checkReceiverAndCallbackData(t *testing.T, r *runtime) string {
	t.Helper()
	recv, err := r.iso.NewFunction(r.scope, r.ctx, cbReceiverMark, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	r.seedGlobal(t, "__recv", recv.Value)

	plain, _ := r.evalText(t, "__recv()")
	method, _ := r.evalText(t,
		"globalThis.obj = { mark: 'M1' }; "+
			"globalThis.obj.method = __recv; "+
			"globalThis.obj.method()")
	explicitReceiver := ""
	if objV, ok := r.eval(t, "globalThis.obj"); ok {
		if res, ok, err := recv.Call(r.scope, objV); err == nil && ok {
			explicitReceiver = r.valueText(t, res)
		}
	}

	payload, err := r.scope.NewString("payload-42")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	withData, err := r.iso.NewFunction(r.scope, r.ctx, cbEchoData, &gov8.FunctionOptions{Data: payload})
	if err != nil {
		t.Fatalf("NewFunction with data: %v", err)
	}
	r.seedGlobal(t, "__withdata", withData.Value)
	dataEcho, _ := r.evalText(t, "__withdata()")

	return jsonString(jobj(
		kv("plain_call_receiver", jstr(plain)),
		kv("method_call_receiver", jstr(method)),
		kv("explicit_receiver", jstr(explicitReceiver)),
		kv("callback_data", jstr(dataEcho)),
	))
}

func checkConstructCallSemantics(t *testing.T, r *runtime) string {
	t.Helper()
	f, err := r.iso.NewFunction(r.scope, r.ctx, cbConstructShape, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	r.seedGlobal(t, "F", f.Value)

	plain, _ := r.evalText(t, "F(0)")
	constructedSeeded, _ := r.evalText(t, "new F(9).seeded")
	constructedShape, _ := r.evalText(t, "new F(9).call_shape")

	hostConstructed := ""
	nine, err := r.scope.Int32(9)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	if inst, ok, err := f.NewInstance(r.scope, nine); err == nil && ok {
		if seeded, ok, err := inst.GetByName(r.scope, r.ctx, "seeded"); err == nil && ok {
			hostConstructed = r.valueText(t, seeded)
		}
	}

	return jsonString(jobj(
		kv("plain", jstr(plain)),
		kv("constructed_seeded", jstr(constructedSeeded)),
		kv("constructed_shape", jstr(constructedShape)),
		kv("host_constructed_seeded", jstr(hostConstructed)),
	))
}

func checkNativeReentersJavascript(t *testing.T, r *runtime) string {
	t.Helper()
	f, err := r.iso.NewFunction(r.scope, r.ctx, cbCallIt, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	r.seedGlobal(t, "__callit", f.Value)

	oneLevel, _ := r.evalText(t, "__callit((x) => x * 6, 7)")
	nested, _ := r.evalText(t, "__callit((x) => __callit((y) => y + 1, x) * 2, 10)")
	return jsonString(jobj(
		kv("one_level", jstr(oneLevel)),
		kv("nested", jstr(nested)),
	))
}

func checkJsExceptionFromNative(t *testing.T, r *runtime) string {
	t.Helper()
	throwErr, err := r.iso.NewFunction(r.scope, r.ctx, cbThrowError, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	r.seedGlobal(t, "__throwError", throwErr.Value)
	throwStr, err := r.iso.NewFunction(r.scope, r.ctx, cbThrowString, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	r.seedGlobal(t, "__throwString", throwStr.Value)

	errorObject := observeThrown(t, r, "__throwError();")
	stringThrow := observeThrown(t, r, "__throwString();")
	jsCatch, _ := r.evalText(t,
		"try { __throwError(); } catch (e) { 'caught:' + e.message; }")
	usableAfter, _ := r.evalText(t, "40 + 2")

	return jsonString(jobj(
		kv("error_object", jsonRaw(errorObject)),
		kv("string_throw", jsonRaw(stringThrow)),
		kv("js_catch", jstr(jsCatch)),
		kv("usable_after", jstr(usableAfter)),
	))
}

func checkNativeDataPropertyGetterSetter(t *testing.T, r *runtime) string {
	t.Helper()
	setterSeen := int64(0)
	seen := false
	accSetter := func(cs *gov8.CallbackScope, _ gov8.PropertyCallbackArguments, value gov8.Value) {
		n, ok, err := cs.IntegerValue(value)
		if err == nil && ok {
			setterSeen = n
			seen = true
		}
	}
	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	if err := ot.SetAccessorWithSetter("prop", accGetter, accSetter); err != nil {
		t.Fatalf("SetAccessorWithSetter: %v", err)
	}
	obj, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	r.seedGlobal(t, "o", obj.Value)

	getterValue, _ := r.evalText(t, "o.prop")
	setterSeenBefore := "null"
	if seen {
		setterSeenBefore = jsonString(jint(setterSeen))
	}
	afterWrite, _ := r.evalText(t, "o.prop = 11; o.prop")
	setterSeenAfter := "null"
	if seen {
		setterSeenAfter = jsonString(jint(setterSeen))
	}
	descriptor, _ := r.evalText(t,
		"JSON.stringify(Object.getOwnPropertyDescriptor(o, 'prop'))")

	return jsonString(jobj(
		kv("getter_value", jstr(getterValue)),
		kv("setter_seen_before", jsonRaw(setterSeenBefore)),
		kv("after_write", jstr(afterWrite)),
		kv("setter_seen_after", jsonRaw(setterSeenAfter)),
		kv("descriptor", jstr(descriptor)),
	))
}

func checkStaticAccessorOnConstructor(t *testing.T, r *runtime) string {
	t.Helper()
	ft, err := r.iso.NewFunctionTemplate(r.scope, cbNoop, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	getterFT, err := r.iso.NewFunctionTemplate(r.scope, staticGetterFn, nil)
	if err != nil {
		t.Fatalf("NewFunctionTemplate getter: %v", err)
	}
	if err := ft.SetAccessorProperty("stat", getterFT, nil, gov8.AttrNone); err != nil {
		t.Fatalf("SetAccessorProperty: %v", err)
	}
	ctor, err := ft.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	r.seedGlobal(t, "C", ctor.Value)

	staticRead, _ := r.evalText(t, "C.stat")
	notOnPrototype, _ := r.evalText(t, "C.prototype.stat")
	return jsonString(jobj(
		kv("static_read", jstr(staticRead)),
		kv("not_on_prototype", jstr(notOnPrototype)),
	))
}

// --- external / internal fields -------------------------------------------------

func checkInternalFieldExternals(t *testing.T, r *runtime) string {
	t.Helper()
	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatalf("NewObjectTemplate: %v", err)
	}
	countSet, err := ot.SetInternalFieldCount(2)
	if err != nil || !countSet {
		t.Fatalf("SetInternalFieldCount: ok=%v err=%v", countSet, err)
	}
	obj, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	fieldCount, err := obj.InternalFieldCount()
	if err != nil {
		t.Fatalf("InternalFieldCount: %v", err)
	}

	// Native heap data referenced through an integer token (never a raw Go
	// pointer); the oracle owns its Box for the whole check and reconstructs
	// the pointee at the end — the Go analog reads through the registry.
	token, err := r.iso.HostRefAdd([]uint32{1234})
	if err != nil {
		t.Fatalf("HostRefAdd: %v", err)
	}
	external, err := r.scope.NewExternal(token)
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}
	externalStored, err := obj.SetInternalField(0, external)
	if err != nil {
		t.Fatalf("SetInternalField 0: %v", err)
	}
	externalRoundtrip := false
	if back, has, err := obj.GetInternalField(0); err == nil && has {
		payload, err := back.ExternalValue()
		if err == nil && payload == token {
			if resolved, ok := r.iso.HostRefGet(payload); ok && resolved != nil {
				externalRoundtrip = true
			}
		}
	}

	ninetyNine, err := r.scope.Int32(99)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	integerStored, err := obj.SetInternalField(1, ninetyNine)
	if err != nil {
		t.Fatalf("SetInternalField 1: %v", err)
	}
	integerValue := int64(-1)
	if back, has, err := obj.GetInternalField(1); err == nil && has {
		integerValue, _ = back.IntegerValueRaw()
	}

	oobSet, err := obj.SetInternalField(2, external)
	if err != nil {
		t.Fatalf("oob SetInternalField: %v", err)
	}
	_, oobHas, err := obj.GetInternalField(2)
	if err != nil {
		t.Fatalf("oob GetInternalField: %v", err)
	}

	obj2, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("NewInstance obj2: ok=%v err=%v", ok, err)
	}
	if err := obj2.SetAlignedPointerInInternalField(0, token, 7); err != nil {
		t.Fatalf("SetAlignedPointerInInternalField: %v", err)
	}
	alignedGot, ok, err := obj2.GetAlignedPointerFromInternalField(0, 7)
	if err != nil || !ok {
		t.Fatalf("GetAlignedPointerFromInternalField: ok=%v err=%v", ok, err)
	}
	alignedRoundtrip := alignedGot == token

	// The host regains ownership and verifies the pointee survived.
	nativeAllocationRoundtrip := false
	if v, ok := r.iso.HostRefGet(token); ok {
		if box, isSlice := v.([]uint32); isSlice && len(box) == 1 && box[0] == 1234 {
			nativeAllocationRoundtrip = true
		}
	}

	return jsonString(jobj(
		kv("count_set", jbool(countSet)),
		kv("field_count", jint(int64(fieldCount))),
		kv("external_stored", jbool(externalStored)),
		kv("external_roundtrip", jbool(externalRoundtrip)),
		kv("integer_stored", jbool(integerStored)),
		kv("integer_value", jint(integerValue)),
		kv("oob_set", jbool(oobSet)),
		kv("oob_get_is_none", jbool(!oobHas)),
		kv("aligned_roundtrip", jbool(alignedRoundtrip)),
		kv("native_allocation_roundtrip", jbool(nativeAllocationRoundtrip)),
	))
}

// slotGuard mirrors the oracle's SlotGuard drop counter.
type slotGuard struct {
	id      int
	dropped *int
}

func (g slotGuard) ReleaseSlotValue() { *g.dropped++ }

func checkIsolateSlotOwnership(t *testing.T, r *runtime) string {
	t.Helper()
	dropped := 0

	// The oracle stores a value that dies with its dedicated isolate; this
	// check releases the host state on the runtime isolate instead — the
	// explicit Go equivalent of the Rust Isolate::drop destructor (Go has
	// no destructors; see ReleaseIsolateHostState's contract).
	firstSet := r.iso.SetSlot("guard", slotGuard{id: 1, dropped: &dropped})
	readBackID := 0
	if v, ok := r.iso.GetSlot("guard"); ok {
		if g, isGuard := v.(slotGuard); isGuard {
			readBackID = g.id
		}
	}

	replaced := r.iso.SetSlot("guard", slotGuard{id: 2, dropped: &dropped})
	dropsAfterReplace := dropped

	removed, _ := r.iso.RemoveSlot("guard")
	removedID := 0
	var removedGuard slotGuard
	hasRemoved := false
	if g, isGuard := removed.(slotGuard); isGuard {
		removedID = g.id
		removedGuard = g
		hasRemoved = true
	}
	_, getAfterRemove := r.iso.GetSlot("guard")
	if hasRemoved {
		// Ownership handed back: the caller releases (drop(removed)).
		removedGuard.ReleaseSlotValue()
	}
	dropsBeforeIsolateDrop := dropped

	r.iso.SetSlot("guard", slotGuard{id: 3, dropped: &dropped})

	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		t.Fatalf("ReleaseIsolateHostState: %v", err)
	}
	dropsAfterIsolateDrop := dropped

	return jsonString(jobj(
		kv("first_set", jbool(firstSet)),
		kv("read_back_id", jint(int64(readBackID))),
		kv("replaced_returns_false", jbool(!replaced)),
		kv("drops_after_replace", jint(int64(dropsAfterReplace))),
		kv("removed_id", jint(int64(removedID))),
		kv("get_after_remove_is_none", jbool(!getAfterRemove)),
		kv("drops_before_isolate_drop", jint(int64(dropsBeforeIsolateDrop))),
		kv("drops_after_isolate_drop", jint(int64(dropsAfterIsolateDrop))),
	))
}

// allHostChecks is the fixed oracle order (rust-oracle/src/checks/host/mod.rs),
// restricted to this slice's 13 checks.
func allHostChecks() []hostCheck {
	return []hostCheck{
		// template construction
		{"template/function_template_construction", checkFunctionTemplateConstruction},
		{"template/instance_prototype_and_constructor", checkInstancePrototypeAndConstructor},
		{"template/object_template_instances", checkObjectTemplateInstances},
		// native function callbacks
		{"callback/arguments_and_return", checkArgumentsAndReturn},
		{"callback/arity_and_out_of_bounds_arguments", checkArityAndOutOfBounds},
		{"callback/receiver_and_callback_data", checkReceiverAndCallbackData},
		{"callback/construct_call_semantics", checkConstructCallSemantics},
		{"callback/native_reenters_javascript", checkNativeReentersJavascript},
		{"callback/js_exception_from_native", checkJsExceptionFromNative},
		// accessor callbacks
		{"accessor/native_data_property_getter_setter", checkNativeDataPropertyGetterSetter},
		{"accessor/static_accessor_on_constructor", checkStaticAccessorOnConstructor},
		// internal fields / external data ownership
		{"external/internal_field_externals", checkInternalFieldExternals},
		{"external/isolate_slot_ownership", checkIsolateSlotOwnership},
	}
}
