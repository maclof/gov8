//! Residual ValueSerializer/ValueDeserializer Wasm-transfer and legacy-wire oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. Existing serializer
//! fixtures cover default/None delegate results, SAB and ArrayBuffer transfer,
//! host objects, raw helpers, release ownership, and ordinary header version
//! 16. This slice exercises only real Wasm-module return and legacy control.

use std::cell::RefCell;
use std::rc::Rc;

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use v8::{ValueDeserializerHelper as _, ValueSerializerHelper as _};

const EMPTY_MODULE: &[u8] = &[0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00];
const ANSWER_MODULE: &[u8] = &[
    0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f, 0x03,
    0x02, 0x01, 0x00, 0x07, 0x07, 0x01, 0x03, b'r', b'u', b'n', 0x00, 0x00, 0x0a, 0x06, 0x01, 0x04,
    0x00, 0x41, 0x2a, 0x0b,
];

fn hex(bytes: &[u8]) -> String {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        output.push(DIGITS[(byte >> 4) as usize] as char);
        output.push(DIGITS[(byte & 0x0f) as usize] as char);
    }
    output
}

fn throw_clone_error(scope: &mut v8::PinScope<'_, '_>, message: v8::Local<'_, v8::String>) {
    let exception = v8::Exception::error(scope, message);
    scope.throw_exception(exception);
}

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

struct FixedWriter {
    id: u32,
    calls: Rc<RefCell<Vec<u32>>>,
}

impl v8::ValueSerializerImpl for FixedWriter {
    fn throw_data_clone_error<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        message: v8::Local<'s, v8::String>,
    ) {
        throw_clone_error(scope, message);
    }

    fn get_wasm_module_transfer_id(
        &self,
        _scope: &mut v8::PinScope<'_, '_>,
        module: v8::Local<v8::WasmModuleObject>,
    ) -> Option<u32> {
        self.calls
            .borrow_mut()
            .push(module.get_compiled_module().get_wire_bytes_ref().len() as u32);
        Some(self.id)
    }
}

struct CompiledReader {
    expected_id: u32,
    compiled: v8::CompiledWasmModule,
    ids: Rc<RefCell<Vec<u32>>>,
    returned: Rc<RefCell<Option<v8::Global<v8::WasmModuleObject>>>>,
}

impl v8::ValueDeserializerImpl for CompiledReader {
    fn get_wasm_module_from_id<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        id: u32,
    ) -> Option<v8::Local<'s, v8::WasmModuleObject>> {
        self.ids.borrow_mut().push(id);
        if id != self.expected_id {
            return None;
        }
        let module = v8::WasmModuleObject::from_compiled_module(scope, &self.compiled)?;
        *self.returned.borrow_mut() = Some(v8::Global::new(scope, module));
        Some(module)
    }
}

fn make_repeated_module_wire() -> (Vec<u8>, v8::CompiledWasmModule, Vec<u32>) {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = v8::WasmModuleObject::compile(scope, ANSWER_MODULE).unwrap();
    let compiled = module.get_compiled_module();
    let holder = v8::Object::new(scope);
    let a = v8::String::new(scope, "a").unwrap();
    let b = v8::String::new(scope, "b").unwrap();
    holder.set(scope, a.into(), module.into()).unwrap();
    holder.set(scope, b.into(), module.into()).unwrap();
    let calls = Rc::new(RefCell::new(Vec::new()));
    let serializer = v8::ValueSerializer::new(
        scope,
        Box::new(FixedWriter {
            id: 21,
            calls: Rc::clone(&calls),
        }),
    );
    serializer.write_header();
    let wrote = serializer.write_value(context, holder.into());
    assert_eq!(wrote, Some(true));
    let wire = serializer.release();
    drop(serializer);
    let callback_observations = calls.borrow().clone();
    (wire, compiled, callback_observations)
}

fn get_property<'s>(
    scope: &v8::PinScope<'s, '_>,
    object: v8::Local<'s, v8::Object>,
    key: &str,
) -> v8::Local<'s, v8::Value> {
    let key = v8::String::new(scope, key).unwrap();
    object.get(scope, key.into()).unwrap()
}

fn execute_module(
    scope: &v8::PinScope<'_, '_>,
    context: v8::Local<'_, v8::Context>,
    module: v8::Local<'_, v8::WasmModuleObject>,
) -> Option<i64> {
    let key = v8::String::new(scope, "transferredModule").unwrap();
    context
        .global(scope)
        .set(scope, key.into(), module.into())?;
    run_script(
        scope,
        "new WebAssembly.Instance(transferredModule).exports.run()",
    )?
    .integer_value(scope)
}

