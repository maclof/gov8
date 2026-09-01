use std::process::{Command, Output};
use std::thread;
use std::time::{Duration, Instant};

fn run_with_deadline() -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-fast-api-substrate"))
        .arg("mode=duplicate-arity")
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .expect("failed to run duplicate-arity probe");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child.try_wait().expect("failed to poll probe").is_some() {
            return child.wait_with_output().expect("failed to collect probe");
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung probe");
            let output = child.wait_with_output().expect("failed to collect timeout");
            panic!(
                "duplicate-arity probe exceeded 15-second deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn duplicate_fast_overload_arities_are_v8_fatal() {
    let output = run_with_deadline();
    assert_eq!(output.status.code(), Some(0x8000_0003_u32 as i32));
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Check failed"));
    assert!(stderr.contains("ArgumentCount()"));
}
