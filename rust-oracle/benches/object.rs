//! Object callback benchmarks.
//!
//! `object/lazy_data_property_first_read` matches Go's
//! `BenchmarkLazyDataPropertyFirstRead`: isolate and context setup are outside
//! the measurement; each iteration opens a fresh handle scope, creates an
//! object and String key, installs one lazy getter, performs the first read,
//! and asserts that the callback produced 42.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};

fn lazy_getter(
    _scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    rv.set_int32(42);
}

fn run_first_read(scope: &mut v8::PinScope<'_, '_>) {
    let object = v8::Object::new(scope);
    let key = v8::String::new(scope, "lazy").unwrap();
    assert_eq!(
        object.set_lazy_data_property(scope, key.into(), lazy_getter),
        Some(true),
        "lazy property installation failed"
    );
    let value = object
        .get(scope, key.into())
        .expect("lazy property first read failed");
    assert_eq!(
        value.int32_value(scope),
        Some(42),
        "lazy getter produced the wrong value"
    );
}

fn lazy_data_property_first_read(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Untimed correctness probe, mirrored by the Go benchmark.
    {
        v8::scope!(let inner, scope);
        run_first_read(inner);
    }

    c.bench_function("object/lazy_data_property_first_read", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            run_first_read(inner);
        })
    });
}

criterion_group! {
    name = object_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = lazy_data_property_first_read
}

criterion_main!(object_benches);
