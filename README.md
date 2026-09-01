# gov8

`gov8` is a project to create a production-quality Go module with feature,
behavioral, and performance parity with the Rust [`v8`](https://crates.io/crates/v8)
crate (`rusty_v8`). The Rust crate and its pinned V8 build provide the executable
reference; a dedicated Rust project will characterize that reference and produce
fixtures and benchmarks that the Go implementation must match.

## Supported Platform

`gov8` intentionally supports **Windows amd64 only**. The reference target is
`x86_64-pc-windows-msvc`; other operating systems, architectures, and Windows
GNU targets are out of scope. This lets both implementations consume the same
SHA-256-pinned rusty_v8 binary rather than merely using similar V8 builds.

## Quick Start

Prerequisites are Go 1.24 or newer, Rust 1.98, and Visual Studio with the MSVC
C++ x64 build tools. From Windows PowerShell:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\setup_windows.ps1
go test ./...
Push-Location rust-oracle
cargo test
Pop-Location
```

The setup script downloads or reuses the exact pinned artifacts, verifies their
hashes, and builds the untracked `build\shim\gov8_shim.dll`. No downloaded
binary is committed. See `rust-oracle/README.md` for the authoritative version
record and `PARITY.md` for implementation status.

## Goals

- Cover the public capabilities of the pinned Rust `v8` crate with equivalent
  Go functionality.
- Match observable success, error, exception, lifecycle, ordering, and
  concurrency behavior.
- Provide an idiomatic Go API without concealing V8 ownership, lifetime,
  isolate-locking, or thread-affinity constraints.
- Achieve performance comparable to or better than the Rust reference on
  equivalent workloads and document any accepted exceptions.
- Make correctness and performance claims reproducible from a clean checkout.
- Support incremental, reviewed delivery through a maintained parity matrix.

## Non-Goals

- Mechanical translation of Rust syntax or type names where that would produce
  an unsafe or misleading Go API.
- Claiming parity through unimplemented stubs, skipped tests, or compile-only
  wrappers.
- Comparing benchmarks that use different V8 versions, flags, workloads, build
  modes, warm-up, or machine conditions.
- Hiding unsupported behavior or known regressions.

## Required Deliverables

The repository is expected to contain:

- A versioned Go module implementing the V8 binding and public API.
- A separately buildable Rust reference/test project pinned to an exact `v8`
  crate and toolchain.
- A parity matrix mapping Rust APIs and behaviors to Go APIs, conformance tests,
  benchmark coverage, status, and documented deviations.
- Shared deterministic fixtures or mechanically comparable normalized outputs.
- Unit, integration, negative, lifecycle, and concurrency tests as applicable.
- Comparative benchmark suites, raw results, and environment metadata.
- Build, platform, dependency, safety, and usage documentation.
- CI checks for formatting, static analysis, tests, and selected benchmarks or
  benchmark smoke tests.

## Parity Requirements

A feature is complete only when all applicable requirements are met:

1. The exact behavior of the pinned Rust reference is characterized by a test.
2. The Go implementation produces the same normalized observable result.
3. Error, exception, boundary, ownership, and lifecycle cases are covered.
4. Concurrency and thread-affinity behavior is tested where relevant.
5. Performance-sensitive paths have equivalent Rust and Go benchmarks.
6. Intentional Go API differences preserve semantics and are documented.
7. Formatting, static analysis, and test suites pass from a clean checkout.

The initial inventory should include platform setup and teardown, isolates,
lockers, handle scopes, contexts, values and conversions, strings, objects,
templates, scripts, exceptions, callbacks, promises, microtasks, modules,
snapshots, serialization, backing stores, external data, weak handles and
garbage-collection integration, threading, and inspector/debugging facilities.
The pinned crate source is authoritative for the final inventory.

## Performance Requirements

Comparisons must use the same V8 revision and configuration wherever technically
possible. Each benchmark report must identify CPU, operating system, toolchain,
build mode, V8 flags, warm-up policy, workload, sample count, and memory method.
At minimum, measure:

- Platform and isolate startup/shutdown.
- Context creation and disposal.
- Script compilation and execution.
- Repeated function calls and host callbacks.
- Value conversion and cross-language boundary overhead.
- Promises, microtasks, modules, snapshots, and serialization where supported.
- Allocations, retained memory, and steady-state process memory.

Performance parity is evaluated per workload, not by a single aggregate score.
Regressions require investigation and a documented cause; accepted regressions
must include impact and follow-up work.

## Agent Workflow

The project `opencode.json` selects `coordinator` as the default and disables
the built-in `build` and `plan` primaries. Definitions under
`.opencode/agents/` establish three roles:

- `coordinator`: the only primary agent; plans work, delegates parallel slices,
  reviews evidence and changes, integrates, verifies, commits, and pushes.
- `go-v8-expert`: implements, tests, profiles, and optimizes the Go module.
- `rust-v8-expert`: maintains the Rust oracle, conformance fixtures, and
  reference benchmarks.

Specialists do not commit or push. The coordinator assigns non-overlapping file
ownership, reviews every result, runs integration checks, and owns Git history.
Parallel work is preferred whenever tasks do not share mutable files or depend
on unfinished results.

## Delivery Rules

- Pin external versions and record upgrades explicitly.
- Keep commits small, coherent, reviewed, and independently testable.
- Never include credentials, generated secrets, or unrelated work in commits.
- Never force-push or bypass failing hooks.
- Report unsupported platforms, APIs, flaky tests, and benchmark variance
  directly.
- A milestone is complete only when its parity entries and evidence are current.

## Current Status

The executable reference is pinned to Rust `v8 =152.2.0`, V8
`15.2.124.1-rusty`, and the Windows MSVC artifact identified in
`rust-oracle/README.md`. The Go binding uses a pure-Go Windows DLL call layer and
an MSVC C ABI shim, avoiding unsafe MinGW/MSVC C++ ABI interoperation.

The current implementation includes lifecycle and advanced isolate controls,
Locker and handle variants, context options and execution scopes, values and
collections (including fixed and primitive arrays), scripts and function/module
code caches, source-text and synthetic modules, synchronous and streaming Wasm
compilation, movable asynchronous compilation and compiled-module restoration,
Go-string and exact V8 String-local exception constructors, and raw message/stack
handles, callbacks/templates/interceptors, native promises, buffers and all
typed-array kinds, structured clone delegates, snapshots, and the complete
pinned simdutf and ICU surfaces. External-reference tables and snapshot remaps,
the audited String/BigInt surface, and all safe specialized runtime values are
also covered. The Rust fixtures contain 374 normalized checks: 362 compare
byte-for-byte with Go, one stack-frame check has a narrowly documented
memory-safety normalization, and 11 checks remain oracle-only (seven residual
script/compiler checks, three custom platform/task observations, and one
Inspector-dependent Function check).
Separate fatal and panic-boundary subprocess tests cover unsafe lifecycle and
callback edges.

This is still not a feature-complete rusty_v8 binding. Major remaining families
include dynamic/source/deferred modules, residual unsafe CreateParams
options, residual object/function APIs, Wasm policy/serializer integration,
Inspector/CRDTP, cppgc, Fast API, and custom platforms/tasks. The
authoritative gaps and intentional API-shape differences are tracked in
`PARITY.md`.
