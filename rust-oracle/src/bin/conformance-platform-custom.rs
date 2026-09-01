//! Custom `PlatformImpl` and transferred task ownership conformance.

use oracle::json::Json;
use oracle::report::{pass, summary_line};
use std::collections::VecDeque;
use std::ffi::c_void;
use std::io::{Read, Write};
use std::process::Stdio;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::{self, ThreadId};
use std::time::{Duration, Instant};

const EMPTY_WASM: &str = "new Uint8Array([0,97,115,109,1,0,0,0])";

struct Captured<T> {
    isolate: usize,
    on_main_thread: bool,
    value: T,
}

struct Delayed {
    captured: Captured<v8::Task>,
    seconds: f64,
}

#[derive(Default)]
struct Queues {
    task: VecDeque<Captured<v8::Task>>,
    non_nestable: VecDeque<Captured<v8::Task>>,
    delayed: VecDeque<Delayed>,
    non_nestable_delayed: VecDeque<Delayed>,
    idle: VecDeque<Captured<v8::IdleTask>>,
}

struct Recorder {
    main_thread: ThreadId,
    queues: Mutex<Queues>,
    impl_drop_count: AtomicUsize,
}

impl Recorder {
    fn new() -> Self {
        Self {
            main_thread: thread::current().id(),
            queues: Mutex::new(Queues::default()),
            impl_drop_count: AtomicUsize::new(0),
        }
    }

    fn capture<T>(&self, isolate_ptr: *mut c_void, value: T) -> Captured<T> {
        Captured {
            isolate: isolate_ptr as usize,
            on_main_thread: thread::current().id() == self.main_thread,
            value,
        }
    }

    fn wait_until(&self, predicate: impl Fn(&Queues) -> bool) {
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            if predicate(&self.queues.lock().unwrap()) {
                return;
            }
            assert!(
                Instant::now() < deadline,
                "timed out waiting for platform callback"
            );
            thread::sleep(Duration::from_millis(1));
        }
    }

    fn drop_all_queued(&self) {
        *self.queues.lock().unwrap() = Queues::default();
    }
}

struct QueuedImpl {
    recorder: Arc<Recorder>,
}

impl v8::PlatformImpl for QueuedImpl {
    fn post_task(&self, isolate_ptr: *mut c_void, task: v8::Task) {
        let captured = self.recorder.capture(isolate_ptr, task);
        self.recorder
            .queues
            .lock()
            .unwrap()
            .task
            .push_back(captured);
    }

    fn post_non_nestable_task(&self, isolate_ptr: *mut c_void, task: v8::Task) {
        let captured = self.recorder.capture(isolate_ptr, task);
        self.recorder
            .queues
            .lock()
            .unwrap()
            .non_nestable
            .push_back(captured);
    }

    fn post_delayed_task(&self, isolate_ptr: *mut c_void, task: v8::Task, seconds: f64) {
        let captured = self.recorder.capture(isolate_ptr, task);
        self.recorder
            .queues
            .lock()
            .unwrap()
            .delayed
            .push_back(Delayed { captured, seconds });
    }

    fn post_non_nestable_delayed_task(
        &self,
        isolate_ptr: *mut c_void,
        task: v8::Task,
        seconds: f64,
    ) {
        let captured = self.recorder.capture(isolate_ptr, task);
        self.recorder
            .queues
            .lock()
            .unwrap()
            .non_nestable_delayed
            .push_back(Delayed { captured, seconds });
    }

    fn post_idle_task(&self, isolate_ptr: *mut c_void, task: v8::IdleTask) {
        let captured = self.recorder.capture(isolate_ptr, task);
        self.recorder
            .queues
            .lock()
            .unwrap()
            .idle
            .push_back(captured);
    }
}

impl Drop for QueuedImpl {
    fn drop(&mut self) {
        self.recorder.impl_drop_count.fetch_add(1, Ordering::SeqCst);
    }
}

struct DefaultImpl;

impl v8::PlatformImpl for DefaultImpl {}

struct PanicImpl;

