//! Small helpers shared by conformance checks. Everything here runs a fixed
//! compile/run path so that every check exercises the same API sequence.

use v8::{Local, PinScope, Script, Value};

/// Compiles `source` in the current context. Returns `None` on syntax error
/// (the exception is left in the isolate's TryCatch, if one is active).
pub(crate) fn compile<'s>(scope: &PinScope<'s, '_>, source: &str) -> Option<Local<'s, Script>> {
    let src = v8::String::new(scope, source)?;
    Script::compile(scope, src, None)
}

/// Compiles and runs `source`, returning the completion value.
pub(crate) fn eval<'s>(scope: &PinScope<'s, '_>, source: &str) -> Option<Local<'s, Value>> {
    compile(scope, source)?.run(scope)
}

/// Compiles and runs `source`, returning the ECMAScript ToString of the
/// completion value (empty string when the script failed).
pub(crate) fn eval_text(scope: &PinScope<'_, '_>, source: &str) -> Option<String> {
    let value = eval(scope, source)?;
    let text = value.to_string(scope)?;
    Some(text.to_rust_string_lossy(scope))
}

/// ToString of a value, as a Rust string ("" when conversion fails).
pub(crate) fn value_text(scope: &PinScope<'_, '_>, value: Local<'_, Value>) -> String {
    value
        .to_string(scope)
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}
