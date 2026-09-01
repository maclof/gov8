//! Negative and boundary characterization for the advanced String/BigInt
//! slice. Complements `src/bin/conformance-strings-bigint.rs` with the
//! cases that must NOT run inside the fixture binary.
//!
//! # Contract characterized here
//!
//! - **Over-`MAX_LENGTH` string creation is recoverable `None`.** All three
//!   creation entry points (`new_from_utf8`, `new_from_one_byte`,
//!   `new_from_two_byte`) return `None` for inputs longer than
//!   `String::MAX_LENGTH` (= `(1 << 29) - 24` = 536870888 on 64-bit,
//!   `v8-primitive.h`), with no exception and no isolate damage. This needs
//!   ~0.5-1 GiB transient buffers, so it lives here instead of the fixture.
//! - **`BigInt::new_from_words` over-limit is a JS `RangeError`, not a
//!   crash.** More than `i::BigInt::kMaxLength` (= 16777215 on 64-bit)
//!   words returns `None` with a pending `RangeError:
//!   Maximum BigInt size exceeded` (`v8/src/objects/bigint.cc`,
//!   `BigInt::FromWords64` -> `ThrowBigIntTooBig`). The negative proof here
//!   goes beyond the fixture check: `has_terminated` stays false, a
//!   `TryCatch::reset()` fully restores the isolate (scripts, BigInt
//!   construction, and a forced GC all work afterwards), and a second
//!   over-limit call throws again — the failure is repeatable, not a
//!   one-shot state machine transition.
//! - **Raw external string destructors fire during isolate disposal when
//!   the string is still alive.** The fixture pins the GC path (destructor
//!   runs on the first forced major GC after the last reference drops).
//!   This file pins the complementary lifetime: a Global held across
//!   `Isolate` drop finalizes the external string in the external-string
//!   table teardown (`Heap::ExternalStringTable::TearDown`), again exactly
//!   once.
//! - **`ValueView` dropped before a GC keeps the isolate healthy.** The
//!   view's documented hazard is GC invalidation while alive; dropping the
//!   view first and then forcing a major GC is the safe pattern (string
//!   content stays readable, JS still runs).
//!
//! # Why there are no V8-fatal child-process probes (unlike
//! `tests/buffers_negative.rs` / `tests/template_advanced_negative.rs`)
//!
//! Every failure reachable from safe Rust through the pinned String/BigInt
//! APIs is recoverable (`None` + an optional pending JS exception). The
//! genuinely unsafe boundaries of these APIs are undefined behavior in the
//! release build, not deterministic fatals, so they are documented and
//! deliberately never executed anywhere in this crate:
//! - `write_v2` / `write_one_byte_v2` with `offset + buffer.len() >
//!   string.length()`: V8's `String::WriteHelperV2` DCHECKs the range but
//!   release builds call `String::WriteToFlat` unguarded — an out-of-bounds
//!   read whose buffer contents are nondeterministic heap garbage (verified
//!   against `v8/src/objects/string.cc` during characterization).
//! - `write_utf8_v2` with `kNullTerminate` and an empty buffer: the NUL
//!   write needs capacity >= 1 (`v8-primitive.h`); capacity 0 is an
//!   out-of-bounds write.
//! - Keeping a `ValueView` alive across an allocation/GC: the viewed data
//!   may move (view-internal warning in the crate).
//! - Touching an external string buffer from Rust after handing it off
//!   (`new_external_onebyte[_raw]` & co.): ownership transferred to V8;
//!   early access is use-after-free, late access without the destructor
//!   protocol is a leak, and freeing the same buffer twice is a double
//!   free (exercised once during characterization, then fixed in the
//!   harness by relinquishing ownership with `Box::into_raw`).

use std::sync::atomic::{AtomicUsize, Ordering};

// ---------------------------------------------------------------------------
// Recoverable creation bounds (needs ~1 GiB transient, hence not a fixture)
// ---------------------------------------------------------------------------

#[test]
fn string_creation_over_max_length_is_none() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // One byte past the limit. utf8 and one-byte share the same buffer.
    let bytes: Vec<u8> = vec![b'a'; v8::String::MAX_LENGTH + 1];
    assert!(
        v8::String::new_from_utf8(scope, &bytes, v8::NewStringType::Normal).is_none(),
        "new_from_utf8 must reject input longer than String::MAX_LENGTH"
    );
    assert!(
        v8::String::new_from_one_byte(scope, &bytes, v8::NewStringType::Normal).is_none(),
        "new_from_one_byte must reject input longer than String::MAX_LENGTH"
    );
    drop(bytes);

    // The two-byte entry point counts UTF-16 code units, so the boundary
    // buffer is twice as wide (~1 GiB transient).
    let units: Vec<u16> = vec![0x41; v8::String::MAX_LENGTH + 1];
    assert!(
        v8::String::new_from_two_byte(scope, &units, v8::NewStringType::Normal).is_none(),
        "new_from_two_byte must reject input longer than String::MAX_LENGTH units"
    );
    drop(units);

    // The isolate is unharmed and still at the exact boundary works.
    let at_limit = vec![b'b'; v8::String::MAX_LENGTH];
    let at_limit_string =
        v8::String::new_from_one_byte(scope, &at_limit, v8::NewStringType::Normal)
            .expect("MAX_LENGTH-sized input must be accepted");
    assert_eq!(at_limit_string.length(), v8::String::MAX_LENGTH);
}

// ---------------------------------------------------------------------------
// BigInt over-limit: repeatable RangeError with full isolate recovery
// ---------------------------------------------------------------------------