impl v8::PlatformImpl for PanicImpl {
    fn post_non_nestable_task(&self, _isolate_ptr: *mut c_void, _task: v8::Task) {
        panic!("platform callback panic marker");
    }
}

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn promise_state(promise: v8::Local<v8::Promise>) -> &'static str {
    match promise.state() {
        v8::PromiseState::Pending => "Pending",
        v8::PromiseState::Fulfilled => "Fulfilled",
        v8::PromiseState::Rejected => "Rejected",
    }
}

fn atomics_promise<'s>(
    scope: &v8::PinScope<'s, '_>,
    timeout: Option<u32>,
) -> v8::Local<'s, v8::Promise> {
    let timeout = timeout
        .map(|value| value.to_string())
        .unwrap_or_else(|| "Infinity".to_owned());
    let source = format!(
        "(() => {{ const b = new SharedArrayBuffer(4); \
         globalThis.a = new Int32Array(b); \
         return Atomics.waitAsync(a, 0, 0, {timeout}).value; }})()"
    );
    run_script(scope, &source).try_into().unwrap()
}

fn transfer_roundtrip(task: v8::Task) -> v8::Task {
    thread::spawn(move || task).join().unwrap()
}

fn default_methods_child() {
    v8::V8::set_flags_from_string("--allow-natives-syntax");
    let platform = v8::new_custom_platform(1, false, true, DefaultImpl).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _promise = atomics_promise(scope, None);
    println!("before-notify");
    std::io::stdout().flush().unwrap();
    run_script(scope, "Atomics.notify(a, 0, 1)");
    println!("after-notify");
}

fn build_lazy_source() -> String {
    let mut source = String::new();
    for index in 0..64 {
        source.push_str(&format!("function lazy{index}(x) {{ let y=x;"));
        for offset in 0..128 {
            source.push_str(&format!("y=(y*33+{offset})|0;"));
        }
        source.push_str("return y; }\n");
    }
    source.push_str("true");
    source
}

