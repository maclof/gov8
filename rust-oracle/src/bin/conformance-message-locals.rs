//! Message, StackFrame local-handle, and TryCatch mutation conformance.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::sync::Mutex;

fn value_text(scope: &v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|text| text.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn string_observation(
    scope: &v8::PinScope<'_, '_>,
    first: Option<v8::Local<'_, v8::String>>,
    second: Option<v8::Local<'_, v8::String>>,
) -> Json {
    Json::obj(vec![
        ("present", Json::b(first.is_some())),
        (
            "is_string",
            Json::b(first.is_some_and(|value| value.is_string())),
        ),
        (
            "text",
            first.map_or(Json::Null, |value| {
                Json::s(&value.to_rust_string_lossy(scope))
            }),
        ),
        (
            "repeat_equal",
            Json::b(match (first, second) {
                (Some(a), Some(b)) => a.strict_equals(b.into()),
                (None, None) => true,
                _ => false,
            }),
        ),
    ])
}

fn value_observation(
    scope: &v8::PinScope<'_, '_>,
    first: Option<v8::Local<'_, v8::Value>>,
    second: Option<v8::Local<'_, v8::Value>>,
    original: Option<v8::Local<'_, v8::Value>>,
) -> Json {
    Json::obj(vec![
        ("present", Json::b(first.is_some())),
        (
            "is_string",
            Json::b(first.is_some_and(|value| value.is_string())),
        ),
        (
            "is_number",
            Json::b(first.is_some_and(|value| value.is_number())),
        ),
        (
            "is_undefined",
            Json::b(first.is_some_and(|value| value.is_undefined())),
        ),
        (
            "is_object",
            Json::b(first.is_some_and(|value| value.is_object())),
        ),
        (
            "text",
            first.map_or(Json::Null, |value| Json::s(&value_text(scope, value))),
        ),
        (
            "repeat_equal",
            Json::b(match (first, second) {
                (Some(a), Some(b)) => a.strict_equals(b),
                (None, None) => true,
                _ => false,
            }),
        ),
        (
            "same_as_origin",
            original.map_or(Json::Null, |original| {
                Json::b(first.is_some_and(|value| value.strict_equals(original)))
            }),
        ),
    ])
}

fn compile_and_run<'s>(
    scope: &v8::PinScope<'s, '_>,
    source: &str,
    origin: Option<&v8::ScriptOrigin<'_>>,
) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, origin)?.run(scope)
}

fn caught_message_case(
    scope: &mut v8::PinScope<'_, '_>,
    label: &'static str,
    source: &str,
    resource: Option<v8::Local<'_, v8::Value>>,
    source_map: Option<v8::Local<'_, v8::Value>>,
    shared: bool,
    opaque: bool,
) -> Json {
    v8::tc_scope!(let tc, scope);
    let origin = resource.map(|resource| {
        v8::ScriptOrigin::new(
            tc, resource, 0, 0, shared, 0, source_map, opaque, false, false, None,
        )
    });
    let run_none = compile_and_run(tc, source, origin.as_ref()).is_none();
    let message = tc.message().unwrap();
    let message_get_a = message.get(tc);
    let message_get_b = message.get(tc);
    let source_a = message.get_source_line(tc);
    let source_b = message.get_source_line(tc);
    let resource_a = message.get_script_resource_name(tc);
    let resource_b = message.get_script_resource_name(tc);
    Json::obj(vec![
        ("case", Json::s(label)),
        ("run_none", Json::b(run_none)),
        (
            "message",
            string_observation(tc, Some(message_get_a), Some(message_get_b)),
        ),
        ("source_line", string_observation(tc, source_a, source_b)),
        (
            "resource",
            value_observation(tc, resource_a, resource_b, resource),
        ),
        (
            "line",
            message
                .get_line_number(tc)
                .map_or(Json::Null, |line| Json::i(line as i64)),
        ),
        ("shared", Json::b(message.is_shared_cross_origin())),
        ("opaque", Json::b(message.is_opaque())),
    ])
}

