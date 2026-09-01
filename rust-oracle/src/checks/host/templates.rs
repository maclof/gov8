//! ObjectTemplate / FunctionTemplate construction checks.
//!
//! Characterized contract (the Go port must reproduce):
//! - `FunctionTemplate::get_function` returns the same function object for
//!   repeated calls within one context and a distinct function per context.
//! - `set_class_name` names the constructor function.
//! - Instances created from the instance template get the prototype
//!   template object as their `[[Prototype]]`, which is also the
//!   `C.prototype` of the context's constructor function.
//! - `set_internal_field_count` on the instance template is observable on
//!   every constructed instance (both `new C(..)` from script and
//!   `Function::new_instance` from the host), and a construct-call callback
//!   can seed internal fields on `args.this()` before V8 returns it.
//! - A construct call ignores non-object return values; the receiver is the
//!   freshly created instance and `new.target` is the constructor function.
//!   A plain call gets the global proxy as receiver (API functions are
//!   sloppy-mode) and `new.target === undefined`.
//! - `ObjectTemplate::new_instance` produces independent objects that share
//!   the template's initial properties; `new_from_template` gives instances
//!   whose prototype is the function template's prototype object.

use super::callbacks::{cb_constant_five, cb_construct_seeds_instance};
use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};

pub(crate) fn function_template_construction() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let template = v8::FunctionTemplate::new(scope, cb_constant_five);
    template.set_class_name(v8::String::new(scope, "Gov8Base").unwrap());

    let f1 = template.get_function(scope).unwrap();
    let f2 = template.get_function(scope).unwrap();
    let same_in_context = f1.strict_equals(f2.into());
    let name = harness::value_text(scope, f1.get_name(scope).into());
    let is_function = f1.is_function();
    // The function created from the template is callable; the callback's
    // typed int32 return value surfaces as a JS number.
    let called = f1
        .call(scope, v8::undefined(scope).into(), &[])
        .map(|v| harness::value_text(scope, v))
        .unwrap_or_default();

    // A second context in the same isolate instantiates its own function.
    let f_ctx2 = {
        let ctx2 = v8::Context::new(scope, Default::default());
        let scope2 = &mut v8::ContextScope::new(scope, ctx2);
        let f = template.get_function(scope2).unwrap();
        v8::Global::new(scope2, f)
    };
    let distinct_across_contexts = {
        v8::scope!(let inner, scope);
        let f2c = v8::Local::new(inner, &f_ctx2);
        !f2c.strict_equals(f1.into())
    };

    let actual = Json::obj(vec![
        ("same_in_context", Json::b(same_in_context)),
        ("name", Json::s(&name)),
        ("is_function", Json::b(is_function)),
        ("call_result", Json::s(&called)),
        (
            "distinct_across_contexts",
            Json::b(distinct_across_contexts),
        ),
    ]);
    let expected = Json::obj(vec![
        ("same_in_context", Json::b(true)),
        ("name", Json::s("Gov8Base")),
        ("is_function", Json::b(true)),
        ("call_result", Json::s("5")),
        ("distinct_across_contexts", Json::b(true)),
    ]);
    vec![expect_eq(
        "template/function_template_construction",
        expected,
        actual,
    )]
}

