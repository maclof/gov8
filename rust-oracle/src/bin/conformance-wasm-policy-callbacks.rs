//! Isolate-level WebAssembly policy callback oracle for `v8` =152.2.0.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::cell::RefCell;
use std::rc::Rc;

const VALID_BYTES_JS: &str = "new Uint8Array([0,97,115,109,1,0,0,0])";
const INVALID_BYTES_JS: &str = "new Uint8Array([0,1,2,3])";

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

fn promise_state(value: v8::PromiseState) -> &'static str {
    match value {
        v8::PromiseState::Pending => "Pending",
        v8::PromiseState::Fulfilled => "Fulfilled",
        v8::PromiseState::Rejected => "Rejected",
    }
}

#[derive(Default)]
struct AllowState {
    calls: RefCell<Vec<(String, bool)>>,
}

unsafe extern "C" fn allow_callback(
    context: v8::Local<v8::Context>,
    source: v8::Local<v8::String>,
) -> bool {
    v8::callback_scope!(unsafe scope, context);
    let state = context.get_slot::<AllowState>().unwrap();
    let marker_key = v8::String::new(scope, "policyMarker").unwrap().into();
    let marker_ok = context
        .global(scope)
        .get(scope, marker_key)
        .is_some_and(|value| value.integer_value(scope) == Some(73));
    state
        .calls
        .borrow_mut()
        .push((source.to_rust_string_lossy(scope), marker_ok));
    true
}

unsafe extern "C" fn deny_callback(
    context: v8::Local<v8::Context>,
    source: v8::Local<v8::String>,
) -> bool {
    v8::callback_scope!(unsafe scope, context);
    let state = context.get_slot::<AllowState>().unwrap();
    state
        .calls
        .borrow_mut()
        .push((source.to_rust_string_lossy(scope), true));
    false
}

unsafe extern "C" fn throwing_callback(
    context: v8::Local<v8::Context>,
    source: v8::Local<v8::String>,
) -> bool {
    v8::callback_scope!(unsafe scope, context);
    let state = context.get_slot::<AllowState>().unwrap();
    state
        .calls
        .borrow_mut()
        .push((source.to_rust_string_lossy(scope), true));
    let message = v8::String::new(scope, "wasm policy boom").unwrap();
    let exception = v8::Exception::type_error(scope, message);
    scope.throw_exception(exception);
    false
}

fn configure_context<'s>(
    scope: &mut v8::PinScope<'s, '_, ()>,
) -> (v8::Local<'s, v8::Context>, Rc<AllowState>) {
    let context = v8::Context::new(scope, Default::default());
    let state = Rc::new(AllowState::default());
    context.set_slot(Rc::clone(&state));
    {
        let scope = &mut v8::ContextScope::new(scope, context);
        let key = v8::String::new(scope, "policyMarker").unwrap().into();
        let value = v8::Integer::new(scope, 73).into();
        context.global(scope).set(scope, key, value).unwrap();
    }
    (context, state)
}

fn sync_policy_case(
    callback: unsafe extern "C" fn(v8::Local<v8::Context>, v8::Local<v8::String>) -> bool,
) -> (bool, bool, String, Vec<(String, bool)>) {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_allow_wasm_code_generation_callback(callback);
    v8::scope!(let scope, isolate);
    let (context, state) = configure_context(scope);
    let scope = &mut v8::ContextScope::new(scope, context);
    v8::tc_scope!(let tc, scope);
    let result = eval(tc, &format!("new WebAssembly.Module({VALID_BYTES_JS})"));
    let caught = tc.has_caught();
    let exception = tc
        .exception()
        .map_or(String::new(), |value| value.to_rust_string_lossy(tc));
    let observations = state.calls.borrow().clone();
    (
        result.is_some_and(|value| value.is_wasm_module_object()),
        caught,
        exception,
        observations,
    )
}

