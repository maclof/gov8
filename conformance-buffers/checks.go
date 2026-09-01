//go:build windows && amd64

// The 20 buffers/serialization conformance checks, in the fixed oracle order
// (rust-oracle/src/bin/conformance-buffers.rs CHECKS). Order is part of the
// observable contract: the fixture is ordered.
package main

import (
	stdruntime "runtime"
	"unsafe"

	gov8 "github.com/maclof/gov8"
)

// --- ArrayBuffer construction ---------------------------------------------------

func checkABNewBasics(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	ab16, err := gov8.NewArrayBuffer(r.scope, r.ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer(16): %v", err)
	}
	ab0, err := gov8.NewArrayBuffer(r.scope, r.ctx, 0)
	if err != nil {
		t.Fatalf("NewArrayBuffer(0): %v", err)
	}
	jsV, ok := r.eval(t, nil, "new ArrayBuffer(8)")
	if !ok {
		t.Fatal("eval new ArrayBuffer(8) failed")
	}
	jsIsAB, _ := jsV.IsArrayBuffer()
	jsAB, err := gov8.AsArrayBuffer(jsV)
	if err != nil {
		t.Fatalf("AsArrayBuffer: %v", err)
	}

	len16, _ := ab16.ByteLength()
	detachable16, _ := ab16.IsDetachable()
	detached16, _ := ab16.WasDetached()
	_, some16, _ := ab16.Data()

	len0, _ := ab0.ByteLength()
	_, some0, _ := ab0.Data()
	// Pinned nuance: the Rust wrapper early-returns false whenever
	// byte_length is nonzero, but for a zero-length buffer the real
	// WasDetached bit is consulted.
	detached0, _ := ab0.WasDetached()

	jsLen, _ := jsAB.ByteLength()
	jsDetachable, _ := jsAB.IsDetachable()
	_, jsSome, _ := jsAB.Data()

	return wantGot("buffers/ab_new_basics",
		obj(
			kv("len16", obj(
				kv("byte_length", i(16)),
				kv("is_detachable", b(true)),
				kv("was_detached", b(false)),
				kv("data_is_some", b(true)))),
			kv("len0", obj(
				kv("byte_length", i(0)),
				kv("data_is_some", b(false)),
				kv("was_detached", b(false)))),
			kv("js_created", obj(
				kv("is_array_buffer", b(true)),
				kv("byte_length", i(8)),
				kv("is_detachable", b(true)),
				kv("data_is_some", b(true)))),
		),
		obj(
			kv("len16", obj(
				kv("byte_length", i(int64(len16))),
				kv("is_detachable", b(detachable16)),
				kv("was_detached", b(detached16)),
				kv("data_is_some", b(some16)))),
			kv("len0", obj(
				kv("byte_length", i(int64(len0))),
				kv("data_is_some", b(some0)),
				kv("was_detached", b(detached0)))),
			kv("js_created", obj(
				kv("is_array_buffer", b(jsIsAB)),
				kv("byte_length", i(int64(jsLen))),
				kv("is_detachable", b(jsDetachable)),
				kv("data_is_some", b(jsSome)))),
		))
}

// --- backing store ownership ------------------------------------------------------