fn queued_child() {
    v8::V8::set_flags_from_string(
        "--allow-natives-syntax --expose-gc --lazy-compile-dispatcher --parallel-compile-tasks-for-lazy",
    );
    let recorder = Arc::new(Recorder::new());
    let platform = v8::new_custom_platform(
        2,
        true,
        true,
        QueuedImpl {
            recorder: recorder.clone(),
        },
    )
    .make_shared();
    v8::V8::initialize_platform(platform.clone());
    v8::V8::initialize();

    let (
        post_from_main,
        post_isolate,
        wasm_state,
        non_nestable_from_main,
        non_nestable_isolate,
        atomics_result,
        timeout_delay,
        timeout_from_main,
        timeout_isolate,
        timeout_state_after_drop,
        delayed_observation,
        idle_observation,
        pump_empty,
    ) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        recorder.drop_all_queued();

        let wasm: v8::Local<v8::Promise> =
            run_script(scope, &format!("WebAssembly.compile({EMPTY_WASM})"))
                .try_into()
                .unwrap();
        recorder.wait_until(|queues| !queues.task.is_empty());
        let posted = recorder.queues.lock().unwrap().task.pop_front().unwrap();
        let post_from_main = posted.on_main_thread;
        let post_isolate = posted.isolate;
        transfer_roundtrip(posted.value).run();
        scope.perform_microtask_checkpoint();
        let wasm_state = promise_state(wasm);

        let atomics = atomics_promise(scope, None);
        run_script(scope, "Atomics.notify(a, 0, 1)");
        recorder.wait_until(|queues| !queues.non_nestable.is_empty());
        let notified = recorder
            .queues
            .lock()
            .unwrap()
            .non_nestable
            .pop_front()
            .unwrap();
        let non_nestable_from_main = notified.on_main_thread;
        let non_nestable_isolate = notified.isolate;
        transfer_roundtrip(notified.value).run();
        scope.perform_microtask_checkpoint();
        let atomics_result = atomics.result(scope).to_rust_string_lossy(scope);

        let timeout = atomics_promise(scope, Some(5000));
        recorder.wait_until(|queues| !queues.non_nestable_delayed.is_empty());
        let delayed_timeout = recorder
            .queues
            .lock()
            .unwrap()
            .non_nestable_delayed
            .pop_front()
            .unwrap();
        let timeout_delay = delayed_timeout.seconds;
        let timeout_from_main = delayed_timeout.captured.on_main_thread;
        let timeout_isolate = delayed_timeout.captured.isolate;
        drop(transfer_roundtrip(delayed_timeout.captured.value));
        scope.perform_microtask_checkpoint();
        let timeout_state_after_drop = promise_state(timeout);

        run_script(
            scope,
            "let garbage=[]; for(let i=0;i<20000;i++) garbage.push({i}); garbage=null; gc(); true",
        );
        scope.memory_pressure_notification(v8::MemoryPressureLevel::Moderate);
        scope.memory_pressure_notification(v8::MemoryPressureLevel::None);
        let delayed_deadline = Instant::now() + Duration::from_secs(2);
        while recorder.queues.lock().unwrap().delayed.is_empty()
            && Instant::now() < delayed_deadline
        {
            let next = recorder.queues.lock().unwrap().task.pop_front();
            if let Some(task) = next {
                task.value.run();
                scope.perform_microtask_checkpoint();
            } else {
                thread::sleep(Duration::from_millis(1));
            }
        }
        let delayed_observation = recorder
            .queues
            .lock()
            .unwrap()
            .delayed
            .pop_front()
            .map(|task| {
                let observation = (
                    task.seconds,
                    task.captured.on_main_thread,
                    task.captured.isolate,
                );
                drop(task.captured.value);
                observation
            });

        run_script(scope, &build_lazy_source());
        let idle_deadline = Instant::now() + Duration::from_secs(10);
        while recorder.queues.lock().unwrap().idle.is_empty() && Instant::now() < idle_deadline {
            thread::sleep(Duration::from_millis(1));
        }
        let idle_observation = recorder
            .queues
            .lock()
            .unwrap()
            .idle
            .pop_front()
            .map(|task| {
                let observation = (task.on_main_thread, task.isolate);
                task.value.run(f64::INFINITY);
                observation
            });
        let pump_empty = v8::Platform::pump_message_loop(&platform, scope, false);
        recorder.drop_all_queued();
        (
            post_from_main,
            post_isolate,
            wasm_state,
            non_nestable_from_main,
            non_nestable_isolate,
            atomics_result,
            timeout_delay,
            timeout_from_main,
            timeout_isolate,
            timeout_state_after_drop,
            delayed_observation,
            idle_observation,
            pump_empty,
        )
    };

    unsafe { v8::V8::dispose() };
    v8::V8::dispose_platform();
    let drops_before = recorder.impl_drop_count.load(Ordering::SeqCst);
    drop(platform);
    let drops_after = recorder.impl_drop_count.load(Ordering::SeqCst);
    let all_isolates_match = [non_nestable_isolate, timeout_isolate]
        .into_iter()
        .chain(delayed_observation.map(|value| value.2))
        .chain(idle_observation.map(|value| value.1))
        .all(|value| value == post_isolate);
    let actual = Json::obj(vec![
        ("post_task_received", Json::b(true)),
        ("post_task_callback_on_main", Json::b(post_from_main)),
        ("post_task_send_roundtrip_then_run", Json::b(true)),
        ("wasm_promise_after_run", Json::s(wasm_state)),
        ("non_nestable_received", Json::b(true)),
        (
            "non_nestable_callback_on_main",
            Json::b(non_nestable_from_main),
        ),
        ("non_nestable_send_roundtrip_then_run", Json::b(true)),
        ("non_nestable_result", Json::s(&atomics_result)),
        ("non_nestable_delayed_received", Json::b(true)),
        ("non_nestable_delay_seconds", Json::f(timeout_delay)),
        (
            "non_nestable_delayed_callback_on_main",
            Json::b(timeout_from_main),
        ),
        (
            "non_nestable_delayed_send_roundtrip_then_drop",
            Json::b(true),
        ),
        (
            "timeout_promise_after_drop",
            Json::s(timeout_state_after_drop),
        ),
        ("delayed_received", Json::b(delayed_observation.is_some())),
        (
            "delayed_seconds",
            delayed_observation
                .map(|value| Json::f(value.0))
                .unwrap_or(Json::Null),
        ),
        (
            "delayed_callback_on_main",
            delayed_observation
                .map(|value| Json::b(value.1))
                .unwrap_or(Json::Null),
        ),
        ("idle_received", Json::b(idle_observation.is_some())),
        (
            "idle_callback_on_main",
            idle_observation
                .map(|value| Json::b(value.0))
                .unwrap_or(Json::Null),
        ),
        (
            "idle_run_deadline",
            idle_observation
                .map(|_| Json::s("Infinity"))
                .unwrap_or(Json::Null),
        ),
        ("all_isolate_pointers_nonzero", Json::b(post_isolate != 0)),
        ("all_isolate_pointers_equal", Json::b(all_isolates_match)),
        ("underlying_pump_executed_task", Json::b(pump_empty)),
        (
            "impl_drops_before_last_platform_ref",
            Json::i(drops_before as i64),
        ),
        (
            "impl_drops_after_last_platform_ref",
            Json::i(drops_after as i64),
        ),
    ]);
    println!(
        "{}",
        pass("platform_custom/queued_all_callbacks", actual).to_line()
    );
}

