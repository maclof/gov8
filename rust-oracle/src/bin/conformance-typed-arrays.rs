//! Typed-array / ArrayBufferView conformance slice for the pinned `v8`
//! crate (rusty_v8 =152.2.0, V8 15.2.124.1-rusty, x86_64-pc-windows-msvc).
//!
//! Characterizes, in fixed order, the observable contract of:
//! - every typed-array kind the crate exposes: `Int8Array`, `Uint8Array`,
//!   `Uint8ClampedArray`, `Int16Array`, `Uint16Array`, `Int32Array`,
//!   `Uint32Array`, `Float16Array`, `Float32Array`, `Float64Array`,
//!   `BigInt64Array`, `BigUint64Array` (crate `src/typed_array.rs`,
//!   `typed_array!` list; engine `src/support.h` `EACH_TYPED_ARRAY`),
//!   including the value predicates (`Value::is_*_array`, crate
//!   `src/value.rs`), `Value::type_of` / `Value::type_repr` tags, JS
//!   constructor names and `BYTES_PER_ELEMENT`.
//! - per-kind size constants (`X::MAX_LENGTH` = `TypedArray::MAX_BYTE_LENGTH
//!   / sizeof(elt)` truncated, engine `include/v8-typed-array.h`;
//!   `kMaxByteLength` is `kMaxSafeInteger` = 2^53-1 with the sandbox off,
//!   engine `src/objects/js-array-buffer.h`) and observed element sizes
//!   through 1-element view geometry.
//! - native `X::new(scope, ab, byte_offset, length)` geometry for aligned,
//!   in-bounds arguments, plus zero-length views at offset 0 and at the
//!   exact end of the buffer. Out-of-bounds or misaligned native calls are
//!   process-fatal V8 CHECK failures, not `None` results: they are
//!   characterized out-of-process by `tests/typed_arrays_negative.rs` and
//!   must never run in this binary.
//! - cross-boundary element semantics: Rust-written bit patterns read from
//!   JS (sign/zero extension, IEEE 32/16-bit float decoding, BigInt
//!   conversion) and JS-written values read back as bytes through
//!   `copy_contents` (modular wrapping for the integer kinds,
//!   `Uint8ClampedArray` clamp with round-half-to-even, float overflow to
//!   Infinity, BigInt64/BigUint64 modular wraparound).
//! - JS-created view geometry: `subarray` (shares the buffer, byte_offset
//!   accumulates), `slice` (fresh buffer at offset 0), length-tracking
//!   views over fixed buffers, and constructor-created own buffers.
//! - the `ArrayBufferView` data/backing-store/copy surface (crate
//!   `src/array_buffer_view.rs`): `buffer()` identity, `has_buffer()`,
//!   `get_backing_store()`, `data()` = buffer data + byte_offset,
//!   `byte_length()`, `byte_offset()`, `copy_contents` (copies
//!   min(dest.len, view.byte_length) bytes, engine `api.cc`
//!   `ArrayBufferView::CopyContents`), `copy_contents_uninit`, and
//!   `get_contents` (off-heap views always return a live slice over the
//!   backing store and ignore the caller's storage size, engine `api.cc`
//!   `ArrayBufferView::GetContents` off-heap path; with
//!   `TYPED_ARRAY_MAX_SIZE_IN_HEAP = 0` in this build nothing is ever
//!   allocated on the V8 heap).
//! - the full view-side detach contract: geometry collapses to zero,
//!   `byte_offset()` is pinned to 0 for detached views, `data()` is null,
//!   buffer identity is retained, copies yield 0 bytes, and JS indexing
//!   yields `undefined`.
//! - `DataView` as an `ArrayBufferView` (byte-granular: odd offsets are
//!   legal, `get_contents`/`copy_contents` include the byte offset,
//!   length-tracking over a fixed buffer).
//! - SharedArrayBuffer-backed views (created from JS: the crate only binds
//!   the `Local<ArrayBuffer>` overload of `TypedArray::New`, so there is no
//!   native SAB construction path — an upstream binding gap the Go port
//!   must know about): geometry, the masquerading `buffer()` result (a
//!   `Local<ArrayBuffer>` whose value satisfies `is_shared_array_buffer()`
//!   and NOT `is_array_buffer()`), shared backing-store access, and
//!   byte-level cross-boundary visibility in both directions.
//! - the JS error path for invalid view construction (deterministic
//!   RangeError messages from engine `src/common/message-template.h`:
//!   `InvalidTypedArrayAlignment`, `InvalidOffset`,
//!   `InvalidTypedArrayLength`, `InvalidDataViewLength`), which the native
//!   path rejects by aborting the process instead.
//! - `Float16Array` availability: `js_float16array` is a shipping (default
//!   on) feature in this build (engine `src/flags/flag-definitions.h`,
//!   `JAVASCRIPT_SHIPPING_FEATURES_BASE`), so `Float16Array::New` passes its
//!   `ApiCheck` (engine `api.cc`: with the flag off the call is CHECK-fatal
//!   with "Float16Array is not supported"), the JS constructor exists, and
//!   `Math.f16round` / `DataView.prototype.setFloat16/getFloat16` work.
//!
//! Deliberately out of scope: `ValueSerializer` wire bytes for typed-array
//! views (the buffers slice owns the serializer contract) and
//! `ArrayBuffer`/`BackingStore` ownership and detach-key mechanics (the
//! buffers slice). Calling `get_backing_store()` on a view whose buffer was
//! detached is likewise left uncharacterized: the crate returns a
//! non-optional `SharedRef` over a C++ shared_ptr that V8 may have
//! cleared on detach, so the call has undefined-ish semantics and no
//! deterministic contract value.
//!
//! Upstream API-surface gap pinned here for the Go port (crate
//! `src/data.rs`): `Float16Array` is the only typed-array kind with NO
//! `From` conversions to `Value`/`Object`/`Data`/`ArrayBufferView`/
//! `TypedArray` (it only has `TryFrom` impls toward itself), so widening a
//! natively-built `Local<Float16Array>` needs `Local::cast_unchecked`
//! (pointer type-punning of a live handle up V8's own class hierarchy; see
//! the SAFETY note in `Kind::native_new`).
//!
//! Everything is normalized per `src/json.rs` rules: no addresses (pointer
//! relationships are recorded as byte offsets, null-ness, or booleans), no
//! timings, exact engine message strings for the pinned build. The runner
//! emits the same JSON-lines protocol as the other slices
//! (`{"check":..,"ok":..,"value"|"expected"/"actual"}` + final summary).
//!
//! This slice performs no platform shutdown, so it can be verified
//! in-process; its fixture is pinned by
//! `tests/conformance_typed_arrays_fixture.rs` (binary output only: the
//! checks live in this binary because the existing `src/checks` registries
//! are shared files that this slice must not modify).
//!
//! Benchmark specifications (to be added as `benches/typed_array.rs` under
//! the shared methodology of `benches/common/mod.rs`: 1 s warm-up, 3 s
//! measurement, 50 samples, fresh nested `HandleScope` per iteration,
//! release bench profile, no V8 flags; Go must mirror all of it):
//! - `typedarray/new_buffer_and_view`: `ArrayBuffer::new(scope, 64)` +
//!   `Uint8Array::new(scope, ab, 0, 64)` per iteration.
//! - `typedarray/new_view_only`: fixed 64-byte `ArrayBuffer`;
//!   `Uint8Array::new(scope, ab, 0, 64)` per iteration.
//! - `typedarray/copy_contents_256`: fixed `Uint8Array` over 256 bytes;
//!   `copy_contents(&mut [u8; 256])` per iteration.
//! - `typedarray/host_element_roundtrip`: fixed `Uint16Array` over 256
//!   bytes; per iteration write 128 `u16` values through the backing store
//!   (`bs[i].set(...)`) and read them back with `copy_contents`.
//! - `typedarray/js_element_roundtrip`: fixed `Uint16Array` stored in a
//!   global; precompiled script `ta[0] = ta[0] + 1; ta[0]` run per
//!   iteration (measures JS element access + number conversion).
//! - `typedarray/sab_view_copy_contents`: fixed `Uint16Array` over a
//!   256-byte `SharedArrayBuffer`; `copy_contents` per iteration (the
//!   relaxed-atomic copy path in `ArrayBufferView::CopyContents`).
//!
//! Regenerate the pinned fixture (byte-exact redirection; PowerShell `>`
//! writes UTF-16, use cmd):
//!
//! ```text
//! cargo build --bin conformance-typed-arrays
//! cmd /c "target\debug\conformance-typed-arrays.exe > tests\fixtures\conformance-typed-arrays-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
//! cargo test
//! ```

use std::io::Write as _;
use std::process::ExitCode;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

// ---------------------------------------------------------------------------
// Helpers (local to this binary; the crate's `checks::harness` is pub(crate)
// and existing files must not be modified to expose it).
// ---------------------------------------------------------------------------

/// Lowercase hex without separators: the canonical encoding for view
/// contents in this slice.
fn hex(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        use std::fmt::Write as _;
        let _ = write!(out, "{byte:02x}");
    }
    out
}

