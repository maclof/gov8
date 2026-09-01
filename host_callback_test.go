//go:build windows && amd64

package gov8_test

import (
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "gov8"
)

func growNestedCoercionStack(depth int) int {
	var padding [1024]byte
	padding[0] = byte(depth)
	if depth == 0 {
		return int(padding[0])
	}
	return int(padding[0]) + growNestedCoercionStack(depth-1)
}

// Native callback behavior tests, mirroring the pinned Rust host oracle's
// callback checks (rust-oracle/src/checks/host/callbacks.rs). The panic
// boundary is characterized out-of-process, exactly like the oracle's
// tests/callback_panic_boundary.rs.

func cbAdd(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	_ = cs
	a := cbIntOrZero(cs, args, 0)
	b := cbIntOrZero(cs, args, 1)
	_ = rv.SetInt32(int32(a + b))
}

func cbIntOrZero(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, i int) int64 {
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

func TestCallbackArgumentsAndReturn(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	f, err := iso.NewFunction(scope, ctx, cbAdd, &gov8.FunctionOptions{Length: 2})
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "add", f.Value) {
		t.Fatal("seeding add failed")
	}

	if got, ok := evalText(t, ctx, scope, "add(20, 22)"); !ok || got != "42" {
		t.Errorf("add(20, 22) = %q (ok=%v); want 42", got, ok)
	}
	if got, ok := evalText(t, ctx, scope, "add(7)"); !ok || got != "7" {
		t.Errorf("add(7) = %q (ok=%v); want 7 (missing arg coerces to 0)", got, ok)
	}
	if got, ok := evalText(t, ctx, scope, "add.length"); !ok || got != "2" {
		t.Errorf("add.length = %q (ok=%v); want 2", got, ok)
	}
	res, err := eval(t, ctx, scope, "add(1, 2)")
	if err != nil {
		t.Fatalf("eval add(1,2): %v", err)
	}
	if isNum, _ := res.IsNumber(); !isNum {
		t.Errorf("SetInt32 result must surface as a JS number")
	}

	a, _ := scope.Int32(20)
	b, _ := scope.Int32(22)
	hostRes, ok, err := f.Call(scope, mustUndefinedT(t, scope), a, b)
	if err != nil || !ok {
		t.Fatalf("host Call: ok=%v err=%v", ok, err)
	}
	if txt, _ := hostRes.ToString(ctx); txt != "42" {
		t.Errorf("host call result = %q; want 42", txt)
	}
}

