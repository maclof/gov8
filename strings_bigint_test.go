//go:build windows && amd64

package gov8_test

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

// Unit tests for the advanced strings / external strings / BigInt slice,
// complementing the byte-for-byte fixture conformance in
// conformance-strings-bigint: API-shape behavior, lifecycle, and the
// observable representation rules, on top of the fixture's exact values.
//
// Thread-affinity note: every subtest body runs on its own goroutine, so
// each subtest creates its own isolate (NewIsolate pins that goroutine to
// the owning OS thread) rather than sharing the parent's.

func TestSBStringCreationRepresentations(t *testing.T) {
	t.Run("latin1 one-byte", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		v, err := scope.NewStringFromOneByte([]byte{0xE9, 0x41}, gov8.StringNormal)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := v.Length(); n != 2 {
			t.Fatalf("length = %d, want 2", n)
		}
		if n, _ := v.Utf8Length(); n != 3 {
			t.Fatalf("utf8_length = %d, want 3", n)
		}
		if ok, _ := v.IsOneByte(); !ok {
			t.Fatal("IsOneByte = false")
		}
	})

	t.Run("twobyte latin1-representable collapses", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		v, err := scope.NewStringFromTwoByte([]uint16{0xE9}, gov8.StringNormal)
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := v.IsOneByte(); !ok {
			t.Fatal("Latin-1 content via the two-byte entry must collapse to one-byte")
		}
	})

	t.Run("surrogate pair stays two-byte", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		v, err := scope.NewStringFromTwoByte([]uint16{0xD83E, 0xDD80}, gov8.StringNormal)
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := v.IsOneByte(); ok {
			t.Fatal("surrogate pair must not be one-byte")
		}
		if ok, _ := v.ContainsOnlyOneByte(); ok {
			t.Fatal("ContainsOnlyOneByte = true for emoji")
		}
	})

	t.Run("internalized equals normal content", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		a, err := scope.NewStringFromUTF8([]byte("intern"), gov8.StringInternalized)
		if err != nil {
			t.Fatal(err)
		}
		b, err := scope.NewString("intern")
		if err != nil {
			t.Fatal(err)
		}
		eq, err := a.StrictEquals(b)
		if err != nil {
			t.Fatal(err)
		}
		if !eq {
			t.Fatal("internalized string must equal the normal string")
		}
	})

	t.Run("lossy utf8 decode", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		v, err := scope.NewStringFromUTF8([]byte{'a', 0xFF, 'b'}, gov8.StringNormal)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := v.Length(); n != 3 {
			t.Fatalf("length = %d, want 3 (a + U+FFFD + b)", n)
		}
		txt, err := v.ToString(ctx)
		if err != nil || txt != "a\uFFFDb" {
			t.Fatalf("text = %q, %v", txt, err)
		}
		_ = iso
	})
}

func TestSBStringMaxLengthConstant(t *testing.T) {
	ml, err := gov8.StringMaxLength()
	if err != nil {
		t.Fatal(err)
	}
	if ml != 536870888 {
		t.Fatalf("StringMaxLength = %d, want 536870888 ((1<<29)-24)", ml)
	}
}

