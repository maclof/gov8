//! Residual classic-script `ScriptOrigin` and `ScriptCompiler` conformance.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};

const CACHE_SOURCE: &str = "(function square(n) { return n * n; })(7) + 1";

static HOST_OPTIONS_SEEN: AtomicBool = AtomicBool::new(false);
static HOST_OPTIONS_LENGTH: AtomicI64 = AtomicI64::new(-1);
static HOST_OPTIONS_VALUE: AtomicI64 = AtomicI64::new(-1);

fn value_kind(value: v8::Local<v8::Value>) -> &'static str {
    if value.is_undefined() {
        "undefined"
    } else if value.is_string() {
        "string"
    } else if value.is_number() {
        "number"
    } else if value.is_object() {
        "object"
    } else {
        "other"
    }
}

// Mirrors the public `ScriptOrigin::new` fields while keeping call sites
// explicit about every cache-relevant origin dimension under test.
#[allow(clippy::too_many_arguments)]
fn make_origin<'s>(
    scope: &v8::PinScope<'s, '_>,
    name: &str,
    line: i32,
    column: i32,
    script_id: i32,
    source_map: Option<&str>,
    shared: bool,
    opaque: bool,
    is_wasm: bool,
) -> v8::ScriptOrigin<'s> {
    let resource_name: v8::Local<v8::Value> = v8::String::new(scope, name).unwrap().into();
    let source_map = source_map.map(|value| v8::String::new(scope, value).unwrap().into());
    v8::ScriptOrigin::new(
        scope,
        resource_name,
        line,
        column,
        shared,
        script_id,
        source_map,
        opaque,
        is_wasm,
        false,
        None,
    )
}

fn origin_arbitrary_values() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let name: v8::Local<v8::Value> = v8::Object::new(scope).into();
    let source_map: v8::Local<v8::Value> = v8::Number::new(scope, 17.5).into();
    let origin = v8::ScriptOrigin::new(
        scope,
        name,
        i32::MIN,
        i32::MAX,
        true,
        i32::MIN,
        Some(source_map),
        true,
        false,
        false,
        None,
    );
    let observed_name = origin.resource_name().unwrap();
    let observed_map = origin.source_map_url().unwrap();
    let undefined: v8::Local<v8::Value> = v8::undefined(scope).into();
    let default_origin = v8::ScriptOrigin::new(
        scope,
        undefined,
        0,
        0,
        false,
        i32::MAX,
        None,
        false,
        false,
        false,
        None,
    );
    let default_name = default_origin.resource_name().unwrap();
    let default_map = default_origin.source_map_url();

    let source_text = v8::String::new(scope, "6 * 7").unwrap();
    let mut source = v8::script_compiler::Source::new(source_text, Some(&origin));
    let compiled = v8::script_compiler::compile(
        scope,
        &mut source,
        v8::script_compiler::CompileOptions::NoCompileOptions,
        v8::script_compiler::NoCacheReason::NoReason,
    );
    let run_value = compiled
        .and_then(|script| script.run(scope))
        .and_then(|value| value.integer_value(scope));

    vec![pass(
        "script-compiler-residual/origin_arbitrary_values",
        Json::obj(vec![
            ("script_id", Json::i(i64::from(origin.script_id()))),
            (
                "resource_name_present",
                Json::b(origin.resource_name().is_some()),
            ),
            ("resource_name_kind", Json::s(value_kind(observed_name))),
            (
                "resource_name_same_value",
                Json::b(observed_name.same_value(name)),
            ),
            (
                "source_map_present",
                Json::b(origin.source_map_url().is_some()),
            ),
            ("source_map_kind", Json::s(value_kind(observed_map))),
            (
                "source_map_same_value",
                Json::b(observed_map.same_value(source_map)),
            ),
            (
                "undefined_resource_name_is_some_undefined",
                Json::b(default_name.is_undefined()),
            ),
            ("absent_source_map_is_none", Json::b(default_map.is_none())),
            (
                "maximum_script_id",
                Json::i(i64::from(default_origin.script_id())),
            ),
            ("compiled", Json::b(compiled.is_some())),
            ("run_value", Json::i(run_value.unwrap_or(-1))),
        ]),
    )]
}

