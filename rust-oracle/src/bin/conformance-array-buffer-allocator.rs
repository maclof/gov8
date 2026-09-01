//! ArrayBuffer allocator ownership conformance for pinned `v8` =152.2.0.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::alloc::{alloc, alloc_zeroed, dealloc, Layout};
use std::cell::Cell;
use std::collections::HashSet;
use std::ffi::c_void;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Barrier, Mutex, MutexGuard};
use std::thread::ThreadId;

#[cfg(windows)]
const SEM_FAILCRITICALERRORS: u32 = 0x0001;
#[cfg(windows)]
const SEM_NOGPFAULTERRORBOX: u32 = 0x0002;
#[cfg(windows)]
const SEM_NOOPENFILEERRORBOX: u32 = 0x8000;

#[cfg(windows)]
unsafe extern "system" {
    #[link_name = "SetErrorMode"]
    fn set_error_mode(mode: u32) -> u32;
}

#[cfg(windows)]
fn suppress_windows_fatal_dialogs() {
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

#[derive(Clone, Default)]
struct Events {
    initialized: Vec<usize>,
    uninitialized: Vec<usize>,
    frees: Vec<usize>,
    freed_first_bytes: Vec<u8>,
    callback_threads: HashSet<ThreadId>,
}

#[derive(Default)]
struct AllocatorState {
    events: Mutex<Events>,
    refuse: AtomicBool,
    drops: AtomicUsize,
}

impl AllocatorState {
    fn lock(&self) -> MutexGuard<'_, Events> {
        self.events
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    fn snapshot(&self) -> Events {
        self.lock().clone()
    }
}

fn layout(length: usize) -> Option<Layout> {
    Layout::from_size_align(length, 16).ok()
}

unsafe extern "C" fn allocate_initialized(state: &AllocatorState, length: usize) -> *mut c_void {
    {
        let mut events = state.lock();
        events.initialized.push(length);
        events.callback_threads.insert(std::thread::current().id());
    }
    if state.refuse.load(Ordering::SeqCst) {
        return std::ptr::null_mut();
    }
    let Some(layout) = layout(length) else {
        return std::ptr::null_mut();
    };
    unsafe { alloc_zeroed(layout).cast() }
}

unsafe extern "C" fn allocate_uninitialized(state: &AllocatorState, length: usize) -> *mut c_void {
    {
        let mut events = state.lock();
        events.uninitialized.push(length);
        events.callback_threads.insert(std::thread::current().id());
    }
    if state.refuse.load(Ordering::SeqCst) {
        return std::ptr::null_mut();
    }
    let Some(layout) = layout(length) else {
        return std::ptr::null_mut();
    };
    let memory = unsafe { alloc(layout) };
    if !memory.is_null() {
        unsafe { memory.write_bytes(0xa5, length) };
    }
    memory.cast()
}

unsafe extern "C" fn free_allocation(state: &AllocatorState, data: *mut c_void, length: usize) {
    let first = if data.is_null() || length == 0 {
        0
    } else {
        unsafe { *data.cast::<u8>() }
    };
    {
        let mut events = state.lock();
        events.frees.push(length);
        events.freed_first_bytes.push(first);
        events.callback_threads.insert(std::thread::current().id());
    }
    if let Some(layout) = layout(length) {
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

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

fn bytes(store: &v8::SharedRef<v8::BackingStore>) -> Vec<u8> {
    store.iter().map(Cell::get).collect()
}

fn lengths(values: &[usize]) -> Json {
    Json::arr(values.iter().map(|value| Json::i(*value as i64)).collect())
}

fn octets(values: &[u8]) -> Json {
    Json::arr(
        values
            .iter()
            .map(|value| Json::i(i64::from(*value)))
            .collect(),
    )
}

fn pin_and_default_factory() -> Vec<CheckOutcome> {
    let implicit = v8::CreateParams::default();
    let implicit_has_set = implicit.has_set_array_buffer_allocator();
    let explicit = v8::CreateParams::default()
        .array_buffer_allocator(v8::new_default_allocator().make_shared());
    let explicit_has_set = explicit.has_set_array_buffer_allocator();
    let mut isolate = v8::Isolate::new(explicit);
    let (length, contents, zero_data_none) = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let buffer = v8::ArrayBuffer::new(scope, 4);
        let store = buffer.get_backing_store();
        let zero = v8::ArrayBuffer::new(scope, 0);
        (buffer.byte_length(), bytes(&store), zero.data().is_none())
    };
    vec![pass(
        "array-buffer-allocator/pin_and_default_factory",
        Json::obj(vec![
            ("rust_crate", Json::s("v8=152.2.0")),
            ("v8", Json::s(v8::V8::get_version())),
            ("implicit_has_set", Json::b(implicit_has_set)),
            ("explicit_has_set", Json::b(explicit_has_set)),
            ("length", Json::i(length as i64)),
            ("zero_initialized", Json::b(contents == [0, 0, 0, 0])),
            ("zero_length_data_none", Json::b(zero_data_none)),
        ]),
    )]
}

fn callbacks_zero_and_transfer() -> Vec<CheckOutcome> {
    let state = Arc::new(AllocatorState::default());
    let allocator = custom_allocator(&state);
    let mut isolate =
        v8::Isolate::new(v8::CreateParams::default().array_buffer_allocator(allocator.clone()));
    let (
        zero_data_none,
        before_transfer,
        after_transfer,
        source_detached,
        transferred_length,
        transferred_bytes,
    ) = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let source = v8::ArrayBuffer::new(scope, 4);
        let source_store = source.get_backing_store();
        for (cell, value) in source_store.iter().zip([1, 2, 3, 4]) {
            cell.set(value);
        }
        drop(source_store);
        let key = v8::String::new(scope, "source").unwrap().into();
        context
            .global(scope)
            .set(scope, key, source.into())
            .unwrap();
        let zero = v8::ArrayBuffer::new(scope, 0);
        let before_transfer = state.snapshot();
        let transferred = eval(
            scope,
            "globalThis.transferred=source.transfer(6); transferred",
        )
        .unwrap()
        .cast::<v8::ArrayBuffer>();
        let transferred_store = transferred.get_backing_store();
        let after_transfer = state.snapshot();
        (
            zero.data().is_none(),
            before_transfer,
            after_transfer,
            source.was_detached(),
            transferred.byte_length(),
            bytes(&transferred_store),
        )
    };
    drop(isolate);
    let after_isolate = state.snapshot();
    drop(allocator);
    let drops = state.drops.load(Ordering::SeqCst);
    vec![pass(
        "array-buffer-allocator/callbacks_zero_and_transfer",
        Json::obj(vec![
            ("before_initialized", lengths(&before_transfer.initialized)),
            (
                "before_uninitialized",
                lengths(&before_transfer.uninitialized),
            ),
            ("zero_length_bypassed", Json::b(zero_data_none)),
            (
                "after_transfer_initialized",
                lengths(&after_transfer.initialized),
            ),
            (
                "after_transfer_uninitialized",
                lengths(&after_transfer.uninitialized),
            ),
            ("after_transfer_frees", lengths(&after_transfer.frees)),
            ("source_detached", Json::b(source_detached)),
            ("transferred_length", Json::i(transferred_length as i64)),
            (
                "transferred_bytes",
                Json::s(
                    &transferred_bytes
                        .iter()
                        .map(|value| format!("{value:02x}"))
                        .collect::<String>(),
                ),
            ),
            ("after_isolate_frees", lengths(&after_isolate.frees)),
            (
                "freed_first_bytes",
                octets(&after_isolate.freed_first_bytes),
            ),
            ("allocator_drops", Json::i(drops as i64)),
        ]),
    )]
}

fn standalone_backing_store_free() -> Vec<CheckOutcome> {
    let state = Arc::new(AllocatorState::default());
    let allocator = custom_allocator(&state);
    let mut isolate =
        v8::Isolate::new(v8::CreateParams::default().array_buffer_allocator(allocator.clone()));
    let store = v8::ArrayBuffer::new_backing_store(&mut isolate, 5);
    let initialized = state.snapshot();
    let contents = store.iter().map(Cell::get).collect::<Vec<_>>();
    drop(store);
    let after_store = state.snapshot();
    drop(isolate);
    drop(allocator);
    vec![pass(
        "array-buffer-allocator/standalone_backing_store_free",
        Json::obj(vec![
            ("initialized", lengths(&initialized.initialized)),
            ("uninitialized", lengths(&initialized.uninitialized)),
            ("contents_zero", Json::b(contents == [0, 0, 0, 0, 0])),
            ("frees_after_drop", lengths(&after_store.frees)),
            (
                "allocator_drops",
                Json::i(state.drops.load(Ordering::SeqCst) as i64),
            ),
        ]),
    )]
}

fn isolate_teardown_owns_allocator() -> Vec<CheckOutcome> {
    let state = Arc::new(AllocatorState::default());
    let allocator = custom_allocator(&state);
    let mut isolate =
        v8::Isolate::new(v8::CreateParams::default().array_buffer_allocator(allocator.clone()));
    drop(allocator);
    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let buffer = v8::ArrayBuffer::new(scope, 9);
        let key = v8::String::new(scope, "kept").unwrap().into();
        context
            .global(scope)
            .set(scope, key, buffer.into())
            .unwrap();
    }
    let before_teardown = state.snapshot();
    let drops_before_teardown = state.drops.load(Ordering::SeqCst);
    drop(isolate);
    let after_teardown = state.snapshot();
    vec![pass(
        "array-buffer-allocator/isolate_teardown_owns_allocator",
        Json::obj(vec![
            ("initialized", lengths(&before_teardown.initialized)),
            ("frees_before_teardown", lengths(&before_teardown.frees)),
            (
                "drops_before_teardown",
                Json::i(drops_before_teardown as i64),
            ),
            ("frees_after_teardown", lengths(&after_teardown.frees)),
            (
                "drops_after_teardown",
                Json::i(state.drops.load(Ordering::SeqCst) as i64),
            ),
        ]),
    )]
}

