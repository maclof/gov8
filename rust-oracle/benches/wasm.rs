//! Comparative benchmarks for synchronous WebAssembly compile/rehydration.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion, Throughput};
use std::cell::Cell;
use std::hint::black_box;
use std::rc::Rc;

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

fn pump_until_resolved(scope: &mut v8::PinScope<'_, '_>, outcome: &Rc<Cell<Option<bool>>>) {
    while outcome.get().is_none() {
        let pumped = v8::Platform::pump_message_loop(&v8::V8::get_current_platform(), scope, false);
        if !pumped {
            std::hint::spin_loop();
        }
    }
}

fn module_compilation(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let probe = Rc::new(Cell::new(None));
    let probe_callback = Rc::clone(&probe);
    let mut compilation = v8::WasmModuleCompilation::new();
    compilation.on_bytes_received(ANSWER_MODULE);
    compilation.finish(scope, None, move |_, result| {
        probe_callback.set(Some(result.is_ok()));
    });
    pump_until_resolved(scope, &probe);
    assert_eq!(probe.get(), Some(true));

    let mut group = c.benchmark_group("wasm/module_compilation");
    group.throughput(Throughput::Bytes(ANSWER_MODULE.len() as u64));
    group.bench_function("answer_module", |b| {
        common::banner();
        b.iter(|| {
            let outcome = Rc::new(Cell::new(None));
            let callback_outcome = Rc::clone(&outcome);
            let mut compilation = v8::WasmModuleCompilation::new();
            compilation.on_bytes_received(black_box(ANSWER_MODULE));
            compilation.finish(scope, None, move |_, result| {
                callback_outcome.set(Some(result.is_ok()));
            });
            pump_until_resolved(scope, &outcome);
            black_box(outcome.get())
        })
    });
    group.finish();
}

criterion_group! {
    name = wasm_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = sync_compile, from_compiled, module_compilation
}

criterion_main!(wasm_benches);
