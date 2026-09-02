//! Core-embedding "advanced" conformance slice for the pinned `v8` crate
//! (=152.2.0, V8 15.2.124.1, x86_64-pc-windows-msvc).
//!
//! Characterizes, in fixed order, the observable contract of the embedding
//! APIs that the base (`conformance`), host (`conformance-host`) and buffers
//! (`conformance-buffers`) slices do not cover. Excluded by milestone scope:
//! ES modules, Wasm, and the inspector/debugger. Areas covered here:
//!
//! - **Handle scopes**: nested `HandleScope`s, `EscapableHandleScope`
//!   escape chains (including a nested escapable scope), and the
//!   deterministic "escape() called twice" panic (caught in-process).
//!   Source: `v8-152.2.0/src/scope.rs` (`EscapableHandleScope::escape`
//!   takes the escape slot; the `.expect` message is pinned).
//! - **`Locker`/`Unlocker` and `IsolateHandle` thread behavior** via
//!   `OwnedIsolate::try_into_shared` -> `SharedIsolate`
//!   (`src/locker.rs`, `src/isolate.rs`): cross-thread sequential locks,
//!   cross-thread `terminate_execution` while the isolate is locked and
//!   looping, the `Locker::unlock` window, `IntoSharedErrorKind`
//!   rejection/recovery, and `IsolateHandle` calls after isolate disposal.
//! - **Context lifecycle**: current/entered-context nesting through
//!   `ContextScope`s (the crate has no `Context::enter`; the scope *is*
//!   the enter/exit), security tokens, embedder data values and aligned
//!   pointers, and the `Rc`-returning `Context` slots (`src/context.rs`).
//! - **Isolate slots**: the raw pointer data slots
//!   (`get_data`/`set_data`/`get_number_of_data_slots`) and typed slots of
//!   several distinct types in one isolate (single-type ownership is
//!   already pinned by the host fixture's `external/isolate_slot_ownership`).
//! - **Script origins / compiler options / unbound scripts**: `ScriptOrigin`
//!   round-trip and V8-assigned-vs-origin script ids, origin line/column
//!   offsets shifting reported exception positions, `UnboundScript`
//!   re-binding into a second context, `ScriptCompiler` options
//!   (`EagerCompile`), `CompileFunction` with declared parameters,
//!   `cached_data_version_tag`, and a code-cache produce/consume round-trip
//!   across two isolates (`src/script.rs`, `src/script_compiler.rs`,
//!   `src/unbound_script.rs`).
//! - **Exception details**: `Message` getters against a scripted origin,
//!   `StackTrace::current_stack_trace` frames captured inside a native
//!   callback, `Exception::capture_stack_trace` on a plain object,
//!   uncaught-exception stack capture via
//!   `set_capture_stack_trace_for_uncaught_exceptions`, and the
//!   `Exception::get_stack_trace` gap for natively created errors
//!   (`src/exception.rs`).
//! - **Termination/interrupts**: same-thread terminate/cancel flag
//!   lifecycle (including stickiness without cancel and across TryCatch
//!   resets), cancel before delivery, and `request_interrupt` delivery
//!   during a bounded JS loop (`src/isolate.rs`).
//! - **Heap/GC where deterministic**: `HeapStatistics` invariants and the
//!   exact `adjust_amount_of_external_allocated_memory` accounting, plus
//!   prologue/epilogue GC callbacks filtered to `kGCTypeMarkSweepCompact`
//!   firing per `low_memory_notification` and staying silent after removal
//!   (`src/isolate.rs`, `src/gc.rs`).
//!
//! Everything is normalized per `src/json.rs` rules: no addresses, no
//! thread ids, no timings, no raw V8-assigned script ids (only equality /
//! positivity / distinctness), exact V8 error strings for the pinned build.
//! The runner emits the same JSON-lines protocol as the other slices
//! (`{"check":..,"ok":..,"value"|"expected"/"actual"}` + final summary);
//! every check id is prefixed `core-advanced/`.
//!
//! This slice performs no platform shutdown, so its fixture can be verified
//! in-process and compared byte-for-byte by
//! `tests/conformance_core_advanced_fixture.rs`.
//!
//! Known gap (documented, not characterized): V8 C++ `v8::SealHandleScope`
//! (`v8/src/handles/handles.h`) has **no Rust binding** in this crate —
//! there is nothing observable to pin; a Go port must track it as
//! unsupported rather than inventing behavior.
//!
//! # Benchmark workload spec (for a future `benches/core-advanced.rs`)
//!
//! Methodology identical to the existing benches (`benches/common/mod.rs`):
//! 1 s warm-up, 3 s measurement, 50 samples, one full operation per
//! `criterion::black_box`-guarded iteration, fresh nested `HandleScope` per
//! iteration where a scope is needed, no V8 flags, default platform,
//! release profile. Workloads, each asserted once for correctness outside
//! the timed loop:
//!
//! - `locker/lock_unlock_roundtrip`: `SharedIsolate` held by the bench
//!   thread; per iteration `shared.lock()` -> drop the `Locker` (no JS).
//! - `locker/lock_run_script`: per iteration lock + `HandleScope` +
//!   `Context` + run `"40 + 2"`.
//! - `scope/escapable_create_escape`: per iteration an
//!   `EscapableHandleScope` under the bench scope + one `Number` +
//!   `escape()`.
//! - `context/new_with_security_token`: per iteration `Context::new` plus
//!   `set_security_token(String)`.
//! - `script/compile_unbound_eager`: per iteration
//!   `ScriptCompiler::compile_unbound_script(EagerCompile)` of the fib(12)
//!   workload source (same source text as `benches/script.rs`).
//! - `script/code_cache_consume`: setup compiles once and produces the
//!   code cache; per iteration `CachedData::new(copy)` +
//!   `Source::new_with_cached_data` + `ConsumeCodeCache` compile in a
//!   fresh context (the copy cost is part of the workload).
//! - `message/capture_stack_trace`: per iteration
//!   `StackTrace::current_stack_trace(scope, 16)` from a native callback
//!   invoked through `Function::call` (depth ~3 frames).
//! - `heap/get_heap_statistics`: per iteration `get_heap_statistics()` and
//!   a `black_box` read of `used_heap_size`.
//!
//! Go comparisons must use the same warm-up/sample policy, sources, and
//! V8 configuration (no flags, default platform, pointer compression off),
//! a release-mode build, and a fresh environment capture.

use std::cell::RefCell;
use std::rc::Rc;
use std::sync::atomic::{AtomicBool, AtomicI32, AtomicUsize, Ordering};
use std::sync::Mutex;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

// ---------------------------------------------------------------------------
// Helpers (local to this binary; `checks::harness` is pub(crate) and shared
// registry files must not be modified by this slice).
// ---------------------------------------------------------------------------

/// Compiles and runs `source`, returning the completion value (`None` on
/// syntax error or runtime throw).
fn eval<'s>(scope: &mut v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let src = v8::String::new(scope, source)?;
    v8::Script::compile(scope, src, None)?.run(scope)
}

/// Compiles and runs `source` with a `ScriptOrigin`, returning both.
fn eval_with_origin<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    source: &str,
    origin: &v8::ScriptOrigin<'_>,
) -> (
    Option<v8::Local<'s, v8::Script>>,
    Option<v8::Local<'s, v8::Value>>,
) {
    let Some(src) = v8::String::new(scope, source) else {
        return (None, None);
    };
    let Some(script) = v8::Script::compile(scope, src, Some(origin)) else {
        return (None, None);
    };
    let value = script.run(scope);
    (Some(script), value)
}

/// Builds a `ScriptOrigin` with only the knobs this slice varies; everything
/// else uses neutral defaults so checks stay comparable.
#[allow(clippy::too_many_arguments)]
fn make_origin<'s>(
    scope: &v8::PinScope<'s, '_>,
    name: &str,
    line_offset: i32,
    column_offset: i32,
    script_id: i32,
    source_map: Option<&str>,
    is_opaque: bool,
    is_shared_cross_origin: bool,
) -> v8::ScriptOrigin<'s> {
    let name_value: v8::Local<v8::Value> = v8::String::new(scope, name).unwrap().into();
    let source_map_url =
        source_map.map(|s| v8::Local::<v8::Value>::from(v8::String::new(scope, s).unwrap()));
    v8::ScriptOrigin::new(
        scope,
        name_value,
        line_offset,
        column_offset,
        is_shared_cross_origin,
        script_id,
        source_map_url,
        is_opaque,
        false, // is_wasm: out of milestone scope
        false, // is_module: out of milestone scope (see negative tests)
        None,  // host_defined_options
    )
}

/// Runs `f`, silencing the panic hook, and returns whether it panicked plus
/// the panic message ("" when it did not panic). Only used around panics
/// that fire *before* any FFI state change, where unwinding is safe by
/// construction (the `escape()` slot check and the `Locker` entry asserts).
fn catch_panic_message<R>(f: impl FnOnce() -> R) -> (Option<R>, String) {
    let previous = std::panic::take_hook();
    std::panic::set_hook(Box::new(|_| {}));
    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(f));
    std::panic::set_hook(previous);
    let message = match &result {
        Err(payload) => payload
            .downcast_ref::<String>()
            .cloned()
            .or_else(|| payload.downcast_ref::<&str>().map(|s| (*s).to_owned()))
            .unwrap_or_default(),
        Ok(_) => String::new(),
    };
    (result.ok(), message)
}

/// ToString of an arbitrary value ("" when conversion fails).
fn value_text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

/// The `IntoSharedErrorKind` encoded as a stable string for the fixture.
fn shared_error_kind(kind: v8::IntoSharedErrorKind) -> &'static str {
    match kind {
        v8::IntoSharedErrorKind::SnapshotCreator => "snapshot_creator",
        v8::IntoSharedErrorKind::LiveWeakHandlesOrPendingFinalizers => {
            "live_weak_handles_or_pending_finalizers"
        }
        v8::IntoSharedErrorKind::EmbedderCppHeap => "embedder_cpp_heap",
        v8::IntoSharedErrorKind::AnotherIsolateEntered => "another_isolate_entered",
        _ => "unknown",
    }
}

/// JSON encoder for the frames captured inside the native callback (plain
/// `fn` callbacks cannot capture; the payload travels through this static).
static FRAMES_CAPTURE: Mutex<Option<String>> = Mutex::new(None);

/// State shared with the interrupt callback through its `data` pointer.
struct InterruptState {
    count: AtomicUsize,
    requested_thread_matches: AtomicBool,
    terminating_at_delivery: AtomicBool,
    data_ptr_matches: AtomicBool,
    self_ptr: usize,
    handle: v8::IsolateHandle,
    requested_thread: RefCell<Option<std::thread::ThreadId>>,
}

unsafe extern "C" fn interrupt_callback(
    _isolate: v8::UnsafeRawIsolatePtr,
    data: *mut std::ffi::c_void,
) {
    let state = unsafe { &*(data as *const InterruptState) };
    state.count.fetch_add(1, Ordering::SeqCst);
    state
        .data_ptr_matches
        .store(data as usize == state.self_ptr, Ordering::SeqCst);
    state
        .terminating_at_delivery
        .store(state.handle.is_execution_terminating(), Ordering::SeqCst);
    if let Some(requested) = state.requested_thread.borrow().as_ref() {
        let same = *requested == std::thread::current().id();
        state.requested_thread_matches.store(same, Ordering::SeqCst);
    }
}

