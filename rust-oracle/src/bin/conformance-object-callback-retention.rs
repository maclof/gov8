//! Object accessor-configuration and lazy-data callback retention oracle.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::cell::{Cell, RefCell};

thread_local! {
    static ACCESSOR_GET_HITS: Cell<u32> = const { Cell::new(0) };
    static ACCESSOR_SET_HITS: Cell<u32> = const { Cell::new(0) };
    static ACCESSOR_STATE: Cell<i64> = const { Cell::new(5) };
    static ACCESSOR_DATA_SELF: Cell<bool> = const { Cell::new(true) };
    static ACCESSOR_KEY_OK: Cell<bool> = const { Cell::new(true) };
    static ACCESSOR_HOLDER_TAG_OK: Cell<bool> = const { Cell::new(true) };
    static ACCESSOR_GET_THROW: RefCell<Vec<bool>> = const { RefCell::new(Vec::new()) };
    static ACCESSOR_SET_THROW: RefCell<Vec<bool>> = const { RefCell::new(Vec::new()) };
    static REPLACEMENT_MARKERS: RefCell<Vec<i64>> = const { RefCell::new(Vec::new()) };
    static LAZY_HITS: Cell<u32> = const { Cell::new(0) };
    static LAZY_DATA_SELF: Cell<bool> = const { Cell::new(true) };
    static LAZY_KEY_OK: Cell<bool> = const { Cell::new(true) };
    static LAZY_HOLDER_TAG_OK: Cell<bool> = const { Cell::new(true) };
    static LAZY_THROW_FLAGS: RefCell<Vec<bool>> = const { RefCell::new(Vec::new()) };
    static LAZY_THROW_HITS: Cell<u32> = const { Cell::new(0) };
    static LAZY_EMPTY_HITS: Cell<u32> = const { Cell::new(0) };
}

fn reset_state() {
    ACCESSOR_GET_HITS.set(0);
    ACCESSOR_SET_HITS.set(0);
    ACCESSOR_STATE.set(5);
    ACCESSOR_DATA_SELF.set(true);
    ACCESSOR_KEY_OK.set(true);
    ACCESSOR_HOLDER_TAG_OK.set(true);
    ACCESSOR_GET_THROW.with_borrow_mut(Vec::clear);
    ACCESSOR_SET_THROW.with_borrow_mut(Vec::clear);
    REPLACEMENT_MARKERS.with_borrow_mut(Vec::clear);
    LAZY_HITS.set(0);
    LAZY_DATA_SELF.set(true);
    LAZY_KEY_OK.set(true);
    LAZY_HOLDER_TAG_OK.set(true);
    LAZY_THROW_FLAGS.with_borrow_mut(Vec::clear);
    LAZY_THROW_HITS.set(0);
    LAZY_EMPTY_HITS.set(0);
}

fn name<'s>(scope: &v8::PinScope<'s, '_, ()>, text: &str) -> v8::Local<'s, v8::Name> {
    v8::String::new(scope, text).unwrap().into()
}

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

fn eval_text(scope: &v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source)
        .map(|value| value.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn eval_integer(scope: &v8::PinScope<'_, '_>, source: &str) -> i64 {
    eval(scope, source)
        .and_then(|value| value.integer_value(scope))
        .unwrap_or(-1)
}

fn set_global(
    scope: &v8::PinScope<'_, '_>,
    context: v8::Local<v8::Context>,
    key: &str,
    value: v8::Local<v8::Value>,
) {
    context
        .global(scope)
        .set(scope, v8::String::new(scope, key).unwrap().into(), value)
        .unwrap();
}

fn data_object<'s>(scope: &v8::PinScope<'s, '_>, marker: i32) -> v8::Local<'s, v8::Object> {
    let data = v8::Object::new(scope);
    data.set(
        scope,
        v8::String::new(scope, "marker").unwrap().into(),
        v8::Integer::new(scope, marker).into(),
    )
    .unwrap();
    data.set(
        scope,
        v8::String::new(scope, "self").unwrap().into(),
        data.into(),
    )
    .unwrap();
    data
}

fn object_integer(scope: &v8::PinScope<'_, '_>, object: v8::Local<v8::Object>, key: &str) -> i64 {
    object
        .get(scope, v8::String::new(scope, key).unwrap().into())
        .and_then(|value| value.integer_value(scope))
        .unwrap_or(-1)
}

fn data_marker_and_self(scope: &v8::PinScope<'_, '_>, data: v8::Local<v8::Value>) -> (i64, bool) {
    let object = data.try_cast::<v8::Object>().unwrap();
    let marker = object_integer(scope, object, "marker");
    let self_value = object
        .get(scope, v8::String::new(scope, "self").unwrap().into())
        .unwrap();
    (marker, data.strict_equals(self_value))
}

fn holder_tag(scope: &v8::PinScope<'_, '_>, args: &v8::PropertyCallbackArguments<'_>) -> i64 {
    object_integer(scope, args.holder(), "holderTag")
}

fn key_text(scope: &v8::PinScope<'_, '_>, key: v8::Local<v8::Name>) -> String {
    let value: v8::Local<v8::Value> = key.into();
    value.to_rust_string_lossy(scope)
}

fn configured_getter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<v8::Name>,
    args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    ACCESSOR_GET_HITS.set(ACCESSOR_GET_HITS.get() + 1);
    let (marker, self_identity) = data_marker_and_self(scope, args.data());
    ACCESSOR_DATA_SELF.set(ACCESSOR_DATA_SELF.get() && self_identity && marker == 73);
    ACCESSOR_KEY_OK.set(ACCESSOR_KEY_OK.get() && key_text(scope, key) == "configured");
    ACCESSOR_HOLDER_TAG_OK.set(ACCESSOR_HOLDER_TAG_OK.get() && holder_tag(scope, &args) == 99);
    ACCESSOR_GET_THROW.with_borrow_mut(|flags| flags.push(args.should_throw_on_error()));
    rv.set_double(ACCESSOR_STATE.get() as f64);
}

