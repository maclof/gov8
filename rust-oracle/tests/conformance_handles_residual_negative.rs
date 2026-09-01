//! Stable boundary observations kept out of the deterministic fixture.

fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-handles-residual-negative"))
        .arg(mode)
        .output()
        .unwrap_or_else(|error| panic!("failed to run {mode}: {error}"))
}

#[test]
fn eternal_double_set_overwrites_on_pinned_v8_build() {
    for _ in 0..3 {
        let output = run("eternal-double-set");
        assert!(output.status.success());
        assert_eq!(output.stdout, b"second\n");
        assert!(output.stderr.is_empty());
    }
}

#[test]
fn nonempty_eternal_standalone_methods_survive_isolate_drop() {
    for _ in 0..3 {
        let output = run("eternal-standalone");
        assert!(output.status.success());
        assert_eq!(
            output.stdout,
            b"empty-before-clear=false\nempty-after-clear=true\n"
        );
        assert!(output.stderr.is_empty());
    }
}

#[test]
fn dropping_nonempty_traced_reference_after_isolate_survives() {
    for _ in 0..3 {
        let output = run("traced-drop-after-isolate");
        assert!(output.status.success());
        assert_eq!(output.stdout, b"dropped\n");
        assert!(output.stderr.is_empty());
    }
}
