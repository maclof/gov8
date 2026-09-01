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

The fixtures under `rust-oracle/tests/fixtures` currently contain 458 normalized
checks. Go matches 444 byte-for-byte, two more after documented safety
normalizations, and twelve are oracle-only implementation targets. Fatal and
panic-boundary subprocess tests are additional evidence and are not counted in
that total.

| Rust API family / behavior | Go implementation | Conformance and benchmark evidence | Status / remaining gaps |
|---|---|---|---|
| Platform initialization, version, dispose ordering | `Initialize`, `EngineVersion`, `VersionString`, `Dispose`, `DisposePlatform`, `Shutdown` | base conformance; startup benchmarks | **complete** for the default platform lifecycle; invalid Rust transitions panic while Go intentionally returns errors |
| Platform/task implementations and message-loop control | `ConfigurePlatform` for built-in variants, `ConfigureCustomPlatform`, `PlatformImpl`, `Task`, `IdleTask`, `Isolate.PumpMessageLoop`, `Isolate.RunIdleTasks`, flags and WebAssembly trap activation | 4-check built-in fixture exact; 3-check custom-platform fixture (two exact, one deadlock-safety normalization); lifecycle/panic/race tests; controls-hooks and Wasm async conformance; matched end-to-end compilation benchmark | **partial**: the pinned platform/task declarations are implemented with explicit transferred-task ownership; a matched custom-dispatch overhead benchmark remains |
| `Isolate` lifecycle and parallel isolates | `NewIsolate`, `NewIsolateWithParams`, `CreateParams`, `SnapshotCreateParams`, external-reference tables, heap/code/space statistics, profiler/notification and control APIs | base, 9-check isolate-advanced, core-advanced, snapshots, external-reference, 5-check snapshot-CreateParams and controls-hooks fixtures; lifecycle/concurrency/fatal tests; startup benchmarks | **partial**: snapshot composition covers every existing safe CreateParams field; unsafe custom allocators, raw Go-stack limits, embedder-owned C++ heaps, heap snapshots and remaining profiler controls are explicit gaps |
| `Locker`, shared isolates and thread affinity | `locker.go`; owner-thread validation | `core-advanced/thread/*`; wrong-thread and concurrent-isolate tests | **complete** for characterized Locker entry/unlock behavior; broader shared-isolate integration remains |
| Local, escapable, persistent and weak handles | `Scope`, `EscapableScope`, `Global`, `Weak`, `Eternal`, `TracedReference`, guaranteed finalizers; disallow/allow JavaScript execution scopes | core-advanced, host, snapshots, context-scopes and 8-check residual-handle fixtures; cppgc traced-target fixture; lifecycle/finalizer/wrong-isolate/fatal-mode tests | **complete** for the pinned handle surface; broader cppgc pointer types are tracked separately |
| Context creation and globals | `NewContext`, `NewContextWithOptions`, `ContextFromSnapshotWithOptions`, global reuse, embedder data/pointers/slots, extras binding, continuation data, promise hooks and execution allow/disallow scopes | base/core/runtime/template, 8-check context-scopes and 4-check context-residual fixtures; fatal/lifecycle tests; context startup benchmarks | **complete** for the safe executable pinned Context declarations; unsupported fatal indices, unaligned pointers and uncleared snapshot slots are rejected before V8 entry |
| Primitive values and conversions | `value.go`, `strings_bigint.go`, `object_ops.go` | base, strings-bigint, runtime-values and 25-check object-ops fixtures; negative type/lifetime tests; conversion benchmarks | **complete** for public `Data` predicates, primitive constructors, predicates and local numeric/string conversions |
| String and BigInt APIs | `strings_bigint.go`, including safe `Latin1ToUTF8` | 17-check fixture, negative/lifetime/thread tests, Go benchmarks | **complete** for all safe executable pinned declarations; unsafe pointer/unchecked constructors map to checked slices or owned Go forms |
| Date, RegExp, JSON, Array, Map, Set, Proxy, Symbol and Private | `runtime_values.go`, `fixed_primitive_arrays.go`, `object_ops.go` | 27-check runtime, 2-check residual-symbol/private, 6-check fixed/primitive-array and Data-predicate fixtures; negative/lifecycle/fatal tests; Go benchmarks | **complete** for the pinned public specialized-value declarations; `Private::for_api(None)` is the documented fatal-input safety normalization |
| Object operations and predicates | `object_ops.go`, `object_residual.go`, `object_callback_retention.go` | 25-check object-ops, 4-check residual and 6-check callback-retention fixtures exact in Go; callback panic, negative/lifecycle/GC/thread/race tests; Go benchmarks | **partial**: all safe executable pinned Object declarations, including configured accessors and lazy data properties, are behaviorally covered; a matched lazy first-read benchmark remains |
| Classic scripts, origins, unbound scripts and code cache | `script.go`, `core_advanced.go`, `script_compiler_residual.go` | base/core-advanced and 7-check residual compiler fixtures exact in Go; negative/lifecycle/thread/race tests; Rust/Go script benchmarks | **complete** for the safe executable pinned declarations: arbitrary-value origins, host-defined options, every compile option/no-cache reason and cache-rejection boundary are covered; the crate exposes no general classic-script streaming API |
| TryCatch, exceptions, Message and StackTrace | `trycatch.go`, `message.go`, `trycatch_listener_residual.go`, advanced exception bindings, raw local getters and five native constructors accepting Go strings or exact V8 String locals | base checks, corrected 10-check advanced, 7-check constructor, 2-check String-local, 4-check message-local and 4-check residual listener/TryCatch fixtures; lifecycle/race/fatal tests | **complete** for the safe executable pinned declarations: structural nesting, identity, termination recovery and full listener Message fidelity are exact; safe `ReThrow` closes the inner catcher immediately, and raw fatal-handle misuse retains the documented normalization |
| Microtask policy and queues | `microtask.go`, context-local hooks, queue-at-creation, running/depth observation and controls hooks | base, controls-hooks and context-scopes fixtures | **complete** for the pinned crate: queue handle ownership, enqueue/checkpoint, policy, attachment, running state, and scope depth are covered; the crate exposes no `MicrotasksScope` constructor |
| Native functions, callbacks and accessors | `callback.go`, `template.go`, `function_advanced.go`, `fast_api.go`, Inspector-backed side-effect evaluation | host, all 6 Function checks and 4-check Fast API substrate fixture exact; cache/fatal subprocess tests; Rust/Go callback and Function benchmarks | **partial**: the characterized Function surface and safe Fast API descriptor/build path match; the broader Fast API surface is tracked separately |
| Object/function templates and interceptors | `template.go`, `template_advanced.go`, `object_callback_retention.go` | host, 14-check template-advanced, and template-data portion of the 6-check callback-retention fixtures exact in Go; negative/lifecycle/GC tests; Go benchmarks | **partial**: configured accessors and attributed primitive/nested-template Data are covered; Rust accepts arbitrary `Name` keys while several Go template conveniences remain string-only, and `build_fast` is tracked under Fast API |
| Promises, resolvers and rejection hooks | `promise.go` | host fixture; lifecycle tests; Rust/Go benchmarks; handler/reject panic subprocess parity | **complete** for the characterized native promise slice; advanced embedder hooks remain elsewhere |
| ArrayBuffer, SharedArrayBuffer and backing stores | `buffer.go` | 21-check buffer fixture; fatal-boundary/lifecycle/deleter tests; Go benchmarks | **complete** for the pinned buffer surface: owned/raw backing stores, sharing, detach, data and reference lifetimes are covered; the crate exposes no externalize method, while custom allocator selection is tracked under `Isolate` |
| Typed arrays and DataView | `typed_arrays.go` | 14-check typed-array fixture; per-kind boundary/fatal tests; Go benchmarks | **complete** for all 12 pinned typed-array kinds and characterized geometry/data behavior |
| Value serializer/deserializer and delegates | `serializer.go`, `serializer_delegates.go`, `serializer_wasm_legacy.go` | buffer, 25-check delegate and 4-check Wasm/legacy residual fixtures; reader/writer panic boundaries; Go benchmarks | **complete** for the safe executable pinned declarations: typed Wasm-module restoration, repeated-reference identity, full `u32` transfer IDs, wire-version reporting and pre-read legacy control are exact |
| Snapshots and startup data | `snapshot.go`, `create_params_snapshot.go`; creation, cloning, validation, rehashability, context/data recovery, safe CreateParams composition, external-reference remapping and ownership | 15-check snapshot/handle, 3-check external-reference and 5-check snapshot-CreateParams fixtures; negative, reuse, concurrent-consumer and cross-thread tests; Go benchmarks | **partial**: safe snapshot consumer parameters and external-reference inputs are exact; embedder-owned allocator/heap inputs remain intentionally unsupported |
| Source-text and synthetic ES modules | `module.go`, `module_cache.go`, `module_synthetic.go`, `module_advanced_residual.go`; source-text/synthetic compile-link-evaluate, phase-aware source resolution/namespaces, deferred evaluation, stalled-TLA diagnostics, import-meta, dynamic import and ShadowRealm callbacks, unbound scripts and opaque code cache | 7-check source-text, 3-check module-cache, 3-check synthetic-module and 9-check advanced residual fixtures exact in Go; callback panic, cache, fatal, lifecycle and thread/race tests; matched Rust/Go module benchmarks | **partial**: the safe executable pinned declarations are covered; dynamic-import `kDefer` callback delivery remains uncharacterized because this build exposes no stable public syntax/flag that drives it |
| Wasm compile/stream/cache APIs | `wasm.go`, `wasm_streaming.go`, `wasm_policy_callbacks.go`; synchronous and streaming compile, caching callbacks, isolate allow/deny and async-settlement policies, movable experimental async compilation, compiled-module extraction/cross-isolate restoration, serializer transfer, trap activation, memory buffer access and predicates | 2-check core, 5-check streaming/async, 2-check policy, 4-check serializer residual and controls fixtures exact in Go; negative/panic/lifecycle/thread/race tests; matched sync compile/rehydration, policy callback and end-to-end async benchmarks | **partial**: positive serialized-cache acceptance remains |
| Inspector and CRDTP | `inspector_transport.go`, `inspector_session_controls.go`, `inspector_client_callbacks.go`, `inspector_client_values.go`, `inspector_object_wrapping.go`, `inspector_inspected_object.go`, `inspector_runtime_events.go`; owned 8/16-bit strings, Inspector/context/session lifecycle, CDP dispatch, Channel and optional Client callbacks, method dispatch queries, object-group release, scheduled-pause control, remote-object wrapping/unwrapping, inspected-object history, idle/async-task lifecycle, owned Inspector stack traces and exception reporting | Function side-effect policy; 5-check session-controls, 5-check client-callback, 4-check client-values, 6-check object-wrapping, 5-check inspected-object and 7-check runtime-events fixtures exact; 7-check CRDTP core and 5-check dispatcher oracles; hardened owner-lifecycle, callback, thread/race and panic tests; Go dispatch benchmarks | **partial**: all materially useful Inspector operations match; CRDTP conversion/value and callback/dispatcher behavior is characterized but not implemented; frontend notification/flush receivers are not reachable through the pinned public send surface |
| cppgc and Rust object tracing | `cppgc.go`; native-owned managed payloads, atomic API-wrapper attachment, scalar identity/tags, traced V8 targets, trace/destruction observation | 6-check default-heap object-wrapping fixture exact; tag/lifecycle/thread/race tests; trace/destroy panic probes | **partial**: default-heap object wrapping, traced-target survival, collection and teardown match; generic `GarbageCollected` values, custom heaps, `Member`/`WeakMember`, cppgc persistent handles, `GcCell`, and explicit process controls remain |
| Fast API / `CFunction` | `fast_api.go`; immutable `CTypeInfo`/`CFunctionInfo`/`CFunction` metadata, native-owned descriptor retention, `FunctionBuilder.BuildFast` and `NewFastFunctionTemplate` | 4-check descriptor, optimized execution, overload/fallback and empty-boundary fixture exact; metadata, ownership, lifecycle, thread/race tests; descriptor-construction benchmark | **partial**: caller-supplied process-lifetime native addresses are supported; arbitrary Go fast callbacks, executable `FastApiCallbackOptions`, the complete type matrix and matched fast-call benchmarks remain |
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
- Go's `PlatformImplFuncs` closes a transferred task when its corresponding
  function field is nil. Rust's trait default runs non-nestable work inline and
  the pinned `Atomics.notify` probe deadlocks while V8 holds its waiter lock;
  dropping the task is the explicit safe normalization. User implementations
  can retain and run every transferred task with the one-shot `Task` API.
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
- Go rejects `Template::Set` inputs that would make the pinned engine fatal
  (JSReceiver values) or expose a non-Value property handle (internal metadata
  such as Context/PrimitiveArray), and rejects the forbidden lazy setter
  `HasNoSideEffect` value before FFI. Primitive and nested Function/Object
  templates preserve the characterized behavior and attributes.
