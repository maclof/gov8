//! Snapshot, handle, and termination conformance runner binary.
//!
//! Independent third slice for the pinned `v8` crate (=152.2.0) on
//! `x86_64-pc-windows-msvc`, alongside the base registry (`conformance`)
//! and the host-interaction registry (`conformance-host`). It characterizes:
//!
//! - `Isolate::snapshot_creator` / `snapshot_creator_from_existing_snapshot`,
//!   `set_default_context`, `add_context`, `add_isolate_data`,
//!   `add_context_data`, `OwnedIsolate::create_blob` with both
//!   `FunctionCodeHandling` policies, `StartupData` predicates
//!   (`is_valid`), and every consumption path
//!   (`CreateParams::snapshot_blob` via `Isolate::new`,
//!   `Context::from_snapshot`, `get_isolate_data_from_snapshot_once`,
//!   `get_context_data_from_snapshot_once` including the "once" and
//!   `DataError` semantics).
//! - `Global`/`Weak` handle cloning, equality, raw round-trips, weak
//!   finalizers under forced collection (`low_memory_notification`),
//!   weak-drop cancellation, and guaranteed finalizers at isolate teardown.
//!   The `Global` type in this crate version has no public `reset` method;
//!   reset happens in `Drop` (pinned source `handle.rs`, `impl Drop for
//!   Global`), which the checks document observably.
//! - Thread-safe isolate handles (`Isolate::thread_safe_handle`):
//!   `terminate_execution` / `cancel_terminate_execution` /
//!   `is_execution_terminating` where safely observable on the isolate
//!   thread, plus a dedicated subprocess mode for cross-thread termination
//!   of a running loop.
//!
//! Output: normalized JSON-lines on stdout (same format as the other
//! runners), exit code 0 when every check passed, 1 otherwise. With
//! `mode=<name>` argument the binary runs one isolated scenario instead;
//! those modes exist for behavior that must not run inside the
//! deterministic report (documented panics, cross-thread interruption).
//!
//! This runner performs no platform shutdown, exactly like `conformance-host`.

use std::cell::RefCell;
use std::io::Write as _;
use std::process::ExitCode;
use std::rc::Rc;

use oracle::json::Json;
use oracle::report::{expect_eq, pass, CheckOutcome};

// ---------------------------------------------------------------------------
// harness (mirrors src/checks/harness.rs, which is pub(crate)-only)
// ---------------------------------------------------------------------------

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let src = v8::String::new(scope, source)?;
    v8::Script::compile(scope, src, None)?.run(scope)
}

fn eval_text(scope: &v8::PinScope<'_, '_>, source: &str) -> Option<String> {
    let value = eval(scope, source)?;
    Some(value.to_string(scope)?.to_rust_string_lossy(scope))
}

/// `DataError` variant name without the (toolchain-dependent) type strings.
fn data_error_kind(error: &v8::DataError) -> &'static str {
    match error {
        v8::DataError::BadType { .. } => "BadType",
        v8::DataError::NoData { .. } => "NoData",
    }
}

