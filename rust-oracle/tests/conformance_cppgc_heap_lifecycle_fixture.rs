use std::io::Read;
use std::os::windows::process::CommandExt;
use std::process::{Command, ExitStatus, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const CREATE_NO_WINDOW: u32 = 0x0800_0000;
const FIXTURE: &str = include_str!(
    "fixtures/conformance-cppgc-heap-lifecycle-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

struct ProbeOutput {
    status: ExitStatus,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn run_with_deadline() -> ProbeOutput {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-cppgc-heap-lifecycle"))
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to execute cppgc heap-lifecycle oracle");
    let mut stdout = child.stdout.take().expect("piped child stdout");
    let mut stderr = child.stderr.take().expect("piped child stderr");
    let stdout_reader = thread::spawn(move || {
        let mut bytes = Vec::new();
        stdout.read_to_end(&mut bytes).expect("read child stdout");
        bytes
    });
    let stderr_reader = thread::spawn(move || {
        let mut bytes = Vec::new();
        stderr.read_to_end(&mut bytes).expect("read child stderr");
        bytes
    });
    let deadline = Instant::now() + Duration::from_secs(20);
    let status = loop {
        if let Some(status) = child.try_wait().expect("failed to poll oracle") {
            break status;
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung oracle");
            let status = child.wait().expect("failed to reap killed oracle");
            let stdout = stdout_reader.join().expect("join stdout reader");
            let stderr = stderr_reader.join().expect("join stderr reader");
            panic!(
                "cppgc heap-lifecycle oracle exceeded deadline; status={status}\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&stdout),
                String::from_utf8_lossy(&stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    };
    ProbeOutput {
        status,
        stdout: stdout_reader.join().expect("join stdout reader"),
        stderr: stderr_reader.join().expect("join stderr reader"),
    }
}

#[test]
fn cppgc_heap_lifecycle_matches_fixture() {
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
fn cppgc_heap_lifecycle_is_deterministic_and_ordered() {
    let first = run_with_deadline();
    let second = run_with_deadline();
    assert!(first.status.success() && second.status.success());
    assert!(first.stderr.is_empty() && second.stderr.is_empty());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());

    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 7);
    for (line, id) in lines[..6].iter().zip([
        "cppgc-heap-lifecycle/pin",
        "cppgc-heap-lifecycle/create_params/default",
        "cppgc-heap-lifecycle/detached/collection_and_terminate",
        "cppgc-heap-lifecycle/process/paired_reinitialize",
        "cppgc-heap-lifecycle/isolate/custom_heap_ownership",
        "cppgc-heap-lifecycle/process/orderly_v8_shutdown",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[6],
        "{\"summary\":{\"total\":6,\"passed\":6,\"failed\":0}}"
    );
}
