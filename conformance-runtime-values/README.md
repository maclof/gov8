# conformance-runtime-values

Go reproduction of the pinned Rust runtime-values oracle registry
(`rust-oracle/src/bin/conformance-runtime-values.rs`, crate `v8 =152.2.0`,
engine `15.2.124.1-rusty`, target `x86_64-pc-windows-msvc`).

## What is pinned

The 27-check normalized JSON-lines report
(`rust-oracle/tests/fixtures/conformance-runtime-values-v8_152.2.0_x86_64-pc-windows-msvc.jsonl`)
is reproduced byte-for-byte by `TestRuntimeValuesFixture`:

- Date: native construction/value_of, JS interop and mutation reflection,
  invalid-time NaN boundary, and the deterministic
  `RangeError: Invalid time value` (`date_construction_and_value_of`,
  `date_invalid_time_value_error`)
- RegExp: flags/source round trip, exec lastIndex semantics for global and
  sticky patterns (including the Some(null) miss shape), invalid-pattern
  SyntaxError parity with the JS constructor, and JS-created regexps
  (`regexp_*`, 4 checks)
- JSON: canonical parse/stringify round trips with number and
  lone-surrogate boundaries, the five pinned parse SyntaxErrors, stringify
  omission/holes/escapes/toJSON, and the C++-specific top-level
  "undefined" rendering plus the circular-structure TypeError
  (`json_*`, 4 checks)
- Array: the negative-length native boundary (collapses to empty; the JS
  constructor throws), elements transfer, index vs named property
  semantics, and the 2^32-1 length saturation (`array_*`, 2 checks)
- Map / Set: SameValueZero keys (NaN, +0/-0), insertion-order as_array,
  returned-handle identity, JS interop in both directions
  (`map_native_ops`, `set_native_ops`, `map_set_js_interop`)
- Proxy: target/handler identity, default trap forwarding, revoke
  semantics (target degrades to the null value), revoked-proxy and
  trap-invariant TypeErrors, JS `Proxy.revocable` observed natively
  (`proxy_*`, 3 checks)
- Symbol and private keys: identity/description, ToString-throw nuance,
  the `Symbol.for` and embedder registries, well-known symbol interop
  (toStringTag/iterator/hasInstance), and the JS-invisibility of private
  symbols (`symbol_*`, `private_symbol_invisibility`, 4 checks)
- Primitive wrapper objects: predicate split, `new Boolean(false)`
  truthiness nuance, conversions (`primitive_wrapper_objects`)
- Property attributes / integrity / descriptors: attribute bit round trip
  with the Just(NONE) missing-property nuance, seal/freeze observable
  through attributes and JS predicates, the native PropertyDescriptor
  flavors (with the define_property backfill ordering), and
  getOwnPropertyDescriptor/GetPropertyNames filters
  (`property_attributes_bits`, `integrity_levels`,
  `native_property_descriptors`, `js_property_descriptor_view`)

## Deviations

None in observable output: all 27 lines match the pinned fixture
byte-for-byte. API-shape differences (documented in `runtime_values.go`)
are behavior-preserving: `Option<Local<T>>` maps to `(T, error)`,
`Maybe<bool>` maps to `(bool, error)`, and the descriptor snapshot in
`native_property_descriptors` is taken before the define_property call
exactly like the Rust runner (v8 backfills descriptor fields when a
define consumes them).

## Run

```
go test ./conformance-runtime-values -v
```

This runner performs no platform shutdown, exactly like the oracle's
runtime-values runner.
