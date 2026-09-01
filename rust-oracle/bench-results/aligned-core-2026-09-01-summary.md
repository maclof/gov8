# Aligned core comparative benchmarks - 2026-09-01

Both implementations used V8 `15.2.124.1-rusty` on the same Windows host.
These 16 workloads use matching sources, arguments, result checks, handle
lifetimes, isolate/context setup, and untimed correctness probes. Go reports
five independent one-second samples. Rust reports Criterion's mean 95%
confidence interval from 50 samples after a one-second warm-up and three-second
measurement period.

Commands:

```text
go test . -run '^$' -bench '^(BenchmarkStartup(IsolateNewDispose|IsolateContextNewDispose|ContextNewDispose)|BenchmarkScript(CompileMinimal|CompileWorkload|CompileAndRunMinimal|CompileAndRunWorkload|RunPrecompiledWorkload)|BenchmarkCallback(NativeCallFromJS|NativeCallFromHost|FunctionNewCall)|BenchmarkPromise(ResolverNewResolve|ResolveThenCheckpoint)|BenchmarkModule(Compile|CompileInstantiate|CompileInstantiateEvaluate))$' -benchmem -benchtime=1s -count=5
cargo bench --locked --bench startup --bench script --bench callback --bench promise --bench module -- --save-baseline aligned-core-2026-09-01
```

| Operation | Go ns/op samples | Go B/op; allocs/op | Rust mean 95% CI | Median ratio |
|---|---:|---:|---:|---:|
| startup/isolate_new_dispose | 1016290, 961504, 1294112, 1194706, 1159604 | 88; 2 | 1.4949-1.6869 ms | 0.73x |
| startup/isolate_context_new_dispose | 1526483, 1501050, 1531047, 1635937, 1769472 | 136; 5 | 1.4862-1.5785 ms | 1.00x |
| startup/context_new_dispose | 392014, 361816, 375456, 421455, 371496 | 48; 3 | 335.52-370.67 us | 1.06x |
| script/compile_minimal | 1461, 1511, 1552, 1423, 1531 | 176; 7 | 393.27-492.86 ns | 3.41x |
| script/compile_workload | 1903, 1866, 1654, 1780, 1895 | 176; 7 | 718.59-783.00 ns | 2.49x |
| script/compile_and_run_minimal | 2092, 2108, 2314, 2034, 1986 | 288; 11 | 611.01-653.60 ns | 3.31x |
| script/compile_and_run_workload | 48180, 51998, 52042, 51779, 57773 | 4416; 13 | 76.106-98.686 us | 0.59x |
| script/run_precompiled_workload | 7582, 7286, 7700, 6888, 7540 | 4304; 9 | 4.0008-4.1757 us | 1.84x |
| callback/native_call_from_js | 3426, 3418, 3162, 3155, 3219 | 632; 19 | 144.63-164.27 ns | 20.84x |
| callback/native_call_from_rust / Go host | 2916, 2960, 2913, 2997, 3006 | 544; 18 | 134.76-142.45 ns | 21.36x |
| callback/function_new_call | 5310, 5106, 6026, 5739, 4996 | 808; 21 | 958.37-1027.6 ns | 5.35x |
| promise/resolver_new_resolve | 2748, 2696, 2732, 2607, 2571 | 344; 15 | 118.89-131.17 ns | 21.56x |
| promise/resolve_then_checkpoint | 3650, 3634, 3585, 3712, 3656 | 440; 18 | 398.36-423.04 ns | 8.89x |
| module/compile | 1699, 2084, 2266, 1966, 1746 | 224; 7 | 657.89-789.72 ns | 2.72x |
| module/compile_instantiate | 10685, 10379, 10354, 10540, 10262 | 4808; 23 | 4.5700-5.3264 us | 2.10x |
| module/compile_instantiate_evaluate | 13492, 12221, 11860, 14036, 13651 | 4872; 26 | 4.6538-5.2484 us | 2.73x |

Ratios use the Go sample median and Rust confidence-interval midpoint. Startup
is at parity and the larger compile-and-run script workload is faster in Go on
this run. Small script/module operations retain roughly 1.8-3.4x wrapper
overhead. Native callbacks and promise creation show the largest new gaps,
roughly 5.4-21.6x, dominated by DLL crossings, Go wrappers, registry/lifetime
bookkeeping, and the Go callback ABI. These are measured optimization findings,
not behavioral gaps or accepted performance parity.

