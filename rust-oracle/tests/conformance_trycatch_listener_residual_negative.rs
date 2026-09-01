#[test]
fn listener_callback_panic_aborts_process() {
    let output =
        std::process::Command::new(env!("CARGO_BIN_EXE_conformance-trycatch-listener-residual"))
            .arg("mode=panic-listener")
            .output()
            .expect("failed to run message-listener panic mode");
    assert_eq!(output.status.code(), Some(-1_073_740_791)); // 0xC0000409
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("message listener panic boundary"));
    assert!(stderr.contains("panic in a function that cannot unwind"));
}
