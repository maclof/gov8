//! Serializer/deserializer DELEGATE conformance slice for the pinned `v8`
//! crate (rusty_v8 =152.2.0, prebuilt static V8, x86_64-pc-windows-msvc).
//!
//! The base buffers slice (`src/bin/conformance-buffers.rs`) pins the plain
//! wire format with default delegates; THIS slice pins delegate completion:
//! every hook of `ValueSerializerImpl` / `ValueDeserializerImpl`, the
//! detection pipeline that decides when they run, ownership of the
//! serializer's growable output buffer, and the failure/panic boundaries.
//!
//! # Characterized surface (pinned-crate source citations)
//!
//! - `ValueSerializerImpl` defaults and exact default error strings:
//!   `v8-152.2.0/src/value_serializer.rs:276-336` (`throw_data_clone_error`
//!   is the only required method; `has_custom_host_object` defaults to
//!   `false`; `is_host_object` / `write_host_object` /
//!   `get_wasm_module_transfer_id` default to throwing
//!   `"Deno serializer: ... not implemented"` and returning `None`;
//!   `get_shared_array_buffer_id` defaults to a silent `None`).
//! - `ValueSerializerHelper` (direct buffer writes from inside
//!   `write_host_object`): `value_serializer.rs:387-472` (`write_header`,
//!   `write_value`, `write_uint32/64`, `write_double`, `write_raw_bytes`,
//!   `transfer_array_buffer`, `set_treat_array_buffer_views_as_host_objects`).
//! - Output-buffer ownership: the delegate glue implements V8's
//!   `ReallocateBufferMemory` / `FreeBufferMemory` with an `AtomicUsize`
//!   capacity (`value_serializer.rs:171-210`); `release()` takes ownership
//!   via `Vec::from_raw_parts` and resets the capacity
//!   (`value_serializer.rs:555-577`); the destructor frees an un-released
//!   buffer through `FreeBufferMemory`.
//! - `ValueDeserializerImpl` defaults (`value_deserializer.rs:188-231`;
//!   default error strings `"Deno deserializer: ... not implemented"`) and
//!   `ValueDeserializerHelper` (`value_deserializer.rs:282-386`:
//!   `read_header`, `read_value`, `read_uint32/64`, `read_double`,
//!   `read_raw_bytes`, `transfer_array_buffer`,
//!   `transfer_shared_array_buffer`, `get_wire_format_version`).
//! - The C++ deserializer keeps the caller's data pointer WITHOUT copying
//!   (`value_deserializer.rs:419-468`): input slices must outlive the
//!   deserializer value (same lifetime rule the buffers slice pins).
//!
//! # Upstream V8 dispatch semantics (designed against
//! `src/objects/value-serializer.cc` on v8 main, retrieved 2026-08; the
//! fixtures below pin the actual build's observable behavior, which matched
//! except where explicitly noted)
//!
//! - The serializer constructor consults `has_custom_host_object` ONCE per
//!   serializer and caches the answer. When true, `is_host_object` is
//!   consulted for every NEW plain JS object/error; an object written twice
//!   is short-circuited by the object-id map BEFORE detection. When false
//!   (the trait default), the fallback is `embedder-field count != 0`
//!   (instances of an `ObjectTemplate` with internal fields are host
//!   objects even with default hooks). The trait-default hook body cannot
//!   be instrumented, so the counters for it stay 0.
//! - `WriteHostObject` writes the `kHostObject` tag (`\` = 0x5c) BEFORE the
//!   delegate runs, and in release builds IGNORES the delegate's Just/Nothing
//!   result - only a pending exception aborts the write (so a delegate
//!   returning `Some(false)` without throwing still "succeeds").
//! - Helper `write_raw_bytes` maps to V8's `WriteRawBytes`: raw bytes with
//!   NO length prefix; framing is entirely the delegate's job.
//! - SAB write: `GetSharedArrayBufferId` completing as Nothing (no
//!   exception) is REJECTED by the pinned build - V8 throws its OWN
//!   `kDataCloneError` ("#<SharedArrayBuffer> could not be cloned.")
//!   directly, NOT through the delegate's `throw_data_clone_error`.
//!   (Upstream main would write the tag with the `Maybe` default; this
//!   build does not - the fixture pins the build.)
//! - Wasm modules: `GetWasmModuleTransferId` completing as Nothing (no
//!   exception) writes NOTHING (the module silently disappears from the
//!   wire; the enclosing write SUCCEEDS). The default Rust hook throws
//!   instead, which fails the write with the deterministic "not
//!   implemented" error. Note the asymmetry with the SAB path.
//! - Transfer maps are keyed by buffer on write (re-registering the same
//!   buffer replaces its id) and by id on read (last registration wins, so
//!   buffers written under one id alias to the final registered target).
//! - Read side: `kHostObject` / `kSharedArrayBuffer` / `kWasmModuleTransfer`
//!   consult ONLY the delegate hooks (`transfer_shared_array_buffer`
//!   registrations are never consulted for the SAB tag). A hook returning
//!   `None` WITHOUT a pending exception is NOT a clean failure on this
//!   build: V8 throws its own "Unable to deserialize cloned data." Error,
//!   which the TryCatch observes.
//! - `throw_data_clone_error` that only records (never throws): the write
//!   fails but NO exception is pending - the embedder decides whether the
//!   data-clone failure surfaces as a JS Error.
//!
//! Everything is normalized per `src/json.rs` rules: no addresses, no
//! timings, exact wire hex + exact V8 error strings for the pinned build.
//! Same JSON-lines protocol as the other slices (`{"check":..,"ok":..,
//! "value"|"expected"/"actual"}` + final summary), all ids prefixed
//! `serdel/`. This slice performs no platform shutdown, so its fixture can
//! be verified in-process and compared byte-for-byte by
//! `tests/conformance_serializer_delegates_fixture.rs`. Panic boundaries
//! (a Rust panic unwinding through the crate's `extern "C"` delegate
//! trampolines aborts the process) are characterized out-of-process by
//! `tests/serializer_delegates_negative.rs`, never here.
//!
//! Wasm scope note (per coordinator): Wasm itself is out of scope. The wasm
//! checks only prove that the transfer-delegate hooks fire and pin their
//! default/None behavior; no Wasm transfer is implemented.
//!
//! # Benchmark workload spec (for a future `benches/serializer.rs`)
//!
//! Methodology identical to the existing benches (`benches/common/mod.rs`):
//! 1 s warm-up, 3 s measurement, 50 samples, one full operation per
//! `criterion::black_box`-guarded iteration, fresh `HandleScope` inside the
//! iteration where needed, no V8 flags, default platform (thread_pool_size
//! 0, no idle-task support), release profile, pointer compression off.
//! Assert each workload once for correctness outside the timed loop.
//!
//! - `serdel/write_primitives_object`: build serializer + `write_value` of
//!   `({a:1,b:"x",c:[1.5,"two",true]})` + `release()` per iteration.
//! - `serdel/read_primitives_object`: `read_value` of the fixed header-less
//!   wire bytes of the workload above (precomputed once).
//! - `serdel/host_object_write`: serializer with
//!   `set_treat_array_buffer_views_as_host_objects(true)`; per iteration
//!   `write_value` of a fresh `Uint8Array(64)` whose delegate writes
//!   uint32 + 64 raw bytes + double (the check-4 codec).
//! - `serdel/host_object_read`: per iteration `read_value` of the fixed
//!   check-4 wire produced from a 64-byte payload.
//! - `serdel/sab_id_write`: per iteration serializer + SAB(64) + delegate
//!   returning `Some(42)` + write + release.
//! - `serdel/transfer_two_buffers_write`: per iteration
//!   `transfer_array_buffer` for two 4 KiB buffers under ids 1/2 + write of
//!   `{a,b}` + release.
//! - `serdel/release_growth_256kib`: per iteration write of the 256 KiB
//!   payload (check `serdel/realloc_growth_large_payload_hashed`) +
//!   release - the allocation-heavy path through `ReallocateBufferMemory`.
//!
//! Go comparisons must use the same warm-up/sample policy, inputs, wire
//! bytes, and V8 configuration (no flags, default platform), a release-mode
//! build, and a fresh environment capture under `bench-results/`.

use std::cell::Cell;
use std::cell::RefCell;
use std::rc::Rc;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};
use v8::ValueDeserializerHelper as _;
use v8::ValueSerializerHelper as _;

// ---------------------------------------------------------------------------
// Helpers (local to this binary; the crate's `checks::harness` is pub(crate)
// and shared registry files must not be modified to expose it).
// ---------------------------------------------------------------------------

/// Lowercase hex without separators: canonical encoding for wire bytes.
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

