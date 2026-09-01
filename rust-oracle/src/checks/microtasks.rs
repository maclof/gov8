//! Microtask scheduling and ordering checks.
//!
//! Uses the same script for every policy so the ordering differences between
//! explicit checkpoints and automatic flushing are directly observable.

use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};

// Note: V8's bare default context has no `queueMicrotask` (that is an
// HTML/embedder API). Promise reaction jobs are the only ES-native microtask
// source, so all ordering here is expressed with promises.
const MICROTASK_SCRIPT: &str = concat!(
    "Promise.resolve().then(() => __order.push('p1'));",
    "Promise.resolve().then(() => __order.push('p2')).then(() => __order.push('p2b'));",
    "new Promise(function (resolve) { resolve(); }).then(() => __order.push('p3'));",
    "Promise.resolve().then(() => { __order.push('p4'); ",
    "Promise.resolve().then(() => __order.push('p4b')); });",
);

fn seed(scope: &v8::PinScope<'_, '_>) {
    harness::eval(scope, "globalThis.__order = [];").unwrap();
}

fn order(scope: &v8::PinScope<'_, '_>) -> String {
    harness::eval_text(scope, "__order.join(',')").unwrap_or_default()
}

pub(crate) fn explicit_policy_ordering() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    seed(scope);
    harness::eval(scope, MICROTASK_SCRIPT).unwrap();
    let after_run = order(scope);

    scope.perform_microtask_checkpoint();
    let after_checkpoint = order(scope);

    scope.perform_microtask_checkpoint();
    let after_second_checkpoint = order(scope);

    let actual = Json::obj(vec![
        ("after_run", Json::s(&after_run)),
        ("after_checkpoint", Json::s(&after_checkpoint)),
        ("after_second_checkpoint", Json::s(&after_second_checkpoint)),
    ]);
    let expected = Json::obj(vec![
        ("after_run", Json::s("")),
        ("after_checkpoint", Json::s("p1,p2,p3,p4,p2b,p4b")),
        ("after_second_checkpoint", Json::s("p1,p2,p3,p4,p2b,p4b")),
    ]);
    vec![expect_eq(
        "microtasks/explicit_policy_ordering",
        expected,
        actual,
    )]
}

pub(crate) fn auto_policy_ordering() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    // The default policy is intentionally left untouched.
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let policy = match scope.get_microtasks_policy() {
        v8::MicrotasksPolicy::Explicit => "Explicit",
        v8::MicrotasksPolicy::Auto => "Auto",
    };

    seed(scope);
    harness::eval(scope, MICROTASK_SCRIPT).unwrap();
    let after_run = order(scope);

    scope.perform_microtask_checkpoint();
    let after_checkpoint = order(scope);

    let actual = Json::obj(vec![
        ("default_policy", Json::s(policy)),
        ("after_run", Json::s(&after_run)),
        ("after_checkpoint", Json::s(&after_checkpoint)),
    ]);
    let expected = Json::obj(vec![
        ("default_policy", Json::s("Auto")),
        ("after_run", Json::s("p1,p2,p3,p4,p2b,p4b")),
        ("after_checkpoint", Json::s("p1,p2,p3,p4,p2b,p4b")),
    ]);
    vec![expect_eq(
        "microtasks/auto_policy_ordering",
        expected,
        actual,
    )]
}

pub(crate) fn native_microtask_queue() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);

    let queue = v8::MicrotaskQueue::new(scope, v8::MicrotasksPolicy::Explicit);
    let context = v8::Context::new(scope, Default::default());
    context.set_microtask_queue(queue.as_ref());
    let attached = std::ptr::eq(
        context.get_microtask_queue().unwrap() as *const v8::MicrotaskQueue,
        queue.as_ref(),
    );

    let scope = &mut v8::ContextScope::new(scope, context);

    harness::eval(
        scope,
        concat!(
            "globalThis.__order = [];",
            "Promise.resolve().then(() => __order.push('n1'));",
            "Promise.resolve().then(() => __order.push('n2'));",
        ),
    )
    .unwrap();
    let after_run = order(scope);

    queue.perform_checkpoint(scope);
    let after_checkpoint = order(scope);

    // Enqueue a microtask through the native API and flush again.
    let fn_value = harness::eval(scope, "() => __order.push('native');").unwrap();
    let function = v8::Local::<v8::Function>::try_from(fn_value).unwrap();
    queue.enqueue_microtask(scope, function);
    queue.perform_checkpoint(scope);
    let after_native_enqueue = order(scope);

    let actual = Json::obj(vec![
        ("queue_attached", Json::b(attached)),
        ("after_run", Json::s(&after_run)),
        ("after_checkpoint", Json::s(&after_checkpoint)),
        ("after_native_enqueue", Json::s(&after_native_enqueue)),
    ]);
    let expected = Json::obj(vec![
        ("queue_attached", Json::b(true)),
        ("after_run", Json::s("")),
        ("after_checkpoint", Json::s("n1,n2")),
        ("after_native_enqueue", Json::s("n1,n2,native")),
    ]);
    vec![expect_eq(
        "microtasks/native_microtask_queue",
        expected,
        actual,
    )]
}
