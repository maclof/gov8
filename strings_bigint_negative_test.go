//go:build windows && amd64

package gov8_test

import (
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

// Negative and boundary characterization for the advanced strings/BigInt
// slice, mirroring rust-oracle/tests/strings_bigint_negative.rs:
//
//   - Over-StringMaxLength creation is a recoverable Go error for all three
//     entry points, with no exception and no isolate damage (the fixture
//     does not carry the ~1 GiB boundary probes; skipped under -short).
//   - Write range violations are rejected in the wrapper and the shim
//     BEFORE the engine is touched. The pinned release engine only DCHECKs
//     String::Write ranges (WriteToFlat reads past the string unchecked)
//     and does not bounds-check the WriteUtf8 NUL against capacity 0, so
//     out-of-range values are undefined behavior there — this port converts
//     them to errors and the UB boundary is never executed anywhere.
//   - A raw external string alive at isolate disposal fires its deleter
//     exactly once during Isolate.Close (external-string-table teardown).
//   - A StringView closed before a forced GC keeps the isolate usable.
//
// # Why there are no V8-fatal child-process probes
//
// Unlike the core-advanced / buffers slices, every failure reachable
// through this API surface is recoverable (a Go error, optionally with a
// pending JS exception the caller's TryCatch observes). The genuinely
// unsafe boundaries are undefined behavior in the pinned release engine,
// not deterministic fatals, and are deliberately never executed: writes
// past the string range, kNullTerminate into a zero-capacity UTF-8 buffer,
// keeping a StringView open across allocations, or touching an external
// buffer from Go after handoff (ownership moved to the shim at creation;
// the deleter protocol frees it exactly once). Wrong-isolate and
// wrong-thread misuse is caught by wrapper checks and returned as errors
// (verified below) rather than being observable process states.

// --- recoverable creation bounds ------------------------------------------------

func TestSBLatin1ToUTF8RejectsShortOutputWithoutMutation(t *testing.T) {
	out := []byte{0xa5, 0xa5, 0xa5}
	if _, err := gov8.Latin1ToUTF8([]byte{0x41, 0xff}, out); err == nil {
		t.Fatal("short output accepted")
	}
	if want := []byte{0xa5, 0xa5, 0xa5}; !slices.Equal(out, want) {
		t.Fatalf("output changed on error: %x", out)
	}
}

func TestSBStringAndBigIntReadersRejectWrongTypesAndLifetimes(t *testing.T) {
	iso, _, scope := newTestRuntime(t)
	number, err := scope.Number(1)
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"StringValue": func() error { _, err := number.StringValue(); return err },
		"Utf8Length":  func() error { _, err := number.Utf8Length(); return err },
		"Length":      func() error { _, err := number.Length(); return err },
		"BigIntInt64": func() error { _, _, err := number.BigIntInt64(); return err },
		"BigIntUint64": func() error {
			_, _, err := number.BigIntUint64()
			return err
		},
		"BigIntWordCount": func() error { _, err := number.BigIntWordCount(); return err },
		"BigIntToWords": func() error {
			_, _, err := number.BigIntToWords(make([]uint64, 1))
			return err
		},
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "not a") {
			t.Errorf("%s wrong-type error = %v", name, err)
		}
	}

	closedScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	str, err := closedScope.NewString("closed")
	if err != nil {
		t.Fatal(err)
	}
	big, err := closedScope.BigIntFromInt64(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := str.Length(); err == nil {
		t.Fatal("Length accepted a closed local")
	}
	if _, _, err := big.BigIntInt64(); err == nil {
		t.Fatal("BigIntInt64 accepted a closed local")
	}

	errCh := make(chan error, 2)
	stringValue, err := scope.NewString("thread")
	if err != nil {
		t.Fatal(err)
	}
	bigint, err := scope.BigIntFromInt64(2)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, err := stringValue.Length(); errCh <- err }()
	go func() { _, _, err := bigint.BigIntUint64(); errCh <- err }()
	for range 2 {
		if err := <-errCh; err == nil || !strings.Contains(err.Error(), "affinity") {
			t.Fatalf("wrong-thread reader error = %v", err)
		}
	}
}

