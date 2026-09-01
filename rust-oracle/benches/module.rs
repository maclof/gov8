//! Classic source-text ES module compile/link/evaluate benchmarks.
//! The isolate and context live across iterations; each measured operation
//! gets a fresh nested handle scope and fresh module graph.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};
use std::hint::black_box;

const DEPENDENCY_SOURCE: &str = "export const base = 40;";
const ENTRY_SOURCE: &str =
    "import { base } from 'export const base = 40;'; export const answer = base + 2;";

fn origin<'s>(scope: &v8::PinScope<'s, '_>, name: &str) -> v8::ScriptOrigin<'s> {
    let resource_name = v8::String::new(scope, name).unwrap().into();
    v8::ScriptOrigin::new(
        scope,
        resource_name,
        0,
        0,
        false,
        -1,
        None,
        false,
        false,
        true,
        None,
    )
}

fn compile<'s>(scope: &v8::PinScope<'s, '_>, name: &str, text: &str) -> v8::Local<'s, v8::Module> {
    let text = v8::String::new(scope, text).unwrap();
    let origin = origin(scope, name);
    let mut source = v8::script_compiler::Source::new(text, Some(&origin));
    v8::script_compiler::compile_module(scope, &mut source).unwrap()
}

#[allow(clippy::unnecessary_wraps)]
fn resolve<'s>(
    context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _import_attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    v8::callback_scope!(unsafe scope, context);
    Some(compile(scope, "dependency.mjs", DEPENDENCY_SOURCE))
}

fn module_compile(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("module/compile", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            black_box(compile(inner, "entry.mjs", ENTRY_SOURCE));
        })
    });
}

fn module_compile_instantiate(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("module/compile_instantiate", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let module = compile(inner, "entry.mjs", ENTRY_SOURCE);
            black_box(module.instantiate_module(inner, resolve).unwrap());
        })
    });
}

fn module_compile_instantiate_evaluate(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    c.bench_function("module/compile_instantiate_evaluate", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let module = compile(inner, "entry.mjs", ENTRY_SOURCE);
            module.instantiate_module(inner, resolve).unwrap();
            black_box(module.evaluate(inner).unwrap());
        })
    });
}

criterion_group! {
    name = module_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = module_compile, module_compile_instantiate,
        module_compile_instantiate_evaluate
}

criterion_main!(module_benches);
