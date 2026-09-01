//! Startup-shape benchmarks: isolate creation/disposal and context
//! creation/disposal. See benches/common/mod.rs for the methodology.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};

fn isolate_new_dispose(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    c.bench_function("startup/isolate_new_dispose", |b| {
        b.iter(|| {
            let isolate = v8::Isolate::new(Default::default());
            drop(isolate);
        })
    });
}

fn isolate_context_new_dispose(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    c.bench_function("startup/isolate_context_new_dispose", |b| {
        b.iter(|| {
            let isolate = &mut v8::Isolate::new(Default::default());
            v8::scope!(let scope, isolate);
            let context = v8::Context::new(scope, Default::default());
            let _ = context;
        })
    });
}

fn context_new_dispose(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    c.bench_function("startup/context_new_dispose", |b| {
        b.iter(|| {
            v8::scope!(let scope, isolate);
            let context = v8::Context::new(scope, Default::default());
            let _ = context;
        })
    });
}

criterion_group! {
    name = startup_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = isolate_new_dispose, isolate_context_new_dispose, context_new_dispose
}

criterion_main!(startup_benches);