fn configured_setter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<v8::Name>,
    value: v8::Local<v8::Value>,
    args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<()>,
) {
    ACCESSOR_SET_HITS.set(ACCESSOR_SET_HITS.get() + 1);
    let (marker, self_identity) = data_marker_and_self(scope, args.data());
    ACCESSOR_DATA_SELF.set(ACCESSOR_DATA_SELF.get() && self_identity && marker == 73);
    ACCESSOR_KEY_OK.set(ACCESSOR_KEY_OK.get() && key_text(scope, key) == "configured");
    ACCESSOR_HOLDER_TAG_OK.set(ACCESSOR_HOLDER_TAG_OK.get() && holder_tag(scope, &args) == 99);
    ACCESSOR_SET_THROW.with_borrow_mut(|flags| flags.push(args.should_throw_on_error()));
    ACCESSOR_STATE.set(value.integer_value(scope).unwrap_or(-1));
}

fn marker_getter(
    scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<v8::Name>,
    args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    let (marker, self_identity) = data_marker_and_self(scope, args.data());
    REPLACEMENT_MARKERS.with_borrow_mut(|markers| markers.push(marker));
    ACCESSOR_DATA_SELF.set(ACCESSOR_DATA_SELF.get() && self_identity);
    rv.set_double(marker as f64);
}

fn lazy_data_getter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<v8::Name>,
    args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    LAZY_HITS.set(LAZY_HITS.get() + 1);
    let (_, self_identity) = data_marker_and_self(scope, args.data());
    LAZY_DATA_SELF.set(LAZY_DATA_SELF.get() && self_identity);
    LAZY_KEY_OK.set(LAZY_KEY_OK.get() && key_text(scope, key) == "retained");
    LAZY_HOLDER_TAG_OK.set(LAZY_HOLDER_TAG_OK.get() && holder_tag(scope, &args) == 44);
    LAZY_THROW_FLAGS.with_borrow_mut(|flags| flags.push(args.should_throw_on_error()));
    rv.set(args.data());
}

fn lazy_marker_getter(
    scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<v8::Name>,
    args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    LAZY_HITS.set(LAZY_HITS.get() + 1);
    let (marker, self_identity) = data_marker_and_self(scope, args.data());
    LAZY_DATA_SELF.set(LAZY_DATA_SELF.get() && self_identity);
    rv.set_double(marker as f64);
}

fn lazy_throw_getter(
    scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<v8::Value>,
) {
    LAZY_THROW_HITS.set(LAZY_THROW_HITS.get() + 1);
    let message = v8::String::new(scope, "lazy boom").unwrap();
    let exception = v8::Exception::error(scope, message);
    scope.throw_exception(exception);
}

fn lazy_empty_getter(
    _scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<v8::Value>,
) {
    LAZY_EMPTY_HITS.set(LAZY_EMPTY_HITS.get() + 1);
}

fn property_attributes_json(attributes: v8::PropertyAttribute) -> Json {
    Json::obj(vec![
        ("bits", Json::i(i64::from(attributes.as_u32()))),
        ("read_only", Json::b(attributes.is_read_only())),
        ("dont_enum", Json::b(attributes.is_dont_enum())),
        ("dont_delete", Json::b(attributes.is_dont_delete())),
    ])
}

