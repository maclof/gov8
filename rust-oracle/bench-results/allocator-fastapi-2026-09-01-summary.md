# ArrayBuffer allocator and Fast API comparative benchmarks - 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same Windows host.
The allocator workload creates and immediately frees one 64-byte backing store;
both harnesses verify exactly one allocation and one free callback per
iteration. Each Fast API iteration invokes a precompiled JavaScript loop that
calls the target 256 times. Both harnesses verify the exact native-fast or slow
callback counter delta outside the timed region.

Commands:

```text
go test . -run '^$' -bench '^(BenchmarkArrayBufferAllocatorBackingStore|BenchmarkFastAPINativeOptimized|BenchmarkFastAPIGoSlowFallback)$' -benchmem -benchtime=1s -count=10
cargo bench --locked --bench array_buffer_allocator --bench fast_api_residual -- --save-baseline allocator-fastapi-2026-09-01
```

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI | Median ratio |
|---|---:|---:|---:|---:|
| backing store 64 create/free | 1234, 1173, 1067, 1055, 1078, 1094, 1082, 1066, 1070, 1160 | 88; 5 | 90.776-102.85 ns | 11.1x |
| native optimized loop, 256 calls | 4938, 5028, 7055, 6873, 6896, 7204, 6759, 7320, 6977, 7686 | 72; 2 | 1.0953-1.1594 us | 6.2x |
| slow callback loop, 256 calls | 237713, 222318, 257402, 241490, 233744, 224744, 227809, 229110, 221164, 219166 | 41032; 1282 | 7.3427-7.6528 us | 30.5x |

The ratios use each Go sample median and the Rust confidence-interval midpoint.
They are measured optimization gaps, not accepted performance parity. The slow
path crosses the Go callback boundary 256 times and allocates about five Go
objects per callback; the native path proves every call used the optimized
native address but still includes Go wrapper invocation of the outer loop.

Raw Criterion estimates, samples, Tukey fences, and reports are stored in
`criterion-allocator-fastapi-2026-09-01/`. Environment metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.

## Fixed-arity backing-store follow-up

The Go path now caches its create, allocator-clone, backing-store-dispose, and
allocator-dispose addresses and invokes them with fixed-arity Windows calls.
This preserves the independent allocator reference held by every BackingStore
while removing variadic frames from its hot lifecycle.

Ten one-second samples were 750.9, 1030, 1074, 1043, 1041, 1125, 1136, 1039,
1017, and 1038 ns/op at 48 bytes and 1 allocation per operation. The median
improved from 1,080 to 1,039.5 ns/op (3.8%), while allocations fell from 88
bytes/5 allocations to 48 bytes/1 allocation. The remaining roughly 10.7x gap
is dominated by two required native-to-Go allocator callbacks plus DLL/V8
transitions rather than Go heap allocation.

## Allocator registry-dispatch follow-up

An atomic most-recently-used registry entry now removes mutex acquisition from
the common single-allocator callback path, retaining the authoritative locked
map for multiple allocators and removal. Interleaved frozen-binary A/B medians
improved from 854.8 to 844.2 ns/op (about 1.2%); allocations remain 48 bytes/1.
CPU profiling shows about 73% less time in the Go dispatcher itself. The small
end-to-end change confirms that native-to-Go callback transitions, rather than
registry contention, dominate the remaining roughly 9x result on this run.

## Fast callback-options follow-up

The native Fast API target now observes immutable callback-options data once
per V8 data-object identity instead of repeating type/context checks on every
steady-state call. The exact fast-call counter still advances on every call and
reset clears the observation cache.

Controlled identical-build samples changed from 5290, 6456, 5917, 7872, and
7086 ns/op to 1493, 1572, 2003, 1967, and 1945 ns/op. The median improved 69.9%
from 6,456 to 1,945 ns/op, leaving about 1.73x versus the Rust 1,127.35 ns
midpoint. Allocations remain 72 bytes/2 for the outer Go `Function.Call`.
The ordinary Go callback fallback bypasses this native Fast API target. A later
fixed-arity/direct-conversion pass reduced its 256-call median from 164,758 to
138,750 ns and allocations from 1,282 to 258, leaving about 18.5x Rust. The
remaining separate gap is dominated by callback crossings and borrowed-scope
lifetime enforcement.

## Rust-matched native target follow-up

The Go benchmark previously reused the callback-options conformance target,
which checked the options data identity on every native call. The Rust target
receives the same callback-options argument but deliberately does not inspect
it. A separate native fixture target now matches that behavior while preserving
the observing target for conformance coverage. Both targets retain the same
sequentially consistent fast-call counter and `value + 1` result.

Ten alternating-order frozen-binary pairs measured the old target at 1464,
1521, 1544, 1504, 1749, 1675, 1548, 1470, 1480, and 1652 ns/op, versus 1403,
1369, 1469, 1397, 1426, 1395, 1374, 1442, 1473, and 1529 ns/op for the matched
target. Every pair improved; the median fell 7.7% from 1532.5 to 1414.5 ns/op.
Allocations remained 72 bytes and 2 allocations, and every sample verified
exactly 256 native fast calls and zero slow fallbacks per timed iteration. The
new median is about 1.25x the Rust midpoint of 1127.35 ns.

