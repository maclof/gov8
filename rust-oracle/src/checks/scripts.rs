//! Script compilation and execution success checks.

use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};

pub(crate) fn arithmetic() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let result = harness::eval_text(scope, "40 + 2");
    let actual = Json::obj(vec![
        ("value", Json::s(&result.clone().unwrap_or_default())),
        ("succeeded", Json::b(result.is_some())),
    ]);
    let expected = Json::obj(vec![("value", Json::s("42")), ("succeeded", Json::b(true))]);
    vec![expect_eq("script/arithmetic", expected, actual)]
}

pub(crate) fn string_concat() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let result = harness::eval_text(scope, "'go' + 'v8' + ' ' + 1");
    let expected_value = Json::s("gov8 1");
    let actual = Json::obj(vec![
        ("value", Json::s(&result.clone().unwrap_or_default())),
        ("succeeded", Json::b(result.is_some())),
    ]);
    let expected = Json::obj(vec![
        ("value", expected_value),
        ("succeeded", Json::b(true)),
    ]);
    vec![expect_eq("script/string_concat", expected, actual)]
}

pub(crate) fn value_types() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    harness::eval(scope, "globalThis.__x = { b: 2, a: 1 };").unwrap();
    let obj = harness::eval(scope, "__x").unwrap();
    let obj_json = harness::eval_text(scope, "JSON.stringify(__x)").unwrap_or_default();

    harness::eval(scope, "globalThis.__x = [1, 2, 3];").unwrap();
    let arr = harness::eval(scope, "__x").unwrap();
    let arr_json = harness::eval_text(scope, "JSON.stringify(__x)").unwrap_or_default();

    harness::eval(scope, "globalThis.__f = function named(x) { return x; };").unwrap();
    let func = harness::eval(scope, "__f").unwrap();

    let actual = Json::obj(vec![
        (
            "object",
            Json::obj(vec![
                ("is_object", Json::b(obj.is_object())),
                ("json", Json::s(&obj_json)),
            ]),
        ),
        (
            "array",
            Json::obj(vec![
                ("is_array", Json::b(arr.is_array())),
                ("is_object", Json::b(arr.is_object())),
                ("json", Json::s(&arr_json)),
            ]),
        ),
        (
            "function",
            Json::obj(vec![
                ("is_function", Json::b(func.is_function())),
                ("is_object", Json::b(func.is_object())),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "object",
            Json::obj(vec![
                ("is_object", Json::b(true)),
                ("json", Json::s("{\"b\":2,\"a\":1}")),
            ]),
        ),
        (
            "array",
            Json::obj(vec![
                ("is_array", Json::b(true)),
                ("is_object", Json::b(true)),
                ("json", Json::s("[1,2,3]")),
            ]),
        ),
        (
            "function",
            Json::obj(vec![
                ("is_function", Json::b(true)),
                ("is_object", Json::b(true)),
            ]),
        ),
    ]);
    vec![expect_eq("script/value_types", expected, actual)]
}

pub(crate) fn script_ids_distinct_and_increasing() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Compiling identical source twice resolves to the same Script through
    // V8's compilation cache, so the script id is identical.
    let first = harness::compile(scope, "1 + 1").unwrap();
    let id_first = first.script_id();
    let second = harness::compile(scope, "1 + 1").unwrap();
    let id_second = second.script_id();
    // Distinct source gets a distinct (and increasing) id.
    let third = harness::compile(scope, "2 + 2").unwrap();
    let id_third = third.script_id();

    let actual = Json::obj(vec![
        ("same_source_same_id", Json::b(id_first == id_second)),
        (
            "different_source_different_id",
            Json::b(id_first != id_third),
        ),
        ("increasing", Json::b(id_third > id_first)),
    ]);
    let expected = Json::obj(vec![
        ("same_source_same_id", Json::b(true)),
        ("different_source_different_id", Json::b(true)),
        ("increasing", Json::b(true)),
    ]);
    vec![expect_eq(
        "script/script_ids_distinct_and_increasing",
        expected,
        actual,
    )]
}

pub(crate) fn empty_source() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let result = harness::eval(scope, "");
    let actual = Json::obj(vec![
        ("compiles", Json::b(result.is_some())),
        (
            "result_is_undefined",
            Json::b(result.as_ref().map(|v| v.is_undefined()).unwrap_or(false)),
        ),
    ]);
    let expected = Json::obj(vec![
        ("compiles", Json::b(true)),
        ("result_is_undefined", Json::b(true)),
    ]);
    vec![expect_eq("script/empty_source", expected, actual)]
}