func checkABBackingStoreOwnership(t tester) obs {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()

	bs, err := iso.NewBackingStoreFromSlice([]byte{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	defer func() { _ = bs.Close() }()

	standaloneCount := useCountIs(t, bs, 1)
	standaloneShared, _ := bs.IsShared()
	standaloneResizable, _ := bs.IsResizableByUserJavaScript()
	standaloneBytes := make([]byte, 4)
	_, _ = bs.ReadAt(standaloneBytes, 0)

	bufferLen := 0
	bufferBytesSame := false
	copied := 0
	contents := make([]byte, 0)
	attachedCount := false
	func() {
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		defer func() { _ = scope.Close() }()
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		defer func() { _ = ctx.Close() }()
		buffer, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
		if err != nil {
			t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
		}
		attachedCount = useCountIs(t, bs, 2)
		bufferLen, _ = buffer.ByteLength()
		view, err := gov8.NewUint8Array(scope, ctx, buffer, 0, 4)
		if err != nil {
			t.Fatalf("NewUint8Array: %v", err)
		}
		out := make([]byte, 4)
		copied, _ = view.CopyContents(out)
		contents = out
		// The attached store reads back identical bytes.
		attached := make([]byte, 4)
		_, _ = bs.ReadAt(attached, 0)
		bufferBytesSame = string(attached) == string(standaloneBytes)
	}()

	// The handle scope closed; a major GC must release the JS-side shared
	// reference while the standalone reference keeps the memory alive.
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	collectedCount := useCountIs(t, bs, 1)
	afterGC := make([]byte, 4)
	_, _ = bs.ReadAt(afterGC, 0)
	bytesSurviveGC := string(afterGC) == string(standaloneBytes)

	reusableAfterGC := false
	func() {
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		defer func() { _ = scope.Close() }()
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		defer func() { _ = ctx.Close() }()
		rebuilt, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
		if err != nil {
			t.Fatalf("rebuild: %v", err)
		}
		n, _ := rebuilt.ByteLength()
		reusableAfterGC = n == 4
	}()

	return wantGot("buffers/ab_backing_store_ownership",
		obj(
			kv("standalone_count", b(true)),
			kv("standalone_shared", b(false)),
			kv("standalone_resizable", b(false)),
			kv("standalone_bytes", s("01020304")),
			kv("attached_count", b(true)),
			kv("buffer_len", i(4)),
			kv("buffer_bytes_same", b(true)),
			kv("copied", i(4)),
			kv("contents", s("01020304")),
			kv("collected_count", b(true)),
			kv("bytes_survive_gc", b(true)),
			kv("reusable_after_gc", b(true)),
		),
		obj(
			kv("standalone_count", b(standaloneCount)),
			kv("standalone_shared", b(standaloneShared)),
			kv("standalone_resizable", b(standaloneResizable)),
			kv("standalone_bytes", s(lowerHex(standaloneBytes))),
			kv("attached_count", b(attachedCount)),
			kv("buffer_len", i(int64(bufferLen))),
			kv("buffer_bytes_same", b(bufferBytesSame)),
			kv("copied", i(int64(copied))),
			kv("contents", s(lowerHex(contents))),
			kv("collected_count", b(collectedCount)),
			kv("bytes_survive_gc", b(bytesSurviveGC)),
			kv("reusable_after_gc", b(reusableAfterGC)),
		))
}

func checkABBackingStoreAlias(t tester) obs {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()

	bs, err := iso.NewBackingStoreFromSlice([]byte{10, 20, 30, 40})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	defer func() { _ = bs.Close() }()
	ab1, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("buffer1: %v", err)
	}
	ab2, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("buffer2: %v", err)
	}
	countTwoBuffers := useCountIs(t, bs, 3)

	// Interior-mutable write through the store, observed through ab2's view.
	if _, err := bs.WriteAt([]byte{99}, 1); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	seenByAB2 := make([]byte, 4)
	ta, err := gov8.NewUint8Array(scope, ctx, ab2, 0, 4)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	copied, _ := ta.CopyContents(seenByAB2)

	detachAB1, err := ab1.Detach(ctx, gov8.Value{})
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	ab1Len, _ := ab1.ByteLength()
	ab2Len, _ := ab2.ByteLength()
	afterDetach := make([]byte, 4)
	copiedAfterDetach, _ := ta.CopyContents(afterDetach)

	return wantGot("buffers/ab_backing_store_alias",
		obj(
			kv("count_two_buffers", b(true)),
			kv("seen_by_ab2", s("0a631e28")),
			kv("copied", i(4)),
			kv("detach_ab1", b(true)),
			kv("ab1_len", i(0)),
			kv("ab2_len", i(4)),
			kv("ab2_after_detach", s("0a631e28")),
			kv("copied_after_detach", i(4)),
		),
		obj(
			kv("count_two_buffers", b(countTwoBuffers)),
			kv("seen_by_ab2", s(lowerHex(seenByAB2))),
			kv("copied", i(int64(copied))),
			kv("detach_ab1", b(detachAB1)),
			kv("ab1_len", i(int64(ab1Len))),
			kv("ab2_len", i(int64(ab2Len))),
			kv("ab2_after_detach", s(lowerHex(afterDetach))),
			kv("copied_after_detach", i(int64(copiedAfterDetach))),
		))
}

func checkABBackingStoreSharedSAB(t tester) obs {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()

	bs, err := iso.NewSharedArrayBufferBackingStore(8)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()
	storeIsShared, _ := bs.IsShared()
	storeLen, _ := bs.ByteLength()

	sab, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
	}
	sabLen, _ := sab.ByteLength()
	fromSAB, err := sab.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	fromSABShared, _ := fromSAB.IsShared()
	_ = fromSAB.Close()

	v := sab.Value
	isSAB, _ := v.IsSharedArrayBuffer()
	notPlainAB, _ := v.IsArrayBuffer()
	countWithSAB := useCountIs(t, bs, 2)

	return wantGot("buffers/ab_backing_store_shared_sab",
		obj(
			kv("store_is_shared", b(true)),
			kv("store_len", i(8)),
			kv("is_shared_array_buffer", b(true)),
			kv("not_plain_array_buffer", b(true)),
			kv("sab_len", i(8)),
			kv("backing_store_is_shared", b(true)),
			kv("use_count_with_sab", b(true)),
		),
		obj(
			kv("store_is_shared", b(storeIsShared)),
			kv("store_len", i(int64(storeLen))),
			kv("is_shared_array_buffer", b(isSAB)),
			kv("not_plain_array_buffer", b(!notPlainAB)),
			kv("sab_len", i(int64(sabLen))),
			kv("backing_store_is_shared", b(fromSABShared)),
			kv("use_count_with_sab", b(countWithSAB)),
		))
}

