//! Exact-output conformance tests for the advanced template/object slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform; the `conformance-template-advanced`
//! binary must reproduce it byte-for-byte. This slice is independent of the
//! base (`tests/conformance_fixture.rs`), host
//! (`tests/conformance_host_fixture.rs`), buffers
//! (`tests/conformance_buffers_fixture.rs`), runtime-values, and snapshot
//! fixtures: each has its own runner and file. Like the buffers slice there
//! is no in-process library entry point to compare (the checks live inside
//! the binary because the shared `src/checks` registries are off-limits to
//! this slice), so only the binary output is pinned here.

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-template-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str =
    include_str!("fixtures/conformance-template-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

#[test]
fn template_advanced_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-template-advanced"))
        .output()
        .expect("failed to run conformance-template-advanced binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-template-advanced binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned template-advanced fixture"
    );
}

#[test]
fn template_advanced_fixture_shape_is_sane() {
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
            line.starts_with("{\"check\":\"tpladv/"),
            "template-advanced fixture must only contain tpladv/ checks: {line}"
        );
    }
    let total = check_lines.len();
    let expected_summary =
        format!("{{\"summary\":{{\"total\":{total},\"passed\":{total},\"failed\":0}}}}");
    assert_eq!(
        summary, &expected_summary,
        "summary must match the check count"
    );
    // Order is part of the observable contract; pin the check id sequence.
    let ids: Vec<&str> = check_lines
        .iter()
        .map(|line| {
            let rest = line.strip_prefix("{\"check\":\"").expect("checked above");
            rest.split('"').next().expect("id is non-empty")
        })
        .collect();
    let expected_ids = [
        "tpladv/named_interceptor_get_set",
        "tpladv/named_interceptor_query_delete_enum_define",
        "tpladv/indexed_interceptor_full_family",
        "tpladv/flag_interceptors",
        "tpladv/return_value_get_and_specials",
        "tpladv/signature_receiver_enforcement",
        "tpladv/intrinsic_data_property",
        "tpladv/constructor_behavior_and_prototype",
        "tpladv/inheritance_chain",
        "tpladv/accessor_property_shapes",
        "tpladv/internal_field_boundaries",
        "tpladv/security_token_contexts",
        "tpladv/call_as_function_handler",
        "tpladv/immutable_proto",
    ];
    assert_eq!(
        ids, expected_ids,
        "check order is part of the pinned contract"
    );
    let _ = FIXTURE_PATH; // keeps the path visible next to the fixture for regeneration
}
