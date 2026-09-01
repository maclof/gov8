# Synthetic module and simdutf comparative benchmarks - 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same otherwise-idle
Windows host. Synthetic-module platform, isolate, context, and outer handle
scope construction were outside the timed region. The simdutf benchmarks used
identical 4096-byte mixed inputs and preallocated destinations.

Commands:

```text
go test -run '^$' -bench '^(BenchmarkSyntheticModule(Create|CreateInstantiateEvaluate)|BenchmarkSIMDUTF(ValidateUTF8Mixed4K|UTF8ToUTF16LE4K|UTF16LEToUTF8_4K|Base64DecodeStandard4K))$' -benchmem -benchtime=1s -count=5 .
cargo bench --locked --bench modules_synthetic --bench simdutf
```

Go reports five independent one-second samples. Rust reports Criterion's mean
95% confidence interval from 50 samples after a one-second warm-up and a
three-second measurement period. These are native harnesses with different
statistical models, so ratios are directional rather than claims of identical
sampling.

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI |
|---|---:|---:|---:|
| Synthetic create | 1612, 1674, 2036, 2121, 2006 | 248; 10 | 286.50-314.36 ns |
| Synthetic create, instantiate, evaluate | 5596, 5405, 5521, 5509, 5795 | 712; 28 | 783.04-856.70 ns |
| simdutf validate mixed UTF-8 4 KiB | 346.5, 328.7, 332.2, 339.8, 426.2 | 80; 2 | 194.49-204.88 ns |
| simdutf UTF-8 to UTF-16LE 4 KiB | 1310, 1321, 1303, 1269, 1394 | 80; 2 | 1093.6-1107.4 ns |
| simdutf UTF-16LE to UTF-8 4 KiB | 1140, 795.6, 773.7, 775.6, 763.3 | 80; 2 | 754.04-762.77 ns |
| simdutf base64 decode 4 KiB | 355.2, 362.6, 366.3, 370.9, 351.1 | 192; 4 | 115.46-120.66 ns |

Synthetic-module operations are currently about 6x slower through the Go
wrapper. The simdutf wrapper is about 1.7x slower for validation, about 1.2x
slower for UTF-8 to UTF-16LE, near native for UTF-16LE to UTF-8 after one noisy
Go sample, and about 3x slower for base64 decode. The Go allocation counts and
FFI boundary are concrete optimization targets; these regressions are recorded
findings, not accepted performance-parity claims.

Raw Criterion estimates, samples, Tukey fences, and reports are stored in
`criterion-synthetic-simdutf-2026-09-01/`. Machine metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.

## simdutf fixed-boundary follow-up

The Go wrapper was then changed to cache its resolved exports, combine base64
size validation with conversion, and use fixed-arity native calls for the five
matched hot paths. The safety checks still run before the underlying unsafe
simdutf primitives. A race-only conformance failure also exposed a
stack-backed-slice pointer-lifetime bug; preserving pointer provenance through
the syscall expression fixed it, and the repeated race suite passes.

Commands:

```text
go test . -run '^$' -bench '^BenchmarkSIMDUTF' -benchmem -benchtime=500ms -count=5
cargo bench --locked --bench simdutf
```

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI |
|---|---:|---:|---:|
| validate mixed UTF-8 4 KiB | 211.4, 212.7, 214.8, 275.8, 276.0 | 0; 0 | 214.63-222.30 ns |
| UTF-8 to UTF-16LE 4 KiB | 1196, 1160, 1260, 1168, 1270 | 0; 0 | 1070.2-1078.2 ns |
| UTF-16LE to UTF-8 4 KiB | 751.1, 752.4, 743.7, 763.1, 786.5 | 0; 0 | 627.30-645.92 ns |
| base64 decode 4 KiB | 197.2, 192.1, 194.1, 194.4, 204.4 | 0; 0 | 118.87-126.06 ns |
| base64 encode 3 KiB | 132.8, 123.7, 126.6, 114.3, 113.5 | 0; 0 | 53.910-58.274 ns |

Validation is at parity in these samples. The transcoders remain about
1.1-1.2x slower, decode about 1.6x, and encode about 2.2x. The short base64
operations are now dominated by the DLL transition rather than Go allocation
or redundant input scans. Complete current Criterion samples and reports are
stored in `criterion-simdutf-fast-2026-09-01/`; the earlier table remains as
the reproducible baseline rather than being overwritten.