/// FNV-1a (64-bit) rendered as 16 lowercase hex chars: compact deterministic
/// digest for the large-payload ownership check.
fn fnv1a_hex(bytes: &[u8]) -> String {
    let mut hash: u64 = 0xcbf2_9ce4_8422_2325;
    for byte in bytes {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    hex(&hash.to_be_bytes())
}

/// Compiles and runs `source`, returning the completion value (`None` on
/// syntax error or runtime throw).
fn eval<'s>(scope: &mut v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let src = v8::String::new(scope, source)?;
    v8::Script::compile(scope, src, None)?.run(scope)
}

/// Delegate hook counters shared between a check and its delegate structs.
#[derive(Default, Clone)]
struct Counts {
    has_custom_host_object: usize,
    is_host_object: usize,
    write_host_object: usize,
    read_host_object: usize,
    get_shared_array_buffer_id: usize,
    get_shared_array_buffer_from_id: usize,
    get_wasm_module_transfer_id: usize,
    get_wasm_module_from_id: usize,
    throw_data_clone_error: usize,
}

type SharedCounts = Rc<RefCell<Counts>>;
type SharedUsize = Rc<RefCell<usize>>;
type SharedU32 = Rc<RefCell<u32>>;

fn counts_json(counts: &Counts) -> Json {
    Json::obj(vec![
        (
            "has_custom_host_object",
            Json::i(counts.has_custom_host_object as i64),
        ),
        ("is_host_object", Json::i(counts.is_host_object as i64)),
        (
            "write_host_object",
            Json::i(counts.write_host_object as i64),
        ),
        ("read_host_object", Json::i(counts.read_host_object as i64)),
        (
            "get_shared_array_buffer_id",
            Json::i(counts.get_shared_array_buffer_id as i64),
        ),
        (
            "get_shared_array_buffer_from_id",
            Json::i(counts.get_shared_array_buffer_from_id as i64),
        ),
        (
            "get_wasm_module_transfer_id",
            Json::i(counts.get_wasm_module_transfer_id as i64),
        ),
        (
            "get_wasm_module_from_id",
            Json::i(counts.get_wasm_module_from_id as i64),
        ),
        (
            "throw_data_clone_error",
            Json::i(counts.throw_data_clone_error as i64),
        ),
    ])
}

/// Minimum delegate required by the trait: `throw_data_clone_error` that
/// records the message and optionally re-throws it as a JS Error (the
/// canonical structured-clone behavior of the base buffers slice). All other
/// hooks keep their trait defaults.
struct SerBase {
    counts: SharedCounts,
    rethrow: bool,
    clone_error: Rc<RefCell<String>>,
}

impl SerBase {
    fn new(counts: &SharedCounts, rethrow: bool) -> (Self, Rc<RefCell<String>>) {
        let slot = Rc::new(RefCell::new(String::new()));
        (
            Self {
                counts: Rc::clone(counts),
                rethrow,
                clone_error: Rc::clone(&slot),
            },
            slot,
        )
    }
}

impl v8::ValueSerializerImpl for SerBase {
    fn throw_data_clone_error<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        message: v8::Local<'s, v8::String>,
    ) {
        self.counts.borrow_mut().throw_data_clone_error += 1;
        let text = message.to_rust_string_lossy(scope);
        *self.clone_error.borrow_mut() = text.clone();
        if self.rethrow {
            if let Some(str_handle) = v8::String::new(scope, &text) {
                let exc = v8::Exception::error(scope, str_handle);
                scope.throw_exception(exc);
            }
        }
    }
}

/// Type/shape description of a value, normalized for JSONL. Objects are
/// probed for the fixed key list ["kind", "n", "a", "b", "x"].
fn describe_value(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> Json {
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
        let text = value
            .to_string(scope)
            .map(|s| s.to_rust_string_lossy(scope))
            .unwrap_or_default();
        return Json::obj(vec![("type", Json::s("string")), ("value", Json::s(&text))]);
    }
    if value.is_shared_array_buffer() {
        if let Ok(sab) = value.try_cast::<v8::SharedArrayBuffer>() {
            let bs = sab.get_backing_store();
            let contents: Vec<u8> = bs.iter().map(Cell::get).collect();
            return Json::obj(vec![
                ("type", Json::s("sharedarraybuffer")),
                ("byte_length", Json::i(sab.byte_length() as i64)),
                ("contents", Json::s(&hex(&contents))),
            ]);
        }
    }
    if value.is_array_buffer() {
        if let Ok(ab) = value.try_cast::<v8::ArrayBuffer>() {
            let bs = ab.get_backing_store();
            let contents: Vec<u8> = bs.iter().map(Cell::get).collect();
            return Json::obj(vec![
                ("type", Json::s("arraybuffer")),
                ("byte_length", Json::i(ab.byte_length() as i64)),
                ("contents", Json::s(&hex(&contents))),
            ]);
        }
    }
    if value.is_object() {
        if let Ok(obj) = value.try_cast::<v8::Object>() {
            let mut fields = vec![("type", Json::s("object"))];
            for key in ["kind", "n", "a", "b", "x"] {
                let observed = v8::String::new(scope, key)
                    .and_then(|k| obj.get(scope, k.into()))
                    .map(|v| describe_value(scope, v))
                    .unwrap_or(Json::Null);
                fields.push((key, observed));
            }
            return Json::obj(fields);
        }
    }
    Json::obj(vec![("type", Json::s("other"))])
}

/// Reads a property of an object value and casts it to an `ArrayBuffer`
/// (identity-compared by the caller via `Local` equality).
fn prop_array_buffer<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    value: v8::Local<'s, v8::Value>,
    key: &str,
) -> Option<v8::Local<'s, v8::ArrayBuffer>> {
    let obj = value.try_cast::<v8::Object>().ok()?;
    let key = v8::String::new(scope, key)?;
    let prop = obj.get(scope, key.into())?;
    prop.try_cast::<v8::ArrayBuffer>().ok()
}

fn backing_store_bytes(bs: &v8::SharedRef<v8::BackingStore>) -> Vec<u8> {
    bs.iter().map(Cell::get).collect()
}

/// TryCatch message text ("" when nothing was caught). Must be a macro:
/// `has_caught`/`message` live on the `PinnedRef<TryCatch>` wrapper, not on
/// the `PinScope` it coerces to.
macro_rules! caught_message {
    ($tc:expr) => {{
        $tc.message()
            .map(|m| m.get($tc).to_rust_string_lossy($tc))
            .unwrap_or_default()
    }};
}

/// The minimal empty wasm module source: proves WebAssembly availability and
/// produces a `WasmModuleObject` for the transfer-hook checks only.
const WASM_EMPTY_MODULE: &str = "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))";

// ---------------------------------------------------------------------------
// Delegate implementations (one focused struct per hook-behavior variant).
// ---------------------------------------------------------------------------

/// Detection pipeline, variant A: claims custom host objects, denies them all
/// (`is_host_object -> Some(false)`); everything must take the native path.
struct DenyAllHosts {
    counts: SharedCounts,
}

impl v8::ValueSerializerImpl for DenyAllHosts {
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
    }

    fn has_custom_host_object(&self, _isolate: &v8::Isolate) -> bool {
        self.counts.borrow_mut().has_custom_host_object += 1;
        true
    }

    fn is_host_object<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _object: v8::Local<'s, v8::Object>,
    ) -> Option<bool> {
        self.counts.borrow_mut().is_host_object += 1;
        Some(false)
    }
}

/// Detection pipeline, variant B: claims every object as a host object and
/// writes a single varint byte from inside `write_host_object`.
struct AdmitAllHosts {
    counts: SharedCounts,
}

impl v8::ValueSerializerImpl for AdmitAllHosts {
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
    }

    fn has_custom_host_object(&self, _isolate: &v8::Isolate) -> bool {
        self.counts.borrow_mut().has_custom_host_object += 1;
        true
    }

    fn is_host_object<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _object: v8::Local<'s, v8::Object>,
    ) -> Option<bool> {
        self.counts.borrow_mut().is_host_object += 1;
        Some(true)
    }

    fn write_host_object<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _object: v8::Local<'s, v8::Object>,
        value_serializer: &dyn v8::ValueSerializerHelper,
    ) -> Option<bool> {
        self.counts.borrow_mut().write_host_object += 1;
        value_serializer.write_uint32(7);
        Some(true)
    }
}

/// Host-object codec, write side (used with the treat-views flag): writes
/// `uint32(42) | raw("host") | double(3.5)` and records whether the deferred
/// object was a typed array.
struct HostWriteCodec {
    counts: SharedCounts,
    saw_typed_array: Rc<RefCell<bool>>,
}

impl v8::ValueSerializerImpl for HostWriteCodec {
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
    }

    fn write_host_object<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        object: v8::Local<'s, v8::Object>,
        value_serializer: &dyn v8::ValueSerializerHelper,
    ) -> Option<bool> {
        self.counts.borrow_mut().write_host_object += 1;
        let as_value: v8::Local<v8::Value> = object.into();
        *self.saw_typed_array.borrow_mut() = as_value.is_typed_array();
        value_serializer.write_uint32(42);
        value_serializer.write_raw_bytes(b"host");
        value_serializer.write_double(3.5);
        Some(true)
    }
}

