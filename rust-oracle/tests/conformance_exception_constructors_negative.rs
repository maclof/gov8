//! Unsafe bypasses of the context-bearing scope requirement.

fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!(
        "CARGO_BIN_EXE_conformance-exception-constructors-negative"
    ))
    .arg(mode)
    .output()
    .unwrap_or_else(|error| panic!("failed to run {mode}: {error}"))
}

#[test]
fn constructor_without_context_is_stable_access_violation() {
    for attempt in 0..3 {
        let output = run("constructor");
        assert_eq!(
            output.status.code(),
            Some(-1_073_741_819),
            "attempt {attempt}"
        );
        assert!(output.stdout.is_empty());
        assert!(output.stderr.is_empty());
    }
}

#[test]
fn primitive_create_message_without_context_survives_unsafe_bypass() {
    for _ in 0..3 {
        let output = run("message");
        assert!(output.status.success());
        assert_eq!(output.stdout, b"Uncaught undefined\n");
        assert!(output.stderr.is_empty());
    }
}

#[test]
fn context_dependent_message_positions_abort_without_context() {
    for attempt in 0..3 {
        let output = run("positions");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert_eq!(
            output.status.code(),
            Some(-1_073_740_791),
            "attempt {attempt}"
        );
        assert!(output.stdout.is_empty());
        assert!(stderr.contains("NonNull::new_unchecked requires that the pointer is non-null"));
        assert!(stderr.contains("thread caused non-unwinding panic. aborting."));
    }
}
