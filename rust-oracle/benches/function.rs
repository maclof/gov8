//! Advanced `ScriptCompiler::compile_function` benchmarks.
//!
//! These mirror the Go `function_advanced_bench_test.go` operation bounds:
//! V8/platform initialization and isolate/context creation are setup, while a
//! fresh handle scope, V8 strings, `Source`, parameters and function compile
//! are measured per iteration. Cache production and the correctness probe are
//! setup for the consume benchmark.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, BatchSize, Criterion};
use std::cell::Cell;
use std::hint::black_box;

const SOURCE: &str = "return left * 10 + right;";

fn compile_function<'s>(
    scope: &v8::PinScope<'s, '_>,
    source_text: &str,
    cached_data: Option<v8::UniqueRef<v8::CachedData<'_>>>,
    options: v8::script_compiler::CompileOptions,
) -> (v8::Local<'s, v8::Function>, bool) {
    let source_text = v8::String::new(scope, source_text).unwrap();
    let mut source = match cached_data {
        Some(cached_data) => {
            v8::script_compiler::Source::new_with_cached_data(source_text, None, cached_data)
        }
        None => v8::script_compiler::Source::new(source_text, None),
    };
    let left = v8::String::new(scope, "left").unwrap();
    let right = v8::String::new(scope, "right").unwrap();
    let function = v8::script_compiler::compile_function(
        scope,
        &mut source,
        &[left, right],
        &[],
        options,
        v8::script_compiler::NoCacheReason::NoReason,
    )
    .expect("valid function source must compile");
    let rejected = source
        .get_cached_data()
        .is_some_and(v8::script_compiler::CachedData::rejected);
    (function, rejected)
}

fn function_compile_cold(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let sequence = Cell::new(0_u64);

    c.bench_function("function/compile_cold", |b| {
        common::banner();
        b.iter_batched(
            || {
                let index = sequence.get();
                sequence.set(index + 1);
                format!("return left * 10 + right; // {index}")
            },
            |source_text| {
                v8::scope!(let inner, scope);
                let (function, rejected) = compile_function(
                    inner,
                    &source_text,
                    None,
                    v8::script_compiler::CompileOptions::NoCompileOptions,
                );
                assert!(!rejected);
                black_box(function);
            },
            BatchSize::SmallInput,
        )
    });
}

fn produce_cache_bytes() -> Vec<u8> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let (function, rejected) = compile_function(
        scope,
        SOURCE,
        None,
        v8::script_compiler::CompileOptions::NoCompileOptions,
    );
    assert!(!rejected);
    function
        .create_code_cache()
        .expect("compiled function must produce code cache")
        .iter()
        .copied()
        .collect()
}

fn function_code_cache_consume(c: &mut Criterion) {
    oracle::ensure_v8();
    let cache_bytes = produce_cache_bytes();

    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Match the Go benchmark's untimed correctness probe before measurement.
    {
        v8::scope!(let check_scope, scope);
        let cached_data = v8::script_compiler::CachedData::new(&cache_bytes);
        let (function, rejected) = compile_function(
            check_scope,
            SOURCE,
            Some(cached_data),
            v8::script_compiler::CompileOptions::ConsumeCodeCache,
        );
        assert!(!rejected);
        let left: v8::Local<v8::Value> = v8::Integer::new(check_scope, 4).into();
        let right: v8::Local<v8::Value> = v8::Integer::new(check_scope, 2).into();
        let result = function
            .call(
                check_scope,
                v8::undefined(check_scope).into(),
                &[left, right],
            )
            .expect("compiled function call must succeed");
        assert_eq!(result.integer_value(check_scope), Some(42));
    }

    c.bench_function("function/code_cache_consume", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let cached_data = v8::script_compiler::CachedData::new(&cache_bytes);
            let (function, rejected) = compile_function(
                inner,
                SOURCE,
                Some(cached_data),
                v8::script_compiler::CompileOptions::ConsumeCodeCache,
            );
            assert!(!rejected);
            black_box(function);
        })
    });
}

criterion_group! {
    name = function_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = function_compile_cold, function_code_cache_consume
}

criterion_main!(function_benches);