fn accessor_configuration_callbacks() -> Vec<CheckOutcome> {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = v8::Object::new(scope);
    object
        .set(
            scope,
            v8::String::new(scope, "holderTag").unwrap().into(),
            v8::Integer::new(scope, 99).into(),
        )
        .unwrap();
    set_global(scope, context, "configuredObject", object.into());

    let install = {
        v8::scope!(let inner, scope);
        let data = data_object(inner, 73);
        let configuration = v8::AccessorConfiguration::new(configured_getter)
            .setter(configured_setter)
            .data(data.into())
            .property_attribute(
                v8::PropertyAttribute::DONT_ENUM | v8::PropertyAttribute::DONT_DELETE,
            );
        object.set_accessor_with_configuration(inner, name(inner, "configured"), configuration)
    };
    scope.low_memory_notification();
    let first = eval_integer(scope, "configuredObject.configured");
    let reflect_set = eval_text(scope, "Reflect.set(configuredObject, 'configured', 11)");
    let strict_set = eval_integer(
        scope,
        "(() => { 'use strict'; return (configuredObject.configured = 12); })()",
    );
    let final_value = eval_integer(scope, "configuredObject.configured");
    let attributes = object
        .get_property_attributes(scope, name(scope, "configured").into())
        .unwrap();
    let enumerable = eval_text(
        scope,
        "Object.keys(configuredObject).includes('configured')",
    );
    let delete_result = eval_text(
        scope,
        "Reflect.deleteProperty(configuredObject, 'configured')",
    );

    vec![pass(
        "object-callback-retention/accessor_configuration",
        Json::obj(vec![
            ("install", install.map_or(Json::Null, Json::b)),
            ("first", Json::i(first)),
            ("reflect_set", Json::s(&reflect_set)),
            ("strict_set", Json::i(strict_set)),
            ("final_value", Json::i(final_value)),
            ("get_hits", Json::i(i64::from(ACCESSOR_GET_HITS.get()))),
            ("set_hits", Json::i(i64::from(ACCESSOR_SET_HITS.get()))),
            (
                "getter_should_throw",
                Json::arr(
                    ACCESSOR_GET_THROW
                        .with_borrow(|flags| flags.iter().copied().map(Json::b).collect()),
                ),
            ),
            (
                "setter_should_throw",
                Json::arr(
                    ACCESSOR_SET_THROW
                        .with_borrow(|flags| flags.iter().copied().map(Json::b).collect()),
                ),
            ),
            ("data_self_identity", Json::b(ACCESSOR_DATA_SELF.get())),
            ("key_matches", Json::b(ACCESSOR_KEY_OK.get())),
            ("holder_matches", Json::b(ACCESSOR_HOLDER_TAG_OK.get())),
            ("attributes", property_attributes_json(attributes)),
            ("enumerable", Json::s(&enumerable)),
            ("delete_result", Json::s(&delete_result)),
        ]),
    )]
}

