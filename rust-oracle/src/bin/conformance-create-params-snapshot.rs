//! Unified `CreateParams`/snapshot conformance for pinned `v8` =152.2.0.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::borrow::Cow;
use std::collections::HashMap;
use std::ffi::{c_char, c_void, CStr};
use std::sync::{LazyLock, Mutex};
use v8::MapFnTo;

const MIB: usize = 1024 * 1024;

static COUNTERS: LazyLock<Mutex<HashMap<String, usize>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));

unsafe extern "C" fn counter_lookup(name: *const c_char) -> *mut i32 {
    let name = unsafe { CStr::from_ptr(name) }
        .to_string_lossy()
        .into_owned();
    let mut counters = COUNTERS.lock().expect("counter map poisoned");
    let address = counters
        .entry(name)
        .or_insert_with(|| Box::into_raw(Box::new(0_i32)) as usize);
    *address as *mut i32
}

fn external_callback(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let pointer = args.data().cast::<v8::External>().value() as usize;
    rv.set(v8::BigInt::new_from_u64(scope, pointer as u64).into());
}

fn external_refs(pointer: usize) -> Cow<'static, [v8::ExternalReference]> {
    vec![
        v8::ExternalReference {
            function: external_callback.map_fn_to(),
        },
        v8::ExternalReference {
            pointer: pointer as *mut c_void,
        },
    ]
    .into()
}

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

fn eval_text(scope: &v8::PinScope<'_, '_>, source: &str) -> Option<String> {
    eval(scope, source).map(|value| value.to_rust_string_lossy(scope))
}

fn plain_blob() -> v8::StartupData {
    plain_blob_with_marker(21)
}

fn plain_blob_with_marker(marker: i32) -> v8::StartupData {
    let mut creator = v8::Isolate::snapshot_creator(None, None);
    {
        v8::scope!(let scope, &mut creator);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval(scope, &format!("globalThis.snapshotMarker = {marker};")).unwrap();
        scope.set_default_context(context);
    }
    creator.create_blob(v8::FunctionCodeHandling::Keep).unwrap()
}

fn external_blob() -> v8::StartupData {
    let mut creator = v8::Isolate::snapshot_creator(Some(external_refs(1)), None);
    {
        v8::scope!(let scope, &mut creator);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval(scope, "globalThis.snapshotMarker = 21;").unwrap();
        let data = v8::External::new(scope, std::ptr::dangling_mut::<c_void>());
        let template = v8::FunctionTemplate::builder(external_callback)
            .data(data.into())
            .build(scope);
        let function = template.get_function(scope).unwrap();
        let key = v8::String::new(scope, "externalValue").unwrap().into();
        context
            .global(scope)
            .set(scope, key, function.into())
            .unwrap();
        scope.set_default_context(context);
    }
    creator.create_blob(v8::FunctionCodeHandling::Keep).unwrap()
}

fn consume_marker(params: v8::CreateParams) -> (i64, usize) {
    let isolate = &mut v8::Isolate::new(params);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let marker = eval(scope, "snapshotMarker")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let buffer_len = v8::ArrayBuffer::new(scope, 17).byte_length();
    (marker, buffer_len)
}

fn snapshot_independent_and_allocator() -> Vec<CheckOutcome> {
    let produced = plain_blob();
    let valid = produced.is_valid();
    let owned_copy = produced.clone();
    drop(produced);

    let implicit = v8::CreateParams::default().snapshot_blob(owned_copy.clone());
    let implicit_reports_unset = !implicit.has_set_array_buffer_allocator();
    let (implicit_marker, implicit_buffer_len) = consume_marker(implicit);

    let explicit = v8::CreateParams::default()
        .snapshot_blob(owned_copy)
        .array_buffer_allocator(v8::new_default_allocator().make_shared());
    let explicit_reports_set = explicit.has_set_array_buffer_allocator();
    let (explicit_marker, explicit_buffer_len) = consume_marker(explicit);

    let replaced_marker = consume_marker(
        v8::CreateParams::default()
            .snapshot_blob(plain_blob_with_marker(11))
            .snapshot_blob(plain_blob_with_marker(22)),
    )
    .0;

    vec![pass(
        "create-params-snapshot/independent_allocator_lifetime",
        Json::obj(vec![
            ("blob_valid", Json::b(valid)),
            ("producer_and_original_dropped", Json::b(true)),
            (
                "implicit_reports_unset_before_finalize",
                Json::b(implicit_reports_unset),
            ),
            ("implicit_marker", Json::i(implicit_marker)),
            (
                "implicit_array_buffer_length",
                Json::i(implicit_buffer_len as i64),
            ),
            ("explicit_reports_set", Json::b(explicit_reports_set)),
            ("explicit_marker", Json::i(explicit_marker)),
            (
                "explicit_array_buffer_length",
                Json::i(explicit_buffer_len as i64),
            ),
            ("second_snapshot_replaces_first", Json::i(replaced_marker)),
        ]),
    )]
}

