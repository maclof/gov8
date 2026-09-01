//! Snapshot-backed `CreateParams` composed with embedder-owned resources.
//!
//! This complements `conformance-create-params-snapshot`: that oracle proves
//! snapshot composition with V8's default shared ArrayBuffer allocator, while
//! this one covers a callback-backed allocator and an embedder cppgc heap.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::alloc::{alloc, alloc_zeroed, dealloc, Layout};
use std::ffi::{c_void, CStr};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex, MutexGuard};
use v8::cppgc::{EmbedderStackState, GarbageCollected, Heap, HeapCreateParams, Visitor};

#[derive(Clone, Default)]
struct AllocatorEvents {
    initialized: Vec<usize>,
    uninitialized: Vec<usize>,
    frees: Vec<usize>,
}

#[derive(Default)]
struct AllocatorState {
    events: Mutex<AllocatorEvents>,
    drops: AtomicUsize,
}

impl AllocatorState {
    fn lock(&self) -> MutexGuard<'_, AllocatorEvents> {
        self.events
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    fn snapshot(&self) -> AllocatorEvents {
        self.lock().clone()
    }
}

fn allocation_layout(length: usize) -> Option<Layout> {
    Layout::from_size_align(length, 16).ok()
}

unsafe extern "C" fn allocate_initialized(state: &AllocatorState, length: usize) -> *mut c_void {
    state.lock().initialized.push(length);
    let Some(layout) = allocation_layout(length) else {
        return std::ptr::null_mut();
    };
    unsafe { alloc_zeroed(layout).cast() }
}

unsafe extern "C" fn allocate_uninitialized(state: &AllocatorState, length: usize) -> *mut c_void {
    state.lock().uninitialized.push(length);
    let Some(layout) = allocation_layout(length) else {
        return std::ptr::null_mut();
    };
    unsafe { alloc(layout).cast() }
}

unsafe extern "C" fn free_allocation(state: &AllocatorState, data: *mut c_void, length: usize) {
    state.lock().frees.push(length);
    if let Some(layout) = allocation_layout(length) {
        if !data.is_null() {
            unsafe { dealloc(data.cast(), layout) };
        }
    }
}

unsafe extern "C" fn drop_allocator(state: *const AllocatorState) {
    let state = unsafe { Arc::from_raw(state) };
    state.drops.fetch_add(1, Ordering::SeqCst);
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

struct Leaf {
    drops: Arc<AtomicUsize>,
}

unsafe impl GarbageCollected for Leaf {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"SnapshotResourceCompositionLeaf"
    }
}

impl Drop for Leaf {
    fn drop(&mut self) {
        self.drops.fetch_add(1, Ordering::SeqCst);
    }
}

#[inline(never)]
fn allocate_unrooted(heap: &Heap, drops: Arc<AtomicUsize>) {
    let pointer = unsafe { v8::cppgc::make_garbage_collected(heap, Leaf { drops }) };
    std::hint::black_box(pointer);
}

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).expect("source string");
    v8::Script::compile(scope, source, None)
        .expect("compile")
        .run(scope)
        .expect("run")
}

fn snapshot(marker: i32) -> v8::StartupData {
    let mut creator = v8::Isolate::snapshot_creator(None, None);
    {
        v8::scope!(let scope, &mut creator);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval(scope, &format!("globalThis.snapshotMarker = {marker};"));
        scope.set_default_context(context);
    }
    creator
        .create_blob(v8::FunctionCodeHandling::Keep)
        .expect("snapshot blob")
}

fn marker(scope: &v8::PinScope<'_, '_>) -> i64 {
    eval(scope, "snapshotMarker")
        .integer_value(scope)
        .expect("integer marker")
}

fn lengths(values: &[usize]) -> Json {
    Json::arr(values.iter().map(|value| Json::i(*value as i64)).collect())
}

