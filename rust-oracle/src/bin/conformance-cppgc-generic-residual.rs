//! Residual generic cppgc oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This covers the
//! publicly executable generic surface left after the Member/WeakMember
//! oracle: GcCell mutation, traced Option<Member<T>>, allocation layout, and
//! GarbageCollected::get_name visibility.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::ffi::CStr;
use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use v8::cppgc::{GarbageCollected, GcCell, Member, Persistent, Visitor, WeakPersistent};

#[derive(Clone)]
struct DropLog(Arc<Mutex<Vec<i32>>>);

impl DropLog {
    fn new() -> Self {
        Self(Arc::new(Mutex::new(Vec::new())))
    }

    fn values(&self) -> Vec<i32> {
        self.0.lock().unwrap().clone()
    }
}

struct CellValue {
    value: i32,
    drops: DropLog,
}

impl Drop for CellValue {
    fn drop(&mut self) {
        self.drops.0.lock().unwrap().push(self.value);
    }
}

struct CellOwner {
    value: GcCell<CellValue>,
}

unsafe impl GarbageCollected for CellOwner {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCGenericResidualCellOwner"
    }
}

struct Child {
    id: i32,
    drops: DropLog,
}

impl Drop for Child {
    fn drop(&mut self) {
        self.drops.0.lock().unwrap().push(self.id);
    }
}

unsafe impl GarbageCollected for Child {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCGenericResidualChild"
    }
}

struct OptionalOwner {
    child: GcCell<Option<Member<Child>>>,
}

unsafe impl GarbageCollected for OptionalOwner {
    fn trace(&self, visitor: &mut Visitor) {
        visitor.trace(&self.child);
    }

    fn get_name(&self) -> &'static CStr {
        c"CppGCGenericResidualOptionalOwner"
    }
}

static NAME_CALLS: AtomicUsize = AtomicUsize::new(0);
static NAMED_DROPS: AtomicUsize = AtomicUsize::new(0);

struct NamedObject;

unsafe impl GarbageCollected for NamedObject {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        NAME_CALLS.fetch_add(1, Ordering::SeqCst);
        c"CppGCGenericResidualNamedObject"
    }
}

impl Drop for NamedObject {
    fn drop(&mut self) {
        NAMED_DROPS.fetch_add(1, Ordering::SeqCst);
    }
}

static ZERO_DROPS: AtomicUsize = AtomicUsize::new(0);
static ALIGNED_DROPS: AtomicUsize = AtomicUsize::new(0);

struct ZeroSized;

unsafe impl GarbageCollected for ZeroSized {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCGenericResidualZeroSized"
    }
}

impl Drop for ZeroSized {
    fn drop(&mut self) {
        ZERO_DROPS.fetch_add(1, Ordering::SeqCst);
    }
}

#[repr(align(16))]
struct Aligned16(u8);

unsafe impl GarbageCollected for Aligned16 {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCGenericResidualAligned16"
    }
}

