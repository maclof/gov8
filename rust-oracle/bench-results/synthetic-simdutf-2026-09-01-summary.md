# Synthetic module and simdutf comparative benchmarks - 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same otherwise-idle
Windows host. Synthetic-module platform, isolate, context, and outer handle
scope construction were outside the timed region. The simdutf benchmarks used
identical 4096-byte mixed inputs and preallocated destinations.

Commands:

```text
go test -run '^$' -bench '^(BenchmarkSyntheticModule(Create|CreateInstantiateEvaluate)|BenchmarkSIMDUTF(ValidateUTF8Mixed4K|UTF8ToUTF16LE4K|UTF16LEToUTF8_4K|Base64DecodeStandard4K))$' -benchmem -benchtime=1s -count=5 .
cargo bench --locked --bench modules_synthetic --bench simdutf
```

Go reports five independent one-second samples. Rust reports Criterion's mean
95% confidence interval from 50 samples after a one-second warm-up and a
three-second measurement period. These are native harnesses with different
statistical models, so ratios are directional rather than claims of identical
sampling.

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI |
|---|---:|---:|---:|
| Synthetic create | 1612, 1674, 2036, 2121, 2006 | 248; 10 | 286.50-314.36 ns |
| Synthetic create, instantiate, evaluate | 5596, 5405, 5521, 5509, 5795 | 712; 28 | 783.04-856.70 ns |
| simdutf validate mixed UTF-8 4 KiB | 346.5, 328.7, 332.2, 339.8, 426.2 | 80; 2 | 194.49-204.88 ns |
| simdutf UTF-8 to UTF-16LE 4 KiB | 1310, 1321, 1303, 1269, 1394 | 80; 2 | 1093.6-1107.4 ns |
| simdutf UTF-16LE to UTF-8 4 KiB | 1140, 795.6, 773.7, 775.6, 763.3 | 80; 2 | 754.04-762.77 ns |
| simdutf base64 decode 4 KiB | 355.2, 362.6, 366.3, 370.9, 351.1 | 192; 4 | 115.46-120.66 ns |

Synthetic-module operations are currently about 6x slower through the Go
wrapper. The simdutf wrapper is about 1.7x slower for validation, about 1.2x
slower for UTF-8 to UTF-16LE, near native for UTF-16LE to UTF-8 after one noisy
Go sample, and about 3x slower for base64 decode. The Go allocation counts and
FFI boundary are concrete optimization targets; these regressions are recorded
findings, not accepted performance-parity claims.

Raw Criterion estimates, samples, Tukey fences, and reports are stored in
`criterion-synthetic-simdutf-2026-09-01/`. Machine metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.
