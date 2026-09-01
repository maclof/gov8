# conformance-controls-hooks

Byte-exact Go reproduction of the pinned Rust oracle's process/isolate
controls & hooks conformance slice
(`rust-oracle/src/bin/conformance-controls-hooks.rs`).

- **Crate / engine**: `v8 =152.2.0` / V8 `15.2.124.1-rusty`
- **Fixture**: `../rust-oracle/tests/fixtures/conformance-controls-hooks-v8_152.2.0_x86_64-pc-windows-msvc.jsonl`
- **Checks**: 22 lines (the full/minor gc-request check emits two lines under
  one id), in the fixed oracle order (the order is part of the observable
  contract)

## Run

```
go test ./conformance-controls-hooks/
go test ./conformance-controls-hooks/ -emit report.jsonl   # write the report for review
```

`TestConformanceFixture` runs the whole registry in a FRESH subprocess (the
Go analog of the oracle fixture tests' `run_binary()`) and compares the
rendered JSON-lines report byte-for-byte against the pinned fixture.
`TestConformanceFixtureShapeIsSane` and
`TestConformanceReportDeterministicAcrossRuns` mirror the oracle's fixture
shape and determinism tests (uniqueness of check ids is intentionally not
asserted — the oracle fixture itself repeats the
`controls/gc_request_requires_expose_gc_subprocess` id for full and minor).

## Covered areas

| Area | Checks | Go surface exercised |
|---|---|---|
| `flags` | 2 | `SetFlagsFromCommandLine` (recognized flags consumed, leftovers returned in engine-compacted order), `SetFlagsFromString("--expose-gc")` + the JS `gc()` global |
| `entropy` | 2 | `SetEntropySource` before/after `Initialize` pinning `Math.random()` to the exact oracle constants (`0.41480742418592154` / `0.8960919850226692`) |
| fatal/gc | 4 | frozen-flags CHECK + API fatal handler, `RequestGarbageCollectionForTesting` fatal without `--expose-gc` (full and minor; the API fatal handler must NOT fire there), WeakRef kept-objects lifecycle with `ClearKeptObjects` |
| memory | 2 | `MemoryPressureNotification` (all three levels), `LowMemoryNotification` reclaiming 32 MiB of ArrayBuffer external memory |
| concurrency controls | 1 | `SetAllowAtomicsWait` toggle vs `Atomics.wait` ("timed-out" / TypeError) |
| background/idle/tz | 3 | `HasPendingBackgroundTasks`, `SetIdle`, `DateTimeConfigurationChangeNotification` (Skip and Redetect) |
| promise hooks | 2 | `SetPromiseHook` (Init/Resolve/Before/After sequence across microtask checkpoints), `SetPromiseRejectCallback` (RejectWithNoHandler, HandlerAddedAfterReject, no events on re-settlement) |
| stack/use/codegen | 3 | `SetPrepareStackTraceCallback` (message formatting, CallSite count, JS hook disabled), `SetUseCounterCallback` (10 pinned feature discriminants), `SetModifyCodeGenerationFromStringsCallback` (skip / block / rewrite / symbol passthrough) |
| message listeners | 1 | `AddMessageListener` + `AddMessageListenerWithErrorLevel` (uncaught-only, duplicate delivery, level filtering, recovery) |
| heap limits | 2 | `AddNearHeapLimitCallback` (replaced callback never fires; doubling raises the 4 MiB configured budget once), `SetOOMErrorHandler` + `SetFatalErrorHandler` on the controlled 10 MiB-capped fatal OOM |

## Subprocess modes

The fatal/heap-pressure paths abort the process by design, so the runner
re-invokes its own test binary with `GOV8_CH_MODE` (the analog of the
oracle's `spawn_self`): `sub-run-all` (fresh full report),
`sub-near-heap-limit`, `sub-fatal-frozen-flags`,
`sub-gc-without-expose-gc-{full,minor}`, `sub-oom-fatal`,
`sub-near-heap-limit-shrink`, `sub-oom-default`,
`sub-invalid-flag-preinit`. Each mode fully controls its own process setup
order (TestMain skips the shared setup when the marker is present) — that
order is itself part of what the modes characterize.

Mode subprocesses install a first-chance vectored exception handler
(`chRawAbortExit`) that terminates with the raw STATUS_BREAKPOINT code:
the Go runtime's own handler would otherwise intercept the engine's int3,
dump goroutines, and exit(2), masking the pinned exit code. This is a
test-harness shim only; production code never installs exception handlers.

## Intentional deviations (documented in the Go source)

- **Exit-code representation**: module-side tests compare Go's unsigned
  `ExitCode()` (2147483651 = 0x80000003, the repo-wide convention); the
  runner normalizes to the Rust fixture's signed form (-2147483645) for
  byte-exact output.
- **Near-heap-limit replacement**: the engine CHECK-refuses registering the
  same C function pointer twice, and Go cannot generate code, so replacing
  a registration is modeled as remove-then-add of the single Go trampoline
  (the Go side tracks whether a previous registration exists). Observable
  behavior matches the oracle: only the most recently added callback is
  invoked, and a replaced callback never fires.
- **Message-listener fan-out**: the engine holds ONE all-levels listener per
  isolate (registered on the first Go registration); per-registration level
  filtering happens in the Go dispatcher, reproducing the engine's
  per-registration filtering exactly.
- **Promise-reject surface**: owned by `promise.go` (its callback anchors
  delivered handles to a supplied scope); this slice's checks consume it.
- **Entropy-source decline**: covered by the module test
  `TestEntropySourceDeclineFallsBack` (same observable assertions as the
  oracle's in-process negative test).

## Known gaps (tracked, not silently stubbed)

- The oracle's `set_date_time_configuration_change_callback` has no binding
  in the pinned crate; only the notification exists. Nothing to port.
- `PromiseRejectAfterResolved` / `PromiseResolveAfterResolved` are never
  delivered in this build even with handlers attached (pinned engine gap);
  the enum values exist for parity.
- `HasPendingBackgroundTasks() == true` is reachable only via background
  Wasm compilation (out of scope); the false path is pinned.
