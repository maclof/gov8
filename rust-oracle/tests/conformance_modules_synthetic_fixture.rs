const FIXTURE: &str =
    include_str!("fixtures/conformance-modules-synthetic-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-modules-synthetic"))
        .output()
        .unwrap()
}

#[test]
fn synthetic_modules_match_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stdout)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn synthetic_modules_are_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 4);
    for (line, id) in lines[..3].iter().zip([
        "modules-synthetic/creation_and_pre_set_exports",
        "modules-synthetic/evaluation_sets_and_invalid_export",
        "modules-synthetic/thrown_evaluation",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[3],
        "{\"summary\":{\"total\":3,\"passed\":3,\"failed\":0}}"
    );
}
