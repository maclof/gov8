//! Process-boundary checks for `PrimitiveArray` preconditions that the Rust
//! wrapper does not validate before entering V8.

fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!(
        "CARGO_BIN_EXE_conformance-fixed-primitive-arrays-negative"
    ))
    .arg(mode)
    .output()
    .unwrap_or_else(|error| panic!("failed to run {mode} probe: {error}"))
}

#[test]
fn primitive_index_and_negative_int_lengths_are_v8_fatal() {
    const WINDOWS_BREAKPOINT: i32 = -2_147_483_645; // 0x80000003
    for (mode, stdout, api, reason) in [
        (
            "get-empty",
            "created length=0\n",
            "v8::PrimitiveArray::Get",
            "index must be greater than or equal to 0 and less than the array length",
        ),
        (
            "get-at-count",
            "created length=1\n",
            "v8::PrimitiveArray::Get",
            "index must be greater than or equal to 0 and less than the array length",
        ),
        (
            "set-at-count",
            "created length=1\n",
            "v8::PrimitiveArray::Set",
            "index must be greater than or equal to 0 and less than the array length",
        ),
        (
            "length-overflow",
            "requested=2147483648\n",
            "v8::PrimitiveArray::New",
            "length must be equal or greater than zero",
        ),
        (
            "length-usize-max",
            "requested=18446744073709551615\n",
            "v8::PrimitiveArray::New",
            "length must be equal or greater than zero",
        ),
    ] {
        let output = run(mode);
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert_eq!(
            output.status.code(),
            Some(WINDOWS_BREAKPOINT),
            "{mode}: expected V8's 0x80000003 fatal boundary; stderr:\n{stderr}"
        );
        assert_eq!(String::from_utf8_lossy(&output.stdout), stdout, "{mode}");
        assert!(
            stderr.contains(&format!("Fatal error in {api}")),
            "{mode}: missing API-specific V8 fatal header; stderr:\n{stderr}"
        );
        assert!(
            stderr.contains(reason),
            "{mode}: missing V8 precondition text; stderr:\n{stderr}"
        );
        assert!(
            !stderr.contains("panicked at"),
            "{mode}: boundary unexpectedly surfaced as Rust panic; stderr:\n{stderr}"
        );
    }
}

#[test]
fn cross_isolate_misuse_is_not_rejected_by_the_pinned_build() {
    for (mode, expected) in [
        (
            "cross-isolate-get",
            "first array ready\nsecond isolate ready\nget=first\noperation survived\n",
        ),
        (
            "cross-isolate-set",
            "first array ready\nsecond isolate ready\nget_after_set=second\noperation survived\n",
        ),
    ] {
        for run_index in 1..=3 {
            let output = run(mode);
            assert!(
                output.status.success(),
                "{mode} run {run_index}: pinned build unexpectedly rejected the misuse; stderr:\n{}",
                String::from_utf8_lossy(&output.stderr)
            );
            assert_eq!(
                String::from_utf8_lossy(&output.stdout),
                expected,
                "{mode} run {run_index}"
            );
            assert!(
                output.stderr.is_empty(),
                "{mode} run {run_index}: unexpected stderr:\n{}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
    }
}
