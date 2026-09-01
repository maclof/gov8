//! Classic source-text ES module conformance for `v8` =152.2.0.
//!
//! This deliberately covers the synchronous/static module embedding surface:
//! `ScriptOrigin`-backed compilation, request metadata, resolve callbacks,
//! status transitions, namespaces, evaluation promises, and stable failure
//! modes. Dynamic import, source-phase/deferred import, synthetic modules,
//! code cache, and stalled top-level-await diagnostics remain separate work.

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

fn status(status: v8::ModuleStatus) -> &'static str {
    match status {
        v8::ModuleStatus::Uninstantiated => "Uninstantiated",
        v8::ModuleStatus::Instantiating => "Instantiating",
        v8::ModuleStatus::Instantiated => "Instantiated",
        v8::ModuleStatus::Evaluating => "Evaluating",
        v8::ModuleStatus::Evaluated => "Evaluated",
        v8::ModuleStatus::Errored => "Errored",
    }
}

fn promise_state(state: v8::PromiseState) -> &'static str {
    match state {
        v8::PromiseState::Pending => "Pending",
        v8::PromiseState::Fulfilled => "Fulfilled",
        v8::PromiseState::Rejected => "Rejected",
    }
}

fn import_phase(phase: v8::ModuleImportPhase) -> &'static str {
    match phase {
        v8::ModuleImportPhase::kSource => "Source",
        v8::ModuleImportPhase::kDefer => "Defer",
        v8::ModuleImportPhase::kEvaluation => "Evaluation",
    }
}

fn origin<'s>(
    scope: &v8::PinScope<'s, '_>,
    name: &str,
    line_offset: i32,
    column_offset: i32,
) -> v8::ScriptOrigin<'s> {
    let resource_name = v8::String::new(scope, name).unwrap().into();
    v8::ScriptOrigin::new(
        scope,
        resource_name,
        line_offset,
        column_offset,
        false,
        -1,
        None,
        false,
        false,
        true,
        None,
    )
}

fn compile<'s>(
    scope: &v8::PinScope<'s, '_>,
    name: &str,
    source_text: &str,
) -> Option<v8::Local<'s, v8::Module>> {
    let source_text = v8::String::new(scope, source_text).unwrap();
    let script_origin = origin(scope, name, 0, 0);
    let mut source = v8::script_compiler::Source::new(source_text, Some(&script_origin));
    v8::script_compiler::compile_module(scope, &mut source)
}

#[allow(clippy::unnecessary_wraps)]
fn compile_specifier<'s>(
    context: v8::Local<'s, v8::Context>,
    specifier: v8::Local<'s, v8::String>,
    _import_attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    v8::callback_scope!(unsafe scope, context);
    let script_origin = origin(scope, "dependency.mjs", 0, 0);
    let mut source = v8::script_compiler::Source::new(specifier, Some(&script_origin));
    v8::script_compiler::compile_module(scope, &mut source)
}

#[allow(clippy::unnecessary_wraps)]
fn unexpected_resolve<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _import_attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    panic!("resolve callback must not run for a module without requests")
}

fn throwing_resolve<'s>(
    context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _import_attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    v8::callback_scope!(unsafe scope, context);
    let exception = v8::String::new(scope, "link boom").unwrap();
    scope.throw_exception(exception.into());
    None
}

