use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

#[test]
fn malformed_object_ids_fail_without_abort_or_hang() {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-inspector-object-wrapping"))
        .arg("mode=invalid-object-ids")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to run invalid-object-id mode");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if child.try_wait().expect("failed to poll oracle").is_some() {
            let output = child.wait_with_output().expect("failed to collect oracle");
            assert!(output.status.success());
            assert_eq!(output.stdout, b"invalid-id-errors=4\n");
            assert!(output.stderr.is_empty());
            return;
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung oracle");
            panic!("invalid-object-id mode exceeded deadline");
        }
        thread::sleep(Duration::from_millis(10));
    }
}
