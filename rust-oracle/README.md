# gov8 Rust Oracle

Executable behavioral oracle and benchmark reference for `gov8`. This crate
pins one exact Rust `v8` (rusty_v8) release, characterizes its observable
behavior as deterministic normalized JSON-lines, and provides reference
benchmarks that the Go implementation must be able to match.

This project is buildable independently on the sole supported platform
(Windows x86_64 MSVC): `cd rust-oracle && cargo test`. Everything needed to
reproduce the pinned build is in this directory.

## Supported platform

The oracle supports exactly one target: **Windows x86_64 MSVC**
(`x86_64-pc-windows-msvc`). This is deliberate, not an accident of the
environment:

- the pinned `v8` crate ships its prebuilt static V8 library for this target
  only, and building the engine from source (`V8_FROM_SOURCE`) is explicitly
  unsupported because it would change the pinned engine build;
- every conformance fixture and benchmark record in this directory was
  captured on this platform, so results from any other platform would not be
  comparable to the recorded evidence.

Builds for any other target fail at compile time by design: the `v8`
dependency is declared only for the supported target in `Cargo.toml`, so the
V8 artifact is never downloaded or built elsewhere, and `src/lib.rs` emits a
single `compile_error!` naming the supported target. There is no runtime,
configuration, or toolchain fallback to any other OS, ABI, or architecture.

## Pinned reference versions

