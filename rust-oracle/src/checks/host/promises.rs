//! Promise / PromiseResolver native API checks and rejection notification
//! events.
//!
//! Characterized contract (the Go port must reproduce):
//! - `PromiseResolver::new` creates a pending promise; `state()` is
//!   `Pending` and `has_handler()` is false until a handler is attached.
//! - `resolve(value)`: returns `Some(true)`, the state becomes `Fulfilled`
//!   and `result()` is the resolved value. The returned bool reports the
//!   success of the *call*, not a settlement change: a second `resolve` or
//!   a `reject` after settlement is silently ignored and still returns
//!   `Some(true)`; state and result are unchanged.
//! - `reject(value)`: returns `Some(true)`, the state becomes `Rejected`
//!   and `result()` is the rejection value.
//! - `promise.then(native_handler)` attaches the handler synchronously
//!   (`has_handler()` is true immediately); with the Explicit microtasks
//!   policy the reaction job runs only at
//!   `perform_microtask_checkpoint`, the native handler receives the
//!   resolved value, and the derived promise settles to the handler's
//!   (implicit undefined) result. The derived promise is a distinct object.
//! - The promise-reject callback is invoked synchronously at reject time
//!   with `PromiseRejectWithNoHandler` when no handler exists, and with
//!   `PromiseHandlerAddedAfterReject` when a handler is later attached to a
//!   rejected promise. No event fires when a handler was attached before
//!   the reject. The AfterResolved events were removed from V8 and never
//!   fire in this build. Additionally, when a reaction registered with
//!   `then` only (no `on_rejected`) runs against a rejected promise, the
//!   *derived* promise is rejected with the same reason and reported as a
//!   second `WithNoHandler` when the reaction job executes.

use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};
use std::cell::RefCell;

fn state_name(state: v8::PromiseState) -> &'static str {
    match state {
        v8::PromiseState::Pending => "Pending",
        v8::PromiseState::Fulfilled => "Fulfilled",
        v8::PromiseState::Rejected => "Rejected",
    }
}

fn cb_noop(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
}

/// Native reaction handler: appends the received value's text to the
/// `__order` script-global array through the object API.
fn cb_push_order(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let global = scope.get_current_context().global(scope);
    let key = v8::String::new(scope, "__order").unwrap();
    let arr_val = global.get(scope, key.into()).unwrap();
    let arr = v8::Local::<v8::Array>::try_from(arr_val).unwrap();
    let idx = arr.length();
    arr.set(
        scope,
        v8::Integer::new(scope, idx as i32).into(),
        args.get(0),
    );
}

thread_local! {
    /// Event names observed by the promise-reject callback, in order.
    static REJECT_EVENTS: RefCell<Vec<&'static str>> = const { RefCell::new(Vec::new()) };
}

unsafe extern "C" fn reject_cb(message: v8::PromiseRejectMessage) {
    let name = match message.get_event() {
        v8::PromiseRejectEvent::PromiseRejectWithNoHandler => "WithNoHandler",
        v8::PromiseRejectEvent::PromiseHandlerAddedAfterReject => "HandlerAddedAfterReject",
        v8::PromiseRejectEvent::PromiseRejectAfterResolved => "RejectAfterResolved",
        v8::PromiseRejectEvent::PromiseResolveAfterResolved => "ResolveAfterResolved",
    };
    REJECT_EVENTS.with(|cell| cell.borrow_mut().push(name));
}

fn reject_events_snapshot() -> Vec<Json> {
    REJECT_EVENTS.with(|cell| cell.borrow().iter().map(|name| Json::s(name)).collect())
}

