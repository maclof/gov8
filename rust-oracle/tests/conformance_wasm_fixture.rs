const FIXTURE: &str =
    include_str!("fixtures/conformance-wasm-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-wasm"))
        .output()
        .unwrap()
}

#[test]
fn wasm_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stdout)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn wasm_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 3);
    for (line, id) in lines[..2].iter().zip([
        "wasm/sync_compile_and_compiled_module",
        "wasm/memory_buffer",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[2],
        "{\"summary\":{\"total\":2,\"passed\":2,\"failed\":0}}"
    );
}
