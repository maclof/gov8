//! Native function callback checks: arguments, receiver, callback data,
//! construct-call semantics, host-to-JS reentrancy, and JS exceptions
//! thrown from native code.
//!
//! Characterized contract (the Go port must reproduce):
//! - A native callback receives `FunctionCallbackArguments`: `length()` is
//!   the number of actually passed arguments, `get(i)` returns `undefined`
//!   for out-of-bounds indices, `this()` is the receiver.
//! - API functions are sloppy-mode: a plain call `f()` receives the global
//!   proxy as receiver; a method call receives the object; a host-driven
//!   `Function::call` receives whatever receiver was passed.
//! - The builder's `.data(..)` value is observable verbatim via
//!   `args.data()`; the builder's `.length(n)` becomes `fn.length`.
//! - Typed return-value setters map to JS values: `set_int32(n)` yields a
//!   JS number.
//! - Construct calls: `is_construct_call()` is true, `new.target` is the
//!   constructor function, `args.this()` is the created instance (which the
//!   callback can seed); non-object return values are ignored for `new`.
//! - A callback may re-enter JavaScript with `Function::call` while V8 is
//!   inside its own invocation (nesting works).
//! - A callback that schedules an exception with `scope.throw_exception`
//!   propagates it to the JS caller exactly like a JS `throw`: TryCatch
//!   catches it with the usual `Uncaught ` message prefix, and a JS
//!   `try/catch` observes the same exception object.
//!
//! Rust panics inside callbacks are NOT characterized here (they abort the
//! process at the `extern "C"` boundary); see `bin/panic-boundary.rs`.

use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};

/// Callback that always returns the int32 constant 5.
pub(crate) fn cb_constant_five(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    rv.set_int32(5);
}

/// Constructor-style callback that seeds internal field 0 of the created
/// instance with its first argument and records the call shape.
pub(crate) fn cb_construct_seeds_instance(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let encoding = call_shape_encoding(&args);
    let encoded = v8::String::new(scope, &encoding).unwrap();
    if args.is_construct_call() {
        // Seeding an internal field on the receiver before V8 returns it.
        args.this().set_internal_field(0, args.get(0).into());
        let key = v8::String::new(scope, "call_shape").unwrap();
        args.this().set(scope, key.into(), encoded.into());
    }
    rv.set(encoded.into());
}

fn call_shape_encoding(args: &v8::FunctionCallbackArguments<'_>) -> String {
    format!(
        "construct={};new_target_function={};new_target_undefined={}",
        args.is_construct_call(),
        args.new_target().is_function(),
        args.new_target().is_undefined()
    )
}

fn cb_add(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let a = args.get(0).integer_value(scope).unwrap_or(0);
    let b = args.get(1).integer_value(scope).unwrap_or(0);
    rv.set_int32((a + b) as i32);
}

fn cb_arity(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    // Index 3 is out of bounds for both call shapes used by the check.
    let encoding = format!(
        "len={};oob3_undefined={}",
        args.length(),
        args.get(3).is_undefined()
    );
    rv.set(v8::String::new(scope, &encoding).unwrap().into());
}

fn cb_receiver_mark(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let key = v8::String::new(scope, "mark").unwrap();
    let mark = args.this().get(scope, key.into()).unwrap();
    rv.set(mark.to_string(scope).unwrap().into());
}

fn cb_echo_data(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    rv.set(args.data().to_string(scope).unwrap().into());
}

fn cb_construct_shape(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let encoding = call_shape_encoding(&args);
    let encoded = v8::String::new(scope, &encoding).unwrap();
    if args.is_construct_call() {
        let seeded = v8::String::new(scope, "seeded").unwrap();
        args.this().set(scope, seeded.into(), args.get(0));
        let key = v8::String::new(scope, "call_shape").unwrap();
        args.this().set(scope, key.into(), encoded.into());
    }
    rv.set(encoded.into());
}

fn cb_callit(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    // Re-enters JavaScript while V8 is inside this native invocation.
    if let Ok(f) = v8::Local::<v8::Function>::try_from(args.get(0)) {
        if let Some(result) = f.call(scope, v8::undefined(scope).into(), &[args.get(1)]) {
            rv.set(result);
        }
    }
}

fn cb_throw_error(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let msg = v8::String::new(scope, "native-boom").unwrap();
    scope.throw_exception(v8::Exception::error(scope, msg));
}

fn cb_throw_string(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let msg = v8::String::new(scope, "native-string-boom").unwrap();
    scope.throw_exception(msg.into());
}