func TestSBStringWrites(t *testing.T) {
	_, _, scope := newTestRuntime(t)
	if _, err := scope.NewString("ab\U0001F980"); err != nil {
		t.Fatal(err)
	}

	t.Run("partial and remainder", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		s, err := scope.NewString("ab\U0001F980")
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]uint16, 2)
		n, err := s.WriteTwoByte(0, buf, 0)
		if err != nil || n != 2 {
			t.Fatalf("n = %d, %v", n, err)
		}
		rest := make([]uint16, 8)
		n, err = s.WriteTwoByte(2, rest, 0)
		if err != nil || n != 2 {
			t.Fatalf("remainder n = %d, %v", n, err)
		}
		if rest[0] != 0xD83E || rest[1] != 0xDD80 {
			t.Fatalf("remainder = %v", rest[:2])
		}
	})

	t.Run("null terminate counts toward capacity", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		s, err := scope.NewString("ab\U0001F980")
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]uint16, 5)
		n, err := s.WriteTwoByte(0, buf, gov8.WriteNullTerminate)
		if err != nil || n != 4 {
			t.Fatalf("n = %d, %v", n, err)
		}
		if buf[4] != 0 {
			t.Fatalf("NUL missing: %v", buf)
		}
	})

	t.Run("out of range offsets are rejected before the engine", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		s, err := scope.NewString("abc")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.WriteTwoByte(4, make([]uint16, 4), 0); err == nil {
			t.Fatal("offset beyond length accepted")
		}
		if _, err := s.WriteTwoByte(-1, make([]uint16, 4), 0); err == nil {
			t.Fatal("negative offset accepted")
		}
		// The wrapper CLAMPS to the remaining range (the Rust API would
		// read past the string here); the clamped write succeeds.
		buf := make([]uint16, 1)
		n, err := s.WriteTwoByte(1, buf, 0)
		if err != nil || n != 1 || buf[0] != 'b' {
			t.Fatalf("clamped write = (%d, %v), buf %v", n, err, buf)
		}
		// The string still works after every rejection.
		full := make([]uint16, 4)
		n, err = s.WriteTwoByte(0, full, 0)
		if err != nil || n != 3 {
			t.Fatalf("post-guard write = (%d, %v)", n, err)
		}
	})

	t.Run("one byte low-byte truncation", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		euro, err := scope.NewString("\u20AC")
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1)
		n, err := euro.WriteOneByte(0, buf, 0)
		if err != nil || n != 1 {
			t.Fatalf("n = %d, %v", n, err)
		}
		if buf[0] != 0xAC {
			t.Fatalf("low byte = %02x, want ac", buf[0])
		}
	})

	t.Run("utf8 capacity truncation never splits a sequence", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		emoji, err := scope.NewString("a\U0001F980b")
		buf := make([]byte, 4)
		n, processed, err := emoji.WriteUTF8(buf, 0)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 || processed != 1 {
			t.Fatalf("n = %d, processed = %d; want 1/1 (the 4-byte emoji never partially fits)", n, processed)
		}
		full := make([]byte, 32)
		n, processed, err = emoji.WriteUTF8(full, 0)
		if err != nil {
			t.Fatal(err)
		}
		if n != 6 || processed != 4 {
			t.Fatalf("full n = %d, processed = %d; want 6/4 (1+4+1 bytes over 4 units)", n, processed)
		}
	})

	t.Run("utf8 lone surrogate flag behavior", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		lone, err := scope.NewStringFromTwoByte([]uint16{0xD83E, 0x0061}, gov8.StringNormal)
		if err != nil {
			t.Fatal(err)
		}
		raw := make([]byte, 32)
		n, _, err := lone.WriteUTF8(raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw[:n]) != "\xED\xA0\xBEa" {
			t.Fatalf("raw encoding = %x", raw[:n])
		}
		fixed := make([]byte, 32)
		n, _, err = lone.WriteUTF8(fixed, gov8.WriteReplaceInvalidUTF8)
		if err != nil {
			t.Fatal(err)
		}
		if string(fixed[:n]) != "\uFFFDa" {
			t.Fatalf("replacement encoding = %x", fixed[:n])
		}
	})

	t.Run("null terminate requires capacity", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		s, err := scope.NewString("abc")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.WriteUTF8(nil, gov8.WriteNullTerminate); err == nil {
			t.Fatal("kNullTerminate with capacity 0 must be rejected (documented OOB write)")
		}
		if _, _, err := s.WriteUTF8(nil, 0); err != nil {
			t.Fatalf("empty buffer without null terminate: %v", err)
		}
	})
}

func TestSBConcatString(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	a, err := scope.NewString("foo")
	if err != nil {
		t.Fatal(err)
	}
	b, err := scope.NewString("bar")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := scope.ConcatString(a, b)
	if err != nil {
		t.Fatal(err)
	}
	txt, err := cat.ToString(ctx)
	if err != nil || txt != "foobar" {
		t.Fatalf("concat = %q, %v", txt, err)
	}
	empty, err := scope.EmptyString()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := scope.ConcatString(empty, b)
	if err != nil {
		t.Fatal(err)
	}
	txt, err = identity.ToString(ctx)
	if err != nil || txt != "bar" {
		t.Fatalf("empty-left concat = %q, %v", txt, err)
	}
	_ = iso
}

