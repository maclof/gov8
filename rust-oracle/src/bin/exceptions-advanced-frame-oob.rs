//! Subprocess-only probe for rusty_v8's unchecked StackTrace::get_frame edge.
//!
//! `get_frame(scope, trace.get_frame_count())` unexpectedly returns `Some`
//! in the pinned release build.  Dereferencing that handle may cross an
//! invalid native boundary, so this executable must only be launched by a
//! parent process that inspects its termination status.

use std::io::Write as _;

fn checkpoint(value: &str) {
    let mut stdout = std::io::stdout().lock();
    let _ = writeln!(stdout, "{value}");
    let _ = stdout.flush();
}

fn probe_callback(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let trace = v8::StackTrace::current_stack_trace(scope, 16).expect("current stack trace");
    let count = trace.get_frame_count();
    checkpoint(&format!("frame_count={count}"));

    let frame = trace
        .get_frame(scope, count)
        .expect("index equal to frame count unexpectedly returned None");
    checkpoint("index_equal_count=some");

    let function = frame
        .get_function_name(scope)
        .map(|s| s.to_rust_string_lossy(scope));
    checkpoint(&format!("function_name={function:?}"));

    let line = frame.get_line_number();
    checkpoint(&format!("line={line}"));

    let script_id = frame.get_script_id();
    checkpoint(&format!("script_id={script_id}"));
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let host = v8::Function::builder(probe_callback)
        .build(scope)
        .expect("native probe function");
    let key = v8::String::new(scope, "host").unwrap();
    context
        .global(scope)
        .set(scope, key.into(), host.into())
        .expect("install host function");
    let source = v8::String::new(scope, "function caller(){ host(); } caller();").unwrap();
    let script = v8::Script::compile(scope, source, None).expect("compile probe script");
    let completed = script.run(scope).is_some();
    checkpoint(&format!("script_completed={completed}"));
    std::process::ExitCode::SUCCESS
}
