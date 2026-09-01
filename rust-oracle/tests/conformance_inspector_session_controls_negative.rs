use std::process::{Command, Output};
use std::thread;
use std::time::{Duration, Instant};

fn run_panic_mode_with_deadline() -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-inspector-session-controls"))
        .arg("mode=panic-pause-client")
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .expect("failed to run inspector pause panic mode");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child
            .try_wait()
            .expect("failed to poll panic mode")
            .is_some()
        {
            return child
                .wait_with_output()
                .expect("failed to collect panic mode output");
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung panic mode");
            let output = child
                .wait_with_output()
                .expect("failed to collect hung panic mode");
            panic!(
                "inspector panic mode exceeded 15-second deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn pause_client_panic_aborts_at_non_unwinding_boundary() {
    let output = run_panic_mode_with_deadline();
    assert_eq!(output.status.code(), Some(-1_073_740_791)); // 0xC0000409
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("inspector pause client panic boundary"));
    assert!(stderr.contains("panic in a function that cannot unwind"));
}
