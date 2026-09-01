# CRDTP dispatcher comparative benchmark - 2026-09-01

Both implementations used rusty_v8 `152.2.0` / V8 `15.2.124.1-rusty` on
the same otherwise-idle Windows host. Channel, dispatcher, one wired `Bench`
domain, JSON-to-CBOR conversion, and parsed dispatchable construction were
outside the timed region.

Each iteration reused `{id:1,method:"Bench.ok",params:{}}`, synchronously
dispatched it, validated the command and call ID, created and sent a success
response, serialized the response, converted it to JSON, validated exact
`{"id":1,"result":{}}`, and checked that both callback counters advanced once.

Commands:

```text
go test . -run '^$' -bench '^BenchmarkCRDTPDispatcherDispatchSuccess$' -benchmem -benchtime=3s -count=5
cargo rustc --locked --manifest-path rust-oracle/Cargo.toml --release --bench crdtp_dispatcher
rust-oracle/target/release/deps/crdtp_dispatcher-1390560b69188395.exe --bench --noplot
```

Go reports five independent three-second samples. Rust reports Criterion's
slope 95% confidence interval from 50 samples after a one-second warm-up and a
three-second measurement period. The harnesses use different statistical
models, so the ratio is directional.

| Go ns/op samples | Go B/op; allocs/op | Rust slope 95% CI |
|---:|---:|---:|
| 3737, 3260, 3319, 3278, 3128 | 472; 26 | 1277.5-1418.5 ns |

The Go sample mean is 3344.4 ns/op, about 2.48x the midpoint of the Rust
confidence interval. This is a measured performance gap, not an accepted
performance-parity claim. Go's callback registry, Windows DLL crossings,
owned wrapper validation, and heap-pinned native out parameters are concrete
optimization targets.

The first long run also exposed that `callErr` had hidden
`syscall.Proc.Call`'s `//go:uintptrescapes` contract. Nested native-to-Go
callbacks could therefore pass movable stack addresses back to native code.
The benchmark failed between roughly 15,000 and 680,000 iterations before the
fix. The wrapper now preserves the escape/liveness contract, all CRDTP output
paths are direct calls, a 350,000-iteration regression test passes, and five
subsequent measured runs completed one million routes each.

Raw Criterion estimates, samples, and Tukey fences are stored in
`criterion-crdtp-dispatcher-2026-09-01/`. Machine metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.

## Fixed-dispatch follow-up

A second pass replaced allocation-producing variadic calls with cached,
fixed-arity dispatch; keeps reentrant response ownership outputs heap-stable;
and reuses an eight-byte invocation buffer for the common short domain
command. Exact conformance, negative lifecycle, wrong-thread and panic tests,
two race runs, a 350,000-route nested-output stress test, vet, and diff checks
passed.

A strict clean-HEAD baseline using the same ABI-37 DLL produced 4140, 4063,
3958, 3785, 3821, 4314, 3920, 3716, 3793, and 3861 ns/op at 392 B/op and 17
allocations/op. Final samples were 2942, 3060, 3100, 3129, 3110, 2893, 2817,
3058, 3021, and 2916 ns/op at 176 B/op and 6 allocations/op. The median fell
from 3890.5 to 3039.5 ns/op (21.9%), bytes fell 55.1%, and allocations fell
64.7%.

The immediately paired pinned Rust run reported a 1455.5-1682.6 ns slope 95%
confidence interval, midpoint 1579.4 ns. Final Go is therefore about 1.92x
slower. Profiles attribute the remaining material gap primarily to DLL and
native-to-Go callback transitions.
