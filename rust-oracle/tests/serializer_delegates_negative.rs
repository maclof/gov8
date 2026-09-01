//! Negative and boundary characterization for the serializer/deserializer
//! delegate slice. Complements `src/bin/conformance-serializer-delegates.rs`
//! with the cases that must NOT run inside the fixture binary.
//!
//! Contract characterized here:
//! - A Rust panic inside ANY delegate hook unwinds through the pinned
//!   crate's `extern "C"` delegate trampolines
//!   (`v8__ValueSerializer__Delegate__*` / `v8__ValueDeserializer__Delegate__*`
//!   in `value_serializer.rs` / `value_deserializer.rs`). As with native
//!   callbacks (see `tests/callback_panic_boundary.rs`), a panic that would
//!   cross `extern "C"` is a non-unwinding panic: the default hook prints the
//!   original message, a second "panic in a function that cannot unwind"
//!   panic is raised, and the process aborts via fail-fast (0xC0000409 on
//!   Windows MSVC). Each probe therefore runs in its own child process
//!   (`current_exe` + `--ignored`, the pattern of
//!   `tests/buffers_negative.rs`) and the parent asserts the abort.
//! - `probe_has_custom_host_object_cache` documents (out of the fixture's
//!   way) that the pinned build consults `has_custom_host_object` exactly
//!   once per `ValueSerializer` construction - the same delegate counting
//!   type fired for every serializer in one process, with both `true` and
//!   `false` answers (the fixture pins `1` for overriding delegates; the
//!   trait-default body is not instrumentable and therefore pins as `0`).

use std::cell::RefCell;
use std::process::Command;
use std::rc::Rc;

use v8::ValueSerializerHelper as _;

/// Runs one `#[ignore]`d probe test from this same test binary in a child
/// process and returns its status plus captured stderr.
fn run_probe(name: &str) -> (std::process::ExitStatus, String, String) {
    let exe = std::env::current_exe().expect("current test executable");
    let output = Command::new(exe)
        .args([
            "--exact",
            name,
            "--ignored",
            "--test-threads",
            "1",
            // Without this the harness buffers probe output and a hard
            // fail-fast abort drops the buffer before it is flushed.
            "--nocapture",
        ])
        .output()
        .expect("failed to spawn probe process");
    let stderr = String::from_utf8_lossy(&output.stderr).to_string();
    let stdout = String::from_utf8_lossy(&output.stdout).to_string();
    (output.status, stderr, stdout)
}

/// Asserts the child died through the extern-"C" panic boundary (not a
/// clean exit and not an ordinary unwinding Rust panic), that the probe's
/// own marker was printed, and that the marker AFTER the hook never ran.
fn assert_delegate_panic_aborts(probe: &str, marker: &str) {
    let (status, stderr, stdout) = run_probe(probe);
    let all = format!("{stdout}{stderr}");
    assert!(
        !status.success(),
        "{probe}: probe unexpectedly survived; status={status}\nstdout:\n{stdout}\nstderr:\n{stderr}"
    );
    assert!(
        all.contains(marker),
        "{probe}: expected the hook's own marker on the output; stdout:\n{stdout}\nstderr:\n{stderr}"
    );
    assert!(
        stderr.contains(marker) && stderr.contains("panic in a function that cannot unwind"),
        "{probe}: expected the extern-C unwinding boundary on stderr; stderr:\n{stderr}"
    );
    assert!(
        !all.contains("marker:after-hook"),
        "{probe}: execution must not return past the panicking hook; stdout:\n{stdout}\nstderr:\n{stderr}"
    );
    assert_eq!(
        status.code(),
        Some(-1073740791),
        "{probe}: expected the 0xC0000409 fail-fast abort code"
    );
}

#[test]
fn panic_in_write_host_object_aborts() {
    assert_delegate_panic_aborts("probe_panic_write_host_object", "marker:write-host-object");
}

#[test]
fn panic_in_read_host_object_aborts() {
    assert_delegate_panic_aborts("probe_panic_read_host_object", "marker:read-host-object");
}

#[test]
fn panic_in_is_host_object_aborts() {
    assert_delegate_panic_aborts("probe_panic_is_host_object", "marker:is-host-object");
}

#[test]
fn panic_in_has_custom_host_object_aborts() {
    assert_delegate_panic_aborts(
        "probe_panic_has_custom_host_object",
        "marker:has-custom-host-object",
    );
}

#[test]
fn panic_in_get_shared_array_buffer_id_aborts() {
    assert_delegate_panic_aborts(
        "probe_panic_get_shared_array_buffer_id",
        "marker:get-sab-id",
    );
}

#[test]
fn panic_in_get_shared_array_buffer_from_id_aborts() {
    assert_delegate_panic_aborts(
        "probe_panic_get_shared_array_buffer_from_id",
        "marker:get-sab-from-id",
    );
}

#[test]
fn panic_in_get_wasm_module_from_id_aborts() {
    assert_delegate_panic_aborts(
        "probe_panic_get_wasm_module_from_id",
        "marker:get-wasm-from-id",
    );
}

