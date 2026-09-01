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

## Split allocator-callback experiment

An ABI-40 experiment replaced the legacy allocator dispatcher with four
operation-specific callbacks. Interleaved frozen-binary samples produced a
1181 ns baseline median and a 1188 ns experimental median, 0.6% slower with
large outliers, so the additive export and Go path were fully reverted. The
final ABI-40 DLL uses the unchanged legacy callback and measured a 1042 ns
median in the subsequent control. The required allocation and free
native-to-Go transitions, plus the one 48-byte public `BackingStore` wrapper,
remain; no allocation reduction or behavioral change was retained.