fn message_local_matrix() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let source = "function fail(){ null.member; }\nfail();";
    let string_resource = v8::String::new(scope, "normal.js").unwrap().into();
    let empty_resource = v8::String::empty(scope).into();
    let number_resource = v8::Integer::new(scope, 42).into();
    let undefined_resource = v8::undefined(scope).into();
    let object_resource = v8::Object::new(scope).into();
    let map = v8::String::new(scope, "normal.js.map").unwrap().into();
    let mut cases = vec![
        caught_message_case(
            scope,
            "string",
            source,
            Some(string_resource),
            Some(map),
            false,
            false,
        ),
        caught_message_case(
            scope,
            "empty",
            source,
            Some(empty_resource),
            None,
            false,
            false,
        ),
        caught_message_case(
            scope,
            "number",
            source,
            Some(number_resource),
            None,
            false,
            false,
        ),
        caught_message_case(
            scope,
            "undefined",
            source,
            Some(undefined_resource),
            None,
            false,
            false,
        ),
        caught_message_case(
            scope,
            "object",
            source,
            Some(object_resource),
            None,
            false,
            false,
        ),
        caught_message_case(scope, "absent", source, None, None, false, false),
        caught_message_case(
            scope,
            "source_url",
            "function fail(){ null.member; }\nfail();\n//# sourceURL=fallback.js",
            None,
            None,
            false,
            false,
        ),
        caught_message_case(
            scope,
            "eval_source_url",
            "eval(\"function fail(){ null.member; }\\nfail();\\n//# sourceURL=eval-message.js\");",
            None,
            None,
            false,
            false,
        ),
    ];

    let primitive = v8::Integer::new(scope, 17).into();
    let message = v8::Exception::create_message(scope, primitive);
    let get_a = message.get(scope);
    let get_b = message.get(scope);
    cases.push(Json::obj(vec![
        ("case", Json::s("primitive_created")),
        (
            "message",
            string_observation(scope, Some(get_a), Some(get_b)),
        ),
        (
            "source_line",
            string_observation(
                scope,
                message.get_source_line(scope),
                message.get_source_line(scope),
            ),
        ),
        (
            "resource",
            value_observation(
                scope,
                message.get_script_resource_name(scope),
                message.get_script_resource_name(scope),
                None,
            ),
        ),
        (
            "line",
            message
                .get_line_number(scope)
                .map_or(Json::Null, |line| Json::i(line as i64)),
        ),
        ("shared", Json::b(message.is_shared_cross_origin())),
        ("opaque", Json::b(message.is_opaque())),
    ]));

    vec![pass(
        "message-locals/message_value_matrix",
        Json::arr(cases),
    )]
}

fn origin_flag_matrix() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let mut cases = Vec::new();
    for (label, shared, opaque) in [
        ("false_true", false, true),
        ("true_true", true, true),
        ("true_false", true, false),
    ] {
        let resource = v8::String::new(scope, label).unwrap().into();
        cases.push(caught_message_case(
            scope,
            label,
            "\n\nthrow new Error('flags');",
            Some(resource),
            None,
            shared,
            opaque,
        ));
    }
    vec![pass(
        "message-locals/message_origin_flags",
        Json::arr(cases),
    )]
}

static FRAME_CAPTURES: Mutex<Vec<Json>> = Mutex::new(Vec::new());

fn frame_string<'s>(
    scope: &v8::PinScope<'s, '_>,
    getter: impl Fn() -> Option<v8::Local<'s, v8::String>>,
) -> Json {
    let first = getter();
    let second = getter();
    string_observation(scope, first, second)
}

fn stack_probe(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let label = if args.length() == 0 {
        "wasm".to_owned()
    } else {
        value_text(scope, args.get(0))
    };
    let current_a = v8::StackTrace::current_script_name_or_source_url(scope);
    let current_b = v8::StackTrace::current_script_name_or_source_url(scope);
    let trace = v8::StackTrace::current_stack_trace(scope, 8).unwrap();
    let frame = trace.get_frame(scope, 0).unwrap();
    FRAME_CAPTURES.lock().unwrap().push(Json::obj(vec![
        ("case", Json::s(&label)),
        (
            "current_name_or_url",
            string_observation(scope, current_a, current_b),
        ),
        ("frame_count_positive", Json::b(trace.get_frame_count() > 0)),
        ("line", Json::i(frame.get_line_number() as i64)),
        ("column", Json::i(frame.get_column() as i64)),
        ("script_id_positive", Json::b(frame.get_script_id() > 0)),
        (
            "script_name",
            frame_string(scope, || frame.get_script_name(scope)),
        ),
        (
            "script_name_or_url",
            frame_string(scope, || frame.get_script_name_or_source_url(scope)),
        ),
        (
            "script_source",
            frame_string(scope, || frame.get_script_source(scope)),
        ),
        (
            "source_map_url",
            frame_string(scope, || frame.get_script_source_mapping_url(scope)),
        ),
        (
            "function_name",
            frame_string(scope, || frame.get_function_name(scope)),
        ),
        ("is_eval", Json::b(frame.is_eval())),
        ("is_constructor", Json::b(frame.is_constructor())),
        ("is_wasm", Json::b(frame.is_wasm())),
        ("is_user_javascript", Json::b(frame.is_user_javascript())),
    ]));
}

fn run_stack_source(
    scope: &mut v8::PinScope<'_, '_>,
    source: &str,
    resource: Option<&str>,
    source_map: Option<&str>,
) {
    let resource_value = resource.map(|value| v8::String::new(scope, value).unwrap().into());
    let map_value = source_map.map(|value| v8::String::new(scope, value).unwrap().into());
    let origin = resource_value.map(|resource| {
        v8::ScriptOrigin::new(
            scope, resource, 0, 0, false, 0, map_value, false, false, false, None,
        )
    });
    compile_and_run(scope, source, origin.as_ref()).unwrap();
}

