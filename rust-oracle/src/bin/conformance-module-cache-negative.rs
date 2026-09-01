//! Subprocess probes for malformed/mismatched module code cache.

const CODE: &str = "export const answer = 42;";

#[allow(clippy::unnecessary_wraps)]
fn no_resolve<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _attrs: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    None
}

fn origin<'s>(scope: &v8::PinScope<'s, '_>, name: &str) -> v8::ScriptOrigin<'s> {
    let name = v8::String::new(scope, name).unwrap().into();
    v8::ScriptOrigin::new(scope, name, 0, 0, false, 0, None, false, false, true, None)
}

fn main() {
    let mode = std::env::args().nth(1).expect("mode");
    oracle::ensure_v8();
    let mut bytes = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let text = v8::String::new(scope, CODE).unwrap();
        let origin = origin(scope, "producer.mjs");
        let mut source = v8::script_compiler::Source::new(text, Some(&origin));
        let module = v8::script_compiler::compile_module(scope, &mut source).unwrap();
        module
            .get_unbound_module_script(scope)
            .create_code_cache()
            .unwrap()
            .to_vec()
    };
    match mode.as_str() {
        "truncated" => bytes.truncate(bytes.len() / 2),
        "corrupt" => {
            let middle = bytes.len() / 2;
            bytes[middle] ^= 0xff;
        }
        "changed-source" => {}
        _ => panic!("unknown mode"),
    }
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let code = if mode == "changed-source" {
        "export const answer = 43;"
    } else {
        CODE
    };
    let text = v8::String::new(scope, code).unwrap();
    let origin = origin(scope, "consumer.mjs");
    let cached = v8::CachedData::new(&bytes);
    let mut source = v8::script_compiler::Source::new_with_cached_data(text, Some(&origin), cached);
    let module = v8::script_compiler::compile_module2(
        scope,
        &mut source,
        v8::script_compiler::CompileOptions::ConsumeCodeCache,
        v8::script_compiler::NoCacheReason::NoReason,
    );
    let rejected = source.get_cached_data().unwrap().rejected();
    let answer = module.map_or(-1, |module| {
        module.instantiate_module(scope, no_resolve).unwrap();
        module.evaluate(scope).unwrap();
        scope.perform_microtask_checkpoint();
        let namespace = module.get_module_namespace().cast::<v8::Object>();
        let key = v8::String::new(scope, "answer").unwrap().into();
        namespace
            .get(scope, key)
            .and_then(|value| value.integer_value(scope))
            .unwrap_or(-1)
    });
    println!(
        "compiled={} rejected={rejected} answer={answer}",
        module.is_some()
    );
}
