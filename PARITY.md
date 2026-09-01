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

The fixtures under `rust-oracle/tests/fixtures` currently contain 374 normalized
checks. Fatal and panic-boundary subprocess tests are additional evidence and are
not counted in that total.

| Rust API family / behavior | Go implementation | Conformance and benchmark evidence | Status / remaining gaps |
|---|---|---|---|
| Platform initialization, version, dispose ordering | `Initialize`, `EngineVersion`, `VersionString`, `Dispose`, `DisposePlatform`, `Shutdown` | base conformance; startup benchmarks | **complete** for the default platform lifecycle; invalid Rust transitions panic while Go intentionally returns errors |
| Platform/task implementations and message-loop control | `ConfigurePlatform` for default/unprotected/single-threaded variants, `Isolate.PumpMessageLoop`, `Isolate.RunIdleTasks`, flags with optional usage, and WebAssembly trap activation | 4-check constructor/idle fixture exact in Go; 3-check custom-platform Rust oracle; controls-hooks and Wasm async conformance; matched end-to-end compilation benchmark | **partial**: built-in platform variants, message/idle pumping, flags and trap controls are exact; custom `PlatformImpl`, `Task` and `IdleTask` implementations remain |
| `Isolate` lifecycle and parallel isolates | `NewIsolate`, `NewIsolateWithParams`, `CreateParams`, external-reference tables, heap/code/space statistics, profiler/notification and control APIs | base, 9-check isolate-advanced, core-advanced, snapshots, external-reference and controls-hooks fixtures; lifecycle/concurrency/fatal tests; startup benchmarks | **partial**: unsafe custom allocators, raw Go-stack limits, embedder-owned C++ heaps, heap snapshots and remaining profiler controls are explicit gaps |
| `Locker`, shared isolates and thread affinity | `locker.go`; owner-thread validation | `core-advanced/thread/*`; wrong-thread and concurrent-isolate tests | **complete** for characterized Locker entry/unlock behavior; broader shared-isolate integration remains |
| Local, escapable, persistent and weak handles | `Scope`, `EscapableScope`, `Global`, `Weak`, `Eternal`, `TracedReference`, guaranteed finalizers; disallow/allow JavaScript execution scopes | core-advanced, host, snapshots, context-scopes and 8-check residual-handle fixtures; lifecycle/finalizer/wrong-isolate/fatal-mode tests | **partial**: execution-control scopes match the pinned crate; cppgc tracing is still required for an unrooted `TracedReference` target |
| Context creation and globals | `NewContext`, `NewContextWithOptions`, `ContextFromSnapshotWithOptions`, global reuse, embedder data/pointers/slots, extras binding, continuation data, promise hooks and execution allow/disallow scopes | base/core/runtime/template, 8-check context-scopes and 4-check context-residual fixtures; fatal/lifecycle tests; context startup benchmarks | **complete** for the safe executable pinned Context declarations; unsupported fatal indices, unaligned pointers and uncleared snapshot slots are rejected before V8 entry |
| Primitive values and conversions | `value.go`, `strings_bigint.go`, `object_ops.go` | base, strings-bigint, runtime-values and 25-check object-ops fixtures; negative type/lifetime tests; conversion benchmarks | **complete** for public `Data` predicates, primitive constructors, predicates and local numeric/string conversions |
| String and BigInt APIs | `strings_bigint.go`, including safe `Latin1ToUTF8` | 17-check fixture, negative/lifetime/thread tests, Go benchmarks | **complete** for all safe executable pinned declarations; unsafe pointer/unchecked constructors map to checked slices or owned Go forms |
| Date, RegExp, JSON, Array, Map, Set, Proxy, Symbol and Private | `runtime_values.go`, `fixed_primitive_arrays.go`, `object_ops.go` | 27-check runtime, 2-check residual-symbol/private, 6-check fixed/primitive-array and Data-predicate fixtures; negative/lifecycle/fatal tests; Go benchmarks | **complete** for the pinned public specialized-value declarations; `Private::for_api(None)` is the documented fatal-input safety normalization |
| Object operations and predicates | `object_ops.go`, `object_residual.go` | 25-check object-ops and 4-check residual fixtures; negative/lifecycle/thread tests; Go benchmarks | **partial**: Value predicates, `type_repr`, local conversions, Data predicates, prototype/property construction, own-name enumeration, preview entries and API-wrapper classification are covered; `AccessorConfiguration` and lazy-data-property data/attribute/side-effect variants remain as a callback-retention slice |
| Classic scripts, origins, unbound scripts and code cache | `script.go`, `core_advanced.go` | base/core-advanced fixtures, 7-check residual compiler Rust oracle, negative tests, Rust/Go script benchmarks | **partial**: direct compilation accepts arbitrary `Value` resource names; host-defined options, arbitrary-value origins for unbound/cached compilation, residual compiler options and cache-rejection variants are now characterized but not yet implemented; streaming compilation remains |
| TryCatch, exceptions, Message and StackTrace | `trycatch.go`, `message.go`, advanced exception bindings, raw local getters and five native constructors accepting Go strings or exact V8 String locals | base checks, 10-check advanced, 7-check constructor, 2-check String-local and 4-check message-local fixtures; lifecycle/race/fatal tests | **partial**: full listener Message fidelity, TryCatch structural nesting and identity helpers remain; String-local constructors preserve exact UTF-16 and external-resource semantics, while raw Message/StackFrame handles and TryCatch mutation are exact with the documented fatal-handle safety normalization |
| Microtask policy and queues | `microtask.go`, context-local hooks, queue-at-creation, running/depth observation and controls hooks | base, controls-hooks and context-scopes fixtures | **complete** for the pinned crate: queue handle ownership, enqueue/checkpoint, policy, attachment, running state, and scope depth are covered; the crate exposes no `MicrotasksScope` constructor |
| Native functions, callbacks and accessors | `callback.go`, `template.go`, `function_advanced.go` | host and 6-check Function fixtures, cache/fatal subprocess tests; Rust/Go callback and Function benchmarks | **partial**: five Function observations match; Inspector-dependent `throwOnSideEffect` remains oracle-only and Fast API is missing |
| Object/function templates and interceptors | `template.go`, `template_advanced.go` | host and 14-check template-advanced fixtures; negative tests; Go benchmarks | **partial**: final upstream option/configuration audit remains |
| Promises, resolvers and rejection hooks | `promise.go` | host fixture; lifecycle tests; Rust/Go benchmarks; handler/reject panic subprocess parity | **complete** for the characterized native promise slice; advanced embedder hooks remain elsewhere |
| ArrayBuffer, SharedArrayBuffer and backing stores | `buffer.go` | 21-check buffer fixture; fatal-boundary/lifecycle/deleter tests; Go benchmarks | **complete** for the pinned buffer surface: owned/raw backing stores, sharing, detach, data and reference lifetimes are covered; the crate exposes no externalize method, while custom allocator selection is tracked under `Isolate` |
| Typed arrays and DataView | `typed_arrays.go` | 14-check typed-array fixture; per-kind boundary/fatal tests; Go benchmarks | **complete** for all 12 pinned typed-array kinds and characterized geometry/data behavior |
| Value serializer/deserializer and delegates | `serializer.go`, `serializer_delegates.go` | buffer and 25-check delegate fixtures; delegate panic boundaries; Go benchmarks | **partial**: legacy-wire-format control and actual Wasm-module return support remain |
| Snapshots and startup data | `snapshot.go`; creation, validation, rehashability, context/data recovery, external-reference remapping and ownership | 15-check snapshot/handle plus 3-check external-reference fixtures; negative, reuse and cross-thread tests; Go benchmarks | **partial**: external-reference creator/consumer inputs are exact; arbitrary additional `CreateParams` inputs to snapshot creators remain |
| Source-text and synthetic ES modules | `module.go`, `module_cache.go`, `module_synthetic.go`; source-text compile/link/evaluate, synthetic exports/callbacks, unbound scripts and opaque code cache | 7-check source-text, 3-check module-cache and 3-check synthetic-module fixtures; cache/resolver/evaluation-callback panic, fatal, lifecycle and thread tests; matched Rust/Go module benchmarks | **partial**: dynamic/source/deferred imports, import-meta callbacks and stalled-TLA diagnostics remain |
| Wasm compile/stream/cache APIs | `wasm.go`, `wasm_streaming.go`; synchronous and streaming compile, caching callbacks, movable experimental async compilation, compiled-module extraction/cross-isolate restoration, trap activation, memory buffer access and predicates | 2-check core, 5-check streaming/async and controls fixtures exact in Go; negative/panic/lifecycle/thread tests; matched sync compile/rehydration and end-to-end async benchmarks | **partial**: positive serialized-cache acceptance, isolate policy callbacks and serializer module return remain |
| Inspector and CRDTP | none | none | **missing** |
| cppgc and Rust object tracing | none | none | **missing** |
| Fast API / `CFunction` | none | none | **missing** |
| simdutf validation, transcoding, lengths, counts, detection and base64 | `simdutf.go`; all 43 pinned public functions plus result/options constants | 5-check full-surface fixture; destination-boundary tests; matched Rust/Go throughput benchmarks | **complete** for the pinned public simdutf module; Go converts Rust's unsafe output/precondition contracts into checked errors |
| ICU controls | `icu.go`; ICU 78 common data, locale and time-zone get/set | 3-check exact fixture; valid-data, fatal, malformed, process-global lifecycle and concurrency tests | **complete** for all five pinned public ICU APIs; Go safely copies/aligned-retains common data and converts Rust panic/fatal input boundaries to errors |
| External-reference API | `external_references.go`; raw/native callback words, owned null-terminated CreateParams tables and snapshot remapping/reuse | 3-check exact fixture; fatal-input normalization, lifecycle, concurrency and creator cleanup tests | **complete** for public value/table behavior and supported native callbacks; Go closures remain deliberately non-serializable |
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
- Go selects built-in platforms through process-global `ConfigurePlatform`
  instead of exposing Rust `SharedRef<Platform>` handles. Single-threaded mode
  applies its required V8 flag atomically; omission returns an error instead of
  the pinned build's later access violation.
