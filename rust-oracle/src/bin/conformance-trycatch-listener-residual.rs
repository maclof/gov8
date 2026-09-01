//! Residual TryCatch structure/identity and message-listener fidelity oracle.
//!
//! Pinned API: rusty_v8 152.2.0, V8 15.2.124.1-rusty. This deliberately
//! excludes the already-characterized single-catch state matrix, two-level
//! rethrow, duplicate listener registration, and error-level filtering.

use std::sync::Mutex;

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

static LISTENER_LOG: Mutex<Vec<Json>> = Mutex::new(Vec::new());

fn value_text(scope: &v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|text| text.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn compile_and_run<'s>(
    scope: &v8::PinScope<'s, '_>,
    source: &str,
    origin: Option<&v8::ScriptOrigin<'_>>,
) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, origin)?.run(scope)
}

fn structural_nesting_and_identity() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    v8::tc_scope!(let outer, scope);
    let (inner, middle) = {
        v8::tc_scope!(let middle, outer);
        let (original, inner) = {
            v8::tc_scope!(let inner, middle);
            let run_none = compile_and_run(
                inner,
                "const e = new Error('three-level'); e.marker = 'three-level'; throw e",
                None,
            )
            .is_none();
            let exception_a = inner.exception().unwrap();
            let exception_b = inner.exception().unwrap();
            let message_a = inner.message().unwrap();
            let message_b = inner.message().unwrap();
            let stack_a = inner.stack_trace().unwrap();
            let stack_b = inner.stack_trace().unwrap();
            let was_caught = inner.has_caught();
            let exception_repeat_identity = exception_a.strict_equals(exception_b);
            let message_repeat_identity = message_a == message_b;
            let stack_repeat_identity = stack_a.strict_equals(stack_b);
            let rethrown = inner.rethrow().unwrap();
            let observation = Json::obj(vec![
                ("run_none", Json::b(run_none)),
                ("has_caught_before_rethrow", Json::b(was_caught)),
                (
                    "exception_repeat_identity",
                    Json::b(exception_repeat_identity),
                ),
                ("message_repeat_identity", Json::b(message_repeat_identity)),
                ("stack_repeat_identity", Json::b(stack_repeat_identity)),
                ("rethrow_is_undefined", Json::b(rethrown.is_undefined())),
            ]);
            (exception_a, observation)
        };

        let middle_exception = middle.exception().unwrap();
        let middle_before_reset = Json::obj(vec![
            ("has_caught", Json::b(middle.has_caught())),
            (
                "same_exception",
                Json::b(middle_exception.strict_equals(original)),
            ),
            ("message_some", Json::b(middle.message().is_some())),
            ("stack_some", Json::b(middle.stack_trace().is_some())),
        ]);
        middle.reset();
        let middle_after_reset = Json::obj(vec![
            ("has_caught", Json::b(middle.has_caught())),
            ("exception_none", Json::b(middle.exception().is_none())),
        ]);
        (
            inner,
            Json::obj(vec![
                ("before_reset", middle_before_reset),
                ("after_reset", middle_after_reset),
            ]),
        )
    };

    let outer_after_middle_reset = Json::obj(vec![
        ("has_caught", Json::b(outer.has_caught())),
        ("exception_none", Json::b(outer.exception().is_none())),
    ]);
    let reused_run_none = compile_and_run(outer, "throw 'outer-reuse'", None).is_none();
    let reused = Json::obj(vec![
        ("run_none", Json::b(reused_run_none)),
        ("has_caught", Json::b(outer.has_caught())),
        (
            "exception",
            outer
                .exception()
                .map_or(Json::Null, |value| Json::s(&value_text(outer, value))),
        ),
    ]);

    vec![pass(
        "trycatch-listener-residual/structural_nesting_identity",
        Json::obj(vec![
            ("inner", inner),
            ("middle", middle),
            ("outer_after_middle_reset", outer_after_middle_reset),
            ("outer_reuse", reused),
        ]),
    )]
}

unsafe extern "C" fn quiet_listener(
    _message: v8::Local<v8::Message>,
    _exception: v8::Local<v8::Value>,
) {
}