/// Kind label for a snapshot-data retrieval result ("Ok" or the error kind).
fn result_kind<T>(result: &Result<v8::Local<'_, T>, v8::DataError>) -> &'static str {
    match result {
        Ok(_) => "Ok",
        Err(error) => data_error_kind(error),
    }
}

/// Shared event log for weak-finalizer callbacks.
type EventLog = Rc<RefCell<Vec<&'static str>>>;

// ---------------------------------------------------------------------------
// snapshot checks
// ---------------------------------------------------------------------------

/// Creates one snapshot blob from a fresh creator isolate whose default
/// context defines `globalThis.a = 7` and a callable `globalThis.f`.
/// `keep` selects the `FunctionCodeHandling` policy.
fn make_blob(keep: bool) -> v8::StartupData {
    let mut creator = v8::Isolate::snapshot_creator(None, None);
    {
        v8::scope!(let scope, &mut creator);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval(scope, "globalThis.a = 7;").unwrap();
        eval(scope, "globalThis.f = function () { return a * 2; };").unwrap();
        scope.set_default_context(context);
    }
    let policy = if keep {
        v8::FunctionCodeHandling::Keep
    } else {
        v8::FunctionCodeHandling::Clear
    };
    creator.create_blob(policy).unwrap()
}

/// Startup-data predicates for both `FunctionCodeHandling` policies.
fn create_blob_policies() -> Vec<CheckOutcome> {
    let mut actual_policy = Vec::new();
    for keep in [false, true] {
        let data = make_blob(keep);
        actual_policy.push(Json::obj(vec![
            ("len_gt_zero", Json::b(!data.is_empty())),
            ("is_valid", Json::b(data.is_valid())),
        ]));
    }
    let policy_shape = Json::obj(vec![
        ("len_gt_zero", Json::b(true)),
        ("is_valid", Json::b(true)),
    ]);
    vec![expect_eq(
        "snapshot/create_blob_policies",
        Json::arr(vec![policy_shape.clone(), policy_shape]),
        Json::arr(actual_policy),
    )]
}

/// Default-context blob consumed through `CreateParams::snapshot_blob`:
/// `Context::new` in the consumer isolate instantiates the snapshotted
/// global object; both data and the compiled function remain usable.
fn default_context_create_params_roundtrip() -> Vec<CheckOutcome> {
    let blob = make_blob(false);
    let params = v8::Isolate::create_params().snapshot_blob(blob);
    let isolate = &mut v8::Isolate::new(params);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let a_is_7 = eval_text(scope, "String(a === 7)").unwrap_or_default();
    let f_call = eval_text(scope, "String(f())").unwrap_or_default();
    let f_is_function = eval(scope, "f").map(|v| v.is_function()).unwrap_or(false);

    let actual = Json::obj(vec![
        ("a_is_7", Json::s(&a_is_7)),
        ("f_call", Json::s(&f_call)),
        ("f_is_function", Json::b(f_is_function)),
    ]);
    let expected = Json::obj(vec![
        ("a_is_7", Json::s("true")),
        ("f_call", Json::s("14")),
        ("f_is_function", Json::b(true)),
    ]);
    vec![expect_eq(
        "snapshot/default_context_create_params_roundtrip",
        expected,
        actual,
    )]
}

/// Snapshot-of-snapshot chain: consume a `Keep` blob in a
/// `snapshot_creator_from_existing_snapshot` isolate, extend the default
/// context, and re-create a blob; then consume the chained blob through
/// `CreateParams` and observe both the inherited and the new global.
fn chained_snapshot_roundtrip() -> Vec<CheckOutcome> {
    let first = make_blob(true);

    let second = {
        let mut creator = v8::Isolate::snapshot_creator_from_existing_snapshot(first, None, None);
        {
            v8::scope!(let scope, &mut creator);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            // Inherited state from the first blob:
            let inherited = eval_text(scope, "String(a)").unwrap_or_default();
            assert_eq!(inherited, "7", "inherited default context state");
            eval(scope, "globalThis.b = a + 1;").unwrap();
            scope.set_default_context(context);
        }
        creator
            .create_blob(v8::FunctionCodeHandling::Clear)
            .unwrap()
    };
    let second_valid = second.is_valid();

    let params = v8::Isolate::create_params().snapshot_blob(second);
    let isolate = &mut v8::Isolate::new(params);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let a = eval_text(scope, "String(a)").unwrap_or_default();
    let b = eval_text(scope, "String(b)").unwrap_or_default();

    let actual = Json::obj(vec![
        ("second_blob_valid", Json::b(second_valid)),
        ("a", Json::s(&a)),
        ("b", Json::s(&b)),
    ]);
    let expected = Json::obj(vec![
        ("second_blob_valid", Json::b(true)),
        ("a", Json::s("7")),
        ("b", Json::s("8")),
    ]);
    vec![expect_eq("snapshot/chained_roundtrip", expected, actual)]
}

/// Added contexts (global proxy included) recover via
/// `Context::from_snapshot(index)`; indices come back from `add_context`
/// in insertion order, out-of-range indices produce `None`, and an added
/// context can be re-added at the same index in a creator isolate.
fn add_context_from_snapshot() -> Vec<CheckOutcome> {
    let index0;
    let index1;
    let blob = {
        let mut creator = v8::Isolate::snapshot_creator(None, None);
        {
            v8::scope!(let scope, &mut creator);
            // Upstream caveat: `create_blob` CHECK-fails if no default
            // context was set, even when only `add_context` is used, so a
            // minimal default context is always part of the blob.
            let default_ctx = v8::Context::new(scope, Default::default());
            scope.set_default_context(default_ctx);
            {
                let ctx0 = v8::Context::new(scope, Default::default());
                {
                    let s0 = &mut v8::ContextScope::new(scope, ctx0);
                    eval(s0, "globalThis.marker = 'c0';").unwrap();
                }
                index0 = scope.add_context(ctx0);
            }
            {
                let ctx1 = v8::Context::new(scope, Default::default());
                {
                    let s1 = &mut v8::ContextScope::new(scope, ctx1);
                    eval(s1, "globalThis.marker = 'c1';").unwrap();
                }
                index1 = scope.add_context(ctx1);
            }
        }
        creator
            .create_blob(v8::FunctionCodeHandling::Clear)
            .unwrap()
    };

    // Consume in a creator-from-existing isolate so the re-add index can be
    // observed too; that isolate is consumed by create_blob at the end.
    let mut consumer = v8::Isolate::snapshot_creator_from_existing_snapshot(blob, None, None);
    let marker0;
    let marker1;
    let oob_is_none;
    let readd_index;
    {
        v8::scope!(let scope, &mut consumer);
        // The consumer is a creator isolate; `create_blob` below requires a
        // default context on it as well, so seed one from the snapshot.
        let default_ctx = v8::Context::new(scope, Default::default());
        scope.set_default_context(default_ctx);

        let ctx0 = v8::Context::from_snapshot(scope, index0, Default::default());
        if let Some(ctx0) = ctx0 {
            let s0 = &mut v8::ContextScope::new(scope, ctx0);
            marker0 = eval_text(s0, "marker").unwrap_or_default();
        } else {
            marker0 = String::from("<none>");
        }

        let ctx1 = v8::Context::from_snapshot(scope, index1, Default::default());
        if let Some(ctx1) = ctx1 {
            let s1 = &mut v8::ContextScope::new(scope, ctx1);
            marker1 = eval_text(s1, "marker").unwrap_or_default();
        } else {
            marker1 = String::from("<none>");
        }

        oob_is_none = v8::Context::from_snapshot(scope, 7, Default::default()).is_none();

        let ctx0_again = v8::Context::from_snapshot(scope, index0, Default::default());
        readd_index = match ctx0_again {
            Some(ctx) => scope.add_context(ctx),
            None => usize::MAX,
        };
    }
    consumer
        .create_blob(v8::FunctionCodeHandling::Clear)
        .unwrap();

    let actual = Json::obj(vec![
        ("index0", Json::i(index0 as i64)),
        ("index1", Json::i(index1 as i64)),
        ("marker0", Json::s(&marker0)),
        ("marker1", Json::s(&marker1)),
        ("oob_is_none", Json::b(oob_is_none)),
        ("readd_index", Json::i(readd_index as i64)),
    ]);
    let expected = Json::obj(vec![
        ("index0", Json::i(0)),
        ("index1", Json::i(1)),
        ("marker0", Json::s("c0")),
        ("marker1", Json::s("c1")),
        ("oob_is_none", Json::b(true)),
        ("readd_index", Json::i(0)),
    ]);
    vec![expect_eq(
        "snapshot/add_context_from_snapshot",
        expected,
        actual,
    )]
}

/// Isolate-snapshot data: retrieved exactly once with
/// `get_isolate_data_from_snapshot_once`; the second request for the same
/// index and out-of-range indices yield `DataError::NoData`.
fn isolate_data_once() -> Vec<CheckOutcome> {
    let int_index;
    let str_index;
    let blob = {
        let mut creator = v8::Isolate::snapshot_creator(None, None);
        {
            v8::scope!(let scope, &mut creator);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            scope.set_default_context(context);
            let int_data: v8::Local<v8::Data> = v8::Integer::new(scope, 41).into();
            int_index = scope.add_isolate_data(int_data);
            let str_data: v8::Local<v8::Data> = v8::String::new(scope, "iso-data").unwrap().into();
            str_index = scope.add_isolate_data(str_data);
        }
        creator
            .create_blob(v8::FunctionCodeHandling::Clear)
            .unwrap()
    };

    let params = v8::Isolate::create_params().snapshot_blob(blob);
    let isolate = &mut v8::Isolate::new(params);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let first = scope.get_isolate_data_from_snapshot_once::<v8::Value>(int_index);
    let int_value = first
        .as_ref()
        .ok()
        .and_then(|v| v.integer_value(scope))
        .unwrap_or(-1);
    let second_kind =
        result_kind(&scope.get_isolate_data_from_snapshot_once::<v8::Value>(int_index));

    let s = scope.get_isolate_data_from_snapshot_once::<v8::String>(str_index);
    let text = s
        .as_ref()
        .map(|v| v.to_rust_string_lossy(scope))
        .unwrap_or_default();
    let oob_kind = result_kind(&scope.get_isolate_data_from_snapshot_once::<v8::Value>(9));

    let actual = Json::obj(vec![
        ("int_index", Json::i(int_index as i64)),
        ("int_value", Json::i(int_value)),
        ("second_read_kind", Json::s(second_kind)),
        ("str_index", Json::i(str_index as i64)),
        ("str_value", Json::s(&text)),
        ("oob_kind", Json::s(oob_kind)),
    ]);
    let expected = Json::obj(vec![
        ("int_index", Json::i(0)),
        ("int_value", Json::i(41)),
        ("second_read_kind", Json::s("NoData")),
        ("str_index", Json::i(1)),
        ("str_value", Json::s("iso-data")),
        ("oob_kind", Json::s("NoData")),
    ]);
    vec![expect_eq("snapshot/isolate_data_once", expected, actual)]
}

/// Context-snapshot data: same "once" semantics through
/// `get_context_data_from_snapshot_once`, plus the `BadType` behavior for a
/// wrongly typed request (`v8::Private` requested over an `Integer`).
fn context_data_once_and_badtype() -> Vec<CheckOutcome> {
    let value_index;
    let text_index;
    let wrong_type_index;
    let blob = {
        let mut creator = v8::Isolate::snapshot_creator(None, None);
        {
            v8::scope!(let scope, &mut creator);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            let value_data: v8::Local<v8::Data> = v8::Integer::new(scope, 5).into();
            value_index = scope.add_context_data(context, value_data);
            let text_data: v8::Local<v8::Data> = v8::String::new(scope, "ctx-data").unwrap().into();
            text_index = scope.add_context_data(context, text_data);
            let wrong_data: v8::Local<v8::Data> = v8::Integer::new(scope, 6).into();
            wrong_type_index = scope.add_context_data(context, wrong_data);
            scope.set_default_context(context);
        }
        creator
            .create_blob(v8::FunctionCodeHandling::Clear)
            .unwrap()
    };

    let params = v8::Isolate::create_params().snapshot_blob(blob);
    let isolate = &mut v8::Isolate::new(params);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let value = scope.get_context_data_from_snapshot_once::<v8::Value>(value_index);
    let int_value = value
        .as_ref()
        .ok()
        .and_then(|v| v.integer_value(scope))
        .unwrap_or(-1);
    let second_kind =
        result_kind(&scope.get_context_data_from_snapshot_once::<v8::Value>(value_index));

    let text_value = scope.get_context_data_from_snapshot_once::<v8::String>(text_index);
    let text = text_value
        .as_ref()
        .map(|v| v.to_rust_string_lossy(scope))
        .unwrap_or_default();

    // Wrongly typed request over a still-filled slot.
    let bad_kind =
        result_kind(&scope.get_context_data_from_snapshot_once::<v8::Private>(wrong_type_index));
    // Is the slot consumed by the failed (bad-type) request? Pinned
    // upstream caveat being characterized: the raw data is fetched from the
    // snapshot before the downcast, so a BadType request consumes the slot;
    // the follow-up correctly typed read observes the consequence.
    let after_bad_kind =
        result_kind(&scope.get_context_data_from_snapshot_once::<v8::Value>(wrong_type_index));

    let actual = Json::obj(vec![
        ("int_value", Json::i(int_value)),
        ("second_read_kind", Json::s(second_kind)),
        ("str_value", Json::s(&text)),
        ("bad_request_kind", Json::s(bad_kind)),
        ("after_bad_request_kind", Json::s(after_bad_kind)),
    ]);
    let expected = Json::obj(vec![
        ("int_value", Json::i(5)),
        ("second_read_kind", Json::s("NoData")),
        ("str_value", Json::s("ctx-data")),
        ("bad_request_kind", Json::s("BadType")),
        ("after_bad_request_kind", Json::s("NoData")),
    ]);
    vec![expect_eq(
        "snapshot/context_data_once_and_badtype",
        expected,
        actual,
    )]
}

/// `StartupData` predicates on created blobs. Note the pinned upstream
/// caveat (characterized by the `mode=invalid-startup-data-fatal`
/// subprocess mode): `StartupData::is_valid()` on data shorter than the
/// snapshot version header does not return `false` — it trips a V8
/// `CHECK` and aborts the process, so it is not safely observable there.
fn startup_data_predicates() -> Vec<CheckOutcome> {
    let blob = make_blob(false);

    let actual = Json::obj(vec![
        ("blob_len_gt_zero", Json::b(!blob.is_empty())),
        ("blob_is_valid", Json::b(blob.is_valid())),
    ]);
    let expected = Json::obj(vec![
        ("blob_len_gt_zero", Json::b(true)),
        ("blob_is_valid", Json::b(true)),
    ]);
    vec![expect_eq(
        "snapshot/startup_data_predicates",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// global handle checks
// ---------------------------------------------------------------------------

/// `Global::into_raw` / `Global::from_raw` round trip preserves the object
/// identity (handle equality is object identity: `v8__Data__EQ` compares
/// the underlying tagged object pointers, so a clone in a *different*
/// global cell still compares equal); a distinct object stays unequal.
fn global_into_raw_roundtrip() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let (keeper, raw, distinct) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let original = v8::Global::new(scope, v8::Object::new(scope));
        let keeper = original.clone(); // new global cell, same object
        let distinct = v8::Global::new(scope, v8::Object::new(scope));
        let raw = original.into_raw();
        (keeper, raw, distinct)
    };

    // Re-adopt the raw cell outside any scope (from_raw takes `&mut Isolate`).
    // SAFETY: same isolate, immediately re-adopted, documented usage.
    let restored = unsafe { v8::Global::from_raw(isolate, raw) };

    let roundtrip_equal = restored == keeper;
    let distinct_unequal = restored != distinct;

    let actual = Json::obj(vec![
        ("roundtrip_equal", Json::b(roundtrip_equal)),
        ("distinct_unequal", Json::b(distinct_unequal)),
    ]);
    let expected = Json::obj(vec![
        ("roundtrip_equal", Json::b(true)),
        ("distinct_unequal", Json::b(true)),
    ]);
    vec![expect_eq(
        "handle/global_into_raw_roundtrip",
        expected,
        actual,
    )]
}

/// Handle equality across isolates: `PartialEq` returns `false` for
/// distinct live isolates even when both hold fresh plain objects, while a
/// handle still equals locals reopened from it in its own isolate.
///
/// Characterized lifecycle constraint (pinned source `isolate.rs`,
/// `OwnedIsolate::new` -> `isolate.enter()`, `Drop` -> `exit()`): each
/// created isolate becomes V8's current isolate on the thread, and only
/// the most recently created still-alive isolate may allocate local
/// handles (run scripts, create contexts, objects). Using an older isolate
/// while a newer one is alive aborts inside V8's `HandleScope::CreateHandle`
/// once its spare handle block is exhausted. Cross-isolate `Global`
/// equality is safe in between because it early-returns `false` without
/// touching either isolate (pinned source `handle.rs`, `PartialEq for
/// Global`). The oracle therefore uses isolates strictly sequentially.
fn global_eq_cross_isolate() -> Vec<CheckOutcome> {
    let isolate_a = &mut v8::Isolate::new(Default::default());
    let global_a = {
        v8::scope!(let scope, isolate_a);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        v8::Global::new(scope, v8::Object::new(scope))
    };

    // The cross-isolate comparison must run while both isolates are alive,
    // so it sits between the two isolate lifetimes.
    let cross_isolate_equal = {
        let isolate_b = &mut v8::Isolate::new(Default::default());
        let global_b = {
            v8::scope!(let scope, isolate_b);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            v8::Global::new(scope, v8::Object::new(scope))
        };
        let cross = global_a == global_b;
        drop(global_b);
        cross
    }; // isolate_b dropped here: isolate_a is current again

    let own_local_equal = {
        v8::scope!(let scope, isolate_a);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let reopened = v8::Local::new(scope, &global_a);
        global_a == reopened
    };

    let actual = Json::obj(vec![
        ("cross_isolate_equal", Json::b(cross_isolate_equal)),
        ("own_local_equal", Json::b(own_local_equal)),
    ]);
    let expected = Json::obj(vec![
        ("cross_isolate_equal", Json::b(false)),
        ("own_local_equal", Json::b(true)),
    ]);
    vec![expect_eq(
        "handle/global_eq_cross_isolate",
        expected,
        actual,
    )]
}

/// A `Global` may be dropped after its host isolate was disposed: the
/// crate's `Drop for Global` detects the disposed isolate and takes the
/// documented no-op path. No panic, no access afterwards.
fn global_drop_after_isolate_dispose() -> Vec<CheckOutcome> {
    let global = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        v8::Global::new(scope, v8::Object::new(scope))
    }; // isolate disposed here, global outlives it
    drop(global); // must be a silent no-op, not a panic
    vec![pass(
        "handle/global_drop_after_isolate_dispose",
        Json::b(true),
    )]
}

// ---------------------------------------------------------------------------
// weak handle checks
// ---------------------------------------------------------------------------

/// `Weak::with_finalizer` fires after the object loses its last strong
/// reference and forced major GCs run (`low_memory_notification`, twice,
/// covering the two-pass weak processing). While a strong `Global` keeps
/// the object alive the weak stays usable.
fn weak_finalizer_fires_after_forced_gc() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let events: EventLog = Rc::new(RefCell::new(Vec::new()));

    let strong = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        v8::Global::new(scope, v8::Object::new(scope))
    };
    let weak_events = Rc::clone(&events);
    let weak = v8::Weak::with_finalizer(
        isolate,
        &strong,
        Box::new(move |_isolate| {
            weak_events.borrow_mut().push("finalizer");
        }),
    );

    let alive_before_gc = !weak.is_empty();
    drop(strong);
    isolate.low_memory_notification();
    isolate.low_memory_notification();

    let collected = weak.is_empty();
    let resurrect_is_none = weak.to_global(isolate).is_none();
    let fired = events.borrow().contains(&"finalizer");
    drop(weak);

    let actual = Json::obj(vec![
        ("alive_before_gc", Json::b(alive_before_gc)),
        ("collected_after_gc", Json::b(collected)),
        ("resurrect_is_none", Json::b(resurrect_is_none)),
        ("finalizer_fired", Json::b(fired)),
    ]);
    let expected = Json::obj(vec![
        ("alive_before_gc", Json::b(true)),
        ("collected_after_gc", Json::b(true)),
        ("resurrect_is_none", Json::b(true)),
        ("finalizer_fired", Json::b(true)),
    ]);
    vec![expect_eq(
        "handle/weak_finalizer_fires_after_forced_gc",
        expected,
        actual,
    )]
}