/// Runs `source` inside a fresh TryCatch and produces the same normalized
/// exception observation shape as the base `exceptions` group.
fn observe_thrown(scope: &mut v8::PinScope<'_, '_>, source: &str) -> Json {
    let source_handle = v8::String::new(scope, source).unwrap();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let compiled = v8::Script::compile(tc, source_handle, None);
    let compile_ok = compiled.is_some();
    let run_ok = compiled.as_ref().and_then(|s| s.run(tc)).is_some();
    let has_caught = tc.has_caught();
    let can_continue = tc.can_continue();
    let message = tc
        .message()
        .map(|m| m.get(tc).to_rust_string_lossy(tc))
        .unwrap_or_default();
    let exception = tc.exception();
    let exception_text = exception
        .map(|e| harness::value_text(tc, e))
        .unwrap_or_default();
    let exception_is_string = exception.is_some_and(|e| e.is_string());

    Json::obj(vec![
        ("compile_ok", Json::b(compile_ok)),
        ("run_ok", Json::b(run_ok)),
        ("has_caught", Json::b(has_caught)),
        ("can_continue", Json::b(can_continue)),
        ("message", Json::s(&message)),
        ("exception_text", Json::s(&exception_text)),
        ("exception_is_string", Json::b(exception_is_string)),
    ])
}

pub(crate) fn arguments_and_return() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let f = v8::Function::builder(cb_add)
        .length(2)
        .build(scope)
        .unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "add").unwrap().into(),
            f.into(),
        )
        .unwrap();

    let js_two_args = harness::eval_text(scope, "add(20, 22)").unwrap_or_default();
    let js_one_arg = harness::eval_text(scope, "add(7)").unwrap_or_default();
    let fn_length = harness::eval_text(scope, "add.length").unwrap_or_default();
    // set_int32 produced a JS number, not something else.
    let result_is_number = harness::eval(scope, "add(1, 2)")
        .map(|v| v.is_number())
        .unwrap_or(false);
    let host_call = f
        .call(
            scope,
            v8::undefined(scope).into(),
            &[
                v8::Integer::new(scope, 20).into(),
                v8::Integer::new(scope, 22).into(),
            ],
        )
        .map(|v| harness::value_text(scope, v))
        .unwrap_or_default();

    let actual = Json::obj(vec![
        ("js_two_args", Json::s(&js_two_args)),
        ("js_one_arg", Json::s(&js_one_arg)),
        ("fn_length", Json::s(&fn_length)),
        ("result_is_number", Json::b(result_is_number)),
        ("host_call", Json::s(&host_call)),
    ]);
    let expected = Json::obj(vec![
        ("js_two_args", Json::s("42")),
        ("js_one_arg", Json::s("7")),
        ("fn_length", Json::s("2")),
        ("result_is_number", Json::b(true)),
        ("host_call", Json::s("42")),
    ]);
    vec![expect_eq("callback/arguments_and_return", expected, actual)]
}

pub(crate) fn arity_and_out_of_bounds_arguments() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let f = v8::Function::new(scope, cb_arity).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "__arity").unwrap().into(),
            f.into(),
        )
        .unwrap();

    let one_arg = harness::eval_text(scope, "__arity(1)").unwrap_or_default();
    let three_args = harness::eval_text(scope, "__arity(1, 2, 3)").unwrap_or_default();

    let actual = Json::obj(vec![
        ("one_arg", Json::s(&one_arg)),
        ("three_args", Json::s(&three_args)),
    ]);
    let expected = Json::obj(vec![
        ("one_arg", Json::s("len=1;oob3_undefined=true")),
        ("three_args", Json::s("len=3;oob3_undefined=true")),
    ]);
    vec![expect_eq(
        "callback/arity_and_out_of_bounds_arguments",
        expected,
        actual,
    )]
}

pub(crate) fn receiver_and_callback_data() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let recv = v8::Function::new(scope, cb_receiver_mark).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "__recv").unwrap().into(),
            recv.into(),
        )
        .unwrap();

    // Plain call: API functions are sloppy, so `this` is the global proxy
    // and its `mark` property is undefined.
    let plain = harness::eval_text(scope, "__recv()").unwrap_or_default();
    // Method call: the receiver is the object.
    let method = harness::eval_text(
        scope,
        concat!(
            "globalThis.obj = { mark: 'M1' }; ",
            "globalThis.obj.method = __recv; ",
            "globalThis.obj.method()"
        ),
    )
    .unwrap_or_default();
    // Host-driven call with an explicit receiver.
    let explicit_receiver = harness::eval(scope, "globalThis.obj")
        .and_then(|obj| recv.call(scope, obj, &[]))
        .map(|v| harness::value_text(scope, v))
        .unwrap_or_default();

    // Builder data reaches the callback verbatim.
    let payload = v8::String::new(scope, "payload-42").unwrap();
    let with_data = v8::Function::builder(cb_echo_data)
        .data(payload.into())
        .build(scope)
        .unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "__withdata").unwrap().into(),
            with_data.into(),
        )
        .unwrap();
    let data_echo = harness::eval_text(scope, "__withdata()").unwrap_or_default();

    let actual = Json::obj(vec![
        ("plain_call_receiver", Json::s(&plain)),
        ("method_call_receiver", Json::s(&method)),
        ("explicit_receiver", Json::s(&explicit_receiver)),
        ("callback_data", Json::s(&data_echo)),
    ]);
    let expected = Json::obj(vec![
        ("plain_call_receiver", Json::s("undefined")),
        ("method_call_receiver", Json::s("M1")),
        ("explicit_receiver", Json::s("M1")),
        ("callback_data", Json::s("payload-42")),
    ]);
    vec![expect_eq(
        "callback/receiver_and_callback_data",
        expected,
        actual,
    )]
}