func TestSBStringViewLifecycle(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	_ = iso
	_ = ctx
	ascii, err := scope.NewString("hello")
	if err != nil {
		t.Fatal(err)
	}
	view, err := scope.NewStringView(ascii)
	if err != nil {
		t.Fatal(err)
	}

	oneByte, length, err := view.Info()
	if err != nil || !oneByte || length != 5 {
		t.Fatalf("info = %v/%d, %v", oneByte, length, err)
	}
	data, _, err := view.Bytes()
	if err != nil || string(data) != "hello" {
		t.Fatalf("bytes = %q, %v", data, err)
	}
	if err := view.Close(); err != nil {
		t.Fatal(err)
	}
	// Deterministic Close: further use is an error, double Close included.
	if _, _, err := view.Info(); err == nil {
		t.Fatal("Info after Close succeeded")
	}
	if _, _, err := view.Bytes(); err == nil {
		t.Fatal("Bytes after Close succeeded")
	}
	if err := view.Close(); err == nil {
		t.Fatal("double Close succeeded")
	}

	t.Run("non-string rejected", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		n, err := scope.Number(1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := scope.NewStringView(n); err == nil {
			t.Fatal("view over a number succeeded")
		}
	})

	t.Run("two-byte view exposes units", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		euro, err := scope.NewString("\u20AC")
		if err != nil {
			t.Fatal(err)
		}
		v, err := scope.NewStringView(euro)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = v.Close() }()
		oneByte, length, err := v.Info()
		if err != nil {
			t.Fatal(err)
		}
		if oneByte || length != 1 {
			t.Fatalf("euro view = oneByte %v, len %d", oneByte, length)
		}
		data, oneByte, err := v.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if oneByte || len(data) != 2 || data[0] != 0xAC || data[1] != 0x20 {
			t.Fatalf("euro view data = %x (oneByte %v)", data, oneByte)
		}
	})
}

func TestSBExternalStringFlavors(t *testing.T) {
	t.Run("static", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		v, err := scope.NewExternalOneByteStringStatic([]byte("static_ext"))
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := v.IsExternalOneByte(); !ok {
			t.Fatal("static one-byte not external")
		}
		res, data, ok, err := v.GetExternalOneByteStringResource()
		if err != nil || !ok || string(data) != "static_ext" || len(data) != 10 {
			t.Fatalf("resource = %x %q %v %v", res, data, ok, err)
		}
		res2, data2, _, err := v.GetExternalOneByteStringResource()
		if err != nil || res != res2 || string(data2) != string(data) {
			t.Fatal("resource getter not stable")
		}
	})

	t.Run("const shared across two isolates", func(t *testing.T) {
		res, err := gov8.CreateExternalOneByteConst("konst")
		if err != nil {
			t.Fatal(err)
		}
		if res.Data() != "konst" {
			t.Fatalf("Data = %q", res.Data())
		}
		// Two live isolates on the owning thread (the entered-isolate model
		// supports this); the SAME const resource backs both strings.
		isoA, err := gov8.NewIsolate()
		if err != nil {
			t.Fatal(err)
		}
		isoB, err := gov8.NewIsolate()
		if err != nil {
			t.Fatal(err)
		}
		scopeA, err := isoA.NewScope()
		if err != nil {
			t.Fatal(err)
		}
		scopeB, err := isoB.NewScope()
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = scopeA.Close()
			_ = scopeB.Close()
			_ = isoB.Close()
			_ = isoA.Close()
		}()
		va, err := scopeA.NewStringFromOneByteConst(res)
		if err != nil {
			t.Fatal(err)
		}
		vb, err := scopeB.NewStringFromOneByteConst(res)
		if err != nil {
			t.Fatal(err)
		}
		ptrA, _, okA, err := va.GetExternalOneByteStringResource()
		if err != nil || !okA {
			t.Fatalf("isolate A resource: %v %v", okA, err)
		}
		ptrB, dataB, okB, err := vb.GetExternalOneByteStringResource()
		if err != nil || !okB {
			t.Fatalf("isolate B resource: %v %v", okB, err)
		}
		if ptrA != ptrB {
			t.Fatalf("const resource differs across isolates: %x != %x", ptrA, ptrB)
		}
		if string(dataB) != "konst" {
			t.Fatalf("isolate B data = %q", dataB)
		}
	})

	t.Run("owned survives GC while held", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		units := []uint16{0x42, 0x43}
		v, err := scope.NewExternalTwoByteString(units)
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := v.IsExternalTwoByte(); !ok {
			t.Fatal("owned two-byte not external")
		}
		if err := iso.LowMemoryNotification(); err != nil {
			t.Fatal(err)
		}
		txt, err := v.ToString(ctx)
		if err != nil || txt != "BC" {
			t.Fatalf("after GC text = %q, %v", txt, err)
		}
	})

	t.Run("plain strings have no resources", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		plain, err := scope.NewString("plain")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, ok, _ := plain.GetExternalOneByteStringResource(); ok {
			t.Fatal("one-byte getter resolved for a plain string")
		}
		if _, ok, _ := plain.GetExternalStringResource(); ok {
			t.Fatal("generic getter resolved for a plain string")
		}
		if _, _, ok, _ := plain.GetExternalStringResourceBase(); ok {
			t.Fatal("base getter resolved for a plain string")
		}
	})
}

