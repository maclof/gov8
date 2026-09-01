//! SyntheticModule conformance for pinned `v8` =152.2.0.

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::Mutex;

static CALLBACKS: AtomicUsize = AtomicUsize::new(0);
static INVALID_CAUGHT: AtomicBool = AtomicBool::new(false);
static INVALID_EXCEPTION: Mutex<String> = Mutex::new(String::new());

macro_rules! create_synthetic {
    ($scope:expr, $exports:expr, $callback:expr) => {{
        let name = v8::String::new($scope, "synthetic-fixture").unwrap();
        let export_names: Vec<_> = $exports
            .iter()
            .map(|name| v8::String::new($scope, name).unwrap())
            .collect();
        v8::Module::create_synthetic_module($scope, name, &export_names, $callback)
    }};
}

fn status(value: v8::ModuleStatus) -> &'static str {
    match value {
        v8::ModuleStatus::Uninstantiated => "Uninstantiated",
        v8::ModuleStatus::Instantiating => "Instantiating",
        v8::ModuleStatus::Instantiated => "Instantiated",
        v8::ModuleStatus::Evaluating => "Evaluating",
        v8::ModuleStatus::Evaluated => "Evaluated",
        v8::ModuleStatus::Errored => "Errored",
    }
}

fn promise_state(value: v8::PromiseState) -> &'static str {
    match value {
        v8::PromiseState::Pending => "Pending",
        v8::PromiseState::Fulfilled => "Fulfilled",
        v8::PromiseState::Rejected => "Rejected",
    }
}

#[allow(clippy::unnecessary_wraps)]
fn no_resolve<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    CALLBACKS.fetch_add(1000, Ordering::SeqCst);
    None
}

#[allow(clippy::unnecessary_wraps)]
fn noop_evaluation<'s>(
    context: v8::Local<'s, v8::Context>,
    _module: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Value>> {
    CALLBACKS.fetch_add(1, Ordering::SeqCst);
    v8::callback_scope!(unsafe scope, context);
    Some(v8::Integer::new(scope, 77).into())
}

#[allow(clippy::unnecessary_wraps)]
fn setting_evaluation<'s>(
    context: v8::Local<'s, v8::Context>,
    module: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Value>> {
    CALLBACKS.fetch_add(1, Ordering::SeqCst);
    v8::callback_scope!(unsafe scope, context);
    for (name, value) in [("a", 1), ("b", 2)] {
        let name = v8::String::new(scope, name).unwrap();
        let value = v8::Integer::new(scope, value).into();
        module
            .set_synthetic_module_export(scope, name, value)
            .unwrap();
    }
    {
        v8::tc_scope!(let tc, scope);
        let missing = v8::String::new(tc, "missing").unwrap();
        let value = v8::undefined(tc).into();
        let result = module.set_synthetic_module_export(tc, missing, value);
        INVALID_CAUGHT.store(result.is_none() && tc.has_caught(), Ordering::SeqCst);
        *INVALID_EXCEPTION.lock().unwrap() = tc
            .exception()
            .map_or(String::new(), |value| value.to_rust_string_lossy(tc));
        tc.reset();
    }
    Some(v8::Integer::new(scope, 99).into())
}

fn throwing_evaluation<'s>(
    context: v8::Local<'s, v8::Context>,
    _module: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Value>> {
    CALLBACKS.fetch_add(1, Ordering::SeqCst);
    v8::callback_scope!(unsafe scope, context);
    let message = v8::String::new(scope, "synthetic boom").unwrap();
    let exception = v8::Exception::type_error(scope, message);
    scope.throw_exception(exception);
    None
}

fn get_i64(scope: &v8::PinScope<'_, '_>, namespace: v8::Local<'_, v8::Object>, name: &str) -> i64 {
    let key = v8::String::new(scope, name).unwrap().into();
    namespace
        .get(scope, key)
        .and_then(|value| value.integer_value(scope))
        .unwrap_or(-1)
}

