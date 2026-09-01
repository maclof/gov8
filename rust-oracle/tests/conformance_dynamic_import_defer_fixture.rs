//! Exact-output checks for dynamic `import.defer()`.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-dynamic-import-defer-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-dynamic-import-defer"))
        .output()
        .expect("failed to run dynamic import.defer oracle")
}

#[test]
fn stdout_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "stderr: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn fixture_shape_and_order() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    let ids = [
        "pin",
        "phase_payload_and_lazy_evaluation",
        "rejected_callback_promise",
        "invalid_attributes_before_callback",
        "repeated_delayed_settlement",
    ];
    assert_eq!(lines.len(), ids.len() + 1);
    for (line, id) in lines.iter().zip(ids) {
        assert!(line.starts_with(&format!(
            "{{\"check\":\"dynamic-import-defer/{id}\",\"ok\":true"
        )));
    }
    assert_eq!(
        lines[ids.len()],
        "{\"summary\":{\"total\":5,\"passed\":5,\"failed\":0}}"
    );
}

#[test]
fn fixture_is_deterministic_across_processes() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
}