Raw Criterion estimates, samples, Tukey fences, and reports are stored in
`criterion-aligned-core-2026-09-01/`. Environment metadata is in
`env-2026-09-01-DESKTOP-VJI58KR.txt`.

## ABI-38 callback follow-up

Cached fixed-arity scalar conversions and setters remove generic variadic
frames. Callback conversions that may execute JavaScript use a one-call native
thread-local result slot, so nested Go re-entry cannot invalidate an output
pointer on a moving Go stack. A regression reproduces that former failure and
checks nested coercion, `MinInt64`, and `MaxUint32` under normal and race runs.

Controlled 500 ms samples changed as follows:

| Operation | Before median | After samples (ns/op) | After median | Current Rust ratio |
|---|---:|---:|---:|---:|
| callback/native_call_from_js | 3158 ns | 2722, 2613, 2485, 2562, 2325 | 2562 ns | 16.6x |
| callback/native_call_from_host | 2995 ns | 2340, 2321, 2878, 4039, 4235 | 2878 ns | 20.8x |
| callback/function_new_call | 4946 ns | 4687, 4279, 4287, 5763, 5729 | 4687 ns | 4.72x |

The separate 256-call ordinary Fast API fallback improved from a 164,758 ns
median and 41,032 bytes/1,282 allocations to 138,750 ns and 20,552 bytes/258
allocations. Its remaining ratio is about 18.5x Rust. The callback crossing and
the retained invocation object required to invalidate borrowed scopes remain.

## ABI-38 Promise follow-up

Five additive direct-return exports retain the original compatibility exports
and error statuses while removing escaping Go output locals. Seven interleaved
frozen-binary pairs produced these distributions:

| Operation | Before samples (ns/op) | After samples (ns/op) | Allocation change | Current Rust ratio |
|---|---:|---:|---:|---:|
| promise/resolver_new_resolve | 1557, 1617, 1588, 1561, 1549, 1636, 1628 | 1147, 1097, 1132, 1071, 1102, 1093, 1066 | 344 B/15 to 152 B/6 | 8.77x |
| promise/resolve_then_checkpoint | 2636, 2663, 2835, 2576, 2608, 2700, 2675 | 2069, 1962, 2068, 1996, 2050, 2141, 1963 | 440 B/18 to 216 B/8 | 4.99x |

The medians improve 30.9% and 23.0%, respectively. Profiling leaves native
crossings, thread-affinity checks, and the required microtask checkpoint as the
dominant costs.

## ABI-38 script and module follow-up

Cached fixed-arity calls remove generic proc frames. Script/module calls that
can synchronously re-enter Go use explicit `uintptrescapes` wrappers; module
resolution copies its guaranteed String specifier directly into a stack-first
buffer instead of routing through generic Value conversion.

| Operation | Before median | After median | Allocation change | Current Rust ratio |
|---|---:|---:|---:|---:|
| script/compile_minimal | 1032 ns | 935.6 ns | 176 B/7 to 96 B/4 | 2.11x |
| script/compile_workload | 1424 ns | 1158 ns | 176 B/7 to 96 B/4 | 1.54x |
| script/compile_and_run_minimal | 2308 ns | 1863 ns | 288 B/11 to 160 B/7 | 2.95x |
| script/run_precompiled_workload | 7271 ns | 6503 ns | 4304 B/9 to 4256 B/8 | 1.59x |
| module/compile | 1659 ns | 1535 ns | 224 B/7 to 112 B/4 | 2.12x |
| module/compile_instantiate | 9569 ns | 7182 ns | 4808 B/23 to 312 B/11 | 1.45x |
| module/compile_instantiate_evaluate | 10685 ns | 8150 ns | 4872 B/26 to 320 B/12 | 1.65x |

The controlled 300 ms three-sample medians improve 7.5-24.9%. The previously
recorded large compile-and-run workload remains faster in Go.

## ABI-39 HandleScope and callback follow-up

Caching the existing HandleScope enter/exit exports and calling them with
fixed arity removes the two variadic wrapper allocations. Five paired 750 ms
samples changed the pure `NewScope`/`Close` median from 295.1 to 200.5 ns and
64 bytes/3 allocations to 48 bytes/1 allocation. Escape analysis confirms the
remaining `Scope` allocation is required by its public lifetime and the
isolate's nesting stack. The scope-local module-cache workload lost the same
two allocations and measured 963.4 ns, about 2.74x its Rust midpoint; a minimal
script compile improved from 1147 to 966.7 ns in that paired run.

