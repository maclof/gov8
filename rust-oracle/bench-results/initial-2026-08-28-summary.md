# Benchmark run: initial-2026-08-28

Raw criterion data: `criterion-initial-2026-08-28/` (estimates, samples,
tukey, plots). Machine metadata: `env-2026-08-28-DESKTOP-VJI58KR.txt`.

## Command

```
cd rust-oracle
cargo bench --bench startup --bench script -- --save-baseline initial-2026-08-28
```

## Methodology (identical for every benchmark)

- Harness: criterion 0.8.2, `harness = false` bench targets
- Build: `bench` profile (release, optimized); V8 engine is the prebuilt
  release static library (`15.2.124.1-rusty`)
- Warm-up: 1 s per benchmark; measurement: 3 s per benchmark; 50 samples
- One operation per iteration; every iteration opens a fresh nested
  `HandleScope` so local handles cannot accumulate across iterations
- In-process banner (printed by each benchmark binary):
  `os=windows arch=x86_64 logical_cpus=16 build_profile=release
  v8_version_string=15.2.124.1-rusty`

## Results (point estimates from criterion `estimates.json`)

| Benchmark                            | mean      | median    | std dev   |
|--------------------------------------|-----------|-----------|-----------|
| startup/isolate_new_dispose          | 2.514 ms  | 2.384 ms  | 0.712 ms  |
| startup/isolate_context_new_dispose  | 4.872 ms  | 4.220 ms  | 1.659 ms  |
| startup/context_new_dispose          | 1.206 ms  | 1.092 ms  | 0.403 ms  |
| script/compile_minimal               | 658 ns    | 655 ns    | 118 ns    |
| script/compile_workload              | 1.891 µs  | 1.823 µs  | 0.619 µs  |
| script/compile_and_run_minimal       | 1.566 µs  | 1.410 µs  | 0.459 µs  |
| script/compile_and_run_workload      | 123.96 µs | 114.91 µs | 53.22 µs  |
| script/run_precompiled_workload      | 8.548 µs  | 8.435 µs  | 1.758 µs  |

Notes:

- Startup benchmarks are comparatively noisy on this machine (high relative
  std dev; isolate teardown includes background GC work). Treat medians, not
  means, as the comparable statistic, and require fresh runs on the same
  machine for Go-vs-Rust comparisons.
- `compile_and_run_minimal` (1.57 µs) vs `compile_minimal` (0.66 µs):
  executing `1 + 1` costs roughly the compile cost again.
- Workload script: fib(12) recursion plus string/number coercions.

## Reproduction requirements for comparisons

Same machine class, same pinned toolchain (`rust-toolchain.toml`), same
`v8` crate pin and artifact SHA-256 (`.cargo/config.toml`), same V8 flags
(none set; default runtime), same benchmark methodology above, and a fresh
env capture. Go-side harnesses must match warm-up/sample policy and run the
identical workload source.
