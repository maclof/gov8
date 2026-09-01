//! Advanced exception/message/stack conformance for `v8` 152.2.0.
//!
//! This is deliberately a stand-alone oracle slice.  It records only stable,
//! normalized observations: V8-assigned script ids are represented by
//! positivity/equality, and no addresses or platform-dependent stack text are
//! emitted.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Mutex;

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

fn text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn string_json(value: Option<String>) -> Json {
    value.map_or(Json::Null, |s| Json::s(&s))
}

fn local_string(
    scope: &v8::PinScope<'_, '_>,
    value: Option<v8::Local<'_, v8::String>>,
) -> Option<String> {
    value.map(|s| s.to_rust_string_lossy(scope))
}

fn origin<'s>(
    scope: &v8::PinScope<'s, '_>,
    name: &str,
    source_map: Option<&str>,
) -> v8::ScriptOrigin<'s> {
    let resource: v8::Local<v8::Value> = v8::String::new(scope, name).unwrap().into();
    let source_map =
        source_map.map(|s| v8::Local::<v8::Value>::from(v8::String::new(scope, s).unwrap()));
    v8::ScriptOrigin::new(
        scope, resource, 0, 0, false, 0, source_map, false, false, false, None,
    )
}

fn compile_and_run<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    source: &str,
    script_origin: Option<&v8::ScriptOrigin<'_>>,
) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, script_origin)?.run(scope)
}

fn try_catch_empty_and_reset() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let initial = Json::obj(vec![
        ("has_caught", Json::b(tc.has_caught())),
        ("can_continue", Json::b(tc.can_continue())),
        ("has_terminated", Json::b(tc.has_terminated())),
        ("is_verbose", Json::b(tc.is_verbose())),
        ("exception_none", Json::b(tc.exception().is_none())),
        ("message_none", Json::b(tc.message().is_none())),
        ("stack_trace_none", Json::b(tc.stack_trace().is_none())),
        ("rethrow_none", Json::b(tc.rethrow().is_none())),
    ]);

    tc.set_verbose(true);
    let verbose_true = tc.is_verbose();
    tc.set_verbose(false);
    let verbose_false = tc.is_verbose();
    let ran = compile_and_run(tc, "40 + 2", None)
        .and_then(|v| v.integer_value(tc))
        .unwrap_or(-1);

    let _ = compile_and_run(tc, "throw 'reset-me'", None);
    let before_reset = Json::obj(vec![
        ("has_caught", Json::b(tc.has_caught())),
        (
            "exception",
            Json::s(&tc.exception().map(|v| text(tc, v)).unwrap_or_default()),
        ),
    ]);
    tc.reset();
    let after_reset = Json::obj(vec![
        ("has_caught", Json::b(tc.has_caught())),
        ("exception_none", Json::b(tc.exception().is_none())),
        ("message_none", Json::b(tc.message().is_none())),
        ("stack_trace_none", Json::b(tc.stack_trace().is_none())),
    ]);

    vec![pass(
        "exceptions-advanced/try-catch/empty_toggle_and_reset",
        Json::obj(vec![
            ("initial", initial),
            ("verbose_true", Json::b(verbose_true)),
            ("verbose_false", Json::b(verbose_false)),
            ("successful_run", Json::i(ran)),
            ("before_reset", before_reset),
            ("after_reset", after_reset),
        ]),
    )]
}

static VERBOSE_COUNT: AtomicUsize = AtomicUsize::new(0);
static VERBOSE_TEXT: Mutex<Option<String>> = Mutex::new(None);

extern "C" fn verbose_listener<'s>(
    message: v8::Local<'s, v8::Message>,
    _exception: v8::Local<'s, v8::Value>,
) {
    let scope = std::pin::pin!(unsafe { v8::CallbackScope::new(message) });
    let scope = &mut scope.init();
    v8::scope!(let scope, scope);
    *VERBOSE_TEXT.lock().unwrap() = Some(message.get(scope).to_rust_string_lossy(scope));
    VERBOSE_COUNT.fetch_add(1, Ordering::SeqCst);
}

fn try_catch_verbose_reporting() -> Vec<CheckOutcome> {
    VERBOSE_COUNT.store(0, Ordering::SeqCst);
    *VERBOSE_TEXT.lock().unwrap() = None;
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.add_message_listener(verbose_listener);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    {
        v8::tc_scope!(let tc, scope);
        let _ = compile_and_run(tc, "throw new Error('quiet')", None);
    }
    let quiet_count = VERBOSE_COUNT.load(Ordering::SeqCst);
    {
        v8::tc_scope!(let tc, scope);
        tc.set_verbose(true);
        let _ = compile_and_run(tc, "throw new Error('reported')", None);
    }

    vec![pass(
        "exceptions-advanced/try-catch/verbose_reporting",
        Json::obj(vec![
            ("quiet_listener_count", Json::i(quiet_count as i64)),
            (
                "verbose_listener_count",
                Json::i(VERBOSE_COUNT.load(Ordering::SeqCst) as i64),
            ),
            (
                "reported_text",
                string_json(VERBOSE_TEXT.lock().unwrap().clone()),
            ),
        ]),
    )]
}

