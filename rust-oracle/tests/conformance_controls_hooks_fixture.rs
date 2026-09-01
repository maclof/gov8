//! Exact-output conformance tests for the process/isolate controls & hooks
//! slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform. The `conformance-controls-hooks` binary
//! must reproduce it byte-for-byte, and must do so identically on repeated
//! runs (the slice records engine constants — entropy-seeded Math.random,
//! heap-limit accounting, fatal-handler payloads — so any nondeterminism is
//! a characterization bug). This slice is independent of the other fixture
//! slices; each has its own runner and file.

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-controls-hooks-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str =
    include_str!("fixtures/conformance-controls-hooks-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run_binary() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-controls-hooks"))
        .output()
        .expect("failed to run conformance-controls-hooks binary")
}

#[test]
fn controls_hooks_binary_stdout_matches_fixture() {
    let output = run_binary();
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-controls-hooks binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned controls-hooks fixture"
    );
}

#[test]
fn controls_hooks_binary_is_deterministic_across_runs() {
    let first = run_binary();
    let second = run_binary();
    assert!(
        first.status.success() && second.status.success(),
        "both runs must succeed"
    );
    assert_eq!(
        first.stdout, second.stdout,
        "two runs of the same binary produced different normalized output"
    );
}

#[test]
fn controls_hooks_fixture_shape_is_sane() {
    let lines: Vec<&str> = FIXTURE.lines().collect();
    assert!(
        lines.len() >= 3,
        "fixture must contain checks and a summary"
    );
    let summary = lines.last().expect("summary line");
    assert!(
        summary.starts_with("{\"summary\":{"),
        "last line must be the summary: {summary}"
    );
    let check_lines = &lines[..lines.len() - 1];
    for line in check_lines {
        assert!(line.starts_with("{\"check\":\""), "bad check line: {line}");
        assert!(
            line.contains("\"ok\":true"),
            "fixture must only record passing checks: {line}"
        );
        assert!(
            line.starts_with("{\"check\":\"controls/"),
            "every check id must live in the controls/ namespace: {line}"
        );
    }
    let total = check_lines.len();
    let expected_summary =
        format!("{{\"summary\":{{\"total\":{total},\"passed\":{total},\"failed\":0}}}}");
    assert_eq!(
        summary, &expected_summary,
        "summary must match the check count"
    );
    let _ = FIXTURE_PATH; // keeps the path visible next to the fixture for regeneration
}
