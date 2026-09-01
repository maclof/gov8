//! Exact-output conformance tests for the object-operations /
//! value-conversion slice.
//!
//! `tests/fixtures/` holds the normalized JSON-lines produced by the pinned
//! oracle on the reference platform; the `conformance-object-ops` binary
//! must reproduce it byte-for-byte. This slice is independent of the base
//! (`tests/conformance_fixture.rs`), host, buffers, snapshots, runtime-values,
//! template-advanced and core-advanced fixtures: it has its own runner and
//! file. Like the other non-base slices, the checks live inside the binary
//! because the shared `src/checks` registries are off-limits to new slices,
//! so only the binary output is pinned here.

const FIXTURE_PATH: &str =
    "tests/fixtures/conformance-object-ops-v8_152.2.0_x86_64-pc-windows-msvc.jsonl";
const FIXTURE: &str =
    include_str!("fixtures/conformance-object-ops-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

#[test]
fn object_ops_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-object-ops"))
        .output()
        .expect("failed to run conformance-object-ops binary");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "conformance-object-ops binary reported failures; stdout:\n{stdout}"
    );
    assert_eq!(
        stdout, FIXTURE,
        "binary output diverged from pinned object-ops fixture"
    );
}

#[test]
fn object_ops_fixture_shape_is_sane() {
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
            line.starts_with("{\"check\":\"obj-ops/"),
            "object-ops fixture must only contain obj-ops/ checks: {line}"
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

/// The covered areas appear exactly once each in the fixed order: prototype,
/// property (has/delete + real-named), identity (hash / creation context /
/// constructor name), receivers, lazy+instance accessors, call-as-*
/// operations and predicates, conversions, instanceof, equality/hash,
/// type representation, the missing-predicates inventory, Data, residual
/// local conversions, and the remaining predicate/helper surface.
#[test]
fn object_ops_fixture_covers_all_areas_in_order() {
    let expected_prefixes = [
        "obj-ops/proto/",
        "obj-ops/property/has_delete_family",
        "obj-ops/property/real_named_interceptor_bypass",
        "obj-ops/identity/identity_hash",
        "obj-ops/identity/creation_context",
        "obj-ops/identity/constructor_name",
        "obj-ops/receiver/get_set_with_receiver",
        "obj-ops/lazy/lazy_data_property",
        "obj-ops/lazy/instance_accessor",
        "obj-ops/call/plain_object_not_callable",
        "obj-ops/call/function_call_and_construct",
        "obj-ops/call/callable_constructor_predicates",
        "obj-ops/convert/to_object",
        "obj-ops/convert/to_boolean",
        "obj-ops/convert/to_integer",
        "obj-ops/convert/to_big_int",
        "obj-ops/convert/to_detail_string",
        "obj-ops/instanceof/api_instance_of",
        "obj-ops/equality/same_value_zero",
        "obj-ops/equality/value_hash",
        "obj-ops/typeof/type_representation",
        "obj-ops/predicates/missing_inventory",
        "obj-ops/data/predicates_and_identity",
        "obj-ops/convert/residual_locals",
        "obj-ops/predicates/module_namespace_and_type_repr",
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