fn accessor_replacement_and_read_only() -> Vec<CheckOutcome> {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = v8::Object::new(scope);
    set_global(scope, context, "replacementObject", object.into());
    let key = name(scope, "replaceable");
    let first_data = data_object(scope, 1);
    let first_install = object.set_accessor_with_configuration(
        scope,
        key,
        v8::AccessorConfiguration::new(marker_getter).data(first_data.into()),
    );
    let second_data = data_object(scope, 2);
    let second_install = object.set_accessor_with_configuration(
        scope,
        key,
        v8::AccessorConfiguration::new(marker_getter).data(second_data.into()),
    );
    let after_reinstall = eval_integer(scope, "replacementObject.replaceable");
    let markers_after_reinstall = REPLACEMENT_MARKERS.with_borrow(Clone::clone);
    let ordinary_replacement = object.define_own_property(
        scope,
        key,
        v8::Integer::new(scope, 88).into(),
        v8::PropertyAttribute::NONE,
    );
    let markers_after_define = REPLACEMENT_MARKERS.with_borrow(Clone::clone);
    let after_ordinary = eval_integer(scope, "replacementObject.replaceable");
    let markers_after_ordinary = REPLACEMENT_MARKERS.with_borrow(Clone::clone);

    let read_only = v8::Object::new(scope);
    set_global(scope, context, "readOnlyObject", read_only.into());
    let read_only_install = read_only.set_accessor_with_configuration(
        scope,
        name(scope, "ro"),
        v8::AccessorConfiguration::new(marker_getter)
            .setter(configured_setter)
            .data(data_object(scope, 3).into())
            .property_attribute(v8::PropertyAttribute::READ_ONLY),
    );
    let reflect_write = eval_text(scope, "Reflect.set(readOnlyObject, 'ro', 10)");
    v8::tc_scope!(let tc, scope);
    let strict_source = v8::String::new(tc, "'use strict'; readOnlyObject.ro = 10").unwrap();
    let strict_result =
        v8::Script::compile(tc, strict_source, None).and_then(|script| script.run(tc));
    let strict_exception = tc
        .exception()
        .map(|exception| exception.to_rust_string_lossy(tc))
        .unwrap_or_default();

    vec![pass(
        "object-callback-retention/accessor_replacement_read_only",
        Json::obj(vec![
            ("first_install", first_install.map_or(Json::Null, Json::b)),
            ("second_install", second_install.map_or(Json::Null, Json::b)),
            ("after_reinstall", Json::i(after_reinstall)),
            (
                "markers_after_reinstall",
                Json::arr(markers_after_reinstall.into_iter().map(Json::i).collect()),
            ),
            (
                "ordinary_replacement",
                ordinary_replacement.map_or(Json::Null, Json::b),
            ),
            (
                "markers_after_define",
                Json::arr(markers_after_define.into_iter().map(Json::i).collect()),
            ),
            ("after_ordinary", Json::i(after_ordinary)),
            (
                "markers_after_ordinary",
                Json::arr(markers_after_ordinary.into_iter().map(Json::i).collect()),
            ),
            (
                "read_only_install",
                read_only_install.map_or(Json::Null, Json::b),
            ),
            ("read_only_reflect_set", Json::s(&reflect_write)),
            (
                "read_only_setter_hits",
                Json::i(i64::from(ACCESSOR_SET_HITS.get())),
            ),
            ("strict_result_none", Json::b(strict_result.is_none())),
            ("strict_caught", Json::b(tc.has_caught())),
            ("strict_exception", Json::s(&strict_exception)),
        ]),
    )]
}

fn all_attributes() -> v8::PropertyAttribute {
    v8::PropertyAttribute::READ_ONLY
        | v8::PropertyAttribute::DONT_ENUM
        | v8::PropertyAttribute::DONT_DELETE
}

fn lazy_data_retention_and_attributes() -> Vec<CheckOutcome> {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = v8::Object::new(scope);
    object
        .set(
            scope,
            v8::String::new(scope, "holderTag").unwrap().into(),
            v8::Integer::new(scope, 44).into(),
        )
        .unwrap();
    set_global(scope, context, "lazyObject", object.into());
    let install = {
        v8::scope!(let inner, scope);
        let data = data_object(inner, 55);
        object.set_lazy_data_property_with_data(
            inner,
            name(inner, "retained"),
            lazy_data_getter,
            data.into(),
            all_attributes(),
            v8::SideEffectType::HasNoSideEffect,
            v8::SideEffectType::HasSideEffectToReceiver,
        )
    };
    let hits_before_attributes = LAZY_HITS.get();
    let before_attributes = object
        .get_property_attributes(scope, name(scope, "retained").into())
        .unwrap();
    let hits_after_attributes = LAZY_HITS.get();
    scope.low_memory_notification();
    let first = object.get(scope, name(scope, "retained").into()).unwrap();
    let second = object.get(scope, name(scope, "retained").into()).unwrap();
    let marker = first
        .try_cast::<v8::Object>()
        .map(|value| object_integer(scope, value, "marker"))
        .unwrap_or(-1);
    let after_attributes = object
        .get_property_attributes(scope, name(scope, "retained").into())
        .unwrap();
    let writable = eval_text(
        scope,
        "Object.getOwnPropertyDescriptor(lazyObject, 'retained').writable",
    );
    let enumerable = eval_text(
        scope,
        "Object.getOwnPropertyDescriptor(lazyObject, 'retained').enumerable",
    );
    let configurable = eval_text(
        scope,
        "Object.getOwnPropertyDescriptor(lazyObject, 'retained').configurable",
    );
    let reflect_set = eval_text(scope, "Reflect.set(lazyObject, 'retained', 9)");
    let reflect_delete = eval_text(scope, "Reflect.deleteProperty(lazyObject, 'retained')");

    vec![pass(
        "object-callback-retention/lazy_data_attributes",
        Json::obj(vec![
            ("install", install.map_or(Json::Null, Json::b)),
            (
                "hits_before_attributes",
                Json::i(i64::from(hits_before_attributes)),
            ),
            (
                "hits_after_attributes",
                Json::i(i64::from(hits_after_attributes)),
            ),
            (
                "attributes_before",
                property_attributes_json(before_attributes),
            ),
            ("marker", Json::i(marker)),
            (
                "first_second_identity",
                Json::b(first.strict_equals(second)),
            ),
            ("hits_after_reads", Json::i(i64::from(LAZY_HITS.get()))),
            ("data_self_identity", Json::b(LAZY_DATA_SELF.get())),
            ("key_matches", Json::b(LAZY_KEY_OK.get())),
            ("holder_matches", Json::b(LAZY_HOLDER_TAG_OK.get())),
            (
                "should_throw",
                Json::arr(
                    LAZY_THROW_FLAGS
                        .with_borrow(|flags| flags.iter().copied().map(Json::b).collect()),
                ),
            ),
            (
                "attributes_after",
                property_attributes_json(after_attributes),
            ),
            ("descriptor_writable", Json::s(&writable)),
            ("descriptor_enumerable", Json::s(&enumerable)),
            ("descriptor_configurable", Json::s(&configurable)),
            ("reflect_set", Json::s(&reflect_set)),
            ("reflect_delete", Json::s(&reflect_delete)),
        ]),
    )]
}

