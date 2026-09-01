# Wasm core comparative benchmarks - 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same otherwise-idle
Windows host and the identical 36-byte module exporting `run() -> i32` with
result 42. Platform, isolate, context, and outer handle-scope construction were
outside the timed region. Each iteration used a fresh nested handle scope.

Commands:

```text
go test -run '^$' -bench '^(BenchmarkWasmSyncCompileAnswerModule|BenchmarkWasmFromCompiledAnswerModule)$' -benchmem -benchtime=1s -count=5 .
cargo bench --locked --bench wasm
```

Go reports five independent one-second samples. Rust reports Criterion's mean
95% confidence interval from 50 samples after a one-second warm-up and a
three-second measurement period. The harnesses use different statistical
models, so ratios are directional rather than claims of identical sampling.

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI |
|---|---:|---:|---:|
| Synchronous compile | 2136, 2144, 2443, 2766, 2830 | 160; 6 | 1827.6-2121.9 ns |
| Restore from compiled module | 786.0, 779.0, 819.5, 823.1, 804.6 | 144; 6 | 163.48-184.96 ns |

The Go wrapper's mean sample is about 1.24x the Rust mean for synchronous
compilation and about 4.60x for compiled-module restoration. The six Go
allocations and the FFI/validation boundary are concrete restoration-path
optimization targets; these are recorded regressions, not accepted
performance-parity claims.

Raw Criterion estimates, samples, Tukey fences, and reports are stored in
`criterion-wasm-core-2026-09-01/`. Machine metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.

## Restoration dispatch follow-up

The restoration wrapper was subsequently changed to resolve its export once
and use a fixed-arity Windows syscall. This keeps the native output slot and
argument frame on the Go stack while retaining the mutex across V8's use of
the shareable compiled representation. The public API and ownership contract
are unchanged.

Fresh before/after commands used the same checkout, host, module, and timed
region as above:

```text
go test -run '^$' -bench '^BenchmarkWasmFromCompiledAnswerModule$' -benchmem -benchtime=1s -count=10 .
cargo bench --locked --bench wasm -- from_compiled
```

| Result | ns/op samples or Criterion time interval | B/op; allocs/op |
|---|---:|---:|
| Go before | 647.4, 948.6, 932.2, 1020, 920.0, 934.9, 990.3, 919.4, 1002, 982.6 | 144; 6 |
| Go after | 549.9, 781.6, 760.8, 771.5, 770.6, 825.7, 754.3, 725.5, 746.8, 823.3 | 88; 4 |
| Rust Criterion | 143.43-154.71 ns (149.43 ns midpoint) | not reported |

The Go median fell from 941.75 ns to 765.7 ns (18.7%), and each restoration
now saves 56 bytes and two allocations. Against the simultaneous pinned Rust
midpoint, the post-change public benchmark remains about 5.1x slower.

Two diagnostic Go benchmarks bound the remaining difference. They use
100,000 fixed iterations per sample so an existing HandleScope can be reused
without unbounded local-handle growth:

```text
go test -run '^$' -bench '^BenchmarkWasmFromCompiled(ExistingScope|NativeFloor)$' -benchmem -benchtime=100000x -count=10 .
```

| Diagnostic | ns/op samples | B/op; allocs/op |
|---|---:|---:|
| Existing scope, public wrapper | 331.4, 336.2, 326.6, 312.2, 316.6, 319.2, 319.2, 365.9, 319.9, 354.7 | 24; 1 |
| DLL + V8 restoration floor | 294.8, 292.4, 261.6, 273.0, 246.8, 282.1, 282.2, 249.3, 275.2, 282.9 | 0; 0 |

The native-floor median is 278.65 ns, already about 1.86x the complete Rust
operation. It excludes Go validation, the compiled-module ownership lock, the
returned Go wrapper, and HandleScope enter/exit. Although the underlying C
bridge signature names only the isolate and compiled representation, an
experimental build proved that V8 consults the current context internally:
removing the validated `Context::Scope` caused an access violation in the
exact compiled-module round-trip test. That experiment was reverted. The
context entry, DLL transition, and V8 restoration therefore form a measured
lower bound for the current safe architecture rather than removable wrapper
work. Complete current Rust samples and reports are stored in
`criterion-wasm-restore-fixed-2026-09-01/`.

## ABI-39 restoration experiment

Ten frozen A/B pairs tested a direct-return export. The public median regressed
from 525.15 to 543.45 ns and the existing-scope median from 363.15 to 383.10 ns;
allocations were unchanged. The native floor was neutral at 274.25 versus
274.00 ns. A one-second confirmation likewise measured 580.10 versus 584.90
ns publicly. The experiment was therefore fully reverted.

These fresh retained-path measurements put the current public operation at
about 3.51-3.91x the frozen Rust 149.43 ns midpoint and its native floor at
about 1.84x. Native transitions account for roughly two thirds of sampled CPU;
required affinity checks and HandleScope lifecycle dominate the remainder.

## Go wrapper escape follow-up

The Go restoration implementation was split into a validated `Value`-returning
helper and a deliberately small public pointer wrapper. The compiler can inline
the wrapper and keep a short-lived result on the caller's stack; callers that
retain the public pointer still get the same heap-backed API. Native restoration,
the compiled-module ownership lock, error ordering, context entry, and scope
lifetime are unchanged.

The Go and pinned Rust restoration workloads use the identical 36-byte module,
compile it once outside the timed region, retain the compiled representation,
and create a fresh nested scope for each timed restoration. The Go benchmark now
also keeps the returned object alive explicitly and performs an untimed type
probe. A second Go workload disposes the producer isolate before constructing a
consumer isolate; all producer/consumer setup remains outside the timed region,
so its timed operation is the same restoration measured by Rust.

Eight two-second frozen A/B pairs alternated old and new Go source against the
same committed ABI-41 DLL (`0FBD464E2A21526067125465110DCB25C2BCBAD86C4B229809DE9E33A66CA6BF`):

```text
go test -c -o gov8-{base,candidate}.test.exe .
gov8-*.test.exe -test.run=^$ -test.bench=^BenchmarkWasmFromCompiledAnswerModule$ -test.benchmem -test.benchtime=2s -test.count=1
gov8-*.test.exe -test.run=^$ -test.bench=^BenchmarkWasmFromCompiledAnswerModuleCrossIsolate$ -test.benchmem -test.benchtime=2s -test.count=1
```

| Workload | Go before ns/op samples | Go after ns/op samples | Before; after allocations | After / Rust midpoint |
|---|---:|---:|---:|---:|
| Same isolate | 480.9, 509.8, 482.2, 504.3, 476.1, 471.2, 466.7, 468.3 | 429.7, 469.2, 459.4, 448.2, 444.9, 453.2, 450.6, 449.0 | 72 B/2; 48 B/1 | 3.01x |
| Producer disposed, consumer isolate | 459.4, 473.6, 449.1, 457.7, 460.0, 456.2, 462.8, 471.2 | 433.8, 437.9, 437.4, 472.9, 431.7, 431.8, 443.7, 438.0 | 72 B/2; 48 B/1 | 2.93x |

The same-isolate median fell from 478.5 to 449.8 ns (6.0%), with all eight
pairs improving. The cross-isolate median fell from 459.7 to 437.65 ns (4.8%),
with seven of eight pairs improving. Both workloads save 24 bytes and one
allocation per restoration. Ratios use the pinned Rust 149.43 ns midpoint;
Rust does not separately time producer-isolate disposal, which is untimed in
the Go cross-isolate workload as well.
