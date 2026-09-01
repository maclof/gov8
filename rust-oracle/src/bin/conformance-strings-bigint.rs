//! Advanced String and BigInt conformance slice for the pinned `v8` crate.
//!
//! Characterizes, in fixed order, the observable contract of:
//! - `String` creation flavors: `new` (UTF-8, lossy on invalid input),
//!   `new_from_utf8` with `NewStringType::Normal`/`Internalized`,
//!   `new_from_one_byte` (Latin-1), `new_from_two_byte` (UTF-16 incl.
//!   surrogate pairs), `empty`, plus the `String::MAX_LENGTH` constant.
//! - `String::concat`: content, associativity, empty identity, one-byte
//!   retention, interaction with external strings, JS visibility.
//! - Writes into host buffers: `write_v2` (UTF-16 units),
//!   `write_one_byte_v2`/`write_one_byte_uninit_v2` (Latin-1; low-byte
//!   truncation for two-byte content) and `write_utf8_v2` (never writes
//!   partial UTF-8 sequences, `kNullTerminate` counts the NUL in the return
//!   value, `kReplaceInvalidUtf8` swaps lone surrogates for U+FFFD). Only
//!   in-range `offset` values are exercised: any `offset` with
//!   `offset + buffer.len() > string.length()` makes the pinned build's
//!   release-mode `String::WriteToFlat` read past the string
//!   (`v8/src/objects/string.cc`; guarded by `DCHECK` only) -- undefined
//!   behavior documented in `tests/strings_bigint_negative.rs`, never run.
//! - `ValueView` encoding flavors (`OneByte`/`TwoByte` per representation),
//!   the ASCII-only `as_str` rule, and the borrowed/owned `to_cow_lossy`
//!   split. The view's "no GC while alive" rule is respected: every view is
//!   dropped before any allocation-heavy call (see the negative test for the
//!   drop-then-GC lifecycle case).
//! - External strings: static one-byte (`new_external_onebyte_static`),
//!   static two-byte (`new_external_twobyte_static`), build-time
//!   `OneByteConst` resources (`create_external_onebyte_const` +
//!   `new_from_onebyte_const`, shareable across isolates), owned variants
//!   (`new_external_onebyte`/`new_external_twobyte`; the crate frees the
//!   buffer via `free_rust_external_onebyte`/`free_rust_external_twobyte`
//!   when V8 finalizes the string), raw variants with custom destructors
//!   (`new_external_onebyte_raw`/`new_external_twobyte_raw`), the predicate
//!   matrix (`is_external`, `is_external_onebyte`, `is_external_twobyte`,
//!   `is_onebyte`, `contains_only_onebyte`), resource getters and identity
//!   (`get_external_onebyte_string_resource`,
//!   `get_external_string_resource`, `get_external_string_resource_base`),
//!   and the raw-deleter lifetime: invoked exactly once, on the first forced
//!   major GC after the last strong reference is dropped (or during isolate
//!   disposal if the string is still alive), receiving the original pointer
//!   and length.
//! - `BigInt`: `new_from_i64`/`new_from_u64` boundaries (`i64::MIN/MAX`,
//!   `u64::MAX`, negatives), `u64_value`/`i64_value` truncation semantics
//!   (lossless flags, two's-complement truncation), `new_from_words`
//!   construction (zero words, multi-word values, sign bit, `-0`
//!   normalization), `word_count`, `to_words_array` (exact / oversized /
//!   truncated buffers, zero BigInt, untouched buffer tails), roundtrip
//!   identity, JS-created BigInts observed through the native API, and the
//!   over-limit failure: more than `i::BigInt::kMaxLength` (= 16777215)
//!   64-bit words returns `None` with a pending JS `RangeError` that a
//!   `TryCatch` observes and resets cleanly
//!   (`v8/src/objects/bigint.cc`, `BigInt::FromWords64` ->
//!   `ThrowBigIntTooBig`).
//!
//! Everything is normalized per `src/json.rs` rules: no addresses (pointer
//! identity is recorded as equality booleans only), no timings, exact V8
//! error strings for the pinned build. The runner emits the same JSON-lines
//! protocol as the other slices
//! (`{"check":..,"ok":..,"value"|"expected"/"actual"}` + final summary).
//!
//! This slice performs no platform shutdown, so it can be verified
//! in-process; its fixture is pinned by
//! `tests/conformance_strings_bigint_fixture.rs` (binary output only: the
//! checks live in this binary because the existing `src/checks` registries
//! are shared files that this slice must not modify).
//!
//! # Benchmark mode
//!
//! `conformance-strings-bigint --bench` runs the string/BigInt reference
//! workloads instead of the conformance report. Methodology mirrors
//! `benches/common/mod.rs` (1 s warm-up, 50 samples, ~60 ms per sample, a
//! fresh nested `HandleScope` per iteration, isolate/context created once)
//! so results are comparable with the criterion benches; output is raw
//! measurements (never compared to fixtures) and is meant to be captured
//! under `bench-results/` next to an environment capture
//! (`scripts/capture-env.ps1`). Run in release mode:
//!
//! ```text
//! cargo run --release --bin conformance-strings-bigint -- --bench
//! ```

use std::io::Write as _;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::OnceLock;
use std::time::{Duration, Instant};

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

// ---------------------------------------------------------------------------
// Helpers (local to this binary; the crate's `checks::harness` is pub(crate)
// and existing files must not be modified to expose it).
// ---------------------------------------------------------------------------

/// Lowercase hex without separators: canonical encoding of host-buffer
/// contents in this slice.
fn hex(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        use std::fmt::Write as _;
        let _ = write!(out, "{byte:02x}");
    }
    out
}

/// Fills `buf` with a poison pattern so the write checks can prove V8 left
/// the tail untouched (any byte still `0xAB` was never written).
fn poison(buf: &mut [u8]) {
    buf.fill(0xAB);
}

fn poison_u16(buf: &mut [u16]) {
    buf.fill(0xABAB);
}

/// Compiles and runs `source`, returning the completion value (`None` on
/// failure; every eval in this slice is expected to succeed and unwraps at
/// the call site).
fn eval<'s>(scope: &mut v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let src = v8::String::new(scope, source)?;
    v8::Script::compile(scope, src, None)?.run(scope)
}

/// Compiles, runs and ToString's `source` ("" on failure).
fn eval_text(scope: &mut v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source)
        .and_then(|v| v.to_string(scope))
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

/// ToString of an arbitrary value ("" when conversion fails).
fn value_text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

// ---------------------------------------------------------------------------
// Raw external string destructor plumbing. V8 invokes the destructor as
// `fn(data, len)` with no user-data parameter, so each destructor targets a
// statically reachable state. The states live in `OnceLock`s (stable
// addresses, set once at check start) because V8 may invoke the destructor
// from the isolate-disposal path after the check's stack frames are gone.
// ---------------------------------------------------------------------------

struct DeleterState {
    invocations: AtomicUsize,
    observed_len: AtomicUsize,
    observed_ptr: AtomicUsize,
    handed_off_ptr: AtomicUsize,
}

impl DeleterState {
    const fn new() -> Self {
        Self {
            invocations: AtomicUsize::new(0),
            observed_len: AtomicUsize::new(0),
            observed_ptr: AtomicUsize::new(0),
            handed_off_ptr: AtomicUsize::new(0),
        }
    }
}

static ONEBYTE_DELETER: OnceLock<DeleterState> = OnceLock::new();
static TWOBYTE_DELETER: OnceLock<DeleterState> = OnceLock::new();

/// One-byte raw external string destructor: reclaims the handed-off
/// allocation (ownership was fully relinquished via `Box::into_raw`) and
/// echoes the callback arguments into the shared state.
unsafe extern "C" fn counting_deleter_onebyte(data: *mut i8, len: usize) {
    let Some(state) = ONEBYTE_DELETER.get() else {
        return;
    };
    state.invocations.fetch_add(1, Ordering::SeqCst);
    state.observed_len.store(len, Ordering::SeqCst);
    state.observed_ptr.store(data as usize, Ordering::SeqCst);
    let slice = std::slice::from_raw_parts_mut(data.cast::<u8>(), len);
    drop(Box::from_raw(slice));
}

/// Two-byte counterpart of [`counting_deleter_onebyte`].
unsafe extern "C" fn counting_deleter_twobyte(data: *mut u16, len: usize) {
    let Some(state) = TWOBYTE_DELETER.get() else {
        return;
    };
    state.invocations.fetch_add(1, Ordering::SeqCst);
    state.observed_len.store(len, Ordering::SeqCst);
    state.observed_ptr.store(data as usize, Ordering::SeqCst);
    let slice = std::slice::from_raw_parts_mut(data, len);
    drop(Box::from_raw(slice));
}

/// Snapshot of both destructor states, normalized for comparison and JSON.
fn deleter_snapshot() -> (usize, usize, bool, usize, usize, bool) {
    let s1 = ONEBYTE_DELETER.get().unwrap();
    let s2 = TWOBYTE_DELETER.get().unwrap();
    (
        s1.invocations.load(Ordering::SeqCst),
        s1.observed_len.load(Ordering::SeqCst),
        s1.observed_ptr.load(Ordering::SeqCst) == s1.handed_off_ptr.load(Ordering::SeqCst),
        s2.invocations.load(Ordering::SeqCst),
        s2.observed_len.load(Ordering::SeqCst),
        s2.observed_ptr.load(Ordering::SeqCst) == s2.handed_off_ptr.load(Ordering::SeqCst),
    )
}

// ---------------------------------------------------------------------------
// Checks. Order is part of the observable contract (the fixture is ordered).
// ---------------------------------------------------------------------------

/// `String::MAX_LENGTH` boundary constant and the empty-string surface
/// (`String::empty`, empty `new`, and their lengths/view flavors).
fn str_max_length_and_empty() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let empty = v8::String::empty(scope);
    let new_empty = v8::String::new(scope, "").unwrap();
    let view = v8::ValueView::new(scope, empty);
    let view_desc = match view.data() {
        v8::ValueViewData::OneByte(bytes) => Json::obj(vec![
            ("kind", Json::s("onebyte")),
            ("len", Json::i(bytes.len() as i64)),
        ]),
        v8::ValueViewData::TwoByte(units) => Json::obj(vec![
            ("kind", Json::s("twobyte")),
            ("len", Json::i(units.len() as i64)),
        ]),
    };
    drop(view);

    let actual = Json::obj(vec![
        // (1 << 29) - 24 on 64-bit targets; v8-primitive.h String::kMaxLength.
        ("max_length", Json::i(v8::String::MAX_LENGTH as i64)),
        (
            "empty",
            Json::obj(vec![
                ("length", Json::i(empty.length() as i64)),
                ("utf8_length", Json::i(empty.utf8_length(scope) as i64)),
                ("is_onebyte", Json::b(empty.is_onebyte())),
                ("view", view_desc),
            ]),
        ),
        (
            "new_empty",
            Json::obj(vec![("length", Json::i(new_empty.length() as i64))]),
        ),
    ]);
    let expected = Json::obj(vec![
        ("max_length", Json::i(536_870_888)),
        (
            "empty",
            Json::obj(vec![
                ("length", Json::i(0)),
                ("utf8_length", Json::i(0)),
                ("is_onebyte", Json::b(true)),
                (
                    "view",
                    Json::obj(vec![("kind", Json::s("onebyte")), ("len", Json::i(0))]),
                ),
            ]),
        ),
        ("new_empty", Json::obj(vec![("length", Json::i(0))])),
    ]);
    vec![expect_eq("strings/max_length_and_empty", expected, actual)]
}

