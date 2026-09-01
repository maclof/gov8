//! Exact-output conformance tests for the built-in runtime values slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform; the `conformance-runtime-values`
//! binary must reproduce it byte-for-byte. This slice is independent of the
//! base (`tests/conformance_fixture.rs`), host
//! (`tests/conformance_host_fixture.rs`), snapshots
//! (`tests/conformance_snapshots_fixture.rs`), and buffers
//! (`tests/conformance_buffers_fixture.rs`) fixtures: each has its own
//! runner and file. Like the buffers slice there is no in-process library
//! entry point to compare (the checks live inside the binary because the
//! shared `src/checks` registries are off-limits to this slice), so only
//! the binary output is pinned here.

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-runtime-values-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str =
    include_str!("fixtures/conformance-runtime-values-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

#[test]
fn runtime_values_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-runtime-values"))
        .output()
        .expect("failed to run conformance-runtime-values binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-runtime-values binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned runtime-values fixture"
    );
}

#[test]
fn runtime_values_fixture_shape_is_sane() {
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
            line.starts_with("{\"check\":\"runtime-values/"),
            "runtime-values fixture must only contain runtime-values/ checks: {line}"
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
