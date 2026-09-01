//! Breadth oracle for generic cppgc state and traced aggregates.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This deliberately
//! exercises public generic shapes that the fixture-oriented Go facade does
//! not yet model: an arbitrary non-scalar `GcCell<T>` and one user-defined
//! `Traced` aggregate containing two strong `Member`s, a `WeakMember`, and a
//! V8 `TracedReference`.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::ffi::CStr;
use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use v8::cppgc::{
    GarbageCollected, GcCell, Member, Persistent, Traced, Visitor, WeakMember, WeakPersistent,
};

#[derive(Clone, Default)]
struct StringLog(Arc<Mutex<Vec<String>>>);

impl StringLog {
    fn push(&self, value: String) {
        self.0.lock().unwrap().push(value);
    }

    fn values(&self) -> Vec<String> {
        self.0.lock().unwrap().clone()
    }
}

struct NestedState {
    revision: i32,
}

struct ComplexState {
    label: String,
    numbers: Vec<i32>,
    nested: Box<NestedState>,
    drops: StringLog,
}

impl ComplexState {
    fn summary(&self) -> String {
        format!(
            "{}:{}:{}",
            self.label,
            self.numbers.iter().sum::<i32>(),
            self.nested.revision
        )
    }
}

impl Drop for ComplexState {
    fn drop(&mut self) {
        self.drops.push(self.summary());
    }
}

struct StateOwner {
    state: GcCell<ComplexState>,
}

unsafe impl GarbageCollected for StateOwner {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCGenericBreadthStateOwner"
    }
}

#[derive(Clone, Default)]
struct NodeDrops(Arc<Mutex<Vec<i32>>>);

impl NodeDrops {
    fn values(&self) -> Vec<i32> {
        let mut values = self.0.lock().unwrap().clone();
        values.sort_unstable();
        values
    }
}

struct GraphNode {
    id: i32,
    label: String,
    bytes: Vec<u8>,
    drops: NodeDrops,
}

unsafe impl GarbageCollected for GraphNode {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCGenericBreadthGraphNode"
    }
}

impl Drop for GraphNode {
    fn drop(&mut self) {
        self.drops.0.lock().unwrap().push(self.id);
    }
}

struct GraphEdges {
    first: Member<GraphNode>,
    second: Member<GraphNode>,
    observer: WeakMember<GraphNode>,
    javascript: v8::TracedReference<v8::Object>,
}

impl Traced for GraphEdges {
    fn trace(&self, visitor: &mut Visitor) {
        visitor.trace(&self.first);
        visitor.trace(&self.second);
        visitor.trace(&self.observer);
        visitor.trace(&self.javascript);
    }
}

struct UserPayload {
    title: String,
    bytes: Vec<u8>,
    nested_marker: Box<i32>,
}

struct GraphOwner {
    edges: GcCell<GraphEdges>,
    payload: UserPayload,
    traces: Arc<AtomicUsize>,
    drops: Arc<AtomicUsize>,
}

static GRAPH_NAME_CALLS: AtomicUsize = AtomicUsize::new(0);

unsafe impl GarbageCollected for GraphOwner {
    fn trace(&self, visitor: &mut Visitor) {
        self.traces.fetch_add(1, Ordering::SeqCst);
        visitor.trace(&self.edges);
    }

    fn get_name(&self) -> &'static CStr {
        GRAPH_NAME_CALLS.fetch_add(1, Ordering::SeqCst);
        c"CppGCGenericBreadthGraphOwner"
    }
}

impl Drop for GraphOwner {
    fn drop(&mut self) {
        self.drops.fetch_add(1, Ordering::SeqCst);
    }
}

fn initialize() -> v8::SharedRef<v8::Platform> {
    v8::V8::set_flags_from_string("--expose-gc");
    let platform = v8::new_unprotected_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform.clone());
    v8::V8::initialize();
    platform
}

fn allocate<'s, T: GarbageCollected + 'static>(
    scope: &mut v8::PinScope<'s, '_, ()>,
    value: T,
) -> v8::cppgc::UnsafePtr<T> {
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    unsafe { v8::cppgc::make_garbage_collected(heap, value) }
}

fn full_cppgc(isolate: &mut v8::OwnedIsolate) {
    v8::scope!(let scope, isolate);
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    unsafe {
        heap.collect_garbage_for_testing(v8::cppgc::EmbedderStackState::NoHeapPointers);
    }
}

fn full_v8_gc(isolate: &mut v8::OwnedIsolate, context: &v8::Global<v8::Context>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    scope.request_garbage_collection_for_testing(v8::GarbageCollectionType::Full);
}