/// State shared with the GC prologue/epilogue callbacks.
struct GcCallbackState {
    prologue_count: AtomicUsize,
    epilogue_count: AtomicUsize,
    prologue_type: AtomicI32,
    epilogue_type: AtomicI32,
    prologue_flags: AtomicI32,
    epilogue_flags: AtomicI32,
}

unsafe extern "C" fn gc_prologue_callback(
    _isolate: v8::UnsafeRawIsolatePtr,
    gc_type: v8::GCType,
    flags: v8::GCCallbackFlags,
    data: *mut std::ffi::c_void,
) {
    let state = unsafe { &*(data as *const GcCallbackState) };
    state.prologue_count.fetch_add(1, Ordering::SeqCst);
    state.prologue_type.store(gc_type.0, Ordering::SeqCst);
    state.prologue_flags.store(flags.0, Ordering::SeqCst);
}

unsafe extern "C" fn gc_epilogue_callback(
    _isolate: v8::UnsafeRawIsolatePtr,
    gc_type: v8::GCType,
    flags: v8::GCCallbackFlags,
    data: *mut std::ffi::c_void,
) {
    let state = unsafe { &*(data as *const GcCallbackState) };
    state.epilogue_count.fetch_add(1, Ordering::SeqCst);
    state.epilogue_type.store(gc_type.0, Ordering::SeqCst);
    state.epilogue_flags.store(flags.0, Ordering::SeqCst);
}

// ---------------------------------------------------------------------------
// Checks. Order is part of the observable contract (the fixture is ordered).
// ---------------------------------------------------------------------------

/// Nested handle scopes plus a two-level `EscapableHandleScope` chain: the
/// escaped values stay usable after intervening scopes were created and
/// closed, and the outer scope's earlier value is untouched.
fn scope_nested_and_escaped() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let outer_value = v8::Number::new(scope, 7.0);

    // Each EscapableHandleScope escapes exactly once; the escaped local
    // carries the *outer* scope's lifetime and stays usable after the
    // escapable scope closed (upstream `escapable_handle_scope` test shape).
    let escaped_number = {
        let esc_a = std::pin::pin!(v8::EscapableHandleScope::new(scope));
        let esc_a = &mut esc_a.init();
        let number = v8::Number::new(esc_a, 8.0);
        esc_a.escape(number)
    };

    // A two-level chain: a nested escapable scope created *from* the outer
    // one escapes the string into it, and the outer one re-escapes it to
    // this scope, mirroring the upstream nested-escape test.
    let escaped_string = {
        let esc_b = std::pin::pin!(v8::EscapableHandleScope::new(scope));
        let esc_b = &mut esc_b.init();
        let deep = {
            let nested = std::pin::pin!(v8::EscapableHandleScope::new(esc_b));
            let nested = &mut nested.init();
            let deep = v8::String::new(nested, "deep").unwrap();
            nested.escape(deep)
        };
        esc_b.escape(deep)
    };

    // An intervening plain nested scope must not disturb escaped values.
    let inner_ok = {
        v8::scope!(let inner_scope, scope);
        let probe = v8::Number::new(inner_scope, 1.5);
        probe.value() == 1.5
    };

    let actual = Json::obj(vec![
        ("inner_scope_usable", Json::b(inner_ok)),
        (
            "escaped_number",
            Json::obj(vec![
                ("is_number", Json::b(escaped_number.is_number())),
                ("value", Json::f(escaped_number.value())),
            ]),
        ),
        (
            "escaped_string",
            Json::obj(vec![
                ("is_string", Json::b(escaped_string.is_string())),
                ("text", Json::s(&escaped_string.to_rust_string_lossy(scope))),
            ]),
        ),
        ("outer_value_unchanged", Json::b(outer_value.value() == 7.0)),
    ]);
    let expected = Json::obj(vec![
        ("inner_scope_usable", Json::b(true)),
        (
            "escaped_number",
            Json::obj(vec![("is_number", Json::b(true)), ("value", Json::f(8.0))]),
        ),
        (
            "escaped_string",
            Json::obj(vec![
                ("is_string", Json::b(true)),
                ("text", Json::s("deep")),
            ]),
        ),
        ("outer_value_unchanged", Json::b(true)),
    ]);
    vec![expect_eq(
        "core-advanced/scope/nested_and_escaped_scopes",
        expected,
        actual,
    )]
}

/// A second `escape()` on the same `EscapableHandleScope` panics with a
/// pinned message. The panic fires before any FFI state change, so it is
/// caught in-process; the first escaped value stays usable afterwards.
fn scope_escape_twice_panics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let escapable = std::pin::pin!(v8::EscapableHandleScope::new(scope));
    let escapable = &mut escapable.init();
    let first = escapable.escape(v8::Number::new(escapable, 1.0));
    let (_second_result, message) = catch_panic_message(|| {
        escapable.escape(v8::Number::new(escapable, 2.0));
    });
    let first_still_usable = first.value() == 1.0;

    let actual = Json::obj(vec![
        ("first_escape_usable", Json::b(first_still_usable)),
        ("panicked", Json::b(!message.is_empty())),
        ("message", Json::s(&message)),
    ]);
    let expected = Json::obj(vec![
        ("first_escape_usable", Json::b(true)),
        ("panicked", Json::b(true)),
        (
            "message",
            Json::s("EscapableHandleScope::escape() called twice"),
        ),
    ]);
    vec![expect_eq(
        "core-advanced/scope/escapable_escape_twice_panics",
        expected,
        actual,
    )]
}

/// Runs `source` under a fresh lock of `shared` and returns the i64 result.
fn locked_eval(shared: &v8::SharedIsolate, source: &str) -> Option<i64> {
    let mut locker = shared.lock();
    v8::scope!(let scope, &mut *locker);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval(scope, source)?.integer_value(scope)
}

/// A `SharedIsolate` serializes two worker threads and the main thread:
/// each `lock()` runs a script and every result is exact regardless of
/// scheduling order.
fn thread_shared_isolate_cross_thread_locks() -> Vec<CheckOutcome> {
    let owned = v8::Isolate::new(Default::default());
    let shared = unsafe { owned.try_into_shared() }.expect("fresh isolate converts");

    let (tx, rx) = std::sync::mpsc::channel::<i64>();
    std::thread::scope(|threads| {
        threads.spawn(|| {
            let _ = tx.send(locked_eval(&shared, "6 * 7").unwrap_or(-1));
        });
        threads.spawn(|| {
            let _ = tx.send(locked_eval(&shared, "20 + 2").unwrap_or(-1));
        });
    });
    drop(tx);
    let mut worker_results = vec![rx.recv().unwrap_or(-2), rx.recv().unwrap_or(-3)];
    worker_results.sort_unstable();

    let main_result = locked_eval(&shared, "40 + 2").unwrap_or(-4);
    drop(shared);

    let actual = Json::obj(vec![
        (
            "worker_results",
            Json::arr(worker_results.into_iter().map(Json::i).collect()),
        ),
        ("main_result", Json::i(main_result)),
    ]);
    let expected = Json::obj(vec![
        ("worker_results", Json::arr(vec![Json::i(22), Json::i(42)])),
        ("main_result", Json::i(42)),
    ]);
    vec![expect_eq(
        "core-advanced/thread/shared_isolate_cross_thread_locks",
        expected,
        actual,
    )]
}

/// Cross-thread `terminate_execution()` through the `IsolateHandle` while
/// the isolate is locked and running an infinite loop; the request is
/// sticky, so no sleeps are needed for determinism, and
/// `cancel_terminate_execution()` restores the isolate.
fn thread_shared_terminate_while_locked() -> Vec<CheckOutcome> {
    let owned = v8::Isolate::new(Default::default());
    let handle = owned.thread_safe_handle();
    let terminator = handle.clone();
    let shared = unsafe { owned.try_into_shared() }.expect("fresh isolate converts");

    // All work under this lock happens in a block so the scope temporaries
    // (and the locker borrow they carry) end before the re-lock below.
    let (ran, has_caught, has_terminated, can_continue, still_terminating, requested_ok) = {
        let mut locker = shared.lock();
        v8::scope!(let scope, &mut *locker);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);

        // Request termination from a foreign thread while this thread holds
        // the lock and is about to run the loop; delivery happens at the
        // loop's first interrupt checkpoint either way.
        let requested = std::thread::spawn(move || terminator.terminate_execution());

        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let source = v8::String::new(tc, "for (;;) { }").unwrap();
        let script = v8::Script::compile(tc, source, None).unwrap();
        let ran = script.run(tc).is_some();
        let still_terminating = handle.is_execution_terminating();
        let requested_ok = requested.join().unwrap_or(false);
        (
            ran,
            tc.has_caught(),
            tc.has_terminated(),
            tc.can_continue(),
            still_terminating,
            requested_ok,
        )
    };

    // The lock was released with the block; cancel the pending termination
    // and re-lock to verify the isolate is fully usable.
    let cancel_ok = handle.cancel_terminate_execution();
    let recovered = locked_eval(&shared, "40 + 2").unwrap_or(-1);
    drop(shared);

    let actual = Json::obj(vec![
        ("terminate_requested", Json::b(requested_ok)),
        ("run_none", Json::b(!ran)),
        ("has_caught", Json::b(has_caught)),
        ("has_terminated", Json::b(has_terminated)),
        ("can_continue", Json::b(can_continue)),
        ("still_terminating_after_run", Json::b(still_terminating)),
        ("cancel_ok", Json::b(cancel_ok)),
        ("recovered_result", Json::i(recovered)),
    ]);
    let expected = Json::obj(vec![
        ("terminate_requested", Json::b(true)),
        ("run_none", Json::b(true)),
        ("has_caught", Json::b(true)),
        ("has_terminated", Json::b(true)),
        ("can_continue", Json::b(false)),
        ("still_terminating_after_run", Json::b(true)),
        ("cancel_ok", Json::b(true)),
        ("recovered_result", Json::i(42)),
    ]);
    vec![expect_eq(
        "core-advanced/thread/shared_terminate_while_locked",
        expected,
        actual,
    )]
}

/// `Locker::unlock` releases the isolate so a blocked second thread can
/// lock and run JS, then reacquires before returning: after the window the
/// original locker still owns the isolate and runs scripts.
fn thread_locker_unlock_window() -> Vec<CheckOutcome> {
    let owned = v8::Isolate::new(Default::default());
    let shared = unsafe { owned.try_into_shared() }.expect("fresh isolate converts");

    let (started_tx, started_rx) = std::sync::mpsc::channel::<()>();
    let (result_tx, result_rx) = std::sync::mpsc::channel::<i64>();

    let (worker_result, main_result) = std::thread::scope(|threads| {
        threads.spawn(|| {
            // Blocks in `locked_eval` until the main thread opens the window.
            let _ = started_tx.send(());
            let result = locked_eval(&shared, "2 + 3").unwrap_or(-1);
            let _ = result_tx.send(result);
        });

        let mut locker = shared.lock();
        let _ = started_rx.recv();

        let worker_result = locker.unlock(|| result_rx.recv().unwrap_or(-2));

        // Back from the window: the same locker still owns the isolate.
        let main_result = {
            v8::scope!(let scope, &mut *locker);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            eval(scope, "1 + 1")
                .and_then(|v| v.integer_value(scope))
                .unwrap_or(-3)
        };
        drop(locker);
        (worker_result, main_result)
    });
    drop(shared);

    let actual = Json::obj(vec![
        ("worker_result", Json::i(worker_result)),
        ("main_result_after_window", Json::i(main_result)),
    ]);
    let expected = Json::obj(vec![
        ("worker_result", Json::i(5)),
        ("main_result_after_window", Json::i(2)),
    ]);
    vec![expect_eq(
        "core-advanced/thread/locker_unlock_window",
        expected,
        actual,
    )]
}