Callback `IntegerValue` now validates same-isolate callback values with one
owner-thread query instead of two. Current-HEAD paired medians improved 0.5-3.8%
after subtracting control drift, with no allocation change. Registry caches
were tested and rejected because they added complexity without a separable
timing benefit; unique invocation storage remains necessary so stale borrowed
callback pointers can never become valid again.

## ABI-40 primitive-constructor and Promise confirmation

Eight primitive constructors now use cached fixed-arity exports. This removes
one variadic frame allocation per value while preserving the returned public
wrapper. Seven interleaved 500 ms pairs measured these representative changes:

| Operation | Before median | After median | Allocation change |
|---|---:|---:|---:|
| callback/native_call_from_host | 2406 ns | 2353 ns | 384 B/11 to 320 B/8 |
| synthetic module full route | 3448 ns | 3273 ns | 296 B/8 to 256 B/6 |
| bigint/from_i64 | 710.7 ns | 659.5 ns | 136 B/5 to 112 B/4 |

The medians improve 2.2%, 5.1%, and 7.2%, respectively. A steady-state
constructor regression also verifies that `Int32` itself performs no Go heap
allocation.

A separate Promise same-source shortcut initially appeared about 2% faster,
but a longer two-second confirmation contradicted the resolver result, so the
experiment was fully reverted. Current controls measured resolver creation at
about 1030-1128 ns and then/checkpoint at about 1909-2693 ns, with 112 B/3 and
176 B/5 allocations. Against the pinned Rust midpoints, the representative
ratios are about 8.43x and 4.74x; native transitions, affinity validation and
the required checkpoint remain the measured floor.

## ABI-40 remaining scalar-constructor and ReturnValue follow-up

The canonical empty-string constructor was the last value constructor still
using the generic closure/variadic path. Cached fixed-arity dispatch changed
seven 500 ms samples from a 155.8 ns median at 16 B/1 allocation to 89.2 ns at
zero allocations, a 42.8% improvement. The regression now requires both
`Int32` and `EmptyString` constructors to remain allocation-free in steady
state.

Arbitrary, float, bool, null, undefined and empty-string ReturnValue setters
now use cached fixed-arity calls; the already optimized integer setters are
unchanged. Seven alternating frozen-binary bool-callback pairs changed the
median from 1465 to 1461 ns while allocations fell from 280 B/8 to 256 B/6.
The 0.3% timing difference is neutral; the 24-byte/two-allocation reduction is
repeatable and applies directly at the callback boundary.

## ABI-41 callback lifecycle and Function construction

The isolate's closed state now has an atomic mirror, so callback-adjacent
validity checks avoid the lifecycle mutex while preserving owner-thread and
shutdown rules. The direct Function constructor consumes the callback entry
that was just registered and uses a fixed-arity export after validating the
scope. Eight frozen original/candidate pairs measured these medians:

| Workload | Original | ABI-41 | Allocation change | Ratio to Rust |
|---|---:|---:|---:|---:|
| callback/native_call_from_js | 2051.5 ns | 2008.5 ns | unchanged at 280 B/6 | about 13.0x |
| callback/native_call_from_host | 2006.5 ns | 1981 ns | unchanged at 320 B/8 | about 14.3x |
| callback/function_new_call | 3885 ns | 3696 ns | 584 B/11 to 512 B/9 | about 3.72x |

The changes improve the paired medians 2.1%, 1.3%, and 4.9%. An independent
atomic-only run improved the callback medians 5.5-6.8%; a direct-construction
control removed the same two allocations and improved Function create/call
2.1%. A fixed-arity Windows thread-ID experiment subsequently produced a 1.7%
regression in one order and a 1.7% gain in reverse order, so it was rejected.

An ABI-41 Promise reprofile retained no production changes. The canonical
loaded medians were 1441.5 ns at 112 B/3 for resolver creation and 2403 ns at
176 B/5 for then/checkpoint, about 11.53x and 5.85x the Rust midpoints. A lower
interleaved control measured about 7.73x and 4.51x, confirming that these small
native-transition workloads remain load-sensitive. Removing another lifecycle
check was neutral or slower and was reverted.

## ABI-41 lock-free callback registry follow-up

Callback entries are immutable after publication, and V8 invokes ordinary
host callbacks synchronously on the isolate's owner thread. A chunked atomic
directory now serves dispatch lookups without taking the global registration
mutex. Registration and lifecycle enumeration retain the locked map; removal
atomically unpublishes the fast slot before freeing its native context. Empty
explicit release cycles reuse the directory from handle one, avoiding
per-registration allocation or unbounded directory growth.

