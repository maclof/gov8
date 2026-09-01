# conformance-snapshots

Go reproduction of the pinned Rust snapshots/handles/termination oracle
registry (`rust-oracle/src/bin/conformance-snapshots.rs`, crate `v8
=152.2.0`, engine `15.2.124.1-rusty`, target `x86_64-pc-windows-msvc`).

## What is pinned

The 15-check normalized JSON-lines report
(`rust-oracle/tests/fixtures/conformance-snapshots-v8_152.2.0_x86_64-pc-windows-msvc.jsonl`)
is reproduced byte-for-byte by `TestSnapshotFixture`:

- snapshot creation and startup data (`snapshot/create_blob_policies`,
  `snapshot/startup_data_predicates`)
- snapshot consumption: default-context round trip through
  `CreateParams::snapshot_blob`, snapshot-of-snapshot chains, added
  contexts with `Context::from_snapshot`, exactly-once isolate/context
  data retrieval with `NoData`/`BadType` outcomes
  (`snapshot/default_context_create_params_roundtrip`,
  `snapshot/chained_roundtrip`, `snapshot/add_context_from_snapshot`,
  `snapshot/isolate_data_once`,
  `snapshot/context_data_once_and_badtype`)
- `Global` identity, clone, raw round trip, cross-isolate equality, and
  drop-after-dispose (`handle/global_*`)
- weak finalizers under forced GC, guaranteed finalizers, drop-cancel and
  equality/clone semantics (`handle/weak_*`)
- same-thread terminate/request/cancel delivery semantics
  (`terminate/request_and_cancel_during_js`)

## Subprocess modes

The Rust oracle characterizes four scenarios in dedicated subprocesses
(`rust-oracle/tests/snapshots_handles_negative.rs`); the Go coverage of
those scenarios lives in the module-root tests (`terminate_test.go`,
`snapshot_test.go`) because Go's test binary already provides the
subprocess isolation:

| Rust mode | Go coverage | Outcome |
|---|---|---|
| `mode=terminate-loop` | `TestSubprocessTerminateLoopFromOtherThread` | matches: one deterministic JSON line |
| `mode=invalid-startup-data-fatal` | `TestSubprocessInvalidStartupBlobGuard` | **deviation**: Go guards and survives (the pinned crate aborts in a V8 `CHECK`) |
| `mode=global-eq-after-dispose` | `TestGlobalDropAfterIsolateDispose` | **deviation**: Go returns errors instead of panicking |
| `mode=drop-creator-without-blob` | `TestSnapshotCreatorLifecycle` | **deviation**: Go resolves the drop panic into an explicit, safe `SnapshotCreator.Close` |

## Run

```
go test ./conformance-snapshots -v
```

This runner performs no platform shutdown, exactly like the oracle's
snapshots runner.