/// `try_into_shared` rejection reasons and recovery: a second isolate
/// entered on top rejects with `AnotherIsolateEntered`, a live `Weak`
/// handle rejects with `LiveWeakHandlesOrPendingFinalizers`; both recover
/// the isolate via `into_isolate`, and after dropping the weak the
/// conversion succeeds and the shared isolate runs JS.
fn thread_into_shared_rejections() -> Vec<CheckOutcome> {
    // Part A: another isolate entered on top rejects the conversion.
    let bottom = v8::Isolate::new(Default::default());
    let top = v8::Isolate::new(Default::default());
    let (entered_kind, recovered_bottom) = unsafe { bottom.try_into_shared() }
        .err()
        .map(|err| {
            let kind = shared_error_kind(err.kind());
            (kind, Some(err.into_isolate()))
        })
        .unwrap_or(("converted_unexpectedly", None));
    // Drop order matters: `top` is entered above `bottom`, and isolates
    // must be dropped in reverse creation order.
    drop(top);
    drop(recovered_bottom);

    // Part B: a live weak handle rejects; dropping it allows conversion.
    let mut owned = v8::Isolate::new(Default::default());
    let weak_holder;
    {
        v8::scope!(let scope, &mut *owned);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let obj = v8::Object::new(scope);
        weak_holder = v8::Weak::<v8::Object>::new(&mut *scope, obj);
    }
    // The weak handle is still alive here: the conversion must reject.
    let (weak_kind, recovered) = unsafe { owned.try_into_shared() }
        .err()
        .map(|err| {
            let kind = shared_error_kind(err.kind());
            (kind, Some(err.into_isolate()))
        })
        .unwrap_or(("converted_unexpectedly", None));
    owned = recovered.expect("conversion must be recoverable");
    // Only now drop the weak handle; the retry must succeed.
    drop(weak_holder);
    let shared_result = unsafe { owned.try_into_shared() }.ok();
    let retry_ok = shared_result.is_some();
    let locked_run = shared_result
        .as_ref()
        .and_then(|shared| locked_eval(shared, "3 * 3"))
        .unwrap_or(-1);
    drop(shared_result);

    let actual = Json::obj(vec![
        ("entered_reject_kind", Json::s(entered_kind)),
        ("weak_reject_kind", Json::s(weak_kind)),
        ("weak_retry_ok", Json::b(retry_ok)),
        ("locked_run_after_recovery", Json::i(locked_run)),
    ]);
    let expected = Json::obj(vec![
        ("entered_reject_kind", Json::s("another_isolate_entered")),
        (
            "weak_reject_kind",
            Json::s("live_weak_handles_or_pending_finalizers"),
        ),
        ("weak_retry_ok", Json::b(true)),
        ("locked_run_after_recovery", Json::i(9)),
    ]);
    vec![expect_eq(
        "core-advanced/thread/into_shared_rejections",
        expected,
        actual,
    )]
}

/// After the isolate is dropped, every `IsolateHandle` control method
/// reports "destroyed" (false) and a registered interrupt never runs.
fn thread_handle_after_dispose() -> Vec<CheckOutcome> {
    static CALL_COUNT: AtomicUsize = AtomicUsize::new(0);
    extern "C" fn never_called(_isolate: v8::UnsafeRawIsolatePtr, _data: *mut std::ffi::c_void) {
        CALL_COUNT.fetch_add(1, Ordering::SeqCst);
    }

    let owned = v8::Isolate::new(Default::default());
    let handle = owned.thread_safe_handle();
    let handle2 = handle.clone();
    drop(owned);

    let actual = Json::obj(vec![
        ("terminate", Json::b(handle.terminate_execution())),
        ("cancel", Json::b(handle.cancel_terminate_execution())),
        ("is_terminating", Json::b(handle.is_execution_terminating())),
        (
            "interrupt_requested",
            Json::b(handle2.request_interrupt(never_called, std::ptr::null_mut())),
        ),
        (
            "interrupt_count",
            Json::i(CALL_COUNT.load(Ordering::SeqCst) as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        ("terminate", Json::b(false)),
        ("cancel", Json::b(false)),
        ("is_terminating", Json::b(false)),
        ("interrupt_requested", Json::b(false)),
        ("interrupt_count", Json::i(0)),
    ]);
    vec![expect_eq(
        "core-advanced/thread/handle_after_dispose",
        expected,
        actual,
    )]
}

/// Context enter/exit nesting through `ContextScope`s (the crate has no
/// `Context::enter`/`Exit`): the current context follows the innermost
/// scope, restores on exit, and both current/entered contexts expose the
/// expected usable global object.
fn context_enter_exit_nesting() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let ctx1 = v8::Context::new(scope, Default::default());
    let ctx2 = v8::Context::new(scope, Default::default());
    let outer = &mut v8::ContextScope::new(scope, ctx1);
    eval(outer, "globalThis.contextMarker = 'ctx1'").unwrap();

    let outer_current = outer.get_current_context();
    let outer_entered = outer.get_entered_or_microtask_context();
    let outer_current_is_ctx1 = outer_current == ctx1;
    let outer_entered_is_ctx1 = outer_entered == ctx1;
    let marker_key = v8::String::new(outer, "contextMarker").unwrap();
    let outer_current_marker = outer_current
        .global(outer)
        .get(outer, marker_key.into())
        .and_then(|value| value.to_string(outer))
        .map(|value| value.to_rust_string_lossy(outer))
        .unwrap_or_default();
    let outer_entered_marker = outer_entered
        .global(outer)
        .get(outer, marker_key.into())
        .and_then(|value| value.to_string(outer))
        .map(|value| value.to_rust_string_lossy(outer))
        .unwrap_or_default();
    let global_identity = ctx1.global(outer) == ctx1.global(outer);
    let globals_distinct = ctx1.global(outer) != ctx2.global(outer);

    let inner_current_is_ctx2;
    let inner_entered_is_ctx2;
    let inner_current_not_ctx1;
    let inner_current_marker;
    let inner_entered_marker;
    {
        let inner = &mut v8::ContextScope::new(outer, ctx2);
        eval(inner, "globalThis.contextMarker = 'ctx2'").unwrap();
        let current = inner.get_current_context();
        let entered = inner.get_entered_or_microtask_context();
        inner_current_is_ctx2 = current == ctx2;
        inner_entered_is_ctx2 = entered == ctx2;
        inner_current_not_ctx1 = current != ctx1;
        let marker_key = v8::String::new(inner, "contextMarker").unwrap();
        inner_current_marker = current
            .global(inner)
            .get(inner, marker_key.into())
            .and_then(|value| value.to_string(inner))
            .map(|value| value.to_rust_string_lossy(inner))
            .unwrap_or_default();
        inner_entered_marker = entered
            .global(inner)
            .get(inner, marker_key.into())
            .and_then(|value| value.to_string(inner))
            .map(|value| value.to_rust_string_lossy(inner))
            .unwrap_or_default();
    }

    let restored_is_ctx1 = outer.get_current_context() == ctx1;
    let restored_entered_is_ctx1 = outer.get_entered_or_microtask_context() == ctx1;

    let actual = Json::obj(vec![
        ("outer_current_is_ctx1", Json::b(outer_current_is_ctx1)),
        ("outer_entered_is_ctx1", Json::b(outer_entered_is_ctx1)),
        ("outer_current_marker", Json::s(&outer_current_marker)),
        ("outer_entered_marker", Json::s(&outer_entered_marker)),
        ("global_identity_stable", Json::b(global_identity)),
        ("globals_distinct", Json::b(globals_distinct)),
        ("inner_current_is_ctx2", Json::b(inner_current_is_ctx2)),
        ("inner_entered_is_ctx2", Json::b(inner_entered_is_ctx2)),
        ("inner_current_not_ctx1", Json::b(inner_current_not_ctx1)),
        ("inner_current_marker", Json::s(&inner_current_marker)),
        ("inner_entered_marker", Json::s(&inner_entered_marker)),
        ("restored_is_ctx1", Json::b(restored_is_ctx1)),
        (
            "restored_entered_is_ctx1",
            Json::b(restored_entered_is_ctx1),
        ),
    ]);
    let expected = Json::obj(vec![
        ("outer_current_is_ctx1", Json::b(true)),
        ("outer_entered_is_ctx1", Json::b(true)),
        ("outer_current_marker", Json::s("ctx1")),
        ("outer_entered_marker", Json::s("ctx1")),
        ("global_identity_stable", Json::b(true)),
        ("globals_distinct", Json::b(true)),
        ("inner_current_is_ctx2", Json::b(true)),
        ("inner_entered_is_ctx2", Json::b(true)),
        ("inner_current_not_ctx1", Json::b(true)),
        ("inner_current_marker", Json::s("ctx2")),
        ("inner_entered_marker", Json::s("ctx2")),
        ("restored_is_ctx1", Json::b(true)),
        ("restored_entered_is_ctx1", Json::b(true)),
    ]);
    vec![expect_eq(
        "core-advanced/context/enter_exit_nesting",
        expected,
        actual,
    )]
}

/// Security tokens: fresh contexts in one isolate carry *distinct* default
/// tokens (not one shared value); `set_security_token` takes any value;
/// equal-content (but distinct) string tokens compare equal under
/// `same_value`; resetting to the default diverges from a customized
/// context; the same string object makes them equal again.
fn context_security_tokens() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let ctx_a = v8::Context::new(scope, Default::default());
    let ctx_b = v8::Context::new(scope, Default::default());

    let default_equal = {
        let sa = &mut v8::ContextScope::new(scope, ctx_a);
        let ta = ctx_a.get_security_token(sa);
        let sb = &mut v8::ContextScope::new(sa, ctx_b);
        ta.same_value(ctx_b.get_security_token(sb))
    };

    let token_a = v8::String::new(scope, "shield-a").unwrap();
    ctx_a.set_security_token(token_a.into());
    let diverges_from_b = {
        let sa = &mut v8::ContextScope::new(scope, ctx_a);
        let ta = ctx_a.get_security_token(sa);
        let sb = &mut v8::ContextScope::new(sa, ctx_b);
        !ta.same_value(ctx_b.get_security_token(sb))
    };

    // Distinct string object, identical content: SameValue says equal.
    let token_a_copy = v8::String::new(scope, "shield-a").unwrap();
    ctx_b.set_security_token(token_a_copy.into());
    let equal_content_equal = {
        let sa = &mut v8::ContextScope::new(scope, ctx_a);
        let ta = ctx_a.get_security_token(sa);
        let sb = &mut v8::ContextScope::new(sa, ctx_b);
        ta.same_value(ctx_b.get_security_token(sb))
    };

    ctx_b.use_default_security_token();
    let reset_diverges = {
        let sa = &mut v8::ContextScope::new(scope, ctx_a);
        let ta = ctx_a.get_security_token(sa);
        let sb = &mut v8::ContextScope::new(sa, ctx_b);
        !ta.same_value(ctx_b.get_security_token(sb))
    };

    // The exact same string object makes the tokens equal again.
    ctx_b.set_security_token(token_a.into());
    let same_object_equal = {
        let sa = &mut v8::ContextScope::new(scope, ctx_a);
        let ta = ctx_a.get_security_token(sa);
        let sb = &mut v8::ContextScope::new(sa, ctx_b);
        ta.same_value(ctx_b.get_security_token(sb))
    };

    let actual = Json::obj(vec![
        ("default_tokens_equal", Json::b(default_equal)),
        ("custom_a_diverges_from_default_b", Json::b(diverges_from_b)),
        (
            "equal_content_tokens_same_value",
            Json::b(equal_content_equal),
        ),
        ("reset_b_diverges_from_custom_a", Json::b(reset_diverges)),
        ("same_object_tokens_equal", Json::b(same_object_equal)),
    ]);
    let expected = Json::obj(vec![
        ("default_tokens_equal", Json::b(false)),
        ("custom_a_diverges_from_default_b", Json::b(true)),
        ("equal_content_tokens_same_value", Json::b(true)),
        ("reset_b_diverges_from_custom_a", Json::b(true)),
        ("same_object_tokens_equal", Json::b(true)),
    ]);
    vec![expect_eq(
        "core-advanced/context/security_tokens",
        expected,
        actual,
    )]
}

