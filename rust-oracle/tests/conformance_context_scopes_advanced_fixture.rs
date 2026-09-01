//! Exact deterministic fixture tests for context/scope/microtask completion.

const FIXTURE: &str = include_str!(
    "fixtures/conformance-context-scopes-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
);

fn run() -> std::process::Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-context-scopes-advanced"))
        .output()
        .expect("failed to run conformance-context-scopes-advanced")
}

#[test]
fn context_scopes_advanced_stdout_matches_fixture() {
    let output = run();
    assert!(
        output.status.success(),
        "binary failed; stdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, FIXTURE.as_bytes());
}

#[test]
fn context_scopes_advanced_is_deterministic_across_processes() {
    let first = run();
    let second = run();
    assert!(first.status.success(), "first process failed");
    assert!(second.status.success(), "second process failed");
    assert_eq!(first.stdout, second.stdout, "process outputs diverged");
    assert_eq!(first.stdout, FIXTURE.as_bytes(), "fixture drifted");
}

#[test]
fn context_scopes_advanced_fixture_has_exact_shape_and_order() {
    let lines: Vec<_> = FIXTURE.lines().collect();
    assert_eq!(lines.len(), 9, "eight checks plus summary required");
    let expected_ids = [
        "context-scopes-advanced/context/options_global_template_and_extras",
        "context-scopes-advanced/context/options_global_object_reuse",
        "context-scopes-advanced/microtask/options_distinct_queues",
        "context-scopes-advanced/microtask/options_shared_queue",
        "context-scopes-advanced/context/continuation_preserved_data",
        "context-scopes-advanced/microtask/running_and_scope_depth",
        "context-scopes-advanced/context/promise_hooks",
        "context-scopes-advanced/scope/disallow_allow_nesting",
    ];
    let checks = &lines[..8];
    let actual_ids: Vec<_> = checks
        .iter()
        .map(|line| {
            assert!(line.contains("\"ok\":true"), "fixture failure: {line}");
            line.strip_prefix("{\"check\":\"")
                .and_then(|rest| rest.split_once('"').map(|(id, _)| id))
                .expect("check id")
        })
        .collect();
    assert_eq!(actual_ids, expected_ids);
    assert_eq!(
        lines[8],
        "{\"summary\":{\"total\":8,\"passed\":8,\"failed\":0}}"
    );
    for token in [
        "global_template",
        "extras_identity_stable",
        "global_identity_reused",
        "queue_a_attached_at_creation",
        "contexts_share_queue",
        "visible_in_second_context",
        "inside_running",
        "inside_depth",
        "disable_stops_hooks",
        "disallowed_again",
    ] {
        assert!(FIXTURE.contains(token), "missing observation: {token}");
    }
}
