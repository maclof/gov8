use std::io::Read;
use std::os::windows::process::CommandExt;
use std::process::{Command, ExitStatus, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const CREATE_NO_WINDOW: u32 = 0x0800_0000;
const STATUS_BREAKPOINT: i32 = 0x8000_0003_u32 as i32;

struct ProbeOutput {
    status: ExitStatus,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn run(mode: &str) -> ProbeOutput {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-fast-api-residual"))
        .arg(mode)
        .env("RUST_BACKTRACE", "0")
        .env("RUST_LIB_BACKTRACE", "0")
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to execute Fast API residual fatal probe");
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
    let deadline = Instant::now() + Duration::from_secs(15);
    let status = loop {
        if let Some(status) = child.try_wait().expect("poll probe") {
            break status;
        }
        if Instant::now() >= deadline {
            child.kill().expect("kill hung probe");
            let status = child.wait().expect("reap killed probe");
            let stdout = stdout_reader.join().expect("join stdout");
            let stderr = stderr_reader.join().expect("join stderr");
            panic!(
                "Fast API residual probe {mode} exceeded deadline; status={status}\nstdout:\n{}\nstderr:\n{}",
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
fn invalid_fast_api_descriptors_are_v8_fatal_during_optimization() {
    for iteration in 0..3 {
        for (mode, stack_marker) in [
            ("mode=options-middle", "UseInfoForFastApiCallArgument"),
            ("mode=clamp-bool", "FastApiCallLoweringReducer"),
        ] {
            let output = run(mode);
            assert_eq!(
                output.status.code(),
                Some(STATUS_BREAKPOINT),
                "{mode} iteration {iteration}"
            );
            assert!(output.stdout.is_empty(), "{mode} iteration {iteration}");
            let stderr = String::from_utf8_lossy(&output.stderr);
            assert!(
                stderr.contains("Fatal error") && stderr.contains("unreachable code"),
                "{mode} iteration {iteration}: {stderr}"
            );
            assert!(
                stderr.contains(stack_marker),
                "{mode} iteration {iteration}: {stderr}"
            );
        }
    }
}
