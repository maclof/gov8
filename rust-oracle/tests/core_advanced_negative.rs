//! Negative and fatal-misuse characterization for the core-advanced slice.
//! Complements `src/bin/conformance-core-advanced.rs` with the cases that
//! must NOT run inside the fixture binary.
//!
//! Contract characterized here:
//! - **`Locker` entry guards are Rust panics before any FFI state change**,
//!   so they are caught in-process: double-locking a `SharedIsolate` on one
//!   thread ("recursive locking is forbidden" — the guard would alias
//!   `&mut Isolate`) and locking a shared isolate while a *different*
//!   isolate is entered on the same thread. Both leave the shared isolate
//!   fully usable afterwards.
//! - **`v8::Weak` is not supported on shared isolates**: creating one under
//!   a `Locker` panics deterministically (the crate rejects weak handles on
//!   converted isolates; the assert fires before any V8 state changes).
//! - **A `ScriptOrigin` flagged `is_module` fed to a classic
//!   `Script::compile` is a V8 `ApiCheck` fatal** ("CompileModule must be
//!   used to compile modules", from `v8/src/api/api.cc`), and **consuming a
//!   corrupted code cache is a V8 deserializer fatal** ("unreachable
//!   code" — corruption is *not* a graceful `rejected: true` in this
//!   build). Both are characterized out-of-process: each probe runs in a
//!   child test process (spawned via `current_exe`, the same pattern as
//!   `tests/buffers_negative.rs` and `tests/callback_panic_boundary.rs`)
//!   and the parent asserts abnormal termination with V8's deterministic
//!   fatal text instead of a Rust panic. Modules are out of milestone
//!   scope, but the rejection boundary is classic-embedding behavior.
//!
//! Documented-but-unprobed misuse (unreachable, unsafe, or
//! nondeterministic — tracked as gaps for the parity matrix, never
//! exercised):
//! - `UnboundScript::bind_to_current_context` **cannot be called without an
//!   entered context through safe code**: the method demands a
//!   context-typed scope (`PinScope<HandleScope<Context>>`), which only a
//!   `ContextScope` produces. The entered-context precondition is enforced
//!   by the type system; a Go port must enforce it at runtime instead.
//! - `Isolate::set_data(slot)` / `StackTrace::get_frame(i)` with
//!   out-of-range indices are unchecked in release V8 (memory corruption /
//!   garbage reads, no deterministic failure) — a Go port must bounds-check
//!   where this crate does not;
//! - dropping a `Locker` out of order, or locking from a thread whose
//!   `unlock` closure left another isolate entered, corrupt guard state and
//!   are unreachable through safe patterns used here;
//! - `SealHandleScope` has no Rust binding in this crate at all.

use std::panic::AssertUnwindSafe;
use std::process::Command;

/// Runs `f` with a silenced panic hook and returns the panic message
/// ("" when it did not panic). Only safe around panics that fire before any
/// FFI state change — exactly the cases this file pins.
fn catch_panic_message(f: impl FnOnce()) -> String {
    let previous = std::panic::take_hook();
    std::panic::set_hook(Box::new(|_| {}));
    let result = std::panic::catch_unwind(AssertUnwindSafe(f));
    std::panic::set_hook(previous);
    match result {
        Ok(()) => String::new(),
        Err(payload) => payload
            .downcast_ref::<String>()
            .cloned()
            .or_else(|| payload.downcast_ref::<&str>().map(|s| (*s).to_owned()))
            .unwrap_or_default(),
    }
}

/// Runs one `#[ignore]`d probe test from this same test binary in a child
/// process and returns its status plus captured stderr.
fn run_probe(name: &str) -> (std::process::ExitStatus, String) {
    let exe = std::env::current_exe().expect("current test executable");
    let output = Command::new(exe)
        .args([
            "--exact",
            name,
            "--ignored",
            "--test-threads",
            "1",
            // Without this the harness buffers probe output and a hard
            // V8 abort drops the buffer before it is flushed.
            "--nocapture",
        ])
        .output()
        .expect("failed to spawn probe process");
    let stderr = String::from_utf8_lossy(&output.stderr).to_string();
    (output.status, stderr)
}

// ---------------------------------------------------------------------------
// In-process panic guards (safe to unwind: asserts fire before FFI changes).
// ---------------------------------------------------------------------------

#[test]
fn locker_double_lock_on_one_thread_panics() {
    oracle::ensure_v8();
    let owned = v8::Isolate::new(Default::default());
    let shared = unsafe { owned.try_into_shared() }.expect("fresh isolate converts");
    let mut locker = shared.lock();
    let message = catch_panic_message(|| {
        let _second = shared.lock();
    });
    assert!(
        message.contains("already locked by this thread"),
        "unexpected panic message: {message}"
    );
    // The guard fired before the second lock was taken; the locker still
    // owns the isolate and can run JS.
    let still_usable = {
        v8::scope!(let scope, &mut *locker);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let src = v8::String::new(scope, "5 * 5").unwrap();
        v8::Script::compile(scope, src, None)
            .unwrap()
            .run(scope)
            .and_then(|v| v.integer_value(scope))
    };
    assert_eq!(still_usable, Some(25));
    drop(locker);
    drop(shared);
}

