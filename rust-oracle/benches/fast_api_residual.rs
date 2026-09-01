//! Optimized native Fast API versus ordinary slow callback benchmark.
//!
//! Matches Go's `BenchmarkFastAPINativeOptimized` and
//! `BenchmarkFastAPIGoSlowFallback`. Isolate/context/function construction,
//! script compilation, and explicit optimization warm-up are outside timing.
//! Each timed iteration calls `benchLoop(256)` once; that loop calls the same
//! `value + 1` target 256 times. The fast workload uses the callback-options
//! descriptor from the residual slice, while the fallback is a normal native
//! Function with no fast descriptor.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion, Throughput};
use std::ffi::c_void;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Once;
use std::time::Instant;
use v8::fast_api::{CFunction, CFunctionInfo, FastApiCallbackOptions, Int64Representation, Type};

const CALLS_PER_ITERATION: u64 = 256;
static INITIALIZE: Once = Once::new();
static FAST_CALLS: AtomicU64 = AtomicU64::new(0);
static SLOW_CALLS: AtomicU64 = AtomicU64::new(0);

fn ensure_v8_for_fast_api() {
    INITIALIZE.call_once(|| {
        v8::V8::set_flags_from_string("--allow-natives-syntax");
        oracle::ensure_v8();
    });
}

extern "C" fn fast_increment(
    _receiver: v8::Local<v8::Object>,
    value: u32,
    _options: *mut FastApiCallbackOptions<'_>,
) -> u32 {
    FAST_CALLS.fetch_add(1, Ordering::SeqCst);
    value + 1
}

const FAST_CALL: CFunction = CFunction::new(
    fast_increment as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[
            Type::V8Value.as_info(),
            Type::Uint32.as_info(),
            Type::CallbackOptions.as_info(),
        ],
        Int64Representation::Number,
    ),
);
const FAST_OVERLOADS: &[CFunction] = &[FAST_CALL];

fn slow_increment(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    SLOW_CALLS.fetch_add(1, Ordering::SeqCst);
    rv.set_uint32(args.get(0).uint32_value(scope).unwrap_or(0) + 1);
}

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).expect("benchmark source");
    v8::Script::compile(scope, source, None)
        .expect("benchmark source compiles")
        .run(scope)
        .expect("benchmark source runs")
}

fn benchmark_execution(c: &mut Criterion, use_fast: bool) {
    common::banner();
    ensure_v8_for_fast_api();
    FAST_CALLS.store(0, Ordering::SeqCst);
    SLOW_CALLS.store(0, Ordering::SeqCst);

    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let target = if use_fast {
        v8::FunctionTemplate::builder(slow_increment)
            .build_fast(scope, FAST_OVERLOADS)
            .get_function(scope)
            .expect("fast function")
    } else {
        v8::Function::builder(slow_increment)
            .build(scope)
            .expect("slow function")
    };
    let target_name = v8::String::new(scope, "benchTarget").unwrap();
    assert_eq!(
        context
            .global(scope)
            .set(scope, target_name.into(), target.into()),
        Some(true)
    );

    let loop_function = v8::Local::<v8::Function>::try_from(run_script(
        scope,
        "function benchLoop(n){let value=0;for(let i=0;i<n;i++)value=benchTarget(i);return value}; benchLoop",
    ))
    .expect("benchLoop function");
    run_script(
        scope,
        "%PrepareFunctionForOptimization(benchLoop); benchLoop(1); %OptimizeFunctionOnNextCall(benchLoop); benchLoop(1)",
    );
    if use_fast {
        assert!(
            FAST_CALLS.load(Ordering::SeqCst) > 0,
            "optimization warm-up never selected the native fast path"
        );
    }

    let receiver: v8::Local<v8::Value> = v8::undefined(scope).into();
    let call_count: v8::Local<v8::Value> =
        v8::Number::new(scope, CALLS_PER_ITERATION as f64).into();
    let name = if use_fast {
        "native_optimized_loop_256"
    } else {
        "slow_callback_fallback_loop_256"
    };
    let mut group = c.benchmark_group("fast_api_residual");
    group.throughput(Throughput::Elements(CALLS_PER_ITERATION));
    group.bench_function(name, |b| {
        b.iter_custom(|iterations| {
            let before_fast = FAST_CALLS.load(Ordering::SeqCst);
            let before_slow = SLOW_CALLS.load(Ordering::SeqCst);
            let start = Instant::now();
            for _ in 0..iterations {
                assert!(
                    loop_function.call(scope, receiver, &[call_count]).is_some(),
                    "benchLoop call failed"
                );
            }
            let elapsed = start.elapsed();
            let fast_delta = FAST_CALLS.load(Ordering::SeqCst) - before_fast;
            let slow_delta = SLOW_CALLS.load(Ordering::SeqCst) - before_slow;
            let expected_calls = iterations * CALLS_PER_ITERATION;
            if use_fast {
                assert_eq!(
                    fast_delta, expected_calls,
                    "every JS call must use the fast callback"
                );
                assert_eq!(slow_delta, 0, "optimized workload used the slow fallback");
            } else {
                assert_eq!(fast_delta, 0, "fallback unexpectedly used the fast path");
                assert_eq!(
                    slow_delta, expected_calls,
                    "fallback must call the slow callback once per JS call"
                );
            }
            elapsed
        });
    });
    group.finish();
}

fn native_optimized(c: &mut Criterion) {
    benchmark_execution(c, true);
}

fn slow_callback_fallback(c: &mut Criterion) {
    benchmark_execution(c, false);
}

criterion_group! {
    name = fast_api_residual_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = native_optimized, slow_callback_fallback
}

criterion_main!(fast_api_residual_benches);
