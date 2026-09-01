//! Exact-output conformance tests for the typed-array / ArrayBufferView
//! slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform; the `conformance-typed-arrays` binary
//! must reproduce it byte-for-byte. This slice is independent of the base
//! (`tests/conformance_fixture.rs`), host (`tests/conformance_host_fixture.rs`)
//! and buffers (`tests/conformance_buffers_fixture.rs`) fixtures: each has
//! its own runner and file. Like the buffers slice there is no in-process
//! library entry point to compare (the checks live inside the binary because
//! the shared `src/checks` registries are off-limits to this slice), so only
//! the binary output is pinned here.
//!
//! Regenerate after a deliberate contract change (see the binary's module
//! docs): run the binary under `cmd /c "... > <fixture path>"` (byte-exact
//! redirection; PowerShell `>` writes UTF-16).

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-typed-arrays-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str =
    include_str!("fixtures/conformance-typed-arrays-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

#[test]
fn typed_arrays_binary_stdout_matches_fixture() {
    assert!(
        !FIXTURE_PATH.is_empty(),
        "fixture path must be recorded next to the fixture file"
    );
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-typed-arrays"))
        .output()
        .expect("failed to run conformance-typed-arrays binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-typed-arrays binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned typed-arrays fixture"
    );
}

#[test]
fn typed_arrays_fixture_shape_is_sane() {
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
            line.starts_with("{\"check\":\"typedarrays/"),
            "typed-arrays fixture must only contain typedarrays/ checks: {line}"
        );
    }
    let total = check_lines.len();
    let expected_summary =
        format!("{{\"summary\":{{\"total\":{total},\"passed\":{total},\"failed\":0}}}}");
    assert_eq!(
        summary, &expected_summary,
        "summary must match the check count"
    );
}

/// Every typed-array kind the crate exposes must appear as a JSON key in
/// the fixture's per-kind rows; this keeps the fixture from silently
/// dropping a kind (e.g. if `Float16Array` were ever removed upstream).
#[test]
fn typed_arrays_fixture_covers_all_twelve_kinds() {
    for key in [
        "int8",
        "uint8",
        "uint8_clamped",
        "int16",
        "uint16",
        "int32",
        "uint32",
        "float16",
        "float32",
        "float64",
        "bigint64",
        "biguint64",
    ] {
        let needle = format!("\"{key}\":{{");
        let hits = FIXTURE.matches(&needle).count();
        assert!(
            hits >= 3,
            "kind {key} under-covered in fixture ({hits} object rows)"
        );
    }
}
