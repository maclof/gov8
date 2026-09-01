//! Exact-output conformance tests.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform. Both the binary and the in-process
//! library run must reproduce it byte-for-byte. When the pinned `v8` crate or
//! platform changes, the fixture must be regenerated deliberately and
//! reviewed (that diff is the behavioral contract).

const FIXTURE_PATH: &str = "tests/fixtures/conformance-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str = include_str!("fixtures/conformance-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

#[test]
fn binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance"))
        .output()
        .expect("failed to run conformance binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned fixture"
    );
}

#[test]
fn library_run_all_matches_fixture() {
    let report = oracle::run_all();
    assert!(
        report.all_passed(),
        "checks failed in-process; report:\n{}",
        report.text
    );
    assert_eq!(
        report.text, FIXTURE,
        "library output diverged from pinned fixture"
    );
}

#[test]
fn fixture_shape_is_sane() {
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
