//! Residual Context snapshot-option and embedder-data conformance.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::ffi::c_void;
use std::rc::Rc;

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn eval_text(scope: &v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source).to_rust_string_lossy(scope)
}

fn queue_ptr(queue: &v8::MicrotaskQueue) -> *mut v8::MicrotaskQueue {
    std::ptr::from_ref(queue).cast_mut()
}

fn context_snapshot() -> (v8::StartupData, usize) {
    let mut creator = v8::Isolate::snapshot_creator(None, None);
    let index;
    {
        v8::scope!(let scope, &mut creator);
        let default_context = v8::Context::new(scope, Default::default());
        scope.set_default_context(default_context);
        let context = v8::Context::new(scope, Default::default());
        {
            let scope = &mut v8::ContextScope::new(scope, context);
            eval(scope, "globalThis.snapshotMarker = 'added-context'");
        }
        index = scope.add_context(context);
    }
    let blob = creator
        .create_blob(v8::FunctionCodeHandling::Clear)
        .unwrap();
    (blob, index)
}

fn from_snapshot_options() -> Vec<CheckOutcome> {
    let (blob, index) = context_snapshot();
    let isolate = &mut v8::Isolate::new(v8::Isolate::create_params().snapshot_blob(blob));
    v8::scope!(let scope, isolate);
    let queue = v8::MicrotaskQueue::new(scope, v8::MicrotasksPolicy::Explicit);
    let template = v8::ObjectTemplate::new(scope);
    template.set(
        v8::String::new(scope, "ignoredTemplateValue")
            .unwrap()
            .into(),
        v8::Integer::new(scope, 73).into(),
    );
    let first = v8::Context::from_snapshot(
        scope,
        index,
        v8::ContextOptions {
            global_template: Some(template),
            microtask_queue: Some(queue_ptr(queue.as_ref())),
            ..Default::default()
        },
    )
    .unwrap();
    let queue_attached_first = std::ptr::eq(first.get_microtask_queue().unwrap(), queue.as_ref());
    let first_global;
    let marker_first;
    let template_ignored;
    let microtask_before;
    {
        let scope = &mut v8::ContextScope::new(scope, first);
        marker_first = eval_text(scope, "snapshotMarker");
        template_ignored = eval_text(scope, "typeof ignoredTemplateValue");
        eval(
            scope,
            "globalThis.transient = 99; globalThis.microtaskDone = false; \
             Promise.resolve().then(() => microtaskDone = true)",
        );
        microtask_before = eval(scope, "microtaskDone").is_false();
        first_global = first.global(scope);
    }
    queue.perform_checkpoint(scope);
    let microtask_after = {
        let scope = &mut v8::ContextScope::new(scope, first);
        eval(scope, "microtaskDone").is_true()
    };

    let second = v8::Context::from_snapshot(
        scope,
        index,
        v8::ContextOptions {
            global_object: Some(first_global.into()),
            microtask_queue: Some(queue_ptr(queue.as_ref())),
            ..Default::default()
        },
    )
    .unwrap();
    let queue_attached_second = std::ptr::eq(second.get_microtask_queue().unwrap(), queue.as_ref());
    let second_global;
    let marker_second;
    let transient_reset;
    {
        let scope = &mut v8::ContextScope::new(scope, second);
        second_global = second.global(scope);
        marker_second = eval_text(scope, "snapshotMarker");
        transient_reset = eval_text(scope, "typeof transient");
    }

    vec![pass(
        "context-residual/from_snapshot_options",
        Json::obj(vec![
            ("snapshot_index", Json::i(index as i64)),
            ("snapshot_marker_first", Json::s(&marker_first)),
            ("global_template_field_ignored", Json::s(&template_ignored)),
            ("queue_attached_first", Json::b(queue_attached_first)),
            ("microtask_before_checkpoint", Json::b(microtask_before)),
            ("microtask_after_checkpoint", Json::b(microtask_after)),
            (
                "global_proxy_reused",
                Json::b(first_global == second_global),
            ),
            ("snapshot_marker_second", Json::s(&marker_second)),
            ("transient_after_global_reuse", Json::s(&transient_reset)),
            ("queue_attached_second", Json::b(queue_attached_second)),
        ]),
    )]
}

fn from_snapshot_without_blob() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let zero = v8::Context::from_snapshot(scope, 0, Default::default());
    let below_maximum = v8::Context::from_snapshot(scope, usize::MAX - 1, Default::default());
    let maximum = v8::Context::from_snapshot(scope, usize::MAX, Default::default());
    let maximum_is_some = maximum.is_some();
    let (maximum_has_builtins, maximum_executes) = match maximum {
        Some(context) => {
            let scope = &mut v8::ContextScope::new(scope, context);
            (
                eval_text(scope, "typeof Object") == "function",
                eval(scope, "6 * 7").integer_value(scope) == Some(42),
            )
        }
        None => (false, false),
    };
    vec![pass(
        "context-residual/from_snapshot_without_blob",
        Json::obj(vec![
            ("index_zero_is_none", Json::b(zero.is_none())),
            (
                "index_usize_max_minus_one_is_none",
                Json::b(below_maximum.is_none()),
            ),
            ("index_usize_max_is_some", Json::b(maximum_is_some)),
            (
                "index_usize_max_has_builtins",
                Json::b(maximum_has_builtins),
            ),
            ("index_usize_max_executes", Json::b(maximum_executes)),
        ]),
    )]
}

