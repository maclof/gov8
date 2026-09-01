//! cppgc `Persistent` and `WeakPersistent` ownership oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. All handles are created,
//! assigned, queried, and destroyed on the isolate thread, as required by the
//! public API contract.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::ffi::CStr;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use v8::cppgc::{GarbageCollected, Persistent, Visitor, WeakPersistent};

const WRAP_TAG: u16 = 7;

struct Managed {
    id: i32,
    drops: Arc<AtomicUsize>,
    drop_order: Arc<Mutex<Vec<i32>>>,
}

unsafe impl GarbageCollected for Managed {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCPersistentOracle"
    }
}

impl Drop for Managed {
    fn drop(&mut self) {
        self.drops.fetch_add(1, Ordering::SeqCst);
        self.drop_order.lock().unwrap().push(self.id);
    }
}

fn initialize() -> v8::SharedRef<v8::Platform> {
    v8::V8::set_flags_from_string("--expose-gc");
    let platform = v8::new_unprotected_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform.clone());
    v8::V8::initialize();
    platform
}

fn empty_callback(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<v8::Value>,
) {
}

fn api_wrapper<'s>(scope: &mut v8::PinScope<'s, '_>) -> v8::Local<'s, v8::Object> {
    let template = v8::FunctionTemplate::new(scope, empty_callback);
    let function = template.get_function(scope).unwrap();
    function.new_instance(scope, &[]).unwrap()
}

fn key<'s>(scope: &v8::PinScope<'s, '_>, text: &str) -> v8::Local<'s, v8::String> {
    v8::String::new(scope, text).unwrap()
}

fn allocate<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    id: i32,
    drops: Arc<AtomicUsize>,
    drop_order: Arc<Mutex<Vec<i32>>>,
) -> v8::cppgc::UnsafePtr<Managed> {
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    unsafe {
        v8::cppgc::make_garbage_collected(
            heap,
            Managed {
                id,
                drops,
                drop_order,
            },
        )
    }
}

fn create_wrapped_pair(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    id: i32,
    drops: Arc<AtomicUsize>,
    drop_order: Arc<Mutex<Vec<i32>>>,
) -> (Persistent<Managed>, WeakPersistent<Managed>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let pointer = allocate(scope, id, drops, drop_order);
    let wrapper = api_wrapper(scope);
    unsafe { v8::Object::wrap::<WRAP_TAG, Managed>(scope, wrapper, &pointer) };
    context
        .global(scope)
        .set(scope, key(scope, "wrapper").into(), wrapper.into())
        .unwrap();
    (Persistent::new(&pointer), WeakPersistent::new(&pointer))
}

fn create_weak_only(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    id: i32,
    drops: Arc<AtomicUsize>,
    drop_order: Arc<Mutex<Vec<i32>>>,
) -> WeakPersistent<Managed> {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let pointer = allocate(scope, id, drops, drop_order);
    WeakPersistent::new(&pointer)
}

fn assign_strong(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    target: &mut Persistent<Managed>,
    id: i32,
    drops: Arc<AtomicUsize>,
    drop_order: Arc<Mutex<Vec<i32>>>,
) -> WeakPersistent<Managed> {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let pointer = allocate(scope, id, drops, drop_order);
    target.set(&pointer);
    WeakPersistent::new(&pointer)
}

fn assign_weak_with_strong(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    target: &mut WeakPersistent<Managed>,
    id: i32,
    drops: Arc<AtomicUsize>,
    drop_order: Arc<Mutex<Vec<i32>>>,
) -> Persistent<Managed> {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let pointer = allocate(scope, id, drops, drop_order);
    target.set(&pointer);
    Persistent::new(&pointer)
}

fn remove_wrapper(isolate: &mut v8::OwnedIsolate, context: &v8::Global<v8::Context>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    context
        .global(scope)
        .set(
            scope,
            key(scope, "wrapper").into(),
            v8::undefined(scope).into(),
        )
        .unwrap();
}

fn full_gc(isolate: &mut v8::OwnedIsolate, context: &v8::Global<v8::Context>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    // Every `UnsafePtr` is confined to an allocation helper whose stack frame
    // has returned before this call. Persistent handles are registered off-heap
    // roots, so the active Rust stack contains no raw cppgc heap pointers.
    unsafe {
        heap.collect_garbage_for_testing(v8::cppgc::EmbedderStackState::NoHeapPointers);
    }
}

fn drop_count(counter: &Arc<AtomicUsize>) -> Json {
    Json::i(counter.load(Ordering::SeqCst) as i64)
}