/// Creation flavors: UTF-8 (lossy on invalid input), internalized, Latin-1
/// one-byte, UTF-16 two-byte (Latin-1-representable content collapses to a
/// one-byte string), and the representation predicates.
#[allow(clippy::too_many_lines)]
fn str_creation_types() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ascii = v8::String::new(scope, "hello oracle").unwrap();
    // Invalid UTF-8 byte 0xFF decodes lossily to U+FFFD.
    let invalid = v8::String::new_from_utf8(scope, b"ab\xFFcd", v8::NewStringType::Normal).unwrap();
    let internalized_a =
        v8::String::new_from_utf8(scope, b"intern-me", v8::NewStringType::Internalized).unwrap();
    let internalized_b =
        v8::String::new_from_utf8(scope, b"intern-me", v8::NewStringType::Internalized).unwrap();
    let latin1 =
        v8::String::new_from_one_byte(scope, &[0xE9, 0x41], v8::NewStringType::Normal).unwrap();
    // Latin-1-representable content through the two-byte entry point: V8
    // stores it one-byte (IsOneByte true).
    let twobyte_latin1 =
        v8::String::new_from_two_byte(scope, &[0xE9], v8::NewStringType::Normal).unwrap();
    // True two-byte content with a surrogate pair.
    let emoji =
        v8::String::new_from_two_byte(scope, &[0xD83E, 0xDD80, 0x0041], v8::NewStringType::Normal)
            .unwrap();

    let actual = Json::obj(vec![
        (
            "ascii",
            Json::obj(vec![
                ("length", Json::i(ascii.length() as i64)),
                ("utf8_length", Json::i(ascii.utf8_length(scope) as i64)),
                ("text", Json::s(&ascii.to_rust_string_lossy(scope))),
                ("is_onebyte", Json::b(ascii.is_onebyte())),
                (
                    "contains_only_onebyte",
                    Json::b(ascii.contains_only_onebyte()),
                ),
            ]),
        ),
        (
            "invalid_utf8",
            Json::obj(vec![
                ("length", Json::i(invalid.length() as i64)),
                ("utf8_length", Json::i(invalid.utf8_length(scope) as i64)),
                ("text", Json::s(&invalid.to_rust_string_lossy(scope))),
            ]),
        ),
        (
            "internalized",
            Json::obj(vec![
                ("length", Json::i(internalized_a.length() as i64)),
                ("text", Json::s(&internalized_a.to_rust_string_lossy(scope))),
                ("is_onebyte", Json::b(internalized_a.is_onebyte())),
                ("is_external", Json::b(internalized_a.is_external())),
                (
                    "same_content_as_b",
                    Json::b(
                        internalized_a.to_rust_string_lossy(scope)
                            == internalized_b.to_rust_string_lossy(scope),
                    ),
                ),
            ]),
        ),
        (
            "latin1",
            Json::obj(vec![
                ("length", Json::i(latin1.length() as i64)),
                ("utf8_length", Json::i(latin1.utf8_length(scope) as i64)),
                ("text", Json::s(&latin1.to_rust_string_lossy(scope))),
                ("is_onebyte", Json::b(latin1.is_onebyte())),
            ]),
        ),
        (
            "twobyte_entry_latin1_content",
            Json::obj(vec![
                ("length", Json::i(twobyte_latin1.length() as i64)),
                ("text", Json::s(&twobyte_latin1.to_rust_string_lossy(scope))),
                ("is_onebyte", Json::b(twobyte_latin1.is_onebyte())),
                (
                    "contains_only_onebyte",
                    Json::b(twobyte_latin1.contains_only_onebyte()),
                ),
            ]),
        ),
        (
            "emoji_surrogate_pair",
            Json::obj(vec![
                ("length", Json::i(emoji.length() as i64)),
                ("utf8_length", Json::i(emoji.utf8_length(scope) as i64)),
                ("text", Json::s(&emoji.to_rust_string_lossy(scope))),
                ("is_onebyte", Json::b(emoji.is_onebyte())),
                (
                    "contains_only_onebyte",
                    Json::b(emoji.contains_only_onebyte()),
                ),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "ascii",
            Json::obj(vec![
                ("length", Json::i(12)),
                ("utf8_length", Json::i(12)),
                ("text", Json::s("hello oracle")),
                ("is_onebyte", Json::b(true)),
                ("contains_only_onebyte", Json::b(true)),
            ]),
        ),
        (
            "invalid_utf8",
            Json::obj(vec![
                ("length", Json::i(5)),
                ("utf8_length", Json::i(7)),
                ("text", Json::s("ab\u{FFFD}cd")),
            ]),
        ),
        (
            "internalized",
            Json::obj(vec![
                ("length", Json::i(9)),
                ("text", Json::s("intern-me")),
                ("is_onebyte", Json::b(true)),
                ("is_external", Json::b(false)),
                ("same_content_as_b", Json::b(true)),
            ]),
        ),
        (
            "latin1",
            Json::obj(vec![
                ("length", Json::i(2)),
                ("utf8_length", Json::i(3)),
                ("text", Json::s("\u{e9}A")),
                ("is_onebyte", Json::b(true)),
            ]),
        ),
        (
            "twobyte_entry_latin1_content",
            Json::obj(vec![
                ("length", Json::i(1)),
                ("text", Json::s("\u{e9}")),
                ("is_onebyte", Json::b(true)),
                ("contains_only_onebyte", Json::b(true)),
            ]),
        ),
        (
            "emoji_surrogate_pair",
            Json::obj(vec![
                ("length", Json::i(3)),
                ("utf8_length", Json::i(5)),
                ("text", Json::s("\u{1F980}A")),
                ("is_onebyte", Json::b(false)),
                ("contains_only_onebyte", Json::b(false)),
            ]),
        ),
    ]);
    vec![expect_eq("strings/creation_types", expected, actual)]
}

/// `String::concat`: content, associativity, empty identity, one-byte
/// retention, external interaction, and JS visibility of the result.
#[allow(clippy::too_many_lines)]
fn str_concat_semantics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let lhs = v8::String::new(scope, "foo").unwrap();
    let bar = v8::String::new(scope, "bar").unwrap();
    let tail = v8::String::new(scope, "baz").unwrap();
    let empty = v8::String::empty(scope);

    let foobar = v8::String::concat(scope, lhs, bar).unwrap();
    let left_assoc =
        v8::String::concat(scope, v8::String::concat(scope, lhs, bar).unwrap(), tail).unwrap();
    let right_assoc =
        v8::String::concat(scope, lhs, v8::String::concat(scope, bar, tail).unwrap()).unwrap();
    let empty_left = v8::String::concat(scope, empty, bar).unwrap();
    let empty_right = v8::String::concat(scope, bar, empty).unwrap();

    // Chained concat stays readable (flattening happens on demand).
    let mut chained = v8::String::new(scope, "x").unwrap();
    for i in 0..8 {
        chained = v8::String::concat(
            scope,
            chained,
            v8::String::new(scope, &format!("y{i}")).unwrap(),
        )
        .unwrap();
    }

    // concat over an external one-byte string produces a non-external,
    // one-byte result.
    static EXT_DATA: &[u8] = b"EXT";
    let ext = v8::String::new_external_onebyte_static(scope, EXT_DATA).unwrap();
    let bang = v8::String::new(scope, "!").unwrap();
    let ext_cat = v8::String::concat(scope, ext, bang).unwrap();

    // The JS side observes the concat result as an ordinary string.
    let js_cat = v8::String::concat(
        scope,
        v8::String::new(scope, "JS").unwrap(),
        v8::String::new(scope, "SEES").unwrap(),
    )
    .unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "cat").unwrap().into(),
            js_cat.into(),
        )
        .unwrap();

    let actual = Json::obj(vec![
        (
            "basic",
            Json::obj(vec![
                ("length", Json::i(foobar.length() as i64)),
                ("text", Json::s(&foobar.to_rust_string_lossy(scope))),
                ("is_onebyte", Json::b(foobar.is_onebyte())),
                (
                    "contains_only_onebyte",
                    Json::b(foobar.contains_only_onebyte()),
                ),
            ]),
        ),
        (
            "associative",
            Json::b(
                left_assoc.to_rust_string_lossy(scope) == right_assoc.to_rust_string_lossy(scope),
            ),
        ),
        (
            "assoc_text",
            Json::s(&left_assoc.to_rust_string_lossy(scope)),
        ),
        (
            "empty_left_text",
            Json::s(&empty_left.to_rust_string_lossy(scope)),
        ),
        (
            "empty_right_text",
            Json::s(&empty_right.to_rust_string_lossy(scope)),
        ),
        (
            "chained",
            Json::obj(vec![
                ("length", Json::i(chained.length() as i64)),
                ("text", Json::s(&chained.to_rust_string_lossy(scope))),
            ]),
        ),
        (
            "with_external",
            Json::obj(vec![
                ("text", Json::s(&ext_cat.to_rust_string_lossy(scope))),
                ("is_onebyte", Json::b(ext_cat.is_onebyte())),
                ("is_external", Json::b(ext_cat.is_external())),
            ]),
        ),
        ("js_sees", Json::s(&eval_text(scope, "cat"))),
        (
            "js_eq",
            Json::s(&eval_text(scope, "cat === 'JSSEES' ? 'EQ' : 'NEQ'")),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "basic",
            Json::obj(vec![
                ("length", Json::i(6)),
                ("text", Json::s("foobar")),
                ("is_onebyte", Json::b(true)),
                ("contains_only_onebyte", Json::b(true)),
            ]),
        ),
        ("associative", Json::b(true)),
        ("assoc_text", Json::s("foobarbaz")),
        ("empty_left_text", Json::s("bar")),
        ("empty_right_text", Json::s("bar")),
        (
            "chained",
            Json::obj(vec![
                ("length", Json::i(17)),
                ("text", Json::s("xy0y1y2y3y4y5y6y7")),
            ]),
        ),
        (
            "with_external",
            Json::obj(vec![
                ("text", Json::s("EXT!")),
                ("is_onebyte", Json::b(true)),
                ("is_external", Json::b(false)),
            ]),
        ),
        ("js_sees", Json::s("JSSEES")),
        ("js_eq", Json::s("EQ")),
    ]);
    vec![expect_eq("strings/concat_semantics", expected, actual)]
}

