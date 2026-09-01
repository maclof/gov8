//! Rust promise callback panic boundaries (out-of-process characterization).
//!
//! The pinned crate adapts a native function callback through an extern-C
//! trampoline, while its promise-reject callback is itself extern C. Rust
//! therefore aborts rather than allowing either panic to unwind into V8.

fn assert_promise_panic_aborts(
    probe: &str,
    before_marker: &str,
    entered_marker: &str,
    panic_marker: &str,
    after_marker: &str,
) {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_promise-panic-boundary"))
        .arg(probe)
        .output()
        .expect("failed to run promise-panic-boundary binary");
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);

    assert!(stderr.contains(before_marker), "{probe}: stderr:\n{stderr}");
    assert!(
        stderr.contains(entered_marker),
        "{probe}: callback was not entered; stderr:\n{stderr}"
    );
    assert!(
        stderr.contains(panic_marker),
        "{probe}: original panic marker missing; stderr:\n{stderr}"
    );
    assert!(
        stderr.contains("panic in a function that cannot unwind"),
        "{probe}: extern-C abort marker missing; stderr:\n{stderr}"
    );
    assert!(
        !stdout.contains(after_marker) && !stderr.contains(after_marker),
        "{probe}: execution returned past callback; stdout:\n{stdout}\nstderr:\n{stderr}"
    );
    assert!(
        !output.status.success(),
        "{probe}: process exited cleanly; stdout:\n{stdout}\nstderr:\n{stderr}"
    );
}

#[test]
fn native_promise_handler_panic_aborts_the_process() {
    assert_promise_panic_aborts(
        "native-handler",
        "marker:promise-native-before-checkpoint",
        "marker:promise-native-entered",
        "promise-native-handler-panic",
        "marker:promise-native-after-checkpoint",
    );
}

#[test]
fn promise_reject_callback_panic_aborts_the_process() {
    assert_promise_panic_aborts(
        "reject-callback",
        "marker:promise-reject-before-reject",
        "marker:promise-reject-entered",
        "promise-reject-callback-panic",
        "marker:promise-reject-after-reject",
    );
}
