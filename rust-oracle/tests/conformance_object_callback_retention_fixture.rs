const FIXTURE: &str = include_str!(
    "fixtures/conformance-object-callback-retention-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-object-callback-retention"))
        .output()
        .unwrap()
}

#[test]
fn object_callback_retention_matches_fixture() {
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
fn object_callback_retention_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 7);
    for (line, id) in lines[..6].iter().zip([
        "object-callback-retention/accessor_configuration",
        "object-callback-retention/accessor_replacement_read_only",
        "object-callback-retention/lazy_data_attributes",
        "object-callback-retention/lazy_side_effect_matrix",
        "object-callback-retention/lazy_throw_empty_failure",
        "object-callback-retention/template_set_with_attr",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[6],
        "{\"summary\":{\"total\":6,\"passed\":6,\"failed\":0}}"
    );
}
