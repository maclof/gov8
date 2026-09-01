//! Residual SourceTextModule code-cache and UnboundModuleScript oracle.

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

const CODE: &str =
    "export const answer = 42;\n//# sourceURL=virtual.mjs\n//# sourceMappingURL=virtual.map";

fn origin<'s>(scope: &v8::PinScope<'s, '_>, name: &str) -> v8::ScriptOrigin<'s> {
    let name = v8::String::new(scope, name).unwrap().into();
    v8::ScriptOrigin::new(scope, name, 0, 0, false, 0, None, false, false, true, None)
}

fn compile<'s>(
    scope: &v8::PinScope<'s, '_>,
    code: &str,
    name: &str,
    cache: Option<v8::UniqueRef<v8::CachedData<'_>>>,
) -> (v8::Local<'s, v8::Module>, bool) {
    let text = v8::String::new(scope, code).unwrap();
    let origin = origin(scope, name);
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
    .unwrap();
    let rejected = source
        .get_cached_data()
        .is_some_and(v8::script_compiler::CachedData::rejected);
    (module, rejected)
}

fn metadata_and_repeated_cache() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let (module, _) = compile(scope, CODE, "origin.mjs", None);
    let unbound = module.get_unbound_module_script(scope);
    let first = unbound.create_code_cache().unwrap().to_vec();
    let second = unbound.create_code_cache().unwrap().to_vec();
    let actual = Json::obj(vec![
        (
            "source_url",
            Json::s(&unbound.get_source_url(scope).to_rust_string_lossy(scope)),
        ),
        (
            "source_mapping_url",
            Json::s(
                &unbound
                    .get_source_mapping_url(scope)
                    .to_rust_string_lossy(scope),
            ),
        ),
        ("cache_non_empty", Json::b(!first.is_empty())),
        ("repeated_same_length", Json::b(first.len() == second.len())),
        ("repeated_same_bytes", Json::b(first == second)),
    ]);
    let expected = Json::obj(vec![
        ("source_url", Json::s("virtual.mjs")),
        ("source_mapping_url", Json::s("virtual.map")),
        ("cache_non_empty", Json::b(true)),
        ("repeated_same_length", Json::b(true)),
        ("repeated_same_bytes", Json::b(true)),
    ]);
    vec![expect_eq(
        "module-cache/unbound_metadata_and_repeated_cache",
        expected,
        actual,
    )]
}

#[allow(clippy::unnecessary_wraps)]
fn no_resolve<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _attrs: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    None
}

fn cross_isolate_roundtrip() -> Vec<CheckOutcome> {
    let bytes = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let (module, _) = compile(scope, CODE, "origin.mjs", None);
        module
            .get_unbound_module_script(scope)
            .create_code_cache()
            .unwrap()
            .to_vec()
    };
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let (module, rejected) = compile(scope, CODE, "origin.mjs", Some(v8::CachedData::new(&bytes)));
    let linked = module.instantiate_module(scope, no_resolve) == Some(true);
    let evaluated = module.evaluate(scope).is_some();
    scope.perform_microtask_checkpoint();
    let namespace = module.get_module_namespace().cast::<v8::Object>();
    let key = v8::String::new(scope, "answer").unwrap().into();
    let answer = namespace
        .get(scope, key)
        .and_then(|v| v.integer_value(scope))
        .unwrap_or(-1);
    let actual = Json::obj(vec![
        ("producer_dropped", Json::b(true)),
        ("cache_rejected", Json::b(rejected)),
        ("linked", Json::b(linked)),
        ("evaluated", Json::b(evaluated)),
        ("answer", Json::i(answer)),
    ]);
    let expected = Json::obj(vec![
        ("producer_dropped", Json::b(true)),
        ("cache_rejected", Json::b(false)),
        ("linked", Json::b(true)),
        ("evaluated", Json::b(true)),
        ("answer", Json::i(42)),
    ]);
    vec![expect_eq(
        "module-cache/cross_isolate_roundtrip",
        expected,
        actual,
    )]
}

fn changed_origin() -> Vec<CheckOutcome> {
    let bytes = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let (module, _) = compile(scope, CODE, "first.mjs", None);
        module
            .get_unbound_module_script(scope)
            .create_code_cache()
            .unwrap()
            .to_vec()
    };
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let (module, rejected) = compile(scope, CODE, "second.mjs", Some(v8::CachedData::new(&bytes)));
    let unbound = module.get_unbound_module_script(scope);
    let actual = Json::obj(vec![
        ("cache_rejected", Json::b(rejected)),
        (
            "source_url",
            Json::s(&unbound.get_source_url(scope).to_rust_string_lossy(scope)),
        ),
    ]);
    let expected = Json::obj(vec![
        ("cache_rejected", Json::b(false)),
        ("source_url", Json::s("virtual.mjs")),
    ]);
    vec![expect_eq("module-cache/changed_origin", expected, actual)]
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut checks = metadata_and_repeated_cache();
    checks.extend(cross_isolate_roundtrip());
    checks.extend(changed_origin());
    let passed = checks.iter().filter(|c| c.passed()).count();
    for check in &checks {
        println!("{}", check.to_line());
    }
    println!(
        "{}",
        summary_line(checks.len(), passed, checks.len() - passed)
    );
    if passed == checks.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
