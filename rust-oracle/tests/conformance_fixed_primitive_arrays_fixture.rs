//! Exact-output and two-process determinism checks for the pinned FixedArray
//! and PrimitiveArray oracle slice.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-fixed-primitive-arrays-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> String {
    let output =
        std::process::Command::new(env!("CARGO_BIN_EXE_conformance-fixed-primitive-arrays"))
            .output()
            .expect("failed to run conformance-fixed-primitive-arrays");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "FixedArray/PrimitiveArray oracle reported failures; stdout:\n{stdout}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stderr)
    );
    stdout
}

#[test]
fn fixed_primitive_arrays_binary_matches_fixture_twice() {
    let first = run();
    let second = run();
    assert_eq!(
        first, second,
        "separate oracle processes must be deterministic"
    );
    assert_eq!(first, FIXTURE, "stdout diverged from the pinned fixture");
}

#[test]
fn fixed_primitive_arrays_fixture_ids_are_exact_and_ordered() {
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
            "fixed-primitive-arrays/primitive_empty_and_defaults",
            "fixed-primitive-arrays/primitive_supported_kinds",
            "fixed-primitive-arrays/primitive_overwrite_and_context_independence",
            "fixed-primitive-arrays/primitive_length_conversion",
            "fixed-primitive-arrays/fixed_empty_and_safe_bounds",
            "fixed-primitive-arrays/fixed_data_kinds",
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