/// `write_v2` (UTF-16 units): full/partial/remainder-offset writes,
/// `kNullTerminate`, and the untouched poison tail. Only in-range offsets
/// are exercised (see the module docs for the out-of-range UB boundary).
fn str_write_two_byte_views() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // "ab" + U+1F980 (surrogate pair): 4 UTF-16 code units.
    let s = v8::String::new(scope, "ab\u{1F980}").unwrap();

    let mut full = [0u16; 8];
    poison_u16(&mut full);
    s.write_v2(scope, 0, &mut full, v8::WriteFlags::default());

    let mut partial = [0u16; 2];
    poison_u16(&mut partial);
    s.write_v2(scope, 0, &mut partial, v8::WriteFlags::default());

    // Offset covering exactly the remainder of the string.
    let mut remainder = [0u16; 3];
    poison_u16(&mut remainder);
    s.write_v2(scope, 1, &mut remainder, v8::WriteFlags::default());

    let mut nullterm = [0u16; 8];
    poison_u16(&mut nullterm);
    s.write_v2(scope, 0, &mut nullterm, v8::WriteFlags::kNullTerminate);

    let units = |buf: &[u16], n: usize| -> Vec<Json> {
        buf[..n].iter().map(|u| Json::i(i64::from(*u))).collect()
    };

    let actual = Json::obj(vec![
        ("length", Json::i(s.length() as i64)),
        ("full", Json::arr(units(&full, 4))),
        (
            "full_tail_untouched",
            Json::b(full[4..].iter().all(|u| *u == 0xABAB)),
        ),
        ("partial_2", Json::arr(units(&partial, 2))),
        ("remainder_from_offset_1", Json::arr(units(&remainder, 3))),
        ("nullterm", Json::arr(units(&nullterm, 5))),
        (
            "nullterm_tail_untouched",
            Json::b(nullterm[5..].iter().all(|u| *u == 0xABAB)),
        ),
    ]);
    let expected = Json::obj(vec![
        ("length", Json::i(4)),
        (
            "full",
            Json::arr(vec![
                Json::i(0x61),
                Json::i(0x62),
                Json::i(0xD83E),
                Json::i(0xDD80),
            ]),
        ),
        ("full_tail_untouched", Json::b(true)),
        ("partial_2", Json::arr(vec![Json::i(0x61), Json::i(0x62)])),
        (
            "remainder_from_offset_1",
            Json::arr(vec![Json::i(0x62), Json::i(0xD83E), Json::i(0xDD80)]),
        ),
        (
            "nullterm",
            Json::arr(vec![
                Json::i(0x61),
                Json::i(0x62),
                Json::i(0xD83E),
                Json::i(0xDD80),
                Json::i(0),
            ]),
        ),
        ("nullterm_tail_untouched", Json::b(true)),
    ]);
    vec![expect_eq("strings/write_two_byte_views", expected, actual)]
}

/// `write_one_byte_v2`/`write_one_byte_uninit_v2`: Latin-1 verbatim,
/// low-byte truncation of two-byte content, offset/remainder, and
/// `kNullTerminate`.
fn str_write_one_byte_views() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ab = v8::String::new(scope, "ab").unwrap();
    let latin1 =
        v8::String::new_from_one_byte(scope, &[0xE9, 0x41], v8::NewStringType::Normal).unwrap();
    // True two-byte content: units are truncated to their low byte.
    let euro = v8::String::new(scope, "\u{20AC}A").unwrap();
    let emoji = v8::String::new(scope, "\u{1F980}").unwrap();

    let mut ascii = [0u8; 8];
    poison(&mut ascii);
    ab.write_one_byte_v2(scope, 0, &mut ascii, v8::WriteFlags::default());

    let mut latin1_buf = [0u8; 8];
    poison(&mut latin1_buf);
    latin1.write_one_byte_v2(scope, 0, &mut latin1_buf, v8::WriteFlags::default());

    let mut euro_buf = [0u8; 8];
    poison(&mut euro_buf);
    euro.write_one_byte_v2(scope, 0, &mut euro_buf, v8::WriteFlags::default());

    let mut emoji_buf = [0u8; 8];
    poison(&mut emoji_buf);
    emoji.write_one_byte_v2(scope, 0, &mut emoji_buf, v8::WriteFlags::default());

    // Offset 1 with a buffer sized exactly to the remainder.
    let mut remainder = [0u8; 1];
    poison(&mut remainder);
    ab.write_one_byte_v2(scope, 1, &mut remainder, v8::WriteFlags::default());

    let mut nullterm = [0u8; 8];
    poison(&mut nullterm);
    ab.write_one_byte_v2(scope, 0, &mut nullterm, v8::WriteFlags::kNullTerminate);

    let mut uninit_buf = [std::mem::MaybeUninit::new(0xABu8); 8];
    ab.write_one_byte_uninit_v2(scope, 0, &mut uninit_buf, v8::WriteFlags::default());
    // SAFETY: write_one_byte_uninit_v2 initialized the first two bytes.
    let uninit_written: Vec<u8> = uninit_buf
        .get(0..2)
        .unwrap()
        .iter()
        .map(|b| unsafe { b.assume_init() })
        .collect();

    let actual = Json::obj(vec![
        ("ascii", Json::s(&hex(&ascii[..2]))),
        (
            "ascii_tail_untouched",
            Json::b(ascii[2..].iter().all(|b| *b == 0xAB)),
        ),
        ("latin1", Json::s(&hex(&latin1_buf[..2]))),
        ("euro_low_byte_truncation", Json::s(&hex(&euro_buf[..2]))),
        ("emoji_low_byte_truncation", Json::s(&hex(&emoji_buf[..2]))),
        ("remainder_from_offset_1", Json::s(&hex(&remainder))),
        ("nullterm", Json::s(&hex(&nullterm[..3]))),
        ("uninit", Json::s(&hex(&uninit_written))),
    ]);
    let expected = Json::obj(vec![
        ("ascii", Json::s("6162")),
        ("ascii_tail_untouched", Json::b(true)),
        ("latin1", Json::s("e941")),
        ("euro_low_byte_truncation", Json::s("ac41")),
        ("emoji_low_byte_truncation", Json::s("3e80")),
        ("remainder_from_offset_1", Json::s("62")),
        ("nullterm", Json::s("616200")),
        ("uninit", Json::s("6162")),
    ]);
    vec![expect_eq("strings/write_one_byte_views", expected, actual)]
}

/// `write_utf8_v2`: exact counts, `processed_characters_return`, capacity
/// truncation that never splits a sequence, `kNullTerminate` (the NUL counts
/// toward the returned byte count), and lone-surrogate handling with and
/// without `kReplaceInvalidUtf8`.
#[allow(clippy::too_many_lines)]
fn str_write_utf8_views() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let abc = v8::String::new(scope, "abc").unwrap();
    let emoji = v8::String::new(scope, "a\u{1F980}b").unwrap();
    let he = v8::String::new(scope, "h\u{e9}").unwrap();
    // Lone high surrogate followed by 'a', created through the two-byte
    // entry point (2 UTF-16 units).
    let lone =
        v8::String::new_from_two_byte(scope, &[0xD83E, 0x0061], v8::NewStringType::Normal).unwrap();

    let mut ascii = [0u8; 32];
    poison(&mut ascii);
    let mut processed = 0usize;
    let ascii_n = abc.write_utf8_v2(
        scope,
        &mut ascii,
        v8::WriteFlags::default(),
        Some(&mut processed),
    );

    // Capacity truncation: 'a' fits, the 4-byte emoji never partially fits.
    let mut cap3 = [0u8; 32];
    poison(&mut cap3);
    let cap3_n = emoji.write_utf8_v2(scope, &mut cap3[..3], v8::WriteFlags::default(), None);
    let mut cap4 = [0u8; 32];
    poison(&mut cap4);
    let cap4_n = emoji.write_utf8_v2(scope, &mut cap4[..4], v8::WriteFlags::default(), None);
    let mut cap5 = [0u8; 32];
    poison(&mut cap5);
    let cap5_n = emoji.write_utf8_v2(scope, &mut cap5[..5], v8::WriteFlags::default(), None);

    let mut full = [0u8; 32];
    poison(&mut full);
    let full_n = emoji.write_utf8_v2(scope, &mut full, v8::WriteFlags::kNullTerminate, None);

    // "h\u{00e9}" is 3 UTF-8 bytes; null termination consumes one capacity byte.
    let mut he_cap4 = [0u8; 32];
    poison(&mut he_cap4);
    let he_cap4_n = he.write_utf8_v2(
        scope,
        &mut he_cap4[..4],
        v8::WriteFlags::kNullTerminate,
        None,
    );
    let mut he_cap3 = [0u8; 32];
    poison(&mut he_cap3);
    let he_cap3_n = he.write_utf8_v2(
        scope,
        &mut he_cap3[..3],
        v8::WriteFlags::kNullTerminate,
        None,
    );
    let mut he_cap2 = [0u8; 32];
    poison(&mut he_cap2);
    let he_cap2_n = he.write_utf8_v2(
        scope,
        &mut he_cap2[..2],
        v8::WriteFlags::kNullTerminate,
        None,
    );

    let mut lone_raw = [0u8; 32];
    poison(&mut lone_raw);
    let mut lone_processed = 0usize;
    let lone_raw_n = lone.write_utf8_v2(
        scope,
        &mut lone_raw,
        v8::WriteFlags::default(),
        Some(&mut lone_processed),
    );
    let mut lone_fixed = [0u8; 32];
    poison(&mut lone_fixed);
    let lone_fixed_n = lone.write_utf8_v2(
        scope,
        &mut lone_fixed,
        v8::WriteFlags::kReplaceInvalidUtf8,
        None,
    );

    let mut empty_buf: [u8; 0] = [];
    let empty_n = abc.write_utf8_v2(scope, &mut empty_buf, v8::WriteFlags::default(), None);

    let actual = Json::obj(vec![
        ("ascii_bytes", Json::i(ascii_n as i64)),
        ("ascii_processed", Json::i(processed as i64)),
        ("ascii_hex", Json::s(&hex(&ascii[..3]))),
        ("cap3_bytes", Json::i(cap3_n as i64)),
        ("cap3_hex", Json::s(&hex(&cap3[..1]))),
        ("cap4_bytes", Json::i(cap4_n as i64)),
        ("cap4_hex", Json::s(&hex(&cap4[..1]))),
        ("cap5_bytes", Json::i(cap5_n as i64)),
        ("cap5_hex", Json::s(&hex(&cap5[..5]))),
        ("full_nullterm_bytes", Json::i(full_n as i64)),
        ("full_nullterm_hex", Json::s(&hex(&full[..7]))),
        ("he_utf8_length", Json::i(he.utf8_length(scope) as i64)),
        ("he_cap4_bytes", Json::i(he_cap4_n as i64)),
        ("he_cap4_hex", Json::s(&hex(&he_cap4[..4]))),
        ("he_cap3_bytes", Json::i(he_cap3_n as i64)),
        ("he_cap3_hex", Json::s(&hex(&he_cap3[..3]))),
        ("he_cap2_bytes", Json::i(he_cap2_n as i64)),
        ("he_cap2_hex", Json::s(&hex(&he_cap2[..2]))),
        ("lone_surrogate_raw_bytes", Json::i(lone_raw_n as i64)),
        (
            "lone_surrogate_raw_processed",
            Json::i(lone_processed as i64),
        ),
        ("lone_surrogate_raw_hex", Json::s(&hex(&lone_raw[..4]))),
        (
            "lone_surrogate_replaced_bytes",
            Json::i(lone_fixed_n as i64),
        ),
        (
            "lone_surrogate_replaced_hex",
            Json::s(&hex(&lone_fixed[..4])),
        ),
        ("empty_buffer_bytes", Json::i(empty_n as i64)),
    ]);
    let expected = Json::obj(vec![
        ("ascii_bytes", Json::i(3)),
        ("ascii_processed", Json::i(3)),
        ("ascii_hex", Json::s("616263")),
        ("cap3_bytes", Json::i(1)),
        ("cap3_hex", Json::s("61")),
        ("cap4_bytes", Json::i(1)),
        ("cap4_hex", Json::s("61")),
        ("cap5_bytes", Json::i(5)),
        ("cap5_hex", Json::s("61f09fa680")),
        ("full_nullterm_bytes", Json::i(7)),
        ("full_nullterm_hex", Json::s("61f09fa6806200")),
        ("he_utf8_length", Json::i(3)),
        ("he_cap4_bytes", Json::i(4)),
        ("he_cap4_hex", Json::s("68c3a900")),
        ("he_cap3_bytes", Json::i(2)),
        ("he_cap3_hex", Json::s("6800ab")),
        ("he_cap2_bytes", Json::i(2)),
        ("he_cap2_hex", Json::s("6800")),
        // Without the flag V8 encodes the lone surrogate as its 3-byte
        // CESU-8-style sequence; with kReplaceInvalidUtf8 it becomes U+FFFD.
        ("lone_surrogate_raw_bytes", Json::i(4)),
        ("lone_surrogate_raw_processed", Json::i(2)),
        ("lone_surrogate_raw_hex", Json::s("eda0be61")),
        ("lone_surrogate_replaced_bytes", Json::i(4)),
        ("lone_surrogate_replaced_hex", Json::s("efbfbd61")),
        ("empty_buffer_bytes", Json::i(0)),
    ]);
    vec![expect_eq("strings/write_utf8_views", expected, actual)]
}