/// Adapts the initialized pinned scope to the older `GcCell::with` argument
/// retained by v8 152.2.0. `PinnedRef` is repr(transparent) over this Pin.
fn handle_scope_ref<'a, 's, 'i>(
    scope: &'a mut v8::PinScope<'s, 'i>,
) -> &'a mut v8::HandleScope<'i> {
    let pinned: &mut Pin<&mut v8::HandleScope<'i>> = unsafe { std::mem::transmute(scope) };
    unsafe { pinned.as_mut().get_unchecked_mut() }
}

fn assert_send_sync<T: Send + Sync>() {}

#[inline(never)]
fn create_state_owner(isolate: &mut v8::OwnedIsolate, drops: StringLog) -> Persistent<StateOwner> {
    v8::scope!(let scope, isolate);
    let owner = allocate(
        scope,
        StateOwner {
            state: GcCell::new(ComplexState {
                label: "alpha".to_owned(),
                numbers: vec![1, 2, 3],
                nested: Box::new(NestedState { revision: 7 }),
                drops,
            }),
        },
    );
    Persistent::new(&owner)
}

fn exercise_state(
    isolate: &mut v8::OwnedIsolate,
    owner: &Persistent<StateOwner>,
    drops: StringLog,
) -> (String, String, String, bool, Vec<String>) {
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let initial = owner.get().unwrap().state.get(scope).summary();
    let first_address = {
        let state = owner.get().unwrap().state.get_mut(scope);
        state.label.push('+');
        state.numbers.push(4);
        state as *mut ComplexState as usize
    };
    let scope: &mut v8::PinScope<'_, '_> = scope;
    let (mutated, stable) = owner
        .get()
        .unwrap()
        .state
        .with(handle_scope_ref(scope), |_, state| {
            state.numbers.push(5);
            state.nested.revision += 5;
            (
                state.summary(),
                state as *mut ComplexState as usize == first_address,
            )
        });
    owner.get().unwrap().state.set(
        scope,
        ComplexState {
            label: "beta".to_owned(),
            numbers: vec![8, 13],
            nested: Box::new(NestedState { revision: 21 }),
            drops,
        },
    );
    let replacement = owner.get().unwrap().state.get(scope).summary();
    (
        initial,
        mutated,
        replacement,
        stable,
        owner.get().unwrap().state.get(scope).drops.values(),
    )
}

fn marker<'s>(scope: &mut v8::PinScope<'s, '_>, value: i32) -> v8::Local<'s, v8::Object> {
    let object = v8::Object::new(scope);
    let key = v8::String::new(scope, "marker").unwrap();
    let value = v8::Integer::new(scope, value);
    assert!(object.set(scope, key.into(), value.into()).is_some());
    object
}

fn graph_node<'s>(
    scope: &mut v8::PinScope<'s, '_, ()>,
    id: i32,
    drops: NodeDrops,
) -> v8::cppgc::UnsafePtr<GraphNode> {
    allocate(
        scope,
        GraphNode {
            id,
            label: format!("node-{id}"),
            bytes: vec![id as u8, (id * 2) as u8],
            drops,
        },
    )
}

type GraphRoots = (
    Persistent<GraphOwner>,
    WeakPersistent<GraphNode>,
    WeakPersistent<GraphNode>,
    WeakPersistent<GraphNode>,
);

#[inline(never)]
fn create_graph(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    node_drops: NodeDrops,
    traces: Arc<AtomicUsize>,
    owner_drops: Arc<AtomicUsize>,
) -> GraphRoots {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let first = graph_node(scope, 1, node_drops.clone());
    let second = graph_node(scope, 2, node_drops.clone());
    let weak = graph_node(scope, 3, node_drops);
    let javascript = marker(scope, 42);
    let javascript = v8::TracedReference::new(scope, javascript);
    let owner = allocate(
        scope,
        GraphOwner {
            edges: GcCell::new(GraphEdges {
                first: Member::new(&first),
                second: Member::new(&second),
                observer: WeakMember::new(&weak),
                javascript,
            }),
            payload: UserPayload {
                title: "graph-owner".to_owned(),
                bytes: vec![3, 1, 4, 1, 5],
                nested_marker: Box::new(2718),
            },
            traces,
            drops: owner_drops,
        },
    );
    (
        Persistent::new(&owner),
        WeakPersistent::new(&first),
        WeakPersistent::new(&second),
        WeakPersistent::new(&weak),
    )
}