fn runtime_exception_details() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let script_origin = origin(scope, "runtime.js", Some("runtime.js.map"));
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let source = "function outer() {\n  function inner() { null.value; }\n  inner();\n}\nouter();";
    let run_none = compile_and_run(tc, source, Some(&script_origin)).is_none();
    let exception = tc.exception().unwrap();
    let message = tc.message().unwrap();
    let stack_value = tc.stack_trace();
    let message_stack = message.get_stack_trace(tc);

    vec![pass(
        "exceptions-advanced/try-catch/runtime_exception_details",
        Json::obj(vec![
            ("run_none", Json::b(run_none)),
            ("has_caught", Json::b(tc.has_caught())),
            ("can_continue", Json::b(tc.can_continue())),
            ("has_terminated", Json::b(tc.has_terminated())),
            ("exception_is_error", Json::b(exception.is_native_error())),
            ("exception", Json::s(&text(tc, exception))),
            (
                "try_catch_stack",
                string_json(stack_value.map(|v| text(tc, v))),
            ),
            (
                "message",
                Json::s(&message.get(tc).to_rust_string_lossy(tc)),
            ),
            (
                "resource_name",
                string_json(message.get_script_resource_name(tc).map(|v| text(tc, v))),
            ),
            (
                "source_line",
                string_json(local_string(tc, message.get_source_line(tc))),
            ),
            (
                "line_number",
                message
                    .get_line_number(tc)
                    .map_or(Json::Null, |v| Json::i(v as i64)),
            ),
            ("start_column", Json::i(message.get_start_column() as i64)),
            ("end_column", Json::i(message.get_end_column() as i64)),
            (
                "wasm_function_index",
                Json::i(i64::from(message.get_wasm_function_index())),
            ),
            ("message_stack_none", Json::b(message_stack.is_none())),
        ]),
    )]
}

fn syntax_exception_details() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let script_origin = origin(scope, "syntax.js", None);
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let source = v8::String::new(tc, "const answer = ;").unwrap();
    let compile_none = v8::Script::compile(tc, source, Some(&script_origin)).is_none();
    let exception = tc.exception().unwrap();
    let message = tc.message().unwrap();
    let stack = tc.stack_trace();

    vec![pass(
        "exceptions-advanced/try-catch/syntax_exception_details",
        Json::obj(vec![
            ("compile_none", Json::b(compile_none)),
            ("has_caught", Json::b(tc.has_caught())),
            ("exception", Json::s(&text(tc, exception))),
            ("stack_trace", string_json(stack.map(|v| text(tc, v)))),
            (
                "message",
                Json::s(&message.get(tc).to_rust_string_lossy(tc)),
            ),
            (
                "resource_name",
                string_json(message.get_script_resource_name(tc).map(|v| text(tc, v))),
            ),
            (
                "source_line",
                string_json(local_string(tc, message.get_source_line(tc))),
            ),
            (
                "line_number",
                message
                    .get_line_number(tc)
                    .map_or(Json::Null, |v| Json::i(v as i64)),
            ),
            (
                "start_position",
                Json::i(message.get_start_position() as i64),
            ),
            ("end_position", Json::i(message.get_end_position() as i64)),
            ("start_column", Json::i(message.get_start_column() as i64)),
            ("end_column", Json::i(message.get_end_column() as i64)),
            (
                "wasm_function_index",
                Json::i(i64::from(message.get_wasm_function_index())),
            ),
        ]),
    )]
}

fn capture_message_disabled() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    tc.set_capture_message(false);
    let run_none = compile_and_run(tc, "function f(){ throw 17; } f();", None).is_none();
    vec![pass(
        "exceptions-advanced/try-catch/capture_message_disabled",
        Json::obj(vec![
            ("run_none", Json::b(run_none)),
            ("has_caught", Json::b(tc.has_caught())),
            (
                "exception",
                Json::s(&tc.exception().map(|v| text(tc, v)).unwrap_or_default()),
            ),
            ("message_none", Json::b(tc.message().is_none())),
            ("stack_trace_none", Json::b(tc.stack_trace().is_none())),
        ]),
    )]
}

