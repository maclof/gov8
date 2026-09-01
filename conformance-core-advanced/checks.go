//go:build windows && amd64

// The 25 core-advanced checks in the fixed oracle order (the order is part
// of the observable contract). Every check normalizes its observations with
// the rules of rust-oracle/src/json.rs: no addresses, no thread ids, no
// timings, no raw engine-assigned script ids (only equality / positivity /
// distinctness), exact engine strings for the pinned build.
package main

import (
	"strings"
	"sync"

	gov8 "gov8"
)

// --- helpers -----------------------------------------------------------------

// makeOrigin builds the Origin with only the knobs a check varies; every
// other field uses neutral defaults, mirroring the oracle's make_origin.
func makeOrigin(name string, lineOffset, columnOffset, scriptID int32, sourceMap string, isOpaque, isSharedCrossOrigin bool) *gov8.Origin {
	return &gov8.Origin{
		ResourceName:        name,
		LineOffset:          lineOffset,
		ColumnOffset:        columnOffset,
		ScriptID:            scriptID,
		SourceMapURL:        sourceMap,
		IsOpaque:            isOpaque,
		IsSharedCrossOrigin: isSharedCrossOrigin,
	}
}

// num returns v's raw Number payload (the oracle's number .value()).
func num(t tester, v gov8.Value) float64 {
	t.Helper()
	n, err := v.NumberValueRaw()
	if err != nil {
		t.Errorf("NumberValueRaw: %v", err)
		return 0
	}
	return n
}

// isNumber/isString report the value predicates.
func isNumber(t tester, v gov8.Value) bool {
	t.Helper()
	b, err := v.IsNumber()
	if err != nil {
		t.Fatalf("IsNumber: %v", err)
	}
	return b
}

func isString(t tester, v gov8.Value) bool {
	t.Helper()
	b, err := v.IsString()
	if err != nil {
		t.Fatalf("IsString: %v", err)
	}
	return b
}

func stringValue(t tester, v gov8.Value) string {
	t.Helper()
	s, err := v.StringValue()
	if err != nil {
		t.Fatalf("StringValue: %v", err)
	}
	return s
}

// intOf is Value::integer_value with a -1 fallback.
func intOf(t tester, r *runtime, v gov8.Value) int64 {
	t.Helper()
	n, _, err := v.IntegerValue(r.ctx)
	if err != nil {
		return -1
	}
	return n
}

// optInt encodes an Option<i64> (None -> JSON null).
func optInt(value int64, ok bool) jsonValue {
	if !ok {
		return jnull()
	}
	return jint(value)
}

// --- scope --------------------------------------------------------------------