func cbArity(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
	oob, err := args.Get(3)
	if err != nil {
		panic(err)
	}
	oobUndef, _ := oob.IsUndefined()
	enc := "len=" + itoa(args.Length()) + ";oob3_undefined=" + b2s(oobUndef)
	encV, err := cs.NewString(enc)
	if err != nil {
		panic(err)
	}
	_ = rv.Set(encV)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

func TestCallbackArityAndOutOfBounds(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	f, err := iso.NewFunction(scope, ctx, cbArity, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "__arity", f.Value) {
		t.Fatal("seeding __arity failed")
	}
	if got, ok := evalText(t, ctx, scope, "__arity(1)"); !ok || got != "len=1;oob3_undefined=true" {
		t.Errorf("one arg: %q (ok=%v)", got, ok)
	}
	if got, ok := evalText(t, ctx, scope, "__arity(1, 2, 3)"); !ok || got != "len=3;oob3_undefined=true" {
		t.Errorf("three args: %q (ok=%v)", got, ok)
	}
}

func TestCallbackReceiverAndData(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	cbReceiverMark := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
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
	recv, err := iso.NewFunction(scope, ctx, cbReceiverMark, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "__recv", recv.Value) {
		t.Fatal("seeding __recv failed")
	}

	// Plain call: sloppy-mode API functions get the global proxy, whose
	// `mark` is undefined.
	if got, ok := evalText(t, ctx, scope, "__recv()"); !ok || got != "undefined" {
		t.Errorf("plain receiver = %q (ok=%v); want undefined", got, ok)
	}
	// Method call: the receiver is the object.
	if got, ok := evalText(t, ctx, scope,
		"globalThis.obj = { mark: 'M1' }; globalThis.obj.method = __recv; globalThis.obj.method()"); !ok || got != "M1" {
		t.Errorf("method receiver = %q (ok=%v); want M1", got, ok)
	}
	// Host-driven call with an explicit receiver.
	objV, err := eval(t, ctx, scope, "globalThis.obj")
	if err != nil {
		t.Fatalf("eval obj: %v", err)
	}
	explicit, ok, err := recv.Call(scope, objV)
	if err != nil || !ok {
		t.Fatalf("explicit receiver call: ok=%v err=%v", ok, err)
	}
	if txt, _ := explicit.ToString(ctx); txt != "M1" {
		t.Errorf("explicit receiver = %q; want M1", txt)
	}

	// Builder data reaches the callback verbatim.
	payload, err := scope.NewString("payload-42")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	cbEchoData := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
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
	withData, err := iso.NewFunction(scope, ctx, cbEchoData, &gov8.FunctionOptions{Data: payload})
	if err != nil {
		t.Fatalf("NewFunction with data: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "__withdata", withData.Value) {
		t.Fatal("seeding __withdata failed")
	}
	if got, ok := evalText(t, ctx, scope, "__withdata()"); !ok || got != "payload-42" {
		t.Errorf("callback data = %q (ok=%v); want payload-42", got, ok)
	}
}

func cbConstructShape(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
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
		if _, err := cs.ObjectSet(this.Value, "seeded", first); err != nil {
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

func TestCallbackConstructCallSemantics(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	f, err := iso.NewFunction(scope, ctx, cbConstructShape, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "F", f.Value) {
		t.Fatal("seeding F failed")
	}

	if got, ok := evalText(t, ctx, scope, "F(0)"); !ok ||
		got != "construct=false;new_target_function=false;new_target_undefined=true" {
		t.Errorf("plain = %q (ok=%v)", got, ok)
	}
	if got, ok := evalText(t, ctx, scope, "new F(9).seeded"); !ok || got != "9" {
		t.Errorf("new F(9).seeded = %q (ok=%v); want 9", got, ok)
	}
	if got, ok := evalText(t, ctx, scope, "new F(9).call_shape"); !ok ||
		got != "construct=true;new_target_function=true;new_target_undefined=false" {
		t.Errorf("construct shape = %q (ok=%v)", got, ok)
	}

	nine, _ := scope.Int32(9)
	inst, ok, err := f.NewInstance(scope, nine)
	if err != nil || !ok {
		t.Fatalf("NewInstance: ok=%v err=%v", ok, err)
	}
	seeded, ok, err := inst.GetByName(scope, ctx, "seeded")
	if err != nil || !ok {
		t.Fatalf("inst.GetByName seeded: ok=%v err=%v", ok, err)
	}
	if txt, _ := seeded.ToString(ctx); txt != "9" {
		t.Errorf("host constructed seeded = %q; want 9", txt)
	}
}

func TestCallbackNativeReentry(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	cbCallIt := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		fn, err := args.Get(0)
		if err != nil {
			panic(err)
		}
		arg, err := args.Get(1)
		if err != nil {
			panic(err)
		}
		undef, err := cs.Scope().Undefined()
		if err != nil {
			panic(err)
		}
		res, ok, err := cs.CallFunction(fn, undef, []gov8.Value{arg})
		if err != nil || !ok {
			return // leaves the default undefined return, like the oracle
		}
		_ = rv.Set(res)
	}
	f, err := iso.NewFunction(scope, ctx, cbCallIt, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "__callit", f.Value) {
		t.Fatal("seeding __callit failed")
	}

	if got, ok := evalText(t, ctx, scope, "__callit((x) => x * 6, 7)"); !ok || got != "42" {
		t.Errorf("one level = %q (ok=%v); want 42", got, ok)
	}
	if got, ok := evalText(t, ctx, scope,
		"__callit((x) => __callit((y) => y + 1, x) * 2, 10)"); !ok || got != "22" {
		t.Errorf("nested = %q (ok=%v); want 22", got, ok)
	}
}

func TestCallbackNestedCoercionConversions(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	valueOfCalls := 0
	lastInteger := int64(0)
	valueOf, err := iso.NewFunction(scope, ctx, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		_ = growNestedCoercionStack(64)
		valueOfCalls++
		_ = rv.SetUint32(41)
	}, nil)
	if err != nil {
		t.Fatalf("NewFunction(valueOf): %v", err)
	}
	coerce, err := iso.NewFunction(scope, ctx, func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		value, getErr := args.Get(0)
		if getErr != nil {
			return
		}
		number, ok, conversionErr := cs.IntegerValue(value)
		if conversionErr == nil && ok {
			lastInteger = number
			_ = rv.SetInt32(int32(number + 1))
		}
	}, nil)
	if err != nil {
		t.Fatalf("NewFunction(coerce): %v", err)
	}
	if !seedGlobal(t, ctx, scope, "valueOfHost", valueOf.Value) ||
		!seedGlobal(t, ctx, scope, "coerceHost", coerce.Value) {
		t.Fatal("seeding nested-coercion functions failed")
	}

	if got, ok := evalText(t, ctx, scope, "coerceHost({valueOf: valueOfHost})"); !ok || got != "42" {
		t.Fatalf("CallbackScope.IntegerValue nested coercion = %q, %v; want 42, true", got, ok)
	}
	if lastInteger != 41 {
		t.Fatalf("CallbackScope.IntegerValue nested result = %d, want 41", lastInteger)
	}
	if _, ok := evalText(t, ctx, scope, "coerceHost(-9223372036854775808)"); !ok {
		t.Fatal("CallbackScope.IntegerValue minimum int64 conversion failed")
	}
	if lastInteger != math.MinInt64 {
		t.Fatalf("CallbackScope.IntegerValue minimum = %d, want %d", lastInteger, int64(math.MinInt64))
	}
	object, err := eval(t, ctx, scope, "({valueOf: valueOfHost})")
	if err != nil {
		t.Fatalf("create coercible object: %v", err)
	}
	got, ok, err := object.Uint32Value(ctx)
	if err != nil || !ok || got != 41 {
		t.Fatalf("Value.Uint32Value nested coercion = %d, %v, %v; want 41, true, nil", got, ok, err)
	}
	maximum, err := eval(t, ctx, scope, "4294967295")
	if err != nil {
		t.Fatalf("create maximum uint32: %v", err)
	}
	got, ok, err = maximum.Uint32Value(ctx)
	if err != nil || !ok || got != math.MaxUint32 {
		t.Fatalf("Value.Uint32Value maximum = %d, %v, %v; want %d, true, nil", got, ok, err, uint32(math.MaxUint32))
	}
	if valueOfCalls != 2 {
		t.Fatalf("valueOf callback calls = %d, want 2", valueOfCalls)
	}
}

func TestCallbackThrowFromNative(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	cbThrowError := func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		exc, err := cs.NewError("native-boom")
		if err != nil {
			panic(err)
		}
		if err := cs.ThrowException(exc); err != nil {
			panic(err)
		}
	}
	throwErr, err := iso.NewFunction(scope, ctx, cbThrowError, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "__throwError", throwErr.Value) {
		t.Fatal("seeding __throwError failed")
	}
	cbThrowString := func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		msg, err := cs.NewString("native-string-boom")
		if err != nil {
			panic(err)
		}
		if err := cs.ThrowException(msg); err != nil {
			panic(err)
		}
	}
	throwStr, err := iso.NewFunction(scope, ctx, cbThrowString, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	if !seedGlobal(t, ctx, scope, "__throwString", throwStr.Value) {
		t.Fatal("seeding __throwString failed")
	}

	observe := func(source string) (compileOK, runOK, hasCaught, canContinue, excIsString bool, message, excText string) {
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		script, cerr := ctx.Compile(scope, source, tc)
		compileOK = cerr == nil
		if compileOK {
			_, rerr := script.Run(scope, tc)
			runOK = rerr == nil
			_ = script.Close()
		}
		hasCaught, _ = tc.HasCaught()
		canContinue, _ = tc.CanContinue()
		message, _ = tc.MessageText(scope, ctx)
		excText, _ = tc.ExceptionText(scope, ctx)
		excIsString, _ = tc.ExceptionIsString()
		return
	}

	compileOK, runOK, hasCaught, canContinue, excIsString, message, excText := observe("__throwError();")
	if !compileOK || runOK || !hasCaught || !canContinue {
		t.Errorf("error object: compile=%v run=%v caught=%v continue=%v",
			compileOK, runOK, hasCaught, canContinue)
	}
	if message != "Uncaught Error: native-boom" || excText != "Error: native-boom" || excIsString {
		t.Errorf("error object: message=%q exc=%q isString=%v", message, excText, excIsString)
	}

	compileOK, runOK, hasCaught, canContinue, excIsString, message, excText = observe("__throwString();")
	if !compileOK || runOK || !hasCaught || !canContinue {
		t.Errorf("string throw: compile=%v run=%v caught=%v continue=%v",
			compileOK, runOK, hasCaught, canContinue)
	}
	if message != "Uncaught native-string-boom" || excText != "native-string-boom" || !excIsString {
		t.Errorf("string throw: message=%q exc=%q isString=%v", message, excText, excIsString)
	}

	// A JS try/catch observes exactly the scheduled exception.
	if got, ok := evalText(t, ctx, scope,
		"try { __throwError(); } catch (e) { 'caught:' + e.message; }"); !ok || got != "caught:native-boom" {
		t.Errorf("js catch = %q (ok=%v); want caught:native-boom", got, ok)
	}
	// The isolate is fully usable afterwards.
	if got, ok := evalText(t, ctx, scope, "40 + 2"); !ok || got != "42" {
		t.Errorf("usable after = %q (ok=%v); want 42", got, ok)
	}
}