fn rethrow_propagation() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let outer = std::pin::pin!(v8::TryCatch::new(scope));
    let outer = &mut outer.init();
    let (original, inner_rethrow) = {
        let inner = std::pin::pin!(v8::TryCatch::new(outer));
        let inner = &mut inner.init();
        let _ = compile_and_run(inner, "throw ({marker: 'same-object'})", None);
        let before = inner.exception().unwrap();
        let rethrown = inner.rethrow().unwrap();
        let returned_is_undefined = rethrown.is_undefined();
        (
            before,
            Json::obj(vec![
                ("returned_value", Json::b(true)),
                ("returned_is_undefined", Json::b(returned_is_undefined)),
            ]),
        )
    };
    let outer_exception = outer.exception().unwrap();
    let outer_same_exception = outer_exception.strict_equals(original);
    let marker = outer_exception
        .to_object(outer)
        .and_then(|o| {
            let key = v8::String::new(outer, "marker")?;
            o.get(outer, key.into())
        })
        .map(|v| text(outer, v))
        .unwrap_or_default();

    vec![pass(
        "exceptions-advanced/try-catch/rethrow_propagation",
        Json::obj(vec![
            ("inner_rethrow", inner_rethrow),
            ("outer_has_caught", Json::b(outer.has_caught())),
            ("outer_same_exception", Json::b(outer_same_exception)),
            ("outer_marker", Json::s(&marker)),
        ]),
    )]
}

fn caught_locals_outlive_try_catch() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let (exception, message) = {
        v8::tc_scope!(let tc, scope);
        let _ = compile_and_run(tc, "throw new TypeError('local-lifetime')", None);
        (tc.exception().unwrap(), tc.message().unwrap())
    };
    vec![pass(
        "exceptions-advanced/try-catch/caught_local_lifetime",
        Json::obj(vec![
            ("exception", Json::s(&text(scope, exception))),
            (
                "message",
                Json::s(&message.get(scope).to_rust_string_lossy(scope)),
            ),
        ]),
    )]
}

fn message_source_url_fallback() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let source =
        "function named(){ throw new Error('url-only'); }\nnamed();\n//# sourceURL=fallback.js";
    let _ = compile_and_run(tc, source, None);
    let message = tc.message().unwrap();
    let trace = v8::Exception::get_stack_trace(tc, tc.exception().unwrap());
    let frame = trace.and_then(|t| t.get_frame(tc, 0));

    vec![pass(
        "exceptions-advanced/message/source_url_fallback",
        Json::obj(vec![
            (
                "resource_name",
                string_json(message.get_script_resource_name(tc).map(|v| text(tc, v))),
            ),
            (
                "frame_script_name",
                string_json(frame.and_then(|f| local_string(tc, f.get_script_name(tc)))),
            ),
            (
                "frame_script_name_or_source_url",
                string_json(
                    frame.and_then(|f| local_string(tc, f.get_script_name_or_source_url(tc))),
                ),
            ),
        ]),
    )]
}

static STACK_CAPTURES: Mutex<Vec<Json>> = Mutex::new(Vec::new());

fn stack_callback(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let label = text(scope, args.get(0));
    let zero = v8::StackTrace::current_stack_trace(scope, 0).map(|s| s.get_frame_count() as i64);
    let one = v8::StackTrace::current_stack_trace(scope, 1).map(|s| s.get_frame_count() as i64);
    let two = v8::StackTrace::current_stack_trace(scope, 2).map(|s| s.get_frame_count() as i64);
    let overflow_none = v8::StackTrace::current_stack_trace(scope, usize::MAX).is_none();
    let current_name = local_string(
        scope,
        v8::StackTrace::current_script_name_or_source_url(scope),
    );
    let trace = v8::StackTrace::current_stack_trace(scope, 16).unwrap();
    let count = trace.get_frame_count();
    let mut frames = Vec::new();
    let mut first_script_id = None;
    for index in 0..count {
        let frame = trace.get_frame(scope, index).unwrap();
        let script_id = frame.get_script_id();
        let is_wasm = frame.is_wasm();
        if first_script_id.is_none() {
            first_script_id = Some(script_id);
        }
        frames.push(Json::obj(vec![
            (
                "function_name",
                string_json(local_string(scope, frame.get_function_name(scope))),
            ),
            (
                "script_name",
                if is_wasm {
                    Json::s("<wasm-url>")
                } else {
                    string_json(local_string(scope, frame.get_script_name(scope)))
                },
            ),
            (
                "script_name_or_source_url",
                if is_wasm {
                    Json::s("<wasm-url>")
                } else {
                    string_json(local_string(
                        scope,
                        frame.get_script_name_or_source_url(scope),
                    ))
                },
            ),
            (
                "source_map_url",
                string_json(local_string(
                    scope,
                    frame.get_script_source_mapping_url(scope),
                )),
            ),
            ("line", Json::i(frame.get_line_number() as i64)),
            ("column", Json::i(frame.get_column() as i64)),
            ("script_id_positive", Json::b(script_id > 0)),
            (
                "same_script_as_first",
                Json::b(first_script_id == Some(script_id)),
            ),
            ("is_eval", Json::b(frame.is_eval())),
            ("is_constructor", Json::b(frame.is_constructor())),
            ("is_wasm", Json::b(is_wasm)),
            ("is_user_javascript", Json::b(frame.is_user_javascript())),
        ]));
    }
    STACK_CAPTURES.lock().unwrap().push(Json::obj(vec![
        ("label", Json::s(&label)),
        ("limit_zero", zero.map_or(Json::Null, Json::i)),
        ("limit_one", one.map_or(Json::Null, Json::i)),
        ("limit_two", two.map_or(Json::Null, Json::i)),
        ("overflow_none", Json::b(overflow_none)),
        ("frame_count", Json::i(count as i64)),
        (
            "index_equal_count_some",
            Json::b(trace.get_frame(scope, count).is_some()),
        ),
        ("current_script_name", string_json(current_name)),
        ("frames", Json::arr(frames)),
    ]));
}

