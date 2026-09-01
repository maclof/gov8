# Host callback/promise benchmark run: host-initial-2026-08-28

Audited and corrected methodology run for `benches/callback.rs` and
`benches/promise.rs`. Companion to `initial-2026-08-28-summary.md` (startup +
script slices, unchanged methodology).

Raw evidence in this directory:

- `criterion/` — raw Criterion tree (`estimates.json`, `sample.json`,
  `tukey.json`, per-benchmark `report/` plots, top-level `report/index.html`),
  saved under baseline name `host-initial-2026-08-28` (plus criterion's
  `new/` copy of the same run)
- `console-callback.log`, `console-promise.log` — full console output of both
  bench binaries, including the in-process environment banner
- `env-DESKTOP-VJI58KR.txt` — machine/toolchain metadata
  (`scripts/capture-env.ps1`)

## Audit findings corrected in this run

Every benchmark previously discarded results with `let _ = ...`, so a
silently failing path (script exception, callback never invoked, resolve
refused, reaction job never running) would have been timed as if it were the
workload. Six such paths were fixed; every iteration now asserts success and
the documented result, and the untimed workload is validated before
measurement. A failed assert panics and aborts the bench binary before any
baseline is saved. Workload shapes were also aligned to the host conformance
checks:

| Benchmark | Silent-failure path fixed | Workload alignment with conformance |
|---|---|---|
| `callback/native_call_from_js` | `script.run` result ignored (exception path would be timed); result value never checked | Asserts run success and result `342.0`; `Function::builder(add_cb).length(2)` (=`fn.length` 2 as in `arguments_and_return`) |
| `callback/native_call_from_rust` | `Function::call` result ignored | Asserts call success and result `42.0`; integer arguments `20`/`22` as in `arguments_and_return` host_call |
| `callback/function_new_call` | `Function::call` result ignored | Asserts call success and result `42.0`; creation via `builder(..).length(2)` |
| `promise/resolver_new_resolve` | `resolve` result ignored; `state()` read but never asserted | Asserts resolve `Some(true)`, state `Fulfilled`, result `42.0` — the `resolver_settlement_semantics` core |
| `promise/resolve_then_checkpoint` | `then` and `resolve` results ignored; nothing verified the reaction job ran | Asserts `then`/`resolve` success and derived promise `Fulfilled` after checkpoint (only possible if the handler ran) — the `native_then_checkpoint` shape, resolve value `Integer 42` |

Documented simplification (must be mirrored by the Go harness): the
`resolve_then_checkpoint` reaction handler is a no-op (conformance appends to
a global array); execution is proven by the derived-promise settlement. The
assertions are part of the measured workload in both languages.

## Command

```
cd rust-oracle
$env:CRITERION_HOME = "<repo>\rust-oracle\target\criterion-host-initial-2026-08-28"
cargo bench --bench callback -- --save-baseline host-initial-2026-08-28
cargo bench --bench promise  -- --save-baseline host-initial-2026-08-28
```

Checks before measurement: `cargo fmt --check`, `cargo check --benches`,
`cargo clippy --benches -- -D warnings`, `cargo bench --no-run` (all exit 0).

## Methodology (identical for every benchmark; unchanged)

- Harness: criterion 0.8.2, `harness = false` bench targets
- Build: `bench` profile (release, optimized); V8 engine is the prebuilt
  release static library (`15.2.124.1-rusty`)
- Warm-up: 1 s per benchmark; measurement: 3 s per benchmark; 50 samples
  (visible in console logs: `Warming up for 1.0000 s`, `Collecting 50
  samples in estimated 3.00xx s`)
- One operation per iteration; every iteration opens a fresh nested
  `HandleScope`; isolate/context created once per benchmark
- Per-iteration success assertions as tabulated above (new in this run;
  part of the measured workload)
- In-process banner: `os=windows arch=x86_64 logical_cpus=16
  build_profile=release v8_version_string=15.2.124.1-rusty`

## Environment (from `env-DESKTOP-VJI58KR.txt`, captured 2026-08-28T14:14:33Z)

- Microsoft Windows 11 Pro, build 26200, 64-bit
- AMD Ryzen 9 PRO 7940HS, 8 cores / 16 logical, max 4001 MHz
- RAM 62.65 GB total / 11.91 GB free at capture
- rustc/cargo 1.98.0 (88d9e12ae 2026-08-18), toolchain overridden by
  `rust-toolchain.toml`
- `v8 = "=152.2.0"`, `criterion = "=0.8.2"`, V8 artifact SHA-256 pinned in
  `.cargo/config.toml` (`0b17ca07…f62b2`)
- No V8 flags set (default runtime); plotters backend (no gnuplot)

## Results (point estimates from criterion `estimates.json`, baseline
`host-initial-2026-08-28`)

| Benchmark | mean | 95% CI (mean) | median | std dev | rel. SD |
|---|---|---|---|---|---|
| callback/native_call_from_js | 0.319 µs | [0.298, 0.340] | 0.334 µs | 0.076 µs | 23.7% |
| callback/native_call_from_rust | 0.291 µs | [0.276, 0.307] | 0.281 µs | 0.056 µs | 19.4% |
| callback/function_new_call | 2.282 µs | [2.153, 2.421] | 2.184 µs | 0.489 µs | 21.4% |
| promise/resolver_new_resolve | 0.352 µs | [0.332, 0.372] | 0.327 µs | 0.073 µs | 20.7% |
| promise/resolve_then_checkpoint | 0.927 µs | [0.894, 0.963] | 0.905 µs | 0.125 µs | 13.5% |

Total measured iterations per benchmark (from console logs): 8.6M
(`native_call_from_js`), 9.8M (`native_call_from_rust`), 1.3M
(`function_new_call`), 9.6M (`resolver_new_resolve`), 3.1M
(`resolve_then_checkpoint`).

Notes:

- Sub-µs iterations on Windows carry high relative variance (timer
  resolution, background work); compare medians and require fresh same-day,
  same-machine runs for Go-vs-Rust deltas. Do not compare against single
  runs on other machines.
- `native_call_from_rust` (0.28 µs median) vs `native_call_from_js`
  (0.33 µs): the JS path adds one script run for two callback invocations,
  so per-callback cost is comparable across the two entry directions.
- `function_new_call` (2.18 µs median) is ~7x a bare host call: creating a
  `Function` object with a native trampoline dominates; only the creation
  delta is attributable to `Function::new`.
- `resolve_then_checkpoint` (0.90 µs) vs `resolver_new_resolve`
  (0.33 µs): the `.then` reaction setup plus the microtask checkpoint cost
  roughly 0.6 µs on top of resolver creation + resolve.
- These numbers are the first assertion-verified baselines; they are not
  comparable to any earlier callback/promise numbers (none existed) — but
  future changes to assertions or workload must be treated as a
  methodology break requiring a fresh paired run on both sides.

## Reproduction requirements for comparisons

Same machine class, same pinned toolchain (`rust-toolchain.toml`), same
`v8` crate pin and artifact SHA-256 (`.cargo/config.toml`), same criterion
pin, same V8 flags (none; default runtime), same benchmark methodology
above — including the per-iteration assertions, which the Go harness must
mirror one-for-one — and a fresh env capture. Go-side harnesses must match
warm-up (1 s), measurement (3 s), and sample (50) policy and use the
identical workload sources and expected results (342, 42, 42, 42, Fulfilled).
