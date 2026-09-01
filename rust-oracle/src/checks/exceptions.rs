//! Exception checks: syntax errors at compile time and runtime throws,
//! observed through the crate's TryCatch API.

use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};

/// Runs `source` (compile + run) inside a fresh TryCatch and returns the
/// normalized observation.
#[allow(clippy::too_many_lines)]
fn observe<'s, 'i>(scope: &mut v8::PinScope<'s, 'i>, source: &str) -> ObservedException {
    let source_handle = v8::String::new(scope, source).unwrap();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let compiled = v8::Script::compile(tc, source_handle, None);
    let compile_ok = compiled.is_some();
    let ran = compiled.as_ref().and_then(|script| script.run(tc));
    let run_ok = ran.is_some();

    let has_caught = tc.has_caught();
    let can_continue = tc.can_continue();
    let message = tc.message().map(|m| m.get(tc).to_rust_string_lossy(tc));
    let exception_text = tc.exception().map(|e| harness::value_text(tc, e));
    let exception_is_string = tc.exception().map(|e| e.is_string()).unwrap_or(false);

    ObservedException {
        compile_ok,
        run_ok,
        has_caught,
        can_continue,
        message: message.unwrap_or_default(),
        exception_text: exception_text.unwrap_or_default(),
        exception_is_string,
    }
}

struct ObservedException {
    compile_ok: bool,
    run_ok: bool,
    has_caught: bool,
    can_continue: bool,
    message: String,
    exception_text: String,
    exception_is_string: bool,
}

impl ObservedException {
    fn to_json(&self) -> Json {
        Json::obj(vec![
            ("compile_ok", Json::b(self.compile_ok)),
            ("run_ok", Json::b(self.run_ok)),
            ("has_caught", Json::b(self.has_caught)),
            ("can_continue", Json::b(self.can_continue)),
            ("message", Json::s(&self.message)),
            ("exception_text", Json::s(&self.exception_text)),
            ("exception_is_string", Json::b(self.exception_is_string)),
        ])
    }

    fn expected(
        compile_ok: bool,
        run_ok: bool,
        has_caught: bool,
        can_continue: bool,
        message: &str,
        exception_text: &str,
        exception_is_string: bool,
    ) -> Json {
        Json::obj(vec![
            ("compile_ok", Json::b(compile_ok)),
            ("run_ok", Json::b(run_ok)),
            ("has_caught", Json::b(has_caught)),
            ("can_continue", Json::b(can_continue)),
            ("message", Json::s(message)),
            ("exception_text", Json::s(exception_text)),
            ("exception_is_string", Json::b(exception_is_string)),
        ])
    }
}

pub(crate) fn syntax_error_compile_fails() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let observed = observe(scope, "1 +");
    vec![expect_eq(
        "exceptions/syntax_error_compile_fails",
        // Message::get() carries the "Uncaught " prefix in this build even for
        // TryCatch-caught exceptions; ToString of the exception does not.
        ObservedException::expected(
            false,
            false,
            true,
            true,
            "Uncaught SyntaxError: Unexpected end of input",
            "SyntaxError: Unexpected end of input",
            false,
        ),
        observed.to_json(),
    )]
}

pub(crate) fn syntax_error_message_position() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let source = "const a = 1;\nconst const b = 2;\nconst c = 3;\n";
    let source_handle = v8::String::new(scope, source).unwrap();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let compiled = v8::Script::compile(tc, source_handle, None);
    let compile_ok = compiled.is_some();
    let (start, line, column, message) = tc
        .message()
        .map(|m| {
            (
                m.get_start_position(),
                m.get_line_number(tc),
                m.get_start_column(),
                m.get(tc).to_rust_string_lossy(tc),
            )
        })
        .unwrap_or((0, None, 0, String::new()));

    let actual = Json::obj(vec![
        ("compile_ok", Json::b(compile_ok)),
        ("start_position", Json::i(start as i64)),
        (
            "line_number",
            line.map(|l| Json::i(l as i64)).unwrap_or(Json::Null),
        ),
        ("start_column", Json::i(column as i64)),
        ("message", Json::s(&message)),
    ]);
    let expected = Json::obj(vec![
        ("compile_ok", Json::b(false)),
        ("start_position", Json::i(19)),
        ("line_number", Json::i(2)),
        ("start_column", Json::i(6)),
        (
            "message",
            Json::s("Uncaught SyntaxError: Unexpected token 'const'"),
        ),
    ]);
    vec![expect_eq(
        "exceptions/syntax_error_message_position",
        expected,
        actual,
    )]
}

pub(crate) fn runtime_reference_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let observed = observe(scope, "missing_thing();");
    vec![expect_eq(
        "exceptions/runtime_reference_error",
        ObservedException::expected(
            true,
            false,
            true,
            true,
            "Uncaught ReferenceError: missing_thing is not defined",
            "ReferenceError: missing_thing is not defined",
            false,
        ),
        observed.to_json(),
    )]
}

pub(crate) fn runtime_type_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let observed = observe(scope, "null.f();");
    vec![expect_eq(
        "exceptions/runtime_type_error",
        ObservedException::expected(
            true,
            false,
            true,
            true,
            "Uncaught TypeError: Cannot read properties of null (reading 'f')",
            "TypeError: Cannot read properties of null (reading 'f')",
            false,
        ),
        observed.to_json(),
    )]
}

pub(crate) fn throw_string() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let observed = observe(scope, "throw 'boom';");
    vec![expect_eq(
        "exceptions/throw_string",
        ObservedException::expected(true, false, true, true, "Uncaught boom", "boom", true),
        observed.to_json(),
    )]
}

pub(crate) fn throw_error_object() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let observed = observe(scope, "throw new Error('oops');");
    vec![expect_eq(
        "exceptions/throw_error_object",
        ObservedException::expected(
            true,
            false,
            true,
            true,
            "Uncaught Error: oops",
            "Error: oops",
            false,
        ),
        observed.to_json(),
    )]
}

pub(crate) fn trycatch_reset_allows_continue() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let caught_text;
    let caught_flag;
    let reset_flag;
    {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let _ = harness::eval(tc, "throw 'reset-me';");
        caught_flag = tc.has_caught();
        tc.reset();
        reset_flag = tc.has_caught();
        caught_text = tc
            .exception()
            .map(|e| harness::value_text(tc, e))
            .unwrap_or_default();
    }

    let after_reset = harness::eval_text(scope, "40 + 2");

    let actual = Json::obj(vec![
        ("caught_before_reset", Json::b(caught_flag)),
        ("caught_after_reset", Json::b(reset_flag)),
        ("exception_after_reset", Json::s(&caught_text)),
        (
            "next_script_value",
            Json::s(&after_reset.clone().unwrap_or_default()),
        ),
    ]);
    let expected = Json::obj(vec![
        ("caught_before_reset", Json::b(true)),
        ("caught_after_reset", Json::b(false)),
        ("exception_after_reset", Json::s("")),
        ("next_script_value", Json::s("42")),
    ]);
    vec![expect_eq(
        "exceptions/trycatch_reset_allows_continue",
        expected,
        actual,
    )]
}
