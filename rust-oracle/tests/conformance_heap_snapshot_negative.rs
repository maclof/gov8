use std::process::Command;

#[test]
fn callback_panic_aborts_at_native_boundary() {
    let output = Command::new(env!("CARGO_BIN_EXE_conformance-heap-snapshot"))
        .arg("--panic")
        .output()
        .expect("failed to execute heap-snapshot panic probe");
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        !output.status.success(),
        "panic probe unexpectedly succeeded"
    );
    assert!(
        stderr.contains("marker:before-heap-snapshot-callback-panic"),
        "{stderr}"
    );
    assert!(
        stderr.contains("heap snapshot callback panic sentinel"),
        "{stderr}"
    );
    assert!(
        !stderr.contains("marker:after-heap-snapshot-callback-panic"),
        "{stderr}"
    );
}
