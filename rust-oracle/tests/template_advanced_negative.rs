//! Negative and boundary characterization for the advanced template/object
//! slice. Complements `src/bin/conformance-template-advanced.rs` with the
//! cases that must NOT run inside the fixture binary.
//!
//! Contract characterized here:
//! - Out-of-range `EmbedderDataTypeTag` values for
//!   `Object::set_aligned_pointer_in_internal_field` are process-fatal V8
//!   failures: the valid tag range is `0..V8_EMBEDDER_DATA_TAG_COUNT`
//!   (`= 15`, `v8/include/v8-internal.h`), and the write goes straight to
//!   V8 with no crate-level guard. Characterized out-of-process: the probe
//!   runs in a child test process (spawned via `current_exe`, the same
//!   pattern as `tests/callback_panic_boundary.rs` and
//!   `tests/buffers_negative.rs`) and the parent asserts abnormal
//!   termination through V8's fatal path.
//! - Indexed enumerators must return uint32-convertible element values:
//!   String elements abort with V8's
//!   `Check failed: Object::ToUint32(*element, &number)` inside
//!   `KeyAccumulator::FilterForEnumerableProperties` when the keys are
//!   consumed (e.g. by `Object.keys`). The filter only runs when the
//!   interceptor carries query/getter state alongside the enumerator: an
//!   enumerator-only handler survives, so the probe pins the full-family
//!   configuration. Named enumerators are NOT stricter: non-Name elements
//!   are silently ToName-converted (Numbers accepted; pinned benignly in
//!   the fixture via the `"1,a,c,b"` enumerator output), so there is no
//!   named-side fatal to characterize.
//! - Invalid handler configurations are rejected by crate-level `assert!`s
//!   (plain Rust panics, process stays alive): an accessor property needs a
//!   getter or a setter, and a property handler configuration with neither
//!   callbacks nor flags is rejected for both the named and the indexed
//!   installer.

use std::process::Command;

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

/// Asserts the child terminated abnormally through V8's fatal path (not a
/// caught Rust panic, which would look entirely different).
fn assert_v8_fatal(name: &str) {
    let (status, stderr) = run_probe(name);
    assert!(
        !status.success(),
        "{name}: probe unexpectedly survived; status={status}"
    );
    assert!(
        stderr.contains("Check failed") || stderr.contains("Fatal"),
        "{name}: expected a V8 fatal on stderr; stderr:\n{stderr}"
    );
    assert!(
        !stderr.contains("panicked at"),
        "{name}: probe failed via Rust panic instead of V8 fatal; stderr:\n{stderr}"
    );
}

#[test]
fn aligned_pointer_tag_out_of_range_is_v8_fatal() {
    assert_v8_fatal("probe_aligned_pointer_tag_out_of_range");
}

#[test]
fn indexed_enumerator_string_elements_are_v8_fatal() {
    let (status, stderr) = run_probe("probe_indexed_enumerator_string_elements");
    assert!(
        !status.success(),
        "indexed enumerator with String elements unexpectedly survived"
    );
    // The exact CHECK text of the pinned build.
    assert!(
        stderr.contains("Check failed: Object::ToUint32(*element, &number)"),
        "expected the ToUint32 CHECK failure; stderr:\n{stderr}"
    );
    assert!(
        !stderr.contains("panicked at"),
        "probe failed via Rust panic instead of V8 fatal; stderr:\n{stderr}"
    );
}

// --- crate-level assertion boundaries (plain Rust panics) -----------------

#[test]
#[should_panic(expected = "assertion failed: getter.is_some() || setter.is_some()")]
fn set_accessor_property_both_none_panics() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let ot = v8::ObjectTemplate::new(scope);
    let key = v8::String::new(scope, "acc").unwrap();
    ot.set_accessor_property(key.into(), None, None, v8::PropertyAttribute::NONE);
}

#[test]
#[should_panic(expected = "assertion failed: configuration.is_some()")]
fn empty_named_handler_configuration_panics() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let ot = v8::ObjectTemplate::new(scope);
    ot.set_named_property_handler(v8::NamedPropertyHandlerConfiguration::new());
}

