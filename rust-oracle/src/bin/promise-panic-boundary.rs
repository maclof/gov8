//! Out-of-process probes for promise callback panic boundaries.
//!
//! Both a native promise reaction and a promise-reject callback cross an
//! `extern "C"` boundary in the pinned `v8` crate. A panic may not unwind
//! through either boundary, so each probe terminates the process before its
//! post-callback marker. See `tests/promise_panic_boundary.rs`.

fn native_promise_handler(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    eprintln!("marker:promise-native-entered");
    panic!("promise-native-handler-panic");
}

unsafe extern "C" fn promise_reject_callback(_message: v8::PromiseRejectMessage<'_>) {
    eprintln!("marker:promise-reject-entered");
    panic!("promise-reject-callback-panic");
}

fn probe_native_handler() {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let resolver = v8::PromiseResolver::new(scope).expect("resolver created");
    let promise = resolver.get_promise(scope);
    let handler = v8::Function::new(scope, native_promise_handler).expect("handler created");
    promise.then(scope, handler).expect("reaction attached");
    resolver
        .resolve(scope, v8::undefined(scope).into())
        .expect("promise resolved");

    eprintln!("marker:promise-native-before-checkpoint");
    scope.perform_microtask_checkpoint();
    eprintln!("marker:promise-native-after-checkpoint");
}

fn probe_reject_callback() {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_promise_reject_callback(promise_reject_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let resolver = v8::PromiseResolver::new(scope).expect("resolver created");
    eprintln!("marker:promise-reject-before-reject");
    resolver
        .reject(scope, v8::undefined(scope).into())
        .expect("promise rejected");
    eprintln!("marker:promise-reject-after-reject");
}

fn main() {
    oracle::ensure_v8();
    match std::env::args().nth(1).as_deref() {
        Some("native-handler") => probe_native_handler(),
        Some("reject-callback") => probe_reject_callback(),
        mode => panic!("unknown promise panic probe: {mode:?}"),
    }
}
