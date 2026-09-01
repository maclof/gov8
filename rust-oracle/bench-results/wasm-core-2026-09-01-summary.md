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
