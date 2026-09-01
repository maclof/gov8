//! Fatal `CreateParams`/snapshot inputs isolated in child processes.

fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-create-params-snapshot"))
        .arg(mode)
        .output()
        .expect("failed to run CreateParams snapshot negative mode")
}

#[test]
fn inverted_heap_limits_are_fatal_at_builder_call() {
    let output = run("mode=inconsistent-heap-limits");
    assert_eq!(output.status.code(), Some(-2_147_483_645)); // 0x80000003
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("initial_heap_size_in_bytes <= maximum_heap_size_in_bytes"));
    assert!(stderr.contains("CreateParams::heap_limits"));
}

#[test]
fn empty_snapshot_is_fatal_during_isolate_creation() {
    let output = run("mode=invalid-snapshot");
    assert_eq!(output.status.code(), Some(-2_147_483_645)); // 0x80000003
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Failed to deserialize the V8 snapshot blob"));
    assert!(stderr.contains("corrupted or missing"));
}
