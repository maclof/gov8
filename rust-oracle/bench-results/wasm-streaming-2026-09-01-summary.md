# Wasm asynchronous compilation comparative benchmark - 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same otherwise-idle
Windows host and the identical 36-byte module exporting `run() -> i32` with
result 42. Platform, isolate, context, and the reusable outer scope were outside
the timed region. Each iteration creates `WasmModuleCompilation`, feeds the
module bytes, registers the resolution callback with `Finish`, and pumps the
default platform nonblocking until the callback runs.

Commands:

```text
go test -run '^$' -bench '^BenchmarkWasmModuleCompilationAnswerModule$' -benchmem -benchtime=1s -count=5 .
cargo bench --locked --bench wasm -- module_compilation
```

Go reports five independent one-second samples. Rust reports Criterion's
displayed slope estimate and 95% confidence interval from 50 samples after a
one-second warm-up and a three-second measurement period. Repeated identical
bytes may benefit from
V8's internal native-module reuse in both harnesses. The callback bookkeeping
is language-specific but intentionally remains inside both end-to-end timed
boundaries.

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust estimate 95% CI |
|---|---:|---:|---:|
| Experimental async compile | 18580, 19308, 27913, 27782, 28117 | 316; 12 | 10.803-12.808 us |

The Go sample mean is about 24.34 us versus Rust's 11.93 us Criterion point
estimate, a directional 2.04x regression. The Go callback registry, DLL-call
validation, scheduling, and 12 allocations are concrete optimization targets;
this is recorded as a regression rather than accepted performance parity.

Raw Criterion estimates, samples, Tukey fences, and reports are stored in
`criterion-wasm-streaming-2026-09-01/`. Machine metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.
