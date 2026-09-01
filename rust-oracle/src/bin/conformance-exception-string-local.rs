//! Exception constructors with arbitrary V8 `String` local inputs.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

const KINDS: [&str; 5] = [
    "Error",
    "RangeError",
    "ReferenceError",
    "SyntaxError",
    "TypeError",
];

fn construct<'s>(
    scope: &v8::PinScope<'s, '_>,
    kind: &str,
    message: v8::Local<v8::String>,
) -> v8::Local<'s, v8::Value> {
    match kind {
        "Error" => v8::Exception::error(scope, message),
        "RangeError" => v8::Exception::range_error(scope, message),
        "ReferenceError" => v8::Exception::reference_error(scope, message),
        "SyntaxError" => v8::Exception::syntax_error(scope, message),
        "TypeError" => v8::Exception::type_error(scope, message),
        _ => unreachable!(),
    }
}

fn text(scope: &v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|value| value.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn code_units(scope: &v8::Isolate, value: v8::Local<v8::String>) -> Json {
    let mut units = vec![0_u16; value.length()];
    value.write_v2(scope, 0, &mut units, v8::WriteFlags::empty());
    Json::arr(
        units
            .into_iter()
            .map(|unit| Json::i(i64::from(unit)))
            .collect(),
    )
}

fn string_flags(value: v8::Local<v8::String>) -> Json {
    Json::obj(vec![
        ("external", Json::b(value.is_external())),
        ("external_one_byte", Json::b(value.is_external_onebyte())),
        ("external_two_byte", Json::b(value.is_external_twobyte())),
    ])
}

fn inputs<'s>(scope: &v8::PinScope<'s, '_, ()>) -> Vec<(&'static str, v8::Local<'s, v8::String>)> {
    vec![
        (
            "ordinary_utf8",
            v8::String::new_from_utf8(scope, "héllo 🦀".as_bytes(), v8::NewStringType::Normal)
                .unwrap(),
        ),
        (
            "embedded_nul",
            v8::String::new_from_utf8(scope, b"left\0right", v8::NewStringType::Normal).unwrap(),
        ),
        (
            "utf16_lone_surrogate",
            v8::String::new_from_two_byte(scope, &[0xd800, b'X' as u16], v8::NewStringType::Normal)
                .unwrap(),
        ),
        (
            "internalized",
            v8::String::new_from_utf8(
                scope,
                b"internalized-value",
                v8::NewStringType::Internalized,
            )
            .unwrap(),
        ),
        (
            "external_one_byte",
            v8::String::new_external_onebyte(
                scope,
                Box::<[u8]>::from([b'e', b'x', b't', b'-', 0xa9]),
            )
            .unwrap(),
        ),
        (
            "external_two_byte",
            v8::String::new_external_twobyte(
                scope,
                Box::<[u16]>::from([b'e' as u16, b'x' as u16, b't' as u16, 0x20ac]),
            )
            .unwrap(),
        ),
    ]
}

fn constructor_observation(
    scope: &mut v8::PinScope<'_, '_>,
    context: v8::Local<v8::Context>,
    kind: &str,
    input: v8::Local<v8::String>,
) -> Json {
    let exception = construct(scope, kind, input);
    let object: v8::Local<v8::Object> = exception.try_into().unwrap();
    let message_key = v8::String::new(scope, "message").unwrap();
    let stack_key = v8::String::new(scope, "stack").unwrap();
    let message = object.get(scope, message_key.into()).unwrap();
    let stack = object.get(scope, stack_key.into()).unwrap();
    let constructor_key = v8::String::new(scope, kind).unwrap();
    let constructor: v8::Local<v8::Object> = context
        .global(scope)
        .get(scope, constructor_key.into())
        .unwrap()
        .try_into()
        .unwrap();
    let prototype_key = v8::String::new(scope, "prototype").unwrap();
    let expected_prototype = constructor.get(scope, prototype_key.into()).unwrap();
    let actual_prototype = object.get_prototype(scope).unwrap();
    let created_message = v8::Exception::create_message(scope, exception);
    Json::obj(vec![
        ("kind", Json::s(kind)),
        ("is_object", Json::b(exception.is_object())),
        ("is_native_error", Json::b(exception.is_native_error())),
        ("is_string", Json::b(exception.is_string())),
        (
            "constructor_name",
            Json::s(&object.get_constructor_name().to_rust_string_lossy(scope)),
        ),
        (
            "instance_of_matching",
            Json::b(exception.instance_of(scope, constructor).unwrap()),
        ),
        (
            "prototype_is_matching",
            Json::b(actual_prototype.strict_equals(expected_prototype)),
        ),
        ("to_string", Json::s(&text(scope, exception))),
        ("message", Json::s(&text(scope, message))),
        ("stack_is_string", Json::b(stack.is_string())),
        ("stack", Json::s(&text(scope, stack))),
        (
            "exception_stack_none",
            Json::b(v8::Exception::get_stack_trace(scope, exception).is_none()),
        ),
        (
            "message_stack_none",
            Json::b(created_message.get_stack_trace(scope).is_none()),
        ),
        (
            "uncaught_message",
            Json::s(&created_message.get(scope).to_rust_string_lossy(scope)),
        ),
    ])
}

fn five_constructors_by_string_kind() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let groups = inputs(scope)
        .into_iter()
        .map(|(input_kind, input)| {
            let constructors = KINDS
                .into_iter()
                .map(|kind| constructor_observation(scope, context, kind, input))
                .collect();
            Json::obj(vec![
                ("input_kind", Json::s(input_kind)),
                ("constructors", Json::arr(constructors)),
            ])
        })
        .collect();
    vec![pass(
        "exception-string-local/five_constructors_by_string_kind",
        Json::arr(groups),
    )]
}

fn identity_observation(
    scope: &mut v8::PinScope<'_, '_>,
    kind: &str,
    input: v8::Local<v8::String>,
) -> Json {
    let exception = construct(scope, kind, input);
    let object: v8::Local<v8::Object> = exception.try_into().unwrap();
    let message_key = v8::String::new(scope, "message").unwrap();
    let property_value = object.get(scope, message_key.into()).unwrap();
    let property: v8::Local<v8::String> = property_value.try_into().unwrap();
    let (input_resource, _) = input.get_external_string_resource_base();
    let (property_resource, _) = property.get_external_string_resource_base();
    Json::obj(vec![
        ("kind", Json::s(kind)),
        (
            "strict_equals",
            Json::b(input.strict_equals(property.into())),
        ),
        (
            "property_text",
            Json::s(&property.to_rust_string_lossy(scope)),
        ),
        ("property_code_units", code_units(scope, property)),
        ("property_flags", string_flags(property)),
        (
            "same_external_resource",
            Json::b(input_resource.is_some() && input_resource == property_resource),
        ),
    ])
}

fn input_and_message_identity() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let values = inputs(scope)
        .into_iter()
        .map(|(input_kind, input)| {
            let constructors = KINDS
                .into_iter()
                .map(|kind| identity_observation(scope, kind, input))
                .collect();
            Json::obj(vec![
                ("input_kind", Json::s(input_kind)),
                ("input_text", Json::s(&input.to_rust_string_lossy(scope))),
                ("input_code_units", code_units(scope, input)),
                ("input_flags", string_flags(input)),
                ("constructors", Json::arr(constructors)),
            ])
        })
        .collect();
    vec![pass(
        "exception-string-local/input_and_message_identity",
        Json::arr(values),
    )]
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut checks = five_constructors_by_string_kind();
    checks.extend(input_and_message_identity());
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
