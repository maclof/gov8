//! Fast API descriptor and `FunctionBuilder::build_fast` substrate oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty on
//! x86_64-pc-windows-msvc. Optimization is requested explicitly with V8 native
//! syntax so call-path counters, rather than optimizer timing, are observed.

use std::ffi::c_void;
use std::sync::atomic::{AtomicUsize, Ordering};

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use v8::fast_api::{CFunction, CFunctionInfo, CTypeInfo, Flags, Int64Representation, Type};

static ADD_FAST_CALLS: AtomicUsize = AtomicUsize::new(0);
static ADD_SLOW_CALLS: AtomicUsize = AtomicUsize::new(0);
static ONE_FAST_CALLS: AtomicUsize = AtomicUsize::new(0);
static TWO_FAST_CALLS: AtomicUsize = AtomicUsize::new(0);
static OVERLOAD_SLOW_CALLS: AtomicUsize = AtomicUsize::new(0);
static EMPTY_SLOW_CALLS: AtomicUsize = AtomicUsize::new(0);

extern "C" fn fast_add(_receiver: v8::Local<v8::Object>, a: u32, b: u32) -> u32 {
    ADD_FAST_CALLS.fetch_add(1, Ordering::SeqCst);
    a + b
}

extern "C" fn fast_one(_receiver: v8::Local<v8::Object>, a: u32) -> u32 {
    ONE_FAST_CALLS.fetch_add(1, Ordering::SeqCst);
    100 + a
}

extern "C" fn fast_two(_receiver: v8::Local<v8::Object>, a: u32, b: u32) -> u32 {
    TWO_FAST_CALLS.fetch_add(1, Ordering::SeqCst);
    200 + a + b
}

const ADD_FAST: CFunction = CFunction::new(
    fast_add as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[
            Type::V8Value.as_info(),
            Type::Uint32.as_info(),
            Type::Uint32.as_info(),
        ],
        Int64Representation::Number,
    ),
);

const ADD_OVERLOADS: &[CFunction] = &[ADD_FAST];

const FAST_ONE: CFunction = CFunction::new(
    fast_one as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[Type::V8Value.as_info(), Type::Uint32.as_info()],
        Int64Representation::Number,
    ),
);

const FAST_TWO: CFunction = CFunction::new(
    fast_two as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[
            Type::V8Value.as_info(),
            Type::Uint32.as_info(),
            Type::Uint32.as_info(),
        ],
        Int64Representation::Number,
    ),
);

const ARITY_OVERLOADS: &[CFunction] = &[FAST_ONE, FAST_TWO];
const DUPLICATE_ARITY_OVERLOADS: &[CFunction] = &[FAST_ONE, FAST_ONE];
const EMPTY_OVERLOADS: &[CFunction] = &[];

fn slow_add(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    ADD_SLOW_CALLS.fetch_add(1, Ordering::SeqCst);
    let a = args.get(0).uint32_value(scope).unwrap_or(0);
    let b = args.get(1).uint32_value(scope).unwrap_or(0);
    rv.set_uint32(a + b);
}

fn slow_overload(
    _scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    OVERLOAD_SLOW_CALLS.fetch_add(1, Ordering::SeqCst);
    rv.set_int32(900 + args.length());
}

fn slow_empty(
    _scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    EMPTY_SLOW_CALLS.fetch_add(1, Ordering::SeqCst);
    rv.set_int32(700 + args.length());
}

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

fn integer(scope: &v8::PinScope<'_, '_>, source: &str) -> Option<i64> {
    run_script(scope, source)?.integer_value(scope)
}

fn set_function(
    scope: &v8::PinScope<'_, '_>,
    context: v8::Local<'_, v8::Context>,
    name: &str,
    function: v8::Local<'_, v8::Function>,
) {
    let name = v8::String::new(scope, name).unwrap();
    assert_eq!(
        context
            .global(scope)
            .set(scope, name.into(), function.into()),
        Some(true)
    );
}

fn counters(slow: &AtomicUsize, fast: &[&AtomicUsize]) -> Json {
    Json::obj(vec![
        ("slow", Json::i(slow.load(Ordering::SeqCst) as i64)),
        (
            "fast",
            Json::arr(
                fast.iter()
                    .map(|counter| Json::i(counter.load(Ordering::SeqCst) as i64))
                    .collect(),
            ),
        ),
    ])
}

