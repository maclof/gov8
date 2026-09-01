//! Residual `Eternal` and `TracedReference` handle oracle for v8 152.2.0.
//!
//! `TracedReference` is only dereferenced while its target is known live. The
//! GC case roots the target with an Eternal, avoiding invented cppgc setup.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

fn property_text(
    scope: &mut v8::PinScope<'_, '_>,
    object: v8::Local<'_, v8::Object>,
    key: &str,
) -> String {
    let key = v8::String::new(scope, key).unwrap();
    object
        .get(scope, key.into())
        .and_then(|value| value.to_string(scope))
        .map(|value| value.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn eternal_empty_set_clear_reuse() -> Vec<CheckOutcome> {
    let eternal = v8::Eternal::<v8::String>::empty();
    let initial_empty = eternal.is_empty();
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let initial_get_none = eternal.get(scope).is_none();
    let first = v8::String::new(scope, "first").unwrap();
    eternal.set(scope, first);
    let after_set_empty = eternal.is_empty();
    let first_get = eternal.get(scope).unwrap();
    let first_identity = first.strict_equals(first_get.into());
    eternal.clear();
    let after_clear_empty = eternal.is_empty();
    let after_clear_get_none = eternal.get(scope).is_none();
    let second = v8::String::new(scope, "second").unwrap();
    eternal.set(scope, second);
    let second_text = eternal.get(scope).unwrap().to_rust_string_lossy(scope);
    eternal.clear();
    vec![pass(
        "handles-residual/eternal/empty_set_clear_reuse",
        Json::obj(vec![
            ("initial_empty", Json::b(initial_empty)),
            ("initial_get_none", Json::b(initial_get_none)),
            ("after_set_empty", Json::b(after_set_empty)),
            ("first_identity", Json::b(first_identity)),
            ("after_clear_empty", Json::b(after_clear_empty)),
            ("after_clear_get_none", Json::b(after_clear_get_none)),
            ("second_text", Json::s(&second_text)),
            ("final_empty", Json::b(eternal.is_empty())),
        ]),
    )]
}

fn eternal_object_across_scopes_and_gc() -> Vec<CheckOutcome> {
    let eternal = v8::Eternal::<v8::Object>::empty();
    let mut isolate = v8::Isolate::new(Default::default());
    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let object = v8::Object::new(scope);
        object.set(
            scope,
            v8::String::new(scope, "marker").unwrap().into(),
            v8::String::new(scope, "alive").unwrap().into(),
        );
        eternal.set(scope, object);
    }
    isolate.low_memory_notification();
    let (marker, repeated_identity) = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let first = eternal.get(scope).unwrap();
        let second = eternal.get(scope).unwrap();
        (
            property_text(scope, first, "marker"),
            first.strict_equals(second.into()),
        )
    };
    eternal.clear();
    vec![pass(
        "handles-residual/eternal/object_across_scopes_gc",
        Json::obj(vec![
            ("marker", Json::s(&marker)),
            ("repeated_get_identity", Json::b(repeated_identity)),
            ("empty_after_clear", Json::b(eternal.is_empty())),
        ]),
    )]
}

fn eternal_cross_context_realm() -> Vec<CheckOutcome> {
    let eternal = v8::Eternal::<v8::Object>::empty();
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let first_context = v8::Context::new(scope, Default::default());
    let original = {
        let scope = &mut v8::ContextScope::new(scope, first_context);
        let object = v8::Object::new(scope);
        eternal.set(scope, object);
        object
    };
    let second_context = v8::Context::new(scope, Default::default());
    let (identity, second_realm_instanceof) = {
        let scope = &mut v8::ContextScope::new(scope, second_context);
        let value = eternal.get(scope).unwrap();
        let key = v8::String::new(scope, "Object").unwrap();
        let constructor = second_context.global(scope).get(scope, key.into()).unwrap();
        let constructor = v8::Local::<v8::Object>::try_from(constructor).unwrap();
        (
            original.strict_equals(value.into()),
            value.instance_of(scope, constructor).unwrap_or(false),
        )
    };
    eternal.clear();
    vec![pass(
        "handles-residual/eternal/cross_context_realm",
        Json::obj(vec![
            ("identity_preserved", Json::b(identity)),
            (
                "instance_of_second_context_object",
                Json::b(second_realm_instanceof),
            ),
        ]),
    )]
}

fn eternal_standalone_after_isolate() -> Vec<CheckOutcome> {
    let eternal = v8::Eternal::<v8::Value>::empty();
    {
        let mut isolate = v8::Isolate::new(Default::default());
        v8::scope!(let scope, &mut isolate);
        let value: v8::Local<v8::Value> = v8::Integer::new(scope, 7).into();
        eternal.set(scope, value);
        eternal.clear();
    }
    let empty_after_isolate = eternal.is_empty();
    eternal.clear();
    vec![pass(
        "handles-residual/eternal/cleared_after_isolate_lifecycle",
        Json::obj(vec![
            ("empty_after_isolate", Json::b(empty_after_isolate)),
            ("clear_after_isolate_safe", Json::b(eternal.is_empty())),
        ]),
    )]
}

