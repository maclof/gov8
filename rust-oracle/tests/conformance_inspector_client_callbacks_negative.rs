use std::io::Read;
use std::os::windows::process::CommandExt;
use std::process::{Command, ExitStatus, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const CREATE_NO_WINDOW: u32 = 0x0800_0000;

struct ProbeOutput {
    status: ExitStatus,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn collect_with_deadline(mode: &str) -> ProbeOutput {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-inspector-client-callbacks"))
        .arg(mode)
        .env("RUST_BACKTRACE", "0")
        .env("RUST_LIB_BACKTRACE", "0")
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to spawn inspector client-callback panic probe");
    let mut stdout = child.stdout.take().expect("piped child stdout");
    let mut stderr = child.stderr.take().expect("piped child stderr");
    // Fatal Rust callbacks can emit a forced second-panic backtrace. Drain
    // both pipes while polling so the child can never block on a full pipe.
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
    let deadline = Instant::now() + Duration::from_secs(15);
    let status = loop {
        if let Some(status) = child.try_wait().expect("failed to poll child") {
            break status;
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung panic probe");
            let status = child.wait().expect("failed to reap killed panic probe");
            let stdout = stdout_reader.join().expect("join stdout reader");
            let stderr = stderr_reader.join().expect("join stderr reader");
            panic!(
                "inspector client-callback panic probe exceeded deadline; status={status}\nstdout:\n{}\nstderr:\n{}",
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

fn run_panic_mode(mode: &str, marker: &str) {
    let output = collect_with_deadline(mode);
    assert_eq!(
        output.status.code(),
        Some(0xC0000409_u32 as i32),
        "unexpected status {:?}\nstdout:\n{}\nstderr:\n{}",
        output.status,
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains(marker), "unexpected stderr: {stderr}");
    assert!(
        stderr.contains("panic in a function that cannot unwind"),
        "unexpected panic boundary: {stderr}"
    );
}

#[test]
fn inspector_client_callback_panics_abort_at_ffi_boundary() {
    for (mode, marker) in [
        (
            "mode=panic-generate",
            "inspector client generate_unique_id panic boundary",
        ),
        (
            "mode=panic-waiting",
            "inspector client run_if_waiting panic boundary",
        ),
        (
            "mode=panic-resource",
            "inspector client resource_name_to_url panic boundary",
        ),
        (
            "mode=panic-console",
            "inspector client console_api_message panic boundary",
        ),
    ] {
        run_panic_mode(mode, marker);
    }
}
