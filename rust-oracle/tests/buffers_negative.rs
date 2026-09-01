//! Negative and boundary characterization for the buffers/serialization
//! slice. Complements `src/bin/conformance-buffers.rs` with the cases that
//! must NOT run inside the fixture binary.
//!
//! Contract characterized here:
//! - `SharedRef::<BackingStore>::assert_use_count_eq` polls for up to one
//!   second and panics on a mismatched count (the fixture-side
//!   `use_count_is` helper relies on catching exactly that panic).
//! - Out-of-bounds AND misaligned-offset `TypedArray::new`, out-of-bounds
//!   `DataView::new`, and impossible `ArrayBuffer::new` allocations are
//!   process-fatal V8 failures, not recoverable `None` results. They are
//!   characterized out-of-process: each probe runs in a child test process
//!   (spawned via `current_exe`, the same pattern as
//!   `tests/callback_panic_boundary.rs`), and the parent asserts abnormal
//!   termination with V8's deterministic fatal text instead of a Rust
//!   panic.

use std::process::Command;

/// A Rust-owned backing store reports exactly 1 reference; asserting any
/// other count panics (after the crate's one-second retry window).
#[test]
#[should_panic(expected = "reference count does not match expectation")]
fn assert_use_count_mismatch_panics() {
    let bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
        1, 2, 3, 4,
    ]));
    bs.assert_use_count_eq(2);
}

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
/// caught Rust panic, which would look entirely different).
fn assert_v8_fatal(name: &str) {
    let (status, stderr) = run_probe(name);
    assert!(
        !status.success(),
        "{name}: probe unexpectedly survived; status={status}"
    );
    assert!(
        stderr.contains("Check failed") || stderr.contains("Fatal"),
        "{name}: expected a V8 fatal on stderr; stderr:\n{stderr}"
    );
    assert!(
        !stderr.contains("panicked at"),
        "{name}: probe failed via Rust panic instead of V8 fatal; stderr:\n{stderr}"
    );
}

#[test]
fn typed_array_out_of_bounds_is_v8_fatal() {
    assert_v8_fatal("probe_typed_array_out_of_bounds");
}

#[test]
fn typed_array_misaligned_offset_is_v8_fatal() {
    assert_v8_fatal("probe_typed_array_misaligned_offset");
}

#[test]
fn data_view_out_of_bounds_is_v8_fatal() {
    assert_v8_fatal("probe_data_view_out_of_bounds");
}

#[test]
fn impossible_array_buffer_allocation_is_fatal() {
    // Allocation failure goes through V8's OOM/fatal path (text varies), so
    // only abnormal non-panic termination is asserted.
    let (status, stderr) = run_probe("probe_impossible_allocation");
    assert!(
        !status.success(),
        "impossible allocation unexpectedly survived"
    );
    assert!(
        !stderr.contains("panicked at"),
        "allocation failure surfaced as a Rust panic; stderr:\n{stderr}"
    );
}

// --- child-process probes (never run in the normal suite: #[ignore]) ------

#[test]
#[ignore]
fn probe_typed_array_out_of_bounds() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // 8 + 16 > 16: V8 CHECK-fails inside the call.
    let _ = v8::Uint8Array::new(scope, ab, 8, 16);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_typed_array_misaligned_offset() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // Offset 4 is not a multiple of the Float64 element size: V8
    // CHECK-fails ("0 == byte_offset % element_size").
    let _ = v8::Float64Array::new(scope, ab, 4, 1);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_data_view_out_of_bounds() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 16);
    // 2 + 100 > 16: V8 CHECK-fails inside the call.
    let _ = v8::DataView::new(scope, ab, 2, 100);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_impossible_allocation() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _ = v8::ArrayBuffer::new(scope, usize::MAX);
    println!("probe:survived");
}
