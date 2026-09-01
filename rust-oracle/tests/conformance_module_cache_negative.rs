fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-module-cache-negative"))
        .arg(mode)
        .output()
        .unwrap()
}

#[test]
fn malformed_and_mismatched_module_caches_are_process_isolated() {
    for (mode, expected) in [
        ("changed-source", "compiled=true rejected=false answer=42"),
        ("truncated", "compiled=true rejected=true answer=42"),
        ("corrupt", "compiled=true rejected=false answer=42"),
    ] {
        let output = run(mode);
        assert!(
            output.status.success(),
            "{mode}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert_eq!(
            String::from_utf8_lossy(&output.stdout).trim(),
            expected,
            "{mode}"
        );
    }
}