fn snapshot_atomics_wait() -> Vec<CheckOutcome> {
    let blob = plain_blob();
    let observe = |allowed| {
        let params = v8::CreateParams::default()
            .snapshot_blob(blob.clone())
            .allow_atomics_wait(allowed);
        let isolate = &mut v8::Isolate::new(params);
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let marker = eval(scope, "snapshotMarker")
            .unwrap()
            .integer_value(scope)
            .unwrap();
        v8::tc_scope!(let tc, scope);
        let result = eval_text(
            tc,
            "Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,1)",
        );
        let exception = tc.exception().map(|value| value.to_rust_string_lossy(tc));
        Json::obj(vec![
            ("marker", Json::i(marker)),
            ("result", result.as_deref().map_or(Json::Null, Json::s)),
            ("caught", Json::b(tc.has_caught())),
            (
                "exception",
                exception.as_deref().map_or(Json::Null, Json::s),
            ),
        ])
    };
    vec![pass(
        "create-params-snapshot/atomics_wait_combination",
        Json::obj(vec![
            ("disabled", observe(false)),
            ("enabled", observe(true)),
        ]),
    )]
}

fn unified_all_safe_parameters() -> Vec<CheckOutcome> {
    COUNTERS.lock().expect("counter map poisoned").clear();
    let blob = external_blob();
    let params = v8::CreateParams::default()
        .snapshot_blob(blob)
        .allow_atomics_wait(false)
        .heap_limits(8 * MIB, 64 * MIB)
        .counter_lookup_callback(counter_lookup)
        .external_references(external_refs(23))
        .array_buffer_allocator(v8::new_default_allocator().make_shared());
    let limits = (
        params.max_old_generation_size_in_bytes(),
        params.max_young_generation_size_in_bytes(),
        params.initial_old_generation_size_in_bytes(),
        params.initial_young_generation_size_in_bytes(),
        params.code_range_size_in_bytes(),
    );
    let allocator_set = params.has_set_array_buffer_allocator();
    let (marker, external_value, array_buffer_length, atomics_none, atomics_exception) = {
        let isolate = &mut v8::Isolate::new(params);
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let marker = eval(scope, "snapshotMarker")
            .unwrap()
            .integer_value(scope)
            .unwrap();
        let external_value = eval_text(scope, "externalValue()").unwrap();
        let array_buffer_length = v8::ArrayBuffer::new(scope, 19).byte_length();
        v8::tc_scope!(let tc, scope);
        let atomics = eval(
            tc,
            "Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,1)",
        );
        let exception = tc
            .exception()
            .map_or(String::new(), |value| value.to_rust_string_lossy(tc));
        (
            marker,
            external_value,
            array_buffer_length,
            atomics.is_none(),
            exception,
        )
    };
    let counters = COUNTERS.lock().expect("counter map poisoned");
    vec![pass(
        "create-params-snapshot/all_safe_parameters",
        Json::obj(vec![
            ("max_old", Json::i(limits.0 as i64)),
            ("max_young", Json::i(limits.1 as i64)),
            ("initial_old", Json::i(limits.2 as i64)),
            ("initial_young", Json::i(limits.3 as i64)),
            ("code_range", Json::i(limits.4 as i64)),
            ("allocator_set", Json::b(allocator_set)),
            ("marker", Json::i(marker)),
            ("external_value", Json::s(&external_value)),
            ("array_buffer_length", Json::i(array_buffer_length as i64)),
            ("atomics_none", Json::b(atomics_none)),
            ("atomics_exception", Json::s(&atomics_exception)),
            ("counter_names_observed", Json::b(!counters.is_empty())),
        ]),
    )]
}

