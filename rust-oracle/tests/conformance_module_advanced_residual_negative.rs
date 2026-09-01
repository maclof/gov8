//! Subprocess-only callback panic boundary.

#[cfg(windows)]
#[test]
fn resolve_source_callback_panic_aborts_without_unwinding_through_v8() {
    let output =
        std::process::Command::new(env!("CARGO_BIN_EXE_conformance-module-advanced-residual"))
            .arg("--panic-resolve-source")
            .output()
            .expect("failed to run resolve-source panic subprocess");
    assert_eq!(output.status.code(), Some(-1_073_740_791)); // 0xC0000409
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("resolve source panic boundary"));
    assert!(stderr.contains("panic in a function that cannot unwind"));
}