pub(crate) fn resolver_settlement_semantics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let resolver = v8::PromiseResolver::new(scope).unwrap();
    let promise = resolver.get_promise(scope);
    let initial_state = state_name(promise.state());
    let initial_has_handler = promise.has_handler();

    let resolve_ok = resolver.resolve(scope, v8::Number::new(scope, 42.0).into());
    let fulfilled_state = state_name(promise.state());
    let fulfilled_result = harness::value_text(scope, promise.result(scope));

    let resolve_again = resolver.resolve(scope, v8::Number::new(scope, 43.0).into());
    let reject_after = resolver.reject(scope, v8::String::new(scope, "late").unwrap().into());
    let still_fulfilled = state_name(promise.state());
    let result_still = harness::value_text(scope, promise.result(scope));

    let resolver2 = v8::PromiseResolver::new(scope).unwrap();
    let promise2 = resolver2.get_promise(scope);
    let reject_ok = resolver2.reject(scope, v8::String::new(scope, "boom").unwrap().into());
    let rejected_state = state_name(promise2.state());
    let rejected_result = harness::value_text(scope, promise2.result(scope));
    let rejected_has_handler = promise2.has_handler();
    promise2.mark_as_handled();

    let actual = Json::obj(vec![
        ("initial_state", Json::s(initial_state)),
        ("initial_has_handler", Json::b(initial_has_handler)),
        ("resolve_ok", resolve_ok.map(Json::b).unwrap_or(Json::Null)),
        ("fulfilled_state", Json::s(fulfilled_state)),
        ("fulfilled_result", Json::s(&fulfilled_result)),
        (
            "resolve_again",
            resolve_again.map(Json::b).unwrap_or(Json::Null),
        ),
        (
            "reject_after",
            reject_after.map(Json::b).unwrap_or(Json::Null),
        ),
        ("still_fulfilled", Json::s(still_fulfilled)),
        ("result_still", Json::s(&result_still)),
        ("reject_ok", reject_ok.map(Json::b).unwrap_or(Json::Null)),
        ("rejected_state", Json::s(rejected_state)),
        ("rejected_result", Json::s(&rejected_result)),
        ("rejected_has_handler", Json::b(rejected_has_handler)),
        ("mark_as_handled_ok", Json::b(true)),
    ]);
    let expected = Json::obj(vec![
        ("initial_state", Json::s("Pending")),
        ("initial_has_handler", Json::b(false)),
        ("resolve_ok", Json::b(true)),
        ("fulfilled_state", Json::s("Fulfilled")),
        ("fulfilled_result", Json::s("42")),
        // Both calls succeed (the settlement itself is silently ignored).
        ("resolve_again", Json::b(true)),
        ("reject_after", Json::b(true)),
        ("still_fulfilled", Json::s("Fulfilled")),
        ("result_still", Json::s("42")),
        ("reject_ok", Json::b(true)),
        ("rejected_state", Json::s("Rejected")),
        ("rejected_result", Json::s("boom")),
        ("rejected_has_handler", Json::b(false)),
        ("mark_as_handled_ok", Json::b(true)),
    ]);
    vec![expect_eq(
        "promise/resolver_settlement_semantics",
        expected,
        actual,
    )]
}

pub(crate) fn native_then_checkpoint() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    harness::eval(scope, "globalThis.__order = [];").unwrap();

    let resolver = v8::PromiseResolver::new(scope).unwrap();
    let promise = resolver.get_promise(scope);

    let handler = v8::Function::new(scope, cb_push_order).unwrap();
    let derived = promise.then(scope, handler).unwrap();

    let has_handler_before_resolve = promise.has_handler();
    let derived_initial_state = state_name(derived.state());
    let derived_is_distinct = !derived.strict_equals(promise.into());

    let resolve_ok = resolver.resolve(scope, v8::Integer::new(scope, 42).into());
    let order_before = harness::eval_text(scope, "__order.join(',')").unwrap_or_default();

    scope.perform_microtask_checkpoint();
    let order_after = harness::eval_text(scope, "__order.join(',')").unwrap_or_default();
    let derived_final_state = state_name(derived.state());
    let derived_result = harness::value_text(scope, derived.result(scope));

    let actual = Json::obj(vec![
        (
            "has_handler_before_resolve",
            Json::b(has_handler_before_resolve),
        ),
        ("derived_initial_state", Json::s(derived_initial_state)),
        ("derived_is_distinct", Json::b(derived_is_distinct)),
        ("resolve_ok", resolve_ok.map(Json::b).unwrap_or(Json::Null)),
        ("order_before_checkpoint", Json::s(&order_before)),
        ("order_after_checkpoint", Json::s(&order_after)),
        ("derived_final_state", Json::s(derived_final_state)),
        ("derived_result", Json::s(&derived_result)),
    ]);
    let expected = Json::obj(vec![
        ("has_handler_before_resolve", Json::b(true)),
        ("derived_initial_state", Json::s("Pending")),
        ("derived_is_distinct", Json::b(true)),
        ("resolve_ok", Json::b(true)),
        ("order_before_checkpoint", Json::s("")),
        ("order_after_checkpoint", Json::s("42")),
        ("derived_final_state", Json::s("Fulfilled")),
        ("derived_result", Json::s("undefined")),
    ]);
    vec![expect_eq(
        "promise/native_then_checkpoint",
        expected,
        actual,
    )]
}

