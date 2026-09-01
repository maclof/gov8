use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

#[test]
fn panic_in_inspectable_get_aborts_at_ffi_boundary() {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-inspector-inspected-object"))
        .arg("mode=panic-callback")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to spawn inspected-object panic probe");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child.try_wait().expect("failed to poll child").is_some() {
            let output = child.wait_with_output().expect("failed to collect child");
            assert!(
                !output.status.success(),
                "panic callback unexpectedly returned"
            );
            let stderr = String::from_utf8_lossy(&output.stderr);
            assert!(
                stderr.contains("inspector inspected-object callback panic boundary"),
                "unexpected stderr: {stderr}"
            );
            assert!(
                stderr.contains("panic in a function that cannot unwind"),
                "unexpected panic boundary: {stderr}"
            );
            return;
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung panic probe");
            let output = child.wait_with_output().expect("failed to collect child");
            panic!(
                "inspected-object panic probe exceeded deadline\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    }
}
