use std::process::Command;

#[test]
fn duplicate_string_and_symbol_names_are_fatal_on_instantiation() {
    for mode in ["duplicate-string", "duplicate-symbol"] {
        let output = Command::new(env!("CARGO_BIN_EXE_conformance-template-name-keys"))
            .args(["--negative", mode])
            .output()
            .expect("failed to execute duplicate Name-key probe");
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert_eq!(output.status.code(), Some(0x8000_0003_u32 as i32), "{mode}");
        assert!(
            stderr.contains(&format!("marker:before-instantiation:{mode}")),
            "{mode}: {stderr}"
        );
        assert!(
            stderr.contains(
                "Check failed: LinearSearch(*desc->GetKey(), descriptor_number) == InternalIndex::NotFound()."
            ),
            "{mode}: {stderr}"
        );
        assert!(
            !stderr.contains(&format!("marker:after-instantiation:{mode}")),
            "{mode}: {stderr}"
        );
        assert!(output.stdout.is_empty(), "{mode}");
    }
}
