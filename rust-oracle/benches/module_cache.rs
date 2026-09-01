//! SourceTextModule code-cache production and consumption benchmarks.
//!
//! Platform, isolate and context setup are outside measurement. See
//! `benches/common/mod.rs` for shared Criterion methodology.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};
use std::hint::black_box;

const CODE: &str = "export const answer = 42;";

fn origin<'s>(scope: &v8::PinScope<'s, '_>) -> v8::ScriptOrigin<'s> {
    let name = v8::String::new(scope, "module-cache-bench.mjs")
        .unwrap()
        .into();
    v8::ScriptOrigin::new(scope, name, 0, 0, false, 0, None, false, false, true, None)
}

fn compile_module<'s>(
    scope: &v8::PinScope<'s, '_>,
    cache: Option<v8::UniqueRef<v8::CachedData<'_>>>,
) -> (v8::Local<'s, v8::Module>, bool) {
    let text = v8::String::new(scope, CODE).unwrap();
    let origin = origin(scope);
    let mut source = cache.map_or_else(
        || v8::script_compiler::Source::new(text, Some(&origin)),
        |cache| v8::script_compiler::Source::new_with_cached_data(text, Some(&origin), cache),
    );
    let options = if source.get_cached_data().is_some() {
        v8::script_compiler::CompileOptions::ConsumeCodeCache
    } else {
        v8::script_compiler::CompileOptions::NoCompileOptions
    };
    let module = v8::script_compiler::compile_module2(
        scope,
        &mut source,
        options,
        v8::script_compiler::NoCacheReason::NoReason,
    )
    .expect("valid module must compile");
    let rejected = source
        .get_cached_data()
        .is_some_and(v8::script_compiler::CachedData::rejected);
    (module, rejected)
}

fn module_cache_create(c: &mut Criterion) {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let (module, rejected) = compile_module(scope, None);
    assert!(!rejected);
    let unbound = module.get_unbound_module_script(scope);
    let rooted = v8::Global::new(scope, unbound);
    assert!(!unbound.create_code_cache().unwrap().is_empty());

    c.bench_function("module_cache/create_code_cache", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let unbound = v8::Local::new(inner, &rooted);
            let cache = unbound
                .create_code_cache()
                .expect("module cache production must succeed");
            assert!(!cache.is_empty());
            black_box(cache);
        })
    });
}

fn produce_cache_bytes() -> Vec<u8> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let (module, rejected) = compile_module(scope, None);
    assert!(!rejected);
    module
        .get_unbound_module_script(scope)
        .create_code_cache()
        .expect("module cache production must succeed")
        .to_vec()
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

fn module_cache_consume(c: &mut Criterion) {
    oracle::ensure_v8();
    // The producer isolate is fully dropped before the consumer is created.
    let bytes = produce_cache_bytes();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Untimed cross-isolate correctness probe, matching the Go benchmark.
    {
        v8::scope!(let check_scope, scope);
        let cache = v8::CachedData::new(&bytes);
        let (module, rejected) = compile_module(check_scope, Some(cache));
        assert!(!rejected);
        assert_eq!(
            module.instantiate_module(check_scope, no_resolve),
            Some(true)
        );
        module.evaluate(check_scope).unwrap();
        check_scope.perform_microtask_checkpoint();
        let namespace = module.get_module_namespace().cast::<v8::Object>();
        let key = v8::String::new(check_scope, "answer").unwrap().into();
        let answer = namespace
            .get(check_scope, key)
            .and_then(|value| value.integer_value(check_scope));
        assert_eq!(answer, Some(42));
    }

    c.bench_function("module_cache/consume_compile", |b| {
        common::banner();
        b.iter(|| {
            v8::scope!(let inner, scope);
            let cache = v8::CachedData::new(&bytes);
            let (module, rejected) = compile_module(inner, Some(cache));
            assert!(!rejected);
            black_box(module);
        })
    });
}

criterion_group! {
    name = module_cache_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = module_cache_create, module_cache_consume
}

criterion_main!(module_cache_benches);
