const FIXTURE: &str = include_str!(
    "fixtures/conformance-trycatch-listener-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-trycatch-listener-residual"))
        .output()
        .expect("failed to run TryCatch/listener residual oracle")
}

#[test]
fn trycatch_listener_residual_matches_fixture() {
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
fn trycatch_listener_residual_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 5);
    for (line, id) in lines[..4].iter().zip([
        "trycatch-listener-residual/structural_nesting_identity",
        "trycatch-listener-residual/nested_configuration",
        "trycatch-listener-residual/nested_termination_recovery",
        "trycatch-listener-residual/listener_full_message_fidelity",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[4],
        "{\"summary\":{\"total\":4,\"passed\":4,\"failed\":0}}"
    );
}