pub(crate) fn instance_prototype_and_constructor() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let template = v8::FunctionTemplate::new(scope, cb_construct_seeds_instance);
    template.set_class_name(v8::String::new(scope, "Gov8Thing").unwrap());
    let instance_template = template.instance_template(scope);
    instance_template.set_internal_field_count(2);
    let template_field_count = instance_template.internal_field_count();

    let prototype_template = template.prototype_template(scope);
    prototype_template.set(
        v8::String::new(scope, "protoMark").unwrap().into(),
        v8::String::new(scope, "on-proto").unwrap().into(),
    );

    let f = template.get_function(scope).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "Gov8Thing").unwrap().into(),
            f.into(),
        )
        .unwrap();

    let proto_check = harness::eval_text(
        scope,
        concat!(
            "const t = new Gov8Thing(5); ",
            "[t instanceof Gov8Thing, t.protoMark, ",
            "Object.getPrototypeOf(t) === Gov8Thing.prototype].join('|')"
        ),
    )
    .unwrap_or_default();

    // The constructed instance is reachable from the host and carries the
    // configured internal field count plus the value seeded by the callback.
    let seeded_field = harness::eval(scope, "t")
        .and_then(|v| v8::Local::<v8::Object>::try_from(v).ok())
        .map(|obj| {
            let count = obj.internal_field_count();
            let seeded = obj
                .get_internal_field(scope, 0)
                .and_then(|d| v8::Local::<v8::Number>::try_from(d).ok())
                .map(|n| Json::f(n.value()))
                .unwrap_or(Json::Null);
            Json::obj(vec![
                ("field_count", Json::i(count as i64)),
                ("seeded_value", seeded),
            ])
        })
        .unwrap_or(Json::Null);

    // A plain call is not a construct call: new.target is undefined and the
    // callback's non-object return value is delivered as-is.
    let plain_call = harness::eval_text(scope, "Gov8Thing(3)").unwrap_or_default();
    // The construct-call shape recorded by the callback on the instance.
    let call_shape = harness::eval_text(scope, "t.call_shape").unwrap_or_default();

    // Host-side construction follows the same path: instance template fields
    // and prototype chain are present.
    let host_instance = f
        .new_instance(scope, &[v8::Integer::new(scope, 9).into()])
        .map(|inst| {
            Json::obj(vec![
                ("field_count", Json::i(inst.internal_field_count() as i64)),
                (
                    "proto_mark",
                    Json::s(
                        &inst
                            .get(scope, v8::String::new(scope, "protoMark").unwrap().into())
                            .map(|v| harness::value_text(scope, v))
                            .unwrap_or_default(),
                    ),
                ),
            ])
        })
        .unwrap_or(Json::Null);

    let actual = Json::obj(vec![
        ("template_field_count", Json::i(template_field_count as i64)),
        ("proto_check", Json::s(&proto_check)),
        ("constructed", seeded_field),
        ("plain_call", Json::s(&plain_call)),
        ("call_shape", Json::s(&call_shape)),
        ("host_instance", host_instance),
    ]);
    let expected = Json::obj(vec![
        ("template_field_count", Json::i(2)),
        ("proto_check", Json::s("true|on-proto|true")),
        (
            "constructed",
            Json::obj(vec![
                ("field_count", Json::i(2)),
                ("seeded_value", Json::f(5.0)),
            ]),
        ),
        (
            "plain_call",
            Json::s("construct=false;new_target_function=false;new_target_undefined=true"),
        ),
        (
            "call_shape",
            Json::s("construct=true;new_target_function=true;new_target_undefined=false"),
        ),
        (
            "host_instance",
            Json::obj(vec![
                ("field_count", Json::i(2)),
                ("proto_mark", Json::s("on-proto")),
            ]),
        ),
    ]);
    vec![expect_eq(
        "template/instance_prototype_and_constructor",
        expected,
        actual,
    )]
}

pub(crate) fn object_template_instances() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    ot.set(
        v8::String::new(scope, "a").unwrap().into(),
        v8::Integer::new(scope, 1).into(),
    );
    let i1 = ot.new_instance(scope).unwrap();
    let i2 = ot.new_instance(scope).unwrap();
    let distinct = !i1.strict_equals(i2.into());
    let a1 = i1
        .get(scope, v8::String::new(scope, "a").unwrap().into())
        .map(|v| harness::value_text(scope, v))
        .unwrap_or_default();
    let a2 = i2
        .get(scope, v8::String::new(scope, "a").unwrap().into())
        .map(|v| harness::value_text(scope, v))
        .unwrap_or_default();
    // Instances are independent: a host-side write to i1 is invisible in i2.
    i1.set(
        scope,
        v8::String::new(scope, "b").unwrap().into(),
        v8::Integer::new(scope, 2).into(),
    )
    .unwrap();
    let b_on_i2_is_undefined = i2
        .get(scope, v8::String::new(scope, "b").unwrap().into())
        .map(|v| v.is_undefined())
        .unwrap_or(false);

    // An object template derived from a function template produces instances
    // whose prototype is that function template's prototype object.
    let ft = v8::FunctionTemplate::new(scope, cb_constant_five);
    ft.set_class_name(v8::String::new(scope, "Gov8Base").unwrap());
    ft.prototype_template(scope).set(
        v8::String::new(scope, "protoMark").unwrap().into(),
        v8::String::new(scope, "on-proto").unwrap().into(),
    );
    let ctor = ft.get_function(scope).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "Gov8Base").unwrap().into(),
            ctor.into(),
        )
        .unwrap();
    let ot2 = v8::ObjectTemplate::new_from_template(scope, ft);
    let o2 = ot2.new_instance(scope).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "o2").unwrap().into(),
            o2.into(),
        )
        .unwrap();
    let proto_is_ft_prototype =
        harness::eval_text(scope, "Object.getPrototypeOf(o2) === Gov8Base.prototype")
            .unwrap_or_default();
    let inherited_mark = harness::eval_text(scope, "o2.protoMark").unwrap_or_default();

    let actual = Json::obj(vec![
        ("instances_distinct", Json::b(distinct)),
        ("i1_a", Json::s(&a1)),
        ("i2_a", Json::s(&a2)),
        ("i2_b_is_undefined", Json::b(b_on_i2_is_undefined)),
        ("proto_is_ft_prototype", Json::s(&proto_is_ft_prototype)),
        ("inherited_mark", Json::s(&inherited_mark)),
    ]);
    let expected = Json::obj(vec![
        ("instances_distinct", Json::b(true)),
        ("i1_a", Json::s("1")),
        ("i2_a", Json::s("1")),
        ("i2_b_is_undefined", Json::b(true)),
        ("proto_is_ft_prototype", Json::s("true")),
        ("inherited_mark", Json::s("on-proto")),
    ]);
    vec![expect_eq(
        "template/object_template_instances",
        expected,
        actual,
    )]
}