/// `Weak::with_guaranteed_finalizer`: not necessarily invoked by a forced
/// GC, but guaranteed to run before the isolate is dropped. The isolate
/// being dropped is the observable point where the callback has definitely
/// run.
fn weak_guaranteed_finalizer_runs_by_teardown() -> Vec<CheckOutcome> {
    let events: EventLog = Rc::new(RefCell::new(Vec::new()));
    let weak = {
        let isolate = &mut v8::Isolate::new(Default::default());
        let strong = {
            v8::scope!(let scope, isolate);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            v8::Global::new(scope, v8::Object::new(scope))
        };
        let weak_events = Rc::clone(&events);
        let weak = v8::Weak::with_guaranteed_finalizer(
            isolate,
            &strong,
            Box::new(move || {
                weak_events.borrow_mut().push("guaranteed");
            }),
        );
        drop(strong); // last strong reference gone; the weak keeps its cell
        weak
        // isolate dropped at block end; the guaranteed finalizer must have
        // run by the time the block returns.
    };
    let fired_after_teardown = events.borrow().contains(&"guaranteed");
    drop(weak);
    vec![expect_eq(
        "handle/weak_guaranteed_finalizer_runs_by_teardown",
        Json::b(true),
        Json::b(fired_after_teardown),
    )]
}

