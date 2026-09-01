#[test]
fn panic_in_platform_callback_aborts() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-platform-custom"))
        .args(["--child", "panic"])
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(0xC000_0409_u32 as i32));
    assert!(String::from_utf8_lossy(&output.stderr).contains("platform callback panic marker"));
}
