# Performance follow-up - 2026-09-02

All measurements used Windows amd64 on an AMD Ryzen 9 PRO 7940HS, Go 1.26.2,
Rust 1.98.0, and V8 `15.2.124.1-rusty`. The source baseline was commit
`40536e7`. Go comparisons used separate frozen test executables and alternated
AB/BA process order. The Rust values are midpoints of the pinned Criterion
confidence intervals already archived in this directory.

## Function-call procedure cache

The candidate resolves `gov8_ca_function_call` once but continues to use
`syscall.Proc.Call`, preserving its `go:uintptrescapes` handling across
synchronous Go callback re-entry. Each sample ran 1,000,000 operations.

| Workload | Baseline ns/op samples | Candidate ns/op samples | Median change | Wins | B/op; allocs/op |
|---|---|---|---:|---:|---:|
| callback from JavaScript | 921.0, 971.5, 941.2, 945.7, 979.5, 996.0, 968.4, 1055, 1075, 962.8, 982.9, 961.7 | 1119, 952.8, 923.9, 971.7, 930.3, 977.8, 954.5, 1039, 991.4, 1007, 964.8, 943.4 | 969.95 to 968.25 ns (-0.18%) | 9/12 | 208; 3 |
| callback from Go | 1056, 1048, 1049, 1043, 1034, 1212, 1041, 1089, 1132, 1064, 1066, 1048 | 1139, 1004, 987.7, 1004, 1036, 1031, 984.8, 1042, 1042, 1025, 1047, 979 | 1052.5 to 1028 ns (-2.33%) | 10/12 | 256; 6 |
| Function new and call | 2860, 2785, 2595, 2595, 2617, 3014, 2740, 2924, 2876, 2716, 2689, 2850 | 2909, 2762, 2574, 2557, 2759, 2570, 2608, 2922, 2868, 2735, 2600, 2506 | 2762.5 to 2671.5 ns (-3.29%) | 9/12 | 448; 7 |

CPU profiles put 44-55% flat time in `runtime.cgocall`; reverse callback
runtime frames account for another 15-26% cumulatively. A direct fixed-arity
Windows call removed 80 bytes and one allocation but regressed the JavaScript
control repeatably, so that separate experiment was rejected.

## Allocator-aware backing-store ownership

The retained candidate moves the Go wrapper's extra allocator shared reference
into the native backing-store wrapper. It preserves V8's allocator reference
plus the explicit post-isolate guard while replacing a clone call and a later
dispose call with the existing create/dispose transitions. Each long sample
ran 2,000,000 create/free operations.

| Version | ns/op samples | Median | B/op; allocs/op |
|---|---|---:|---:|
| ABI-43 baseline | 673.8, 704.8, 675.8, 670.7, 672.9, 674.2, 676, 661.5, 674.6, 695.3 | 674.4 | 48; 1 |
| allocator-aware candidate | 603.5, 605, 605.5, 607.9, 608.2, 611.7, 624.3, 614.5, 603, 609.9 | 608.05 | 48; 1 |

The candidate won 10/10 pairs and improved the median 9.84%. Native host and
default-allocator controls were flat, so the improvement is attributable to
the removed ownership transitions. The remaining result is about 6.28x the
Rust 96.813 ns midpoint and remains dominated by the two required allocator
callbacks. The baseline ABI-43 DLL SHA-256 was
`1B84B0E672A500337F74613938ACC0BC4512A319E9F6EE5C14524AE209834935`;
the final ABI-44 DLL SHA-256 is
`8CEB37068FB30C0D326C47B6718A3E08BC969B60E333464A5ED46AE76255D87A`.

## Wasm restoration benchmark alignment

The Go benchmark did not hold the untimed outer Context scope used by the Rust
workload. After aligning that setup and adding explicit timer fences, ten
balanced samples changed from 311.1, 364.8, 326.4, 322.1, 319.4, 337.2,
342.4, 327.9, 337.8, and 331.1 ns/op to 333.2, 333.7, 338.2, 330.7, 342.0,
331.1, 338.8, 335.5, 354.2, and 393.2 ns/op. The aligned median is 336.85
ns/op at 48 bytes and one allocation, about 2.25x the Rust 149.43 ns midpoint.
This is a benchmark-validity correction, not a production optimization.

## Rejected boundary-local experiments

Promise settlement profiles placed 55-60% flat time in `runtime.cgocall`; the
Promise methods themselves allocate nothing. Making the hot-procedure resolver
inlineable was neutral for resolver settlement and regressed then/checkpoint,
so it was reverted. Current long-run medians are about 5.33x and 3.12x their
Rust comparators. Custom-platform dispatch similarly remained about 242-246
ns/op at 32 bytes and one allocation (roughly 5.5-5.6x Rust); two inlining
candidates were noise or regressions and were reverted.

Focused normal, race, conformance, vet, formatting, and ownership tests passed
for every retained candidate. Rejected candidates and temporary profiles were
removed.