## Synthetic-module follow-up

The synthetic-module path now reuses the module's existing native persistent
handle for callback identity, keeps up to eight export wires on the stack,
avoids small duplicate-check sets and transient string copies, and dispatches
the ten-argument creation call through a fixed-arity syscall. The Go registry
also stores callback entries by value. Pointer-bearing string data remains
GC-visible through the call, including for stack-backed caller strings.

Matched Go command:

```text
go test . -run '^$' -bench '^BenchmarkSyntheticModule(Create|CreateInstantiateEvaluate)$' -benchmem -benchtime=1s -count=10
```

| Operation | Version | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI |
|---|---|---:|---:|---:|
| create | baseline | 1832, 1799, 2855, 2327, 2386, 2604, 2636, 2207, 2380, 2801 | 248; 10 | - |
| create | optimized | 1238, 1195, 1646, 1660, 1687, 1736, 1703, 1596, 1660, 1650 | 136; 6 | 535.89-615.53 ns |
| create, instantiate, evaluate | baseline | 5641, 5872, 6038, 5724, 5699, 6020, 6351, 6633, 7228, 6461 | 664; 22 | - |
| create, instantiate, evaluate | optimized | 4850, 4720, 4899, 4966, 4972, 5134, 4944, 4914, 4731, 5221 | 552; 18 | 1.2766-1.4200 us |

The create median improved from 2383 to 1655 ns/op (30.5%) and the full path
from 6029 to 4929 ns/op (18.2%). The remaining gaps are about 2.9x and 3.7x
against the Rust mean, dominated by DLL/V8 creation and the evaluation callback
transition. Complete current Rust samples are stored in
`criterion-synthetic-fast-2026-09-01/`.

## Synthetic callback-registry follow-up

Callback IDs are now allocated atomically and published only after native
module creation succeeds. Native addresses are cached, export names are passed
without a transient byte copy, and evaluation uses fixed-arity dispatch with
explicit pointer-escape semantics. Ten 500 ms samples measured:

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI |
|---|---:|---:|---:|
| create | 1646, 1700, 1725, 1726, 1805, 1698, 1807, 1797, 1733, 2602 | 136; 6 | 535.89-615.53 ns |
| create, instantiate, evaluate | 4544, 4854, 5948, 5468, 8224, 7952, 6876, 5434, 5657, 5286 | 424; 14 | 1.2766-1.4200 us |

The full path removes another 128 bytes and four allocations. Its timing was
noisier and did not demonstrate a stable latency improvement over the preceding
run, so synthetic modules remain roughly 3-4x Rust rather than being promoted
to performance parity.

## simdutf redundant-dispatch follow-up

The five fixed hot exports cache raw procedure addresses. Successful fast calls
no longer clear an error string that is only observed after a negative status,
and base64 capacity checks use simdutf's exact scalar formulas locally instead
of making a second runtime-selected simdutf call. All paths remain allocation
free and the complete normal and race conformance suites pass.

Ten 500 ms current-DLL samples gave median times of 275 ns for validation,
1,203 ns for UTF-8 to UTF-16LE, 750 ns for UTF-16LE to UTF-8, 198 ns for base64
decode, and 123 ns for base64 encode. Alternating old/new DLL runs were highly
frequency-sensitive and showed no stable additional latency reduction. The
current ratios therefore remain approximately 1.1-1.2x Rust for transcoding and
1.6-2.2x for short base64 calls; this change removes redundant native work but
does not close the Windows DLL-transition floor.

## ABI-39 scope and synthetic-cleanup follow-up

The generic HandleScope improvement removes two allocations from both matched
synthetic workloads. Synthetic-module close now also uses its already-cached
unregister export directly, removing one further allocation with neutral paired
latency. Seven 500 ms current-HEAD control/candidate pairs changed create from
112 bytes/3 allocations to 96 bytes/2 and the full create/instantiate/evaluate
path from 312 bytes/9 allocations to 296 bytes/8. Median times were effectively
flat at 1254 versus 1249 ns and 3230 versus 3225 ns, respectively.

Against the frozen Rust midpoints, these current controlled times are about
2.17x for create and 2.39x for the full path. The improvement beyond the
explicit scope/cleanup allocation savings is treated as machine-regime drift,
not attributed to these edits.
