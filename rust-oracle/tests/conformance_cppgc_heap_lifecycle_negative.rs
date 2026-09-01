use std::io::Read;
use std::os::windows::process::CommandExt;
use std::process::{Command, ExitStatus, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const CREATE_NO_WINDOW: u32 = 0x0800_0000;
const STATUS_ACCESS_VIOLATION: i32 = 0xc000_0005_u32 as i32;
const STATUS_BREAKPOINT: i32 = 0x8000_0003_u32 as i32;

struct ProbeOutput {
    status: ExitStatus,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn run(mode: &str) -> ProbeOutput {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-cppgc-heap-lifecycle"))
        .arg(mode)
        .env("RUST_BACKTRACE", "0")
        .env("RUST_LIB_BACKTRACE", "0")
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to execute cppgc heap-lifecycle probe");
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
    let deadline = Instant::now() + Duration::from_secs(15);
    let status = loop {
        if let Some(status) = child.try_wait().expect("failed to poll probe") {
            break status;
        }
        if Instant::now() >= deadline {
            child.kill().expect("failed to kill hung probe");
            let status = child.wait().expect("failed to reap killed probe");
            let stdout = stdout_reader.join().expect("join stdout reader");
            let stderr = stderr_reader.join().expect("join stderr reader");
            panic!(
                "cppgc heap-lifecycle probe {mode} exceeded deadline; status={status}\nstdout:\n{}\nstderr:\n{}",
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
fn heap_creation_and_duplicate_initialization_fail_out_of_process() {
    for iteration in 0..3 {
        let output = run("create-before-initialize");
        assert_eq!(
            output.status.code(),
            Some(STATUS_ACCESS_VIOLATION),
            "iteration {iteration}"
        );
        assert!(output.stdout.is_empty(), "iteration {iteration}");

        let output = run("initialize-twice");
        assert_eq!(output.status.code(), Some(3), "iteration {iteration}");
        assert!(output.stdout.is_empty(), "iteration {iteration}");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.contains("Check failed: !internal::g_page_allocator."),
            "iteration {iteration}: {stderr}"
        );
    }
}

#[test]
fn detached_testing_mode_rejects_duplicate_or_attached_enable() {
    for iteration in 0..3 {
        let output = run("enable-detached-twice");
        assert_eq!(output.status.code(), Some(3), "iteration {iteration}");
        assert!(output.stdout.is_empty(), "iteration {iteration}");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.contains("Check failed: !in_detached_testing_mode_."),
            "iteration {iteration}: {stderr}"
        );

        let output = run("enable-detached-attached");
        assert_eq!(
            output.status.code(),
            Some(STATUS_BREAKPOINT),
            "iteration {iteration}"
        );
        assert!(output.stdout.is_empty(), "iteration {iteration}");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.contains("Check failed: (isolate_) == nullptr."),
            "iteration {iteration}: {stderr}"
        );
    }
}

#[test]
fn unsafe_shutdown_transitions_have_no_runtime_guard() {
    for (mode, expected) in [
        (
            "shutdown-before-initialize",
            "shutdown_before_initialize_returned\n",
        ),
        ("shutdown-twice", "shutdown_twice_returned\n"),
        (
            "shutdown-with-live-heap",
            "shutdown_with_live_heap_returned\n",
        ),
    ] {
        let output = run(mode);
        assert!(
            output.status.success(),
            "{mode}\nstdout:\n{}\nstderr:\n{}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        assert_eq!(String::from_utf8(output.stdout).unwrap(), expected);
        assert!(output.stderr.is_empty());
    }
}
