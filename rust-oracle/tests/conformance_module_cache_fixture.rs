const FIXTURE: &str =
    include_str!("fixtures/conformance-module-cache-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-module-cache"))
        .output()
        .unwrap()
}

#[test]
fn module_cache_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stdout)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn module_cache_is_deterministic_across_processes() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    assert_eq!(FIXTURE.lines().count(), 4);
}
