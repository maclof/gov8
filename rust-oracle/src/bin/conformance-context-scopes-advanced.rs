//! Context construction, execution scopes, and advanced microtask conformance
//! for the pinned `v8` crate (=152.2.0 / V8 15.2.124.1).
//!
//! Existing fixtures already cover default context entry nesting and ordinary
//! explicit/automatic microtask ordering.  This slice instead characterizes
//! construction-time `ContextOptions`, continuation-preserved data,
//! `MicrotaskQueue` running/depth observations, context-level promise hooks,
//! and nested allow/disallow JavaScript execution scopes.

use std::convert::TryFrom as _;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Mutex;

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

fn eval<'s>(scope: &mut v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

fn eval_text(scope: &mut v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source)
        .and_then(|v| v.to_string(scope))
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn eval_function<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    source: &str,
) -> v8::Local<'s, v8::Function> {
    v8::Local::<v8::Function>::try_from(eval(scope, source).unwrap()).unwrap()
}

fn queue_ptr(queue: &v8::MicrotaskQueue) -> *mut v8::MicrotaskQueue {
    std::ptr::from_ref(queue).cast_mut()
}

fn context_options_global_template_and_extras() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    let key = v8::String::new(scope, "fromTemplate").unwrap();
    let value = v8::Integer::new(scope, 73);
    template.set(key.into(), value.into());
    let context = v8::Context::new(
        scope,
        v8::ContextOptions {
            global_template: Some(template),
            ..Default::default()
        },
    );
    let scope = &mut v8::ContextScope::new(scope, context);
    let extras_a = context.get_extras_binding_object(scope);
    let extras_b = context.get_extras_binding_object(scope);
    let extras_names = extras_a
        .get_own_property_names(scope, Default::default())
        .map(|names| names.length() as i64);

    vec![pass(
        "context-scopes-advanced/context/options_global_template_and_extras",
        Json::obj(vec![
            (
                "template_value",
                Json::s(&eval_text(scope, "String(fromTemplate)")),
            ),
            (
                "template_value_is_own",
                Json::b(eval_text(scope, "Object.hasOwn(globalThis, 'fromTemplate')") == "true"),
            ),
            ("extras_is_object", Json::b(extras_a.is_object())),
            ("extras_identity_stable", Json::b(extras_a == extras_b)),
            (
                "extras_own_property_count",
                extras_names.map_or(Json::Null, Json::i),
            ),
        ]),
    )]
}

fn context_options_global_object_reuse() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    let template_key = v8::String::new(scope, "templated").unwrap();
    template.set(template_key.into(), v8::Integer::new(scope, 9).into());
    let first = v8::Context::new(
        scope,
        v8::ContextOptions {
            global_template: Some(template),
            ..Default::default()
        },
    );
    let reused_global = {
        let first_scope = &mut v8::ContextScope::new(scope, first);
        let _ = eval(first_scope, "globalThis.transient = 41");
        first.global(first_scope)
    };
    let second = v8::Context::new(
        scope,
        v8::ContextOptions {
            global_template: Some(template),
            global_object: Some(reused_global.into()),
            ..Default::default()
        },
    );
    let second_scope = &mut v8::ContextScope::new(scope, second);
    let second_global = second.global(second_scope);

    vec![pass(
        "context-scopes-advanced/context/options_global_object_reuse",
        Json::obj(vec![
            (
                "global_identity_reused",
                Json::b(second_global == reused_global),
            ),
            (
                "transient_type_after_reuse",
                Json::s(&eval_text(second_scope, "typeof transient")),
            ),
            (
                "template_value_after_reuse",
                Json::s(&eval_text(second_scope, "String(templated)")),
            ),
            (
                "builtins_available",
                Json::b(eval_text(second_scope, "typeof Object") == "function"),
            ),
        ]),
    )]
}