func checkSABBackingStoreOwnedExternal(t tester) obs {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()

	owned, err := iso.NewSharedArrayBufferBackingStoreFromSlice([]byte{1, 3, 5, 7})
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStoreFromSlice: %v", err)
	}
	ownedShared, _ := owned.IsShared()
	ownedBytes := make([]byte, 4)
	_, _ = owned.ReadAt(ownedBytes, 0)
	ownedAttachedLength := 0
	func() {
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		defer func() { _ = ctx.Close() }()
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		defer func() { _ = scope.Close() }()
		sab, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, owned)
		if err != nil {
			t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
		}
		ownedAttachedLength, _ = sab.ByteLength()
	}()
	_ = iso.LowMemoryNotification()
	if err := owned.Close(); err != nil {
		t.Fatalf("owned.Close: %v", err)
	}

	invocations := 0
	observedLen := 0
	observedData := uintptr(0)
	registered := uintptr(0x6B6B6B6B6E)
	memory := []byte{9, 8, 6}
	external, err := iso.NewSharedArrayBufferBackingStoreFromPtr(
		unsafe.Pointer(&memory[0]), len(memory),
		func(_ unsafe.Pointer, byteLength int, deleterData uintptr) {
			invocations++
			observedLen = byteLength
			observedData = deleterData
		}, registered)
	if err != nil {
		t.Fatalf("NewSharedArrayBufferBackingStoreFromPtr: %v", err)
	}
	externalShared, _ := external.IsShared()
	externalBytes := make([]byte, 3)
	_, _ = external.ReadAt(externalBytes, 0)
	func() {
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		defer func() { _ = ctx.Close() }()
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		defer func() { _ = scope.Close() }()
		if _, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, external); err != nil {
			t.Fatalf("NewSharedArrayBufferWithBackingStore: %v", err)
		}
	}()
	_ = iso.LowMemoryNotification()
	if err := external.Close(); err != nil {
		t.Fatalf("external.Close: %v", err)
	}
	stdruntime.KeepAlive(memory)

	return wantGot("buffers/sab_backing_store_owned_external",
		obj(
			kv("owned_is_shared", b(true)),
			kv("owned_contents", s("01030507")),
			kv("owned_attached_byte_length", i(4)),
			kv("external_is_shared", b(true)),
			kv("external_contents", s("090806")),
			kv("external_invocations", i(1)),
			kv("external_observed_byte_length", i(3)),
			kv("external_deleter_data_roundtrip", b(true)),
		),
		obj(
			kv("owned_is_shared", b(ownedShared)),
			kv("owned_contents", s(lowerHex(ownedBytes))),
			kv("owned_attached_byte_length", i(int64(ownedAttachedLength))),
			kv("external_is_shared", b(externalShared)),
			kv("external_contents", s(lowerHex(externalBytes))),
			kv("external_invocations", i(int64(invocations))),
			kv("external_observed_byte_length", i(int64(observedLen))),
			kv("external_deleter_data_roundtrip", b(observedData == registered)),
		))
}

func checkABResizableBackingStore(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	v, ok := r.eval(t, nil, "new ArrayBuffer(8, {maxByteLength: 16})")
	if !ok {
		t.Fatal("eval resizable ArrayBuffer failed")
	}
	ab, err := gov8.AsArrayBuffer(v)
	if err != nil {
		t.Fatalf("AsArrayBuffer: %v", err)
	}
	bs, err := ab.GetBackingStore()
	if err != nil {
		t.Fatalf("GetBackingStore: %v", err)
	}
	defer func() { _ = bs.Close() }()

	byteLength, _ := ab.ByteLength()
	resizable, _ := bs.IsResizableByUserJavaScript()
	shared, _ := bs.IsShared()
	detachable, _ := ab.IsDetachable()

	return wantGot("buffers/ab_resizable_backing_store",
		obj(
			kv("byte_length", i(8)),
			kv("is_resizable_by_user_javascript", b(true)),
			kv("is_shared", b(false)),
			kv("is_detachable", b(true)),
		),
		obj(
			kv("byte_length", i(int64(byteLength))),
			kv("is_resizable_by_user_javascript", b(resizable)),
			kv("is_shared", b(shared)),
			kv("is_detachable", b(detachable)),
		))
}

// --- detach ---------------------------------------------------------------------

func checkABDetachBasic(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	if _, ok := r.eval(t, nil, "globalThis.ab = new ArrayBuffer(8)"); !ok {
		t.Fatal("seed eval failed")
	}
	abV, ok := r.eval(t, nil, "ab")
	if !ok {
		t.Fatal("eval ab failed")
	}
	ab, err := gov8.AsArrayBuffer(abV)
	if err != nil {
		t.Fatalf("AsArrayBuffer: %v", err)
	}

	beforeLen, _ := ab.ByteLength()
	beforeDetachable, _ := ab.IsDetachable()
	beforeDetached, _ := ab.WasDetached()
	_, beforeSome, _ := ab.Data()

	detachResult, err := ab.Detach(r.ctx, gov8.Value{})
	if err != nil {
		t.Fatalf("detach: %v", err)
	}

	afterLen, _ := ab.ByteLength()
	afterDetached, _ := ab.WasDetached()
	_, afterSome, _ := ab.Data()

	jsSees, _ := r.evalText(t, nil, "`${ab.byteLength},${ab.detached}`")
	secondDetach, _ := ab.Detach(r.ctx, gov8.Value{})

	return wantGot("buffers/ab_detach_basic",
		obj(
			kv("before", obj(
				kv("byte_length", i(8)),
				kv("is_detachable", b(true)),
				kv("was_detached", b(false)),
				kv("data_is_some", b(true)))),
			kv("detach_result", b(true)),
			kv("after", obj(
				kv("byte_length", i(0)),
				kv("was_detached", b(true)),
				kv("data_is_some", b(false)))),
			kv("js_sees", s("0,true")),
			kv("second_detach", b(true)),
		),
		obj(
			kv("before", obj(
				kv("byte_length", i(int64(beforeLen))),
				kv("is_detachable", b(beforeDetachable)),
				kv("was_detached", b(beforeDetached)),
				kv("data_is_some", b(beforeSome)))),
			kv("detach_result", b(detachResult)),
			kv("after", obj(
				kv("byte_length", i(int64(afterLen))),
				kv("was_detached", b(afterDetached)),
				kv("data_is_some", b(afterSome)))),
			kv("js_sees", s(jsSees)),
			kv("second_detach", b(secondDetach)),
		))
}

