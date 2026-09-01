//! Comparative benchmarks for synchronous WebAssembly compile/rehydration.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion, Throughput};
use std::hint::black_box;

const ANSWER_MODULE: &[u8] = &[
    0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f, 0x03,
    0x02, 0x01, 0x00, 0x07, 0x07, 0x01, 0x03, b'r', b'u', b'n', 0x00, 0x00, 0x0a, 0x06, 0x01, 0x04,
    0x00, 0x41, 0x2a, 0x0b,
];

fn sync_compile(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    assert!(v8::WasmModuleObject::compile(scope, ANSWER_MODULE).is_some());
    let mut group = c.benchmark_group("wasm/sync_compile");
    group.throughput(Throughput::Bytes(ANSWER_MODULE.len() as u64));
    group.bench_function("answer_module", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            black_box(v8::WasmModuleObject::compile(inner, black_box(ANSWER_MODULE)).unwrap());
        })
    });
    group.finish();
}

fn from_compiled(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let compiled = v8::WasmModuleObject::compile(scope, ANSWER_MODULE)
        .unwrap()
        .get_compiled_module();
    assert!(v8::WasmModuleObject::from_compiled_module(scope, &compiled).is_some());
    c.bench_function("wasm/from_compiled/answer_module", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            black_box(v8::WasmModuleObject::from_compiled_module(inner, &compiled).unwrap());
        })
    });
}

criterion_group! {
    name = wasm_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = sync_compile, from_compiled
}

criterion_main!(wasm_benches);
