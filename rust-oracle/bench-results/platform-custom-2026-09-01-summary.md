# Custom-platform dispatch comparative benchmark - 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same Windows host.
The timed unit allocates one native no-op `v8::Task`, dispatches it through the
custom-platform callback, runs it once, and deletes it. Both harnesses perform
one correctness probe, 10,000 explicit warm-up operations, reset all counters,
and validate counts outside timing.

The matched baseline used commit `21f395d`'s production implementation with
only the new atomic benchmark harness applied:

```text
go test ./conformance/platform-custom -run '^$' -bench '^BenchmarkCustomPlatformNoopTaskDispatch$' -benchmem -benchtime=3s -count=10 -cpu=1
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

## Retained-work follow-up

A second pass defers retained-work registration and finalizer installation
until the posting callback returns. Synchronously consumed tasks therefore
skip both map operations, while retained tasks keep the same disposal and
teardown guarantees. The active platform entry and consumed handle use atomic
fast paths, and the native task exports are resolved once. Conformance,
lifecycle, panic, race, root, and vet checks passed.

Ten fresh-process three-second A/B pairs against commit `ca9fb09` produced
baseline samples 460.0, 587.9, 492.1, 453.6, 456.1, 477.8, 456.4, 546.5,
459.5, and 434.0 ns/op. Post-change samples were 426.6, 435.3, 387.7, 360.4,
507.1, 392.7, 383.2, 390.7, 417.2, and 404.6 ns/op. The median improved from
459.75 to 398.65 ns/op (13.3%), with post-change faster in nine of ten pairs;
allocation remained 48 B/op and one allocation/op.

Against the Rust confidence-interval midpoint above, the public Go path is
still about 9.1x slower. Diagnostic direct-transition and thread-ID floors
were approximately 47.6 ns and 51.35 ns respectively, while the native
callback/task/delete floor remained hundreds of nanoseconds. The remaining
allocation and callback boundary therefore remain measured performance work.

## ABI-39 cold-retention follow-up

Task and IdleTask now keep entry/work identifiers in a cold sidecar allocated
only when a posting callback returns without consuming the transferred task.
The synchronous matched path retains its one unique user-visible wrapper but
shrinks it from 48 to 32 bytes. Forced-GC conformance verifies retained tasks
still survive and drain exactly once during platform shutdown.

Ten fresh-process one-second pairs produced a baseline median of 315.85 ns and
a candidate median of 308.90 ns, a 2.2% improvement. Against the Rust 43.62 ns
confidence-interval midpoint, current overhead is about 7.08x. Profiles place
roughly 57% in callback runtime and 27% in `Task.Run`; the required callback
transition and owner check now dominate.

## Verified thread-ID and single-swap follow-up

The next slice reads the Windows amd64 TEB thread ID after a one-time
`GetCurrentThreadId` verification and falls back to that API on any mismatch.
`Task.take` and `IdleTask.take` also use their mutex-guarded atomic swap as the
single consumed check and claim, removing a redundant load without changing
ownership or finalization. Thirty-two locked OS threads compare both thread-ID
paths with Win32 for 1,000 iterations each; focused lifecycle, wrong-thread,
conformance, race and vet checks pass.

Twelve alternating fresh-process pairs used clean-master and current test
executables at a fixed 2,000,000 iterations:

| Version | Samples (ns/op) | Median | B/op; allocs/op |
|---|---:|---:|---:|
| clean master | 299.2, 300.9, 285.6, 289.9, 343.8, 302.9, 323.8, 323.3, 312.7, 329.6, 323.6, 301.2 | 307.8 | 32; 1 |
| current | 281.1, 275.2, 263.6, 296.6, 267.8, 302.7, 301.6, 307.1, 290.2, 296.2, 316.8, 265.5 | 293.2 | 32; 1 |

Current won 11/12 pairs and improved the median 4.74%. Against the archived
43.62 ns Rust confidence-interval midpoint, the remaining ratio is about
6.72x. This comparison measures the integrated thread-ID and single-swap
slice; it does not attribute the full change to either component alone.