#[test]
fn bigint_over_limit_words_is_repeatable_range_error_with_recovery() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // The TryCatch phase is block-scoped so `scope` is free afterwards.
    {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();

        // Two independent over-limit attempts throw identically: the failure
        // is a validation path, not a poisoned state.
        for attempt in 0..2 {
            let words = vec![1u64; 16_777_216]; // kMaxLength + 1
            let result = v8::BigInt::new_from_words(tc, false, &words);
            drop(words);
            assert!(result.is_none(), "attempt {attempt}: expected None");
            assert!(
                tc.has_caught(),
                "attempt {attempt}: exception must be pending"
            );
            assert!(
                !tc.has_terminated(),
                "attempt {attempt}: execution must not be terminated"
            );
            let message = tc
                .message()
                .map(|m| m.get(tc).to_rust_string_lossy(tc))
                .unwrap_or_default();
            assert_eq!(message, "Uncaught RangeError: Maximum BigInt size exceeded");
            tc.reset();
            assert!(
                !tc.has_caught(),
                "attempt {attempt}: reset must clear the catch"
            );
        }

        // Full recovery inside the same TryCatch: scripts and BigInt
        // construction work right after the reset.
        let src = v8::String::new(tc, "40 + 2").unwrap();
        let value = v8::Script::compile(tc, src, None)
            .expect("compile after reset")
            .run(tc)
            .expect("run after reset");
        assert_eq!(value.int32_value(tc), Some(42));
        let big = v8::BigInt::new_from_i64(tc, i64::MIN);
        assert_eq!(big.i64_value(), (i64::MIN, true));
    }

    // A forced major GC after the throw proves the heap is consistent.
    scope.low_memory_notification();
}

// ---------------------------------------------------------------------------
// Raw external string destructor at isolate disposal (string still alive)
// ---------------------------------------------------------------------------

static NEG_DEL_CALLS: AtomicUsize = AtomicUsize::new(0);
static NEG_DEL_LEN: AtomicUsize = AtomicUsize::new(0);
static NEG_DEL_PTR: AtomicUsize = AtomicUsize::new(0);

/// Counting-only destructor over a leaked (never-freed) buffer: the buffer
/// lives for the whole process, so the destructor safely records facts
/// without any ownership transfer.
unsafe extern "C" fn neg_counting_deleter(data: *mut i8, len: usize) {
    NEG_DEL_CALLS.fetch_add(1, Ordering::SeqCst);
    NEG_DEL_LEN.store(len, Ordering::SeqCst);
    NEG_DEL_PTR.store(data as usize, Ordering::SeqCst);
}

#[test]
fn external_string_alive_at_isolate_drop_fires_deleter_exactly_once() {
    oracle::ensure_v8();
    let (raw_ptr, held_global) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        let (g, raw) = {
            v8::scope!(let scope, isolate);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            // Leak the buffer for the whole process: this probe transfers no
            // ownership, it only observes the destructor's firing.
            let leaked: &'static mut [u8] = Vec::leak(vec![7u8; 9]);
            let raw = leaked.as_mut_ptr() as *mut i8;
            let s = unsafe {
                v8::String::new_external_onebyte_raw(scope, raw, 9, neg_counting_deleter)
            }
            .expect("raw external string creation");
            assert_eq!(s.to_rust_string_lossy(scope), "\u{7}".repeat(9));
            (v8::Global::new(scope, s), raw as usize)
        };
        assert_eq!(NEG_DEL_CALLS.load(Ordering::SeqCst), 0);
        // The Global escapes the inner scope; the isolate is dropped when the
        // outer block ends, with the Global still holding the string alive.
        (raw, g)
    };
    // Isolate disposal (and with it the external-string-table teardown that
    // finalizes the still-alive string) has completed by this point.
    assert_eq!(
        NEG_DEL_CALLS.load(Ordering::SeqCst),
        1,
        "destructor must fire exactly once at isolate drop"
    );
    assert_eq!(NEG_DEL_LEN.load(Ordering::SeqCst), 9);
    assert_eq!(NEG_DEL_PTR.load(Ordering::SeqCst), raw_ptr);
    // Safe to drop after disposal: Global::drop no-ops on a disposed isolate.
    drop(held_global);
}

// ---------------------------------------------------------------------------
// ValueView lifecycle: drop before GC keeps everything usable
// ---------------------------------------------------------------------------

#[test]
fn value_view_dropped_before_gc_keeps_isolate_usable() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    let (held, kind_ok) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let s = v8::String::new(scope, "view-then-gc").unwrap();
        let view = v8::ValueView::new(scope, s);
        let kind_ok = matches!(view.data(), v8::ValueViewData::OneByte(_));
        // The documented safe pattern: the view is dropped before any GC.
        drop(view);
        (v8::Global::new(scope, s), kind_ok)
    };
    assert!(kind_ok, "ASCII string must present a one-byte view");
    // Forced major GC runs between scope blocks, after the view is gone.
    isolate.low_memory_notification();
    let text_ok = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let text = unsafe { held.open(scope) }.to_rust_string_lossy(scope);
        drop(held);
        text == "view-then-gc"
    };
    assert!(
        text_ok,
        "string content must survive a GC that ran after the view was dropped"
    );

    // The isolate still runs JavaScript afterwards.
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let src = v8::String::new(scope, "'still' + 'alive'").unwrap();
    let value = v8::Script::compile(scope, src, None)
        .unwrap()
        .run(scope)
        .unwrap();
    assert_eq!(
        value.to_string(scope).unwrap().to_rust_string_lossy(scope),
        "stillalive"
    );
}
