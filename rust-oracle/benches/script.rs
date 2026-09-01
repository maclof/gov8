//! Script compilation and execution benchmarks.
//! See benches/common/mod.rs for the methodology.
//!
//! Each iteration opens a fresh nested `HandleScope` so local handles do not
//! accumulate across iterations; the isolate and context are created once
//! per benchmark to isolate script work from isolate startup cost (isolate
//! startup is measured separately in benches/startup.rs).

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};

const MINIMAL_SOURCE: &str = "1 + 1";
const WORKLOAD_SOURCE: &str = concat!(
    "function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }",
    "fib(12) + '|' + (2 + 3) + '|' + String(1.5).toUpperCase()"
);

fn script_compile_minimal(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("script/compile_minimal", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let source = v8::String::new(inner, MINIMAL_SOURCE).unwrap();
            let _ = v8::Script::compile(inner, source, None);
        })
    });
}

fn script_compile_workload(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("script/compile_workload", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let source = v8::String::new(inner, WORKLOAD_SOURCE).unwrap();
            let _ = v8::Script::compile(inner, source, None);
        })
    });
}

fn script_compile_and_run_minimal(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("script/compile_and_run_minimal", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let source = v8::String::new(inner, MINIMAL_SOURCE).unwrap();
            let script = v8::Script::compile(inner, source, None).unwrap();
            let _ = script.run(inner);
        })
    });
}

fn script_compile_and_run_workload(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("script/compile_and_run_workload", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let source = v8::String::new(inner, WORKLOAD_SOURCE).unwrap();
            let script = v8::Script::compile(inner, source, None).unwrap();
            let _ = script.run(inner);
        })
    });
}

fn script_run_precompiled_workload(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    // Compile once and root the script in a Global so only execution is
    // measured per iteration.
    let compiled = {
        let source = v8::String::new(scope, WORKLOAD_SOURCE).unwrap();
        v8::Global::new(scope, v8::Script::compile(scope, source, None).unwrap())
    };
    c.bench_function("script/run_precompiled_workload", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let script = v8::Local::new(inner, &compiled);
            let _ = script.run(inner);
        })
    });
}

criterion_group! {
    name = script_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = script_compile_minimal, script_compile_workload,
        script_compile_and_run_minimal, script_compile_and_run_workload,
        script_run_precompiled_workload
}

criterion_main!(script_benches);
