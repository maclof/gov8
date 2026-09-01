//! Fatal WebAssembly API preconditions, invoked only by subprocess tests.

use std::cell::RefCell;

const EMPTY_MODULE: &[u8] = &[0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00];

thread_local! {
    static STREAM: RefCell<Option<v8::WasmStreaming<false>>> = const { RefCell::new(None) };
}

fn callback(
    _scope: &mut v8::PinScope,
    _source: v8::Local<v8::Value>,
    stream: v8::WasmStreaming<false>,
) {
    STREAM.with(|slot| slot.borrow_mut().replace(stream));
}

fn run_script(scope: &v8::PinScope<'_, '_>, source: &str) {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap();
}

fn streaming_cache_mark_after_bytes() {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_wasm_streaming_callback(callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    run_script(scope, "WebAssembly.compileStreaming('negative')");
    let mut stream = STREAM.with(|slot| slot.borrow_mut().take().unwrap());
    stream.on_bytes_received(EMPTY_MODULE);
    let _ = stream.set_has_compiled_module_bytes();
}

fn module_compilation_cache_mark_after_bytes() {
    let mut compilation = v8::WasmModuleCompilation::new();
    compilation.on_bytes_received(EMPTY_MODULE);
    compilation.set_has_compiled_module_bytes();
}

fn main() {
    oracle::ensure_v8();
    match std::env::args().nth(1).as_deref() {
        Some("streaming-cache-mark-after-bytes") => streaming_cache_mark_after_bytes(),
        Some("compilation-cache-mark-after-bytes") => module_compilation_cache_mark_after_bytes(),
        mode => panic!("unknown negative mode: {mode:?}"),
    }
}