fn context_options_distinct_queues() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let queue_a = v8::MicrotaskQueue::new(scope, v8::MicrotasksPolicy::Explicit);
    let queue_b = v8::MicrotaskQueue::new(scope, v8::MicrotasksPolicy::Explicit);
    let context_a = v8::Context::new(
        scope,
        v8::ContextOptions {
            microtask_queue: Some(queue_ptr(queue_a.as_ref())),
            ..Default::default()
        },
    );
    let context_b = v8::Context::new(
        scope,
        v8::ContextOptions {
            microtask_queue: Some(queue_ptr(queue_b.as_ref())),
            ..Default::default()
        },
    );
    let attached_a = std::ptr::eq(context_a.get_microtask_queue().unwrap(), queue_a.as_ref());
    let attached_b = std::ptr::eq(context_b.get_microtask_queue().unwrap(), queue_b.as_ref());
    {
        let scope_a = &mut v8::ContextScope::new(scope, context_a);
        let _ = eval(
            scope_a,
            "globalThis.order = []; Promise.resolve().then(() => order.push('a'));",
        );
    }
    {
        let scope_b = &mut v8::ContextScope::new(scope, context_b);
        let _ = eval(
            scope_b,
            "globalThis.order = []; Promise.resolve().then(() => order.push('b'));",
        );
    }
    queue_a.perform_checkpoint(scope);
    let after_a = {
        let scope_a = &mut v8::ContextScope::new(scope, context_a);
        eval_text(scope_a, "order.join(',')")
    };
    let b_before = {
        let scope_b = &mut v8::ContextScope::new(scope, context_b);
        eval_text(scope_b, "order.join(',')")
    };
    queue_b.perform_checkpoint(scope);
    let b_after = {
        let scope_b = &mut v8::ContextScope::new(scope, context_b);
        eval_text(scope_b, "order.join(',')")
    };

    vec![pass(
        "context-scopes-advanced/microtask/options_distinct_queues",
        Json::obj(vec![
            ("queue_a_attached_at_creation", Json::b(attached_a)),
            ("queue_b_attached_at_creation", Json::b(attached_b)),
            (
                "queues_distinct",
                Json::b(!std::ptr::eq(queue_a.as_ref(), queue_b.as_ref())),
            ),
            ("a_after_a_checkpoint", Json::s(&after_a)),
            ("b_before_b_checkpoint", Json::s(&b_before)),
            ("b_after_b_checkpoint", Json::s(&b_after)),
        ]),
    )]
}

fn context_options_shared_queue() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let queue = v8::MicrotaskQueue::new(scope, v8::MicrotasksPolicy::Explicit);
    let options = || v8::ContextOptions {
        microtask_queue: Some(queue_ptr(queue.as_ref())),
        ..Default::default()
    };
    let context_a = v8::Context::new(scope, options());
    let context_b = v8::Context::new(scope, options());
    {
        let scope_a = &mut v8::ContextScope::new(scope, context_a);
        let _ = eval(
            scope_a,
            "globalThis.done = ''; Promise.resolve().then(() => done = 'a');",
        );
    }
    {
        let scope_b = &mut v8::ContextScope::new(scope, context_b);
        let _ = eval(
            scope_b,
            "globalThis.done = ''; Promise.resolve().then(() => done = 'b');",
        );
    }
    queue.perform_checkpoint(scope);
    let a = {
        let scope_a = &mut v8::ContextScope::new(scope, context_a);
        eval_text(scope_a, "done")
    };
    let b = {
        let scope_b = &mut v8::ContextScope::new(scope, context_b);
        eval_text(scope_b, "done")
    };
    vec![pass(
        "context-scopes-advanced/microtask/options_shared_queue",
        Json::obj(vec![
            (
                "contexts_share_queue",
                Json::b(std::ptr::eq(
                    context_a.get_microtask_queue().unwrap(),
                    context_b.get_microtask_queue().unwrap(),
                )),
            ),
            ("context_a_after_checkpoint", Json::s(&a)),
            ("context_b_after_checkpoint", Json::s(&b)),
        ]),
    )]
}