fn nested_configuration_independence() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    assert!(isolate.add_message_listener(quiet_listener));
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    v8::tc_scope!(let outer, scope);
    outer.set_verbose(true);
    outer.set_capture_message(false);
    let (inner_defaults, inner_capture_disabled, inner_verbose_forces_message) = {
        v8::tc_scope!(let inner, outer);
        let defaults = Json::obj(vec![
            ("verbose", Json::b(inner.is_verbose())),
            (
                "run_none",
                Json::b(compile_and_run(inner, "throw new Error('inner-default')", None).is_none()),
            ),
            ("message_some", Json::b(inner.message().is_some())),
            ("stack_some", Json::b(inner.stack_trace().is_some())),
        ]);
        inner.reset();
        inner.set_verbose(false);
        inner.set_capture_message(false);
        let disabled = Json::obj(vec![
            ("verbose", Json::b(inner.is_verbose())),
            (
                "run_none",
                Json::b(
                    compile_and_run(inner, "throw new Error('inner-no-message')", None).is_none(),
                ),
            ),
            ("message_none", Json::b(inner.message().is_none())),
            ("stack_none", Json::b(inner.stack_trace().is_none())),
        ]);
        inner.reset();
        inner.set_verbose(true);
        inner.set_capture_message(false);
        let verbose_forces_message = Json::obj(vec![
            ("verbose", Json::b(inner.is_verbose())),
            (
                "run_none",
                Json::b(compile_and_run(inner, "throw new Error('inner-verbose')", None).is_none()),
            ),
            ("message_some", Json::b(inner.message().is_some())),
            ("stack_some", Json::b(inner.stack_trace().is_some())),
        ]);
        inner.reset();
        (defaults, disabled, verbose_forces_message)
    };
    let outer_before_throw = Json::obj(vec![
        ("verbose", Json::b(outer.is_verbose())),
        ("has_caught", Json::b(outer.has_caught())),
    ]);
    let outer_run_none =
        compile_and_run(outer, "throw new Error('outer-no-message')", None).is_none();
    let outer_after_throw = Json::obj(vec![
        ("run_none", Json::b(outer_run_none)),
        ("has_caught", Json::b(outer.has_caught())),
        ("message_none", Json::b(outer.message().is_none())),
        ("stack_none", Json::b(outer.stack_trace().is_none())),
    ]);

    vec![pass(
        "trycatch-listener-residual/nested_configuration",
        Json::obj(vec![
            ("inner_defaults", inner_defaults),
            ("inner_capture_disabled", inner_capture_disabled),
            ("inner_verbose_forces_message", inner_verbose_forces_message),
            ("outer_before_throw", outer_before_throw),
            ("outer_after_throw", outer_after_throw),
        ]),
    )]
}

fn request_termination(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    assert!(scope.terminate_execution());
}

fn nested_termination_recovery() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let handle = isolate.thread_safe_handle();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let terminate = v8::Function::builder(request_termination)
        .build(scope)
        .unwrap();
    let key = v8::String::new(scope, "terminateNow").unwrap();
    context
        .global(scope)
        .set(scope, key.into(), terminate.into())
        .unwrap();

    v8::tc_scope!(let outer, scope);
    let inner = {
        v8::tc_scope!(let middle, outer);
        let observation = {
            v8::tc_scope!(let inner, middle);
            let run_none =
                compile_and_run(inner, "terminateNow(); while (true) {}", None).is_none();
            let before_cancel = Json::obj(vec![
                ("run_none", Json::b(run_none)),
                ("has_caught", Json::b(inner.has_caught())),
                ("can_continue", Json::b(inner.can_continue())),
                ("has_terminated", Json::b(inner.has_terminated())),
                ("exception_none", Json::b(inner.exception().is_none())),
                (
                    "exception_is_null",
                    Json::b(inner.exception().is_some_and(|value| value.is_null())),
                ),
                ("message_none", Json::b(inner.message().is_none())),
            ]);
            let cancel_ok = handle.cancel_terminate_execution();
            inner.reset();
            let reuse =
                compile_and_run(inner, "6 * 7", None).and_then(|value| value.integer_value(inner));
            Json::obj(vec![
                ("before_cancel", before_cancel),
                ("cancel_ok", Json::b(cancel_ok)),
                ("after_reset_has_caught", Json::b(inner.has_caught())),
                ("after_reset_terminated", Json::b(inner.has_terminated())),
                ("reuse", reuse.map_or(Json::Null, Json::i)),
            ])
        };
        let middle_empty = !middle.has_caught();
        Json::obj(vec![
            ("inner", observation),
            ("middle_empty", Json::b(middle_empty)),
        ])
    };
    let outer_empty = !outer.has_caught();

    vec![pass(
        "trycatch-listener-residual/nested_termination_recovery",
        Json::obj(vec![
            ("nested", inner),
            ("outer_empty", Json::b(outer_empty)),
        ]),
    )]
}