pub(crate) fn construct_call_semantics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let f = v8::Function::new(scope, cb_construct_shape).unwrap();
    context
        .global(scope)
        .set(scope, v8::String::new(scope, "F").unwrap().into(), f.into())
        .unwrap();

    let plain = harness::eval_text(scope, "F(0)").unwrap_or_default();
    let constructed_seeded = harness::eval_text(scope, "new F(9).seeded").unwrap_or_default();
    let constructed_shape = harness::eval_text(scope, "new F(9).call_shape").unwrap_or_default();

    // Host-side construction via Function::new_instance.
    let host_constructed = f
        .new_instance(scope, &[v8::Integer::new(scope, 9).into()])
        .and_then(|inst| {
            inst.get(scope, v8::String::new(scope, "seeded").unwrap().into())
                .map(|v| harness::value_text(scope, v))
        })
        .unwrap_or_default();

    let actual = Json::obj(vec![
        ("plain", Json::s(&plain)),
        ("constructed_seeded", Json::s(&constructed_seeded)),
        ("constructed_shape", Json::s(&constructed_shape)),
        ("host_constructed_seeded", Json::s(&host_constructed)),
    ]);
    let expected = Json::obj(vec![
        (
            "plain",
            Json::s("construct=false;new_target_function=false;new_target_undefined=true"),
        ),
        ("constructed_seeded", Json::s("9")),
        (
            "constructed_shape",
            Json::s("construct=true;new_target_function=true;new_target_undefined=false"),
        ),
        ("host_constructed_seeded", Json::s("9")),
    ]);
    vec![expect_eq(
        "callback/construct_call_semantics",
        expected,
        actual,
    )]
}

pub(crate) fn native_reenters_javascript() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let f = v8::Function::new(scope, cb_callit).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "__callit").unwrap().into(),
            f.into(),
        )
        .unwrap();

    let one_level = harness::eval_text(scope, "__callit((x) => x * 6, 7)").unwrap_or_default();
    // The re-entered JS calls back into the native function again.
    let nested = harness::eval_text(scope, "__callit((x) => __callit((y) => y + 1, x) * 2, 10)")
        .unwrap_or_default();

    let actual = Json::obj(vec![
        ("one_level", Json::s(&one_level)),
        ("nested", Json::s(&nested)),
    ]);
    let expected = Json::obj(vec![
        ("one_level", Json::s("42")),
        ("nested", Json::s("22")),
    ]);
    vec![expect_eq(
        "callback/native_reenters_javascript",
        expected,
        actual,
    )]
}

pub(crate) fn js_exception_from_native() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let throw_err = v8::Function::new(scope, cb_throw_error).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "__throwError").unwrap().into(),
            throw_err.into(),
        )
        .unwrap();
    let throw_str = v8::Function::new(scope, cb_throw_string).unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "__throwString").unwrap().into(),
            throw_str.into(),
        )
        .unwrap();

    let error_object = observe_thrown(scope, "__throwError();");
    let string_throw = observe_thrown(scope, "__throwString();");

    // A JS try/catch observes exactly the scheduled exception.
    let js_catch = harness::eval_text(
        scope,
        "try { __throwError(); } catch (e) { 'caught:' + e.message; }",
    )
    .unwrap_or_default();
    // The isolate is fully usable after a caught native-thrown exception.
    let usable_after = harness::eval_text(scope, "40 + 2").unwrap_or_default();

    let actual = Json::obj(vec![
        ("error_object", error_object),
        ("string_throw", string_throw),
        ("js_catch", Json::s(&js_catch)),
        ("usable_after", Json::s(&usable_after)),
    ]);
    let expected = Json::obj(vec![
        (
            "error_object",
            Json::obj(vec![
                ("compile_ok", Json::b(true)),
                ("run_ok", Json::b(false)),
                ("has_caught", Json::b(true)),
                ("can_continue", Json::b(true)),
                ("message", Json::s("Uncaught Error: native-boom")),
                ("exception_text", Json::s("Error: native-boom")),
                ("exception_is_string", Json::b(false)),
            ]),
        ),
        (
            "string_throw",
            Json::obj(vec![
                ("compile_ok", Json::b(true)),
                ("run_ok", Json::b(false)),
                ("has_caught", Json::b(true)),
                ("can_continue", Json::b(true)),
                ("message", Json::s("Uncaught native-string-boom")),
                ("exception_text", Json::s("native-string-boom")),
                ("exception_is_string", Json::b(true)),
            ]),
        ),
        ("js_catch", Json::s("caught:native-boom")),
        ("usable_after", Json::s("42")),
    ]);
    vec![expect_eq(
        "callback/js_exception_from_native",
        expected,
        actual,
    )]
}
