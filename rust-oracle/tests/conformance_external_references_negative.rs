fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!(
        "CARGO_BIN_EXE_conformance-external-references-negative"
    ))
    .arg(mode)
    .output()
    .unwrap()
}

#[test]
fn missing_table_is_fatal_but_short_table_maps_missing_pointer_to_null() {
    let missing = run("missing-table");
    assert!(!missing.status.success());
    assert!(
        String::from_utf8_lossy(&missing.stderr)
            .contains("No external references provided via API"),
        "{}",
        String::from_utf8_lossy(&missing.stderr)
    );

    let short = run("short-table");
    assert!(short.status.success());
    assert_eq!(short.stdout, b"0\n");
}
