use std::io::Read;
use std::os::windows::process::CommandExt;
use std::process::{Command, ExitStatus, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const CREATE_NO_WINDOW: u32 = 0x0800_0000;
const FIXTURE: &str =
    include_str!("fixtures/conformance-fast-api-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

struct ProbeOutput {
    status: ExitStatus,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn run_with_deadline() -> ProbeOutput {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-fast-api-residual"))
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to execute Fast API residual oracle");
    let mut stdout = child.stdout.take().expect("piped stdout");
    let mut stderr = child.stderr.take().expect("piped stderr");
    let stdout_reader = thread::spawn(move || {
        let mut bytes = Vec::new();
        stdout.read_to_end(&mut bytes).expect("read stdout");
        bytes
    });
    let stderr_reader = thread::spawn(move || {
        let mut bytes = Vec::new();
        stderr.read_to_end(&mut bytes).expect("read stderr");
        bytes
    });
    let deadline = Instant::now() + Duration::from_secs(20);
    let status = loop {
        if let Some(status) = child.try_wait().expect("poll oracle") {
            break status;
        }
        if Instant::now() >= deadline {
            child.kill().expect("kill hung oracle");
            let status = child.wait().expect("reap killed oracle");
            let stdout = stdout_reader.join().expect("join stdout");
            let stderr = stderr_reader.join().expect("join stderr");
            panic!(
                "Fast API residual oracle exceeded deadline; status={status}\nstdout:\n{}\nstderr:\n{}",
                String::from_utf8_lossy(&stdout),
                String::from_utf8_lossy(&stderr)
            );
        }
        thread::sleep(Duration::from_millis(10));
    };
    ProbeOutput {
        status,
        stdout: stdout_reader.join().expect("join stdout"),
        stderr: stderr_reader.join().expect("join stderr"),
    }
}

#[test]
fn fast_api_residual_matches_fixture() {
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
fn fast_api_residual_is_deterministic_and_ordered() {
    let first = run_with_deadline();
    let second = run_with_deadline();
    assert!(first.status.success() && second.status.success());
    assert!(first.stderr.is_empty() && second.stderr.is_empty());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());

    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 9);
    for (line, id) in lines[..8].iter().zip([
        "fast-api-residual/pin_and_public_surface",
        "fast-api-residual/options/external_data_and_callback_scope",
        "fast-api-residual/options/undefined_data_and_type_fallback",
        "fast-api-residual/one_byte/direct_as_bytes_boundaries",
        "fast-api-residual/one_byte/optimized_input_matrix",
        "fast-api-residual/ctype_info/constructor_flag_matrix",
        "fast-api-residual/ctype_info/optimized_flag_semantics",
        "fast-api-residual/ctype_info/allow_shared_v8value_semantics",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[8],
        "{\"summary\":{\"total\":8,\"passed\":8,\"failed\":0}}"
    );
}
