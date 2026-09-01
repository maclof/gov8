const FIXTURE: &str = include_str!(
    "fixtures/conformance-script-compiler-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-script-compiler-residual"))
        .output()
        .unwrap()
}

#[test]
fn script_compiler_residual_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "stdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn script_compiler_residual_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 8);
    for (line, id) in lines[..7].iter().zip([
        "script-compiler-residual/origin_arbitrary_values",
        "script-compiler-residual/host_defined_options",
        "script-compiler-residual/compile_options",
        "script-compiler-residual/no_cache_reasons",
        "script-compiler-residual/cache_origin_source_mismatch",
        "script-compiler-residual/syntax_failure_source_state",
        "script-compiler-residual/permissive_boundaries",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[7],
        "{\"summary\":{\"total\":7,\"passed\":7,\"failed\":0}}"
    );
}