fn host_defined_options() -> Vec<CheckOutcome> {
    HOST_OPTIONS_SEEN.store(false, Ordering::SeqCst);
    HOST_OPTIONS_LENGTH.store(-1, Ordering::SeqCst);
    HOST_OPTIONS_VALUE.store(-1, Ordering::SeqCst);

    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let callback = v8::Function::new(
        scope,
        |scope: &mut v8::PinScope, _: v8::FunctionCallbackArguments, mut rv: v8::ReturnValue| {
            HOST_OPTIONS_SEEN.store(true, Ordering::SeqCst);
            if let Some(data) = scope.get_current_host_defined_options() {
                // The exact public API type supplied in the origin is known here.
                let options = unsafe { v8::Local::<v8::PrimitiveArray>::cast_unchecked(data) };
                HOST_OPTIONS_LENGTH.store(options.length() as i64, Ordering::SeqCst);
                let value = options.get(scope, 0).integer_value(scope).unwrap_or(-1);
                HOST_OPTIONS_VALUE.store(value, Ordering::SeqCst);
                rv.set(v8::Number::new(scope, value as f64).into());
            }
        },
    )
    .unwrap();
    let key = v8::String::new(scope, "observeHostOptions").unwrap();
    context
        .global(scope)
        .set(scope, key.into(), callback.into());

    let options = v8::PrimitiveArray::new(scope, 2);
    options.set(scope, 0, v8::Integer::new(scope, 73).into());
    options.set(scope, 1, v8::String::new(scope, "meta").unwrap().into());
    let resource_name: v8::Local<v8::Value> =
        v8::String::new(scope, "host-options.js").unwrap().into();
    let origin = v8::ScriptOrigin::new(
        scope,
        resource_name,
        0,
        0,
        false,
        123,
        None,
        false,
        false,
        false,
        Some(options.into()),
    );
    let source_text = v8::String::new(scope, "observeHostOptions()").unwrap();
    let mut source = v8::script_compiler::Source::new(source_text, Some(&origin));
    let result = v8::script_compiler::compile(
        scope,
        &mut source,
        v8::script_compiler::CompileOptions::EagerCompile,
        v8::script_compiler::NoCacheReason::NoReason,
    )
    .and_then(|script| script.run(scope))
    .and_then(|value| value.integer_value(scope));
    let outside_run_absent = scope.get_current_host_defined_options().is_none();

    vec![pass(
        "script-compiler-residual/host_defined_options",
        Json::obj(vec![
            (
                "callback_seen",
                Json::b(HOST_OPTIONS_SEEN.load(Ordering::SeqCst)),
            ),
            (
                "length",
                Json::i(HOST_OPTIONS_LENGTH.load(Ordering::SeqCst)),
            ),
            (
                "first_value",
                Json::i(HOST_OPTIONS_VALUE.load(Ordering::SeqCst)),
            ),
            ("run_value", Json::i(result.unwrap_or(-1))),
            ("outside_run_absent", Json::b(outside_run_absent)),
        ]),
    )]
}

fn options_matrix() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let options = [
        (
            "none",
            v8::script_compiler::CompileOptions::NoCompileOptions,
        ),
        ("eager", v8::script_compiler::CompileOptions::EagerCompile),
        (
            "produce_hints",
            v8::script_compiler::CompileOptions::ProduceCompileHints,
        ),
        (
            "consume_hints",
            v8::script_compiler::CompileOptions::ConsumeCompileHints,
        ),
        (
            "follow_magic_comment",
            v8::script_compiler::CompileOptions::FollowCompileHintsMagicComment,
        ),
        (
            "follow_per_function_magic_comment",
            v8::script_compiler::CompileOptions::FollowCompileHintsPerFunctionMagicComment,
        ),
    ];
    let mut observations = Vec::new();
    for (name, option) in options {
        let source_text =
            v8::String::new(scope, "function hinted(a) { return a + 1; } hinted(41)").unwrap();
        let mut source = v8::script_compiler::Source::new(source_text, None);
        let script = v8::script_compiler::compile(
            scope,
            &mut source,
            option,
            v8::script_compiler::NoCacheReason::NoReason,
        );
        let run_value = script
            .and_then(|script| script.run(scope))
            .and_then(|value| value.integer_value(scope));
        observations.push(Json::obj(vec![
            ("name", Json::s(name)),
            ("bits", Json::i(i64::from(option.bits()))),
            ("compiled", Json::b(script.is_some())),
            ("run_value", Json::i(run_value.unwrap_or(-1))),
            (
                "cached_data_absent",
                Json::b(source.get_cached_data().is_none()),
            ),
        ]));
    }
    vec![pass(
        "script-compiler-residual/compile_options",
        Json::arr(observations),
    )]
}