func checkABDetachKeyGate(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 8)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	key, err := r.scope.NewString("owner")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	wrong, err := r.scope.NewString("other")
	if err != nil {
		t.Fatalf("NewString: %v", err)
	}
	if err := ab.SetDetachKey(key); err != nil {
		t.Fatalf("SetDetachKey: %v", err)
	}

	wrongKeyOK, err := ab.Detach(r.ctx, wrong)
	if err != nil {
		t.Fatalf("detach wrong: %v", err)
	}
	untouchedLen, _ := ab.ByteLength()
	untouchedDetached, _ := ab.WasDetached()

	// A set detach key also rejects a detach attempt WITHOUT a key.
	noneKeyOK, err := ab.Detach(r.ctx, gov8.Value{})
	if err != nil {
		t.Fatalf("detach none: %v", err)
	}
	noneLen, _ := ab.ByteLength()
	noneDetached, _ := ab.WasDetached()

	rightKeyOK, err := ab.Detach(r.ctx, key)
	if err != nil {
		t.Fatalf("detach right: %v", err)
	}
	finalLen, _ := ab.ByteLength()
	finalDetached, _ := ab.WasDetached()

	return wantGot("buffers/ab_detach_key_gate",
		obj(
			kv("wrong_key_is_none", b(true)),
			kv("untouched_after_wrong", obj(
				kv("byte_length", i(8)),
				kv("was_detached", b(false)))),
			kv("none_key_result", b(false)),
			kv("state_after_none", obj(
				kv("byte_length", i(8)),
				kv("was_detached", b(false)))),
			kv("right_key_result", b(true)),
			kv("final_state", obj(
				kv("byte_length", i(0)),
				kv("was_detached", b(true)))),
		),
		obj(
			kv("wrong_key_is_none", b(!wrongKeyOK)),
			kv("untouched_after_wrong", obj(
				kv("byte_length", i(int64(untouchedLen))),
				kv("was_detached", b(untouchedDetached)))),
			kv("none_key_result", b(noneKeyOK)),
			kv("state_after_none", obj(
				kv("byte_length", i(int64(noneLen))),
				kv("was_detached", b(noneDetached)))),
			kv("right_key_result", b(rightKeyOK)),
			kv("final_state", obj(
				kv("byte_length", i(int64(finalLen))),
				kv("was_detached", b(finalDetached)))),
		))
}

func checkABDetachViewsFollow(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	if _, ok := r.eval(t, nil,
		"globalThis.ab = new ArrayBuffer(8); globalThis.ta = new Uint8Array(ab, 2, 4)"); !ok {
		t.Fatal("seed eval failed")
	}
	abV, ok := r.eval(t, nil, "ab")
	if !ok {
		t.Fatal("eval ab failed")
	}
	ab, err := gov8.AsArrayBuffer(abV)
	if err != nil {
		t.Fatalf("AsArrayBuffer: %v", err)
	}
	taV, ok := r.eval(t, nil, "ta")
	if !ok {
		t.Fatal("eval ta failed")
	}
	ta, err := gov8.AsTypedArray(taV)
	if err != nil {
		t.Fatalf("AsTypedArray: %v", err)
	}

	beforeLen, _ := ta.Length()
	beforeOffset, _ := ta.ByteOffset()
	beforeBytes, _ := ta.ByteLength()
	jsBefore, _ := r.evalText(t, nil, "`${ta.length},${ta.byteLength},${ta[0]}`")

	detachResult, err := ab.Detach(r.ctx, gov8.Value{})
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	afterLen, _ := ta.Length()
	afterBytes, _ := ta.ByteLength()

	viewBufferSame := false
	if viewBuf, err := ta.Buffer(); err == nil {
		viewBufferSame, _ = gov8.Same(viewBuf.Value, ab.Value)
	}
	jsAfter, _ := r.evalText(t, nil, "`${ta.length},${ta.byteLength},${ta[0]}`")
	// A zero-length view over the now-detached (zero-length) buffer.
	_, viewErr := gov8.NewUint8Array(r.scope, r.ctx, ab, 0, 0)
	viewAfterDetach := viewErr == nil

	return wantGot("buffers/ab_detach_views_follow",
		obj(
			kv("before", obj(
				kv("length", i(4)),
				kv("byte_offset", i(2)),
				kv("byte_length", i(4)),
				kv("js", s("4,4,0")))),
			kv("detach_result", b(true)),
			kv("after", obj(
				kv("length", i(0)),
				kv("byte_length", i(0)))),
			kv("view_buffer_is_detached_ab", b(true)),
			kv("js_after", s("0,0,undefined")),
			kv("view_after_detach_is_some", b(true)),
		),
		obj(
			kv("before", obj(
				kv("length", i(int64(beforeLen))),
				kv("byte_offset", i(int64(beforeOffset))),
				kv("byte_length", i(int64(beforeBytes))),
				kv("js", s(jsBefore)))),
			kv("detach_result", b(detachResult)),
			kv("after", obj(
				kv("length", i(int64(afterLen))),
				kv("byte_length", i(int64(afterBytes)))),
			),
			kv("view_buffer_is_detached_ab", b(viewBufferSame)),
			kv("js_after", s(jsAfter)),
			kv("view_after_detach_is_some", b(viewAfterDetach)),
		))
}

func checkABDetachJSTransfer(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	jsSees, ok := r.evalText(t, nil,
		"const src = new ArrayBuffer(8); const dst = src.transfer(); "+
			"`${src.detached},${src.byteLength},${dst.byteLength}`")
	if !ok {
		t.Fatal("eval transfer failed")
	}
	return wantGot("buffers/ab_detach_js_transfer",
		obj(kv("js_sees", s("true,0,8"))),
		obj(kv("js_sees", s(jsSees))))
}