fn side_effect_type(index: usize) -> v8::SideEffectType {
    match index {
        0 => v8::SideEffectType::HasSideEffect,
        1 => v8::SideEffectType::HasNoSideEffect,
        2 => v8::SideEffectType::HasSideEffectToReceiver,
        _ => unreachable!(),
    }
}

fn lazy_side_effect_matrix() -> Vec<CheckOutcome> {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = v8::Object::new(scope);
    let names = [
        "HasSideEffect",
        "HasNoSideEffect",
        "HasSideEffectToReceiver",
    ];
    let mut cases = Vec::new();
    // V8 rejects `HasNoSideEffect` for setters with a process-fatal CHECK;
    // that invalid configuration is covered by the subprocess probe.
    for getter_index in 0..3 {
        for setter_index in [0, 2] {
            let property = format!("p{getter_index}{setter_index}");
            let marker = (getter_index * 3 + setter_index + 1) as i32;
            let data = data_object(scope, marker);
            let install = object.set_lazy_data_property_with_data(
                scope,
                name(scope, &property),
                lazy_marker_getter,
                data.into(),
                v8::PropertyAttribute::NONE,
                side_effect_type(getter_index),
                side_effect_type(setter_index),
            );
            let first = object
                .get(scope, name(scope, &property).into())
                .and_then(|value| value.integer_value(scope))
                .unwrap_or(-1);
            let second = object
                .get(scope, name(scope, &property).into())
                .and_then(|value| value.integer_value(scope))
                .unwrap_or(-1);
            cases.push(Json::obj(vec![
                ("getter", Json::s(names[getter_index])),
                ("setter", Json::s(names[setter_index])),
                ("install", install.map_or(Json::Null, Json::b)),
                ("first", Json::i(first)),
                ("second", Json::i(second)),
            ]));
        }
    }
    vec![pass(
        "object-callback-retention/lazy_side_effect_matrix",
        Json::obj(vec![
            ("cases", Json::arr(cases)),
            ("callback_hits", Json::i(i64::from(LAZY_HITS.get()))),
            ("all_data_self_identity", Json::b(LAZY_DATA_SELF.get())),
        ]),
    )]
}

fn caught_get(
    scope: &mut v8::PinScope<'_, '_>,
    object: v8::Local<v8::Object>,
    key: v8::Local<v8::Name>,
) -> (bool, bool, String) {
    v8::tc_scope!(let tc, scope);
    let value = object.get(tc, key.into());
    let exception = tc
        .exception()
        .map(|exception| exception.to_rust_string_lossy(tc))
        .unwrap_or_default();
    (value.is_some(), tc.has_caught(), exception)
}

fn caught_define(
    scope: &mut v8::PinScope<'_, '_>,
    object: v8::Local<v8::Object>,
    key: v8::Local<v8::Name>,
) -> (Option<bool>, bool, String) {
    v8::tc_scope!(let tc, scope);
    let result = object.define_own_property(
        tc,
        key,
        v8::Integer::new(tc, 99).into(),
        v8::PropertyAttribute::NONE,
    );
    let exception = tc
        .exception()
        .map(|exception| exception.to_rust_string_lossy(tc))
        .unwrap_or_default();
    (result, tc.has_caught(), exception)
}

