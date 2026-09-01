//! Default-platform constructor and message-loop conformance for rusty_v8 152.2.0.
//!
//! Each platform kind is initialized in a fresh child process because V8's
//! process-global platform state cannot be reset after disposal. `Task` and
//! `IdleTask` construction is intentionally not covered here: their public
//! wrappers have private constructors and are only delivered to embedders by
//! the custom `PlatformImpl` API, which is a separate conformance slice.

use oracle::json::Json;
use oracle::report::{pass, summary_line};
use std::process::{Command, Output};

const ATOMICS_SETUP: &str = r#"
  const shared = new SharedArrayBuffer(4);
  const ints = new Int32Array(shared);
  globalThis.platformResolved = false;
  const waiter = Atomics.waitAsync(ints, 0, 0);
  waiter.value.then(value => {
    if (value !== 'ok') throw new Error('unexpected wait result: ' + value);
    globalThis.platformResolved = true;
  });
  if (Atomics.notify(ints, 0, 1) !== 1) throw new Error('notify failed');
"#;

#[derive(Clone, Copy)]
enum Kind {
    Default,
    Unprotected,
    SingleThreaded,
}

impl Kind {
    fn name(self) -> &'static str {
        match self {
            Self::Default => "default",
            Self::Unprotected => "unprotected_default",
            Self::SingleThreaded => "single_threaded_default",
        }
    }

    fn check_id(self) -> &'static str {
        match self {
            Self::Default => "platform/default_idle_enabled",
            Self::Unprotected => "platform/unprotected_idle_disabled",
            Self::SingleThreaded => "platform/single_threaded_idle_enabled",
        }
    }

    fn idle_enabled(self) -> bool {
        !matches!(self, Self::Unprotected)
    }

    fn thread_pool_size(self) -> Option<u32> {
        match self {
            Self::Default => Some(0),
            Self::Unprotected => Some(u32::MAX),
            Self::SingleThreaded => None,
        }
    }
}

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn script_bool(scope: &v8::PinScope<'_, '_>, source: &str) -> bool {
    run_script(scope, source).boolean_value(scope)
}

fn child(kind: Kind) {
    let flags = if matches!(kind, Kind::SingleThreaded) {
        "--single-threaded --allow-natives-syntax"
    } else {
        "--allow-natives-syntax"
    };
    v8::V8::set_flags_from_string(flags);

    let unique = match kind {
        Kind::Default => v8::new_default_platform(0, true),
        Kind::Unprotected => v8::new_unprotected_default_platform(u32::MAX, false),
        Kind::SingleThreaded => v8::new_single_threaded_default_platform(true),
    };
    let platform = unique.make_shared();
    platform.assert_use_count_eq(1);
    v8::V8::initialize_platform(platform.clone());
    platform.assert_use_count_eq(2);
    v8::V8::initialize();
    platform.assert_use_count_eq(2);

    let (
        empty_pump,
        usable_after_idle,
        unresolved_before,
        first_pump,
        resolved_after,
        empty_after_drain,
    ) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);

        let current = v8::V8::get_current_platform();
        platform.assert_use_count_eq(3);
        drop(current);
        platform.assert_use_count_eq(2);

        let empty_pump = v8::Platform::pump_message_loop(&platform, scope, false);
        v8::Platform::run_idle_tasks(&platform, scope, 0.0);
        v8::Platform::run_idle_tasks(&platform, scope, 0.002);
        for boundary in [f64::NEG_INFINITY, -1.0, f64::NAN, f64::INFINITY] {
            v8::Platform::run_idle_tasks(&platform, scope, boundary);
        }
        let usable_after_idle = script_bool(scope, "6 * 7 === 42");

        run_script(scope, ATOMICS_SETUP);
        let unresolved_before = script_bool(scope, "platformResolved === false");
        let first_pump = v8::Platform::pump_message_loop(&platform, scope, true);
        scope.perform_microtask_checkpoint();
        let mut additional_tasks = 0usize;
        while v8::Platform::pump_message_loop(&platform, scope, false) {
            additional_tasks += 1;
            scope.perform_microtask_checkpoint();
            assert!(
                additional_tasks < 100,
                "message-loop task drain did not quiesce"
            );
        }
        scope.perform_microtask_checkpoint();
        let resolved_after = script_bool(scope, "platformResolved === true");
        let empty_after_drain = v8::Platform::pump_message_loop(&platform, scope, false);
        (
            empty_pump,
            usable_after_idle,
            unresolved_before,
            first_pump,
            resolved_after,
            empty_after_drain,
        )
    };
    unsafe { v8::V8::dispose() };
    platform.assert_use_count_eq(2);
    v8::V8::dispose_platform();
    platform.assert_use_count_eq(1);

    let actual = Json::obj(vec![
        ("constructor", Json::s(kind.name())),
        (
            "thread_pool_size_argument",
            kind.thread_pool_size()
                .map(|value| Json::i(i64::from(value)))
                .unwrap_or(Json::Null),
        ),
        ("idle_task_support", Json::b(kind.idle_enabled())),
        (
            "single_threaded_flag",
            Json::b(matches!(kind, Kind::SingleThreaded)),
        ),
        ("v8_version", Json::s(v8::V8::get_version())),
        ("shared_count_new", Json::i(1)),
        ("shared_count_initialized", Json::i(2)),
        ("shared_count_with_current_handle", Json::i(3)),
        ("shared_count_after_dispose_platform", Json::i(1)),
        ("empty_pump_executed_task", Json::b(empty_pump)),
        (
            "idle_seconds_inputs",
            Json::arr(
                ["0", "0.002", "-Infinity", "-1", "NaN", "Infinity"]
                    .into_iter()
                    .map(Json::s)
                    .collect(),
            ),
        ),
        ("idle_all_boundary_calls_returned", Json::b(true)),
        ("usable_after_idle", Json::b(usable_after_idle)),
        ("atomics_unresolved_before_pump", Json::b(unresolved_before)),
        ("wait_pump_executed_task", Json::b(first_pump)),
        ("atomics_resolved_after_drain", Json::b(resolved_after)),
        ("post_drain_pump_executed_task", Json::b(empty_after_drain)),
    ]);
    println!("{}", pass(kind.check_id(), actual).to_line());
}

