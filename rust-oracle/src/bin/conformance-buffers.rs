//! Buffers/serialization conformance slice for the pinned `v8` crate.
//!
//! Characterizes, in fixed order, the observable contract of:
//! - `ArrayBuffer` construction (native + JS-created, the zero-length
//!   `data()` behavior), backing-store view predicates, JS `byteLength`.
//! - `BackingStore` ownership for non-shared stores (`from_vec` /
//!   `from_boxed_slice`), shared stores (`SharedArrayBuffer`), aliasing one
//!   store through several `ArrayBuffer`s, use-count transitions under GC
//!   (via the crate's `assert_use_count_eq` polling assertion), and the
//!   external `new_backing_store_from_ptr` deleter callback.
//! - Detach: native `detach(None/Some(key))`, `[[ArrayBufferDetachKey]]`
//!   gating, view-follows-buffer semantics, and JS
//!   `ArrayBuffer.prototype.transfer`.
//! - `ArrayBufferView` / `TypedArray` / `DataView` geometry and boundary
//!   behavior (in-bounds views, out-of-bounds `TypedArray::new` returning
//!   `None`, per-type `MAX_LENGTH` constants).
//! - `ValueSerializer` / `ValueDeserializer` wire bytes for primitives,
//!   plain objects and `ArrayBuffer`s (cloned and transferred), the
//!   data-clone error path, and deterministic deserializer failures.
//!
//! Everything is normalized per `src/json.rs` rules: no addresses, no
//! timings, exact V8 error strings for the pinned build. The runner emits
//! the same JSON-lines protocol as the base and host slices
//! (`{"check":..,"ok":..,"value"|"expected"/"actual"}` + final summary).
//!
//! This slice performs no platform shutdown, so it can be verified
//! in-process; its fixture is pinned by
//! `tests/conformance_buffers_fixture.rs` (binary output only: the checks
//! live in this binary because the existing `src/checks` registries are
//! shared files that this slice must not modify).

use std::cell::Cell;
use std::cell::RefCell;
use std::ffi::c_void;
use std::io::Write as _;
use std::process::ExitCode;
use std::rc::Rc;
use std::sync::atomic::{AtomicUsize, Ordering};

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};
use v8::ValueDeserializerHelper as _;
use v8::ValueSerializerHelper as _;

// ---------------------------------------------------------------------------
// Helpers (local to this binary; the crate's `checks::harness` is pub(crate)
// and existing files must not be modified to expose it).
// ---------------------------------------------------------------------------

/// Lowercase hex without separators: the canonical encoding for wire bytes
/// and backing-store contents in this slice.
fn hex(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        use std::fmt::Write as _;
        let _ = write!(out, "{byte:02x}");
    }
    out
}

fn hex_to_bytes(s: &str) -> Vec<u8> {
    (0..s.len() / 2)
        .map(|i| u8::from_str_radix(&s[2 * i..2 * i + 2], 16).unwrap_or(0))
        .collect()
}

/// Compiles and runs `source`, returning the completion value (`None` on
/// syntax error or runtime throw; every eval in this slice is expected to
/// succeed and unwraps the result at the call site).
fn eval<'s>(scope: &mut v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let src = v8::String::new(scope, source)?;
    v8::Script::compile(scope, src, None)?.run(scope)
}

/// Compiles, runs and ToString's `source` ("" on failure).
fn eval_text(scope: &mut v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source)
        .and_then(|v| v.to_string(scope))
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

/// ToString of an arbitrary value ("" when conversion fails).
fn value_text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

/// Reads the full contents of an `ArrayBuffer` through its backing store
/// (`BackingStore` derefs to `[Cell<u8>]`).
fn backing_store_bytes(bs: &v8::SharedRef<v8::BackingStore>) -> Vec<u8> {
    bs.iter().map(Cell::get).collect()
}

/// Reports `assert_use_count_eq(n)` without aborting the run: true when the
/// live shared-reference count equals n. The crate's assertion polls for up
/// to one second before panicking; the panic is caught here and reported as
/// the observation (a mismatched expectation only costs time during
/// characterization, never correctness).
fn use_count_is(bs: &v8::SharedRef<v8::BackingStore>, n: usize) -> bool {
    std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        bs.assert_use_count_eq(n);
    }))
    .is_ok()
}

/// Captures the message handed to `throw_data_clone_error` and re-throws it
/// as a regular Error so a surrounding TryCatch observes the failure.
struct DataCloneErrorReporter {
    slot: Rc<RefCell<Option<String>>>,
}

impl v8::ValueSerializerImpl for DataCloneErrorReporter {
    fn throw_data_clone_error<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        message: v8::Local<'s, v8::String>,
    ) {
        let text = message.to_rust_string_lossy(scope);
        if let Some(str_handle) = v8::String::new(scope, &text) {
            let exc = v8::Exception::error(scope, str_handle);
            scope.throw_exception(exc);
        }
        *self.slot.borrow_mut() = Some(text);
    }
}

struct SerOutcome {
    /// `write_value` returned `Some(true)`.
    ok: bool,
    /// `release()` bytes after the attempt (partial output on failure).
    wire_hex: String,
    /// Message forwarded to `throw_data_clone_error` ("" if never called).
    clone_error: String,
}

