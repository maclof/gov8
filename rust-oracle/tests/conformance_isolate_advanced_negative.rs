//! Process-fatal lifecycle boundary for the isolate-advanced slice.

#[test]
fn array_buffer_without_entered_context_is_stable_access_violation() {
    for attempt in 0..3 {
        let output =
            std::process::Command::new(env!("CARGO_BIN_EXE_conformance-isolate-advanced-negative"))
                .output()
                .expect("failed to run isolate-advanced negative probe");
        assert_eq!(
            output.status.code(),
            Some(-1_073_741_819),
            "attempt {attempt}: expected Windows STATUS_ACCESS_VIOLATION"
        );
        assert!(
            output.stdout.is_empty(),
            "attempt {attempt}: unexpected stdout"
        );
        assert!(
            output.stderr.is_empty(),
            "attempt {attempt}: unexpected stderr"
        );
    }
}
