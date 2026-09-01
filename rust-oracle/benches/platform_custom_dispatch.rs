//! Custom PlatformImpl no-op task dispatch benchmark.
//!
//! The benchmark-only C++ producer allocates one no-op `v8::Task` per
//! iteration and calls rusty_v8's exact CustomPlatform PostTask callback.
//! `ImmediateImpl` consumes it synchronously with `Task::run`, which invokes
//! the virtual Run method and deletes the task. Context construction and all
//! created/dispatched/run/destroyed counter validation are outside timing.
//! The one-probe/10,000-operation explicit warm-up and reset boundaries match
//! the Go benchmark exactly.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};
use std::ffi::c_void;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Instant;

const EXPLICIT_WARM_UP_ITERATIONS: u64 = 10_000;

unsafe extern "C" {
    fn gov8_oracle_platform_bench_post_noop_task(
        context: *mut c_void,
        isolate: *mut c_void,
    ) -> bool;
    fn gov8_oracle_platform_bench_reset_noop_task_counts();
    fn gov8_oracle_platform_bench_noop_task_counts(
        created: *mut u64,
        run: *mut u64,
        destroyed: *mut u64,
    );
}

#[derive(Default)]
struct Counters {
    dispatched: AtomicU64,
}

struct ImmediateImpl {
    counters: Arc<Counters>,
    isolate_identity: usize,
}

impl v8::PlatformImpl for ImmediateImpl {
    fn post_task(&self, isolate_ptr: *mut c_void, task: v8::Task) {
        assert_eq!(isolate_ptr as usize, self.isolate_identity);
        self.counters.dispatched.fetch_add(1, Ordering::SeqCst);
        task.run();
    }
}

fn native_counts() -> (u64, u64, u64) {
    let mut created = 0;
    let mut run = 0;
    let mut destroyed = 0;
    unsafe {
        gov8_oracle_platform_bench_noop_task_counts(&mut created, &mut run, &mut destroyed);
    }
    (created, run, destroyed)
}

fn noop_task_dispatch(c: &mut Criterion) {
    common::banner();
    // Link the package's native bridge archive without initializing V8; the
    // synthetic task dispatch itself does not require process-global setup.
    let _native_bridge_link = oracle::report::summary_line(0, 0, 0);
    let counters = Arc::new(Counters::default());
    let mut isolate_token = 0_u8;
    let isolate_identity = std::ptr::addr_of_mut!(isolate_token).cast::<c_void>();
    let implementation: Box<dyn v8::PlatformImpl> = Box::new(ImmediateImpl {
        counters: counters.clone(),
        isolate_identity: isolate_identity as usize,
    });
    // This is the same double-boxed context shape owned by
    // `Platform::new_custom` in rusty_v8 152.2.0.
    let context = Box::into_raw(Box::new(implementation)).cast::<c_void>();

    unsafe { gov8_oracle_platform_bench_reset_noop_task_counts() };
    assert!(unsafe { gov8_oracle_platform_bench_post_noop_task(context, isolate_identity) });
    assert_eq!(counters.dispatched.load(Ordering::SeqCst), 1);
    assert_eq!(native_counts(), (1, 1, 1));
    unsafe { gov8_oracle_platform_bench_reset_noop_task_counts() };
    counters.dispatched.store(0, Ordering::SeqCst);

    for _ in 0..EXPLICIT_WARM_UP_ITERATIONS {
        assert!(unsafe { gov8_oracle_platform_bench_post_noop_task(context, isolate_identity) });
    }
    assert_eq!(
        counters.dispatched.load(Ordering::SeqCst),
        EXPLICIT_WARM_UP_ITERATIONS
    );
    assert_eq!(
        native_counts(),
        (
            EXPLICIT_WARM_UP_ITERATIONS,
            EXPLICIT_WARM_UP_ITERATIONS,
            EXPLICIT_WARM_UP_ITERATIONS
        )
    );
    unsafe { gov8_oracle_platform_bench_reset_noop_task_counts() };
    counters.dispatched.store(0, Ordering::SeqCst);

    c.bench_function("platform_custom/noop_task_post_dispatch_run_delete", |b| {
        b.iter_custom(|iterations| {
            let before_dispatched = counters.dispatched.load(Ordering::SeqCst);
            let before_native = native_counts();
            let start = Instant::now();
            for _ in 0..iterations {
                assert!(unsafe {
                    gov8_oracle_platform_bench_post_noop_task(context, isolate_identity)
                });
            }
            let elapsed = start.elapsed();
            let after_native = native_counts();
            assert_eq!(
                counters.dispatched.load(Ordering::SeqCst) - before_dispatched,
                iterations
            );
            assert_eq!(after_native.0 - before_native.0, iterations);
            assert_eq!(after_native.1 - before_native.1, iterations);
            assert_eq!(after_native.2 - before_native.2, iterations);
            elapsed
        });
    });

    let implementation = unsafe { Box::from_raw(context.cast::<Box<dyn v8::PlatformImpl>>()) };
    drop(implementation);
    assert_eq!(Arc::strong_count(&counters), 1);
}

criterion_group! {
    name = platform_custom_dispatch_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = noop_task_dispatch
}

criterion_main!(platform_custom_dispatch_benches);