/// Serializes `value` with a fresh `ValueSerializer` inside the caller's
/// TryCatch scope. `prep` runs after construction and before `write_value`
/// (used to register ArrayBuffer transfers).
fn serialize_with(
    scope: &mut v8::PinScope<'_, '_>,
    value: v8::Local<'_, v8::Value>,
    prep: impl FnOnce(&v8::ValueSerializer<'_>),
) -> SerOutcome {
    let captured: Rc<RefCell<Option<String>>> = Rc::new(RefCell::new(None));
    let reporter = DataCloneErrorReporter {
        slot: Rc::clone(&captured),
    };
    let serializer = v8::ValueSerializer::new(scope, Box::new(reporter));
    prep(&serializer);
    let context = scope.get_current_context();
    let ok = serializer.write_value(context, value) == Some(true);
    let wire = serializer.release();
    drop(serializer);
    let clone_error = captured.borrow().clone().unwrap_or_default();
    SerOutcome {
        ok,
        wire_hex: hex(&wire),
        clone_error,
    }
}

fn serialize(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> SerOutcome {
    serialize_with(scope, value, |_| {})
}

/// Type/shape description of a deserialized value, normalized for JSONL.
/// `keys` are probed only when the value is a plain object.
fn describe_value(
    scope: &mut v8::PinScope<'_, '_>,
    value: v8::Local<'_, v8::Value>,
    keys: &[&'static str],
) -> Json {
    if value.is_undefined() {
        return Json::obj(vec![("type", Json::s("undefined"))]);
    }
    if value.is_null() {
        return Json::obj(vec![("type", Json::s("null"))]);
    }
    if value.is_boolean() {
        return Json::obj(vec![
            ("type", Json::s("boolean")),
            ("value", Json::b(value.boolean_value(scope))),
        ]);
    }
    if value.is_int32() {
        return Json::obj(vec![
            ("type", Json::s("int32")),
            (
                "value",
                Json::i(i64::from(value.int32_value(scope).unwrap_or_default())),
            ),
        ]);
    }
    if value.is_number() {
        let n = value.number_value(scope).unwrap_or_default();
        if n.fract() == 0.0 && n.abs() < 9_000_000_000.0 {
            return Json::obj(vec![
                ("type", Json::s("number")),
                ("value", Json::i(n as i64)),
            ]);
        }
        return Json::obj(vec![("type", Json::s("number")), ("value", Json::f(n))]);
    }
    if value.is_string() {
        return Json::obj(vec![
            ("type", Json::s("string")),
            ("value", Json::s(&value_text(scope, value))),
        ]);
    }
    if value.is_array_buffer() {
        if let Ok(ab) = value.try_cast::<v8::ArrayBuffer>() {
            let bs = ab.get_backing_store();
            return Json::obj(vec![
                ("type", Json::s("arraybuffer")),
                ("byte_length", Json::i(ab.byte_length() as i64)),
                ("contents", Json::s(&hex(&backing_store_bytes(&bs)))),
            ]);
        }
    }
    if value.is_object() {
        if let Ok(obj) = value.try_cast::<v8::Object>() {
            let mut fields = vec![("type", Json::s("object"))];
            for key in keys {
                let observed = v8::String::new(scope, key)
                    .and_then(|k| obj.get(scope, k.into()))
                    .map(|v| describe_value(scope, v, &[]))
                    .unwrap_or(Json::Null);
                fields.push((key, observed));
            }
            return Json::obj(fields);
        }
    }
    Json::obj(vec![("type", Json::s("other"))])
}

/// Deserializes `bytes` inside the caller's TryCatch and normalizes the
/// outcome: the described value (or null) plus the caught message, if any.
/// This must be a macro because `has_caught`/`message` live on the
/// `PinnedRef<TryCatch>` wrapper, not on the `PinScope` it coerces to.
///
/// IMPORTANT: `$bytes` must reference a binding that outlives the macro
/// block (e.g. a `let bytes = ...;` in the caller). The C++ deserializer
/// stores the raw data pointer, so a temporary like `&hex_to_bytes(&wire)`
/// would dangle after the constructor's statement and fail intermittently.
macro_rules! deser_describe {
    ($tc:expr, $bytes:expr, $keys:expr) => {{
        struct NoCustomReads;
        impl v8::ValueDeserializerImpl for NoCustomReads {}

        let deserializer = v8::ValueDeserializer::new($tc, Box::new(NoCustomReads), $bytes);
        let context = $tc.get_current_context();
        let value = deserializer.read_value(context);
        drop(deserializer);

        let described = value
            .map(|v| describe_value($tc, v, $keys))
            .unwrap_or(Json::Null);
        let caught = $tc.has_caught();
        let message = $tc
            .message()
            .map(|m| m.get($tc).to_rust_string_lossy($tc))
            .unwrap_or_default();
        Json::obj(vec![
            ("read", described),
            ("caught", Json::b(caught)),
            ("message", Json::s(&message)),
        ])
    }};
}

// ---------------------------------------------------------------------------
// Checks. Order is part of the observable contract (the fixture is ordered).
// ---------------------------------------------------------------------------

fn ab_new_basics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ab16 = v8::ArrayBuffer::new(scope, 16);
    let ab0 = v8::ArrayBuffer::new(scope, 0);
    let js_ab_value = eval(scope, "new ArrayBuffer(8)").unwrap();
    let js_is_ab = js_ab_value.is_array_buffer();
    let js_ab = js_ab_value.try_cast::<v8::ArrayBuffer>().ok().unwrap();

    let actual = Json::obj(vec![
        (
            "len16",
            Json::obj(vec![
                ("byte_length", Json::i(ab16.byte_length() as i64)),
                ("is_detachable", Json::b(ab16.is_detachable())),
                ("was_detached", Json::b(ab16.was_detached())),
                ("data_is_some", Json::b(ab16.data().is_some())),
            ]),
        ),
        (
            "len0",
            Json::obj(vec![
                ("byte_length", Json::i(ab0.byte_length() as i64)),
                ("data_is_some", Json::b(ab0.data().is_some())),
                // Pinned nuance: the Rust wrapper early-returns false whenever
                // byte_length is nonzero, but for a zero-length buffer the
                // real WasDetached bit is consulted.
                ("was_detached", Json::b(ab0.was_detached())),
            ]),
        ),
        (
            "js_created",
            Json::obj(vec![
                ("is_array_buffer", Json::b(js_is_ab)),
                ("byte_length", Json::i(js_ab.byte_length() as i64)),
                ("is_detachable", Json::b(js_ab.is_detachable())),
                ("data_is_some", Json::b(js_ab.data().is_some())),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "len16",
            Json::obj(vec![
                ("byte_length", Json::i(16)),
                ("is_detachable", Json::b(true)),
                ("was_detached", Json::b(false)),
                ("data_is_some", Json::b(true)),
            ]),
        ),
        (
            "len0",
            Json::obj(vec![
                ("byte_length", Json::i(0)),
                ("data_is_some", Json::b(false)),
                ("was_detached", Json::b(false)),
            ]),
        ),
        (
            "js_created",
            Json::obj(vec![
                ("is_array_buffer", Json::b(true)),
                ("byte_length", Json::i(8)),
                ("is_detachable", Json::b(true)),
                ("data_is_some", Json::b(true)),
            ]),
        ),
    ]);
    vec![expect_eq("buffers/ab_new_basics", expected, actual)]
}

/// Ownership chain for a Rust-owned (non-shared) backing store:
/// standalone refcount 1 -> +1 while an ArrayBuffer wraps it -> back to 1
/// after the JS buffer is collected -> bytes still alive and intact, and
/// usable to build a new buffer afterwards.
fn ab_backing_store_ownership() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
        1, 2, 3, 4,
    ]));
    let standalone_count = use_count_is(&bs, 1);
    let standalone_shared = bs.is_shared();
    let standalone_resizable = bs.is_resizable_by_user_javascript();
    let standalone_bytes = hex(&backing_store_bytes(&bs));

    let (attached_count, buffer_len, buffer_bytes_same, copied, contents) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let buffer = v8::ArrayBuffer::with_backing_store(scope, &bs);
        let attached_count = use_count_is(&bs, 2);
        let buffer_len = buffer.byte_length();
        let buffer_bytes_same =
            backing_store_bytes(&buffer.get_backing_store()) == backing_store_bytes(&bs);
        let ta = v8::Uint8Array::new(scope, buffer, 0, 4).unwrap();
        let mut out = [0u8; 4];
        let copied = ta.copy_contents(&mut out);
        (
            attached_count,
            buffer_len,
            buffer_bytes_same,
            copied,
            hex(&out),
        )
    };

    // The handle scope closed; a major GC must release the JS-side shared
    // reference while the standalone reference keeps the memory alive.
    isolate.low_memory_notification();
    let collected_count = use_count_is(&bs, 1);
    let bytes_survive_gc = hex(&backing_store_bytes(&bs)) == standalone_bytes;
    let reusable_after_gc = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let rebuilt = v8::ArrayBuffer::with_backing_store(scope, &bs);
        rebuilt.byte_length() == 4
    };

    let actual = Json::obj(vec![
        ("standalone_count", Json::b(standalone_count)),
        ("standalone_shared", Json::b(standalone_shared)),
        ("standalone_resizable", Json::b(standalone_resizable)),
        ("standalone_bytes", Json::s(&standalone_bytes)),
        ("attached_count", Json::b(attached_count)),
        ("buffer_len", Json::i(buffer_len as i64)),
        ("buffer_bytes_same", Json::b(buffer_bytes_same)),
        ("copied", Json::i(copied as i64)),
        ("contents", Json::s(&contents)),
        ("collected_count", Json::b(collected_count)),
        ("bytes_survive_gc", Json::b(bytes_survive_gc)),
        ("reusable_after_gc", Json::b(reusable_after_gc)),
    ]);
    let expected = Json::obj(vec![
        ("standalone_count", Json::b(true)),
        ("standalone_shared", Json::b(false)),
        ("standalone_resizable", Json::b(false)),
        ("standalone_bytes", Json::s("01020304")),
        ("attached_count", Json::b(true)),
        ("buffer_len", Json::i(4)),
        ("buffer_bytes_same", Json::b(true)),
        ("copied", Json::i(4)),
        ("contents", Json::s("01020304")),
        ("collected_count", Json::b(true)),
        ("bytes_survive_gc", Json::b(true)),
        ("reusable_after_gc", Json::b(true)),
    ]);
    vec![expect_eq(
        "buffers/ab_backing_store_ownership",
        expected,
        actual,
    )]
}