fn run() {
    let platform = initialize();
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    let context_global = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        v8::Global::new(scope, context)
    };
    let order = Arc::new(Mutex::new(Vec::new()));
    let root_drops = Arc::new(AtomicUsize::new(0));
    let weak_drops = Arc::new(AtomicUsize::new(0));
    let first_drops = Arc::new(AtomicUsize::new(0));
    let second_drops = Arc::new(AtomicUsize::new(0));
    let reused_drops = Arc::new(AtomicUsize::new(0));
    let teardown_drops = Arc::new(AtomicUsize::new(0));
    let mut outcomes: Vec<CheckOutcome> = Vec::new();

    let empty_strong = Persistent::<Managed>::empty();
    let empty_weak = WeakPersistent::<Managed>::empty();
    let (root, root_weak) = create_wrapped_pair(
        &mut isolate,
        &context_global,
        10,
        root_drops.clone(),
        order.clone(),
    );
    let root_first = root.get().unwrap();
    let root_second = root.get().unwrap();
    outcomes.push(pass(
        "cppgc-persistent/handles/empty_new_get_identity",
        Json::obj(vec![
            (
                "empty_persistent_none",
                Json::b(empty_strong.get().is_none()),
            ),
            ("empty_weak_none", Json::b(empty_weak.get().is_none())),
            ("new_id", Json::i(i64::from(root_first.id))),
            (
                "repeated_get_identity",
                Json::b(std::ptr::eq(root_first, root_second)),
            ),
            (
                "strong_weak_identity",
                Json::b(std::ptr::eq(root_first, root_weak.get().unwrap())),
            ),
        ]),
    ));

    remove_wrapper(&mut isolate, &context_global);
    full_gc(&mut isolate, &context_global);
    outcomes.push(pass(
        "cppgc-persistent/strong/root_after_wrapper_removal",
        Json::obj(vec![
            (
                "strong_id_after_full_gc",
                Json::i(i64::from(root.get().unwrap().id)),
            ),
            ("weak_still_present", Json::b(root_weak.get().is_some())),
            ("drops_while_strong", drop_count(&root_drops)),
        ]),
    ));

    let weak_only = create_weak_only(
        &mut isolate,
        &context_global,
        20,
        weak_drops.clone(),
        order.clone(),
    );
    full_gc(&mut isolate, &context_global);
    let weak_cleared_once = weak_only.get().is_none();
    full_gc(&mut isolate, &context_global);
    outcomes.push(pass(
        "cppgc-persistent/weak/clearing_and_destruction",
        Json::obj(vec![
            ("cleared_after_full_gc", Json::b(weak_cleared_once)),
            (
                "still_clear_after_second_gc",
                Json::b(weak_only.get().is_none()),
            ),
            ("drops_after_two_gcs", drop_count(&weak_drops)),
        ]),
    ));

    let mut reusable_strong = Persistent::<Managed>::empty();
    let weak_first = assign_strong(
        &mut isolate,
        &context_global,
        &mut reusable_strong,
        30,
        first_drops.clone(),
        order.clone(),
    );
    let first_id_before_reassign = reusable_strong.get().unwrap().id;
    let weak_second = assign_strong(
        &mut isolate,
        &context_global,
        &mut reusable_strong,
        31,
        second_drops.clone(),
        order.clone(),
    );
    full_gc(&mut isolate, &context_global);
    let first_cleared = weak_first.get().is_none();
    let second_alive = reusable_strong.get().map(|value| value.id);

    let mut reusable_weak = WeakPersistent::<Managed>::empty();
    reusable_weak.set(&reusable_strong);
    let weak_set_identity =
        std::ptr::eq(reusable_weak.get().unwrap(), reusable_strong.get().unwrap());
    let empty_source = Persistent::<Managed>::empty();
    reusable_strong.set(&empty_source);
    let strong_none_after_empty_set = reusable_strong.get().is_none();
    drop(empty_source);
    let second_not_destroyed_synchronously = second_drops.load(Ordering::SeqCst) == 0;
    full_gc(&mut isolate, &context_global);
    let second_cleared = weak_second.get().is_none() && reusable_weak.get().is_none();

    let reused_observer = assign_strong(
        &mut isolate,
        &context_global,
        &mut reusable_strong,
        32,
        reused_drops.clone(),
        order.clone(),
    );
    reusable_weak.set(&reusable_strong);
    let persistent_reused_id = reusable_strong.get().map(|value| value.id);
    let weak_reused_id = reusable_weak.get().map(|value| value.id);
    let reused_identity =
        std::ptr::eq(reusable_strong.get().unwrap(), reusable_weak.get().unwrap());
    drop(reusable_strong);
    full_gc(&mut isolate, &context_global);
    let reused_weaks_cleared = reusable_weak.get().is_none() && reused_observer.get().is_none();
    outcomes.push(pass(
        "cppgc-persistent/reassign/release_and_reuse",
        Json::obj(vec![
            (
                "first_id_before_reassign",
                Json::i(i64::from(first_id_before_reassign)),
            ),
            ("first_weak_cleared", Json::b(first_cleared)),
            ("first_drops", drop_count(&first_drops)),
            (
                "second_id_after_reassign",
                Json::i(i64::from(second_alive.unwrap_or(-1))),
            ),
            ("weak_set_identity", Json::b(weak_set_identity)),
            (
                "strong_none_after_empty_set",
                Json::b(strong_none_after_empty_set),
            ),
            (
                "release_not_synchronous",
                Json::b(second_not_destroyed_synchronously),
            ),
            ("second_weaks_cleared", Json::b(second_cleared)),
            ("second_drops", drop_count(&second_drops)),
            (
                "persistent_reused_id",
                Json::i(i64::from(persistent_reused_id.unwrap_or(-1))),
            ),
            (
                "reused_weak_id",
                Json::i(i64::from(weak_reused_id.unwrap_or(-1))),
            ),
            ("persistent_weak_reused_identity", Json::b(reused_identity)),
            ("reused_weaks_cleared", Json::b(reused_weaks_cleared)),
            ("reused_drops", drop_count(&reused_drops)),
        ]),
    ));

    drop(root_weak);
    full_gc(&mut isolate, &context_global);
    let alive_after_weak_handle_drop = root_drops.load(Ordering::SeqCst) == 0;
    drop(root);
    let not_destroyed_on_strong_handle_drop = root_drops.load(Ordering::SeqCst) == 0;
    full_gc(&mut isolate, &context_global);
    let after_release_gc = root_drops.load(Ordering::SeqCst);
    full_gc(&mut isolate, &context_global);

    drop(empty_strong);
    drop(empty_weak);
    drop(weak_only);
    drop(weak_first);
    drop(weak_second);
    drop(reused_observer);
    drop(reusable_weak);

    let mut teardown_weak = WeakPersistent::<Managed>::empty();
    let teardown_strong = assign_weak_with_strong(
        &mut isolate,
        &context_global,
        &mut teardown_weak,
        90,
        teardown_drops.clone(),
        order.clone(),
    );
    drop(context_global);
    drop(isolate);
    let teardown_drops_after_isolate = teardown_drops.load(Ordering::SeqCst);
    let teardown_weak_cleared = teardown_weak.get().is_none();
    drop(teardown_weak);
    drop(teardown_strong);
    let teardown_drops_after_handle_drops = teardown_drops.load(Ordering::SeqCst);
    let v8_dispose = unsafe { v8::V8::dispose() };
    unsafe { v8::cppgc::shutdown_process() };
    v8::V8::dispose_platform();
    drop(platform);

    let observed_order = order.lock().unwrap().clone();
    outcomes.push(pass(
        "cppgc-persistent/lifecycle/drop_order_exactly_once",
        Json::obj(vec![
            (
                "alive_after_weak_handle_drop",
                Json::b(alive_after_weak_handle_drop),
            ),
            (
                "strong_drop_not_synchronous",
                Json::b(not_destroyed_on_strong_handle_drop),
            ),
            ("drops_after_release_gc", Json::i(after_release_gc as i64)),
            ("drops_after_repeated_gc", drop_count(&root_drops)),
            (
                "target_drops_during_isolate_teardown",
                Json::i(teardown_drops_after_isolate as i64),
            ),
            (
                "weak_cleared_by_isolate_teardown",
                Json::b(teardown_weak_cleared),
            ),
            (
                "handle_drops_after_isolate_do_not_redestroy",
                Json::b(teardown_drops_after_handle_drops == 1),
            ),
            (
                "all_object_drop_order",
                Json::arr(
                    observed_order
                        .into_iter()
                        .map(|id| Json::i(i64::from(id)))
                        .collect(),
                ),
            ),
            ("v8_dispose", Json::b(v8_dispose)),
            ("cppgc_shutdown", Json::b(true)),
        ]),
    ));

    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(outcomes.len(), outcomes.len(), 0));
}

fn main() {
    run();
}