func TestSBExternalRawDeleter(t *testing.T) {
	// Own runtime (not newTestRuntime): this test closes the scope early
	// by design, which would trip the shared cleanup's Close.
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ctx.Close(); err != nil {
			t.Errorf("ctx.Close: %v", err)
		}
		if err := iso.Close(); err != nil {
			t.Errorf("iso.Close: %v", err)
		}
	}()

	var calls, gotLen atomic.Int64
	var gotPtr, handed uintptr

	v, payload, err := scope.NewExternalOneByteStringRaw(
		[]byte{7, 7, 7}, func(data uintptr, length int) {
			calls.Add(1)
			gotLen.Store(int64(length))
			gotPtr = data
		})
	if err != nil {
		t.Fatal(err)
	}
	handed = payload
	if ok, _ := v.IsExternalOneByte(); !ok {
		t.Fatal("raw one-byte not external")
	}

	// No deleter call while the local holds the string alive.
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("deleter fired while alive: %d calls", calls.Load())
	}
	txt, err := v.ToString(ctx)
	if err != nil || txt != "\a\a\a" {
		t.Fatalf("text = %q, %v", txt, err)
	}

	// Root it in a Global, drop the local by closing the scope, force a
	// major GC: the deleter fires exactly once with the handed-off pointer.
	global, err := gov8.NewGlobal(scope, v)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("deleter fired while the Global held the string")
	}
	if err := global.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("deleter calls after GC = %d, want 1", calls.Load())
	}
	if gotLen.Load() != 3 || gotPtr != handed {
		t.Fatalf("deleter args = (ptr %x, len %d), want (%x, 3)", gotPtr, gotLen.Load(), handed)
	}
	// Subsequent GCs are no-ops for the deleter.
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("deleter fired again: %d calls", calls.Load())
	}
}

