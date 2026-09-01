const FIXTURE: &str = include_str!(
    "fixtures/conformance-exception-string-local-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-exception-string-local"))
        .output()
        .unwrap()
}

#[test]
fn exception_string_local_matches_fixture() {
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
fn exception_string_local_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 3);
    for (line, id) in lines[..2].iter().zip([
        "exception-string-local/five_constructors_by_string_kind",
        "exception-string-local/input_and_message_identity",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[2],
        "{\"summary\":{\"total\":2,\"passed\":2,\"failed\":0}}"
    );
}