fn child_output(mode: &str) -> Output {
    Command::new(std::env::current_exe().unwrap())
        .args(["--child", mode])
        .output()
        .unwrap()
}

fn driver() {
    let modes = ["default", "unprotected", "single-threaded"];
    let mut lines = Vec::new();
    for mode in modes {
        let output = child_output(mode);
        assert!(
            output.status.success(),
            "child {mode} failed\nstdout:\n{}\nstderr:\n{}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        let text = String::from_utf8(output.stdout).unwrap();
        assert_eq!(text.lines().count(), 1, "unexpected child output: {text:?}");
        lines.push(text);
    }
    for line in lines {
        print!("{line}");
    }
    let missing_flag = Command::new(std::env::current_exe().unwrap())
        .arg("--missing-single-threaded-flag")
        .output()
        .unwrap();
    let code = missing_flag.status.code().unwrap();
    let actual = Json::obj(vec![
        ("trigger", Json::s("WebAssembly.compile(empty_module)")),
        ("success", Json::b(missing_flag.status.success())),
        ("windows_status", Json::s(&format!("0x{:08X}", code as u32))),
        ("stdout_empty", Json::b(missing_flag.stdout.is_empty())),
        ("stderr_empty", Json::b(missing_flag.stderr.is_empty())),
    ]);
    println!(
        "{}",
        pass("platform/single_threaded_without_required_flag", actual).to_line()
    );
    println!("{}", summary_line(4, 4, 0));
}

fn missing_single_threaded_flag() {
    let platform = v8::new_single_threaded_default_platform(false).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    // WebAssembly compilation requests worker execution when the required
    // --single-threaded flag was omitted, reaching the platform invariant.
    let source = v8::String::new(
        scope,
        "WebAssembly.compile(new Uint8Array([0,97,115,109,1,0,0,0]))",
    )
    .unwrap();
    let script = v8::Script::compile(scope, source, None).unwrap();
    let _ = script.run(scope);
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    match args.as_slice() {
        [_] => driver(),
        [_, flag, mode] if flag == "--child" => match mode.as_str() {
            "default" => child(Kind::Default),
            "unprotected" => child(Kind::Unprotected),
            "single-threaded" => child(Kind::SingleThreaded),
            _ => panic!("unknown child mode: {mode}"),
        },
        [_, flag] if flag == "--missing-single-threaded-flag" => missing_single_threaded_flag(),
        _ => panic!("usage: conformance-platform [--child MODE|--missing-single-threaded-flag]"),
    }
}