fn compile_and_requests() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let source = concat!(
        "import './side.mjs';\n",
        "export { value } from './dep.mjs' with { kind: 'fixture' };"
    );
    let module = compile(scope, "requests.mjs", source).unwrap();
    let requests = module.get_module_requests();
    let mut normalized = Vec::new();
    for index in 0..requests.length() {
        let request =
            v8::Local::<v8::ModuleRequest>::try_from(requests.get(scope, index).unwrap()).unwrap();
        let location = module.source_offset_to_location(request.get_source_offset());
        let attributes = request.get_import_attributes();
        let mut normalized_attributes = Vec::new();
        for attribute_index in 0..attributes.length() {
            let data = attributes.get(scope, attribute_index).unwrap();
            let value = v8::Local::<v8::Value>::try_from(data).unwrap();
            normalized_attributes.push(Json::s(&value.to_rust_string_lossy(scope)));
        }
        normalized.push(Json::obj(vec![
            (
                "specifier",
                Json::s(&request.get_specifier().to_rust_string_lossy(scope)),
            ),
            ("phase", Json::s(import_phase(request.get_phase()))),
            ("line", Json::i(i64::from(location.get_line_number()))),
            ("column", Json::i(i64::from(location.get_column_number()))),
            ("attributes", Json::arr(normalized_attributes)),
        ]));
    }

    let actual = Json::obj(vec![
        ("status", Json::s(status(module.get_status()))),
        ("source_text", Json::b(module.is_source_text_module())),
        ("synthetic", Json::b(module.is_synthetic_module())),
        (
            "script_id_positive",
            Json::b(module.script_id().unwrap_or(-1) > 0),
        ),
        ("request_count", Json::i(requests.length() as i64)),
        ("requests", Json::arr(normalized)),
    ]);
    let expected = Json::obj(vec![
        ("status", Json::s("Uninstantiated")),
        ("source_text", Json::b(true)),
        ("synthetic", Json::b(false)),
        ("script_id_positive", Json::b(true)),
        ("request_count", Json::i(2)),
        (
            "requests",
            Json::arr(vec![
                Json::obj(vec![
                    ("specifier", Json::s("./side.mjs")),
                    ("phase", Json::s("Evaluation")),
                    ("line", Json::i(0)),
                    ("column", Json::i(7)),
                    ("attributes", Json::arr(vec![])),
                ]),
                Json::obj(vec![
                    ("specifier", Json::s("./dep.mjs")),
                    ("phase", Json::s("Evaluation")),
                    ("line", Json::i(1)),
                    ("column", Json::i(22)),
                    (
                        "attributes",
                        Json::arr(vec![Json::s("kind"), Json::s("fixture"), Json::s("62")]),
                    ),
                ]),
            ]),
        ),
    ]);
    vec![expect_eq("modules/compile_requests", expected, actual)]
}

fn link_evaluate_namespace() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let source = concat!(
        "import { base } from 'export const base = 40;';",
        "export let answer = base + 2; answer += 1;"
    );
    let module = compile(scope, "entry.mjs", source).unwrap();
    let before = status(module.get_status());
    let instantiated = module.instantiate_module(scope, compile_specifier);
    let after_link = status(module.get_status());
    let namespace_before = module.get_module_namespace();
    let evaluation = module.evaluate(scope).unwrap();
    let evaluate_returns_promise = evaluation.is_promise();
    let promise = v8::Local::<v8::Promise>::try_from(evaluation).unwrap();
    scope.perform_microtask_checkpoint();
    let namespace_after = module.get_module_namespace();
    let namespace = v8::Local::<v8::Object>::try_from(namespace_after).unwrap();
    let answer_key: v8::Local<v8::Value> = v8::String::new(scope, "answer").unwrap().into();
    let answer = namespace
        .get(scope, answer_key)
        .unwrap()
        .integer_value(scope)
        .unwrap();

    let actual = Json::obj(vec![
        ("before", Json::s(before)),
        ("instantiate_result", Json::b(instantiated == Some(true))),
        ("after_link", Json::s(after_link)),
        (
            "namespace_stable",
            Json::b(namespace_before.strict_equals(namespace_after)),
        ),
        (
            "evaluate_returns_promise",
            Json::b(evaluate_returns_promise),
        ),
        ("promise_state", Json::s(promise_state(promise.state()))),
        ("after_evaluate", Json::s(status(module.get_status()))),
        ("answer", Json::i(answer)),
    ]);
    let expected = Json::obj(vec![
        ("before", Json::s("Uninstantiated")),
        ("instantiate_result", Json::b(true)),
        ("after_link", Json::s("Instantiated")),
        ("namespace_stable", Json::b(true)),
        ("evaluate_returns_promise", Json::b(true)),
        ("promise_state", Json::s("Fulfilled")),
        ("after_evaluate", Json::s("Evaluated")),
        ("answer", Json::i(43)),
    ]);
    vec![expect_eq(
        "modules/link_evaluate_namespace",
        expected,
        actual,
    )]
}

