//! Negative and boundary characterization for the typed-array /
//! ArrayBufferView slice. Complements `src/bin/conformance-typed-arrays.rs`
//! with the cases that must NOT run inside the fixture binary.
//!
//! # Wrapper contract: which operations are CHECK-fatal
//!
//! The Go port must treat every one of the following native operations as
//! **process-fatal V8 aborts** (never as recoverable `None`/error results,
//! never as JS-exception-shaped failures). They all abort inside the engine
//! before any observable return value exists:
//!
//! 1. **Alignment**: `X::new(scope, ab, byte_offset, length)` with
//!    `byte_offset % element_size != 0`, for every kind whose element size
//!    is greater than 1 — including when `length == 0`. Engine CHECK
//!    `CHECK_EQ(0, byte_offset % element_size)` in
//!    `Factory::NewJSTypedArray` (engine `src/heap/factory.cc`; fatal text
//!    contains `byte_offset % element_size`). Zero-length views are NOT
//!    exempt from the alignment CHECK. 1-byte kinds have no alignment
//!    constraint; `DataView::new` has none either (byte-granular).
//! 2. **Bounds**: `X::new(scope, ab, byte_offset, length)` where
//!    `byte_offset > buffer byte_length`, `length * element_size >`
//!    `buffer byte_length`, or `byte_offset + length * element_size >`
//!    `buffer byte_length`. Engine CHECKs
//!    `CHECK_LE(byte_length, buffer->GetByteLength())`,
//!    `CHECK_LE(byte_offset, buffer->GetByteLength())`,
//!    `CHECK_LE(byte_offset + byte_length, buffer->GetByteLength())` in
//!    `Factory::NewJSArrayBufferView` (fatal text contains
//!    `buffer->GetByteLength()`).
//! 3. **Per-type max length**: `X::new` with `length > X::MAX_LENGTH`
//!    (= `TypedArray::MAX_BYTE_LENGTH / element_size`). This is an
//!    `Utils::ApiCheck` in `Type##Array::New` (engine `src/api.cc`,
//!    `TYPED_ARRAY_NEW`) with the deterministic message
//!    `length exceeds max allowed value`.
//! 4. **DataView**: `DataView::new(scope, ab, byte_offset, byte_length)`
//!    fails fatally under the same `NewJSArrayBufferView` bounds CHECKs
//!    (no alignment rule, but byte_offset/byte_length must stay in bounds).
//! 5. **Float16Array with the feature flag off** (not this build):
//!    `Float16Array::New` starts with
//!    `Utils::ApiCheck(i::v8_flags.js_float16array, ...,
//!    "Float16Array is not supported")` (engine `src/api.cc`), so an
//!    embedder that turns `js_float16array` off gets a deterministic
//!    abort. In the pinned build the flag ships ON
//!    (`JAVASCRIPT_SHIPPING_FEATURES_BASE` in
//!    `src/flags/flag-definitions.h`) and native construction succeeds.
//!
//! The corresponding **JavaScript** operations do NOT abort: the typed-array
//! and DataView constructors throw deterministic `RangeError`s instead
//! (pinned in the `typedarrays/js_error_paths` fixture check). The Go
//! wrapper must never map native misuse onto those JS-shaped errors, and
//! must never route JS errors into native aborts.
//!
//! Deliberately out of scope (owned by `tests/buffers_negative.rs`):
//! out-of-bounds `Uint8Array`/`Float64Array` construction and the
//! `assert_use_count_eq` panic. Detached-buffer view construction is NOT
//! fatal for in-bounds zero-length arguments (pinned in the fixture).
//!
//! Mechanics: each probe runs in a child test process (spawned via
//! `current_exe`, the same pattern as `tests/buffers_negative.rs`) and the
//! parent asserts abnormal termination with V8's deterministic fatal text
//! instead of a Rust panic.

use std::process::Command;

/// Runs one `#[ignore]`d probe test from this same test binary in a child
/// process and returns its status plus captured stderr.
fn run_probe(name: &str) -> (std::process::ExitStatus, String) {
    let exe = std::env::current_exe().expect("current test executable");
    let output = Command::new(exe)
        .args([
            "--exact",
            name,
            "--ignored",
            "--test-threads",
            "1",
            // Without this the harness buffers probe output and a hard
            // V8 abort drops the buffer before it is flushed.
            "--nocapture",
        ])
        .output()
        .expect("failed to spawn probe process");
    let stderr = String::from_utf8_lossy(&output.stderr).to_string();
    (output.status, stderr)
}

