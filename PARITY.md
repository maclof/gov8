# rusty_v8 parity matrix

This matrix tracks observable parity against the sole supported reference:

- Rust crate: `v8 =152.2.0`
- Engine: V8 `15.2.124.1-rusty`
- Target: `x86_64-pc-windows-msvc`
- Artifact SHA-256: `0b17ca072bae37dd4ff00e6014d2b413becb031c9342ee11cb8226a5881f62b2`

Status meanings: **complete** means the listed slice has executable Rust and Go
behavior evidence; **partial** means a production implementation exists but the
pinned upstream family still has named gaps; **oracle** means Rust behavior is
characterized but the Go implementation is not integrated; **missing** means no
production Go implementation exists. A family is not promoted merely because a
type or stub exists.

The fixtures under `rust-oracle/tests/fixtures` currently contain 316 normalized
checks. Fatal and panic-boundary subprocess tests are additional evidence and are
not counted in that total.

| Rust API family / behavior | Go implementation | Conformance and benchmark evidence | Status / remaining gaps |
|---|---|---|---|
| Platform initialization, version, dispose ordering | `Initialize`, `EngineVersion`, `VersionString`, `Dispose`, `DisposePlatform`, `Shutdown` | base conformance; startup benchmarks | **complete** for the default platform lifecycle; invalid Rust transitions panic while Go intentionally returns errors |
| Platform/task implementations and message-loop control | none | none | **missing**: `PlatformImpl`, custom/single-threaded/unprotected platforms, `Task`, `IdleTask`, message-loop pumping, idle tasks, flags-with-usage and trap-handler controls |
| `Isolate` lifecycle and parallel isolates | `NewIsolate`, `NewIsolateWithParams`, `CreateParams`, heap/code/space statistics, profiler/notification and control APIs | base, 9-check isolate-advanced, core-advanced, snapshots and controls-hooks fixtures; lifecycle/concurrency/fatal tests; startup benchmarks | **partial**: unsafe custom allocators, non-empty raw external references, raw Go-stack limits, embedder-owned C++ heaps, heap snapshots and remaining profiler controls are explicit gaps |
| `Locker`, shared isolates and thread affinity | `locker.go`; owner-thread validation | `core-advanced/thread/*`; wrong-thread and concurrent-isolate tests | **complete** for characterized Locker entry/unlock behavior; broader shared-isolate integration remains |
| Local, escapable, persistent and weak handles | `Scope`, `EscapableScope`, `Global`, `Weak`, `Eternal`, `TracedReference`, guaranteed finalizers | core-advanced, host, snapshots and 8-check residual-handle fixtures; lifecycle/finalizer/wrong-isolate tests | **partial**: cppgc tracing is still required for an unrooted `TracedReference` target; remaining execution-control handle scopes require audit |
| Context creation and globals | `NewContext`, `NewContextWithOptions`, global reuse, extras binding, continuation data, promise hooks and execution allow/disallow scopes | base/core/runtime/template plus 8-check context-scopes fixture and fatal subprocess tests; context startup benchmarks | **partial**: snapshot-context options and residual embedder-data APIs remain |
| Primitive values and conversions | `value.go`, `strings_bigint.go` | base, strings-bigint and runtime-values fixtures; conversion benchmarks | **partial**: residual `Data` predicates/helpers and niche conversion APIs remain |
| String and BigInt APIs | `strings_bigint.go` | 16-check fixture, negative/lifetime tests, Go benchmarks | **partial**: implemented surface is strongly covered; final upstream declaration audit remains |
| Date, RegExp, JSON, Array, Map, Set, Proxy, Symbol and Private | `runtime_values.go`, `fixed_primitive_arrays.go` | 27-check runtime fixture plus 6-check fixed/primitive-array fixture; negative/lifecycle tests; Go benchmarks | **partial**: characterized `FixedArray`/`PrimitiveArray` behavior is complete; residual `Data` predicates/helpers remain |
| Object operations and predicates | `object_ops.go` | 22-check fixture; negative tests; Go benchmarks | **partial**: prototype/property constructors, own-name variants, preview entries, API-wrapper and accessor variants remain |
| Classic scripts, origins, unbound scripts and code cache | `script.go`, `core_advanced.go` | base and core-advanced fixtures; negative tests; Rust/Go script benchmarks | **partial**: residual compiler options, streaming compilation and cache-rejection variants remain |
| TryCatch, exceptions, Message and StackTrace | `trycatch.go`, `message.go`, advanced exception bindings and five native constructors | base checks, 10-check advanced fixture and 7-check constructor fixture; lifecycle/race/fatal tests | **partial**: constructor/CreateMessage coverage is complete for the pinned public API; the advanced stack check uses the documented fatal-handle safety normalization and residual message APIs require audit |
| Microtask policy and queues | `microtask.go`, context-local hooks, queue-at-creation, running/depth observation and controls hooks | base, controls-hooks and context-scopes fixtures | **partial**: the pinned crate exposes no MicrotasksScope constructor; remaining embedder queue hooks require audit |
| Native functions, callbacks and accessors | `callback.go`, `template.go`, `function_advanced.go` | host and 6-check Function fixtures, cache/fatal subprocess tests; Rust/Go callback and Function benchmarks | **partial**: five Function observations match; Inspector-dependent `throwOnSideEffect` remains oracle-only and Fast API is missing |
| Object/function templates and interceptors | `template.go`, `template_advanced.go` | host and 14-check template-advanced fixtures; negative tests; Go benchmarks | **partial**: final upstream option/configuration audit remains |
| Promises, resolvers and rejection hooks | `promise.go` | host fixture; lifecycle tests; Rust/Go benchmarks; handler/reject panic subprocess parity | **complete** for the characterized native promise slice; advanced embedder hooks remain elsewhere |
| ArrayBuffer, SharedArrayBuffer and backing stores | `buffer.go` | 20-check buffer fixture; fatal-boundary/lifecycle tests; Go benchmarks | **partial**: implemented core is strong; final allocator/externalization audit remains |
| Typed arrays and DataView | `typed_arrays.go` | 14-check typed-array fixture; per-kind boundary/fatal tests; Go benchmarks | **complete** for all 12 pinned typed-array kinds and characterized geometry/data behavior |
| Value serializer/deserializer and delegates | `serializer.go`, `serializer_delegates.go` | buffer and 25-check delegate fixtures; delegate panic boundaries; Go benchmarks | **partial**: legacy-wire-format control and actual Wasm-module return support remain |
| Snapshots and startup data | `snapshot.go` | 15-check snapshot/handle fixture; negative and cross-thread tests; Go benchmarks | **partial**: `StartupData::can_be_rehashed` and remaining creator options are missing |
| Source-text ES modules | `module.go`, `module_cache.go`, source-text compile/link/evaluate, unbound scripts and opaque code cache | 7-check module and 3-check module-cache fixtures; cache/resolver panic/lifecycle/thread tests; matched Rust/Go module benchmarks | **partial**: synthetic modules, dynamic/source/deferred imports, import-meta callbacks and stalled-TLA diagnostics remain |
| Wasm compile/stream/cache APIs | none; serializer exposes only a reduced no-module path | Wasm appears only as JS-observed exception/serialization behavior | **missing** |
| Inspector and CRDTP | none | none | **missing** |
| cppgc and Rust object tracing | none | none | **missing** |
| Fast API / `CFunction` | none | none | **missing** |
| ICU controls and simdutf | none | none | **missing** |
| External-reference API | none | none | **missing** |
| Non-Windows-amd64 targets | none | unsupported-target guards | **out of scope** by the pinned-artifact project decision |

