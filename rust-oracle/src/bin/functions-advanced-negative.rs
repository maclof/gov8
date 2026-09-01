//! Subprocess-only fatal-boundary probes for `Function::create_code_cache`.
//! V8 requires a function returned directly by `ScriptCompiler::compile_function`;
//! passing any other public `Function` kind terminates the process through the
//! V8 API-check boundary.

fn noop(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
}

fn produce_cache() -> Vec<u8> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let text = v8::String::new(scope, "return left * 10 + right;").unwrap();
    let mut source = v8::script_compiler::Source::new(text, None);
    let left = v8::String::new(scope, "left").unwrap();
    let right = v8::String::new(scope, "right").unwrap();
    v8::script_compiler::compile_function(
        scope,
        &mut source,
        &[left, right],
        &[],
        v8::script_compiler::CompileOptions::NoCompileOptions,
        v8::script_compiler::NoCacheReason::NoReason,
    )
    .unwrap()
    .create_code_cache()
    .unwrap()
    .iter()
    .copied()
    .collect()
}

fn cache_mismatch(mode: &str) {
    let mut cache = produce_cache();
    if mode == "cache-truncated" {
        cache.truncate(cache.len() / 2);
    } else if mode == "cache-corrupt" {
        let middle = cache.len() / 2;
        cache[middle] ^= 0xff;
    }
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let source_text = if mode == "cache-source" {
        "return left * 10 + right + 1;"
    } else {
        "return left * 10 + right;"
    };
    let text = v8::String::new(scope, source_text).unwrap();
    let cached = v8::script_compiler::CachedData::new(&cache);
    let mut source = v8::script_compiler::Source::new_with_cached_data(text, None, cached);
    let parameter_names: &[&str] = match mode {
        "cache-parameter-names" => &["x", "y"],
        "cache-parameter-count" => &["left"],
        _ => &["left", "right"],
    };
    let parameters: Vec<_> = parameter_names
        .iter()
        .map(|name| v8::String::new(scope, name).unwrap())
        .collect();
    let function = v8::script_compiler::compile_function(
        scope,
        &mut source,
        &parameters,
        &[],
        v8::script_compiler::CompileOptions::ConsumeCodeCache,
        v8::script_compiler::NoCacheReason::NoReason,
    );
    let rejected = source.get_cached_data().unwrap().rejected();
    let (length, call_value) = function.map_or((-1, -1), |function| {
        let length_key: v8::Local<v8::Value> = v8::String::new(scope, "length").unwrap().into();
        let length = function
            .get(scope, length_key)
            .unwrap()
            .integer_value(scope)
            .unwrap();
        let result = function
            .call(
                scope,
                v8::undefined(scope).into(),
                &[
                    v8::Integer::new(scope, 4).into(),
                    v8::Integer::new(scope, 2).into(),
                ],
            )
            .and_then(|value| value.integer_value(scope))
            .unwrap_or(-1);
        (length, result)
    });
    println!(
        "compiled={} rejected={rejected} length={length} call={call_value}",
        function.is_some()
    );
}

fn builder_length(length: i32) {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let function = v8::Function::builder(noop).length(length).build(scope);
    let observed = function.and_then(|function| {
        let key: v8::Local<v8::Value> = v8::String::new(scope, "length").unwrap().into();
        function.get(scope, key)?.integer_value(scope)
    });
    println!("built={} observed={observed:?}", function.is_some());
}

fn main() {
    let mode = std::env::args().nth(1).expect("negative mode");
    oracle::ensure_v8();
    if mode.starts_with("cache-") {
        cache_mismatch(&mode);
        return;
    }
    if mode == "length-negative" {
        builder_length(-1);
        return;
    }
    if mode == "length-large" {
        builder_length(i32::MAX);
        return;
    }
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let function = match mode.as_str() {
        "native" => v8::Function::new(scope, noop).unwrap(),
        "script" => {
            let source = v8::String::new(scope, "(function ordinary(){ return 1; })").unwrap();
            let value = v8::Script::compile(scope, source, None)
                .unwrap()
                .run(scope)
                .unwrap();
            v8::Local::<v8::Function>::try_from(value).unwrap()
        }
        "bound" => {
            let source = v8::String::new(scope, "(function target(){}).bind(null)").unwrap();
            let value = v8::Script::compile(scope, source, None)
                .unwrap()
                .run(scope)
                .unwrap();
            v8::Local::<v8::Function>::try_from(value).unwrap()
        }
        other => panic!("unknown negative mode: {other}"),
    };

    let _must_abort = function.create_code_cache();
    panic!("Function::create_code_cache unexpectedly returned");
}
