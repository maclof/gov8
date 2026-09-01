//go:build windows && amd64

package gov8_test

import (
	"strings"
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

// Script-origin, compiler, message and heap unit tests, mirroring the
// remaining oracle checks one for one.

const benchFibIIFESource = "(function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); })(12)"

// --- script origins ---------------------------------------------------------------

// TestScriptOriginRoundtrip mirrors core-advanced/script/origin_roundtrip.
func TestScriptOriginRoundtrip(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	origin := &gov8.Origin{
		ResourceName:        "app.js",
		ScriptID:            777,
		SourceMapURL:        "map.url",
		IsOpaque:            true,
		IsSharedCrossOrigin: true,
	}
	script, err := ctx.CompileWithOrigin(scope, "1 + 1", origin, nil)
	if err != nil {
		t.Fatalf("CompileWithOrigin: %v", err)
	}
	defer func() { _ = script.Close() }()

	v, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	n, _, err := v.IntegerValue(ctx)
	if err != nil || n != 2 {
		t.Errorf("run value = %d (%v), want 2", n, err)
	}

	id, err := script.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if id == 777 {
		t.Error("script adopted the origin's declared id")
	}
	if id <= 0 {
		t.Errorf("script id = %d, want positive", id)
	}
	plain, err := ctx.Compile(scope, "2 + 2", nil)
	if err != nil {
		t.Fatalf("plain compile: %v", err)
	}
	defer func() { _ = plain.Close() }()
	plainID, err := plain.ID()
	if err != nil {
		t.Fatalf("plain ID: %v", err)
	}
	if plainID == id {
		t.Error("distinct scripts share an id")
	}
	// Origin getters are the Go struct fields the engine was handed (the
	// origin is embedder-owned end to end).
	if origin.ResourceName != "app.js" || origin.SourceMapURL != "map.url" || origin.ScriptID != 777 {
		t.Error("origin fields changed")
	}
}

// TestScriptOriginShiftsExceptionPositions mirrors
// core-advanced/script/origin_shifts_exception_positions.
func TestScriptOriginShiftsExceptionPositions(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	origin := &gov8.Origin{ResourceName: "shift.js", LineOffset: 100, ColumnOffset: 5}
	script, cerr := ctx.CompileWithOrigin(scope, "\nthrow new Error('boom')\n", origin, tc)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	defer func() { _ = script.Close() }()
	if _, rerr := script.Run(scope, tc); rerr == nil {
		t.Fatal("run unexpectedly succeeded")
	}
	caught, err := tc.HasCaught()
	if err != nil || !caught {
		t.Fatalf("HasCaught = %v (%v)", caught, err)
	}
	msg, ok, err := tc.Message(scope)
	if err != nil || !ok {
		t.Fatalf("Message = %v (%v)", ok, err)
	}
	defer func() { _ = tc.Reset() }()

	assertMessage(t, scope, ctx, msg, messageExpectation{
		text:          "Uncaught Error: boom",
		line:          102,
		startPosition: 1,
		endPosition:   2,
		startColumn:   0,
		endColumn:     1,
		sourceLine:    "throw new Error('boom')",
		resourceName:  "shift.js",
		errorLevel:    8,
		isOpaque:      false,
		shared:        false,
	})
}

type messageExpectation struct {
	text          string
	line          int32
	startPosition int64
	endPosition   int64
	startColumn   int64
	endColumn     int64
	sourceLine    string
	resourceName  string
	errorLevel    int64
	isOpaque      bool
	shared        bool
}

func assertMessage(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, msg *gov8.Message, want messageExpectation) {
	t.Helper()
	if text, err := msg.Text(ctx); err != nil || text != want.text {
		t.Errorf("text = %q (%v), want %q", text, err, want.text)
	}
	if line, ok, err := msg.LineNumber(ctx); err != nil || !ok || line != want.line {
		t.Errorf("line = %d/%v (%v), want %d", line, ok, err, want.line)
	}
	if sp, _ := msg.StartPosition(); sp != want.startPosition {
		t.Errorf("start position = %d, want %d", sp, want.startPosition)
	}
	if ep, _ := msg.EndPosition(); ep != want.endPosition {
		t.Errorf("end position = %d, want %d", ep, want.endPosition)
	}
	if sc, _ := msg.StartColumn(); sc != want.startColumn {
		t.Errorf("start column = %d, want %d", sc, want.startColumn)
	}
	if ec, _ := msg.EndColumn(); ec != want.endColumn {
		t.Errorf("end column = %d, want %d", ec, want.endColumn)
	}
	sourceLine, ok, err := msg.SourceLine(ctx)
	if err != nil || !ok || sourceLine != want.sourceLine {
		t.Errorf("source line = %q/%v (%v), want %q", sourceLine, ok, err, want.sourceLine)
	}
	if name, err := msg.ResourceName(ctx); err != nil || name != want.resourceName {
		t.Errorf("resource name = %q (%v), want %q", name, err, want.resourceName)
	}
	if level, _ := msg.ErrorLevel(); level != want.errorLevel {
		t.Errorf("error level = %d, want %d", level, want.errorLevel)
	}
	if opaque, _ := msg.IsOpaque(); opaque != want.isOpaque {
		t.Errorf("is opaque = %v", opaque)
	}
	if shared, _ := msg.IsSharedCrossOrigin(); shared != want.shared {
		t.Errorf("is shared cross origin = %v", shared)
	}
}

// --- unbound scripts and the compiler ---------------------------------------------

// TestUnboundRebind mirrors core-advanced/script/unbound_rebind.
func TestUnboundRebind(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx1 := newCtx(t, iso)
	defer func() { _ = ctx1.Close() }()
	ctx2 := newCtx(t, iso)
	defer func() { _ = ctx2.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	cs1, err := ctx1.Enter()
	if err != nil {
		t.Fatalf("Enter ctx1: %v", err)
	}
	defer func() { _ = cs1.Close() }()

	const source = "globalThis.n = (globalThis.n | 0) + 1; globalThis.n"
	script1, err := ctx1.Compile(scope, source, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	defer func() { _ = script1.Close() }()
	unbound, err := script1.Unbound()
	if err != nil {
		t.Fatalf("Unbound: %v", err)
	}
	defer func() { _ = unbound.Close() }()
	scriptID, _ := script1.ID()
	unboundID, err := unbound.ID()
	if err != nil {
		t.Fatalf("unbound ID: %v", err)
	}
	if scriptID != unboundID {
		t.Errorf("ids differ: script %d unbound %d", scriptID, unboundID)
	}

	runBound := func() int64 {
		t.Helper()
		bound, err := unbound.Bind(scope)
		if err != nil {
			t.Fatalf("Bind: %v", err)
		}
		v, err := bound.Run(ctx1, scope, nil)
		if err != nil {
			t.Fatalf("bound run: %v", err)
		}
		n, _, err := v.IntegerValue(ctx1)
		if err != nil {
			t.Fatalf("int: %v", err)
		}
		return n
	}

	if got := runBound(); got != 1 {
		t.Errorf("ctx1 first = %d", got)
	}
	if got := runBound(); got != 2 {
		t.Errorf("ctx1 second = %d", got)
	}

	// Bind into ctx2 with fully separated globals.
	cs2, err := ctx2.Enter()
	if err != nil {
		t.Fatalf("Enter ctx2: %v", err)
	}
	bound2, err := unbound.Bind(scope)
	if err != nil {
		t.Fatalf("Bind ctx2: %v", err)
	}
	v2, err := bound2.Run(ctx2, scope, nil)
	if err != nil {
		t.Fatalf("bound ctx2 run: %v", err)
	}
	n2, _, _ := v2.IntegerValue(ctx2)
	if err := cs2.Close(); err != nil {
		t.Fatalf("Close ctx2 scope: %v", err)
	}
	if n2 != 1 {
		t.Errorf("ctx2 first = %d", n2)
	}

	after, ok := evalInt(t, scope, ctx1, "globalThis.n")
	if !ok || after != 2 {
		t.Errorf("ctx1 after ctx2 run = %d/%v", after, ok)
	}
}

// TestCompilerOptionsAndUnbound mirrors
// core-advanced/script/compiler_options_and_unbound.
func TestCompilerOptionsAndUnbound(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	unbound, err := ctx.CompileUnbound(scope, "1 + 2",
		&gov8.Origin{ResourceName: "eager.js"}, gov8.OptEagerCompile, nil)
	if err != nil {
		t.Fatalf("CompileUnbound eager: %v", err)
	}
	defer func() { _ = unbound.Close() }()
	id, err := unbound.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if id == 0 {
		t.Error("unbound script kept the unassigned origin id 0")
	}

	tag, err := gov8.CachedDataVersionTag()
	if err != nil {
		t.Fatalf("CachedDataVersionTag: %v", err)
	}
	if tag != 3252425384 {
		t.Errorf("cached data version tag = %d, want 3252425384", tag)
	}

	// CompileFunction with declared parameters, then call it.
	fn, err := ctx.CompileFunction(scope, "return a * b;", []string{"a", "b"}, nil)
	if err != nil {
		t.Fatalf("CompileFunction: %v", err)
	}
	result, err := gov8.CallFunction(ctx, scope, fn, caUndefined(t, scope),
		[]gov8.Value{caNumber(t, scope, 6), caNumber(t, scope, 7)}, nil)
	if err != nil {
		t.Fatalf("CallFunction: %v", err)
	}
	n, _, err := result.IntegerValue(ctx)
	if err != nil || n != 42 {
		t.Errorf("compile function call = %d (%v), want 42", n, err)
	}
}

func mustValue(t *testing.T, v gov8.Value, err error) gov8.Value {
	t.Helper()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	return v
}

// TestCodeCacheRoundtrip mirrors core-advanced/script/code_cache_roundtrip:
// produce in one isolate, consume in a fresh one, run = 144.
func TestCodeCacheRoundtrip(t *testing.T) {
	const source = benchFibIIFESource
	origin := &gov8.Origin{ResourceName: "cached.js"}

	var cache []byte
	func() {
		producer := newIso(t)
		defer func() { _ = producer.Close() }()
		pctx := newCtx(t, producer)
		defer func() { _ = pctx.Close() }()
		pscope := newScope(t, producer)
		defer func() { _ = pscope.Close() }()
		unbound, err := pctx.CompileUnbound(pscope, source, origin, gov8.OptNoCompileOptions, nil)
		if err != nil {
			t.Fatalf("producer compile: %v", err)
		}
		defer func() { _ = unbound.Close() }()
		cache, err = unbound.CreateCodeCache()
		if err != nil {
			t.Fatalf("CreateCodeCache: %v", err)
		}
	}()
	if len(cache) == 0 {
		t.Fatal("empty code cache")
	}

	consumer := newIso(t)
	defer func() { _ = consumer.Close() }()
	cctx := newCtx(t, consumer)
	defer func() { _ = cctx.Close() }()
	cscope := newScope(t, consumer)
	defer func() { _ = cscope.Close() }()

	// A healthy cache passes the sanity precheck.
	if status, err := consumer.CheckCodeCache(cache); err != nil || status != 0 {
		t.Fatalf("CheckCodeCache healthy = %d (%v)", status, err)
	}
	script, rejected, err := cctx.CompileCached(cscope, source, origin, cache, nil)
	if err != nil {
		t.Fatalf("CompileCached: %v", err)
	}
	defer func() { _ = script.Close() }()
	if rejected {
		t.Error("healthy cache reported rejected")
	}
	v, err := script.Run(cscope, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	n, _, _ := v.IntegerValue(cctx)
	if n != 144 {
		t.Errorf("run value = %d, want 144", n)
	}

	// Header-level corruption is rejected by the Go prevalidation without
	// reaching the (upstream fatal) deserializer.
	corrupt := append([]byte(nil), cache...)
	corrupt[0] ^= 0xFF
	if status, err := consumer.CheckCodeCache(corrupt); err != nil || status == 0 {
		t.Fatalf("CheckCodeCache corrupt header = %d (%v), want non-zero", status, err)
	}
	if _, _, err := cctx.CompileCached(cscope, source, origin, corrupt, nil); err == nil {
		t.Fatal("corrupt header cache was not rejected")
	}
}

// --- messages ----------------------------------------------------------------------

// TestMessageExceptionDetails mirrors
// core-advanced/message/exception_details_with_origin.
func TestMessageExceptionDetails(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()

	script, cerr := ctx.CompileWithOrigin(scope,
		"function boom() {\n  null.f();\n}\nboom();\n",
		&gov8.Origin{ResourceName: "detail.js"}, tc)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	defer func() { _ = script.Close() }()
	if _, rerr := script.Run(scope, tc); rerr == nil {
		t.Fatal("run did not throw")
	}
	caught, _ := tc.HasCaught()
	if !caught {
		t.Fatal("runtime error must produce a message")
	}
	msg, ok, err := tc.Message(scope)
	if err != nil || !ok {
		t.Fatalf("Message = %v (%v)", ok, err)
	}
	defer func() { _ = tc.Reset() }()

	assertMessage(t, scope, ctx, msg, messageExpectation{
		text:          "Uncaught TypeError: Cannot read properties of null (reading 'f')",
		line:          2,
		startPosition: 25,
		endPosition:   26,
		startColumn:   7,
		endColumn:     8,
		sourceLine:    "  null.f();",
		resourceName:  "detail.js",
		errorLevel:    8,
		isOpaque:      false,
		shared:        false,
	})

	if text, err := tc.ExceptionText(scope, ctx); err != nil ||
		text != "TypeError: Cannot read properties of null (reading 'f')" {
		t.Errorf("exception text = %q (%v)", text, err)
	}
	if trace, ok, err := msg.StackTrace(); err != nil || ok || trace != nil {
		t.Errorf("message stack trace = %v/%v (%v), want none", trace, ok, err)
	}
}

// framesCapture receives the frame JSON the native callback builds (plain
// data; the callback runs synchronously on this goroutine).
var framesCapture []map[string]any

// TestMessageCurrentStackFrames mirrors
// core-advanced/message/current_stack_frames.
func TestMessageCurrentStackFrames(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	host, err := iso.NewFunction(scope, ctx, func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		s := cs.Scope()
		frames := []map[string]any(nil)
		trace, ok, err := s.CurrentStackTrace(16)
		if err == nil && ok {
			count, _ := trace.FrameCount()
			for i := 0; i < count; i++ {
				frame, err := trace.Frame(i)
				if err != nil {
					break
				}
				rec := map[string]any{}
				if name, ok, _ := frame.FunctionName(); ok {
					rec["function"] = name
				} else {
					rec["function"] = nil
				}
				rec["line"], _ = frame.LineNumber()
				rec["column"], _ = frame.Column()
				if script, ok, _ := frame.ScriptName(); ok {
					rec["script"] = script
				} else {
					rec["script"] = nil
				}
				sid, _ := frame.ScriptID()
				rec["script_id_positive"] = sid > 0
				rec["is_eval"], _ = frame.IsEval()
				rec["is_constructor"], _ = frame.IsConstructor()
				rec["is_wasm"], _ = frame.IsWasm()
				rec["is_user_javascript"], _ = frame.IsUserJavaScript()
				frames = append(frames, rec)
			}
		}
		current := ""
		if name, ok, _ := s.CurrentScriptNameOrSourceURL(); ok {
			current = name
		}
		framesCapture = []map[string]any{{
			"frame_count":         len(frames),
			"frames":              frames,
			"current_script_name": current,
		}}
		if err := rv.SetInt32(1); err != nil {
			t.Errorf("SetInt32: %v", err)
		}
	}, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}

	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(scope, ctx, "host", host.Value); err != nil {
		t.Fatalf("SetByName: %v", err)
	}

	framesCapture = nil
	script, cerr := ctx.CompileWithOrigin(scope,
		"function target(n) { return host(n); }\nglobalThis.result = target(9);",
		&gov8.Origin{ResourceName: "frames.js"}, nil)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	if _, rerr := script.Run(scope, nil); rerr != nil {
		t.Fatalf("run: %v", rerr)
	}
	_ = script.Close()
	if len(framesCapture) != 1 {
		t.Fatal("callback did not run")
	}
	got := framesCapture[0]
	if got["frame_count"] != 2 {
		t.Fatalf("frame count = %v, want 2", got["frame_count"])
	}
	frames := got["frames"].([]map[string]any)
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(frames))
	}
	first, second := frames[0], frames[1]
	if first["function"] != "target" || second["function"] != nil {
		t.Errorf("function names = %v / %v", first["function"], second["function"])
	}
	if first["line"] != int64(1) || first["column"] != int64(29) {
		t.Errorf("first frame position = %v:%v, want 1:29", first["line"], first["column"])
	}
	if second["line"] != int64(2) || second["column"] != int64(21) {
		t.Errorf("second frame position = %v:%v, want 2:21", second["line"], second["column"])
	}
	if first["script"] != "frames.js" || second["script"] != "frames.js" {
		t.Errorf("script names = %v / %v", first["script"], second["script"])
	}
	if first["script_id_positive"] != true || first["is_eval"] != false ||
		first["is_constructor"] != false || first["is_wasm"] != false ||
		first["is_user_javascript"] != true {
		t.Errorf("first frame flags = %+v", first)
	}
	if got["current_script_name"] != "frames.js" {
		t.Errorf("current script name = %v", got["current_script_name"])
	}
}

// TestUncaughtCaptureAndCaptureStackTrace mirrors
// core-advanced/message/uncaught_capture_and_capture_stack_trace.
func TestUncaughtCaptureAndCaptureStackTrace(t *testing.T) {
	// (a)+(b): plain-object capture and the native-error trace gap.
	capturedOK := false
	plainStackFirstLine := ""
	nativeTraceNone := false
	func() {
		iso := newIso(t)
		defer func() { _ = iso.Close() }()
		ctx := newCtx(t, iso)
		defer func() { _ = ctx.Close() }()
		scope := newScope(t, iso)
		defer func() { _ = scope.Close() }()
		// The context is entered for the block: the engine resolves the
		// Error prototype through the entered context.
		cs, err := ctx.Enter()
		if err != nil {
			t.Fatalf("Enter: %v", err)
		}
		defer func() { _ = cs.Close() }()

		obj, err := scope.NewObject(ctx)
		if err != nil {
			t.Fatalf("NewObject: %v", err)
		}
		captured, err := gov8.CaptureStackTrace(ctx, scope, obj.Value)
		if err != nil {
			t.Fatalf("CaptureStackTrace: %v", err)
		}
		capturedOK = captured
		stackVal, found, err := obj.GetByName(scope, ctx, "stack")
		if err != nil || !found {
			t.Fatalf("stack property = %v (%v)", found, err)
		}
		text, err := stackVal.ToString(ctx)
		if err != nil {
			t.Fatalf("stack text: %v", err)
		}
		plainStackFirstLine = strings.SplitN(text, "\n", 2)[0]

		nativeError, err := scope.NewError("native-err")
		if err != nil {
			t.Fatalf("NewError: %v", err)
		}
		_, ok, err := gov8.ExceptionStackTrace(scope, nativeError)
		if err != nil {
			t.Fatalf("ExceptionStackTrace: %v", err)
		}
		nativeTraceNone = !ok
	}()

	// (c) default: an uncaught exception's message carries no trace.
	defaultUncaughtNone := false
	func() {
		iso := newIso(t)
		defer func() { _ = iso.Close() }()
		ctx := newCtx(t, iso)
		defer func() { _ = ctx.Close() }()
		scope := newScope(t, iso)
		defer func() { _ = scope.Close() }()
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		script, cerr := ctx.Compile(scope, "function f1() { throw new Error('x'); } f1();", tc)
		if cerr != nil {
			t.Fatalf("compile: %v", cerr)
		}
		defer func() { _ = script.Close() }()
		if _, rerr := script.Run(scope, tc); rerr == nil {
			t.Fatal("run unexpectedly succeeded")
		}
		msg, ok, err := tc.Message(scope)
		if err != nil || !ok {
			t.Fatalf("Message = %v (%v)", ok, err)
		}
		_, hasTrace, err := msg.StackTrace()
		if err != nil {
			t.Fatalf("StackTrace: %v", err)
		}
		defaultUncaughtNone = !hasTrace
	}()

	// (d) enabling capture with a frame limit attaches a truncated trace.
	enabledCount := 0
	var enabledNames []string
	func() {
		iso := newIso(t)
		defer func() { _ = iso.Close() }()
		if err := iso.SetCaptureStackTraceForUncaughtExceptions(true, 3); err != nil {
			t.Fatalf("SetCaptureStackTraceForUncaughtExceptions: %v", err)
		}
		ctx := newCtx(t, iso)
		defer func() { _ = ctx.Close() }()
		scope := newScope(t, iso)
		defer func() { _ = scope.Close() }()
		tc, err := iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		const src = "function d1() { d2(); }\nfunction d2() { d3(); }\nfunction d3() { throw new Error('deep'); }\nd1();"
		script, cerr := ctx.Compile(scope, src, tc)
		if cerr != nil {
			t.Fatalf("compile: %v", cerr)
		}
		defer func() { _ = script.Close() }()
		if _, rerr := script.Run(scope, tc); rerr == nil {
			t.Fatal("run unexpectedly succeeded")
		}
		msg, ok, err := tc.Message(scope)
		if err != nil || !ok {
			t.Fatalf("Message = %v (%v)", ok, err)
		}
		trace, hasTrace, err := msg.StackTrace()
		if err != nil || !hasTrace {
			t.Fatalf("enabled trace missing (%v/%v)", hasTrace, err)
		}
		count, _ := trace.FrameCount()
		enabledCount = count
		for i := 0; i < count; i++ {
			frame, err := trace.Frame(i)
			if err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
			if name, ok, _ := frame.FunctionName(); ok {
				enabledNames = append(enabledNames, name)
			} else {
				enabledNames = append(enabledNames, "")
			}
		}
	}()

	if !capturedOK {
		t.Error("capture on plain object failed")
	}
	if plainStackFirstLine != "Error" {
		t.Errorf("plain stack first line = %q, want Error", plainStackFirstLine)
	}
	if !nativeTraceNone {
		t.Error("native error unexpectedly has a stack trace")
	}
	if !defaultUncaughtNone {
		t.Error("default uncaught message has a stack trace")
	}
	if enabledCount != 3 {
		t.Errorf("enabled trace frame count = %d, want 3", enabledCount)
	}
	if len(enabledNames) != 3 || enabledNames[0] != "d3" || enabledNames[1] != "d2" || enabledNames[2] != "d1" {
		t.Errorf("enabled trace names = %v", enabledNames)
	}
}

// --- termination ---------------------------------------------------------------------

// TestTerminateSameThreadLifecycle mirrors
// core-advanced/terminate/same_thread_flag_lifecycle.
func TestTerminateSameThreadLifecycle(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	handle := iso.ThreadSafeHandle()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	if initial := handle.IsExecutionTerminating(); initial {
		t.Error("initially terminating")
	}
	if !handle.TerminateExecution() {
		t.Fatal("terminate request refused")
	}
	if after := handle.IsExecutionTerminating(); after {
		t.Error("terminating flag visible before delivery")
	}

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	script, cerr := ctx.Compile(scope, "1 + 1", tc)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	defer func() { _ = script.Close() }()
	if _, rerr := script.Run(scope, tc); rerr == nil {
		t.Fatal("run unexpectedly succeeded")
	}
	if after := handle.IsExecutionTerminating(); !after {
		t.Error("flag not visible after delivery")
	}
	// The flag self-clears after the abort fully unwinds; the rerun in the
	// same TryCatch works.
	if n, ok := evalInt(t, scope, ctx, "40 + 2"); !ok || n != 42 {
		t.Errorf("rerun = %d/%v, want 42", n, ok)
	}
	if err := tc.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	// The oracle reads the TryCatch flags AFTER Reset: the caught
	// termination clears while can_continue stays false.
	if caught, _ := tc.HasCaught(); caught {
		t.Error("same-thread termination left the trycatch caught after reset")
	}
	if terminated, _ := tc.HasTerminated(); terminated {
		t.Error("trycatch reports terminated after reset")
	}
	if can, _ := tc.CanContinue(); can {
		t.Error("can continue after termination")
	}
	if after := handle.IsExecutionTerminating(); after {
		t.Error("still terminating after unwind")
	}
	if !handle.CancelTerminateExecution() {
		t.Error("cancel refused")
	}
	if after := handle.IsExecutionTerminating(); after {
		t.Error("still terminating after cancel")
	}
	if n, ok := evalInt(t, scope, ctx, "40 + 2"); !ok || n != 42 {
		t.Errorf("recovered = %d/%v, want 42", n, ok)
	}
}

// TestTerminateCancelBeforeDelivery mirrors
// core-advanced/terminate/cancel_before_delivery.
func TestTerminateCancelBeforeDelivery(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()

	if err := iso.TerminateExecution(); err != nil {
		t.Fatalf("TerminateExecution: %v", err)
	}
	if err := iso.CancelTerminateExecution(); err != nil {
		t.Fatalf("CancelTerminateExecution: %v", err)
	}
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	defer func() { _ = tc.Close() }()
	if n, ok := evalInt(t, scope, ctx, "6 + 1"); !ok || n != 7 {
		t.Errorf("result = %d/%v, want 7", n, ok)
	}
	if caught, _ := tc.HasCaught(); caught {
		t.Error("trycatch caught after cancel")
	}
}

// --- heap -------------------------------------------------------------------------------

// TestHeapStatisticsInvariants mirrors core-advanced/heap/statistics_invariants.
func TestHeapStatisticsInvariants(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newScope(t, iso)
	defer func() { _ = scope.Close() }()
	if _, err := scope.NewObject(ctx); err != nil {
		t.Fatalf("probe object: %v", err)
	}
	_ = scope.Close()

	stats, err := iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	if stats.UsedHeapSize == 0 {
		t.Error("used heap size is zero")
	}
	if stats.TotalHeapSize < stats.UsedHeapSize {
		t.Error("total < used")
	}
	if stats.TotalAvailableSize == 0 {
		t.Error("available size is zero")
	}
	if stats.HeapSizeLimit == 0 {
		t.Error("heap limit is zero")
	}
	if stats.NumberOfNativeContexts < 1 {
		t.Error("native contexts < 1")
	}
	if stats.NumberOfDetachedContexts != 0 {
		t.Error("detached contexts != 0")
	}
	if stats.DoesZapGarbage {
		t.Error("zap garbage enabled")
	}
	if stats.TotalGlobalHandlesSize < stats.UsedGlobalHandlesSize {
		t.Error("total global handles < used")
	}
	if stats.TotalAllocatedBytes == 0 {
		t.Error("total allocated bytes is zero")
	}
	if external := stats.ExternalMemory; external != 0 {
		t.Errorf("initial external memory = %d, want 0", external)
	}

	up, err := iso.AdjustAmountOfExternalAllocatedMemory(1024)
	if err != nil {
		t.Fatalf("adjust up: %v", err)
	}
	if up != 1024 {
		t.Errorf("adjust up returned %d, want 1024", up)
	}
	// The pinned build's HeapStatistics::external_memory does not reflect
	// the adjust calls; the RETURN value tracks the total.
	if stats2, _ := iso.GetHeapStatistics(); stats2.ExternalMemory != 0 {
		t.Errorf("external after up = %d, want 0", stats2.ExternalMemory)
	}
	down, err := iso.AdjustAmountOfExternalAllocatedMemory(-1024)
	if err != nil {
		t.Fatalf("adjust down: %v", err)
	}
	if down != 0 {
		t.Errorf("adjust down returned %d, want 0", down)
	}
	if stats3, _ := iso.GetHeapStatistics(); stats3.ExternalMemory != 0 {
		t.Errorf("external after down = %d, want 0", stats3.ExternalMemory)
	}

	spaces, err := iso.NumberOfHeapSpaces()
	if err != nil {
		t.Fatalf("NumberOfHeapSpaces: %v", err)
	}
	if spaces != 13 {
		t.Errorf("heap spaces = %d, want 13", spaces)
	}
}

// TestGCNotificationCallbacks mirrors
// core-advanced/heap/gc_notification_callbacks.
func TestGCNotificationCallbacks(t *testing.T) {
	iso := newIso(t)
	defer func() { _ = iso.Close() }()
	ctx := newCtx(t, iso)
	defer func() { _ = ctx.Close() }()
	_ = ctx

	var prologueCount, epilogueCount atomic.Int64
	var prologueType, epilogueType atomic.Int64
	var prologueFlags, epilogueFlags atomic.Int64

	prologue, err := iso.AddGCPrologueCallback(func(gcType gov8.GCType, flags gov8.GCCallbackFlags) {
		prologueCount.Add(1)
		prologueType.Store(int64(gcType))
		prologueFlags.Store(int64(flags))
	}, gov8.GCTypeMarkSweepCompact)
	if err != nil {
		t.Fatalf("AddGCPrologueCallback: %v", err)
	}
	epilogue, err := iso.AddGCEpilogueCallback(func(gcType gov8.GCType, flags gov8.GCCallbackFlags) {
		epilogueCount.Add(1)
		epilogueType.Store(int64(gcType))
		epilogueFlags.Store(int64(flags))
	}, gov8.GCTypeMarkSweepCompact)
	if err != nil {
		t.Fatalf("AddGCEpilogueCallback: %v", err)
	}

	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}

	if got := prologueCount.Load(); got != 2 {
		t.Errorf("prologue count after first gc = %d, want 2", got)
	}
	if got := epilogueCount.Load(); got != 2 {
		t.Errorf("epilogue count after first gc = %d, want 2", got)
	}
	if got := prologueType.Load(); got != 4 {
		t.Errorf("prologue gc type = %d, want 4", got)
	}
	if got := epilogueType.Load(); got != 4 {
		t.Errorf("epilogue gc type = %d, want 4", got)
	}
	if got := prologueFlags.Load(); got != 16 {
		t.Errorf("prologue flags = %d, want 16", got)
	}
	if got := epilogueFlags.Load(); got != 16 {
		t.Errorf("epilogue flags = %d, want 16", got)
	}

	if err := iso.RemoveGCPrologueCallback(prologue); err != nil {
		t.Fatalf("RemoveGCPrologueCallback: %v", err)
	}
	if err := iso.RemoveGCEpilogueCallback(epilogue); err != nil {
		t.Fatalf("RemoveGCEpilogueCallback: %v", err)
	}
	if err := iso.RemoveGCPrologueCallback(prologue); err == nil {
		t.Error("double removal succeeded")
	}

	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("second LowMemoryNotification: %v", err)
	}
	if got := prologueCount.Load(); got != 2 {
		t.Errorf("prologue count after removal = %d, want 2", got)
	}
	if got := epilogueCount.Load(); got != 2 {
		t.Errorf("epilogue count after removal = %d, want 2", got)
	}
}

func caUndefined(t *testing.T, scope *gov8.Scope) gov8.Value {
	t.Helper()
	v, err := scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	return v
}