impl Drop for Aligned16 {
    fn drop(&mut self) {
        ALIGNED_DROPS.fetch_add(1, Ordering::SeqCst);
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

fn full_gc(isolate: &mut v8::OwnedIsolate) {
    v8::scope!(let scope, isolate);
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    unsafe {
        heap.collect_garbage_for_testing(v8::cppgc::EmbedderStackState::NoHeapPointers);
    }
}

#[inline(never)]
fn create_cell_owner(isolate: &mut v8::OwnedIsolate, drops: DropLog) -> Persistent<CellOwner> {
    v8::scope!(let scope, isolate);
    let owner = allocate(
        scope,
        CellOwner {
            value: GcCell::new(CellValue { value: 10, drops }),
        },
    );
    Persistent::new(&owner)
}

fn observe_initial_and_set(
    isolate: &mut v8::OwnedIsolate,
    owner: &Persistent<CellOwner>,
    drops: DropLog,
) -> (i32, i32, Vec<i32>) {
    v8::scope!(let scope, isolate);
    let initial = owner.get().unwrap().value.get(scope).value;
    owner
        .get()
        .unwrap()
        .value
        .set(scope, CellValue { value: 20, drops });
    let after_set = owner.get().unwrap().value.get(scope).value;
    (
        initial,
        after_set,
        owner.get().unwrap().value.get(scope).drops.values(),
    )
}

/// Adapts the initialized pinned scope to the older argument shape retained by
/// `GcCell::with` in v8 152.2.0. `PinnedRef` is publicly documented and
/// declared as a transparent wrapper around `Pin<&mut T>` in `scope.rs`.
fn handle_scope_ref<'a, 's, 'i>(
    scope: &'a mut v8::PinScope<'s, 'i>,
) -> &'a mut v8::HandleScope<'i> {
    let pinned: &mut Pin<&mut v8::HandleScope<'i>> = unsafe {
        // SAFETY: PinnedRef is repr(transparent) over exactly this Pin type.
        std::mem::transmute(scope)
    };
    unsafe {
        // SAFETY: the HandleScope remains pinned in its ScopeStorage. We only
        // create a temporary mutable reference and never move the scope.
        pinned.as_mut().get_unchecked_mut()
    }
}

fn mutate_cell(
    isolate: &mut v8::OwnedIsolate,
    owner: &Persistent<CellOwner>,
) -> (i32, i32, i32, bool) {
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let (get_mut_value, get_mut_address) = {
        let value = owner.get().unwrap().value.get_mut(scope);
        value.value += 1;
        (value.value, value as *mut CellValue as usize)
    };
    let scope: &mut v8::PinScope<'_, '_> = scope;
    let (with_value, same_address) =
        owner
            .get()
            .unwrap()
            .value
            .with(handle_scope_ref(scope), |_, value| {
                let same_address = value as *mut CellValue as usize == get_mut_address;
                value.value += 9;
                (value.value, same_address)
            });
    let final_value = owner.get().unwrap().value.get(scope).value;
    (get_mut_value, with_value, final_value, same_address)
}

#[inline(never)]
fn create_optional_owner(isolate: &mut v8::OwnedIsolate) -> Persistent<OptionalOwner> {
    v8::scope!(let scope, isolate);
    let owner = allocate(
        scope,
        OptionalOwner {
            child: GcCell::new(None),
        },
    );
    Persistent::new(&owner)
}

#[inline(never)]
fn replace_child(
    isolate: &mut v8::OwnedIsolate,
    owner: &Persistent<OptionalOwner>,
    id: i32,
    drops: DropLog,
) -> WeakPersistent<Child> {
    v8::scope!(let scope, isolate);
    let child = allocate(scope, Child { id, drops });
    owner
        .get()
        .unwrap()
        .child
        .set(scope, Some(Member::new(&child)));
    WeakPersistent::new(&child)
}

fn optional_child_id(
    isolate: &mut v8::OwnedIsolate,
    owner: &Persistent<OptionalOwner>,
) -> Option<i32> {
    v8::scope!(let scope, isolate);
    owner
        .get()
        .unwrap()
        .child
        .get(scope)
        .as_ref()
        .and_then(|member| unsafe { member.get() })
        .map(|child| child.id)
}

fn clear_optional_child(isolate: &mut v8::OwnedIsolate, owner: &Persistent<OptionalOwner>) {
    v8::scope!(let scope, isolate);
    owner.get().unwrap().child.set(scope, None);
}

#[inline(never)]
fn create_named(isolate: &mut v8::OwnedIsolate) -> Persistent<NamedObject> {
    v8::scope!(let scope, isolate);
    let value = allocate(scope, NamedObject);
    Persistent::new(&value)
}

fn custom_name_observation(isolate: &mut v8::OwnedIsolate) -> (bool, bool, usize) {
    NAME_CALLS.store(0, Ordering::SeqCst);
    let mut snapshot = Vec::new();
    isolate.take_heap_snapshot(|chunk| {
        snapshot.extend_from_slice(chunk);
        true
    });
    let needle = b"CppGCGenericResidualNamedObject";
    let contains_name = snapshot
        .windows(needle.len())
        .any(|window| window == needle);
    let calls = NAME_CALLS.load(Ordering::SeqCst);
    (contains_name, calls > 0, calls)
}

