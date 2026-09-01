//! Exact-output verification for the classic source-text ES module slice.

const FIXTURE: &str =
    include_str!("fixtures/conformance-modules-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

#[test]
fn modules_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-modules"))
        .output()
        .expect("failed to run conformance-modules binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-modules reported failures; stdout:\n{stdout}"
    );
    assert_eq!(stdout, FIXTURE, "module fixture diverged");
}

#[test]
fn modules_fixture_shape_and_coverage() {
    let lines: Vec<&str> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 8, "seven checks plus one summary");
    let checks = &lines[..7];
    let expected_ids = [
        "modules/compile_requests",
        "modules/link_evaluate_namespace",
        "modules/syntax_failure",
        "modules/negative_origin_offsets",
        "modules/repeated_evaluate",
        "modules/link_failure",
        "modules/evaluation_rejection",
    ];
    for (line, id) in checks.iter().zip(expected_ids) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[7],
        "{\"summary\":{\"total\":7,\"passed\":7,\"failed\":0}}"
    );
}

#[test]
fn modules_fixture_is_deterministic_across_processes() {
    let first = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-modules"))
        .output()
        .unwrap();
    let second = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-modules"))
        .output()
        .unwrap();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
}
