use std::process::{Command, Output};
use std::thread;
use std::time::{Duration, Instant};

const FIXTURE: &str = include_str!(
    "fixtures/conformance-inspector-session-controls-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run_with_deadline() -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-inspector-session-controls"))
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .expect("failed to run inspector session-controls oracle");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child.try_wait().expect("failed to poll oracle").is_some() {
            return child
                .wait_with_output()
                .expect("failed to collect oracle output");
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung inspector oracle");
            let output = child
                .wait_with_output()
                .expect("failed to collect hung oracle");
            panic!(
                "inspector oracle exceeded 15-second deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn inspector_session_controls_match_fixture() {
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
fn inspector_session_controls_are_deterministic_and_ordered() {
    let first = run_with_deadline();
    let second = run_with_deadline();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 6);
    for (line, id) in lines[..5].iter().zip([
        "inspector-session-controls/can_dispatch_method",
        "inspector-session-controls/release_object_group",
        "inspector-session-controls/schedule_then_cancel",
        "inspector-session-controls/scheduled_pause_notifications",
        "inspector-session-controls/drop_session_cancels_scheduled_pause",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[5],
        "{\"summary\":{\"total\":5,\"passed\":5,\"failed\":0}}"
    );
}
