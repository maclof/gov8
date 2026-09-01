# rusty_v8 parity matrix

This matrix tracks observable parity against the sole supported reference:

- Rust crate: `v8 =152.2.0`
- Engine: V8 `15.2.124.1-rusty`
- Target: `x86_64-pc-windows-msvc`
- Artifact SHA-256: `0b17ca072bae37dd4ff00e6014d2b413becb031c9342ee11cb8226a5881f62b2`

The declaration-level denominator and intentional language-shape clusters are tracked in
`API_AUDIT.md`: 1,696 of 1,857 public declarations currently have a matched Go
equivalent or documented semantic shape. Ten more declarations retain safe Go
behavioral equivalents but intentionally differ from Rust's borrowed or generic
API shape; none is missing as an executable surface.

Status meanings: **complete** means the listed slice has executable Rust and Go
behavior evidence; **partial** means a production implementation exists but the
pinned upstream family still has named gaps; **oracle** means Rust behavior is
characterized but the Go implementation is not integrated; **missing** means no
production Go implementation exists. A family is not promoted merely because a
type or stub exists.

The fixtures under `rust-oracle/tests/fixtures` currently contain 524 normalized
checks. Go matches 522 byte-for-byte and two more after documented safety
normalizations; no check is oracle-only. Fatal and panic-boundary
subprocess tests are additional evidence and are not counted in that total.

