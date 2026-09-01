//! Exact-output conformance tests for the serializer/deserializer delegate
//! slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform; the `conformance-serializer-delegates`
//! binary must reproduce it byte-for-byte. Like the buffers slice, the
//! checks live inside the binary (the shared `src/checks` registries are
//! off-limits to this slice), so only the binary output is pinned here.
//! The panic/fatal delegate boundaries characterized out-of-process live in
//! `tests/serializer_delegates_negative.rs`.

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-serializer-delegates-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str = include_str!(
    "fixtures/conformance-serializer-delegates-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

#[test]
fn serdel_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-serializer-delegates"))
        .output()
        .expect("failed to run conformance-serializer-delegates binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-serializer-delegates binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned serializer-delegates fixture"
    );
}

#[test]
fn serdel_fixture_shape_is_sane() {
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
        assert!(
            line.starts_with("{\"check\":\"serdel/"),
            "bad check line: {line}"
        );
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