/// Host-object codec, read side: consumes exactly the bytes written by
/// [`HostWriteCodec`] via the helper read primitives and rebuilds a host
/// object `{kind: "host", n: 42}`.
struct HostReadCodec {
    counts: SharedCounts,
    read_u32: SharedUsize,
    read_raw: SharedUsize,
    read_f64: SharedUsize,
    wire_version: SharedU32,
}

impl HostReadCodec {
    fn new(counts: &SharedCounts) -> (Self, SharedUsize, SharedUsize, SharedUsize, SharedU32) {
        let read_u32 = Rc::new(RefCell::new(0usize));
        let read_raw = Rc::new(RefCell::new(0usize));
        let read_f64 = Rc::new(RefCell::new(0usize));
        let wire_version = Rc::new(RefCell::new(0u32));
        let delegate = Self {
            counts: Rc::clone(counts),
            read_u32: Rc::clone(&read_u32),
            read_raw: Rc::clone(&read_raw),
            read_f64: Rc::clone(&read_f64),
            wire_version: Rc::clone(&wire_version),
        };
        (delegate, read_u32, read_raw, read_f64, wire_version)
    }
}

impl v8::ValueDeserializerImpl for HostReadCodec {
    fn read_host_object<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        deserializer: &dyn v8::ValueDeserializerHelper,
    ) -> Option<v8::Local<'s, v8::Object>> {
        self.counts.borrow_mut().read_host_object += 1;
        *self.wire_version.borrow_mut() = deserializer.get_wire_format_version();

        let mut magic = 0u32;
        let got_u32 = deserializer.read_uint32(&mut magic);
        *self.read_u32.borrow_mut() += 1;

        let raw = deserializer.read_raw_bytes(4);
        *self.read_raw.borrow_mut() += 1;

        let mut d = 0f64;
        let got_f64 = deserializer.read_double(&mut d);
        *self.read_f64.borrow_mut() += 1;

        if !got_u32 || magic != 42 || !got_f64 || d != 3.5 {
            return None;
        }
        if !raw.is_some_and(|r| r == b"host") {
            return None;
        }

        let obj = v8::Object::new(scope);
        let kind_key = v8::String::new(scope, "kind")?;
        let kind_val = v8::String::new(scope, "host")?;
        let n_key = v8::String::new(scope, "n")?;
        let n_val = v8::Number::new(scope, 42.0);
        obj.set(scope, kind_key.into(), kind_val.into());
        obj.set(scope, n_key.into(), n_val.into());
        Some(obj)
    }
}

/// `write_host_object` returning `Some(false)` WITHOUT throwing and without
/// writing anything (pins the release-build "result ignored" semantics).
struct HostWriteDeny {
    counts: SharedCounts,
}

impl v8::ValueSerializerImpl for HostWriteDeny {
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
    }

    fn write_host_object<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _object: v8::Local<'s, v8::Object>,
        _value_serializer: &dyn v8::ValueSerializerHelper,
    ) -> Option<bool> {
        self.counts.borrow_mut().write_host_object += 1;
        Some(false)
    }
}

/// `write_host_object` that throws its OWN RangeError and returns `None`
/// (the delegate-drives-the-exception completion path).
struct HostWriteCustomThrow {
    counts: SharedCounts,
}

impl v8::ValueSerializerImpl for HostWriteCustomThrow {
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
    }

    fn write_host_object<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        _object: v8::Local<'s, v8::Object>,
        _value_serializer: &dyn v8::ValueSerializerHelper,
    ) -> Option<bool> {
        self.counts.borrow_mut().write_host_object += 1;
        let msg = v8::String::new(scope, "host serialization refused").unwrap();
        let exc = v8::Exception::range_error(scope, msg);
        scope.throw_exception(exc);
        None
    }
}

/// SAB id hook returning a fixed id (write roundtrip path).
struct SabIdCustom;

impl v8::ValueSerializerImpl for SabIdCustom {
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
    }

    fn get_shared_array_buffer_id<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _shared_array_buffer: v8::Local<'s, v8::SharedArrayBuffer>,
    ) -> Option<u32> {
        Some(42)
    }
}

/// Wasm transfer-id hook completing with `None` WITHOUT throwing (the
/// "module silently dropped from the wire" path).
struct WasmIdNone {
    counts: SharedCounts,
}

impl v8::ValueSerializerImpl for WasmIdNone {
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
    }

    fn get_wasm_module_transfer_id(
        &self,
        _scope: &mut v8::PinScope<'_, '_>,
        _module: v8::Local<v8::WasmModuleObject>,
    ) -> Option<u32> {
        self.counts.borrow_mut().get_wasm_module_transfer_id += 1;
        None
    }
}

/// SAB-from-id hook returning a fresh SAB for id 42 (read roundtrip path).
struct SabFromIdRoundtrip {
    counts: SharedCounts,
    observed_id: Rc<RefCell<u32>>,
}

impl v8::ValueDeserializerImpl for SabFromIdRoundtrip {
    fn get_shared_array_buffer_from_id<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        transfer_id: u32,
    ) -> Option<v8::Local<'s, v8::SharedArrayBuffer>> {
        self.counts.borrow_mut().get_shared_array_buffer_from_id += 1;
        *self.observed_id.borrow_mut() = transfer_id;
        if transfer_id != 42 {
            return None;
        }
        let bs = v8::SharedRef::from(v8::SharedArrayBuffer::new_backing_store(scope, 4));
        bs[0].set(5);
        bs[1].set(6);
        bs[2].set(7);
        bs[3].set(8);
        Some(v8::SharedArrayBuffer::with_backing_store(scope, &bs))
    }
}

/// SAB-from-id hook completing with `None` WITHOUT throwing (clean read
/// failure path; also used to prove registrations are not consulted).
struct SabFromIdNone {
    counts: SharedCounts,
}

impl v8::ValueDeserializerImpl for SabFromIdNone {
    fn get_shared_array_buffer_from_id<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _transfer_id: u32,
    ) -> Option<v8::Local<'s, v8::SharedArrayBuffer>> {
        self.counts.borrow_mut().get_shared_array_buffer_from_id += 1;
        None
    }
}

/// Default deserializer delegate: every hook keeps its trait default, so a
/// host-object / SAB-id / wasm-id payload fails with the deterministic
/// "not implemented" Error (which one is visible from the message).
struct DeserDefaults;

impl v8::ValueDeserializerImpl for DeserDefaults {}

// ---------------------------------------------------------------------------
// Checks. Order is part of the observable contract (the fixture is ordered).
// ---------------------------------------------------------------------------