pub(crate) fn reject_callback_events() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    // The callback must be installed before the isolate is borrowed by the
    // handle scope below.
    isolate.set_promise_reject_callback(reject_cb);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let handler = v8::Function::new(scope, cb_noop).unwrap();

    // Case A: reject with no handler fires WithNoHandler synchronously;
    // attaching a handler afterwards fires HandlerAddedAfterReject.
    let resolver1 = v8::PromiseResolver::new(scope).unwrap();
    let promise1 = resolver1.get_promise(scope);
    let reject_ok = resolver1.reject(scope, v8::String::new(scope, "x").unwrap().into());
    let after_reject = reject_events_snapshot();
    let caught = promise1.catch(scope, handler).is_some();
    let after_catch = reject_events_snapshot();
    scope.perform_microtask_checkpoint();
    let after_checkpoint_a = reject_events_snapshot();

    // Case B: a handler attached before the reject produces no event.
    let resolver2 = v8::PromiseResolver::new(scope).unwrap();
    let promise2 = resolver2.get_promise(scope);
    promise2.then(scope, handler).unwrap();
    let reject2_ok = resolver2.reject(scope, v8::String::new(scope, "y").unwrap().into());
    let after_prehandled_reject = reject_events_snapshot();
    scope.perform_microtask_checkpoint();
    let after_checkpoint_b = reject_events_snapshot();

    // Case C: rejecting an already fulfilled promise leaves the settlement
    // untouched; the AfterResolved events were removed from V8 and never
    // fire in this build.
    let resolver3 = v8::PromiseResolver::new(scope).unwrap();
    let promise3 = resolver3.get_promise(scope);
    resolver3
        .resolve(scope, v8::Integer::new(scope, 1).into())
        .unwrap();
    let reject3_ok = resolver3.reject(scope, v8::String::new(scope, "z").unwrap().into());
    let after_reject_fulfilled = reject_events_snapshot();
    let promise3_state = state_name(promise3.state());
    scope.perform_microtask_checkpoint();
    let after_checkpoint_c = reject_events_snapshot();

    let actual = Json::obj(vec![
        ("reject_ok", reject_ok.map(Json::b).unwrap_or(Json::Null)),
        ("after_reject", Json::arr(after_reject)),
        ("catch_attached", Json::b(caught)),
        ("after_catch", Json::arr(after_catch)),
        ("after_checkpoint_a", Json::arr(after_checkpoint_a)),
        ("reject2_ok", reject2_ok.map(Json::b).unwrap_or(Json::Null)),
        (
            "after_prehandled_reject",
            Json::arr(after_prehandled_reject),
        ),
        ("after_checkpoint_b", Json::arr(after_checkpoint_b)),
        ("reject3_ok", reject3_ok.map(Json::b).unwrap_or(Json::Null)),
        ("after_reject_fulfilled", Json::arr(after_reject_fulfilled)),
        ("promise3_state", Json::s(promise3_state)),
        ("after_checkpoint_c", Json::arr(after_checkpoint_c)),
    ]);
    let expected = Json::obj(vec![
        ("reject_ok", Json::b(true)),
        ("after_reject", Json::arr(vec![Json::s("WithNoHandler")])),
        ("catch_attached", Json::b(true)),
        (
            "after_catch",
            Json::arr(vec![
                Json::s("WithNoHandler"),
                Json::s("HandlerAddedAfterReject"),
            ]),
        ),
        // The catch handler fulfills the derived promise: no new event.
        (
            "after_checkpoint_a",
            Json::arr(vec![
                Json::s("WithNoHandler"),
                Json::s("HandlerAddedAfterReject"),
            ]),
        ),
        // The boolean reports call success, not a settlement change.
        ("reject2_ok", Json::b(true)),
        // No event at reject time: promise2 already has a handler.
        (
            "after_prehandled_reject",
            Json::arr(vec![
                Json::s("WithNoHandler"),
                Json::s("HandlerAddedAfterReject"),
            ]),
        ),
        // `then` registered no on_rejected, so when the reaction job runs
        // the derived promise is rejected with the same reason and reported
        // as unhandled.
        (
            "after_checkpoint_b",
            Json::arr(vec![
                Json::s("WithNoHandler"),
                Json::s("HandlerAddedAfterReject"),
                Json::s("WithNoHandler"),
            ]),
        ),
        // Ignored (promise already fulfilled) but still reported as success.
        ("reject3_ok", Json::b(true)),
        // RejectAfterResolved / ResolveAfterResolved were removed from V8.
        (
            "after_reject_fulfilled",
            Json::arr(vec![
                Json::s("WithNoHandler"),
                Json::s("HandlerAddedAfterReject"),
                Json::s("WithNoHandler"),
            ]),
        ),
        ("promise3_state", Json::s("Fulfilled")),
        (
            "after_checkpoint_c",
            Json::arr(vec![
                Json::s("WithNoHandler"),
                Json::s("HandlerAddedAfterReject"),
                Json::s("WithNoHandler"),
            ]),
        ),
    ]);
    vec![expect_eq(
        "promise/reject_callback_events",
        expected,
        actual,
    )]
}
