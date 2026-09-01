//! Subprocess-only probe for creating an ArrayBuffer without an entered
//! Context in v8 152.2.0.

fn main() {
    oracle::ensure_v8();
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    // No ContextScope is entered. On the pinned Windows build the safe Rust
    // call deterministically terminates with an access violation and emits no
    // Rust panic or V8 fatal diagnostic.
    let _ = v8::ArrayBuffer::new(scope, 17);
}
