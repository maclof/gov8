use std::process::{Command, Output, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const FIXTURE: &str = include_str!(
    "fixtures/conformance-cppgc-object-wrapping-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run_with_deadline() -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-cppgc-object-wrapping"))
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to run cppgc object-wrapping oracle");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child.try_wait().expect("failed to poll oracle").is_some() {
            return child
                .wait_with_output()
                .expect("failed to collect oracle output");
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung cppgc oracle");
            let output = child
                .wait_with_output()
                .expect("failed to collect hung cppgc oracle");
            panic!(
                "cppgc object-wrapping oracle exceeded deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn cppgc_object_wrapping_matches_fixture() {
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
fn cppgc_object_wrapping_is_deterministic_and_ordered() {
    let first = run_with_deadline();
    let second = run_with_deadline();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());

    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 7);
    for (line, id) in lines[..6].iter().zip([
        "cppgc-object-wrapping/default_heap_and_api_wrapper",
        "cppgc-object-wrapping/wrap_unwrap_identity",
        "cppgc-object-wrapping/traced_reference_survival",
        "cppgc-object-wrapping/gc_destruction",
        "cppgc-object-wrapping/tag_boundaries",
        "cppgc-object-wrapping/orderly_teardown",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[6],
        "{\"summary\":{\"total\":6,\"passed\":6,\"failed\":0}}"
    );
}
