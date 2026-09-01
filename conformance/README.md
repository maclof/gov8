# gov8 conformance runner

Re-implements the pinned Rust oracle's 34 conformance checks
(`../rust-oracle/src/checks`, fixed order) on top of the Go binding and
compares the normalized JSON-lines report **byte-for-byte** against the
checked-in fixture:

```
../rust-oracle/tests/fixtures/conformance-v8_152.2.0_x86_64-pc-windows-msvc.jsonl
```

## Run

```
go test ./conformance -v
```

The runner walks the full process lifecycle in one ordered pass —
Initialize, 32 checks, `V8::Dispose()` (must return true), then
`DisposePlatform` — exactly like the oracle's `run_all`.

Emit the report (for future fixture-regeneration reviews):

```
go test ./conformance -run TestConformanceFixture -emit report.jsonl
```

## Encoding rules (must match `rust-oracle/src/json.rs`)

- Objects: insertion-ordered keys, no whitespace.
- Strings: minimal escaping (`\"`, `\\`, `\n`, `\r`, `\t`, `\b`, `\f`,
  `\u00XX` for control characters below 0x20), raw UTF-8 otherwise.
- Integers: plain decimal. Floats: shortest round-trip plain decimal, never
  exponent notation, no trailing `.0`, magnitudes restricted to
  `[1e-4, 1e15)`. JS-side number formatting is captured through ECMAScript
  `ToString` and stored as strings.

## Status

34/34 checks reproduce the pinned fixture byte-for-byte on Windows amd64
(V8 15.2.124.1-rusty, artifact sha256
`0b17ca072bae37dd4ff00e6014d2b413becb031c9342ee11cb8226a5881f62b2`).

Known API-scope gaps relative to the full Rust crate (not fixture checks):
external strings, native callbacks/function templates, snapshots,
value serializer/delegates, inspector, and wasm APIs are not part of this
slice. The shim contains fail-loud stubs for Rust-side callback symbols the
artifact references at link time; they abort the process if ever reached,
which cannot happen through the public Go API.