| Component              | Value |
|------------------------|-------|
| `v8` crate (rusty_v8)  | `=152.2.0` (crates.io, published 2026-08-20, MIT) |
| Crate repository       | <https://github.com/denoland/rusty_v8> |
| Crate source revision  | `2768994f664e8a6e3aba27503606c58339136e2a` (from the published tarball's `.cargo_vcs_info.json`; recorded as `dirty: true`, meaning the published tarball is not byte-identical to that commit — the tarball is authoritative) |
| Underlying V8 engine   | **15.2.124.1** (`v8/include/v8-version.h` in the crate: major 15, minor 2, build 124, patch 1); runtime `V8::get_version()` reports `15.2.124.1-rusty` (crate-appended embedder suffix) |
| Prebuilt V8 artifact   | `rusty_v8_release_x86_64-pc-windows-msvc.lib.gz`, 39,957,087 bytes, SHA-256 `0b17ca072bae37dd4ff00e6014d2b413becb031c9342ee11cb8226a5881f62b2` (tag `v152.2.0` of the crate repository's GitHub releases) |
| Rust toolchain         | `1.98.0` (`rust-toolchain.toml`); rustc 1.98.0 (88d9e12ae 2026-08-18), cargo 1.98.0; target `x86_64-pc-windows-msvc` |
| Benchmark harness      | `criterion =0.8.2` (dev-dependency) |
| Cargo.lock             | Committed; 144 pinned packages; the `v8` crate has no regular dependencies beyond `bitflags`, `paste`, `temporal_capi` |

Why `152.2.0`: it is the newest published `v8` release, it ships a prebuilt
Windows x86_64 MSVC static library (no depot_tools/clang build required), and
its upstream `rust-toolchain.toml` pins 1.91.0, comfortably below the
installed 1.98.0. The edition-2024 crate builds cleanly on 1.98.0.

## Build configuration

- `Cargo.toml` declares the `v8` dependency only for
  `cfg(all(target_os = "windows", target_arch = "x86_64", target_env = "msvc"))`,
  and `src/lib.rs` enforces the same condition with a `compile_error!`; the
  two must stay in sync. Unsupported targets therefore fail early and
  clearly (see "Supported platform" above).
- `.cargo/config.toml` sets `RUSTY_V8_ARCHIVE_SHA256`; the crate's `build.rs`
  verifies this hash of the downloaded `.gz` artifact **and** on cache reuse,
  so a build either links the pinned bytes or fails loudly.
- The artifact cache lives at `%USERPROFILE%\.cargo\.rusty_v8\` (cache key
  `v152.2.0/rusty_v8_release_x86_64-pc-windows-msvc.lib.gz` with all
  non-alphanumerics escaped to `_`). Pre-populating that file makes builds
  offline.
- Default crate features are used (`use_custom_libcxx`); pointer compression
  and the sandbox are **off**. This must stay identical in Go comparisons.
- V8 runtime flags: none are set; the platform is
  `new_default_platform(0, false)` (default worker pool, no idle tasks).

## Platform caveats (Windows x86_64 MSVC)

- The crate always uses the **release** prebuilt V8 artifact on Windows, even
  for debug/test builds (`prebuilt_profile()` in `build.rs`). Debug builds
  only make the Rust binding layer slower/verifiable, never the engine.
- Linking emits `LNK4098: defaultlib 'libcmt.lib' conflicts ...` on this
  configuration (the artifact expects a static CRT flavor while rustc links
  the dynamic UCRT CRT). It is a warning only; V8's own allocations stay
  inside the static library, and all value transfer happens through V8 API
  copies. Treat any *behavioral* difference first as a V8-version difference,
  not a CRT artifact.
- The build script also links system libraries `winmm`, `dbghelp`, and
  `msvcprt`; the MSVC toolchain must be installed (VS 2022 was used here).
- `libclang`/bindgen are **not** required: without `V8_FROM_SOURCE` the
  crate uses the `src_binding_release_x86_64-pc-windows-msvc.rs` shipped in
  the published tarball under `gen/`.
- The published crate tarball vendors the full V8 C++ source under `v8/`;
  building from source (`V8_FROM_SOURCE=1`) is explicitly unsupported for
  the oracle because it would change the pinned engine build.

## Layout

```
src/lib.rs                  platform init guard + run_all()/run_host_all()
src/json.rs                 canonical JSON writer (documented rules + tests)
src/report.rs               check outcome -> JSONL line encoding
src/checks/                 ordered check registry (34 checks, 6 groups)
src/checks/host/            ordered host-interaction registry (18 checks, 6 groups)
src/bin/conformance.rs      prints the base JSONL report; exit 1 on any failure
src/bin/conformance-host.rs prints the host-interaction JSONL report (no shutdown)
src/bin/panic-boundary.rs   dedicated panic-boundary characterization executable
tests/conformance_fixture.rs  exact-output tests against the pinned base fixture
tests/conformance_host_fixture.rs exact-output tests against the pinned host fixture
tests/fixtures/             pinned normalized output (the shared contract)
tests/v8_lifecycle_negative.rs  double V8::initialize() panics (own process)
tests/v8_dispose_semantics.rs   second V8::dispose() panics (own process)
tests/callback_panic_boundary.rs panic in native callback aborts (spawns child)
tests/terminate_from_other_thread.rs cross-thread terminate_execution (own process)
benches/startup.rs          isolate/context startup benchmarks
benches/script.rs           script compile/run benchmarks
benches/callback.rs         native callback benchmarks
benches/promise.rs          native promise benchmarks
bench-results/              raw benchmark output + environment metadata
scripts/capture-env.ps1     machine metadata capture for benchmark runs
```

## Commands

Run from Windows PowerShell in the `rust-oracle` directory on the supported
platform:

```
cargo build
cargo test
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo bench --bench startup --bench script -- --save-baseline <name>
```

All of these pass as of this writing (see "Status" below).

## Conformance runner

`cargo run --bin conformance` prints one JSON object per line and a final
summary line. Every check has a hand-written expectation compiled into the
runner; a check that observes something else emits `ok:false` with
`expected`/`actual`, so failures are mechanically diffable. The checked-in
fixture under `tests/fixtures/` is the byte-exact expected output; both the
binary and the in-process library run are asserted to reproduce it, which
also proves the report is deterministic across processes.

Line format:

```
{"check":"<id>","ok":true,"value":<normalized value>}          # pass
{"check":"<id>","ok":false,"expected":<E>,"actual":<A>}        # fail
{"summary":{"total":N,"passed":P,"failed":F}}                  # final line
```

Check groups and IDs (fixed order, 34 checks):

- `platform/version_constants`, `platform/version_string`,
  `platform/current_platform_present`, `platform/dispose_returns_true`,
  `platform/dispose_platform_no_panic`
- `isolate/context_script_roundtrip`, `isolate/sequential_isolates`,
  `isolate/global_object_native_access`,
  `isolate/context_reports_default_microtask_queue`
- `values/undefined`, `values/null`, `values/booleans`,
  `values/integers`, `values/number_f64`, `values/number_special`,
  `values/string_roundtrip`, `values/value_to_string_conversions`,
  `values/bigint_roundtrip`, `values/script_number_formatting`
- `script/arithmetic`, `script/string_concat`, `script/value_types`,
  `script/script_ids_distinct_and_increasing`, `script/empty_source`
- `exceptions/syntax_error_compile_fails`,
  `exceptions/syntax_error_message_position`,
  `exceptions/runtime_reference_error`, `exceptions/runtime_type_error`,
  `exceptions/throw_string`, `exceptions/throw_error_object`,
  `exceptions/trycatch_reset_allows_continue`
- `microtasks/explicit_policy_ordering`, `microtasks/auto_policy_ordering`,
  `microtasks/native_microtask_queue`

### Host-interaction runner

`cargo run --bin conformance-host` prints the same line format for the
host-interaction slice, pinned by
`tests/fixtures/conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl`
and verified in-process via `oracle::run_host_all()`. The slice performs no
platform shutdown, so its fixture can be verified in a process that keeps
using V8 afterwards (the base `run_all()` may still run after it).

Check groups and IDs (fixed order, 18 checks):

- `template/function_template_construction`,
  `template/instance_prototype_and_constructor`,
  `template/object_template_instances`
- `callback/arguments_and_return`,
  `callback/arity_and_out_of_bounds_arguments`,
  `callback/receiver_and_callback_data`,
  `callback/construct_call_semantics`,
  `callback/native_reenters_javascript`,
  `callback/js_exception_from_native`
- `accessor/native_data_property_getter_setter`,
  `accessor/static_accessor_on_constructor`
- `external/internal_field_externals`, `external/isolate_slot_ownership`
- `promise/resolver_settlement_semantics`, `promise/native_then_checkpoint`,
  `promise/reject_callback_events`
- `lifecycle/global_clone_equality`, `lifecycle/weak_collect_forced_gc`

Regenerate it (same review discipline as the base fixture):

```
cargo run --bin conformance-host > tests\fixtures\conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl
cargo test
```

(Use byte-exact redirection; PowerShell's `>` writes UTF-16 by default.)

### Normalization rules (Go must implement the same rules)

- JSON objects: keys in insertion order, no whitespace; writers must not
  reorder.
- Strings: minimal escaping (`\"`, `\\`, `\n`, `\r`, `\t`, `\b`, `\f`,
  `\u00XX` for control characters below 0x20); other code points as raw
  UTF-8.
- Integers: plain decimal.
- Floats: shortest round-trip plain-decimal notation, never exponent
  notation, no trailing `.0` (e.g. `2.5`, `42`, `-1234.5`). Only floats with
  magnitude in `[1e-4, 1e15)` may be emitted (enforced by debug assertion in
  `src/json.rs`).
- Non-finite JS numbers are recorded as the strings `NaN`, `Infinity`,
  `-Infinity`.
- JS-side number formatting is captured via ECMAScript `ToString` and stored
  as strings (so `1e+21` stays `1e+21` regardless of the host formatter).
- No addresses, timestamps, random seeds, heap sizes, or script ids appear in
  the output.

### Regenerating the fixture

The fixture pins behavior; it must only change deliberately (from the
`rust-oracle` directory):

```
cargo run --bin conformance > tests\fixtures\conformance-v8_152.2.0_x86_64-pc-windows-msvc.jsonl
cargo test
```

Review the fixture diff like a contract change: it *is* the behavioral
contract for the Go port. The file name encodes the crate version and
target; a crate upgrade means a new fixture file, not an edit of the old
one.

## Characterized contract highlights (from this exact build)

Findings the Go side must reproduce (all encoded as checks above):

1. **`V8::initialize()` is not idempotent in this crate.** A second call
   panics with `Invalid global state` (crate-level state machine:
   `PlatformInitialized -> Initialized -> Disposed -> PlatformShutdown`).
   Raw V8 C++ `V8::Initialize()` is idempotent; the crate is stricter.
   Characterized by `tests/v8_lifecycle_negative.rs`.
2. **Shutdown ordering is enforced**: `dispose()` before
   `dispose_platform()`, each exactly once; violations panic. A second
   `dispose()` panics rather than returning false.
   Characterized by `tests/v8_dispose_semantics.rs`.
3. `V8::get_version()` / `VERSION_STRING` report `15.2.124.1-rusty` — the
   crate appends an embedder suffix to V8's version string.
4. `Message::get()` carries the `Uncaught ` prefix for exceptions caught by
   a `TryCatch`, while `ToString` of the exception value does not
   (`Uncaught boom` vs `boom`).
5. Compiling identical source twice in one isolate resolves through V8's
   compilation cache to the same Script (same `script_id`); distinct source
   produces a strictly increasing id.
6. V8's bare default context has no `queueMicrotask` global (that is an
   embedder API); promise reaction jobs are the only ES-native microtask
   source. FIFO order for a mixed script is
   `p1,p2,p3,p4,p2b,p4b` (jobs enqueued during microtask execution run after
   jobs enqueued during the script).