// --- views ------------------------------------------------------------------------

func checkViewTypedArrayBounds(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	ta, err := gov8.NewUint8Array(r.scope, r.ctx, ab, 4, 8)
	if err != nil {
		t.Fatalf("NewUint8Array: %v", err)
	}
	bufferStrictEqual := false
	if viewBuf, err := ta.Buffer(); err == nil {
		bufferStrictEqual, _ = gov8.Same(viewBuf.Value, ab.Value)
	}

	contents := make([]byte, 8)
	copied, _ := ta.CopyContents(contents)

	length, _ := ta.Length()
	offset, _ := ta.ByteOffset()
	byteLen, _ := ta.ByteLength()
	hasBuffer, _ := ta.HasBuffer()

	// Out-of-bounds and misaligned-offset construction are V8 CHECK-fatals
	// in the pinned oracle (process aborts, characterized out-of-process by
	// tests/buffers_negative.rs); gov8 prevalidates both at the boundary and
	// returns errors, so the in-bounds boundary views below stay observable.
	_, endZeroErr := gov8.NewUint8Array(r.scope, r.ctx, ab, 16, 0)
	_, zeroErr := gov8.NewUint8Array(r.scope, r.ctx, ab, 0, 0)

	return wantGot("buffers/view_typed_array_bounds",
		obj(
			kv("in_bounds", obj(
				kv("length", i(8)),
				kv("byte_offset", i(4)),
				kv("byte_length", i(8)),
				kv("has_buffer", b(true)),
				kv("buffer_strict_equal", b(true)),
				kv("contents", s("0000000000000000")),
				kv("copied", i(8)))),
			kv("end_zero_len_is_some", b(true)),
			kv("zero_len_is_some", b(true)),
		),
		obj(
			kv("in_bounds", obj(
				kv("length", i(int64(length))),
				kv("byte_offset", i(int64(offset))),
				kv("byte_length", i(int64(byteLen))),
				kv("has_buffer", b(hasBuffer)),
				kv("buffer_strict_equal", b(bufferStrictEqual)),
				kv("contents", s(lowerHex(contents))),
				kv("copied", i(int64(copied))))),
			kv("end_zero_len_is_some", b(endZeroErr == nil)),
			kv("zero_len_is_some", b(zeroErr == nil)),
		))
}

func checkViewDataViewBounds(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 16)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	dv, err := gov8.NewDataView(r.scope, r.ctx, ab, 2, 8)
	if err != nil {
		t.Fatalf("NewDataView: %v", err)
	}
	byteOffset, _ := dv.ByteOffset()
	byteLength, _ := dv.ByteLength()
	v := dv.Value

	isDataView, _ := v.IsDataView()
	isView, _ := v.IsArrayBufferView()
	isTyped, _ := v.IsTypedArray()

	return wantGot("buffers/view_data_view_bounds",
		obj(
			kv("is_data_view", b(true)),
			kv("is_array_buffer_view", b(true)),
			kv("is_typed_array", b(false)),
			kv("byte_offset", i(2)),
			kv("byte_length", i(8)),
		),
		obj(
			kv("is_data_view", b(isDataView)),
			kv("is_array_buffer_view", b(isView)),
			kv("is_typed_array", b(isTyped)),
			kv("byte_offset", i(int64(byteOffset))),
			kv("byte_length", i(int64(byteLength))),
		))
}

func checkViewMaxSizes(t tester) obs {
	limits, err := gov8.TypedArrayLimitsQuery()
	if err != nil {
		t.Fatalf("TypedArrayLimitsQuery: %v", err)
	}
	return wantGot("buffers/view_max_sizes",
		obj(
			kv("typed_array_max_byte_length", i(9_007_199_254_740_991)),
			kv("uint8_max_length", i(9_007_199_254_740_991)),
			kv("float64_max_length", i(1_125_899_906_842_623)),
			kv("bigint64_max_length", i(1_125_899_906_842_623)),
			kv("typed_array_max_size_in_heap", i(0)),
		),
		obj(
			kv("typed_array_max_byte_length", i(limits.MaxByteLength)),
			kv("uint8_max_length", i(limits.Uint8MaxLength)),
			kv("float64_max_length", i(limits.Float64MaxLength)),
			kv("bigint64_max_length", i(limits.BigInt64MaxLength)),
			kv("typed_array_max_size_in_heap", i(limits.MaxSizeInHeap)),
		))
}

// --- external backing store --------------------------------------------------------

