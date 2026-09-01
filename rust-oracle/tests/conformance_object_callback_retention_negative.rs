fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-object-callback-retention"))
        .args(["--negative", mode])
        .output()
        .unwrap()
}

#[test]
fn accessor_and_lazy_callback_panics_fail_fast() {
    for (mode, entered, panic_text) in [
        (
            "accessor-getter-panic",
            "marker:accessor-getter-entered",
            "accessor-getter-panic",
        ),
        (
            "accessor-setter-panic",
            "marker:accessor-setter-entered",
            "accessor-setter-panic",
        ),
        (
            "lazy-getter-panic",
            "marker:lazy-getter-entered",
            "lazy-getter-panic",
        ),
    ] {
        let output = run(mode);
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert_eq!(output.status.code(), Some(0xC000_0409_u32 as i32), "{mode}");
        assert!(stderr.contains(&format!("marker:before-{mode}")), "{mode}");
        assert!(stderr.contains(entered), "{mode}");
        assert!(stderr.contains(panic_text), "{mode}");
        assert!(
            stderr.contains("panic in a function that cannot unwind"),
            "{mode}"
        );
        assert!(!stderr.contains(&format!("marker:after-{mode}")), "{mode}");
        assert!(output.stdout.is_empty(), "{mode}");
    }
}

#[test]
fn invalid_lazy_setter_side_effect_is_v8_fatal() {
    let output = run("lazy-invalid-setter-side-effect");
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert_eq!(output.status.code(), Some(0x8000_0003_u32 as i32));
    assert!(stderr.contains("marker:before-lazy-invalid-setter-side-effect"));
    assert!(stderr.contains("Check failed: value != SideEffectType::kHasNoSideEffect"));
    assert!(!stderr.contains("marker:after-lazy-invalid-setter-side-effect"));
}

#[test]
fn js_receiver_values_are_rejected_by_template_set() {
    for mode in ["template-object-value", "template-function-value"] {
        let output = run(mode);
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert_eq!(output.status.code(), Some(0x8000_0003_u32 as i32), "{mode}");
        assert!(
            stderr.contains("Fatal error in v8::Template::Set"),
            "{mode}"
        );
        assert!(
            stderr.contains("Invalid value, must be a primitive or a Template"),
            "{mode}"
        );
        assert!(!stderr.contains(&format!("marker:after-{mode}")), "{mode}");
    }
}

#[test]
fn representative_non_value_data_is_stored_but_not_a_value() {
    for (mode, expected) in [
        (
            "template-context-data",
            "survived instance=true is_context=true is_value=false\n",
        ),
        (
            "template-primitive-array-data",
            "survived instance=true is_fixed_array=true is_value=false\n",
        ),
    ] {
        for _ in 0..2 {
            let output = run(mode);
            assert!(output.status.success(), "{mode}");
            assert_eq!(output.stdout, expected.as_bytes(), "{mode}");
            let stderr = String::from_utf8_lossy(&output.stderr);
            assert!(stderr.contains(&format!("marker:before-{mode}")), "{mode}");
            assert!(stderr.contains(&format!("marker:after-{mode}")), "{mode}");
        }
    }
}
