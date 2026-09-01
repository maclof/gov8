use std::process::{Command, Output};
use std::thread;
use std::time::{Duration, Instant};

const FIXTURE: &str = include_str!(
    "fixtures/conformance-inspector-runtime-events-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run_with_deadline() -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-inspector-runtime-events"))
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .expect("failed to run inspector runtime-events oracle");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child.try_wait().expect("failed to poll oracle").is_some() {
            return child.wait_with_output().expect("failed to collect oracle");
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung inspector oracle");
            let output = child.wait_with_output().expect("failed to collect oracle");
            panic!(
                "inspector runtime-events oracle exceeded deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn inspector_runtime_events_match_fixture() {
    let output = run_with_deadline();
    assert!(
        output.status.success(),
        "stdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn inspector_runtime_events_are_deterministic_and_ordered() {
    let first = run_with_deadline();
    let second = run_with_deadline();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 8);
    for (line, id) in lines[..7].iter().zip([
        "inspector-runtime-events/idle_transitions",
        "inspector-runtime-events/async_one_shot",
        "inspector-runtime-events/async_recurring_cancel",
        "inspector-runtime-events/async_all_canceled_and_null",
        "inspector-runtime-events/stack_trace_and_exception",
        "inspector-runtime-events/exception_without_stack",
        "inspector-runtime-events/unregistered_exception",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[7],
        "{\"summary\":{\"total\":7,\"passed\":7,\"failed\":0}}"
    );
}