fn current_stack_frames_and_limits() -> Vec<CheckOutcome> {
    STACK_CAPTURES.lock().unwrap().clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let host = v8::Function::builder(stack_callback).build(scope).unwrap();
    let key = v8::String::new(scope, "host").unwrap();
    context.global(scope).set(scope, key.into(), host.into());
    let script_origin = origin(scope, "stack.js", Some("stack.js.map"));
    let source = r#"function HostCtor(){ host('constructor'); }
eval("function evalCaller(){ host('eval'); }\n//# sourceURL=eval-source.js");
function normal(){ host('normal'); }
new HostCtor();
evalCaller();
normal();
const wasmBytes = new Uint8Array([0,97,115,109,1,0,0,0,1,4,1,96,0,0,2,12,1,3,101,110,118,4,104,111,115,116,0,0,3,2,1,0,7,5,1,1,102,0,1,10,6,1,4,0,16,0,11]);
new WebAssembly.Instance(new WebAssembly.Module(wasmBytes), {env:{host(){ host('wasm'); }}}).exports.f();
//# sourceMappingURL=stack.js.map"#;
    let ran = compile_and_run(scope, source, Some(&script_origin)).is_some();
    let captures = std::mem::take(&mut *STACK_CAPTURES.lock().unwrap());
    vec![pass(
        "exceptions-advanced/stack/current_frames_and_limits",
        Json::obj(vec![
            ("run_succeeded", Json::b(ran)),
            ("captures", Json::arr(captures)),
        ]),
    )]
}

fn wasm_trap_message() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let source = r#"const b = new Uint8Array([0,97,115,109,1,0,0,0,1,4,1,96,0,0,3,2,1,0,7,5,1,1,102,0,0,10,5,1,3,0,0,11]);
new WebAssembly.Instance(new WebAssembly.Module(b)).exports.f();"#;
    let run_none = compile_and_run(tc, source, None).is_none();
    let message = tc.message().unwrap();
    let trace = v8::Exception::get_stack_trace(tc, tc.exception().unwrap());
    vec![pass(
        "exceptions-advanced/message/wasm_trap",
        Json::obj(vec![
            ("run_none", Json::b(run_none)),
            (
                "message",
                Json::s(&message.get(tc).to_rust_string_lossy(tc)),
            ),
            (
                "wasm_function_index",
                Json::i(i64::from(message.get_wasm_function_index())),
            ),
            ("exception_stack_trace_none", Json::b(trace.is_none())),
        ]),
    )]
}

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    try_catch_empty_and_reset,
    try_catch_verbose_reporting,
    runtime_exception_details,
    syntax_exception_details,
    capture_message_disabled,
    rethrow_propagation,
    caught_locals_outlive_try_catch,
    message_source_url_fallback,
    current_stack_frames_and_limits,
    wasm_trap_message,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let outcomes: Vec<CheckOutcome> = CHECKS.iter().flat_map(|check| check()).collect();
    let total = outcomes.len();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let failed = total - passed;
    let mut output = String::new();
    for outcome in &outcomes {
        output.push_str(&outcome.to_line());
        output.push('\n');
    }
    output.push_str(&summary_line(total, passed, failed));
    output.push('\n');
    print!("{output}");
    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
