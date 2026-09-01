use std::process::{Command, Output, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const FIXTURE: &str =
    include_str!("fixtures/conformance-cppgc-member-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run_with_deadline() -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-cppgc-member"))
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to execute cppgc Member oracle");
    let deadline = Instant::now() + Duration::from_secs(20);
    loop {
        if child.try_wait().expect("failed to poll oracle").is_some() {
            return child.wait_with_output().expect("failed to collect oracle");
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung oracle");
            let output = child.wait_with_output().expect("failed to collect oracle");
            panic!(
                "cppgc Member oracle exceeded deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn cppgc_member_matches_fixture() {
    let output = run_with_deadline();
    assert!(
        output.status.success(),
        "stdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(output.stderr.is_empty());
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn cppgc_member_is_deterministic_and_ordered() {
    let first = run_with_deadline();
    let second = run_with_deadline();
    assert!(first.status.success() && second.status.success());
    assert!(first.stderr.is_empty() && second.stderr.is_empty());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());

    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 6);
    for (line, id) in lines[..5].iter().zip([
        "cppgc-member/handles/empty_new_set_get",
        "cppgc-member/strong/reassign_clear_reuse",
        "cppgc-member/weak/clearing_and_reuse",
        "cppgc-member/cycle/unreachable_strong_cycle",
        "cppgc-member/lifecycle/owner_isolate_teardown",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[5],
        "{\"summary\":{\"total\":5,\"passed\":5,\"failed\":0}}"
    );
}
