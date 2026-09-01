use std::io::Read;
use std::os::windows::process::CommandExt;
use std::process::{Command, ExitStatus, Stdio};
use std::thread;
use std::time::{Duration, Instant};

const CREATE_NO_WINDOW: u32 = 0x0800_0000;
const STATUS_BREAKPOINT: i32 = 0x8000_0003_u32 as i32;
const STATUS_STACK_BUFFER_OVERRUN: i32 = 0xc000_0409_u32 as i32;

struct ProbeOutput {
    status: ExitStatus,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn run(mode: &str) -> ProbeOutput {
    let mut child = Command::new(env!("CARGO_BIN_EXE_conformance-cppgc-object-wrapping"))
        .arg(mode)
        .env("RUST_BACKTRACE", "0")
        .env("RUST_LIB_BACKTRACE", "0")
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("failed to run cppgc object-wrapping probe");
    let mut stdout = child.stdout.take().expect("piped child stdout");
    let mut stderr = child.stderr.take().expect("piped child stderr");
    // A non-unwinding callback panic can emit enough diagnostic output to
    // fill a Windows pipe. Drain both streams concurrently while polling.
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
            child.kill().expect("failed to kill hung cppgc probe");
            let status = child.wait().expect("failed to reap killed cppgc probe");
            let stdout = stdout_reader.join().expect("join stdout reader");
            let stderr = stderr_reader.join().expect("join stderr reader");
            panic!(
                "cppgc probe {mode} exceeded deadline; status={status}\nstdout:\n{}\nstderr:\n{}",
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
fn cppgc_trace_panic_aborts_at_the_rustobj_ffi_boundary() {
    for iteration in 0..4 {
        let output = run("panic-trace");
        assert_eq!(
            output.status.code(),
            Some(STATUS_STACK_BUFFER_OVERRUN),
            "iteration {iteration}"
        );
        assert!(output.stdout.is_empty(), "iteration {iteration}");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.contains("cppgc trace callback panic"),
            "iteration {iteration}: {stderr}"
        );
        assert!(
            stderr.contains("panic in a function that cannot unwind"),
            "iteration {iteration}: {stderr}"
        );
        assert!(
            stderr.contains("rusty_v8_RustObj_trace"),
            "iteration {iteration}: {stderr}"
        );
    }
}

#[test]
fn duplicate_explicit_cppgc_initialization_is_v8_fatal() {
    for iteration in 0..4 {
        let output = run("explicit-init-after-v8");
        assert_eq!(
            output.status.code(),
            Some(STATUS_BREAKPOINT),
            "iteration {iteration}"
        );
        assert!(output.stdout.is_empty(), "iteration {iteration}");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.contains("Fatal error"),
            "iteration {iteration}: {stderr}"
        );
        assert!(
            stderr.contains("Check failed: !internal::g_page_allocator."),
            "iteration {iteration}: {stderr}"
        );
    }
}

#[test]
fn unsafe_tag_type_and_rewrap_edges_are_observed_in_fresh_processes() {
    for (mode, expected) in [
        ("tag-min", "tag_edge_survived=0\n"),
        ("tag-max", "tag_edge_survived=32766\n"),
        ("wrong-tag", "wrong_tag_some=true\n"),
        ("wrong-type", "wrong_type_some=true\n"),
        ("rewrap-same", "rewrap_tag=1,id=2\n"),
        ("rewrap-different", "rewrap_tag=2,id=2\n"),
    ] {
        let output = run(mode);
        assert!(
            output.status.success(),
            "{mode}\nstdout:\n{}\nstderr:\n{}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        assert_eq!(
            String::from_utf8(output.stdout).expect("utf-8 stdout"),
            expected,
            "{mode}"
        );
    }
}
