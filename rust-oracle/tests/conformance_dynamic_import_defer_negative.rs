//! Fatal boundary when only the legacy dynamic-import callback is installed.

#[cfg(windows)]
use std::os::windows::process::CommandExt;

#[cfg(windows)]
const CREATE_NO_WINDOW: u32 = 0x0800_0000;

#[cfg(windows)]
#[test]
fn defer_import_with_only_legacy_callback_hits_v8_check() {
    for run in 0..2 {
        let output =
            std::process::Command::new(env!("CARGO_BIN_EXE_conformance-dynamic-import-defer"))
                .arg("--legacy-only-fatal")
                .creation_flags(CREATE_NO_WINDOW)
                .output()
                .expect("failed to run legacy-only import.defer subprocess");
        assert_eq!(
            output.status.code(),
            Some(0x8000_0003_u32 as i32),
            "run {run}: stdout={} stderr={}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(stderr.contains("Fatal error"), "run {run}: {stderr}");
        assert!(
            stderr.contains(
                "Check failed: (host_import_module_with_phase_dynamically_callback_) != nullptr"
            ),
            "run {run}: {stderr}"
        );
    }
}