fn constraint_boundaries_without_isolate() -> Vec<CheckOutcome> {
    let defaults = v8::CreateParams::default();
    let zero = v8::CreateParams::default().heap_limits(0, 0);
    let one = v8::CreateParams::default()
        .set_max_old_generation_size_in_bytes(1)
        .set_max_young_generation_size_in_bytes(1)
        .set_initial_old_generation_size_in_bytes(1)
        .set_initial_young_generation_size_in_bytes(1)
        .set_code_range_size_in_bytes(1);
    let max = v8::CreateParams::default()
        .set_max_old_generation_size_in_bytes(usize::MAX)
        .set_max_young_generation_size_in_bytes(usize::MAX)
        .set_initial_old_generation_size_in_bytes(usize::MAX)
        .set_initial_young_generation_size_in_bytes(usize::MAX)
        .set_code_range_size_in_bytes(usize::MAX);
    let inconsistent = v8::CreateParams::default()
        .set_max_old_generation_size_in_bytes(16 * MIB)
        .set_initial_old_generation_size_in_bytes(32 * MIB);
    let inconsistent_isolate_marker = consume_marker(
        v8::CreateParams::default()
            .snapshot_blob(plain_blob())
            .set_max_old_generation_size_in_bytes(16 * MIB)
            .set_initial_old_generation_size_in_bytes(32 * MIB),
    )
    .0;
    let tiny_direct_isolate_marker = consume_marker(
        v8::CreateParams::default()
            .snapshot_blob(plain_blob())
            .set_max_old_generation_size_in_bytes(1)
            .set_max_young_generation_size_in_bytes(1)
            .set_initial_old_generation_size_in_bytes(1)
            .set_initial_young_generation_size_in_bytes(1)
            .set_code_range_size_in_bytes(1),
    )
    .0;
    let encode = |params: &v8::CreateParams| {
        Json::arr(vec![
            Json::i(params.max_old_generation_size_in_bytes() as i64),
            Json::i(params.max_young_generation_size_in_bytes() as i64),
            Json::i(params.initial_old_generation_size_in_bytes() as i64),
            Json::i(params.initial_young_generation_size_in_bytes() as i64),
            Json::i(params.code_range_size_in_bytes() as i64),
        ])
    };
    vec![pass(
        "create-params-snapshot/constraint_builder_boundaries",
        Json::obj(vec![
            (
                "order",
                Json::s("max_old,max_young,initial_old,initial_young,code_range"),
            ),
            ("defaults", encode(&defaults)),
            ("heap_limits_zero", encode(&zero)),
            ("ones", encode(&one)),
            (
                "usize_max_round_trips",
                Json::b(
                    max.max_old_generation_size_in_bytes() == usize::MAX
                        && max.max_young_generation_size_in_bytes() == usize::MAX
                        && max.initial_old_generation_size_in_bytes() == usize::MAX
                        && max.initial_young_generation_size_in_bytes() == usize::MAX
                        && max.code_range_size_in_bytes() == usize::MAX,
                ),
            ),
            (
                "inconsistent_builder_round_trips",
                Json::b(
                    inconsistent.max_old_generation_size_in_bytes() == 16 * MIB
                        && inconsistent.initial_old_generation_size_in_bytes() == 32 * MIB,
                ),
            ),
            (
                "inconsistent_direct_isolate_marker",
                Json::i(inconsistent_isolate_marker),
            ),
            (
                "tiny_direct_isolate_marker",
                Json::i(tiny_direct_isolate_marker),
            ),
        ]),
    )]
}

fn cloned_blob_parameter_reuse() -> Vec<CheckOutcome> {
    let blob = plain_blob();
    let mut values = Vec::new();
    for allowed in [true, false, true] {
        let params = v8::CreateParams::default()
            .snapshot_blob(blob.clone())
            .allow_atomics_wait(allowed);
        values.push(Json::i(consume_marker(params).0));
    }
    drop(blob);
    vec![pass(
        "create-params-snapshot/cloned_blob_parameter_reuse",
        Json::obj(vec![
            ("consumer_markers", Json::arr(values)),
            ("create_params_is_single_use", Json::b(true)),
            ("startup_data_clone_is_reusable", Json::b(true)),
            ("all_consumers_and_blob_dropped", Json::b(true)),
        ]),
    )]
}

fn mode_inconsistent_heap_limits() {
    let blob = plain_blob();
    let params = v8::CreateParams::default()
        .snapshot_blob(blob)
        .heap_limits(64 * MIB, 32 * MIB);
    let _ = v8::Isolate::new(params);
}

fn mode_invalid_snapshot() {
    let params = v8::CreateParams::default().snapshot_blob(v8::StartupData::from(Vec::new()));
    let _ = v8::Isolate::new(params);
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    match std::env::args().nth(1).as_deref() {
        Some("mode=inconsistent-heap-limits") => mode_inconsistent_heap_limits(),
        Some("mode=invalid-snapshot") => mode_invalid_snapshot(),
        Some(other) => {
            eprintln!("unknown mode: {other}");
            return std::process::ExitCode::FAILURE;
        }
        None => {
            let checks: Vec<CheckOutcome> = [
                snapshot_independent_and_allocator(),
                snapshot_atomics_wait(),
                unified_all_safe_parameters(),
                constraint_boundaries_without_isolate(),
                cloned_blob_parameter_reuse(),
            ]
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
