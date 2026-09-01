//! SyntheticModule creation and evaluation benchmarks.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};
use std::hint::black_box;

#[allow(clippy::unnecessary_wraps)]
fn evaluate<'s>(
    context: v8::Local<'s, v8::Context>,
    module: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Value>> {
    v8::callback_scope!(unsafe scope, context);
    let name = v8::String::new(scope, "answer").unwrap();
    let value = v8::Integer::new(scope, 42).into();
    module
        .set_synthetic_module_export(scope, name, value)
        .unwrap();
    Some(v8::undefined(scope).into())
}

#[allow(clippy::unnecessary_wraps)]
fn no_resolve<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    None
}

fn create<'s>(scope: &v8::PinScope<'s, '_>) -> v8::Local<'s, v8::Module> {
    let module_name = v8::String::new(scope, "benchmark-synthetic").unwrap();
    let answer = v8::String::new(scope, "answer").unwrap();
    v8::Module::create_synthetic_module(scope, module_name, &[answer], evaluate)
}

fn synthetic_create(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("modules_synthetic/create", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            black_box(create(inner));
        })
    });
}

fn synthetic_create_instantiate_evaluate(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("modules_synthetic/create_instantiate_evaluate", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let module = create(inner);
            assert_eq!(module.instantiate_module(inner, no_resolve), Some(true));
            black_box(module.evaluate(inner).unwrap());
        })
    });
}

criterion_group! {
    name = synthetic_module_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = synthetic_create, synthetic_create_instantiate_evaluate
}

criterion_main!(synthetic_module_benches);