/// One backing store aliased by two ArrayBuffers: writes through the store
/// are visible through both views, and detaching one buffer leaves the
/// other fully functional.
fn ab_backing_store_alias() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_boxed_slice(
        Box::new([10, 20, 30, 40]),
    ));
    let ab1 = v8::ArrayBuffer::with_backing_store(scope, &bs);
    let ab2 = v8::ArrayBuffer::with_backing_store(scope, &bs);
    let count_two_buffers = use_count_is(&bs, 3);

    // Interior-mutable write through the store, observed through ab2's view.
    bs[1].set(99);
    let mut seen_by_ab2 = [0u8; 4];
    let copied = v8::Uint8Array::new(scope, ab2, 0, 4)
        .unwrap()
        .copy_contents(&mut seen_by_ab2);

    let detach_ab1 = ab1.detach(None) == Some(true);
    let ab1_len = ab1.byte_length();
    let ab2_len = ab2.byte_length();
    let mut after_detach = [0u8; 4];
    let copied_after_detach = v8::Uint8Array::new(scope, ab2, 0, 4)
        .unwrap()
        .copy_contents(&mut after_detach);

    let actual = Json::obj(vec![
        ("count_two_buffers", Json::b(count_two_buffers)),
        ("seen_by_ab2", Json::s(&hex(&seen_by_ab2))),
        ("copied", Json::i(copied as i64)),
        ("detach_ab1", Json::b(detach_ab1)),
        ("ab1_len", Json::i(ab1_len as i64)),
        ("ab2_len", Json::i(ab2_len as i64)),
        ("ab2_after_detach", Json::s(&hex(&after_detach))),
        ("copied_after_detach", Json::i(copied_after_detach as i64)),
    ]);
    let expected = Json::obj(vec![
        ("count_two_buffers", Json::b(true)),
        ("seen_by_ab2", Json::s("0a631e28")),
        ("copied", Json::i(4)),
        ("detach_ab1", Json::b(true)),
        ("ab1_len", Json::i(0)),
        ("ab2_len", Json::i(4)),
        ("ab2_after_detach", Json::s("0a631e28")),
        ("copied_after_detach", Json::i(4)),
    ]);
    vec![expect_eq(
        "buffers/ab_backing_store_alias",
        expected,
        actual,
    )]
}

/// Shared backing store (`SharedArrayBuffer` flavor): the store reports
/// `is_shared`, the JS value is a SharedArrayBuffer, and both the SAB and
/// the store agree on length.
fn ab_backing_store_shared_sab() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let bs = v8::SharedRef::from(v8::SharedArrayBuffer::new_backing_store(scope, 8));
    let store_is_shared = bs.is_shared();
    let store_len = bs.byte_length();
    let sab = v8::SharedArrayBuffer::with_backing_store(scope, &bs);
    let sab_len = sab.byte_length();
    let from_sab_shared = sab.get_backing_store().is_shared();
    let sab_value: v8::Local<v8::Value> = sab.into();
    let is_sab = sab_value.is_shared_array_buffer();
    let not_plain_ab = !sab_value.is_array_buffer();
    let count_with_sab = use_count_is(&bs, 2);

    let actual = Json::obj(vec![
        ("store_is_shared", Json::b(store_is_shared)),
        ("store_len", Json::i(store_len as i64)),
        ("is_shared_array_buffer", Json::b(is_sab)),
        ("not_plain_array_buffer", Json::b(not_plain_ab)),
        ("sab_len", Json::i(sab_len as i64)),
        ("backing_store_is_shared", Json::b(from_sab_shared)),
        ("use_count_with_sab", Json::b(count_with_sab)),
    ]);
    let expected = Json::obj(vec![
        ("store_is_shared", Json::b(true)),
        ("store_len", Json::i(8)),
        ("is_shared_array_buffer", Json::b(true)),
        ("not_plain_array_buffer", Json::b(true)),
        ("sab_len", Json::i(8)),
        ("backing_store_is_shared", Json::b(true)),
        ("use_count_with_sab", Json::b(true)),
    ]);
    vec![expect_eq(
        "buffers/ab_backing_store_shared_sab",
        expected,
        actual,
    )]
}