fn continuation_preserved_data() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context_a = v8::Context::new(scope, Default::default());
    let context_b = v8::Context::new(scope, Default::default());
    let initial_undefined;
    let after_script;
    {
        let scope_a = &mut v8::ContextScope::new(scope, context_a);
        initial_undefined = scope_a
            .get_continuation_preserved_embedder_data()
            .is_undefined();
        let value = v8::String::new(scope_a, "continuation-a").unwrap();
        scope_a.set_continuation_preserved_embedder_data(value.into());
        let _ = eval(scope_a, "Promise.resolve().then(() => 1); 6 * 7");
        after_script = eval_text(scope_a, "String(6 * 7)");
    }
    let visible_in_b;
    {
        let scope_b = &mut v8::ContextScope::new(scope, context_b);
        visible_in_b = scope_b
            .get_continuation_preserved_embedder_data()
            .to_rust_string_lossy(scope_b);
    }
    scope.perform_microtask_checkpoint();
    let after_checkpoint;
    {
        let scope_a = &mut v8::ContextScope::new(scope, context_a);
        after_checkpoint = scope_a
            .get_continuation_preserved_embedder_data()
            .to_rust_string_lossy(scope_a);
    }
    {
        let scope_b = &mut v8::ContextScope::new(scope, context_b);
        scope_b.set_continuation_preserved_embedder_data(v8::undefined(scope_b).into());
    }
    let reset_visible_in_a = {
        let scope_a = &mut v8::ContextScope::new(scope, context_a);
        scope_a
            .get_continuation_preserved_embedder_data()
            .is_undefined()
    };

    vec![pass(
        "context-scopes-advanced/context/continuation_preserved_data",
        Json::obj(vec![
            ("initial_undefined", Json::b(initial_undefined)),
            ("script_completed", Json::s(&after_script)),
            ("visible_in_second_context", Json::s(&visible_in_b)),
            ("survives_microtask_checkpoint", Json::s(&after_checkpoint)),
            (
                "reset_in_second_visible_in_first",
                Json::b(reset_visible_in_a),
            ),
        ]),
    )]
}

static QUEUE_ADDRESS: AtomicUsize = AtomicUsize::new(0);
static QUEUE_OBSERVATIONS: Mutex<Vec<(bool, i32, bool, i32)>> = Mutex::new(Vec::new());

fn queue_observer_callback(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let ptr = QUEUE_ADDRESS.load(Ordering::SeqCst) as *const v8::MicrotaskQueue;
    let queue = unsafe { &*ptr };
    let running_before = queue.is_running_microtasks();
    let depth_before = queue.get_microtasks_scope_depth();
    queue.perform_checkpoint(scope);
    let running_after_nested_checkpoint = queue.is_running_microtasks();
    let depth_after_nested_checkpoint = queue.get_microtasks_scope_depth();
    QUEUE_OBSERVATIONS.lock().unwrap().push((
        running_before,
        depth_before,
        running_after_nested_checkpoint,
        depth_after_nested_checkpoint,
    ));
}

fn microtask_running_and_depth() -> Vec<CheckOutcome> {
    QUEUE_OBSERVATIONS.lock().unwrap().clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let queue = v8::MicrotaskQueue::new(scope, v8::MicrotasksPolicy::Explicit);
    QUEUE_ADDRESS.store(queue_ptr(queue.as_ref()) as usize, Ordering::SeqCst);
    let context = v8::Context::new(
        scope,
        v8::ContextOptions {
            microtask_queue: Some(queue_ptr(queue.as_ref())),
            ..Default::default()
        },
    );
    let outside_before = (
        queue.is_running_microtasks(),
        queue.get_microtasks_scope_depth(),
    );
    {
        let context_scope = &mut v8::ContextScope::new(scope, context);
        let function = v8::Function::builder(queue_observer_callback)
            .build(context_scope)
            .unwrap();
        queue.enqueue_microtask(context_scope, function);
    }
    queue.perform_checkpoint(scope);
    let outside_after = (
        queue.is_running_microtasks(),
        queue.get_microtasks_scope_depth(),
    );
    let observations = std::mem::take(&mut *QUEUE_OBSERVATIONS.lock().unwrap());
    let inside = observations
        .first()
        .copied()
        .unwrap_or((false, -1, false, -1));

    vec![pass(
        "context-scopes-advanced/microtask/running_and_scope_depth",
        Json::obj(vec![
            ("outside_before_running", Json::b(outside_before.0)),
            ("outside_before_depth", Json::i(i64::from(outside_before.1))),
            ("callback_count", Json::i(observations.len() as i64)),
            ("inside_running", Json::b(inside.0)),
            ("inside_depth", Json::i(i64::from(inside.1))),
            ("after_nested_checkpoint_running", Json::b(inside.2)),
            (
                "after_nested_checkpoint_depth",
                Json::i(i64::from(inside.3)),
            ),
            ("outside_after_running", Json::b(outside_after.0)),
            ("outside_after_depth", Json::i(i64::from(outside_after.1))),
        ]),
    )]
}