fn lazy_throw_empty_and_install_failure() -> Vec<CheckOutcome> {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = v8::Object::new(scope);
    let throw_key = name(scope, "throws");
    let throw_install = object.set_lazy_data_property(scope, throw_key, lazy_throw_getter);
    let first = caught_get(scope, object, throw_key);
    let hits_after_first = LAZY_THROW_HITS.get();
    let second = caught_get(scope, object, throw_key);
    let hits_after_second = LAZY_THROW_HITS.get();
    let replace_after_throw = caught_define(scope, object, throw_key);
    let hits_after_replace = LAZY_THROW_HITS.get();
    let read_after_replace = caught_get(scope, object, throw_key);

    let empty_key = name(scope, "empty");
    let empty_install = object.set_lazy_data_property(scope, empty_key, lazy_empty_getter);
    let empty_first = object.get(scope, empty_key.into()).unwrap();
    let empty_second = object.get(scope, empty_key.into()).unwrap();

    let sealed = eval(scope, "Object.preventExtensions({})")
        .unwrap()
        .try_cast::<v8::Object>()
        .unwrap();
    v8::tc_scope!(let tc, scope);
    let accessor_failure = sealed.set_accessor_with_configuration(
        tc,
        name(tc, "a"),
        v8::AccessorConfiguration::new(marker_getter).data(data_object(tc, 4).into()),
    );
    let accessor_caught = tc.has_caught();
    tc.reset();
    let lazy_failure = sealed.set_lazy_data_property_with_data(
        tc,
        name(tc, "l"),
        lazy_marker_getter,
        data_object(tc, 5).into(),
        v8::PropertyAttribute::NONE,
        v8::SideEffectType::HasSideEffect,
        v8::SideEffectType::HasSideEffect,
    );
    let lazy_caught = tc.has_caught();

    vec![pass(
        "object-callback-retention/lazy_throw_empty_failure",
        Json::obj(vec![
            ("throw_install", throw_install.map_or(Json::Null, Json::b)),
            ("first_present", Json::b(first.0)),
            ("first_caught", Json::b(first.1)),
            ("first_exception", Json::s(&first.2)),
            ("second_present", Json::b(second.0)),
            ("second_caught", Json::b(second.1)),
            ("second_exception", Json::s(&second.2)),
            ("hits_after_first", Json::i(i64::from(hits_after_first))),
            ("hits_after_second", Json::i(i64::from(hits_after_second))),
            (
                "hits_after_replace_attempt",
                Json::i(i64::from(hits_after_replace)),
            ),
            ("throw_hits", Json::i(i64::from(LAZY_THROW_HITS.get()))),
            (
                "replace_after_throw",
                replace_after_throw.0.map_or(Json::Null, Json::b),
            ),
            ("replace_caught", Json::b(replace_after_throw.1)),
            ("replace_exception", Json::s(&replace_after_throw.2)),
            ("read_after_replace_present", Json::b(read_after_replace.0)),
            ("read_after_replace_caught", Json::b(read_after_replace.1)),
            (
                "read_after_replace_exception",
                Json::s(&read_after_replace.2),
            ),
            ("empty_install", empty_install.map_or(Json::Null, Json::b)),
            ("empty_first_undefined", Json::b(empty_first.is_undefined())),
            (
                "empty_second_undefined",
                Json::b(empty_second.is_undefined()),
            ),
            ("empty_hits", Json::i(i64::from(LAZY_EMPTY_HITS.get()))),
            (
                "accessor_nonextensible",
                accessor_failure.map_or(Json::Null, Json::b),
            ),
            ("accessor_nonextensible_caught", Json::b(accessor_caught)),
            (
                "lazy_nonextensible",
                lazy_failure.map_or(Json::Null, Json::b),
            ),
            ("lazy_nonextensible_caught", Json::b(lazy_caught)),
        ]),
    )]
}

fn return_ten(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    rv.set_int32(10);
}

fn return_forty_two(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    rv.set_int32(42);
}

fn call_integer(scope: &v8::PinScope<'_, '_>, function: v8::Local<v8::Function>) -> i64 {
    function
        .call(scope, v8::undefined(scope).into(), &[])
        .and_then(|value| value.integer_value(scope))
        .unwrap_or(-1)
}

