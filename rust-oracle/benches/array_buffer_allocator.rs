//! ArrayBuffer allocator callback benchmark.
//!
//! Matches Go's `BenchmarkArrayBufferAllocatorBackingStore`: allocator and
//! isolate setup are outside measurement. Each timed iteration allocates one
//! 64-byte standalone BackingStore through the custom allocator and drops it,
//! invoking exactly one allocation callback and one free callback. Counter
//! validation is performed after the explicitly timed batch.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};
use std::alloc::{alloc, alloc_zeroed, dealloc, Layout};
use std::ffi::c_void;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Instant;

const BACKING_STORE_SIZE: usize = 64;

#[derive(Default)]
struct AllocatorState {
    allocations: AtomicU64,
    frees: AtomicU64,
}

fn layout(length: usize) -> Option<Layout> {
    Layout::from_size_align(length, 16).ok()
}

unsafe extern "C" fn allocate_initialized(state: &AllocatorState, length: usize) -> *mut c_void {
    state.allocations.fetch_add(1, Ordering::SeqCst);
    let Some(layout) = layout(length) else {
        return std::ptr::null_mut();
    };
    unsafe { alloc_zeroed(layout).cast() }
}

unsafe extern "C" fn allocate_uninitialized(state: &AllocatorState, length: usize) -> *mut c_void {
    state.allocations.fetch_add(1, Ordering::SeqCst);
    let Some(layout) = layout(length) else {
        return std::ptr::null_mut();
    };
    unsafe { alloc(layout).cast() }
}

unsafe extern "C" fn free_allocation(state: &AllocatorState, data: *mut c_void, length: usize) {
    state.frees.fetch_add(1, Ordering::SeqCst);
    if !data.is_null() && length != 0 {
        // Go's allocator bridge observes the first byte when invoking Free.
        std::hint::black_box(unsafe { *data.cast::<u8>() });
    }
    if let Some(layout) = layout(length) {
        if !data.is_null() {
            unsafe { dealloc(data.cast(), layout) };
        }
    }
}

unsafe extern "C" fn drop_allocator(state: *const AllocatorState) {
    drop(unsafe { Arc::from_raw(state) });
}

static ALLOCATOR_VTABLE: v8::RustAllocatorVtable<AllocatorState> = v8::RustAllocatorVtable {
    allocate: allocate_initialized,
    allocate_uninitialized,
    free: free_allocation,
    drop: drop_allocator,
};

fn custom_allocator(state: &Arc<AllocatorState>) -> v8::SharedRef<v8::Allocator> {
    unsafe {
        v8::new_rust_allocator(Arc::into_raw(Arc::clone(state)), &ALLOCATOR_VTABLE).make_shared()
    }
}

fn backing_store_64_create_free(c: &mut Criterion) {
    common::banner();
    oracle::ensure_v8();
    let state = Arc::new(AllocatorState::default());
    let allocator = custom_allocator(&state);
    let mut isolate =
        v8::Isolate::new(v8::CreateParams::default().array_buffer_allocator(allocator.clone()));

    // Untimed correctness probe with the exact measured operation.
    let before_allocations = state.allocations.load(Ordering::SeqCst);
    let before_frees = state.frees.load(Ordering::SeqCst);
    let store = v8::ArrayBuffer::new_backing_store(&mut isolate, BACKING_STORE_SIZE);
    assert_eq!(store.len(), BACKING_STORE_SIZE);
    drop(store);
    assert_eq!(
        state.allocations.load(Ordering::SeqCst),
        before_allocations + 1
    );
    assert_eq!(state.frees.load(Ordering::SeqCst), before_frees + 1);

    c.bench_function("array_buffer_allocator/backing_store_64_create_free", |b| {
        b.iter_custom(|iterations| {
            let before_allocations = state.allocations.load(Ordering::SeqCst);
            let before_frees = state.frees.load(Ordering::SeqCst);
            let start = Instant::now();
            for _ in 0..iterations {
                let store = v8::ArrayBuffer::new_backing_store(&mut isolate, BACKING_STORE_SIZE);
                drop(store);
            }
            let elapsed = start.elapsed();
            assert_eq!(
                state.allocations.load(Ordering::SeqCst) - before_allocations,
                iterations,
                "each BackingStore must invoke one allocation callback"
            );
            assert_eq!(
                state.frees.load(Ordering::SeqCst) - before_frees,
                iterations,
                "each BackingStore drop must invoke one free callback"
            );
            elapsed
        });
    });

    drop(isolate);
    drop(allocator);
    assert_eq!(Arc::strong_count(&state), 1);
}

criterion_group! {
    name = array_buffer_allocator_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = backing_store_64_create_free
}

criterion_main!(array_buffer_allocator_benches);
