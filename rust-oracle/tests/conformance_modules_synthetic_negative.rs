fn run(mode: &str) -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-modules-synthetic-negative"))
        .arg(mode)
        .output()
        .unwrap()
}

#[test]
fn callback_returning_none_without_exception_is_v8_fatal() {
    let output = run("none");
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!output.status.success());
    assert_eq!(stdout, "created\ninstantiated\n");
    assert!(stderr.contains("Fatal error"));
    assert!(stderr.contains("Check failed: has_exception()."));
}

#[test]
fn duplicate_names_callback_panic_and_cross_isolate_are_process_fatal() {
    let duplicate = run("duplicate");
    let duplicate_stdout = String::from_utf8_lossy(&duplicate.stdout);
    let duplicate_stderr = String::from_utf8_lossy(&duplicate.stderr);
    assert!(!duplicate.status.success());
    assert_eq!(duplicate_stdout, "created\n");
    assert!(duplicate_stderr.contains("Check failed: IsTheHole(exports->Lookup(name))."));

    let panic = run("panic");
    let panic_stdout = String::from_utf8_lossy(&panic.stdout);
    let panic_stderr = String::from_utf8_lossy(&panic.stderr);
    assert!(!panic.status.success());
    assert_eq!(panic_stdout, "created\ninstantiated\n");
    assert!(panic_stderr.contains("synthetic callback panic"));
    assert!(panic_stderr.contains("panic in a function that cannot unwind"));

    let cross = run("cross-isolate");
    let cross_stderr = String::from_utf8_lossy(&cross.stderr);
    assert_eq!(cross.status.code(), Some(101));
    assert!(cross_stderr.contains("attempt to access Handle hosted by disposed Isolate"));
}