fn allow_wasm_policy() -> Vec<CheckOutcome> {
    let allowed = sync_policy_case(allow_callback);
    let denied = sync_policy_case(deny_callback);
    let thrown = sync_policy_case(throwing_callback);

    let replacement = {
        let isolate = &mut v8::Isolate::new(Default::default());
        isolate.set_allow_wasm_code_generation_callback(deny_callback);
        isolate.set_allow_wasm_code_generation_callback(allow_callback);
        v8::scope!(let scope, isolate);
        let (context, state) = configure_context(scope);
        let scope = &mut v8::ContextScope::new(scope, context);
        let compiled = eval(scope, &format!("new WebAssembly.Module({VALID_BYTES_JS})"))
            .is_some_and(|value| value.is_wasm_module_object());
        let calls = state.calls.borrow().len();
        (compiled, calls)
    };

    let encode = |case: (bool, bool, String, Vec<(String, bool)>)| {
        let source = case.3.first().map_or("", |item| item.0.as_str());
        Json::obj(vec![
            ("compiled", Json::b(case.0)),
            ("caught", Json::b(case.1)),
            (
                "exception",
                if case.2.is_empty() {
                    Json::Null
                } else {
                    Json::s(&case.2)
                },
            ),
            ("calls", Json::i(case.3.len() as i64)),
            ("source", Json::s(source)),
            (
                "context_marker_seen",
                Json::b(case.3.first().is_some_and(|item| item.1)),
            ),
        ])
    };

    vec![pass(
        "wasm-policy-callbacks/sync_allow_deny_exception",
        Json::obj(vec![
            ("allow", encode(allowed)),
            ("deny", encode(denied)),
            ("throw", encode(thrown)),
            (
                "replacement",
                Json::obj(vec![
                    ("last_setter_wins", Json::b(replacement.0)),
                    ("calls", Json::i(replacement.1 as i64)),
                    ("clear_api_exposed", Json::b(false)),
                ]),
            ),
        ]),
    )]
}

#[derive(Clone)]
struct AsyncObservation {
    success: &'static str,
    context_marker_seen: bool,
    resolver_promise_same: bool,
    result_is_wasm_module: bool,
    result_is_native_error: bool,
    result_text: String,
}

struct AsyncState {
    original: v8::Global<v8::Promise>,
    observations: RefCell<Vec<AsyncObservation>>,
    callback_result: RefCell<Option<v8::Global<v8::Value>>>,
}

unsafe extern "C" fn async_resolve_callback(
    _isolate: v8::UnsafeRawIsolatePtr,
    context: v8::Local<v8::Context>,
    resolver: v8::Local<v8::PromiseResolver>,
    result: v8::Local<v8::Value>,
    success: v8::WasmAsyncSuccess,
) {
    v8::callback_scope!(unsafe scope, context);
    let state = context.get_slot::<AsyncState>().unwrap();
    let original = v8::Local::new(scope, &state.original);
    let resolver_promise = resolver.get_promise(scope);
    let marker_key = v8::String::new(scope, "asyncMarker").unwrap().into();
    let marker_seen = context
        .global(scope)
        .get(scope, marker_key)
        .is_some_and(|value| value.integer_value(scope) == Some(91));
    state.observations.borrow_mut().push(AsyncObservation {
        success: match success {
            v8::WasmAsyncSuccess::Success => "Success",
            v8::WasmAsyncSuccess::Fail => "Fail",
        },
        context_marker_seen: marker_seen,
        resolver_promise_same: resolver_promise.strict_equals(original.into()),
        result_is_wasm_module: result.is_wasm_module_object(),
        result_is_native_error: result.is_native_error(),
        result_text: result.to_rust_string_lossy(scope),
    });
    state
        .callback_result
        .borrow_mut()
        .replace(v8::Global::new(scope, result));
    match success {
        v8::WasmAsyncSuccess::Success => resolver.resolve(scope, result).unwrap(),
        v8::WasmAsyncSuccess::Fail => resolver.reject(scope, result).unwrap(),
    };
}

fn pump_until_callback(scope: &mut v8::PinScope<'_, '_>, state: &Rc<AsyncState>) {
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(10);
    while state.observations.borrow().is_empty() {
        assert!(
            std::time::Instant::now() < deadline,
            "timed out waiting for Wasm async resolve callback"
        );
        if !v8::Platform::pump_message_loop(&v8::V8::get_current_platform(), scope, false) {
            std::thread::yield_now();
        }
    }
    scope.perform_microtask_checkpoint();
}

