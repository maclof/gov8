//! Isolate and context lifecycle checks.

use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};

pub(crate) fn context_script_roundtrip() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let value = harness::eval(scope, "40 + 2");
    let actual = Json::obj(vec![
        (
            "result_string",
            Json::s(
                &value
                    .as_ref()
                    .map(|v| harness::value_text(scope, *v))
                    .unwrap_or_default(),
            ),
        ),
        (
            "is_number",
            Json::b(value.as_ref().map(|v| v.is_number()).unwrap_or(false)),
        ),
        (
            "number_value",
            value
                .as_ref()
                .and_then(|v| v.number_value(scope))
                .map(Json::f)
                .unwrap_or(Json::Null),
        ),
    ]);
    let expected = Json::obj(vec![
        ("result_string", Json::s("42")),
        ("is_number", Json::b(true)),
        ("number_value", Json::f(42.0)),
    ]);
    vec![expect_eq(
        "isolate/context_script_roundtrip",
        expected,
        actual,
    )]
}

pub(crate) fn sequential_isolates() -> Vec<CheckOutcome> {
    let mut observed = Vec::new();
    for i in 1..=3i64 {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let source = format!("'iso-' + {i}");
        observed.push(Json::s(
            &harness::eval_text(scope, &source).unwrap_or_default(),
        ));
    }
    let expected = Json::arr(vec![Json::s("iso-1"), Json::s("iso-2"), Json::s("iso-3")]);
    vec![expect_eq(
        "isolate/sequential_isolates",
        expected,
        Json::arr(observed),
    )]
}

pub(crate) fn global_object_native_access() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    harness::eval(scope, "globalThis.gv = 7;").unwrap();
    let global = context.global(scope);

    let key = v8::String::new(scope, "gv").unwrap();
    let read_back = global
        .get(scope, key.into())
        .as_ref()
        .and_then(|v| v.integer_value(scope));

    let nkey = v8::String::new(scope, "nv").unwrap();
    let nval = v8::Number::new(scope, 42.0);
    let set_result = global.set(scope, nkey.into(), nval.into());

    let script_read = harness::eval_text(scope, "nv").unwrap_or_default();

    let actual = Json::obj(vec![
        (
            "read_back_int",
            read_back.map(Json::i).unwrap_or(Json::Null),
        ),
        ("set_result", Json::b(set_result.unwrap_or(false))),
        ("script_read", Json::s(&script_read)),
    ]);
    let expected = Json::obj(vec![
        ("read_back_int", Json::i(7)),
        ("set_result", Json::b(true)),
        ("script_read", Json::s("42")),
    ]);
    vec![expect_eq(
        "isolate/global_object_native_access",
        expected,
        actual,
    )]
}

pub(crate) fn context_reports_default_microtask_queue() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let policy = match scope.get_microtasks_policy() {
        v8::MicrotasksPolicy::Explicit => "Explicit",
        v8::MicrotasksPolicy::Auto => "Auto",
    };
    let actual = Json::obj(vec![
        ("default_policy", Json::s(policy)),
        (
            "context_has_microtask_queue",
            Json::b(context.get_microtask_queue().is_some()),
        ),
    ]);
    // A default context does carry the isolate's default microtask queue.
    let expected = Json::obj(vec![
        ("default_policy", Json::s("Auto")),
        ("context_has_microtask_queue", Json::b(true)),
    ]);
    vec![expect_eq(
        "isolate/context_reports_default_microtask_queue",
        expected,
        actual,
    )]
}