/// Context embedder data (values and aligned pointers) and the `Rc`-based
/// `Context` slots, including `clear_all_slots` semantics.
fn context_embedder_data_and_slots() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // The default embedder slot value is not a well-defined JS value here
    // (ToString on it is unsafe), so only predicates are recorded.
    let before_any_set = match context.get_embedder_data(scope, 0) {
        None => Json::s("none"),
        Some(v) => Json::obj(vec![
            ("null", Json::b(v.is_null())),
            ("undefined", Json::b(v.is_undefined())),
            ("int32", Json::b(v.is_int32())),
            ("string", Json::b(v.is_string())),
            ("number", Json::b(v.is_number())),
            ("object", Json::b(v.is_object())),
        ]),
    };
    context.set_embedder_data(0, v8::Integer::new(scope, 11).into());
    let read0 = context
        .get_embedder_data(scope, 0)
        .and_then(|v| v.integer_value(scope));
    context.set_embedder_data(1, v8::Integer::new(scope, 12).into());
    let read1 = context
        .get_embedder_data(scope, 1)
        .and_then(|v| v.integer_value(scope));
    let read0_after_slot1 = context
        .get_embedder_data(scope, 0)
        .and_then(|v| v.integer_value(scope));
    context.set_embedder_data(0, v8::Integer::new(scope, 13).into());
    let read0_overwritten = context
        .get_embedder_data(scope, 0)
        .and_then(|v| v.integer_value(scope));

    // Aligned pointer round-trip through embedder data slot 2.
    let boxed = Box::new(0xABCD_u64);
    let raw = Box::into_raw(boxed);
    unsafe {
        context.set_aligned_pointer_in_embedder_data(2, raw.cast::<std::ffi::c_void>());
    }
    let pointer_roundtrip = context
        .get_aligned_pointer_from_embedder_data(2)
        .cast::<u64>()
        == raw;
    drop(unsafe { Box::from_raw(raw) });

    // Rc slots: set returns the previous value instead of dropping it.
    let first_previous = context.set_slot(Rc::new(7_u32)).is_none();
    let first_read = context.get_slot::<u32>().map(|rc| *rc);
    let second_previous = context.set_slot(Rc::new(8_u32)).map(|rc| *rc);
    let second_read = context.get_slot::<u32>().map(|rc| *rc);
    let removed = context.remove_slot::<u32>().map(|rc| *rc);
    let removed_again = context.remove_slot::<u32>().is_none();
    let other_type_set = context.set_slot(Rc::new(99_u64)).is_none();
    let other_type_read = context.get_slot::<u64>().map(|rc| *rc);
    // The u32 slot was removed above, so it reads back empty here.
    let u32_gone = context.get_slot::<u32>().is_none();

    context.clear_all_slots();
    let after_clear_u64 = context.get_slot::<u64>().is_none();
    let after_clear_u32 = context.get_slot::<u32>().is_none();
    let embedder_survives_clear = context
        .get_embedder_data(scope, 0)
        .and_then(|v| v.integer_value(scope));
    let slot_set_again_after_clear = context.set_slot(Rc::new(5_u32)).is_none();
    let reinit_read = context.get_slot::<u32>().map(|rc| *rc);

    let opt_i64 = |value: Option<i64>| match value {
        Some(v) => Json::i(v),
        None => Json::Null,
    };
    let actual = Json::obj(vec![
        ("embedder_before_any_set", before_any_set),
        ("embedder_read0", Json::i(read0.unwrap_or(-1))),
        ("embedder_read1", Json::i(read1.unwrap_or(-1))),
        (
            "embedder_read0_after_slot1",
            Json::i(read0_after_slot1.unwrap_or(-1)),
        ),
        (
            "embedder_read0_overwritten",
            Json::i(read0_overwritten.unwrap_or(-1)),
        ),
        ("aligned_pointer_roundtrip", Json::b(pointer_roundtrip)),
        ("slot_first_previous_is_none", Json::b(first_previous)),
        (
            "slot_first_read",
            Json::i(first_read.map(i64::from).unwrap_or(-1)),
        ),
        (
            "slot_second_previous",
            opt_i64(second_previous.map(i64::from)),
        ),
        ("slot_second_read", opt_i64(second_read.map(i64::from))),
        ("slot_removed", opt_i64(removed.map(i64::from))),
        ("slot_removed_again_is_none", Json::b(removed_again)),
        ("slot_other_type_set", Json::b(other_type_set)),
        (
            "slot_other_type_read",
            opt_i64(other_type_read.map(|v| v as i64)),
        ),
        ("slot_u32_gone_after_remove", Json::b(u32_gone)),
        ("after_clear_u64_is_none", Json::b(after_clear_u64)),
        ("after_clear_u32_is_none", Json::b(after_clear_u32)),
        (
            "embedder_survives_clear",
            Json::i(embedder_survives_clear.unwrap_or(-1)),
        ),
        (
            "slot_set_again_after_clear",
            Json::b(slot_set_again_after_clear),
        ),
        (
            "slot_reinit_read",
            Json::i(reinit_read.map(i64::from).unwrap_or(-1)),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "embedder_before_any_set",
            Json::obj(vec![
                ("null", Json::b(false)),
                ("undefined", Json::b(false)),
                ("int32", Json::b(false)),
                ("string", Json::b(false)),
                ("number", Json::b(false)),
                ("object", Json::b(false)),
            ]),
        ),
        ("embedder_read0", Json::i(11)),
        ("embedder_read1", Json::i(12)),
        ("embedder_read0_after_slot1", Json::i(11)),
        ("embedder_read0_overwritten", Json::i(13)),
        ("aligned_pointer_roundtrip", Json::b(true)),
        ("slot_first_previous_is_none", Json::b(true)),
        ("slot_first_read", Json::i(7)),
        ("slot_second_previous", Json::i(7)),
        ("slot_second_read", Json::i(8)),
        ("slot_removed", Json::i(8)),
        ("slot_removed_again_is_none", Json::b(true)),
        ("slot_other_type_set", Json::b(true)),
        ("slot_other_type_read", Json::i(99)),
        ("slot_u32_gone_after_remove", Json::b(true)),
        ("after_clear_u64_is_none", Json::b(true)),
        ("after_clear_u32_is_none", Json::b(true)),
        ("embedder_survives_clear", Json::i(13)),
        ("slot_set_again_after_clear", Json::b(true)),
        ("slot_reinit_read", Json::i(5)),
    ]);
    vec![expect_eq(
        "core-advanced/context/embedder_data_and_slots",
        expected,
        actual,
    )]
}

/// Raw isolate data slots: bounded count, null by default, exact pointer
/// round-trip, per-slot independence.
fn slots_isolate_raw_data() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let slot_count = isolate.get_number_of_data_slots();

    let initial_null = isolate.get_data(0).is_null();
    let sentinel_a = Box::into_raw(Box::new(0x1111_u64));
    let sentinel_b = Box::into_raw(Box::new(0x2222_u64));
    isolate.set_data(0, sentinel_a.cast());
    let roundtrip_a = isolate.get_data(0).cast::<u64>() == sentinel_a;
    isolate.set_data(1, sentinel_b.cast());
    let slot1_roundtrip = isolate.get_data(1).cast::<u64>() == sentinel_b;
    let slot0_unaffected = isolate.get_data(0).cast::<u64>() == sentinel_a;
    isolate.set_data(0, std::ptr::null_mut());
    let cleared0 = isolate.get_data(0).is_null();
    let slot1_survives = isolate.get_data(1).cast::<u64>() == sentinel_b;
    drop(unsafe { Box::from_raw(sentinel_a) });
    drop(unsafe { Box::from_raw(sentinel_b) });

    let actual = Json::obj(vec![
        ("slot_count", Json::i(i64::from(slot_count))),
        ("initial_null", Json::b(initial_null)),
        ("roundtrip_a", Json::b(roundtrip_a)),
        ("slot1_roundtrip", Json::b(slot1_roundtrip)),
        ("slot0_unaffected", Json::b(slot0_unaffected)),
        ("cleared0_null", Json::b(cleared0)),
        ("slot1_survives", Json::b(slot1_survives)),
    ]);
    let expected = Json::obj(vec![
        ("slot_count", Json::i(3)),
        ("initial_null", Json::b(true)),
        ("roundtrip_a", Json::b(true)),
        ("slot1_roundtrip", Json::b(true)),
        ("slot0_unaffected", Json::b(true)),
        ("cleared0_null", Json::b(true)),
        ("slot1_survives", Json::b(true)),
    ]);
    vec![expect_eq(
        "core-advanced/slots/isolate_raw_data_slots",
        expected,
        actual,
    )]
}

