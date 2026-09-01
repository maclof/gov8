//! Negative lifecycle characterization: double `V8::initialize()`.
//!
//! Each file in `tests/` is its own binary/process because the crate's global
//! V8 state machine (and its mutex) is process-wide and poisoned by panics.

#[test]
#[should_panic(expected = "Invalid global state")]
fn double_initialize_panics() {
    let platform = v8::new_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();
    // Unlike raw V8 C++, where V8::Initialize() is idempotent, the crate's
    // global state machine panics on the second call.
    v8::V8::initialize();
}