func TestSBBigIntWords(t *testing.T) {
	t.Run("construction and text", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		_ = iso
		cases := []struct {
			sign  bool
			words []uint64
			text  string
			wc    int
		}{
			{false, nil, "0", 0},
			{true, nil, "0", 0}, // sign over zero words normalizes to zero
			{false, []uint64{1}, "1", 1},
			{false, []uint64{^uint64(0)}, "18446744073709551615", 1},
			{true, []uint64{3}, "-3", 1},
			{false, []uint64{^uint64(0), 1}, "36893488147419103231", 2},
		}
		for _, tc := range cases {
			v, err := scope.BigIntFromWords(ctx, tc.sign, tc.words, nil)
			if err != nil {
				t.Fatalf("%v/%v: %v", tc.sign, tc.words, err)
			}
			txt, err := v.ToString(ctx)
			if err != nil || txt != tc.text {
				t.Fatalf("text = %q, want %q (%v)", txt, tc.text, err)
			}
			wc, err := v.BigIntWordCount()
			if err != nil || wc != tc.wc {
				t.Fatalf("word_count = %d, want %d (%v)", wc, tc.wc, err)
			}
		}
	})

	t.Run("extraction truncation and sign", func(t *testing.T) {
		iso, ctx, scope := newTestRuntime(t)
		_ = iso
		v, err := scope.BigIntFromWords(ctx, true, []uint64{3}, nil)
		if err != nil {
			t.Fatal(err)
		}
		buf := []uint64{0xDE, 0xAD, 0xBE, 0xEF}
		sign, words, err := v.BigIntToWords(buf)
		if err != nil {
			t.Fatal(err)
		}
		if !sign {
			t.Fatal("sign = false for a negative BigInt")
		}
		if len(words) != 1 || words[0] != 3 {
			t.Fatalf("words = %v", words)
		}
		if buf[1] != 0xAD {
			t.Fatalf("tail overwritten: %x", buf)
		}
	})

	t.Run("u64 view of negative is two's complement", func(t *testing.T) {
		_, _, scope := newTestRuntime(t)
		v, err := scope.BigIntFromInt64(-1)
		if err != nil {
			t.Fatal(err)
		}
		u, lossless, err := v.BigIntUint64()
		if err != nil {
			t.Fatal(err)
		}
		if u != ^uint64(0) || lossless {
			t.Fatalf("u64 view = %d/%v, want max/false", u, lossless)
		}
	})
}

func TestSBBigIntOverLimitIsRepeatableRangeError(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)

	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tc.Close(); err != nil {
			t.Errorf("tc.Close: %v", err)
		}
	}()

	for attempt := 0; attempt < 2; attempt++ {
		words := make([]uint64, 16_777_216)
		_, err := scope.BigIntFromWords(ctx, false, words, tc)
		if err == nil {
			t.Fatalf("attempt %d: over-limit construction succeeded", attempt)
		}
		caught, _ := tc.HasCaught()
		terminated, _ := tc.HasTerminated()
		if !caught || terminated {
			t.Fatalf("attempt %d: caught=%v terminated=%v", attempt, caught, terminated)
		}
		msg, _ := tc.MessageText(scope, ctx)
		if msg != "Uncaught RangeError: Maximum BigInt size exceeded" {
			t.Fatalf("attempt %d: message = %q", attempt, msg)
		}
		if err := tc.Reset(); err != nil {
			t.Fatal(err)
		}
		caught, _ = tc.HasCaught()
		if caught {
			t.Fatalf("attempt %d: reset did not clear the catch", attempt)
		}
	}

	// Full recovery: scripts and BigInt construction work after the reset.
	v, err := scope.BigIntFromInt64(-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, lossless, _ := v.BigIntInt64(); !lossless {
		t.Fatal("BigInt construction broken after recovery")
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
}

func TestSBStringAffinity(t *testing.T) {
	// Engine resources are thread-affine: the string APIs must refuse
	// wrong-thread use with errors, never crash. The isolate is created on
	// this test's goroutine (pinned); the helper goroutine below is not.
	// Own runtime: the deferred closes here would trip the shared cleanup.
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Close(); err != nil {
			t.Errorf("scope.Close: %v", err)
		}
		if err := iso.Close(); err != nil {
			t.Errorf("iso.Close: %v", err)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	errCh := make(chan error, 1)
	go func() {
		defer wg.Done()
		_, err := scope.NewStringFromOneByte([]byte{0x41}, gov8.StringNormal)
		errCh <- err
	}()
	wg.Wait()
	errAffinity := <-errCh
	if errAffinity == nil || !strings.Contains(errAffinity.Error(), "affinity") {
		t.Fatalf("wrong-thread creation error = %v", errAffinity)
	}
}
