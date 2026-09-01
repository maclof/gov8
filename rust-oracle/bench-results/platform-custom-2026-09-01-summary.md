# Custom-platform dispatch comparative benchmark - 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same Windows host.
The timed unit allocates one native no-op `v8::Task`, dispatches it through the
custom-platform callback, runs it once, and deletes it. Both harnesses perform
one correctness probe, 10,000 explicit warm-up operations, reset all counters,
and validate counts outside timing.

The matched baseline used commit `21f395d`'s production implementation with
only the new atomic benchmark harness applied:

```text
go test ./conformance-platform-custom -run '^$' -bench '^BenchmarkCustomPlatformNoopTaskDispatch$' -benchmem -benchtime=3s -count=10 -cpu=1
```

The optimized implementation used the same command with `-count=15`.

| Version | Go ns/op samples | Go B/op; allocs/op |
|---|---:|---:|
| baseline | 809.0, 792.6, 852.5, 821.5, 715.8, 791.5, 794.1, 883.9, 760.3, 751.9 | 56; 2 |
| optimized | 534.6, 572.0, 586.6, 587.3, 610.6, 573.6, 521.0, 536.2, 547.5, 560.4, 650.6, 604.5, 620.5, 590.4, 502.5 | 48; 1 |

The median improved from 793.35 to 573.6 ns/op (27.7%) and removed one
allocation. The paired Rust command was:

```text
cargo bench --locked --bench platform_custom_dispatch
```

The final Rust mean 95% confidence interval was 40.59-46.65 ns/op. Go's real
`Task.Run(*Isolate)` path performs public owner-thread and isolate-identity
checks that Rust's opaque task token does not expose, but the roughly 13x
remaining callback/ownership-boundary gap is still tracked as performance
work rather than accepted parity. Complete Criterion samples and reports are
stored in `criterion-platform-custom-2026-09-01/`; machine metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.
