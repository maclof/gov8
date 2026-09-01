//! Exact deterministic fixture contract for Eternal and TracedReference.

const FIXTURE: &str =
    include_str!("fixtures/conformance-handles-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");
const IDS: &[&str] = &[
    "handles-residual/eternal/empty_set_clear_reuse",
    "handles-residual/eternal/object_across_scopes_gc",
    "handles-residual/eternal/cross_context_realm",
    "handles-residual/eternal/cleared_after_isolate_lifecycle",
    "handles-residual/traced/empty_reset_reuse",
    "handles-residual/traced/object_identity_mutation",
    "handles-residual/traced/cross_context_realm",
    "handles-residual/traced/externally_rooted_gc",
];

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-handles-residual"))
        .output()
        .expect("failed to run residual-handles oracle")
}

#[test]
fn handles_residual_stdout_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "oracle failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn handles_residual_is_two_process_deterministic() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
}

#[test]
fn handles_residual_fixture_has_exact_order_and_count() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 9, "eight checks plus summary");
    let observed: Vec<_> = lines[..8]
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
    assert!(lines[..8].iter().all(|line| line.contains("\"ok\":true")));
    assert_eq!(
        lines[8],
        "{\"summary\":{\"total\":8,\"passed\":8,\"failed\":0}}"
    );
}
