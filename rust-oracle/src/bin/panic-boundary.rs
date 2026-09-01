//! Dedicated characterization executable for the Rust panic boundary of
//! native callbacks.
//!
//! A native callback runs inside the crate's `extern "C"` trampoline
//! (`FunctionCallback`). Since rustc 1.81, a panic that would unwind out of
//! an `extern "C"` function aborts the process. This binary installs a
//! panicking callback, calls it, and emits markers on stderr so the
//! out-of-process assertion in `tests/callback_panic_boundary.rs` can pin:
//! - the callback was entered,
//! - the panic message was emitted by the default hook,
//! - the host code after the call never ran.
//!
//! The exit/termination status itself is asserted by the test. This must
//! stay its own process: the abort takes the whole process down.

fn cb_panic(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    eprintln!("marker:callback-entered");
    panic!("host-callback-panic");
    // Unreachable: unwinding out of the extern "C" trampoline aborts.
}

fn main() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    eprintln!("marker:before-call");
    let f = v8::Function::new(scope, cb_panic).expect("function created");
    let result = f.call(scope, v8::undefined(scope).into(), &[]);
    // If this line is ever reached, the panic boundary is catchable and the
    // out-of-process test fails loudly.
    eprintln!("marker:after-call result_is_none={}", result.is_none());
}
