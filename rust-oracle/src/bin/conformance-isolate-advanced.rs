//! Advanced isolate/CreateParams/statistics oracle for v8 152.2.0.
//!
//! Values which depend on addresses, heap activity, or background scheduling
//! are reduced to stable relationships. Unsafe custom allocators, synthetic
//! stack limits, cppgc heaps, and disposal-after-use are deliberately excluded.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::collections::HashMap;
use std::ffi::{c_char, CStr};
use std::sync::{LazyLock, Mutex};

fn eval_text(scope: &mut v8::PinScope<'_, '_>, source: &str) -> Option<String> {
    let source = v8::String::new(scope, source)?;
    let script = v8::Script::compile(scope, source, None)?;
    script
        .run(scope)?
        .to_string(scope)
        .map(|value| value.to_rust_string_lossy(scope))
}

fn create_params_constraints() -> Vec<CheckOutcome> {
    const MIB: usize = 1024 * 1024;
    let defaults = v8::CreateParams::default();
    let configured = v8::CreateParams::default()
        .set_max_old_generation_size_in_bytes(128 * MIB)
        .set_max_young_generation_size_in_bytes(16 * MIB)
        .set_code_range_size_in_bytes(64 * MIB)
        .set_initial_old_generation_size_in_bytes(8 * MIB)
        .set_initial_young_generation_size_in_bytes(2 * MIB);

    let mut marker = 0_u32;
    // Merely round-trips a live pointer through the builder. It is never used
    // to create an isolate because it is not a real stack boundary.
    let with_stack = unsafe { v8::CreateParams::default().set_stack_limit(&mut marker) };

    vec![pass(
        "isolate-advanced/create-params/constraint_getters",
        Json::obj(vec![
            (
                "defaults_zero",
                Json::b(
                    defaults.max_old_generation_size_in_bytes() == 0
                        && defaults.max_young_generation_size_in_bytes() == 0
                        && defaults.code_range_size_in_bytes() == 0
                        && defaults.initial_old_generation_size_in_bytes() == 0
                        && defaults.initial_young_generation_size_in_bytes() == 0,
                ),
            ),
            (
                "default_stack_null",
                Json::b(defaults.stack_limit().is_null()),
            ),
            (
                "max_old",
                Json::i(configured.max_old_generation_size_in_bytes() as i64),
            ),
            (
                "max_young",
                Json::i(configured.max_young_generation_size_in_bytes() as i64),
            ),
            (
                "code_range",
                Json::i(configured.code_range_size_in_bytes() as i64),
            ),
            (
                "initial_old",
                Json::i(configured.initial_old_generation_size_in_bytes() as i64),
            ),
            (
                "initial_young",
                Json::i(configured.initial_young_generation_size_in_bytes() as i64),
            ),
            (
                "stack_pointer_round_trip",
                Json::b(with_stack.stack_limit() == &mut marker),
            ),
        ]),
    )]
}

fn create_params_derived_limits() -> Vec<CheckOutcome> {
    const MIB: usize = 1024 * 1024;
    let heap = v8::CreateParams::default().heap_limits(32 * MIB, 96 * MIB);
    let system = v8::CreateParams::default()
        .heap_limits_from_system_memory(512 * MIB as u64, 1024 * MIB as u64);
    let encode = |params: &v8::CreateParams| {
        Json::obj(vec![
            (
                "max_old",
                Json::i(params.max_old_generation_size_in_bytes() as i64),
            ),
            (
                "max_young",
                Json::i(params.max_young_generation_size_in_bytes() as i64),
            ),
            (
                "initial_old",
                Json::i(params.initial_old_generation_size_in_bytes() as i64),
            ),
            (
                "initial_young",
                Json::i(params.initial_young_generation_size_in_bytes() as i64),
            ),
            (
                "code_range",
                Json::i(params.code_range_size_in_bytes() as i64),
            ),
        ])
    };
    vec![pass(
        "isolate-advanced/create-params/derived_heap_limits",
        Json::obj(vec![
            ("heap_bounds", encode(&heap)),
            ("system_memory", encode(&system)),
        ]),
    )]
}