/// `ValueView`: encoding flavor follows the string representation, `as_str`
/// is ASCII-only, `to_cow_lossy` borrows ASCII and transcodes otherwise.
fn str_value_view_flavors() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let describe = |view: &v8::ValueView<'_>| -> Json {
        match view.data() {
            v8::ValueViewData::OneByte(bytes) => Json::obj(vec![
                ("kind", Json::s("onebyte")),
                ("len", Json::i(bytes.len() as i64)),
                ("hex", Json::s(&hex(bytes))),
                ("as_str_is_some", Json::b(view.as_str().is_some())),
            ]),
            v8::ValueViewData::TwoByte(units) => {
                let first = units
                    .first()
                    .map_or(Vec::new(), |u| u.to_be_bytes().to_vec());
                Json::obj(vec![
                    ("kind", Json::s("twobyte")),
                    ("len", Json::i(units.len() as i64)),
                    ("first_unit_hex", Json::s(&hex(&first))),
                    ("as_str_is_some", Json::b(view.as_str().is_some())),
                ])
            }
        }
    };

    let ascii = v8::String::new(scope, "hello").unwrap();
    let ascii_view = v8::ValueView::new(scope, ascii);
    let ascii_cow_borrowed = matches!(ascii_view.to_cow_lossy(), std::borrow::Cow::Borrowed(_));
    let ascii_described = describe(&ascii_view);
    drop(ascii_view);

    let euro = v8::String::new(scope, "\u{20AC}").unwrap();
    let euro_view = v8::ValueView::new(scope, euro);
    let euro_described = describe(&euro_view);
    let euro_cow = euro_view.to_cow_lossy().to_string();
    drop(euro_view);

    let latin1 =
        v8::String::new_from_one_byte(scope, &[0xE9, 0x41], v8::NewStringType::Normal).unwrap();
    let latin1_view = v8::ValueView::new(scope, latin1);
    let latin1_described = describe(&latin1_view);
    drop(latin1_view);

    let empty = v8::String::empty(scope);
    let empty_view = v8::ValueView::new(scope, empty);
    let empty_described = describe(&empty_view);
    drop(empty_view);

    let actual = Json::obj(vec![
        ("ascii", ascii_described),
        ("ascii_cow_borrowed", Json::b(ascii_cow_borrowed)),
        ("euro", euro_described),
        ("euro_cow", Json::s(&euro_cow)),
        ("latin1", latin1_described),
        ("empty", empty_described),
    ]);
    let expected = Json::obj(vec![
        (
            "ascii",
            Json::obj(vec![
                ("kind", Json::s("onebyte")),
                ("len", Json::i(5)),
                ("hex", Json::s("68656c6c6f")),
                ("as_str_is_some", Json::b(true)),
            ]),
        ),
        ("ascii_cow_borrowed", Json::b(true)),
        (
            "euro",
            Json::obj(vec![
                ("kind", Json::s("twobyte")),
                ("len", Json::i(1)),
                ("first_unit_hex", Json::s("20ac")),
                ("as_str_is_some", Json::b(false)),
            ]),
        ),
        ("euro_cow", Json::s("\u{20ac}")),
        (
            "latin1",
            Json::obj(vec![
                ("kind", Json::s("onebyte")),
                ("len", Json::i(2)),
                ("hex", Json::s("e941")),
                ("as_str_is_some", Json::b(false)),
            ]),
        ),
        (
            "empty",
            Json::obj(vec![
                ("kind", Json::s("onebyte")),
                ("len", Json::i(0)),
                ("hex", Json::s("")),
                ("as_str_is_some", Json::b(true)),
            ]),
        ),
    ]);
    vec![expect_eq("strings/value_view_flavors", expected, actual)]
}