The candidate CPU profile attributes 95.6% cumulatively to the outer public
`Function.Call`; the DLL transition is 82.3% flat in `runtime.cgocall`, while
isolate and scope validation are 12.0% and 10.6% cumulative. Those checks and
the single outer native crossing remain part of the public workload.

## Split allocator-callback experiment

An ABI-40 experiment replaced the legacy allocator dispatcher with four
operation-specific callbacks. Interleaved frozen-binary samples produced a
1181 ns baseline median and a 1188 ns experimental median, 0.6% slower with
large outliers, so the additive export and Go path were fully reverted. The
final ABI-40 DLL uses the unchanged legacy callback and measured a 1042 ns
median in the subsequent control. The required allocation and free
native-to-Go transitions, plus the one 48-byte public `BackingStore` wrapper,
remain; no allocation reduction or behavioral change was retained.

## ABI-41 allocator floor audit

The allocator workload was rechecked against the committed ABI-41 DLL
`0FBD464E2A21526067125465110DCB25C2BCBAD86C4B229809DE9E33A66CA6BF`.
Both languages compile against V8 `15.2.124.1-rusty`, create and immediately
free one 64-byte zeroed backing store per iteration, and validate exactly one
allocation and one free callback outside timing. The Go harness now also runs
the same untimed correctness probe as Rust and measures counter deltas so the
probe is excluded from the timed result.

Seven fresh one-second public samples were 746.3, 896.8, 764.8, 769.3, 764.1,
770.1, and 773.0 ns/op at 48 bytes and one allocation. Their 769.3 ns median is
7.95x the pinned Rust 96.813 ns confidence-interval midpoint, so the prior
roughly 9x wording remains directionally correct but overstates this run.

A five-second CPU profile measured 753.2 ns/op. Native/DLL calls accounted for
63.5% flat and 86.7% cumulative time. Go callback runtime frames accounted for
14.2% cumulative time, while the complete Go dispatcher body was below 1%
cumulative. The allocation profile attributed the sole 48-byte allocation to
the public `BackingStore` wrapper.

Additional diagnostics used the same DLL and public setup:

| Diagnostic | ns/op samples | Median | B/op; allocs/op |
|---|---:|---:|---:|
| Exact public host allocator, balanced with native-handle floor | 1016, 796.2, 819.0, 807.5, 802.4, 755.0, 759.2, 799.0 | 800.7 | 48; 1 |
| Host allocator, native handles only | 559.3, 575.2, 593.4, 556.5, 552.5, 548.7, 533.1, 533.9 | 554.5 | 0; 0 |
| Public host allocator without benchmark counter bodies | 742.9, 778.9, 740.9, 757.0, 778.9, 846.7, 747.0 | 757.0 | 48; 1 |
| Public default native allocator | 380.1, 418.7, 438.3, 413.2, 482.2, 631.5, 581.3 | 438.3 | 48; 1 |
| Default allocator, native handles only | 217.8, 241.9, 282.2, 265.1, 275.1, 282.1, 292.5 | 275.1 | 0; 0 |

The native-handle host floor retains both native-to-Go callbacks and verifies
their exact counts, but omits the public wrapper and the extra allocator shared
reference that permits a Go backing store to outlive its isolate. The default
native-handle floor omits callbacks as well. Together these measurements show
that the remaining result is not registry contention or Go heap churn: it is
split between required callback/runtime crossings and the public ownership
lifecycle layered over V8's allocation work.

The end-to-end workloads are observably equivalent, but their callback ABIs are
not equally cheap. Rust calls unsafe native callbacks directly in-process; Go's
safe callback contract crosses the Windows runtime boundary twice. Go also
retains an independent allocator reference for its post-isolate backing-store
lifetime behavior. No production change was accepted: removing those costs
would weaken callback observation or backing-store ownership. The previously
rejected split-callback ABI was not repeated.

## ABI-41 Function call validation follow-up

The matched Fast API benchmark makes one public `Function.Call` for each
256-call optimized JavaScript loop. When the function, supplied scope,
receiver, and arguments name the exact same Go `Scope`, the function check now
proves isolate affinity and scope lifetime once. Non-alias calls retain their
previous validation sequence and error ordering. This changes neither the
inner callback-options ABI nor the exact fast/slow counter proof.

Eight order-balanced frozen-executable one-second pairs measured the original
path at 1262, 1622, 2617, 1333, 1241, 1266, 1193, and 1494 ns/op, versus 1488,
1229, 1495, 1391, 1153, 1174, 1169, and 1185 ns/op for the same-scope path.
The candidate won six pairs and reduced the median 7.1%, from 1299.5 to 1207
ns/op. Allocations remained 72 bytes and two allocations. The current median
is about 1.07x the pinned Rust 1127.35 ns midpoint.

A five-second profile changed from 1531 to 1404 ns/op. Cumulative
`Isolate.check` time fell from 15.5% to 3.0%, and `currentThreadID` from 14.9%
to 2.85%; the native transition consequently rose from 80.2% to 83.4% flat.
Caching the procedure lookup by itself was neutral or slower and was rejected.
The remaining two allocations marshal the single outer argument and output
wire; an additive fixed-arity native export was not justified at this residual
gap.