fn async_case(valid: bool, replace_initial_callback: bool) -> Json {
    let isolate = &mut v8::Isolate::new(Default::default());
    if replace_initial_callback {
        isolate.set_wasm_async_resolve_promise_callback(panic_async);
    }
    isolate.set_wasm_async_resolve_promise_callback(async_resolve_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let key = v8::String::new(scope, "asyncMarker").unwrap().into();
    context
        .global(scope)
        .set(scope, key, v8::Integer::new(scope, 91).into())
        .unwrap();
    let bytes = if valid {
        VALID_BYTES_JS
    } else {
        INVALID_BYTES_JS
    };
    let promise = eval(scope, &format!("WebAssembly.compile({bytes})"))
        .unwrap()
        .cast::<v8::Promise>();
    let before = promise_state(promise.state());
    let state = Rc::new(AsyncState {
        original: v8::Global::new(scope, promise),
        observations: RefCell::new(Vec::new()),
        callback_result: RefCell::new(None),
    });
    context.set_slot(Rc::clone(&state));
    pump_until_callback(scope, &state);
    let observation = state.observations.borrow()[0].clone();
    let callback_result = state.callback_result.borrow_mut().take().unwrap();
    let callback_result = v8::Local::new(scope, callback_result);
    let promise_result = promise.result(scope);
    let actual = Json::obj(vec![
        ("state_before", Json::s(before)),
        ("state_after", Json::s(promise_state(promise.state()))),
        (
            "callback_calls",
            Json::i(state.observations.borrow().len() as i64),
        ),
        ("success", Json::s(observation.success)),
        (
            "context_marker_seen",
            Json::b(observation.context_marker_seen),
        ),
        (
            "resolver_promise_same",
            Json::b(observation.resolver_promise_same),
        ),
        (
            "result_is_wasm_module",
            Json::b(observation.result_is_wasm_module),
        ),
        (
            "result_is_native_error",
            Json::b(observation.result_is_native_error),
        ),
        ("result_text", Json::s(&observation.result_text)),
        (
            "settled_value_same",
            Json::b(promise_result.strict_equals(callback_result)),
        ),
    ]);
    context.remove_slot::<AsyncState>();
    actual
}

fn async_resolution() -> Vec<CheckOutcome> {
    vec![pass(
        "wasm-policy-callbacks/async_success_failure_settlement",
        Json::obj(vec![
            ("valid", async_case(true, false)),
            ("invalid", async_case(false, false)),
            ("replacement", async_case(true, true)),
            ("clear_api_exposed", Json::b(false)),
        ]),
    )]
}

unsafe extern "C" fn panic_allow(
    _context: v8::Local<v8::Context>,
    _source: v8::Local<v8::String>,
) -> bool {
    panic!("allow wasm callback panic boundary")
}

unsafe extern "C" fn panic_async(
    _isolate: v8::UnsafeRawIsolatePtr,
    _context: v8::Local<v8::Context>,
    _resolver: v8::Local<v8::PromiseResolver>,
    _result: v8::Local<v8::Value>,
    _success: v8::WasmAsyncSuccess,
) {
    panic!("wasm async callback panic boundary")
}

fn panic_allow_mode() {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_allow_wasm_code_generation_callback(panic_allow);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _ = eval(scope, &format!("new WebAssembly.Module({VALID_BYTES_JS})"));
}

fn panic_async_mode() {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_wasm_async_resolve_promise_callback(panic_async);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _promise = eval(scope, &format!("WebAssembly.compile({VALID_BYTES_JS})"));
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(10);
    loop {
        assert!(
            std::time::Instant::now() < deadline,
            "timed out waiting for panic-async callback"
        );
        v8::Platform::pump_message_loop(&v8::V8::get_current_platform(), scope, false);
        std::thread::yield_now();
    }
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    match std::env::args().nth(1).as_deref() {
        Some("mode=panic-allow") => panic_allow_mode(),
        Some("mode=panic-async") => panic_async_mode(),
        Some(other) => {
            eprintln!("unknown mode: {other}");
            return std::process::ExitCode::FAILURE;
        }
        None => {
            let checks: Vec<CheckOutcome> = [allow_wasm_policy(), async_resolution()]
                .into_iter()
                .flatten()
                .collect();
            let passed = checks.iter().filter(|check| check.passed()).count();
            for check in &checks {
                println!("{}", check.to_line());
            }
            println!(
                "{}",
                summary_line(checks.len(), passed, checks.len() - passed)
            );
            return if passed == checks.len() {
                std::process::ExitCode::SUCCESS
            } else {
                std::process::ExitCode::FAILURE
            };
        }
    }
    std::process::ExitCode::SUCCESS
}
