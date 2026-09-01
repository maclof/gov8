use std::process::Command;

const FIXTURE: &str =
    include_str!("fixtures/conformance-heap-snapshot-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> Vec<u8> {
    let output = Command::new(env!("CARGO_BIN_EXE_conformance-heap-snapshot"))
        .output()
        .expect("failed to execute heap-snapshot oracle");
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
fn heap_snapshot_matches_fixture() {
    assert_eq!(run(), FIXTURE.as_bytes());
}

#[test]
fn heap_snapshot_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert_eq!(first, second);
    assert_eq!(first, FIXTURE.as_bytes());
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 4);
    for (line, id) in lines[..3].iter().zip([
        "heap-snapshot/stream/success",
        "heap-snapshot/stream/callback_abort",
        "heap-snapshot/lifecycle/repeat_after_abort",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[3],
        "{\"summary\":{\"total\":3,\"passed\":3,\"failed\":0}}"
    );
}