fn syntax_failure() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    v8::tc_scope!(let tc, scope);

    let source_text = v8::String::new(tc, "export const = 1;").unwrap();
    let script_origin = origin(tc, "syntax.mjs", 3, 5);
    let mut source = v8::script_compiler::Source::new(source_text, Some(&script_origin));
    let compiled = v8::script_compiler::compile_module(tc, &mut source);
    let message = tc.message().unwrap();
    let actual = Json::obj(vec![
        ("compiled", Json::b(compiled.is_some())),
        ("caught", Json::b(tc.has_caught())),
        (
            "exception",
            Json::s(&tc.exception().unwrap().to_rust_string_lossy(tc)),
        ),
        (
            "message",
            Json::s(&message.get(tc).to_rust_string_lossy(tc)),
        ),
        (
            "line",
            Json::i(message.get_line_number(tc).map_or(-1, |line| line as i64)),
        ),
        ("start_column", Json::i(message.get_start_column() as i64)),
        (
            "resource",
            Json::s(
                &message
                    .get_script_resource_name(tc)
                    .unwrap()
                    .to_rust_string_lossy(tc),
            ),
        ),
    ]);
    let expected = Json::obj(vec![
        ("compiled", Json::b(false)),
        ("caught", Json::b(true)),
        ("exception", Json::s("SyntaxError: Unexpected token '='")),
        (
            "message",
            Json::s("Uncaught SyntaxError: Unexpected token '='"),
        ),
        ("line", Json::i(4)),
        ("start_column", Json::i(18)),
        ("resource", Json::s("syntax.mjs")),
    ]);
    vec![expect_eq("modules/syntax_failure", expected, actual)]
}

fn negative_origin_offsets() -> Vec<CheckOutcome> {
    fn observe(line_offset: i32, column_offset: i32) -> Json {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        v8::tc_scope!(let tc, scope);

        let source_text = v8::String::new(tc, "export const = 1;").unwrap();
        let script_origin = origin(tc, "negative-offset.mjs", line_offset, column_offset);
        let mut source = v8::script_compiler::Source::new(source_text, Some(&script_origin));
        let compiled = v8::script_compiler::compile_module(tc, &mut source);
        let message = tc.message();
        Json::obj(vec![
            ("compiled", Json::b(compiled.is_some())),
            ("caught", Json::b(tc.has_caught())),
            (
                "exception",
                tc.exception().map_or(Json::Null, |exception| {
                    Json::s(&exception.to_rust_string_lossy(tc))
                }),
            ),
            (
                "message",
                message.map_or(Json::Null, |message| {
                    Json::s(&message.get(tc).to_rust_string_lossy(tc))
                }),
            ),
            (
                "line",
                message.map_or(Json::Null, |message| {
                    message
                        .get_line_number(tc)
                        .map_or(Json::Null, |line| Json::i(line as i64))
                }),
            ),
            (
                "start_column",
                message.map_or(Json::Null, |message| {
                    Json::i(message.get_start_column() as i64)
                }),
            ),
            (
                "start_position",
                message.map_or(Json::Null, |message| {
                    Json::i(i64::from(message.get_start_position()))
                }),
            ),
            (
                "resource",
                message.map_or(Json::Null, |message| {
                    message
                        .get_script_resource_name(tc)
                        .map_or(Json::Null, |resource| {
                            Json::s(&resource.to_rust_string_lossy(tc))
                        })
                }),
            ),
        ])
    }

    let actual = Json::obj(vec![
        ("line_minus_one", observe(-1, 0)),
        ("column_minus_one", observe(0, -1)),
    ]);
    // Filled from the pinned engine's observed output; negative offsets are
    // accepted by ScriptOrigin and affect only the projected diagnostic
    // coordinates, not parsing or the source-relative start position.
    let expected = Json::obj(vec![
        (
            "line_minus_one",
            Json::obj(vec![
                ("compiled", Json::b(false)),
                ("caught", Json::b(true)),
                ("exception", Json::s("SyntaxError: Unexpected token '='")),
                (
                    "message",
                    Json::s("Uncaught SyntaxError: Unexpected token '='"),
                ),
                ("line", Json::i(0)),
                ("start_column", Json::i(13)),
                ("start_position", Json::i(13)),
                ("resource", Json::s("negative-offset.mjs")),
            ]),
        ),
        (
            "column_minus_one",
            Json::obj(vec![
                ("compiled", Json::b(false)),
                ("caught", Json::b(true)),
                ("exception", Json::s("SyntaxError: Unexpected token '='")),
                (
                    "message",
                    Json::s("Uncaught SyntaxError: Unexpected token '='"),
                ),
                ("line", Json::i(1)),
                ("start_column", Json::i(12)),
                ("start_position", Json::i(13)),
                ("resource", Json::s("negative-offset.mjs")),
            ]),
        ),
    ]);
    vec![expect_eq(
        "modules/negative_origin_offsets",
        expected,
        actual,
    )]
}