fn embedder_data_growth_and_pointer() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let string: v8::Local<v8::Value> = v8::String::new(scope, "high-slot").unwrap().into();
    context.set_embedder_data(1024, string);
    let string_read = context.get_embedder_data(scope, 1024).unwrap();
    let high_identity = string_read.strict_equals(string);
    let high_text = string_read.to_rust_string_lossy(scope);

    let object: v8::Local<v8::Value> = v8::Object::new(scope).into();
    context.set_embedder_data(1025, object);
    let object_identity = context
        .get_embedder_data(scope, 1025)
        .unwrap()
        .strict_equals(object);
    let high_slot_unchanged = context
        .get_embedder_data(scope, 1024)
        .unwrap()
        .strict_equals(string);

    unsafe { context.set_aligned_pointer_in_embedder_data(8, std::ptr::null_mut()) };
    let null_roundtrip = context.get_aligned_pointer_from_embedder_data(8).is_null();
    let pointer = Box::into_raw(Box::new(0x1234_5678_u64));
    unsafe { context.set_aligned_pointer_in_embedder_data(8, pointer.cast::<c_void>()) };
    let pointer_roundtrip = context
        .get_aligned_pointer_from_embedder_data(8)
        .cast::<u64>()
        == pointer;
    unsafe { context.set_aligned_pointer_in_embedder_data(8, std::ptr::null_mut()) };
    let cleared_pointer = context.get_aligned_pointer_from_embedder_data(8).is_null();
    drop(unsafe { Box::from_raw(pointer) });

    vec![pass(
        "context-residual/embedder_data_growth_and_pointer",
        Json::obj(vec![
            ("high_slot", Json::i(1024)),
            ("high_slot_identity", Json::b(high_identity)),
            ("high_slot_text", Json::s(&high_text)),
            ("adjacent_object_identity", Json::b(object_identity)),
            ("high_slot_unchanged", Json::b(high_slot_unchanged)),
            ("null_pointer_roundtrip", Json::b(null_roundtrip)),
            ("nonnull_pointer_roundtrip", Json::b(pointer_roundtrip)),
            ("cleared_pointer_roundtrip", Json::b(cleared_pointer)),
        ]),
    )]
}

fn cleared_slots_snapshot() -> Vec<CheckOutcome> {
    let mut creator = v8::Isolate::snapshot_creator(None, None);
    let cleared;
    {
        v8::scope!(let scope, &mut creator);
        let context = v8::Context::new(scope, Default::default());
        {
            let scope = &mut v8::ContextScope::new(scope, context);
            context.set_slot(Rc::new(String::from("host-only")));
            cleared = context.get_slot::<String>().is_some();
            context.clear_all_slots();
            eval(scope, "globalThis.afterClear = 42");
        }
        scope.set_default_context(context);
    }
    let blob = creator
        .create_blob(v8::FunctionCodeHandling::Clear)
        .unwrap();
    let valid = blob.is_valid();
    let isolate = &mut v8::Isolate::new(v8::Isolate::create_params().snapshot_blob(blob));
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let restored = eval(scope, "afterClear").integer_value(scope).unwrap();
    vec![pass(
        "context-residual/clear_slots_before_snapshot",
        Json::obj(vec![
            ("slot_present_before_clear", Json::b(cleared)),
            (
                "slot_absent_after_restore",
                Json::b(context.get_slot::<String>().is_none()),
            ),
            ("blob_valid", Json::b(valid)),
            ("snapshot_value", Json::i(restored)),
        ]),
    )]
}

fn negative_probe(mode: &str) {
    oracle::ensure_v8();
    if mode == "uncleared-slot-snapshot" {
        let mut creator = v8::Isolate::snapshot_creator(None, None);
        {
            v8::scope!(let scope, &mut creator);
            let context = v8::Context::new(scope, Default::default());
            context.set_slot(Rc::new(7_u32));
            scope.set_default_context(context);
        }
        let _ = creator.create_blob(v8::FunctionCodeHandling::Clear);
        return;
    }
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    match mode {
        "negative-index" => context.set_embedder_data(-3, v8::Integer::new(scope, 1).into()),
        "too-large" => context.set_embedder_data(1_000_000, v8::Integer::new(scope, 1).into()),
        "unaligned-pointer" => unsafe {
            context.set_aligned_pointer_in_embedder_data(0, std::ptr::dangling_mut::<c_void>())
        },
        "reserved-annex" => {
            context.set_slot(Rc::new(1_u32));
            context.set_embedder_data(-1, v8::Integer::new(scope, 1).into());
            let _ = context.get_slot::<u32>();
        }
        _ => panic!("unknown negative probe {mode}"),
    }
}

fn run() {
    oracle::ensure_v8();
    let checks = [
        from_snapshot_options,
        from_snapshot_without_blob,
        embedder_data_growth_and_pointer,
        cleared_slots_snapshot,
    ];
    let outcomes: Vec<_> = checks.into_iter().flat_map(|check| check()).collect();
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(outcomes.len(), outcomes.len(), 0));
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    match args.as_slice() {
        [_] => run(),
        [_, flag, mode] if flag == "--negative" => negative_probe(mode),
        _ => panic!("usage: conformance-context-residual [--negative MODE]"),
    }
}