/// Full 16-byte lowercase-hex of a zeroed buffer with `prefix` at the front.
fn hex16(prefix: &[u8]) -> String {
    let mut buf = [0u8; 16];
    buf[..prefix.len()].copy_from_slice(prefix);
    hex(&buf)
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

/// Stores `value` on the current context's global object under `name` and
/// reports whether the store succeeded.
fn set_global(
    scope: &mut v8::PinScope<'_, '_>,
    name: &str,
    value: v8::Local<'_, v8::Value>,
) -> bool {
    let global = scope.get_current_context().global(scope);
    let Some(key) = v8::String::new(scope, name) else {
        return false;
    };
    global.set(scope, key.into(), value) == Some(true)
}

/// Widens a concrete handle to `Local<Value>` (every kind has a `From`
/// conversion toward `Value`; DataView included).
fn value_of<'s, T>(typed: v8::Local<'s, T>) -> v8::Local<'s, v8::Value>
where
    v8::Local<'s, v8::Value>: From<v8::Local<'s, T>>,
{
    typed.into()
}

/// Narrows a `Local<Value>` to the shared `ArrayBufferView` surface via the
/// crate's predicate casts (`Value::is_array_buffer_view`).
fn to_view<'s>(value: v8::Local<'s, v8::Value>) -> v8::Local<'s, v8::ArrayBufferView> {
    value
        .try_cast::<v8::ArrayBufferView>()
        .expect("value is an ArrayBufferView")
}

/// A fresh 16-byte ArrayBuffer in the current context.
fn ab16<'s>(scope: &mut v8::PinScope<'s, '_>) -> v8::Local<'s, v8::ArrayBuffer> {
    v8::ArrayBuffer::new(scope, 16)
}

/// Element size in bytes for one kind (engine `include/v8-typed-array.h`
/// defines each constructor over a fixed-width element type).
fn element_size_of(kind: Kind) -> usize {
    match kind {
        Kind::Int8Array | Kind::Uint8Array | Kind::Uint8ClampedArray => 1,
        Kind::Int16Array | Kind::Uint16Array | Kind::Float16Array => 2,
        Kind::Int32Array | Kind::Uint32Array | Kind::Float32Array => 4,
        Kind::Float64Array | Kind::BigInt64Array | Kind::BigUint64Array => 8,
    }
}

// ---------------------------------------------------------------------------
// The 12 typed-array kinds. The `typed_array!` macro in crate
// `src/typed_array.rs` instantiates `new` + `MAX_LENGTH` per kind; crate
// `src/value.rs` exposes one `is_*_array` predicate per kind.
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, PartialEq, Eq)]
// Variant names deliberately mirror the pinned crate's type names
// (`v8::Int8Array`, ...), so the shared `Array` suffix is intentional.
#[allow(clippy::enum_variant_names)]
enum Kind {
    Int8Array,
    Uint8Array,
    Uint8ClampedArray,
    Int16Array,
    Uint16Array,
    Int32Array,
    Uint32Array,
    Float16Array,
    Float32Array,
    Float64Array,
    BigInt64Array,
    BigUint64Array,
}

/// Fixed order: part of the observable contract (fixture order).
const ALL_KINDS: [Kind; 12] = [
    Kind::Int8Array,
    Kind::Uint8Array,
    Kind::Uint8ClampedArray,
    Kind::Int16Array,
    Kind::Uint16Array,
    Kind::Int32Array,
    Kind::Uint32Array,
    Kind::Float16Array,
    Kind::Float32Array,
    Kind::Float64Array,
    Kind::BigInt64Array,
    Kind::BigUint64Array,
];

impl Kind {
    fn key(self) -> &'static str {
        match self {
            Kind::Int8Array => "int8",
            Kind::Uint8Array => "uint8",
            Kind::Uint8ClampedArray => "uint8_clamped",
            Kind::Int16Array => "int16",
            Kind::Uint16Array => "uint16",
            Kind::Int32Array => "int32",
            Kind::Uint32Array => "uint32",
            Kind::Float16Array => "float16",
            Kind::Float32Array => "float32",
            Kind::Float64Array => "float64",
            Kind::BigInt64Array => "bigint64",
            Kind::BigUint64Array => "biguint64",
        }
    }

    fn ctor(self) -> &'static str {
        match self {
            Kind::Int8Array => "Int8Array",
            Kind::Uint8Array => "Uint8Array",
            Kind::Uint8ClampedArray => "Uint8ClampedArray",
            Kind::Int16Array => "Int16Array",
            Kind::Uint16Array => "Uint16Array",
            Kind::Int32Array => "Int32Array",
            Kind::Uint32Array => "Uint32Array",
            Kind::Float16Array => "Float16Array",
            Kind::Float32Array => "Float32Array",
            Kind::Float64Array => "Float64Array",
            Kind::BigInt64Array => "BigInt64Array",
            Kind::BigUint64Array => "BigUint64Array",
        }
    }

    fn is_specific(self, value: &v8::Value) -> bool {
        match self {
            Kind::Int8Array => value.is_int8_array(),
            Kind::Uint8Array => value.is_uint8_array(),
            Kind::Uint8ClampedArray => value.is_uint8_clamped_array(),
            Kind::Int16Array => value.is_int16_array(),
            Kind::Uint16Array => value.is_uint16_array(),
            Kind::Int32Array => value.is_int32_array(),
            Kind::Uint32Array => value.is_uint32_array(),
            Kind::Float16Array => value.is_float16_array(),
            Kind::Float32Array => value.is_float32_array(),
            Kind::Float64Array => value.is_float64_array(),
            Kind::BigInt64Array => value.is_big_int64_array(),
            Kind::BigUint64Array => value.is_big_uint64_array(),
        }
    }

    fn max_length(self) -> usize {
        match self {
            Kind::Int8Array => v8::Int8Array::MAX_LENGTH,
            Kind::Uint8Array => v8::Uint8Array::MAX_LENGTH,
            Kind::Uint8ClampedArray => v8::Uint8ClampedArray::MAX_LENGTH,
            Kind::Int16Array => v8::Int16Array::MAX_LENGTH,
            Kind::Uint16Array => v8::Uint16Array::MAX_LENGTH,
            Kind::Int32Array => v8::Int32Array::MAX_LENGTH,
            Kind::Uint32Array => v8::Uint32Array::MAX_LENGTH,
            Kind::Float16Array => v8::Float16Array::MAX_LENGTH,
            Kind::Float32Array => v8::Float32Array::MAX_LENGTH,
            Kind::Float64Array => v8::Float64Array::MAX_LENGTH,
            Kind::BigInt64Array => v8::BigInt64Array::MAX_LENGTH,
            Kind::BigUint64Array => v8::BigUint64Array::MAX_LENGTH,
        }
    }

    /// Native construction through the concrete type, widened to the shared
    /// `ArrayBufferView` surface via the crate's `TryFrom` predicate casts
    /// (`Value::is_array_buffer_view` accepts every kind).
    ///
    /// SAFETY (Float16Array arm only): upstream gap documented in the module
    /// header — `Float16Array` is the only kind without a `From` conversion
    /// to `Local<Value>` in crate `src/data.rs`. `Local::cast_unchecked` is
    /// a pure pointer type-punning of an existing, scope-rooted handle; a
    /// `Float16Array` is a `Value` in V8's own class hierarchy
    /// (`Float16Array : TypedArray : ArrayBufferView : Object : Value`), the
    /// target is strictly up the hierarchy, so the transmute cannot
    /// invalidate or mislabel live data for the lifetime of the handle.
    fn native_new<'s>(
        self,
        scope: &v8::PinScope<'s, '_>,
        ab: v8::Local<'_, v8::ArrayBuffer>,
        byte_offset: usize,
        length: usize,
    ) -> Option<v8::Local<'s, v8::ArrayBufferView>> {
        let value: v8::Local<'s, v8::Value> = match self {
            Kind::Int8Array => v8::Int8Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::Uint8Array => v8::Uint8Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::Uint8ClampedArray => {
                v8::Uint8ClampedArray::new(scope, ab, byte_offset, length)?.into()
            }
            Kind::Int16Array => v8::Int16Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::Uint16Array => v8::Uint16Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::Int32Array => v8::Int32Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::Uint32Array => v8::Uint32Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::Float16Array => {
                let typed = v8::Float16Array::new(scope, ab, byte_offset, length)?;
                // See SAFETY note above.
                unsafe { v8::Local::cast_unchecked(typed) }
            }
            Kind::Float32Array => v8::Float32Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::Float64Array => v8::Float64Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::BigInt64Array => v8::BigInt64Array::new(scope, ab, byte_offset, length)?.into(),
            Kind::BigUint64Array => v8::BigUint64Array::new(scope, ab, byte_offset, length)?.into(),
        };
        value.try_cast::<v8::ArrayBufferView>().ok()
    }
}

/// JS element-read table: bytes written by Rust through the backing store,
/// then read back from JS as `String(Array.from(ta))`.
struct ReadCase {
    kind: Kind,
    bytes: [u8; 16],
    view_len: usize,
    expected_js: &'static str,
}