/// Several typed slots coexist in one isolate; removal is per-type and
/// re-setting after removal reports "first set" again.
fn slots_isolate_multiple_types() -> Vec<CheckOutcome> {
    struct Alpha {
        value: u32,
    }
    struct Beta {
        label: &'static str,
    }

    let isolate = &mut v8::Isolate::new(Default::default());
    let set_alpha = isolate.set_slot(Alpha { value: 1 });
    let set_beta = isolate.set_slot(Beta { label: "beta" });
    let alpha_read = isolate.get_slot::<Alpha>().map(|a| a.value);
    let beta_read = isolate.get_slot::<Beta>().map(|b| b.label);
    let removed_alpha = isolate.remove_slot::<Alpha>().map(|a| a.value);
    let removed_alpha_again = isolate.remove_slot::<Alpha>().is_none();
    let beta_survives = isolate.get_slot::<Beta>().map(|b| b.label);
    let set_alpha_again = isolate.set_slot(Alpha { value: 2 });
    let alpha_read_again = isolate.get_slot::<Alpha>().map(|a| a.value);

    let label_to_json = |label: Option<&'static str>| match label {
        Some(l) => Json::s(l),
        None => Json::Null,
    };
    let actual = Json::obj(vec![
        ("set_alpha_first", Json::b(set_alpha)),
        ("set_beta_first", Json::b(set_beta)),
        ("alpha_read", Json::i(alpha_read.unwrap_or(u32::MAX) as i64)),
        ("beta_read", label_to_json(beta_read)),
        (
            "removed_alpha",
            Json::i(removed_alpha.unwrap_or(u32::MAX) as i64),
        ),
        ("removed_alpha_again_is_none", Json::b(removed_alpha_again)),
        ("beta_survives", label_to_json(beta_survives)),
        ("set_alpha_again_first", Json::b(set_alpha_again)),
        (
            "alpha_read_again",
            Json::i(alpha_read_again.unwrap_or(u32::MAX) as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        ("set_alpha_first", Json::b(true)),
        ("set_beta_first", Json::b(true)),
        ("alpha_read", Json::i(1)),
        ("beta_read", Json::s("beta")),
        ("removed_alpha", Json::i(1)),
        ("removed_alpha_again_is_none", Json::b(true)),
        ("beta_survives", Json::s("beta")),
        ("set_alpha_again_first", Json::b(true)),
        ("alpha_read_again", Json::i(2)),
    ]);
    vec![expect_eq(
        "core-advanced/slots/isolate_multiple_types",
        expected,
        actual,
    )]
}

/// `ScriptOrigin` round-trip: resource name, source-map URL and the origin
/// script id are preserved verbatim; the compiled script gets its own
/// V8-assigned id (positive and distinct from the origin's 777 and from a
/// separately compiled script's id).
fn script_origin_roundtrip() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let origin = make_origin(scope, "app.js", 0, 0, 777, Some("map.url"), true, true);
    let (script, value) = eval_with_origin(scope, "1 + 1", &origin);

    let script_id = script.as_ref().map(|s| s.script_id()).unwrap_or(0);
    let script_matches_origin_id = script_id == 777;
    let script_id_positive = script_id > 0;
    let src2 = v8::String::new(scope, "2 + 2").unwrap();
    let plain_id = v8::Script::compile(scope, src2, None).map(|s| s.script_id());

    let resource_name = origin
        .resource_name()
        .map(|v| value_text(scope, v))
        .unwrap_or_default();
    let source_map_url = origin
        .source_map_url()
        .map(|v| value_text(scope, v))
        .unwrap_or_default();

    let actual = Json::obj(vec![
        ("origin_script_id", Json::i(i64::from(origin.script_id()))),
        ("resource_name", Json::s(&resource_name)),
        ("source_map_url", Json::s(&source_map_url)),
        (
            "script_matches_origin_id",
            Json::b(script_matches_origin_id),
        ),
        ("script_id_positive", Json::b(script_id_positive)),
        ("plain_id_distinct", Json::b(plain_id != Some(script_id))),
        (
            "run_value",
            Json::i(value.and_then(|v| v.integer_value(scope)).unwrap_or(-1)),
        ),
    ]);
    let expected = Json::obj(vec![
        ("origin_script_id", Json::i(777)),
        ("resource_name", Json::s("app.js")),
        ("source_map_url", Json::s("map.url")),
        ("script_matches_origin_id", Json::b(false)),
        ("script_id_positive", Json::b(true)),
        ("plain_id_distinct", Json::b(true)),
        ("run_value", Json::i(2)),
    ]);
    vec![expect_eq(
        "core-advanced/script/origin_roundtrip",
        expected,
        actual,
    )]
}

/// Origin line/column offsets shift the reported exception positions while
/// the raw source line and in-source character positions stay exact.
fn script_origin_shifts_positions() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let origin = make_origin(scope, "shift.js", 100, 5, 0, None, false, false);
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let source = v8::String::new(tc, "\nthrow new Error('boom')\n").unwrap();
    let script = v8::Script::compile(tc, source, Some(&origin)).unwrap();
    let ran = script.run(tc).is_some();

    let message = tc.message().expect("throw must produce a message");
    let actual = Json::obj(vec![
        ("run_none", Json::b(!ran)),
        ("text", Json::s(&message.get(tc).to_rust_string_lossy(tc))),
        (
            "line_number",
            Json::i(message.get_line_number(tc).unwrap_or(0) as i64),
        ),
        (
            "start_position",
            Json::i(i64::from(message.get_start_position())),
        ),
        (
            "end_position",
            Json::i(i64::from(message.get_end_position())),
        ),
        ("start_column", Json::i(message.get_start_column() as i64)),
        ("end_column", Json::i(message.get_end_column() as i64)),
        (
            "source_line",
            Json::s(
                &message
                    .get_source_line(tc)
                    .map(|s| s.to_rust_string_lossy(tc))
                    .unwrap_or_default(),
            ),
        ),
        (
            "resource_name",
            Json::s(
                &message
                    .get_script_resource_name(tc)
                    .map(|v| value_text(tc, v))
                    .unwrap_or_default(),
            ),
        ),
        ("error_level", Json::i(i64::from(message.error_level()))),
        ("is_opaque", Json::b(message.is_opaque())),
        (
            "is_shared_cross_origin",
            Json::b(message.is_shared_cross_origin()),
        ),
    ]);
    let expected = Json::obj(vec![
        ("run_none", Json::b(true)),
        ("text", Json::s("Uncaught Error: boom")),
        // Line offset (100) shifts the reported line; the column offset (5)
        // is NOT folded into message columns; positions point at the
        // `throw` keyword region; MessageErrorLevel is the 8-bit level 8.
        ("line_number", Json::i(102)),
        ("start_position", Json::i(1)),
        ("end_position", Json::i(2)),
        ("start_column", Json::i(0)),
        ("end_column", Json::i(1)),
        ("source_line", Json::s("throw new Error('boom')")),
        ("resource_name", Json::s("shift.js")),
        ("error_level", Json::i(8)),
        ("is_opaque", Json::b(false)),
        ("is_shared_cross_origin", Json::b(false)),
    ]);
    vec![expect_eq(
        "core-advanced/script/origin_shifts_exception_positions",
        expected,
        actual,
    )]
}

/// An `UnboundScript` compiled in one context runs in that context, keeps
/// its script id when bound there, and can be re-bound into a second
/// context with fully separated globals.
fn script_unbound_rebind() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let ctx1 = v8::Context::new(scope, Default::default());
    let ctx2 = v8::Context::new(scope, Default::default());
    let cs1 = &mut v8::ContextScope::new(scope, ctx1);

    let source =
        v8::String::new(cs1, "globalThis.n = (globalThis.n | 0) + 1; globalThis.n").unwrap();
    let script1 = v8::Script::compile(cs1, source, None).unwrap();
    let unbound = script1.get_unbound_script(cs1);
    let id_matches = script1.script_id() == unbound.script_id();

    let ctx1_first = script1
        .run(cs1)
        .and_then(|v| v.integer_value(cs1))
        .unwrap_or(-1);
    let bound1 = unbound.bind_to_current_context(cs1);
    let ctx1_second = bound1
        .run(cs1)
        .and_then(|v| v.integer_value(cs1))
        .unwrap_or(-1);

    let ctx2_first;
    {
        let cs2 = &mut v8::ContextScope::new(cs1, ctx2);
        let bound2 = unbound.bind_to_current_context(cs2);
        ctx2_first = bound2
            .run(cs2)
            .and_then(|v| v.integer_value(cs2))
            .unwrap_or(-1);
    }

    let ctx1_after = eval(cs1, "globalThis.n")
        .and_then(|v| v.integer_value(cs1))
        .unwrap_or(-1);

    let actual = Json::obj(vec![
        ("ids_match_script_unbound", Json::b(id_matches)),
        ("ctx1_first", Json::i(ctx1_first)),
        ("ctx1_second", Json::i(ctx1_second)),
        ("ctx2_first", Json::i(ctx2_first)),
        ("ctx1_after_ctx2_run", Json::i(ctx1_after)),
    ]);
    let expected = Json::obj(vec![
        ("ids_match_script_unbound", Json::b(true)),
        ("ctx1_first", Json::i(1)),
        ("ctx1_second", Json::i(2)),
        ("ctx2_first", Json::i(1)),
        ("ctx1_after_ctx2_run", Json::i(2)),
    ]);
    vec![expect_eq(
        "core-advanced/script/unbound_rebind",
        expected,
        actual,
    )]
}

/// `ScriptCompiler` entry points and options: unbound eager compile,
/// plain-`Source` compile without cache data, `CompileFunction` with
/// declared parameters, and the pinned `cached_data_version_tag`.
fn script_compiler_options() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let origin = make_origin(scope, "eager.js", 0, 0, 0, None, false, false);
    let source_string = v8::String::new(scope, "1 + 2").unwrap();
    let mut source = v8::script_compiler::Source::new(source_string, Some(&origin));
    let unbound = v8::script_compiler::compile_unbound_script(
        scope,
        &mut source,
        v8::script_compiler::CompileOptions::EagerCompile,
        v8::script_compiler::NoCacheReason::NoReason,
    );
    let unbound_ok = unbound.is_some();
    let unbound_id_matches_origin_zero = unbound.as_ref().map(|u| u.script_id()) == Some(0);
    let no_cached_data = source.get_cached_data().is_none();
    let version_tag = v8::script_compiler::cached_data_version_tag();

    let fn_source_string = v8::String::new(scope, "return a * b;").unwrap();
    let mut fn_source = v8::script_compiler::Source::new(fn_source_string, None);
    let arg_a = v8::String::new(scope, "a").unwrap();
    let arg_b = v8::String::new(scope, "b").unwrap();
    let function = v8::script_compiler::compile_function(
        scope,
        &mut fn_source,
        &[arg_a, arg_b],
        &[],
        v8::script_compiler::CompileOptions::NoCompileOptions,
        v8::script_compiler::NoCacheReason::NoReason,
    );
    let call_result = function.and_then(|f| {
        f.call(
            scope,
            v8::undefined(scope).into(),
            &[
                v8::Number::new(scope, 6.0).into(),
                v8::Number::new(scope, 7.0).into(),
            ],
        )
        .and_then(|v| v.integer_value(scope))
    });

    let actual = Json::obj(vec![
        ("unbound_eager_ok", Json::b(unbound_ok)),
        (
            "unbound_id_matches_origin_zero",
            Json::b(unbound_id_matches_origin_zero),
        ),
        ("source_has_no_cached_data", Json::b(no_cached_data)),
        ("cached_data_version_tag", Json::i(i64::from(version_tag))),
        ("compile_function_call", Json::i(call_result.unwrap_or(-1))),
    ]);
    let expected = Json::obj(vec![
        ("unbound_eager_ok", Json::b(true)),
        // The origin's script id (0 = "unassigned") is ignored on fresh
        // compiles: the unbound script gets a V8-assigned id.
        ("unbound_id_matches_origin_zero", Json::b(false)),
        ("source_has_no_cached_data", Json::b(true)),
        ("cached_data_version_tag", Json::i(3252425384)),
        ("compile_function_call", Json::i(42)),
    ]);
    vec![expect_eq(
        "core-advanced/script/compiler_options_and_unbound",
        expected,
        actual,
    )]
}

