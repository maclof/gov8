# conformance-core-advanced

Byte-exact Go reproduction of the pinned Rust oracle's core-advanced
conformance slice (`rust-oracle/src/bin/conformance-core-advanced.rs`).

- **Crate / engine**: `v8 =152.2.0` / V8 `15.2.124.1-rusty`
- **Fixture**: `../rust-oracle/tests/fixtures/conformance-core-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl`
- **Checks**: 25, in the fixed oracle order (the order is part of the
  observable contract)

## Run

```
go test ./conformance-core-advanced/
go test ./conformance-core-advanced/ -emit report.jsonl   # write the report for review
```

`TestConformanceFixture` compares the rendered JSON-lines report
byte-for-byte against the pinned fixture. `TestConformanceFixtureShapeIsSane`,
`TestConformanceFixtureCoversAllAreasInOrder`, and
`TestConformanceReportDeterministicAcrossRuns` mirror the oracle's fixture
shape, coverage-order, and determinism tests.

## Covered areas

| Area | Checks | Go surface exercised |
|---|---|---|
| `scope/` | 2 | `Scope.NewEscapableScope` / `Escape` / `Close` (nested + two-level escape chains) |
| `thread/` | 5 | `Isolate.TryIntoShared`, `SharedIsolate.Lock`, `Locker.Close`/`UnlockWindow`, `ThreadSafeHandle` controls, `RequestInterrupt` |
| `context/` | 3 | `Context.Enter`, `Isolate.CurrentContext`/`EnteredOrMicrotaskContext`, `SameValue`, security tokens, embedder data (values + aligned pointers), host-side context slots |
| `slots/` | 2 | `Isolate.DataSlotCount`/`GetData`/`SetData` (raw engine slots), `Isolate.SetSlot`/`GetSlot`/`RemoveSlot` (host slots) |
| `script/` | 5 | `Origin`, `Context.CompileWithOrigin`, `UnboundScript` (`Script.Unbound`, `ID`, `Bind`, `CreateCodeCache`), `CompileUnbound` (`OptEagerCompile`), `CachedDataVersionTag`, `CompileFunction` + `CallFunction`, `CheckCodeCache`/`CompileCached` |
| `message/` | 3 | `TryCatch.Message`, all `Message` getters, `Scope.CurrentStackTrace`/`CurrentScriptNameOrSourceURL`, `StackTrace.Frame`/`StackFrame` getters, `CaptureStackTrace`, `ExceptionStackTrace`, `SetCaptureStackTraceForUncaughtExceptions` |
| `terminate/` | 3 | `ThreadSafeHandle.TerminateExecution`/`CancelTerminateExecution`/`IsExecutionTerminating`, `Isolate.TerminateExecution`/`CancelTerminateExecution`, interrupt delivery |
| `heap/` | 2 | `Isolate.GetHeapStatistics`, `AdjustAmountOfExternalAllocatedMemory`, `NumberOfHeapSpaces`, `AddGCPrologueCallback`/`AddGCEpilogueCallback` + removal, `LowMemoryNotification` |

## Intentional deviations (documented in the Go source)

- **Panic-to-error**: the pinned crate's panic guards (second
  `EscapableHandleScope::escape`, recursive `Locker`, locking with another
  isolate entered) surface as Go errors carrying the exact pinned message
  text; the escape-twice check records the error text in the report.
- **Oversight prevention**: the Go wrapper rejects out-of-range isolate
  data slots, out-of-range stack-trace frame indices, negative/huge
  embedder slots, unaligned embedder pointers, and value/pointer slot
  mixing BEFORE the engine is touched (all are unchecked or fatal upstream
  in this build). Consumer code caches are prevalidated with the engine's
  graceful `CachedData::CompatibilityCheck` header sanity check before the
  (fatal-prone) code-cache deserializer is entered.
- **Keyed slots**: the crate's `TypeId`-keyed isolate/context slots map to
  explicit `any` keys (the module-wide established mapping).
- **Origin getters**: `Origin` is an embedder-owned Go struct; the
  round-trip observations read the same values handed to the engine.
- **Slot offsets**: embedder-data slots and raw isolate data slots are
  offset by the pinned crate's internal reservations (2 and 1 slots) so
  the reported counts and default-slot contents match the oracle exactly.

## Known gaps (tracked, not silently stubbed)

- A finalizer-less `Weak` is invisible to the Go-side weak-liveness
  registry, so `TryIntoShared` cannot reject it (the conversion-rejection
  check uses the finalizer variant, whose observable outcome is identical).
  Closing this needs a liveness hook in the weak-handle implementation
  outside this slice's file ownership.
- Weak creation on a locked shared isolate is not rejected in Go (the
  pinned crate panics); same ownership constraint as above.
- Consuming a code cache corrupted MID-PAYLOAD (past the header sanity
  checks) is an upstream engine fatal in this build; the Go prevalidation
  cannot detect it without running the deserializer. The boundary is
  characterized in a dedicated subprocess by the gov8 negative tests
  (`TestCodeCacheCorruptionIsEngineFatal`), never in-process.
- `v8::SealHandleScope` has no binding in the pinned crate and no Go API
  here; explicitly unsupported, nothing observable to pin.