fn no_cache_reason_matrix() -> Vec<CheckOutcome> {
    use v8::script_compiler::NoCacheReason;
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let reasons = [
        ("NoReason", NoCacheReason::NoReason),
        (
            "BecauseCachingDisabled",
            NoCacheReason::BecauseCachingDisabled,
        ),
        ("BecauseNoResource", NoCacheReason::BecauseNoResource),
        ("BecauseInlineScript", NoCacheReason::BecauseInlineScript),
        ("BecauseModule", NoCacheReason::BecauseModule),
        (
            "BecauseStreamingSource",
            NoCacheReason::BecauseStreamingSource,
        ),
        ("BecauseInspector", NoCacheReason::BecauseInspector),
        (
            "BecauseScriptTooSmall",
            NoCacheReason::BecauseScriptTooSmall,
        ),
        ("BecauseCacheTooCold", NoCacheReason::BecauseCacheTooCold),
        ("BecauseV8Extension", NoCacheReason::BecauseV8Extension),
        (
            "BecauseExtensionModule",
            NoCacheReason::BecauseExtensionModule,
        ),
        ("BecausePacScript", NoCacheReason::BecausePacScript),
        (
            "BecauseInDocumentWrite",
            NoCacheReason::BecauseInDocumentWrite,
        ),
        (
            "BecauseResourceWithNoCacheHandler",
            NoCacheReason::BecauseResourceWithNoCacheHandler,
        ),
        (
            "BecauseDeferredProduceCodeCache",
            NoCacheReason::BecauseDeferredProduceCodeCache,
        ),
    ];
    let mut observations = Vec::new();
    for (name, reason) in reasons {
        // SAFETY: `NoCacheReason` is explicitly `#[repr(C)]`; reading its
        // discriminant as the target C ABI's 32-bit enum representation is
        // precisely the FFI value passed to V8 and does not move `reason`.
        let discriminant = unsafe { std::ptr::from_ref(&reason).cast::<i32>().read() };
        let source_text = v8::String::new(scope, "20 + 22").unwrap();
        let mut source = v8::script_compiler::Source::new(source_text, None);
        let script = v8::script_compiler::compile(
            scope,
            &mut source,
            v8::script_compiler::CompileOptions::NoCompileOptions,
            reason,
        );
        let run_value = script
            .and_then(|script| script.run(scope))
            .and_then(|value| value.integer_value(scope));
        observations.push(Json::obj(vec![
            ("name", Json::s(name)),
            ("discriminant", Json::i(i64::from(discriminant))),
            ("compiled", Json::b(script.is_some())),
            ("run_value", Json::i(run_value.unwrap_or(-1))),
        ]));
    }
    vec![pass(
        "script-compiler-residual/no_cache_reasons",
        Json::arr(observations),
    )]
}

fn produce_cache() -> Vec<u8> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let origin = make_origin(
        scope,
        "cache-base.js",
        3,
        4,
        101,
        Some("base.map"),
        false,
        false,
        false,
    );
    let source_text = v8::String::new(scope, CACHE_SOURCE).unwrap();
    let mut source = v8::script_compiler::Source::new(source_text, Some(&origin));
    let unbound = v8::script_compiler::compile_unbound_script(
        scope,
        &mut source,
        v8::script_compiler::CompileOptions::NoCompileOptions,
        v8::script_compiler::NoCacheReason::NoReason,
    )
    .unwrap();
    unbound.create_code_cache().unwrap().to_vec()
}

#[derive(Clone, Copy)]
struct OriginConfig<'a> {
    name: &'a str,
    line: i32,
    column: i32,
    script_id: i32,
    source_map: Option<&'a str>,
    shared: bool,
    opaque: bool,
}

fn consume_cache(cache: &[u8], source_code: &str, config: Option<OriginConfig<'_>>) -> Json {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let origin = config.map(|config| {
        make_origin(
            scope,
            config.name,
            config.line,
            config.column,
            config.script_id,
            config.source_map,
            config.shared,
            config.opaque,
            false,
        )
    });
    let source_text = v8::String::new(scope, source_code).unwrap();
    let cached_data = v8::script_compiler::CachedData::new(cache);
    let before_rejected = cached_data.rejected();
    let input_len = cached_data.len();
    let mut source = v8::script_compiler::Source::new_with_cached_data(
        source_text,
        origin.as_ref(),
        cached_data,
    );
    let before_present = source.get_cached_data().is_some();
    let script = v8::script_compiler::compile(
        scope,
        &mut source,
        v8::script_compiler::CompileOptions::ConsumeCodeCache,
        v8::script_compiler::NoCacheReason::NoReason,
    );
    let rejected = source.get_cached_data().unwrap().rejected();
    let bytes_preserved = source.get_cached_data().unwrap().as_ref() == cache;
    let run_value = script
        .and_then(|script| script.run(scope))
        .and_then(|value| value.integer_value(scope));
    Json::obj(vec![
        ("input_len_positive", Json::b(input_len > 0)),
        ("before_present", Json::b(before_present)),
        ("before_rejected", Json::b(before_rejected)),
        ("compiled", Json::b(script.is_some())),
        ("after_rejected", Json::b(rejected)),
        ("bytes_preserved", Json::b(bytes_preserved)),
        ("run_value", Json::i(run_value.unwrap_or(-1))),
    ])
}

