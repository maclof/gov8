# conformance-serializer-delegates

Go re-implementation of the 25 serializer/deserializer **delegate**
checks of the pinned Rust oracle
(`rust-oracle/src/bin/conformance-serializer-delegates.rs`, crate
`v8 =152.2.0`). The runner executes the checks in the fixed oracle
registry order and compares the normalized JSON-lines report
byte-for-byte against the pinned fixture
`rust-oracle/tests/fixtures/conformance-serializer-delegates-v8_152.2.0_x86_64-pc-windows-msvc.jsonl`.

```
go test ./conformance-serializer-delegates/ -count=1
go test ./conformance-serializer-delegates/ -run TestSerdelFixture -emit report.jsonl   # review artifact
```

Scope covered by the delegate surface (`serializer_delegates.go` +
`internal/shim/features/serialization_delegates.inc`):

- host-object detection pipeline (`has_custom_host_object`,
  `is_host_object`, embedder-field fallback, object-id short-circuit),
- `write_host_object` / `read_host_object` with the helper
  read/write primitives (`write_uint32/uint64/double/raw_bytes`,
  `read_uint32/uint64/double/raw_bytes`, `get_wire_format_version`),
- SAB transfer ids on write and read (including the pinned rejection of a
  silent `None`, surfaced as V8's own
  `#<SharedArrayBuffer> could not be cloned.`),
- delegate-only Wasm transfer semantics (no Wasm API exposed),
- writer/reader transfer collisions (last registration wins on each side),
- serializer output-buffer ownership (`realloc`/`free` inside the shim,
  idempotent-empty second `release`, drop-without-release frees through
  the delegate),
- data-clone-error reporting with and without rethrow, serializer state
  after a clone error, and explicit `read_header` /
  `get_wire_format_version` semantics.

Panic/fatal delegate boundaries are characterized out-of-process by the
root module's `serializer_delegates_negative_test.go` (fail-fast abort,
exit code 0xC0000409, exactly like the oracle's extern-"C" panic
boundary). Benchmark workloads matching the oracle's spec live in
`serializer_delegates_bench_test.go`; raw samples:
`bench-results.txt`.