/// External strings, static and const flavors: predicates, resource data
/// echo, JS visibility, GC survival, and cross-isolate `OneByteConst`
/// sharing. Pointer identity lives in the dedicated identity check.
#[allow(clippy::too_many_lines)]
fn str_external_static_and_const() -> Vec<CheckOutcome> {
    static STATIC_DATA: &[u8] = b"static_ext";
    static CONST_DATA: &[u8] = b"konst";
    static KONST: v8::OneByteConst = v8::String::create_external_onebyte_const(CONST_DATA);
    static TWOBYTE_DATA: &[u16] = &[0xD83E, 0xDD80, 0x0041];

    let isolate = &mut v8::Isolate::new(Default::default());
    let (predicates, res_static, res_const, base_enc, js_eq, held) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);

        let s = v8::String::new_external_onebyte_static(scope, STATIC_DATA).unwrap();
        let k = v8::String::new_from_onebyte_const(scope, &KONST).unwrap();
        let t = v8::String::new_external_twobyte_static(scope, TWOBYTE_DATA).unwrap();

        let predicates = Json::obj(vec![
            (
                "static_onebyte",
                Json::obj(vec![
                    ("is_external", Json::b(s.is_external())),
                    ("is_external_onebyte", Json::b(s.is_external_onebyte())),
                    ("is_external_twobyte", Json::b(s.is_external_twobyte())),
                    ("is_onebyte", Json::b(s.is_onebyte())),
                    ("text", Json::s(&s.to_rust_string_lossy(scope))),
                ]),
            ),
            (
                "const_onebyte",
                Json::obj(vec![
                    ("is_external_onebyte", Json::b(k.is_external_onebyte())),
                    ("text", Json::s(&k.to_rust_string_lossy(scope))),
                    ("const_as_str", Json::s(KONST.as_str())),
                ]),
            ),
            (
                "twobyte_static",
                Json::obj(vec![
                    ("is_external_twobyte", Json::b(t.is_external_twobyte())),
                    ("is_onebyte", Json::b(t.is_onebyte())),
                    ("contains_only_onebyte", Json::b(t.contains_only_onebyte())),
                    ("len", Json::i(t.length() as i64)),
                    ("text", Json::s(&t.to_rust_string_lossy(scope))),
                    // Raw units echo verbatim through write_v2 (surrogate
                    // pair included) and the poison tail stays untouched.
                    ("units_echo", {
                        let mut buf = [0xABABu16; 8];
                        t.write_v2(scope, 0, &mut buf, v8::WriteFlags::default());
                        let tail_untouched = buf[3..].iter().all(|u| *u == 0xABAB);
                        Json::obj(vec![
                            (
                                "units",
                                Json::arr(
                                    buf[..3]
                                        .iter()
                                        .map(|u| Json::i(i64::from(*u)))
                                        .collect::<Vec<_>>(),
                                ),
                            ),
                            ("tail_untouched", Json::b(tail_untouched)),
                        ])
                    }),
                ]),
            ),
        ]);

        // Resource data echo for the static and const resources.
        let r1 = s.get_external_onebyte_string_resource();
        let rk = k.get_external_onebyte_string_resource();
        let res_static = Json::obj(vec![
            ("resource_is_some", Json::b(r1.is_some())),
            (
                "data",
                Json::s(&String::from_utf8_lossy(
                    unsafe { r1.unwrap().as_ref() }.as_bytes(),
                )),
            ),
            (
                "len",
                Json::i(unsafe { r1.unwrap().as_ref() }.length() as i64),
            ),
        ]);
        let res_const = Json::obj(vec![
            ("resource_is_some", Json::b(rk.is_some())),
            (
                "resource_data",
                Json::s(&String::from_utf8_lossy(
                    unsafe { rk.unwrap().as_ref() }.as_bytes(),
                )),
            ),
        ]);

        // Base-class getter: resolves for one-byte externals and matches the
        // typed getter; reports the OneByte encoding (valid variant).
        let (base, enc) = s.get_external_string_resource_base();
        let base_enc = Json::obj(vec![
            ("base_is_some", Json::b(base.is_some())),
            (
                "base_matches_onebyte_getter",
                Json::b(
                    base.map(|b| b.as_ptr() == r1.unwrap().as_ptr().cast())
                        .unwrap_or(false),
                ),
            ),
            (
                "enc",
                Json::s(match enc {
                    v8::Encoding::OneByte => "OneByte",
                    v8::Encoding::TwoByte => "TwoByte",
                    v8::Encoding::Unknown => "Unknown",
                }),
            ),
        ]);

        context
            .global(scope)
            .set(
                scope,
                v8::String::new(scope, "exts").unwrap().into(),
                s.into(),
            )
            .unwrap();
        let js_eq = eval_text(scope, "exts === 'static_ext' ? 'EQ' : 'NEQ'");

        let held = v8::Global::new(scope, s);
        (predicates, res_static, res_const, base_enc, js_eq, held)
    };

    // The external string survives a forced major GC while referenced.
    isolate.low_memory_notification();
    let survived = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let text = unsafe { held.open(scope) }.to_rust_string_lossy(scope);
        drop(held);
        text == "static_ext"
    };

    // OneByteConst resources are Sync: the same static creates external
    // strings in a second isolate.
    let const_shared_across_isolates = {
        let isolate2 = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate2);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let k2 = v8::String::new_from_onebyte_const(scope, &KONST).unwrap();
        k2.is_external_onebyte() && k2.to_rust_string_lossy(scope) == "konst"
    };

    let actual = Json::obj(vec![
        ("predicates", predicates),
        ("static_resource", res_static),
        ("const_resource", res_const),
        ("base", base_enc),
        ("js_eq", Json::s(&js_eq)),
        ("survives_forced_gc_while_held", Json::b(survived)),
        (
            "const_shared_across_isolates",
            Json::b(const_shared_across_isolates),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "predicates",
            Json::obj(vec![
                (
                    "static_onebyte",
                    Json::obj(vec![
                        ("is_external", Json::b(true)),
                        ("is_external_onebyte", Json::b(true)),
                        ("is_external_twobyte", Json::b(false)),
                        ("is_onebyte", Json::b(true)),
                        ("text", Json::s("static_ext")),
                    ]),
                ),
                (
                    "const_onebyte",
                    Json::obj(vec![
                        ("is_external_onebyte", Json::b(true)),
                        ("text", Json::s("konst")),
                        ("const_as_str", Json::s("konst")),
                    ]),
                ),
                (
                    "twobyte_static",
                    Json::obj(vec![
                        ("is_external_twobyte", Json::b(true)),
                        ("is_onebyte", Json::b(false)),
                        ("contains_only_onebyte", Json::b(false)),
                        ("len", Json::i(3)),
                        ("text", Json::s("\u{1F980}A")),
                        (
                            "units_echo",
                            Json::obj(vec![
                                (
                                    "units",
                                    Json::arr(vec![
                                        Json::i(0xD83E),
                                        Json::i(0xDD80),
                                        Json::i(0x0041),
                                    ]),
                                ),
                                ("tail_untouched", Json::b(true)),
                            ]),
                        ),
                    ]),
                ),
            ]),
        ),
        (
            "static_resource",
            Json::obj(vec![
                ("resource_is_some", Json::b(true)),
                ("data", Json::s("static_ext")),
                ("len", Json::i(10)),
            ]),
        ),
        (
            "const_resource",
            Json::obj(vec![
                ("resource_is_some", Json::b(true)),
                ("resource_data", Json::s("konst")),
            ]),
        ),
        (
            "base",
            Json::obj(vec![
                ("base_is_some", Json::b(true)),
                ("base_matches_onebyte_getter", Json::b(true)),
                ("enc", Json::s("OneByte")),
            ]),
        ),
        ("js_eq", Json::s("EQ")),
        ("survives_forced_gc_while_held", Json::b(true)),
        ("const_shared_across_isolates", Json::b(true)),
    ]);
    vec![expect_eq(
        "strings/external_static_and_const",
        expected,
        actual,
    )]
}

/// Resource getters across all external flavors and plain strings, plus
/// pointer identity/difference assertions (recorded as booleans only).
fn str_external_resource_identity() -> Vec<CheckOutcome> {
    static D1: &[u8] = b"AAA";
    static D2: &[u8] = b"BBBB";
    static TD: &[u16] = &[0x20AC];

    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let s1 = v8::String::new_external_onebyte_static(scope, D1).unwrap();
    let s2 = v8::String::new_external_onebyte_static(scope, D2).unwrap();
    let t = v8::String::new_external_twobyte_static(scope, TD).unwrap();
    let plain = v8::String::new(scope, "plain").unwrap();

    let r1 = s1.get_external_onebyte_string_resource();
    let r2 = s2.get_external_onebyte_string_resource();
    let rt = t.get_external_onebyte_string_resource();
    let rt_generic = t.get_external_string_resource();
    let (base_t, _enc_t) = t.get_external_string_resource_base();

    let actual = Json::obj(vec![
        // Same string: stable resource pointer. Different statics: distinct.
        ("s1_is_some", Json::b(r1.is_some())),
        (
            "s1_stable",
            Json::b(
                r1.map(|a| {
                    a.as_ptr() == s1.get_external_onebyte_string_resource().unwrap().as_ptr()
                })
                .unwrap_or(false),
            ),
        ),
        (
            "distinct_statics_distinct_resources",
            Json::b(r1.unwrap().as_ptr() != r2.unwrap().as_ptr()),
        ),
        // Two-byte external: the one-byte getter is None, the generic
        // (two-byte-typed) getter resolves and equals the base pointer.
        ("twobyte_onebyte_getter_none", Json::b(rt.is_none())),
        ("twobyte_generic_is_some", Json::b(rt_generic.is_some())),
        ("twobyte_base_is_some", Json::b(base_t.is_some())),
        (
            "twobyte_generic_matches_base",
            Json::b(
                rt_generic
                    .map(|g| g.as_ptr() == base_t.unwrap().as_ptr().cast())
                    .unwrap_or(false),
            ),
        ),
        // One-byte externals: the generic (two-byte-typed) getter is None in
        // this pinned build.
        (
            "onebyte_generic_is_none",
            Json::b(s1.get_external_string_resource().is_none()),
        ),
        // Plain strings have no resources at all.
        (
            "plain",
            Json::obj(vec![
                ("is_external", Json::b(plain.is_external())),
                (
                    "onebyte_getter_none",
                    Json::b(plain.get_external_onebyte_string_resource().is_none()),
                ),
                (
                    "generic_none",
                    Json::b(plain.get_external_string_resource().is_none()),
                ),
                (
                    "base_none",
                    Json::b(plain.get_external_string_resource_base().0.is_none()),
                ),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        ("s1_is_some", Json::b(true)),
        ("s1_stable", Json::b(true)),
        ("distinct_statics_distinct_resources", Json::b(true)),
        ("twobyte_onebyte_getter_none", Json::b(true)),
        ("twobyte_generic_is_some", Json::b(true)),
        ("twobyte_base_is_some", Json::b(true)),
        ("twobyte_generic_matches_base", Json::b(true)),
        ("onebyte_generic_is_none", Json::b(true)),
        (
            "plain",
            Json::obj(vec![
                ("is_external", Json::b(false)),
                ("onebyte_getter_none", Json::b(true)),
                ("generic_none", Json::b(true)),
                ("base_none", Json::b(true)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "strings/external_resource_identity",
        expected,
        actual,
    )]
}

/// Owned external strings (crate-managed buffers): content, predicates, and
/// survival across a forced GC while referenced; after release and a forced
/// GC the crate's free functions reclaim the buffer (a healthy process is
/// the observable contract).
fn str_external_owned() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());

    let (held2b, was_external, text_before) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        // Ownership of the boxed UTF-16 data moves into the crate/V8 here.
        let data: Box<[u16]> = vec![0x0042u16; 5].into_boxed_slice();
        let s = v8::String::new_external_twobyte(scope, data).unwrap();
        (
            v8::Global::new(scope, s),
            s.is_external_twobyte(),
            s.to_rust_string_lossy(scope),
        )
    };

    isolate.low_memory_notification();

    let (survived, owned1b_ok) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let survived = unsafe { held2b.open(scope) }.to_rust_string_lossy(scope) == text_before;
        drop(held2b);

        let data: Box<[u8]> = b"owned-1b".to_vec().into_boxed_slice();
        let s2 = v8::String::new_external_onebyte(scope, data).unwrap();
        let owned1b_ok = s2.is_external_onebyte() && s2.to_rust_string_lossy(scope) == "owned-1b";
        (survived, owned1b_ok)
    };
    // `held2b` was released inside the block; this final forced GC drives the
    // crate's free function.
    isolate.low_memory_notification();

    let actual = Json::obj(vec![
        ("owned_twobyte_is_external", Json::b(was_external)),
        ("owned_twobyte_survives_gc", Json::b(survived)),
        ("owned_onebyte_ok", Json::b(owned1b_ok)),
    ]);
    let expected = Json::obj(vec![
        ("owned_twobyte_is_external", Json::b(true)),
        ("owned_twobyte_survives_gc", Json::b(true)),
        ("owned_onebyte_ok", Json::b(true)),
    ]);
    vec![expect_eq("strings/external_owned", expected, actual)]
}

/// Raw external strings with custom destructors: the destructor is not
/// called while the string is alive, fires exactly once on the first forced
/// major GC after the last strong reference drops, receives the original
/// pointer and length, and stays idle across subsequent GCs.
fn str_external_deleter_lifetime() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());

    ONEBYTE_DELETER.get_or_init(DeleterState::new);
    TWOBYTE_DELETER.get_or_init(DeleterState::new);

    let (raw1, raw1_ok) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let memory: Box<[u8]> = vec![7u8; 9].into_boxed_slice();
        let raw = Box::into_raw(memory);
        ONEBYTE_DELETER
            .get()
            .unwrap()
            .handed_off_ptr
            .store(raw.cast::<u8>() as usize, Ordering::SeqCst);
        let s = unsafe {
            v8::String::new_external_onebyte_raw(
                scope,
                raw.cast::<i8>(),
                9,
                counting_deleter_onebyte,
            )
        }
        .unwrap();
        let ok = s.is_external_onebyte() && s.to_rust_string_lossy(scope) == "\u{7}".repeat(9);
        (v8::Global::new(scope, s), ok)
    };

    let (raw2, raw2_ok) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let memory: Box<[u16]> = vec![0x0042u16, 0x0063].into_boxed_slice();
        let raw = Box::into_raw(memory);
        TWOBYTE_DELETER
            .get()
            .unwrap()
            .handed_off_ptr
            .store(raw.cast::<u16>() as usize, Ordering::SeqCst);
        let s = unsafe {
            v8::String::new_external_twobyte_raw(
                scope,
                raw.cast::<u16>(),
                2,
                counting_deleter_twobyte,
            )
        }
        .unwrap();
        let ok = s.is_external_twobyte() && s.to_rust_string_lossy(scope) == "Bc";
        (v8::Global::new(scope, s), ok)
    };

    // While the globals hold the strings alive, a forced GC must not fire
    // either destructor.
    isolate.low_memory_notification();
    let snapshot_while_alive = deleter_snapshot();
    let not_called_while_alive = snapshot_while_alive.0 == 0 && snapshot_while_alive.3 == 0;

    drop(raw1);
    drop(raw2);
    // The first forced major GC after the last reference drop finalizes both
    // external strings and runs each destructor exactly once.
    isolate.low_memory_notification();
    let after_one_gc = deleter_snapshot();
    // Subsequent GCs are no-ops for the destructors.
    isolate.low_memory_notification();
    isolate.low_memory_notification();
    let after_extra_gcs = deleter_snapshot();

    let actual = Json::obj(vec![
        ("raw_onebyte_ok", Json::b(raw1_ok)),
        ("raw_twobyte_ok", Json::b(raw2_ok)),
        ("not_called_while_alive", Json::b(not_called_while_alive)),
        (
            "after_one_gc",
            Json::obj(vec![
                ("onebyte_calls", Json::i(after_one_gc.0 as i64)),
                ("onebyte_len", Json::i(after_one_gc.1 as i64)),
                ("onebyte_ptr_echo", Json::b(after_one_gc.2)),
                ("twobyte_calls", Json::i(after_one_gc.3 as i64)),
                ("twobyte_len", Json::i(after_one_gc.4 as i64)),
                ("twobyte_ptr_echo", Json::b(after_one_gc.5)),
            ]),
        ),
        (
            "exactly_once_across_extra_gcs",
            Json::b(after_extra_gcs == after_one_gc),
        ),
    ]);
    let expected = Json::obj(vec![
        ("raw_onebyte_ok", Json::b(true)),
        ("raw_twobyte_ok", Json::b(true)),
        ("not_called_while_alive", Json::b(true)),
        (
            "after_one_gc",
            Json::obj(vec![
                ("onebyte_calls", Json::i(1)),
                ("onebyte_len", Json::i(9)),
                ("onebyte_ptr_echo", Json::b(true)),
                ("twobyte_calls", Json::i(1)),
                ("twobyte_len", Json::i(2)),
                ("twobyte_ptr_echo", Json::b(true)),
            ]),
        ),
        ("exactly_once_across_extra_gcs", Json::b(true)),
    ]);
    vec![expect_eq(
        "strings/external_deleter_lifetime",
        expected,
        actual,
    )]
}