fn cache_origin_and_source_mismatch() -> Vec<CheckOutcome> {
    let cache = produce_cache();
    let base = OriginConfig {
        name: "cache-base.js",
        line: 3,
        column: 4,
        script_id: 101,
        source_map: Some("base.map"),
        shared: false,
        opaque: false,
    };
    let variants = [
        ("same", Some(base), CACHE_SOURCE),
        (
            "resource_name",
            Some(OriginConfig {
                name: "other.js",
                ..base
            }),
            CACHE_SOURCE,
        ),
        (
            "line",
            Some(OriginConfig { line: 30, ..base }),
            CACHE_SOURCE,
        ),
        (
            "column",
            Some(OriginConfig { column: 40, ..base }),
            CACHE_SOURCE,
        ),
        (
            "script_id",
            Some(OriginConfig {
                script_id: 202,
                ..base
            }),
            CACHE_SOURCE,
        ),
        (
            "source_map",
            Some(OriginConfig {
                source_map: Some("other.map"),
                ..base
            }),
            CACHE_SOURCE,
        ),
        (
            "origin_flags",
            Some(OriginConfig {
                shared: true,
                opaque: true,
                ..base
            }),
            CACHE_SOURCE,
        ),
        ("no_origin", None, CACHE_SOURCE),
        ("changed_source", Some(base), "40 + 3"),
    ];
    let mut observations = Vec::new();
    for (name, config, source) in variants {
        observations.push(Json::obj(vec![
            ("case", Json::s(name)),
            ("result", consume_cache(&cache, source, config)),
        ]));
    }
    vec![pass(
        "script-compiler-residual/cache_origin_source_mismatch",
        Json::obj(vec![
            ("cache_produced", Json::b(!cache.is_empty())),
            ("cases", Json::arr(observations)),
        ]),
    )]
}

fn syntax_failure_source_state() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let origin = make_origin(
        scope,
        "syntax.js",
        10,
        20,
        909,
        Some("syntax.map"),
        false,
        false,
        false,
    );
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let source_text = v8::String::new(tc, "let = ;").unwrap();
    let mut source = v8::script_compiler::Source::new(source_text, Some(&origin));
    let before_absent = source.get_cached_data().is_none();
    let script = v8::script_compiler::compile(
        tc,
        &mut source,
        v8::script_compiler::CompileOptions::EagerCompile,
        v8::script_compiler::NoCacheReason::BecauseNoResource,
    );
    let message = tc.message().unwrap();
    let exception = tc.exception().unwrap().to_rust_string_lossy(tc);
    vec![pass(
        "script-compiler-residual/syntax_failure_source_state",
        Json::obj(vec![
            ("compile_none", Json::b(script.is_none())),
            ("has_caught", Json::b(tc.has_caught())),
            ("exception", Json::s(&exception)),
            (
                "resource_name",
                Json::s(
                    &message
                        .get_script_resource_name(tc)
                        .unwrap()
                        .to_rust_string_lossy(tc),
                ),
            ),
            (
                "line_number",
                Json::i(message.get_line_number(tc).map_or(-1, |line| line as i64)),
            ),
            ("start_column", Json::i(message.get_start_column() as i64)),
            ("cache_absent_before", Json::b(before_absent)),
            (
                "cache_absent_after",
                Json::b(source.get_cached_data().is_none()),
            ),
        ]),
    )]
}

