# Function and module-cache comparative benchmarks — 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same otherwise-idle
Windows host. Platform, isolate and context construction were outside the
timed region. Each timed operation used a fresh handle scope; cache production
and correctness probes for consume cases were untimed setup. Function source,
parameters, module source, resource name, and cache lifecycle were identical
across languages.

Commands:

```text
go test . -run '^$' -bench 'Benchmark(FunctionAdvanced|ModuleCodeCache)' -benchmem -count=5 -benchtime=1s
cargo bench --locked --bench function --bench module_cache
```

Go reports five independent one-second samples. Rust reports Criterion's mean
95% confidence interval from 50 samples after a one-second warm-up and a
three-second measurement period. The harnesses use their native statistical
models, so ratios are directional rather than claims of identical sampling.

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI |
|---|---:|---:|---:|
| Function cold compile | 6571, 6315, 6391, 6329, 6194 | 304; 9 | 5.2834–6.6619 µs |
| Function cache consume | 1221, 1204, 1243, 1219, 1235 | 304; 9 | 423.57–453.83 ns |
| Module cache create | 4261, 4041, 4167, 4456, 4160 | 4880; 9 | 2.1954–2.2112 µs |
| Module cache consume | 1314, 1289, 1308, 1312, 1335 | 248; 7 | 346.29–356.47 ns |

The Go wrapper is roughly even with native Rust for cold function compilation,
while the three cache-bound operations currently show measurable wrapper and
FFI overhead. These are baseline findings to optimize, not accepted parity
claims.

Raw Criterion estimates, samples, Tukey fences, and reports are stored in
`criterion-function-module-cache-2026-09-01/`. Machine metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.

The copied Criterion directories also retain `base/` and `change/` data from
the immediately preceding calibration run. Criterion's displayed change labels
therefore compare two same-day calibration runs, not released gov8 baselines.