fn create_params_allocator_and_external_references() -> Vec<CheckOutcome> {
    let default_has_allocator = v8::CreateParams::default().has_set_array_buffer_allocator();
    let allocator = v8::new_default_allocator().make_shared();
    let params = v8::CreateParams::default().array_buffer_allocator(allocator.clone());
    let configured_has_allocator = params.has_set_array_buffer_allocator();
    drop(params);
    let buffer_len = {
        let mut isolate = v8::Isolate::new(Default::default());
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let len = v8::ArrayBuffer::new(scope, 17).byte_length();
        len
    };
    let empty_external_references_usable = {
        let params =
            v8::CreateParams::default().external_references(std::borrow::Cow::Owned(Vec::new()));
        let mut isolate = v8::Isolate::new(params);
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval_text(scope, "String(6 * 7)") == Some("42".to_owned())
    };
    vec![pass(
        "isolate-advanced/create-params/allocator_external_references",
        Json::obj(vec![
            ("default_has_allocator", Json::b(default_has_allocator)),
            (
                "configured_has_allocator",
                Json::b(configured_has_allocator),
            ),
            ("array_buffer_length", Json::i(buffer_len as i64)),
            (
                "empty_external_references_usable",
                Json::b(empty_external_references_usable),
            ),
        ]),
    )]
}

fn atomics_wait(allowed: bool) -> Json {
    let mut isolate = v8::Isolate::new(v8::CreateParams::default().allow_atomics_wait(allowed));
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    v8::tc_scope!(let tc, scope);
    let result = eval_text(
        tc,
        "Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 1)",
    );
    let exception = tc
        .exception()
        .and_then(|value| value.to_string(tc))
        .map(|value| value.to_rust_string_lossy(tc));
    Json::obj(vec![
        (
            "result",
            result.as_deref().map(Json::s).unwrap_or(Json::Null),
        ),
        ("caught", Json::b(tc.has_caught())),
        (
            "exception",
            exception.as_deref().map(Json::s).unwrap_or(Json::Null),
        ),
    ])
}

fn create_params_atomics_wait() -> Vec<CheckOutcome> {
    vec![pass(
        "isolate-advanced/create-params/allow_atomics_wait",
        Json::obj(vec![
            ("disabled", atomics_wait(false)),
            ("enabled", atomics_wait(true)),
        ]),
    )]
}

static COUNTERS: LazyLock<Mutex<HashMap<String, usize>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));

unsafe extern "C" fn counter_lookup(name: *const c_char) -> *mut i32 {
    // The boxes are intentionally leaked: V8 requires each returned address
    // to remain valid for the whole isolate lifetime (and may retain it).
    let name = unsafe { CStr::from_ptr(name) }
        .to_string_lossy()
        .into_owned();
    let mut counters = COUNTERS.lock().expect("counter map poisoned");
    let address = counters
        .entry(name)
        .or_insert_with(|| Box::into_raw(Box::new(0_i32)) as usize);
    *address as *mut i32
}

fn create_params_counter_callback() -> Vec<CheckOutcome> {
    COUNTERS.lock().expect("counter map poisoned").clear();
    {
        let params = v8::CreateParams::default().counter_lookup_callback(counter_lookup);
        let mut isolate = v8::Isolate::new(params);
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        for source in [
            "1 + 1",
            "2 + 2",
            "function oracleCounter(){ return 3; } oracleCounter()",
        ] {
            let _ = eval_text(scope, source);
        }
    }
    let counters = COUNTERS.lock().expect("counter map poisoned");
    let cache_misses = counters
        .get("c:V8.CompilationCacheMisses")
        .map(|address| unsafe { *(*address as *const i32) })
        .unwrap_or_default();
    vec![pass(
        "isolate-advanced/create-params/counter_lookup_callback",
        Json::obj(vec![
            ("callback_observed_names", Json::b(!counters.is_empty())),
            (
                "compilation_cache_misses_positive",
                Json::b(cache_misses > 0),
            ),
        ]),
    )]
}

