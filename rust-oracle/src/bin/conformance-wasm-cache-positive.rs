//! Positive serialized WebAssembly cache oracle.
//!
//! rusty_v8 152.2.0 omits the public C++ `CompiledWasmModule::Serialize`
//! method from its Rust wrapper. This executable uses a deliberately narrow,
//! pinned MSVC ABI bridge to that exported V8 method solely to produce oracle
//! cache bytes. Consumers still use the safe public rusty_v8 caching API.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::cell::RefCell;
use std::ffi::c_void;
use std::rc::Rc;

const ANSWER_MODULE: &[u8] = &[
    0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f, 0x03,
    0x02, 0x01, 0x00, 0x07, 0x07, 0x01, 0x03, b'r', b'u', b'n', 0x00, 0x00, 0x0a, 0x06, 0x01, 0x04,
    0x00, 0x41, 0x2a, 0x0b,
];
const EMPTY_MODULE: &[u8] = &[0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00];
const SEM_FAILCRITICALERRORS: u32 = 0x0001;
const SEM_NOGPFAULTERRORBOX: u32 = 0x0002;
const SEM_NOOPENFILEERRORBOX: u32 = 0x8000;

#[link(name = "kernel32")]
unsafe extern "system" {
    #[link_name = "SetErrorMode"]
    fn set_error_mode(mode: u32) -> u32;
}

thread_local! {
    static STREAM: RefCell<Option<v8::WasmStreaming<false>>> = const { RefCell::new(None) };
    static CACHE_BYTES: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
    static CACHE_OBSERVATION: RefCell<Option<(Vec<u8>, bool)>> = const { RefCell::new(None) };
}

unsafe extern "C" {
    fn gov8_oracle_compiled_wasm_module_serialize(
        compiled_module: *mut c_void,
        output: *mut *mut u8,
        output_size: *mut usize,
    ) -> bool;
    fn gov8_oracle_serialized_wasm_module_free(bytes: *mut u8);
}

fn serialize(compiled: &v8::CompiledWasmModule) -> Vec<u8> {
    // Pinned rusty_v8 represents CompiledWasmModule as exactly one pointer to
    // a heap-allocated v8::CompiledWasmModule (src/wasm.rs:244, 249).
    let raw = unsafe { *(std::ptr::from_ref(compiled).cast::<*mut c_void>()) };
    let mut output = std::ptr::null_mut();
    let mut output_size = 0;
    let ok =
        unsafe { gov8_oracle_compiled_wasm_module_serialize(raw, &mut output, &mut output_size) };
    assert!(ok);
    assert!(!output.is_null());
    let bytes = unsafe { std::slice::from_raw_parts(output, output_size) }.to_vec();
    unsafe { gov8_oracle_serialized_wasm_module_free(output) };
    bytes
}

