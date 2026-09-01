//! Native callback benchmarks.
//! See benches/common/mod.rs for the methodology.
//!
//! Comparable workloads for the Go port (shapes mirror the `host/callbacks`
//! conformance checks in `src/checks/host/callbacks.rs`):
//! - `callback/native_call_from_js`: one precompiled script per iteration
//!   calls a native `add(a, b)` function twice (`add(20, 22) + add(100, 200)`);
//!   the callback returns via `ReturnValue::set_int32`. Extends the JS-driven
//!   half of `arguments_and_return` (`add(20, 22)` -> "42") with a second
//!   call; the asserted total is 342.
//! - `callback/native_call_from_rust`: the host calls the same-shaped native
//!   function once per iteration via `Function::call` with an undefined
//!   receiver and two integer arguments (the host-driven half of
//!   `arguments_and_return`); the asserted result is 42.
//! - `callback/function_new_call`: measures native-callback function
//!   creation (`Function::builder(..).length(2)`, the conformance build
//!   shape) plus a single asserted host call per iteration.
//!
//! Failure policy: the workload is validated untimed before measurement and
//! every timed iteration re-asserts that execution succeeded and produced
//! the documented result. A failed assert panics and aborts the benchmark
//! binary before any baseline is saved, so a silently failing path (script
//! exception, callback never invoked, wrong settlement) can never be timed.
//! The assertions are part of the measured workload: the Go-side harness
//! must mirror them one-for-one.
//!
//! The isolate and context are created once per benchmark; each iteration
//! opens a fresh nested `HandleScope`.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};

/// `add(20, 22) + add(100, 200)` with the `set_int32` callback.
const EXPECTED_JS_RESULT: f64 = 342.0;
/// `add(20, 22)` for host-driven `Function::call`.
const EXPECTED_HOST_RESULT: f64 = 42.0;

fn add_cb(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let a = args.get(0).integer_value(scope).unwrap_or(0);
    let b = args.get(1).integer_value(scope).unwrap_or(0);
    rv.set_int32((a + b) as i32);
}

/// Asserts the workload result is the expected number. Shared by the
/// untimed validation pass and every timed iteration so a broken or
/// silently failing workload aborts the run instead of being timed.
fn assert_result_is(
    scope: &v8::PinScope<'_, '_>,
    value: v8::Local<v8::Value>,
    expected: f64,
    bench: &str,
) {
    let n = value.number_value(scope).unwrap_or_else(|| {
        panic!("{bench}: workload result is not a number (native callback did not run)")
    });
    assert_eq!(
        n, expected,
        "{bench}: unexpected workload result (native callback misbehaved)"
    );
}

/// The exact per-iteration timed workload of `native_call_from_js`,
/// asserted. Used both untimed (validation) and inside the timed loop.
fn run_asserted_js_roundtrip(scope: &mut v8::PinScope<'_, '_>, script: &v8::Global<v8::Script>) {
    let script = v8::Local::new(scope, script);
    let value = script.run(scope).unwrap_or_else(|| {
        panic!("callback/native_call_from_js: script execution failed (uncaught exception)")
    });
    assert_result_is(
        scope,
        value,
        EXPECTED_JS_RESULT,
        "callback/native_call_from_js",
    );
}

/// The exact per-iteration timed workload of `native_call_from_rust`,
/// asserted.
fn run_asserted_host_call(scope: &mut v8::PinScope<'_, '_>, f: v8::Local<v8::Function>) {
    let a = v8::Integer::new(scope, 20);
    let b = v8::Integer::new(scope, 22);
    let value = f
        .call(scope, v8::undefined(scope).into(), &[a.into(), b.into()])
        .unwrap_or_else(|| panic!("callback/native_call_from_rust: Function::call failed"));
    assert_result_is(
        scope,
        value,
        EXPECTED_HOST_RESULT,
        "callback/native_call_from_rust",
    );
}

fn native_call_from_js(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Built with `.length(2)` like the conformance `arguments_and_return`
    // check (the observable `fn.length` is 2 there).
    let f = v8::Function::builder(add_cb)
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
    let script = {
        let source = v8::String::new(scope, "add(20, 22) + add(100, 200)").unwrap();
        v8::Global::new(scope, v8::Script::compile(scope, source, None).unwrap())
    };

    // Untimed validation: the workload must succeed and produce the
    // documented result before any measurement starts.
    run_asserted_js_roundtrip(scope, &script);

    c.bench_function("callback/native_call_from_js", |b| {
        b.iter(|| {
            v8::scope!(let inner, scope);
            run_asserted_js_roundtrip(inner, &script);
        })
    });
}

fn native_call_from_rust(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let f_global = {
        let f = v8::Function::builder(add_cb)
            .length(2)
            .build(scope)
            .unwrap();
        v8::Global::new(scope, f)
    };

    // Untimed validation before measurement.
    {
        let f = v8::Local::new(scope, &f_global);
        run_asserted_host_call(scope, f);
    }

    c.bench_function("callback/native_call_from_rust", |b| {
        b.iter(|| {
            v8::scope!(let inner, scope);
            let f = v8::Local::new(inner, &f_global);
            run_asserted_host_call(inner, f);
        })
    });
}

fn function_new_call(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Untimed validation before measurement: create once and call, with the
    // same assertions the timed iterations make.
    {
        let f = v8::Function::builder(add_cb)
            .length(2)
            .build(scope)
            .unwrap();
        run_asserted_host_call(scope, f);
    }

    c.bench_function("callback/function_new_call", |b| {
        b.iter(|| {
            v8::scope!(let inner, scope);
            let f = v8::Function::builder(add_cb)
                .length(2)
                .build(inner)
                .unwrap();
            run_asserted_host_call(inner, f);
        })
    });
}

criterion_group! {
    name = callback_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = native_call_from_js, native_call_from_rust, function_new_call
}

criterion_main!(callback_benches);