/// Owned-byte and raw-pointer constructors for SharedArrayBuffer backing
/// stores. Both produce shared stores; the raw-pointer deleter observes the
/// original callback arguments exactly once after the final reference drops.
fn sab_backing_store_owned_external() -> Vec<CheckOutcome> {
    struct DeleterState {
        invocations: AtomicUsize,
        observed_len: AtomicUsize,
        data_echo: AtomicUsize,
    }

    unsafe extern "C" fn counting_deleter(
        data: *mut c_void,
        byte_length: usize,
        deleter_data: *mut c_void,
    ) {
        let state = unsafe { &*(deleter_data as *const DeleterState) };
        state.invocations.fetch_add(1, Ordering::SeqCst);
        state.observed_len.store(byte_length, Ordering::SeqCst);
        state
            .data_echo
            .store(deleter_data as usize, Ordering::SeqCst);
        if byte_length > 0 {
            let slice = std::ptr::slice_from_raw_parts_mut(data.cast::<u8>(), byte_length);
            drop(unsafe { Box::from_raw(slice) });
        }
    }

    let isolate = &mut v8::Isolate::new(Default::default());
    let owned = v8::SharedRef::from(v8::SharedArrayBuffer::new_backing_store_from_vec(vec![
        1, 3, 5, 7,
    ]));
    let owned_is_shared = owned.is_shared();
    let owned_contents = hex(&backing_store_bytes(&owned));
    let owned_attached_length = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        v8::SharedArrayBuffer::with_backing_store(scope, &owned).byte_length()
    };
    isolate.low_memory_notification();
    drop(owned);

    let state = Box::leak(Box::new(DeleterState {
        invocations: AtomicUsize::new(0),
        observed_len: AtomicUsize::new(0),
        data_echo: AtomicUsize::new(0),
    }));
    let state_addr = state as *const DeleterState as usize;
    let memory = vec![9u8, 8, 6].into_boxed_slice();
    let raw_memory = Box::into_raw(memory);
    let external = v8::SharedRef::from(unsafe {
        v8::SharedArrayBuffer::new_backing_store_from_ptr(
            raw_memory.cast::<c_void>(),
            3,
            counting_deleter,
            state as *const DeleterState as *mut c_void,
        )
    });
    let external_is_shared = external.is_shared();
    let external_contents = hex(&backing_store_bytes(&external));
    {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let _ = v8::SharedArrayBuffer::with_backing_store(scope, &external);
    }
    isolate.low_memory_notification();
    drop(external);

    let actual = Json::obj(vec![
        ("owned_is_shared", Json::b(owned_is_shared)),
        ("owned_contents", Json::s(&owned_contents)),
        (
            "owned_attached_byte_length",
            Json::i(owned_attached_length as i64),
        ),
        ("external_is_shared", Json::b(external_is_shared)),
        ("external_contents", Json::s(&external_contents)),
        (
            "external_invocations",
            Json::i(state.invocations.load(Ordering::SeqCst) as i64),
        ),
        (
            "external_observed_byte_length",
            Json::i(state.observed_len.load(Ordering::SeqCst) as i64),
        ),
        (
            "external_deleter_data_roundtrip",
            Json::b(state.data_echo.load(Ordering::SeqCst) == state_addr),
        ),
    ]);
    let expected = Json::obj(vec![
        ("owned_is_shared", Json::b(true)),
        ("owned_contents", Json::s("01030507")),
        ("owned_attached_byte_length", Json::i(4)),
        ("external_is_shared", Json::b(true)),
        ("external_contents", Json::s("090806")),
        ("external_invocations", Json::i(1)),
        ("external_observed_byte_length", Json::i(3)),
        ("external_deleter_data_roundtrip", Json::b(true)),
    ]);
    vec![expect_eq(
        "buffers/sab_backing_store_owned_external",
        expected,
        actual,
    )]
}

/// A JS-created resizable ArrayBuffer reports
/// `is_resizable_by_user_javascript` on its backing store.
fn ab_resizable_backing_store() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let value = eval(scope, "new ArrayBuffer(8, {maxByteLength: 16})").unwrap();
    let ab = value.try_cast::<v8::ArrayBuffer>().ok().unwrap();
    let bs = ab.get_backing_store();

    let actual = Json::obj(vec![
        ("byte_length", Json::i(ab.byte_length() as i64)),
        (
            "is_resizable_by_user_javascript",
            Json::b(bs.is_resizable_by_user_javascript()),
        ),
        ("is_shared", Json::b(bs.is_shared())),
        ("is_detachable", Json::b(ab.is_detachable())),
    ]);
    let expected = Json::obj(vec![
        ("byte_length", Json::i(8)),
        ("is_resizable_by_user_javascript", Json::b(true)),
        ("is_shared", Json::b(false)),
        ("is_detachable", Json::b(true)),
    ]);
    vec![expect_eq(
        "buffers/ab_resizable_backing_store",
        expected,
        actual,
    )]
}

/// Native detach without a key: sizes collapse, data goes away, JS-side
/// `byteLength`/`detached` agree, and a second detach is a no-op success.
fn ab_detach_basic() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    eval(scope, "globalThis.ab = new ArrayBuffer(8)").unwrap();
    let ab_value = eval(scope, "ab").unwrap();
    let ab = ab_value.try_cast::<v8::ArrayBuffer>().ok().unwrap();

    let before = (
        ab.byte_length(),
        ab.is_detachable(),
        ab.was_detached(),
        ab.data().is_some(),
    );
    let detach_result = ab.detach(None);
    let after = (ab.byte_length(), ab.was_detached(), ab.data().is_some());
    let js_sees = eval_text(scope, "`${ab.byteLength},${ab.detached}`");
    let second_detach = ab.detach(None);

    let actual = Json::obj(vec![
        (
            "before",
            Json::obj(vec![
                ("byte_length", Json::i(before.0 as i64)),
                ("is_detachable", Json::b(before.1)),
                ("was_detached", Json::b(before.2)),
                ("data_is_some", Json::b(before.3)),
            ]),
        ),
        ("detach_result", Json::b(detach_result == Some(true))),
        (
            "after",
            Json::obj(vec![
                ("byte_length", Json::i(after.0 as i64)),
                ("was_detached", Json::b(after.1)),
                ("data_is_some", Json::b(after.2)),
            ]),
        ),
        ("js_sees", Json::s(&js_sees)),
        ("second_detach", Json::b(second_detach == Some(true))),
    ]);
    let expected = Json::obj(vec![
        (
            "before",
            Json::obj(vec![
                ("byte_length", Json::i(8)),
                ("is_detachable", Json::b(true)),
                ("was_detached", Json::b(false)),
                ("data_is_some", Json::b(true)),
            ]),
        ),
        ("detach_result", Json::b(true)),
        (
            "after",
            Json::obj(vec![
                ("byte_length", Json::i(0)),
                ("was_detached", Json::b(true)),
                ("data_is_some", Json::b(false)),
            ]),
        ),
        ("js_sees", Json::s("0,true")),
        ("second_detach", Json::b(true)),
    ]);
    vec![expect_eq("buffers/ab_detach_basic", expected, actual)]
}