fn repeated_evaluate() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = compile(
        scope,
        "repeat.mjs",
        concat!(
            "globalThis.__module_runs = (globalThis.__module_runs || 0) + 1;",
            "export const answer = 43;"
        ),
    )
    .unwrap();
    module
        .instantiate_module(scope, unexpected_resolve)
        .unwrap();

    let first_value = module.evaluate(scope).unwrap();
    let first_promise = v8::Local::<v8::Promise>::try_from(first_value).unwrap();
    scope.perform_microtask_checkpoint();
    let first_state = promise_state(first_promise.state());
    let namespace_before = module.get_module_namespace();
    let namespace_object = v8::Local::<v8::Object>::try_from(namespace_before).unwrap();
    let answer_key: v8::Local<v8::Value> = v8::String::new(scope, "answer").unwrap().into();
    let answer_before = namespace_object
        .get(scope, answer_key)
        .unwrap()
        .integer_value(scope)
        .unwrap();

    v8::tc_scope!(let tc, scope);
    let second_value = module.evaluate(tc);
    let second_is_some = second_value.is_some();
    let second_is_promise = second_value.is_some_and(|value| value.is_promise());
    let second_promise =
        second_value.and_then(|value| v8::Local::<v8::Promise>::try_from(value).ok());
    let second_state = second_promise.map_or("None", |promise| promise_state(promise.state()));
    let promise_identity = second_value.is_some_and(|value| first_value.strict_equals(value));
    let caught = tc.has_caught();
    let exception = tc.exception().map_or(Json::Null, |exception| {
        Json::s(&exception.to_rust_string_lossy(tc))
    });
    tc.perform_microtask_checkpoint();
    let namespace_after = module.get_module_namespace();
    let namespace_after_object = v8::Local::<v8::Object>::try_from(namespace_after).unwrap();
    let answer_key: v8::Local<v8::Value> = v8::String::new(tc, "answer").unwrap().into();
    let answer_after = namespace_after_object
        .get(tc, answer_key)
        .unwrap()
        .integer_value(tc)
        .unwrap();
    let runs_key: v8::Local<v8::Value> = v8::String::new(tc, "__module_runs").unwrap().into();
    let runs = context
        .global(tc)
        .get(tc, runs_key)
        .unwrap()
        .integer_value(tc)
        .unwrap();

    let actual = Json::obj(vec![
        ("second_is_some", Json::b(second_is_some)),
        ("second_is_promise", Json::b(second_is_promise)),
        ("status", Json::s(status(module.get_status()))),
        ("first_promise_state", Json::s(first_state)),
        ("second_promise_state", Json::s(second_state)),
        ("same_promise", Json::b(promise_identity)),
        (
            "namespace_stable",
            Json::b(namespace_before.strict_equals(namespace_after)),
        ),
        ("answer_before", Json::i(answer_before)),
        ("answer_after", Json::i(answer_after)),
        ("evaluation_count", Json::i(runs)),
        ("caught", Json::b(caught)),
        ("exception", exception),
    ]);
    let expected = Json::obj(vec![
        ("second_is_some", Json::b(true)),
        ("second_is_promise", Json::b(true)),
        ("status", Json::s("Evaluated")),
        ("first_promise_state", Json::s("Fulfilled")),
        ("second_promise_state", Json::s("Fulfilled")),
        ("same_promise", Json::b(true)),
        ("namespace_stable", Json::b(true)),
        ("answer_before", Json::i(43)),
        ("answer_after", Json::i(43)),
        ("evaluation_count", Json::i(1)),
        ("caught", Json::b(false)),
        ("exception", Json::Null),
    ]);
    vec![expect_eq("modules/repeated_evaluate", expected, actual)]
}

