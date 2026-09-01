//! Process-boundary checks for advanced exception/stack APIs.
//!
//! The probe intentionally dereferences the handle returned by
//! `StackTrace::get_frame(scope, StackTrace::get_frame_count())`.  The Rust
//! binding does not reject the out-of-range index, so it must never execute
//! in this test process.

#[test]
fn frame_at_count_dereference_is_a_stable_access_violation() {
    const WINDOWS_ACCESS_VIOLATION: i32 = -1_073_741_819; // 0xC0000005
    const RUNS: usize = 6;
    const CHECKPOINTS: &[u8] = b"frame_count=2\nindex_equal_count=some\n";

    for run in 1..=RUNS {
        let output =
            std::process::Command::new(env!("CARGO_BIN_EXE_exceptions-advanced-frame-oob"))
                .output()
                .expect("failed to launch out-of-range StackFrame probe");

        assert!(
            !output.status.success(),
            "probe run {run} unexpectedly lived"
        );
        assert_eq!(
            output.status.code(),
            Some(WINDOWS_ACCESS_VIOLATION),
            "probe run {run} did not terminate with 0xC0000005; stderr: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert_eq!(
            output.stdout, CHECKPOINTS,
            "probe run {run} crossed a different boundary"
        );
    }
}
