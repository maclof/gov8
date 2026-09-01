//! Adjacent lifetime behavior: global handle clone/equality semantics,
//! weak handles under forced GC, and the thread constraint on isolate
//! disposal.
//!
//! Characterized contract (the Go port must reproduce):
//! - Cloning a `Global` yields another strong handle to the same object:
//!   locals reopened from both compare strictly equal, the handles compare
//!   equal (`PartialEq`), and distinct objects compare unequal.
//! - A `Weak` handle stays non-empty while a strong handle keeps the object
//!   alive across a forced major GC, and reports empty (`is_empty`,
//!   `to_global == None`) once the last strong reference is gone and a major
//!   GC has run. An explicitly empty weak is empty from the start.
//!   Forcing uses `Isolate::low_memory_notification()` (a synchronous major
//!   GC) with no other references, which makes the outcome deterministic
//!   without deviating from the pinned "no V8 flags" configuration.
//!
//! Cross-thread isolate access is characterized in
//! `tests/terminate_from_other_thread.rs` (termination via the thread-safe
//! `IsolateHandle`); moving the `Isolate` itself across threads is rejected
//! at compile time by the crate (no `Send`), which needs no runtime test.

use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};

pub(crate) fn global_clone_equality() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let obj1 = v8::Object::new(scope);
    let g1 = v8::Global::new(scope, obj1);
    let g1_clone = g1.clone();
    let obj2 = v8::Object::new(scope);
    let g2 = v8::Global::new(scope, obj2);

    let l1 = v8::Local::new(scope, &g1);
    let l1_clone = v8::Local::new(scope, &g1_clone);
    let l2 = v8::Local::new(scope, &g2);

    let clone_same_object = l1.strict_equals(l1_clone.into());
    let distinct_objects = !l1.strict_equals(l2.into());
    let eq_impl_clone = g1 == g1_clone;
    let ne_impl_distinct = g1 != g2;

    let actual = Json::obj(vec![
        ("clone_same_object", Json::b(clone_same_object)),
        ("distinct_objects", Json::b(distinct_objects)),
        ("eq_impl_clone", Json::b(eq_impl_clone)),
        ("ne_impl_distinct", Json::b(ne_impl_distinct)),
    ]);
    let expected = Json::obj(vec![
        ("clone_same_object", Json::b(true)),
        ("distinct_objects", Json::b(true)),
        ("eq_impl_clone", Json::b(true)),
        ("ne_impl_distinct", Json::b(true)),
    ]);
    vec![expect_eq(
        "lifecycle/global_clone_equality",
        expected,
        actual,
    )]
}

pub(crate) fn weak_collect_forced_gc() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());

    // Create the object inside a context-carrying handle scope so only the
    // Global escapes (it then outlives both the scope and the context).
    let held = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let obj = v8::Object::new(scope);
        v8::Global::new(scope, obj)
    };
    let weak = v8::Weak::new(isolate, &held);

    // A forced major GC must not collect the strongly held object.
    // `low_memory_notification` synchronously performs a major GC and, unlike
    // `request_garbage_collection_for_testing`, needs no `--expose-gc` flag,
    // so the oracle's pinned "no V8 flags" configuration stays intact.
    isolate.low_memory_notification();
    let alive_while_strong = !weak.is_empty();
    let resurrect_while_strong = weak.to_global(isolate).is_some();

    // Drop the last strong reference and force collection. The second major
    // GC covers the weak-handle two-pass processing.
    drop(held);
    isolate.low_memory_notification();
    isolate.low_memory_notification();
    let collected_after_gc = weak.is_empty();
    let resurrect_after_gc_is_none = weak.to_global(isolate).is_none();

    // An explicitly created empty weak handle is empty without any GC.
    let empty_weak_is_empty = v8::Weak::<v8::Object>::empty(isolate).is_empty();

    let actual = Json::obj(vec![
        ("alive_while_strong", Json::b(alive_while_strong)),
        ("resurrect_while_strong", Json::b(resurrect_while_strong)),
        ("collected_after_gc", Json::b(collected_after_gc)),
        (
            "resurrect_after_gc_is_none",
            Json::b(resurrect_after_gc_is_none),
        ),
        ("empty_weak_is_empty", Json::b(empty_weak_is_empty)),
    ]);
    let expected = Json::obj(vec![
        ("alive_while_strong", Json::b(true)),
        ("resurrect_while_strong", Json::b(true)),
        ("collected_after_gc", Json::b(true)),
        ("resurrect_after_gc_is_none", Json::b(true)),
        ("empty_weak_is_empty", Json::b(true)),
    ]);
    vec![expect_eq(
        "lifecycle/weak_collect_forced_gc",
        expected,
        actual,
    )]
}