7. Default microtasks policy is `Auto`: with the default policy, reaction
   jobs queued by a script have run by the time `Script::run` returns; with
   `Explicit` they only run on `perform_microtask_checkpoint`. A second
   checkpoint is a no-op.
8. A default context does carry the isolate's default microtask queue
   (`Context::get_microtask_queue()` is `Some`).
9. `u64::MAX` → `BigInt` → `i64_value` yields `(-1, lossless=false)`;
   in-range i64 round-trips losslessly.
10. `TypeError`/`ReferenceError` message texts (pinned, see fixture):
    `Uncaught TypeError: Cannot read properties of null (reading 'f')`,
    `Uncaught ReferenceError: missing_thing is not defined`. Syntax error
    for `1 +`: `Uncaught SyntaxError: Unexpected end of input`; positions
    are 0-based character offsets, 1-based lines, 0-based columns.

## Host-interaction contract highlights (from this exact build)

Findings the Go side must reproduce (all encoded as checks in the host
fixture; exact crate citations in the `src/checks/host/` module docs):

1. **A Rust panic inside a native callback aborts the process.** The
   callback trampoline is an `extern "C"` fn
   (`v8` crate `src/support.rs`, `MapFnFrom for FunctionCallback`); since
   rustc 1.81 a panic unwinding out of `extern "C"` is a non-unwinding
   panic: the original message prints, then "panic in a function that
   cannot unwind", then a fail-fast abort with exit code 0xC0000409.
   Characterized out-of-process by `src/bin/panic-boundary.rs` +
   `tests/callback_panic_boundary.rs`. A Go host must therefore treat
   panics in Go callbacks as process-fatal (or convert them before
   re-entering V8), never as catchable JS exceptions.
