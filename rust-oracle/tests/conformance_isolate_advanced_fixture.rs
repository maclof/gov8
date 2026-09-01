//! Exact deterministic fixture contract for the isolate-advanced oracle.

const FIXTURE: &str =
    include_str!("fixtures/conformance-isolate-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

const IDS: &[&str] = &[
    "isolate-advanced/create-params/constraint_getters",
    "isolate-advanced/create-params/derived_heap_limits",
    "isolate-advanced/create-params/allocator_external_references",
    "isolate-advanced/create-params/allow_atomics_wait",
    "isolate-advanced/create-params/counter_lookup_callback",
    "isolate-advanced/statistics/heap_invariants",
    "isolate-advanced/statistics/heap_spaces",
    "isolate-advanced/statistics/code_metadata",
    "isolate-advanced/isolate/notifications_profiler_controls",
];

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-isolate-advanced"))
        .output()
        .expect("failed to run conformance-isolate-advanced")
}

#[test]
fn isolate_advanced_stdout_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "oracle failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        String::from_utf8(output.stdout).expect("UTF-8 stdout"),
        FIXTURE
    );
}

#[test]
fn isolate_advanced_is_deterministic_across_processes() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(
        first.stdout, second.stdout,
        "two successful oracle runs diverged"
    );
    assert_eq!(first.stdout, FIXTURE.as_bytes());
}

#[test]
fn isolate_advanced_fixture_has_exact_order_and_count() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 10, "nine checks plus summary");
    let observed: Vec<_> = lines[..9]
        .iter()
        .map(|line| {
            line.split_once("\"check\":\"")
                .unwrap()
                .1
                .split_once('"')
                .unwrap()
                .0
        })
        .collect();
    assert_eq!(observed, IDS);
    assert!(lines[..9].iter().all(|line| line.contains("\"ok\":true")));
    assert_eq!(
        lines[9],
        "{\"summary\":{\"total\":9,\"passed\":9,\"failed\":0}}"
    );
}