fn context_promise_hooks() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _ = eval(
        scope,
        r#"globalThis.events = [];
globalThis.initHook = function(_promise, parent) { events.push('init:' + (parent === undefined ? 'undefined' : 'promise')); };
globalThis.beforeHook = function(_promise) { events.push('before'); };
globalThis.afterHook = function(_promise) { events.push('after'); };
globalThis.resolveHook = function(_promise) { events.push('resolve'); };"#,
    );
    let init = eval_function(scope, "initHook");
    let before = eval_function(scope, "beforeHook");
    let after = eval_function(scope, "afterHook");
    let resolve = eval_function(scope, "resolveHook");
    scope.set_promise_hooks(Some(init), Some(before), Some(after), Some(resolve));
    let _ = eval(
        scope,
        "globalThis.p = Promise.resolve(1); globalThis.q = p.then(v => v + 1);",
    );
    let synchronous = eval_text(scope, "events.join(',')");
    scope.perform_microtask_checkpoint();
    let after_checkpoint = eval_text(scope, "events.join(',')");
    scope.set_promise_hooks(None, None, None, None);
    let _ = eval(scope, "Promise.resolve(3)");
    let after_disable = eval_text(scope, "events.join(',')");

    vec![pass(
        "context-scopes-advanced/context/promise_hooks",
        Json::obj(vec![
            ("synchronous", Json::s(&synchronous)),
            ("after_checkpoint", Json::s(&after_checkpoint)),
            ("after_disable", Json::s(&after_disable)),
            (
                "disable_stops_hooks",
                Json::b(after_disable == after_checkpoint),
            ),
        ]),
    )]
}

fn javascript_execution_scope_nesting() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let baseline = eval_text(scope, "String(40 + 2)");

    let throw_failure = {
        v8::tc_scope!(let tc, scope);
        let run_none = {
            let disallow = std::pin::pin!(v8::DisallowJavascriptExecutionScope::new(
                tc,
                v8::OnFailure::ThrowOnFailure,
            ));
            let disallow = &mut disallow.init();
            eval(disallow, "43").is_none()
        };
        Json::obj(vec![
            ("run_none", Json::b(run_none)),
            ("has_caught", Json::b(tc.has_caught())),
            (
                "exception",
                Json::s(
                    &tc.exception()
                        .and_then(|v| v.to_string(tc))
                        .map(|s| s.to_rust_string_lossy(tc))
                        .unwrap_or_default(),
                ),
            ),
        ])
    };

    let nested = {
        v8::tc_scope!(let tc, scope);
        let (allowed_value, disallowed_again) = {
            let disallow = std::pin::pin!(v8::DisallowJavascriptExecutionScope::new(
                tc,
                v8::OnFailure::ThrowOnFailure,
            ));
            let disallow = &mut disallow.init();
            let allowed_value = {
                let allow = std::pin::pin!(v8::AllowJavascriptExecutionScope::new(disallow));
                let allow = &mut allow.init();
                eval_text(allow, "String(44)")
            };
            let disallowed_again = eval(disallow, "45").is_none();
            (allowed_value, disallowed_again)
        };
        Json::obj(vec![
            ("allowed_value", Json::s(&allowed_value)),
            ("disallowed_again", Json::b(disallowed_again)),
            ("has_caught_after_restore", Json::b(tc.has_caught())),
        ])
    };
    let after = eval_text(scope, "String(46)");

    vec![pass(
        "context-scopes-advanced/scope/disallow_allow_nesting",
        Json::obj(vec![
            ("before_scope", Json::s(&baseline)),
            ("throw_on_failure", throw_failure),
            ("nested_allow", nested),
            ("after_scope", Json::s(&after)),
        ]),
    )]
}

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    context_options_global_template_and_extras,
    context_options_global_object_reuse,
    context_options_distinct_queues,
    context_options_shared_queue,
    continuation_preserved_data,
    microtask_running_and_depth,
    context_promise_hooks,
    javascript_execution_scope_nesting,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let outcomes: Vec<_> = CHECKS.iter().flat_map(|check| check()).collect();
    let total = outcomes.len();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let failed = total - passed;
    let mut output = String::new();
    for outcome in &outcomes {
        output.push_str(&outcome.to_line());
        output.push('\n');
    }
    output.push_str(&summary_line(total, passed, failed));
    output.push('\n');
    print!("{output}");
    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