/// Dropping a `Weak` whose object is still strongly reachable resets the
/// underlying weak cell and cancels the pending finalizer: the callback
/// never fires, even after forced GCs and isolate teardown.
fn weak_drop_cancels_finalizer() -> Vec<CheckOutcome> {
    let events: EventLog = Rc::new(RefCell::new(Vec::new()));
    {
        let isolate = &mut v8::Isolate::new(Default::default());
        let strong = {
            v8::scope!(let scope, isolate);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            v8::Global::new(scope, v8::Object::new(scope))
        };
        let weak_events = Rc::clone(&events);
        let weak = v8::Weak::with_finalizer(
            isolate,
            &strong,
            Box::new(move |_isolate| {
                weak_events.borrow_mut().push("cancelled-should-not-run");
            }),
        );
        drop(weak); // cancels the finalizer; the object is still strongly held
        isolate.low_memory_notification();
        isolate.low_memory_notification();
        assert!(events.borrow().is_empty(), "cancelled finalizer fired");
        drop(strong);
    }
    let still_empty = events.borrow().is_empty();
    vec![expect_eq(
        "handle/weak_drop_cancels_finalizer",
        Json::b(true),
        Json::b(still_empty),
    )]
}

/// `Weak` equality and clone semantics: clones point at the same object
/// (and carry no finalizer), an empty weak equals another empty weak, a
/// live weak equals its strong `Global`, and after collection the weak no
/// longer equals anything.
fn weak_equality_and_clone() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let strong = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        v8::Global::new(scope, v8::Object::new(scope))
    };
    let weak = v8::Weak::new(isolate, &strong);
    let weak_clone = weak.clone(); // documented: clone carries no finalizer

    let weak_equals_clone = weak == weak_clone;
    let weak_equals_global = weak == strong;
    let to_local_some = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        weak.to_local(scope).is_some()
    };

    drop(strong);
    isolate.low_memory_notification();
    isolate.low_memory_notification();

    let collected_empty = weak.is_empty();
    let collected_clone_empty = weak_clone.is_empty();
    let collected_to_local_none = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        weak.to_local(scope).is_none()
    };

    let empty1 = v8::Weak::<v8::Object>::empty(isolate);
    let empty2 = v8::Weak::<v8::Object>::empty(isolate);
    let empty_equals_empty = empty1 == empty2;

    let actual = Json::obj(vec![
        ("weak_equals_clone", Json::b(weak_equals_clone)),
        ("weak_equals_global", Json::b(weak_equals_global)),
        ("to_local_some", Json::b(to_local_some)),
        ("collected_empty", Json::b(collected_empty)),
        ("collected_to_local_none", Json::b(collected_to_local_none)),
        ("collected_clone_empty", Json::b(collected_clone_empty)),
        ("empty_equals_empty", Json::b(empty_equals_empty)),
    ]);
    let expected = Json::obj(vec![
        ("weak_equals_clone", Json::b(true)),
        ("weak_equals_global", Json::b(true)),
        ("to_local_some", Json::b(true)),
        ("collected_empty", Json::b(true)),
        ("collected_to_local_none", Json::b(true)),
        ("collected_clone_empty", Json::b(true)),
        ("empty_equals_empty", Json::b(true)),
    ]);
    vec![expect_eq(
        "handle/weak_equality_and_clone",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// termination checks (same-thread, safely observable)
// ---------------------------------------------------------------------------

/// Native callback (must be a non-capturing `fn`: `Function::builder`
/// requires `UnitType`/`Copy` callbacks in this crate version). It records
/// the termination flag on JS globals because Rust captures are not
/// available: `__termFlagBefore` / `__termFlagAfter`, then requests
/// termination mid-execution.
fn cb_request_terminate(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    let before = scope.is_execution_terminating();
    let requested = scope.terminate_execution();
    assert!(requested, "terminate_execution rejected inside JS");
    let after = scope.is_execution_terminating();

    let context = scope.get_current_context();
    let global = context.global(scope);
    let before_key = v8::String::new(scope, "__termFlagBefore").unwrap();
    global
        .set(
            scope,
            before_key.into(),
            v8::Boolean::new(scope, before).into(),
        )
        .unwrap();
    let after_key = v8::String::new(scope, "__termFlagAfter").unwrap();
    global
        .set(
            scope,
            after_key.into(),
            v8::Boolean::new(scope, after).into(),
        )
        .unwrap();
}

/// Requests termination from inside a running native callback (same
/// thread), pins the flag-visibility semantics, verifies the interrupted
/// script reports through TryCatch, and restores the isolate with
/// `cancel_terminate_execution`.
///
/// Characterized delivery semantics: `terminate_execution()` only *enqueues*
/// an interrupt; `is_execution_terminating()` flips to `true` only once the
/// interrupt is *delivered* at the next interrupt check (here: the loop
/// back-edge), not synchronously at the request site.
fn terminate_request_and_cancel_during_js() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let handle = isolate.thread_safe_handle();

    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let request = v8::Function::builder(cb_request_terminate)
        .build(scope)
        .unwrap();
    context
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "__requestTerminate").unwrap().into(),
            request.into(),
        )
        .unwrap();

    let (ran_ok, has_caught, can_continue, has_terminated) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        // Characterized delivery semantics: the termination request is
        // delivered at the next interrupt check (loop back-edge), not
        // synchronously inside the native callback, so the script must
        // keep running for the request to land.
        let ran_ok = eval(tc, "__requestTerminate(); while (true) { }").is_some();
        (
            ran_ok,
            tc.has_caught(),
            tc.can_continue(),
            tc.has_terminated(),
        )
    };
    assert!(!ran_ok, "terminated script must not complete");
    assert!(has_caught, "termination must surface in the TryCatch");
    assert!(!can_continue, "termination is not recoverable in-trycatch");

    // Characterized: once the termination exception has fully unwound to
    // the embedder, V8 has already cleared the terminate flag; the durable
    // post-abort observable is the TryCatch's terminated state.
    let flag_after_abort = scope.is_execution_terminating();
    let cancelled = handle.cancel_terminate_execution();
    assert!(cancelled, "cancel_terminate_execution rejected");
    let idle_again = scope.is_execution_terminating();
    let flag_before = eval_text(scope, "String(__termFlagBefore)").unwrap_or_default();
    let flag_after = eval_text(scope, "String(__termFlagAfter)").unwrap_or_default();
    let next = eval_text(scope, "String(7 * 6)").unwrap_or_default();
    assert_eq!(next, "42", "isolate must be reusable after cancellation");

    let actual = Json::obj(vec![
        ("flag_before_request", Json::s(&flag_before)),
        ("flag_after_request", Json::s(&flag_after)),
        ("flag_after_abort", Json::b(flag_after_abort)),
        ("ran_ok", Json::b(ran_ok)),
        ("has_caught", Json::b(has_caught)),
        ("can_continue", Json::b(can_continue)),
        ("has_terminated", Json::b(has_terminated)),
        ("cancel_ok", Json::b(cancelled)),
        ("idle_again", Json::b(idle_again)),
        ("reused_result", Json::s(&next)),
    ]);
    let expected = Json::obj(vec![
        ("flag_before_request", Json::s("false")),
        ("flag_after_request", Json::s("false")),
        ("flag_after_abort", Json::b(false)),
        ("ran_ok", Json::b(false)),
        ("has_caught", Json::b(true)),
        ("can_continue", Json::b(false)),
        ("has_terminated", Json::b(true)),
        ("cancel_ok", Json::b(true)),
        ("idle_again", Json::b(false)),
        ("reused_result", Json::s("42")),
    ]);
    vec![expect_eq(
        "terminate/request_and_cancel_during_js",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// registry + report
// ---------------------------------------------------------------------------

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    // snapshot creation and startup data
    create_blob_policies,
    startup_data_predicates,
    // snapshot consumption
    default_context_create_params_roundtrip,
    chained_snapshot_roundtrip,
    add_context_from_snapshot,
    isolate_data_once,
    context_data_once_and_badtype,
    // global handle semantics
    global_into_raw_roundtrip,
    global_eq_cross_isolate,
    global_drop_after_isolate_dispose,
    // weak handle semantics
    weak_finalizer_fires_after_forced_gc,
    weak_guaranteed_finalizer_runs_by_teardown,
    weak_drop_cancels_finalizer,
    weak_equality_and_clone,
    // thread-safe isolate handle
    terminate_request_and_cancel_during_js,
];

fn run_report() -> ExitCode {
    let mut outcomes = Vec::new();
    for check in CHECKS {
        outcomes.extend(check());
    }
    let total = outcomes.len();
    let mut passed = 0usize;
    let mut text = String::new();
    for outcome in &outcomes {
        if outcome.passed() {
            passed += 1;
        }
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    let failed = total - passed;
    text.push_str(&oracle::report::summary_line(total, passed, failed));
    text.push('\n');

    let stdout = std::io::stdout();
    let mut lock = stdout.lock();
    let _ = lock.write_all(text.as_bytes());
    let _ = lock.flush();
    if failed == 0 {
        ExitCode::SUCCESS
    } else {
        ExitCode::FAILURE
    }
}

// ---------------------------------------------------------------------------
// dedicated subprocess modes
// ---------------------------------------------------------------------------

/// Panics with the crate's documented message: a snapshot-creator isolate
/// must not be dropped without `create_blob` (pinned source
/// `isolate.rs`, `impl Drop for OwnedIsolate`).
fn mode_drop_creator_without_blob() -> ExitCode {
    let mut creator = v8::Isolate::snapshot_creator(None, None);
    {
        v8::scope!(let scope, &mut creator);
        let context = v8::Context::new(scope, Default::default());
        let _entered = &mut v8::ContextScope::new(scope, context);
        // No create_blob: dropping the creator isolate must panic.
    }
    drop(creator);
    println!("{{\"mode\":\"drop-creator-without-blob\",\"panicked\":false}}");
    ExitCode::SUCCESS
}

/// `PartialEq` on a `Global` whose host isolate has been disposed: the
/// crate panics with "attempt to access Handle hosted by disposed
/// Isolate" (pinned source `isolate.rs`, `IsolateLiveness::
/// assert_access_allowed`). Subprocess-isolated because it panics.
fn mode_global_eq_after_dispose() -> ExitCode {
    let (global, other) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let global = v8::Global::new(scope, v8::Object::new(scope));
        let other = global.clone();
        (global, other)
    }; // isolate disposed here
    let equal = global == other; // must panic
    println!("{{\"mode\":\"global-eq-after-dispose\",\"panicked\":false,\"equal\":{equal}}}");
    ExitCode::SUCCESS
}

/// Cross-thread termination through a cloned `IsolateHandle`: a foreign
/// thread requests termination while a tight JS loop runs; the loop must
/// report via TryCatch and the isolate must be reusable after
/// `cancel_terminate_execution`. Prints one JSON line; deterministic
/// because the interrupt flag persists until delivered.
fn mode_terminate_loop_from_other_thread() -> ExitCode {
    let isolate = &mut v8::Isolate::new(Default::default());
    let handle = isolate.thread_safe_handle();
    let terminator_handle = handle.clone();

    let terminator = std::thread::spawn(move || {
        // The flag persists until delivered (or cancelled), so this sleep
        // only bounds the total time; the outcome is identical either way.
        std::thread::sleep(std::time::Duration::from_millis(100));
        terminator_handle.terminate_execution()
    });

    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let (requested, ran_ok, has_caught, can_continue) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let source = v8::String::new(tc, "while (true) { }").unwrap();
        let script = v8::Script::compile(tc, source, None).unwrap();
        let ran_ok = script.run(tc).is_some();
        (
            terminator.join().expect("terminator thread"),
            ran_ok,
            tc.has_caught(),
            tc.can_continue(),
        )
    };

    let cancelled = handle.cancel_terminate_execution();
    let reused = eval_text(scope, "String(40 + 2)").unwrap_or_default();
    assert!(requested && !ran_ok && has_caught && !can_continue && cancelled && reused == "42");

    println!(
        "{{\"mode\":\"terminate-loop\",\"requested\":{requested},\"ran_ok\":{ran_ok},\
         \"has_caught\":{has_caught},\"can_continue\":{can_continue},\
         \"cancel_ok\":{cancelled},\"reused\":\"{reused}\"}}"
    );
    ExitCode::SUCCESS
}

