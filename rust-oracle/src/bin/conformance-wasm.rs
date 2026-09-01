//! Public WebAssembly compile/stream/cache conformance for `v8` 152.2.0.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

const EMPTY_MODULE: &[u8] = &[0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00];
const ANSWER_MODULE: &[u8] = &[
    0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // header
    0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f, // () -> i32
    0x03, 0x02, 0x01, 0x00, // one function of type zero
    0x07, 0x07, 0x01, 0x03, b'r', b'u', b'n', 0x00, 0x00, // export run
    0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b, // return 42
];

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

fn bytes_json(bytes: &[u8]) -> Json {
    Json::arr(bytes.iter().map(|byte| Json::i(i64::from(*byte))).collect())
}

fn compile_failure(scope: &mut v8::PinScope<'_, '_>, bytes: &[u8]) -> Json {
    v8::tc_scope!(let tc, scope);
    let module = v8::WasmModuleObject::compile(tc, bytes);
    let exception = tc.exception();
    Json::obj(vec![
        ("module_none", Json::b(module.is_none())),
        ("caught", Json::b(tc.has_caught())),
        (
            "exception",
            exception.map_or(Json::Null, |value| Json::s(&value.to_rust_string_lossy(tc))),
        ),
        (
            "is_native_error",
            Json::b(exception.is_some_and(|value| value.is_native_error())),
        ),
    ])
}

fn compiled_survives_producer_isolate() -> Json {
    let compiled = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        v8::WasmModuleObject::compile(scope, ANSWER_MODULE)
            .unwrap()
            .get_compiled_module()
    };
    let bytes_after_drop = compiled.get_wire_bytes_ref() == ANSWER_MODULE;
    let source_after_drop = compiled.source_url().to_owned();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = v8::WasmModuleObject::from_compiled_module(scope, &compiled);
    let works = module.is_some_and(|module| {
        let key = v8::String::new(scope, "module").unwrap();
        context.global(scope).set(scope, key.into(), module.into());
        run_script(scope, "new WebAssembly.Instance(module).exports.run()")
            .and_then(|value| value.integer_value(scope))
            == Some(42)
    });
    Json::obj(vec![
        ("wire_bytes_equal", Json::b(bytes_after_drop)),
        ("source_url", Json::s(&source_after_drop)),
        ("from_compiled_some", Json::b(module.is_some())),
        ("executes", Json::b(works)),
    ])
}

fn sync_compile_and_compiled_module() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let empty = v8::WasmModuleObject::compile(scope, EMPTY_MODULE).unwrap();
    let module = v8::WasmModuleObject::compile(scope, ANSWER_MODULE).unwrap();
    let module_value: v8::Local<v8::Value> = module.into();
    let compiled_a = module.get_compiled_module();
    let compiled_b = module.get_compiled_module();
    let restored_a = v8::WasmModuleObject::from_compiled_module(scope, &compiled_a).unwrap();
    let restored_b = v8::WasmModuleObject::from_compiled_module(scope, &compiled_a).unwrap();
    let key = v8::String::new(scope, "module").unwrap();
    context.global(scope).set(scope, key.into(), module_value);
    let answer = run_script(scope, "new WebAssembly.Instance(module).exports.run()")
        .and_then(|value| value.integer_value(scope));
    let empty_compiled = empty.get_compiled_module();
    let invalid_empty = compile_failure(scope, &[]);
    let invalid_magic = compile_failure(scope, &[0, 1, 2, 3, 4, 5, 6, 7]);
    let mut trailing = EMPTY_MODULE.to_vec();
    trailing.push(0xff);
    let invalid_trailing = compile_failure(scope, &trailing);
    let actual = Json::obj(vec![
        (
            "empty_module_is_wasm",
            Json::b(empty.is_wasm_module_object()),
        ),
        (
            "module_predicates",
            Json::obj(vec![
                (
                    "is_wasm_module",
                    Json::b(module_value.is_wasm_module_object()),
                ),
                ("is_object", Json::b(module_value.is_object())),
            ]),
        ),
        ("executes_to", answer.map_or(Json::Null, Json::i)),
        (
            "compiled",
            Json::obj(vec![
                ("wire_bytes", bytes_json(compiled_a.get_wire_bytes_ref())),
                (
                    "wire_bytes_repeat_equal",
                    Json::b(compiled_a.get_wire_bytes_ref() == compiled_b.get_wire_bytes_ref()),
                ),
                ("source_url", Json::s(compiled_a.source_url())),
                ("source_url_repeat", Json::s(compiled_a.source_url())),
                (
                    "empty_wire_bytes",
                    bytes_json(empty_compiled.get_wire_bytes_ref()),
                ),
            ]),
        ),
        (
            "restored",
            Json::obj(vec![
                ("a_distinct_from_original", Json::b(restored_a != module)),
                ("b_distinct_from_a", Json::b(restored_b != restored_a)),
                (
                    "wire_bytes_equal",
                    Json::b(restored_a.get_compiled_module().get_wire_bytes_ref() == ANSWER_MODULE),
                ),
            ]),
        ),
        ("invalid_empty", invalid_empty),
        ("invalid_magic", invalid_magic),
        ("invalid_trailing", invalid_trailing),
        (
            "producer_isolate_lifetime",
            compiled_survives_producer_isolate(),
        ),
    ]);
    vec![pass("wasm/sync_compile_and_compiled_module", actual)]
}

fn memory_buffer() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let value = run_script(
        scope,
        "globalThis.memory = new WebAssembly.Memory({initial:1, maximum:2}); memory",
    )
    .unwrap();
    let memory: v8::Local<v8::WasmMemoryObject> = value.try_into().unwrap();
    let before_a = memory.buffer();
    let before_b = memory.buffer();
    let before_bytes = before_a.byte_length();
    let before_detached = before_a.was_detached();
    let grow_result = run_script(scope, "memory.grow(1)")
        .and_then(|value| value.integer_value(scope))
        .unwrap();
    let after_a = memory.buffer();
    let after_b = memory.buffer();
    let actual = Json::obj(vec![
        ("is_wasm_memory", Json::b(value.is_wasm_memory_object())),
        ("is_object", Json::b(value.is_object())),
        ("before_bytes", Json::i(before_bytes as i64)),
        ("before_detached", Json::b(before_detached)),
        ("before_repeat_same", Json::b(before_a == before_b)),
        ("grow_return", Json::i(grow_result)),
        ("after_bytes", Json::i(after_a.byte_length() as i64)),
        ("after_repeat_same", Json::b(after_a == after_b)),
        ("buffer_replaced", Json::b(after_a != before_a)),
        (
            "old_bytes_after_grow",
            Json::i(before_a.byte_length() as i64),
        ),
        ("old_detached", Json::b(before_a.was_detached())),
    ]);
    vec![pass("wasm/memory_buffer", actual)]
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut checks = sync_compile_and_compiled_module();
    checks.extend(memory_buffer());
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