Six order-balanced 300,000-iteration frozen-binary pairs measured these
medians, with allocations unchanged throughout:

| Workload | Locked registry | Atomic registry | Change | B/op; allocs/op |
|---|---:|---:|---:|---:|
| callback/native_call_from_js | 2028 ns | 2019.5 ns | -0.4% | 280; 6 |
| callback/native_call_from_host | 2057 ns | 1948 ns | -5.3% | 320; 8 |
| callback/function_new_call | 3735 ns | 3588 ns | -3.9% | 512; 9 |

A separate six-pair two-second confirmation changed the host median from
2371.5 to 2232 ns (-5.9%) and Function create/call from 5047 to 4817 ns
(-4.6%). The independently run JS confirmation changed 2405 to 2360 ns
(-1.9%). Concurrent lookup/removal race coverage verifies that readers observe
only the exact immutable entry or its nil tombstone.

## ABI-41 Script Run output-slot follow-up

`Script.Run` now reuses the heap-resident script lifecycle word as its
synchronous native output slot. This removes the dedicated escaping output
allocation without growing the 32-byte `Script` wrapper. The shim writes the
slot only after JavaScript returns; nested same-script execution therefore
finishes and clears its result before the outer call writes its result.

Six order-balanced, same-test-tree one-second pairs measured these medians:

| Workload | Before | After | Allocation change |
|---|---:|---:|---:|
| script/compile_minimal | 905.7 ns | 885.9 ns | unchanged at 80 B/2 |
| script/compile_workload | 1191.5 ns | 1175 ns | unchanged at 80 B/2 |
| script/compile_and_run_minimal | 1809.5 ns | 1811 ns | 144 B/5 to 136 B/4 |
| script/compile_and_run_workload | 53146 ns | 53150 ns | 4272 B/7 to 4264 B/6 |

The compile-and-run timings are neutral while every Run path removes exactly
8 bytes and one allocation. A separate ten-pair two-second precompiled-run
confirmation improved the median from 5446 to 5373.5 ns (-1.3%), reducing
4240 B/6 to 4232 B/5 (about 1.31x the pinned Rust midpoint). Same-script
reentry and success/error/success TryCatch tests cover slot restoration.

## ABI-41 Function call same-scope follow-up

`Function.Call` now reuses the function's successful scope/isolate proof when
the call scope, receiver, and arguments carry the identical `Scope` pointer.
Different-scope calls retain the prior full checks and error ordering. Focused
coverage exercises zero, one, and multiple arguments, invalid values,
wrong-thread and closed/foreign lifetimes, thrown exceptions, and nested
JavaScript re-entry through a Go callback.

Six order-balanced 500 ms frozen-executable pairs produced these controls:

| Workload | Original samples (ns/op) | Same-scope samples (ns/op) | Median change | B/op; allocs/op |
|---|---:|---:|---:|---:|
| callback/native_call_from_js | 1810, 1776, 2104, 1798, 2224, 1789 | 2029, 1781, 1801, 1812, 1835, 1824 | 1804 to 1818 (+0.8%, neutral) | 272; 5 |
| callback/native_call_from_host | 1796, 1859, 2478, 1854, 1913, 1862 | 2088, 1725, 1794, 1787, 2007, 1811 | 1860.5 to 1802.5 (-3.1%) | 320; 8 |
| callback/function_new_call | 3904, 4037, 4680, 3935, 4026, 3735 | 4336, 3658, 3568, 3321, 3888, 3958 | 3980.5 to 3773 (-5.2%) | 512; 9 |

The JavaScript-origin control does not use the optimized outer
`Function.Call` and stayed neutral. The host and create/call medians improved
in four of six pairs, leaving approximately 13.0x and 3.80x versus their
pinned Rust midpoints. Allocations are unchanged; this pass removes redundant
thread-affinity work rather than weakening lifetime enforcement.

## ABI-41 callback argument-buffer follow-up

The ordinary function trampoline now keeps its two most common argument wires
in a fixed stack buffer and uses the existing heap allocation only above two
arguments. The callback frame layout and exports are unchanged. Boundary tests
cover 0, 1, 2, 3 and 64 arguments; repeated `Get` identity; both out-of-bounds
directions; constructor calls; and nested stack/heap-path re-entry.

The first candidate used eight inline slots. Six order-balanced 300,000-iteration
pairs changed the JS median from 1942.5 to 1896.5 ns, but regressed host calls
from 1926.5 to 1990 ns and Function create/call from 3592.5 to 3719 ns. In a
two-second confirmation, Function create/call lost five of six pairs. That
candidate was rejected; its 64-byte unconditional stack expansion was larger
than the matched two-argument workload required.

