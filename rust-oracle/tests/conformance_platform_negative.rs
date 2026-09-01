#[test]
fn single_threaded_platform_without_required_flag_access_violates() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-platform"))
        .arg("--missing-single-threaded-flag")
        .output()
        .unwrap();
    assert!(!output.status.success());
    assert_eq!(output.status.code(), Some(0xC000_0005_u32 as i32));
    assert!(output.stdout.is_empty());
    assert!(output.stderr.is_empty());
}
