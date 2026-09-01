//! Classic source-text ES module compile/link/evaluate benchmarks.
//! The isolate and context live across iterations; each measured operation
//! gets a fresh nested handle scope and fresh module graph.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};
use std::cell::RefCell;
use std::hint::black_box;

const DEPENDENCY_SPECIFIER: &str = DEPENDENCY_SOURCE;
const DEPENDENCY_SOURCE: &str = "export const base = 40;";
const ENTRY_SOURCE: &str =
    "import { base } from 'export const base = 40;'; export const answer = base + 2;";

thread_local! {
    static RESOLVED_MODULES: RefCell<Vec<v8::Global<v8::Module>>> = const { RefCell::new(Vec::new()) };
}

fn clear_resolved_modules() {
    RESOLVED_MODULES.with(|modules| modules.borrow_mut().clear());
}

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
    specifier: v8::Local<'s, v8::String>,
    import_attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    v8::callback_scope!(unsafe scope, context);
    assert_eq!(specifier.to_rust_string_lossy(scope), DEPENDENCY_SPECIFIER);
    assert_eq!(import_attributes.length(), 0);
    let dependency = compile(scope, "dependency.mjs", DEPENDENCY_SOURCE);
    let persistent = v8::Global::new(scope, dependency);
    let local = v8::Local::new(scope, &persistent);
    RESOLVED_MODULES.with(|modules| modules.borrow_mut().push(persistent));
    Some(local)
}

fn correctness_probe(scope: &mut v8::PinScope<'_, '_>) {
    clear_resolved_modules();
    v8::scope!(let inner, scope);
    let entry = v8::Global::new(inner, compile(inner, "entry.mjs", ENTRY_SOURCE));
    let module = v8::Local::new(inner, &entry);
    assert_eq!(module.instantiate_module(inner, resolve), Some(true));
    module.evaluate(inner).unwrap();
    inner.perform_microtask_checkpoint();
    let namespace = module.get_module_namespace().cast::<v8::Object>();
    let key = v8::String::new(inner, "answer").unwrap().into();
    let answer = namespace
        .get(inner, key)
        .and_then(|value| value.integer_value(inner));
    assert_eq!(answer, Some(42));
    clear_resolved_modules();
    drop(entry);
}

fn module_compile(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    correctness_probe(scope);
    c.bench_function("module/compile", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let module = v8::Global::new(inner, compile(inner, "entry.mjs", ENTRY_SOURCE));
            black_box(module);
        })
    });
}

fn module_compile_instantiate(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    correctness_probe(scope);
    c.bench_function("module/compile_instantiate", |b| {
        common::banner();
        b.iter(|| {
            clear_resolved_modules();
            v8::scope!(let inner, scope);
            let entry = v8::Global::new(inner, compile(inner, "entry.mjs", ENTRY_SOURCE));
            let module = v8::Local::new(inner, &entry);
            assert_eq!(module.instantiate_module(inner, resolve), Some(true));
            clear_resolved_modules();
            black_box(entry);
        })
    });
}

fn module_compile_instantiate_evaluate(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    correctness_probe(scope);
    c.bench_function("module/compile_instantiate_evaluate", |b| {
        common::banner();
        b.iter(|| {
            clear_resolved_modules();
            v8::scope!(let inner, scope);
            let entry = v8::Global::new(inner, compile(inner, "entry.mjs", ENTRY_SOURCE));
            let module = v8::Local::new(inner, &entry);
            assert_eq!(module.instantiate_module(inner, resolve), Some(true));
            black_box(module.evaluate(inner).unwrap());
            clear_resolved_modules();
            black_box(entry);
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
