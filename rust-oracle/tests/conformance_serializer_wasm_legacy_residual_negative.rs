#[test]
fn wasm_writer_callback_panic_aborts_process() {
    let output = std::process::Command::new(env!(
        "CARGO_BIN_EXE_conformance-serializer-wasm-legacy-residual"
    ))
    .arg("mode=panic-wasm-writer")
    .output()
    .expect("failed to run Wasm transfer-id panic mode");
    assert_eq!(output.status.code(), Some(-1_073_740_791)); // 0xC0000409
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("wasm transfer-id writer panic boundary"));
    assert!(stderr.contains("panic in a function that cannot unwind"));
}