fn optional_string(scope: &v8::PinScope<'_, '_>, value: Option<v8::Local<'_, v8::String>>) -> Json {
    value.map_or(Json::Null, |value| {
        Json::s(&value.to_rust_string_lossy(scope))
    })
}

unsafe extern "C" fn full_listener(
    message: v8::Local<v8::Message>,
    exception: v8::Local<v8::Value>,
) {
    v8::callback_scope!(unsafe scope, message);
    let get_a = message.get(scope);
    let get_b = message.get(scope);
    let source_a = message.get_source_line(scope);
    let source_b = message.get_source_line(scope);
    let resource_a = message.get_script_resource_name(scope);
    let resource_b = message.get_script_resource_name(scope);
    let stack = message.get_stack_trace(scope);
    let first_frame = stack.and_then(|stack| stack.get_frame(scope, 0));
    let recreated = v8::Exception::create_message(scope, exception);
    let resource = Json::obj(vec![
        ("present", Json::b(resource_a.is_some())),
        (
            "is_string",
            Json::b(resource_a.is_some_and(|value| value.is_string())),
        ),
        (
            "text",
            resource_a.map_or(Json::Null, |value| Json::s(&value_text(scope, value))),
        ),
        (
            "repeat_identity",
            Json::b(match (resource_a, resource_b) {
                (Some(a), Some(b)) => a.strict_equals(b),
                (None, None) => true,
                _ => false,
            }),
        ),
    ]);
    let stack_json = Json::obj(vec![
        ("present", Json::b(stack.is_some())),
        (
            "frame_count",
            stack.map_or(Json::Null, |stack| Json::i(stack.get_frame_count() as i64)),
        ),
        (
            "first",
            first_frame.map_or(Json::Null, |frame| {
                Json::obj(vec![
                    ("line", Json::i(frame.get_line_number() as i64)),
                    ("column", Json::i(frame.get_column() as i64)),
                    (
                        "function",
                        optional_string(scope, frame.get_function_name(scope)),
                    ),
                    (
                        "script",
                        optional_string(scope, frame.get_script_name(scope)),
                    ),
                    (
                        "script_or_url",
                        optional_string(scope, frame.get_script_name_or_source_url(scope)),
                    ),
                    ("is_eval", Json::b(frame.is_eval())),
                    ("is_wasm", Json::b(frame.is_wasm())),
                    ("is_user_js", Json::b(frame.is_user_javascript())),
                ])
            }),
        ),
    ]);
    LISTENER_LOG.lock().unwrap().push(Json::obj(vec![
        ("text", Json::s(&get_a.to_rust_string_lossy(scope))),
        (
            "text_repeat_identity",
            Json::b(get_a.strict_equals(get_b.into())),
        ),
        ("source_line", optional_string(scope, source_a)),
        (
            "source_repeat_identity",
            Json::b(match (source_a, source_b) {
                (Some(a), Some(b)) => a.strict_equals(b.into()),
                (None, None) => true,
                _ => false,
            }),
        ),
        ("resource", resource),
        (
            "line",
            message
                .get_line_number(scope)
                .map_or(Json::Null, |value| Json::i(value as i64)),
        ),
        (
            "start_position",
            Json::i(i64::from(message.get_start_position())),
        ),
        (
            "end_position",
            Json::i(i64::from(message.get_end_position())),
        ),
        ("start_column", Json::i(message.get_start_column() as i64)),
        ("end_column", Json::i(message.get_end_column() as i64)),
        (
            "wasm_function_index",
            Json::i(i64::from(message.get_wasm_function_index())),
        ),
        ("error_level", Json::i(i64::from(message.error_level()))),
        ("shared", Json::b(message.is_shared_cross_origin())),
        ("opaque", Json::b(message.is_opaque())),
        ("stack", stack_json),
        ("exception_text", Json::s(&value_text(scope, exception))),
        (
            "exception_is_native_error",
            Json::b(exception.is_native_error()),
        ),
        ("exception_is_number", Json::b(exception.is_number())),
        ("recreated_message_identity", Json::b(message == recreated)),
    ]));
}

