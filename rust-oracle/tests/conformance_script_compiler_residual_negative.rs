fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-script-compiler-residual"))
        .args(["--negative", mode])
        .output()
        .unwrap()
}

#[test]
fn consume_option_without_cached_data_is_access_violation() {
    let output = run("consume-without-cache");
    assert_eq!(output.status.code(), Some(0xC000_0005_u32 as i32));
    assert!(output.stdout.is_empty());
    assert!(output.stderr.is_empty());
}

#[test]
fn selected_malformed_cache_inputs_are_deterministic() {
    let cases = [
        (
            "empty-cache",
            "survived compiled=true rejected=true run_value=50\n",
        ),
        (
            "truncated-cache",
            "survived compiled=true rejected=true run_value=50\n",
        ),
        (
            "corrupt-cache",
            "survived compiled=true rejected=false run_value=50\n",
        ),
    ];
    for (mode, expected) in cases {
        for _ in 0..2 {
            let output = run(mode);
            assert!(
                output.status.success(),
                "{mode}: stderr={}",
                String::from_utf8_lossy(&output.stderr)
            );
            assert_eq!(output.stdout, expected.as_bytes(), "{mode}");
            assert!(output.stderr.is_empty(), "{mode}");
        }
    }
}