/// Code-cache produce/consume round-trip across two isolates: the cache is
/// produced from an unbound script, consumed with `ConsumeCodeCache`
/// without rejection, and the consumed script runs correctly. Cache bytes
/// are intentionally not pinned (they embed build- and seed-dependent
/// data); length positivity and the `rejected` flag are the contract.
fn script_code_cache_roundtrip() -> Vec<CheckOutcome> {
    const SOURCE: &str = "(function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); })(12)";

    // Produce: the producing isolate is dropped before the consumer one is
    // created (a code cache is plain bytes, not a handle).
    let cache_bytes: Vec<u8> = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let origin = make_origin(scope, "cached.js", 0, 0, 0, None, false, false);
        let source_string = v8::String::new(scope, SOURCE).unwrap();
        let mut source = v8::script_compiler::Source::new(source_string, Some(&origin));
        let unbound = v8::script_compiler::compile_unbound_script(
            scope,
            &mut source,
            v8::script_compiler::CompileOptions::NoCompileOptions,
            v8::script_compiler::NoCacheReason::NoReason,
        )
        .expect("compile for cache production");
        let cache = unbound
            .create_code_cache()
            .expect("code cache must be produced");
        cache.iter().copied().collect()
    };
    let cache_len = cache_bytes.len();

    let (consume_ok, rejected, run_value) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let origin = make_origin(scope, "cached.js", 0, 0, 0, None, false, false);
        let source_string = v8::String::new(scope, SOURCE).unwrap();
        let cached_data = v8::script_compiler::CachedData::new(&cache_bytes);
        let mut source = v8::script_compiler::Source::new_with_cached_data(
            source_string,
            Some(&origin),
            cached_data,
        );
        let script = v8::script_compiler::compile(
            scope,
            &mut source,
            v8::script_compiler::CompileOptions::ConsumeCodeCache,
            v8::script_compiler::NoCacheReason::NoReason,
        );
        let rejected = source
            .get_cached_data()
            .map(|c| c.rejected())
            .unwrap_or(true);
        let run = script
            .and_then(|s| s.run(scope))
            .and_then(|v| v.integer_value(scope));
        (script.is_some(), rejected, run.unwrap_or(-1))
    };

    let actual = Json::obj(vec![
        ("cache_produced", Json::b(cache_len > 0)),
        ("consume_compiles", Json::b(consume_ok)),
        ("cache_rejected", Json::b(rejected)),
        ("run_value", Json::i(run_value)),
    ]);
    let expected = Json::obj(vec![
        ("cache_produced", Json::b(true)),
        ("consume_compiles", Json::b(true)),
        ("cache_rejected", Json::b(false)),
        ("run_value", Json::i(144)),
    ]);
    vec![expect_eq(
        "core-advanced/script/code_cache_roundtrip",
        expected,
        actual,
    )]
    // NOTE: the corruption boundary of this contract is characterized in
    // `tests/core_advanced_negative.rs`:
    // `code_cache_corruption_is_v8_fatal`. Consuming a corrupted cache in
    // this build is a V8 deserializer fatal ("unreachable code"), not a
    // graceful `rejected: true`, so it must never run inside the fixture.
}

/// `Message` detail getters for a runtime TypeError raised under a
/// scripted origin: exact message text, 1-based line, source line, resource
/// name, in-source positions, error level and origin-flag pass-through.
fn message_exception_details() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let origin = make_origin(scope, "detail.js", 0, 0, 0, None, false, false);
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let source = v8::String::new(tc, "function boom() {\n  null.f();\n}\nboom();\n").unwrap();
    let script = v8::Script::compile(tc, source, Some(&origin)).unwrap();
    let ran = script.run(tc).is_some();
    let message = tc.message().expect("runtime error must produce a message");

    let actual = Json::obj(vec![
        ("run_none", Json::b(!ran)),
        ("text", Json::s(&message.get(tc).to_rust_string_lossy(tc))),
        (
            "line_number",
            Json::i(message.get_line_number(tc).unwrap_or(0) as i64),
        ),
        (
            "source_line",
            Json::s(
                &message
                    .get_source_line(tc)
                    .map(|s| s.to_rust_string_lossy(tc))
                    .unwrap_or_default(),
            ),
        ),
        (
            "resource_name",
            Json::s(
                &message
                    .get_script_resource_name(tc)
                    .map(|v| value_text(tc, v))
                    .unwrap_or_default(),
            ),
        ),
        (
            "start_position",
            Json::i(i64::from(message.get_start_position())),
        ),
        (
            "end_position",
            Json::i(i64::from(message.get_end_position())),
        ),
        ("start_column", Json::i(message.get_start_column() as i64)),
        ("end_column", Json::i(message.get_end_column() as i64)),
        ("error_level", Json::i(i64::from(message.error_level()))),
        ("is_opaque", Json::b(message.is_opaque())),
        (
            "is_shared_cross_origin",
            Json::b(message.is_shared_cross_origin()),
        ),
        (
            "exception_text",
            Json::s(
                &tc.exception()
                    .map(|e| value_text(tc, e))
                    .unwrap_or_default(),
            ),
        ),
        (
            "message_stack_trace_is_none",
            Json::b(message.get_stack_trace(tc).is_none()),
        ),
    ]);
    let expected = Json::obj(vec![
        ("run_none", Json::b(true)),
        (
            "text",
            Json::s("Uncaught TypeError: Cannot read properties of null (reading 'f')"),
        ),
        ("line_number", Json::i(2)),
        ("source_line", Json::s("  null.f();")),
        ("resource_name", Json::s("detail.js")),
        // Positions/columns point at the failed property load (`f`), not at
        // the receiver: 0-based column 7 on line 2, char range 25..=25.
        ("start_position", Json::i(25)),
        ("end_position", Json::i(26)),
        ("start_column", Json::i(7)),
        ("end_column", Json::i(8)),
        ("error_level", Json::i(8)),
        ("is_opaque", Json::b(false)),
        ("is_shared_cross_origin", Json::b(false)),
        (
            "exception_text",
            Json::s("TypeError: Cannot read properties of null (reading 'f')"),
        ),
        ("message_stack_trace_is_none", Json::b(true)),
    ]);
    vec![expect_eq(
        "core-advanced/message/exception_details_with_origin",
        expected,
        actual,
    )]
}

/// Native callback capturing `StackTrace::current_stack_trace` frames.
fn frames_callback(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let script_name = v8::StackTrace::current_script_name_or_source_url(scope)
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default();
    let mut frames = Vec::new();
    let mut count = 0usize;
    if let Some(trace) = v8::StackTrace::current_stack_trace(scope, 16) {
        count = trace.get_frame_count();
        for index in 0..count {
            let frame = match trace.get_frame(scope, index) {
                Some(frame) => frame,
                None => break,
            };
            let function_name = frame
                .get_function_name(scope)
                .map(|s| s.to_rust_string_lossy(scope));
            let script = frame
                .get_script_name(scope)
                .map(|s| s.to_rust_string_lossy(scope));
            frames.push(Json::obj(vec![
                (
                    "function",
                    match function_name {
                        Some(name) => Json::s(&name),
                        None => Json::Null,
                    },
                ),
                ("line", Json::i(frame.get_line_number() as i64)),
                ("column", Json::i(frame.get_column() as i64)),
                (
                    "script",
                    match script {
                        Some(name) => Json::s(&name),
                        None => Json::Null,
                    },
                ),
                ("script_id_positive", Json::b(frame.get_script_id() > 0)),
                ("is_eval", Json::b(frame.is_eval())),
                ("is_constructor", Json::b(frame.is_constructor())),
                ("is_wasm", Json::b(frame.is_wasm())),
                ("is_user_javascript", Json::b(frame.is_user_javascript())),
            ]));
        }
    }
    let encoded = Json::obj(vec![
        ("frame_count", Json::i(count as i64)),
        ("frames", Json::arr(frames)),
        ("current_script_name", Json::s(&script_name)),
    ]);
    *FRAMES_CAPTURE.lock().unwrap() = Some(encoded.to_json_string());
    rv.set_int32(1);
}

/// Frames captured with `StackTrace::current_stack_trace` inside a native
/// callback invoked from JS: the native (callback) frame itself is NOT part
/// of the JS stack trace — frames are the JS callers only, topmost first.
fn message_current_stack_frames() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let host = v8::Function::builder(frames_callback)
        .build(scope)
        .expect("native function");
    let name = v8::String::new(scope, "host").unwrap();
    context
        .global(scope)
        .set(scope, name.into(), host.into())
        .expect("set global");

    let origin = make_origin(scope, "frames.js", 0, 0, 0, None, false, false);
    let _ = eval_with_origin(
        scope,
        "function target(n) { return host(n); }\nglobalThis.result = target(9);",
        &origin,
    );

    let captured = FRAMES_CAPTURE.lock().unwrap().take().unwrap_or_default();
    let observed = captured_json(&captured);
    let expected_captured = Json::obj(vec![
        ("frame_count", Json::i(2)),
        (
            "frames",
            Json::arr(vec![
                Json::obj(vec![
                    ("function", Json::s("target")),
                    ("line", Json::i(1)),
                    ("column", Json::i(29)),
                    ("script", Json::s("frames.js")),
                    ("script_id_positive", Json::b(true)),
                    ("is_eval", Json::b(false)),
                    ("is_constructor", Json::b(false)),
                    ("is_wasm", Json::b(false)),
                    ("is_user_javascript", Json::b(true)),
                ]),
                Json::obj(vec![
                    ("function", Json::Null),
                    ("line", Json::i(2)),
                    ("column", Json::i(21)),
                    ("script", Json::s("frames.js")),
                    ("script_id_positive", Json::b(true)),
                    ("is_eval", Json::b(false)),
                    ("is_constructor", Json::b(false)),
                    ("is_wasm", Json::b(false)),
                    ("is_user_javascript", Json::b(true)),
                ]),
            ]),
        ),
        ("current_script_name", Json::s("frames.js")),
    ]);
    vec![expect_eq(
        "core-advanced/message/current_stack_frames",
        expected_captured,
        observed,
    )]
}