/// `BigInt::new_from_i64`/`new_from_u64` boundaries and the
/// `u64_value`/`i64_value` truncation semantics.
#[allow(clippy::too_many_lines)]
fn bigint_i64_u64_views() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let zero = v8::BigInt::new_from_i64(scope, 0);
    let neg_one = v8::BigInt::new_from_i64(scope, -1);
    let imin = v8::BigInt::new_from_i64(scope, i64::MIN);
    let imax = v8::BigInt::new_from_i64(scope, i64::MAX);
    let neg42 = v8::BigInt::new_from_i64(scope, -42);
    let umax = v8::BigInt::new_from_u64(scope, u64::MAX);
    let uzero = v8::BigInt::new_from_u64(scope, 0);
    // 2^64 built from words: both 64-bit views truncate.
    let two64 = v8::BigInt::new_from_words(scope, false, &[0, 1]).unwrap();

    let i64_of = |b: &v8::BigInt| -> Json {
        let (value, lossless) = b.i64_value();
        Json::obj(vec![
            ("value", Json::i(value)),
            ("lossless", Json::b(lossless)),
        ])
    };
    let u64_of = |b: &v8::BigInt| -> Json {
        let (value, lossless) = b.u64_value();
        // u64 does not fit Json::i in general; values wider than i64::MAX
        // are encoded exactly as high/low 32-bit halves.
        let encoded = if value <= i64::MAX as u64 {
            Json::i(value as i64)
        } else {
            Json::obj(vec![
                ("lo", Json::i((value & 0xFFFF_FFFF) as i64)),
                ("hi", Json::i((value >> 32) as i64)),
            ])
        };
        Json::obj(vec![("value", encoded), ("lossless", Json::b(lossless))])
    };

    let actual = Json::obj(vec![
        ("zero_i64", i64_of(&zero)),
        ("zero_word_count", Json::i(zero.word_count() as i64)),
        ("neg_one_i64", i64_of(&neg_one)),
        ("neg_one_u64", u64_of(&neg_one)),
        ("i64_min_i64", i64_of(&imin)),
        ("i64_min_word_count", Json::i(imin.word_count() as i64)),
        ("i64_max_i64", i64_of(&imax)),
        ("neg42_u64", u64_of(&neg42)),
        ("u64_max_i64", i64_of(&umax)),
        ("u64_max_u64", u64_of(&umax)),
        ("u64_zero_i64", i64_of(&uzero)),
        ("two64_text", Json::s(&value_text(scope, two64.into()))),
        ("two64_i64", i64_of(&two64)),
        ("two64_u64", u64_of(&two64)),
        ("two64_word_count", Json::i(two64.word_count() as i64)),
    ]);
    let expected = Json::obj(vec![
        (
            "zero_i64",
            Json::obj(vec![("value", Json::i(0)), ("lossless", Json::b(true))]),
        ),
        ("zero_word_count", Json::i(0)),
        (
            "neg_one_i64",
            Json::obj(vec![("value", Json::i(-1)), ("lossless", Json::b(true))]),
        ),
        (
            "neg_one_u64",
            Json::obj(vec![
                (
                    "value",
                    Json::obj(vec![
                        ("lo", Json::i(0xFFFF_FFFF)),
                        ("hi", Json::i(0xFFFF_FFFF)),
                    ]),
                ),
                ("lossless", Json::b(false)),
            ]),
        ),
        (
            "i64_min_i64",
            Json::obj(vec![
                ("value", Json::i(-9_223_372_036_854_775_808)),
                ("lossless", Json::b(true)),
            ]),
        ),
        ("i64_min_word_count", Json::i(1)),
        (
            "i64_max_i64",
            Json::obj(vec![
                ("value", Json::i(9_223_372_036_854_775_807)),
                ("lossless", Json::b(true)),
            ]),
        ),
        (
            "neg42_u64",
            Json::obj(vec![
                (
                    "value",
                    Json::obj(vec![
                        ("lo", Json::i(0xFFFF_FFD6)),
                        ("hi", Json::i(0xFFFF_FFFF)),
                    ]),
                ),
                ("lossless", Json::b(false)),
            ]),
        ),
        (
            "u64_max_i64",
            Json::obj(vec![("value", Json::i(-1)), ("lossless", Json::b(false))]),
        ),
        (
            "u64_max_u64",
            Json::obj(vec![
                (
                    "value",
                    Json::obj(vec![
                        ("lo", Json::i(0xFFFF_FFFF)),
                        ("hi", Json::i(0xFFFF_FFFF)),
                    ]),
                ),
                ("lossless", Json::b(true)),
            ]),
        ),
        (
            "u64_zero_i64",
            Json::obj(vec![("value", Json::i(0)), ("lossless", Json::b(true))]),
        ),
        ("two64_text", Json::s("18446744073709551616")),
        (
            "two64_i64",
            Json::obj(vec![("value", Json::i(0)), ("lossless", Json::b(false))]),
        ),
        (
            "two64_u64",
            Json::obj(vec![("value", Json::i(0)), ("lossless", Json::b(false))]),
        ),
        ("two64_word_count", Json::i(2)),
    ]);
    vec![expect_eq("bigint/i64_u64_views", expected, actual)]
}

