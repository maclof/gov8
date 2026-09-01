//! Subprocess-only allocation-refusal/OOM boundary.

#[cfg(windows)]
use std::os::windows::process::CommandExt;

#[cfg(windows)]
const CREATE_NO_WINDOW: u32 = 0x0800_0000;

#[cfg(windows)]
#[test]
fn null_allocation_result_is_a_fatal_oom() {
    for run in 0..2 {
        let output =
            std::process::Command::new(env!("CARGO_BIN_EXE_conformance-array-buffer-allocator"))
                .arg("--refused-allocation")
                .creation_flags(CREATE_NO_WINDOW)
                .output()
                .expect("failed to run refused-allocation subprocess");
        assert_eq!(
            output.status.code(),
            Some(0x8000_0003_u32 as i32),
            "run {run}: stdout={} stderr={}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.contains("Fatal process out of memory: v8::ArrayBuffer::New"),
            "run {run}: {stderr}"
        );
    }
}
