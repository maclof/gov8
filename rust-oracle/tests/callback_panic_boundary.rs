//! Rust panic boundary of native callbacks (out-of-process characterization).
//!
//! A native callback executes inside the pinned crate's `extern "C"`
//! trampoline (`v8::support::MapFnFrom for FunctionCallback` -> `c_fn` in
//! `src/support.rs`). Since rustc 1.81, a panic that would unwind out of an
//! `extern "C"` function is a non-unwinding panic: the default hook prints
//! the original panic message, a second panic ("panic in a function that
//! cannot unwind") is raised, and the process aborts via fail-fast.
//!
//! Observed on this exact build (rustc 1.98.0, Windows x86_64 MSVC, v8
//! =152.2.0):
//! - stderr contains the callback's own panic message,
//! - stderr contains "panic in a function that cannot unwind",
//! - host code after the call never runs,
//! - the process exits with the fail-fast code 0xC0000409
//!   (`Some(-1073740791)` as reported by `ExitStatus::code`).
//!
//! This must stay its own process; the assertion therefore spawns the
//! dedicated `panic-boundary` binary instead of panicking in-process.

#[test]
fn callback_panic_aborts_the_process() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_panic-boundary"))
        .output()
        .expect("failed to run panic-boundary binary");
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);

    // The host code before the call and inside the callback both ran.
    assert!(stderr.contains("marker:before-call"), "stderr:\n{stderr}");
    assert!(
        stderr.contains("marker:callback-entered"),
        "stderr:\n{stderr}"
    );

    // The callback's panic message went through the default hook.
    assert!(stderr.contains("host-callback-panic"), "stderr:\n{stderr}");
    // The unwinder refused to cross the extern "C" callback trampoline.
    assert!(
        stderr.contains("panic in a function that cannot unwind"),
        "stderr:\n{stderr}"
    );

    // Execution never returned into the host after the panicking call.
    assert!(
        !stdout.contains("marker:after-call") && !stderr.contains("marker:after-call"),
        "callback panic must not return; stdout:\n{stdout}\nstderr:\n{stderr}"
    );

    // Fail-fast abort, not a clean exit: 0xC0000409 on Windows MSVC.
    assert!(
        !output.status.success(),
        "process must not exit cleanly; stdout:\n{stdout}\nstderr:\n{stderr}"
    );
    assert_eq!(
        output.status.code(),
        Some(-1073740791),
        "expected the 0xC0000409 fail-fast abort code"
    );
}
