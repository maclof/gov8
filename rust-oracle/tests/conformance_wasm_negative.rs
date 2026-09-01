fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-wasm-negative"))
        .arg(mode)
        .output()
        .unwrap()
}

#[test]
fn cache_mark_after_bytes_is_fatal() {
    for mode in [
        "streaming-cache-mark-after-bytes",
        "compilation-cache-mark-after-bytes",
    ] {
        let output = run(mode);
        assert!(!output.status.success(), "{mode} unexpectedly succeeded");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.contains("SetHasCompiledModuleBytes has to be called before OnBytesReceived"),
            "{mode}: {stderr}"
        );
    }
}
