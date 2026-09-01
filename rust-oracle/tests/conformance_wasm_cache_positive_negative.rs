use std::process::Command;

fn run(args: &[&str]) -> std::process::Output {
    Command::new(env!("CARGO_BIN_EXE_conformance-wasm-cache-positive"))
        .args(args)
        .output()
        .expect("failed to execute Wasm cache fatal probe")
}

#[test]
fn setting_cached_bytes_twice_is_fatal() {
    let output = run(&["--negative-double-set"]);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert_eq!(output.status.code(), Some(0x8000_0003_u32 as i32));
    assert!(stderr.contains("marker:first-set:accepted=true"));
    assert!(stderr.contains("SetCachedCompiledModuleBytes can only be called once"));
    assert!(!stderr.contains("marker:after-second-set"));
    assert!(output.stdout.is_empty());
}

#[test]
fn unchecked_wire_mismatch_and_truncation_are_fatal() {
    for mode in ["mismatched-wire", "truncated-cache"] {
        let output = run(&["--rejection-probe", mode]);
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert_eq!(
            output.status.code(),
            Some(0x8000_0003_u32 as i32),
            "{mode}: {stderr}"
        );
        assert!(
            stderr.contains(&format!("marker:before-attempt:{mode}")),
            "{mode}: {stderr}"
        );
        assert!(
            stderr.contains("Check failed: 0 == reader->current_size()."),
            "{mode}: {stderr}"
        );
        assert!(
            !stderr.contains(&format!("marker:after-attempt:{mode}")),
            "{mode}: {stderr}"
        );
        assert!(output.stdout.is_empty(), "{mode}");
    }
}
