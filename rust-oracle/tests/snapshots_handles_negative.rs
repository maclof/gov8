//! Negative and subprocess-isolated characterization for the
//! snapshot/handle/termination slice.
//!
//! Each scenario runs the `conformance-snapshots` binary in a dedicated
//! process with one `mode=<name>` argument (the same auto-discovered
//! binary/test architecture as the other slices). Modes exist for behavior
//! that must never run inside the deterministic JSON-lines report:
//!
//! - `mode=drop-creator-without-blob`: dropping a snapshot-creator isolate
//!   without `OwnedIsolate::create_blob` panics with the crate's
//!   documented message (pinned source `isolate.rs`, `impl Drop for
//!   OwnedIsolate`).
//! - `mode=global-eq-after-dispose`: handle access after the host isolate
//!   was disposed panics (pinned source `isolate.rs`, `IsolateLiveness`).
//! - `mode=terminate-loop`: cross-thread termination of a tight JS loop
//!   through a cloned `IsolateHandle`, then cancellation and reuse;
//!   prints one deterministic JSON line.
//! - `mode=invalid-startup-data-fatal`: upstream caveat —
//!   `StartupData::is_valid()` on a blob shorter than the snapshot version
//!   header trips a V8 `CHECK` (`Snapshot::VersionIsValid`) and aborts the
//!   process instead of returning `false`.

use std::process::Command;

fn run_mode(mode: &str) -> std::process::Output {
    Command::new(env!("CARGO_BIN_EXE_conformance-snapshots"))
        .arg(mode)
        .output()
        .expect("failed to run conformance-snapshots binary")
}

#[test]
fn dropping_snapshot_creator_without_create_blob_panics() {
    let output = run_mode("mode=drop-creator-without-blob");
    assert!(
        !output.status.success(),
        "dropping a creator isolate without create_blob must not succeed"
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("create_blob before dropping an isolate"),
        "unexpected panic message: {stderr}"
    );
    // Rust's default panic path exits with 101 on Windows.
    assert_eq!(
        output.status.code(),
        Some(101),
        "panic must exit with the standard Rust code"
    );
}

#[test]
fn handle_equality_after_isolate_dispose_panics() {
    let output = run_mode("mode=global-eq-after-dispose");
    assert!(
        !output.status.success(),
        "comparing a Global after its isolate was disposed must not succeed"
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("disposed Isolate"),
        "unexpected panic message: {stderr}"
    );
    assert_eq!(output.status.code(), Some(101));
}

#[test]
fn terminate_loop_from_other_thread_is_recoverable() {
    let output = run_mode("mode=terminate-loop");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "cross-thread terminate + cancel + reuse must succeed; stdout:\n{stdout}"
    );
    // The mode prints exactly one deterministic JSON line documenting the
    // observable sequence: the interrupt is requested from a foreign
    // thread, the loop reports through the TryCatch, cancellation restores
    // the isolate, and a follow-up script evaluates normally.
    let expected = concat!(
        "{\"mode\":\"terminate-loop\",\"requested\":true,\"ran_ok\":false,",
        "\"has_caught\":true,\"can_continue\":false,\"cancel_ok\":true,",
        "\"reused\":\"42\"}\n"
    );
    assert_eq!(stdout, expected, "termination report diverged");
}

#[test]
fn startup_data_is_valid_on_undersized_blob_is_a_v8_fatal() {
    // Upstream caveat, deliberately out-of-process: V8 CHECK-fails instead
    // of answering `false`. On Windows the prebuilt V8 aborts with status
    // STATUS_BREAKPOINT (0x80000003); we only assert "not success" plus the
    // CHECK report so the contract survives unrelated exit-code details.
    let output = run_mode("mode=invalid-startup-data-fatal");
    assert!(
        !output.status.success(),
        "is_valid() on undersized data must not return normally"
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("Check failed") && stderr.contains("raw_size"),
        "unexpected fatal report: {stderr}"
    );
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(
        !stdout.contains("is_valid"),
        "the is_valid call must never return: {stdout}"
    );
}

#[test]
fn unknown_mode_is_rejected() {
    let output = run_mode("mode=does-not-exist");
    assert!(!output.status.success());
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("unknown mode"),
        "unexpected rejection message: {stderr}"
    );
}
