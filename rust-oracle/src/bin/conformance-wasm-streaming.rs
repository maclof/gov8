//! WebAssembly streaming and asynchronous module-compilation conformance.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::cell::RefCell;
use std::rc::Rc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

const EMPTY_MODULE: &[u8] = &[0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00];
const ANSWER_MODULE: &[u8] = &[
    0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f, 0x03,
    0x02, 0x01, 0x00, 0x07, 0x07, 0x01, 0x03, b'r', b'u', b'n', 0x00, 0x00, 0x0a, 0x06, 0x01, 0x04,
    0x00, 0x41, 0x2a, 0x0b,
];

thread_local! {
    static STREAM: RefCell<Option<v8::WasmStreaming<false>>> = const { RefCell::new(None) };
    static STREAM_SOURCE: RefCell<Option<(String, bool, bool)>> = const { RefCell::new(None) };
    static CACHE_OBSERVATION: RefCell<Option<(Vec<u8>, bool)>> = const { RefCell::new(None) };
}

fn streaming_callback(
    scope: &mut v8::PinScope,
    source: v8::Local<v8::Value>,
    stream: v8::WasmStreaming<false>,
) {
    STREAM_SOURCE.with(|slot| {
        slot.borrow_mut().replace((
            source.to_rust_string_lossy(scope),
            source.is_string(),
            source.is_object(),
        ));
    });
    STREAM.with(|slot| assert!(slot.borrow_mut().replace(stream).is_none()));
}

fn caching_callback(interface: &mut v8::ModuleCachingInterface) {
    let wire = interface.get_wire_bytes().to_vec();
    let accepted = interface.set_cached_compiled_module_bytes(&[]);
    CACHE_OBSERVATION.with(|slot| {
        slot.borrow_mut().replace((wire, accepted));
    });
}

unsafe extern "C" fn compilation_caching_callback(interface: *mut v8::ModuleCachingInterface) {
    caching_callback(unsafe { &mut *interface });
}

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn promise_state(state: v8::PromiseState) -> &'static str {
    match state {
        v8::PromiseState::Pending => "Pending",
        v8::PromiseState::Fulfilled => "Fulfilled",
        v8::PromiseState::Rejected => "Rejected",
    }
}

fn bytes_json(bytes: &[u8]) -> Json {
    Json::arr(bytes.iter().map(|byte| Json::i(i64::from(*byte))).collect())
}

fn pump(scope: &mut v8::PinScope<'_, '_>) {
    while v8::Platform::pump_message_loop(&v8::V8::get_current_platform(), scope, false) {}
    scope.perform_microtask_checkpoint();
}

fn take_stream() -> v8::WasmStreaming<false> {
    STREAM.with(|slot| slot.borrow_mut().take().unwrap())
}