fn template_set_with_attr() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);

    let nested_function_template = v8::FunctionTemplate::new(scope, return_forty_two);
    let nested_object_template = v8::ObjectTemplate::new(scope);
    nested_object_template.set_with_attr(
        name(scope, "nestedValue"),
        v8::Integer::new(scope, 81).into(),
        v8::PropertyAttribute::NONE,
    );
    let object_template = v8::ObjectTemplate::new(scope);
    object_template.set_with_attr(
        name(scope, "primitive"),
        v8::String::new(scope, "template-value").unwrap().into(),
        all_attributes(),
    );
    object_template.set_with_attr(
        name(scope, "nestedFunction"),
        nested_function_template.into(),
        v8::PropertyAttribute::DONT_ENUM,
    );
    object_template.set_with_attr(
        name(scope, "nestedObject"),
        nested_object_template.into(),
        v8::PropertyAttribute::NONE,
    );

    let root_function_template = v8::FunctionTemplate::new(scope, return_ten);
    root_function_template.set_with_attr(
        name(scope, "primitive"),
        v8::Integer::new(scope, 9).into(),
        v8::PropertyAttribute::DONT_ENUM,
    );
    root_function_template.set_with_attr(
        name(scope, "nestedFunction"),
        nested_function_template.into(),
        all_attributes(),
    );

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let first = object_template.new_instance(scope).unwrap();
    let second = object_template.new_instance(scope).unwrap();
    let first_primitive = first.get(scope, name(scope, "primitive").into()).unwrap();
    let second_primitive = second.get(scope, name(scope, "primitive").into()).unwrap();
    let first_nested_function = first
        .get(scope, name(scope, "nestedFunction").into())
        .unwrap()
        .try_cast::<v8::Function>()
        .unwrap();
    let second_nested_function = second
        .get(scope, name(scope, "nestedFunction").into())
        .unwrap()
        .try_cast::<v8::Function>()
        .unwrap();
    let first_nested_object = first
        .get(scope, name(scope, "nestedObject").into())
        .unwrap()
        .try_cast::<v8::Object>()
        .unwrap();
    let second_nested_object = second
        .get(scope, name(scope, "nestedObject").into())
        .unwrap()
        .try_cast::<v8::Object>()
        .unwrap();
    let primitive_attributes = first
        .get_property_attributes(scope, name(scope, "primitive").into())
        .unwrap();
    let nested_function_attributes = first
        .get_property_attributes(scope, name(scope, "nestedFunction").into())
        .unwrap();
    let first_write = first.set(
        scope,
        name(scope, "primitive").into(),
        v8::String::new(scope, "changed").unwrap().into(),
    );
    let first_primitive_after_write = first
        .get(scope, name(scope, "primitive").into())
        .unwrap()
        .to_rust_string_lossy(scope);

    let root_function = root_function_template.get_function(scope).unwrap();
    let root_function_again = root_function_template.get_function(scope).unwrap();
    let root_primitive = object_integer(scope, root_function.into(), "primitive");
    let root_nested_function = root_function
        .get(scope, name(scope, "nestedFunction").into())
        .unwrap()
        .try_cast::<v8::Function>()
        .unwrap();
    let root_primitive_attributes = root_function
        .get_property_attributes(scope, name(scope, "primitive").into())
        .unwrap();
    let root_nested_attributes = root_function
        .get_property_attributes(scope, name(scope, "nestedFunction").into())
        .unwrap();

    vec![pass(
        "object-callback-retention/template_set_with_attr",
        Json::obj(vec![
            (
                "object_primitive",
                Json::s(&first_primitive.to_rust_string_lossy(scope)),
            ),
            (
                "primitive_shared_value",
                Json::b(first_primitive.strict_equals(second_primitive)),
            ),
            (
                "primitive_attributes",
                property_attributes_json(primitive_attributes),
            ),
            (
                "nested_function_attributes",
                property_attributes_json(nested_function_attributes),
            ),
            (
                "nested_function_call",
                Json::i(call_integer(scope, first_nested_function)),
            ),
            (
                "nested_function_shared_across_instances",
                Json::b(first_nested_function.strict_equals(second_nested_function.into())),
            ),
            (
                "nested_object_value",
                Json::i(object_integer(scope, first_nested_object, "nestedValue")),
            ),
            (
                "nested_object_distinct_across_instances",
                Json::b(!first_nested_object.strict_equals(second_nested_object.into())),
            ),
            ("read_only_write", first_write.map_or(Json::Null, Json::b)),
            (
                "first_primitive_after_write",
                Json::s(&first_primitive_after_write),
            ),
            (
                "second_primitive_after_first_write",
                Json::s(&second_primitive.to_rust_string_lossy(scope)),
            ),
            ("function_call", Json::i(call_integer(scope, root_function))),
            (
                "function_same_context_identity",
                Json::b(root_function.strict_equals(root_function_again.into())),
            ),
            ("function_primitive", Json::i(root_primitive)),
            (
                "function_primitive_attributes",
                property_attributes_json(root_primitive_attributes),
            ),
            (
                "function_nested_call",
                Json::i(call_integer(scope, root_nested_function)),
            ),
            (
                "function_nested_attributes",
                property_attributes_json(root_nested_attributes),
            ),
        ]),
    )]
}