fn backing_store_outlives_isolate() -> Vec<CheckOutcome> {
    let state = Arc::new(AllocatorState::default());
    let allocator = custom_allocator(&state);
    let mut isolate =
        v8::Isolate::new(v8::CreateParams::default().array_buffer_allocator(allocator.clone()));
    drop(allocator);
    let store = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let buffer = v8::ArrayBuffer::new(scope, 7);
        let store = buffer.get_backing_store();
        store[0].set(77);
        store
    };
    drop(isolate);
    let after_isolate = state.snapshot();
    let drops_after_isolate = state.drops.load(Ordering::SeqCst);
    let store_still_readable = store[0].get();
    drop(store);
    let after_store = state.snapshot();
    vec![pass(
        "array-buffer-allocator/backing_store_outlives_isolate",
        Json::obj(vec![
            ("frees_after_isolate", lengths(&after_isolate.frees)),
            ("drops_after_isolate", Json::i(drops_after_isolate as i64)),
            (
                "store_readable_after_isolate",
                Json::i(i64::from(store_still_readable)),
            ),
            ("frees_after_store", lengths(&after_store.frees)),
            ("freed_first_bytes", octets(&after_store.freed_first_bytes)),
            (
                "drops_after_store",
                Json::i(state.drops.load(Ordering::SeqCst) as i64),
            ),
        ]),
    )]
}