#[inline(never)]
fn create_layout_roots(
    isolate: &mut v8::OwnedIsolate,
) -> (Persistent<ZeroSized>, Persistent<Aligned16>, bool, u8) {
    v8::scope!(let scope, isolate);
    let zero = allocate(scope, ZeroSized);
    let aligned = allocate(scope, Aligned16(7));
    let is_aligned = (unsafe { aligned.as_ref() } as *const Aligned16 as usize).is_multiple_of(16);
    let marker = unsafe { aligned.as_ref() }.0;
    (
        Persistent::new(&zero),
        Persistent::new(&aligned),
        is_aligned,
        marker,
    )
}

fn run() {
    let platform = initialize();
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    let mut checks: Vec<CheckOutcome> = Vec::new();

    let cell_drops = DropLog::new();
    let cell_owner = create_cell_owner(&mut isolate, cell_drops.clone());
    let (initial, after_set, drops_after_set) =
        observe_initial_and_set(&mut isolate, &cell_owner, cell_drops.clone());
    checks.push(pass(
        "cppgc-generic-residual/gc-cell/new_get_set_drop",
        Json::obj(vec![
            ("initial", Json::i(i64::from(initial))),
            ("after_set", Json::i(i64::from(after_set))),
            (
                "replaced_value_dropped_synchronously",
                Json::b(drops_after_set == [10]),
            ),
            (
                "drops_after_set",
                Json::arr(
                    drops_after_set
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
        ]),
    ));

    let (get_mut_value, with_value, final_value, same_address) =
        mutate_cell(&mut isolate, &cell_owner);
    checks.push(pass(
        "cppgc-generic-residual/gc-cell/get-mut_with",
        Json::obj(vec![
            ("get_mut_value", Json::i(i64::from(get_mut_value))),
            ("with_value", Json::i(i64::from(with_value))),
            ("final_value", Json::i(i64::from(final_value))),
            ("same_cell_storage", Json::b(same_address)),
        ]),
    ));
    drop(cell_owner);
    full_gc(&mut isolate);
    let after_owner_collection = cell_drops.values();
    full_gc(&mut isolate);
    checks.push(pass(
        "cppgc-generic-residual/gc-cell/lifecycle",
        Json::obj(vec![
            (
                "drops_after_owner_collection",
                Json::arr(
                    after_owner_collection
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
            (
                "current_value_dropped_once",
                Json::b(after_owner_collection == [10, 30]),
            ),
            (
                "repeat_gc_does_not_redestroy",
                Json::b(cell_drops.values() == after_owner_collection),
            ),
        ]),
    ));

    let child_drops = DropLog::new();
    let optional_owner = create_optional_owner(&mut isolate);
    let initially_none = optional_child_id(&mut isolate, &optional_owner).is_none();
    let first = replace_child(&mut isolate, &optional_owner, 1, child_drops.clone());
    full_gc(&mut isolate);
    let first_survives = first.get().map(|child| child.id) == Some(1)
        && optional_child_id(&mut isolate, &optional_owner) == Some(1);
    let before_replace = child_drops.values();
    let second = replace_child(&mut isolate, &optional_owner, 2, child_drops.clone());
    let replacement_not_synchronous = child_drops.values() == before_replace;
    full_gc(&mut isolate);
    let drops_after_replace = child_drops.values();
    let first_cleared = first.get().is_none();
    let second_survives = second.get().map(|child| child.id) == Some(2)
        && optional_child_id(&mut isolate, &optional_owner) == Some(2);
    checks.push(pass(
        "cppgc-generic-residual/member/replacement_barrier",
        Json::obj(vec![
            ("initially_none", Json::b(initially_none)),
            ("first_survives_traced_some", Json::b(first_survives)),
            (
                "replacement_not_synchronous",
                Json::b(replacement_not_synchronous),
            ),
            ("old_child_cleared_after_gc", Json::b(first_cleared)),
            (
                "replacement_survives_write_barrier",
                Json::b(second_survives),
            ),
            (
                "drops_after_replace",
                Json::arr(
                    drops_after_replace
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
        ]),
    ));

    clear_optional_child(&mut isolate, &optional_owner);
    let visible_none = optional_child_id(&mut isolate, &optional_owner).is_none();
    let before_none_gc = child_drops.values();
    full_gc(&mut isolate);
    let after_none_gc = child_drops.values();
    checks.push(pass(
        "cppgc-generic-residual/option-member/some_none_trace",
        Json::obj(vec![
            ("none_visible_immediately", Json::b(visible_none)),
            (
                "none_assignment_not_synchronous",
                Json::b(before_none_gc == [1]),
            ),
            ("former_some_cleared", Json::b(second.get().is_none())),
            (
                "drops_after_none_gc",
                Json::arr(
                    after_none_gc
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
            (
                "both_children_dropped_once",
                Json::b(after_none_gc == [1, 2]),
            ),
        ]),
    ));

    let named = create_named(&mut isolate);
    let (snapshot_contains_name, get_name_called, raw_call_count) =
        custom_name_observation(&mut isolate);
    let named_alive_during_snapshot = named.get().is_some();
    drop(named);
    full_gc(&mut isolate);
    checks.push(pass(
        "cppgc-generic-residual/name/heap_snapshot",
        Json::obj(vec![
            (
                "snapshot_contains_custom_name",
                Json::b(snapshot_contains_name),
            ),
            ("get_name_called", Json::b(get_name_called)),
            ("get_name_call_count_positive", Json::b(raw_call_count > 0)),
            (
                "named_alive_during_snapshot",
                Json::b(named_alive_during_snapshot),
            ),
            (
                "named_dropped_after_root_release",
                Json::b(NAMED_DROPS.load(Ordering::SeqCst) == 1),
            ),
        ]),
    ));

    ZERO_DROPS.store(0, Ordering::SeqCst);
    ALIGNED_DROPS.store(0, Ordering::SeqCst);
    let (zero, aligned, aligned_address, marker) = create_layout_roots(&mut isolate);
    let before_release = (
        ZERO_DROPS.load(Ordering::SeqCst),
        ALIGNED_DROPS.load(Ordering::SeqCst),
    );
    drop(zero);
    drop(aligned);
    full_gc(&mut isolate);
    let after_release = (
        ZERO_DROPS.load(Ordering::SeqCst),
        ALIGNED_DROPS.load(Ordering::SeqCst),
    );
    full_gc(&mut isolate);
    let after_repeat = (
        ZERO_DROPS.load(Ordering::SeqCst),
        ALIGNED_DROPS.load(Ordering::SeqCst),
    );
    checks.push(pass(
        "cppgc-generic-residual/layout/zero_align16_destruction",
        Json::obj(vec![
            (
                "zero_size",
                Json::i(std::mem::size_of::<ZeroSized>() as i64),
            ),
            (
                "zero_alignment",
                Json::i(std::mem::align_of::<ZeroSized>() as i64),
            ),
            (
                "aligned_size",
                Json::i(std::mem::size_of::<Aligned16>() as i64),
            ),
            (
                "aligned_alignment",
                Json::i(std::mem::align_of::<Aligned16>() as i64),
            ),
            ("aligned_address", Json::b(aligned_address)),
            ("aligned_marker", Json::i(i64::from(marker))),
            ("no_drop_while_rooted", Json::b(before_release == (0, 0))),
            ("both_dropped_once", Json::b(after_release == (1, 1))),
            (
                "repeat_gc_does_not_redestroy",
                Json::b(after_repeat == after_release),
            ),
        ]),
    ));

    drop(optional_owner);
    drop(first);
    drop(second);
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
