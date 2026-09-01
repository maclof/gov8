//! Exact-output tests for the advanced exceptions oracle slice.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-exceptions-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

#[test]
fn exceptions_advanced_binary_stdout_matches_fixture() {
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_conformance-exceptions-advanced"))
        .output()
        .expect("failed to run conformance-exceptions-advanced");
    let stdout = String::from_utf8(output.stdout).expect("stdout was not UTF-8");
    assert!(
        output.status.success(),
        "advanced exceptions binary failed; stdout:\n{stdout}"
    );
    assert_eq!(stdout, FIXTURE, "advanced exceptions fixture drifted");
}

#[test]
fn exceptions_advanced_binary_is_deterministic_across_processes() {
    let run = || {
        std::process::Command::new(env!("CARGO_BIN_EXE_conformance-exceptions-advanced"))
            .output()
            .expect("failed to run conformance-exceptions-advanced")
    };
    let first = run();
    let second = run();
    assert!(
        first.status.success(),
        "first advanced exceptions process failed; stdout:\n{}",
        String::from_utf8_lossy(&first.stdout)
    );
    assert!(
        second.status.success(),
        "second advanced exceptions process failed; stdout:\n{}",
        String::from_utf8_lossy(&second.stdout)
    );
    assert_eq!(first.stdout, second.stdout, "process outputs diverged");
    assert_eq!(first.stdout, FIXTURE.as_bytes(), "process output drifted");
}

#[test]
fn exceptions_advanced_fixture_shape_and_coverage() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(
        lines.len(),
        11,
        "fixture must contain ten checks and summary"
    );
    let checks = &lines[..lines.len() - 1];
    for line in checks {
        assert!(
            line.starts_with("{\"check\":\"exceptions-advanced/"),
            "unexpected fixture line: {line}"
        );
        assert!(
            line.contains("\"ok\":true"),
            "fixture records failure: {line}"
        );
    }
    let expected_ids = [
        "exceptions-advanced/try-catch/empty_toggle_and_reset",
        "exceptions-advanced/try-catch/verbose_reporting",
        "exceptions-advanced/try-catch/runtime_exception_details",
        "exceptions-advanced/try-catch/syntax_exception_details",
        "exceptions-advanced/try-catch/capture_message_disabled",
        "exceptions-advanced/try-catch/rethrow_propagation",
        "exceptions-advanced/try-catch/caught_local_lifetime",
        "exceptions-advanced/message/source_url_fallback",
        "exceptions-advanced/stack/current_frames_and_limits",
        "exceptions-advanced/message/wasm_trap",
    ];
    let actual_ids: Vec<_> = checks
        .iter()
        .map(|line| {
            line.strip_prefix("{\"check\":\"")
                .and_then(|rest| rest.split_once('"').map(|(id, _)| id))
                .expect("check line must contain a check id")
        })
        .collect();
    assert_eq!(
        actual_ids, expected_ids,
        "check order or membership drifted"
    );
    for area in ["/try-catch/", "/message/", "/stack/"] {
        assert!(
            checks.iter().any(|line| line.contains(area)),
            "missing {area} coverage"
        );
    }
    for required_observation in [
        "\"verbose_true\"",
        "\"reported_text\"",
        "\"message_none\"",
        "\"returned_is_undefined\"",
        "caught_local_lifetime",
        "\"source_line\"",
        "\"frame_script_name_or_source_url\"",
        "\"source_map_url\"",
        "\"is_constructor\"",
        "\"is_eval\"",
        "\"is_wasm\"",
        "\"wasm_function_index\"",
        "\"limit_zero\"",
        "\"overflow_none\"",
    ] {
        assert!(
            FIXTURE.contains(required_observation),
            "missing required observation {required_observation}"
        );
    }
    let total = checks.len();
    let expected_summary =
        format!("{{\"summary\":{{\"total\":{total},\"passed\":{total},\"failed\":0}}}}");
    assert_eq!(lines.last().copied(), Some(expected_summary.as_str()));
}
