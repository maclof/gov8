//! Subprocess probes for exception construction/message access without an
//! entered Context. Modes are kept outside the deterministic fixture until
//! their process boundary is known.

fn main() {
    oracle::ensure_v8();
    let mode = std::env::args().nth(1).expect("mode");
    let mut isolate = v8::Isolate::new(Default::default());
    v8::scope!(let scope, &mut isolate);
    let undefined: v8::Local<v8::Value> = v8::undefined(scope).into();
    // SAFETY: deliberate negative probe. The public type state normally makes
    // these APIs uncallable without a ContextScope; this bypass asks what the
    // native boundary does if an embedder defeats that compile-time guard.
    let scope: &mut v8::PinScope<'_, '_> = unsafe { std::mem::transmute(scope) };
    match mode.as_str() {
        "constructor" => {
            let text = v8::String::new(scope, "boundary").unwrap();
            let error = v8::Exception::type_error(scope, text);
            println!(
                "native={} object={}",
                error.is_native_error(),
                error.is_object()
            );
        }
        "message" => {
            let message = v8::Exception::create_message(scope, undefined);
            println!("{}", message.get(scope).to_rust_string_lossy(scope));
        }
        "positions" => {
            let message = v8::Exception::create_message(scope, undefined);
            println!(
                "line={:?} source={}",
                message.get_line_number(scope),
                message.get_source_line(scope).is_some()
            );
        }
        _ => std::process::exit(2),
    }
}
