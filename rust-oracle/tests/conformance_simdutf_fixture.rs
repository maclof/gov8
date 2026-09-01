const FIXTURE: &str =
    include_str!("fixtures/conformance-simdutf-v8_152.2.0_x86_64-pc-windows-msvc.jsonl");

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-simdutf"))
        .output()
        .unwrap()
}

#[test]
fn simdutf_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stdout)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn simdutf_is_deterministic_and_ordered() {
    let first = run();
    let second = run();
    assert!(first.status.success() && second.status.success());
    assert_eq!(first.stdout, second.stdout);
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 6);
    for (line, id) in lines[..5].iter().zip([
        "simdutf/validation",
        "simdutf/unicode_conversions",
        "simdutf/latin1_conversions",
        "simdutf/lengths_counts_detection",
        "simdutf/base64",
    ]) {
        assert!(line.starts_with(&format!("{{\"check\":\"{id}\",\"ok\":true")));
    }
    assert_eq!(
        lines[5],
        "{\"summary\":{\"total\":5,\"passed\":5,\"failed\":0}}"
    );
}