- `Object.PreviewEntries` takes an explicit Context because Go scopes do not
  cache a current Context. Context snapshot restoration ignores
  `GlobalTemplate` exactly like the pinned Rust wrapper, preserves the
  `usize::MAX` index wrap, and rejects fatal embedder-index, alignment and
  uncleared-host-slot states before FFI.
- Synthetic modules reject duplicate export names and invalid UTF-8 names before
  V8, normalize a zero callback result to `undefined`, and reject reentrant
  `Module.Close`; the corresponding Rust paths CHECK-fail, fatal, or abort.
- Go simdutf conversion and base64 methods validate output capacity and the
  `convert_valid_*` input preconditions before calling the native functions;
  violating those Rust `unsafe` contracts is undefined behavior.
- Go exposes `CompiledWasmModule.Close` in place of Rust `Drop`, copies compiled
  wire/source data into owned Go values, and requires an explicit Context for
  `WasmMemoryObject.Buffer`; the compiled handle remains cross-thread and
  cross-isolate safe until closed.
- Go requires `SetWasmStreamingCallback` before the isolate's first Context,
  requires an explicit Context when finishing `WasmModuleCompilation`, exposes
  default-platform pumping on `Isolate`, and restricts the uncharacterized
  module-cache setter to one call. Pending streams or resolutions make
  `ReleaseIsolateHostState` return an error, preventing use-after-free during
  isolate disposal.
