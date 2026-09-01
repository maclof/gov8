//! Residual exception-constructor and `Exception::create_message` oracle for
//! v8 152.2.0. Script stack/source details already covered by the advanced
//! exceptions slice are only observed here where they define create_message's
//! reconstruction behavior.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::sync::Mutex;

fn text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|value| value.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn optional_text(
    scope: &mut v8::PinScope<'_, '_>,
    value: Option<v8::Local<'_, v8::Value>>,
) -> Json {
    value.map_or(Json::Null, |value| Json::s(&text(scope, value)))
}

fn constructor_matrix() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let values = [
        "Error",
        "RangeError",
        "ReferenceError",
        "SyntaxError",
        "TypeError",
    ]
    .into_iter()
    .map(|kind| {
        let input = v8::String::new(scope, "oracle-message").unwrap();
        let exception = match kind {
            "Error" => v8::Exception::error(scope, input),
            "RangeError" => v8::Exception::range_error(scope, input),
            "ReferenceError" => v8::Exception::reference_error(scope, input),
            "SyntaxError" => v8::Exception::syntax_error(scope, input),
            "TypeError" => v8::Exception::type_error(scope, input),
            _ => unreachable!(),
        };
        let object = v8::Local::<v8::Object>::try_from(exception).unwrap();
        let message_key = v8::String::new(scope, "message").unwrap();
        let message_property = object.get(scope, message_key.into()).unwrap();
        let constructor_key = v8::String::new(scope, kind).unwrap();
        let constructor = context
            .global(scope)
            .get(scope, constructor_key.into())
            .unwrap();
        let constructor = v8::Local::<v8::Object>::try_from(constructor).unwrap();
        let created_message = v8::Exception::create_message(scope, exception);
        Json::obj(vec![
            ("kind", Json::s(kind)),
            ("to_string", Json::s(&text(scope, exception))),
            ("message_property", Json::s(&text(scope, message_property))),
            ("is_object", Json::b(exception.is_object())),
            ("is_native_error", Json::b(exception.is_native_error())),
            (
                "constructor_name",
                Json::s(&object.get_constructor_name().to_rust_string_lossy(scope)),
            ),
            (
                "prototype_is_object",
                Json::b(object.get_prototype(scope).is_some_and(|p| p.is_object())),
            ),
            (
                "instance_of_matching",
                Json::b(exception.instance_of(scope, constructor).unwrap_or(false)),
            ),
            (
                "uncaught_message",
                Json::s(&created_message.get(scope).to_rust_string_lossy(scope)),
            ),
            (
                "exception_stack_none",
                Json::b(v8::Exception::get_stack_trace(scope, exception).is_none()),
            ),
        ])
    })
    .collect();
    vec![pass(
        "exception-constructors/constructors/five_native_error_kinds",
        Json::arr(values),
    )]
}

fn constructor_message_boundaries() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let empty = v8::Exception::type_error(scope, v8::String::empty(scope));
    let multiline =
        v8::Exception::error(scope, v8::String::new(scope, "first\nsecond 🦀").unwrap());
    vec![pass(
        "exception-constructors/constructors/message_boundaries",
        Json::obj(vec![
            ("empty_to_string", Json::s(&text(scope, empty))),
            (
                "empty_uncaught",
                Json::s(
                    &v8::Exception::create_message(scope, empty)
                        .get(scope)
                        .to_rust_string_lossy(scope),
                ),
            ),
            ("multiline_to_string", Json::s(&text(scope, multiline))),
            (
                "multiline_uncaught",
                Json::s(
                    &v8::Exception::create_message(scope, multiline)
                        .get(scope)
                        .to_rust_string_lossy(scope),
                ),
            ),
        ]),
    )]
}