| Rust API family / behavior | Go implementation | Conformance and benchmark evidence | Status / remaining gaps |
|---|---|---|---|
| Platform initialization, version, dispose ordering | `Initialize`, `EngineVersion`, `VersionString`, `Dispose`, `DisposePlatform`, `Shutdown` | base conformance; startup benchmarks | **complete** for the default platform lifecycle; invalid Rust transitions panic while Go intentionally returns errors |
| Platform/task implementations and message-loop control | `ConfigurePlatform` for built-in variants, `ConfigureCustomPlatform`, `PlatformImpl`, `Task`, `IdleTask`, `Isolate.PumpMessageLoop`, `Isolate.RunIdleTasks`, flags and WebAssembly trap activation | 4-check built-in fixture exact; 3-check custom-platform fixture (two exact, one deadlock-safety normalization); lifecycle/panic/race tests; controls-hooks and Wasm async conformance; matched end-to-end compilation and custom-dispatch benchmarks | **complete** for the safe executable pinned platform/task declarations with explicit transferred-task ownership; the synchronous non-nestable default retains the documented deadlock-safety normalization. Deferred finalizer bookkeeping and cold retained-work sidecars reduce the hot wrapper to 32 bytes/one allocation; the current paired callback/ownership boundary is about 7.08x Rust |
| `Isolate` lifecycle and parallel isolates | `NewIsolate`, `NewIsolateWithParams`, `CreateParams`, `SnapshotCreateParams`, external-reference tables, custom ArrayBuffer allocator and cppgc heap transfer, heap/code/space statistics, profiler/notification and control APIs, heap-snapshot streaming | base, 9-check isolate-advanced, core-advanced, snapshots, external-reference, 5-check snapshot-CreateParams, 2-check snapshot-resource composition, controls-hooks, 3-check heap-snapshot, 6-check allocator and 6-check cppgc heap-lifecycle fixtures exact; lifecycle/concurrency/fatal tests; startup benchmarks | **complete** for the safe executable pinned declarations, including snapshot composition with callback-backed ArrayBuffer allocators and custom cppgc heaps; raw stack-limit pointers remain an intentional Rust-only ownership shape |
| `Locker`, shared isolates and thread affinity | `locker.go`; owner-thread validation; weak-handle shared-isolate guards | `core-advanced/thread/*`; wrong-thread, concurrent-isolate, conversion-rejection, and post-conversion weak tests | **complete** for the characterized shared-isolate surface |
| Local, escapable, persistent and weak handles | `Scope`, `EscapableScope`, `Global`, `Weak`, `Eternal`, `TracedReference`, guaranteed finalizers; disallow/allow JavaScript execution scopes | core-advanced, host, snapshots, context-scopes and 8-check residual-handle fixtures; cppgc traced-target fixture; lifecycle/finalizer/wrong-isolate/fatal-mode tests; scope lifecycle benchmark | **complete** for safe managed-handle behavior; Rust raw `Local`/`SealedLocal` casts, unchecked lifetime extension and generic handle traits are intentionally not exposed. Cached fixed-arity HandleScope entry/exit reduces steady-state lifecycle from 3 to the one API-required allocation and improves its median 32% |
| Context creation and globals | `NewContext`, `NewContextWithOptions`, `ContextFromSnapshotWithOptions`, global reuse, embedder data/pointers/slots, extras binding, continuation data, promise hooks and execution allow/disallow scopes | base/core/runtime/template, 8-check context-scopes and 4-check context-residual fixtures; fatal/lifecycle tests; context startup benchmarks | **complete** for the safe executable pinned Context declarations; unsupported fatal indices, unaligned pointers and uncleared snapshot slots are rejected before V8 entry |
| Primitive values and conversions | `value.go`, `strings_bigint.go`, `object_ops.go` | base, strings-bigint, runtime-values and 25-check object-ops fixtures; negative type/lifetime tests; conversion benchmarks | **complete** for public `Data` predicates, primitive constructors, predicates and local numeric/string conversions. Cached fixed-arity primitive constructors are allocation-free before the returned public wrapper and remove one allocation per constructed value |
| String and BigInt APIs | `strings_bigint.go`, including safe `Latin1ToUTF8` | 17-check fixture, negative/lifetime/thread tests, Go benchmarks | **complete** for all safe executable pinned declarations; unsafe pointer/unchecked constructors map to checked slices or owned Go forms |
| Date, RegExp, JSON, Array, Map, Set, Proxy, Symbol and Private | `runtime_values.go`, `fixed_primitive_arrays.go`, `object_ops.go` | 27-check runtime, 2-check residual-symbol/private, 6-check fixed/primitive-array and Data-predicate fixtures; negative/lifecycle/fatal tests; Go benchmarks | **complete** for the pinned public specialized-value declarations; `Private::for_api(None)` is the documented fatal-input safety normalization |
| Object operations and predicates | `object_ops.go`, `object_residual.go`, `object_callback_retention.go` | 25-check object-ops, 4-check residual and 6-check callback-retention fixtures exact in Go; callback panic, negative/lifecycle/GC/thread/race tests; matched lazy first-read benchmark plus Go object benchmarks | **complete** for all safe executable pinned Object declarations, including configured accessors and lazy data properties. Callback-scope reuse and direct install status reduce the matched path to 12 allocations and improve its controlled median another 23.6%; current overhead is about 3.38x Rust |
| Classic scripts, origins, unbound scripts and code cache | `script.go`, `core_advanced.go`, `script_compiler_residual.go` | base/core-advanced and 7-check residual compiler fixtures exact in Go; negative/lifecycle/thread/race tests; five aligned Rust/Go compile/run benchmarks with exact result checks | **complete** for the safe executable pinned declarations: arbitrary-value origins, host-defined options, every compile option/no-cache reason and cache-rejection boundary are covered; the crate exposes no general classic-script streaming API. Cached callback-safe fixed-arity calls remove 3-4 allocations, and the heap-stable lifecycle-word output slot removes one allocation from every Run without growing the wrapper. The large compile-and-run workload is faster in Go; current remaining ratios span about 1.3-2.9x |
| TryCatch, exceptions, Message and StackTrace | `trycatch.go`, `message.go`, `trycatch_listener_residual.go`, advanced exception bindings, raw local getters and five native constructors accepting Go strings or exact V8 String locals | base checks, corrected 10-check advanced, 7-check constructor, 2-check String-local, 4-check message-local and 4-check residual listener/TryCatch fixtures; lifecycle/race/fatal tests | **complete** for the safe executable pinned declarations: structural nesting, identity, termination recovery and full listener Message fidelity are exact; safe `ReThrow` closes the inner catcher immediately, and raw fatal-handle misuse retains the documented normalization |
| Microtask policy and queues | `microtask.go`, context-local hooks, queue-at-creation, running/depth observation and controls hooks | base, controls-hooks and context-scopes fixtures | **complete** for the pinned crate: queue handle ownership, enqueue/checkpoint, policy, attachment, running state, and scope depth are covered; the crate exposes no `MicrotasksScope` constructor |
| Native functions, callbacks and accessors | `callback.go`, `template.go`, `function_advanced.go`, `fast_api.go`, Inspector-backed side-effect evaluation | host, all 6 Function checks, 4-check Fast API substrate and 8-check Fast API residual fixtures exact; cache/fatal subprocess, nested-coercion, retained-argument, wrong-thread and race tests; Rust/Go callback, Function and optimized fast-path benchmarks | **complete** for the safe executable pinned callback surface; fast entry points remain process-lifetime native addresses and never dispatch through Go's callback ABI. Direct scalar conversions preserve nested JS/Go re-entry without movable-stack pointers, while lock-free isolate closure and callback-entry checks plus direct validated Function construction reduce lifecycle overhead and remove two Function allocations. Exact-scope alias validation removes duplicate owner-thread checks, a two-slot native argument buffer removes one native allocation with heap fallback, and function-only terminal `SetInt32` now completes in the trampoline without a reverse DLL transition. `FunctionCallbackArguments` owns packed length/construct snapshots, and every frame-backed accessor validates callback lifetime and owner thread before reading trampoline-owned memory. Function frames also snapshot positive `IsInt32` results for the first two exact argument wires; an isolated old/new-DLL comparison improved JS, host and Function-call medians by 9.3%, 1.3% and 6.9% with unchanged allocations, while the coercive non-Int32 control measured a 0.9% median native-type-check tax. The mandatory lifetime checks make the combined safe path 0.7-4.5% slower than the prior unsafe-to-retain baseline in the longer confirmation; current absolute ratios are about 12.5x and 15.0x Rust for JS/host and 3.99x for Function create/call |
| Object/function templates and interceptors | `template.go`, `template_advanced.go`, `object_callback_retention.go`, `template_name_keys.go` | host, 14-check template-advanced, template-data portion of the 6-check callback-retention fixture, 5-check arbitrary Name-key fixture and 5-check template-accessor Name-key fixture exact in Go; negative/fatal/lifecycle/GC/thread/race tests; Go benchmarks | **complete** for the safe executable pinned template declarations: shared values, accessor properties and native-data conveniences accept String and Symbol keys with exact retention, replacement, attributes and publication behavior; `build_fast` is tracked under Fast API |
| Promises, resolvers and rejection hooks | `promise.go` | host fixture; lifecycle tests; Rust/Go benchmarks; handler/reject panic subprocess parity | **complete** for the safe executable pinned Promise, PromiseResolver and rejection-hook surface; Go retains callbacks through explicit isolate-owned registries and rejects cross-isolate handlers before their local wires can reach V8. A TLS-backed direct `NumberValue` result plus fixed-arity dispatch reduce resolver creation/resolve to one allocation, while then/checkpoint remains at five. Reusing the reaction shims' checked handler casts removes one native predicate for `Then`/`Catch` and two for `Then2`; with the same untimed outer ContextScope as Rust, current medians are about 7.51x Rust for resolver creation/resolve and 4.45x for then/checkpoint. A further same-source shortcut was rejected after a longer confirmation contradicted its initial small gain |
| ArrayBuffer, SharedArrayBuffer and backing stores | `buffer.go`, `array_buffer_allocator.go`; initialized/uninitialized allocation, free/drop observation and shared allocator ownership | 21-check buffer and 6-check allocator fixtures exact; fatal-boundary/lifecycle/deleter, post-isolate/post-shutdown and concurrent-thread tests; matched allocator lifecycle benchmark | **complete** for the safe executable pinned surface, including pre-initialize factories, zero-size bypass, transfer allocation, backing-store lifetime and shared multi-isolate use; Rust's raw pointer-returning allocator vtable is represented by a native-memory callback façade. Fixed-arity lifecycle calls cut Go allocations from 5 to 1, and the atomic registry fast path cuts dispatcher CPU about 73%. The current end-to-end ratio is about 7.95x; native-handle floors isolate it to the two required native-to-Go callbacks plus safe post-isolate ownership rather than registry contention |
| Typed arrays and DataView | `typed_arrays.go` | 14-check typed-array fixture; per-kind boundary/fatal tests; Go benchmarks | **complete** for all 12 pinned typed-array kinds and characterized geometry/data behavior |
| Value serializer/deserializer and delegates | `serializer.go`, `serializer_delegates.go`, `serializer_wasm_legacy.go` | buffer, 25-check delegate and 4-check Wasm/legacy residual fixtures; reader/writer panic boundaries; Go benchmarks | **complete** for the safe executable pinned declarations: typed Wasm-module restoration, repeated-reference identity, full `u32` transfer IDs, wire-version reporting and pre-read legacy control are exact |
| Snapshots and startup data | `snapshot.go`, `create_params_snapshot.go`; creation, cloning, validation, rehashability, context/data recovery, safe CreateParams composition, external-reference remapping and ownership | 15-check snapshot/handle, 3-check external-reference, 5-check snapshot-CreateParams and 2-check snapshot-resource composition fixtures exact; negative, ownership, reuse, concurrent-consumer and cross-thread tests; Go benchmarks | **complete** for the safe executable pinned declarations, including snapshot-backed isolate creation with custom ArrayBuffer allocator and cppgc heap ownership |
| Source-text and synthetic ES modules | `module.go`, `module_cache.go`, `module_synthetic.go`, `module_advanced_residual.go`; source-text/synthetic compile-link-evaluate, phase-aware source resolution/namespaces, deferred evaluation, stalled-TLA diagnostics, import-meta, dynamic import and ShadowRealm callbacks, unbound scripts and opaque code cache | 7-check source-text, 3-check module-cache, 3-check synthetic-module, 9-check advanced residual and 5-check dynamic `import.defer` fixtures exact in Go; callback panic, cache, fatal, lifecycle, nested-evaluation and thread/race tests; matched Rust/Go module benchmarks including persistent and scope-local cache lifetimes | **complete** for the safe executable pinned declarations, including `kDefer` callback delivery, lazy namespace evaluation, rejection and delayed settlement under `--js-defer-import-eval`. Stack-first resolver conversion and fixed-arity calls halve instantiate/evaluate allocations; the latest archived aligned source-module follow-up measured about 1.45-2.12x Rust, while later scope changes reduced shared overhead without remeasuring all three routes. Archived cache measurements span about 2.1-2.9x, and the controlled synthetic create and full routes are about 2.17x and 2.39x, respectively. Further fused-cache and synthetic TLS/lifecycle shortcuts were slower and rejected |
| Wasm compile/stream/cache APIs | `wasm.go`, `wasm_streaming.go`, `wasm_cache_positive.go`, `wasm_policy_callbacks.go`; synchronous and streaming compile, typed and raw caching, isolate allow/deny and async-settlement policies, movable experimental async compilation, compiled-module extraction/cross-isolate restoration, serializer transfer, trap activation, memory buffer access and predicates | 2-check core, 5-check streaming/async, 2-check policy, 4-check serializer residual, 4-check positive serialized-cache and controls fixtures exact in Go; fatal mismatch/truncation plus negative/panic/lifecycle/thread/race tests; matched sync compile/rehydration, policy callback and end-to-end async benchmarks | **complete** for the safe executable pinned Wasm surface; Go additionally binds V8's public compiled-module serializer to provide provenance-checked cache reuse. Splitting validated restoration from its public pointer wrapper removes one allocation and leaves same-isolate and cross-isolate restoration about 3.01x and 2.93x Rust. Matching Rust's nonblocking spin/poll benchmark boundary brings asynchronous compilation to about 1.03x without changing public runtime behavior |
| Inspector and CRDTP | `inspector_transport.go`, `inspector_session_controls.go`, `inspector_client_callbacks.go`, `inspector_client_values.go`, `inspector_object_wrapping.go`, `inspector_inspected_object.go`, `inspector_runtime_events.go`, `crdtp.go`, `crdtp_dispatcher.go`; owned 8/16-bit strings, Inspector/context/session lifecycle, CDP dispatch, Channel and optional Client callbacks, method dispatch queries, object-group release, scheduled-pause control, remote-object wrapping/unwrapping, inspected-object history, idle/async-task lifecycle, owned Inspector stack traces and exception reporting, CRDTP conversion, dispatch values, responses, serializable helpers, channels, domains and fallthrough | Function side-effect policy; 5-check session-controls, 5-check client-callback, 4-check client-values, 6-check object-wrapping, 5-check inspected-object, 7-check runtime-events, 7-check CRDTP core and 5-check dispatcher fixtures exact; hardened owner-lifecycle, callback, thread/race and panic tests; matched Rust/Go CRDTP dispatch benchmark | **complete** for the safe executable pinned Inspector/CRDTP behavior; frontend notification/flush receivers are implemented, but the pinned Rust API exposes no public trigger for them, and zero deliveries are verified. Fused conversion/copy, cached call-ID forwarding, coalesced callback state and a callback-lifetime serialized view reduce the matched synchronous route to 5 allocations and about 1.75-2.26x the Rust time; retained bytes continue to use native-owned storage |
| cppgc and Rust object tracing | `cppgc.go`, `cppgc_persistent.go`, `cppgc_member.go`, `cppgc_heap_lifecycle.go`, `cppgc_generic_residual.go`, `cppgc_generic_graph.go`; native-owned managed payloads, atomic API-wrapper attachment, scalar identity/tags, arbitrary copied generic state, traced V8 targets, trace/name/destruction observation, strong/weak persistent handles, indexed owner-mediated strong/weak member graphs and custom heap/process ownership | 6-check default-heap object-wrapping, 5-check Persistent/WeakPersistent, 5-check Member/WeakMember, 6-check custom-heap/process, 7-check generic residual and 5-check generic-breadth fixtures exact; tag/lifecycle/thread/race tests; trace/name/destroy panic probes; generic state/edge mutation and heap lifecycle benchmarks | **complete** for safe executable pinned cppgc behavior, including non-scalar state replacement, multiple traced edges, write barriers, weak clearing and combined V8 traced references; ten borrowed or freely composable generic Rust declarations retain intentionally different safe Go shapes and are listed in `API_AUDIT.md` |
| Fast API / `CFunction` | `fast_api.go`; immutable `CTypeInfo`/`CFunctionInfo`/`CFunction` metadata, native-owned descriptor retention, `FunctionBuilder.BuildFast` and `NewFastFunctionTemplate` | 4-check descriptor/overload and 8-check callback-options, one-byte-string and flag/type-matrix fixtures exact; metadata, ownership, lifecycle, thread/race and invalid-signature tests; optimized native fast-path versus Go fallback benchmark with counter proof | **complete** for the safe executable pinned surface, including callback-options descriptors and all flag/type execution; six borrowed callback-local/unchecked Rust ABI shapes remain intentionally unexposed. A separate counter-proven native target matches Rust's ignored callback-options workload; reusing the outer call's exact-scope validation reduced current paired medians to about 1.05-1.07x Rust. Direct scalar conversion reduced the separate generic Go callback fallback to about 18.5x |
| simdutf validation, transcoding, lengths, counts, detection and base64 | `simdutf.go`; all 43 pinned public functions plus result/options constants | 5-check full-surface fixture; destination-boundary and race tests; matched Rust/Go throughput benchmarks | **complete** for the pinned public simdutf module; Go converts Rust's unsafe output/precondition contracts into checked errors. Latest ABI-41 samples measured validation at 1.28x, transcoding at 1.08-1.14x, and short base64 calls at 1.42-1.97x Rust; all remain allocation-free |
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
  default-platform pumping on `Isolate`, and converts the module-cache
  double-set fatal check into a one-shot Go error. The raw setter retains V8's
  fatal mismatch/truncation preconditions; the typed cache path validates wire
  provenance before native entry. Pending streams or resolutions make
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
  not exposed as native fast calls. Native callback-options and one-byte-string
  execution are covered, while borrowed callback-local option/string wrappers
  remain intentionally unexposed. Duplicate public argument counts are rejected
  before the pinned V8 CHECK boundary.
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
  Persistent handles return copied object metadata rather than exposing raw
  cppgc pointers. Isolate teardown drains their native wrappers; later Close is
  idempotent, while Get and Set report the closed isolate. Go's generic object
  facade similarly uses copied cells and owner-mediated member edges rather
  than exposing Rust's callback-borrowed `Visitor`, raw `UnsafePtr`, or freely
  composable generic `Member<T>` and `WeakMember<T>` fields.
