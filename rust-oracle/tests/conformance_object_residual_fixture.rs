const FIXTURE: &str =
    include_str!("fixtures/conformance-object-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-object-residual"))
        .output()
        .unwrap()
}

#[test]
fn object_residual_matches_fixture() {
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
fn object_residual_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 5);
    assert!(lines[0].starts_with(
        "{\"check\":\"object-residual/constructor/prototype_properties\",\"ok\":true"
    ));
    assert!(lines[1].starts_with("{\"check\":\"object-residual/names/own_filters\",\"ok\":true"));
    assert!(lines[2].starts_with("{\"check\":\"object-residual/preview/collections\",\"ok\":true"));
    assert!(lines[3]
        .starts_with("{\"check\":\"object-residual/api_wrapper/classification\",\"ok\":true"));
    assert_eq!(
        lines[4],
        "{\"summary\":{\"total\":4,\"passed\":4,\"failed\":0}}"
    );
}
