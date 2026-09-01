#[cfg(windows)]
#[test]
fn private_for_api_none_access_violates() {
    let status =
        std::process::Command::new(env!("CARGO_BIN_EXE_runtime-values-residual-private-none"))
            .status()
            .expect("run Private::for_api(None) probe");
    assert_eq!(status.code(), Some(-1073741819)); // NTSTATUS 0xC0000005
}
