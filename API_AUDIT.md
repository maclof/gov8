# rusty_v8 public API audit

This declaration-level audit complements `PARITY.md` and prevents fixture
coverage from being mistaken for complete upstream API coverage.

Reference: Rust `v8 = 152.2.0`, V8 `15.2.124.1-rusty`, target
`x86_64-pc-windows-msvc`, audited 2026-09-02.

The checked-in [declaration ledger](audit/v8_152_2_0_declarations.csv) records
the stable source ID, Rust path, kind, classification, Go mapping, evidence and
rationale for every included declaration. Generated binding enum-value noise,
deref-inherited duplicates and blanket trait methods are excluded. Public
rustdoc items, inherent methods and constants, fields, enum variants and trait
members are included. Small generated wrapper declarations that rusty_v8
publicly reexports remain in the ledger.

| Classification | Declarations | Percent |
|---|---:|---:|
| Matched Go equivalent or documented semantic shape | 1,687 | 90.8% |
| Partial language-shape difference with safe behavioral equivalent | 10 | 0.5% |
| Incomplete safe executable surface | 11 | 0.6% |
| Intentional raw/borrowed/generic Rust shape (ledger status `unsafe`) | 149 | 8.0% |
| Ambiguous pending exact executable evidence | 0 | 0.0% |
| Total | 1,857 | 100% |

The ledger has 21 `partial` rows: ten declarations have safe behavioral Go
equivalents but not literal Rust borrowed/generic API shapes, while eleven have
confirmed safe executable remainder. There is no ambiguous bucket. The ledger's
`unsafe` status is a project taxonomy for 149 intentionally unexposed carrier
shapes; it does not mean every corresponding Rust declaration is an `unsafe
fn`. These `unsafe`-status rows are not implementation backlog unless the
project chooses a comparably safe Go abstraction.

## Source-family inventory

| Rust source family | Public declarations | Current mapping |
|---|---:|---|
| `V8.rs` | 16 | Lifecycle, flags, version and process hooks matched; raw platform-handle shape hidden |
| `platform.rs` | 21 | Platforms, transferred task dispatch, pumping and custom-dispatch benchmark covered; the trait's five synchronous default methods are not matched |
| `isolate.rs` | 212 | Safe surface broadly matched; raw pointers, explicit enter/exit and ownership machinery hidden |
| `isolate_create_params.rs` | 22 | Safe fields and snapshot/allocator/cppgc composition matched; raw stack-pointer input intentionally hidden |
| `locker.rs` | 5 | Matched |
| `scope.rs` | 86 | Safe scopes/TryCatch/guards broadly matched; current and entered-or-microtask getters return identity-only `ContextRef` values rather than usable Context handles; Rust pinning and unsafe lifetime machinery hidden |
| `handle.rs` | 38 | Managed handles broadly matched; checked generic cast coverage lacks Promise conversion, while raw `Local`/`SealedLocal` and unchecked casts remain hidden |
| `support.rs` | 22 | Rust `UniqueRef`/`SharedRef`, mapping and vtable machinery intentionally absent |
| `context.rs` and `microtask.rs` | 11 | Matched |
| `data.rs` | 528 | Value hierarchy, predicates, conversions and specialized values matched |
| Object/property families | 78 | Safe object and property operations broadly matched; creation-context comparison exists but the Context-returning getter remains incomplete |
| `regexp.rs` and `string.rs` | 103 | Matched with checked and owned Go shapes |
| `array_buffer.rs` | 14 | Buffer behavior and safe allocator ownership matched; raw generic vtable fields remain intentionally hidden |
| `function.rs` and `template.rs` | 140 | Normal callbacks/templates matched; Fast API tracked separately |
| Script/compiler/module families | 80 | Safe surface matched, including dynamic-import `kDefer` delivery |
| Promise and snapshot families | 19 | Resolver-created Promise behavior and snapshot composition are matched; conversion of a JavaScript-produced Value into the typed Promise API is tracked under `handle.rs` |
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

The 149 `unsafe`-status rows are individually identified in the ledger. Their
exact rationale totals are: 49 Rust pinning/lexical-scope construction
declarations,
32 generic smart-pointer or mapping-vtable declarations, 21 raw or unchecked
handle declarations, 15 raw isolate/manual-entry declarations, 10 generated
ABI-layout declarations, 9 raw allocator/backing-pointer declarations, 6
callback-borrowed Fast API declarations, 4 raw Inspector wrapper/iterator
declarations, 2 raw stack-pointer declarations and 1 Rust `SharedRef` platform
ownership declaration. Safe behavior above these raw shapes is classified and
tested separately rather than counted as a raw Go API.

Forty-seven of the 71 function rows in this bucket are safe Rust methods on an
intentionally absent carrier, including `Local::new`, pinning
scaffolding, smart-pointer accessors, `FastApiOneByteString::as_bytes`, and
`V8::get_current_platform`. Their executable behavior is mapped through the
safe Go handles, conversions, scopes, callbacks, and platform operations named
in each ledger row. Conversely, the three explicitly unsafe `UnsafePtr` rows
remain in the partial bucket because Go supplies an owner-mediated behavioral
equivalent. The status names classify Go API-shape treatment, not Rust syntax.

The row-level reconciliation found eleven incomplete safe executable
declarations: the `PlatformImpl` trait and its five default methods, two active
Context getters, `Object::get_creation_context`, and the two safe `Local` cast
methods whose Go coverage lacks checked Promise conversion. It also preserves
the earlier correction of three stale unsafe classifications for APIs already
implemented and covered in Go: `Global::into_raw`, `Global::from_raw`, and
`ArrayBuffer::new_backing_store_from_ptr`.

## Confirmed residual language-shape differences

Ten of the 21 partial declarations are intentional language-shape differences:

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
callback-local native/borrowed-pointer Fast API items carry the intentional
`unsafe`-shape status.

## Confirmed safe executable gaps

The other eleven `partial` rows are implementation or behavior remainder:

1. `platform::PlatformImpl` and its five task-posting methods: custom dispatch
   is implemented, but Go's interface requires explicit methods and its nil
   function adapters close transferred work. Rust provides five synchronous
   defaults that run ordinary, non-nestable and delayed tasks immediately and
   run idle tasks with deadline zero.
2. `PinnedRef::get_current_context` and
   `PinnedRef::get_entered_or_microtask_context`: Go exposes identity-only
   `ContextRef` getters, but they do not provide usable returned Context handles.
3. `Object::get_creation_context`: Go can compare an object's creation Context
   with a supplied Context, but cannot return the Context itself.
4. Safe `Local::try_cast` and `Local::cast`: checked conversions cover many Go
   value wrappers, but there is no checked Value-to-Promise conversion, so the
   generic safe cast surface is incomplete.

These eleven rows are safe executable backlog, not raw-carrier API-shape
differences. Together the classification arithmetic is
`1,687 matched + 21 partial + 149 unsafe-status = 1,857`, with the partial
bucket split into ten intentional shape differences and eleven executable gaps.

## Reproduction

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify_api_audit_ledger.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify_api_audit_ledger.ps1 -Regenerate
```

The script generates pinned rustdoc JSON with `cargo rustdoc`, reconstructs
associated-item owners, and validates every stable source ID and cited
repository file. The reviewed ledger arithmetic is
`1,687 + 21 + 149 = 1,857`. `-Regenerate`
canonicalizes row order and source-derived columns while retaining the reviewed
classification fields. `FastApiOneByteString::as_bytes` is restored explicitly
because rustdoc hides inherent methods behind that generated alias.