2. **Callbacks receive sloppy-mode receivers.** A plain call `f()` gets the
   global proxy as `this`; a method call gets the object; a host
   `Function::call` gets whatever receiver was passed. `args.get(i)` is
   `undefined` for out-of-bounds indices, and `args.length()` counts only
   actually passed arguments. Builder `.length(n)` becomes `fn.length`,
   `.data(v)` is observable verbatim via `args.data()`.
3. **Construct calls**: `is_construct_call()` true, `new.target` is the
   constructor function, `args.this()` is the created instance which the
   callback can mutate (including seeding internal fields on
   template-created instances); non-object return values are ignored for
   `new`.
4. **Re-entrancy works**: a native callback may call back into JavaScript
   (`Function::call`) while V8 is inside its invocation, including nested
   host->JS->host->JS.
5. **Native-thrown exceptions behave exactly like JS `throw`**:
   `scope.throw_exception(value)` propagates to the JS caller, TryCatch
   reports the usual `Uncaught ` message prefix, and a JS `try/catch`
   observes the same exception object. The isolate stays fully usable.
6. **Accessor descriptor shape**: a native data property (setter present,
   no storage) reads via the getter, writes via the setter (no value is
   stored), and `getOwnPropertyDescriptor` synthesizes a *data-shaped*
   descriptor `{"value":<getter value>,"writable":true,"enumerable":true,
   "configurable":true}` — not an accessor descriptor.
7. **Aligned-pointer internal field tags are bounded**: the tag argument is
   an `EmbedderDataTypeTag` valid in `0..V8_EMBEDDER_DATA_TAG_COUNT`
   (`= 15`); out-of-range values abort inside V8. `External` values
   round-trip through ordinary internal fields with their raw pointer
   preserved; out-of-bounds internal field access is rejected by the crate
   (`set_internal_field` -> false, `get_internal_field` -> `None`).
8. **`PromiseResolver::resolve/reject` return bools report call success,
   not settlement change**: repeated resolve/reject on an already-settled
   resolver returns `Some(true)` and silently does nothing; state and
   result stay unchanged.
9. **Rejection notifications**: `PromiseRejectWithNoHandler` fires
   synchronously at reject time; `PromiseHandlerAddedAfterReject` fires
   synchronously when a handler is attached to a rejected promise; no event
   fires when a handler precedes the reject. The AfterResolved events were
   **removed from V8** (`kDeprecated...` in `v8-promise.h`) and never fire.
   `p.then(f)` on a rejected promise leaves the derived promise unhandled:
   when its reaction job runs it is rejected with the same reason and
   reported as a second `WithNoHandler` at the checkpoint.