fn panic_child() {
    v8::V8::set_flags_from_string("--allow-natives-syntax");
    let platform = v8::new_custom_platform(1, false, true, PanicImpl).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _promise = atomics_promise(scope, None);
    run_script(scope, "Atomics.notify(a, 0, 1)");
}

fn run_child(mode: &str) -> std::process::Output {
    std::process::Command::new(std::env::current_exe().unwrap())
        .args(["--child", mode])
        .output()
        .unwrap()
}

fn driver() {
    let mut immediate = std::process::Command::new(std::env::current_exe().unwrap())
        .args(["--child", "default"])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_millis(500);
    let timed_out = loop {
        if immediate.try_wait().unwrap().is_some() {
            break false;
        }
        if Instant::now() >= deadline {
            break true;
        }
        thread::sleep(Duration::from_millis(5));
    };
    if timed_out {
        immediate.kill().unwrap();
    }
    immediate.wait().unwrap();
    let mut immediate_stdout = String::new();
    immediate
        .stdout
        .take()
        .unwrap()
        .read_to_string(&mut immediate_stdout)
        .unwrap();
    let actual = Json::obj(vec![
        ("callback_kind", Json::s("post_non_nestable_task")),
        ("default_body", Json::s("task.run()")),
        (
            "entered_atomics_notify",
            Json::b(immediate_stdout.contains("before-notify")),
        ),
        (
            "returned_from_atomics_notify",
            Json::b(immediate_stdout.contains("after-notify")),
        ),
        ("did_not_exit_within_500ms", Json::b(timed_out)),
        ("terminated_by_harness", Json::b(timed_out)),
    ]);
    println!(
        "{}",
        pass("platform_custom/default_immediate_deadlock", actual).to_line()
    );

    let queued = run_child("queued");
    assert!(
        queued.status.success(),
        "queued child failed\nstdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&queued.stdout),
        String::from_utf8_lossy(&queued.stderr)
    );
    print!("{}", String::from_utf8(queued.stdout).unwrap());
    let panic = run_child("panic");
    let stderr = String::from_utf8_lossy(&panic.stderr);
    let actual = Json::obj(vec![
        ("success", Json::b(panic.status.success())),
        (
            "windows_status",
            Json::s(&format!("0x{:08X}", panic.status.code().unwrap() as u32)),
        ),
        (
            "panic_marker_present",
            Json::b(stderr.contains("platform callback panic marker")),
        ),
    ]);
    println!(
        "{}",
        pass("platform_custom/callback_panic", actual).to_line()
    );
    println!("{}", summary_line(3, 3, 0));
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    match args.as_slice() {
        [_] => driver(),
        [_, flag, mode] if flag == "--child" => match mode.as_str() {
            "default" => default_methods_child(),
            "queued" => queued_child(),
            "panic" => panic_child(),
            _ => panic!("unknown mode {mode}"),
        },
        _ => panic!("usage: conformance-platform-custom [--child MODE]"),
    }
}