fn stack_frame_locals() -> Vec<CheckOutcome> {
    FRAME_CAPTURES.lock().unwrap().clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let probe = v8::Function::builder(stack_probe).build(scope).unwrap();
    let key = v8::String::new(scope, "probe").unwrap();
    context.global(scope).set(scope, key.into(), probe.into());

    let named_source = "function named(){ probe('named'); }\nnamed();\n(function(){ probe('anonymous'); })();\n//# sourceMappingURL=named.map";
    run_stack_source(scope, named_source, Some("named.js"), Some("named.map"));
    let eval_source = "eval(\"function evalNamed(){ probe('eval_source_url'); }\\nevalNamed();\\n//# sourceURL=eval-local.js\\n//# sourceMappingURL=eval-local.map\");";
    run_stack_source(scope, eval_source, Some("eval-host.js"), None);
    let source_url =
        "function sourced(){ probe('source_url_only'); }\nsourced();\n//# sourceURL=source-only.js";
    run_stack_source(scope, source_url, None, None);
    run_stack_source(
        scope,
        "(function(){ probe('anonymous_no_origin'); })();",
        None,
        None,
    );
    let wasm_source = "const bytes=new Uint8Array([0,97,115,109,1,0,0,0,1,4,1,96,0,0,2,12,1,3,101,110,118,4,104,111,115,116,0,0,3,2,1,0,7,5,1,1,102,0,1,10,6,1,4,0,16,0,11]);new WebAssembly.Instance(new WebAssembly.Module(bytes),{env:{host:probe}}).exports.f();";
    run_stack_source(scope, wasm_source, Some("wasm-host.js"), None);

    let captures = std::mem::take(&mut *FRAME_CAPTURES.lock().unwrap());
    vec![pass(
        "message-locals/stack_frame_string_getters",
        Json::arr(captures),
    )]
}

fn try_catch_mutation() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    v8::tc_scope!(let tc, scope);

    let first_none = compile_and_run(tc, "throw new Error('first')", None).is_none();
    let first_exception = tc.exception().unwrap();
    let first_message = tc.message().unwrap();
    let first_text = value_text(tc, first_exception);
    let first_message_text = first_message.get(tc).to_rust_string_lossy(tc);
    let second_none = compile_and_run(tc, "throw new TypeError('second')", None).is_none();
    let second_exception = tc.exception().unwrap();
    let second_message = tc.message().unwrap();
    let overwritten = Json::obj(vec![
        ("first_none", Json::b(first_none)),
        ("first_exception", Json::s(&first_text)),
        ("first_message", Json::s(&first_message_text)),
        ("second_none", Json::b(second_none)),
        ("has_caught", Json::b(tc.has_caught())),
        (
            "second_exception",
            Json::s(&value_text(tc, second_exception)),
        ),
        (
            "second_message",
            Json::s(&second_message.get(tc).to_rust_string_lossy(tc)),
        ),
        (
            "exception_changed",
            Json::b(!first_exception.strict_equals(second_exception)),
        ),
        ("message_changed", Json::b(first_message != second_message)),
        (
            "first_exception_after",
            Json::s(&value_text(tc, first_exception)),
        ),
        (
            "first_message_after",
            Json::s(&first_message.get(tc).to_rust_string_lossy(tc)),
        ),
    ]);

    tc.reset();
    let first_reset = Json::obj(vec![
        ("has_caught", Json::b(tc.has_caught())),
        ("exception_none", Json::b(tc.exception().is_none())),
        ("message_none", Json::b(tc.message().is_none())),
    ]);
    tc.set_capture_message(false);
    let disabled_none = compile_and_run(tc, "throw 31", None).is_none();
    let disabled = Json::obj(vec![
        ("run_none", Json::b(disabled_none)),
        ("has_caught", Json::b(tc.has_caught())),
        (
            "exception",
            Json::s(&value_text(tc, tc.exception().unwrap())),
        ),
        ("message_none", Json::b(tc.message().is_none())),
    ]);
    tc.reset();
    let disabled_reset = Json::obj(vec![
        ("has_caught", Json::b(tc.has_caught())),
        ("exception_none", Json::b(tc.exception().is_none())),
        ("message_none", Json::b(tc.message().is_none())),
    ]);
    tc.set_capture_message(true);
    let enabled_none = compile_and_run(tc, "throw new Error('enabled')", None).is_none();
    let enabled_message = tc.message();
    let enabled = Json::obj(vec![
        ("run_none", Json::b(enabled_none)),
        ("has_caught", Json::b(tc.has_caught())),
        ("message_some", Json::b(enabled_message.is_some())),
        (
            "message",
            enabled_message.map_or(Json::Null, |message| {
                Json::s(&message.get(tc).to_rust_string_lossy(tc))
            }),
        ),
    ]);

    vec![pass(
        "message-locals/try_catch_mutation",
        Json::obj(vec![
            ("overwrite_without_reset", overwritten),
            ("first_reset", first_reset),
            ("capture_disabled", disabled),
            ("disabled_reset", disabled_reset),
            ("reset_reenabled", enabled),
        ]),
    )]
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut checks = message_local_matrix();
    checks.extend(origin_flag_matrix());
    checks.extend(stack_frame_locals());
    checks.extend(try_catch_mutation());
    let passed = checks.iter().filter(|check| check.passed()).count();
    for check in &checks {
        println!("{}", check.to_line());
    }
    println!(
        "{}",
        summary_line(checks.len(), passed, checks.len() - passed)
    );
    if passed == checks.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
