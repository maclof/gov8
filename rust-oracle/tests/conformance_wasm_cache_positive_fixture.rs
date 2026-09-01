use std::process::Command;

const FIXTURE: &str = include_str!(
    "fixtures/conformance-wasm-cache-positive-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> Vec<u8> {
    let output = Command::new(env!("CARGO_BIN_EXE_conformance-wasm-cache-positive"))
        .output()
        .expect("failed to execute positive Wasm cache oracle");
    assert!(
        output.status.success(),
        "stdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(output.stderr.is_empty());
    output.stdout
}

#[test]
fn wasm_cache_positive_matches_fixture() {
    assert_eq!(run(), FIXTURE.as_bytes());
}

#[test]
fn wasm_cache_positive_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert_eq!(first, second);
    assert_eq!(first, FIXTURE.as_bytes());

    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 5);
    for (line, id) in lines[..4].iter().zip([
        "wasm-cache-positive/producer/determinism",
        "wasm-cache-positive/streaming/accepted_cross_isolate",
        "wasm-cache-positive/streaming/rejection_fallback",
        "wasm-cache-positive/module_compilation/accepted",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[4],
        "{\"summary\":{\"total\":4,\"passed\":4,\"failed\":0}}"
    );
}
