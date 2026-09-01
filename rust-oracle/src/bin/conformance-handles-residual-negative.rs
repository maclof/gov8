//! Subprocess-only contract violations for residual handle types.

fn eternal_double_set() {
    let eternal = v8::Eternal::<v8::String>::empty();
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    eternal.set(scope, v8::String::new(scope, "first").unwrap());
    eternal.set(scope, v8::String::new(scope, "second").unwrap());
    println!(
        "{}",
        eternal.get(scope).unwrap().to_rust_string_lossy(scope)
    );
}

fn eternal_after_isolate(mode: &str) {
    let eternal = v8::Eternal::<v8::String>::empty();
    {
        let mut isolate = v8::Isolate::new(Default::default());
        v8::scope!(let scope, &mut isolate);
        eternal.set(scope, v8::String::new(scope, "former-isolate").unwrap());
    }
    match mode {
        "eternal-standalone" => {
            println!("empty-before-clear={}", eternal.is_empty());
            eternal.clear();
            println!("empty-after-clear={}", eternal.is_empty());
        }
        "eternal-wrong-isolate" => {
            let mut isolate = v8::Isolate::new(Default::default());
            v8::scope!(let scope, &mut isolate);
            println!(
                "get={}",
                eternal
                    .get(scope)
                    .map(|value| value.to_rust_string_lossy(scope))
                    .unwrap_or_else(|| "none".to_owned())
            );
        }
        _ => unreachable!(),
    }
}

fn make_traced() -> v8::TracedReference<v8::String> {
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let value = v8::String::new(scope, "former-isolate").unwrap();
    v8::TracedReference::new(scope, value)
}

fn traced_after_isolate_drop() {
    let traced = make_traced();
    drop(traced);
    println!("dropped");
}

fn traced_wrong_isolate() {
    let traced = make_traced();
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    println!(
        "get={}",
        traced
            .get(scope)
            .map(|value| value.to_rust_string_lossy(scope))
            .unwrap_or_else(|| "none".to_owned())
    );
}

fn main() {
    oracle::ensure_v8();
    match std::env::args().nth(1).as_deref() {
        Some("eternal-double-set") => eternal_double_set(),
        Some(mode @ ("eternal-standalone" | "eternal-wrong-isolate")) => {
            eternal_after_isolate(mode)
        }
        Some("traced-drop-after-isolate") => traced_after_isolate_drop(),
        Some("traced-wrong-isolate") => traced_wrong_isolate(),
        _ => std::process::exit(2),
    }
}
