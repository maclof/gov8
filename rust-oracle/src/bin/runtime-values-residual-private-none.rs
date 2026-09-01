//! Fatal subprocess probe for the pinned `Private::for_api(None)` boundary.

fn main() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _ = v8::Private::for_api(scope, None);
}
