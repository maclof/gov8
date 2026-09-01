//! Exact fixture checks for unified `CreateParams` snapshot combinations.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-create-params-snapshot-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-create-params-snapshot"))
        .output()
        .expect("failed to run CreateParams snapshot oracle")
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
        "independent_allocator_lifetime",
        "atomics_wait_combination",
        "all_safe_parameters",
        "constraint_builder_boundaries",
        "cloned_blob_parameter_reuse",
    ];
    assert_eq!(lines.len(), ids.len() + 1);
    for (line, id) in lines.iter().zip(ids) {
        assert!(line.starts_with(&format!(
            "{{\"check\":\"create-params-snapshot/{id}\",\"ok\":true"
        )));
    }
    assert_eq!(
        lines[ids.len()],
        "{\"summary\":{\"total\":5,\"passed\":5,\"failed\":0}}"
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
