//! Native promise API benchmarks.
//! See benches/common/mod.rs for the methodology.
//!
//! Comparable workloads for the Go port (shapes mirror the `promise/`
//! conformance checks in `src/checks/host/promises.rs`):
//! - `promise/resolver_new_resolve`: one `PromiseResolver` per iteration,
//!   created natively, resolved with the number 42. Mirrors the core of
//!   `resolver_settlement_semantics` (`resolve_ok == Some(true)`, state
//!   `Fulfilled`, result "42"); every iteration asserts all three.
//! - `promise/resolve_then_checkpoint`: the full native promise round-trip —
//!   resolver creation, `.then` with a native handler, resolution with the
//!   integer 42 (the `native_then_checkpoint` shape), and the microtask
//!   checkpoint that runs the reaction job — under the Explicit microtasks
//!   policy. Mirrors `native_then_checkpoint`; every iteration asserts the
//!   derived promise settled to `Fulfilled` after the checkpoint, which is
//!   only possible if the reaction job actually executed the native handler.
//!
//! Intentional simplification (documented for the Go harness): the reaction
//! handler is a no-op, unlike the conformance handler that appends to a
//! global array; execution is still proven by the derived-promise
//! settlement, which cannot reach `Fulfilled` unless the handler ran. The
//! per-iteration `state()` read is part of the measured workload.
//!
//! Failure policy: the workload is validated untimed before measurement and
//! every timed iteration re-asserts success (`Some(..)` results, expected
//! settlement). A failed assert panics and aborts the benchmark binary
//! before any baseline is saved, so a silently failing path (resolve/then
//! throwing, reaction job not running) can never be timed. The Go-side
//! harness must mirror these assertions one-for-one.
//!
//! The isolate and context are created once per benchmark; each iteration
//! opens a fresh nested `HandleScope`.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};

fn promise_handler_cb(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
}

/// The exact per-iteration timed workload of `resolver_new_resolve`,
/// asserted. Used both untimed (validation) and inside the timed loop.
fn run_asserted_resolver_roundtrip(scope: &mut v8::PinScope<'_, '_>) {
    let resolver = v8::PromiseResolver::new(scope).unwrap();
    let promise = resolver.get_promise(scope);
    // `unwrap_or_else` turns a throwing resolve (None) into a panic; the
    // assert rejects a resolve that reported no settlement change (Some(false)).
    let resolved = resolver
        .resolve(scope, v8::Number::new(scope, 42.0).into())
        .unwrap_or_else(|| panic!("promise/resolver_new_resolve: resolve threw"));
    assert!(
        resolved,
        "promise/resolver_new_resolve: resolve did not report success"
    );
    assert_eq!(
        promise.state(),
        v8::PromiseState::Fulfilled,
        "promise/resolver_new_resolve: promise did not settle to Fulfilled"
    );
    let result = promise
        .result(scope)
        .number_value(scope)
        .unwrap_or_else(|| {
            panic!("promise/resolver_new_resolve: settled result is not the number 42")
        });
    assert_eq!(
        result, 42.0,
        "promise/resolver_new_resolve: unexpected settled result"
    );
}

/// The exact per-iteration timed workload of `resolve_then_checkpoint`,
/// asserted.
fn run_asserted_then_checkpoint(
    scope: &mut v8::PinScope<'_, '_>,
    handler_global: &v8::Global<v8::Function>,
) {
    let resolver = v8::PromiseResolver::new(scope).unwrap();
    let promise = resolver.get_promise(scope);
    let handler = v8::Local::new(scope, handler_global);
    let derived = promise
        .then(scope, handler)
        .unwrap_or_else(|| panic!("promise/resolve_then_checkpoint: then threw"));
    let resolved = resolver
        .resolve(scope, v8::Integer::new(scope, 42).into())
        .unwrap_or_else(|| panic!("promise/resolve_then_checkpoint: resolve threw"));
    assert!(
        resolved,
        "promise/resolve_then_checkpoint: resolve did not report success"
    );
    scope.perform_microtask_checkpoint();
    // The derived promise settles only if the checkpoint actually ran the
    // reaction job; a Pending state here means the microtask never executed.
    assert_eq!(
        derived.state(),
        v8::PromiseState::Fulfilled,
        "promise/resolve_then_checkpoint: derived promise did not settle \
         (reaction job did not run)"
    );
}

fn resolver_new_resolve(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Untimed validation before measurement.
    run_asserted_resolver_roundtrip(scope);

    c.bench_function("promise/resolver_new_resolve", |b| {
        b.iter(|| {
            v8::scope!(let inner, scope);
            run_asserted_resolver_roundtrip(inner);
        })
    });
}

fn resolve_then_checkpoint(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let handler_global = {
        let handler = v8::Function::new(scope, promise_handler_cb).unwrap();
        v8::Global::new(scope, handler)
    };

    // Untimed validation before measurement.
    run_asserted_then_checkpoint(scope, &handler_global);

    c.bench_function("promise/resolve_then_checkpoint", |b| {
        b.iter(|| {
            v8::scope!(let inner, scope);
            run_asserted_then_checkpoint(inner, &handler_global);
        })
    });
}

criterion_group! {
    name = promise_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = resolver_new_resolve, resolve_then_checkpoint
}

criterion_main!(promise_benches);