/// Detection pipeline with `has_custom_host_object -> true` and
/// `is_host_object -> Some(false)`: the constructor consults
/// `has_custom_host_object` exactly once; every NEW plain object consults
/// `is_host_object`; an object written twice is short-circuited by the
/// object-id map BEFORE detection (a `^` reference, no second consult).
fn detection_denies_all_hosts() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let a = eval(tc, "({marker: 1})").unwrap();
    let b = eval(tc, "({other: 2})").unwrap();

    let serializer = v8::ValueSerializer::new(
        tc,
        Box::new(DenyAllHosts {
            counts: Rc::clone(&counts),
        }),
    );
    let ctx = tc.get_current_context();
    let ok1 = serializer.write_value(ctx, a) == Some(true);
    let ok1_again = serializer.write_value(ctx, a) == Some(true);
    let ok2 = serializer.write_value(ctx, b) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("ok1", Json::b(ok1)),
        ("ok1_again", Json::b(ok1_again)),
        ("ok2", Json::b(ok2)),
        ("wire", Json::s(&wire)),
        ("counts", counts_json(&actual_counts)),
    ]);
    let expected = Json::obj(vec![
        ("ok1", Json::b(true)),
        ("ok1_again", Json::b(true)),
        ("ok2", Json::b(true)),
        // o "marker" I(1) { 1 | ^ id0 | o "other" I(2) { 1.
        (
            "wire",
            Json::s("6f22066d61726b657249027b015e006f22056f7468657249047b01"),
        ),
        (
            "counts",
            Json::obj(vec![
                ("has_custom_host_object", Json::i(1)),
                ("is_host_object", Json::i(2)),
                ("write_host_object", Json::i(0)),
                ("read_host_object", Json::i(0)),
                ("get_shared_array_buffer_id", Json::i(0)),
                ("get_shared_array_buffer_from_id", Json::i(0)),
                ("get_wasm_module_transfer_id", Json::i(0)),
                ("get_wasm_module_from_id", Json::i(0)),
                ("throw_data_clone_error", Json::i(0)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "serdel/detection_denies_all_hosts",
        expected,
        actual,
    )]
}

/// Without custom host objects, detection falls back to embedder fields:
/// an instance of an `ObjectTemplate` with internal fields routes to the
/// DEFAULT `write_host_object` (deterministic error), while a plain `{}`
/// stays on the native path with zero detection calls beyond the
/// constructor's cached `has_custom_host_object`.
fn detection_embedder_fields_without_custom() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();

    // Built in the outer scope; valid inside the nested TryCatch scopes.
    let templ = v8::ObjectTemplate::new(scope);
    templ.set_internal_field_count(2);
    let with_fields: v8::Local<v8::Value> = templ.new_instance(scope).unwrap().into();
    let plain = eval(scope, "({})").unwrap();

    let (embedder_ok, embedder_wire, embedder_caught_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let (delegate, _clone_error) = SerBase::new(&counts, true);
        let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
        let ctx = tc.get_current_context();
        let ok = serializer.write_value(ctx, with_fields) == Some(true);
        let wire = hex(&serializer.release());
        drop(serializer);
        (ok, wire, caught_message!(tc))
    };

    let (plain_ok, plain_wire) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let (delegate, _clone_error) = SerBase::new(&counts, true);
        let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
        let ctx = tc.get_current_context();
        let ok = serializer.write_value(ctx, plain) == Some(true);
        let wire = hex(&serializer.release());
        drop(serializer);
        (ok, wire)
    };

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("embedder_ok", Json::b(embedder_ok)),
        ("embedder_wire", Json::s(&embedder_wire)),
        ("embedder_caught_message", Json::s(&embedder_caught_message)),
        ("plain_ok", Json::b(plain_ok)),
        ("plain_wire", Json::s(&plain_wire)),
        ("counts", counts_json(&actual_counts)),
    ]);
    let expected = Json::obj(vec![
        ("embedder_ok", Json::b(false)),
        // The kHostObject tag is written before the default hook fails.
        ("embedder_wire", Json::s("5c")),
        (
            "embedder_caught_message",
            Json::s("Uncaught Error: Deno serializer: write_host_object not implemented"),
        ),
        ("plain_ok", Json::b(true)),
        ("plain_wire", Json::s("6f7b00")),
        // The default has_custom_host_object returns false WITHOUT any way
        // to observe it (trait default body - not instrumentable); the
        // observed routing (embedder fields -> host object, plain object ->
        // native path) is the evidence that false was cached.
        (
            "counts",
            Json::obj(vec![
                ("has_custom_host_object", Json::i(0)),
                ("is_host_object", Json::i(0)),
                ("write_host_object", Json::i(0)),
                ("read_host_object", Json::i(0)),
                ("get_shared_array_buffer_id", Json::i(0)),
                ("get_shared_array_buffer_from_id", Json::i(0)),
                ("get_wasm_module_transfer_id", Json::i(0)),
                ("get_wasm_module_from_id", Json::i(0)),
                ("throw_data_clone_error", Json::i(0)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "serdel/detection_embedder_fields_without_custom",
        expected,
        actual,
    )]
}

/// `is_host_object -> Some(true)` routes a plain object to
/// `write_host_object`, whose delegate bytes follow the 0x5c tag.
fn detection_admits_host_routes_to_write() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let plain = eval(tc, "({})").unwrap();
    let serializer = v8::ValueSerializer::new(
        tc,
        Box::new(AdmitAllHosts {
            counts: Rc::clone(&counts),
        }),
    );
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, plain) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("counts", counts_json(&actual_counts)),
    ]);
    let expected = Json::obj(vec![
        ("ok", Json::b(true)),
        // kHostObject tag + the delegate's write_uint32(7) varint.
        ("wire", Json::s("5c07")),
        (
            "counts",
            Json::obj(vec![
                ("has_custom_host_object", Json::i(1)),
                ("is_host_object", Json::i(1)),
                ("write_host_object", Json::i(1)),
                ("read_host_object", Json::i(0)),
                ("get_shared_array_buffer_id", Json::i(0)),
                ("get_shared_array_buffer_from_id", Json::i(0)),
                ("get_wasm_module_transfer_id", Json::i(0)),
                ("get_wasm_module_from_id", Json::i(0)),
                ("throw_data_clone_error", Json::i(0)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "serdel/detection_admits_host_routes_to_write",
        expected,
        actual,
    )]
}

/// Full host-object write/read roundtrip through the treat-views flag and
/// the helper traits, pinning the exact delegate-controlled wire bytes and
/// the helper read order.
fn host_write_read_roundtrip() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let saw_typed_array = Rc::new(RefCell::new(false));

    let wire = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
            1, 2, 3, 4,
        ]));
        let ab = v8::ArrayBuffer::with_backing_store(tc, &bs);
        let ta = v8::Uint8Array::new(tc, ab, 0, 4).unwrap();
        let value: v8::Local<v8::Value> = ta.into();

        let serializer = v8::ValueSerializer::new(
            tc,
            Box::new(HostWriteCodec {
                counts: Rc::clone(&counts),
                saw_typed_array: Rc::clone(&saw_typed_array),
            }),
        );
        serializer.set_treat_array_buffer_views_as_host_objects(true);
        let ctx = tc.get_current_context();
        let ok = serializer.write_value(ctx, value) == Some(true);
        assert!(ok, "host write must succeed");
        let w = serializer.release();
        drop(serializer);
        hex(&w)
    };

    let (read, read_caught, u32_calls, raw_calls, f64_calls, wire_version) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bytes = hex_to_bytes(&wire);
        let (delegate, read_u32, read_raw, read_f64, wire_version) = HostReadCodec::new(&counts);
        let deserializer = v8::ValueDeserializer::new(tc, Box::new(delegate), &bytes);
        let ctx = tc.get_current_context();
        let value = deserializer.read_value(ctx);
        drop(deserializer);
        let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
        let u32_calls = *read_u32.borrow();
        let raw_calls = *read_raw.borrow();
        let f64_calls = *read_f64.borrow();
        let version = *wire_version.borrow();
        (
            described,
            tc.has_caught(),
            u32_calls,
            raw_calls,
            f64_calls,
            version,
        )
    };

    let actual_counts = counts.borrow().clone();
    let undefined = Json::obj(vec![("type", Json::s("undefined"))]);
    let actual = Json::obj(vec![
        ("wire", Json::s(&wire)),
        ("saw_typed_array", Json::b(*saw_typed_array.borrow())),
        ("read", read),
        ("read_caught", Json::b(read_caught)),
        ("read_u32_calls", Json::i(u32_calls as i64)),
        ("read_raw_calls", Json::i(raw_calls as i64)),
        ("read_f64_calls", Json::i(f64_calls as i64)),
        ("wire_version", Json::i(i64::from(wire_version))),
        ("counts", counts_json(&actual_counts)),
    ]);
    let expected = Json::obj(vec![
        // 5c tag + varint(42) + raw "host" (NO length prefix - the helper's
        // write_raw_bytes maps to V8's WriteRawBytes, which only appends the
        // bytes; framing is the delegate's job) + LE double 3.5.
        ("wire", Json::s("5c2a686f73740000000000000c40")),
        ("saw_typed_array", Json::b(true)),
        (
            "read",
            Json::obj(vec![
                ("type", Json::s("object")),
                (
                    "kind",
                    Json::obj(vec![
                        ("type", Json::s("string")),
                        ("value", Json::s("host")),
                    ]),
                ),
                (
                    "n",
                    Json::obj(vec![("type", Json::s("int32")), ("value", Json::i(42))]),
                ),
                ("a", undefined.clone()),
                ("b", undefined.clone()),
                ("x", undefined),
            ]),
        ),
        ("read_caught", Json::b(false)),
        ("read_u32_calls", Json::i(1)),
        ("read_raw_calls", Json::i(1)),
        ("read_f64_calls", Json::i(1)),
        // Header-less data reports legacy wire format version 0.
        ("wire_version", Json::i(0)),
        (
            "counts",
            Json::obj(vec![
                ("has_custom_host_object", Json::i(0)),
                ("is_host_object", Json::i(0)),
                ("write_host_object", Json::i(1)),
                ("read_host_object", Json::i(1)),
                ("get_shared_array_buffer_id", Json::i(0)),
                ("get_shared_array_buffer_from_id", Json::i(0)),
                ("get_wasm_module_transfer_id", Json::i(0)),
                ("get_wasm_module_from_id", Json::i(0)),
                ("throw_data_clone_error", Json::i(0)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "serdel/host_write_read_roundtrip",
        expected,
        actual,
    )]
}

/// Default `write_host_object` under the treat-views flag: deterministic
/// "not implemented" Error, failed write, and the tag byte already on the
/// buffer (the partial wire is NOT rolled back).
fn host_default_write_error_partial_wire() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let ab = v8::ArrayBuffer::new(tc, 4);
    let ta = v8::Uint8Array::new(tc, ab, 0, 4).unwrap();
    let value: v8::Local<v8::Value> = ta.into();

    let (delegate, clone_error) = SerBase::new(&counts, true);
    let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
    serializer.set_treat_array_buffer_views_as_host_objects(true);
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, value) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);
    let clone_error_text = clone_error.borrow().clone();
    let caught = tc.has_caught();
    let trycatch_message = caught_message!(tc);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("clone_error_called_with", Json::s(&clone_error_text)),
        ("caught", Json::b(caught)),
        ("caught_message", Json::s(&trycatch_message)),
        ("counts", counts_json(&actual_counts)),
    ]);
    let expected = Json::obj(vec![
        ("ok", Json::b(false)),
        ("wire", Json::s("5c")),
        // The exception came from the Rust hook itself; V8 did NOT route it
        // through throw_data_clone_error.
        ("clone_error_called_with", Json::s("")),
        ("caught", Json::b(true)),
        (
            "caught_message",
            Json::s("Uncaught Error: Deno serializer: write_host_object not implemented"),
        ),
        // The default has_custom_host_object is the trait default body: it
        // runs (and its false result drives the native path for plain
        // objects) but cannot be counted.
        (
            "counts",
            Json::obj(vec![
                ("has_custom_host_object", Json::i(0)),
                ("is_host_object", Json::i(0)),
                ("write_host_object", Json::i(0)),
                ("read_host_object", Json::i(0)),
                ("get_shared_array_buffer_id", Json::i(0)),
                ("get_shared_array_buffer_from_id", Json::i(0)),
                ("get_wasm_module_transfer_id", Json::i(0)),
                ("get_wasm_module_from_id", Json::i(0)),
                ("throw_data_clone_error", Json::i(0)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "serdel/host_default_write_error_partial_wire",
        expected,
        actual,
    )]
}

/// Default `read_host_object`: deterministic "not implemented" Error on a
/// kHostObject-tagged payload.
fn host_read_default_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = hex_to_bytes("5c2a04686f73740000000000000c40");
    let deserializer = v8::ValueDeserializer::new(tc, Box::new(DeserDefaults), &bytes);
    let ctx = tc.get_current_context();
    let value = deserializer.read_value(ctx);
    drop(deserializer);
    let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
    let caught = tc.has_caught();
    let message = caught_message!(tc);

    let actual = Json::obj(vec![
        ("read", described),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
    ]);
    let expected = Json::obj(vec![
        ("read", Json::Null),
        ("caught", Json::b(true)),
        (
            "message",
            Json::s("Uncaught Error: Deno deserializer: read_host_object not implemented"),
        ),
    ]);
    vec![expect_eq(
        "serdel/host_read_default_error",
        expected,
        actual,
    )]
}

/// `read_host_object` completing with `None` WITHOUT throwing: the pinned
/// build still surfaces a deterministic ENGINE error ("Unable to
/// deserialize cloned data.") - a hook's silent `None` is never a clean
/// `read_value` success, and the TryCatch is NOT left empty here.
fn host_read_none_throws_engine_error() -> Vec<CheckOutcome> {
    struct ReadNone;
    impl v8::ValueDeserializerImpl for ReadNone {
        fn read_host_object<'s>(
            &self,
            _scope: &mut v8::PinScope<'s, '_>,
            _deserializer: &dyn v8::ValueDeserializerHelper,
        ) -> Option<v8::Local<'s, v8::Object>> {
            None
        }
    }

    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = hex_to_bytes("5c2a04686f73740000000000000c40");
    let deserializer = v8::ValueDeserializer::new(tc, Box::new(ReadNone), &bytes);
    let ctx = tc.get_current_context();
    let value = deserializer.read_value(ctx);
    drop(deserializer);
    let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
    let caught = tc.has_caught();
    let message = caught_message!(tc);

    let actual = Json::obj(vec![
        ("read", described),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
    ]);
    let expected = Json::obj(vec![
        ("read", Json::Null),
        ("caught", Json::b(true)),
        (
            "message",
            Json::s("Uncaught Error: Unable to deserialize cloned data."),
        ),
    ]);
    vec![expect_eq(
        "serdel/host_read_none_throws_engine_error",
        expected,
        actual,
    )]
}