struct GraphView {
    first: Option<i32>,
    second: Option<i32>,
    weak: Option<i32>,
    first_weak_same: bool,
    marker: i64,
    first_payload: String,
    second_payload: String,
    owner_payload: String,
}

fn graph_view(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    owner: &Persistent<GraphOwner>,
) -> GraphView {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let owner = owner.get().unwrap();
    let edges = owner.edges.get(scope);
    let first = unsafe { edges.first.get() };
    let second = unsafe { edges.second.get() };
    let weak = unsafe { edges.observer.get() };
    let javascript = edges.javascript.get(scope).expect("traced JS object");
    let key = v8::String::new(scope, "marker").unwrap();
    let marker = javascript
        .get(scope, key.into())
        .and_then(|value| value.integer_value(scope))
        .unwrap();
    GraphView {
        first: first.map(|value| value.id),
        second: second.map(|value| value.id),
        weak: weak.map(|value| value.id),
        first_weak_same: first
            .zip(weak)
            .is_some_and(|(first, weak)| std::ptr::eq(first, weak)),
        marker,
        first_payload: first
            .map(|value| format!("{}:{:?}", value.label, value.bytes))
            .unwrap_or_default(),
        second_payload: second
            .map(|value| format!("{}:{:?}", value.label, value.bytes))
            .unwrap_or_default(),
        owner_payload: format!(
            "{}:{:?}:{}",
            owner.payload.title, owner.payload.bytes, owner.payload.nested_marker
        ),
    }
}

#[inline(never)]
fn mutate_graph(
    isolate: &mut v8::OwnedIsolate,
    context: &v8::Global<v8::Context>,
    owner: &Persistent<GraphOwner>,
    drops: NodeDrops,
) -> (WeakPersistent<GraphNode>, WeakPersistent<GraphNode>) {
    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, context);
    let scope = &mut v8::ContextScope::new(scope, context);
    let fourth = graph_node(scope, 4, drops.clone());
    let fifth = graph_node(scope, 5, drops);
    let edges = owner.get().unwrap().edges.get_mut(scope);
    edges.first.set(&fourth);
    edges.second.set(&fifth);
    edges.observer.set(&fourth);
    (WeakPersistent::new(&fourth), WeakPersistent::new(&fifth))
}

fn custom_name_observation(isolate: &mut v8::OwnedIsolate) -> (bool, usize) {
    GRAPH_NAME_CALLS.store(0, Ordering::SeqCst);
    let mut snapshot = Vec::new();
    isolate.take_heap_snapshot(|chunk| {
        snapshot.extend_from_slice(chunk);
        true
    });
    let needle = b"CppGCGenericBreadthGraphOwner";
    let contains = snapshot
        .windows(needle.len())
        .any(|window| window == needle);
    (contains, GRAPH_NAME_CALLS.load(Ordering::SeqCst))
}

