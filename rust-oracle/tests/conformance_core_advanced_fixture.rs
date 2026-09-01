//! Exact-output conformance tests for the core-advanced slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform; the `conformance-core-advanced` binary
//! must reproduce it byte-for-byte. This slice is independent of the base
//! (`tests/conformance_fixture.rs`), host (`tests/conformance_host_fixture.rs`),
//! buffers (`tests/conformance_buffers_fixture.rs`), snapshots and
//! runtime-values fixtures: it has its own runner and file. Like the buffers
//! slice, the checks live inside the binary because the shared
//! `src/checks` registries are off-limits to new slices, so only the binary
//! output is pinned here.

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-core-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str =
    include_str!("fixtures/conformance-core-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

#[test]
fn core_advanced_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-core-advanced"))
        .output()
        .expect("failed to run conformance-core-advanced binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-core-advanced binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned core-advanced fixture"
    );
}

#[test]
fn core_advanced_fixture_shape_is_sane() {
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
            line.starts_with("{\"check\":\"core-advanced/"),
            "core-advanced fixture must only contain core-advanced/ checks: {line}"
        );
    }
    let total = check_lines.len();
    let expected_summary =
        format!("{{\"summary\":{{\"total\":{total},\"passed\":{total},\"failed\":0}}}}");
    assert_eq!(
        summary, &expected_summary,
        "summary must match the check count"
    );
    // Every check id is unique: the fixture is a stable, ordered contract.
    let mut ids: Vec<&str> = check_lines
        .iter()
        .filter_map(|line| {
            let start = line.find("\"check\":\"")? + "\"check\":\"".len();
            let end = line[start..].find('"')? + start;
            Some(&line[start..end])
        })
        .collect();
    let unique = ids.len();
    ids.sort_unstable();
    ids.dedup();
    assert_eq!(ids.len(), unique, "duplicate check ids in fixture");
    let _ = FIXTURE_PATH; // keeps the path visible next to the fixture for regeneration
}

/// The covered areas appear exactly once each in the fixed order: scopes,
/// threads (Locker/IsolateHandle), contexts, slots, scripts (origins /
/// compiler / unbound / code cache), messages (exception details / frames /
/// uncaught capture), termination and heap.
#[test]
fn core_advanced_fixture_covers_all_areas_in_order() {
    let expected_prefixes = [
        "core-advanced/scope/",
        "core-advanced/thread/",
        "core-advanced/context/",
        "core-advanced/slots/",
        "core-advanced/script/",
        "core-advanced/message/",
        "core-advanced/terminate/",
        "core-advanced/heap/",
    ];
    let positions: Vec<Option<usize>> = expected_prefixes
        .iter()
        .map(|prefix| FIXTURE.lines().position(|line| line.contains(prefix)))
        .collect();
    let all_present = positions.iter().all(|p| p.is_some());
    assert!(all_present, "every area must be covered: {positions:?}");
    let mut order: Vec<usize> = positions.into_iter().flatten().collect();
    let sorted = order.clone();
    order.sort_unstable();
    assert_eq!(
        order, sorted,
        "areas must appear in the documented group order"
    );
}
