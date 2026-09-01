use std::process::{Command, Output, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const FIXTURE: &str =
    include_str!("fixtures/conformance-crdtp-core-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run_with_deadline() -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-crdtp-core"))
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to run CRDTP core oracle");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child.try_wait().expect("failed to poll oracle").is_some() {
            return child.wait_with_output().expect("failed to collect oracle");
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung CRDTP oracle");
            let output = child.wait_with_output().expect("failed to collect oracle");
            panic!(
                "CRDTP core oracle exceeded deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn crdtp_core_matches_fixture() {
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
fn crdtp_core_is_deterministic_and_ordered() {
    let first = run_with_deadline();
    let second = run_with_deadline();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 8);
    for (line, id) in lines[..7].iter().zip([
        "crdtp-core/conversion_success",
        "crdtp-core/conversion_failures",
        "crdtp-core/dispatchable_valid_owned",
        "crdtp-core/dispatchable_optional_fields",
        "crdtp-core/dispatchable_invalid",
        "crdtp-core/dispatch_responses",
        "crdtp-core/serializable_helpers",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[7],
        "{\"summary\":{\"total\":7,\"passed\":7,\"failed\":0}}"
    );
}