func checkExtBackingStoreDeleter(t tester) obs {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso.Close() }()

	invocations := 0
	observedLen := 0
	observedData := uintptr(0)
	registered := uintptr(0x5A5A5A5A5E)
	memory := make([]byte, 12)
	for i := range memory {
		memory[i] = 7
	}

	bs, err := iso.NewBackingStoreFromPtr(unsafe.Pointer(&memory[0]), len(memory),
		func(data unsafe.Pointer, byteLength int, deleterData uintptr) {
			_ = data
			invocations++
			observedLen = byteLength
			observedData = deleterData
		}, registered)
	if err != nil {
		t.Fatalf("NewBackingStoreFromPtr: %v", err)
	}

	storeSeesBytes := false
	{
		observed := make([]byte, 12)
		n, _ := bs.ReadAt(observed, 0)
		storeSeesBytes = n == 12 && allAre(observed, 7)
	}

	func() {
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		defer func() { _ = ctx.Close() }()
		scope, err := iso.NewScope()
		if err != nil {
			t.Fatalf("NewScope: %v", err)
		}
		defer func() { _ = scope.Close() }()
		buffer, err := gov8.NewArrayBufferWithBackingStore(scope, ctx, bs)
		if err != nil {
			t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
		}
		if n, _ := buffer.ByteLength(); n != 12 {
			t.Errorf("buffer byte_length = %d", n)
		}
	}()
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatalf("LowMemoryNotification: %v", err)
	}
	if err := bs.Close(); err != nil {
		t.Fatalf("bs.Close: %v", err)
	}
	// The registered memory stays valid until the deleter ran (above).
	stdruntime.KeepAlive(memory)

	return wantGot("buffers/ext_backing_store_deleter",
		obj(
			kv("store_sees_bytes", b(true)),
			kv("invocations", i(1)),
			kv("observed_byte_length", i(12)),
			kv("deleter_data_roundtrip", b(true)),
		),
		obj(
			kv("store_sees_bytes", b(storeSeesBytes)),
			kv("invocations", i(int64(invocations))),
			kv("observed_byte_length", i(int64(observedLen))),
			kv("deleter_data_roundtrip", b(observedData == registered)),
		))
}

// --- serialization -------------------------------------------------------------------

func checkSerWirePrimitives(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	type entry struct {
		name string
		val  jsonValue
	}
	var entries []entry

	add := func(name, source string, withHeader bool) {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		v, ok := r.eval(t, tc, source)
		if !ok {
			t.Fatalf("eval %s failed", source)
		}
		outcome := serializeWith(t, r, tc, v, func(ser *gov8.ValueSerializer) {
			if withHeader {
				if err := ser.WriteHeader(); err != nil {
					t.Fatalf("WriteHeader: %v", err)
				}
			}
		})
		entries = append(entries, entry{name, obj(
			kv("ok", b(outcome.ok)),
			kv("wire", s(lowerHex(outcome.wire))),
			kv("clone_error", s(outcome.cloneError)))})
		roundtrip, _ := deserDescribe(t, r, tc, outcome.wire, nil)
		entries = append(entries, entry{roundtripTag(name), roundtrip})
	}

	add("undefined", "undefined", false)
	add("null", "null", false)
	add("false", "false", false)
	add("true", "true", false)
	add("zero", "0", false)
	add("one", "1", false)
	add("neg_one", "-1", false)
	add("two_point_five", "2.5", false)
	add("string_abc", `"abc"`, false)
	// Canonical embedder flow: an explicit WriteHeader() before the value
	// (write_value alone emits NO header bytes).
	add("true_hdr", "true", true)
	add("string_abc_hdr", `"abc"`, true)

	actual := jsonObj{}
	for _, e := range entries {
		actual = append(actual, kv(e.name, e.val))
	}

	hostObjectRejection := "Uncaught Error: Deno deserializer: read_host_object not implemented"
	wire := func(ok bool, hexText, error string) jsonValue {
		return obj(kv("ok", b(ok)), kv("wire", s(hexText)), kv("clone_error", s(error)))
	}
	readOK := func(described jsonValue) jsonValue {
		return obj(kv("read", described), kv("caught", b(false)), kv("message", s("")))
	}
	readRejected := obj(
		kv("read", jsonNull{}),
		kv("caught", b(true)),
		kv("message", s(hostObjectRejection)))

	// Wire version 16 headers (ff 10) are what this build's serializer
	// emits, and its own deserializer rejects them via the host-object
	// error path; header-less data deserializes as legacy version 0.
	expected := jsonObj{
		kv("undefined", wire(true, "5f", "")),
		kv("undefined_rt", readOK(obj(kv("type", s("undefined"))))),
		kv("null", wire(true, "30", "")),
		kv("null_rt", readOK(obj(kv("type", s("null"))))),
		kv("false", wire(true, "46", "")),
		kv("false_rt", readOK(obj(kv("type", s("boolean")), kv("value", b(false))))),
		kv("true", wire(true, "54", "")),
		kv("true_rt", readOK(obj(kv("type", s("boolean")), kv("value", b(true))))),
		kv("zero", wire(true, "4900", "")),
		kv("zero_rt", readOK(obj(kv("type", s("int32")), kv("value", i(0))))),
		kv("one", wire(true, "4902", "")),
		kv("one_rt", readOK(obj(kv("type", s("int32")), kv("value", i(1))))),
		kv("neg_one", wire(true, "4901", "")),
		kv("neg_one_rt", readOK(obj(kv("type", s("int32")), kv("value", i(-1))))),
		kv("two_point_five", wire(true, "4e0000000000000440", "")),
		kv("two_point_five_rt", readOK(obj(kv("type", s("number")), kv("value", f(2.5))))),
		kv("string_abc", wire(true, "2203616263", "")),
		kv("string_abc_rt", readOK(obj(kv("type", s("string")), kv("value", s("abc"))))),
		kv("true_hdr", wire(true, "ff1054", "")),
		kv("true_hdr_rt", readRejected),
		kv("string_abc_hdr", wire(true, "ff102203616263", "")),
		kv("string_abc_hdr_rt", readRejected),
	}
	return wantGot("buffers/ser_wire_primitives", expected, actual)
}

func roundtripTag(name string) string {
	switch name {
	case "undefined":
		return "undefined_rt"
	case "null":
		return "null_rt"
	case "false":
		return "false_rt"
	case "true":
		return "true_rt"
	case "zero":
		return "zero_rt"
	case "one":
		return "one_rt"
	case "neg_one":
		return "neg_one_rt"
	case "two_point_five":
		return "two_point_five_rt"
	case "string_abc":
		return "string_abc_rt"
	case "true_hdr":
		return "true_hdr_rt"
	case "string_abc_hdr":
		return "string_abc_hdr_rt"
	}
	panic("unknown primitive name " + name)
}