func TestSBStringCreationOverMaxLengthIsError(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates ~1 GiB transient buffers")
	}
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
		if err := scope.Close(); err != nil {
			t.Errorf("scope.Close: %v", err)
		}
		if err := ctx.Close(); err != nil {
			t.Errorf("ctx.Close: %v", err)
		}
		if err := iso.Close(); err != nil {
			t.Errorf("iso.Close: %v", err)
		}
	}()

	maxLength, err := gov8.StringMaxLength()
	if err != nil {
		t.Fatal(err)
	}

	// One byte past the limit: UTF-8 and one-byte share the same bound.
	bytes := make([]byte, maxLength+1)
	for i := range bytes {
		bytes[i] = 'a'
	}
	if _, err := scope.NewStringFromUTF8(bytes, gov8.StringNormal); err == nil {
		t.Fatal("NewStringFromUTF8 accepted input longer than StringMaxLength")
	}
	if _, err := scope.NewStringFromOneByte(bytes, gov8.StringNormal); err == nil {
		t.Fatal("NewStringFromOneByte accepted input longer than StringMaxLength")
	}

	// The two-byte entry counts code units, so the boundary buffer is twice
	// as wide (~1 GiB transient).
	units := make([]uint16, maxLength+1)
	if _, err := scope.NewStringFromTwoByte(units, gov8.StringNormal); err == nil {
		t.Fatal("NewStringFromTwoByte accepted input longer than StringMaxLength units")
	}

	// The isolate is unharmed: exactly at the limit still works.
	atLimit := make([]byte, maxLength)
	atLimit[0] = 'b'
	atLimit[len(atLimit)-1] = 'b'
	atLimitString, err := scope.NewStringFromOneByte(atLimit, gov8.StringNormal)
	if err != nil {
		t.Fatalf("MAX_LENGTH-sized input rejected: %v", err)
	}
	if n, _ := atLimitString.Length(); n != maxLength {
		t.Fatalf("at-limit length = %d, want %d", n, maxLength)
	}
}

// --- write-range guards (converted UB) ------------------------------------------

func TestSBWriteRangeGuardsFireBeforeTheEngine(t *testing.T) {
	_, _, scope := newTestRuntime(t)
	s, err := scope.NewString("abc")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := s.Length()
	if n != 3 {
		t.Fatalf("length = %d", n)
	}

	// Offset at the very end with capacity is legal and writes nothing.
	buf := make([]uint16, 4)
	got, err := s.WriteTwoByte(3, buf, 0)
	if err != nil || got != 0 {
		t.Fatalf("end-offset write = (%d, %v)", got, err)
	}

	// Offset beyond the length and negative offsets are rejected as plain
	// Go errors. A capacity smaller than the remaining range is CLAMPED to
	// the remaining range (documented deviation: the Rust API would read
	// past the string here; this port clamps instead of UB).
	if _, err := s.WriteTwoByte(4, buf, 0); err == nil {
		t.Fatal("offset beyond length accepted")
	}
	if _, err := s.WriteOneByte(4, make([]byte, 4), 0); err == nil {
		t.Fatal("one-byte offset beyond length accepted")
	}
	if got, err := s.WriteTwoByte(1, make([]uint16, 1), 0); err != nil || got != 1 {
		t.Fatalf("clamped remainder write = (%d, %v)", got, err)
	}

	// The string still works after every rejection.
	got, err = s.WriteTwoByte(0, buf, 0)
	if err != nil || got != 3 {
		t.Fatalf("post-guard write = (%d, %v)", got, err)
	}
}

// --- deleter at isolate disposal ---------------------------------------------------

func TestSBExternalStringAliveAtIsolateCloseFiresDeleterOnce(t *testing.T) {
	var calls, gotLen atomic.Int64
	gotPtr, handed := uintptr(0), uintptr(0)

	func() {
		iso, err := gov8.NewIsolate()
		if err != nil {
			t.Fatal(err)
		}
		var held *gov8.Global
		func() {
			scope, err := iso.NewScope()
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := iso.NewContext()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := ctx.Close(); err != nil {
					t.Errorf("ctx.Close: %v", err)
				}
			}()
			s, payload, err := scope.NewExternalOneByteStringRaw(
				[]byte{7, 7, 7, 7, 7, 7, 7, 7, 7},
				func(data uintptr, length int) {
					calls.Add(1)
					gotLen.Store(int64(length))
					atomic.StoreUintptr(&gotPtr, data)
				})
			if err != nil {
				t.Fatal(err)
			}
			handed = payload
			if txt, terr := s.ToString(ctx); terr != nil || txt != "\a\a\a\a\a\a\a\a\a" {
				t.Fatalf("text = %q, %v", txt, terr)
			}
			held, err = gov8.NewGlobal(scope, s)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.Close(); err != nil {
				t.Fatal(err)
			}
		}()
		if calls.Load() != 0 {
			t.Fatalf("deleter fired before disposal: %d", calls.Load())
		}
		// The Global still holds the string; disposal finalizes it through
		// the external-string-table teardown.
		if err := iso.Close(); err != nil {
			t.Fatalf("iso.Close: %v", err)
		}
		// Global.Close after isolate disposal is a documented silent no-op.
		if err := held.Close(); err != nil {
			t.Fatalf("held.Close after disposal: %v", err)
		}
	}()

	if calls.Load() != 1 {
		t.Fatalf("deleter calls after isolate disposal = %d, want 1", calls.Load())
	}
	if gotLen.Load() != 9 {
		t.Fatalf("deleter length = %d, want 9", gotLen.Load())
	}
	if atomic.LoadUintptr(&gotPtr) != handed {
		t.Fatalf("deleter pointer = %x, want handed-off %x", atomic.LoadUintptr(&gotPtr), handed)
	}
}

