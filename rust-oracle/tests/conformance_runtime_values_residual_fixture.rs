const FIXTURE: &str = include_str!(
    "fixtures/conformance-runtime-values-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-runtime-values-residual"))
        .output()
        .unwrap()
}

#[test]
fn runtime_values_residual_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "stdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn runtime_values_residual_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 3);
    assert!(lines[0]
        .starts_with("{\"check\":\"runtime-values-residual/symbol/all_well_known\",\"ok\":true"));
    assert!(lines[1].starts_with(
        "{\"check\":\"runtime-values-residual/private/for_api_some_names\",\"ok\":true"
    ));
    assert_eq!(
        lines[2],
        "{\"summary\":{\"total\":2,\"passed\":2,\"failed\":0}}"
    );
}