// TestCallbackDataOutlivesCreationScope verifies the documented ownership of
// callback data: the shim copies the embedder data into a Global held by the
// dispatch context, so a function kept alive by the JS context stays callable
// with intact Data() even after the scope that created the data value closed.
func TestCallbackDataOutlivesCreationScope(t *testing.T) {
	iso, ctx, _ := newTestRuntime(t)

	cbEchoData := func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
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

	// Creation scope: closes right after setup.
	setupScope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	payload, err := setupScope.NewString("persistent-payload")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	f, err := iso.NewFunction(setupScope, ctx, cbEchoData, &gov8.FunctionOptions{Data: payload})
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	global, err := ctx.GlobalObject(setupScope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if ok, err := global.SetByName(setupScope, ctx, "__dataFn", f.Value); err != nil || !ok {
		t.Fatalf("SetByName: ok=%v err=%v", ok, err)
	}
	if err := setupScope.Close(); err != nil {
		t.Fatalf("setupScope.Close: %v", err)
	}

	// The creation-scope data value and function wire are gone; the function
	// itself lives on in the context and its callback data must resolve.
	callScope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope 2: %v", err)
	}
	if _, err := ctx.Compile(callScope, "", nil); err != nil {
		t.Fatalf("warmup compile: %v", err)
	}
	res, err := eval(t, ctx, callScope, "__dataFn()")
	if err != nil {
		t.Fatalf("call after scope close: %v", err)
	}
	if txt, err := res.ToString(ctx); err != nil || txt != "persistent-payload" {
		t.Errorf("data after scope close = %q (err=%v); want persistent-payload", txt, err)
	}
	if err := callScope.Close(); err != nil {
		t.Errorf("callScope.Close: %v", err)
	}
}