#[test]
fn panic_in_throw_data_clone_error_aborts() {
    assert_delegate_panic_aborts(
        "probe_panic_throw_data_clone_error",
        "marker:throw-data-clone-error",
    );
}

// --- child-process probes (never run in the normal suite: #[ignore]) ------

fn probe_isolate() -> &'static mut v8::OwnedIsolate {
    Box::leak(Box::new(v8::Isolate::new(Default::default())))
}

fn rethrow_noop(scope: &mut v8::PinScope<'_, '_>, message: v8::Local<'_, v8::String>) {
    let text = message.to_rust_string_lossy(scope);
    if let Some(str_handle) = v8::String::new(scope, &text) {
        let exc = v8::Exception::error(scope, str_handle);
        scope.throw_exception(exc);
    }
}

#[test]
#[ignore]
fn probe_panic_write_host_object() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    struct Boom;
    impl v8::ValueSerializerImpl for Boom {
        fn throw_data_clone_error<'s>(
            &self,
            scope: &mut v8::PinScope<'s, '_>,
            message: v8::Local<'s, v8::String>,
        ) {
            rethrow_noop(scope, message);
        }

        fn write_host_object<'s>(
            &self,
            _scope: &mut v8::PinScope<'s, '_>,
            _object: v8::Local<'s, v8::Object>,
            _value_serializer: &dyn v8::ValueSerializerHelper,
        ) -> Option<bool> {
            eprintln!("marker:write-host-object");
            panic!("serdel-panic-write-host-object");
        }
    }

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let ab = v8::ArrayBuffer::new(tc, 4);
    let ta = v8::Uint8Array::new(tc, ab, 0, 4).unwrap();
    let value: v8::Local<v8::Value> = ta.into();
    let serializer = v8::ValueSerializer::new(tc, Box::new(Boom));
    serializer.set_treat_array_buffer_views_as_host_objects(true);
    let ctx = tc.get_current_context();
    let _ = serializer.write_value(ctx, value);
    eprintln!("marker:after-hook");
}

#[test]
#[ignore]
fn probe_panic_read_host_object() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    struct Boom;
    impl v8::ValueDeserializerImpl for Boom {
        fn read_host_object<'s>(
            &self,
            _scope: &mut v8::PinScope<'s, '_>,
            _deserializer: &dyn v8::ValueDeserializerHelper,
        ) -> Option<v8::Local<'s, v8::Object>> {
            eprintln!("marker:read-host-object");
            panic!("serdel-panic-read-host-object");
        }
    }

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = [0x5cu8];
    let deserializer = v8::ValueDeserializer::new(tc, Box::new(Boom), &bytes);
    let ctx = tc.get_current_context();
    let _ = deserializer.read_value(ctx);
    eprintln!("marker:after-hook");
}

#[test]
#[ignore]
fn probe_panic_is_host_object() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    struct Boom;
    impl v8::ValueSerializerImpl for Boom {
        fn throw_data_clone_error<'s>(
            &self,
            scope: &mut v8::PinScope<'s, '_>,
            message: v8::Local<'s, v8::String>,
        ) {
            rethrow_noop(scope, message);
        }

        fn has_custom_host_object(&self, _isolate: &v8::Isolate) -> bool {
            true
        }

        fn is_host_object<'s>(
            &self,
            _scope: &mut v8::PinScope<'s, '_>,
            _object: v8::Local<'s, v8::Object>,
        ) -> Option<bool> {
            eprintln!("marker:is-host-object");
            panic!("serdel-panic-is-host-object");
        }
    }

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let obj = v8::Object::new(tc);
    let value: v8::Local<v8::Value> = obj.into();
    let serializer = v8::ValueSerializer::new(tc, Box::new(Boom));
    let ctx = tc.get_current_context();
    let _ = serializer.write_value(ctx, value);
    eprintln!("marker:after-hook");
}

#[test]
#[ignore]
fn probe_panic_has_custom_host_object() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    struct Boom;
    impl v8::ValueSerializerImpl for Boom {
        fn throw_data_clone_error<'s>(
            &self,
            scope: &mut v8::PinScope<'s, '_>,
            message: v8::Local<'s, v8::String>,
        ) {
            rethrow_noop(scope, message);
        }

        fn has_custom_host_object(&self, _isolate: &v8::Isolate) -> bool {
            eprintln!("marker:has-custom-host-object");
            panic!("serdel-panic-has-custom-host-object");
        }
    }

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let obj = v8::Object::new(tc);
    let value: v8::Local<v8::Value> = obj.into();
    // The hook fires during ValueSerializer construction (or first write).
    let serializer = v8::ValueSerializer::new(tc, Box::new(Boom));
    let ctx = tc.get_current_context();
    let _ = serializer.write_value(ctx, value);
    eprintln!("marker:after-hook");
}

