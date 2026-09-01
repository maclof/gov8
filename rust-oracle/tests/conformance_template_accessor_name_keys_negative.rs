use std::process::Command;

#[test]
fn accessor_property_requires_getter_or_setter() {
    for mode in ["none-none-object", "none-none-function"] {
        let output = Command::new(env!(
            "CARGO_BIN_EXE_conformance-template-accessor-name-keys"
        ))
        .args(["--negative", mode])
        .output()
        .expect("failed to execute accessor-property negative probe");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert_eq!(output.status.code(), Some(101), "{mode}: {stderr}");
        assert!(
            stderr.contains("assertion failed: getter.is_some() || setter.is_some()"),
            "{mode}: {stderr}"
        );
        assert!(output.stdout.is_empty(), "{mode}");
    }
}