// TestCallbackPanicAbortsProcess characterizes the Go panic boundary out of
// process: a panic inside a native callback must terminate the whole process
// (fail-fast), never return into the host, and print the panic message — the
// observable equivalent of the pinned oracle's callback_panic_boundary test.
func TestCallbackPanicAbortsProcess(t *testing.T) {
	if os.Getenv("GOV8_HOST_TEST_CALLBACK_PANIC_CHILD") == "1" {
		callbackPanicChild(t)
		return // never reached: the child aborts
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=TestCallbackPanicAbortsProcess", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOV8_HOST_TEST_CALLBACK_PANIC_CHILD=1")
	out, err := cmd.CombinedOutput()
	stdoutStderr := string(out)

	if !strings.Contains(stdoutStderr, "marker:before-call") {
		t.Errorf("host code before the call must run; output:\n%s", stdoutStderr)
	}
	if !strings.Contains(stdoutStderr, "marker:callback-entered") {
		t.Errorf("the callback must be entered; output:\n%s", stdoutStderr)
	}
	if !strings.Contains(stdoutStderr, "host-callback-panic") {
		t.Errorf("the panic message must be printed; output:\n%s", stdoutStderr)
	}
	if strings.Contains(stdoutStderr, "marker:after-call") {
		t.Errorf("host code after the call must never run; output:\n%s", stdoutStderr)
	}
	if err == nil {
		t.Fatalf("the process must not exit cleanly; output:\n%s", stdoutStderr)
	}
	var ee *exec.ExitError
	if !asExitError(err, &ee) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	// Fail-fast abort, not a clean exit: 0xC0000409 on Windows, matching the
	// pinned oracle's callback panic boundary (Rust's ExitStatus reports the
	// same code sign-extended as -1073740791; Go's ExitCode reports the
	// unsigned representation).
	if got := ee.ExitCode(); got != 3221226505 {
		t.Errorf("exit code = %d; want 3221226505 (0xC0000409); output:\n%s", got, stdoutStderr)
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// callbackPanicChild runs in the aborting subprocess (still inside the test
// framework; TestMain has initialized the engine).
func callbackPanicChild(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	cbPanic := func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
		_ = cs
		os.Stderr.WriteString("marker:callback-entered\n")
		panic("host-callback-panic")
	}
	f, err := iso.NewFunction(scope, ctx, cbPanic, nil)
	if err != nil {
		os.Stderr.WriteString("marker:new-function-failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stderr.WriteString("marker:before-call\n")
	_, _, _ = f.Call(scope, mustUndefinedT(t, scope))
	// Unreachable: the panic boundary aborts the process.
	os.Stderr.WriteString("marker:after-call\n")
}