/// Upstream caveat, characterized out-of-process on purpose:
/// `StartupData::is_valid()` on an embedder-provided blob shorter than the
/// snapshot version header does NOT return `false`; V8 trips a `CHECK`
/// (`Snapshot::VersionIsValid`) and aborts the process. The negative test
/// asserts the non-zero exit and the "Check failed" report.
fn mode_invalid_startup_data_fatal() -> ExitCode {
    let empty = v8::StartupData::from(Vec::new());
    let valid = empty.is_valid(); // fatal CHECK failure, never reached
    println!("{{\"mode\":\"invalid-startup-data-fatal\",\"is_valid\":{valid}}}");
    ExitCode::SUCCESS
}

fn main() -> ExitCode {
    oracle::ensure_v8();
    match std::env::args().nth(1).as_deref() {
        None => run_report(),
        Some("mode=drop-creator-without-blob") => mode_drop_creator_without_blob(),
        Some("mode=global-eq-after-dispose") => mode_global_eq_after_dispose(),
        Some("mode=terminate-loop") => mode_terminate_loop_from_other_thread(),
        Some("mode=invalid-startup-data-fatal") => mode_invalid_startup_data_fatal(),
        Some(other) => {
            eprintln!("unknown mode: {other}");
            ExitCode::FAILURE
        }
    }
}