fn run() {
    assert_send_sync::<GcCell<ComplexState>>();

    let platform = initialize();
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    let context = {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, Default::default());
        v8::Global::new(scope, context)
    };
    let mut checks: Vec<CheckOutcome> = Vec::new();

    let state_drops = StringLog::default();
    let state_owner = create_state_owner(&mut isolate, state_drops.clone());
    let (initial, mutated, replacement, storage_stable, after_set) =
        exercise_state(&mut isolate, &state_owner, state_drops.clone());
    checks.push(pass(
        "cppgc-generic-breadth/gc-cell/non-scalar-replacement",
        Json::obj(vec![
            ("initial", Json::s(&initial)),
            ("mutated", Json::s(&mutated)),
            ("replacement", Json::s(&replacement)),
            ("storage_stable_across_borrows", Json::b(storage_stable)),
            ("gc_cell_state_is_send_sync", Json::b(true)),
            (
                "replacement_dropped_old_synchronously",
                Json::b(after_set == ["alpha+:15:12"]),
            ),
            (
                "drops_after_set",
                Json::arr(after_set.iter().map(|value| Json::s(value)).collect()),
            ),
        ]),
    ));
    drop(state_owner);
    full_cppgc(&mut isolate);
    let state_after_collection = state_drops.values();
    full_cppgc(&mut isolate);
    checks.push(pass(
        "cppgc-generic-breadth/gc-cell/non-scalar-lifecycle",
        Json::obj(vec![
            (
                "drops_after_owner_collection",
                Json::arr(
                    state_after_collection
                        .iter()
                        .map(|value| Json::s(value))
                        .collect(),
                ),
            ),
            (
                "current_state_dropped_once",
                Json::b(state_after_collection == ["alpha+:15:12", "beta:21:21"]),
            ),
            (
                "repeat_gc_does_not_redestroy",
                Json::b(state_drops.values() == state_after_collection),
            ),
        ]),
    ));

    let node_drops = NodeDrops::default();
    let traces = Arc::new(AtomicUsize::new(0));
    let owner_drops = Arc::new(AtomicUsize::new(0));
    let (owner, first, second, weak) = create_graph(
        &mut isolate,
        &context,
        node_drops.clone(),
        traces.clone(),
        owner_drops.clone(),
    );
    full_v8_gc(&mut isolate, &context);
    full_cppgc(&mut isolate);
    let initial_graph = graph_view(&mut isolate, &context, &owner);
    checks.push(pass(
        "cppgc-generic-breadth/traced-aggregate/two-strong-weak-js",
        Json::obj(vec![
            ("first", Json::i(i64::from(initial_graph.first.unwrap()))),
            ("second", Json::i(i64::from(initial_graph.second.unwrap()))),
            (
                "weak_cleared",
                Json::b(initial_graph.weak.is_none() && weak.get().is_none()),
            ),
            ("traced_js_marker", Json::i(initial_graph.marker)),
            ("first_payload", Json::s(&initial_graph.first_payload)),
            ("second_payload", Json::s(&initial_graph.second_payload)),
            ("owner_payload", Json::s(&initial_graph.owner_payload)),
            (
                "trace_calls_positive",
                Json::b(traces.load(Ordering::SeqCst) > 0),
            ),
            (
                "strong_targets_survive",
                Json::b(first.get().is_some() && second.get().is_some()),
            ),
            (
                "drops_after_initial_gc",
                Json::arr(
                    node_drops
                        .values()
                        .into_iter()
                        .map(|id| Json::i(i64::from(id)))
                        .collect(),
                ),
            ),
        ]),
    ));

    let (fourth, fifth) = mutate_graph(&mut isolate, &context, &owner, node_drops.clone());
    let mutation_not_synchronous = node_drops.values() == [3];
    full_cppgc(&mut isolate);
    let mutated_graph = graph_view(&mut isolate, &context, &owner);
    let after_mutation_drops = node_drops.values();
    checks.push(pass(
        "cppgc-generic-breadth/traced-aggregate/mutation-barriers",
        Json::obj(vec![
            (
                "mutation_not_synchronous",
                Json::b(mutation_not_synchronous),
            ),
            ("old_first_collected", Json::b(first.get().is_none())),
            ("old_second_collected", Json::b(second.get().is_none())),
            (
                "new_first",
                Json::i(i64::from(mutated_graph.first.unwrap())),
            ),
            (
                "new_second",
                Json::i(i64::from(mutated_graph.second.unwrap())),
            ),
            (
                "weak_tracks_new_first",
                Json::b(mutated_graph.weak == Some(4) && mutated_graph.first_weak_same),
            ),
            (
                "new_targets_survive",
                Json::b(fourth.get().is_some() && fifth.get().is_some()),
            ),
            ("traced_js_still_live", Json::b(mutated_graph.marker == 42)),
            (
                "drops_after_mutation_gc",
                Json::arr(
                    after_mutation_drops
                        .into_iter()
                        .map(|id| Json::i(i64::from(id)))
                        .collect(),
                ),
            ),
        ]),
    ));

    let (snapshot_contains_name, name_calls) = custom_name_observation(&mut isolate);
    drop(owner);
    full_cppgc(&mut isolate);
    let after_owner_collection = node_drops.values();
    full_cppgc(&mut isolate);
    checks.push(pass(
        "cppgc-generic-breadth/traced-aggregate/name-and-lifecycle",
        Json::obj(vec![
            (
                "snapshot_contains_custom_name",
                Json::b(snapshot_contains_name),
            ),
            ("get_name_called", Json::b(name_calls > 0)),
            (
                "owner_dropped_once",
                Json::b(owner_drops.load(Ordering::SeqCst) == 1),
            ),
            (
                "all_nodes_dropped_once",
                Json::b(after_owner_collection == [1, 2, 3, 4, 5]),
            ),
            (
                "new_weak_handles_cleared",
                Json::b(fourth.get().is_none() && fifth.get().is_none()),
            ),
            (
                "repeat_gc_does_not_redestroy",
                Json::b(node_drops.values() == after_owner_collection),
            ),
        ]),
    ));

    drop(first);
    drop(second);
    drop(weak);
    drop(fourth);
    drop(fifth);
    drop(context);
    drop(isolate);
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