// --- view lifecycle -----------------------------------------------------------------

func TestSBStringViewClosedBeforeGCKeepsIsolateUsable(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	_ = ctx
	s, err := scope.NewString("view-then-gc")
	if err != nil {
		t.Fatal(err)
	}
	view, err := scope.NewStringView(s)
	if err != nil {
		t.Fatal(err)
	}
	oneByte, _, err := view.Info()
	if err != nil || !oneByte {
		t.Fatalf("info = %v, %v", oneByte, err)
	}
	// The safe pattern: the view is closed before any GC.
	if err := view.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.LowMemoryNotification(); err != nil {
		t.Fatal(err)
	}
	txt, err := s.ToString(ctx)
	if err != nil || txt != "view-then-gc" {
		t.Fatalf("after GC text = %q, %v", txt, err)
	}

	// The isolate still runs JavaScript afterwards.
	script, err := ctx.Compile(scope, "'still' + 'alive'", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Errorf("script.Close: %v", err)
		}
	}()
	res, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := res.ToString(ctx)
	if err != nil || out != "stillalive" {
		t.Fatalf("js = %q, %v", out, err)
	}
}

// --- misuse guards -------------------------------------------------------------------

func TestSBForeignIsolateAndClosedResourceErrors(t *testing.T) {
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
	ctxA, err := isoA.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scopeB, err := isoB.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	scopeBClosed := false
	defer func() {
		if err := scopeA.Close(); err != nil {
			t.Errorf("scopeA.Close: %v", err)
		}
		if err := ctxA.Close(); err != nil {
			t.Errorf("ctxA.Close: %v", err)
		}
		if !scopeBClosed {
			if err := scopeB.Close(); err != nil {
				t.Errorf("scopeB.Close: %v", err)
			}
		}
		if err := isoB.Close(); err != nil {
			t.Errorf("isoB.Close: %v", err)
		}
		if err := isoA.Close(); err != nil {
			t.Errorf("isoA.Close: %v", err)
		}
	}()

	a, err := scopeA.NewString("A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := scopeB.NewString("B")
	if err != nil {
		t.Fatal(err)
	}

	// Cross-isolate operands are refused before any engine call.
	if _, err := scopeA.ConcatString(a, b); err == nil ||
		!strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("cross-isolate concat error = %v", err)
	}

	// Wrong-isolate BigInt construction.
	if _, err := scopeA.BigIntFromWords(ctxA, false, []uint64{1}, nil); err == nil {
		_ = err // (valid call; below is the wrong-scope variant)
	}
	if _, err := scopeB.BigIntFromWords(ctxA, false, []uint64{1}, nil); err == nil {
		t.Fatal("cross-isolate BigIntFromWords accepted")
	}

	// A closed scope refuses further string creation.
	if err := scopeB.Close(); err != nil {
		t.Fatal(err)
	}
	scopeBClosed = true
	if _, err := scopeB.NewString("after"); err == nil {
		t.Fatal("creation on a closed scope accepted")
	}

	// A nil deleter is rejected without creating the string.
	if _, _, err := scopeA.NewExternalOneByteStringRaw([]byte{1}, nil); err == nil {
		t.Fatal("nil deleter accepted")
	}

	// A nil const resource is rejected.
	if _, err := scopeA.NewStringFromOneByteConst(nil); err == nil {
		t.Fatal("nil const resource accepted")
	}
	_ = a
	_ = b
}

// The probe helper set is inherited from core_advanced_negative_test.go;
// this slice adds no fatal probes (see the file comment) but verifies the
// conformance binary behavior distinction: GOV8_PROBE is absent, so no
// probe body ever runs in a normal pass.
func TestSBNoProbeEnvironmentLeak(t *testing.T) {
	if v := os.Getenv("GOV8_PROBE"); v != "" {
		t.Fatalf("GOV8_PROBE unexpectedly set to %q in a normal test pass", v)
	}
}
