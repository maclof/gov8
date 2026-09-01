const FIXTURE: &str =
    include_str!("fixtures/conformance-message-locals-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-message-locals"))
        .output()
        .unwrap()
}

#[test]
fn message_locals_match_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stdout)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn message_locals_are_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 5);
    for (line, id) in lines[..4].iter().zip([
        "message-locals/message_value_matrix",
        "message-locals/message_origin_flags",
        "message-locals/stack_frame_string_getters",
        "message-locals/try_catch_mutation",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[4],
        "{\"summary\":{\"total\":4,\"passed\":4,\"failed\":0}}"
    );
}