/// `write_host_object -> Some(false)` WITHOUT throwing: the release build
/// ignores the delegate's bool result once no exception is pending, so the
/// write "succeeds" with just the kHostObject tag on the wire.
fn write_host_object_false_result_ignored() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let ab = v8::ArrayBuffer::new(tc, 4);
    let ta = v8::Uint8Array::new(tc, ab, 0, 4).unwrap();
    let value: v8::Local<v8::Value> = ta.into();

    let serializer = v8::ValueSerializer::new(
        tc,
        Box::new(HostWriteDeny {
            counts: Rc::clone(&counts),
        }),
    );
    serializer.set_treat_array_buffer_views_as_host_objects(true);
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, value) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);
    let caught = tc.has_caught();

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("caught", Json::b(caught)),
        ("counts", counts_json(&actual_counts)),
    ]);
    let expected = Json::obj(vec![
        ("ok", Json::b(true)),
        ("wire", Json::s("5c")),
        ("caught", Json::b(false)),
        (
            "counts",
            Json::obj(vec![
                ("has_custom_host_object", Json::i(0)),
                ("is_host_object", Json::i(0)),
                ("write_host_object", Json::i(1)),
                ("read_host_object", Json::i(0)),
                ("get_shared_array_buffer_id", Json::i(0)),
                ("get_shared_array_buffer_from_id", Json::i(0)),
                ("get_wasm_module_transfer_id", Json::i(0)),
                ("get_wasm_module_from_id", Json::i(0)),
                ("throw_data_clone_error", Json::i(0)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "serdel/write_host_object_false_result_ignored",
        expected,
        actual,
    )]
}

/// `throw_data_clone_error` that only RECORDS (never throws): the write
/// fails but no exception is pending - the "rethrow=false" completion.
fn clone_error_delegate_without_rethrow() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let value = eval(tc, "() => 1").unwrap();
    let (delegate, clone_error) = SerBase::new(&counts, false);
    let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, value) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);
    let clone_error_text = clone_error.borrow().clone();
    let caught = tc.has_caught();
    let trycatch_message = caught_message!(tc);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("clone_error_called_with", Json::s(&clone_error_text)),
        ("caught", Json::b(caught)),
        ("caught_message", Json::s(&trycatch_message)),
        (
            "throw_calls",
            Json::i(actual_counts.throw_data_clone_error as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        ("ok", Json::b(false)),
        ("wire", Json::s("")),
        (
            "clone_error_called_with",
            Json::s("() => 1 could not be cloned."),
        ),
        ("caught", Json::b(false)),
        ("caught_message", Json::s("")),
        ("throw_calls", Json::i(1)),
    ]);
    vec![expect_eq(
        "serdel/clone_error_delegate_without_rethrow",
        expected,
        actual,
    )]
}

/// A `write_host_object` that throws its OWN exception: the custom error
/// propagates to the TryCatch verbatim; `throw_data_clone_error` is NOT
/// involved; the tag byte stays on the partial wire.
fn clone_error_with_custom_host_exception() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let ab = v8::ArrayBuffer::new(tc, 4);
    let ta = v8::Uint8Array::new(tc, ab, 0, 4).unwrap();
    let value: v8::Local<v8::Value> = ta.into();

    let (delegate, clone_error) = SerBase::new(&counts, true);
    let _ = delegate;
    let serializer = v8::ValueSerializer::new(
        tc,
        Box::new(HostWriteCustomThrow {
            counts: Rc::clone(&counts),
        }),
    );
    serializer.set_treat_array_buffer_views_as_host_objects(true);
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, value) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);
    let clone_error_text = clone_error.borrow().clone();
    let caught = tc.has_caught();
    let trycatch_message = caught_message!(tc);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("clone_error_called_with", Json::s(&clone_error_text)),
        ("caught", Json::b(caught)),
        ("caught_message", Json::s(&trycatch_message)),
        ("counts", counts_json(&actual_counts)),
    ]);
    let expected = Json::obj(vec![
        ("ok", Json::b(false)),
        ("wire", Json::s("5c")),
        ("clone_error_called_with", Json::s("")),
        ("caught", Json::b(true)),
        (
            "caught_message",
            Json::s("Uncaught RangeError: host serialization refused"),
        ),
        (
            "counts",
            Json::obj(vec![
                ("has_custom_host_object", Json::i(0)),
                ("is_host_object", Json::i(0)),
                ("write_host_object", Json::i(1)),
                ("read_host_object", Json::i(0)),
                ("get_shared_array_buffer_id", Json::i(0)),
                ("get_shared_array_buffer_from_id", Json::i(0)),
                ("get_wasm_module_transfer_id", Json::i(0)),
                ("get_wasm_module_from_id", Json::i(0)),
                ("throw_data_clone_error", Json::i(0)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "serdel/clone_error_with_custom_host_exception",
        expected,
        actual,
    )]
}

/// SAB serialization through a custom `get_shared_array_buffer_id`: the
/// id lands on the wire as `u` + varint; the source SAB is untouched.
fn sab_write_custom_id() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let bs = v8::SharedRef::from(v8::SharedArrayBuffer::new_backing_store(tc, 8));
    for (i, v) in [1u8, 2, 3, 4, 5, 6, 7, 8].iter().enumerate() {
        bs[i].set(*v);
    }
    let sab = v8::SharedArrayBuffer::with_backing_store(tc, &bs);
    let value: v8::Local<v8::Value> = sab.into();

    let serializer = v8::ValueSerializer::new(tc, Box::new(SabIdCustom));
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, value) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);

    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("source_byte_length", Json::i(sab.byte_length() as i64)),
        ("source_contents", Json::s(&hex(&backing_store_bytes(&bs)))),
    ]);
    let expected = Json::obj(vec![
        ("ok", Json::b(true)),
        // kSharedArrayBuffer 'u' + varint(42).
        ("wire", Json::s("752a")),
        ("source_byte_length", Json::i(8)),
        ("source_contents", Json::s("0102030405060708")),
    ]);
    vec![expect_eq("serdel/sab_write_custom_id", expected, actual)]
}

