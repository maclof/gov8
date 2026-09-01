# rusty_v8 public API audit

This declaration-level audit complements `PARITY.md` and prevents fixture
coverage from being mistaken for complete upstream API coverage.

Reference: Rust `v8 = 152.2.0`, V8 `15.2.124.1-rusty`, target
`x86_64-pc-windows-msvc`, audited 2026-09-01.

Generated bindings, deref-inherited duplicates, blanket trait methods and
private-module internals are excluded. Public rustdoc items, inherent methods
and constants, fields, enum variants and trait members are included.

| Classification | Declarations | Percent |
|---|---:|---:|
| Matched Go equivalent or documented semantic shape | 1,639 | 88.2% |
| Partial behavior or evidence | 20 | 1.1% |
| Missing executable surface | 42 | 2.3% |
| Unsafe or intentionally unsupported Rust ownership shape | 149 | 8.0% |
| Ambiguous pending exact executable evidence | 8 | 0.4% |
| Total | 1,858 | 100% |

The confirmed executable remainder is 62 declarations. Including the eight
ambiguous declarations gives an upper bound of 70. The 149 intentionally
unsupported declarations are not implementation backlog unless the project
chooses a comparably safe Go abstraction.

## Source-family inventory

| Rust source family | Public declarations | Current mapping |
|---|---:|---|
| `V8.rs` | 16 | Lifecycle, flags, version and process hooks matched; raw platform-handle shape hidden |
| `platform.rs` | 21 | Platforms and task pumping matched; shutdown-notification shape and dispatch overhead remain |
| `isolate.rs` | 212 | Safe surface broadly matched; raw pointers, explicit enter/exit and ownership machinery hidden |
| `isolate_create_params.rs` | 22 | Safe fields matched; cppgc heap transfer and raw allocator/stack inputs remain |
| `locker.rs` | 5 | Matched |
| `scope.rs` | 86 | Safe scopes/TryCatch/guards matched; Rust pinning and unsafe lifetime machinery hidden |
| `handle.rs` | 38 | Managed handles matched; raw `Local`/`SealedLocal` and unchecked casts hidden |
| `support.rs` | 22 | Rust `UniqueRef`/`SharedRef`, mapping and vtable machinery intentionally absent |
| `context.rs` and `microtask.rs` | 11 | Matched |
| `data.rs` | 528 | Value hierarchy, predicates, conversions and specialized values matched |
| Object/property families | 78 | Safe object and property operations matched |
| `regexp.rs` and `string.rs` | 103 | Matched with checked and owned Go shapes |
| `array_buffer.rs` | 14 | Buffer behavior matched; allocator factories and vtable ownership remain |
| `function.rs` and `template.rs` | 140 | Normal callbacks/templates matched; Fast API tracked separately |
| Script/compiler/module families | 80 | Safe surface matched except dynamic-import `kDefer` delivery |
| Promise and snapshot families | 19 | Safe behavior matched; allocator/cppgc gaps live under CreateParams |
| Serializer/deserializer | 40 | Matched |
| `wasm.rs` | 28 | Safe executable surface matched, including positive serialized-cache behavior |
| `inspector.rs` and `crdtp.rs` | 146 | Safe owned/closed surface matched; raw boxed/vtable representations hidden |
| `cppgc.rs` | 60 | Persistent handles matched; generic tracing, members, cells and custom heaps remain |
| `fast_api.rs` | 75 | Descriptor substrate matched; callback options, one-byte arguments and type matrix remain |
| `simdutf.rs`, `icu.rs` and `json.rs` | 83 | Matched |
| `external_references.rs` | 16 | Matched for the supported native-address shape |
| `lib.rs` constants/macros | 13 | Constants matched; Rust lexical-scope macros have no Go analogue |

Some related low-count files are grouped above. These rustdoc-family figures
precede manual macro/type-alias reconciliation; the classified 1,858-item
denominator above is authoritative.

## Confirmed residual clusters

1. cppgc custom heap, CreateParams, marking/sweeping/stack enums, collection
   and termination.
2. cppgc `Member`, `WeakMember`, `GcCell`, generic `GarbageCollected`,
   `Visitor`, `Traced` and allocation.
3. `CreateParams::cpp_heap` ownership transfer.
4. ArrayBuffer allocator types, vtable ownership and default/custom factories.
5. Executable `FastApiCallbackOptions`, including callback-local data.
6. `FastApiOneByteString::as_bytes` callback arguments.
7. Full executable Fast API type/flag matrix and safe Go callback adaptation.
8. Dynamic-import `ModuleImportPhase::kDefer` callback delivery.

The eight ambiguous declarations are Fast API generated/ABI behaviors. They
remain uncounted as matched until the dedicated Rust oracle resolves them.

## Reproduction

```powershell
cargo doc --manifest-path rust-oracle\Cargo.toml --locked -p v8 --no-deps --target-dir $env:TEMP\gov8-rustdoc-audit
go doc -all .
rg -n '^pub |^\s+pub fn|^\s+pub unsafe fn' $cargoRegistry\v8-152.2.0\src -g '*.rs'
rg -n '\*\*complete\*\*|\*\*partial\*\*|\*\*missing\*\*' PARITY.md
```

The rustdoc count stops before deref and trait-implementation sections to avoid
inherited-method multiplication. Macro-generated APIs and aliases require
manual reconciliation; `FastApiOneByteString::as_bytes` is manually restored
because rustdoc hides it behind a generated alias.