fn creation_and_pre_set_exports() -> Vec<CheckOutcome> {
    CALLBACKS.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = create_synthetic!(scope, &["b", "a"], noop_evaluation);
    let before = status(module.get_status());
    let (pre_set, pre_set_caught, pre_set_exception) = {
        v8::tc_scope!(let tc, scope);
        let a = v8::String::new(tc, "a").unwrap();
        let pre_value = v8::Integer::new(tc, 10).into();
        let result = module.set_synthetic_module_export(tc, a, pre_value);
        let caught = tc.has_caught();
        let exception = tc
            .exception()
            .map_or(String::new(), |value| value.to_rust_string_lossy(tc));
        tc.reset();
        (result, caught, exception)
    };
    let requests = module.get_module_requests().length();
    let instantiated = module.instantiate_module(scope, no_resolve);
    let b = v8::String::new(scope, "b").unwrap();
    let post_value = v8::Integer::new(scope, 20).into();
    let post_link_set = module.set_synthetic_module_export(scope, b, post_value);
    let namespace_before = module.get_module_namespace();
    let evaluation = module.evaluate(scope).unwrap();
    let evaluation_is_promise = evaluation.is_promise();
    let evaluation_result = evaluation.integer_value(scope).unwrap_or(-1);
    let second_evaluation = module.evaluate(scope);
    let second_evaluation_same =
        second_evaluation.is_some_and(|value| evaluation.strict_equals(value));
    let second_promise: Option<v8::Local<v8::Promise>> =
        second_evaluation.and_then(|value| value.try_into().ok());
    scope.perform_microtask_checkpoint();
    let second_promise_state =
        second_promise.map_or("None", |promise| promise_state(promise.state()));
    let second_result_is_undefined =
        second_promise.is_some_and(|promise| promise.result(scope).is_undefined());
    let namespace_after = module.get_module_namespace();
    let object = namespace_after.cast::<v8::Object>();
    let actual = Json::obj(vec![
        ("before", Json::s(before)),
        ("source_text", Json::b(module.is_source_text_module())),
        ("synthetic", Json::b(module.is_synthetic_module())),
        ("script_id_none", Json::b(module.script_id().is_none())),
        (
            "identity_hash_nonzero",
            Json::b(module.get_identity_hash().get() != 0),
        ),
        ("requests", Json::i(requests as i64)),
        ("pre_set_is_none", Json::b(pre_set.is_none())),
        ("pre_set_caught", Json::b(pre_set_caught)),
        ("pre_set_exception", Json::s(&pre_set_exception)),
        ("instantiate", Json::b(instantiated == Some(true))),
        ("post_link_set", Json::b(post_link_set == Some(true))),
        (
            "resolver_calls",
            Json::i((CALLBACKS.load(Ordering::SeqCst) / 1000) as i64),
        ),
        (
            "evaluation_calls",
            Json::i((CALLBACKS.load(Ordering::SeqCst) % 1000) as i64),
        ),
        ("status", Json::s(status(module.get_status()))),
        ("evaluation_is_promise", Json::b(evaluation_is_promise)),
        ("evaluation_result", Json::i(evaluation_result)),
        ("second_evaluate_some", Json::b(second_evaluation.is_some())),
        ("second_is_promise", Json::b(second_promise.is_some())),
        ("second_promise_state", Json::s(second_promise_state)),
        (
            "second_result_is_undefined",
            Json::b(second_result_is_undefined),
        ),
        ("second_evaluation_same", Json::b(second_evaluation_same)),
        (
            "namespace_stable",
            Json::b(namespace_before.strict_equals(namespace_after)),
        ),
        (
            "a_is_undefined",
            Json::b({
                let key = v8::String::new(scope, "a").unwrap().into();
                object.get(scope, key).unwrap().is_undefined()
            }),
        ),
        ("b", Json::i(get_i64(scope, object, "b"))),
    ]);
    let expected = Json::obj(vec![
        ("before", Json::s("Uninstantiated")),
        ("source_text", Json::b(false)),
        ("synthetic", Json::b(true)),
        ("script_id_none", Json::b(true)),
        ("identity_hash_nonzero", Json::b(true)),
        ("requests", Json::i(0)),
        ("pre_set_is_none", Json::b(true)),
        ("pre_set_caught", Json::b(true)),
        (
            "pre_set_exception",
            Json::s("ReferenceError: Export 'a' is not defined in module"),
        ),
        ("instantiate", Json::b(true)),
        ("post_link_set", Json::b(true)),
        ("resolver_calls", Json::i(0)),
        ("evaluation_calls", Json::i(1)),
        ("status", Json::s("Evaluated")),
        ("evaluation_is_promise", Json::b(false)),
        ("evaluation_result", Json::i(77)),
        ("second_evaluate_some", Json::b(true)),
        ("second_is_promise", Json::b(true)),
        ("second_promise_state", Json::s("Fulfilled")),
        ("second_result_is_undefined", Json::b(true)),
        ("second_evaluation_same", Json::b(false)),
        ("namespace_stable", Json::b(true)),
        ("a_is_undefined", Json::b(true)),
        ("b", Json::i(20)),
    ]);
    vec![expect_eq(
        "modules-synthetic/creation_and_pre_set_exports",
        expected,
        actual,
    )]
}