/// Default `get_shared_array_buffer_id` (`None`, no exception): the pinned
/// build REJECTS the write - V8 throws its own kDataCloneError directly
/// (not via the delegate hook) and no tag reaches the wire.
fn sab_write_default_none_is_rejected() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let sab = v8::SharedArrayBuffer::with_backing_store(
        tc,
        &v8::SharedRef::from(v8::SharedArrayBuffer::new_backing_store(tc, 8)),
    );
    let value: v8::Local<v8::Value> = sab.into();

    let (delegate, _clone_error) = SerBase::new(&counts, true);
    let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, value) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);
    let caught = tc.has_caught();
    let trycatch_message = caught_message!(tc);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("caught", Json::b(caught)),
        ("caught_message", Json::s(&trycatch_message)),
        ("counts", counts_json(&actual_counts)),
    ]);
    let expected = Json::obj(vec![
        // The pinned build REJECTS a Nothing-completion of
        // get_shared_array_buffer_id: V8 throws its OWN kDataCloneError
        // (interpolating the SAB) DIRECTLY - not routed through the
        // delegate's throw_data_clone_error (count stays 0).
        ("ok", Json::b(false)),
        ("wire", Json::s("")),
        ("caught", Json::b(true)),
        (
            "caught_message",
            Json::s("Uncaught Error: #<SharedArrayBuffer> could not be cloned."),
        ),
        // The default has_custom_host_object is the trait default body and
        // cannot be counted; get_shared_array_buffer_id's default (None)
        // is likewise silent.
        (
            "counts",
            Json::obj(vec![
                ("has_custom_host_object", Json::i(0)),
                ("is_host_object", Json::i(0)),
                ("write_host_object", Json::i(0)),
                ("read_host_object", Json::i(0)),
                ("get_shared_array_buffer_id", Json::i(0)),
                ("get_shared_array_buffer_from_id", Json::i(0)),
                ("get_wasm_module_transfer_id", Json::i(0)),
                ("get_wasm_module_from_id", Json::i(0)),
                ("throw_data_clone_error", Json::i(0)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "serdel/sab_write_default_none_is_rejected",
        expected,
        actual,
    )]
}

/// SAB read roundtrip: `get_shared_array_buffer_from_id` supplies the SAB
/// registered under the wire id; the returned value IS a shared buffer.
fn sab_read_roundtrip() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let observed_id = Rc::new(RefCell::new(0u32));

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = hex_to_bytes("752a");
    let deserializer = v8::ValueDeserializer::new(
        tc,
        Box::new(SabFromIdRoundtrip {
            counts: Rc::clone(&counts),
            observed_id: Rc::clone(&observed_id),
        }),
        &bytes,
    );
    let ctx = tc.get_current_context();
    let value = deserializer.read_value(ctx);
    drop(deserializer);
    let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
    let caught = tc.has_caught();
    let message = caught_message!(tc);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("read", described),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
        ("observed_id", Json::i(i64::from(*observed_id.borrow()))),
        (
            "sab_hook_calls",
            Json::i(actual_counts.get_shared_array_buffer_from_id as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "read",
            Json::obj(vec![
                ("type", Json::s("sharedarraybuffer")),
                ("byte_length", Json::i(4)),
                ("contents", Json::s("05060708")),
            ]),
        ),
        ("caught", Json::b(false)),
        ("message", Json::s("")),
        ("observed_id", Json::i(42)),
        ("sab_hook_calls", Json::i(1)),
    ]);
    vec![expect_eq("serdel/sab_read_roundtrip", expected, actual)]
}

/// Default `get_shared_array_buffer_from_id`: deterministic "not
/// implemented" Error on a kSharedArrayBuffer payload.
fn sab_read_default_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = hex_to_bytes("752a");
    let deserializer = v8::ValueDeserializer::new(tc, Box::new(DeserDefaults), &bytes);
    let ctx = tc.get_current_context();
    let value = deserializer.read_value(ctx);
    drop(deserializer);
    let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
    let caught = tc.has_caught();
    let message = caught_message!(tc);

    let actual = Json::obj(vec![
        ("read", described),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
    ]);
    let expected = Json::obj(vec![
        ("read", Json::Null),
        ("caught", Json::b(true)),
        (
            "message",
            Json::s(
                "Uncaught Error: Deno deserializer: \
                 get_shared_array_buffer_from_id not implemented",
            ),
        ),
    ]);
    vec![expect_eq("serdel/sab_read_default_error", expected, actual)]
}

/// `get_shared_array_buffer_from_id -> None` without throwing: the pinned
/// build still surfaces the deterministic engine error "Unable to
/// deserialize cloned data." (a silent None never yields a clean read).
fn sab_read_none_throws_engine_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = hex_to_bytes("752a");
    let deserializer = v8::ValueDeserializer::new(
        tc,
        Box::new(SabFromIdNone {
            counts: Rc::clone(&counts),
        }),
        &bytes,
    );
    let ctx = tc.get_current_context();
    let value = deserializer.read_value(ctx);
    drop(deserializer);
    let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
    let caught = tc.has_caught();
    let message = caught_message!(tc);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("read", described),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
        (
            "sab_hook_calls",
            Json::i(actual_counts.get_shared_array_buffer_from_id as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        ("read", Json::Null),
        ("caught", Json::b(true)),
        (
            "message",
            Json::s("Uncaught Error: Unable to deserialize cloned data."),
        ),
        ("sab_hook_calls", Json::i(1)),
    ]);
    vec![expect_eq(
        "serdel/sab_read_none_throws_engine_error",
        expected,
        actual,
    )]
}

/// `transfer_shared_array_buffer` registrations are NOT consulted by the
/// SAB read path: the delegate hook is called even for a registered id, and
/// its `None` surfaces the same deterministic engine error.
fn sab_read_transfer_registration_not_consulted() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();

    let bs = v8::SharedRef::from(v8::SharedArrayBuffer::new_backing_store(scope, 4));
    bs[0].set(9);
    let registered = v8::SharedArrayBuffer::with_backing_store(scope, &bs);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = hex_to_bytes("752a");
    let deserializer = v8::ValueDeserializer::new(
        tc,
        Box::new(SabFromIdNone {
            counts: Rc::clone(&counts),
        }),
        &bytes,
    );
    deserializer.transfer_shared_array_buffer(42, registered);
    let ctx = tc.get_current_context();
    let value = deserializer.read_value(ctx);
    drop(deserializer);
    let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
    let caught = tc.has_caught();
    let message = caught_message!(tc);

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("read", described),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
        (
            "sab_hook_calls",
            Json::i(actual_counts.get_shared_array_buffer_from_id as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        ("read", Json::Null),
        ("caught", Json::b(true)),
        (
            "message",
            Json::s("Uncaught Error: Unable to deserialize cloned data."),
        ),
        ("sab_hook_calls", Json::i(1)),
    ]);
    vec![expect_eq(
        "serdel/sab_read_transfer_registration_not_consulted",
        expected,
        actual,
    )]
}

/// Serializing a WasmModuleObject with the DEFAULT delegate: the default
/// `get_wasm_module_transfer_id` throws its deterministic "not implemented"
/// error, which the write surfaces as a failure.
fn wasm_write_default_delegate_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let Some(module) = eval(tc, WASM_EMPTY_MODULE) else {
        // WebAssembly unavailable in this build: pin the availability fact
        // instead of the write behavior.
        let actual = Json::obj(vec![("wasm_available", Json::b(false))]);
        let expected = Json::obj(vec![("wasm_available", Json::b(false))]);
        return vec![expect_eq(
            "serdel/wasm_write_default_delegate_error",
            expected,
            actual,
        )];
    };

    // Default delegate: the wasm hook keeps its trait default, which throws
    // the deterministic "not implemented" error.
    let counts: SharedCounts = Rc::default();
    let (delegate, _clone_error) = SerBase::new(&counts, true);
    let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, module) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);
    let caught = tc.has_caught();
    let trycatch_message = caught_message!(tc);

    let actual = Json::obj(vec![
        ("wasm_available", Json::b(true)),
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("caught", Json::b(caught)),
        ("caught_message", Json::s(&trycatch_message)),
        // The wasm failure comes from the Rust hook's own throw;
        // throw_data_clone_error is not involved.
        ("throw_calls", Json::i(0)),
    ]);
    let expected = Json::obj(vec![
        ("wasm_available", Json::b(true)),
        ("ok", Json::b(false)),
        ("wire", Json::s("")),
        ("caught", Json::b(true)),
        (
            "caught_message",
            Json::s(
                "Uncaught Error: Deno serializer: \
                 get_wasm_module_transfer_id not implemented",
            ),
        ),
        ("throw_calls", Json::i(0)),
    ]);
    vec![expect_eq(
        "serdel/wasm_write_default_delegate_error",
        expected,
        actual,
    )]
}

