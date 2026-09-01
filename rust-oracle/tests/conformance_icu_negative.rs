fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-icu-negative"))
        .arg(mode)
        .output()
        .unwrap()
}

#[test]
fn valid_common_data_initializes_in_fresh_process() {
    let output = run("common-data-valid");
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        output.stdout,
        b"common=Ok(());align=0;locale=nb-NO;timezone_set=true;timezone=UTC;intl=1,234.5\n"
    );
}

#[test]
fn locale_interior_nul_panics_without_crossing_ffi() {
    let output = run("locale-interior-nul");
    assert!(!output.status.success());
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Invalid locale"), "{stderr}");
}

#[test]
fn boundary_probes_are_process_isolated() {
    for mode in ["locale-overlong", "locale-malformed"] {
        let output = run(mode);
        assert!(output.status.success(), "{mode}");
        assert_eq!(output.stdout, b"und\n", "{mode}");
    }

    let misaligned = run("common-data-misaligned");
    assert!(misaligned.status.success());
    assert_eq!(misaligned.stdout, b"Err(3)\n");

    let empty = run("common-data-empty");
    const WINDOWS_ACCESS_VIOLATION: i32 = -1_073_741_819; // 0xC0000005
    assert_eq!(empty.status.code(), Some(WINDOWS_ACCESS_VIOLATION));
    assert!(empty.stdout.is_empty());
    assert!(empty.stderr.is_empty());
}