/// `BigInt::new_from_words`: zero words (both sign bits), single-word,
/// multi-word values, sign semantics, and `word_count`.
fn bigint_words_construction() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let zero = v8::BigInt::new_from_words(scope, false, &[]).unwrap();
    let zero_negsign = v8::BigInt::new_from_words(scope, true, &[]).unwrap();
    let one = v8::BigInt::new_from_words(scope, false, &[1]).unwrap();
    let umax = v8::BigInt::new_from_words(scope, false, &[u64::MAX]).unwrap();
    let two65_minus_1 = v8::BigInt::new_from_words(scope, false, &[u64::MAX, 1]).unwrap();
    let neg3 = v8::BigInt::new_from_words(scope, true, &[3]).unwrap();
    let three_words = v8::BigInt::new_from_words(scope, false, &[1, 1, 1]).unwrap();

    let actual = Json::obj(vec![
        (
            "zero_words",
            Json::obj(vec![
                ("text", Json::s(&value_text(scope, zero.into()))),
                ("word_count", Json::i(zero.word_count() as i64)),
            ]),
        ),
        // A sign bit over zero words normalizes to plain zero.
        (
            "zero_words_negsign_text",
            Json::s(&value_text(scope, zero_negsign.into())),
        ),
        (
            "one_word",
            Json::obj(vec![
                ("text", Json::s(&value_text(scope, one.into()))),
                ("word_count", Json::i(one.word_count() as i64)),
            ]),
        ),
        (
            "u64_max_word",
            Json::obj(vec![
                ("text", Json::s(&value_text(scope, umax.into()))),
                ("word_count", Json::i(umax.word_count() as i64)),
            ]),
        ),
        (
            "words_max_plus_one",
            Json::obj(vec![
                ("text", Json::s(&value_text(scope, two65_minus_1.into()))),
                ("word_count", Json::i(two65_minus_1.word_count() as i64)),
            ]),
        ),
        (
            "negative_words",
            Json::obj(vec![
                ("text", Json::s(&value_text(scope, neg3.into()))),
                ("word_count", Json::i(neg3.word_count() as i64)),
            ]),
        ),
        (
            "three_words",
            Json::obj(vec![
                ("text", Json::s(&value_text(scope, three_words.into()))),
                ("word_count", Json::i(three_words.word_count() as i64)),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "zero_words",
            Json::obj(vec![("text", Json::s("0")), ("word_count", Json::i(0))]),
        ),
        ("zero_words_negsign_text", Json::s("0")),
        (
            "one_word",
            Json::obj(vec![("text", Json::s("1")), ("word_count", Json::i(1))]),
        ),
        (
            "u64_max_word",
            Json::obj(vec![
                ("text", Json::s("18446744073709551615")),
                ("word_count", Json::i(1)),
            ]),
        ),
        (
            "words_max_plus_one",
            Json::obj(vec![
                ("text", Json::s("36893488147419103231")),
                ("word_count", Json::i(2)),
            ]),
        ),
        (
            "negative_words",
            Json::obj(vec![("text", Json::s("-3")), ("word_count", Json::i(1))]),
        ),
        (
            "three_words",
            Json::obj(vec![
                ("text", Json::s("340282366920938463481821351505477763073")),
                ("word_count", Json::i(3)),
            ]),
        ),
    ]);
    vec![expect_eq("bigint/words_construction", expected, actual)]
}

/// `BigInt::word_count` and `to_words_array`: exact, oversized, and
/// truncated buffers; zero BigInt extraction leaves the buffer untouched;
/// negative values report the sign bit and absolute-value words.
fn bigint_words_extraction() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let zero = v8::BigInt::new_from_i64(scope, 0);
    let mut zero_buf = [0xDEADu64; 4];
    let (zero_sign, zero_words) = zero.to_words_array(&mut zero_buf);
    // Encode the borrowed words before re-reading the raw buffer.
    let zero_words_json = Json::arr(zero_words.iter().map(|w| Json::i(*w as i64)).collect());
    let zero_buf_untouched = zero_buf[0] == 0xDEAD && zero_buf[3] == 0xDEAD;

    let value = v8::BigInt::new_from_words(scope, false, &[1, 1]).unwrap(); // 2^64 + 1
    let mut small = [0u64; 1];
    let (trunc_sign, trunc_words) = value.to_words_array(&mut small);
    let mut exact = [0u64; 2];
    let (exact_sign, exact_words) = value.to_words_array(&mut exact);
    let mut oversized = [0xEEu64; 5];
    let (over_sign, over_words) = value.to_words_array(&mut oversized);
    let over_words_json = Json::arr(over_words.iter().map(|w| Json::i(*w as i64)).collect());
    let oversized_tail_untouched = oversized[2] == 0xEE && oversized[4] == 0xEE;

    let negative = v8::BigInt::new_from_words(scope, true, &[3]).unwrap();
    let mut neg_buf = [0u64; 4];
    let (neg_sign, neg_words) = negative.to_words_array(&mut neg_buf);

    let words_json =
        |words: &[u64]| -> Json { Json::arr(words.iter().map(|w| Json::i(*w as i64)).collect()) };

    let actual = Json::obj(vec![
        (
            "zero",
            Json::obj(vec![
                ("sign", Json::b(zero_sign)),
                ("words", zero_words_json),
                ("buffer_untouched", Json::b(zero_buf_untouched)),
            ]),
        ),
        (
            "truncated_to_one_word",
            Json::obj(vec![
                ("sign", Json::b(trunc_sign)),
                ("words", words_json(trunc_words)),
            ]),
        ),
        (
            "exact",
            Json::obj(vec![
                ("sign", Json::b(exact_sign)),
                ("words", words_json(exact_words)),
            ]),
        ),
        (
            "oversized",
            Json::obj(vec![
                ("sign", Json::b(over_sign)),
                ("words", over_words_json),
                ("tail_untouched", Json::b(oversized_tail_untouched)),
            ]),
        ),
        (
            "negative",
            Json::obj(vec![
                ("sign", Json::b(neg_sign)),
                ("words", words_json(neg_words)),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "zero",
            Json::obj(vec![
                ("sign", Json::b(false)),
                ("words", Json::arr(vec![])),
                ("buffer_untouched", Json::b(true)),
            ]),
        ),
        // Truncation silently drops the high word.
        (
            "truncated_to_one_word",
            Json::obj(vec![
                ("sign", Json::b(false)),
                ("words", Json::arr(vec![Json::i(1)])),
            ]),
        ),
        (
            "exact",
            Json::obj(vec![
                ("sign", Json::b(false)),
                ("words", Json::arr(vec![Json::i(1), Json::i(1)])),
            ]),
        ),
        (
            "oversized",
            Json::obj(vec![
                ("sign", Json::b(false)),
                ("words", Json::arr(vec![Json::i(1), Json::i(1)])),
                ("tail_untouched", Json::b(true)),
            ]),
        ),
        (
            "negative",
            Json::obj(vec![
                ("sign", Json::b(true)),
                ("words", Json::arr(vec![Json::i(3)])),
            ]),
        ),
    ]);
    vec![expect_eq("bigint/words_extraction", expected, actual)]
}

/// Word roundtrip identity and JS interop: words -> BigInt -> words,
/// two's-complement i64 truncation of large values, JS-created BigInts
/// observed through the native API, and native BigInts observed from JS.
fn bigint_roundtrip_and_js() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Words -> BigInt -> same words back out.
    let source = v8::BigInt::new_from_words(scope, false, &[u64::MAX, 42]).unwrap();
    let mut echo = [0u64; 3];
    let (echo_sign, echo_words) = source.to_words_array(&mut echo);
    let echo_matches = echo_words == [u64::MAX, 42] && !echo_sign;
    let echo_word_count = source.word_count();

    // Sign bit with the same words: -(42 * 2^64 + 2^64 - 1); the i64 view is
    // the two's-complement truncation of the value's low 64 bits.
    let signed = v8::BigInt::new_from_words(scope, true, &[u64::MAX, 42]).unwrap();
    let (signed_i64, signed_lossless) = signed.i64_value();

    // JS-created BigInt observed natively.
    let js_bigint: v8::Local<v8::BigInt> = eval(scope, "2n ** 64n + 1n")
        .and_then(|v| v.try_cast::<v8::BigInt>().ok())
        .unwrap();
    let mut js_words = [0u64; 4];
    let (js_sign, js_words_out) = js_bigint.to_words_array(&mut js_words);
    let (js_i64, js_i64_lossless) = js_bigint.i64_value();

    // JS 0n has zero words.
    let js_zero: v8::Local<v8::BigInt> = eval(scope, "0n")
        .and_then(|v| v.try_cast::<v8::BigInt>().ok())
        .unwrap();

    // Native BigInt observed from JS.
    let native = v8::BigInt::new_from_words(scope, false, &[5, 1]).unwrap();
    let native_text = value_text(scope, native.into());
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "nat").unwrap().into(),
            native.into(),
        )
        .unwrap();
    let js_typeof = eval_text(scope, "typeof nat");
    let js_sum = eval_text(scope, "(nat + 1n).toString()");

    let words_json =
        |words: &[u64]| -> Json { Json::arr(words.iter().map(|w| Json::i(*w as i64)).collect()) };

    let actual = Json::obj(vec![
        (
            "words_roundtrip",
            Json::obj(vec![
                ("matches", Json::b(echo_matches)),
                ("word_count", Json::i(echo_word_count as i64)),
                ("echoed", words_json(echo_words)),
            ]),
        ),
        (
            "signed_words_i64_truncation",
            Json::obj(vec![
                ("i64", Json::i(signed_i64)),
                ("i64_lossless", Json::b(signed_lossless)),
            ]),
        ),
        (
            "js_bigint",
            Json::obj(vec![
                ("sign", Json::b(js_sign)),
                ("words", words_json(js_words_out)),
                ("i64", Json::i(js_i64)),
                ("i64_lossless", Json::b(js_i64_lossless)),
            ]),
        ),
        ("js_zero_word_count", Json::i(js_zero.word_count() as i64)),
        ("native_to_js_text", Json::s(&native_text)),
        ("js_typeof", Json::s(&js_typeof)),
        ("js_sum", Json::s(&js_sum)),
    ]);
    let expected = Json::obj(vec![
        (
            "words_roundtrip",
            Json::obj(vec![
                ("matches", Json::b(true)),
                ("word_count", Json::i(2)),
                // u64::MAX echoes as -1 through the i64 JSON encoding.
                ("echoed", Json::arr(vec![Json::i(-1), Json::i(42)])),
            ]),
        ),
        (
            "signed_words_i64_truncation",
            Json::obj(vec![("i64", Json::i(1)), ("i64_lossless", Json::b(false))]),
        ),
        (
            "js_bigint",
            Json::obj(vec![
                ("sign", Json::b(false)),
                ("words", Json::arr(vec![Json::i(1), Json::i(1)])),
                ("i64", Json::i(1)),
                ("i64_lossless", Json::b(false)),
            ]),
        ),
        ("js_zero_word_count", Json::i(0)),
        ("native_to_js_text", Json::s("18446744073709551621")),
        ("js_typeof", Json::s("bigint")),
        ("js_sum", Json::s("18446744073709551622")),
    ]);
    vec![expect_eq("bigint/roundtrip_and_js", expected, actual)]
}

/// The `new_from_words` over-limit failure: more than `i::BigInt::kMaxLength`
/// (= 16777215 on 64-bit) words returns `None` with a pending JS
/// `RangeError`; a TryCatch observes it, resets it, and the isolate stays
/// fully usable.
fn bigint_max_words_range_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    // One word beyond kMaxLength: 16777215 + 1 = 16777216 words (128 MiB of
    // input, transiently allocated).
    const OVER_LIMIT_WORDS: usize = 16_777_216;
    let over_words = vec![1u64; OVER_LIMIT_WORDS];
    let over = v8::BigInt::new_from_words(tc, false, &over_words);
    drop(over_words);
    let over_is_none = over.is_none();
    let caught = tc.has_caught();
    let message = tc
        .message()
        .map(|m| m.get(tc).to_rust_string_lossy(tc))
        .unwrap_or_default();
    let exception = tc
        .exception()
        .and_then(|e| e.to_string(tc))
        .map(|s| s.to_rust_string_lossy(tc))
        .unwrap_or_default();
    let has_terminated = tc.has_terminated();
    tc.reset();

    // The isolate stays fully usable after the reset.
    let usable = eval_text(tc, "1 + 1");
    let bigints_still_usable = v8::BigInt::new_from_i64(tc, 7).i64_value() == (7, true);

    let actual = Json::obj(vec![
        ("returns_none", Json::b(over_is_none)),
        ("caught", Json::b(caught)),
        ("has_terminated", Json::b(has_terminated)),
        ("message", Json::s(&message)),
        ("exception", Json::s(&exception)),
        ("usable_after_reset", Json::s(&usable)),
        ("bigints_still_usable", Json::b(bigints_still_usable)),
    ]);
    let expected = Json::obj(vec![
        ("returns_none", Json::b(true)),
        ("caught", Json::b(true)),
        ("has_terminated", Json::b(false)),
        (
            "message",
            Json::s("Uncaught RangeError: Maximum BigInt size exceeded"),
        ),
        (
            "exception",
            Json::s("RangeError: Maximum BigInt size exceeded"),
        ),
        ("usable_after_reset", Json::s("2")),
        ("bigints_still_usable", Json::b(true)),
    ]);
    vec![expect_eq("bigint/max_words_range_error", expected, actual)]
}

