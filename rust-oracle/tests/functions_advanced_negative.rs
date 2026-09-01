//! Process-fatal preconditions of `Function::create_code_cache`.
//! Only functions returned by `ScriptCompiler::compile_function` carry the
//! required wrapped source. These probes stay in a dedicated child binary so
//! no fatal V8 state can contaminate the conformance process.

fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_functions-advanced-negative"))
        .arg(mode)
        .output()
        .unwrap_or_else(|error| panic!("failed to run {mode} probe: {error}"))
}

#[test]
fn native_and_ordinary_script_functions_are_v8_fatal() {
    for mode in ["native", "script"] {
        let output = run(mode);
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            !output.status.success(),
            "{mode}: non-CompileFunction cache probe unexpectedly survived"
        );
        assert!(
            stderr.contains("Fatal error in v8::ScriptCompiler::CreateCodeCacheForFunction"),
            "{mode}: missing V8 fatal header; stderr:\n{stderr}"
        );
        assert!(
            stderr.contains("Expected SharedFunctionInfo with wrapped source code"),
            "{mode}: missing wrapped-source precondition; stderr:\n{stderr}"
        );
        assert!(
            !stderr.contains("panicked at"),
            "{mode}: boundary unexpectedly surfaced as Rust panic; stderr:\n{stderr}"
        );
    }
}

#[test]
fn bound_function_cache_attempt_is_access_violation() {
    let output = run("bound");
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        !output.status.success(),
        "bound function cache probe unexpectedly survived"
    );
    // The wrapper passes a bound function through an unchecked C++ JSFunction
    // cast before the wrapped-source ApiCheck. On the pinned Windows build the
    // result is STATUS_ACCESS_VIOLATION, with no Rust panic or V8 diagnostic.
    assert_eq!(output.status.code(), Some(-1_073_741_819));
    assert!(
        stderr.is_empty(),
        "bound-function access violation unexpectedly wrote stderr:\n{stderr}"
    );
}

#[test]
fn compile_function_cache_mismatch_boundaries() {
    for (mode, expected) in [
        (
            "cache-source",
            "compiled=true rejected=true length=2 call=43",
        ),
        (
            "cache-parameter-names",
            "compiled=true rejected=false length=2 call=42",
        ),
        (
            "cache-parameter-count",
            "compiled=true rejected=false length=2 call=42",
        ),
        (
            "cache-truncated",
            "compiled=true rejected=true length=2 call=42",
        ),
        (
            "cache-corrupt",
            "compiled=true rejected=false length=2 call=42",
        ),
    ] {
        let output = run(mode);
        assert!(
            output.status.success(),
            "{mode}: cache mismatch was unexpectedly fatal; stderr:\n{}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert_eq!(
            String::from_utf8_lossy(&output.stdout).trim(),
            expected,
            "{mode}: unexpected consume result"
        );
    }
}

#[test]
fn function_builder_length_boundaries() {
    for (mode, expected) in [
        ("length-negative", "built=true observed=Some(65535)"),
        ("length-large", "built=true observed=Some(65535)"),
    ] {
        let output = run(mode);
        assert!(
            output.status.success(),
            "{mode}: builder length was unexpectedly fatal; stderr:\n{}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert_eq!(String::from_utf8_lossy(&output.stdout).trim(), expected);
    }
}
