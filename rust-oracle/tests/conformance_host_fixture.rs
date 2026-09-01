//! Exact-output conformance tests for the host-interaction slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform. Both the `conformance-host` binary and
//! the in-process `oracle::run_host_all()` run must reproduce it
//! byte-for-byte. This slice is independent of the 34-check base fixture
//! (`tests/conformance_fixture.rs`); each has its own runner and file.

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str =
    include_str!("fixtures/conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

#[test]
fn host_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-host"))
        .output()
        .expect("failed to run conformance-host binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-host binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned host fixture"
    );
}

#[test]
fn host_library_run_matches_fixture() {
    let report = oracle::run_host_all();
    assert!(
        report.all_passed(),
        "host checks failed in-process; report:\n{}",
        report.text
    );
    assert_eq!(
        report.text, FIXTURE,
        "library output diverged from pinned host fixture"
    );
}

#[test]
fn host_fixture_shape_is_sane() {
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