- Go copies ICU common-data input into 16-byte-aligned native process-lifetime
  storage. Empty input returns ICU's error instead of Rust's access violation,
  misaligned input is made safe by the aligned copy, and locale interior NUL or
  invalid UTF-8 is reported as an error instead of a CString panic.
- Rust `StackTrace::get_frame(frame_count)` returns `Some` in the pinned build,
  but dereferencing that one-past-end handle access-violates (`0xC0000005`, 8/8
  subprocess probes). Go `StackTrace.Frame` checks `i >= FrameCount` before the
  frame-getter FFI and returns an error. The advanced fixture comparison
  normalizes only the four repetitions of this field on its single stack line;
  the emitted Go report retains the truthful `false` values.
- Rust `Private::for_api` accepts an optional name at the type level, but a
  `None` name access-violates (`0xC0000005`) in the pinned build. Go's
  `PrivateForApi` requires a live same-isolate String and rejects its zero
  `Value` representation before FFI; anonymous fresh private symbols remain
  available through `NewPrivate(Value{})`.

## Verification state

On 2026-09-01, the Rust fixtures contain 374 checks. Go compares 362 checks
byte-for-byte; the advanced stack line passes after the single fatal-handle
safety normalization documented above. Eleven checks remain oracle-only: seven
script/compiler residual checks, three custom-platform/task observations, and the Function
`throwOnSideEffect` observation that requires Inspector. The Rust
oracle suites pass formatting, strict Clippy and full tests; the Go suite passes
`go test ./... -count=1`, `go vet ./...`, full race checks and benchmark smoke runs.
`scripts/verify_windows.ps1` explicitly reruns every current conformance package.

The remaining rows are real product scope. In particular, residual Wasm policy,
Inspector, cppgc, Fast API and custom platforms are
not silently deferred.