fn evaluation_sets_exports_and_recovers_invalid() -> Vec<CheckOutcome> {
    CALLBACKS.store(0, Ordering::SeqCst);
    INVALID_CAUGHT.store(false, Ordering::SeqCst);
    INVALID_EXCEPTION.lock().unwrap().clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = create_synthetic!(scope, &["a", "b"], setting_evaluation);
    module.instantiate_module(scope, no_resolve).unwrap();
    let value = module.evaluate(scope).unwrap();
    let evaluation_is_promise = value.is_promise();
    let evaluation_result = value.integer_value(scope).unwrap_or(-1);
    let namespace = module.get_module_namespace().cast::<v8::Object>();
    let missing_key = v8::String::new(scope, "missing").unwrap().into();
    let actual = Json::obj(vec![
        (
            "invalid_export_caught",
            Json::b(INVALID_CAUGHT.load(Ordering::SeqCst)),
        ),
        (
            "invalid_export_exception",
            Json::s(&INVALID_EXCEPTION.lock().unwrap()),
        ),
        ("status", Json::s(status(module.get_status()))),
        ("evaluation_is_promise", Json::b(evaluation_is_promise)),
        ("evaluation_result", Json::i(evaluation_result)),
        ("a", Json::i(get_i64(scope, namespace, "a"))),
        ("b", Json::i(get_i64(scope, namespace, "b"))),
        (
            "missing_is_undefined",
            Json::b(namespace.get(scope, missing_key).unwrap().is_undefined()),
        ),
        (
            "isolate_recovers",
            Json::b(
                v8::Script::compile(scope, v8::String::new(scope, "6*7").unwrap(), None)
                    .unwrap()
                    .run(scope)
                    .unwrap()
                    .integer_value(scope)
                    == Some(42),
            ),
        ),
    ]);
    let expected = Json::obj(vec![
        ("invalid_export_caught", Json::b(true)),
        (
            "invalid_export_exception",
            Json::s("ReferenceError: Export 'missing' is not defined in module"),
        ),
        ("status", Json::s("Evaluated")),
        ("evaluation_is_promise", Json::b(false)),
        ("evaluation_result", Json::i(99)),
        ("a", Json::i(1)),
        ("b", Json::i(2)),
        ("missing_is_undefined", Json::b(true)),
        ("isolate_recovers", Json::b(true)),
    ]);
    vec![expect_eq(
        "modules-synthetic/evaluation_sets_and_invalid_export",
        expected,
        actual,
    )]
}

fn thrown_evaluation() -> Vec<CheckOutcome> {
    CALLBACKS.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = create_synthetic!(scope, &[] as &[&str], throwing_evaluation);
    module.instantiate_module(scope, no_resolve).unwrap();
    v8::tc_scope!(let tc, scope);
    let evaluation = module.evaluate(tc);
    let caught = tc.has_caught();
    let exception = tc.exception();
    let exception_text = exception.map_or(String::new(), |value| value.to_rust_string_lossy(tc));
    let stored_exception_same =
        exception.is_some_and(|value| module.get_exception().strict_equals(value));
    let actual = Json::obj(vec![
        ("evaluate_some", Json::b(evaluation.is_some())),
        ("trycatch_caught", Json::b(caught)),
        ("status", Json::s(status(module.get_status()))),
        ("exception", Json::s(&exception_text)),
        ("stored_exception_same", Json::b(stored_exception_same)),
    ]);
    let expected = Json::obj(vec![
        ("evaluate_some", Json::b(false)),
        ("trycatch_caught", Json::b(true)),
        ("status", Json::s("Errored")),
        ("exception", Json::s("TypeError: synthetic boom")),
        ("stored_exception_same", Json::b(true)),
    ]);
    vec![expect_eq(
        "modules-synthetic/thrown_evaluation",
        expected,
        actual,
    )]
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut checks = creation_and_pre_set_exports();
    checks.extend(evaluation_sets_exports_and_recovers_invalid());
    checks.extend(thrown_evaluation());
    let passed = checks.iter().filter(|c| c.passed()).count();
    for check in &checks {
        println!("{}", check.to_line());
    }
    println!(
        "{}",
        summary_line(checks.len(), passed, checks.len() - passed)
    );
    if passed == checks.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