fn create_message_primitives() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let symbol: v8::Local<v8::Value> =
        v8::Symbol::new(scope, Some(v8::String::new(scope, "sym").unwrap())).into();
    let values: [(&str, v8::Local<v8::Value>); 7] = [
        ("undefined", v8::undefined(scope).into()),
        ("null", v8::null(scope).into()),
        ("boolean", v8::Boolean::new(scope, true).into()),
        ("number", v8::Integer::new(scope, 42).into()),
        ("string", v8::String::new(scope, "plain").unwrap().into()),
        ("bigint", v8::BigInt::new_from_i64(scope, 99).into()),
        ("symbol", symbol),
    ];
    let messages = values
        .into_iter()
        .map(|(kind, value)| {
            let message = v8::Exception::create_message(scope, value);
            Json::obj(vec![
                ("kind", Json::s(kind)),
                (
                    "text",
                    Json::s(&message.get(scope).to_rust_string_lossy(scope)),
                ),
                (
                    "stack_none",
                    Json::b(message.get_stack_trace(scope).is_none()),
                ),
            ])
        })
        .collect();
    vec![pass(
        "exception-constructors/create-message/primitive_values",
        Json::arr(messages),
    )]
}

fn create_message_native_details() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let error =
        v8::Exception::range_error(scope, v8::String::new(scope, "native-details").unwrap());
    let message = v8::Exception::create_message(scope, error);
    let resource = message.get_script_resource_name(scope);
    let resource = optional_text(scope, resource);
    vec![pass(
        "exception-constructors/create-message/native_error_details",
        Json::obj(vec![
            (
                "text",
                Json::s(&message.get(scope).to_rust_string_lossy(scope)),
            ),
            (
                "source_line",
                message
                    .get_source_line(scope)
                    .map_or(Json::Null, |s| Json::s(&s.to_rust_string_lossy(scope))),
            ),
            ("resource", resource),
            (
                "line",
                message
                    .get_line_number(scope)
                    .map_or(Json::Null, |n| Json::i(n as i64)),
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
                Json::i(message.get_wasm_function_index() as i64),
            ),
            ("error_level", Json::i(message.error_level() as i64)),
            (
                "shared_cross_origin",
                Json::b(message.is_shared_cross_origin()),
            ),
            ("opaque", Json::b(message.is_opaque())),
            (
                "message_stack_none",
                Json::b(message.get_stack_trace(scope).is_none()),
            ),
            (
                "exception_stack_none",
                Json::b(v8::Exception::get_stack_trace(scope, error).is_none()),
            ),
        ]),
    )]
}

fn create_message_scripted_reconstruction() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let resource: v8::Local<v8::Value> = v8::String::new(scope, "constructors.js").unwrap().into();
    let origin = v8::ScriptOrigin::new(
        scope, resource, 0, 0, false, 0, None, false, false, false, None,
    );
    let source = "function makeError(){ return new Error('scripted'); }\nmakeError();";
    let source = v8::String::new(scope, source).unwrap();
    let error = v8::Script::compile(scope, source, Some(&origin))
        .unwrap()
        .run(scope)
        .unwrap();
    let exception_trace = v8::Exception::get_stack_trace(scope, error);
    let message = v8::Exception::create_message(scope, error);
    let resource = message.get_script_resource_name(scope);
    let resource = optional_text(scope, resource);
    vec![pass(
        "exception-constructors/create-message/scripted_error_reconstruction",
        Json::obj(vec![
            (
                "text",
                Json::s(&message.get(scope).to_rust_string_lossy(scope)),
            ),
            ("resource", resource),
            (
                "source_line",
                message
                    .get_source_line(scope)
                    .map_or(Json::Null, |s| Json::s(&s.to_rust_string_lossy(scope))),
            ),
            (
                "line",
                message
                    .get_line_number(scope)
                    .map_or(Json::Null, |n| Json::i(n as i64)),
            ),
            ("start_column", Json::i(message.get_start_column() as i64)),
            ("end_column", Json::i(message.get_end_column() as i64)),
            (
                "exception_frames",
                Json::i(exception_trace.map_or(0, |t| t.get_frame_count()) as i64),
            ),
            (
                "message_frames",
                Json::i(
                    message
                        .get_stack_trace(scope)
                        .map_or(0, |t| t.get_frame_count()) as i64,
                ),
            ),
        ]),
    )]
}

