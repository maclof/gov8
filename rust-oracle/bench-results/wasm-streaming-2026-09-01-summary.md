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

## Resolution-allocation follow-up

The Go implementation was subsequently changed to bundle all callback-local
resolution wrappers in one allocation, store resolution registrations by
value, allocate callback IDs atomically, and cache fixed-arity exports for
creation, byte delivery, finish, and message-loop pumping. Focused normal and
race tests plus vet passed. Observable callback-local invalidation and pending
compilation teardown guards are unchanged.

A clean-HEAD baseline using the same ABI-37 DLL produced 21444, 23802, 29083,
30936, 29249, 31297, 30521, 31055, 29477, and 29615 ns/op at 316 B/op and 12
allocations/op. The optimized samples were 20961, 23466, 29669, 28434, 30781,
28650, 28173, 28635, 28583, and 27947 ns/op at 196 B/op and 4 allocations/op.
The median fell from 29546 to 28508.5 ns/op (3.5%), bytes fell 38.0%, and
allocations fell 66.7%. Asynchronous V8 scheduling dominates the noisy timing,
but the allocation reduction is deterministic.

An immediately following pinned Rust run reported a 12.134-14.006 us slope
95% confidence interval with a 13.188 us point estimate. The optimized Go
median remains about 2.16x slower, so callback and scheduling boundaries remain
measured performance work rather than accepted parity.

## Matched polling-boundary follow-up

The Go harness previously called `runtime.Gosched` whenever a nonblocking
message-loop pump found no task. The Rust harness performs only its spin hint
and next poll, so the scheduler yield added work outside the matched operation.
Removing that yield changes benchmark polling only; public platform and Wasm
runtime behavior is unchanged.

Six controlled old/new pairs changed the median from 26.26 to 12.94 us, a
50.7% reduction, with allocations unchanged at 196 B/4. A ten-sample
order-balanced final control measured 13.53 us, about 1.03x the archived Rust
13.19 us result. Profiling now attributes 72.7% flat time to required native
crossings; a separate duplicate-validation experiment regressed 2.2% and was
fully reverted.