/// `get_wasm_module_transfer_id -> None` (no throw): the module SILENTLY
/// disappears from the wire while the enclosing object write succeeds.
fn wasm_write_none_silently_drops_module() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let counts: SharedCounts = Rc::default();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let Some(module) = eval(tc, WASM_EMPTY_MODULE) else {
        let actual = Json::obj(vec![("wasm_available", Json::b(false))]);
        let expected = Json::obj(vec![("wasm_available", Json::b(false))]);
        return vec![expect_eq(
            "serdel/wasm_write_none_silently_drops_module",
            expected,
            actual,
        )];
    };

    let holder = eval(tc, "({m: null})").unwrap();
    if let Ok(obj) = holder.try_cast::<v8::Object>() {
        let key = v8::String::new(tc, "m").unwrap();
        obj.set(tc, key.into(), module);
    }

    let serializer = v8::ValueSerializer::new(
        tc,
        Box::new(WasmIdNone {
            counts: Rc::clone(&counts),
        }),
    );
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, holder) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);
    let caught = tc.has_caught();

    let actual_counts = counts.borrow().clone();
    let actual = Json::obj(vec![
        ("wasm_available", Json::b(true)),
        ("ok", Json::b(ok)),
        ("wire", Json::s(&wire)),
        ("caught", Json::b(caught)),
        (
            "wasm_hook_calls",
            Json::i(actual_counts.get_wasm_module_transfer_id as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        ("wasm_available", Json::b(true)),
        ("ok", Json::b(true)),
        // o "m" { 1 - key written, value ABSENT: the module was dropped.
        ("wire", Json::s("6f22016d7b01")),
        ("caught", Json::b(false)),
        ("wasm_hook_calls", Json::i(1)),
    ]);
    vec![expect_eq(
        "serdel/wasm_write_none_silently_drops_module",
        expected,
        actual,
    )]
}

/// Default `get_wasm_module_from_id`: deterministic "not implemented" Error
/// on a kWasmModuleTransfer payload (`w` + varint 21).
fn wasm_read_default_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let bytes = hex_to_bytes("7715");
    let deserializer = v8::ValueDeserializer::new(tc, Box::new(DeserDefaults), &bytes);
    let ctx = tc.get_current_context();
    let value = deserializer.read_value(ctx);
    drop(deserializer);
    let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
    let caught = tc.has_caught();
    let message = caught_message!(tc);

    let actual = Json::obj(vec![
        ("read", described),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
    ]);
    let expected = Json::obj(vec![
        ("read", Json::Null),
        ("caught", Json::b(true)),
        (
            "message",
            Json::s(
                "Uncaught Error: Deno deserializer: \
                 get_wasm_module_from_id not implemented",
            ),
        ),
    ]);
    vec![expect_eq(
        "serdel/wasm_read_default_error",
        expected,
        actual,
    )]
}

/// Writer-side transfer collision: re-registering the SAME buffer under a
/// new id replaces its mapping (last registration wins on write).
fn transfer_writer_reregister_same_buffer() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
        1, 1, 1, 1,
    ]));
    let ab = v8::ArrayBuffer::with_backing_store(tc, &bs);
    let value: v8::Local<v8::Value> = ab.into();

    let (delegate, _clone_error) = SerBase::new(&Rc::default(), true);
    let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
    serializer.transfer_array_buffer(7, ab);
    serializer.transfer_array_buffer(9, ab);
    let holder = eval(tc, "({x: null})").unwrap();
    if let Ok(obj) = holder.try_cast::<v8::Object>() {
        let key = v8::String::new(tc, "x").unwrap();
        obj.set(tc, key.into(), value);
    }
    let ctx = tc.get_current_context();
    let ok = serializer.write_value(ctx, holder) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);

    let actual = Json::obj(vec![("ok", Json::b(ok)), ("wire", Json::s(&wire))]);
    let expected = Json::obj(vec![
        ("ok", Json::b(true)),
        // o "x" t varint(9) { 1 - id 9 replaced id 7 for the same buffer.
        ("wire", Json::s("6f22017874097b01")),
    ]);
    vec![expect_eq(
        "serdel/transfer_writer_reregister_same_buffer",
        expected,
        actual,
    )]
}

/// Reader-side transfer collisions: two DIFFERENT buffers written under the
/// SAME id alias to the one registered target; re-registering the id on the
/// reader replaces the target (last registration wins on read).
fn transfer_collision_reader_alias_last_wins() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Wire: ({a: <transfer 7>, b: <transfer 7>}).
    let wire = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bs1 = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
            1, 1, 1, 1,
        ]));
        let bs2 = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
            2, 2, 2, 2,
        ]));
        let ab1 = v8::ArrayBuffer::with_backing_store(tc, &bs1);
        let ab2 = v8::ArrayBuffer::with_backing_store(tc, &bs2);
        let (delegate, _clone_error) = SerBase::new(&Rc::default(), true);
        let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
        // Collision: both buffers share transfer id 7.
        serializer.transfer_array_buffer(7, ab1);
        serializer.transfer_array_buffer(7, ab2);
        let holder = eval(tc, "({a: null, b: null})").unwrap();
        if let Ok(obj) = holder.try_cast::<v8::Object>() {
            let ka = v8::String::new(tc, "a").unwrap();
            let kb = v8::String::new(tc, "b").unwrap();
            let va: v8::Local<v8::Value> = ab1.into();
            let vb: v8::Local<v8::Value> = ab2.into();
            obj.set(tc, ka.into(), va);
            obj.set(tc, kb.into(), vb);
        }
        let ctx = tc.get_current_context();
        let ok = serializer.write_value(ctx, holder) == Some(true);
        assert!(ok, "collision wire must serialize");
        let w = serializer.release();
        drop(serializer);
        hex(&w)
    };

    // Read with TWO registrations for id 7 (t1 then t2): the last must win
    // and both properties must alias that one target.
    let (read, read_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let t_bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
            0, 0, 0, 0,
        ]));
        let t1 = v8::ArrayBuffer::with_backing_store(tc, &t_bs);
        let t2_bs = v8::SharedRef::from(v8::ArrayBuffer::new_backing_store_from_vec(vec![
            3, 3, 3, 3,
        ]));
        let t2 = v8::ArrayBuffer::with_backing_store(tc, &t2_bs);

        struct NoCustomReads;
        impl v8::ValueDeserializerImpl for NoCustomReads {}

        let bytes = hex_to_bytes(&wire);
        let deserializer = v8::ValueDeserializer::new(tc, Box::new(NoCustomReads), &bytes);
        deserializer.transfer_array_buffer(7, t1);
        deserializer.transfer_array_buffer(7, t2);
        let ctx = tc.get_current_context();
        let value = deserializer.read_value(ctx);
        drop(deserializer);

        let described = value
            .map(|v| {
                let a = prop_array_buffer(tc, v, "a");
                let b = prop_array_buffer(tc, v, "b");
                Json::obj(vec![
                    ("a_is_t2", Json::b(a.is_some_and(|x| x == t2))),
                    ("b_is_t2", Json::b(b.is_some_and(|x| x == t2))),
                    ("a_is_t1", Json::b(a.is_some_and(|x| x == t1))),
                    ("t2_byte_length", Json::i(t2.byte_length() as i64)),
                ])
            })
            .unwrap_or(Json::Null);
        (described, tc.has_caught())
    };

    let actual = Json::obj(vec![
        ("wire", Json::s(&wire)),
        ("read", read),
        ("read_caught", Json::b(read_caught)),
    ]);
    let expected = Json::obj(vec![
        // o "a" t 7 "b" t 7 { 2.
        ("wire", Json::s("6f220161740722016274077b02")),
        (
            "read",
            Json::obj(vec![
                ("a_is_t2", Json::b(true)),
                ("b_is_t2", Json::b(true)),
                ("a_is_t1", Json::b(false)),
                ("t2_byte_length", Json::i(4)),
            ]),
        ),
        ("read_caught", Json::b(false)),
    ]);
    vec![expect_eq(
        "serdel/transfer_collision_reader_alias_last_wins",
        expected,
        actual,
    )]
}

