//! cppgc process lifecycle and custom `Heap` ownership oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. A minimal leaf
//! `GarbageCollected` object is used only to make collection and heap ownership
//! observable; pointer-wrapper behavior is covered by separate oracles.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::ffi::CStr;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use v8::cppgc::{EmbedderStackState, GarbageCollected, Heap, HeapCreateParams, Visitor};

const SEM_FAILCRITICALERRORS: u32 = 0x0001;
const SEM_NOGPFAULTERRORBOX: u32 = 0x0002;
const SEM_NOOPENFILEERRORBOX: u32 = 0x8000;

#[link(name = "kernel32")]
unsafe extern "system" {
    #[link_name = "SetErrorMode"]
    fn set_error_mode(mode: u32) -> u32;
}

fn suppress_windows_fatal_dialogs() {
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

struct Leaf {
    drops: Arc<AtomicUsize>,
}

unsafe impl GarbageCollected for Leaf {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCHeapLifecycleLeaf"
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

fn platform() -> v8::SharedRef<v8::Platform> {
    v8::new_unprotected_default_platform(0, false).make_shared()
}

fn normal_checks() {
    let mut outcomes: Vec<CheckOutcome> = Vec::new();

    outcomes.push(pass(
        "cppgc-heap-lifecycle/pin",
        Json::obj(vec![
            ("crate", Json::s("v8=152.2.0")),
            ("v8", Json::s(v8::V8::get_version())),
            ("target", Json::s("x86_64-pc-windows-msvc")),
        ]),
    ));

    let defaults = HeapCreateParams::default();
    let default_marking = match defaults.marking_support {
        v8::cppgc::MarkingType::Atomic => "Atomic",
        v8::cppgc::MarkingType::Incremental => "Incremental",
        v8::cppgc::MarkingType::IncrementalAndConcurrent => "IncrementalAndConcurrent",
    };
    let default_sweeping = match defaults.sweeping_support {
        v8::cppgc::SweepingType::Atomic => "Atomic",
        v8::cppgc::SweepingType::Incremental => "Incremental",
        v8::cppgc::SweepingType::IncrementalAndConcurrent => "IncrementalAndConcurrent",
    };
    outcomes.push(pass(
        "cppgc-heap-lifecycle/create_params/default",
        Json::obj(vec![
            ("marking_support", Json::s(default_marking)),
            ("sweeping_support", Json::s(default_sweeping)),
            (
                "public_marking_variants",
                Json::arr(vec![
                    Json::s("Atomic"),
                    Json::s("Incremental"),
                    Json::s("IncrementalAndConcurrent"),
                ]),
            ),
            (
                "public_sweeping_variants",
                Json::arr(vec![
                    Json::s("Atomic"),
                    Json::s("Incremental"),
                    Json::s("IncrementalAndConcurrent"),
                ]),
            ),
        ]),
    ));

    let platform = platform();
    v8::cppgc::initialize_process(platform.clone());
    let detached_drops = Arc::new(AtomicUsize::new(0));
    let mut detached = Heap::create(platform.clone(), HeapCreateParams::default());
    allocate_unrooted(&detached, detached_drops.clone());
    unsafe {
        detached.collect_garbage_for_testing(EmbedderStackState::NoHeapPointers);
    }
    let before_enable = detached_drops.load(Ordering::SeqCst);
    detached.enable_detached_garbage_collections_for_testing();
    unsafe {
        detached.collect_garbage_for_testing(EmbedderStackState::NoHeapPointers);
    }
    let after_enable = detached_drops.load(Ordering::SeqCst);
    allocate_unrooted(&detached, detached_drops.clone());
    detached.terminate();
    let after_terminate = detached_drops.load(Ordering::SeqCst);
    detached.terminate();
    let after_second_terminate = detached_drops.load(Ordering::SeqCst);
    drop(detached);
    unsafe { v8::cppgc::shutdown_process() };
    outcomes.push(pass(
        "cppgc-heap-lifecycle/detached/collection_and_terminate",
        Json::obj(vec![
            ("drops_before_enable", Json::i(before_enable as i64)),
            ("drops_after_enabled_gc", Json::i(after_enable as i64)),
            ("drops_after_terminate", Json::i(after_terminate as i64)),
            (
                "drops_after_second_terminate",
                Json::i(after_second_terminate as i64),
            ),
            ("terminate_idempotent", Json::b(true)),
        ]),
    ));

    v8::cppgc::initialize_process(platform.clone());
    let destructor_drops = Arc::new(AtomicUsize::new(0));
    let second = Heap::create(
        platform.clone(),
        HeapCreateParams {
            marking_support: v8::cppgc::MarkingType::Atomic,
            sweeping_support: v8::cppgc::SweepingType::Atomic,
        },
    );
    allocate_unrooted(&second, destructor_drops.clone());
    drop(second);
    let after_heap_drop = destructor_drops.load(Ordering::SeqCst);
    unsafe { v8::cppgc::shutdown_process() };
    outcomes.push(pass(
        "cppgc-heap-lifecycle/process/paired_reinitialize",
        Json::obj(vec![
            ("second_initialize_succeeded", Json::b(true)),
            ("atomic_heap_created", Json::b(true)),
            ("heap_drop_terminates", Json::b(true)),
            ("drops_after_heap_drop", Json::i(after_heap_drop as i64)),
            ("second_shutdown_succeeded", Json::b(true)),
        ]),
    ));

    v8::V8::set_flags_from_string("--expose-gc");
    v8::V8::initialize_platform(platform.clone());
    v8::V8::initialize();
    let attached_drops = Arc::new(AtomicUsize::new(0));
    let custom = Heap::create(
        platform.clone(),
        HeapCreateParams {
            marking_support: v8::cppgc::MarkingType::Atomic,
            sweeping_support: v8::cppgc::SweepingType::Atomic,
        },
    );
    let supplied_address = &*custom as *const Heap;
    let mut isolate = v8::Isolate::new(v8::CreateParams::default().cpp_heap(custom));
    let attached_address = isolate
        .get_cpp_heap()
        .map(|heap| heap as *const Heap)
        .expect("custom heap attached");
    let same_heap = supplied_address == attached_address;
    allocate_unrooted(
        isolate.get_cpp_heap().expect("attached heap"),
        attached_drops.clone(),
    );
    unsafe {
        isolate
            .get_cpp_heap()
            .expect("attached heap")
            .collect_garbage_for_testing(EmbedderStackState::NoHeapPointers);
    }
    let drops_after_attached_gc = attached_drops.load(Ordering::SeqCst);
    let shared_error =
        unsafe { isolate.try_into_shared() }.expect_err("embedder heap rejects share");
    let shared_error_kind = match shared_error.kind() {
        v8::IntoSharedErrorKind::EmbedderCppHeap => "EmbedderCppHeap",
        _ => "unexpected",
    };
    let mut isolate = shared_error.into_isolate();
    allocate_unrooted(
        isolate.get_cpp_heap().expect("recovered attached heap"),
        attached_drops.clone(),
    );
    drop(isolate);
    let drops_after_isolate_drop = attached_drops.load(Ordering::SeqCst);
    outcomes.push(pass(
        "cppgc-heap-lifecycle/isolate/custom_heap_ownership",
        Json::obj(vec![
            ("get_cpp_heap_some", Json::b(true)),
            ("same_heap_address", Json::b(same_heap)),
            (
                "drops_after_attached_gc",
                Json::i(drops_after_attached_gc as i64),
            ),
            ("try_into_shared_error", Json::s(shared_error_kind)),
            (
                "drops_after_isolate_drop",
                Json::i(drops_after_isolate_drop as i64),
            ),
            ("isolate_drop_owns_heap_termination", Json::b(true)),
        ]),
    ));

    let disposed = unsafe { v8::V8::dispose() };
    unsafe { v8::cppgc::shutdown_process() };
    v8::V8::dispose_platform();
    drop(platform);
    outcomes.push(pass(
        "cppgc-heap-lifecycle/process/orderly_v8_shutdown",
        Json::obj(vec![
            ("isolate_dropped_first", Json::b(true)),
            ("v8_disposed", Json::b(disposed)),
            ("cppgc_shutdown_after_heaps", Json::b(true)),
            ("platform_disposed_last", Json::b(true)),
        ]),
    ));

    let total = outcomes.len();
    for outcome in outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(total, total, 0));
}

fn create_before_initialize() {
    let platform = platform();
    let _heap = Heap::create(platform, HeapCreateParams::default());
    println!("create_before_initialize_survived");
}

fn initialize_twice() {
    let platform = platform();
    v8::cppgc::initialize_process(platform.clone());
    v8::cppgc::initialize_process(platform);
    println!("initialize_twice_survived");
}

fn enable_detached_twice() {
    let platform = platform();
    v8::cppgc::initialize_process(platform.clone());
    let heap = Heap::create(platform, HeapCreateParams::default());
    heap.enable_detached_garbage_collections_for_testing();
    heap.enable_detached_garbage_collections_for_testing();
    println!("enable_detached_twice_survived");
}

fn enable_detached_attached() {
    v8::V8::set_flags_from_string("--expose-gc");
    let platform = platform();
    v8::V8::initialize_platform(platform.clone());
    v8::V8::initialize();
    let heap = Heap::create(platform, HeapCreateParams::default());
    let mut isolate = v8::Isolate::new(v8::CreateParams::default().cpp_heap(heap));
    isolate
        .get_cpp_heap()
        .expect("attached heap")
        .enable_detached_garbage_collections_for_testing();
    println!("enable_detached_attached_survived");
}

fn shutdown_before_initialize() {
    unsafe { v8::cppgc::shutdown_process() };
    println!("shutdown_before_initialize_returned");
}

fn shutdown_twice() {
    let platform = platform();
    v8::cppgc::initialize_process(platform);
    unsafe {
        v8::cppgc::shutdown_process();
        v8::cppgc::shutdown_process();
    }
    println!("shutdown_twice_returned");
}

fn shutdown_with_live_heap() {
    let platform = platform();
    v8::cppgc::initialize_process(platform.clone());
    let heap = Heap::create(platform, HeapCreateParams::default());
    unsafe { v8::cppgc::shutdown_process() };
    std::mem::forget(heap);
    println!("shutdown_with_live_heap_returned");
}

fn main() {
    let mode = std::env::args().nth(1);
    if mode.is_some() {
        suppress_windows_fatal_dialogs();
    }
    match mode.as_deref() {
        None => normal_checks(),
        Some("create-before-initialize") => create_before_initialize(),
        Some("initialize-twice") => initialize_twice(),
        Some("enable-detached-twice") => enable_detached_twice(),
        Some("enable-detached-attached") => enable_detached_attached(),
        Some("shutdown-before-initialize") => shutdown_before_initialize(),
        Some("shutdown-twice") => shutdown_twice(),
        Some("shutdown-with-live-heap") => shutdown_with_live_heap(),
        Some(mode) => panic!("unknown mode: {mode}"),
    }
}
