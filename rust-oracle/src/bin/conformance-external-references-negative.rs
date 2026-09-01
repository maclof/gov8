//! External-reference snapshot mismatches, invoked only by subprocess tests.

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

fn refs(pointer: usize) -> Cow<'static, [v8::ExternalReference]> {
    vec![
        v8::ExternalReference {
            function: callback.map_fn_to(),
        },
        v8::ExternalReference {
            pointer: pointer as *mut c_void,
        },
    ]
    .into()
}

fn make_blob() -> v8::StartupData {
    let mut creator = v8::Isolate::snapshot_creator(Some(refs(1)), None);
    {
        v8::scope!(let scope, &mut creator);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let data = v8::External::new(scope, std::ptr::dangling_mut::<c_void>());
        let template = v8::FunctionTemplate::builder(callback)
            .data(data.into())
            .build(scope);
        let function = template.get_function(scope).unwrap();
        context
            .global(scope)
            .set(
                scope,
                v8::String::new(scope, "f").unwrap().into(),
                function.into(),
            )
            .unwrap();
        scope.set_default_context(context);
    }
    creator.create_blob(v8::FunctionCodeHandling::Keep).unwrap()
}

fn consume(params: v8::CreateParams) {
    let isolate = &mut v8::Isolate::new(params);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let source = v8::String::new(scope, "f()").unwrap();
    let value = v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap();
    println!("{}", value.to_rust_string_lossy(scope));
}

fn main() {
    oracle::ensure_v8();
    let mode = std::env::args().nth(1);
    let blob = make_blob();
    match mode.as_deref() {
        Some("missing-table") => {
            consume(v8::CreateParams::default().snapshot_blob(blob));
        }
        Some("short-table") => {
            let short: Cow<'static, [v8::ExternalReference]> = vec![v8::ExternalReference {
                function: callback.map_fn_to(),
            }]
            .into();
            consume(
                v8::CreateParams::default()
                    .snapshot_blob(blob)
                    .external_references(short),
            );
        }
        mode => panic!("unknown external-reference negative mode: {mode:?}"),
    }
}
