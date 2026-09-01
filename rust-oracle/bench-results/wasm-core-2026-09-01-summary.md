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
