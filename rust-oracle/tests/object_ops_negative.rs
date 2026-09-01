//! Negative and fatal-misuse characterization for the object-operations and
//! value-conversion slice. Complements `src/bin/conformance-object-ops.rs`
//! with the cases that must NOT run inside the fixture binary.
//!
//! Contract characterized here:
//! - **`Local` downcasts are the only safe type guards**: converting a
//!   primitive-carrying `Local<Value>` to `Local<Object>` via the safe API
//!   (`try_from` / `try_cast`) fails without touching V8 state, while the
//!   same conversion for a genuine object (e.g. a function) succeeds and
//!   the isolate stays fully usable afterwards.
//! - **The `Object` methods carry no Rust-side type assertions**: the
//!   bindings pass the raw handle straight over the FFI. Feeding them a
//!   confounded local (a `Local<Object>` that actually wraps a Number) is
//!   undefined behavior by construction; in this pinned build both probed
//!   calls (`get_identity_hash` and `delete`) deterministically terminate
//!   the process with `STATUS_ACCESS_VIOLATION` (0xC0000005) — NOT a
//!   catchable Rust panic, NOT a graceful `None`, and NOT a clean V8
//!   ApiCheck fatal with explanatory text. Both are characterized
//!   out-of-process: each probe runs in a child test process (spawned via
//!   `current_exe`, the same pattern as `tests/core_advanced_negative.rs`)
//!   and the parent asserts the access-violation exit code and the absence
//!   of any Rust panic.
//!
//! Consequence for the Go port (parity-matrix finding): the Rust crate's
//! `Object`/`Value` operation family relies on the type system for receiver
//! correctness and performs no runtime checks; a Go FFI must add its own
//! receiver-type verification for the *whole* family, because the failure
//! mode of a wrong receiver here is a process crash, not an error value.
//!
//! Documented-but-unprobed misuse (unreachable, unsafe, or nondeterministic
//! — tracked as gaps for the parity matrix, never exercised):
//! - Every other `Object`/`Value` method on a confounded local (get, set,
//!   has, call_as_*, creation context, ...) is equally unchecked; only the
//!   two probes above are pinned, and only on this platform/build where the
//!   crash manifests as a deterministic access violation.
//! - `to_detail_string`/`to_object` on a *revoked Proxy* are graceful
//!   (JS-observable exceptions already covered by the TryCatch discipline
//!   in the fixture), so they are not "negative".
//! - `Value::get_hash` of strings/bigints/oddballs is seeded per isolate:
//!   the fixture pins only within-isolate stability; cross-process or
//!   cross-isolate hash equality must never be asserted.

use std::process::Command;

/// The raw Windows exit code of `STATUS_ACCESS_VIOLATION` (0xC0000005) as
/// reported by `ExitStatus::code()` (i32 interpretation).
const STATUS_ACCESS_VIOLATION: i32 = -1073741819;

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
            // crash drops the buffer before it is flushed.
            "--nocapture",
        ])
        .output()
        .expect("failed to spawn probe process");
    let stderr = String::from_utf8_lossy(&output.stderr).to_string();
    (output.status, stderr)
}

// ---------------------------------------------------------------------------
// In-process safe-API guards (no V8 state is touched on rejection).
// ---------------------------------------------------------------------------

#[test]
fn local_downcast_guards_reject_primitives_and_accept_objects() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let number: v8::Local<v8::Value> = v8::Number::new(scope, 5.0).into();
    let string: v8::Local<v8::Value> = v8::String::new(scope, "s").unwrap().into();
    let function: v8::Local<v8::Value> = v8::Function::builder(
        |_scope: &mut v8::PinScope<'_, '_>,
         _args: v8::FunctionCallbackArguments<'_>,
         _rv: v8::ReturnValue<'_, v8::Value>| {},
    )
    .build(scope)
    .expect("native function")
    .into();
    let plain: v8::Local<v8::Value> = v8::Object::new(scope).into();

    // Safe downcasts reject primitives with no side effects...
    assert!(
        v8::Local::<v8::Object>::try_from(number).is_err(),
        "Number must not downcast to Object"
    );
    assert!(
        number.try_cast::<v8::Object>().is_err(),
        "Number must not try_cast to Object"
    );
    assert!(
        string.try_cast::<v8::Object>().is_err(),
        "String must not try_cast to Object"
    );
    // ...and accept every genuine object, including functions (functions
    // are objects) and API-created plain objects.
    assert!(
        function.try_cast::<v8::Object>().is_ok(),
        "Function must downcast to Object (functions are objects)"
    );
    assert!(plain.try_cast::<v8::Object>().is_ok());

    // A rejected downcast leaves the isolate fully usable.
    let still_usable = {
        let src = v8::String::new(scope, "6 * 7").unwrap();
        v8::Script::compile(scope, src, None)
            .unwrap()
            .run(scope)
            .and_then(|v| v.integer_value(scope))
    };
    assert_eq!(still_usable, Some(42));
}

// ---------------------------------------------------------------------------
// Child-process probes for process-fatal misuse (never run in the suite:
// #[ignore], spawned via current_exe above).
// ---------------------------------------------------------------------------

/// A Number handle confounded into `Local<Object>` and passed to
/// `Object::get_identity_hash` crashes the process with a deterministic
/// access violation.
#[test]
fn confounded_object_identity_hash_is_access_violation() {
    let (status, stderr) = run_probe("probe_confounded_object_identity_hash");
    assert!(
        !status.success(),
        "confounded get_identity_hash unexpectedly survived; status={status}"
    );
    assert_eq!(
        status.code(),
        Some(STATUS_ACCESS_VIOLATION),
        "expected STATUS_ACCESS_VIOLATION; status={status}"
    );
    assert!(
        !stderr.contains("panicked at"),
        "confounded local surfaced as a Rust panic; stderr:\n{stderr}"
    );
}

#[test]
#[ignore]
fn probe_confounded_object_identity_hash() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let number: v8::Local<v8::Value> = v8::Number::new(scope, 5.0).into();
    // SAFETY: deliberate type confound for the negative probe; the call is
    // expected to crash with STATUS_ACCESS_VIOLATION (no Rust-side check).
    let confounded: v8::Local<v8::Object> = unsafe { std::mem::transmute(number) };
    let hash = confounded.get_identity_hash();
    println!("probe:hash={}", hash.get());
    println!("probe:survived");
}

/// The same confound through `Object::delete` also crashes the process with
/// a deterministic access violation.
#[test]
fn confounded_object_delete_is_access_violation() {
    let (status, stderr) = run_probe("probe_confounded_object_delete");
    assert!(
        !status.success(),
        "confounded delete unexpectedly survived; status={status}"
    );
    assert_eq!(
        status.code(),
        Some(STATUS_ACCESS_VIOLATION),
        "expected STATUS_ACCESS_VIOLATION; status={status}"
    );
    assert!(
        !stderr.contains("panicked at"),
        "confounded delete surfaced as a Rust panic; stderr:\n{stderr}"
    );
}

#[test]
#[ignore]
fn probe_confounded_object_delete() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let number: v8::Local<v8::Value> = v8::Number::new(scope, 5.0).into();
    // SAFETY: deliberate type confound for the negative probe.
    let confounded: v8::Local<v8::Object> = unsafe { std::mem::transmute(number) };
    let key = v8::String::new(scope, "k").unwrap();
    let deleted = confounded.delete(scope, key.into());
    println!("probe:deleted={deleted:?}");
    println!("probe:survived");
}