fn streaming_finish_and_url() -> Vec<CheckOutcome> {
    STREAM_SOURCE.with(|slot| *slot.borrow_mut() = None);
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_wasm_streaming_callback(streaming_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let promise_value = run_script(
        scope,
        "WebAssembly.compileStreaming('https://input.example/module.wasm')",
    );
    let promise: v8::Local<v8::Promise> = promise_value.try_into().unwrap();
    let source = STREAM_SOURCE.with(|slot| slot.borrow_mut().take().unwrap());
    let before_state = promise_state(promise.state());
    let pending_before = scope.has_pending_background_tasks();
    let mut stream = take_stream();
    stream.on_bytes_received(&[]);
    stream.on_bytes_received(&ANSWER_MODULE[..9]);
    stream.on_bytes_received(&ANSWER_MODULE[9..]);
    stream.set_url("https://compiled.example/chunked.wasm");
    let before_finish_state = promise_state(promise.state());
    stream.finish();
    let after_finish_state = promise_state(promise.state());
    let pending_after_finish = scope.has_pending_background_tasks();
    pump(scope);
    let after_pump_state = promise_state(promise.state());
    let result = promise.result(scope);
    let module: v8::Local<v8::WasmModuleObject> = result.try_into().unwrap();
    let key = v8::String::new(scope, "module").unwrap();
    context.global(scope).set(scope, key.into(), module.into());
    let answer = run_script(scope, "new WebAssembly.Instance(module).exports.run()")
        .integer_value(scope)
        .unwrap();
    let actual = Json::obj(vec![
        (
            "source",
            Json::obj(vec![
                ("text", Json::s(&source.0)),
                ("is_string", Json::b(source.1)),
                ("is_object", Json::b(source.2)),
            ]),
        ),
        ("promise_before", Json::s(before_state)),
        ("pending_before", Json::b(pending_before)),
        ("promise_before_finish", Json::s(before_finish_state)),
        ("promise_after_finish", Json::s(after_finish_state)),
        ("pending_after_finish", Json::b(pending_after_finish)),
        ("promise_after_pump", Json::s(after_pump_state)),
        (
            "result_is_wasm_module",
            Json::b(result.is_wasm_module_object()),
        ),
        (
            "wire_bytes",
            bytes_json(module.get_compiled_module().get_wire_bytes_ref()),
        ),
        (
            "source_url",
            Json::s(module.get_compiled_module().source_url()),
        ),
        ("executes_to", Json::i(answer)),
    ]);
    vec![pass("wasm/streaming_finish_and_url", actual)]
}

fn streaming_abort_and_drop() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_wasm_streaming_callback(streaming_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let promise: v8::Local<v8::Promise> =
        run_script(scope, "WebAssembly.compileStreaming('abort-with-value')")
            .try_into()
            .unwrap();
    let stream = take_stream();
    let exception: v8::Local<v8::Value> = v8::Object::new(scope).into();
    stream.abort(Some(exception));
    pump(scope);
    let with_value_state = promise_state(promise.state());
    let with_value_identity = promise.result(scope).strict_equals(exception);
    let pending_after_value = scope.has_pending_background_tasks();

    let promise_none: v8::Local<v8::Promise> =
        run_script(scope, "WebAssembly.compileStreaming('abort-without-value')")
            .try_into()
            .unwrap();
    take_stream().abort(None);
    pump(scope);
    let none_state = promise_state(promise_none.state());
    let pending_after_none = scope.has_pending_background_tasks();

    let promise_drop: v8::Local<v8::Promise> =
        run_script(scope, "WebAssembly.compileStreaming('drop-stream')")
            .try_into()
            .unwrap();
    drop(take_stream());
    pump(scope);
    let drop_state = promise_state(promise_drop.state());
    let pending_after_drop = scope.has_pending_background_tasks();

    let actual = Json::obj(vec![
        (
            "abort_with_exception",
            Json::obj(vec![
                ("state", Json::s(with_value_state)),
                ("same_exception", Json::b(with_value_identity)),
                ("pending_tasks", Json::b(pending_after_value)),
            ]),
        ),
        (
            "abort_without_exception",
            Json::obj(vec![
                ("state", Json::s(none_state)),
                ("pending_tasks", Json::b(pending_after_none)),
            ]),
        ),
        (
            "drop_without_finish",
            Json::obj(vec![
                ("state", Json::s(drop_state)),
                ("pending_tasks", Json::b(pending_after_drop)),
            ]),
        ),
    ]);
    vec![pass("wasm/streaming_abort_and_drop", actual)]
}

fn streaming_cache_rejection() -> Vec<CheckOutcome> {
    CACHE_OBSERVATION.with(|slot| *slot.borrow_mut() = None);
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_wasm_streaming_callback(streaming_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let promise: v8::Local<v8::Promise> =
        run_script(scope, "WebAssembly.compileStreaming('cached-module')")
            .try_into()
            .unwrap();
    let mut stream = take_stream().set_has_compiled_module_bytes();
    stream.on_bytes_received(EMPTY_MODULE);
    stream.finish(caching_callback);
    let callback = CACHE_OBSERVATION.with(|slot| slot.borrow_mut().take().unwrap());
    let after_finish = promise_state(promise.state());
    pump(scope);
    let after_pump = promise_state(promise.state());
    let result = promise.result(scope);
    let actual = Json::obj(vec![
        ("wire_bytes", bytes_json(&callback.0)),
        ("empty_cache_accepted", Json::b(callback.1)),
        ("state_after_finish", Json::s(after_finish)),
        ("state_after_pump", Json::s(after_pump)),
        ("result_text", Json::s(&result.to_rust_string_lossy(scope))),
        ("result_is_error", Json::b(result.is_native_error())),
        (
            "pending_tasks",
            Json::b(scope.has_pending_background_tasks()),
        ),
    ]);
    vec![pass("wasm/streaming_cache_rejection", actual)]
}

#[derive(Default)]
struct CompilationResult {
    calls: usize,
    ok: bool,
    wire: Vec<u8>,
    source_url: String,
    error: String,
    error_handle: Option<v8::Global<v8::Value>>,
}

fn pump_until(scope: &mut v8::PinScope<'_, '_>, result: &Rc<RefCell<CompilationResult>>) {
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(10);
    while std::time::Instant::now() < deadline {
        pump(scope);
        if result.borrow().calls != 0 {
            return;
        }
        std::thread::yield_now();
    }
    panic!("asynchronous Wasm compilation did not finish within 10 seconds");
}

fn finish_compilation(
    mut compilation: v8::WasmModuleCompilation,
    scope: &mut v8::PinScope<'_, '_>,
    bytes: &[u8],
    url: &str,
) -> Rc<RefCell<CompilationResult>> {
    compilation.on_bytes_received(&bytes[..bytes.len().min(3)]);
    compilation.on_bytes_received(&bytes[bytes.len().min(3)..]);
    compilation.set_url(url);
    let result = Rc::new(RefCell::new(CompilationResult::default()));
    let output = Rc::clone(&result);
    compilation.finish(scope, None, move |isolate, resolved| {
        let mut output = output.borrow_mut();
        output.calls += 1;
        match resolved {
            Ok(module) => {
                let compiled = module.get_compiled_module();
                output.ok = true;
                output.wire = compiled.get_wire_bytes_ref().to_vec();
                output.source_url = compiled.source_url().to_owned();
            }
            Err(error) => output.error_handle = Some(v8::Global::new(isolate, error)),
        }
    });
    result
}

fn materialize_error(scope: &v8::PinScope<'_, '_>, result: &Rc<RefCell<CompilationResult>>) {
    let mut result = result.borrow_mut();
    if let Some(error) = &result.error_handle {
        result.error = v8::Local::new(scope, error).to_rust_string_lossy(scope);
    }
}

fn compilation_json(result: &CompilationResult) -> Json {
    Json::obj(vec![
        ("callback_calls", Json::i(result.calls as i64)),
        ("ok", Json::b(result.ok)),
        ("wire_bytes", bytes_json(&result.wire)),
        ("source_url", Json::s(&result.source_url)),
        ("error", Json::s(&result.error)),
    ])
}

fn module_compilation_success_failure() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let success = finish_compilation(
        v8::WasmModuleCompilation::new(),
        scope,
        ANSWER_MODULE,
        "https://async.example/answer.wasm",
    );
    pump_until(scope, &success);
    materialize_error(scope, &success);
    let success_value = compilation_json(&success.borrow());

    let failure = finish_compilation(
        v8::WasmModuleCompilation::default(),
        scope,
        &[0, 1, 2, 3, 4, 5, 6, 7],
        "https://async.example/bad.wasm",
    );
    pump_until(scope, &failure);
    materialize_error(scope, &failure);
    let failure_value = compilation_json(&failure.borrow());

    let actual = Json::obj(vec![
        ("success", success_value),
        ("failure", failure_value),
        (
            "pending_tasks",
            Json::b(scope.has_pending_background_tasks()),
        ),
    ]);
    vec![pass("wasm/module_compilation_success_failure", actual)]
}

fn module_compilation_lifecycle() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let compilation = std::thread::spawn(|| {
        let mut compilation = v8::WasmModuleCompilation::new();
        compilation.on_bytes_received(&ANSWER_MODULE[..11]);
        compilation.on_bytes_received(&ANSWER_MODULE[11..]);
        compilation
    })
    .join()
    .unwrap();
    let cross_thread = finish_compilation(
        compilation,
        scope,
        &[],
        "https://async.example/cross-thread.wasm",
    );
    pump_until(scope, &cross_thread);
    materialize_error(scope, &cross_thread);

    CACHE_OBSERVATION.with(|slot| *slot.borrow_mut() = None);
    let mut cached = v8::WasmModuleCompilation::new();
    cached.set_has_compiled_module_bytes();
    cached.on_bytes_received(EMPTY_MODULE);
    let cached_result = Rc::new(RefCell::new(CompilationResult::default()));
    let cached_output = Rc::clone(&cached_result);
    cached.finish(
        scope,
        Some(compilation_caching_callback),
        move |isolate, resolved| {
            let mut output = cached_output.borrow_mut();
            output.calls += 1;
            match resolved {
                Ok(module) => {
                    let compiled = module.get_compiled_module();
                    output.ok = true;
                    output.wire = compiled.get_wire_bytes_ref().to_vec();
                    output.source_url = compiled.source_url().to_owned();
                }
                Err(error) => output.error_handle = Some(v8::Global::new(isolate, error)),
            }
        },
    );
    pump_until(scope, &cached_result);
    materialize_error(scope, &cached_result);
    let cache_observation = CACHE_OBSERVATION.with(|slot| slot.borrow_mut().take().unwrap());

    let serialization_calls = Arc::new(AtomicUsize::new(0));
    let calls = Arc::clone(&serialization_calls);
    let mut serialization = v8::WasmModuleCompilation::new();
    serialization.set_more_functions_can_be_serialized_callback(move |module| {
        assert_eq!(module.get_wire_bytes_ref(), ANSWER_MODULE);
        calls.fetch_add(1, Ordering::SeqCst);
    });
    let serialized = finish_compilation(
        serialization,
        scope,
        ANSWER_MODULE,
        "https://async.example/serialization.wasm",
    );
    pump_until(scope, &serialized);
    materialize_error(scope, &serialized);

    let abort_on_worker = std::thread::spawn(|| {
        let mut compilation = v8::WasmModuleCompilation::default();
        compilation.on_bytes_received(EMPTY_MODULE);
        compilation.abort();
        true
    })
    .join()
    .unwrap();
    drop(v8::WasmModuleCompilation::new());

    let actual = Json::obj(vec![
        ("cross_thread", compilation_json(&cross_thread.borrow())),
        (
            "cache_rejection",
            Json::obj(vec![
                ("wire_bytes", bytes_json(&cache_observation.0)),
                ("empty_cache_accepted", Json::b(cache_observation.1)),
                ("resolution", compilation_json(&cached_result.borrow())),
            ]),
        ),
        (
            "serialization",
            Json::obj(vec![
                ("resolution", compilation_json(&serialized.borrow())),
                (
                    "callback_calls",
                    Json::i(serialization_calls.load(Ordering::SeqCst) as i64),
                ),
            ]),
        ),
        ("abort_on_worker_completed", Json::b(abort_on_worker)),
        ("drop_unfinished_completed", Json::b(true)),
    ]);
    vec![pass("wasm/module_compilation_lifecycle", actual)]
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut checks = streaming_finish_and_url();
    checks.extend(streaming_abort_and_drop());
    checks.extend(streaming_cache_rejection());
    checks.extend(module_compilation_success_failure());
    checks.extend(module_compilation_lifecycle());
    let passed = checks.iter().filter(|check| check.passed()).count();
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