fn metadata() -> Vec<CheckOutcome> {
    let types = [
        ("Void", Type::Void),
        ("Bool", Type::Bool),
        ("Uint8", Type::Uint8),
        ("Int32", Type::Int32),
        ("Uint32", Type::Uint32),
        ("Int64", Type::Int64),
        ("Uint64", Type::Uint64),
        ("Float32", Type::Float32),
        ("Float64", Type::Float64),
        ("Pointer", Type::Pointer),
        ("V8Value", Type::V8Value),
        ("SeqOneByteString", Type::SeqOneByteString),
        ("ApiObject", Type::ApiObject),
        ("Any", Type::Any),
        ("CallbackOptions", Type::CallbackOptions),
    ];
    let flags = [
        ("AllowShared", Flags::AllowShared),
        ("EnforceRange", Flags::EnforceRange),
        ("Clamp", Flags::Clamp),
        ("IsRestricted", Flags::IsRestricted),
    ];
    let all_flags = flags.iter().fold(Flags::empty(), |combined, (_, flag)| {
        combined | Flags::from_bits_retain(flag.bits())
    });
    let descriptor = ADD_OVERLOADS[0];
    let descriptor_copy = descriptor;
    vec![pass(
        "fast-api-substrate/native_descriptor_metadata",
        Json::obj(vec![
            (
                "types",
                Json::arr(
                    types
                        .iter()
                        .map(|(name, value)| {
                            Json::obj(vec![
                                ("name", Json::s(name)),
                                ("discriminant", Json::i(*value as u8 as i64)),
                            ])
                        })
                        .collect(),
                ),
            ),
            (
                "int64_representations",
                Json::arr(vec![
                    Json::obj(vec![
                        ("name", Json::s("Number")),
                        (
                            "discriminant",
                            Json::i(Int64Representation::Number as u8 as i64),
                        ),
                    ]),
                    Json::obj(vec![
                        ("name", Json::s("BigInt")),
                        (
                            "discriminant",
                            Json::i(Int64Representation::BigInt as u8 as i64),
                        ),
                    ]),
                ]),
            ),
            (
                "flags",
                Json::arr(
                    flags
                        .iter()
                        .map(|(name, value)| {
                            Json::obj(vec![
                                ("name", Json::s(name)),
                                ("bits", Json::i(value.bits() as i64)),
                                (
                                    "round_trip",
                                    Json::b(Flags::from_bits(value.bits()).is_some_and(
                                        |round_trip| round_trip.bits() == value.bits(),
                                    )),
                                ),
                            ])
                        })
                        .collect(),
                ),
            ),
            ("empty_flag_bits", Json::i(Flags::empty().bits() as i64)),
            ("all_flag_bits", Json::i(all_flags.bits() as i64)),
            (
                "unknown_flag_rejected",
                Json::b(Flags::from_bits(0x10).is_none()),
            ),
            (
                "unknown_flag_truncated",
                Json::i(Flags::from_bits_truncate(0x1f).bits() as i64),
            ),
            (
                "layout",
                Json::obj(vec![
                    ("type_size", Json::i(std::mem::size_of::<Type>() as i64)),
                    ("flags_size", Json::i(std::mem::size_of::<Flags>() as i64)),
                    (
                        "ctype_info_size",
                        Json::i(std::mem::size_of::<CTypeInfo>() as i64),
                    ),
                    (
                        "cfunction_info_size",
                        Json::i(std::mem::size_of::<CFunctionInfo>() as i64),
                    ),
                    (
                        "cfunction_size",
                        Json::i(std::mem::size_of::<CFunction>() as i64),
                    ),
                    (
                        "cfunction_align",
                        Json::i(std::mem::align_of::<CFunction>() as i64),
                    ),
                ]),
            ),
            (
                "address_identity",
                Json::b(descriptor.address() == fast_add as *const c_void),
            ),
            (
                "copied_address_identity",
                Json::b(descriptor_copy.address() == descriptor.address()),
            ),
            (
                "copied_type_info_identity",
                Json::b(std::ptr::eq(
                    descriptor_copy.type_info(),
                    descriptor.type_info(),
                )),
            ),
        ]),
    )]
}

fn single_overload_execution() -> Vec<CheckOutcome> {
    ADD_FAST_CALLS.store(0, Ordering::SeqCst);
    ADD_SLOW_CALLS.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());

    // Persist the context and function, then close the HandleScope in which
    // FunctionTemplate::build_fast read the static descriptor.
    let (context_global, function_global) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let template = v8::FunctionTemplate::builder(slow_add).build_fast(scope, ADD_OVERLOADS);
        let function = template.get_function(scope).unwrap();
        (
            v8::Global::new(scope, context),
            v8::Global::new(scope, function),
        )
    };

    let observation = {
        v8::scope!(let scope, isolate);
        let context = v8::Local::new(scope, &context_global);
        let scope = &mut v8::ContextScope::new(scope, context);
        let function = v8::Local::new(scope, &function_global);
        set_function(scope, context, "fastAdd", function);
        let cold = integer(
            scope,
            "function addWrap(a,b){return fastAdd(a,b)};\
             %PrepareFunctionForOptimization(addWrap); addWrap(19,23)",
        );
        let after_cold = counters(&ADD_SLOW_CALLS, &[&ADD_FAST_CALLS]);
        let optimized = integer(
            scope,
            "%OptimizeFunctionOnNextCall(addWrap); addWrap(20,22)",
        );
        let after_optimized = counters(&ADD_SLOW_CALLS, &[&ADD_FAST_CALLS]);
        let incompatible = integer(scope, "addWrap('19',23)");
        let after_incompatible = counters(&ADD_SLOW_CALLS, &[&ADD_FAST_CALLS]);
        Json::obj(vec![
            ("creation_handle_scope_closed", Json::b(true)),
            ("cold_result", cold.map_or(Json::Null, Json::i)),
            ("after_cold", after_cold),
            ("optimized_result", optimized.map_or(Json::Null, Json::i)),
            ("after_optimized", after_optimized),
            (
                "incompatible_result",
                incompatible.map_or(Json::Null, Json::i),
            ),
            ("after_incompatible", after_incompatible),
        ])
    };
    vec![pass(
        "fast-api-substrate/single_overload_execution_and_lifetime",
        observation,
    )]
}