/// Asserts the child terminated abnormally through V8's fatal path (not a
/// caught Rust panic, which would look entirely different) and that stderr
/// contains the given deterministic CHECK fragments.
fn assert_v8_fatal(name: &str, fragments: &[&str]) {
    let (status, stderr) = run_probe(name);
    assert!(
        !status.success(),
        "{name}: probe unexpectedly survived; status={status}"
    );
    for fragment in fragments {
        assert!(
            stderr.contains(fragment),
            "{name}: stderr missing {fragment:?}; stderr:\n{stderr}"
        );
    }
    assert!(
        !stderr.contains("panicked at"),
        "{name}: probe failed via Rust panic instead of V8 fatal; stderr:\n{stderr}"
    );
}

#[test]
fn int16_misaligned_offset_is_v8_fatal() {
    assert_v8_fatal(
        "probe_int16_misaligned_offset",
        &["byte_offset % element_size"],
    );
}

#[test]
fn int32_misaligned_offset_is_v8_fatal() {
    assert_v8_fatal(
        "probe_int32_misaligned_offset",
        &["byte_offset % element_size"],
    );
}

#[test]
fn biguint64_misaligned_offset_is_v8_fatal() {
    assert_v8_fatal(
        "probe_biguint64_misaligned_offset",
        &["byte_offset % element_size"],
    );
}

#[test]
fn float16_misaligned_offset_is_v8_fatal() {
    assert_v8_fatal(
        "probe_float16_misaligned_offset",
        &["byte_offset % element_size"],
    );
}

#[test]
fn misaligned_zero_length_is_still_v8_fatal() {
    assert_v8_fatal(
        "probe_misaligned_zero_length",
        &["byte_offset % element_size"],
    );
}

#[test]
fn offset_past_end_zero_length_is_v8_fatal() {
    assert_v8_fatal(
        "probe_offset_past_end_zero_length",
        &["byte_offset <= buffer->GetByteLength()"],
    );
}

#[test]
fn out_of_bounds_span_is_v8_fatal() {
    assert_v8_fatal(
        "probe_out_of_bounds_span",
        &["byte_length <= buffer->GetByteLength()"],
    );
}

#[test]
fn data_view_offset_past_end_is_v8_fatal() {
    assert_v8_fatal(
        "probe_data_view_offset_past_end",
        &["byte_offset <= buffer->GetByteLength()"],
    );
}

#[test]
fn float64_length_exceeding_max_is_api_check_fatal() {
    assert_v8_fatal(
        "probe_float64_length_exceeding_max",
        &["Float64Array::New", "length exceeds max allowed value"],
    );
}

// --- child-process probes (never run in the normal suite: #[ignore]) ------

/// Every probe uses the same prologue: platform + isolate + context + a
/// fresh 16-byte ArrayBuffer. (Plain repetition instead of a macro:
/// `macro_rules!` hygiene would hide the macro-created `ab` binding from
/// the probe body.) Everything after the checked call in each probe is
/// unreachable when the contract holds: the process aborts inside the call
/// under test.

#[test]
#[ignore]
fn probe_int16_misaligned_offset() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // Offset 3 is not a multiple of the 2-byte element size: engine CHECK
    // `0 == byte_offset % element_size` fails inside the call.
    let _ = v8::Int16Array::new(scope, ab, 3, 2);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_int32_misaligned_offset() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // Offset 2 is not a multiple of the 4-byte element size.
    let _ = v8::Int32Array::new(scope, ab, 2, 1);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_biguint64_misaligned_offset() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // Offset 4 is not a multiple of the 8-byte element size.
    let _ = v8::BigUint64Array::new(scope, ab, 4, 1);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_float16_misaligned_offset() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // Float16Array follows the 2-byte alignment class when the feature is
    // enabled (it ships on in this build).
    let _ = v8::Float16Array::new(scope, ab, 3, 1);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_misaligned_zero_length() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // The alignment CHECK is unconditional: length == 0 does not exempt a
    // misaligned offset.
    let _ = v8::Int16Array::new(scope, ab, 1, 0);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_offset_past_end_zero_length() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // 17 > 16: engine CHECK `byte_offset <= buffer->GetByteLength()` fails
    // even with length == 0.
    let _ = v8::Uint8Array::new(scope, ab, 17, 0);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_out_of_bounds_span() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // 5 * 4 = 20 > 16: engine CHECK
    // `byte_length <= buffer->GetByteLength()` fires first
    // (NewJSArrayBufferView order).
    let _ = v8::Int32Array::new(scope, ab, 0, 5);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_data_view_offset_past_end() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // DataView has no alignment rule but the same bounds CHECKs: 17 > 16.
    let _ = v8::DataView::new(scope, ab, 17, 0);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_float64_length_exceeding_max() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // usize::MAX > Float64Array::MAX_LENGTH: Utils::ApiCheck aborts with
    // "length exceeds max allowed value" before the factory is reached.
    let _ = v8::Float64Array::new(scope, ab, 0, usize::MAX);
    println!("probe:survived");
}