- Go Fast API descriptors accept only nonzero caller-supplied addresses of
  process-lifetime native code with the declared ABI; Go callback addresses are
  not exposed as native fast calls. Executable `FastApiCallbackOptions` is
  rejected until its data semantics can match the slow callback, and duplicate
  public argument counts are rejected before the pinned V8 CHECK boundary.
- Inspector `UnwrapObject` maps the returned native context to an existing
  registered Go `Context` wrapper and copies its value into the caller's Scope;
  it never invents a second owner. Object-ID failures preserve Inspector's
  exact UTF-16 diagnostic in a typed error, while owned remote-object protocol
  bytes remain usable after the session, Inspector and isolate are closed. Go
  also rejects a non-innermost destination Scope so the copied local cannot be
  invalidated when a nested V8 handle scope closes.
- Go `InspectorInspectable` is explicitly isolate-bound and move-only:
  `AddInspectedObject` consumes it, while `Close` deterministically releases an
  unadded value. Getter results must be created in the borrowed callback scope;
  callback errors, panics and invalid results fail fast at the non-unwinding
  native boundary. The optional drop hook must not re-enter V8.
- Rust owns its boxed Inspector client; Go retains a caller-owned interface in
  the callback registry and releases that reference synchronously on
  `Inspector.Close`. Client methods are independent optional capability
  interfaces, strings are copied before returning to V8, and console stack
  traces are exposed only as callback-lifetime borrowed markers. Value
  callbacks receive borrowed scope-local values; returned default contexts
  must be live, same-isolate, and registered with that Inspector.
- Go cppgc wrappers keep only integer registry IDs in native managed payloads;
  Go pointers never enter cppgc memory, and allocation plus wrapper attachment
  is atomic. Wrong-tag unwraps return `ok=false` instead of exposing Rust's
  unsafe typed-pointer contract. Trace observers may run on GC workers, while
  destruction observers must not re-enter their isolate during teardown.

## Verification state

On 2026-09-01, the Rust fixtures contain 458 normalized checks. Go compares 444 checks
byte-for-byte; the advanced stack line and custom-platform inline-deadlock probe
pass after the two narrow safety normalizations documented above. Twelve CRDTP
checks are currently oracle-only. The Rust oracle suites pass formatting,
strict Clippy and full tests; the Go suite passes
`go test ./... -count=1`, `go vet ./...`, full race checks and benchmark smoke runs.
`scripts/verify_windows.ps1` explicitly reruns every current conformance package.

The remaining rows are real product scope. In particular, CRDTP, cppgc and the
remaining Fast API surface are not silently deferred.
