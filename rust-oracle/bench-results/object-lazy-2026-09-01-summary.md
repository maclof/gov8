# Lazy data-property first-read comparative benchmark - 2026-09-01

Both implementations used rusty_v8 `152.2.0` / V8 `15.2.124.1-rusty` on
the same otherwise-idle Windows host. Platform initialization, isolate and
context creation, and the outer handle scope were outside the timed region.

Each iteration opened a fresh nested handle scope, created an object and
String key, installed one lazy getter returning 42, performed the first read
that materialized the data property, validated the exact integer result, and
closed the nested scope. Both harnesses perform the same validation once
before timing.

Commands:

```text
go test . -run '^$' -bench '^BenchmarkLazyDataPropertyFirstRead$' -benchtime=1s -count=5
cargo bench --manifest-path rust-oracle/Cargo.toml --bench object -- object/lazy_data_property_first_read --noplot
```

Go reports five independent one-second samples. Rust reports Criterion's
slope 95% confidence interval from 50 samples after a one-second warm-up and
a three-second measurement period. The harnesses use different statistical
models, so the ratio is directional.

| Go ns/op samples | Go B/op samples; allocs/op | Rust slope 95% CI |
|---:|---:|---:|
| 8233, 8447, 10353, 8420, 8857 | 741, 736, 731, 745, 680; 22 | 1476.8-1683.1 ns |

The Go sample mean is 8862 ns/op, about 5.61x the midpoint of the Rust
confidence interval. This is a measured performance gap, not an accepted
performance-parity claim. Go's callback registry, Windows DLL crossings,
owned-wrapper validation, and per-iteration callback retention are concrete
optimization targets.

Raw Criterion estimates, samples, and Tukey fences are stored in
`criterion-object-lazy-2026-09-01/`. Machine metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.

## Callback-reuse follow-up

The Go path was subsequently changed to reuse the native callback context for
the same zero-data getter function, validate the Name at the consuming shim
boundary, combine the borrowed Scope and CallbackScope allocation, and avoid
the generic ReturnValue setter for int32 results. Focused lifecycle, failure,
callback-retention, conformance, race, and vet checks passed.

Fresh before/after runs used the same commands and matched workload described
above. The before Go median was 6866.5 ns/op at 20 allocations. The final Go
samples were 3470, 3553, 5071, 5311, 5363, 5425, 5317, 5053, 5194, and 5738
ns/op at 464 B/op and 15 allocations/op. The median fell to 5252.5 ns/op,
23.5% below the fresh baseline, with 25% fewer allocations.

The immediately following Rust run reported a 1094.2-1300.5 ns slope 95%
confidence interval with a 1194.4 ns point estimate. The post-change Go median
therefore remains about 4.40x the Rust estimate. General scope, object, string,
property-read, numeric-conversion, and DLL callback crossings now dominate the
matched workload, so this remains a measured performance gap.

## ABI-39 direct install-status follow-up

An additive direct-status export now returns the lazy-property installation
result without a Go output pointer, and the Go wrapper calls its cached address
with a fixed nine-argument syscall. The compatibility export remains available.

Seven paired 500 ms samples changed from 4601, 3995, 4985, 5290, 5854, 5724,
and 5523 ns/op to 2990, 3045, 4043, 4502, 4223, 4288, and 3999 ns/op. The
median fell from 5290 to 4043 ns/op (23.6%), while allocation cost fell from
448 bytes/14 allocations to 360 bytes/12 allocations. Against the frozen Rust
1194.4 ns point estimate, the conservative paired ratio is now about 3.38x.
Focused lifecycle, callback-retention, conformance, race, and vet checks pass.
