# rusty_v8 public API audit

This declaration-level audit complements `PARITY.md` and prevents fixture
coverage from being mistaken for complete upstream API coverage.

Reference: Rust `v8 = 152.2.0`, V8 `15.2.124.1-rusty`, target
`x86_64-pc-windows-msvc`, audited 2026-09-01.

The checked-in [declaration ledger](audit/v8_152_2_0_declarations.csv) records
the stable source ID, Rust path, kind, classification, Go mapping, evidence and
rationale for every included declaration. Generated binding enum-value noise,
deref-inherited duplicates and blanket trait methods are excluded. Public
rustdoc items, inherent methods and constants, fields, enum variants and trait
members are included. Small generated wrapper declarations that rusty_v8
publicly reexports remain in the ledger.

| Classification | Declarations | Percent |
|---|---:|---:|
| Matched Go equivalent or documented semantic shape | 1,696 | 91.3% |
| Partial language-shape difference with safe behavioral equivalent | 10 | 0.5% |
| Missing executable surface | 0 | 0.0% |
| Unsafe or intentionally unsupported Rust ownership shape | 151 | 8.1% |
| Ambiguous pending exact executable evidence | 0 | 0.0% |
| Total | 1,857 | 100% |

The confirmed executable declaration remainder is zero. There is no remaining
ambiguous bucket. The ten partial declarations have safe behavioral Go
equivalents but not literal Rust borrowed/generic API shapes. The 151
intentionally unsupported declarations are not implementation backlog unless
the project chooses a comparably safe Go abstraction.

## Source-family inventory

| Rust source family | Public declarations | Current mapping |
|---|---:|---|
| `V8.rs` | 16 | Lifecycle, flags, version and process hooks matched; raw platform-handle shape hidden |
| `platform.rs` | 21 | Platforms, transferred task dispatch, pumping and matched custom-dispatch benchmark covered |
| `isolate.rs` | 212 | Safe surface broadly matched; raw pointers, explicit enter/exit and ownership machinery hidden |
| `isolate_create_params.rs` | 22 | Safe fields and snapshot/allocator/cppgc composition matched; raw stack-pointer input intentionally hidden |
| `locker.rs` | 5 | Matched |
| `scope.rs` | 86 | Safe scopes/TryCatch/guards matched; Rust pinning and unsafe lifetime machinery hidden |
| `handle.rs` | 38 | Managed handles matched; raw `Local`/`SealedLocal` and unchecked casts hidden |
| `support.rs` | 22 | Rust `UniqueRef`/`SharedRef`, mapping and vtable machinery intentionally absent |
| `context.rs` and `microtask.rs` | 11 | Matched |
| `data.rs` | 528 | Value hierarchy, predicates, conversions and specialized values matched |
| Object/property families | 78 | Safe object and property operations matched |
| `regexp.rs` and `string.rs` | 103 | Matched with checked and owned Go shapes |
| `array_buffer.rs` | 14 | Buffer behavior and safe allocator ownership matched; raw generic vtable fields remain intentionally hidden |
| `function.rs` and `template.rs` | 140 | Normal callbacks/templates matched; Fast API tracked separately |
| Script/compiler/module families | 80 | Safe surface matched, including dynamic-import `kDefer` delivery |
| Promise and snapshot families | 19 | Safe behavior matched, including allocator and custom-heap snapshot composition |
| Serializer/deserializer | 40 | Matched |
| `wasm.rs` | 28 | Safe executable surface matched, including positive serialized-cache behavior |
| `inspector.rs` and `crdtp.rs` | 146 | Safe owned/closed surface matched; raw boxed/vtable representations hidden |
| `cppgc.rs` | 60 | Safe executable behavior matched, including arbitrary copied generic state, indexed strong/weak graphs, V8 traced references, tracing/name callbacks, persistent handles and custom heaps; ten literal borrowed/generic shapes remain partial |
| `fast_api.rs` | 75 | Descriptor, constructor and flag execution matched; callback-local borrowed ABI shapes are intentionally unsupported |
| `simdutf.rs`, `icu.rs` and `json.rs` | 83 | Matched |
| `external_references.rs` | 16 | Matched for the supported native-address shape |
| `lib.rs` constants/macros | 13 | Constants matched; Rust lexical-scope macros have no Go analogue |