/// `[[ArrayBufferDetachKey]]` gates native detach: a mismatched key leaves
/// the buffer untouched (and returns None), while the stored key detaches.
fn ab_detach_key_gate() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ab = v8::ArrayBuffer::new(scope, 8);
    let key = v8::String::new(scope, "owner").unwrap();
    let wrong = v8::String::new(scope, "other").unwrap();
    ab.set_detach_key(key.into());

    let wrong_key_is_none = ab.detach(Some(wrong.into())).is_none();
    let untouched = (ab.byte_length(), ab.was_detached());
    let none_key_result = ab.detach(None);
    let state_after_none = (ab.byte_length(), ab.was_detached());
    let right_key_result = ab.detach(Some(key.into()));
    let final_state = (ab.byte_length(), ab.was_detached());

    let actual = Json::obj(vec![
        ("wrong_key_is_none", Json::b(wrong_key_is_none)),
        (
            "untouched_after_wrong",
            Json::obj(vec![
                ("byte_length", Json::i(untouched.0 as i64)),
                ("was_detached", Json::b(untouched.1)),
            ]),
        ),
        ("none_key_result", Json::b(none_key_result == Some(true))),
        (
            "state_after_none",
            Json::obj(vec![
                ("byte_length", Json::i(state_after_none.0 as i64)),
                ("was_detached", Json::b(state_after_none.1)),
            ]),
        ),
        ("right_key_result", Json::b(right_key_result == Some(true))),
        (
            "final_state",
            Json::obj(vec![
                ("byte_length", Json::i(final_state.0 as i64)),
                ("was_detached", Json::b(final_state.1)),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        ("wrong_key_is_none", Json::b(true)),
        (
            "untouched_after_wrong",
            Json::obj(vec![
                ("byte_length", Json::i(8)),
                ("was_detached", Json::b(false)),
            ]),
        ),
        // A set detach key also rejects a detach attempt WITHOUT a key.
        ("none_key_result", Json::b(false)),
        (
            "state_after_none",
            Json::obj(vec![
                ("byte_length", Json::i(8)),
                ("was_detached", Json::b(false)),
            ]),
        ),
        ("right_key_result", Json::b(true)),
        (
            "final_state",
            Json::obj(vec![
                ("byte_length", Json::i(0)),
                ("was_detached", Json::b(true)),
            ]),
        ),
    ]);
    vec![expect_eq("buffers/ab_detach_key_gate", expected, actual)]
}

/// Views follow their buffer through a native detach: geometry collapses to
/// zero while `buffer()` still resolves to the detached ArrayBuffer, and JS
/// reads through the view observe the collapse.
fn ab_detach_views_follow() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    eval(scope, "globalThis.ab = new ArrayBuffer(8)").unwrap();
    eval(scope, "globalThis.ta = new Uint8Array(ab, 2, 4)").unwrap();
    let ab_value = eval(scope, "ab").unwrap();
    let ab = ab_value.try_cast::<v8::ArrayBuffer>().ok().unwrap();
    let ta_value = eval(scope, "ta").unwrap();
    let ta = ta_value.try_cast::<v8::TypedArray>().ok().unwrap();

    let before = (ta.length(), ta.byte_offset(), ta.byte_length());
    let js_before = eval_text(scope, "`${ta.length},${ta.byteLength},${ta[0]}`");

    let detach_result = ab.detach(None);
    let after = (ta.length(), ta.byte_length());
    let view_buffer_is_detached_ab = ta.buffer(scope).map(|b| b == ab).unwrap_or(false);
    let js_after = eval_text(scope, "`${ta.length},${ta.byteLength},${ta[0]}`");
    // A zero-length view over the now-detached (zero-length) buffer.
    let view_after_detach = v8::Uint8Array::new(scope, ab, 0, 0).is_some();

    let actual = Json::obj(vec![
        (
            "before",
            Json::obj(vec![
                ("length", Json::i(before.0 as i64)),
                ("byte_offset", Json::i(before.1 as i64)),
                ("byte_length", Json::i(before.2 as i64)),
                ("js", Json::s(&js_before)),
            ]),
        ),
        ("detach_result", Json::b(detach_result == Some(true))),
        (
            "after",
            Json::obj(vec![
                ("length", Json::i(after.0 as i64)),
                ("byte_length", Json::i(after.1 as i64)),
            ]),
        ),
        (
            "view_buffer_is_detached_ab",
            Json::b(view_buffer_is_detached_ab),
        ),
        ("js_after", Json::s(&js_after)),
        ("view_after_detach_is_some", Json::b(view_after_detach)),
    ]);
    let expected = Json::obj(vec![
        (
            "before",
            Json::obj(vec![
                ("length", Json::i(4)),
                ("byte_offset", Json::i(2)),
                ("byte_length", Json::i(4)),
                ("js", Json::s("4,4,0")),
            ]),
        ),
        ("detach_result", Json::b(true)),
        (
            "after",
            Json::obj(vec![("length", Json::i(0)), ("byte_length", Json::i(0))]),
        ),
        ("view_buffer_is_detached_ab", Json::b(true)),
        ("js_after", Json::s("0,0,undefined")),
        ("view_after_detach_is_some", Json::b(true)),
    ]);
    vec![expect_eq(
        "buffers/ab_detach_views_follow",
        expected,
        actual,
    )]
}

/// JS `ArrayBuffer.prototype.transfer` detaches the source and produces a
/// sized destination without any native detach call.
fn ab_detach_js_transfer() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let js_sees = eval_text(
        scope,
        "const src = new ArrayBuffer(8); \
         const dst = src.transfer(); \
         `${src.detached},${src.byteLength},${dst.byteLength}`",
    );

    let actual = Json::obj(vec![("js_sees", Json::s(&js_sees))]);
    let expected = Json::obj(vec![("js_sees", Json::s("true,0,8"))]);
    vec![expect_eq("buffers/ab_detach_js_transfer", expected, actual)]
}

/// TypedArray geometry and construction boundaries over a 16-byte buffer.
fn view_typed_array_bounds() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ab = v8::ArrayBuffer::new(scope, 16);
    let ta = v8::Uint8Array::new(scope, ab, 4, 8).unwrap();
    let buffer_strict_equal = ta.buffer(scope).map(|b| b == ab).unwrap_or(false);

    let mut contents = [0u8; 8];
    let copied = ta.copy_contents(&mut contents);

    let actual = Json::obj(vec![
        (
            "in_bounds",
            Json::obj(vec![
                ("length", Json::i(ta.length() as i64)),
                ("byte_offset", Json::i(ta.byte_offset() as i64)),
                ("byte_length", Json::i(ta.byte_length() as i64)),
                ("has_buffer", Json::b(ta.has_buffer())),
                ("buffer_strict_equal", Json::b(buffer_strict_equal)),
                ("contents", Json::s(&hex(&contents))),
                ("copied", Json::i(copied as i64)),
            ]),
        ),
        // Out-of-bounds and misaligned-offset construction are both V8
        // CHECK-fatals (process aborts), not empty locals; those boundaries
        // are characterized out-of-process by tests/buffers_negative.rs and
        // must never run in this binary.
        (
            "end_zero_len_is_some",
            Json::b(v8::Uint8Array::new(scope, ab, 16, 0).is_some()),
        ),
        (
            "zero_len_is_some",
            Json::b(v8::Uint8Array::new(scope, ab, 0, 0).is_some()),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "in_bounds",
            Json::obj(vec![
                ("length", Json::i(8)),
                ("byte_offset", Json::i(4)),
                ("byte_length", Json::i(8)),
                ("has_buffer", Json::b(true)),
                ("buffer_strict_equal", Json::b(true)),
                ("contents", Json::s("0000000000000000")),
                ("copied", Json::i(8)),
            ]),
        ),
        ("end_zero_len_is_some", Json::b(true)),
        ("zero_len_is_some", Json::b(true)),
    ]);
    vec![expect_eq(
        "buffers/view_typed_array_bounds",
        expected,
        actual,
    )]
}

/// DataView geometry through the ArrayBufferView surface.
fn view_data_view_bounds() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ab = v8::ArrayBuffer::new(scope, 16);
    let dv = v8::DataView::new(scope, ab, 2, 8);
    let byte_offset = dv.byte_offset();
    let byte_length = dv.byte_length();
    let dv_value: v8::Local<v8::Value> = dv.into();

    let actual = Json::obj(vec![
        ("is_data_view", Json::b(dv_value.is_data_view())),
        (
            "is_array_buffer_view",
            Json::b(dv_value.is_array_buffer_view()),
        ),
        ("is_typed_array", Json::b(dv_value.is_typed_array())),
        ("byte_offset", Json::i(byte_offset as i64)),
        ("byte_length", Json::i(byte_length as i64)),
    ]);
    let expected = Json::obj(vec![
        ("is_data_view", Json::b(true)),
        ("is_array_buffer_view", Json::b(true)),
        ("is_typed_array", Json::b(false)),
        ("byte_offset", Json::i(2)),
        ("byte_length", Json::i(8)),
    ]);
    vec![expect_eq("buffers/view_data_view_bounds", expected, actual)]
}

