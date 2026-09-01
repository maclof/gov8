//! Thread-constraint characterization: terminating JS execution from a
//! second thread through the thread-safe `IsolateHandle`.
//!
//! The `Isolate` itself is not `Send`/`Sync` (the compiler rejects moving it
//! across threads; no runtime test needed or possible), but the crate
//! exposes `Isolate::thread_safe_handle()` exactly for cross-thread control.
//! This test pins:
//! - `terminate_execution()` from another thread interrupts a tight JS loop,
//! - the interrupted `Script::run` returns `None` and leaves the exception
//!   in the active TryCatch,
//! - `cancel_terminate_execution()` restores the isolate to a fully usable
//!   state afterwards.
//!
//! Own binary/process (like the other lifecycle tests) because it manages
//! the platform lifecycle itself.

use std::time::Duration;

#[test]
fn terminate_execution_from_another_thread() {
    let platform = v8::new_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();

    let isolate = &mut v8::Isolate::new(Default::default());
    let handle = isolate.thread_safe_handle();
    // The handle is Clone: one copy moves to the terminating thread, one
    // stays with the host for the cancellation afterwards.
    let terminator_handle = handle.clone();

    // Request termination from a foreign thread. The flag persists until it
    // is delivered (or cancelled), so there is no race: the running loop
    // hits its next interrupt check and stops deterministically.
    let terminator = std::thread::spawn(move || {
        // Give the main thread a moment to enter the script; the outcome is
        // identical either way, the wait just bounds total test time.
        std::thread::sleep(Duration::from_millis(100));
        terminator_handle.terminate_execution()
    });
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let (requested, ran_ok, has_caught, can_continue) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let source = v8::String::new(tc, "while (true) { }").unwrap();
        let script = v8::Script::compile(tc, source, None).unwrap();
        let ran_ok = script.run(tc).is_some();
        (
            terminator.join().expect("terminator thread"),
            ran_ok,
            tc.has_caught(),
            tc.can_continue(),
        )
    };

    // The loop never completed on its own; termination interrupted it.
    assert!(requested, "terminate_execution must be accepted");
    assert!(!ran_ok, "terminated script must return None");
    assert!(has_caught, "termination must leave the TryCatch set");
    assert!(
        !can_continue,
        "termination is not recoverable inside the same TryCatch"
    );

    // Cancel the termination and verify the isolate is fully reusable.
    assert!(handle.cancel_terminate_execution());
    let next = v8::String::new(scope, "40 + 2").unwrap();
    let next_script = v8::Script::compile(scope, next, None).unwrap();
    let value = next_script
        .run(scope)
        .expect("isolate reusable after cancel");
    assert_eq!(
        value.integer_value(scope),
        Some(42),
        "wrong result after cancellation"
    );
}