func checkSerWireObject(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	var wireHex string
	func() {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		v, ok := r.eval(t, tc, `({a: 1, b: "x"})`)
		if !ok {
			t.Fatal("eval object failed")
		}
		// Header-less: this pinned V8 writes wire-format version 16 headers
		// (see the *_hdr primitive entries) that its own deserializer
		// rejects, so the canonical roundtrip demos stay header-less (the
		// deserializer accepts header-less data as legacy version 0).
		outcome := serialize(t, r, tc, v)
		if !outcome.ok {
			t.Fatal("object must serialize")
		}
		wireHex = lowerHex(outcome.wire)
	}()

	var roundtrip jsonValue
	func() {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		roundtrip, _ = deserDescribe(t, r, tc, hexDecode(wireHex), []string{"a", "b"})
	}()

	return wantGot("buffers/ser_wire_object",
		obj(
			kv("wire", s("6f22016149022201622201787b02")),
			kv("roundtrip", obj(
				kv("read", obj(
					kv("type", s("object")),
					kv("a", obj(kv("type", s("int32")), kv("value", i(1)))),
					kv("b", obj(kv("type", s("string")), kv("value", s("x")))))),
				kv("caught", b(false)),
				kv("message", s("")))),
		),
		obj(
			kv("wire", s(wireHex)),
			kv("roundtrip", roundtrip),
		))
}

func checkSerArrayBufferClone(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	bs, err := r.iso.NewBackingStoreFromSlice([]byte{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	defer func() { _ = bs.Close() }()
	ab, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, bs)
	if err != nil {
		t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
	}

	var outcome serOutcome
	sourceLenAfter := 0
	func() {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		outcome = serialize(t, r, tc, ab.Value)
		sourceLenAfter, _ = ab.ByteLength()
	}()

	var roundtrip jsonValue
	func() {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		roundtrip, _ = deserDescribe(t, r, tc, outcome.wire, nil)
	}()

	return wantGot("buffers/ser_array_buffer_clone",
		obj(
			kv("ok", b(true)),
			kv("wire", s("420401020304")),
			kv("source_byte_length_after_write", i(4)),
			kv("roundtrip", obj(
				kv("read", obj(
					kv("type", s("arraybuffer")),
					kv("byte_length", i(4)),
					kv("contents", s("01020304")))),
				kv("caught", b(false)),
				kv("message", s("")))),
		),
		obj(
			kv("ok", b(outcome.ok)),
			kv("wire", s(lowerHex(outcome.wire))),
			kv("source_byte_length_after_write", i(int64(sourceLenAfter))),
			kv("roundtrip", roundtrip),
		))
}

func checkSerTransferSemantics(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	bs, err := r.iso.NewBackingStoreFromSlice([]byte{9, 8, 7, 6})
	if err != nil {
		t.Fatalf("NewBackingStoreFromSlice: %v", err)
	}
	defer func() { _ = bs.Close() }()
	ab, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, bs)
	if err != nil {
		t.Fatalf("NewArrayBufferWithBackingStore: %v", err)
	}

	okWrite := false
	wireHex := ""
	sourceLenAfter := 0
	sourceDetached := false
	func() {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		outcome := serializeWith(t, r, tc, ab.Value, func(ser *gov8.ValueSerializer) {
			if err := ser.TransferArrayBuffer(7, ab); err != nil {
				t.Fatalf("TransferArrayBuffer: %v", err)
			}
		})
		okWrite = outcome.ok
		wireHex = lowerHex(outcome.wire)
		sourceLenAfter, _ = ab.ByteLength()
		sourceDetached, _ = ab.WasDetached()
	}()

	// Receiving side registers transfer id 7 against a fresh zeroed buffer.
	withTransfer := func() jsonValue {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		targetBS, err := r.iso.NewBackingStoreFromSlice(make([]byte, 4))
		if err != nil {
			t.Fatalf("target store: %v", err)
		}
		defer func() { _ = targetBS.Close() }()
		target, err := gov8.NewArrayBufferWithBackingStore(r.scope, r.ctx, targetBS)
		if err != nil {
			t.Fatalf("target buffer: %v", err)
		}
		vd, err := gov8.NewValueDeserializer(r.scope, r.ctx, hexDecode(wireHex))
		if err != nil {
			t.Fatalf("NewValueDeserializer: %v", err)
		}
		defer func() { _ = vd.Close() }()
		if err := vd.TransferArrayBuffer(7, target); err != nil {
			t.Fatalf("TransferArrayBuffer: %v", err)
		}
		described := jsonValue(jsonNull{})
		if v, rerr := vd.ReadValue(r.ctx, tc); rerr == nil {
			described = describeValue(t, r, v, nil)
		}
		caught, _ := tc.HasCaught()
		targetLen, _ := target.ByteLength()
		targetContents := make([]byte, 4)
		_, _ = targetBS.ReadAt(targetContents, 0)
		return obj(
			kv("read", described),
			kv("caught", b(caught)),
			kv("target_byte_length", i(int64(targetLen))),
			kv("target_contents", s(lowerHex(targetContents))))
	}()

	// Without registering the id, deserialization must fail with a caught,
	// deterministic error message.
	withoutTransfer := func() jsonValue {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		out, _ := deserDescribe(t, r, tc, hexDecode(wireHex), nil)
		return out
	}()

	return wantGot("buffers/ser_transfer_semantics",
		obj(
			kv("ok", b(true)),
			kv("wire", s("7407")),
			// Pinned V8 no longer detaches the source at write time.
			kv("source_byte_length_after_write", i(4)),
			kv("source_was_detached", b(false)),
			kv("with_transfer", obj(
				kv("read", obj(
					kv("type", s("arraybuffer")),
					kv("byte_length", i(4)),
					// Transfer reuses the receiving buffer's own store.
					kv("contents", s("00000000")))),
				kv("caught", b(false)),
				kv("target_byte_length", i(4)),
				kv("target_contents", s("00000000")))),
			kv("without_transfer", obj(
				kv("read", jsonNull{}),
				kv("caught", b(true)),
				kv("message", s("Uncaught Error: Unable to deserialize cloned data.")))),
		),
		obj(
			kv("ok", b(okWrite)),
			kv("wire", s(wireHex)),
			kv("source_byte_length_after_write", i(int64(sourceLenAfter))),
			kv("source_was_detached", b(sourceDetached)),
			kv("with_transfer", withTransfer),
			kv("without_transfer", withoutTransfer),
		))
}

