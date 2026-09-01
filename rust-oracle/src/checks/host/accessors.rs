//! Accessor callback checks: native data property accessors (getter and
//! setter) on object templates and static accessors on constructors.
//!
//! Characterized contract (the Go port must reproduce):
//! - `ObjectTemplate::set_accessor_with_setter` installs a native data
//!   property: every read invokes the getter, every write invokes the
//!   setter, and no storage backing the property exists (the property value
//!   stays the getter's value after an intercepted write).
//! - The setter receives the assigned value; assignments do not create a
//!   plain data property. `getOwnPropertyDescriptor` synthesizes a
//!   *data-shaped* descriptor (`value` from the getter, `writable` from
//!   setter presence), not an accessor-shaped one.
//! - `FunctionTemplate::set_accessor_property` installs a getter as a
//!   *static* property on the constructor function itself.

use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};
use std::cell::Cell;

thread_local! {
    /// Records the last value passed to the accessor setter. Single-threaded
    /// check execution makes this deterministic.
    static SETTER_SEEN: Cell<Option<i64>> = const { Cell::new(None) };
}

fn acc_getter(
    _scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    rv.set_int32(7);
}

fn acc_setter(
    scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<'_, v8::Name>,
    value: v8::Local<'_, v8::Value>,
    _args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, ()>,
) {
    SETTER_SEEN.with(|cell| cell.set(value.integer_value(scope)));
}

/// The static accessor is installed as a getter *function template*, so its
/// callback is a plain function callback.
fn static_getter_fn(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    rv.set_int32(33);
}

fn cb_noop(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
}

pub(crate) fn native_data_property_getter_setter() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    ot.set_accessor_with_setter(
        v8::String::new(scope, "prop").unwrap().into(),
        acc_getter,
        acc_setter,
    );
    let obj = ot.new_instance(scope).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "o").unwrap().into(),
            obj.into(),
        )
        .unwrap();

    let getter_value = harness::eval_text(scope, "o.prop").unwrap_or_default();
    let setter_seen_before = SETTER_SEEN.with(Cell::get);

    // The write is intercepted by the setter; the read goes to the getter,
    // so the property value does not change and no storage exists.
    let after_write = harness::eval_text(scope, "o.prop = 11; o.prop").unwrap_or_default();
    let setter_seen_after = SETTER_SEEN.with(Cell::get);

    // The own property is NOT accessor-shaped: for a native data property
    // V8 synthesizes a data descriptor from the getter's value with
    // writable reflecting setter presence. Functions are omitted by
    // JSON.stringify in the accessor case, so the observed descriptor is
    // pinned verbatim.
    let descriptor = harness::eval_text(
        scope,
        "JSON.stringify(Object.getOwnPropertyDescriptor(o, 'prop'))",
    )
    .unwrap_or_default();

    let actual = Json::obj(vec![
        ("getter_value", Json::s(&getter_value)),
        (
            "setter_seen_before",
            setter_seen_before.map(Json::i).unwrap_or(Json::Null),
        ),
        ("after_write", Json::s(&after_write)),
        (
            "setter_seen_after",
            setter_seen_after.map(Json::i).unwrap_or(Json::Null),
        ),
        ("descriptor", Json::s(&descriptor)),
    ]);
    let expected = Json::obj(vec![
        ("getter_value", Json::s("7")),
        ("setter_seen_before", Json::Null),
        ("after_write", Json::s("7")),
        ("setter_seen_after", Json::i(11)),
        (
            "descriptor",
            Json::s("{\"value\":7,\"writable\":true,\"enumerable\":true,\"configurable\":true}"),
        ),
    ]);
    vec![expect_eq(
        "accessor/native_data_property_getter_setter",
        expected,
        actual,
    )]
}

pub(crate) fn static_accessor_on_constructor() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ft = v8::FunctionTemplate::new(scope, cb_noop);
    let getter_ft = v8::FunctionTemplate::new(scope, static_getter_fn);
    ft.set_accessor_property(
        v8::String::new(scope, "stat").unwrap().into(),
        Some(getter_ft),
        None,
        v8::PropertyAttribute::NONE,
    );

    let ctor = ft.get_function(scope).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "C").unwrap().into(),
            ctor.into(),
        )
        .unwrap();

    let static_read = harness::eval_text(scope, "C.stat").unwrap_or_default();
    // The accessor lives on the constructor, not on its prototype.
    let not_on_prototype = harness::eval_text(scope, "C.prototype.stat").unwrap_or_default();

    let actual = Json::obj(vec![
        ("static_read", Json::s(&static_read)),
        ("not_on_prototype", Json::s(&not_on_prototype)),
    ]);
    let expected = Json::obj(vec![
        ("static_read", Json::s("33")),
        ("not_on_prototype", Json::s("undefined")),
    ]);
    vec![expect_eq(
        "accessor/static_accessor_on_constructor",
        expected,
        actual,
    )]
}
