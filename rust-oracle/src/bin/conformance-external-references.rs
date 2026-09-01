//! `ExternalReference` and `CreateParams::external_references` conformance.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::borrow::Cow;
use std::ffi::c_void;
use v8::MapFnTo;

fn callback(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let pointer = args.data().cast::<v8::External>().value() as usize;
    rv.set(v8::BigInt::new_from_u64(scope, pointer as u64).into());
}

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn refs(pointer: usize, terminated: bool) -> Cow<'static, [v8::ExternalReference]> {
    let mut refs = vec![
        v8::ExternalReference {
            function: callback.map_fn_to(),
        },
        v8::ExternalReference {
            pointer: pointer as *mut c_void,
        },
    ];
    if terminated {
        refs.push(v8::ExternalReference {
            pointer: std::ptr::null_mut(),
        });
    }
    refs.into()
}

fn external_reference_value_semantics() -> Vec<CheckOutcome> {
    let null_a = v8::ExternalReference {
        pointer: std::ptr::null_mut(),
    };
    let null_b = v8::ExternalReference {
        pointer: std::ptr::null_mut(),
    };
    let one = v8::ExternalReference {
        pointer: std::ptr::dangling_mut::<c_void>(),
    };
    let one_copy = one;
    let two = v8::ExternalReference {
        pointer: 2_usize as *mut c_void,
    };
    let function_a = v8::ExternalReference {
        function: callback.map_fn_to(),
    };
    let function_b = function_a;
    vec![pass(
        "external-references/value_semantics",
        Json::obj(vec![
            (
                "size",
                Json::i(std::mem::size_of::<v8::ExternalReference>() as i64),
            ),
            (
                "align",
                Json::i(std::mem::align_of::<v8::ExternalReference>() as i64),
            ),
            ("null_equal", Json::b(null_a == null_b)),
            ("copy_equal", Json::b(one == one_copy)),
            ("different_pointer_unequal", Json::b(one != two)),
            ("function_copy_equal", Json::b(function_a == function_b)),
            ("null_debug", Json::s(&format!("{null_a:?}"))),
        ]),
    )]
}

fn empty_external_references() -> Vec<CheckOutcome> {
    let ordinary_result = {
        let params = v8::CreateParams::default()
            .external_references(Cow::Borrowed(&[] as &[v8::ExternalReference]));
        let isolate = &mut v8::Isolate::new(params);
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval(scope, "40 + 2").integer_value(scope).unwrap()
    };

    let blob = {
        let mut creator = v8::Isolate::snapshot_creator(
            Some(Cow::Borrowed(&[] as &[v8::ExternalReference])),
            None,
        );
        {
            v8::scope!(let scope, &mut creator);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            eval(scope, "globalThis.snapshotted = 41");
            scope.set_default_context(context);
        }
        creator
            .create_blob(v8::FunctionCodeHandling::Clear)
            .unwrap()
    };
    let blob_valid = blob.is_valid();
    let blob_nonempty = !blob.is_empty();

    let no_table_result = {
        let isolate =
            &mut v8::Isolate::new(v8::CreateParams::default().snapshot_blob(blob.clone()));
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval(scope, "snapshotted + 1").integer_value(scope).unwrap()
    };
    let empty_table_result = {
        let params = v8::CreateParams::default()
            .snapshot_blob(blob)
            .external_references(Vec::<v8::ExternalReference>::new().into());
        let isolate = &mut v8::Isolate::new(params);
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        eval(scope, "snapshotted + 2").integer_value(scope).unwrap()
    };

    vec![pass(
        "external-references/empty_table",
        Json::obj(vec![
            ("ordinary_isolate_result", Json::i(ordinary_result)),
            ("blob_nonempty", Json::b(blob_nonempty)),
            ("blob_valid", Json::b(blob_valid)),
            ("reuse_without_table", Json::i(no_table_result)),
            ("reuse_with_empty_table", Json::i(empty_table_result)),
        ]),
    )]
}

fn make_external_blob() -> v8::StartupData {
    let mut creator = v8::Isolate::snapshot_creator(Some(refs(1, false)), None);
    {
        v8::scope!(let scope, &mut creator);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let data = v8::External::new(scope, std::ptr::dangling_mut::<c_void>());
        let template = v8::FunctionTemplate::builder(callback)
            .data(data.into())
            .build(scope);
        let function = template.get_function(scope).unwrap();
        let name = v8::String::new(scope, "externalValue").unwrap();
        context
            .global(scope)
            .set(scope, name.into(), function.into())
            .unwrap();
        scope.set_default_context(context);
    }
    creator.create_blob(v8::FunctionCodeHandling::Keep).unwrap()
}

fn consume_external_blob(blob: v8::StartupData, pointer: usize, terminated: bool) -> String {
    let params = v8::CreateParams::default()
        .snapshot_blob(blob)
        .external_references(refs(pointer, terminated));
    let isolate = &mut v8::Isolate::new(params);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval(scope, "externalValue()").to_rust_string_lossy(scope)
}

fn snapshot_remap_and_reuse() -> Vec<CheckOutcome> {
    let blob = make_external_blob();
    let blob_len = blob.len();
    let blob_valid = blob.is_valid();
    let first = consume_external_blob(blob.clone(), 2, false);
    let second = consume_external_blob(blob.clone(), 3, true);
    let third = consume_external_blob(blob, 4, false);
    vec![pass(
        "external-references/snapshot_remap_and_reuse",
        Json::obj(vec![
            ("blob_nonempty", Json::b(blob_len != 0)),
            ("blob_valid", Json::b(blob_valid)),
            ("producer_dropped", Json::b(true)),
            ("auto_terminated_table_result", Json::s(&first)),
            ("explicitly_terminated_table_result", Json::s(&second)),
            ("third_reuse_result", Json::s(&third)),
        ]),
    )]
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut checks = external_reference_value_semantics();
    checks.extend(empty_external_references());
    checks.extend(snapshot_remap_and_reuse());
    let passed = checks.iter().filter(|check| check.passed()).count();
    for check in &checks {
        println!("{}", check.to_line());
    }
    println!(
        "{}",
        summary_line(checks.len(), passed, checks.len() - passed)
    );
    if passed == checks.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
