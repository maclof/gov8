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

## Persistent-global module-cache follow-up

After the consumption benchmark was tightened to retain the compiled module in
a persistent global exactly like Rust, the Go implementation cached the native
export and replaced its heap-allocated 15-argument variadic frame with the
fixed-arity Windows syscall form. The matched command was:

```text
go test . -run '^$' -bench '^BenchmarkModuleCodeCacheConsume$' -benchmem -benchtime=1s -count=10
cargo bench --locked --bench module_cache -- consume_compile_persistent_global
```

| Version | Go ns/op samples | Go B/op; allocs/op |
|---|---:|---:|
| baseline | 1552, 2035, 2173, 2104, 1953, 2052, 2033, 1918, 2026, 2096 | 264; 7 |
| optimized | 1263, 1267, 1806, 1906, 1975, 2044, 2088, 1870, 1694, 1819 | 120; 5 |

The paired median improved from 2034 to 1844.5 ns/op (9.3%) and removed 144
bytes plus two allocations. The final Rust Criterion time interval was
569.85-636.49 ns/op (606.21 ns midpoint), leaving roughly 3x overhead in V8/DLL
calls, scope lifecycle, persistent-wrapper registration and disposal. Complete
Rust samples and reports are stored in
`criterion-module-cache-fixed-2026-09-01/`.