/// Parses captured text back into the canonical `Json` tree. The text was
/// produced by `Json::to_json_string` on this exact shape, so a recursive
/// descent over the documented grammar is a faithful structural re-parse.
fn captured_json(text: &str) -> Json {
    struct Parser<'a> {
        bytes: &'a [u8],
        pos: usize,
    }
    impl Parser<'_> {
        fn skip_ws(&mut self) {
            while self.pos < self.bytes.len()
                && matches!(self.bytes[self.pos], b' ' | b'\t' | b'\n' | b'\r')
            {
                self.pos += 1;
            }
        }
        fn value(&mut self) -> Option<Json> {
            self.skip_ws();
            match *self.bytes.get(self.pos)? {
                b'{' => {
                    self.pos += 1;
                    let mut pairs = Vec::new();
                    self.skip_ws();
                    if self.bytes.get(self.pos) == Some(&b'}') {
                        self.pos += 1;
                        return Some(Json::obj(pairs));
                    }
                    loop {
                        self.skip_ws();
                        let key = match self.value()? {
                            Json::Str(k) => {
                                // Keys are 'static in the writer; leak is fine
                                // (captured text is tiny and fixed).
                                let leaked: &'static str = Box::leak(k.into_boxed_str());
                                leaked
                            }
                            _ => return None,
                        };
                        self.skip_ws();
                        if self.bytes.get(self.pos) != Some(&b':') {
                            return None;
                        }
                        self.pos += 1;
                        let value = self.value()?;
                        pairs.push((key, value));
                        self.skip_ws();
                        match self.bytes.get(self.pos) {
                            Some(b',') => self.pos += 1,
                            Some(b'}') => {
                                self.pos += 1;
                                return Some(Json::obj(pairs));
                            }
                            _ => return None,
                        }
                    }
                }
                b'[' => {
                    self.pos += 1;
                    let mut items = Vec::new();
                    self.skip_ws();
                    if self.bytes.get(self.pos) == Some(&b']') {
                        self.pos += 1;
                        return Some(Json::arr(items));
                    }
                    loop {
                        let value = self.value()?;
                        items.push(value);
                        self.skip_ws();
                        match self.bytes.get(self.pos) {
                            Some(b',') => self.pos += 1,
                            Some(b']') => {
                                self.pos += 1;
                                return Some(Json::arr(items));
                            }
                            _ => return None,
                        }
                    }
                }
                b'"' => {
                    self.pos += 1;
                    let mut out = String::new();
                    loop {
                        let byte = *self.bytes.get(self.pos)?;
                        self.pos += 1;
                        match byte {
                            b'"' => return Some(Json::Str(out)),
                            b'\\' => {
                                let esc = *self.bytes.get(self.pos)?;
                                self.pos += 1;
                                match esc {
                                    b'"' => out.push('"'),
                                    b'\\' => out.push('\\'),
                                    b'n' => out.push('\n'),
                                    b'r' => out.push('\r'),
                                    b't' => out.push('\t'),
                                    b'b' => out.push('\u{08}'),
                                    b'f' => out.push('\u{0C}'),
                                    b'u' => {
                                        let hex = std::str::from_utf8(
                                            &self.bytes[self.pos..self.pos + 4],
                                        )
                                        .ok()?;
                                        let code = u32::from_str_radix(hex, 16).ok()?;
                                        self.pos += 4;
                                        out.push(char::from_u32(code)?);
                                    }
                                    _ => return None,
                                }
                            }
                            _ => {
                                // Copy the full UTF-8 sequence verbatim.
                                let start = self.pos - 1;
                                let mut end = self.pos;
                                while end < self.bytes.len() && (self.bytes[end] & 0xC0) == 0x80 {
                                    end += 1;
                                }
                                out.push_str(std::str::from_utf8(&self.bytes[start..end]).ok()?);
                                self.pos = end;
                            }
                        }
                    }
                }
                b't' => {
                    self.pos += 4;
                    Some(Json::Bool(true))
                }
                b'f' => {
                    self.pos += 5;
                    Some(Json::Bool(false))
                }
                b'n' => {
                    self.pos += 4;
                    Some(Json::Null)
                }
                b'-' | b'0'..=b'9' => {
                    let start = self.pos;
                    if self.bytes[self.pos] == b'-' {
                        self.pos += 1;
                    }
                    while self
                        .bytes
                        .get(self.pos)
                        .map(|b| b.is_ascii_digit())
                        .unwrap_or(false)
                    {
                        self.pos += 1;
                    }
                    let text = std::str::from_utf8(&self.bytes[start..self.pos]).ok()?;
                    text.parse::<i64>().ok().map(Json::Int)
                }
                _ => None,
            }
        }
    }
    let mut parser = Parser {
        bytes: text.as_bytes(),
        pos: 0,
    };
    parser.value().unwrap_or(Json::Null)
}

/// `Exception::capture_stack_trace` on a plain object, the native-error
/// `get_stack_trace` gap, and uncaught-exception stack capture toggled by
/// `set_capture_stack_trace_for_uncaught_exceptions` with a frame limit.
/// The default-vs-enabled comparison uses separate isolates because the
/// capture flag is isolate-level and one-way per check.
fn message_uncaught_capture() -> Vec<CheckOutcome> {
    // (a)+(b): plain-object capture and the native-error trace gap.
    let (captured, plain_stack_first_line, native_trace_is_none) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);

        let plain = v8::Object::new(scope);
        let captured = v8::Exception::capture_stack_trace(context, plain);
        let plain_stack_first_line = plain
            .get(scope, v8::String::new(scope, "stack").unwrap().into())
            .and_then(|v| v.to_string(scope))
            .map(|s| {
                s.to_rust_string_lossy(scope)
                    .split('\n')
                    .next()
                    .unwrap_or_default()
                    .to_owned()
            })
            .unwrap_or_default();

        let native_error =
            v8::Exception::error(scope, v8::String::new(scope, "native-err").unwrap());
        let native_trace_is_none = v8::Exception::get_stack_trace(scope, native_error).is_none();
        (captured, plain_stack_first_line, native_trace_is_none)
    };

    // (c) default: an uncaught exception's Message carries no stack trace.
    let default_uncaught_trace_is_none = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let src = v8::String::new(tc, "function f1() { throw new Error('x'); } f1();").unwrap();
        let _ = v8::Script::compile(tc, src, None).unwrap().run(tc);
        tc.message()
            .map(|m| m.get_stack_trace(tc).is_none())
            .unwrap_or(false)
    };

    // (d) enabling capture with a frame limit attaches a truncated trace.
    let enabled_trace = {
        let mut isolate = v8::Isolate::new(Default::default());
        isolate.set_capture_stack_trace_for_uncaught_exceptions(true, 3);
        v8::scope!(let scope, &mut *isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let src = v8::String::new(
            tc,
            "function d1() { d2(); }\nfunction d2() { d3(); }\nfunction d3() { throw new Error('deep'); }\nd1();",
        )
        .unwrap();
        let _ = v8::Script::compile(tc, src, None).unwrap().run(tc);
        tc.message().and_then(|m| {
            m.get_stack_trace(tc).map(|trace| {
                let count = trace.get_frame_count();
                let names: Vec<Json> = (0..count)
                    .filter_map(|i| trace.get_frame(tc, i))
                    .map(|frame| match frame.get_function_name(tc) {
                        Some(name) => Json::s(&name.to_rust_string_lossy(tc)),
                        None => Json::Null,
                    })
                    .collect();
                Json::obj(vec![
                    ("frame_count", Json::i(count as i64)),
                    ("function_names", Json::arr(names)),
                ])
            })
        })
    };

    let actual = Json::obj(vec![
        (
            "capture_on_plain_object_ok",
            Json::b(captured == Some(true)),
        ),
        ("plain_stack_first_line", Json::s(&plain_stack_first_line)),
        ("native_error_trace_is_none", Json::b(native_trace_is_none)),
        (
            "default_uncaught_trace_is_none",
            Json::b(default_uncaught_trace_is_none),
        ),
        (
            "enabled_trace",
            match enabled_trace {
                Some(shape) => shape,
                None => Json::Null,
            },
        ),
    ]);
    let expected = Json::obj(vec![
        ("capture_on_plain_object_ok", Json::b(true)),
        ("plain_stack_first_line", Json::s("Error")),
        ("native_error_trace_is_none", Json::b(true)),
        ("default_uncaught_trace_is_none", Json::b(true)),
        (
            "enabled_trace",
            Json::obj(vec![
                ("frame_count", Json::i(3)),
                (
                    "function_names",
                    Json::arr(vec![Json::s("d3"), Json::s("d2"), Json::s("d1")]),
                ),
            ]),
        ),
    ]);
    vec![expect_eq(
        "core-advanced/message/uncaught_capture_and_capture_stack_trace",
        expected,
        actual,
    )]
}

/// Same-thread termination requested before any JS runs: the pending
/// request is invisible to `is_execution_terminating()` until delivery, the
/// first `Script::run` returns `None` with a *clean* TryCatch (no caught
/// exception, `has_terminated` false, `can_continue` false), the flag then
/// becomes observable, and the isolate self-recovers on the next run — a
/// later `cancel_terminate_execution()` is accepted even though nothing is
/// pending. (Mid-execution delivery behaves differently; see the shared
/// isolate check above: `has_caught`/`has_terminated` true there.)
fn terminate_same_thread_lifecycle() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    // The handle mirrors the isolate-level methods (same FFI entry points)
    // and stays usable while the TryCatch holds the scope borrow.
    let handle = isolate.thread_safe_handle();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let initial = handle.is_execution_terminating();
    let terminate_ok = handle.terminate_execution();
    let after_request = handle.is_execution_terminating();

    let (ran, has_caught, has_terminated, can_continue, after_delivery, rerun_ran, after_reset) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let src = v8::String::new(tc, "1 + 1").unwrap();
        let script = v8::Script::compile(tc, src, None).unwrap();
        let ran = script.run(tc).is_some();
        let after_delivery = handle.is_execution_terminating();
        // Sticky: another run in the same TryCatch also fails without cancel.
        let rerun_ran = script.run(tc).is_some();
        // Reset does not clear the pending termination.
        tc.reset();
        let after_reset = handle.is_execution_terminating();
        (
            ran,
            tc.has_caught(),
            tc.has_terminated(),
            tc.can_continue(),
            after_delivery,
            rerun_ran,
            after_reset,
        )
    };

    let cancel_ok = handle.cancel_terminate_execution();
    let after_cancel = handle.is_execution_terminating();
    let recovered = eval(scope, "40 + 2")
        .and_then(|v| v.integer_value(scope))
        .unwrap_or(-1);

    let actual = Json::obj(vec![
        ("initial_terminating", Json::b(initial)),
        ("terminate_ok", Json::b(terminate_ok)),
        ("after_request", Json::b(after_request)),
        ("run_none", Json::b(!ran)),
        ("has_caught", Json::b(has_caught)),
        ("has_terminated", Json::b(has_terminated)),
        ("can_continue", Json::b(can_continue)),
        ("after_delivery", Json::b(after_delivery)),
        ("rerun_succeeded", Json::b(rerun_ran)),
        ("after_reset", Json::b(after_reset)),
        ("cancel_ok", Json::b(cancel_ok)),
        ("after_cancel", Json::b(after_cancel)),
        ("recovered", Json::i(recovered)),
    ]);
    let expected = Json::obj(vec![
        ("initial_terminating", Json::b(false)),
        ("terminate_ok", Json::b(true)),
        ("after_request", Json::b(false)),
        ("run_none", Json::b(true)),
        ("has_caught", Json::b(false)),
        ("has_terminated", Json::b(false)),
        ("can_continue", Json::b(false)),
        ("after_delivery", Json::b(true)),
        ("rerun_succeeded", Json::b(true)),
        ("after_reset", Json::b(false)),
        ("cancel_ok", Json::b(true)),
        ("after_cancel", Json::b(false)),
        ("recovered", Json::i(42)),
    ]);
    vec![expect_eq(
        "core-advanced/terminate/same_thread_flag_lifecycle",
        expected,
        actual,
    )]
}