/// Pinned size limits: the shared TypedArray byte limit, per-type element
/// limits, and the maximum on-heap typed-array size.
fn view_max_sizes() -> Vec<CheckOutcome> {
    let actual = Json::obj(vec![
        (
            "typed_array_max_byte_length",
            Json::i(v8::TypedArray::MAX_BYTE_LENGTH as i64),
        ),
        (
            "uint8_max_length",
            Json::i(v8::Uint8Array::MAX_LENGTH as i64),
        ),
        (
            "float64_max_length",
            Json::i(v8::Float64Array::MAX_LENGTH as i64),
        ),
        (
            "bigint64_max_length",
            Json::i(v8::BigInt64Array::MAX_LENGTH as i64),
        ),
        (
            "typed_array_max_size_in_heap",
            Json::i(v8::TYPED_ARRAY_MAX_SIZE_IN_HEAP as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "typed_array_max_byte_length",
            Json::i(9_007_199_254_740_991),
        ),
        ("uint8_max_length", Json::i(9_007_199_254_740_991)),
        ("float64_max_length", Json::i(1_125_899_906_842_623)),
        ("bigint64_max_length", Json::i(1_125_899_906_842_623)),
        ("typed_array_max_size_in_heap", Json::i(0)),
    ]);
    vec![expect_eq("buffers/view_max_sizes", expected, actual)]
}

/// External backing store with a counting deleter: the deleter runs exactly
/// once, after the JS buffer is collected and the last Rust reference is
/// dropped, and observes the original byte length plus the deleter_data
/// pointer it was constructed with.
fn ext_backing_store_deleter() -> Vec<CheckOutcome> {
    struct DeleterState {
        invocations: AtomicUsize,
        observed_len: AtomicUsize,
        data_echo: AtomicUsize,
    }

    // Reclaims the boxed slice this test handed to V8 and echoes the
    // callback arguments into the shared state.
    unsafe extern "C" fn counting_deleter(
        data: *mut c_void,
        byte_length: usize,
        deleter_data: *mut c_void,
    ) {
        let state = unsafe { &*(deleter_data as *const DeleterState) };
        state.invocations.fetch_add(1, Ordering::SeqCst);
        state.observed_len.store(byte_length, Ordering::SeqCst);
        state
            .data_echo
            .store(deleter_data as usize, Ordering::SeqCst);
        if byte_length > 0 {
            let slice = std::ptr::slice_from_raw_parts_mut(data.cast::<u8>(), byte_length);
            drop(unsafe { Box::from_raw(slice) });
        }
    }

    let isolate = &mut v8::Isolate::new(Default::default());
    let state = Box::leak(Box::new(DeleterState {
        invocations: AtomicUsize::new(0),
        observed_len: AtomicUsize::new(0),
        data_echo: AtomicUsize::new(0),
    }));
    let state_addr = state as *const DeleterState as usize;

    let memory = vec![7u8; 12].into_boxed_slice();
    let raw_memory = Box::into_raw(memory);
    let bs = v8::SharedRef::from(unsafe {
        v8::ArrayBuffer::new_backing_store_from_ptr(
            raw_memory.cast::<c_void>(),
            12,
            counting_deleter,
            state as *const DeleterState as *mut c_void,
        )
    });

    let store_sees_bytes = {
        let observed = backing_store_bytes(&bs);
        observed.len() == 12 && observed.iter().all(|byte| *byte == 7)
    };

    {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let buffer = v8::ArrayBuffer::with_backing_store(scope, &bs);
        debug_assert_eq!(buffer.byte_length(), 12);
    }
    isolate.low_memory_notification();
    drop(bs);

    let actual = Json::obj(vec![
        ("store_sees_bytes", Json::b(store_sees_bytes)),
        (
            "invocations",
            Json::i(state.invocations.load(Ordering::SeqCst) as i64),
        ),
        (
            "observed_byte_length",
            Json::i(state.observed_len.load(Ordering::SeqCst) as i64),
        ),
        (
            "deleter_data_roundtrip",
            Json::b(state.data_echo.load(Ordering::SeqCst) == state_addr),
        ),
    ]);
    let expected = Json::obj(vec![
        ("store_sees_bytes", Json::b(true)),
        ("invocations", Json::i(1)),
        ("observed_byte_length", Json::i(12)),
        ("deleter_data_roundtrip", Json::b(true)),
    ]);
    vec![expect_eq(
        "buffers/ext_backing_store_deleter",
        expected,
        actual,
    )]
}

/// Wire bytes for primitive values (each serialization carries the header)
/// plus successful roundtrips.
fn ser_wire_primitives() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut entries: Vec<(&'static str, Json)> = Vec::new();
    for (name, source) in [
        ("undefined", "undefined"),
        ("null", "null"),
        ("false", "false"),
        ("true", "true"),
        ("zero", "0"),
        ("one", "1"),
        ("neg_one", "-1"),
        ("two_point_five", "2.5"),
        ("string_abc", "\"abc\""),
        // Canonical embedder flow: an explicit WriteHeader() before the
        // value (write_value alone emits NO header bytes).
        ("true_hdr", "#with-header"),
        ("string_abc_hdr", "#with-header"),
    ] {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let (eval_source, with_header) = match name {
            "true_hdr" => ("true", true),
            "string_abc_hdr" => ("\"abc\"", true),
            _ => (source, false),
        };
        let value = eval(tc, eval_source).unwrap();
        let outcome = serialize_with(tc, value, |serializer| {
            if with_header {
                serializer.write_header();
            }
        });
        entries.push((
            name,
            Json::obj(vec![
                ("ok", Json::b(outcome.ok)),
                ("wire", Json::s(&outcome.wire_hex)),
                ("clone_error", Json::s(&outcome.clone_error)),
            ]),
        ));
        let roundtrip_bytes = hex_to_bytes(&outcome.wire_hex);
        let roundtrip = deser_describe!(tc, &roundtrip_bytes, &[]);
        entries.push((roundtrip_tag(name), roundtrip));
    }
    let actual = Json::obj(entries);
    let host_object_rejection =
        "Uncaught Error: Deno deserializer: read_host_object not implemented";
    let wire = |ok: bool, hex_text: &str, error: &str| {
        Json::obj(vec![
            ("ok", Json::b(ok)),
            ("wire", Json::s(hex_text)),
            ("clone_error", Json::s(error)),
        ])
    };
    let read_ok = |described: Json| {
        Json::obj(vec![
            ("read", described),
            ("caught", Json::b(false)),
            ("message", Json::s("")),
        ])
    };
    let read_rejected = Json::obj(vec![
        ("read", Json::Null),
        ("caught", Json::b(true)),
        ("message", Json::s(host_object_rejection)),
    ]);
    let expected = Json::obj(vec![
        ("undefined", wire(true, "5f", "")),
        (
            "undefined_rt",
            read_ok(Json::obj(vec![("type", Json::s("undefined"))])),
        ),
        ("null", wire(true, "30", "")),
        (
            "null_rt",
            read_ok(Json::obj(vec![("type", Json::s("null"))])),
        ),
        ("false", wire(true, "46", "")),
        (
            "false_rt",
            read_ok(Json::obj(vec![
                ("type", Json::s("boolean")),
                ("value", Json::b(false)),
            ])),
        ),
        ("true", wire(true, "54", "")),
        (
            "true_rt",
            read_ok(Json::obj(vec![
                ("type", Json::s("boolean")),
                ("value", Json::b(true)),
            ])),
        ),
        ("zero", wire(true, "4900", "")),
        (
            "zero_rt",
            read_ok(Json::obj(vec![
                ("type", Json::s("int32")),
                ("value", Json::i(0)),
            ])),
        ),
        ("one", wire(true, "4902", "")),
        (
            "one_rt",
            read_ok(Json::obj(vec![
                ("type", Json::s("int32")),
                ("value", Json::i(1)),
            ])),
        ),
        ("neg_one", wire(true, "4901", "")),
        (
            "neg_one_rt",
            read_ok(Json::obj(vec![
                ("type", Json::s("int32")),
                ("value", Json::i(-1)),
            ])),
        ),
        ("two_point_five", wire(true, "4e0000000000000440", "")),
        (
            "two_point_five_rt",
            read_ok(Json::obj(vec![
                ("type", Json::s("number")),
                ("value", Json::f(2.5)),
            ])),
        ),
        ("string_abc", wire(true, "2203616263", "")),
        (
            "string_abc_rt",
            read_ok(Json::obj(vec![
                ("type", Json::s("string")),
                ("value", Json::s("abc")),
            ])),
        ),
        // Wire version 16 headers (ff 10) are what this build's serializer
        // emits, and its own deserializer rejects them via the host-object
        // error path; header-less data deserializes as legacy version 0.
        ("true_hdr", wire(true, "ff1054", "")),
        ("true_hdr_rt", read_rejected.clone()),
        ("string_abc_hdr", wire(true, "ff102203616263", "")),
        ("string_abc_hdr_rt", read_rejected),
    ]);
    vec![expect_eq("buffers/ser_wire_primitives", expected, actual)]
}

fn roundtrip_tag(name: &str) -> &'static str {
    // Static names keep the Json::obj key type; map each known input.
    match name {
        "undefined" => "undefined_rt",
        "null" => "null_rt",
        "false" => "false_rt",
        "true" => "true_rt",
        "zero" => "zero_rt",
        "one" => "one_rt",
        "neg_one" => "neg_one_rt",
        "two_point_five" => "two_point_five_rt",
        "string_abc" => "string_abc_rt",
        "true_hdr" => "true_hdr_rt",
        "string_abc_hdr" => "string_abc_hdr_rt",
        _ => unreachable!(),
    }
}

