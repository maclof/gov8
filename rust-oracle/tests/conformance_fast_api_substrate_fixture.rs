use std::process::{Command, Output};
use std::thread;
use std::time::{Duration, Instant};

const FIXTURE: &str =
    include_str!("fixtures/conformance-fast-api-substrate-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run_with_deadline() -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-fast-api-substrate"))
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .expect("failed to run Fast API substrate oracle");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child.try_wait().expect("failed to poll oracle").is_some() {
            return child.wait_with_output().expect("failed to collect output");
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung Fast API oracle");
            let output = child.wait_with_output().expect("failed to collect timeout");
            panic!(
                "Fast API oracle exceeded 15-second deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn fast_api_substrate_matches_fixture() {
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
fn fast_api_substrate_is_deterministic_and_ordered() {
    let first = run_with_deadline();
    let second = run_with_deadline();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 5);
    for (line, id) in lines[..4].iter().zip([
        "fast-api-substrate/native_descriptor_metadata",
        "fast-api-substrate/single_overload_execution_and_lifetime",
        "fast-api-substrate/two_overload_arity_and_fallback",
        "fast-api-substrate/empty_overloads_safe_boundary",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[4],
        "{\"summary\":{\"total\":4,\"passed\":4,\"failed\":0}}"
    );
}