fn wasm_cross_isolate_rehydration() -> Vec<CheckOutcome> {
    let (wire, compiled, writer_observations) = make_repeated_module_wire();
    let ids = Rc::new(RefCell::new(Vec::new()));
    let returned = Rc::new(RefCell::new(None));

    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    v8::tc_scope!(let tc, scope);
    let deserializer = v8::ValueDeserializer::new(
        tc,
        Box::new(CompiledReader {
            expected_id: 21,
            compiled,
            ids: Rc::clone(&ids),
            returned: Rc::clone(&returned),
        }),
        &wire,
    );
    let header = deserializer.read_header(context);
    let version = deserializer.get_wire_format_version();
    let value = deserializer.read_value(context);
    drop(deserializer);
    let object = value.and_then(|value| value.try_cast::<v8::Object>().ok());
    let a = object
        .map(|object| get_property(tc, object, "a"))
        .and_then(|value| value.try_cast::<v8::WasmModuleObject>().ok());
    let b = object
        .map(|object| get_property(tc, object, "b"))
        .and_then(|value| value.try_cast::<v8::WasmModuleObject>().ok());
    let returned_global = returned.borrow_mut().take();
    let same_as_callback_return = match (a, returned_global.as_ref()) {
        (Some(a), Some(returned)) => a == v8::Local::new(tc, returned),
        _ => false,
    };
    let execution = a.and_then(|module| execute_module(tc, context, module));
    let bytes_equal =
        a.is_some_and(|module| module.get_compiled_module().get_wire_bytes_ref() == ANSWER_MODULE);
    let reader_ids = ids.borrow().clone();
    drop(returned_global);

    vec![pass(
        "serializer-wasm-legacy/wasm_cross_isolate_rehydration",
        Json::obj(vec![
            ("wire", Json::s(&hex(&wire))),
            (
                "writer_module_byte_lengths",
                Json::arr(
                    writer_observations
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
            ("header", header.map_or(Json::Null, Json::b)),
            ("version", Json::i(i64::from(version))),
            ("caught", Json::b(tc.has_caught())),
            (
                "reader_ids",
                Json::arr(
                    reader_ids
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
            ("a_is_module", Json::b(a.is_some())),
            ("b_is_module", Json::b(b.is_some())),
            (
                "repeated_identity",
                Json::b(matches!((a, b), (Some(a), Some(b)) if a == b)),
            ),
            ("same_as_callback_return", Json::b(same_as_callback_return)),
            ("wire_bytes_equal", Json::b(bytes_equal)),
            ("executes_to", execution.map_or(Json::Null, Json::i)),
        ]),
    )]
}

struct SequenceReader {
    expected_id: u32,
    modules: RefCell<Vec<v8::CompiledWasmModule>>,
    ids: Rc<RefCell<Vec<u32>>>,
}

impl v8::ValueDeserializerImpl for SequenceReader {
    fn get_wasm_module_from_id<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        id: u32,
    ) -> Option<v8::Local<'s, v8::WasmModuleObject>> {
        self.ids.borrow_mut().push(id);
        if id != self.expected_id {
            return None;
        }
        let modules = self.modules.borrow();
        let index = self.ids.borrow().len() - 1;
        let compiled = modules.get(index).or_else(|| modules.last())?;
        v8::WasmModuleObject::from_compiled_module(scope, compiled)
    }
}

fn make_collision_wire() -> (Vec<u8>, Vec<v8::CompiledWasmModule>, Vec<u32>) {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let answer = v8::WasmModuleObject::compile(scope, ANSWER_MODULE).unwrap();
    let empty = v8::WasmModuleObject::compile(scope, EMPTY_MODULE).unwrap();
    let compiled = vec![answer.get_compiled_module(), empty.get_compiled_module()];
    let holder = v8::Object::new(scope);
    holder
        .set(
            scope,
            v8::String::new(scope, "a").unwrap().into(),
            answer.into(),
        )
        .unwrap();
    holder
        .set(
            scope,
            v8::String::new(scope, "b").unwrap().into(),
            empty.into(),
        )
        .unwrap();
    let calls = Rc::new(RefCell::new(Vec::new()));
    let serializer = v8::ValueSerializer::new(
        scope,
        Box::new(FixedWriter {
            id: 7,
            calls: Rc::clone(&calls),
        }),
    );
    let wrote = serializer.write_value(context, holder.into());
    assert_eq!(wrote, Some(true));
    let wire = serializer.release();
    drop(serializer);
    let callback_observations = calls.borrow().clone();
    (wire, compiled, callback_observations)
}

fn wasm_repeated_id_replacement() -> Vec<CheckOutcome> {
    let (wire, modules, writer_observations) = make_collision_wire();
    let ids = Rc::new(RefCell::new(Vec::new()));
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    v8::tc_scope!(let tc, scope);
    let deserializer = v8::ValueDeserializer::new(
        tc,
        Box::new(SequenceReader {
            expected_id: 7,
            modules: RefCell::new(modules),
            ids: Rc::clone(&ids),
        }),
        &wire,
    );
    let value = deserializer.read_value(context);
    drop(deserializer);
    let object = value.and_then(|value| value.try_cast::<v8::Object>().ok());
    let a = object
        .map(|object| get_property(tc, object, "a"))
        .and_then(|value| value.try_cast::<v8::WasmModuleObject>().ok());
    let b = object
        .map(|object| get_property(tc, object, "b"))
        .and_then(|value| value.try_cast::<v8::WasmModuleObject>().ok());
    let a_answer = a.and_then(|module| execute_module(tc, context, module));
    let b_has_run = b.is_some_and(|module| {
        let key = v8::String::new(tc, "moduleB").unwrap();
        context
            .global(tc)
            .set(tc, key.into(), module.into())
            .unwrap();
        run_script(tc, "typeof new WebAssembly.Instance(moduleB).exports.run")
            .and_then(|value| value.to_string(tc))
            .is_some_and(|value| value.to_rust_string_lossy(tc) == "function")
    });
    let reader_ids = ids.borrow().clone();
    vec![pass(
        "serializer-wasm-legacy/wasm_repeated_id_replacement",
        Json::obj(vec![
            ("wire", Json::s(&hex(&wire))),
            (
                "writer_module_byte_lengths",
                Json::arr(
                    writer_observations
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
            (
                "reader_ids",
                Json::arr(
                    reader_ids
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
            ("caught", Json::b(tc.has_caught())),
            ("a_is_module", Json::b(a.is_some())),
            ("b_is_module", Json::b(b.is_some())),
            (
                "same_identity",
                Json::b(matches!((a, b), (Some(a), Some(b)) if a == b)),
            ),
            ("a_executes_to", a_answer.map_or(Json::Null, Json::i)),
            ("b_has_run_export", Json::b(b_has_run)),
        ]),
    )]
}

struct NoneReader {
    ids: Rc<RefCell<Vec<u32>>>,
}

impl v8::ValueDeserializerImpl for NoneReader {
    fn get_wasm_module_from_id<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        id: u32,
    ) -> Option<v8::Local<'s, v8::WasmModuleObject>> {
        self.ids.borrow_mut().push(id);
        None
    }
}

fn exception_text(tc: &v8::PinnedRef<'_, v8::TryCatch<'_, '_, v8::HandleScope<'_>>>) -> Json {
    tc.exception()
        .map_or(Json::Null, |value| Json::s(&value.to_rust_string_lossy(tc)))
}

fn wasm_max_id_and_none_failure() -> Vec<CheckOutcome> {
    let (compiled, max_wire, writer_observations) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let module = v8::WasmModuleObject::compile(scope, ANSWER_MODULE).unwrap();
        let compiled = module.get_compiled_module();
        let calls = Rc::new(RefCell::new(Vec::new()));
        let serializer = v8::ValueSerializer::new(
            scope,
            Box::new(FixedWriter {
                id: u32::MAX,
                calls: Rc::clone(&calls),
            }),
        );
        assert_eq!(serializer.write_value(context, module.into()), Some(true));
        let wire = serializer.release();
        drop(serializer);
        let observations = calls.borrow().clone();
        (compiled, wire, observations)
    };
    let success_ids = Rc::new(RefCell::new(Vec::new()));
    let returned = Rc::new(RefCell::new(None));
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let success = {
        v8::tc_scope!(let tc, scope);
        let deserializer = v8::ValueDeserializer::new(
            tc,
            Box::new(CompiledReader {
                expected_id: u32::MAX,
                compiled,
                ids: Rc::clone(&success_ids),
                returned: Rc::clone(&returned),
            }),
            &max_wire,
        );
        let value = deserializer.read_value(context);
        drop(deserializer);
        let is_module = value.is_some_and(|value| value.is_wasm_module_object());
        returned.borrow_mut().take();
        Json::obj(vec![
            ("wire", Json::s(&hex(&max_wire))),
            (
                "writer_module_byte_lengths",
                Json::arr(
                    writer_observations
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
            ("read_is_module", Json::b(is_module)),
            ("caught", Json::b(tc.has_caught())),
            (
                "ids",
                Json::arr(
                    success_ids
                        .borrow()
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
        ])
    };
    let failure_ids = Rc::new(RefCell::new(Vec::new()));
    let failure = {
        v8::tc_scope!(let tc, scope);
        let bytes = [0x77, 0x2a];
        let deserializer = v8::ValueDeserializer::new(
            tc,
            Box::new(NoneReader {
                ids: Rc::clone(&failure_ids),
            }),
            &bytes,
        );
        let value = deserializer.read_value(context);
        drop(deserializer);
        Json::obj(vec![
            ("read_none", Json::b(value.is_none())),
            ("caught", Json::b(tc.has_caught())),
            ("exception", exception_text(tc)),
            (
                "ids",
                Json::arr(
                    failure_ids
                        .borrow()
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
        ])
    };
    vec![pass(
        "serializer-wasm-legacy/wasm_max_id_and_none_failure",
        Json::obj(vec![("max_id_success", success), ("none_failure", failure)]),
    )]
}

struct Defaults;

impl v8::ValueDeserializerImpl for Defaults {}

fn legacy_case(bytes: &[u8], settings: &[bool]) -> Json {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    v8::tc_scope!(let tc, scope);
    let deserializer = v8::ValueDeserializer::new(tc, Box::new(Defaults), bytes);
    for setting in settings {
        deserializer.set_supports_legacy_wire_format(*setting);
    }
    let header = deserializer.read_header(context);
    let version = deserializer.get_wire_format_version();
    let value = if header == Some(true) {
        deserializer.read_value(context)
    } else {
        None
    };
    drop(deserializer);
    Json::obj(vec![
        ("header", header.map_or(Json::Null, Json::b)),
        ("version", Json::i(i64::from(version))),
        (
            "value_is_true",
            Json::b(value.is_some_and(|value| value.is_true())),
        ),
        ("caught", Json::b(tc.has_caught())),
        ("exception", exception_text(tc)),
    ])
}

fn legacy_wire_format_control() -> Vec<CheckOutcome> {
    vec![pass(
        "serializer-wasm-legacy/legacy_wire_format_control",
        Json::obj(vec![
            ("headerless_default", legacy_case(&[0x54], &[])),
            ("headerless_false", legacy_case(&[0x54], &[false])),
            ("headerless_true", legacy_case(&[0x54], &[true])),
            (
                "headerless_true_then_false",
                legacy_case(&[0x54], &[true, false]),
            ),
            (
                "headerless_false_then_true",
                legacy_case(&[0x54], &[false, true]),
            ),
            (
                "version12_false",
                legacy_case(&[0xff, 0x0c, 0x54], &[false]),
            ),
            ("version12_true", legacy_case(&[0xff, 0x0c, 0x54], &[true])),
            (
                "version13_false",
                legacy_case(&[0xff, 0x0d, 0x54], &[false]),
            ),
            ("version16_true", legacy_case(&[0xff, 0x10, 0x54], &[true])),
        ]),
    )]
}

struct PanicWriter;

impl v8::ValueSerializerImpl for PanicWriter {
    fn throw_data_clone_error<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        message: v8::Local<'s, v8::String>,
    ) {
        throw_clone_error(scope, message);
    }

    fn get_wasm_module_transfer_id(
        &self,
        _scope: &mut v8::PinScope<'_, '_>,
        _module: v8::Local<v8::WasmModuleObject>,
    ) -> Option<u32> {
        panic!("wasm transfer-id writer panic boundary")
    }
}

fn panic_writer_mode() {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = v8::WasmModuleObject::compile(scope, EMPTY_MODULE).unwrap();
    let serializer = v8::ValueSerializer::new(scope, Box::new(PanicWriter));
    let _ = serializer.write_value(context, module.into());
}

const CHECKS: &[fn() -> Vec<CheckOutcome>] = &[
    wasm_cross_isolate_rehydration,
    wasm_repeated_id_replacement,
    wasm_max_id_and_none_failure,
    legacy_wire_format_control,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    if std::env::args().nth(1).as_deref() == Some("mode=panic-wasm-writer") {
        panic_writer_mode();
        return std::process::ExitCode::FAILURE;
    }
    let outcomes: Vec<CheckOutcome> = CHECKS.iter().flat_map(|check| check()).collect();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!(
        "{}",
        summary_line(outcomes.len(), passed, outcomes.len() - passed)
    );
    if passed == outcomes.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