#[test]
#[should_panic(expected = "assertion failed: configuration.is_some()")]
fn empty_indexed_handler_configuration_panics() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let ot = v8::ObjectTemplate::new(scope);
    ot.set_indexed_property_handler(v8::IndexedPropertyHandlerConfiguration::new());
}

// --- child-process probes (never run in the normal suite: #[ignore]) ------

#[test]
#[ignore]
fn probe_aligned_pointer_tag_out_of_range() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ot = v8::ObjectTemplate::new(scope);
    ot.set_internal_field_count(1);
    let obj = ot.new_instance(scope).unwrap();
    // Tag 99 is outside 0..V8_EMBEDDER_DATA_TAG_COUNT (= 15): V8
    // CHECK-fails inside the external-pointer write.
    obj.set_aligned_pointer_in_internal_field(0, std::ptr::null(), 99);
    println!("probe:survived");
}

#[test]
#[ignore]
fn probe_indexed_enumerator_string_elements() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    fn bad_indexed_getter(
        _scope: &mut v8::PinScope<'_, '_>,
        _index: u32,
        _args: v8::PropertyCallbackArguments<'_>,
        _rv: v8::ReturnValue<'_, v8::Value>,
    ) -> v8::Intercepted {
        v8::Intercepted::kNo
    }

    fn bad_indexed_setter(
        _scope: &mut v8::PinScope<'_, '_>,
        _index: u32,
        _value: v8::Local<'_, v8::Value>,
        _args: v8::PropertyCallbackArguments<'_>,
        _rv: v8::ReturnValue<'_, v8::Boolean>,
    ) -> v8::Intercepted {
        v8::Intercepted::kNo
    }

    fn bad_indexed_query(
        _scope: &mut v8::PinScope<'_, '_>,
        _index: u32,
        _args: v8::PropertyCallbackArguments<'_>,
        _rv: v8::ReturnValue<'_, v8::Integer>,
    ) -> v8::Intercepted {
        v8::Intercepted::kNo
    }

    fn bad_indexed_deleter(
        _scope: &mut v8::PinScope<'_, '_>,
        _index: u32,
        _args: v8::PropertyCallbackArguments<'_>,
        _rv: v8::ReturnValue<'_, v8::Boolean>,
    ) -> v8::Intercepted {
        v8::Intercepted::kNo
    }

    fn bad_enumerator(
        scope: &mut v8::PinScope<'_, '_>,
        _args: v8::PropertyCallbackArguments<'_>,
        mut rv: v8::ReturnValue<'_, v8::Array>,
    ) {
        // Strings are not uint32-convertible through Object::ToUint32.
        let elements: Vec<v8::Local<v8::Value>> = ["9", "4"]
            .iter()
            .map(|n| v8::String::new(scope, n).unwrap().into())
            .collect();
        let array = v8::Array::new_with_elements(scope, &elements);
        rv.set(array);
    }

    let ot = v8::ObjectTemplate::new(scope);
    // The enumerable-property filter that CHECK-fails on non-uint32
    // element keys only runs when the interceptor carries query/getter
    // state alongside the enumerator (the full-family configuration of
    // tpladv/indexed_interceptor_full_family).
    ot.set_indexed_property_handler(
        v8::IndexedPropertyHandlerConfiguration::new()
            .getter(bad_indexed_getter)
            .setter(bad_indexed_setter)
            .query(bad_indexed_query)
            .deleter(bad_indexed_deleter)
            .enumerator(bad_enumerator),
    );
    let obj = ot.new_instance(scope).unwrap();
    scope
        .get_current_context()
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "io").unwrap().into(),
            obj.into(),
        )
        .unwrap();
    // Consuming the keys drives the fatal ToUint32 CHECK.
    let src = v8::String::new(scope, "Object.keys(io)").unwrap();
    let _ = v8::Script::compile(scope, src, None).and_then(|s| s.run(scope));
    println!("probe:survived");
}