fn statistics_heap_and_spaces() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    let heap = isolate.get_heap_statistics();
    let heap_value = Json::obj(vec![
        (
            "used_le_total",
            Json::b(heap.used_heap_size() <= heap.total_heap_size()),
        ),
        (
            "executable_le_total",
            Json::b(heap.total_heap_size_executable() <= heap.total_heap_size()),
        ),
        ("physical_positive", Json::b(heap.total_physical_size() > 0)),
        (
            "available_positive",
            Json::b(heap.total_available_size() > 0),
        ),
        ("heap_limit_positive", Json::b(heap.heap_size_limit() > 0)),
        (
            "global_handles_coherent",
            Json::b(heap.used_global_handles_size() <= heap.total_global_handles_size()),
        ),
        ("malloced_positive", Json::b(heap.malloced_memory() > 0)),
        (
            "peak_malloced_positive",
            Json::b(heap.peak_malloced_memory() > 0),
        ),
        ("external_memory_zero", Json::b(heap.external_memory() == 0)),
        (
            "allocated_positive",
            Json::b(heap.total_allocated_bytes() > 0),
        ),
        (
            "native_contexts",
            Json::i(heap.number_of_native_contexts() as i64),
        ),
        (
            "detached_contexts",
            Json::i(heap.number_of_detached_contexts() as i64),
        ),
        ("zaps_garbage", Json::b(heap.does_zap_garbage())),
    ]);
    let count = isolate.number_of_heap_spaces();
    let spaces = (0..count)
        .map(|index| {
            let space = isolate
                .get_heap_space_statistics(index)
                .expect("valid heap space");
            Json::obj(vec![
                ("name", Json::s(&space.space_name().to_string_lossy())),
                (
                    "used_le_size",
                    Json::b(space.space_used_size() <= space.space_size()),
                ),
                (
                    "available_le_size",
                    Json::b(space.space_available_size() <= space.space_size()),
                ),
                (
                    "physical_le_size",
                    Json::b(space.physical_space_size() <= space.space_size()),
                ),
            ])
        })
        .collect();
    let boundary_none = isolate.get_heap_space_statistics(count).is_none();
    let huge_none = isolate.get_heap_space_statistics(usize::MAX).is_none();
    vec![
        pass("isolate-advanced/statistics/heap_invariants", heap_value),
        pass(
            "isolate-advanced/statistics/heap_spaces",
            Json::obj(vec![
                ("count", Json::i(count as i64)),
                ("spaces", Json::arr(spaces)),
                ("index_at_count_none", Json::b(boundary_none)),
                ("usize_max_none", Json::b(huge_none)),
            ]),
        ),
    ]
}

fn statistics_code_metadata() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    let before = isolate.get_heap_code_and_metadata_statistics();
    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let _ = eval_text(
            scope,
            "function metadataOracle(a){ return a + 1; } metadataOracle(41)",
        );
    }
    let after = isolate.get_heap_code_and_metadata_statistics();
    let value = match (before, after) {
        (Some(before), Some(after)) => Json::obj(vec![
            ("available", Json::b(true)),
            (
                "before_external_source_zero",
                Json::b(before.external_script_source_size() == 0),
            ),
            (
                "before_profiler_metadata_zero",
                Json::b(before.cpu_profiler_metadata_size() == 0),
            ),
            (
                "after_code_positive",
                Json::b(after.code_and_metadata_size() > 0),
            ),
            (
                "after_bytecode_positive",
                Json::b(after.bytecode_and_metadata_size() > 0),
            ),
            (
                "code_not_decreased",
                Json::b(after.code_and_metadata_size() >= before.code_and_metadata_size()),
            ),
        ]),
        _ => Json::obj(vec![("available", Json::b(false))]),
    };
    vec![pass("isolate-advanced/statistics/code_metadata", value)]
}

fn isolate_notifications_and_profiler_controls() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    let cpp_heap_present = isolate.get_cpp_heap().is_some();
    isolate.use_detailed_source_positions_for_profiling();
    isolate.collect_cpu_profiler_sample(None);
    isolate.collect_cpu_profiler_sample(Some(42));
    isolate.memory_pressure_notification(v8::MemoryPressureLevel::Moderate);
    isolate.memory_pressure_notification(v8::MemoryPressureLevel::Critical);
    isolate.memory_pressure_notification(v8::MemoryPressureLevel::None);
    isolate.low_memory_notification();
    isolate.clear_kept_objects();
    let usable_after_notifications = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval_text(scope, "String(40 + 2)") == Some("42".to_owned())
    };
    let heap = isolate.get_heap_statistics();
    vec![pass(
        "isolate-advanced/isolate/notifications_profiler_controls",
        Json::obj(vec![
            ("cpp_heap_present", Json::b(cpp_heap_present)),
            (
                "usable_after_notifications",
                Json::b(usable_after_notifications),
            ),
            (
                "heap_coherent",
                Json::b(heap.used_heap_size() <= heap.total_heap_size()),
            ),
        ]),
    )]
}

type CheckFn = fn() -> Vec<CheckOutcome>;
const CHECKS: &[CheckFn] = &[
    create_params_constraints,
    create_params_derived_limits,
    create_params_allocator_and_external_references,
    create_params_atomics_wait,
    create_params_counter_callback,
    statistics_heap_and_spaces,
    statistics_code_metadata,
    isolate_notifications_and_profiler_controls,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let outcomes: Vec<_> = CHECKS.iter().flat_map(|check| check()).collect();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let failed = outcomes.len() - passed;
    let mut output = String::new();
    for outcome in &outcomes {
        output.push_str(&outcome.to_line());
        output.push('\n');
    }
    output.push_str(&summary_line(outcomes.len(), passed, failed));
    output.push('\n');
    print!("{output}");
    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