The accepted two-slot candidate was measured with byte-identical frozen test
executables, baseline DLL SHA-256
`0FBD464E2A21526067125465110DCB25C2BCBAD86C4B229809DE9E33A66CA6BF`, and
candidate DLL SHA-256
`4645ACCB14EDB83C5140B0EC439ED6A608823836BC61B7330B7D0F4AD13132AF`.
Six order-balanced fixed-300,000-iteration pairs produced:

| Workload | Baseline samples (ns/op) | Two-slot samples (ns/op) | Median change | B/op; allocs/op |
|---|---:|---:|---:|---:|
| callback/native_call_from_js | 2284, 2197, 2001, 1833, 2368, 1985 | 2085, 1954, 1741, 1748, 1852, 2018 | 2099 to 1903 (-9.3%) | 272; 5 |
| callback/native_call_from_host | 2235, 1941, 1849, 1893, 2064, 1914 | 1942, 1865, 1745, 1759, 2110, 2042 | 1927.5 to 1903.5 (-1.2%) | 320; 8 |
| callback/function_new_call | 3912, 3494, 3267, 3749, 3401, 3489 | 3664, 3433, 3164, 3167, 3792, 3420 | 3491.5 to 3426.5 (-1.9%) | 512; 9 |

Six additional order-balanced two-second pairs confirmed the direction:

| Workload | Baseline samples (ns/op) | Two-slot samples (ns/op) | Median change |
|---|---:|---:|---:|
| callback/native_call_from_js | 2340, 2307, 2622, 2255, 2184, 2372 | 2171, 2182, 2005, 1993, 2232, 2149 | 2323.5 to 2160 (-7.0%) |
| callback/native_call_from_host | 2712, 2499, 2692, 2925, 2511, 3182 | 2545, 2166, 2643, 2599, 2622, 2564 | 2702 to 2581.5 (-4.5%) |
| callback/function_new_call | 4735, 4538, 4758, 4494, 5133, 4839 | 4734, 4537, 4400, 4580, 4785, 4884 | 4746.5 to 4657 (-1.9%) |

Go allocation counts do not include the removed native `new[]`/`delete[]` and
therefore remain unchanged. The fixed-run candidate medians are approximately
12.3x, 13.7x and 3.45x the corresponding pinned Rust midpoints.

## ABI-41 Promise handler-validation consolidation

The Promise reaction shims already reject non-function handlers before their
checked cast. Successful `Then` and `Catch` calls now rely on that existing
check instead of making a separate native predicate call; successful `Then2`
calls remove both standalone predicates. Foreign values remain rejected from
Go before a native call, and `Then2` retains a first-handler predicate only on
the negative path needed to preserve mixed-invalid error ordering.

Eight order-balanced fixed-300,000-iteration frozen-executable pairs measured
the affected then/checkpoint route as follows. The resolver route is an
unaffected load control.

| Workload | Baseline samples (ns/op) | Consolidated samples (ns/op) | Median change | B/op; allocs/op |
|---|---:|---:|---:|---:|
| promise/resolver_new_resolve | 1172, 1193, 1916, 1101, 1114, 1110, 1085, 1091 | 1103, 1039, 1117, 1112, 1123, 1170, 1088, 1030 | 1112 to 1107.5 (-0.4%, control) | 112; 3 |
| promise/resolve_then_checkpoint | 1938, 2753, 2287, 1957, 1925, 2000, 2037, 2132 | 1863, 2028, 1897, 1960, 1910, 1926, 1998, 1917 | 2018.5 to 1921.5 (-4.8%; 7/8 pair wins) | 176; 5 |

Six balanced fixed-1,000,000-iteration confirmation pairs changed the
then/checkpoint median from 2324.5 to 2110.5 ns (-9.2%; 4/6 pair wins), while
the resolver control moved from 1205 to 1152.5 ns (-4.4%). A final
then-only fixed-2,000,000-iteration confirmation changed samples from 2632,
2286, 2322, 2253, 2376, 2396 ns to 2236, 2201, 2250, 2095, 2188, 2877 ns:
2349 to 2218.5 ns (-5.6%; 5/6 pair wins), with allocations unchanged. The
final median is 5.40x the pinned Rust confidence-interval midpoint of 410.7 ns;
the 1,000,000-iteration resolver control is 9.22x its 125.03 ns Rust midpoint.