fn link_failure() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = compile(scope, "link.mjs", "import './missing.mjs';").unwrap();
    v8::tc_scope!(let tc, scope);
    let result = module.instantiate_module(tc, throwing_resolve);
    let actual = Json::obj(vec![
        ("result_is_none", Json::b(result.is_none())),
        ("caught", Json::b(tc.has_caught())),
        (
            "exception",
            Json::s(&tc.exception().unwrap().to_rust_string_lossy(tc)),
        ),
        ("status", Json::s(status(module.get_status()))),
    ]);
    let expected = Json::obj(vec![
        ("result_is_none", Json::b(true)),
        ("caught", Json::b(true)),
        ("exception", Json::s("link boom")),
        ("status", Json::s("Uninstantiated")),
    ]);
    vec![expect_eq("modules/link_failure", expected, actual)]
}

fn evaluation_rejection() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = compile(
        scope,
        "reject.mjs",
        "await Promise.reject(new RangeError('module rejected'));",
    )
    .unwrap();
    let linked = module.instantiate_module(scope, unexpected_resolve);
    let has_tla = module.has_top_level_await();
    let graph_async = module.is_graph_async();
    let evaluation = module.evaluate(scope).unwrap();
    let promise = v8::Local::<v8::Promise>::try_from(evaluation).unwrap();
    let state_before = promise_state(promise.state());
    scope.perform_microtask_checkpoint();
    let state_after = promise_state(promise.state());
    let reason = promise.result(scope).to_rust_string_lossy(scope);
    let actual = Json::obj(vec![
        ("linked", Json::b(linked == Some(true))),
        ("has_top_level_await", Json::b(has_tla)),
        ("graph_async", Json::b(graph_async)),
        ("state_before_checkpoint", Json::s(state_before)),
        ("state_after_checkpoint", Json::s(state_after)),
        ("status", Json::s(status(module.get_status()))),
        ("exception", Json::s(&reason)),
        (
            "stored_exception_same",
            Json::b(module.get_exception().strict_equals(promise.result(scope))),
        ),
    ]);
    let expected = Json::obj(vec![
        ("linked", Json::b(true)),
        ("has_top_level_await", Json::b(true)),
        ("graph_async", Json::b(true)),
        ("state_before_checkpoint", Json::s("Pending")),
        ("state_after_checkpoint", Json::s("Rejected")),
        ("status", Json::s("Errored")),
        ("exception", Json::s("RangeError: module rejected")),
        ("stored_exception_same", Json::b(true)),
    ]);
    vec![expect_eq("modules/evaluation_rejection", expected, actual)]
}

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    compile_and_requests,
    link_evaluate_namespace,
    syntax_failure,
    negative_origin_offsets,
    repeated_evaluate,
    link_failure,
    evaluation_rejection,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut outcomes = Vec::new();
    for check in CHECKS {
        outcomes.extend(check());
    }
    let total = outcomes.len();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let failed = total - passed;
    let mut text = String::new();
    for outcome in &outcomes {
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    text.push_str(&summary_line(total, passed, failed));
    text.push('\n');

    use std::io::Write as _;
    let mut stdout = std::io::stdout().lock();
    stdout.write_all(text.as_bytes()).unwrap();
    stdout.flush().unwrap();

    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