fn permissive_origin_and_option_boundaries() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let unknown = v8::script_compiler::CompileOptions::from_bits_retain(1 << 20);
    let unknown_source = v8::String::new(scope, "40 + 2").unwrap();
    let mut unknown_source = v8::script_compiler::Source::new(unknown_source, None);
    let unknown_script = v8::script_compiler::compile(
        scope,
        &mut unknown_source,
        unknown,
        v8::script_compiler::NoCacheReason::NoReason,
    );
    let unknown_result = unknown_script
        .and_then(|script| script.run(scope))
        .and_then(|value| value.integer_value(scope));

    let wasm_origin = make_origin(
        scope,
        "wasm-marked-classic.js",
        0,
        0,
        0,
        None,
        false,
        false,
        true,
    );
    let wasm_source = v8::String::new(scope, "6 * 7").unwrap();
    let mut wasm_source = v8::script_compiler::Source::new(wasm_source, Some(&wasm_origin));
    let wasm_script = v8::script_compiler::compile(
        scope,
        &mut wasm_source,
        v8::script_compiler::CompileOptions::NoCompileOptions,
        v8::script_compiler::NoCacheReason::NoReason,
    );
    let wasm_result = wasm_script
        .and_then(|script| script.run(scope))
        .and_then(|value| value.integer_value(scope));

    vec![pass(
        "script-compiler-residual/permissive_boundaries",
        Json::obj(vec![
            ("unknown_option_bits", Json::i(i64::from(unknown.bits()))),
            ("unknown_option_compiled", Json::b(unknown_script.is_some())),
            (
                "unknown_option_run_value",
                Json::i(unknown_result.unwrap_or(-1)),
            ),
            (
                "wasm_marked_classic_compiled",
                Json::b(wasm_script.is_some()),
            ),
            (
                "wasm_marked_classic_run_value",
                Json::i(wasm_result.unwrap_or(-1)),
            ),
        ]),
    )]
}

fn negative_probe(mode: &str) {
    oracle::ensure_v8();
    let cache = produce_cache();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let source_text = v8::String::new(scope, CACHE_SOURCE).unwrap();
    match mode {
        "consume-without-cache" => {
            let mut source = v8::script_compiler::Source::new(source_text, None);
            let script = v8::script_compiler::compile(
                scope,
                &mut source,
                v8::script_compiler::CompileOptions::ConsumeCodeCache,
                v8::script_compiler::NoCacheReason::NoReason,
            );
            println!("survived compiled={}", script.is_some());
        }
        "unknown-option" => {
            let mut source = v8::script_compiler::Source::new(source_text, None);
            let option = v8::script_compiler::CompileOptions::from_bits_retain(1 << 20);
            let script = v8::script_compiler::compile(
                scope,
                &mut source,
                option,
                v8::script_compiler::NoCacheReason::NoReason,
            );
            println!("survived compiled={}", script.is_some());
        }
        "is-wasm" => {
            let origin = make_origin(scope, "wasm-origin.js", 0, 0, 0, None, false, false, true);
            let mut source = v8::script_compiler::Source::new(source_text, Some(&origin));
            let script = v8::script_compiler::compile(
                scope,
                &mut source,
                v8::script_compiler::CompileOptions::NoCompileOptions,
                v8::script_compiler::NoCacheReason::NoReason,
            );
            println!("survived compiled={}", script.is_some());
        }
        "empty-cache" | "truncated-cache" | "corrupt-cache" => {
            let bytes: &[u8] = match mode {
                "empty-cache" => &[],
                "truncated-cache" => &cache[..cache.len() / 2],
                "corrupt-cache" => {
                    let middle = cache.len() / 2;
                    let corrupt = Box::leak(cache.into_boxed_slice());
                    corrupt[middle] ^= 0xff;
                    corrupt
                }
                _ => unreachable!(),
            };
            let cached_data = v8::script_compiler::CachedData::new(bytes);
            let mut source =
                v8::script_compiler::Source::new_with_cached_data(source_text, None, cached_data);
            let script = v8::script_compiler::compile(
                scope,
                &mut source,
                v8::script_compiler::CompileOptions::ConsumeCodeCache,
                v8::script_compiler::NoCacheReason::NoReason,
            );
            let run_value = script
                .and_then(|script| script.run(scope))
                .and_then(|value| value.integer_value(scope));
            println!(
                "survived compiled={} rejected={} run_value={}",
                script.is_some(),
                source.get_cached_data().unwrap().rejected(),
                run_value.unwrap_or(-1)
            );
        }
        _ => panic!("unknown negative probe {mode}"),
    }
}

fn run() {
    oracle::ensure_v8();
    let checks = [
        origin_arbitrary_values,
        host_defined_options,
        options_matrix,
        no_cache_reason_matrix,
        cache_origin_and_source_mismatch,
        syntax_failure_source_state,
        permissive_origin_and_option_boundaries,
    ];
    let outcomes: Vec<_> = checks.into_iter().flat_map(|check| check()).collect();
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(outcomes.len(), outcomes.len(), 0));
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    match args.as_slice() {
        [_] => run(),
        [_, flag, mode] if flag == "--negative" => negative_probe(mode),
        _ => panic!("usage: conformance-script-compiler-residual [--negative MODE]"),
    }
}