/// Plain object serialization: pinned wire bytes plus property roundtrip.
fn ser_wire_object() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let wire = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let value = eval(tc, "({a: 1, b: \"x\"})").unwrap();
        // Header-less: this pinned V8 writes wire-format version 16 headers
        // (see the `*_hdr` primitive entries) that its own deserializer
        // rejects, so the canonical roundtrip demos stay header-less (the
        // deserializer accepts header-less data as legacy version 0).
        let outcome = serialize(tc, value);
        assert!(outcome.ok, "object must serialize");
        outcome.wire_hex
    };
    let roundtrip = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bytes = hex_to_bytes(&wire);
        deser_describe!(tc, &bytes, &["a", "b"])
    };

    let actual = Json::obj(vec![("wire", Json::s(&wire)), ("roundtrip", roundtrip)]);
    let expected = Json::obj(vec![
        ("wire", Json::s("6f22016149022201622201787b02")),
        (
            "roundtrip",
            Json::obj(vec![
                (
                    "read",
                    Json::obj(vec![
                        ("type", Json::s("object")),
                        (
                            "a",
                            Json::obj(vec![("type", Json::s("int32")), ("value", Json::i(1))]),
                        ),
                        (
                            "b",
                            Json::obj(vec![("type", Json::s("string")), ("value", Json::s("x"))]),
                        ),
                    ]),
                ),
                ("caught", Json::b(false)),
                ("message", Json::s("")),
            ]),
        ),
    ]);
    vec![expect_eq("buffers/ser_wire_object", expected, actual)]
}

/// Non-transferred ArrayBuffer serialization clones the contents into the
/// wire format; the roundtrip yields a fresh, equally-sized buffer.
fn ser_array_buffer_clone() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
        1, 2, 3, 4,
    ]));
    let ab = v8::ArrayBuffer::with_backing_store(scope, &bs);
    let ab_value: v8::Local<v8::Value> = ab.into();

    let (ok, wire, source_len_after) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let outcome = serialize(tc, ab_value);
        (outcome.ok, outcome.wire_hex, ab.byte_length())
    };
    let roundtrip = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bytes = hex_to_bytes(&wire);
        deser_describe!(tc, &bytes, &[])
    };

    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        (
            "source_byte_length_after_write",
            Json::i(source_len_after as i64),
        ),
        ("roundtrip", roundtrip),
    ]);
    let expected = Json::obj(vec![
        ("ok", Json::b(true)),
        ("wire", Json::s("420401020304")),
        ("source_byte_length_after_write", Json::i(4)),
        (
            "roundtrip",
            Json::obj(vec![
                (
                    "read",
                    Json::obj(vec![
                        ("type", Json::s("arraybuffer")),
                        ("byte_length", Json::i(4)),
                        ("contents", Json::s("01020304")),
                    ]),
                ),
                ("caught", Json::b(false)),
                ("message", Json::s("")),
            ]),
        ),
    ]);
    vec![expect_eq(
        "buffers/ser_array_buffer_clone",
        expected,
        actual,
    )]
}