fn shared_allocator_across_isolate_threads() -> Vec<CheckOutcome> {
    let state = Arc::new(AllocatorState::default());
    let allocator = custom_allocator(&state);
    let barrier = Arc::new(Barrier::new(3));
    let handles = [11_usize, 13_usize].map(|length| {
        let allocator = allocator.clone();
        let barrier = Arc::clone(&barrier);
        std::thread::spawn(move || {
            let mut isolate =
                v8::Isolate::new(v8::CreateParams::default().array_buffer_allocator(allocator));
            barrier.wait();
            {
                v8::scope!(let scope, &mut isolate);
                let context = v8::Context::new(scope, Default::default());
                let scope = &mut v8::ContextScope::new(scope, context);
                let buffer = v8::ArrayBuffer::new(scope, length);
                let key = v8::String::new(scope, "threadBuffer").unwrap().into();
                context
                    .global(scope)
                    .set(scope, key, buffer.into())
                    .unwrap();
            }
            drop(isolate);
        })
    });
    barrier.wait();
    for handle in handles {
        handle.join().unwrap();
    }
    let mut events = state.snapshot();
    events.initialized.sort_unstable();
    events.uninitialized.sort_unstable();
    events.frees.sort_unstable();
    let distinct_callback_threads = events.callback_threads.len();
    let drops_before_owner = state.drops.load(Ordering::SeqCst);
    drop(allocator);
    vec![pass(
        "array-buffer-allocator/shared_allocator_across_isolate_threads",
        Json::obj(vec![
            ("initialized_sorted", lengths(&events.initialized)),
            ("uninitialized_sorted", lengths(&events.uninitialized)),
            ("frees_sorted", lengths(&events.frees)),
            (
                "distinct_callback_threads",
                Json::i(distinct_callback_threads as i64),
            ),
            ("drops_before_owner", Json::i(drops_before_owner as i64)),
            (
                "drops_after_owner",
                Json::i(state.drops.load(Ordering::SeqCst) as i64),
            ),
        ]),
    )]
}

fn run_refused_allocation() {
    oracle::ensure_v8();
    let state = Arc::new(AllocatorState::default());
    state.refuse.store(true, Ordering::SeqCst);
    let allocator = custom_allocator(&state);
    let mut isolate =
        v8::Isolate::new(v8::CreateParams::default().array_buffer_allocator(allocator));
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _ = v8::ArrayBuffer::new(scope, 8);
}

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    pin_and_default_factory,
    callbacks_zero_and_transfer,
    standalone_backing_store_free,
    isolate_teardown_owns_allocator,
    backing_store_outlives_isolate,
    shared_allocator_across_isolate_threads,
];

fn main() -> std::process::ExitCode {
    let args: Vec<_> = std::env::args().collect();
    if args.iter().any(|arg| arg == "--refused-allocation") {
        #[cfg(windows)]
        suppress_windows_fatal_dialogs();
        run_refused_allocation();
        return std::process::ExitCode::SUCCESS;
    }
    oracle::ensure_v8();
    let outcomes: Vec<_> = CHECKS.iter().flat_map(|check| check()).collect();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let failed = outcomes.len() - passed;
    let mut text = String::new();
    for outcome in &outcomes {
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    text.push_str(&summary_line(outcomes.len(), passed, failed));
    text.push('\n');
    print!("{text}");
    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
