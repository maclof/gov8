use std::process::Command;

const FIXTURE: &str =
    include_str!("fixtures/conformance-template-name-keys-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> Vec<u8> {
    let output = Command::new(env!("CARGO_BIN_EXE_conformance-template-name-keys"))
        .output()
        .expect("failed to execute template Name-key oracle");
    assert!(
        output.status.success(),
        "stdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(output.stderr.is_empty());
    output.stdout
}

#[test]
fn template_name_keys_match_fixture() {
    assert_eq!(run(), FIXTURE.as_bytes());
}

#[test]
fn template_name_keys_are_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert_eq!(first, second);
    assert_eq!(first, FIXTURE.as_bytes());

    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 6);
    for (line, id) in lines[..5].iter().zip([
        "template-name-keys/object/string_symbol_attributes",
        "template-name-keys/function/string_symbol",
        "template-name-keys/symbol/distinct_same_description",
        "template-name-keys/intrinsic/symbol",
        "template-name-keys/lifecycle/retained_and_late_mutation",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[5],
        "{\"summary\":{\"total\":5,\"passed\":5,\"failed\":0}}"
    );
}
