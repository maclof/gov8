//! Subprocess-only probes for unchecked `PrimitiveArray` preconditions.
//!
//! The Rust API accepts `usize` indices and lengths but forwards them to V8 as
//! `int`, and `set`/`get` do not prevalidate bounds. These modes must only be
//! launched by a parent process that inspects their native termination status.

use std::io::Write as _;

fn checkpoint(text: &str) {
    let mut stdout = std::io::stdout().lock();
    writeln!(stdout, "{text}").unwrap();
    stdout.flush().unwrap();
}

fn in_one_isolate(mode: &str) {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    match mode {
        "get-empty" => {
            let array = v8::PrimitiveArray::new(scope, 0);
            checkpoint("created length=0");
            let _ = array.get(scope, 0);
        }
        "get-at-count" => {
            let array = v8::PrimitiveArray::new(scope, 1);
            checkpoint("created length=1");
            let _ = array.get(scope, array.length());
        }
        "set-at-count" => {
            let array = v8::PrimitiveArray::new(scope, 1);
            checkpoint("created length=1");
            array.set(scope, array.length(), v8::undefined(scope));
        }
        "length-overflow" => {
            let requested = i32::MAX as usize + 1;
            checkpoint(&format!("requested={requested}"));
            let _ = v8::PrimitiveArray::new(scope, requested);
        }
        "length-usize-max" => {
            checkpoint(&format!("requested={}", usize::MAX));
            let _ = v8::PrimitiveArray::new(scope, usize::MAX);
        }
        other => panic!("unknown one-isolate mode: {other}"),
    }
    checkpoint("unexpectedly survived");
}

fn cross_isolate(mode: &str) {
    let first = &mut v8::Isolate::new(Default::default());
    v8::scope!(let first_scope, first);
    let first_context = v8::Context::new(first_scope, Default::default());
    let first_scope = &mut v8::ContextScope::new(first_scope, first_context);
    let array = v8::PrimitiveArray::new(first_scope, 1);
    array.set(
        first_scope,
        0,
        v8::String::new(first_scope, "first").unwrap().into(),
    );
    checkpoint("first array ready");

    let second = &mut v8::Isolate::new(Default::default());
    v8::scope!(let second_scope, second);
    let second_context = v8::Context::new(second_scope, Default::default());
    let second_scope = &mut v8::ContextScope::new(second_scope, second_context);
    checkpoint("second isolate ready");

    match mode {
        "cross-isolate-get" => {
            let value = array.get(second_scope, 0);
            checkpoint(&format!("get={}", value.to_rust_string_lossy(second_scope)));
        }
        "cross-isolate-set" => {
            let item = v8::String::new(second_scope, "second").unwrap();
            array.set(second_scope, 0, item.into());
            let value = array.get(second_scope, 0);
            checkpoint(&format!(
                "get_after_set={}",
                value.to_rust_string_lossy(second_scope)
            ));
        }
        other => panic!("unknown cross-isolate mode: {other}"),
    }
    checkpoint("operation survived");
}

fn main() {
    oracle::ensure_v8();
    let mode = std::env::args().nth(1).expect("negative mode");
    if mode.starts_with("cross-isolate-") {
        cross_isolate(&mode);
    } else {
        in_one_isolate(&mode);
    }
}
