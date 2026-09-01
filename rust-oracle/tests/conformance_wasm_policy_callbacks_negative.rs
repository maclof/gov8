//! Panic boundaries for the two raw extern-C WebAssembly callbacks.

fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-wasm-policy-callbacks"))
        .arg(mode)
        .output()
        .expect("failed to run WebAssembly callback panic mode")
}

fn assert_non_unwinding_abort(output: std::process::Output, message: &str) {
    assert_eq!(output.status.code(), Some(-1_073_740_791)); // 0xC0000409
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains(message));
    assert!(stderr.contains("panic in a function that cannot unwind"));
}

#[test]
fn allow_callback_panic_aborts_process() {
    assert_non_unwinding_abort(
        run("mode=panic-allow"),
        "allow wasm callback panic boundary",
    );
}

#[test]
fn async_resolve_callback_panic_aborts_process() {
    assert_non_unwinding_abort(
        run("mode=panic-async"),
        "wasm async callback panic boundary",
    );
}
