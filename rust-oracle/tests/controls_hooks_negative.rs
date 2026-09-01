//! Negative/edge-case characterization for process/isolate controls & hooks.
//!
//! These paths abort the process (fatal CHECKs and the controlled fatal OOM),
//! so each one is observed by spawning the `conformance-controls-hooks`
//! binary in a dedicated subprocess mode. Every heap-pressure subprocess caps
//! its heap with `CreateParams::heap_limits(0, 10 MiB)`: the OOMs here are
//! the *intended, bounded* fatal path, never uncontrolled process growth.
//!
//! Environment notes (pinned build, x86_64-pc-windows-msvc, v8 =152.2.0):
//! - fatal CHECK failures abort with STATUS_BREAKPOINT, observed by
//!   `ExitStatus::code()` as `Some(-2147483645)` (0x80000003);
//! - the API fatal handler is invoked for the flags-freeze CHECK and the OOM
//!   path, but NOT for the "Must use --expose-gc" FATAL;
//! - unrecognized V8 flags are printed to STDERR by V8 itself (pre-init
//!   only) and otherwise ignored.

const EXIT_STATUS_BREAKPOINT: i32 = -2147483645;

fn spawn(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-controls-hooks"))
        .arg(mode)
        .output()
        .expect("failed to spawn conformance-controls-hooks subprocess mode")
}

#[test]
fn post_init_flag_value_change_is_fatal() {
    let output = spawn("sub-fatal-frozen-flags");
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);

    // The pre-change marker ran; execution never returned past the mutation.
    assert!(
        stderr.contains("MARK:before-flag-change"),
        "stderr:\n{stderr}"
    );
    assert!(
        !stdout.contains("SURVIVED"),
        "flag mutation must not survive; stdout:\n{stdout}"
    );

    // The registered fatal handler saw the frozen-flags CHECK failure.
    assert!(stderr.contains("FATAL "), "stderr:\n{stderr}");
    assert!(
        stderr.contains("message=\"Check failed: !IsFrozen().\""),
        "stderr:\n{stderr}"
    );
    // Official-build V8_Fatal carries no file/line.
    assert!(
        stderr.contains("FATAL file=\"\" line=0"),
        "stderr:\n{stderr}"
    );
    assert!(stderr.contains("# Fatal error"), "stderr:\n{stderr}");

    assert_eq!(output.status.code(), Some(EXIT_STATUS_BREAKPOINT));
}

#[test]
fn gc_request_without_expose_gc_is_fatal_for_full_and_minor() {
    for mode in [
        "sub-gc-without-expose-gc-full",
        "sub-gc-without-expose-gc-minor",
    ] {
        let output = spawn(mode);
        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        assert!(
            stderr.contains("MARK:before-gc-request"),
            "{mode} stderr:\n{stderr}"
        );
        assert!(
            !stdout.contains("SURVIVED"),
            "{mode} must not survive; stdout:\n{stdout}"
        );
        assert!(
            stderr.contains("# Fatal error in v8::Isolate::RequestGarbageCollectionForTesting"),
            "{mode} stderr:\n{stderr}"
        );
        assert!(
            stderr.contains("# Must use --expose-gc"),
            "{mode} stderr:\n{stderr}"
        );
        // Site-specific fatal-handler coverage: this fatal site does NOT call
        // the API fatal handler (contrast with the flags-freeze CHECK).
        assert!(
            !stderr.contains("FATAL file="),
            "{mode} must not invoke the API fatal handler; stderr:\n{stderr}"
        );
        assert_eq!(output.status.code(), Some(EXIT_STATUS_BREAKPOINT), "{mode}");
    }
}

#[test]
fn shrinking_near_heap_limit_callback_forces_controlled_oom() {
    let output = spawn("sub-near-heap-limit-shrink");
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);

    // The callback ran and shrank the limit (V8's configured 4 MiB budget).
    assert!(
        stderr.contains("SHRINK call=1 current=4194304"),
        "stderr:\n{stderr}"
    );
    // The OOM handler observed the heap-OOM details before the abort.
    assert!(
        stderr.contains("OOM location=\"Reached heap limit\" is_heap_oom=true"),
        "stderr:\n{stderr}"
    );
    assert!(
        !stdout.contains("SURVIVED"),
        "shrunk limit must end in fatal OOM; stdout:\n{stdout}"
    );
    assert_eq!(output.status.code(), Some(EXIT_STATUS_BREAKPOINT));
}

#[test]
fn heap_oom_without_handlers_uses_default_fatal_path() {
    let output = spawn("sub-oom-default");
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);

    assert!(stderr.contains("MARK:before-loop"), "stderr:\n{stderr}");
    assert!(
        stderr.contains("# Fatal JavaScript out of memory: Reached heap limit"),
        "stderr:\n{stderr}"
    );
    // No embedder handlers were installed, so no handler markers exist.
    assert!(!stderr.contains("OOM location="), "stderr:\n{stderr}");
    assert!(!stderr.contains("FATAL file="), "stderr:\n{stderr}");
    assert!(!stdout.contains("SURVIVED"), "stdout:\n{stdout}");
    assert_eq!(output.status.code(), Some(EXIT_STATUS_BREAKPOINT));
}

#[test]
fn unrecognized_flags_preinit_print_to_stderr_and_are_ignored() {
    let output = spawn("sub-invalid-flag-preinit");
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);

    assert!(
        output.status.success(),
        "unrecognized flags before initialize() must not abort; stderr:\n{stderr}"
    );
    // V8 prints these to stderr via PrintF(stderr, ...).
    assert!(
        stderr.contains("Error: unrecognized flag --definitely-not-a-real-flag"),
        "stderr:\n{stderr}"
    );
    assert!(
        stderr.contains("Try --help for options"),
        "stderr:\n{stderr}"
    );
    // The recognized flag in the same string still took effect, and the
    // isolate evaluates normally.
    assert!(
        stdout.contains("RESULT result=2 gc_type=1"),
        "stdout:\n{stdout}"
    );
}

#[test]
fn unknown_mode_is_a_clean_failure() {
    let output = spawn("bogus-mode");
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert_eq!(output.status.code(), Some(1));
    assert!(
        stderr.contains("unknown mode: bogus-mode"),
        "stderr:\n{stderr}"
    );
}

/// The entropy source may decline to fill the buffer (returns false); V8
/// falls back to its default randomness source and `Math.random()` stays a
/// valid float in [0, 1). The exact values are NOT deterministic in this
/// mode, so only range and absence-of-error are asserted. This test owns the
/// only in-process V8 usage in this file; the entropy source is process
/// state and must not race other V8 users.
#[test]
fn entropy_source_returning_false_falls_back_cleanly() {
    let platform = v8::new_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();
    fn declining_entropy(buf: &mut [u8]) -> bool {
        buf.fill(0);
        false
    }
    v8::V8::set_entropy_source(declining_entropy);

    for _ in 0..3 {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let source = v8::String::new(scope, "Math.random()").unwrap();
        let script = v8::Script::compile(scope, source, None).expect("compile");
        let value = script.run(scope).expect("run");
        let text = value.to_string(scope).unwrap().to_rust_string_lossy(scope);
        let parsed: f64 = text.parse().expect("Math.random() must parse");
        assert!((0.0..1.0).contains(&parsed), "out of range: {text}");
    }
}