struct OriginOptions<'a> {
    name: &'a str,
    line: i32,
    column: i32,
    shared: bool,
    opaque: bool,
    map: Option<&'a str>,
}

fn listener_case(
    source: &str,
    compile_only: bool,
    capture_stack: bool,
    origin_options: Option<OriginOptions<'_>>,
    verbose: bool,
) -> Json {
    LISTENER_LOG.lock().unwrap().clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_capture_stack_trace_for_uncaught_exceptions(capture_stack, 3);
    let added = isolate.add_message_listener(full_listener);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let origin = origin_options.map(|options| {
        let resource = v8::String::new(scope, options.name).unwrap().into();
        let map = options
            .map
            .map(|value| v8::Local::<v8::Value>::from(v8::String::new(scope, value).unwrap()));
        v8::ScriptOrigin::new(
            scope,
            resource,
            options.line,
            options.column,
            options.shared,
            0,
            map,
            options.opaque,
            false,
            false,
            None,
        )
    });
    let operation_none = if verbose {
        v8::tc_scope!(let tc, scope);
        tc.set_verbose(true);
        compile_and_run(tc, source, origin.as_ref()).is_none()
    } else {
        let source = v8::String::new(scope, source).unwrap();
        match v8::Script::compile(scope, source, origin.as_ref()) {
            Some(script) if !compile_only => script.run(scope).is_none(),
            Some(_) => false,
            None => true,
        }
    };
    let records = std::mem::take(&mut *LISTENER_LOG.lock().unwrap());
    Json::obj(vec![
        ("registration_ok", Json::b(added)),
        ("operation_none", Json::b(operation_none)),
        ("records", Json::arr(records)),
    ])
}

fn listener_full_message_fidelity() -> Vec<CheckOutcome> {
    let runtime = listener_case(
        "function fail(){ throw new TypeError('listener-boom'); }\nfail();",
        false,
        true,
        Some(OriginOptions {
            name: "listener-rich.js",
            line: 4,
            column: 6,
            shared: true,
            opaque: false,
            map: Some("listener-rich.js.map"),
        }),
        false,
    );
    let primitive = listener_case("throw 17", false, false, None, false);
    let syntax = listener_case(
        "function broken(",
        true,
        false,
        Some(OriginOptions {
            name: "syntax-listener.js",
            line: 0,
            column: 0,
            shared: false,
            opaque: true,
            map: None,
        }),
        false,
    );
    let verbose = listener_case(
        "throw new Error('verbose-listener')",
        false,
        false,
        None,
        true,
    );
    vec![pass(
        "trycatch-listener-residual/listener_full_message_fidelity",
        Json::obj(vec![
            ("runtime_with_stack", runtime),
            ("primitive_without_stack", primitive),
            ("syntax_compile", syntax),
            ("caught_verbose", verbose),
        ]),
    )]
}

unsafe extern "C" fn panic_listener(
    _message: v8::Local<v8::Message>,
    _exception: v8::Local<v8::Value>,
) {
    panic!("message listener panic boundary")
}

fn panic_listener_mode() {
    let isolate = &mut v8::Isolate::new(Default::default());
    assert!(isolate.add_message_listener(panic_listener));
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _ = compile_and_run(scope, "throw new Error('trigger-listener-panic')", None);
}

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    structural_nesting_and_identity,
    nested_configuration_independence,
    nested_termination_recovery,
    listener_full_message_fidelity,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    if std::env::args().nth(1).as_deref() == Some("mode=panic-listener") {
        panic_listener_mode();
        return std::process::ExitCode::FAILURE;
    }
    let outcomes: Vec<CheckOutcome> = CHECKS.iter().flat_map(|check| check()).collect();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!(
        "{}",
        summary_line(outcomes.len(), passed, outcomes.len() - passed)
    );
    if passed == outcomes.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
