//! Negative lifecycle characterization: dispose ordering and one-shot
//! dispose semantics. Own binary/process; see v8_lifecycle_negative.rs.

#[test]
#[should_panic(expected = "Invalid global state")]
fn double_dispose_panics() {
    let platform = v8::new_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();
    {
        let isolate = v8::Isolate::new(Default::default());
        drop(isolate);
    }
    // SAFETY: all isolates have been dropped above.
    assert!(unsafe { v8::V8::dispose() });
    // dispose_platform() is required after dispose(); a second dispose()
    // violates the state machine and must panic.
    // SAFETY: V8 is already disposed once.
    let _disposable_again = unsafe { v8::V8::dispose() };
}
