//! Exact fixture contract for residual exception constructors/messages.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-exception-constructors-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);
const IDS: &[&str] = &[
    "exception-constructors/constructors/five_native_error_kinds",
    "exception-constructors/constructors/message_boundaries",
    "exception-constructors/create-message/primitive_values",
    "exception-constructors/create-message/native_error_details",
    "exception-constructors/create-message/scripted_error_reconstruction",
    "exception-constructors/create-message/current_stack_fallback",
    "exception-constructors/lifecycle/cross_context_global",
];

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-exception-constructors"))
        .output()
        .expect("failed to run exception-constructor oracle")
}

#[test]
fn exception_constructor_stdout_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "oracle failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn exception_constructor_output_is_two_process_deterministic() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
}

#[test]
fn exception_constructor_fixture_has_exact_order_and_count() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 8, "seven checks plus summary");
    let observed: Vec<_> = lines[..7]
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
    assert!(lines[..7].iter().all(|line| line.contains("\"ok\":true")));
    assert_eq!(
        lines[7],
        "{\"summary\":{\"total\":7,\"passed\":7,\"failed\":0}}"
    );
}
