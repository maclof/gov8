//! Exact-output conformance tests for the snapshot/handle/termination
//! slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform. The `conformance-snapshots` binary must
//! reproduce it byte-for-byte. This slice is independent of the 34-check
//! base fixture (`tests/conformance_fixture.rs`) and the 18-check host
//! fixture (`tests/conformance_host_fixture.rs`); each has its own runner
//! and file.
//!
//! Unlike the other two slices this registry lives entirely in its binary
//! (the `oracle` library exposes only the shared `report`/`json` encoding),
//! so there is no in-process double run here — determinism across processes
//! is pinned explicitly by `binary_output_is_deterministic_across_runs`.

use std::process::Command;

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-snapshots-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str =
    include_str!("fixtures/conformance-snapshots-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run_binary() -> String {
    let output = Command::new(env!("CARGO_BIN_EXE_conformance-snapshots"))
        .output()
        .expect("failed to run conformance-snapshots binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-snapshots binary reported failures; stdout:\n{stdout}"
    );
    stdout
}

#[test]
fn snapshots_binary_stdout_matches_fixture() {
    let stdout = run_binary();
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned snapshots fixture"
    );
}

#[test]
fn snapshots_binary_output_is_deterministic_across_runs() {
    let first = run_binary();
    let second = run_binary();
    assert_eq!(first, second, "two runs produced different reports");
}

#[test]
fn snapshots_fixture_shape_is_sane() {
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
    // Slice-family prefixes must stay disjoint from the other two fixtures.
    let known_prefixes = ["snapshot/", "handle/", "terminate/"];
    for line in check_lines {
        let id = line
            .split("\"check\":\"")
            .nth(1)
            .and_then(|rest| rest.split('"').next())
            .expect("check id");
        assert!(
            known_prefixes.iter().any(|p| id.starts_with(p)),
            "unexpected check id {id}; prefixes are part of the contract"
        );
    }
    let _ = FIXTURE_PATH; // keeps the path visible next to the fixture for regeneration
}