/// Output-buffer growth through the delegate's `ReallocateBufferMemory`:
/// a 256 KiB payload forces several reallocations; `release()` hands back
/// exactly the written bytes (ownership transfer, contents intact).
fn realloc_growth_large_payload_hashed() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let payload: Vec<u8> = (0..262_144u32)
        .map(|i| ((i.wrapping_mul(31).wrapping_add(7)) & 0xff) as u8)
        .collect();

    let (delegate, _clone_error) = SerBase::new(&Rc::default(), true);
    let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
    serializer.write_uint32(1);
    serializer.write_raw_bytes(&payload);
    serializer.write_uint32(2);
    let wire = serializer.release();
    drop(serializer);

    let first16 = &wire[..16];
    let last16 = &wire[wire.len() - 16..];

    let actual = Json::obj(vec![
        ("len", Json::i(wire.len() as i64)),
        ("fnv1a", Json::s(&fnv1a_hex(&wire))),
        ("first16", Json::s(&hex(first16))),
        ("last16", Json::s(&hex(last16))),
    ]);
    let expected = Json::obj(vec![
        // varint(1) + 262144 payload bytes + varint(2).
        ("len", Json::i(262_146)),
        ("fnv1a", Json::s("879dea323af0902a")),
        ("first16", Json::s("010726456483a2c1e0ff1e3d5c7b9ab9")),
        ("last16", Json::s("36557493b2d1f00f2e4d6c8baac9e802")),
    ]);
    vec![expect_eq(
        "serdel/realloc_growth_large_payload_hashed",
        expected,
        actual,
    )]
}

/// Buffer ownership paths: `release()` consumes the buffer (a second
/// release is empty); dropping a serializer WITHOUT releasing frees through
/// the delegate's `FreeBufferMemory`; both paths leave the engine healthy.
fn release_ownership_drop_paths() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let (delegate, _clone_error) = SerBase::new(&Rc::default(), true);
    let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
    serializer.write_uint32(7);
    let first = serializer.release();
    let second = serializer.release();
    drop(serializer);

    // Drop WITHOUT release: ~1 KiB pending, freed by the destructor.
    {
        let (delegate2, _clone_error2) = SerBase::new(&Rc::default(), true);
        let serializer2 = v8::ValueSerializer::new(tc, Box::new(delegate2));
        serializer2.write_raw_bytes(&[0xAB; 1024]);
        drop(serializer2);
    }

    // Engine still healthy afterwards.
    let after = {
        let (delegate3, _clone_error3) = SerBase::new(&Rc::default(), true);
        let serializer3 = v8::ValueSerializer::new(tc, Box::new(delegate3));
        serializer3.write_uint32(5);
        hex(&serializer3.release())
    };

    let actual = Json::obj(vec![
        ("first_len", Json::i(first.len() as i64)),
        ("first_hex", Json::s(&hex(&first))),
        ("second_len", Json::i(second.len() as i64)),
        ("after_hex", Json::s(&after)),
    ]);
    let expected = Json::obj(vec![
        ("first_len", Json::i(1)),
        ("first_hex", Json::s("07")),
        ("second_len", Json::i(0)),
        ("after_hex", Json::s("05")),
    ]);
    vec![expect_eq(
        "serdel/release_ownership_drop_paths",
        expected,
        actual,
    )]
}

/// Serializer state after a data-clone error: the failed object already
/// occupies id-map space, so a LATER write of the same object emits a bare
/// `^` reference to a never-written id (a serialization hazard to mirror),
/// while fresh objects keep working.
fn serializer_state_after_clone_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();

    let f = eval(tc, "() => 1").unwrap();
    let fresh = eval(tc, "({})").unwrap();

    let (delegate, _clone_error) = SerBase::new(&Rc::default(), true);
    let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
    let ctx = tc.get_current_context();
    let ok_function = serializer.write_value(ctx, f) == Some(true);
    let ok_fresh_object = serializer.write_value(ctx, fresh) == Some(true);
    let ok_function_again = serializer.write_value(ctx, f) == Some(true);
    let wire = hex(&serializer.release());
    drop(serializer);

    let actual = Json::obj(vec![
        ("ok_function", Json::b(ok_function)),
        ("ok_fresh_object", Json::b(ok_fresh_object)),
        ("ok_function_again", Json::b(ok_function_again)),
        ("wire", Json::s(&wire)),
    ]);
    let expected = Json::obj(vec![
        ("ok_function", Json::b(false)),
        ("ok_fresh_object", Json::b(true)),
        ("ok_function_again", Json::b(true)),
        // {} wire + '^' reference (id 1 -> varint 0) for the failed object.
        ("wire", Json::s("6f7b005e00")),
    ]);
    vec![expect_eq(
        "serdel/serializer_state_after_clone_error",
        expected,
        actual,
    )]
}

/// Explicit `read_header` + `get_wire_format_version`: a version-16 payload
/// read header-first succeeds; the same bytes read WITHOUT `read_header`
/// are parsed as legacy version 0 and fail deterministically (the header
/// bytes are consumed as tags instead).
fn read_header_and_wire_format_version() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let wire = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let value = eval(tc, "true").unwrap();
        let (delegate, _clone_error) = SerBase::new(&Rc::default(), true);
        let serializer = v8::ValueSerializer::new(tc, Box::new(delegate));
        serializer.write_header();
        let ctx = tc.get_current_context();
        let ok = serializer.write_value(ctx, value) == Some(true);
        assert!(ok);
        let w = serializer.release();
        drop(serializer);
        hex(&w)
    };

    let (header_ok, version, header_read) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bytes = hex_to_bytes(&wire);
        let deserializer = v8::ValueDeserializer::new(tc, Box::new(DeserDefaults), &bytes);
        let ctx = tc.get_current_context();
        let header = deserializer.read_header(ctx);
        let ver = deserializer.get_wire_format_version();
        let value = deserializer.read_value(ctx);
        drop(deserializer);
        let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
        (header == Some(true), i64::from(ver), described)
    };

    let (no_header_read, no_header_caught, no_header_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bytes = hex_to_bytes(&wire);
        let deserializer = v8::ValueDeserializer::new(tc, Box::new(DeserDefaults), &bytes);
        let ctx = tc.get_current_context();
        let value = deserializer.read_value(ctx);
        drop(deserializer);
        let described = value.map(|v| describe_value(tc, v)).unwrap_or(Json::Null);
        (described, tc.has_caught(), caught_message!(tc))
    };

    let actual = Json::obj(vec![
        ("wire", Json::s(&wire)),
        ("with_header_ok", Json::b(header_ok)),
        ("with_header_version", Json::i(version)),
        ("with_header_read", header_read),
        ("without_header_read", no_header_read),
        ("without_header_caught", Json::b(no_header_caught)),
        ("without_header_message", Json::s(&no_header_message)),
    ]);
    let expected = Json::obj(vec![
        // kVersion tag + varint(16) + 'T'.
        ("wire", Json::s("ff1054")),
        ("with_header_ok", Json::b(true)),
        ("with_header_version", Json::i(16)),
        (
            "with_header_read",
            Json::obj(vec![("type", Json::s("boolean")), ("value", Json::b(true))]),
        ),
        ("without_header_read", Json::Null),
        ("without_header_caught", Json::b(true)),
        (
            "without_header_message",
            Json::s("Uncaught Error: Deno deserializer: read_host_object not implemented"),
        ),
    ]);
    vec![expect_eq(
        "serdel/read_header_and_wire_format_version",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

const CHECKS: &[fn() -> Vec<CheckOutcome>] = &[
    detection_denies_all_hosts,
    detection_embedder_fields_without_custom,
    detection_admits_host_routes_to_write,
    host_write_read_roundtrip,
    host_default_write_error_partial_wire,
    host_read_default_error,
    host_read_none_throws_engine_error,
    write_host_object_false_result_ignored,
    clone_error_delegate_without_rethrow,
    clone_error_with_custom_host_exception,
    sab_write_custom_id,
    sab_write_default_none_is_rejected,
    sab_read_roundtrip,
    sab_read_default_error,
    sab_read_none_throws_engine_error,
    sab_read_transfer_registration_not_consulted,
    wasm_write_default_delegate_error,
    wasm_write_none_silently_drops_module,
    wasm_read_default_error,
    transfer_writer_reregister_same_buffer,
    transfer_collision_reader_alias_last_wins,
    realloc_growth_large_payload_hashed,
    release_ownership_drop_paths,
    serializer_state_after_clone_error,
    read_header_and_wire_format_version,
];

fn main() -> std::process::ExitCode {
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
            use std::io::Write as _;
            let _ = writeln!(out, "{}", outcome.to_line());
            let _ = out.flush();
        }
    }
    let failed = total - passed;
    use std::io::Write as _;
    let _ = writeln!(out, "{}", summary_line(total, passed, failed));
    let _ = out.flush();
    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