// ---------------------------------------------------------------------------
// Registry and report assembly
// ---------------------------------------------------------------------------

type CheckFn = fn() -> Vec<CheckOutcome>;

/// Fixed check order; the JSON-lines fixture follows exactly this order.
const CHECKS: &[CheckFn] = &[
    str_max_length_and_empty,
    str_creation_types,
    str_concat_semantics,
    str_write_two_byte_views,
    str_write_one_byte_views,
    str_write_utf8_views,
    str_value_view_flavors,
    str_external_static_and_const,
    str_external_resource_identity,
    str_external_owned,
    str_external_deleter_lifetime,
    bigint_i64_u64_views,
    bigint_words_construction,
    bigint_words_extraction,
    bigint_roundtrip_and_js,
    bigint_max_words_range_error,
];

fn run_checks() -> Vec<CheckOutcome> {
    let mut outcomes = Vec::new();
    for check in CHECKS {
        outcomes.extend(check());
    }
    outcomes
}

fn run_conformance() -> i32 {
    oracle::ensure_v8();
    let outcomes = run_checks();
    let total = outcomes.len();
    let mut passed = 0usize;
    let mut text = String::new();
    for outcome in &outcomes {
        if outcome.passed() {
            passed += 1;
        }
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    let failed = total - passed;
    text.push_str(&summary_line(total, passed, failed));
    text.push('\n');
    let stdout = std::io::stdout();
    let mut lock = stdout.lock();
    let _ = lock.write_all(text.as_bytes());
    let _ = lock.flush();
    if failed == 0 {
        0
    } else {
        1
    }
}

// ---------------------------------------------------------------------------
// Benchmark mode (see the module docs for the methodology contract).
// ---------------------------------------------------------------------------

const BENCH_WARM_UP: Duration = Duration::from_secs(1);
const BENCH_SAMPLE_TARGET: Duration = Duration::from_millis(60);
const BENCH_SAMPLES: usize = 50;

type BenchFn = fn(&mut v8::PinScope<'_, '_>) -> u64;

struct BenchSpec {
    name: &'static str,
    run: BenchFn,
}

fn bench_string_new_ascii_32(scope: &mut v8::PinScope<'_, '_>) -> u64 {
    let s = v8::String::new(scope, "abcdefghijklmnopqrstuvwxyzabcd").unwrap();
    u64::from(s.is_onebyte()) + s.length() as u64
}

fn bench_string_from_two_byte_16(scope: &mut v8::PinScope<'_, '_>) -> u64 {
    let units: [u16; 16] = [
        0x0048, 0x00E9, 0xD83E, 0xDD80, 0xD83E, 0xDD80, 0x0063, 0x0064, 0x0065, 0x0066, 0x0067,
        0x0068, 0x0069, 0x006A, 0x006B, 0x006C,
    ];
    let s = v8::String::new_from_two_byte(scope, &units, v8::NewStringType::Normal).unwrap();
    u64::from(s.contains_only_onebyte()) + s.length() as u64
}

fn bench_string_concat_x4_read(scope: &mut v8::PinScope<'_, '_>) -> u64 {
    let a = v8::String::new(scope, "abcdefgh").unwrap();
    let b = v8::String::concat(scope, a, a).unwrap();
    let c = v8::String::concat(scope, b, b).unwrap();
    let d = v8::String::concat(scope, c, c).unwrap();
    let e = v8::String::concat(scope, d, d).unwrap();
    e.to_rust_string_lossy(scope).len() as u64
}

fn bench_string_write_utf8_64(scope: &mut v8::PinScope<'_, '_>) -> u64 {
    let s = v8::String::new(
        scope,
        "The quick brown fox jumps over the lazy dog \u{20AC}\u{1F980}!",
    )
    .unwrap();
    let mut buf = [0u8; 128];
    let n = s.write_utf8_v2(scope, &mut buf, v8::WriteFlags::default(), None);
    u64::from(n > 0)
}

static BENCH_EXT_DATA: &[u8] = b"benchmark-static-external-onebyte-payload";

fn bench_string_external_static_new(scope: &mut v8::PinScope<'_, '_>) -> u64 {
    let s = v8::String::new_external_onebyte_static(scope, BENCH_EXT_DATA).unwrap();
    u64::from(s.is_external_onebyte()) + s.length() as u64
}

fn bench_bigint_new_from_i64(scope: &mut v8::PinScope<'_, '_>) -> u64 {
    let b = v8::BigInt::new_from_i64(scope, -1_234_567_890_123_456_789);
    let (v, lossless) = b.i64_value();
    (v as u64).wrapping_add(u64::from(lossless))
}

static BENCH_WORDS: [u64; 2] = [u64::MAX, 42];

fn bench_bigint_words_roundtrip(scope: &mut v8::PinScope<'_, '_>) -> u64 {
    let b = v8::BigInt::new_from_words(scope, false, &BENCH_WORDS).unwrap();
    let mut words = [0u64; 2];
    let (sign, out) = b.to_words_array(&mut words);
    u64::from(out == BENCH_WORDS && !sign) + b.word_count() as u64
}

const BENCHES: &[BenchSpec] = &[
    BenchSpec {
        name: "strings_bigint/string_new_ascii_32",
        run: bench_string_new_ascii_32,
    },
    BenchSpec {
        name: "strings_bigint/string_from_two_byte_16",
        run: bench_string_from_two_byte_16,
    },
    BenchSpec {
        name: "strings_bigint/string_concat_x4_read",
        run: bench_string_concat_x4_read,
    },
    BenchSpec {
        name: "strings_bigint/string_write_utf8_64",
        run: bench_string_write_utf8_64,
    },
    BenchSpec {
        name: "strings_bigint/string_external_static_new",
        run: bench_string_external_static_new,
    },
    BenchSpec {
        name: "strings_bigint/bigint_new_from_i64",
        run: bench_bigint_new_from_i64,
    },
    BenchSpec {
        name: "strings_bigint/bigint_words_roundtrip",
        run: bench_bigint_words_roundtrip,
    },
];

fn bench_environment_banner() {
    eprintln!(
        "# oracle bench env: os={} arch={} logical_cpus={} build_profile={} \
         v8_version_string={} v8_get_version={} mode=hand-rolled-bench \
         warmup_ms={} samples={} target_ms_per_sample={}",
        std::env::consts::OS,
        std::env::consts::ARCH,
        std::thread::available_parallelism().map_or(0, std::num::NonZeroUsize::get),
        if cfg!(debug_assertions) {
            "debug"
        } else {
            "release"
        },
        v8::VERSION_STRING,
        v8::V8::get_version(),
        BENCH_WARM_UP.as_millis(),
        BENCH_SAMPLES,
        BENCH_SAMPLE_TARGET.as_millis(),
    );
}

/// One timed block: `iterations` calls to `run`, each in a fresh nested
/// handle scope, inside one context. Returns elapsed seconds and a checksum
/// preventing dead-code elimination.
fn timed_block(isolate: &mut v8::Isolate, iterations: u64, run: BenchFn) -> (f64, u64) {
    let start = Instant::now();
    let mut acc = 0u64;
    {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        for _ in 0..iterations {
            v8::scope!(let inner, scope);
            acc = acc.wrapping_add(run(inner));
        }
    }
    (start.elapsed().as_secs_f64(), acc)
}

/// Runs one benchmark: 1 s warm-up, an in-place calibration of the
/// iterations-per-sample count, then 50 samples. Returns per-iteration
/// milliseconds per sample plus the checksum.
fn measure_bench(isolate: &mut v8::Isolate, run: BenchFn) -> (Vec<f64>, u64, u64) {
    // Warm-up: 1 s of unmeasured single iterations.
    let warmup_start = Instant::now();
    let mut iters = 0u64;
    while warmup_start.elapsed() < BENCH_WARM_UP {
        let _ = timed_block(isolate, 1, run);
        iters += 1;
    }

    // Calibrate iterations per sample so one sample lasts ~60 ms, keeping
    // the timer overhead per sample in the sub-percent range.
    let calibration_iters = 1000u64.max((iters / 4).min(100_000));
    let (cal_elapsed, _) = timed_block(isolate, calibration_iters, run);
    let per_iter = cal_elapsed / calibration_iters as f64;
    let iters_per_sample = ((BENCH_SAMPLE_TARGET.as_secs_f64() / per_iter).ceil() as u64).max(1);

    let mut samples = Vec::with_capacity(BENCH_SAMPLES);
    let mut checksum = 0u64;
    for _ in 0..BENCH_SAMPLES {
        let (elapsed, acc) = timed_block(isolate, iters_per_sample, run);
        samples.push(elapsed * 1000.0 / iters_per_sample as f64);
        checksum = checksum.wrapping_add(acc);
    }
    (samples, checksum, iters_per_sample)
}

fn run_benchmarks() {
    oracle::ensure_v8();
    bench_environment_banner();
    let isolate = &mut v8::Isolate::new(Default::default());
    let stdout = std::io::stdout();
    let mut out = stdout.lock();
    for spec in BENCHES {
        let (samples, checksum, iters_per_sample) = measure_bench(isolate, spec.run);
        let mut sorted = samples.clone();
        sorted.sort_by(f64::total_cmp);
        let median = sorted[sorted.len() / 2];
        let mean: f64 = samples.iter().sum::<f64>() / samples.len() as f64;
        let raw: Vec<String> = samples.iter().map(|s| format!("{s:.6}")).collect();
        let line = format!(
            "{{\"bench\":\"{}\",\"samples\":{},\"iters_per_sample\":{},\
             \"median_ms\":{median:.6},\"mean_ms\":{mean:.6},\"min_ms\":{:.6},\
             \"max_ms\":{:.6},\"checksum\":{checksum},\"raw_ms\":[{}]}}\n",
            spec.name,
            samples.len(),
            iters_per_sample,
            sorted[0],
            sorted[sorted.len() - 1],
            raw.join(","),
        );
        let _ = out.write_all(line.as_bytes());
        let _ = out.flush();
    }
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

fn main() -> std::process::ExitCode {
    if std::env::args().any(|arg| arg == "--bench") {
        run_benchmarks();
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::from(run_conformance() as u8)
    }
}