/// Hand-pinned read cases. Every value is exactly representable in its
/// element type, so the JS number formatting is the ECMAScript shortest
/// form. IEEE half patterns: 0x3C00=1.0, 0x3800=0.5, 0xC000=-2.0,
/// 0x7BFF=65504 (max finite).
fn read_cases() -> Vec<ReadCase> {
    vec![
        ReadCase {
            kind: Kind::Int8Array,
            bytes: [0x80, 0x7F, 0xFF, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
            view_len: 4,
            expected_js: "-128,127,-1,0",
        },
        ReadCase {
            kind: Kind::Uint8Array,
            bytes: [0x80, 0x7F, 0xFF, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
            view_len: 4,
            expected_js: "128,127,255,0",
        },
        ReadCase {
            kind: Kind::Uint8ClampedArray,
            bytes: [0x80, 0x7F, 0xFF, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
            view_len: 4,
            expected_js: "128,127,255,0",
        },
        ReadCase {
            kind: Kind::Int16Array,
            bytes: [
                0x00, 0x80, 0xFF, 0x7F, 0xFF, 0xFF, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0,
            ],
            view_len: 4,
            expected_js: "-32768,32767,-1,0",
        },
        ReadCase {
            kind: Kind::Uint16Array,
            bytes: [
                0x00, 0x80, 0xFF, 0x7F, 0xFF, 0xFF, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0,
            ],
            view_len: 4,
            expected_js: "32768,32767,65535,0",
        },
        ReadCase {
            kind: Kind::Int32Array,
            bytes: [
                0x00, 0x00, 0x00, 0x80, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00,
                0x00, 0x00,
            ],
            view_len: 4,
            expected_js: "-2147483648,2147483647,-1,0",
        },
        ReadCase {
            kind: Kind::Uint32Array,
            bytes: [
                0x00, 0x00, 0x00, 0x80, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00,
                0x00, 0x00,
            ],
            view_len: 4,
            expected_js: "2147483648,2147483647,4294967295,0",
        },
        ReadCase {
            kind: Kind::Float16Array,
            // IEEE half patterns: 0x3C00=1.0, 0x3800=0.5, 0xC000=-2.0,
            // 0x7BFF=65504 (max finite).
            bytes: [
                0x00, 0x3C, 0x00, 0x38, 0x00, 0xC0, 0xFF, 0x7B, 0, 0, 0, 0, 0, 0, 0, 0,
            ],
            view_len: 4,
            expected_js: "1,0.5,-2,65504",
        },
        ReadCase {
            kind: Kind::Float32Array,
            bytes: [
                0x00, 0x00, 0x80, 0x3F, 0x00, 0x00, 0x20, 0xC0, 0x00, 0x00, 0x00, 0x3F, 0x00, 0x00,
                0x00, 0x00,
            ],
            view_len: 4,
            expected_js: "1,-2.5,0.5,0",
        },
        ReadCase {
            kind: Kind::Float64Array,
            bytes: [
                0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF8, 0x3F, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
                0xE0, 0xBF,
            ],
            view_len: 2,
            expected_js: "1.5,-0.5",
        },
        ReadCase {
            kind: Kind::BigInt64Array,
            bytes: [
                0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
                0xFF, 0xFF,
            ],
            view_len: 2,
            expected_js: "1,-1",
        },
        ReadCase {
            kind: Kind::BigUint64Array,
            bytes: [
                0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
                0xFF, 0xFF,
            ],
            view_len: 2,
            expected_js: "1,18446744073709551615",
        },
    ]
}

/// JS element-write table: script writes into the global `w`, then Rust
/// reads the full 16-byte buffer back through `copy_contents`.
struct WriteCase {
    kind: Kind,
    script: &'static str,
    /// Expected first bytes; the rest of the 16-byte buffer stays zeroed.
    expected_prefix: Vec<u8>,
}

/// Hand-pinned write cases; expectations derived from the ECMAScript
/// conversion rules (ToUint8/ToInt8/ToUint16/ToInt16/ToUint32/ToInt32 wrap
/// modulo 2^width; ToUint8Clamp clamps and rounds half to even; typed float
/// writes round to the nearest element value with overflow to Infinity;
/// ToBigInt64/ToBigUint64 wrap modulo 2^64).
fn write_cases() -> Vec<WriteCase> {
    vec![
        // -129 wraps to 127, 128 wraps to -128 (0x80), 255 wraps to -1.
        WriteCase {
            kind: Kind::Int8Array,
            script: "w[0]=-129;w[1]=128;w[2]=255;",
            expected_prefix: vec![
                (-129i32).rem_euclid(256) as u8,
                128i32.rem_euclid(256) as u8,
                255u8,
            ],
        },
        // 256 wraps to 0, -1 wraps to 255.
        WriteCase {
            kind: Kind::Uint8Array,
            script: "w[0]=256;w[1]=-1;",
            expected_prefix: vec![0x00, 0xFF],
        },
        // 300 clamps to 255, -1 clamps to 0, and fractional values round
        // half-to-even: 1.5 -> 2, 2.5 -> 2, 0.5 -> 0.
        WriteCase {
            kind: Kind::Uint8ClampedArray,
            script: "w[0]=300;w[1]=-1;w[2]=1.5;w[3]=2.5;w[4]=0.5;",
            expected_prefix: vec![255, 0, 2, 2, 0],
        },
        // -32769 wraps to 0x7FFF, 32768 wraps to 0x8000.
        WriteCase {
            kind: Kind::Int16Array,
            script: "w[0]=-32769;w[1]=32768;",
            expected_prefix: {
                let mut v = Vec::new();
                v.extend_from_slice(&((-32769i32).rem_euclid(65_536) as u16).to_le_bytes());
                v.extend_from_slice(&32768u16.to_le_bytes());
                v
            },
        },
        // 65536 wraps to 0, -1 wraps to 0xFFFF.
        WriteCase {
            kind: Kind::Uint16Array,
            script: "w[0]=65536;w[1]=-1;",
            expected_prefix: {
                let mut v = Vec::new();
                v.extend_from_slice(&0u16.to_le_bytes());
                v.extend_from_slice(&u16::MAX.to_le_bytes());
                v
            },
        },
        // -2147483649 wraps to 0x7FFFFFFF, 2147483648 wraps to 0x80000000.
        WriteCase {
            kind: Kind::Int32Array,
            script: "w[0]=-2147483649;w[1]=2147483648;",
            expected_prefix: {
                let mut v = Vec::new();
                // -2147483649 wraps to +2147483647 (0x7FFFFFFF).
                v.extend_from_slice(
                    &((-2_147_483_649i64).rem_euclid(4_294_967_296) as u32).to_le_bytes(),
                );
                v.extend_from_slice(&2_147_483_648u32.to_le_bytes());
                v
            },
        },
        // 4294967296 wraps to 0, -1 wraps to 0xFFFFFFFF.
        WriteCase {
            kind: Kind::Uint32Array,
            script: "w[0]=4294967296;w[1]=-1;",
            expected_prefix: {
                let mut v = Vec::new();
                v.extend_from_slice(&0u32.to_le_bytes());
                v.extend_from_slice(&u32::MAX.to_le_bytes());
                v
            },
        },
        // 1e50 overflows f32 to +Infinity; 0.1 rounds to the f32 neighbor
        // 0x3DCCCCCD (little-endian cd cc cc 3d).
        WriteCase {
            kind: Kind::Float32Array,
            script: "w[0]=1e50;w[1]=0.1;",
            expected_prefix: {
                let mut v = Vec::new();
                v.extend_from_slice(&f32::INFINITY.to_le_bytes());
                v.extend_from_slice(&0.1f32.to_le_bytes());
                v
            },
        },
        WriteCase {
            kind: Kind::Float64Array,
            script: "w[0]=1.5;w[1]=-0.5;",
            expected_prefix: {
                let mut v = Vec::new();
                v.extend_from_slice(&1.5f64.to_le_bytes());
                v.extend_from_slice(&(-0.5f64).to_le_bytes());
                v
            },
        },
        // f16(1.5)=0x3E00, f16(-2)=0xC000, f16(0.5)=0x3800.
        WriteCase {
            kind: Kind::Float16Array,
            script: "w[0]=1.5;w[1]=-2;w[2]=0.5;",
            expected_prefix: vec![0x00, 0x3E, 0x00, 0xC0, 0x00, 0x38],
        },
        // 2^63 wraps to 0x8000000000000000; -2^63-1 wraps to 0x7FFFFFFFFFFFFFFF.
        WriteCase {
            kind: Kind::BigInt64Array,
            script: "w[0]=9223372036854775808n;w[1]=-9223372036854775809n;",
            expected_prefix: {
                let mut v = Vec::new();
                v.extend_from_slice(&9_223_372_036_854_775_808u64.to_le_bytes());
                v.extend_from_slice(&9_223_372_036_854_775_807u64.to_le_bytes());
                v
            },
        },
        // -1n wraps to u64::MAX; 2^64 wraps to 0.
        WriteCase {
            kind: Kind::BigUint64Array,
            script: "w[0]=-1n;w[1]=18446744073709551616n;",
            expected_prefix: {
                let mut v = Vec::new();
                v.extend_from_slice(&u64::MAX.to_le_bytes());
                v.extend_from_slice(&0u64.to_le_bytes());
                v
            },
        },
    ]
}

// ---------------------------------------------------------------------------
// Checks. Order is part of the observable contract (the fixture is ordered).
// ---------------------------------------------------------------------------

/// Predicate/tag matrix over JS-created instances of all 12 kinds plus the
/// DataView and ArrayBuffer contrast rows.
#[allow(clippy::too_many_lines)]
fn kind_predicates() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut actual: Vec<(&'static str, Json)> = Vec::new();
    for kind in ALL_KINDS {
        let value = eval(scope, &format!("new {}(4)", kind.ctor())).unwrap();
        let ctor_name = eval_text(scope, &format!("(new {}(4)).constructor.name", kind.ctor()));
        let bpe = eval_text(scope, &format!("String({}.BYTES_PER_ELEMENT)", kind.ctor()));
        actual.push((
            kind.key(),
            Json::obj(vec![
                ("is_typed_array", Json::b(value.is_typed_array())),
                (
                    "is_array_buffer_view",
                    Json::b(value.is_array_buffer_view()),
                ),
                ("is_data_view", Json::b(value.is_data_view())),
                (
                    "is_shared_array_buffer",
                    Json::b(value.is_shared_array_buffer()),
                ),
                ("specific_predicate", Json::b(kind.is_specific(&value))),
                ("ctor_name", Json::s(&ctor_name)),
                ("bytes_per_element", Json::s(&bpe)),
                (
                    "type_of",
                    Json::s(&value.type_of(scope).to_rust_string_lossy(scope)),
                ),
                ("type_repr", Json::s(value.type_repr())),
            ]),
        ));
    }
    // Contrast rows: a DataView is a view but not a typed array; an
    // ArrayBuffer is neither.
    let dv = eval(scope, "new DataView(new ArrayBuffer(8))").unwrap();
    actual.push((
        "data_view",
        Json::obj(vec![
            ("is_typed_array", Json::b(dv.is_typed_array())),
            ("is_array_buffer_view", Json::b(dv.is_array_buffer_view())),
            ("is_data_view", Json::b(dv.is_data_view())),
            ("ctor_name", Json::s("DataView")),
            (
                "type_of",
                Json::s(&dv.type_of(scope).to_rust_string_lossy(scope)),
            ),
            ("type_repr", Json::s(dv.type_repr())),
        ]),
    ));
    let ab = eval(scope, "new ArrayBuffer(8)").unwrap();
    actual.push((
        "array_buffer",
        Json::obj(vec![
            ("is_typed_array", Json::b(ab.is_typed_array())),
            ("is_array_buffer_view", Json::b(ab.is_array_buffer_view())),
            ("is_data_view", Json::b(ab.is_data_view())),
            ("type_repr", Json::s(ab.type_repr())),
        ]),
    ));

    let mut expected: Vec<(&'static str, Json)> = Vec::new();
    for kind in ALL_KINDS {
        // Upstream gap (pinned evidence for the Go port): crate
        // `src/value.rs` `Value::type_repr` has NO `is_float16_array`
        // branch — the chain goes `is_float32_array` -> `is_typed_array`,
        // so a Float16Array reports the generic "TypedArray" tag while
        // every other kind reports its own name.
        let type_repr = match kind {
            Kind::Float16Array => "TypedArray",
            other => other.ctor(),
        };
        expected.push((
            kind.key(),
            Json::obj(vec![
                ("is_typed_array", Json::b(true)),
                ("is_array_buffer_view", Json::b(true)),
                ("is_data_view", Json::b(false)),
                ("is_shared_array_buffer", Json::b(false)),
                ("specific_predicate", Json::b(true)),
                ("ctor_name", Json::s(kind.ctor())),
                (
                    "bytes_per_element",
                    Json::s(&element_size_of(kind).to_string()),
                ),
                ("type_of", Json::s("object")),
                ("type_repr", Json::s(type_repr)),
            ]),
        ));
    }
    expected.push((
        "data_view",
        Json::obj(vec![
            ("is_typed_array", Json::b(false)),
            ("is_array_buffer_view", Json::b(true)),
            ("is_data_view", Json::b(true)),
            ("ctor_name", Json::s("DataView")),
            ("type_of", Json::s("object")),
            ("type_repr", Json::s("DataView")),
        ]),
    ));
    expected.push((
        "array_buffer",
        Json::obj(vec![
            ("is_typed_array", Json::b(false)),
            ("is_array_buffer_view", Json::b(false)),
            ("is_data_view", Json::b(false)),
            ("type_repr", Json::s("ArrayBuffer")),
        ]),
    ));

    vec![expect_eq(
        "typedarrays/kind_predicates",
        Json::obj(expected),
        Json::obj(actual),
    )]
}

/// Pinned size limits: `TypedArray::MAX_BYTE_LENGTH` is
/// `ArrayBuffer::kMaxByteLength` = `kMaxSafeInteger` (2^53-1) with the
/// sandbox off, and each kind's `MAX_LENGTH` is that divided by the element
/// size, truncated. `TYPED_ARRAY_MAX_SIZE_IN_HEAP` is 0 in this build.
fn constants() -> Vec<CheckOutcome> {
    let mut actual: Vec<(&'static str, Json)> = Vec::new();
    for kind in ALL_KINDS {
        actual.push((kind.key(), Json::i(kind.max_length() as i64)));
    }
    actual.push((
        "typed_array_max_byte_length",
        Json::i(v8::TypedArray::MAX_BYTE_LENGTH as i64),
    ));
    actual.push((
        "typed_array_max_size_in_heap",
        Json::i(v8::TYPED_ARRAY_MAX_SIZE_IN_HEAP as i64),
    ));

    let max = 9_007_199_254_740_991i64; // 2^53 - 1
    let mut expected: Vec<(&'static str, Json)> = Vec::new();
    for kind in ALL_KINDS {
        expected.push((kind.key(), Json::i(max / element_size_of(kind) as i64)));
    }
    expected.push(("typed_array_max_byte_length", Json::i(max)));
    expected.push(("typed_array_max_size_in_heap", Json::i(0)));

    vec![expect_eq(
        "typedarrays/constants",
        Json::obj(expected),
        Json::obj(actual),
    )]
}

/// Observed element size through 1-element view geometry, cross-checked
/// against the constants (`MAX_BYTE_LENGTH / MAX_LENGTH`). All 12 views sit
/// at aligned offset 0 of the same 16-byte buffer, which is itself a
/// boundary fact: kinds of every element size can share one buffer.
fn element_sizes() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = ab16(scope);

    let mut actual: Vec<(&'static str, Json)> = Vec::new();
    for kind in ALL_KINDS {
        let view = kind.native_new(scope, ab, 0, 1).unwrap();
        actual.push((
            kind.key(),
            Json::obj(vec![
                ("observed_byte_length", Json::i(view.byte_length() as i64)),
                (
                    "derived_from_constants",
                    Json::i((v8::TypedArray::MAX_BYTE_LENGTH / kind.max_length()) as i64),
                ),
                ("byte_offset", Json::i(view.byte_offset() as i64)),
            ]),
        ));
    }

    let mut expected: Vec<(&'static str, Json)> = Vec::new();
    for kind in ALL_KINDS {
        expected.push((
            kind.key(),
            Json::obj(vec![
                (
                    "observed_byte_length",
                    Json::i(element_size_of(kind) as i64),
                ),
                (
                    "derived_from_constants",
                    Json::i(element_size_of(kind) as i64),
                ),
                ("byte_offset", Json::i(0)),
            ]),
        ));
    }

    vec![expect_eq(
        "typedarrays/element_sizes",
        Json::obj(expected),
        Json::obj(actual),
    )]
}

/// Native construction geometry for aligned in-bounds arguments: a view of
/// 3 elements starting at offset = element_size over a 32-byte buffer, plus
/// zero-length views at offset 0 and at the exact end of the buffer (all
/// legal; the out-of-bounds and misaligned equivalents are process-fatal,
/// see tests/typed_arrays_negative.rs).
#[allow(clippy::too_many_lines)]
fn native_geometry() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = v8::ArrayBuffer::new(scope, 32);

    let mut actual: Vec<(&'static str, Json)> = Vec::new();
    for kind in ALL_KINDS {
        let size = element_size_of(kind);
        let view = kind.native_new(scope, ab, size, 3).unwrap();
        let typed = view
            .try_cast::<v8::TypedArray>()
            .expect("every kind is a TypedArray");
        assert!(set_global(scope, "ta", view.into()));
        let js = eval_text(scope, "`${ta.length},${ta.byteLength},${ta.byteOffset}`");
        actual.push((
            kind.key(),
            Json::obj(vec![
                ("length", Json::i(typed.length() as i64)),
                ("byte_length", Json::i(view.byte_length() as i64)),
                ("byte_offset", Json::i(view.byte_offset() as i64)),
                ("js", Json::s(&js)),
            ]),
        ));
        // Zero-length views at the start and at the exact end.
        let zero_start = kind.native_new(scope, ab, 0, 0).is_some();
        let zero_end = kind.native_new(scope, ab, 32, 0).is_some();
        actual.push((
            kind.key(),
            Json::obj(vec![
                ("zero_len_at_start_is_some", Json::b(zero_start)),
                ("zero_len_at_end_is_some", Json::b(zero_end)),
            ]),
        ));
    }

    let mut expected: Vec<(&'static str, Json)> = Vec::new();
    for kind in ALL_KINDS {
        let size = element_size_of(kind);
        expected.push((
            kind.key(),
            Json::obj(vec![
                ("length", Json::i(3)),
                ("byte_length", Json::i((3 * size) as i64)),
                ("byte_offset", Json::i(size as i64)),
                ("js", Json::s(&format!("3,{},{}", 3 * size, size))),
            ]),
        ));
        expected.push((
            kind.key(),
            Json::obj(vec![
                ("zero_len_at_start_is_some", Json::b(true)),
                ("zero_len_at_end_is_some", Json::b(true)),
            ]),
        ));
    }

    vec![expect_eq(
        "typedarrays/native_geometry",
        Json::obj(expected),
        Json::obj(actual),
    )]
}

/// Rust-written bit patterns read from JS through each element type.
fn read_bit_patterns() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut actual: Vec<(&'static str, Json)> = Vec::new();
    let mut expected: Vec<(&'static str, Json)> = Vec::new();
    for case in read_cases() {
        let ab = ab16(scope);
        let store = ab.get_backing_store();
        for (i, byte) in case.bytes.iter().enumerate() {
            store[i].set(*byte);
        }
        let view = case.kind.native_new(scope, ab, 0, case.view_len).unwrap();
        assert!(set_global(scope, "ta", view.into()));
        let js = eval_text(scope, "String(Array.from(ta))");
        actual.push((case.kind.key(), Json::s(&js)));
        expected.push((case.kind.key(), Json::s(case.expected_js)));
    }

    vec![expect_eq(
        "typedarrays/read_bit_patterns",
        Json::obj(expected),
        Json::obj(actual),
    )]
}

/// JS-written values read back as bytes through `copy_contents`.
fn write_bit_patterns() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut actual: Vec<(&'static str, Json)> = Vec::new();
    let mut expected: Vec<(&'static str, Json)> = Vec::new();
    for case in write_cases() {
        // The view spans the whole 16-byte buffer (16/size elements); that
        // keeps `copied` deterministic per element size.
        let view_len = 16 / element_size_of(case.kind);
        let ab = ab16(scope);
        let view = case.kind.native_new(scope, ab, 0, view_len).unwrap();
        assert!(set_global(scope, "w", view.into()));
        eval(scope, case.script).unwrap();
        let mut bytes = [0u8; 16];
        let copied = view.copy_contents(&mut bytes);
        actual.push((
            case.kind.key(),
            Json::obj(vec![
                ("copied", Json::i(copied as i64)),
                ("readback", Json::s(&hex16(&bytes))),
            ]),
        ));
        expected.push((
            case.kind.key(),
            Json::obj(vec![
                (
                    "copied",
                    Json::i((view_len * element_size_of(case.kind)) as i64),
                ),
                ("readback", Json::s(&hex16(&case.expected_prefix))),
            ]),
        ));
    }

    vec![expect_eq(
        "typedarrays/write_bit_patterns",
        Json::obj(expected),
        Json::obj(actual),
    )]
}

/// JS-created view geometry: subarray shares the buffer with an accumulated
/// byte offset, slice makes a fresh buffer, length-tracking views over a
/// fixed buffer adopt the remainder, and constructor-created views get
/// their own buffer.
#[allow(clippy::too_many_lines)]
fn js_view_geometry() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = ab16(scope);
    assert!(set_global(scope, "ab", ab.into()));
    // Define the views as global properties once; every eval below reads
    // them from the global object.
    eval(
        scope,
        "globalThis.base = new Uint16Array(ab, 4, 4); \
         globalThis.sub = base.subarray(1, 3); \
         globalThis.sliced = base.slice(1, 3); \
         globalThis.tracking = new Uint8Array(ab, 8); \
         globalThis.tracking_dv = new DataView(ab, 4);",
    )
    .unwrap();
    let base = eval(scope, "base").unwrap();
    let sub = eval(scope, "sub").unwrap();
    let sliced = eval(scope, "sliced").unwrap();
    let tracking = eval(scope, "tracking").unwrap();
    let tracking_dv = eval(scope, "tracking_dv").unwrap();
    let own = eval(scope, "new Int8Array(4)").unwrap();
    eval(
        scope,
        "globalThis.from_iterable = new Int16Array([1, 2, 3]);",
    )
    .unwrap();
    let from_iterable = eval(scope, "from_iterable").unwrap();
    let tracking_dv_view = to_view(tracking_dv);

    let geometry = |scope: &mut v8::PinScope<'_, '_>,
                    value: v8::Local<'_, v8::Value>,
                    shares_ab: bool|
     -> Json {
        let view = to_view(value);
        let buffer = view.buffer(scope).unwrap();
        Json::obj(vec![
            ("is_typed_array", Json::b(value.is_typed_array())),
            ("byte_offset", Json::i(view.byte_offset() as i64)),
            ("byte_length", Json::i(view.byte_length() as i64)),
            (
                "length",
                Json::i(
                    value
                        .try_cast::<v8::TypedArray>()
                        .map(|t| t.length())
                        .unwrap_or_default() as i64,
                ),
            ),
            ("shares_ab", Json::b(buffer == ab)),
            ("expected_shares_ab", Json::b(shares_ab)),
        ])
    };

    let actual = Json::obj(vec![
        ("base", geometry(scope, base, true)),
        ("subarray", geometry(scope, sub, true)),
        ("slice", geometry(scope, sliced, false)),
        ("length_tracking_ta", geometry(scope, tracking, true)),
        (
            "length_tracking_dv",
            Json::obj(vec![
                ("is_typed_array", Json::b(tracking_dv.is_typed_array())),
                (
                    "byte_offset",
                    Json::i(tracking_dv_view.byte_offset() as i64),
                ),
                (
                    "byte_length",
                    Json::i(tracking_dv_view.byte_length() as i64),
                ),
            ]),
        ),
        ("own_buffer_ta", geometry(scope, own, false)),
        ("from_iterable", geometry(scope, from_iterable, false)),
        (
            "from_iterable_elements",
            Json::s(&eval_text(scope, "String(globalThis.from_iterable)")),
        ),
        (
            "subarray_buffer_identity_via_js",
            Json::b(eval_text(scope, "String(sub.buffer === ab)") == "true"),
        ),
        (
            "slice_buffer_not_ab_via_js",
            Json::b(eval_text(scope, "String(sliced.buffer === ab)") == "false"),
        ),
    ]);

    let expected = Json::obj(vec![
        (
            "base",
            Json::obj(vec![
                ("is_typed_array", Json::b(true)),
                ("byte_offset", Json::i(4)),
                ("byte_length", Json::i(8)),
                ("length", Json::i(4)),
                ("shares_ab", Json::b(true)),
                ("expected_shares_ab", Json::b(true)),
            ]),
        ),
        (
            "subarray",
            Json::obj(vec![
                ("is_typed_array", Json::b(true)),
                ("byte_offset", Json::i(6)),
                ("byte_length", Json::i(4)),
                ("length", Json::i(2)),
                ("shares_ab", Json::b(true)),
                ("expected_shares_ab", Json::b(true)),
            ]),
        ),
        (
            "slice",
            Json::obj(vec![
                ("is_typed_array", Json::b(true)),
                ("byte_offset", Json::i(0)),
                ("byte_length", Json::i(4)),
                ("length", Json::i(2)),
                ("shares_ab", Json::b(false)),
                ("expected_shares_ab", Json::b(false)),
            ]),
        ),
        (
            "length_tracking_ta",
            Json::obj(vec![
                ("is_typed_array", Json::b(true)),
                ("byte_offset", Json::i(8)),
                ("byte_length", Json::i(8)),
                ("length", Json::i(8)),
                ("shares_ab", Json::b(true)),
                ("expected_shares_ab", Json::b(true)),
            ]),
        ),
        (
            "length_tracking_dv",
            Json::obj(vec![
                ("is_typed_array", Json::b(false)),
                ("byte_offset", Json::i(4)),
                ("byte_length", Json::i(12)),
            ]),
        ),
        (
            "own_buffer_ta",
            Json::obj(vec![
                ("is_typed_array", Json::b(true)),
                ("byte_offset", Json::i(0)),
                ("byte_length", Json::i(4)),
                ("length", Json::i(4)),
                ("shares_ab", Json::b(false)),
                ("expected_shares_ab", Json::b(false)),
            ]),
        ),
        (
            "from_iterable",
            Json::obj(vec![
                ("is_typed_array", Json::b(true)),
                ("byte_offset", Json::i(0)),
                ("byte_length", Json::i(6)),
                ("length", Json::i(3)),
                ("shares_ab", Json::b(false)),
                ("expected_shares_ab", Json::b(false)),
            ]),
        ),
        ("from_iterable_elements", Json::s("1,2,3")),
        ("subarray_buffer_identity_via_js", Json::b(true)),
        ("slice_buffer_not_ab_via_js", Json::b(true)),
    ]);

    vec![expect_eq("typedarrays/js_view_geometry", expected, actual)]
}

/// The `ArrayBufferView` data/backing-store/copy surface on a
/// natively-constructed Uint8Array at offset 3, length 5.
#[allow(clippy::too_many_lines)]
fn view_surface() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = ab16(scope);
    let view = to_view(value_of(v8::Uint8Array::new(scope, ab, 3, 5).unwrap()));

    // Seed the store so every copy below has deterministic content.
    let store = ab.get_backing_store();
    for (i, byte) in [1u8, 2, 3, 4, 5].iter().enumerate() {
        store[3 + i].set(*byte);
    }

    let buffer_identity = view.buffer(scope).map(|b| b == ab).unwrap_or(false);
    let store_via_view = view.get_backing_store();
    let store_len = store_via_view
        .as_ref()
        .map(|s| s.byte_length())
        .unwrap_or(0);
    let store_shared = store_via_view
        .as_ref()
        .map(|s| s.is_shared())
        .unwrap_or(true);
    assert!(set_global(scope, "ta", view.into()));
    let js_read = eval_text(scope, "String(ta[0])");
    store[3].set(42);
    let js_sees_store_write = eval_text(scope, "String(ta[0])");
    eval(scope, "ta[0] = 7;").unwrap();
    let store_sees_js_write = store[3].get();

    let base_ptr = ab.data().unwrap().as_ptr() as usize;
    let data_delta = view.data() as usize - base_ptr;

    let mut dest = [0xEEu8; 8];
    let copied = view.copy_contents(&mut dest);
    let mut uninit_dest = [std::mem::MaybeUninit::new(0xEEu8); 8];
    let copied_uninit = view.copy_contents_uninit(&mut uninit_dest);
    let uninit_match = uninit_dest[..copied_uninit]
        .iter()
        .zip(dest.iter())
        .all(|(a, b)| unsafe { a.assume_init() } == *b);

    let mut storage = [0u8; 8];
    let contents = view.get_contents(&mut storage);
    let contents_ptr_is_data = contents.as_ptr() == view.data().cast::<u8>();
    let mut tiny = [0u8; 1];
    let contents_tiny_storage = view.get_contents(&mut tiny);
    let tiny_len = contents_tiny_storage.len();
    let tiny_matches = contents_tiny_storage == contents;

    let actual = Json::obj(vec![
        ("buffer_identity", Json::b(buffer_identity)),
        ("has_buffer", Json::b(view.has_buffer())),
        (
            "get_backing_store_is_some",
            Json::b(store_via_view.is_some()),
        ),
        ("store_byte_length", Json::i(store_len as i64)),
        ("store_is_shared", Json::b(store_shared)),
        ("js_read", Json::s(&js_read)),
        ("js_sees_store_write", Json::s(&js_sees_store_write)),
        ("store_sees_js_write", Json::i(store_sees_js_write as i64)),
        ("data_delta_is_byte_offset", Json::b(data_delta == 3)),
        (
            "copy",
            Json::obj(vec![
                ("copied", Json::i(copied as i64)),
                ("bytes", Json::s(&hex(&dest))),
                (
                    "sentinel_tail_intact",
                    Json::b(dest[5..].iter().all(|b| *b == 0xEE)),
                ),
            ]),
        ),
        (
            "copy_uninit",
            Json::obj(vec![
                ("copied", Json::i(copied_uninit as i64)),
                ("matches_copy_contents", Json::b(uninit_match)),
            ]),
        ),
        (
            "get_contents",
            Json::obj(vec![
                ("len", Json::i(contents.len() as i64)),
                ("bytes", Json::s(&hex(contents))),
                ("ptr_is_data", Json::b(contents_ptr_is_data)),
                ("len_with_tiny_storage", Json::i(tiny_len as i64)),
                ("tiny_storage_matches", Json::b(tiny_matches)),
            ]),
        ),
    ]);

    let expected = Json::obj(vec![
        ("buffer_identity", Json::b(true)),
        ("has_buffer", Json::b(true)),
        ("get_backing_store_is_some", Json::b(true)),
        ("store_byte_length", Json::i(16)),
        ("store_is_shared", Json::b(false)),
        ("js_read", Json::s("1")),
        ("js_sees_store_write", Json::s("42")),
        ("store_sees_js_write", Json::i(7)),
        ("data_delta_is_byte_offset", Json::b(true)),
        (
            "copy",
            Json::obj(vec![
                ("copied", Json::i(5)),
                // Snapshot taken AFTER the `ta[0] = 7` JS write above: the
                // first byte is 0x07, not the seeded 0x2A.
                ("bytes", Json::s("0702030405eeeeee")),
                ("sentinel_tail_intact", Json::b(true)),
            ]),
        ),
        (
            "copy_uninit",
            Json::obj(vec![
                ("copied", Json::i(5)),
                ("matches_copy_contents", Json::b(true)),
            ]),
        ),
        (
            "get_contents",
            Json::obj(vec![
                ("len", Json::i(5)),
                ("bytes", Json::s("0702030405")),
                ("ptr_is_data", Json::b(true)),
                // Off-heap views ignore the caller's storage size entirely:
                // GetContents returns a live slice over the backing store
                // (engine api.cc ArrayBufferView::GetContents off-heap path).
                ("len_with_tiny_storage", Json::i(5)),
                ("tiny_storage_matches", Json::b(true)),
            ]),
        ),
    ]);

    vec![expect_eq("typedarrays/view_surface", expected, actual)]
}

/// `copy_contents` clamping: a larger destination is only partially filled
/// (sentinel preserved), a smaller destination truncates, zero-length views
/// copy nothing, and DataView copies include the byte offset.
fn copy_contents_bounds() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = ab16(scope);
    let store = ab.get_backing_store();
    for i in 0..16 {
        store[i].set(i as u8);
    }

    let full = to_view(value_of(v8::Uint8Array::new(scope, ab, 0, 16).unwrap()));
    let tail = to_view(value_of(v8::Uint8Array::new(scope, ab, 8, 8).unwrap()));
    let zero = to_view(value_of(v8::Uint8Array::new(scope, ab, 16, 0).unwrap()));
    let dv = to_view(value_of(v8::DataView::new(scope, ab, 3, 9)));

    let mut big = [0xEEu8; 24];
    let full_copied = full.copy_contents(&mut big);
    let mut small = [0xEEu8; 4];
    let tail_copied = tail.copy_contents(&mut small);
    let mut untouched = [0xEEu8; 4];
    let zero_copied = zero.copy_contents(&mut untouched);
    let mut dv_dest = [0xEEu8; 16];
    let dv_copied = dv.copy_contents(&mut dv_dest);

    let actual = Json::obj(vec![
        (
            "dest_larger",
            Json::obj(vec![
                ("copied", Json::i(full_copied as i64)),
                ("bytes", Json::s(&hex(&big))),
            ]),
        ),
        (
            "dest_smaller",
            Json::obj(vec![
                ("copied", Json::i(tail_copied as i64)),
                ("bytes", Json::s(&hex(&small))),
            ]),
        ),
        (
            "zero_len_view",
            Json::obj(vec![
                ("copied", Json::i(zero_copied as i64)),
                ("bytes", Json::s(&hex(&untouched))),
            ]),
        ),
        (
            "data_view",
            Json::obj(vec![
                ("copied", Json::i(dv_copied as i64)),
                ("bytes", Json::s(&hex(&dv_dest))),
            ]),
        ),
    ]);

    let mut big_expected = [0xEEu8; 24];
    for (i, byte) in big_expected.iter_mut().enumerate().take(16) {
        *byte = i as u8;
    }
    let mut small_expected = [0xEEu8; 4];
    for (i, byte) in [8u8, 9, 10, 11].iter().enumerate() {
        small_expected[i] = *byte;
    }
    let mut dv_expected = [0xEEu8; 16];
    for (i, byte) in [3u8, 4, 5, 6, 7, 8, 9, 10, 11].iter().enumerate() {
        dv_expected[i] = *byte;
    }

    let expected = Json::obj(vec![
        (
            "dest_larger",
            Json::obj(vec![
                ("copied", Json::i(16)),
                ("bytes", Json::s(&hex(&big_expected))),
            ]),
        ),
        (
            "dest_smaller",
            Json::obj(vec![
                ("copied", Json::i(4)),
                ("bytes", Json::s(&hex(&small_expected))),
            ]),
        ),
        (
            "zero_len_view",
            Json::obj(vec![("copied", Json::i(0)), ("bytes", Json::s("eeeeeeee"))]),
        ),
        (
            "data_view",
            Json::obj(vec![
                ("copied", Json::i(9)),
                ("bytes", Json::s(&hex(&dv_expected))),
            ]),
        ),
    ]);

    vec![expect_eq(
        "typedarrays/copy_contents_bounds",
        expected,
        actual,
    )]
}

/// Full view-side detach contract: geometry collapses to zero (and
/// `byte_offset` is pinned to 0, not merely clamped), `data()` becomes null,
/// buffer identity is retained, copies yield 0 bytes, and JS indexing
/// yields `undefined`.
#[allow(clippy::too_many_lines)]
fn detached_view() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = ab16(scope);
    let ta = v8::Uint8Array::new(scope, ab, 3, 5).unwrap();
    let store = ab.get_backing_store();
    for (i, byte) in [1u8, 2, 3, 4, 5].iter().enumerate() {
        store[3 + i].set(*byte);
    }
    assert!(set_global(scope, "ab", ab.into()));
    assert!(set_global(scope, "ta", ta.into()));

    let detach_ok = ab.detach(None) == Some(true);

    let buffer_identity = ta.buffer(scope).map(|b| b == ab).unwrap_or(false);
    let mut dest = [0xEEu8; 8];
    let copied = ta.copy_contents(&mut dest);
    let mut storage = [0u8; 8];
    let contents_len = ta.get_contents(&mut storage).len();
    let js_length = eval_text(scope, "String(ta.length)");
    let js_element = eval_text(scope, "String(ta[0])");
    let js_byte_offset = eval_text(scope, "String(ta.byteOffset)");
    let js_byte_length = eval_text(scope, "String(ta.byteLength)");

    let actual = Json::obj(vec![
        ("detach_ok", Json::b(detach_ok)),
        ("length", Json::i(ta.length() as i64)),
        ("byte_length", Json::i(ta.byte_length() as i64)),
        ("byte_offset", Json::i(ta.byte_offset() as i64)),
        ("has_buffer", Json::b(ta.has_buffer())),
        ("buffer_identity", Json::b(buffer_identity)),
        ("data_is_null", Json::b(ta.data().is_null())),
        (
            "copy",
            Json::obj(vec![
                ("copied", Json::i(copied as i64)),
                ("sentinel_intact", Json::b(dest.iter().all(|b| *b == 0xEE))),
            ]),
        ),
        ("get_contents_len", Json::i(contents_len as i64)),
        ("js_length", Json::s(&js_length)),
        ("js_element", Json::s(&js_element)),
        ("js_byte_offset", Json::s(&js_byte_offset)),
        ("js_byte_length", Json::s(&js_byte_length)),
    ]);

    let expected = Json::obj(vec![
        ("detach_ok", Json::b(true)),
        ("length", Json::i(0)),
        ("byte_length", Json::i(0)),
        // Pinned: the engine's ArrayBufferView::ByteOffset()/ByteLength()
        // return 0 for detached views rather than the stored geometry.
        ("byte_offset", Json::i(0)),
        ("has_buffer", Json::b(true)),
        ("buffer_identity", Json::b(true)),
        ("data_is_null", Json::b(true)),
        (
            "copy",
            Json::obj(vec![
                ("copied", Json::i(0)),
                ("sentinel_intact", Json::b(true)),
            ]),
        ),
        ("get_contents_len", Json::i(0)),
        ("js_length", Json::s("0")),
        ("js_element", Json::s("undefined")),
        ("js_byte_offset", Json::s("0")),
        ("js_byte_length", Json::s("0")),
    ]);

    vec![expect_eq("typedarrays/detached_view", expected, actual)]
}

/// DataView as an ArrayBufferView: byte-granular geometry (odd offsets are
/// legal, unlike typed arrays), offset-aware copies and get_contents, and
/// element access through the JS DataView prototype.
#[allow(clippy::too_many_lines)]
fn data_view_surface() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = ab16(scope);
    let store = ab.get_backing_store();
    for i in 0..16 {
        store[i].set((i * 16 + 1) as u8);
    }

    let dv = v8::DataView::new(scope, ab, 3, 9);
    let view = to_view(value_of(dv));
    let buffer_identity = view.buffer(scope).map(|b| b == ab).unwrap_or(false);
    let mut storage = [0u8; 16];
    let contents = view.get_contents(&mut storage);
    let contents_ptr_is_data = contents.as_ptr() == view.data().cast::<u8>();
    // get_contents returns a LIVE slice over the backing store (not a
    // snapshot): pre-write it shows the seeded bytes; the same slice is
    // re-read after the JS writes below and reflects them.
    let contents_pre_write = contents.to_vec();
    let mut dest = [0u8; 16];
    let copied = view.copy_contents(&mut dest);

    assert!(set_global(scope, "dv", dv.into()));
    let js_get = eval_text(scope, "String(dv.getUint8(0))");
    let js_set_get = eval_text(scope, "dv.setUint16(0, 0xBEEF); String(dv.getUint16(0))");
    let js_geometry = eval_text(scope, "`${dv.byteOffset},${dv.byteLength}`");
    let store_sees_js_write = hex16(&[
        store[3].get(),
        store[4].get(),
        store[5].get(),
        store[6].get(),
        store[7].get(),
    ]);

    let actual = Json::obj(vec![
        ("byte_offset", Json::i(view.byte_offset() as i64)),
        ("byte_length", Json::i(view.byte_length() as i64)),
        ("buffer_identity", Json::b(buffer_identity)),
        (
            "get_contents",
            Json::obj(vec![
                ("len", Json::i(contents.len() as i64)),
                ("pre_write", Json::s(&hex(&contents_pre_write))),
                // Re-read of the SAME slice after the JS writes: live view.
                ("post_write", Json::s(&hex(contents))),
                ("ptr_is_data", Json::b(contents_ptr_is_data)),
            ]),
        ),
        (
            "copy",
            Json::obj(vec![
                ("copied", Json::i(copied as i64)),
                ("bytes", Json::s(&hex(&dest))),
            ]),
        ),
        ("js_get", Json::s(&js_get)),
        ("js_set_get", Json::s(&js_set_get)),
        ("js_geometry", Json::s(&js_geometry)),
        ("store_sees_js_write", Json::s(&store_sees_js_write)),
    ]);

    let mut contents_expected = [0u8; 9];
    for (i, byte) in contents_expected.iter_mut().enumerate() {
        *byte = ((3 + i) * 16 + 1) as u8;
    }
    // store[3..8] after `dv.setUint16(0, 0xBEEF)`: DataView set/get default
    // to BIG-endian, so offsets 3,4 hold BE EF, then the seeded 0x51, 0x61,
    // 0x71 (store[i] = i*16+1).
    let store_after_js_write = hex16(&[0xBE, 0xEF, 0x51, 0x61, 0x71]);
    let expected = Json::obj(vec![
        ("byte_offset", Json::i(3)),
        ("byte_length", Json::i(9)),
        ("buffer_identity", Json::b(true)),
        (
            "get_contents",
            Json::obj(vec![
                ("len", Json::i(9)),
                ("pre_write", Json::s(&hex(&contents_expected))),
                // Re-read of the SAME slice after `dv.setUint16(0, 0xBEEF)`:
                // DataView set/get default to BIG-endian, so the first two
                // seeded bytes become BE EF; the rest is the live store.
                ("post_write", Json::s("beef5161718191a1b1")),
                ("ptr_is_data", Json::b(true)),
            ]),
        ),
        (
            "copy",
            Json::obj(vec![
                ("copied", Json::i(9)),
                ("bytes", Json::s(&hex16(&contents_expected))),
            ]),
        ),
        ("js_get", Json::s("49")),
        ("js_set_get", Json::s("48879")),
        ("js_geometry", Json::s("3,9")),
        ("store_sees_js_write", Json::s(&store_after_js_write)),
    ]);

    vec![expect_eq("typedarrays/data_view_surface", expected, actual)]
}

/// SharedArrayBuffer-backed views (created from JS; the crate binds no
/// native SAB path): geometry, the masquerading `buffer()` result, shared
/// backing-store access, byte-level visibility in both directions, and the
/// JS RangeError path for invalid SAB view construction.
#[allow(clippy::too_many_lines)]
fn sab_views() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let bs = v8::SharedRef::from(v8::SharedArrayBuffer::new_backing_store(scope, 16));
    let sab = v8::SharedArrayBuffer::with_backing_store(scope, &bs);
    assert!(set_global(scope, "sab", sab.into()));
    let view_value = eval(scope, "new Uint16Array(sab, 4, 4)").unwrap();
    assert!(set_global(scope, "ta", view_value));

    let view = view_value.try_cast::<v8::ArrayBufferView>().ok().unwrap();
    let typed = view_value.try_cast::<v8::TypedArray>().ok().unwrap();

    // The masquerade: view.buffer() hands back a Local<ArrayBuffer> for a
    // SharedArrayBuffer object. ArrayBuffer::byte_length/was_detached work
    // directly on it, but the value predicates reveal what it really is
    // (is_shared_array_buffer, and NOT is_array_buffer).
    let buffer = view.buffer(scope);
    let buffer_is_some = buffer.is_some();
    let (buf_is_sab, buf_is_ab, buf_len, buf_detached) = match buffer {
        Some(b) => {
            let len = b.byte_length();
            let detached = b.was_detached();
            let value: v8::Local<v8::Value> = b.into();
            (
                value.is_shared_array_buffer(),
                value.is_array_buffer(),
                len,
                detached,
            )
        }
        None => (false, false, 0, false),
    };
    let store_via_view = view.get_backing_store();
    let (view_store_shared, view_store_len) = store_via_view
        .as_ref()
        .map(|s| (s.is_shared(), s.byte_length()))
        .unwrap_or((false, 0));

    // Rust -> JS through the shared store.
    bs[4].set(0x34);
    bs[5].set(0x12);
    let js_view_read = eval_text(scope, "String(ta[0])");
    // Pinned quirk: in this build a SharedArrayBuffer does NOT expose
    // integer-indexed element access from script (`sab[4]` is undefined);
    // reads go through views.
    let sab_direct_index = eval_text(scope, "String(sab[4])");
    let js_view_byte_read = eval_text(scope, "String(new Uint8Array(sab)[4])");
    // JS -> Rust through the view.
    eval(scope, "ta[1] = 48879;").unwrap();
    let mut bytes = [0u8; 8];
    let copied = view.copy_contents(&mut bytes);

    // JS construction errors over a SAB (RangeError, never a native abort).
    let misaligned = eval_text(
        scope,
        "try { new Float64Array(sab, 4, 1); 'no-error' } \
         catch (e) { e.constructor.name + ': ' + e.message }",
    );
    let out_of_bounds = eval_text(
        scope,
        "try { new Uint8Array(sab, 0, 100); 'no-error' } \
         catch (e) { e.constructor.name + ': ' + e.message }",
    );
    let offset_past_end = eval_text(
        scope,
        "try { new Uint8Array(sab, 17, 0); 'no-error' } \
         catch (e) { e.constructor.name + ': ' + e.message }",
    );

    let actual = Json::obj(vec![
        ("is_typed_array", Json::b(view_value.is_typed_array())),
        ("length", Json::i(typed.length() as i64)),
        ("byte_offset", Json::i(view.byte_offset() as i64)),
        ("byte_length", Json::i(view.byte_length() as i64)),
        ("buffer_is_some", Json::b(buffer_is_some)),
        ("buffer_is_shared_array_buffer", Json::b(buf_is_sab)),
        ("buffer_is_plain_array_buffer", Json::b(buf_is_ab)),
        ("buffer_byte_length", Json::i(buf_len as i64)),
        ("buffer_was_detached", Json::b(buf_detached)),
        ("view_store_is_some", Json::b(store_via_view.is_some())),
        ("view_store_is_shared", Json::b(view_store_shared)),
        ("view_store_byte_length", Json::i(view_store_len as i64)),
        ("js_view_read", Json::s(&js_view_read)),
        ("sab_direct_index", Json::s(&sab_direct_index)),
        ("js_view_byte_read", Json::s(&js_view_byte_read)),
        (
            "rust_sees_js_write",
            Json::obj(vec![
                ("copied", Json::i(copied as i64)),
                ("bytes", Json::s(&hex(&bytes))),
            ]),
        ),
        ("js_misaligned_error", Json::s(&misaligned)),
        ("js_out_of_bounds_error", Json::s(&out_of_bounds)),
        ("js_offset_past_end_error", Json::s(&offset_past_end)),
    ]);

    let expected = Json::obj(vec![
        ("is_typed_array", Json::b(true)),
        ("length", Json::i(4)),
        ("byte_offset", Json::i(4)),
        ("byte_length", Json::i(8)),
        ("buffer_is_some", Json::b(true)),
        // The masquerade: view.buffer() hands back a Local<ArrayBuffer> for
        // a SharedArrayBuffer object; only the value predicates can tell.
        ("buffer_is_shared_array_buffer", Json::b(true)),
        ("buffer_is_plain_array_buffer", Json::b(false)),
        ("buffer_byte_length", Json::i(16)),
        ("buffer_was_detached", Json::b(false)),
        ("view_store_is_some", Json::b(true)),
        ("view_store_is_shared", Json::b(true)),
        ("view_store_byte_length", Json::i(16)),
        ("js_view_read", Json::s("4660")),
        ("sab_direct_index", Json::s("undefined")),
        ("js_view_byte_read", Json::s("52")),
        (
            "rust_sees_js_write",
            Json::obj(vec![
                ("copied", Json::i(8)),
                ("bytes", Json::s("3412efbe00000000")),
            ]),
        ),
        (
            "js_misaligned_error",
            Json::s("RangeError: start offset of Float64Array should be a multiple of 8"),
        ),
        (
            "js_out_of_bounds_error",
            Json::s("RangeError: Invalid typed array length: 100"),
        ),
        (
            "js_offset_past_end_error",
            // Pinned: an explicit zero length past the end reports the
            // length-based message ("...length: 0"), not the offset message.
            Json::s("RangeError: Invalid typed array length: 0"),
        ),
    ]);

    vec![expect_eq("typedarrays/sab_views", expected, actual)]
}

/// JS construction errors over a plain ArrayBuffer: deterministic
/// RangeErrors with pinned messages. The native `X::new` equivalents of
/// these abort the process (see tests/typed_arrays_negative.rs) - the Go
/// wrapper must never map native misuse onto these JS-shaped errors.
fn js_error_paths() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = ab16(scope);
    assert!(set_global(scope, "ab", ab.into()));

    let probe = |scope: &mut v8::PinScope<'_, '_>, expr: &str| {
        eval_text(
            scope,
            &format!("try {{ {expr}; 'no-error' }} catch (e) {{ e.constructor.name + ': ' + e.message }}"),
        )
    };

    let actual = Json::obj(vec![
        (
            "misaligned_offset",
            Json::s(&probe(scope, "new Float64Array(ab, 4, 1)")),
        ),
        (
            "misaligned_zero_length",
            Json::s(&probe(scope, "new Int16Array(ab, 1, 0)")),
        ),
        (
            "out_of_bounds_length",
            Json::s(&probe(scope, "new Uint8Array(ab, 0, 100)")),
        ),
        (
            "offset_past_end_zero_length",
            Json::s(&probe(scope, "new Uint8Array(ab, 17, 0)")),
        ),
        (
            "data_view_out_of_bounds",
            Json::s(&probe(scope, "new DataView(ab, 2, 100)")),
        ),
        // Contrast: byte-granular odd offsets are legal from JS too.
        (
            "data_view_odd_offset_ok",
            Json::s(&eval_text(
                scope,
                "String(new DataView(ab, 3, 9).byteLength)",
            )),
        ),
    ]);

    let expected = Json::obj(vec![
        (
            "misaligned_offset",
            Json::s("RangeError: start offset of Float64Array should be a multiple of 8"),
        ),
        (
            "misaligned_zero_length",
            Json::s("RangeError: start offset of Int16Array should be a multiple of 2"),
        ),
        (
            "out_of_bounds_length",
            Json::s("RangeError: Invalid typed array length: 100"),
        ),
        (
            "offset_past_end_zero_length",
            // Pinned: an explicit zero length past the end reports the
            // length-based message ("...length: 0"), not the offset message
            // (engine typed-array-createtypedarray.tq order of checks).
            Json::s("RangeError: Invalid typed array length: 0"),
        ),
        (
            "data_view_out_of_bounds",
            Json::s("RangeError: Invalid DataView length 100"),
        ),
        ("data_view_odd_offset_ok", Json::s("9")),
    ]);

    vec![expect_eq("typedarrays/js_error_paths", expected, actual)]
}

/// Float16Array availability in this build: `js_float16array` ships on, so
/// the native constructor passes its ApiCheck, the JS constructor exists,
/// and the f16 helpers work.
#[allow(clippy::too_many_lines)]
fn float16_availability() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let ab = ab16(scope);
    assert!(set_global(scope, "ab", ab.into()));

    let native = v8::Float16Array::new(scope, ab, 0, 2);
    let native_is_some = native.is_some();
    let native_geometry = native.as_ref().map(|ta| {
        Json::obj(vec![
            ("length", Json::i(ta.length() as i64)),
            ("byte_length", Json::i(ta.byte_length() as i64)),
            ("byte_offset", Json::i(ta.byte_offset() as i64)),
        ])
    });

    let typeof_ctor = eval_text(scope, "typeof Float16Array");
    let ctor_name = eval_text(scope, "Float16Array.name");
    let js_built_len = eval_text(scope, "String(new Float16Array(3).length)");
    let f16round = eval_text(scope, "String(Math.f16round(1.5))");
    let f16round_int = eval_text(scope, "String(Math.f16round(2))");
    let dv_f16 = eval_text(
        scope,
        "const d = new DataView(ab); d.setFloat16(0, 1.5); String(d.getFloat16(0))",
    );
    let native_value = native.map(|ta| unsafe { v8::Local::cast_unchecked(ta) });
    // Native-built view, JS read (the widening used here is documented in
    // the SAFETY note on Kind::native_new).
    let native_js_read = match native_value {
        Some(value) if set_global(scope, "h", value) => {
            eval_text(scope, "h[0] = 1.5; String(h[0])")
        }
        _ => String::new(),
    };

    let actual = Json::obj(vec![
        ("native_new_is_some", Json::b(native_is_some)),
        ("native_geometry", native_geometry.unwrap_or(Json::Null)),
        ("typeof_ctor", Json::s(&typeof_ctor)),
        ("ctor_name", Json::s(&ctor_name)),
        ("js_built_length", Json::s(&js_built_len)),
        ("f16round_1_5", Json::s(&f16round)),
        ("f16round_2", Json::s(&f16round_int)),
        ("data_view_set_get_float16", Json::s(&dv_f16)),
        ("native_view_js_roundtrip", Json::s(&native_js_read)),
    ]);

    let expected = Json::obj(vec![
        ("native_new_is_some", Json::b(true)),
        (
            "native_geometry",
            Json::obj(vec![
                ("length", Json::i(2)),
                ("byte_length", Json::i(4)),
                ("byte_offset", Json::i(0)),
            ]),
        ),
        ("typeof_ctor", Json::s("function")),
        ("ctor_name", Json::s("Float16Array")),
        ("js_built_length", Json::s("3")),
        ("f16round_1_5", Json::s("1.5")),
        ("f16round_2", Json::s("2")),
        ("data_view_set_get_float16", Json::s("1.5")),
        ("native_view_js_roundtrip", Json::s("1.5")),
    ]);

    vec![expect_eq(
        "typedarrays/float16_availability",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// Runner.
// ---------------------------------------------------------------------------

const CHECKS: &[fn() -> Vec<CheckOutcome>] = &[
    kind_predicates,
    constants,
    element_sizes,
    native_geometry,
    read_bit_patterns,
    write_bit_patterns,
    js_view_geometry,
    view_surface,
    copy_contents_bounds,
    detached_view,
    data_view_surface,
    sab_views,
    js_error_paths,
    float16_availability,
];

fn main() -> ExitCode {
    oracle::ensure_v8();
    let mut outcomes = Vec::new();
    for check in CHECKS {
        outcomes.extend(check());
    }
    let total = outcomes.len();
    let mut passed = 0usize;
    let mut text = String::new();
    for outcome in &outcomes {
        if outcome.passed() {
            passed += 1;
        }
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    let failed = total - passed;
    text.push_str(&summary_line(total, passed, failed));
    text.push('\n');

    let stdout = std::io::stdout();
    let mut lock = stdout.lock();
    let _ = lock.write_all(text.as_bytes());
    let _ = lock.flush();
    if failed == 0 {
        ExitCode::SUCCESS
    } else {
        ExitCode::FAILURE
    }
}