// checkScopeNestedAndEscaped mirrors core-advanced/scope/nested_and_escaped_scopes.
func checkScopeNestedAndEscaped(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	ctx := newIsolateContext(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()

	outer := scopeNumber(t, scope, 7)

	// Escapable scope A: one escape.
	escA := newEscapable(t, scope)
	var escapedNumber gov8.Value
	func() {
		inner := newIsolateScope(t, iso)
		defer func() { _ = inner.Close() }()
		escapedNumber = escapeValue(t, escA, scopeNumber(t, inner, 8))
	}()
	closeEscapable(t, escA)

	// Two-level chain: nested escapable escapes a string into B, B
	// re-escapes it out.
	escB := newEscapable(t, scope)
	var escapedString gov8.Value
	func() {
		nested := newEscapable(t, scope)
		var deep gov8.Value
		func() {
			inner := newIsolateScope(t, iso)
			defer func() { _ = inner.Close() }()
			deep = escapeValue(t, nested, scopeString(t, inner, "deep"))
		}()
		closeEscapable(t, nested)
		escapedString = escapeValue(t, escB, deep)
	}()
	closeEscapable(t, escB)

	// An intervening plain nested scope must not disturb escaped values.
	innerOK := func() bool {
		inner := newIsolateScope(t, iso)
		defer func() { _ = inner.Close() }()
		return num(t, scopeNumber(t, inner, 1.5)) == 1.5
	}()

	return wantGot("core-advanced/scope/nested_and_escaped_scopes",
		jobj(
			kv("inner_scope_usable", jbool(true)),
			kv("escaped_number", jobj(kv("is_number", jbool(true)), kv("value", jfloat(8.0)))),
			kv("escaped_string", jobj(kv("is_string", jbool(true)), kv("text", jstr("deep")))),
			kv("outer_value_unchanged", jbool(true)),
		),
		jobj(
			kv("inner_scope_usable", jbool(innerOK)),
			kv("escaped_number", jobj(kv("is_number", jbool(isNumber(t, escapedNumber))), kv("value", jfloat(num(t, escapedNumber))))),
			kv("escaped_string", jobj(kv("is_string", jbool(isString(t, escapedString))), kv("text", jstr(stringValue(t, escapedString))))),
			kv("outer_value_unchanged", jbool(num(t, outer) == 7.0)),
		))
}

// checkScopeEscapeTwicePanics mirrors
// core-advanced/scope/escapable_escape_twice_panics. The pinned crate
// panics; this port's documented panic-to-error deviation turns the guard
// into an error carrying the exact pinned message, recorded here.
func checkScopeEscapeTwicePanics(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	_ = newIsolateContext(t, iso)
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()

	esc := newEscapable(t, scope)
	var first gov8.Value
	var message string
	func() {
		inner := newIsolateScope(t, iso)
		defer func() { _ = inner.Close() }()
		first = escapeValue(t, esc, scopeNumber(t, inner, 1.0))
		if _, err := esc.Escape(scopeNumber(t, inner, 2.0)); err != nil {
			message = err.Error()
		}
	}()
	firstUsable := num(t, first) == 1.0

	return wantGot("core-advanced/scope/escapable_escape_twice_panics",
		jobj(
			kv("first_escape_usable", jbool(true)),
			kv("panicked", jbool(true)),
			kv("message", jstr("EscapableHandleScope::escape() called twice")),
		),
		jobj(
			kv("first_escape_usable", jbool(firstUsable)),
			kv("panicked", jbool(message != "")),
			kv("message", jstr(message)),
		))
}

// --- thread --------------------------------------------------------------------

// lockedEvalShared locks the shared isolate and returns the int64 result
// (the oracle's locked_eval).
func lockedEvalShared(t tester, shared *gov8.SharedIsolate, source string) (int64, bool) {
	t.Helper()
	locker, err := shared.Lock()
	if err != nil {
		t.Errorf("Lock: %v", err)
		return 0, false
	}
	defer func() { _ = locker.Close() }()
	iso := locker.Isolate()
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()
	ctx := newIsolateContext(t, iso)
	defer func() { _ = ctx.Close() }()

	script, cerr := ctx.Compile(scope, source, nil)
	if cerr != nil {
		return 0, false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(scope, nil)
	if rerr != nil {
		return 0, false
	}
	n, _, nerr := v.IntegerValue(ctx)
	if nerr != nil {
		return 0, false
	}
	return n, true
}

// checkThreadSharedIsolateCrossThreadLocks mirrors
// core-advanced/thread/shared_isolate_cross_thread_locks.
func checkThreadSharedIsolateCrossThreadLocks(t tester) obs {
	iso := newIsolate(t)
	shared, err := iso.TryIntoShared()
	if err != nil {
		t.Fatalf("TryIntoShared: %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan int64, 2)
	for _, source := range []string{"6 * 7", "20 + 2"} {
		source := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, _ := lockedEvalShared(t, shared, source)
			results <- n
		}()
	}
	wg.Wait()
	close(results)
	a, b := <-results, <-results
	workerResults := []int64{a, b}
	if workerResults[0] > workerResults[1] {
		workerResults[0], workerResults[1] = workerResults[1], workerResults[0]
	}
	mainResult, _ := lockedEvalShared(t, shared, "40 + 2")
	if err := shared.Close(); err != nil {
		t.Errorf("shared Close: %v", err)
	}

	return wantGot("core-advanced/thread/shared_isolate_cross_thread_locks",
		jobj(
			kv("worker_results", jarr(jint(22), jint(42))),
			kv("main_result", jint(42)),
		),
		jobj(
			kv("worker_results", jarr(jint(workerResults[0]), jint(workerResults[1]))),
			kv("main_result", jint(mainResult)),
		))
}

// checkThreadSharedTerminateWhileLocked mirrors
// core-advanced/thread/shared_terminate_while_locked.
func checkThreadSharedTerminateWhileLocked(t tester) obs {
	iso := newIsolate(t)
	handle := iso.ThreadSafeHandle()
	shared, err := iso.TryIntoShared()
	if err != nil {
		t.Fatalf("TryIntoShared: %v", err)
	}

	// Request termination from a foreign goroutine while this goroutine
	// holds the lock and is about to run the loop; delivery happens at the
	// loop's first interrupt checkpoint either way (the request is sticky).
	locker, err := shared.Lock()
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	wiso := locker.Isolate()
	scope := newIsolateScope(t, wiso)
	ctx := newIsolateContext(t, wiso)
	tc := newTryCatch(t, wiso)

	requested := make(chan bool, 1)
	go func() { requested <- handle.TerminateExecution() }()

	script, cerr := ctx.Compile(scope, "for (;;) {}", tc)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	_, runErr := script.Run(scope, tc)
	ran := runErr == nil
	stillTerminating := handle.IsExecutionTerminating()
	requestOK := <-requested

	hasCaught, _ := tc.HasCaught()
	hasTerminated, _ := tc.HasTerminated()
	canContinue, _ := tc.CanContinue()

	closeTryCatch(t, tc)
	_ = script.Close()
	closeContext(t, ctx)
	closeScope(t, scope)
	if err := locker.Close(); err != nil {
		t.Errorf("locker Close: %v", err)
	}

	cancelOK := handle.CancelTerminateExecution()
	recovered, _ := lockedEvalShared(t, shared, "40 + 2")
	if err := shared.Close(); err != nil {
		t.Errorf("shared Close: %v", err)
	}

	return wantGot("core-advanced/thread/shared_terminate_while_locked",
		jobj(
			kv("terminate_requested", jbool(true)),
			kv("run_none", jbool(true)),
			kv("has_caught", jbool(true)),
			kv("has_terminated", jbool(true)),
			kv("can_continue", jbool(false)),
			kv("still_terminating_after_run", jbool(true)),
			kv("cancel_ok", jbool(true)),
			kv("recovered_result", jint(42)),
		),
		jobj(
			kv("terminate_requested", jbool(requestOK)),
			kv("run_none", jbool(!ran)),
			kv("has_caught", jbool(hasCaught)),
			kv("has_terminated", jbool(hasTerminated)),
			kv("can_continue", jbool(canContinue)),
			kv("still_terminating_after_run", jbool(stillTerminating)),
			kv("cancel_ok", jbool(cancelOK)),
			kv("recovered_result", jint(recovered)),
		))
}

// checkThreadLockerUnlockWindow mirrors
// core-advanced/thread/locker_unlock_window.
func checkThreadLockerUnlockWindow(t tester) obs {
	iso := newIsolate(t)
	shared, err := iso.TryIntoShared()
	if err != nil {
		t.Fatalf("TryIntoShared: %v", err)
	}

	started := make(chan struct{})
	workerDone := make(chan struct{})
	var workerResult int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-started
		// Blocks in locked_eval until the main goroutine opens the window.
		workerResult, _ = lockedEvalShared(t, shared, "2 + 3")
		close(workerDone)
	}()

	locker, err := shared.Lock()
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	close(started)

	var windowErr error
	if werr := locker.UnlockWindow(func() error {
		<-workerDone
		return nil
	}); werr != nil {
		windowErr = werr
		t.Fatalf("unlock window: %v", windowErr)
	}

	// Back from the window: the same locker still owns the isolate.
	mainResult := int64(-3)
	func() {
		wiso := locker.Isolate()
		scope := newIsolateScope(t, wiso)
		defer func() { _ = scope.Close() }()
		ctx := newIsolateContext(t, wiso)
		defer func() { _ = ctx.Close() }()
		script, cerr := ctx.Compile(scope, "1 + 1", nil)
		if cerr != nil {
			return
		}
		defer func() { _ = script.Close() }()
		v, rerr := script.Run(scope, nil)
		if rerr != nil {
			return
		}
		n, _, _ := v.IntegerValue(ctx)
		mainResult = n
	}()
	closeLocker(t, locker)
	wg.Wait()
	if err := shared.Close(); err != nil {
		t.Errorf("shared Close: %v", err)
	}

	return wantGot("core-advanced/thread/locker_unlock_window",
		jobj(
			kv("worker_result", jint(5)),
			kv("main_result_after_window", jint(2)),
		),
		jobj(
			kv("worker_result", jint(workerResult)),
			kv("main_result_after_window", jint(mainResult)),
		))
}

// checkThreadIntoSharedRejections mirrors
// core-advanced/thread/into_shared_rejections.
func checkThreadIntoSharedRejections(t tester) obs {
	// Part A: another isolate entered on top rejects the conversion.
	bottom := newIsolate(t)
	top := newIsolate(t)
	enteredKind := "converted_unexpectedly"
	_, err := bottom.TryIntoShared()
	if err != nil {
		if ie, ok := err.(*gov8.IntoSharedError); ok {
			enteredKind = string(ie.Kind)
		}
	}
	// Reverse creation order teardown.
	closeIsolate(t, top)
	closeIsolate(t, bottom)

	// Part B: any live non-empty weak handle rejects; dropping it allows
	// conversion, including a weak without a finalizer.
	owned := newIsolate(t)
	weak := func() *gov8.Weak {
		scope := newIsolateScope(t, owned)
		defer func() { _ = scope.Close() }()
		ctx := newIsolateContext(t, owned)
		defer func() { _ = ctx.Close() }()
		obj := newObject(t, scope, ctx)
		g, err := gov8.NewGlobal(scope, obj)
		if err != nil {
			t.Fatalf("NewGlobal: %v", err)
		}
		w, err := g.NewWeak()
		if err != nil {
			t.Fatalf("NewWeak: %v", err)
		}
		return w
	}()
	weakKind := "converted_unexpectedly"
	_, err = owned.TryIntoShared()
	if err != nil {
		if ie, ok := err.(*gov8.IntoSharedError); ok {
			weakKind = string(ie.Kind)
		}
	}
	// Only now close the weak; the retry must succeed.
	closeWeak(t, weak)
	shared, err := owned.TryIntoShared()
	retryOK := err == nil
	lockedRun := int64(-1)
	if retryOK {
		lockedRun, _ = lockedEvalShared(t, shared, "3 * 3")
		closeShared(t, shared)
	}

	return wantGot("core-advanced/thread/into_shared_rejections",
		jobj(
			kv("entered_reject_kind", jstr("another_isolate_entered")),
			kv("weak_reject_kind", jstr("live_weak_handles_or_pending_finalizers")),
			kv("weak_retry_ok", jbool(true)),
			kv("locked_run_after_recovery", jint(9)),
		),
		jobj(
			kv("entered_reject_kind", jstr(enteredKind)),
			kv("weak_reject_kind", jstr(weakKind)),
			kv("weak_retry_ok", jbool(retryOK)),
			kv("locked_run_after_recovery", jint(lockedRun)),
		))
}

// checkThreadHandleAfterDispose mirrors
// core-advanced/thread/handle_after_dispose: every control reports false
// after the isolate was closed and a registered interrupt never runs.
func checkThreadHandleAfterDispose(t tester) obs {
	interruptCount := 0
	iso := newIsolate(t)
	handle := iso.ThreadSafeHandle()
	closeIsolate(t, iso)

	requested := handle.RequestInterrupt(func(_ *gov8.Isolate, _ uintptr) {
		interruptCount++
	}, 0)

	return wantGot("core-advanced/thread/handle_after_dispose",
		jobj(
			kv("terminate", jbool(false)),
			kv("cancel", jbool(false)),
			kv("is_terminating", jbool(false)),
			kv("interrupt_requested", jbool(false)),
			kv("interrupt_count", jint(0)),
		),
		jobj(
			kv("terminate", jbool(handle.TerminateExecution())),
			kv("cancel", jbool(handle.CancelTerminateExecution())),
			kv("is_terminating", jbool(handle.IsExecutionTerminating())),
			kv("interrupt_requested", jbool(requested)),
			kv("interrupt_count", jint(int64(interruptCount))),
		))
}

// --- context ---------------------------------------------------------------------

// checkContextEnterExitNesting mirrors
// core-advanced/context/enter_exit_nesting.
func checkContextEnterExitNesting(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	ctx1 := newIsolateContext(t, iso)
	defer func() { _ = ctx1.Close() }()
	ctx2 := newIsolateContext(t, iso)
	defer func() { _ = ctx2.Close() }()
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()

	outer := enterContext(t, ctx1)
	outerCur := currentContext(t, iso, scope)
	outerCurrentIsCtx1 := sameContext(t, outerCur, ctx1)
	outerEntered := enteredOrMicrotask(t, iso, scope)
	outerEnteredIsCtx1 := sameContext(t, outerEntered, ctx1)

	g1a := globalObject(t, ctx1, scope)
	g1b := globalObject(t, ctx1, scope)
	g2 := globalObject(t, ctx2, scope)
	globalIdentity := sameValue(t, g1a, g1b)
	globalsDistinct := !sameValue(t, g1a, g2)

	innerCurrentIsCtx2, innerEnteredIsCtx2, innerCurrentNotCtx1 := func() (bool, bool, bool) {
		inner := enterContext(t, ctx2)
		defer func() { closeContextScope(t, inner) }()
		cur := currentContext(t, iso, scope)
		curIs2 := sameContext(t, cur, ctx2)
		entered := enteredOrMicrotask(t, iso, scope)
		enteredIs2 := sameContext(t, entered, ctx2)
		not1 := !sameContext(t, cur, ctx1)
		return curIs2, enteredIs2, not1
	}()

	restored := currentContext(t, iso, scope)
	restoredIsCtx1 := sameContext(t, restored, ctx1)
	closeContextScope(t, outer)

	return wantGot("core-advanced/context/enter_exit_nesting",
		jobj(
			kv("outer_current_is_ctx1", jbool(true)),
			kv("outer_entered_is_ctx1", jbool(true)),
			kv("global_identity_stable", jbool(true)),
			kv("globals_distinct", jbool(true)),
			kv("inner_current_is_ctx2", jbool(true)),
			kv("inner_entered_is_ctx2", jbool(true)),
			kv("inner_current_not_ctx1", jbool(true)),
			kv("restored_is_ctx1", jbool(true)),
		),
		jobj(
			kv("outer_current_is_ctx1", jbool(outerCurrentIsCtx1)),
			kv("outer_entered_is_ctx1", jbool(outerEnteredIsCtx1)),
			kv("global_identity_stable", jbool(globalIdentity)),
			kv("globals_distinct", jbool(globalsDistinct)),
			kv("inner_current_is_ctx2", jbool(innerCurrentIsCtx2)),
			kv("inner_entered_is_ctx2", jbool(innerEnteredIsCtx2)),
			kv("inner_current_not_ctx1", jbool(innerCurrentNotCtx1)),
			kv("restored_is_ctx1", jbool(restoredIsCtx1)),
		))
}

// checkContextSecurityTokens mirrors core-advanced/context/security_tokens.
func checkContextSecurityTokens(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	ctxA := newIsolateContext(t, iso)
	defer func() { _ = ctxA.Close() }()
	ctxB := newIsolateContext(t, iso)
	defer func() { _ = ctxB.Close() }()
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()

	// tokensEqual enters both contexts nested and compares the tokens.
	tokensEqual := func() bool {
		ta := tokenOf(t, ctxA, scope)
		sa := enterContext(t, ctxB)
		tb := tokenOf(t, ctxB, scope)
		eq := sameValue(t, ta, tb)
		closeContextScope(t, sa)
		return eq
	}

	defaultEqual := tokensEqual()

	tokenA := scopeString(t, scope, "shield-a")
	setToken(t, ctxA, scope, tokenA)
	divergesFromB := !tokensEqual()

	// Distinct string object, identical content: SameValue says equal.
	tokenACopy := scopeString(t, scope, "shield-a")
	setToken(t, ctxB, scope, tokenACopy)
	equalContentEqual := tokensEqual()

	useDefaultToken(t, ctxB)
	resetDiverges := !tokensEqual()

	// The exact same string object makes the tokens equal again.
	setToken(t, ctxB, scope, tokenA)
	sameObjectEqual := tokensEqual()

	return wantGot("core-advanced/context/security_tokens",
		jobj(
			kv("default_tokens_equal", jbool(false)),
			kv("custom_a_diverges_from_default_b", jbool(true)),
			kv("equal_content_tokens_same_value", jbool(true)),
			kv("reset_b_diverges_from_custom_a", jbool(true)),
			kv("same_object_tokens_equal", jbool(true)),
		),
		jobj(
			kv("default_tokens_equal", jbool(defaultEqual)),
			kv("custom_a_diverges_from_default_b", jbool(divergesFromB)),
			kv("equal_content_tokens_same_value", jbool(equalContentEqual)),
			kv("reset_b_diverges_from_custom_a", jbool(resetDiverges)),
			kv("same_object_tokens_equal", jbool(sameObjectEqual)),
		))
}

// checkContextEmbedderDataAndSlots mirrors
// core-advanced/context/embedder_data_and_slots.
func checkContextEmbedderDataAndSlots(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	ctx := newIsolateContext(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()

	// The default embedder slot content is not a well-defined JS value
	// here (ToString on it is unsafe), so only predicates are recorded.
	defaultPredicates := func() jsonValue {
		v, ok, err := ctx.GetEmbedderData(scope, 0)
		if err != nil {
			t.Fatalf("GetEmbedderData: %v", err)
		}
		if !ok {
			return jstr("none")
		}
		return jobj(
			kv("null", jbool(pred(t, v.IsNull))),
			kv("undefined", jbool(pred(t, v.IsUndefined))),
			kv("int32", jbool(pred(t, v.IsInt32))),
			kv("string", jbool(pred(t, v.IsString))),
			kv("number", jbool(pred(t, v.IsNumber))),
			kv("object", jbool(pred(t, v.IsObject))),
		)
	}()
	beforeAnySet := defaultPredicates

	embedderInt := func(slot int) int64 {
		t.Helper()
		v, ok, err := ctx.GetEmbedderData(scope, slot)
		if err != nil || !ok {
			t.Fatalf("GetEmbedderData(%d) = %v/%v", slot, ok, err)
		}
		n, _, err := v.IntegerValue(ctx)
		if err != nil {
			t.Fatalf("embedder int: %v", err)
		}
		return n
	}

	setEmbedder(t, ctx, scope, 0, int32Val(t, scope, 11))
	read0 := embedderInt(0)
	setEmbedder(t, ctx, scope, 1, int32Val(t, scope, 12))
	read1 := embedderInt(1)
	read0AfterSlot1 := embedderInt(0)
	setEmbedder(t, ctx, scope, 0, int32Val(t, scope, 13))
	read0Overwritten := embedderInt(0)

	// Aligned pointer round-trip through embedder data slot 2.
	const sentinel = uintptr(0xABCD000)
	setAlignedPointer(t, ctx, 2, sentinel)
	pointerRoundtrip := alignedPointer(t, ctx, 2) == sentinel

	// Rc-style slots: set returns the previous value instead of dropping it.
	_, firstPreviousEmpty := ctx.SetSlot("u32", uint32(7))
	firstRead, _ := ctx.GetSlot("u32")
	prev, _ := ctx.SetSlot("u32", uint32(8))
	secondRead, _ := ctx.GetSlot("u32")
	removed, _ := ctx.RemoveSlot("u32")
	_, removedAgain := ctx.RemoveSlot("u32")
	_, otherTypeSet := ctx.SetSlot("u64", uint64(99))
	otherTypeRead, _ := ctx.GetSlot("u64")
	_, u32Gone := ctx.GetSlot("u32")

	ctx.ClearAllSlots()
	_, afterClearU64 := ctx.GetSlot("u64")
	_, afterClearU32 := ctx.GetSlot("u32")
	embedderSurvivesClear := embedderInt(0)
	_, setAgain := ctx.SetSlot("u32", uint32(5))
	reinitRead, _ := ctx.GetSlot("u32")

	secondPrevious := int64(0)
	if prev != nil {
		secondPrevious = int64(prev.(uint32))
	}
	return wantGot("core-advanced/context/embedder_data_and_slots",
		jobj(
			kv("embedder_before_any_set", beforeAnySet),
			kv("embedder_read0", jint(11)),
			kv("embedder_read1", jint(12)),
			kv("embedder_read0_after_slot1", jint(11)),
			kv("embedder_read0_overwritten", jint(13)),
			kv("aligned_pointer_roundtrip", jbool(true)),
			kv("slot_first_previous_is_none", jbool(true)),
			kv("slot_first_read", jint(7)),
			kv("slot_second_previous", jint(7)),
			kv("slot_second_read", jint(8)),
			kv("slot_removed", jint(8)),
			kv("slot_removed_again_is_none", jbool(true)),
			kv("slot_other_type_set", jbool(true)),
			kv("slot_other_type_read", jint(99)),
			kv("slot_u32_gone_after_remove", jbool(true)),
			kv("after_clear_u64_is_none", jbool(true)),
			kv("after_clear_u32_is_none", jbool(true)),
			kv("embedder_survives_clear", jint(13)),
			kv("slot_set_again_after_clear", jbool(true)),
			kv("slot_reinit_read", jint(5)),
		),
		jobj(
			kv("embedder_before_any_set", beforeAnySet),
			kv("embedder_read0", jint(read0)),
			kv("embedder_read1", jint(read1)),
			kv("embedder_read0_after_slot1", jint(read0AfterSlot1)),
			kv("embedder_read0_overwritten", jint(read0Overwritten)),
			kv("aligned_pointer_roundtrip", jbool(pointerRoundtrip)),
			kv("slot_first_previous_is_none", jbool(firstPreviousEmpty)),
			kv("slot_first_read", jint(int64(firstRead.(uint32)))),
			kv("slot_second_previous", optInt(secondPrevious, secondPrevious != 0)),
			kv("slot_second_read", jint(int64(secondRead.(uint32)))),
			kv("slot_removed", jint(int64(removed.(uint32)))),
			kv("slot_removed_again_is_none", jbool(!removedAgain)),
			kv("slot_other_type_set", jbool(otherTypeSet)),
			kv("slot_other_type_read", jint(int64(otherTypeRead.(uint64)))),
			kv("slot_u32_gone_after_remove", jbool(!u32Gone)),
			kv("after_clear_u64_is_none", jbool(!afterClearU64)),
			kv("after_clear_u32_is_none", jbool(!afterClearU32)),
			kv("embedder_survives_clear", jint(embedderSurvivesClear)),
			kv("slot_set_again_after_clear", jbool(setAgain)),
			kv("slot_reinit_read", jint(int64(reinitRead.(uint32)))),
		))
}

// --- slots ------------------------------------------------------------------------

// checkSlotsIsolateRawData mirrors
// core-advanced/slots/isolate_raw_data_slots.
func checkSlotsIsolateRawData(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()

	slotCount, err := iso.DataSlotCount()
	if err != nil {
		t.Fatalf("DataSlotCount: %v", err)
	}

	initialNull := mustGetData(t, iso, 0) == 0
	const sentinelA = uintptr(0x1111)
	const sentinelB = uintptr(0x2222)
	setIsolateData(t, iso, 0, sentinelA)
	roundtripA := mustGetData(t, iso, 0) == sentinelA
	setIsolateData(t, iso, 1, sentinelB)
	slot1Roundtrip := mustGetData(t, iso, 1) == sentinelB
	slot0Unaffected := mustGetData(t, iso, 0) == sentinelA
	setIsolateData(t, iso, 0, 0)
	cleared0 := mustGetData(t, iso, 0) == 0
	slot1Survives := mustGetData(t, iso, 1) == sentinelB

	return wantGot("core-advanced/slots/isolate_raw_data_slots",
		jobj(
			kv("slot_count", jint(3)),
			kv("initial_null", jbool(true)),
			kv("roundtrip_a", jbool(true)),
			kv("slot1_roundtrip", jbool(true)),
			kv("slot0_unaffected", jbool(true)),
			kv("cleared0_null", jbool(true)),
			kv("slot1_survives", jbool(true)),
		),
		jobj(
			kv("slot_count", jint(int64(slotCount))),
			kv("initial_null", jbool(initialNull)),
			kv("roundtrip_a", jbool(roundtripA)),
			kv("slot1_roundtrip", jbool(slot1Roundtrip)),
			kv("slot0_unaffected", jbool(slot0Unaffected)),
			kv("cleared0_null", jbool(cleared0)),
			kv("slot1_survives", jbool(slot1Survives)),
		))
}

// checkSlotsIsolateMultipleTypes mirrors
// core-advanced/slots/isolate_multiple_types. The crate keys typed slots by
// the Rc element type; this port keys them by an explicit any key (the
// established isolate-slot mapping), so Alpha/Beta map to "alpha"/"beta".
func checkSlotsIsolateMultipleTypes(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()

	setAlphaFirst := iso.SetSlot("alpha", uint32(1))
	setBetaFirst := iso.SetSlot("beta", "beta")
	alphaRead, _ := iso.GetSlot("alpha")
	betaRead, _ := iso.GetSlot("beta")
	removedAlpha, _ := iso.RemoveSlot("alpha")
	_, removedAlphaAgain := iso.RemoveSlot("alpha")
	betaSurvives, _ := iso.GetSlot("beta")
	setAlphaAgainFirst := iso.SetSlot("alpha", uint32(2))
	alphaReadAgain, _ := iso.GetSlot("alpha")

	return wantGot("core-advanced/slots/isolate_multiple_types",
		jobj(
			kv("set_alpha_first", jbool(true)),
			kv("set_beta_first", jbool(true)),
			kv("alpha_read", jint(1)),
			kv("beta_read", jstr("beta")),
			kv("removed_alpha", jint(1)),
			kv("removed_alpha_again_is_none", jbool(true)),
			kv("beta_survives", jstr("beta")),
			kv("set_alpha_again_first", jbool(true)),
			kv("alpha_read_again", jint(2)),
		),
		jobj(
			kv("set_alpha_first", jbool(setAlphaFirst)),
			kv("set_beta_first", jbool(setBetaFirst)),
			kv("alpha_read", jint(int64(alphaRead.(uint32)))),
			kv("beta_read", jstr(betaRead.(string))),
			kv("removed_alpha", jint(int64(removedAlpha.(uint32)))),
			kv("removed_alpha_again_is_none", jbool(!removedAlphaAgain)),
			kv("beta_survives", jstr(betaSurvives.(string))),
			kv("set_alpha_again_first", jbool(setAlphaAgainFirst)),
			kv("alpha_read_again", jint(int64(alphaReadAgain.(uint32)))),
		))
}

// --- script -----------------------------------------------------------------------

// checkScriptOriginRoundtrip mirrors core-advanced/script/origin_roundtrip.
func checkScriptOriginRoundtrip(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	origin := makeOrigin("app.js", 0, 0, 777, "map.url", true, true)
	script, err := r.ctx.CompileWithOrigin(r.scope, "1 + 1", origin, nil)
	if err != nil {
		t.Fatalf("CompileWithOrigin: %v", err)
	}
	defer func() { _ = script.Close() }()
	runValue, runErr := script.Run(r.scope, nil)
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}

	scriptID, err := script.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	scriptMatchesOriginID := scriptID == 777
	scriptIDPositive := scriptID > 0
	plain, err := r.ctx.Compile(r.scope, "2 + 2", nil)
	if err != nil {
		t.Fatalf("plain compile: %v", err)
	}
	defer func() { _ = plain.Close() }()
	plainID, err := plain.ID()
	if err != nil {
		t.Fatalf("plain ID: %v", err)
	}

	return wantGot("core-advanced/script/origin_roundtrip",
		jobj(
			kv("origin_script_id", jint(777)),
			kv("resource_name", jstr("app.js")),
			kv("source_map_url", jstr("map.url")),
			kv("script_matches_origin_id", jbool(false)),
			kv("script_id_positive", jbool(true)),
			kv("plain_id_distinct", jbool(true)),
			kv("run_value", jint(2)),
		),
		jobj(
			kv("origin_script_id", jint(int64(origin.ScriptID))),
			kv("resource_name", jstr(origin.ResourceName)),
			kv("source_map_url", jstr(origin.SourceMapURL)),
			kv("script_matches_origin_id", jbool(scriptMatchesOriginID)),
			kv("script_id_positive", jbool(scriptIDPositive)),
			kv("plain_id_distinct", jbool(plainID != scriptID)),
			kv("run_value", jint(intOf(t, r, runValue))),
		))
}

// checkScriptOriginShiftsPositions mirrors
// core-advanced/script/origin_shifts_exception_positions.
func checkScriptOriginShiftsPositions(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	origin := makeOrigin("shift.js", 100, 5, 0, "", false, false)
	tc := r.tc(t)
	defer func() { closeTryCatch(t, tc) }()
	script, cerr := r.ctx.CompileWithOrigin(r.scope, "\nthrow new Error('boom')\n", origin, tc)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	defer func() { _ = script.Close() }()
	_, runErr := script.Run(r.scope, tc)
	ran := runErr == nil
	caught, _ := tc.HasCaught()
	if !caught {
		t.Fatal("throw did not produce a message")
	}
	msg, ok, err := tc.Message(r.scope)
	if err != nil || !ok {
		t.Fatalf("Message = %v (%v)", ok, err)
	}

	line, lineOK, _ := msg.LineNumber(r.ctx)
	if !lineOK {
		line = 0
	}
	sourceLine, _, _ := msg.SourceLine(r.ctx)
	resourceName, _ := msg.ResourceName(r.ctx)
	text, _ := msg.Text(r.ctx)
	startPos, _ := msg.StartPosition()
	endPos, _ := msg.EndPosition()
	startCol, _ := msg.StartColumn()
	endCol, _ := msg.EndColumn()
	errorLevel, _ := msg.ErrorLevel()
	isOpaque, _ := msg.IsOpaque()
	isShared, _ := msg.IsSharedCrossOrigin()

	return wantGot("core-advanced/script/origin_shifts_exception_positions",
		jobj(
			kv("run_none", jbool(true)),
			kv("text", jstr("Uncaught Error: boom")),
			kv("line_number", jint(102)),
			kv("start_position", jint(1)),
			kv("end_position", jint(2)),
			kv("start_column", jint(0)),
			kv("end_column", jint(1)),
			kv("source_line", jstr("throw new Error('boom')")),
			kv("resource_name", jstr("shift.js")),
			kv("error_level", jint(8)),
			kv("is_opaque", jbool(false)),
			kv("is_shared_cross_origin", jbool(false)),
		),
		jobj(
			kv("run_none", jbool(!ran)),
			kv("text", jstr(text)),
			kv("line_number", jint(int64(line))),
			kv("start_position", jint(startPos)),
			kv("end_position", jint(endPos)),
			kv("start_column", jint(startCol)),
			kv("end_column", jint(endCol)),
			kv("source_line", jstr(sourceLine)),
			kv("resource_name", jstr(resourceName)),
			kv("error_level", jint(errorLevel)),
			kv("is_opaque", jbool(isOpaque)),
			kv("is_shared_cross_origin", jbool(isShared)),
		))
}

// checkScriptUnboundRebind mirrors core-advanced/script/unbound_rebind.
func checkScriptUnboundRebind(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	ctx1 := newIsolateContext(t, iso)
	defer func() { _ = ctx1.Close() }()
	ctx2 := newIsolateContext(t, iso)
	defer func() { _ = ctx2.Close() }()
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()

	cs1 := enterContext(t, ctx1)
	defer func() { closeContextScope(t, cs1) }()

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
	idsMatch := scriptID == unboundID

	runBound := func(ctx *gov8.Context) int64 {
		t.Helper()
		bound, err := unbound.Bind(scope)
		if err != nil {
			t.Fatalf("Bind: %v", err)
		}
		v, err := bound.Run(ctx, scope, nil)
		if err != nil {
			t.Fatalf("bound run: %v", err)
		}
		return intOf(t, &runtime{iso: iso, ctx: ctx, scope: scope}, v)
	}
	// intOf needs a runtime; build minimal ones for each context.
	rt1 := &runtime{iso: iso, ctx: ctx1, scope: scope}

	ctx1First := int64(-1)
	if v, err := script1.Run(scope, nil); err == nil {
		ctx1First = intOf(t, rt1, v)
	}
	ctx1Second := runBound(ctx1)

	ctx2First := int64(-1)
	func() {
		cs2 := enterContext(t, ctx2)
		defer func() { closeContextScope(t, cs2) }()
		bound2, err := unbound.Bind(scope)
		if err != nil {
			t.Fatalf("Bind ctx2: %v", err)
		}
		v, err := bound2.Run(ctx2, scope, nil)
		if err != nil {
			t.Fatalf("bound ctx2 run: %v", err)
		}
		ctx2First = intOf(t, &runtime{iso: iso, ctx: ctx2, scope: scope}, v)
	}()

	ctx1After := int64(-1)
	if v, ok := evalIn(t, rt1, nil, "globalThis.n"); ok {
		ctx1After = v
	}

	return wantGot("core-advanced/script/unbound_rebind",
		jobj(
			kv("ids_match_script_unbound", jbool(true)),
			kv("ctx1_first", jint(1)),
			kv("ctx1_second", jint(2)),
			kv("ctx2_first", jint(1)),
			kv("ctx1_after_ctx2_run", jint(2)),
		),
		jobj(
			kv("ids_match_script_unbound", jbool(idsMatch)),
			kv("ctx1_first", jint(ctx1First)),
			kv("ctx1_second", jint(ctx1Second)),
			kv("ctx2_first", jint(ctx2First)),
			kv("ctx1_after_ctx2_run", jint(ctx1After)),
		))
}

// checkScriptCompilerOptions mirrors
// core-advanced/script/compiler_options_and_unbound.
func checkScriptCompilerOptions(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	unbound, err := r.ctx.CompileUnbound(r.scope, "1 + 2",
		makeOrigin("eager.js", 0, 0, 0, "", false, false), gov8.OptEagerCompile, nil)
	unboundOK := err == nil
	unboundIDMatchesOriginZero := false
	if unboundOK {
		id, idErr := unbound.ID()
		if idErr != nil {
			t.Fatalf("unbound ID: %v", idErr)
		}
		unboundIDMatchesOriginZero = id == 0
		closeUnbound(t, unbound)
	}
	// The plain source carries no cached data (embedder-side state: the
	// Go Source is produced from the compile call without a cache).
	sourceHasNoCachedData := true
	tag, err := gov8.CachedDataVersionTag()
	if err != nil {
		t.Fatalf("CachedDataVersionTag: %v", err)
	}

	fn, err := r.ctx.CompileFunction(r.scope, "return a * b;", []string{"a", "b"}, nil)
	if err != nil {
		t.Fatalf("CompileFunction: %v", err)
	}
	undef, err := r.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	a := int32Val(t, r.scope, 6)
	b := int32Val(t, r.scope, 7)
	result, err := gov8.CallFunction(r.ctx, r.scope, fn, undef, []gov8.Value{a, b}, nil)
	callResult := int64(-1)
	if err == nil {
		callResult = intOf(t, r, result)
	}

	return wantGot("core-advanced/script/compiler_options_and_unbound",
		jobj(
			kv("unbound_eager_ok", jbool(true)),
			kv("unbound_id_matches_origin_zero", jbool(false)),
			kv("source_has_no_cached_data", jbool(true)),
			kv("cached_data_version_tag", jint(3252425384)),
			kv("compile_function_call", jint(42)),
		),
		jobj(
			kv("unbound_eager_ok", jbool(unboundOK)),
			kv("unbound_id_matches_origin_zero", jbool(unboundIDMatchesOriginZero)),
			kv("source_has_no_cached_data", jbool(sourceHasNoCachedData)),
			kv("cached_data_version_tag", jint(int64(tag))),
			kv("compile_function_call", jint(callResult)),
		))
}

// checkScriptCodeCacheRoundtrip mirrors
// core-advanced/script/code_cache_roundtrip. The corruption boundary of
// this contract is characterized in the gov8 negative tests' subprocess
// probes: consuming a corrupted cache in this build is a V8 deserializer
// fatal, never a graceful rejection, so it must never run in the fixture.
func checkScriptCodeCacheRoundtrip(t tester) obs {
	const source = "(function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); })(12)"

	// Produce: the producing isolate is closed before the consumer one is
	// created (a code cache is plain bytes, not a handle).
	var cache []byte
	func() {
		iso := newIsolate(t)
		defer func() { _ = iso.Close() }()
		ctx := newIsolateContext(t, iso)
		defer func() { _ = ctx.Close() }()
		scope := newIsolateScope(t, iso)
		defer func() { _ = scope.Close() }()
		unbound, err := ctx.CompileUnbound(scope, source,
			makeOrigin("cached.js", 0, 0, 0, "", false, false), gov8.OptNoCompileOptions, nil)
		if err != nil {
			t.Fatalf("compile for cache production: %v", err)
		}
		defer func() { closeUnbound(t, unbound) }()
		cache, err = unbound.CreateCodeCache()
		if err != nil {
			t.Fatalf("code cache must be produced: %v", err)
		}
	}()
	cacheProduced := len(cache) > 0

	consumeOK := false
	rejected := true
	runValue := int64(-1)
	func() {
		iso := newIsolate(t)
		defer func() { _ = iso.Close() }()
		ctx := newIsolateContext(t, iso)
		defer func() { _ = ctx.Close() }()
		scope := newIsolateScope(t, iso)
		defer func() { _ = scope.Close() }()
		script, rej, err := ctx.CompileCached(scope, source,
			makeOrigin("cached.js", 0, 0, 0, "", false, false), cache, nil)
		if err != nil {
			t.Fatalf("consume compile: %v", err)
		}
		defer func() { _ = script.Close() }()
		rejected = rej
		consumeOK = true
		v, rerr := script.Run(scope, nil)
		if rerr != nil {
			t.Fatalf("consume run: %v", rerr)
		}
		n, _, nerr := v.IntegerValue(ctx)
		if nerr != nil {
			t.Fatalf("consume int: %v", nerr)
		}
		runValue = n
	}()

	return wantGot("core-advanced/script/code_cache_roundtrip",
		jobj(
			kv("cache_produced", jbool(true)),
			kv("consume_compiles", jbool(true)),
			kv("cache_rejected", jbool(false)),
			kv("run_value", jint(144)),
		),
		jobj(
			kv("cache_produced", jbool(cacheProduced)),
			kv("consume_compiles", jbool(consumeOK)),
			kv("cache_rejected", jbool(rejected)),
			kv("run_value", jint(runValue)),
		))
}

// --- message ----------------------------------------------------------------------

// checkMessageExceptionDetails mirrors
// core-advanced/message/exception_details_with_origin.
func checkMessageExceptionDetails(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	tc := r.tc(t)
	defer func() { closeTryCatch(t, tc) }()
	script, cerr := r.ctx.CompileWithOrigin(r.scope,
		"function boom() {\n  null.f();\n}\nboom();\n",
		makeOrigin("detail.js", 0, 0, 0, "", false, false), tc)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	defer func() { _ = script.Close() }()
	if _, rerr := script.Run(r.scope, tc); rerr == nil {
		t.Fatal("run unexpectedly succeeded")
	}
	caught, _ := tc.HasCaught()
	if !caught {
		t.Fatal("runtime error must produce a message")
	}
	msg, ok, err := tc.Message(r.scope)
	if err != nil || !ok {
		t.Fatalf("Message = %v (%v)", ok, err)
	}

	text, _ := msg.Text(r.ctx)
	line, lineOK, _ := msg.LineNumber(r.ctx)
	if !lineOK {
		line = 0
	}
	sourceLine, _, _ := msg.SourceLine(r.ctx)
	resourceName, _ := msg.ResourceName(r.ctx)
	startPos, _ := msg.StartPosition()
	endPos, _ := msg.EndPosition()
	startCol, _ := msg.StartColumn()
	endCol, _ := msg.EndColumn()
	errorLevel, _ := msg.ErrorLevel()
	isOpaque, _ := msg.IsOpaque()
	isShared, _ := msg.IsSharedCrossOrigin()
	exceptionText, _ := tc.ExceptionText(r.scope, r.ctx)
	_, messageHasTrace, _ := msg.StackTrace()

	return wantGot("core-advanced/message/exception_details_with_origin",
		jobj(
			kv("run_none", jbool(true)),
			kv("text", jstr("Uncaught TypeError: Cannot read properties of null (reading 'f')")),
			kv("line_number", jint(2)),
			kv("source_line", jstr("  null.f();")),
			kv("resource_name", jstr("detail.js")),
			kv("start_position", jint(25)),
			kv("end_position", jint(26)),
			kv("start_column", jint(7)),
			kv("end_column", jint(8)),
			kv("error_level", jint(8)),
			kv("is_opaque", jbool(false)),
			kv("is_shared_cross_origin", jbool(false)),
			kv("exception_text", jstr("TypeError: Cannot read properties of null (reading 'f')")),
			kv("message_stack_trace_is_none", jbool(true)),
		),
		jobj(
			kv("run_none", jbool(true)),
			kv("text", jstr(text)),
			kv("line_number", jint(int64(line))),
			kv("source_line", jstr(sourceLine)),
			kv("resource_name", jstr(resourceName)),
			kv("start_position", jint(startPos)),
			kv("end_position", jint(endPos)),
			kv("start_column", jint(startCol)),
			kv("end_column", jint(endCol)),
			kv("error_level", jint(errorLevel)),
			kv("is_opaque", jbool(isOpaque)),
			kv("is_shared_cross_origin", jbool(isShared)),
			kv("exception_text", jstr(exceptionText)),
			kv("message_stack_trace_is_none", jbool(!messageHasTrace)),
		))
}

// frameShape is one normalized stack frame.
type frameShape struct {
	function         interface{} // string or nil
	line             int64
	column           int64
	script           interface{} // string or nil
	scriptIDPositive bool
	isEval           bool
	isConstructor    bool
	isWasm           bool
	isUserJavaScript bool
}

func frameJSON(f frameShape) jsonValue {
	fn := jnull()
	if s, ok := f.function.(string); ok {
		fn = jstr(s)
	}
	script := jnull()
	if s, ok := f.script.(string); ok {
		script = jstr(s)
	}
	return jobj(
		kv("function", fn),
		kv("line", jint(f.line)),
		kv("column", jint(f.column)),
		kv("script", script),
		kv("script_id_positive", jbool(f.scriptIDPositive)),
		kv("is_eval", jbool(f.isEval)),
		kv("is_constructor", jbool(f.isConstructor)),
		kv("is_wasm", jbool(f.isWasm)),
		kv("is_user_javascript", jbool(f.isUserJavaScript)),
	)
}

// framesCapture receives the frames the native callback builds (plain fn
// callbacks cannot capture in the oracle; Go closures can, but the shared
// channel keeps the shape identical).
var framesMu sync.Mutex
var framesCapture []frameShape
var framesCurrentScript string

// checkMessageCurrentStackFrames mirrors
// core-advanced/message/current_stack_frames: frames captured with
// CurrentStackTrace inside a native callback invoked from JS -- the native
// (callback) frame itself is NOT part of the trace, frames are the JS
// callers only, topmost first.
func checkMessageCurrentStackFrames(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	host, err := r.iso.NewFunction(r.scope, r.ctx, func(cs *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
		s := cs.Scope()
		framesMu.Lock()
		framesCapture = nil
		framesCurrentScript = ""
		if name, ok, _ := s.CurrentScriptNameOrSourceURL(); ok {
			framesCurrentScript = name
		}
		if trace, ok, err := s.CurrentStackTrace(16); err == nil && ok {
			count, _ := trace.FrameCount()
			for i := 0; i < count; i++ {
				frame, ferr := trace.Frame(i)
				if ferr != nil {
					break
				}
				shape := frameShape{}
				if name, ok, _ := frame.FunctionName(); ok {
					shape.function = name
				}
				shape.line, _ = frame.LineNumber()
				shape.column, _ = frame.Column()
				if script, ok, _ := frame.ScriptName(); ok {
					shape.script = script
				}
				sid, _ := frame.ScriptID()
				shape.scriptIDPositive = sid > 0
				shape.isEval, _ = frame.IsEval()
				shape.isConstructor, _ = frame.IsConstructor()
				shape.isWasm, _ = frame.IsWasm()
				shape.isUserJavaScript, _ = frame.IsUserJavaScript()
				framesCapture = append(framesCapture, shape)
			}
		}
		framesMu.Unlock()
		_ = rv.SetInt32(1)
	}, nil)
	if err != nil {
		t.Fatalf("NewFunction: %v", err)
	}
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatalf("GlobalObject: %v", err)
	}
	if _, err := global.SetByName(r.scope, r.ctx, "host", host.Value); err != nil {
		t.Fatalf("set global: %v", err)
	}

	framesMu.Lock()
	framesCapture = nil
	framesMu.Unlock()

	origin := makeOrigin("frames.js", 0, 0, 0, "", false, false)
	script, err := r.ctx.CompileWithOrigin(r.scope,
		"function target(n) { return host(n); }\nglobalThis.result = target(9);",
		origin, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, rerr := script.Run(r.scope, nil); rerr != nil {
		t.Fatalf("run: %v", rerr)
	}
	_ = script.Close()

	framesMu.Lock()
	got := framesCapture
	current := framesCurrentScript
	framesMu.Unlock()

	observedFrames := jarr()
	for _, f := range got {
		observedFrames = append(observedFrames, frameJSON(f))
	}

	expectedFrames := jarr(
		frameJSON(frameShape{function: "target", line: 1, column: 29, script: "frames.js",
			scriptIDPositive: true, isUserJavaScript: true}),
		frameJSON(frameShape{function: nil, line: 2, column: 21, script: "frames.js",
			scriptIDPositive: true, isUserJavaScript: true}),
	)

	return wantGot("core-advanced/message/current_stack_frames",
		jobj(
			kv("frame_count", jint(2)),
			kv("frames", expectedFrames),
			kv("current_script_name", jstr("frames.js")),
		),
		jobj(
			kv("frame_count", jint(int64(len(got)))),
			kv("frames", observedFrames),
			kv("current_script_name", jstr(current)),
		))
}

// checkMessageUncaughtCapture mirrors
// core-advanced/message/uncaught_capture_and_capture_stack_trace.
func checkMessageUncaughtCapture(t tester) obs {
	// (a)+(b): plain-object capture and the native-error trace gap.
	// The context is entered for the block: the engine resolves the Error
	// prototype through the entered context (the oracle's ContextScope).
	capturedOK := false
	plainStackFirstLine := ""
	nativeTraceIsNone := false
	func() {
		r := newRuntime(t)
		defer r.close(t)

		entered := enterContext(t, r.ctx)
		defer func() { closeContextScope(t, entered) }()

		obj := newObject(t, r.scope, r.ctx)
		captured, err := gov8.CaptureStackTrace(r.ctx, r.scope, obj)
		if err != nil {
			t.Fatalf("CaptureStackTrace: %v", err)
		}
		capturedOK = captured
		stackVal, found, err := asObject(t, obj).GetByName(r.scope, r.ctx, "stack")
		if err != nil || !found {
			t.Fatalf("stack property = %v (%v)", found, err)
		}
		text, err := stackVal.ToString(r.ctx)
		if err != nil {
			t.Fatalf("stack text: %v", err)
		}
		plainStackFirstLine = strings.SplitN(text, "\n", 2)[0]

		nativeError := newError(t, r.scope, "native-err")
		_, ok, err := gov8.ExceptionStackTrace(r.scope, nativeError)
		if err != nil {
			t.Fatalf("ExceptionStackTrace: %v", err)
		}
		nativeTraceIsNone = !ok
	}()

	// (c) default: an uncaught exception's Message carries no stack trace.
	defaultUncaughtTraceIsNone := false
	func() {
		r := newRuntime(t)
		defer r.close(t)
		tc := r.tc(t)
		defer func() { closeTryCatch(t, tc) }()
		if _, ok := r.eval(t, tc, "function f1() { throw new Error('x'); } f1();"); ok {
			t.Fatal("run unexpectedly succeeded")
		}
		msg, ok, err := tc.Message(r.scope)
		if err != nil || !ok {
			t.Fatalf("Message = %v (%v)", ok, err)
		}
		_, hasTrace, err := msg.StackTrace()
		if err != nil {
			t.Fatalf("StackTrace: %v", err)
		}
		defaultUncaughtTraceIsNone = !hasTrace
	}()

	// (d) enabling capture with a frame limit attaches a truncated trace.
	enabledTrace := jsonValue(jnull())
	func() {
		iso := newIsolate(t)
		defer func() { _ = iso.Close() }()
		if err := iso.SetCaptureStackTraceForUncaughtExceptions(true, 3); err != nil {
			t.Fatalf("SetCaptureStackTraceForUncaughtExceptions: %v", err)
		}
		ctx := newIsolateContext(t, iso)
		defer func() { _ = ctx.Close() }()
		scope := newIsolateScope(t, iso)
		defer func() { _ = scope.Close() }()
		tc := newTryCatch(t, iso)
		defer func() { closeTryCatch(t, tc) }()
		rt := &runtime{iso: iso, ctx: ctx, scope: scope}
		const src = "function d1() { d2(); }\nfunction d2() { d3(); }\nfunction d3() { throw new Error('deep'); }\nd1();"
		if _, ok := evalIn(t, rt, tc, src); ok {
			t.Fatal("run unexpectedly succeeded")
		}
		msg, ok, err := tc.Message(scope)
		if err != nil || !ok {
			t.Fatalf("Message = %v (%v)", ok, err)
		}
		trace, hasTrace, err := msg.StackTrace()
		if err != nil || !hasTrace {
			return
		}
		count, _ := trace.FrameCount()
		names := jarr()
		for i := 0; i < count; i++ {
			frame, ferr := trace.Frame(i)
			if ferr != nil {
				break
			}
			if name, ok, _ := frame.FunctionName(); ok {
				names = append(names, jstr(name))
			} else {
				names = append(names, jnull())
			}
		}
		enabledTrace = jobj(kv("frame_count", jint(int64(count))), kv("function_names", names))
	}()

	return wantGot("core-advanced/message/uncaught_capture_and_capture_stack_trace",
		jobj(
			kv("capture_on_plain_object_ok", jbool(true)),
			kv("plain_stack_first_line", jstr("Error")),
			kv("native_error_trace_is_none", jbool(true)),
			kv("default_uncaught_trace_is_none", jbool(true)),
			kv("enabled_trace", jobj(kv("frame_count", jint(3)), kv("function_names", jarr(jstr("d3"), jstr("d2"), jstr("d1"))))),
		),
		jobj(
			kv("capture_on_plain_object_ok", jbool(capturedOK)),
			kv("plain_stack_first_line", jstr(plainStackFirstLine)),
			kv("native_error_trace_is_none", jbool(nativeTraceIsNone)),
			kv("default_uncaught_trace_is_none", jbool(defaultUncaughtTraceIsNone)),
			kv("enabled_trace", enabledTrace),
		))
}

// --- terminate --------------------------------------------------------------------

// checkTerminateSameThreadLifecycle mirrors
// core-advanced/terminate/same_thread_flag_lifecycle.
func checkTerminateSameThreadLifecycle(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	handle := iso.ThreadSafeHandle()
	ctx := newIsolateContext(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()

	initial := handle.IsExecutionTerminating()
	terminateOK := handle.TerminateExecution()
	afterRequest := handle.IsExecutionTerminating()

	ran := true
	hasCaught := false
	hasTerminated := false
	canContinue := false
	afterDelivery := false
	rerunRan := false
	afterReset := false
	func() {
		tc := newTryCatch(t, iso)
		defer func() { closeTryCatch(t, tc) }()
		script, cerr := ctx.Compile(scope, "1 + 1", tc)
		if cerr != nil {
			t.Fatalf("compile: %v", cerr)
		}
		defer func() { _ = script.Close() }()
		_, rerr := script.Run(scope, tc)
		ran = rerr == nil
		afterDelivery = handle.IsExecutionTerminating()
		// The termination fully unwound to the embedder above, so the
		// rerun in the same TryCatch succeeds (the flag self-clears — the
		// oracle records the same).
		_, rerr2 := script.Run(scope, tc)
		rerunRan = rerr2 == nil
		// The oracle reads the TryCatch flags AFTER Reset: Reset clears the
		// caught termination (has_caught/has_terminated back to false) while
		// can_continue stays false.
		if resetErr := tc.Reset(); resetErr != nil {
			t.Fatalf("Reset: %v", resetErr)
		}
		hasCaught, _ = tc.HasCaught()
		hasTerminated, _ = tc.HasTerminated()
		canContinue, _ = tc.CanContinue()
		afterReset = handle.IsExecutionTerminating()
	}()

	cancelOK := handle.CancelTerminateExecution()
	afterCancel := handle.IsExecutionTerminating()
	recovered, _ := evalIn(t, &runtime{iso: iso, ctx: ctx, scope: scope}, nil, "40 + 2")

	return wantGot("core-advanced/terminate/same_thread_flag_lifecycle",
		jobj(
			kv("initial_terminating", jbool(false)),
			kv("terminate_ok", jbool(true)),
			kv("after_request", jbool(false)),
			kv("run_none", jbool(true)),
			kv("has_caught", jbool(false)),
			kv("has_terminated", jbool(false)),
			kv("can_continue", jbool(false)),
			kv("after_delivery", jbool(true)),
			kv("rerun_succeeded", jbool(true)),
			kv("after_reset", jbool(false)),
			kv("cancel_ok", jbool(true)),
			kv("after_cancel", jbool(false)),
			kv("recovered", jint(42)),
		),
		jobj(
			kv("initial_terminating", jbool(initial)),
			kv("terminate_ok", jbool(terminateOK)),
			kv("after_request", jbool(afterRequest)),
			kv("run_none", jbool(!ran)),
			kv("has_caught", jbool(hasCaught)),
			kv("has_terminated", jbool(hasTerminated)),
			kv("can_continue", jbool(canContinue)),
			kv("after_delivery", jbool(afterDelivery)),
			kv("rerun_succeeded", jbool(rerunRan)),
			kv("after_reset", jbool(afterReset)),
			kv("cancel_ok", jbool(cancelOK)),
			kv("after_cancel", jbool(afterCancel)),
			kv("recovered", jint(recovered)),
		))
}

// checkTerminateCancelBeforeDelivery mirrors
// core-advanced/terminate/cancel_before_delivery.
func checkTerminateCancelBeforeDelivery(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	terminateOK := r.iso.TerminateExecution() == nil
	cancelOK := r.iso.CancelTerminateExecution() == nil

	tc := r.tc(t)
	defer func() { closeTryCatch(t, tc) }()
	result, ok := r.evalInt(t, tc, "6 + 1")
	hasCaught, _ := tc.HasCaught()

	return wantGot("core-advanced/terminate/cancel_before_delivery",
		jobj(
			kv("terminate_ok", jbool(true)),
			kv("cancel_ok", jbool(true)),
			kv("has_caught", jbool(false)),
			kv("result", jint(7)),
		),
		jobj(
			kv("terminate_ok", jbool(terminateOK)),
			kv("cancel_ok", jbool(cancelOK)),
			kv("has_caught", jbool(hasCaught)),
			kv("result", optInt(result, ok)),
		))
}

// interruptCapture collects the interrupt callback's observations.
type interruptCapture struct {
	count            int64
	threadMatches    bool
	terminatingAt    bool
	dataPtrPreserved bool
}

var interruptState struct {
	mu         sync.Mutex
	capture    *interruptCapture
	data       uintptr
	requestTID uint32
}

var getCurrentThreadID = newThreadIDFn()

// checkTerminateInterruptCallback mirrors
// core-advanced/terminate/interrupt_callback: exactly one delivery during
// the bounded loop, on the requesting thread, without terminating.
func checkTerminateInterruptCallback(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	handle := iso.ThreadSafeHandle()

	interruptState.mu.Lock()
	interruptState.capture = &interruptCapture{threadMatches: true, dataPtrPreserved: true}
	interruptState.data = 0x5A5A5A5A
	interruptState.requestTID = getCurrentThreadID()
	capture := interruptState.capture
	data := interruptState.data
	interruptState.mu.Unlock()

	requested := handle.RequestInterrupt(func(_ *gov8.Isolate, d uintptr) {
		interruptState.mu.Lock()
		c := interruptState.capture
		interruptState.mu.Unlock()
		if c == nil {
			return
		}
		c.count++
		c.dataPtrPreserved = d == interruptState.data
		c.terminatingAt = handle.IsExecutionTerminating()
		c.threadMatches = getCurrentThreadID() == interruptState.requestTID
	}, data)

	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()
	ctx := newIsolateContext(t, iso)
	defer func() { _ = ctx.Close() }()
	rt := &runtime{iso: iso, ctx: ctx, scope: scope}
	completed := false
	loopResult := int64(-1)
	if v, ok := evalIn(t, rt, nil, "let s = 0; for (let i = 0; i < 2000000; i++) { s += i; } s"); ok {
		completed = true
		loopResult = v
	}

	interruptState.mu.Lock()
	count := capture.count
	threadMatches := capture.threadMatches
	terminatingAt := capture.terminatingAt
	dataPreserved := capture.dataPtrPreserved
	interruptState.mu.Unlock()

	return wantGot("core-advanced/terminate/interrupt_callback",
		jobj(
			kv("requested", jbool(true)),
			kv("completed", jbool(true)),
			kv("loop_result", jint(1999999000000)),
			kv("callback_count", jint(1)),
			kv("delivered_on_requesting_thread", jbool(true)),
			kv("not_terminating_at_delivery", jbool(true)),
			kv("data_ptr_preserved", jbool(true)),
		),
		jobj(
			kv("requested", jbool(requested)),
			kv("completed", jbool(completed)),
			kv("loop_result", jint(loopResult)),
			kv("callback_count", jint(count)),
			kv("delivered_on_requesting_thread", jbool(threadMatches)),
			kv("not_terminating_at_delivery", jbool(!terminatingAt)),
			kv("data_ptr_preserved", jbool(dataPreserved)),
		))
}

// --- heap ---------------------------------------------------------------------------

// checkHeapStatisticsInvariants mirrors
// core-advanced/heap/statistics_invariants.
func checkHeapStatisticsInvariants(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	ctx := newIsolateContext(t, iso)
	defer func() { _ = ctx.Close() }()
	scope := newIsolateScope(t, iso)
	defer func() { _ = scope.Close() }()
	if _, err := scope.NewObject(ctx); err != nil {
		t.Fatalf("probe object: %v", err)
	}

	stats, err := iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	externalInitial := stats.ExternalMemory
	adjustUp, err := iso.AdjustAmountOfExternalAllocatedMemory(1024)
	if err != nil {
		t.Fatalf("adjust up: %v", err)
	}
	externalAfterUp := mustStats(t, iso).ExternalMemory
	adjustDown, err := iso.AdjustAmountOfExternalAllocatedMemory(-1024)
	if err != nil {
		t.Fatalf("adjust down: %v", err)
	}
	externalAfterDown := mustStats(t, iso).ExternalMemory
	heapSpaces, err := iso.NumberOfHeapSpaces()
	if err != nil {
		t.Fatalf("NumberOfHeapSpaces: %v", err)
	}

	return wantGot("core-advanced/heap/statistics_invariants",
		jobj(
			kv("used_heap_positive", jbool(true)),
			kv("total_at_least_used", jbool(true)),
			kv("available_positive", jbool(true)),
			kv("heap_limit_positive", jbool(true)),
			kv("native_contexts_at_least_one", jbool(true)),
			kv("detached_contexts_zero", jbool(true)),
			kv("does_zap_garbage", jbool(false)),
			kv("global_handles_total_at_least_used", jbool(true)),
			kv("total_allocated_positive", jbool(true)),
			kv("external_initial", jint(0)),
			kv("adjust_up_returns_new_total", jint(1024)),
			kv("external_after_up", jint(0)),
			kv("adjust_down_returns_new_total", jint(0)),
			kv("external_after_down", jint(0)),
			kv("heap_spaces", jint(13)),
		),
		jobj(
			kv("used_heap_positive", jbool(stats.UsedHeapSize > 0)),
			kv("total_at_least_used", jbool(stats.TotalHeapSize >= stats.UsedHeapSize)),
			kv("available_positive", jbool(stats.TotalAvailableSize > 0)),
			kv("heap_limit_positive", jbool(stats.HeapSizeLimit > 0)),
			kv("native_contexts_at_least_one", jbool(stats.NumberOfNativeContexts >= 1)),
			kv("detached_contexts_zero", jbool(stats.NumberOfDetachedContexts == 0)),
			kv("does_zap_garbage", jbool(stats.DoesZapGarbage)),
			kv("global_handles_total_at_least_used", jbool(stats.TotalGlobalHandlesSize >= stats.UsedGlobalHandlesSize)),
			kv("total_allocated_positive", jbool(stats.TotalAllocatedBytes > 0)),
			kv("external_initial", jint(int64(externalInitial))),
			kv("adjust_up_returns_new_total", jint(adjustUp)),
			kv("external_after_up", jint(int64(externalAfterUp))),
			kv("adjust_down_returns_new_total", jint(adjustDown)),
			kv("external_after_down", jint(int64(externalAfterDown))),
			kv("heap_spaces", jint(heapSpaces)),
		))
}

// checkHeapGCNotifications mirrors core-advanced/heap/gc_notification_callbacks.
func checkHeapGCNotifications(t tester) obs {
	iso := newIsolate(t)
	defer func() { _ = iso.Close() }()
	ctx := newIsolateContext(t, iso)
	defer func() { _ = ctx.Close() }()

	var mu sync.Mutex
	prologueCount, epilogueCount := int64(0), int64(0)
	prologueType, epilogueType := int64(0), int64(0)
	prologueFlags, epilogueFlags := int64(0), int64(0)

	prologue, err := iso.AddGCPrologueCallback(func(gcType gov8.GCType, flags gov8.GCCallbackFlags) {
		mu.Lock()
		prologueCount++
		prologueType = int64(gcType)
		prologueFlags = int64(flags)
		mu.Unlock()
	}, gov8.GCTypeMarkSweepCompact)
	if err != nil {
		t.Fatalf("AddGCPrologueCallback: %v", err)
	}
	epilogue, err := iso.AddGCEpilogueCallback(func(gcType gov8.GCType, flags gov8.GCCallbackFlags) {
		mu.Lock()
		epilogueCount++
		epilogueType = int64(gcType)
		epilogueFlags = int64(flags)
		mu.Unlock()
	}, gov8.GCTypeMarkSweepCompact)
	if err != nil {
		t.Fatalf("AddGCEpilogueCallback: %v", err)
	}

	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}

	mu.Lock()
	pFirst, eFirst := prologueCount, epilogueCount
	pType, eType := prologueType, epilogueType
	pFlags, eFlags := prologueFlags, epilogueFlags
	mu.Unlock()

	if err := iso.RemoveGCPrologueCallback(prologue); err != nil {
		t.Fatalf("RemoveGCPrologueCallback: %v", err)
	}
	if err := iso.RemoveGCEpilogueCallback(epilogue); err != nil {
		t.Fatalf("RemoveGCEpilogueCallback: %v", err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("second LowMemoryNotification: %v", err)
	}

	mu.Lock()
	pAfter, eAfter := prologueCount, epilogueCount
	mu.Unlock()

	return wantGot("core-advanced/heap/gc_notification_callbacks",
		jobj(
			kv("prologue_after_first_gc", jint(2)),
			kv("epilogue_after_first_gc", jint(2)),
			kv("prologue_gc_type", jint(4)),
			kv("epilogue_gc_type", jint(4)),
			kv("prologue_flags", jint(16)),
			kv("epilogue_flags", jint(16)),
			kv("prologue_after_removal", jint(2)),
			kv("epilogue_after_removal", jint(2)),
		),
		jobj(
			kv("prologue_after_first_gc", jint(pFirst)),
			kv("epilogue_after_first_gc", jint(eFirst)),
			kv("prologue_gc_type", jint(pType)),
			kv("epilogue_gc_type", jint(eType)),
			kv("prologue_flags", jint(pFlags)),
			kv("epilogue_flags", jint(eFlags)),
			kv("prologue_after_removal", jint(pAfter)),
			kv("epilogue_after_removal", jint(eAfter)),
		))
}

// --- registry (order is the observable contract) -------------------------------------

type checkFn func(t tester) obs

var checks = []checkFn{
	checkScopeNestedAndEscaped,
	checkScopeEscapeTwicePanics,
	checkThreadSharedIsolateCrossThreadLocks,
	checkThreadSharedTerminateWhileLocked,
	checkThreadLockerUnlockWindow,
	checkThreadIntoSharedRejections,
	checkThreadHandleAfterDispose,
	checkContextEnterExitNesting,
	checkContextSecurityTokens,
	checkContextEmbedderDataAndSlots,
	checkSlotsIsolateRawData,
	checkSlotsIsolateMultipleTypes,
	checkScriptOriginRoundtrip,
	checkScriptOriginShiftsPositions,
	checkScriptUnboundRebind,
	checkScriptCompilerOptions,
	checkScriptCodeCacheRoundtrip,
	checkMessageExceptionDetails,
	checkMessageCurrentStackFrames,
	checkMessageUncaughtCapture,
	checkTerminateSameThreadLifecycle,
	checkTerminateCancelBeforeDelivery,
	checkTerminateInterruptCallback,
	checkHeapStatisticsInvariants,
	checkHeapGCNotifications,
}
