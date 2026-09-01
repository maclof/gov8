//! Process-isolated SyntheticModule misuse and callback-boundary probes.

use std::io::Write as _;

fn noop<'s>(
    _context: v8::Local<'s, v8::Context>,
    _module: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Value>> {
    None
}

#[allow(clippy::unnecessary_wraps)]
fn complete<'s>(
    context: v8::Local<'s, v8::Context>,
    _module: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Value>> {
    v8::callback_scope!(unsafe scope, context);
    Some(v8::undefined(scope).into())
}

fn panics<'s>(
    _context: v8::Local<'s, v8::Context>,
    _module: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Value>> {
    panic!("synthetic callback panic")
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

fn make<'s>(
    scope: &v8::PinScope<'s, '_>,
    duplicate: bool,
    panic_callback: bool,
) -> v8::Local<'s, v8::Module> {
    let name = v8::String::new(scope, "negative-synthetic").unwrap();
    let a = v8::String::new(scope, "a").unwrap();
    let b = if duplicate {
        v8::String::new(scope, "a").unwrap()
    } else {
        v8::String::new(scope, "b").unwrap()
    };
    if panic_callback {
        v8::Module::create_synthetic_module(scope, name, &[a, b], panics)
    } else if duplicate {
        v8::Module::create_synthetic_module(scope, name, &[a, b], complete)
    } else {
        v8::Module::create_synthetic_module(scope, name, &[a, b], noop)
    }
}

fn main() {
    let mode = std::env::args().nth(1).expect("mode");
    oracle::ensure_v8();
    if mode == "cross-isolate" {
        let global = {
            let isolate = &mut v8::Isolate::new(Default::default());
            v8::scope!(let scope, isolate);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            let module = make(scope, false, false);
            v8::Global::new(scope, module)
        };
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let _wrong = v8::Local::new(scope, &global);
        println!("survived");
        return;
    }

    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = make(scope, mode == "duplicate", mode == "panic");
    println!("created");
    std::io::stdout().flush().unwrap();
    module.instantiate_module(scope, no_resolve).unwrap();
    println!("instantiated");
    std::io::stdout().flush().unwrap();
    v8::tc_scope!(let tc, scope);
    let result = module.evaluate(tc);
    println!(
        "some={} caught={} status={:?}",
        result.is_some(),
        tc.has_caught(),
        module.get_status()
    );
}
