//! Exact fixture checks for isolate-level WebAssembly policy callbacks.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-wasm-policy-callbacks-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-wasm-policy-callbacks"))
        .output()
        .expect("failed to run WebAssembly policy callback oracle")
}

#[test]
fn stdout_matches_fixture() {
    let output = run();
    assert!(output.status.success());
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn fixture_order_and_coverage() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    let ids = [
        "sync_allow_deny_exception",
        "async_success_failure_settlement",
    ];
    assert_eq!(lines.len(), ids.len() + 1);
    for (line, id) in lines.iter().zip(ids) {
        assert!(line.starts_with(&format!(
            "{{\"check\":\"wasm-policy-callbacks/{id}\",\"ok\":true"
        )));
    }
    assert_eq!(
        lines[ids.len()],
        "{\"summary\":{\"total\":2,\"passed\":2,\"failed\":0}}"
    );
}

#[test]
fn deterministic_across_processes() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
}