/// Transferred ArrayBuffer serialization: registering the transfer detaches
/// the source buffer at write time; the receiving side must re-register the
/// same id or deserialization fails deterministically.
#[allow(clippy::too_many_lines)]
fn ser_transfer_semantics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
        9, 8, 7, 6,
    ]));
    let ab = v8::ArrayBuffer::with_backing_store(scope, &bs);
    let ab_value: v8::Local<v8::Value> = ab.into();

    let (ok, wire, source_len_after, source_was_detached) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let outcome = serialize_with(tc, ab_value, |serializer| {
            serializer.transfer_array_buffer(7, ab);
        });
        (
            outcome.ok,
            outcome.wire_hex,
            ab.byte_length(),
            ab.was_detached(),
        )
    };

    // Receiving side registers transfer id 7 against a fresh zeroed buffer.
    let with_transfer = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let target_bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
            0, 0, 0, 0,
        ]));
        let target = v8::ArrayBuffer::with_backing_store(tc, &target_bs);

        struct NoCustomReads;
        impl v8::ValueDeserializerImpl for NoCustomReads {}
        let wire_bytes = hex_to_bytes(&wire);
        let deserializer = v8::ValueDeserializer::new(tc, Box::new(NoCustomReads), &wire_bytes);
        deserializer.transfer_array_buffer(7, target);
        let context = tc.get_current_context();
        let value = deserializer.read_value(context);
        let described = value
            .map(|v| describe_value(tc, v, &[]))
            .unwrap_or(Json::Null);
        let caught = tc.has_caught();
        drop(deserializer);
        Json::obj(vec![
            ("read", described),
            ("caught", Json::b(caught)),
            ("target_byte_length", Json::i(target.byte_length() as i64)),
            (
                "target_contents",
                Json::s(&hex(&backing_store_bytes(&target_bs))),
            ),
        ])
    };

    // Without registering the id, deserialization must fail with a caught,
    // deterministic error message.
    let without_transfer = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bytes = hex_to_bytes(&wire);
        deser_describe!(tc, &bytes, &[])
    };

    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        (
            "source_byte_length_after_write",
            Json::i(source_len_after as i64),
        ),
        ("source_was_detached", Json::b(source_was_detached)),
        ("with_transfer", with_transfer),
        ("without_transfer", without_transfer),
    ]);
    let expected = Json::obj(vec![
        ("ok", Json::b(true)),
        ("wire", Json::s("7407")),
        // Pinned V8 no longer detaches the source at write time.
        ("source_byte_length_after_write", Json::i(4)),
        ("source_was_detached", Json::b(false)),
        (
            "with_transfer",
            Json::obj(vec![
                (
                    "read",
                    Json::obj(vec![
                        ("type", Json::s("arraybuffer")),
                        ("byte_length", Json::i(4)),
                        // Transfer reuses the receiving buffer's own store.
                        ("contents", Json::s("00000000")),
                    ]),
                ),
                ("caught", Json::b(false)),
                ("target_byte_length", Json::i(4)),
                ("target_contents", Json::s("00000000")),
            ]),
        ),
        (
            "without_transfer",
            Json::obj(vec![
                ("read", Json::Null),
                ("caught", Json::b(true)),
                (
                    "message",
                    Json::s("Uncaught Error: Unable to deserialize cloned data."),
                ),
            ]),
        ),
    ]);
    vec![expect_eq(
        "buffers/ser_transfer_semantics",
        expected,
        actual,
    )]
}

/// A JS function is unserializable: `write_value` fails, the delegate's
/// `throw_data_clone_error` receives a deterministic message, and the
/// re-thrown exception is observable through TryCatch.
fn ser_unserializable_function() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let (is_function, outcome, caught, message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let value = eval(tc, "() => 1").unwrap();
        let is_function = value.is_function();
        let outcome = serialize(tc, value);
        let caught = tc.has_caught();
        let message = tc
            .message()
            .map(|m| m.get(tc).to_rust_string_lossy(tc))
            .unwrap_or_default();
        (is_function, outcome, caught, message)
    };

    let actual = Json::obj(vec![
        ("is_function", Json::b(is_function)),
        ("write_ok", Json::b(outcome.ok)),
        ("wire", Json::s(&outcome.wire_hex)),
        ("clone_error", Json::s(&outcome.clone_error)),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
    ]);
    let expected = Json::obj(vec![
        ("is_function", Json::b(true)),
        ("write_ok", Json::b(false)),
        ("wire", Json::s("")),
        ("clone_error", Json::s("() => 1 could not be cloned.")),
        ("caught", Json::b(true)),
        (
            "message",
            Json::s("Uncaught Error: () => 1 could not be cloned."),
        ),
    ]);
    vec![expect_eq(
        "buffers/ser_unserializable_function",
        expected,
        actual,
    )]
}

/// Deterministic deserializer failures: empty input, truncated header, a
/// wrong header byte, and a truncated body each fail with caught errors.
fn ser_deserialize_invalid() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut entries: Vec<(&'static str, Json)> = Vec::new();
    for (name, bytes) in [
        ("empty", Vec::new()),
        ("truncated_header", vec![0xFF]),
        ("bad_header", vec![0x00, 0x00]),
        ("truncated_body", vec![0xFF, 0x0D, 0x42]),
    ] {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        entries.push((name, deser_describe!(tc, &bytes, &[])));
    }
    let actual = Json::obj(entries);
    let unable = "Uncaught Error: Unable to deserialize cloned data.";
    let host_object = "Uncaught Error: Deno deserializer: read_host_object not implemented";
    let rejected = |message: &str| {
        Json::obj(vec![
            ("read", Json::Null),
            ("caught", Json::b(true)),
            ("message", Json::s(message)),
        ])
    };
    let expected = Json::obj(vec![
        ("empty", rejected(unable)),
        ("truncated_header", rejected(host_object)),
        ("bad_header", rejected(unable)),
        ("truncated_body", rejected(host_object)),
    ]);
    vec![expect_eq(
        "buffers/ser_deserialize_invalid",
        expected,
        actual,
    )]
}

/// Serializing a detached ArrayBuffer fails deterministically.
fn ser_detached_source() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ab = v8::ArrayBuffer::new(scope, 4);
    let detached = ab.detach(None) == Some(true);
    let ab_value: v8::Local<v8::Value> = ab.into();

    let (ok, wire, clone_error) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let outcome = serialize(tc, ab_value);
        (outcome.ok, outcome.wire_hex, outcome.clone_error)
    };

    let actual = Json::obj(vec![
        ("detached", Json::b(detached)),
        ("write_ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("clone_error", Json::s(&clone_error)),
    ]);
    let expected = Json::obj(vec![
        ("detached", Json::b(true)),
        ("write_ok", Json::b(false)),
        ("wire", Json::s("")),
        (
            "clone_error",
            Json::s("An ArrayBuffer is detached and could not be cloned."),
        ),
    ]);
    vec![expect_eq("buffers/ser_detached_source", expected, actual)]
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

const CHECKS: &[fn() -> Vec<CheckOutcome>] = &[
    ab_new_basics,
    ab_backing_store_ownership,
    ab_backing_store_alias,
    ab_backing_store_shared_sab,
    sab_backing_store_owned_external,
    ab_resizable_backing_store,
    ab_detach_basic,
    ab_detach_key_gate,
    ab_detach_views_follow,
    ab_detach_js_transfer,
    view_typed_array_bounds,
    view_data_view_bounds,
    view_max_sizes,
    ext_backing_store_deleter,
    ser_wire_primitives,
    ser_wire_object,
    ser_array_buffer_clone,
    ser_transfer_semantics,
    ser_unserializable_function,
    ser_deserialize_invalid,
    ser_detached_source,
];

fn main() -> ExitCode {
    oracle::ensure_v8();
    let stdout = std::io::stdout();
    let mut out = stdout.lock();
    let mut total = 0usize;
    let mut passed = 0usize;
    for check in CHECKS {
        for outcome in check() {
            total += 1;
            if outcome.passed() {
                passed += 1;
            }
            let _ = writeln!(out, "{}", outcome.to_line());
            let _ = out.flush();
        }
    }
    let failed = total - passed;
    let _ = writeln!(out, "{}", summary_line(total, passed, failed));
    let _ = out.flush();
    if failed == 0 {
        ExitCode::SUCCESS
    } else {
        ExitCode::FAILURE
    }
}