func checkSerUnserializableFunction(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	isFunction := false
	outcome := serOutcome{}
	caught := false
	message := ""
	func() {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		v, ok := r.eval(t, tc, "() => 1")
		if !ok {
			t.Fatal("eval function failed")
		}
		isFunction, _ = v.IsFunction()
		outcome = serialize(t, r, tc, v)
		caught, _ = tc.HasCaught()
		message, _ = tc.MessageText(r.scope, r.ctx)
	}()

	return wantGot("buffers/ser_unserializable_function",
		obj(
			kv("is_function", b(true)),
			kv("write_ok", b(false)),
			kv("wire", s("")),
			kv("clone_error", s("() => 1 could not be cloned.")),
			kv("caught", b(true)),
			kv("message", s("Uncaught Error: () => 1 could not be cloned.")),
		),
		obj(
			kv("is_function", b(isFunction)),
			kv("write_ok", b(outcome.ok)),
			kv("wire", s(lowerHex(outcome.wire))),
			kv("clone_error", s(outcome.cloneError)),
			kv("caught", b(caught)),
			kv("message", s(message)),
		))
}

func checkSerDeserializeInvalid(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	type entry struct {
		name string
		val  jsonValue
	}
	entries := make([]entry, 0, 4)
	add := func(name string, data []byte) {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		out, _ := deserDescribe(t, r, tc, data, nil)
		entries = append(entries, entry{name, out})
	}
	add("empty", nil)
	add("truncated_header", []byte{0xFF})
	add("bad_header", []byte{0x00, 0x00})
	add("truncated_body", []byte{0xFF, 0x0D, 0x42})

	actual := jsonObj{}
	for _, e := range entries {
		actual = append(actual, kv(e.name, e.val))
	}

	unable := "Uncaught Error: Unable to deserialize cloned data."
	hostObject := "Uncaught Error: Deno deserializer: read_host_object not implemented"
	rejected := func(message string) jsonValue {
		return obj(
			kv("read", jsonNull{}),
			kv("caught", b(true)),
			kv("message", s(message)))
	}
	expected := jsonObj{
		kv("empty", rejected(unable)),
		kv("truncated_header", rejected(hostObject)),
		kv("bad_header", rejected(unable)),
		kv("truncated_body", rejected(hostObject)),
	}
	return wantGot("buffers/ser_deserialize_invalid", expected, actual)
}

func checkSerDetachedSource(t tester) obs {
	r := newRuntime(t)
	defer r.close(t)

	ab, err := gov8.NewArrayBuffer(r.scope, r.ctx, 4)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	detached, err := ab.Detach(r.ctx, gov8.Value{})
	if err != nil {
		t.Fatalf("detach: %v", err)
	}

	var outcome serOutcome
	func() {
		tc, err := r.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		defer func() { _ = tc.Close() }()
		outcome = serialize(t, r, tc, ab.Value)
	}()

	return wantGot("buffers/ser_detached_source",
		obj(
			kv("detached", b(true)),
			kv("write_ok", b(false)),
			kv("wire", s("")),
			kv("clone_error", s("An ArrayBuffer is detached and could not be cloned.")),
		),
		obj(
			kv("detached", b(detached)),
			kv("write_ok", b(outcome.ok)),
			kv("wire", s(lowerHex(outcome.wire))),
			kv("clone_error", s(outcome.cloneError)),
		))
}

// --- registry ------------------------------------------------------------------------

// checkFn is one check producing its normalized outcome.
type checkFn func(tester) obs

// allChecks is the fixed oracle order.
func allChecks() []checkFn {
	return []checkFn{
		checkABNewBasics,
		checkABBackingStoreOwnership,
		checkABBackingStoreAlias,
		checkABBackingStoreSharedSAB,
		checkSABBackingStoreOwnedExternal,
		checkABResizableBackingStore,
		checkABDetachBasic,
		checkABDetachKeyGate,
		checkABDetachViewsFollow,
		checkABDetachJSTransfer,
		checkViewTypedArrayBounds,
		checkViewDataViewBounds,
		checkViewMaxSizes,
		checkExtBackingStoreDeleter,
		checkSerWirePrimitives,
		checkSerWireObject,
		checkSerArrayBufferClone,
		checkSerTransferSemantics,
		checkSerUnserializableFunction,
		checkSerDeserializeInvalid,
		checkSerDetachedSource,
	}
}