/// Cancelling the termination request before it is delivered leaves the
/// isolate fully usable: the next script simply runs.
fn terminate_cancel_before_delivery() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let terminate_ok = scope.terminate_execution();
    let cancel_ok = scope.cancel_terminate_execution();

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let value = eval(tc, "6 + 1").and_then(|v| v.integer_value(tc));
    let actual = Json::obj(vec![
        ("terminate_ok", Json::b(terminate_ok)),
        ("cancel_ok", Json::b(cancel_ok)),
        ("has_caught", Json::b(tc.has_caught())),
        ("result", Json::i(value.unwrap_or(-1))),
    ]);
    let expected = Json::obj(vec![
        ("terminate_ok", Json::b(true)),
        ("cancel_ok", Json::b(true)),
        ("has_caught", Json::b(false)),
        ("result", Json::i(7)),
    ]);
    vec![expect_eq(
        "core-advanced/terminate/cancel_before_delivery",
        expected,
        actual,
    )]
}

/// `request_interrupt` delivers exactly one callback invocation during the
/// next JS execution (here: a bounded loop), on the requesting thread,
/// without terminating execution and with the data pointer preserved.
fn terminate_interrupt_callback() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let handle = isolate.thread_safe_handle();
    let state = Box::leak(Box::new(InterruptState {
        count: AtomicUsize::new(0),
        requested_thread_matches: AtomicBool::new(false),
        terminating_at_delivery: AtomicBool::new(false),
        data_ptr_matches: AtomicBool::new(false),
        self_ptr: 0,
        handle,
        requested_thread: RefCell::new(Some(std::thread::current().id())),
    }));
    let state_ptr: *mut InterruptState = state;
    state.self_ptr = state_ptr as usize;

    let requested = state
        .handle
        .request_interrupt(interrupt_callback, state_ptr.cast());

    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let loop_source = "let s = 0; for (let i = 0; i < 2000000; i++) { s += i; } s";
    let run_value = eval(scope, loop_source).and_then(|v| v.integer_value(scope));

    let actual = Json::obj(vec![
        ("requested", Json::b(requested)),
        ("completed", Json::b(run_value.is_some())),
        ("loop_result", Json::i(run_value.unwrap_or(-1))),
        (
            "callback_count",
            Json::i(state.count.load(Ordering::SeqCst) as i64),
        ),
        (
            "delivered_on_requesting_thread",
            Json::b(state.requested_thread_matches.load(Ordering::SeqCst)),
        ),
        (
            "not_terminating_at_delivery",
            Json::b(!state.terminating_at_delivery.load(Ordering::SeqCst)),
        ),
        (
            "data_ptr_preserved",
            Json::b(state.data_ptr_matches.load(Ordering::SeqCst)),
        ),
    ]);
    let expected = Json::obj(vec![
        ("requested", Json::b(true)),
        ("completed", Json::b(true)),
        ("loop_result", Json::i(1_999_999_000_000)),
        ("callback_count", Json::i(1)),
        ("delivered_on_requesting_thread", Json::b(true)),
        ("not_terminating_at_delivery", Json::b(true)),
        ("data_ptr_preserved", Json::b(true)),
    ]);
    vec![expect_eq(
        "core-advanced/terminate/interrupt_callback",
        expected,
        actual,
    )]
}

/// `HeapStatistics` deterministic invariants for a fresh isolate plus the
/// exact `adjust_amount_of_external_allocated_memory` accounting. Sizes
/// themselves are machine-dependent and never pinned.
fn heap_statistics_invariants() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    {
        v8::scope!(let scope, &mut *isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let _probe = v8::Object::new(scope);
    }

    let stats = isolate.get_heap_statistics();
    let external_initial = stats.external_memory();
    let adjust_up_returns = isolate.adjust_amount_of_external_allocated_memory(1024);
    let external_after_up = isolate.get_heap_statistics().external_memory();
    let adjust_down_returns = isolate.adjust_amount_of_external_allocated_memory(-1024);
    let external_after_down = isolate.get_heap_statistics().external_memory();
    let heap_spaces = isolate.number_of_heap_spaces();

    let actual = Json::obj(vec![
        ("used_heap_positive", Json::b(stats.used_heap_size() > 0)),
        (
            "total_at_least_used",
            Json::b(stats.total_heap_size() >= stats.used_heap_size()),
        ),
        (
            "available_positive",
            Json::b(stats.total_available_size() > 0),
        ),
        ("heap_limit_positive", Json::b(stats.heap_size_limit() > 0)),
        (
            "native_contexts_at_least_one",
            Json::b(stats.number_of_native_contexts() >= 1),
        ),
        (
            "detached_contexts_zero",
            Json::b(stats.number_of_detached_contexts() == 0),
        ),
        ("does_zap_garbage", Json::b(stats.does_zap_garbage())),
        (
            "global_handles_total_at_least_used",
            Json::b(stats.total_global_handles_size() >= stats.used_global_handles_size()),
        ),
        (
            "total_allocated_positive",
            Json::b(stats.total_allocated_bytes() > 0),
        ),
        ("external_initial", Json::i(external_initial as i64)),
        ("adjust_up_returns_new_total", Json::i(adjust_up_returns)),
        ("external_after_up", Json::i(external_after_up as i64)),
        (
            "adjust_down_returns_new_total",
            Json::i(adjust_down_returns),
        ),
        ("external_after_down", Json::i(external_after_down as i64)),
        ("heap_spaces", Json::i(heap_spaces as i64)),
    ]);
    let expected = Json::obj(vec![
        ("used_heap_positive", Json::b(true)),
        ("total_at_least_used", Json::b(true)),
        ("available_positive", Json::b(true)),
        ("heap_limit_positive", Json::b(true)),
        ("native_contexts_at_least_one", Json::b(true)),
        ("detached_contexts_zero", Json::b(true)),
        ("does_zap_garbage", Json::b(false)),
        ("global_handles_total_at_least_used", Json::b(true)),
        ("total_allocated_positive", Json::b(true)),
        // HeapStatistics::external_memory reads 0 for a fresh isolate and
        // does not reflect the adjust calls in this build; the adjust
        // *return value* tracks the running total (V8 returns the new
        // amount, not the previous one).
        ("external_initial", Json::i(0)),
        ("adjust_up_returns_new_total", Json::i(1024)),
        ("external_after_up", Json::i(0)),
        ("adjust_down_returns_new_total", Json::i(0)),
        ("external_after_down", Json::i(0)),
        ("heap_spaces", Json::i(13)),
    ]);
    vec![expect_eq(
        "core-advanced/heap/statistics_invariants",
        expected,
        actual,
    )]
}

/// GC prologue/epilogue callbacks filtered to `kGCTypeMarkSweepCompact`
/// fire exactly once per `low_memory_notification` with the collect-all
/// flags, and stop firing after removal.
fn heap_gc_notifications() -> Vec<CheckOutcome> {
    let mut isolate = v8::Isolate::new(Default::default());
    {
        v8::scope!(let scope, &mut *isolate);
        let _context = v8::Context::new(scope, Default::default());
    }

    let state = Box::leak(Box::new(GcCallbackState {
        prologue_count: AtomicUsize::new(0),
        epilogue_count: AtomicUsize::new(0),
        prologue_type: AtomicI32::new(0),
        epilogue_type: AtomicI32::new(0),
        prologue_flags: AtomicI32::new(0),
        epilogue_flags: AtomicI32::new(0),
    }));
    let state_ptr: *mut GcCallbackState = state;
    let filter = v8::GCType::kGCTypeMarkSweepCompact;

    isolate.add_gc_prologue_callback(gc_prologue_callback, state_ptr.cast(), filter);
    isolate.add_gc_epilogue_callback(gc_epilogue_callback, state_ptr.cast(), filter);

    isolate.low_memory_notification();

    let prologue_after_first = state.prologue_count.load(Ordering::SeqCst);
    let epilogue_after_first = state.epilogue_count.load(Ordering::SeqCst);
    let prologue_gc_type = state.prologue_type.load(Ordering::SeqCst);
    let epilogue_gc_type = state.epilogue_type.load(Ordering::SeqCst);
    let prologue_flags = state.prologue_flags.load(Ordering::SeqCst);
    let epilogue_flags = state.epilogue_flags.load(Ordering::SeqCst);

    isolate.remove_gc_prologue_callback(gc_prologue_callback, state_ptr.cast());
    isolate.remove_gc_epilogue_callback(gc_epilogue_callback, state_ptr.cast());
    isolate.low_memory_notification();

    let prologue_after_removal = state.prologue_count.load(Ordering::SeqCst);
    let epilogue_after_removal = state.epilogue_count.load(Ordering::SeqCst);

    let actual = Json::obj(vec![
        (
            "prologue_after_first_gc",
            Json::i(prologue_after_first as i64),
        ),
        (
            "epilogue_after_first_gc",
            Json::i(epilogue_after_first as i64),
        ),
        ("prologue_gc_type", Json::i(i64::from(prologue_gc_type))),
        ("epilogue_gc_type", Json::i(i64::from(epilogue_gc_type))),
        ("prologue_flags", Json::i(i64::from(prologue_flags))),
        ("epilogue_flags", Json::i(i64::from(epilogue_flags))),
        (
            "prologue_after_removal",
            Json::i(prologue_after_removal as i64),
        ),
        (
            "epilogue_after_removal",
            Json::i(epilogue_after_removal as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        // One `low_memory_notification()` runs exactly two
        // MarkSweepCompact-filtered GC cycles in this build; both prologue
        // and epilogue fire per cycle with the collect-all flag (16).
        ("prologue_after_first_gc", Json::i(2)),
        ("epilogue_after_first_gc", Json::i(2)),
        ("prologue_gc_type", Json::i(4)),
        ("epilogue_gc_type", Json::i(4)),
        ("prologue_flags", Json::i(16)),
        ("epilogue_flags", Json::i(16)),
        ("prologue_after_removal", Json::i(2)),
        ("epilogue_after_removal", Json::i(2)),
    ]);
    vec![expect_eq(
        "core-advanced/heap/gc_notification_callbacks",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// Registry and entry point (order is the observable contract).
// ---------------------------------------------------------------------------

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    scope_nested_and_escaped,
    scope_escape_twice_panics,
    thread_shared_isolate_cross_thread_locks,
    thread_shared_terminate_while_locked,
    thread_locker_unlock_window,
    thread_into_shared_rejections,
    thread_handle_after_dispose,
    context_enter_exit_nesting,
    context_security_tokens,
    context_embedder_data_and_slots,
    slots_isolate_raw_data,
    slots_isolate_multiple_types,
    script_origin_roundtrip,
    script_origin_shifts_positions,
    script_unbound_rebind,
    script_compiler_options,
    script_code_cache_roundtrip,
    message_exception_details,
    message_current_stack_frames,
    message_uncaught_capture,
    terminate_same_thread_lifecycle,
    terminate_cancel_before_delivery,
    terminate_interrupt_callback,
    heap_statistics_invariants,
    heap_gc_notifications,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut outcomes = Vec::new();
    for check in CHECKS {
        outcomes.extend(check());
    }
    let total = outcomes.len();
    let mut passed = 0usize;
    let mut text = String::new();
    for outcome in &outcomes {
        if outcome.passed() {
            passed += 1;
        }
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    let failed = total - passed;
    text.push_str(&summary_line(total, passed, failed));
    text.push('\n');

    use std::io::Write as _;
    let stdout = std::io::stdout();
    let mut lock = stdout.lock();
    let _ = lock.write_all(text.as_bytes());
    let _ = lock.flush();

    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