fn allocator_composition() -> CheckOutcome {
    let state = Arc::new(AllocatorState::default());
    let allocator = custom_allocator(&state);
    // The new fixture deliberately uses allocator-then-snapshot. The existing
    // CreateParams snapshot fixture covers snapshot-then-allocator.
    let params = v8::CreateParams::default()
        .array_buffer_allocator(allocator.clone())
        .snapshot_blob(snapshot(41));
    drop(allocator);

    let mut isolate = v8::Isolate::new(params);
    let (observed_marker, store) = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let observed_marker = marker(scope);
        let buffer = v8::ArrayBuffer::new(scope, 9);
        let store = buffer.get_backing_store();
        store[0].set(73);
        (observed_marker, store)
    };
    let before_isolate_drop = state.snapshot();
    let drops_before_isolate_drop = state.drops.load(Ordering::SeqCst);
    drop(isolate);
    let after_isolate_drop = state.snapshot();
    let drops_after_isolate_drop = state.drops.load(Ordering::SeqCst);
    let byte_after_isolate_drop = store[0].get();
    drop(store);
    let after_store_drop = state.snapshot();
    let drops_after_store_drop = state.drops.load(Ordering::SeqCst);

    pass(
        "snapshot-resource-composition/custom_allocator",
        Json::obj(vec![
            ("builder_order", Json::s("allocator_then_snapshot")),
            ("marker", Json::i(observed_marker)),
            (
                "initialized_before_isolate_drop",
                lengths(&before_isolate_drop.initialized),
            ),
            (
                "uninitialized_before_isolate_drop",
                lengths(&before_isolate_drop.uninitialized),
            ),
            (
                "frees_before_isolate_drop",
                lengths(&before_isolate_drop.frees),
            ),
            (
                "allocator_drops_before_isolate_drop",
                Json::i(drops_before_isolate_drop as i64),
            ),
            (
                "frees_after_isolate_drop",
                lengths(&after_isolate_drop.frees),
            ),
            (
                "allocator_drops_after_isolate_drop",
                Json::i(drops_after_isolate_drop as i64),
            ),
            (
                "store_byte_after_isolate_drop",
                Json::i(i64::from(byte_after_isolate_drop)),
            ),
            ("frees_after_store_drop", lengths(&after_store_drop.frees)),
            (
                "allocator_drops_after_store_drop",
                Json::i(drops_after_store_drop as i64),
            ),
        ]),
    )
}

fn cppgc_heap_composition(platform: v8::SharedRef<v8::Platform>) -> CheckOutcome {
    let drops = Arc::new(AtomicUsize::new(0));
    let heap = Heap::create(
        platform,
        HeapCreateParams {
            marking_support: v8::cppgc::MarkingType::Atomic,
            sweeping_support: v8::cppgc::SweepingType::Atomic,
        },
    );
    let supplied_address = &*heap as *const Heap;
    // This is the reverse of the allocator case and proves that neither
    // builder overwrites the other independent CreateParams field.
    let params = v8::CreateParams::default()
        .snapshot_blob(snapshot(42))
        .cpp_heap(heap);
    let mut isolate = v8::Isolate::new(params);
    let attached_address = isolate
        .get_cpp_heap()
        .map(|heap| heap as *const Heap)
        .expect("attached cppgc heap");
    let observed_marker = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        marker(scope)
    };
    allocate_unrooted(
        isolate.get_cpp_heap().expect("attached cppgc heap"),
        Arc::clone(&drops),
    );
    unsafe {
        isolate
            .get_cpp_heap()
            .expect("attached cppgc heap")
            .collect_garbage_for_testing(EmbedderStackState::NoHeapPointers);
    }
    let drops_after_collection = drops.load(Ordering::SeqCst);
    allocate_unrooted(
        isolate.get_cpp_heap().expect("attached cppgc heap"),
        Arc::clone(&drops),
    );
    let share_error =
        unsafe { isolate.try_into_shared() }.expect_err("embedder cppgc heap must reject sharing");
    let share_error_kind = match share_error.kind() {
        v8::IntoSharedErrorKind::EmbedderCppHeap => "EmbedderCppHeap",
        _ => "unexpected",
    };
    let isolate = share_error.into_isolate();
    drop(isolate);
    let drops_after_isolate_drop = drops.load(Ordering::SeqCst);

    pass(
        "snapshot-resource-composition/custom_cppgc_heap",
        Json::obj(vec![
            ("builder_order", Json::s("snapshot_then_cpp_heap")),
            ("marker", Json::i(observed_marker)),
            (
                "same_heap_address",
                Json::b(supplied_address == attached_address),
            ),
            (
                "drops_after_forced_collection",
                Json::i(drops_after_collection as i64),
            ),
            ("try_into_shared_error", Json::s(share_error_kind)),
            (
                "drops_after_isolate_drop",
                Json::i(drops_after_isolate_drop as i64),
            ),
            ("isolate_drop_owns_heap", Json::b(true)),
        ]),
    )
}

fn main() -> std::process::ExitCode {
    v8::V8::set_flags_from_string("--expose-gc");
    let platform = v8::new_unprotected_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform.clone());
    v8::V8::initialize();

    let checks = vec![
        allocator_composition(),
        cppgc_heap_composition(platform.clone()),
    ];
    let passed = checks.iter().filter(|check| check.passed()).count();
    for check in &checks {
        println!("{}", check.to_line());
    }
    println!(
        "{}",
        summary_line(checks.len(), passed, checks.len() - passed)
    );

    let disposed = unsafe { v8::V8::dispose() };
    unsafe { v8::cppgc::shutdown_process() };
    v8::V8::dispose_platform();
    drop(platform);
    if passed == checks.len() && disposed {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
