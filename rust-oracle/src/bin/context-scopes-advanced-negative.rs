//! Subprocess-only probes for JavaScript execution disallow failure modes.

use std::io::Write as _;

fn checkpoint(text: &str) {
    let mut stdout = std::io::stdout().lock();
    let _ = writeln!(stdout, "{text}");
    let _ = stdout.flush();
}

fn main() -> std::process::ExitCode {
    let mode = std::env::args().nth(1).unwrap_or_default();
    let on_failure = match mode.as_str() {
        "crash" => v8::OnFailure::CrashOnFailure,
        "dump" => v8::OnFailure::DumpOnFailure,
        _ => {
            eprintln!("usage: context-scopes-advanced-negative <crash|dump>");
            return std::process::ExitCode::from(2);
        }
    };

    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    checkpoint(&format!("mode={mode}"));
    let disallow = std::pin::pin!(v8::DisallowJavascriptExecutionScope::new(scope, on_failure,));
    let disallow = &mut disallow.init();
    checkpoint("scope=entered");
    let source = v8::String::new(disallow, "42").unwrap();
    let script = v8::Script::compile(disallow, source, None).unwrap();
    checkpoint("script=compiled");
    let result = script.run(disallow);
    checkpoint(&format!("run_some={}", result.is_some()));
    std::process::ExitCode::SUCCESS
}
