//! Exact-output checks for residual advanced module APIs.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-module-advanced-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-module-advanced-residual"))
        .output()
        .expect("failed to run advanced residual module oracle")
}

#[test]
fn stdout_matches_fixture() {
    let output = run();
    assert!(output.status.success());
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn fixture_shape_and_order() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    let ids = [
        "instantiate2_source_phase",
        "instantiate2_source_exception",
        "deferred_namespace",
        "stalled_top_level_await",
        "deferred_exception",
        "stalled_tla_resolution_lifecycle",
        "import_meta_callback",
        "dynamic_import_callbacks",
        "shadow_realm_callback",
    ];
    assert_eq!(lines.len(), ids.len() + 1);
    for (line, id) in lines.iter().zip(ids) {
        assert!(line.starts_with(&format!(
            "{{\"check\":\"module-advanced-residual/{id}\",\"ok\":true"
        )));
    }
    assert_eq!(
        lines[ids.len()],
        "{\"summary\":{\"total\":9,\"passed\":9,\"failed\":0}}"
    );
}

#[test]
fn fixture_is_deterministic_across_processes() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(first.stdout, FIXTURE.as_bytes());
}