10. **Weak handles need a forced major GC to observe collection.**
    `request_garbage_collection_for_testing` requires `--expose-gc` and is
    unusable under the oracle's no-flags configuration; use
    `low_memory_notification()` (synchronous major GC). With the last
    strong reference dropped, one `low_memory_notification()` suffices to
    empty a `Weak`; `Global` clones compare equal and distinct objects
    compare unequal.
11. **Cross-thread control** uses `Isolate::thread_safe_handle()` (Clone +
    Send): `terminate_execution()` from another thread deterministically
    interrupts a tight JS loop (`Script::run` -> `None`, TryCatch set,
    `can_continue` false), and `cancel_terminate_execution()` restores the
    isolate to full usability. Moving the `Isolate` itself across threads
    is rejected at compile time.
12. **Isolate slots own their data**: `set_slot` drops a replaced value
    immediately, `remove_slot` returns ownership, and a value still stored
    is dropped when the isolate is dropped.

## Benchmarks

- `startup/isolate_new_dispose` — `Isolate::new` + drop
- `startup/isolate_context_new_dispose` — isolate + handle scope + context
- `startup/context_new_dispose` — context creation within one live isolate
- `script/compile_minimal` — compile `"1 + 1"`
- `script/compile_workload` — compile the fib(12) + coercion workload
- `script/compile_and_run_minimal` / `script/compile_and_run_workload`
- `script/run_precompiled_workload` — execution only (script rooted in a
  `Global`, compiled once)
- `callback/native_call_from_js` — precompiled script calls a native
  `add(a, b)` twice per iteration
- `callback/native_call_from_rust` — host `Function::call` with undefined
  receiver and two number arguments
- `callback/function_new_call` — `Function::new` wrapper creation + one host
  call per iteration
- `promise/resolver_new_resolve` — native resolver creation + resolve(42)
- `promise/resolve_then_checkpoint` — resolver + `then(native handler)` +
  resolve + `perform_microtask_checkpoint` under the Explicit policy

Methodology: 1 s warm-up, 3 s measurement, 50 samples per benchmark (set in
`benches/common/mod.rs`); fresh nested `HandleScope` per iteration; isolate
and context created once per benchmark except where startup is itself the
measured operation. V8 flags: none (default). Build: release bench profile
with the prebuilt release V8 artifact.

Raw output lives in `bench-results/`:

- `criterion-initial-2026-08-28/` — full criterion data (estimates, samples,
  tukey, plots) from the recorded run
- `initial-2026-08-28-summary.md` — measured numbers + commands
- `env-2026-08-28-DESKTOP-VJI58KR.txt` — machine metadata
- `criterion-function-module-cache-2026-09-01/` — full Criterion data for the
  advanced function and module-cache comparison
- `function-module-cache-2026-09-01-summary.md` — matching Go samples, Rust
  confidence intervals, commands, and workload boundaries
- `env-2026-09-01-DESKTOP-VJI58KR.txt` — environment metadata for that run

To record a new run (Windows PowerShell, from the `rust-oracle` directory):

```
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\capture-env.ps1 > bench-results\env-<date>-<host>.txt
cargo bench --bench startup --bench script -- --save-baseline <name>
Copy-Item -Recurse target\criterion bench-results\criterion-<name>
```

Comparisons against Go must use the same warm-up/sample policy, the same
workload sources, the same V8 configuration (no flags, default platform,
pointer compression off), a release-mode build, and a fresh environment
capture on the same machine.

## Status

Checked on this machine (2026-09-01, Rust toolchain 1.98.0, Go 1.26.2):

- `cargo fmt --check` — clean
- `cargo check` — clean (only the documented LNK4098 linker warning)
- `cargo test --locked` — all unit, fixture, deterministic-process,
  lifecycle, panic, and fatal-path tests pass; the fixture corpus contains 374
  normalized checks
- `cargo clippy --all-targets -- -D warnings` — clean
- `cargo bench --locked -- --test` — every benchmark smoke-runs successfully
- advanced function and module-cache benchmarks — measured against the Go
  implementations with repeated samples; raw data and the comparison summary
  are committed under `bench-results/`
- Unsupported-target guard: `cargo check --target x86_64-pc-windows-gnu`
  (installed temporarily, then removed) fails immediately with the single
  "Supported platform" `compile_error!` and never downloads or builds the V8
  artifact.