Some related low-count files are grouped above. These semantic family groups
total 1,856 declarations. The ledger adds the one source-visible declaration
rustdoc omits, `FastApiOneByteString::as_bytes`, for a reproducible total of
1,857. The former 1,858 total contained one unexplained matched row: it had no
Rust declaration, stable ID or evidence and has been removed. No executable Go
surface changed as a result.

## Classification boundary

The 151 unsafe rows are individually identified in the ledger. Their exact
rationale totals are: 49 Rust pinning/lexical-scope construction declarations,
32 generic smart-pointer or mapping-vtable declarations, 23 raw or unchecked
handle declarations, 15 raw isolate/manual-entry declarations, 10 generated
ABI-layout declarations, 9 raw allocator/backing-pointer declarations, 6
callback-borrowed Fast API declarations, 4 raw Inspector wrapper/iterator
declarations, 2 raw stack-pointer declarations and 1 Rust `SharedRef` platform
ownership declaration. Safe behavior above these raw shapes is classified and
tested separately rather than counted as a raw Go API.

The row-level reconciliation found no missing safe executable declaration. It
also corrected three stale unsafe classifications for APIs already implemented
and covered in Go: `Global::into_raw`, `Global::from_raw`, and
`ArrayBuffer::new_backing_store_from_ptr`.

## Confirmed residual language-shape differences

No executable declaration remains missing. The ten partial declarations are:

1. `cppgc::Visitor` and `Visitor::trace`: Rust exposes a callback-borrowed GC
   visitor; Go keeps visitation native and exposes declarative indexed traced
   edges, now exercised by the generic-breadth oracle.
2. `cppgc::Traced` and `Traced::trace`: Rust's freely implementable generic
   trait maps to native tracing of the Go facade's configured members and V8
   traced reference.
3. `cppgc::InternalFieldIndex`: Rust exposes the raw alias; Go uses the fixed
   native API-wrapper field contract.
4. `cppgc::UnsafePtr<T>`, `UnsafePtr::new` and `UnsafePtr::as_ref`: Go never
   exposes an unrooted raw cppgc pointer or unchecked borrowed reference.
5. Generic `cppgc::Member<T>` and `cppgc::WeakMember<T>` types: Go provides
   owner-mediated indexed strong and weak member operations instead of freely
   composable fields; mutation barriers and weak clearing are exact.

The generic-breadth fixture now closes the executable behavior behind six of
these ten declarations: `Visitor`, `Visitor::trace`, `Traced`, `Traced::trace`,
`Member<T>` and `WeakMember<T>`. The strict language-shape count remains ten
because Go intentionally exposes none of those borrowed Rust types directly.
The other four are `InternalFieldIndex` and the three `UnsafePtr` declarations;
they provide no missing safe executable behavior and remain deliberately raw.

These are literal API-shape differences, not unimplemented safe behavior. The
Fast API residual oracle resolved its former ambiguous and partial buckets; six
callback-local native/borrowed-pointer Fast API items are classified as unsafe
or intentionally unsupported.

## Reproduction

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify_api_audit_ledger.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify_api_audit_ledger.ps1 -Regenerate
```

The script generates pinned rustdoc JSON with `cargo rustdoc`, reconstructs
associated-item owners, validates every stable source ID and cited repository
file, and checks the exact `1,696 + 10 + 151 = 1,857` arithmetic. `-Regenerate`
canonicalizes row order and source-derived columns while retaining the reviewed
classification fields. `FastApiOneByteString::as_bytes` is restored explicitly
because rustdoc hides inherent methods behind that generated alias.
