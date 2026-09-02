# Promise-slice conformance runner

Re-implements the three Promise/PromiseResolver checks of the pinned Rust
oracle's host slice (`../../rust-oracle/src/checks/host/promises.rs`, registry
order in `src/checks/host/mod.rs`) on top of the Go binding and compares the
normalized JSON-lines **byte-for-byte** against the corresponding lines of
the checked-in host fixture:

```
../../rust-oracle/tests/fixtures/conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl
```

The host fixture pins all 18 host-slice checks; this runner owns exactly the
three promise checks (the other 15 belong to sibling feature slices):

1. `promise/resolver_settlement_semantics`
2. `promise/native_then_checkpoint`
3. `promise/reject_callback_events`

## Run

```
go test ./conformance/host-promises -v
```

Emit this slice's report (fixture-regeneration reviews):

```
go test ./conformance/host-promises -run TestHostPromiseFixture -emit promise-report.jsonl
```

## Encoding rules

Identical to `../README.md` and `rust-oracle/src/json.rs`:
insertion-ordered objects, no whitespace, minimal string escaping, plain
decimal integers.

## Characterized behavior (from the oracle)

- `resolve`/`reject` return the success of the **call**, not a settlement
  change: repeat settlement attempts are silently ignored and still report
  true; state and result are unchanged.
- `then` attaches synchronously (`has_handler` true immediately); under the
  Explicit microtasks policy the reaction job runs only at
  `perform_microtask_checkpoint`; the derived promise is a distinct object
  and settles to the handler's (implicit undefined) result.
- The promise-reject callback fires synchronously at reject time
  (`WithNoHandler` when unhandled), fires `HandlerAddedAfterReject` when a
  handler is attached to a rejected promise, fires nothing when a handler
  preceded the reject, and reports the derived promise of a bare `then` on a
  rejected promise as a second `WithNoHandler` when the reaction job runs.
  `RejectAfterResolved`/`ResolveAfterResolved` were removed from V8 and
  never fire on the pinned build.

## Go-side callback path (implementation note)

Native reaction handlers cross the ABI as integer registry ids only (no Go
pointers are retained by the shim); the shim's trampolines dispatch through
two process-lifetime entries created once with `syscall.NewCallback`. See
`promise.go` in the module root for the full ownership/lifetime contract,
including the documented deviation: a panic inside a Go handler is recovered
and behaves like the handler returning undefined, whereas the Rust oracle
aborts the process.

## Status

3/3 promise checks reproduce the pinned host fixture byte-for-byte on
Windows amd64 (V8 15.2.124.1-rusty, artifact sha256
`0b17ca072bae37dd4ff00e6014d2b413becb031c9342ee11cb8226a5881f62b2`).
