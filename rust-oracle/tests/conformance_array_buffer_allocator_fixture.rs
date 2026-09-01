//! Exact-output checks for custom ArrayBuffer allocator behavior.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-array-buffer-allocator-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-array-buffer-allocator"))
        .output()
        .expect("failed to run ArrayBuffer allocator oracle")
}

#[test]
fn stdout_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "stderr: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn fixture_shape_and_order() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    let ids = [
        "pin_and_default_factory",
        "callbacks_zero_and_transfer",
        "standalone_backing_store_free",
        "isolate_teardown_owns_allocator",
        "backing_store_outlives_isolate",
        "shared_allocator_across_isolate_threads",
    ];
    assert_eq!(lines.len(), ids.len() + 1);
    for (line, id) in lines.iter().zip(ids) {
        assert!(line.starts_with(&format!(
            "{{\"check\":\"array-buffer-allocator/{id}\",\"ok\":true"
        )));
    }
    assert_eq!(
        lines[ids.len()],
        "{\"summary\":{\"total\":6,\"passed\":6,\"failed\":0}}"
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