fn overload_arity_and_fallback() -> Vec<CheckOutcome> {
    ONE_FAST_CALLS.store(0, Ordering::SeqCst);
    TWO_FAST_CALLS.store(0, Ordering::SeqCst);
    OVERLOAD_SLOW_CALLS.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let template = v8::FunctionTemplate::builder(slow_overload).build_fast(scope, ARITY_OVERLOADS);
    let function = template.get_function(scope).unwrap();
    set_function(scope, context, "overloaded", function);

    let cold = run_script(
        scope,
        "function f0(){return overloaded()}\
         function f1(a){return overloaded(a)}\
         function f2(a,b){return overloaded(a,b)}\
         function f3(a,b,c){return overloaded(a,b,c)}\
         %PrepareFunctionForOptimization(f0);\
         %PrepareFunctionForOptimization(f1);\
         %PrepareFunctionForOptimization(f2);\
         %PrepareFunctionForOptimization(f3);\
         [f0(),f1(1),f2(1,2),f3(1,2,3)].join(',')",
    )
    .and_then(|value| value.to_string(scope))
    .map(|value| value.to_rust_string_lossy(scope));
    let after_cold = counters(&OVERLOAD_SLOW_CALLS, &[&ONE_FAST_CALLS, &TWO_FAST_CALLS]);

    let mut optimized_cases = Vec::new();
    for (name, source) in [
        ("one_arg", "%OptimizeFunctionOnNextCall(f1); f1(1)"),
        ("two_args", "%OptimizeFunctionOnNextCall(f2); f2(1,2)"),
        ("zero_args", "%OptimizeFunctionOnNextCall(f0); f0()"),
        ("three_args", "%OptimizeFunctionOnNextCall(f3); f3(1,2,3)"),
        ("type_mismatch", "f1('x')"),
    ] {
        let value = integer(scope, source);
        optimized_cases.push(Json::obj(vec![
            ("case", Json::s(name)),
            ("result", value.map_or(Json::Null, Json::i)),
            (
                "calls",
                counters(&OVERLOAD_SLOW_CALLS, &[&ONE_FAST_CALLS, &TWO_FAST_CALLS]),
            ),
        ]));
    }
    vec![pass(
        "fast-api-substrate/two_overload_arity_and_fallback",
        Json::obj(vec![
            (
                "cold_results",
                cold.map_or(Json::Null, |value| Json::s(&value)),
            ),
            ("after_cold", after_cold),
            ("optimized_cases", Json::arr(optimized_cases)),
        ]),
    )]
}

fn empty_overloads_boundary() -> Vec<CheckOutcome> {
    EMPTY_SLOW_CALLS.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let template = v8::FunctionTemplate::builder(slow_empty).build_fast(scope, EMPTY_OVERLOADS);
    let function = template.get_function(scope).unwrap();
    set_function(scope, context, "emptyFast", function);
    let direct = integer(scope, "emptyFast(1,2)");
    let construct = run_script(
        scope,
        "try { new emptyFast(); 'constructed' } catch (e) { e.name + ':' + e.message }",
    )
    .and_then(|value| value.to_string(scope))
    .map(|value| value.to_rust_string_lossy(scope));
    vec![pass(
        "fast-api-substrate/empty_overloads_safe_boundary",
        Json::obj(vec![
            ("built", Json::b(true)),
            ("direct_result", direct.map_or(Json::Null, Json::i)),
            (
                "slow_calls",
                Json::i(EMPTY_SLOW_CALLS.load(Ordering::SeqCst) as i64),
            ),
            (
                "construct_result",
                construct.map_or(Json::Null, |value| Json::s(&value)),
            ),
        ]),
    )]
}

fn duplicate_arity_mode() {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _ =
        v8::FunctionTemplate::builder(slow_overload).build_fast(scope, DUPLICATE_ARITY_OVERLOADS);
}

const CHECKS: &[fn() -> Vec<CheckOutcome>] = &[
    metadata,
    single_overload_execution,
    overload_arity_and_fallback,
    empty_overloads_boundary,
];

fn main() -> std::process::ExitCode {
    v8::V8::set_flags_from_string("--allow-natives-syntax");
    oracle::ensure_v8();
    if std::env::args().nth(1).as_deref() == Some("mode=duplicate-arity") {
        duplicate_arity_mode();
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
