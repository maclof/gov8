//! Exact-output and two-process determinism tests for the advanced Function
//! oracle slice.

const FIXTURE: &str =
    include_str!("fixtures/conformance-functions-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> String {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-functions-advanced"))
        .output()
        .expect("failed to run conformance-functions-advanced");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "advanced Function oracle reported failures; stdout:\n{stdout}"
    );
    stdout
}

#[test]
fn functions_advanced_binary_matches_fixture_twice() {
    let first = run();
    let second = run();
    assert_eq!(
        first, second,
        "separate oracle processes must be deterministic"
    );
    assert_eq!(first, FIXTURE, "stdout diverged from the pinned fixture");
}

#[test]
fn functions_advanced_fixture_ids_are_exact_and_ordered() {
    let ids: Vec<&str> = FIXTURE
        .lines()
        .filter_map(|line| {
            let start = line.find("\"check\":\"")? + "\"check\":\"".len();
            let end = line[start..].find('"')? + start;
            Some(&line[start..end])
        })
        .collect();
    assert_eq!(
        ids,
        [
            "functions-advanced/names_and_bound",
            "functions-advanced/direct_builder_constructor_behavior",
            "functions-advanced/script_metadata",
            "functions-advanced/bound_construct_semantics",
            "functions-advanced/side_effect_policies",
            "functions-advanced/code_cache_roundtrip",
        ]
    );
    assert_eq!(
        FIXTURE.lines().last(),
        Some("{\"summary\":{\"total\":6,\"passed\":6,\"failed\":0}}")
    );
    assert!(
        FIXTURE
            .lines()
            .take(6)
            .all(|line| line.contains("\"ok\":true")),
        "fixture must pin only passing checks"
    );
}