## Known intentional API-shape differences

- Rust lifecycle misuse commonly panics; Go returns explicit errors where it
  can do so before entering V8. Panic/fatal behavior inside engine callbacks is
  preserved with fail-fast subprocess coverage.
- Rust generic `Local`, `Global`, `SharedRef` and `UniqueRef` machinery is not
  copied mechanically. Go exposes concrete ownership types while preserving
  isolate, scope, thread and close requirements.
- Go pins an isolate-owning goroutine to its OS thread and validates affinity on
  every public engine operation.
- Rust `StackTrace::get_frame(frame_count)` returns `Some` in the pinned build,
  but dereferencing that one-past-end handle access-violates (`0xC0000005`, 8/8
  subprocess probes). Go `StackTrace.Frame` checks `i >= FrameCount` before the
  frame-getter FFI and returns an error. The advanced fixture comparison
  normalizes only the four repetitions of this field on its single stack line;
  the emitted Go report retains the truthful `false` values.

## Verification state

On 2026-09-01, the Rust fixtures contain 316 checks. Go compares 314 checks
byte-for-byte; the advanced stack line passes after the single fatal-handle
safety normalization documented above; and the Function side-effect policy is
implemented as metadata but its `throwOnSideEffect` observation remains
oracle-only until Inspector exists. The Rust oracle suites pass formatting,
strict Clippy and full tests; the Go suite passes `go test ./... -count=1`,
`go vet ./...`, full race checks and benchmark smoke runs.
`scripts/verify_windows.ps1` explicitly reruns every current conformance package.

The remaining rows are real product scope. In particular, Wasm, Inspector,
cppgc, Fast API, custom platforms, ICU and simdutf are not silently deferred.
