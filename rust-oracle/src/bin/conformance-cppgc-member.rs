//! cppgc `Member` and `WeakMember` graph-edge oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. `Persistent` and
//! `WeakPersistent` are used only as off-heap roots/observers so that the
//! behavior under test remains the on-heap Member/WeakMember edge.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::ffi::CStr;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use v8::cppgc::{
    GarbageCollected, GcCell, Member, Persistent, Traced, Visitor, WeakMember, WeakPersistent,
};

struct Edges {
    strong: Member<Node>,
    weak: WeakMember<Node>,
}

impl Traced for Edges {
    fn trace(&self, visitor: &mut Visitor) {
        visitor.trace(&self.strong);
        visitor.trace(&self.weak);
    }
}

struct Node {
    id: i32,
    edges: GcCell<Edges>,
    drops: Arc<AtomicUsize>,
    drop_ids: Arc<Mutex<Vec<i32>>>,
}

#[derive(Clone)]
struct DropTracker {
    drops: Arc<AtomicUsize>,
    drop_ids: Arc<Mutex<Vec<i32>>>,
}

unsafe impl GarbageCollected for Node {
    fn trace(&self, visitor: &mut Visitor) {
        visitor.trace(&self.edges);
    }

    fn get_name(&self) -> &'static CStr {
        c"CppGCMemberOracleNode"
    }
}

impl Drop for Node {
    fn drop(&mut self) {
        self.drops.fetch_add(1, Ordering::SeqCst);
        self.drop_ids.lock().unwrap().push(self.id);
    }
}

fn initialize() -> v8::SharedRef<v8::Platform> {
    v8::V8::set_flags_from_string("--expose-gc");
    let platform = v8::new_unprotected_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform.clone());
    v8::V8::initialize();
    platform
}

fn allocate<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    id: i32,
    drops: Arc<AtomicUsize>,
    drop_ids: Arc<Mutex<Vec<i32>>>,
) -> v8::cppgc::UnsafePtr<Node> {
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    unsafe {
        v8::cppgc::make_garbage_collected(
            heap,
            Node {
                id,
                edges: GcCell::new(Edges {
                    strong: Member::empty(),
                    weak: WeakMember::empty(),
                }),
                drops,
                drop_ids,
            },
        )
    }
}

#[inline(never)]
fn create_root(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    id: i32,
    drops: Arc<AtomicUsize>,
    drop_ids: Arc<Mutex<Vec<i32>>>,
) -> (Persistent<Node>, WeakPersistent<Node>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let node = allocate(scope, id, drops, drop_ids);
    (Persistent::new(&node), WeakPersistent::new(&node))
}

#[inline(never)]
fn create_initialized_pair(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    owner_id: i32,
    child_id: i32,
    drops: Arc<AtomicUsize>,
    drop_ids: Arc<Mutex<Vec<i32>>>,
) -> (Persistent<Node>, WeakPersistent<Node>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let child = allocate(scope, child_id, drops.clone(), drop_ids.clone());
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    let owner = unsafe {
        v8::cppgc::make_garbage_collected(
            heap,
            Node {
                id: owner_id,
                edges: GcCell::new(Edges {
                    strong: Member::new(&child),
                    weak: WeakMember::new(&child),
                }),
                drops,
                drop_ids,
            },
        )
    };
    (Persistent::new(&owner), WeakPersistent::new(&child))
}

#[inline(never)]
fn assign_new_child(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    owner: &Persistent<Node>,
    child_id: i32,
    set_strong: bool,
    set_weak: bool,
    tracker: DropTracker,
) -> WeakPersistent<Node> {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let child = allocate(scope, child_id, tracker.drops, tracker.drop_ids);
    let edges = owner.get().unwrap().edges.get_mut(scope);
    if set_strong {
        edges.strong.set(&child);
    }
    if set_weak {
        edges.weak.set(&child);
    }
    WeakPersistent::new(&child)
}

fn edge_ids(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    owner: &Persistent<Node>,
) -> (Option<i32>, Option<i32>, bool) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let edges = owner.get().unwrap().edges.get(scope);
    let strong = unsafe { edges.strong.get() };
    let weak = unsafe { edges.weak.get() };
    (
        strong.map(|node| node.id),
        weak.map(|node| node.id),
        strong.zip(weak).is_some_and(|(a, b)| std::ptr::eq(a, b)),
    )
}

fn clear_strong(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    owner: &Persistent<Node>,
) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let empty = Member::<Node>::empty();
    owner.get().unwrap().edges.get_mut(scope).strong.set(&empty);
}

fn full_gc(isolate: &mut v8::OwnedIsolate, context: &v8::Global<v8::Context>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    unsafe {
        heap.collect_garbage_for_testing(v8::cppgc::EmbedderStackState::NoHeapPointers);
    }
}

