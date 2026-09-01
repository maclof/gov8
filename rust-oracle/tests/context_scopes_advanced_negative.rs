//! Process-boundary contracts for JavaScript execution disallow modes.

fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_context-scopes-advanced-negative"))
        .arg(mode)
        .output()
        .expect("failed to run context-scopes-advanced-negative")
}

#[test]
fn crash_on_failure_is_a_v8_fatal_breakpoint() {
    const WINDOWS_BREAKPOINT: i32 = -2_147_483_645; // 0x80000003
    const CHECKPOINTS: &[u8] = b"mode=crash\nscope=entered\nscript=compiled\n";
    for attempt in 1..=3 {
        let output = run("crash");
        assert_eq!(
            output.status.code(),
            Some(WINDOWS_BREAKPOINT),
            "attempt {attempt} did not hit V8's fatal breakpoint"
        );
        assert_eq!(
            output.stdout, CHECKPOINTS,
            "attempt {attempt} boundary moved"
        );
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.contains("Fatal error"),
            "attempt {attempt}: {stderr}"
        );
        assert!(
            stderr.contains("Invoke in DisallowJavascriptExecutionScope"),
            "attempt {attempt}: {stderr}"
        );
    }
}

#[test]
fn dump_on_failure_allows_execution_without_diagnostic() {
    const EXPECTED: &[u8] = b"mode=dump\nscope=entered\nscript=compiled\nrun_some=true\n";
    for attempt in 1..=3 {
        let output = run("dump");
        assert!(output.status.success(), "attempt {attempt} failed");
        assert_eq!(output.stdout, EXPECTED, "attempt {attempt} output drifted");
        assert!(
            output.stderr.is_empty(),
            "attempt {attempt} unexpectedly diagnosed: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }
}
