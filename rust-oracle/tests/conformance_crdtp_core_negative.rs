use std::process::Command;

#[test]
fn notification_method_interior_nul_is_a_normal_rust_panic() {
    let output = Command::new(env!("CARGO_BIN_EXE_conformance-crdtp-core"))
        .arg("mode=notification-interior-nul")
        .env("RUST_BACKTRACE", "0")
        .output()
        .expect("failed to run CRDTP notification panic probe");
    assert_eq!(output.status.code(), Some(101));
    assert!(output.stdout.is_empty());
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("method name must not contain null bytes: NulError(3"),
        "unexpected stderr: {stderr}"
    );
    assert!(!stderr.contains("panic in a function that cannot unwind"));
}
