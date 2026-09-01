//! Exact-output conformance tests for the advanced String/BigInt slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform; the `conformance-strings-bigint`
//! binary must reproduce it byte-for-byte. This slice is independent of the
//! base (`tests/conformance_fixture.rs`), host
//! (`tests/conformance_host_fixture.rs`), and buffers
//! (`tests/conformance_buffers_fixture.rs`) fixtures: each has its own
//! runner and file. Like the buffers slice there is no in-process library
//! entry point to compare (the checks live inside the binary because the
//! shared `src/checks` registries are off-limits to this slice), so only
//! the binary output is pinned here.
//!
//! The binary also has a `--bench` mode (raw measurements, documented in
//! the binary's module docs); only the default conformance mode is pinned.

#[test]
fn strings_bigint_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-strings-bigint"))
        .output()
        .expect("failed to run conformance-strings-bigint binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-strings-bigint binary reported failures; stdout:\n{stdout}"
    );
    let fixture =
        include_str!("fixtures/conformance-strings-bigint-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");
    assert_eq!(
        stdout, fixture,
        "binary output diverged from pinned strings-bigint fixture"
    );
}

#[test]
fn strings_bigint_fixture_shape_is_sane() {
    let fixture =
        include_str!("fixtures/conformance-strings-bigint-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");
    let lines: Vec<&str> = fixture.lines().collect();
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
            line.starts_with("{\"check\":\"strings/") || line.starts_with("{\"check\":\"bigint/"),
            "strings-bigint fixture must only contain strings/ or bigint/ checks: {line}"
        );
    }
    let total = check_lines.len();
    let expected_summary =
        format!("{{\"summary\":{{\"total\":{total},\"passed\":{total},\"failed\":0}}}}");
    assert_eq!(
        summary, &expected_summary,
        "summary must match the check count"
    );
    // Check-ID prefix groups appear in a fixed order: all strings/ checks
    // precede all bigint/ checks (the binary's registry order).
    let last_strings = check_lines
        .iter()
        .rposition(|l| l.starts_with("{\"check\":\"strings/"));
    let first_bigint = check_lines
        .iter()
        .position(|l| l.starts_with("{\"check\":\"bigint/"));
    match (last_strings, first_bigint) {
        (Some(s), Some(b)) => assert!(
            s < b,
            "strings/ checks must precede bigint/ checks in the fixture"
        ),
        (None, None) => panic!("fixture has no strings/ or bigint/ checks"),
        _ => {}
    }
}
