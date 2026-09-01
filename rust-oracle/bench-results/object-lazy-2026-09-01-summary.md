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