static CALLBACK_MESSAGE: Mutex<Option<Json>> = Mutex::new(None);

fn message_callback(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let message = v8::Exception::create_message(scope, args.get(0));
    *CALLBACK_MESSAGE.lock().unwrap() = Some(Json::obj(vec![
        (
            "text",
            Json::s(&message.get(scope).to_rust_string_lossy(scope)),
        ),
        (
            "source_line",
            message
                .get_source_line(scope)
                .map_or(Json::Null, |s| Json::s(&s.to_rust_string_lossy(scope))),
        ),
        (
            "line",
            message
                .get_line_number(scope)
                .map_or(Json::Null, |n| Json::i(n as i64)),
        ),
        (
            "frames",
            Json::i(
                message
                    .get_stack_trace(scope)
                    .map_or(0, |t| t.get_frame_count()) as i64,
            ),
        ),
    ]));
}

fn create_message_current_stack_fallback() -> Vec<CheckOutcome> {
    *CALLBACK_MESSAGE.lock().unwrap() = None;
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let function = v8::Function::new(scope, message_callback).unwrap();
    context.global(scope).set(
        scope,
        v8::String::new(scope, "nativeMessage").unwrap().into(),
        function.into(),
    );
    let resource: v8::Local<v8::Value> = v8::String::new(scope, "callback.js").unwrap().into();
    let origin = v8::ScriptOrigin::new(
        scope, resource, 0, 0, false, 0, None, false, false, false, None,
    );
    let source =
        v8::String::new(scope, "function outer(){ nativeMessage(17); }\nouter();").unwrap();
    let _ = v8::Script::compile(scope, source, Some(&origin))
        .unwrap()
        .run(scope);
    let value = CALLBACK_MESSAGE.lock().unwrap().take().unwrap();
    vec![pass(
        "exception-constructors/create-message/current_stack_fallback",
        value,
    )]
}

fn cross_context_lifecycle() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    let global = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let error =
            v8::Exception::type_error(scope, v8::String::new(scope, "cross-context").unwrap());
        v8::Global::new(scope, error)
    };
    let value = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let error = v8::Local::new(scope, &global);
        let constructor_key = v8::String::new(scope, "TypeError").unwrap();
        let constructor = context
            .global(scope)
            .get(scope, constructor_key.into())
            .unwrap();
        let constructor = v8::Local::<v8::Object>::try_from(constructor).unwrap();
        let message = v8::Exception::create_message(scope, error);
        Json::obj(vec![
            ("is_native_error", Json::b(error.is_native_error())),
            ("to_string", Json::s(&text(scope, error))),
            (
                "message",
                Json::s(&message.get(scope).to_rust_string_lossy(scope)),
            ),
            (
                "instance_of_second_context",
                Json::b(error.instance_of(scope, constructor).unwrap_or(false)),
            ),
        ])
    };
    vec![pass(
        "exception-constructors/lifecycle/cross_context_global",
        value,
    )]
}

type CheckFn = fn() -> Vec<CheckOutcome>;
const CHECKS: &[CheckFn] = &[
    constructor_matrix,
    constructor_message_boundaries,
    create_message_primitives,
    create_message_native_details,
    create_message_scripted_reconstruction,
    create_message_current_stack_fallback,
    cross_context_lifecycle,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let outcomes: Vec<_> = CHECKS.iter().flat_map(|check| check()).collect();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let failed = outcomes.len() - passed;
    let mut output = String::new();
    for outcome in &outcomes {
        output.push_str(&outcome.to_line());
        output.push('\n');
    }
    output.push_str(&summary_line(outcomes.len(), passed, failed));
    output.push('\n');
    print!("{output}");
    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