#[test]
fn locker_lock_while_another_isolate_entered_panics() {
    oracle::ensure_v8();
    // The shared isolate is converted first (conversion requires no other
    // isolate entered), then a plain isolate is entered on this thread.
    let owned = v8::Isolate::new(Default::default());
    let shared = unsafe { owned.try_into_shared() }.expect("fresh isolate converts");
    let entered = v8::Isolate::new(Default::default());
    let message = catch_panic_message(|| {
        let _locker = shared.lock();
    });
    assert!(
        message.contains("while another isolate is entered"),
        "unexpected panic message: {message}"
    );
    // Drop order is reverse creation: the entered isolate first, then the
    // shared isolate (which was never locked).
    drop(entered);
    drop(shared);
}

#[test]
fn weak_creation_on_shared_isolate_panics() {
    oracle::ensure_v8();
    let owned = v8::Isolate::new(Default::default());
    let shared = unsafe { owned.try_into_shared() }.expect("fresh isolate converts");
    {
        let mut locker = shared.lock();
        v8::scope!(let scope, &mut *locker);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let obj = v8::Object::new(scope);
        let message = catch_panic_message(|| {
            let _weak = v8::Weak::<v8::Object>::new(&mut *scope, obj);
        });
        assert!(
            message.contains("not supported on shared isolates"),
            "unexpected panic message: {message}"
        );
    }
    // The assert fired before any weak was registered; the shared isolate
    // remains fully usable.
    let still_usable = {
        let mut locker = shared.lock();
        v8::scope!(let scope, &mut *locker);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let src = v8::String::new(scope, "6 * 7").unwrap();
        v8::Script::compile(scope, src, None)
            .unwrap()
            .run(scope)
            .and_then(|v| v.integer_value(scope))
    };
    assert_eq!(still_usable, Some(42));
    drop(shared);
}

// ---------------------------------------------------------------------------
// Child-process probes for process-fatal misuse (never run in the suite:
// #[ignore], spawned via current_exe above).
// ---------------------------------------------------------------------------

#[test]
fn module_origin_classic_compile_is_v8_fatal() {
    let (status, stderr) = run_probe("probe_module_origin_classic_compile");
    assert!(
        !status.success(),
        "module-origin classic compile unexpectedly survived; status={status}"
    );
    assert!(
        stderr.contains("Check failed") || stderr.contains("Fatal"),
        "expected a V8 ApiCheck fatal on stderr; stderr:\n{stderr}"
    );
    assert!(
        stderr.contains("CompileModule must be used to compile modules"),
        "expected the pinned ApiCheck message on stderr; stderr:\n{stderr}"
    );
    assert!(
        !stderr.contains("panicked at"),
        "module-origin compile surfaced as a Rust panic; stderr:\n{stderr}"
    );
}

#[test]
#[ignore]
fn probe_module_origin_classic_compile() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let name: v8::Local<v8::Value> = v8::String::new(scope, "m.js").unwrap().into();
    // is_module = true with a classic compile: V8 ApiCheck-fails inside the
    // call ("CompileModule must be used to compile modules").
    let origin = v8::ScriptOrigin::new(scope, name, 0, 0, false, 0, None, false, false, true, None);
    let src = v8::String::new(scope, "export const x = 1;").unwrap();
    let _ = v8::Script::compile(scope, src, Some(&origin));
    println!("probe:survived");
}

/// Producing a code cache, flipping one mid-payload byte, and consuming it
/// with `ConsumeCodeCache` hits V8's deserializer "unreachable code" fatal:
/// corruption is NOT a graceful `rejected: true` in this build. The flip
/// position is fixed (mid-cache) so the outcome is deterministic.
#[test]
fn code_cache_corruption_is_v8_fatal() {
    let (status, stderr) = run_probe("probe_code_cache_corruption");
    assert!(
        !status.success(),
        "corrupted code cache unexpectedly survived; status={status}"
    );
    assert!(
        stderr.contains("Fatal") || stderr.contains("Check failed"),
        "expected a V8 fatal on stderr; stderr:\n{stderr}"
    );
    assert!(
        !stderr.contains("panicked at"),
        "corrupted cache surfaced as a Rust panic; stderr:\n{stderr}"
    );
}

#[test]
#[ignore]
fn probe_code_cache_corruption() {
    oracle::ensure_v8();
    let mut cache_bytes: Vec<u8> = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let name: v8::Local<v8::Value> = v8::String::new(scope, "cached.js").unwrap().into();
        let origin =
            v8::ScriptOrigin::new(scope, name, 0, 0, false, 0, None, false, false, false, None);
        let source_string = v8::String::new(
            scope,
            "(function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); })(12)",
        )
        .unwrap();
        let mut source = v8::script_compiler::Source::new(source_string, Some(&origin));
        let unbound = v8::script_compiler::compile_unbound_script(
            scope,
            &mut source,
            v8::script_compiler::CompileOptions::NoCompileOptions,
            v8::script_compiler::NoCacheReason::NoReason,
        )
        .unwrap();
        unbound
            .create_code_cache()
            .unwrap()
            .iter()
            .copied()
            .collect()
    };
    let flip = cache_bytes.len() / 2;
    cache_bytes[flip] ^= 0xFF;

    // A fresh isolate consumes the corrupted cache: V8 fatal inside the
    // deserializer.
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let name: v8::Local<v8::Value> = v8::String::new(scope, "cached.js").unwrap().into();
    let origin =
        v8::ScriptOrigin::new(scope, name, 0, 0, false, 0, None, false, false, false, None);
    let source_string = v8::String::new(
        scope,
        "(function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); })(12)",
    )
    .unwrap();
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
    let _ = script.and_then(|s| s.run(scope));
    println!("probe:survived");
}