fn panic_accessor_getter(
    _scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<v8::Value>,
) {
    eprintln!("marker:accessor-getter-entered");
    panic!("accessor-getter-panic");
}

fn panic_accessor_setter(
    _scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<v8::Name>,
    _value: v8::Local<v8::Value>,
    _args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<()>,
) {
    eprintln!("marker:accessor-setter-entered");
    panic!("accessor-setter-panic");
}

fn panic_lazy_getter(
    _scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<v8::Value>,
) {
    eprintln!("marker:lazy-getter-entered");
    panic!("lazy-getter-panic");
}

fn negative_probe(mode: &str) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = v8::Object::new(scope);
    eprintln!("marker:before-{mode}");
    match mode {
        "accessor-getter-panic" => {
            object.set_accessor(scope, name(scope, "p"), panic_accessor_getter);
            object.get(scope, name(scope, "p").into());
        }
        "accessor-setter-panic" => {
            object.set_accessor_with_setter(
                scope,
                name(scope, "p"),
                marker_getter,
                panic_accessor_setter,
            );
            object.set(
                scope,
                name(scope, "p").into(),
                v8::Integer::new(scope, 1).into(),
            );
        }
        "lazy-getter-panic" => {
            object.set_lazy_data_property(scope, name(scope, "p"), panic_lazy_getter);
            object.get(scope, name(scope, "p").into());
        }
        "lazy-invalid-setter-side-effect" => {
            object.set_lazy_data_property_with_data(
                scope,
                name(scope, "p"),
                lazy_marker_getter,
                data_object(scope, 1).into(),
                v8::PropertyAttribute::NONE,
                v8::SideEffectType::HasSideEffect,
                v8::SideEffectType::HasNoSideEffect,
            );
        }
        "template-object-value" => {
            let template = v8::ObjectTemplate::new(scope);
            template.set_with_attr(
                name(scope, "invalid"),
                object.into(),
                v8::PropertyAttribute::NONE,
            );
        }
        "template-function-value" => {
            let template = v8::ObjectTemplate::new(scope);
            let function = v8::Function::new(scope, return_ten).unwrap();
            template.set_with_attr(
                name(scope, "invalid"),
                function.into(),
                v8::PropertyAttribute::NONE,
            );
        }
        "template-context-data" => {
            let template = v8::ObjectTemplate::new(scope);
            template.set_with_attr(
                name(scope, "internal"),
                context.into(),
                v8::PropertyAttribute::NONE,
            );
            let instance = template.new_instance(scope).unwrap();
            let value = instance.get(scope, name(scope, "internal").into()).unwrap();
            println!(
                "survived instance=true is_context={} is_value={}",
                value.is_context(),
                value.is_value()
            );
        }
        "template-primitive-array-data" => {
            let template = v8::ObjectTemplate::new(scope);
            let array = v8::PrimitiveArray::new(scope, 1);
            array.set(scope, 0, v8::Integer::new(scope, 7).into());
            template.set_with_attr(
                name(scope, "internal"),
                array.into(),
                v8::PropertyAttribute::NONE,
            );
            let instance = template.new_instance(scope).unwrap();
            let value = instance.get(scope, name(scope, "internal").into()).unwrap();
            println!(
                "survived instance=true is_fixed_array={} is_value={}",
                value.is_fixed_array(),
                value.is_value()
            );
        }
        _ => panic!("unknown negative probe {mode}"),
    }
    eprintln!("marker:after-{mode}");
}

fn run() {
    oracle::ensure_v8();
    let checks = [
        accessor_configuration_callbacks,
        accessor_replacement_and_read_only,
        lazy_data_retention_and_attributes,
        lazy_side_effect_matrix,
        lazy_throw_empty_and_install_failure,
        template_set_with_attr,
    ];
    let outcomes: Vec<_> = checks.into_iter().flat_map(|check| check()).collect();
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(outcomes.len(), outcomes.len(), 0));
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    match args.as_slice() {
        [_] => run(),
        [_, flag, mode] if flag == "--negative" => negative_probe(mode),
        _ => panic!("usage: conformance-object-callback-retention [--negative MODE]"),
    }
}