fn suppress_windows_fatal_dialogs() {
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

fn streaming_callback(
    _scope: &mut v8::PinScope,
    _source: v8::Local<v8::Value>,
    stream: v8::WasmStreaming<false>,
) {
    STREAM.with(|slot| assert!(slot.borrow_mut().replace(stream).is_none()));
}

fn caching_callback(interface: &mut v8::ModuleCachingInterface) {
    let wire = interface.get_wire_bytes().to_vec();
    let cache = CACHE_BYTES.with_borrow(Clone::clone);
    let accepted = interface.set_cached_compiled_module_bytes(&cache);
    CACHE_OBSERVATION.with(|slot| {
        assert!(slot.borrow_mut().replace((wire, accepted)).is_none());
    });
}

unsafe extern "C" fn compilation_caching_callback(interface: *mut v8::ModuleCachingInterface) {
    caching_callback(unsafe { &mut *interface });
}

fn double_set_callback(interface: &mut v8::ModuleCachingInterface) {
    let cache = CACHE_BYTES.with_borrow(Clone::clone);
    let first = interface.set_cached_compiled_module_bytes(&cache);
    eprintln!("marker:first-set:accepted={first}");
    let _ = interface.set_cached_compiled_module_bytes(&cache);
    eprintln!("marker:after-second-set");
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

fn pump_until_settled(scope: &mut v8::PinScope<'_, '_>, promise: v8::Local<v8::Promise>) {
    for _ in 0..10_000 {
        let _ = v8::Platform::pump_message_loop(&v8::V8::get_current_platform(), scope, false);
        scope.perform_microtask_checkpoint();
        if promise.state() != v8::PromiseState::Pending {
            return;
        }
        std::thread::yield_now();
    }
    panic!("WebAssembly cache promise did not settle within 10000 pumps");
}

fn take_stream() -> v8::WasmStreaming<false> {
    STREAM.with(|slot| slot.borrow_mut().take().unwrap())
}

fn set_cache(bytes: &[u8]) {
    CACHE_BYTES.with_borrow_mut(|slot| {
        slot.clear();
        slot.extend_from_slice(bytes);
    });
    CACHE_OBSERVATION.with(|slot| *slot.borrow_mut() = None);
}

fn produce_cache() -> (Vec<u8>, bool) {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = v8::WasmModuleObject::compile(scope, ANSWER_MODULE).unwrap();
    let compiled = module.get_compiled_module();
    let first = serialize(&compiled);
    let repeated = serialize(&compiled);
    (first.clone(), first == repeated)
}

fn producer_determinism(cache: &[u8], repeated_equal: bool) -> CheckOutcome {
    let (independent, independent_repeated_equal) = produce_cache();
    // Serialized Wasm code contains CPU-feature-dependent bytes. Record the
    // portable API guarantees rather than a hash tied to this processor.
    pass(
        "wasm-cache-positive/producer/determinism",
        Json::obj(vec![
            ("serialized_nonempty", Json::b(!cache.is_empty())),
            ("repeat_same_compiled_equal", Json::b(repeated_equal)),
            (
                "independent_producer_repeat_equal",
                Json::b(independent_repeated_equal),
            ),
            (
                "independent_isolate_bytes_equal",
                Json::b(cache == independent),
            ),
        ]),
    )
}

fn streaming_attempt(wire: &[u8], cache: &[u8], url: &str) -> Json {
    STREAM.with(|slot| *slot.borrow_mut() = None);
    set_cache(cache);
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_wasm_streaming_callback(streaming_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let promise: v8::Local<v8::Promise> = run_script(
        scope,
        "WebAssembly.compileStreaming('https://cache-input.example/module.wasm')",
    )
    .try_into()
    .unwrap();
    let mut stream = take_stream().set_has_compiled_module_bytes();
    stream.on_bytes_received(&wire[..wire.len().min(5)]);
    stream.on_bytes_received(&wire[wire.len().min(5)..]);
    stream.set_url(url);
    stream.finish(caching_callback);
    let after_finish = promise_state(promise.state());
    pump_until_settled(scope, promise);
    let after_pump = promise_state(promise.state());
    let observation = CACHE_OBSERVATION.with(|slot| slot.borrow_mut().take().unwrap());
    let result = promise.result(scope);
    let module = result.try_cast::<v8::WasmModuleObject>();
    let result_is_module = module.is_ok();
    let (executes_to, reserialized_equal, restored_distinct, restored_executes_to, source_url) =
        if let Ok(module) = module {
            let compiled = module.get_compiled_module();
            let reserialized_equal = serialize(&compiled) == cache;
            let source_url = compiled.source_url().to_owned();
            let restored = v8::WasmModuleObject::from_compiled_module(scope, &compiled).unwrap();
            let restored_distinct = !module.strict_equals(restored.into());
            context.global(scope).set(
                scope,
                v8::String::new(scope, "cachedModule").unwrap().into(),
                module.into(),
            );
            context.global(scope).set(
                scope,
                v8::String::new(scope, "restoredModule").unwrap().into(),
                restored.into(),
            );
            let execute = |name| {
                run_script(
                    scope,
                    &format!("new WebAssembly.Instance({name}).exports.run?.()"),
                )
                .integer_value(scope)
            };
            (
                execute("cachedModule"),
                reserialized_equal,
                restored_distinct,
                execute("restoredModule"),
                source_url,
            )
        } else {
            (None, false, false, None, String::new())
        };
    Json::obj(vec![
        ("callback_wire_matches", Json::b(observation.0 == wire)),
        ("cache_accepted", Json::b(observation.1)),
        ("state_after_finish", Json::s(after_finish)),
        ("state_after_pump", Json::s(after_pump)),
        ("result_is_module", Json::b(result_is_module)),
        ("executes_to", executes_to.map_or(Json::Null, Json::i)),
        ("reserialized_equals_input", Json::b(reserialized_equal)),
        ("restored_object_distinct", Json::b(restored_distinct)),
        (
            "restored_executes_to",
            restored_executes_to.map_or(Json::Null, Json::i),
        ),
        ("source_url", Json::s(&source_url)),
    ])
}

fn streaming_acceptance(cache: &[u8]) -> CheckOutcome {
    pass(
        "wasm-cache-positive/streaming/accepted_cross_isolate",
        streaming_attempt(ANSWER_MODULE, cache, "https://cache.example/accepted.wasm"),
    )
}

fn streaming_rejections(cache: &[u8]) -> CheckOutcome {
    let mut header_corrupted = cache.to_vec();
    header_corrupted[0] ^= 0x5a;
    let mut tail_corrupted = cache.to_vec();
    let last = tail_corrupted.len() - 1;
    tail_corrupted[last] ^= 0x5a;
    pass(
        "wasm-cache-positive/streaming/rejection_fallback",
        Json::obj(vec![
            (
                "header_corruption",
                streaming_attempt(
                    ANSWER_MODULE,
                    &header_corrupted,
                    "https://cache.example/header-corruption.wasm",
                ),
            ),
            (
                "tail_corruption",
                streaming_attempt(
                    ANSWER_MODULE,
                    &tail_corrupted,
                    "https://cache.example/tail-corruption.wasm",
                ),
            ),
        ]),
    )
}

#[derive(Default)]
struct CompilationResult {
    calls: usize,
    module: Option<v8::Global<v8::WasmModuleObject>>,
    error: Option<v8::Global<v8::Value>>,
}

fn module_compilation_acceptance(cache: &[u8]) -> CheckOutcome {
    set_cache(cache);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let mut compilation = v8::WasmModuleCompilation::new();
    compilation.set_has_compiled_module_bytes();
    compilation.on_bytes_received(ANSWER_MODULE);
    compilation.set_url("https://cache.example/module-compilation.wasm");
    let result = Rc::new(RefCell::new(CompilationResult::default()));
    let callback_result = Rc::clone(&result);
    compilation.finish(
        scope,
        Some(compilation_caching_callback),
        move |isolate, resolved| {
            let mut result = callback_result.borrow_mut();
            result.calls += 1;
            match resolved {
                Ok(module) => result.module = Some(v8::Global::new(isolate, module)),
                Err(error) => result.error = Some(v8::Global::new(isolate, error)),
            }
        },
    );
    for _ in 0..10_000 {
        let _ = v8::Platform::pump_message_loop(&v8::V8::get_current_platform(), scope, false);
        scope.perform_microtask_checkpoint();
        if result.borrow().calls != 0 {
            break;
        }
        std::thread::yield_now();
    }
    assert_eq!(
        result.borrow().calls,
        1,
        "module compilation callback timeout"
    );
    let observation = CACHE_OBSERVATION.with(|slot| slot.borrow_mut().take().unwrap());
    let result = result.borrow();
    let module = result
        .module
        .as_ref()
        .map(|module| v8::Local::new(scope, module));
    let (executes_to, serialized_equal, source_url) =
        module.map_or((None, false, String::new()), |module| {
            context.global(scope).set(
                scope,
                v8::String::new(scope, "compiledModule").unwrap().into(),
                module.into(),
            );
            let compiled = module.get_compiled_module();
            (
                run_script(
                    scope,
                    "new WebAssembly.Instance(compiledModule).exports.run()",
                )
                .integer_value(scope),
                serialize(&compiled) == cache,
                compiled.source_url().to_owned(),
            )
        });
    pass(
        "wasm-cache-positive/module_compilation/accepted",
        Json::obj(vec![
            (
                "callback_wire_matches",
                Json::b(observation.0 == ANSWER_MODULE),
            ),
            ("cache_accepted", Json::b(observation.1)),
            ("resolution_calls", Json::i(result.calls as i64)),
            ("resolved_module", Json::b(module.is_some())),
            ("resolved_error", Json::b(result.error.is_some())),
            ("executes_to", executes_to.map_or(Json::Null, Json::i)),
            ("reserialized_equals_input", Json::b(serialized_equal)),
            ("source_url", Json::s(&source_url)),
        ]),
    )
}

fn negative_double_set() {
    suppress_windows_fatal_dialogs();
    v8::V8::set_flags_from_string("--no-liftoff --no-wasm-lazy-compilation");
    oracle::ensure_v8();
    let (cache, _) = produce_cache();
    set_cache(&cache);
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_wasm_streaming_callback(streaming_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _: v8::Local<v8::Promise> = run_script(
        scope,
        "WebAssembly.compileStreaming('https://cache.example/double-set.wasm')",
    )
    .try_into()
    .unwrap();
    let mut stream = take_stream().set_has_compiled_module_bytes();
    stream.on_bytes_received(ANSWER_MODULE);
    eprintln!("marker:before-finish");
    stream.finish(double_set_callback);
    eprintln!("marker:after-finish");
}

fn rejection_probe(mode: &str) {
    suppress_windows_fatal_dialogs();
    v8::V8::set_flags_from_string("--no-liftoff --no-wasm-lazy-compilation");
    oracle::ensure_v8();
    let (cache, _) = produce_cache();
    let (wire, candidate) = match mode {
        "mismatched-wire" => (EMPTY_MODULE, cache.as_slice()),
        "truncated-cache" => (ANSWER_MODULE, &cache[..cache.len() - 1]),
        "corrupted-cache" => {
            let last = cache.len() - 1;
            CACHE_BYTES.with_borrow_mut(|bytes| {
                bytes.clear();
                bytes.extend_from_slice(&cache);
                bytes[last] ^= 0x5a;
            });
            let corrupted = CACHE_BYTES.with_borrow(Clone::clone);
            eprintln!("marker:before-attempt:{mode}");
            let result = streaming_attempt(
                ANSWER_MODULE,
                &corrupted,
                "https://cache.example/corrupted.wasm",
            );
            eprintln!("marker:after-attempt:{mode}");
            println!("{}", result.to_json_string());
            return;
        }
        _ => panic!("unknown rejection probe: {mode}"),
    };
    eprintln!("marker:before-attempt:{mode}");
    let result = streaming_attempt(wire, candidate, "https://cache.example/rejection.wasm");
    eprintln!("marker:after-attempt:{mode}");
    println!("{}", result.to_json_string());
}

fn run_fixture() {
    // Native-module serialization requires at least one TurboFan-compiled
    // function; the default Liftoff-only snapshot is deliberately rejected.
    v8::V8::set_flags_from_string("--no-liftoff --no-wasm-lazy-compilation");
    oracle::ensure_v8();
    let (cache, repeated_equal) = produce_cache();
    let checks = [
        producer_determinism(&cache, repeated_equal),
        streaming_acceptance(&cache),
        streaming_rejections(&cache),
        module_compilation_acceptance(&cache),
    ];
    for check in &checks {
        println!("{}", check.to_line());
    }
    println!("{}", summary_line(checks.len(), checks.len(), 0));
}

fn main() {
    let args: Vec<_> = std::env::args().collect();
    if args.len() == 2 && args[1] == "--negative-double-set" {
        negative_double_set();
    } else if args.len() == 3 && args[1] == "--rejection-probe" {
        rejection_probe(&args[2]);
    } else {
        assert_eq!(
            args.len(),
            1,
            "usage: conformance-wasm-cache-positive [--negative-double-set] [--rejection-probe MODE]"
        );
        run_fixture();
    }
}