- Go CRDTP values use explicit, idempotent `Close` and consume response or
  parameter artifacts exactly once. Notification methods containing an
  interior NUL return an error before native entry and leave parameters live;
  the pinned Rust `CString` conversion panics. Go also rejects malformed or
  concurrently active dispatchables before unchecked native dispatch.

## Verification state

On 2026-09-01, the Rust fixtures contain 524 normalized checks. Go compares 522 checks
byte-for-byte; the advanced stack line and custom-platform inline-deadlock probe
pass after the two narrow safety normalizations documented above. No fixture is
oracle-only.
The Rust oracle suites pass formatting,
strict Clippy and full tests; the Go suite passes
`go test ./... -count=1`, `go vet ./...`, full race checks and benchmark smoke runs.
`scripts/verify_windows.ps1` explicitly reruns every current conformance package.

All audited safe executable behavior is covered. This does not claim literal
Rust API-shape parity: ten borrowed or generic cppgc declarations retain safe,
intentional Go shapes, and 151 raw/borrowed/generic carrier declarations remain
unexposed under the audit ledger's broadly named `unsafe` shape status.
It also does not claim performance parity. Matched Go harnesses and archived
repeated comparisons now cover all 37 pinned Rust workloads, so there is no
remaining measurement-coverage backlog. Material optimization gaps remain in
ordinary JS/host callbacks (about 12.3-18.7x), Function create/call (about 3.45-4.87x),
promises (about 4.45-7.51x), custom allocator callbacks
(about 7.95x), custom-platform callbacks (about 7.1x), compiled-Wasm restoration
(about 2.9-3.0x), lazy-property materialization (roughly 2.8-4.3x), synthetic modules
(about 2.2-2.4x), cached modules (roughly 2.1-2.9x), small
script operations (about 1.3-2.9x), CRDTP dispatch (roughly 1.75-2.26x), native
Fast API execution (about 1.05-1.07x), and simdutf
base64 (roughly 1.42-1.97x). Startup is at parity and the large compile-and-run script
workload is faster in Go on the current run. The remaining work is performance
engineering across these measured boundaries, not missing safe behavior or
unmeasured workloads.