#[test]
#[ignore]
fn probe_panic_get_shared_array_buffer_id() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    struct Boom;
    impl v8::ValueSerializerImpl for Boom {
        fn throw_data_clone_error<'s>(
            &self,
            scope: &mut v8::PinScope<'s, '_>,
            message: v8::Local<'s, v8::String>,
        ) {
            rethrow_noop(scope, message);
        }

        fn get_shared_array_buffer_id<'s>(
            &self,
            _scope: &mut v8::PinScope<'s, '_>,
            _shared_array_buffer: v8::Local<'s, v8::SharedArrayBuffer>,
        ) -> Option<u32> {
            eprintln!("marker:get-sab-id");
            panic!("serdel-panic-get-sab-id");
        }
    }

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let sab = v8::SharedArrayBuffer::with_backing_store(
        tc,
        &v8::SharedRef::from(v8::SharedArrayBuffer::new_backing_store(tc, 8)),
    );
    let value: v8::Local<v8::Value> = sab.into();
    let serializer = v8::ValueSerializer::new(tc, Box::new(Boom));
    let ctx = tc.get_current_context();
    let _ = serializer.write_value(ctx, value);
    eprintln!("marker:after-hook");
}

#[test]
#[ignore]
fn probe_panic_get_shared_array_buffer_from_id() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    struct Boom;
    impl v8::ValueDeserializerImpl for Boom {
        fn get_shared_array_buffer_from_id<'s>(
            &self,
            _scope: &mut v8::PinScope<'s, '_>,
            _transfer_id: u32,
        ) -> Option<v8::Local<'s, v8::SharedArrayBuffer>> {
            eprintln!("marker:get-sab-from-id");
            panic!("serdel-panic-get-sab-from-id");
        }
    }

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = [0x75u8, 0x2a];
    let deserializer = v8::ValueDeserializer::new(tc, Box::new(Boom), &bytes);
    let ctx = tc.get_current_context();
    let _ = deserializer.read_value(ctx);
    eprintln!("marker:after-hook");
}

#[test]
#[ignore]
fn probe_panic_get_wasm_module_from_id() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    struct Boom;
    impl v8::ValueDeserializerImpl for Boom {
        fn get_wasm_module_from_id<'s>(
            &self,
            _scope: &mut v8::PinScope<'s, '_>,
            _clone_id: u32,
        ) -> Option<v8::Local<'s, v8::WasmModuleObject>> {
            eprintln!("marker:get-wasm-from-id");
            panic!("serdel-panic-get-wasm-from-id");
        }
    }

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = [0x77u8, 0x15];
    let deserializer = v8::ValueDeserializer::new(tc, Box::new(Boom), &bytes);
    let ctx = tc.get_current_context();
    let _ = deserializer.read_value(ctx);
    eprintln!("marker:after-hook");
}

#[test]
#[ignore]
fn probe_panic_throw_data_clone_error() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    struct Boom;
    impl v8::ValueSerializerImpl for Boom {
        fn throw_data_clone_error<'s>(
            &self,
            _scope: &mut v8::PinScope<'s, '_>,
            _message: v8::Local<'s, v8::String>,
        ) {
            eprintln!("marker:throw-data-clone-error");
            panic!("serdel-panic-throw-data-clone-error");
        }
    }

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    // A function triggers the engine-side data-clone error path, which calls
    // into the delegate's throw_data_clone_error hook.
    let src = v8::String::new(tc, "() => 1").unwrap();
    let value = v8::Script::compile(tc, src, None)
        .and_then(|s| s.run(tc))
        .expect("function eval");
    let serializer = v8::ValueSerializer::new(tc, Box::new(Boom));
    let ctx = tc.get_current_context();
    let _ = serializer.write_value(ctx, value);
    eprintln!("marker:after-hook");
}

/// Diagnostic (kept out of the normal suite): constructs TWO serializers
/// with a counting `has_custom_host_object` in the SAME isolate and prints
/// the counts after each construction, documenting when the pinned build
/// consults the hook.
#[test]
#[ignore]
fn probe_has_custom_host_object_cache() {
    oracle::ensure_v8();
    let isolate = probe_isolate();
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    type Counter = Rc<RefCell<usize>>;

    struct Counting(bool, Counter);
    impl v8::ValueSerializerImpl for Counting {
        fn throw_data_clone_error<'s>(
            &self,
            scope: &mut v8::PinScope<'s, '_>,
            message: v8::Local<'s, v8::String>,
        ) {
            rethrow_noop(scope, message);
        }

        fn has_custom_host_object(&self, _isolate: &v8::Isolate) -> bool {
            *self.1.borrow_mut() += 1;
            self.0
        }
    }

    macro_rules! run_one {
        ($custom:literal) => {{
            let counter: Counter = Rc::new(RefCell::new(0));
            let tc = std::pin::pin!(v8::TryCatch::new(scope));
            let tc = &mut tc.init();
            let obj = v8::Object::new(tc);
            let value: v8::Local<v8::Value> = obj.into();
            let serializer =
                v8::ValueSerializer::new(tc, Box::new(Counting($custom, Rc::clone(&counter))));
            let ctx = tc.get_current_context();
            let ok = serializer.write_value(ctx, value);
            eprintln!(
                "probe:has-custom custom={} count={} write={ok:?}",
                $custom,
                counter.borrow()
            );
        }};
    }

    run_one!(true);
    run_one!(true);
    run_one!(false);
}
