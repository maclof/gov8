//! Heap-snapshot streaming behavior for rusty_v8 152.2.0 / V8
//! 15.2.124.1-rusty.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

const SEM_FAILCRITICALERRORS: u32 = 0x0001;
const SEM_NOGPFAULTERRORBOX: u32 = 0x0002;
const SEM_NOOPENFILEERRORBOX: u32 = 0x8000;

#[link(name = "kernel32")]
unsafe extern "system" {
    #[link_name = "SetErrorMode"]
    fn set_error_mode(mode: u32) -> u32;
}

fn suppress_windows_fatal_dialogs() {
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

fn eval_text(isolate: &mut v8::Isolate, source: &str) -> Option<String> {
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let source = v8::String::new(scope, source)?;
    let script = v8::Script::compile(scope, source, None)?;
    script
        .run(scope)
        .map(|value| value.to_rust_string_lossy(scope))
}

fn install_marker(isolate: &mut v8::Isolate) -> v8::Global<v8::Context> {
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let source = v8::String::new(
        scope,
        "globalThis.gov8HeapSnapshotMarker={label:'gov8-snapshot-marker',values:[1,2,3]}; 'ready'",
    )
    .unwrap();
    let result = v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
        .to_rust_string_lossy(scope);
    assert_eq!(result, "ready");
    v8::Global::new(scope, context)
}

fn full_snapshot(isolate: &mut v8::Isolate) -> (Vec<u8>, Vec<usize>) {
    let mut bytes = Vec::new();
    let mut sizes = Vec::new();
    isolate.take_heap_snapshot(|chunk| {
        sizes.push(chunk.len());
        bytes.extend_from_slice(chunk);
        true
    });
    (bytes, sizes)
}

fn successful_stream() -> CheckOutcome {
    let mut isolate = v8::Isolate::new(Default::default());
    let _held_context = install_marker(&mut isolate);
    let (bytes, chunk_sizes) = full_snapshot(&mut isolate);
    let text = String::from_utf8(bytes).expect("heap snapshot is UTF-8 JSON");
    pass(
        "heap-snapshot/stream/success",
        Json::obj(vec![
            ("callback_called", Json::b(!chunk_sizes.is_empty())),
            ("final_empty_chunk", Json::b(chunk_sizes.last() == Some(&0))),
            (
                "has_nonempty_data_chunk",
                Json::b(chunk_sizes.iter().any(|size| *size > 0)),
            ),
            (
                "json_document",
                Json::b(text.starts_with('{') && text.ends_with('}')),
            ),
            (
                "has_snapshot_meta",
                Json::b(text.contains("\"snapshot\"") && text.contains("\"meta\"")),
            ),
            (
                "has_nodes_edges_strings",
                Json::b(
                    text.contains("\"nodes\"")
                        && text.contains("\"edges\"")
                        && text.contains("\"strings\""),
                ),
            ),
            (
                "contains_marker",
                Json::b(text.contains("gov8-snapshot-marker")),
            ),
        ]),
    )
}

fn callback_abort() -> CheckOutcome {
    let mut isolate = v8::Isolate::new(Default::default());
    let _held_context = install_marker(&mut isolate);
    let mut calls = 0usize;
    let mut delivered = 0usize;
    isolate.take_heap_snapshot(|chunk| {
        calls += 1;
        delivered += chunk.len();
        false
    });
    let usable = eval_text(&mut isolate, "String(40+2)").as_deref() == Some("42");
    pass(
        "heap-snapshot/stream/callback_abort",
        Json::obj(vec![
            ("exactly_one_callback", Json::b(calls == 1)),
            ("first_chunk_nonempty", Json::b(delivered > 0)),
            ("isolate_usable_after_abort", Json::b(usable)),
        ]),
    )
}

fn repeat_after_abort() -> CheckOutcome {
    let mut isolate = v8::Isolate::new(Default::default());
    let _held_context = install_marker(&mut isolate);
    isolate.take_heap_snapshot(|_| false);
    let (first, first_chunks) = full_snapshot(&mut isolate);
    let (second, second_chunks) = full_snapshot(&mut isolate);
    let first = String::from_utf8(first).expect("first heap snapshot is UTF-8");
    let second = String::from_utf8(second).expect("second heap snapshot is UTF-8");
    pass(
        "heap-snapshot/lifecycle/repeat_after_abort",
        Json::obj(vec![
            (
                "first_complete",
                Json::b(first.starts_with('{') && first.ends_with('}')),
            ),
            (
                "second_complete",
                Json::b(second.starts_with('{') && second.ends_with('}')),
            ),
            (
                "callbacks_each_time",
                Json::b(!first_chunks.is_empty() && !second_chunks.is_empty()),
            ),
            (
                "marker_each_time",
                Json::b(
                    first.contains("gov8-snapshot-marker")
                        && second.contains("gov8-snapshot-marker"),
                ),
            ),
        ]),
    )
}

fn panic_probe() {
    suppress_windows_fatal_dialogs();
    oracle::ensure_v8();
    let mut isolate = v8::Isolate::new(Default::default());
    eprintln!("marker:before-heap-snapshot-callback-panic");
    isolate.take_heap_snapshot(|_| panic!("heap snapshot callback panic sentinel"));
    eprintln!("marker:after-heap-snapshot-callback-panic");
}

fn run_fixture() {
    oracle::ensure_v8();
    let outcomes = [successful_stream(), callback_abort(), repeat_after_abort()];
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(outcomes.len(), outcomes.len(), 0));
}

fn main() {
    let args: Vec<_> = std::env::args().collect();
    if args.len() == 2 && args[1] == "--panic" {
        panic_probe();
    } else {
        assert_eq!(args.len(), 1, "usage: conformance-heap-snapshot [--panic]");
        run_fixture();
    }
}