#[inline(never)]
fn create_cycle(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    drops: Arc<AtomicUsize>,
    drop_ids: Arc<Mutex<Vec<i32>>>,
) -> (WeakPersistent<Node>, WeakPersistent<Node>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let first = allocate(scope, 30, drops.clone(), drop_ids.clone());
    let second = allocate(scope, 31, drops, drop_ids);
    unsafe { first.as_ref() }
        .edges
        .get_mut(scope)
        .strong
        .set(&second);
    unsafe { second.as_ref() }
        .edges
        .get_mut(scope)
        .strong
        .set(&first);
    (WeakPersistent::new(&first), WeakPersistent::new(&second))
}

fn sorted_drop_ids(drop_ids: &Arc<Mutex<Vec<i32>>>) -> Vec<i32> {
    let mut ids = drop_ids.lock().unwrap().clone();
    ids.sort_unstable();
    ids
}

fn run() {
    let platform = initialize();
    let drops = Arc::new(AtomicUsize::new(0));
    let drop_ids = Arc::new(Mutex::new(Vec::new()));
    let tracker = DropTracker {
        drops: drops.clone(),
        drop_ids: drop_ids.clone(),
    };
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    let context = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        v8::Global::new(scope, context)
    };
    let mut checks: Vec<CheckOutcome> = Vec::new();

    let (root, root_observer) =
        create_root(&mut isolate, &context, 1, drops.clone(), drop_ids.clone());
    let empty = edge_ids(&mut isolate, &context, &root);
    let child_two = assign_new_child(
        &mut isolate,
        &context,
        &root,
        2,
        true,
        true,
        tracker.clone(),
    );
    let assigned = edge_ids(&mut isolate, &context, &root);
    let (initialized_root, initialized_child) = create_initialized_pair(
        &mut isolate,
        &context,
        10,
        11,
        drops.clone(),
        drop_ids.clone(),
    );
    let initialized = edge_ids(&mut isolate, &context, &initialized_root);
    checks.push(pass(
        "cppgc-member/handles/empty_new_set_get",
        Json::obj(vec![
            ("empty_strong_none", Json::b(empty.0.is_none())),
            ("empty_weak_none", Json::b(empty.1.is_none())),
            ("set_strong_id", Json::i(i64::from(assigned.0.unwrap()))),
            ("set_weak_id", Json::i(i64::from(assigned.1.unwrap()))),
            ("set_repeated_identity", Json::b(assigned.2)),
            ("new_strong_id", Json::i(i64::from(initialized.0.unwrap()))),
            ("new_weak_id", Json::i(i64::from(initialized.1.unwrap()))),
            ("new_repeated_identity", Json::b(initialized.2)),
        ]),
    ));

    full_gc(&mut isolate, &context);
    let child_two_survived = child_two.get().map(|node| node.id);
    let before_reassign_drops = drops.load(Ordering::SeqCst);
    let child_three = assign_new_child(
        &mut isolate,
        &context,
        &root,
        3,
        true,
        false,
        tracker.clone(),
    );
    let no_synchronous_drop = drops.load(Ordering::SeqCst) == before_reassign_drops;
    full_gc(&mut isolate, &context);
    let after_reassign = edge_ids(&mut isolate, &context, &root);
    let child_two_cleared = child_two.get().is_none() && after_reassign.1.is_none();
    let before_clear = drops.load(Ordering::SeqCst);
    clear_strong(&mut isolate, &context, &root);
    let clear_not_synchronous = drops.load(Ordering::SeqCst) == before_clear;
    full_gc(&mut isolate, &context);
    let child_three_cleared = child_three.get().is_none();
    let child_four = assign_new_child(
        &mut isolate,
        &context,
        &root,
        4,
        true,
        true,
        tracker.clone(),
    );
    full_gc(&mut isolate, &context);
    let reused = edge_ids(&mut isolate, &context, &root);
    checks.push(pass(
        "cppgc-member/strong/reassign_clear_reuse",
        Json::obj(vec![
            (
                "child_survives_member_only",
                Json::b(child_two_survived == Some(2)),
            ),
            ("drops_while_strong", Json::i(before_reassign_drops as i64)),
            ("reassign_not_synchronous", Json::b(no_synchronous_drop)),
            ("old_child_and_weak_cleared", Json::b(child_two_cleared)),
            (
                "new_strong_id",
                Json::i(i64::from(after_reassign.0.unwrap())),
            ),
            ("clear_not_synchronous", Json::b(clear_not_synchronous)),
            ("cleared_child_collected", Json::b(child_three_cleared)),
            ("reused_strong_id", Json::i(i64::from(reused.0.unwrap()))),
            ("reused_weak_id", Json::i(i64::from(reused.1.unwrap()))),
            ("reused_identity", Json::b(reused.2)),
            (
                "reused_observer_id",
                Json::i(i64::from(child_four.get().unwrap().id)),
            ),
        ]),
    ));

    let (weak_root, weak_root_observer) =
        create_root(&mut isolate, &context, 20, drops.clone(), drop_ids.clone());
    let weak_only = assign_new_child(
        &mut isolate,
        &context,
        &weak_root,
        21,
        false,
        true,
        tracker.clone(),
    );
    full_gc(&mut isolate, &context);
    let first_weak_cleared =
        weak_only.get().is_none() && edge_ids(&mut isolate, &context, &weak_root).1.is_none();
    let reused_weak = assign_new_child(
        &mut isolate,
        &context,
        &weak_root,
        22,
        true,
        true,
        tracker.clone(),
    );
    full_gc(&mut isolate, &context);
    let weak_reused_alive = edge_ids(&mut isolate, &context, &weak_root);
    clear_strong(&mut isolate, &context, &weak_root);
    full_gc(&mut isolate, &context);
    let weak_reused_cleared =
        reused_weak.get().is_none() && edge_ids(&mut isolate, &context, &weak_root).1.is_none();
    checks.push(pass(
        "cppgc-member/weak/clearing_and_reuse",
        Json::obj(vec![
            ("weak_only_cleared", Json::b(first_weak_cleared)),
            (
                "weak_only_drop_seen",
                Json::b(sorted_drop_ids(&drop_ids).contains(&21)),
            ),
            (
                "reused_alive_while_strong",
                Json::b(weak_reused_alive.0 == Some(22) && weak_reused_alive.1 == Some(22)),
            ),
            ("reused_identity", Json::b(weak_reused_alive.2)),
            ("reused_cleared_after_release", Json::b(weak_reused_cleared)),
        ]),
    ));

    let (cycle_a, cycle_b) = create_cycle(&mut isolate, &context, drops.clone(), drop_ids.clone());
    full_gc(&mut isolate, &context);
    let cycle_ids = sorted_drop_ids(&drop_ids);
    checks.push(pass(
        "cppgc-member/cycle/unreachable_strong_cycle",
        Json::obj(vec![
            ("first_cleared", Json::b(cycle_a.get().is_none())),
            ("second_cleared", Json::b(cycle_b.get().is_none())),
            ("first_dropped", Json::b(cycle_ids.contains(&30))),
            ("second_dropped", Json::b(cycle_ids.contains(&31))),
        ]),
    ));

    drop(root);
    drop(initialized_root);
    drop(weak_root);
    full_gc(&mut isolate, &context);
    let before_teardown_ids = sorted_drop_ids(&drop_ids);
    let (teardown_root, teardown_root_observer) =
        create_root(&mut isolate, &context, 40, drops.clone(), drop_ids.clone());
    let teardown_child = assign_new_child(
        &mut isolate,
        &context,
        &teardown_root,
        41,
        true,
        true,
        tracker,
    );
    drop(context);
    drop(isolate);
    let after_teardown_ids = sorted_drop_ids(&drop_ids);
    let teardown_handles_cleared =
        teardown_root_observer.get().is_none() && teardown_child.get().is_none();
    drop(teardown_root);
    drop(teardown_root_observer);
    drop(teardown_child);
    drop(root_observer);
    drop(child_two);
    drop(child_three);
    drop(child_four);
    drop(initialized_child);
    drop(weak_root_observer);
    drop(weak_only);
    drop(reused_weak);
    drop(cycle_a);
    drop(cycle_b);
    let after_handle_drop_ids = sorted_drop_ids(&drop_ids);
    let all_expected = [1, 2, 3, 4, 10, 11, 20, 21, 22, 30, 31, 40, 41];
    checks.push(pass(
        "cppgc-member/lifecycle/owner_isolate_teardown",
        Json::obj(vec![
            (
                "pre_teardown_ids",
                Json::arr(
                    before_teardown_ids
                        .iter()
                        .map(|id| Json::i(i64::from(*id)))
                        .collect(),
                ),
            ),
            (
                "teardown_handles_cleared",
                Json::b(teardown_handles_cleared),
            ),
            (
                "teardown_added_owner_and_child",
                Json::b(after_teardown_ids.ends_with(&[40, 41])),
            ),
            ("all_ids_once", Json::b(after_teardown_ids == all_expected)),
            (
                "handle_drop_does_not_redestroy",
                Json::b(after_handle_drop_ids == after_teardown_ids),
            ),
            ("total_drops", Json::i(drops.load(Ordering::SeqCst) as i64)),
        ]),
    ));

    let v8_dispose = unsafe { v8::V8::dispose() };
    unsafe { v8::cppgc::shutdown_process() };
    v8::V8::dispose_platform();
    drop(platform);

    for check in &checks {
        println!("{}", check.to_line());
    }
    println!("{}", summary_line(checks.len(), checks.len(), 0));
    assert!(v8_dispose);
}

fn main() {
    run();
}
