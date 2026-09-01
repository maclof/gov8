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