fn traced_empty_reset_reuse() -> Vec<CheckOutcome> {
    let mut traced = v8::TracedReference::<v8::Value>::empty();
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let initial_none = traced.get(scope).is_none();
    let first: v8::Local<v8::Value> = v8::Integer::new(scope, 11).into();
    traced.reset(scope, Some(first));
    let first_value = traced.get(scope).and_then(|v| v.integer_value(scope));
    traced.reset(scope, None);
    let reset_none = traced.get(scope).is_none();
    let second: v8::Local<v8::Value> = v8::String::new(scope, "reused").unwrap().into();
    traced.reset(scope, Some(second));
    let second_value = traced
        .get(scope)
        .and_then(|v| v.to_string(scope))
        .map(|v| v.to_rust_string_lossy(scope));
    traced.reset(scope, None);
    vec![pass(
        "handles-residual/traced/empty_reset_reuse",
        Json::obj(vec![
            ("initial_get_none", Json::b(initial_none)),
            ("first_value", first_value.map_or(Json::Null, Json::i)),
            ("after_reset_none", Json::b(reset_none)),
            (
                "second_value",
                second_value.as_deref().map_or(Json::Null, Json::s),
            ),
            ("final_get_none", Json::b(traced.get(scope).is_none())),
        ]),
    )]
}

fn traced_object_identity_and_mutation() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = v8::Object::new(scope);
    let mut traced = v8::TracedReference::new(scope, object);
    let retrieved = traced.get(scope).unwrap();
    retrieved.set(
        scope,
        v8::String::new(scope, "value").unwrap().into(),
        v8::Integer::new(scope, 42).into(),
    );
    let identity = object.strict_equals(retrieved.into());
    let value = property_text(scope, object, "value");
    traced.reset(scope, None);
    vec![pass(
        "handles-residual/traced/object_identity_mutation",
        Json::obj(vec![
            ("identity", Json::b(identity)),
            ("mutation_visible", Json::s(&value)),
            ("reset_get_none", Json::b(traced.get(scope).is_none())),
        ]),
    )]
}

fn traced_cross_context_realm() -> Vec<CheckOutcome> {
    let mut traced = v8::TracedReference::<v8::Object>::empty();
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let first_context = v8::Context::new(scope, Default::default());
    let original = {
        let scope = &mut v8::ContextScope::new(scope, first_context);
        let object = v8::Object::new(scope);
        traced.reset(scope, Some(object));
        object
    };
    let second_context = v8::Context::new(scope, Default::default());
    let (identity, second_realm_instanceof) = {
        let scope = &mut v8::ContextScope::new(scope, second_context);
        let value = traced.get(scope).unwrap();
        let key = v8::String::new(scope, "Object").unwrap();
        let constructor = second_context.global(scope).get(scope, key.into()).unwrap();
        let constructor = v8::Local::<v8::Object>::try_from(constructor).unwrap();
        (
            original.strict_equals(value.into()),
            value.instance_of(scope, constructor).unwrap_or(false),
        )
    };
    traced.reset(scope, None);
    vec![pass(
        "handles-residual/traced/cross_context_realm",
        Json::obj(vec![
            ("identity_preserved", Json::b(identity)),
            (
                "instance_of_second_context_object",
                Json::b(second_realm_instanceof),
            ),
        ]),
    )]
}

fn traced_externally_rooted_gc() -> Vec<CheckOutcome> {
    let eternal = v8::Eternal::<v8::Object>::empty();
    let mut traced = v8::TracedReference::<v8::Object>::empty();
    let mut isolate = v8::Isolate::new(Default::default());
    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let object = v8::Object::new(scope);
        object.set(
            scope,
            v8::String::new(scope, "rooted").unwrap().into(),
            v8::String::new(scope, "yes").unwrap().into(),
        );
        eternal.set(scope, object);
        traced.reset(scope, Some(object));
    }
    isolate.low_memory_notification();
    let (available, same_as_root, marker) = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let traced_value = traced.get(scope);
        let root = eternal.get(scope).unwrap();
        (
            traced_value.is_some(),
            traced_value.is_some_and(|value| value.strict_equals(root.into())),
            traced_value.map_or_else(String::new, |value| property_text(scope, value, "rooted")),
        )
    };
    {
        v8::scope!(let scope, &mut isolate);
        traced.reset(scope, None);
    }
    eternal.clear();
    isolate.low_memory_notification();
    vec![pass(
        "handles-residual/traced/externally_rooted_gc",
        Json::obj(vec![
            ("available_after_gc", Json::b(available)),
            ("same_as_eternal_root", Json::b(same_as_root)),
            ("marker", Json::s(&marker)),
            ("reset_before_unroot", Json::b(true)),
        ]),
    )]
}

type CheckFn = fn() -> Vec<CheckOutcome>;
const CHECKS: &[CheckFn] = &[
    eternal_empty_set_clear_reuse,
    eternal_object_across_scopes_and_gc,
    eternal_cross_context_realm,
    eternal_standalone_after_isolate,
    traced_empty_reset_reuse,
    traced_object_identity_and_mutation,
    traced_cross_context_realm,
    traced_externally_rooted_gc,
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
