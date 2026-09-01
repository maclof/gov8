const FIXTURE: &str = include_str!(
    "fixtures/conformance-external-references-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-external-references"))
        .output()
        .unwrap()
}

#[test]
fn external_references_match_fixture() {
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
fn external_references_are_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 4);
    for (line, id) in lines[..3].iter().zip([
        "external-references/value_semantics",
        "external-references/empty_table",
        "external-references/snapshot_remap_and_reuse",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[3],
        "{\"summary\":{\"total\":3,\"passed\":3,\"failed\":0}}"
    );
}
