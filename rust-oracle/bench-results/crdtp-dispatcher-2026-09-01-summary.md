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

## ABI-40 callback-view follow-up

An additive callback export now supplies a native-owned serialized byte view
for the duration of the channel callback. Go copies that ephemeral view while
the callback is active and clears it on exit; retained and post-callback
`Bytes` calls continue through the original owned native value. The common
response output slot is embedded in a heap-stable callback frame, with a
separate heap fallback for nested/reentrant sends.

Against a frozen ABI-39 baseline, the median changed from 3279 ns at 176 B/6
allocations to 3001 ns at 192 B/5 allocations, an 8.48% time improvement and
one fewer allocation. The extra 16 bytes hold the two view words. Using the
pinned Rust midpoint of 1579.05 ns, the current route is about 1.90x Rust.
Normal, race and exact conformance runs cover repeated and retained byte access;
a 350,000-route nested-output stress test verifies reentrant storage safety.

## ABI-41 bounded reprofile

The exact matched workload was reprofiled against the committed ABI-41 DLL
`0FBD464E2A21526067125465110DCB25C2BCBAD86C4B229809DE9E33A66CA6BF`.
The Rust and Go timed boundaries remain equivalent: both reuse the parsed
request; synchronously dispatch one domain callback; validate command and call
ID; create and send a success response; copy its serialized CBOR; convert and
validate the exact JSON; and verify one callback and delivery per iteration.

Seven fresh one-second Go samples were 2372, 2454, 3571, 3835, 3549, 3684,
and 3832 ns/op at 192 bytes and five allocations. Their 3571 ns median is 2.26x
the pinned Rust 1579.05 ns midpoint. Host frequency varied materially during
this pass: the eight baseline legs of the subsequent balanced experiment were
3081, 2761, 2762, 2494, 2460, 4378, 2581, and 2855 ns/op, a 2761.5 ns median
or 1.75x Rust. The prior roughly 1.9x result remains representative of the
observed range.

A five-second profile measured 3200 ns/op. Native/DLL transitions accounted
for 68.3% flat and 92.5% cumulative CPU. The nested send-response route was
65.8% cumulative, channel delivery 31.4%, and CBOR-to-JSON validation 23.4%.
Required borrowed-value thread checks accounted for about 5.1%. The five Go
allocations were the retainable domain invocation and channel message wrappers,
the synchronized success-response wrapper, and the two owned serialized byte
copies used for exact validation.

Two Go-only experiments were rejected:

- A value-returning response constructor became inlineable, but the response
  still escaped through `sync.Mutex.Lock` in public `SendResponse`. Allocation
  remained 192 bytes and five allocations, so the experiment was reverted.
- Calling `syscall.Syscall6` directly instead of the annotated escaping wrapper
  preserved allocations but regressed the balanced median from 2761.5 to
  2876 ns/op (4.1%); only three of eight pairs improved. Candidate samples were
  4288, 3114, 2746, 2670, 3546, 2529, 3006, and 2616 ns/op. It was reverted.

No production change was accepted. The remaining difference is dominated by
the required native-to-Go callback topology and owned validation copies; the
retainable callback wrappers, thread checks, and heap-stable reentrant output
storage enforce public lifecycle and moving-stack safety.
