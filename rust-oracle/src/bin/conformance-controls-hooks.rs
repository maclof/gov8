//! Process/isolate controls & hooks conformance runner (production slice).
//!
//! Characterizes the observable, deterministic contract of the pinned
//! `v8` crate (=152.2.0, V8 15.2.124.1-rusty) for process- and isolate-level
//! controls and hooks on x86_64-pc-windows-msvc, EXCLUDING modules, Wasm and
//! inspector. Prints the normalized JSON-lines report to stdout; exit code 0
//! iff every check passed. The report must be byte-for-byte reproducible by
//! the Go implementation; see `tests/fixtures/conformance-controls-hooks-*.jsonl`.
//!
//! # Process-level setup order (itself part of the contract)
//!
//! Everything in this binary is order-sensitive and runs exactly once:
//! 1. `V8::set_flags_from_command_line` (crate src/V8.rs:100) — must precede
//!    `V8::initialize()`; unrecognized args are returned untouched.
//! 2. `V8::set_flags_from_string("--expose-gc")` (src/V8.rs:165) — required
//!    for `Isolate::request_garbage_collection_for_testing`
//!    (src/isolate.rs:2044-2050 "only valid ... if --expose_gc was
//!    specified"; enforced by a fatal CHECK in this build) and to expose the
//!    JS `gc()` global in contexts created afterwards.
//! 3. `V8::set_entropy_source` (src/V8.rs:173) — pins `Math.random()` to a
//!    fixed seed; upstream characterization lives in the crate's
//!    tests/test_api_entropy_source.rs.
//! 4. `V8::initialize_platform` + `V8::initialize` (src/V8.rs:188/:237),
//!    identical platform config to `oracle::ensure_v8`
//!    (`new_default_platform(0, false)`).
//!
//! After step 4 the flag set is frozen: any further flag mutation CHECK-fails
//! with "Check failed: !IsFrozen()." and aborts the process (see
//! `controls/frozen_flags_fatal_subprocess`, characterized out-of-process).
//!
//! # Fatal/OOM/near-heap-limit handlers
//!
//! These are characterized only in short-lived subprocesses (this binary
//! re-execs itself with a mode argument). No check ever lets the heap grow
//! without a configured, bounded ceiling: `CreateParams::heap_limits
//! (0, 10 MiB)` caps every heap-pressure workload, and the near-heap-limit
//! callback doubles the limit once and stops, or deliberately shrinks it
//! (negative test only) to force the intended, controlled fatal OOM.
//!
//! # Benchmark specs (to be run by criterion benches and the Go harness;
//!   identical machine/flags/warm-up/iteration policy on both sides)
//!
//! - hooks/promise_hook_overhead: isolate with `set_promise_hook` recording
//!   hook vs identical isolate without; N = 10k `Promise.resolve().then`
//!   reaction cycles with `MicrotasksPolicy::Explicit` and one checkpoint per
//!   1k cycles. Metric: ns/iteration.
//! - hooks/message_listener_overhead: `add_message_listener` + uncaught
//!   `throw new Error("x")` per iteration vs the same throw with no listener.
//! - hooks/use_counter_overhead: `set_use_counter_callback` (no-op recording
//!   fn) + compile/eval of the 10-script use-counter workload vs no callback.
//! - hooks/entropy_source_overhead: `Math.random()` in a 100k-iteration JS
//!   loop with the fixed entropy source vs default entropy.
//! - hooks/codegen_callback_overhead: `eval` routed through the
//!   modify-code-generation callback (allow, no modification) in a
//!   disallowed context vs plain `eval` in an allowed context.
//! - hooks/prepare_stack_trace_overhead: `catch (e) { e.stack }` with the
//!   native callback vs with `Error.prepareStackTrace` set from JS.
//! - hooks/near_heap_limit_registration: isolate create +
//!   `add_near_heap_limit_callback` + dispose vs plain create/dispose (no
//!   heap pressure).
//! - hooks/memory_pressure_notification: `memory_pressure_notification(
//!   Moderate)` per iteration vs empty loop.
//!
//! Policy: release profile, criterion `warm_up_time = 3s`,
//! `measurement_time = 10s`, sample size 100; the Go harness must use the
//! same inputs, iteration counts, V8 flags and platform settings, and report
//! distributions (p50/p95) rather than single runs. Raw output goes under
//! `rust-oracle/bench-results/` next to the existing runs.
//!
//! # Untestable gaps (characterization limits of the pinned crate, verified
//!   empirically; see tests/controls_hooks_negative.rs for the fatal paths)
//!
//! - `V8::set_date_time_configuration_change_callback` is not exposed by the
//!   crate; only `date_time_configuration_change_notification` exists.
//! - `PromiseRejectEvent::PromiseRejectAfterResolved` /
//!   `PromiseResolveAfterResolved` are never delivered in this build, even
//!   with handlers attached before re-settling and the reject callback
//!   installed; only RejectWithNoHandler/HandlerAddedAfterReject are
//!   observable.
//! - `has_pending_background_tasks() == true` is reachable only via
//!   background Wasm compilation (out of scope); the false path is pinned.
//! - Idle support: only `Isolate::set_idle` is exposed
//!   (src/isolate.rs:1449); no `PerformIdleNotificationDeadline` equivalent,
//!   and the oracle platform is created with idle-task support off.
//! - The fatal handler is NOT invoked at every fatal site: it fired for the
//!   flags-freeze CHECK and the OOM path, but not for the
//!   "Must use --expose-gc" FATAL. Handler coverage is site-specific.
//! - Fatal handler always observes empty file and line 0 in this build
//!   (official-build V8_Fatal), so no build-path leakage is recorded.
//! - OOM `OomDetails::detail` is the empty string on the heap-OOM path.
//! - kSloppyMode is never counted for plain sloppy scripts in this build
//!   (only kStrictMode was observed); the 10 pinned use-counter features are
//!   the deterministic subset of `UseCounterFeature` reachable from plain JS.

use std::io::Write as _;
use std::process::ExitCode;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Mutex;

use oracle::json::Json;
use oracle::report::{expect_eq, CheckOutcome};

// ---------------------------------------------------------------------------
// Process-wide V8 setup (fixed order, exactly once).
// ---------------------------------------------------------------------------

static SETUP_RESULT: Mutex<Option<Json>> = Mutex::new(None);

fn entropy_fill_42(buf: &mut [u8]) -> bool {
    buf.fill(42);
    true
}

fn entropy_fill_7(buf: &mut [u8]) -> bool {
    buf.fill(7);
    true
}

/// Runs the whole process setup in the contractual order. Returns the
/// normalized observations of the pre-initialize steps.
fn ensure_v8_setup() -> Json {
    let mut guard = SETUP_RESULT.lock().unwrap();
    if let Some(setup) = guard.as_ref() {
        return setup.clone();
    }
    // 1. Command-line flags (pre-init). "--log-colour" is recognized by this
    //    engine and consumed; anything else is returned to the embedder.
    let leftover = v8::V8::set_flags_from_command_line(vec![
        "conformance-controls-hooks".to_string(),
        "--log-colour".to_string(),
        "--should-be-ignored".to_string(),
    ]);
    // 2. String flags (pre-init). Enables the JS `gc()` global for contexts
    //    created after initialization and unlocks
    //    `request_garbage_collection_for_testing`.
    v8::V8::set_flags_from_string("--expose-gc");
    // 3. Entropy source (pre-init) — pins Math.random().
    v8::V8::set_entropy_source(entropy_fill_42);
    // 4. Platform + V8, identical to oracle::ensure_v8.
    let platform = v8::new_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();
    let setup = Json::obj(vec![(
        "command_line_unrecognized",
        Json::arr(leftover.into_iter().map(|arg| Json::s(&arg)).collect()),
    )]);
    *guard = Some(setup.clone());
    setup
}

// ---------------------------------------------------------------------------
// Subprocess helpers. Fatal/OOM/near-heap-limit paths must be characterized
// out-of-process: they abort the process by design.
// ---------------------------------------------------------------------------

fn spawn_self(mode: &str) -> std::process::Output {
    let exe = std::env::current_exe().expect("current_exe");
    std::process::Command::new(exe)
        .arg(mode)
        .output()
        .expect("failed to spawn self for subprocess characterization")
}

fn stdout_text(output: &std::process::Output) -> String {
    String::from_utf8_lossy(&output.stdout).into_owned()
}

fn stderr_text(output: &std::process::Output) -> String {
    String::from_utf8_lossy(&output.stderr).into_owned()
}

fn exit_code_json(output: &std::process::Output) -> Json {
    Json::i(output.status.code().unwrap_or_default() as i64)
}

fn exit_code_hex_json(output: &std::process::Output) -> Json {
    Json::s(&format!(
        "0x{:08X}",
        output.status.code().unwrap_or_default() as u32
    ))
}

/// Parses the values of a `RESULT k=v k=v ...` line emitted by the
/// near-heap-limit subprocess. Keys are a fixed, self-produced vocabulary
/// with a stable order; a missing/malformed field yields the sentinel -1.
fn parse_result_values(stdout: &str) -> Vec<i64> {
    let line = stdout
        .lines()
        .find(|line| line.starts_with("RESULT "))
        .unwrap_or_default();
    line["RESULT ".len()..]
        .split_whitespace()
        .map(|pair| {
            pair.split_once('=')
                .and_then(|(_, value)| value.parse::<i64>().ok())
                .unwrap_or(-1)
        })
        .collect()
}

// ---------------------------------------------------------------------------
// Shared callback state. Fixture callbacks only append small normalized
// records; everything runs on the isolate's own thread.
// ---------------------------------------------------------------------------

static USE_COUNTER_SEEN: Mutex<Vec<i64>> = Mutex::new(Vec::new());
static PROMISE_HOOK_SEQ: Mutex<Vec<&'static str>> = Mutex::new(Vec::new());
static PROMISE_REJECT_SEQ: Mutex<Vec<&'static str>> = Mutex::new(Vec::new());
static PREPARE_STACK_CALLS: Mutex<Vec<String>> = Mutex::new(Vec::new());
static CODEGEN_CALLS: Mutex<Vec<String>> = Mutex::new(Vec::new());
static MESSAGE_LOG: Mutex<Vec<String>> = Mutex::new(Vec::new());

/// 0 = block, 1 = allow with modified source "999", 2 = allow unchanged.
const CODEGEN_BLOCK: usize = 0;
const CODEGEN_MODIFY: usize = 1;
const CODEGEN_ALLOW_AS_IS: usize = 2;
static CODEGEN_MODE: AtomicUsize = AtomicUsize::new(CODEGEN_BLOCK);

/// Extracts the pending exception text from a TryCatch scope.
macro_rules! caught_text {
    ($tc:ident) => {
        $tc.exception().and_then(|exception| {
            let text = exception.to_string($tc)?;
            Some(text.to_rust_string_lossy($tc))
        })
    };
}

/// Renders collected string records as a JSON array (and clears them).
fn drain_texts(slot: &Mutex<Vec<String>>) -> Json {
    let mut guard = slot.lock().unwrap();
    Json::arr(guard.drain(..).map(|text| Json::s(&text)).collect())
}

fn json_strings(values: &[&str]) -> Json {
    Json::arr(values.iter().map(|value| Json::s(value)).collect())
}

// ---------------------------------------------------------------------------
// Tiny eval helpers (same fixed compile/run path for every check).
// ---------------------------------------------------------------------------

fn eval_text(scope: &v8::PinScope<'_, '_>, source: &str) -> Option<String> {
    let source_handle = v8::String::new(scope, source)?;
    let script = v8::Script::compile(scope, source_handle, None)?;
    let value = script.run(scope)?;
    let text = value.to_string(scope)?;
    Some(text.to_rust_string_lossy(scope))
}

fn optional_json(value: &Option<String>) -> Json {
    match value {
        Some(text) => Json::s(text),
        None => Json::Null,
    }
}

// ---------------------------------------------------------------------------
// The checks, in contractual order.
// ---------------------------------------------------------------------------

/// `V8::set_flags_from_command_line` before `V8::initialize`: recognized
/// flags are consumed, everything else (including argv[0]) is returned.
fn flags_command_line_preinit() -> Vec<CheckOutcome> {
    let setup = ensure_v8_setup();
    vec![expect_eq(
        "controls/flags_command_line_preinit",
        Json::obj(vec![(
            "command_line_unrecognized",
            json_strings(&["conformance-controls-hooks", "--should-be-ignored"]),
        )]),
        setup,
    )]
}

/// `--expose-gc` set before initialization exposes the JS `gc()` global in
/// every context created afterwards (flag values are read at context
/// creation; the set is frozen once V8 is initialized).
fn flags_expose_gc_preinit() -> Vec<CheckOutcome> {
    ensure_v8_setup();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let typeof_gc = eval_text(scope, "typeof gc").unwrap();
    let gc_callable = eval_text(scope, "gc(); typeof gc === 'function'").unwrap();
    vec![expect_eq(
        "controls/flags_expose_gc_preinit",
        Json::obj(vec![
            ("typeof_gc", Json::s("function")),
            ("gc_callable", Json::b(true)),
        ]),
        Json::obj(vec![
            ("typeof_gc", Json::s(&typeof_gc)),
            ("gc_callable", Json::b(&gc_callable == "true")),
        ]),
    )]
}

/// The entropy source installed before `V8::initialize()` seeds every fresh
/// isolate's PRNG identically: `Math.random()` returns the same constant in
/// three independently created isolates. (Crate pin:
/// tests/test_api_entropy_source.rs.)
fn entropy_source_before_init() -> Vec<CheckOutcome> {
    ensure_v8_setup();
    let mut results = Vec::new();
    for _ in 0..3 {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        results.push(eval_text(scope, "Math.random()").unwrap());
    }
    let all_equal = results.iter().all(|value| *value == results[0]);
    vec![expect_eq(
        "controls/entropy_source_before_init",
        Json::obj(vec![
            ("identical_across_isolates", Json::b(true)),
            ("value", Json::s("0.41480742418592154")),
        ]),
        Json::obj(vec![
            ("identical_across_isolates", Json::b(all_equal)),
            ("value", Json::s(&results[0])),
        ]),
    )]
}

/// Replacing the entropy source AFTER `V8::initialize()` still affects
/// isolates created afterwards (the per-isolate PRNG is seeded lazily), and
/// yields a different constant.
fn entropy_source_replace_after_init() -> Vec<CheckOutcome> {
    ensure_v8_setup();
    v8::V8::set_entropy_source(entropy_fill_7);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let value = eval_text(scope, "Math.random()").unwrap();
    vec![expect_eq(
        "controls/entropy_source_replace_after_init",
        Json::obj(vec![
            ("value", Json::s("0.8960919850226692")),
            ("differs_from_pre_init_seed", Json::b(true)),
        ]),
        Json::obj(vec![
            ("value", Json::s(&value)),
            (
                "differs_from_pre_init_seed",
                Json::b(value != "0.41480742418592154"),
            ),
        ]),
    )]
}

/// Post-initialization flag mutation is fatal: V8 freezes its flags during
/// `V8::initialize()`. The registered fatal handler observes
/// (file="", line=0, "Check failed: !IsFrozen().") and the process aborts
/// with STATUS_BREAKPOINT (0x80000003). No recovery exists; characterized in
/// a subprocess.
fn frozen_flags_fatal_subprocess() -> Vec<CheckOutcome> {
    let output = spawn_self("sub-fatal-frozen-flags");
    let stderr = stderr_text(&output);
    let stdout = stdout_text(&output);
    vec![expect_eq(
        "controls/frozen_flags_fatal_subprocess",
        Json::obj(vec![
            ("exit_code", Json::i(-2147483645)),
            ("exit_code_hex", Json::s("0x80000003")),
            ("handler_called", Json::b(true)),
            ("handler_file", Json::s("")),
            ("handler_line", Json::i(0)),
            ("handler_message", Json::s("Check failed: !IsFrozen().")),
            ("banner_in_stderr", Json::b(true)),
            ("survived", Json::b(false)),
        ]),
        Json::obj(vec![
            ("exit_code", exit_code_json(&output)),
            ("exit_code_hex", exit_code_hex_json(&output)),
            ("handler_called", Json::b(stderr.contains("FATAL "))),
            (
                "handler_file",
                Json::s(if stderr.contains("FATAL file=\"\"") {
                    ""
                } else {
                    "<unmatched>"
                }),
            ),
            (
                "handler_line",
                Json::i(if stderr.contains(" line=0 ") { 0 } else { -1 }),
            ),
            (
                "handler_message",
                Json::s(
                    if stderr.contains("message=\"Check failed: !IsFrozen().\"") {
                        "Check failed: !IsFrozen()."
                    } else {
                        "<unmatched>"
                    },
                ),
            ),
            (
                "banner_in_stderr",
                Json::b(stderr.contains("# Fatal error")),
            ),
            ("survived", Json::b(stdout.contains("SURVIVED"))),
        ]),
    )]
}

/// `Isolate::request_garbage_collection_for_testing` (both Full and Minor)
/// without `--expose-gc` fails a fatal CHECK ("Must use --expose-gc") and
/// aborts. The API fatal handler is NOT invoked at this site (upstream
/// caveat). Subprocess-only; exit is STATUS_BREAKPOINT.
fn gc_request_requires_expose_gc_subprocess() -> Vec<CheckOutcome> {
    let mut outcomes = Vec::new();
    for kind in ["full", "minor"] {
        let output = spawn_self(&format!("sub-gc-without-expose-gc-{kind}"));
        let stderr = stderr_text(&output);
        let stdout = stdout_text(&output);
        outcomes.push(expect_eq(
            "controls/gc_request_requires_expose_gc_subprocess",
            Json::obj(vec![
                ("kind", Json::s(kind)),
                ("exit_code", Json::i(-2147483645)),
                ("exit_code_hex", Json::s("0x80000003")),
                (
                    "fatal_function",
                    Json::s("v8::Isolate::RequestGarbageCollectionForTesting"),
                ),
                ("fatal_message", Json::s("Must use --expose-gc")),
                ("api_fatal_handler_called", Json::b(false)),
                ("survived", Json::b(false)),
            ]),
            Json::obj(vec![
                ("kind", Json::s(kind)),
                ("exit_code", exit_code_json(&output)),
                ("exit_code_hex", exit_code_hex_json(&output)),
                (
                    "fatal_function",
                    Json::s(
                        if stderr.contains(
                            "# Fatal error in v8::Isolate::RequestGarbageCollectionForTesting",
                        ) {
                            "v8::Isolate::RequestGarbageCollectionForTesting"
                        } else {
                            "<unmatched>"
                        },
                    ),
                ),
                (
                    "fatal_message",
                    Json::s(if stderr.contains("# Must use --expose-gc") {
                        "Must use --expose-gc"
                    } else {
                        "<unmatched>"
                    }),
                ),
                (
                    "api_fatal_handler_called",
                    Json::b(stderr.contains("FATAL ")),
                ),
                ("survived", Json::b(stdout.contains("SURVIVED"))),
            ]),
        ));
    }
    outcomes
}

/// With `--expose-gc` (set pre-init in this process): a full collection
/// keeps still-referenced WeakRef targets alive (the kept-objects set),
/// `clear_kept_objects` drops that set so the next full collection clears
/// every WeakRef, and a Minor request runs without fatal error.
fn gc_request_full_minor_clear_kept_objects() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval_text(
        scope,
        "globalThis.w = []; for (let i = 0; i < 4242; i++) w.push(new WeakRef({ i }));",
    )
    .unwrap();
    scope.request_garbage_collection_for_testing(v8::GarbageCollectionType::Full);
    let kept = eval_text(scope, "w.every(r => r.deref() !== undefined)").unwrap();
    scope.clear_kept_objects();
    scope.request_garbage_collection_for_testing(v8::GarbageCollectionType::Full);
    let cleared = eval_text(scope, "w.every(r => r.deref() === undefined)").unwrap();
    scope.request_garbage_collection_for_testing(v8::GarbageCollectionType::Minor);
    vec![expect_eq(
        "controls/gc_request_full_minor_clear_kept_objects",
        Json::obj(vec![
            ("kept_after_full_gc", Json::b(true)),
            ("cleared_after_clear_kept_objects", Json::b(true)),
            ("minor_request_survived", Json::b(true)),
        ]),
        Json::obj(vec![
            ("kept_after_full_gc", Json::b(&kept == "true")),
            (
                "cleared_after_clear_kept_objects",
                Json::b(&cleared == "true"),
            ),
            ("minor_request_survived", Json::b(true)),
        ]),
    )]
}

/// `memory_pressure_notification` accepts all three levels back-to-back and
/// leaves the isolate fully usable.
fn memory_pressure_levels() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    scope.memory_pressure_notification(v8::MemoryPressureLevel::Moderate);
    scope.memory_pressure_notification(v8::MemoryPressureLevel::Critical);
    scope.memory_pressure_notification(v8::MemoryPressureLevel::None);
    let still_running = eval_text(scope, "1 + 1").unwrap();
    vec![expect_eq(
        "controls/memory_pressure_levels",
        Json::obj(vec![
            ("levels", json_strings(&["Moderate", "Critical", "None"])),
            ("isolate_still_running", Json::s("2")),
        ]),
        Json::obj(vec![
            ("levels", json_strings(&["Moderate", "Critical", "None"])),
            ("isolate_still_running", Json::s(&still_running)),
        ]),
    )]
}

/// `low_memory_notification` runs a full collection that reclaims unreachable
/// ArrayBuffer backing stores: `HeapStatistics::external_memory()` returns
/// from exactly 32 MiB (32 x 1 MiB allocations) back to the baseline of 0.
fn low_memory_notification_external_memory() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let baseline = scope.get_heap_statistics().external_memory();
    eval_text(
        scope,
        "globalThis.bufs = []; for (let i = 0; i < 32; i++) bufs.push(new ArrayBuffer(1 << 20));",
    )
    .unwrap();
    let after_alloc = scope.get_heap_statistics().external_memory();
    eval_text(scope, "bufs = null;").unwrap();
    scope.low_memory_notification();
    let after_gc = scope.get_heap_statistics().external_memory();
    vec![expect_eq(
        "controls/low_memory_notification_external_memory",
        Json::obj(vec![
            ("baseline_bytes", Json::i(21)),
            ("after_alloc_bytes", Json::i(33554453)),
            ("after_gc_bytes", Json::i(21)),
        ]),
        Json::obj(vec![
            ("baseline_bytes", Json::i(baseline as i64)),
            ("after_alloc_bytes", Json::i(after_alloc as i64)),
            ("after_gc_bytes", Json::i(after_gc as i64)),
        ]),
    )]
}

/// `Isolate::set_allow_atomics_wait` toggles whether `Atomics.wait` may
/// block: allowed (timeout 0) it deterministically returns "timed-out";
/// disallowed it throws a TypeError before any blocking. The toggle can be
/// flipped repeatedly on a live isolate.
fn atomics_wait_toggle() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval_text(
        scope,
        "globalThis.a = new Int32Array(new SharedArrayBuffer(4)); 'ok'",
    )
    .unwrap();
    const WAIT: &str = "Atomics.wait(globalThis.a, 0, 0, 0)";
    let mut actual = Vec::new();
    for allow in [true, false, true] {
        scope.set_allow_atomics_wait(allow);
        let observation;
        {
            v8::tc_scope!(let tc, scope);
            let result = eval_text(tc, WAIT);
            let error = if tc.has_caught() {
                caught_text!(tc)
            } else {
                None
            };
            observation = Json::obj(vec![
                ("allowed", Json::b(allow)),
                ("result", optional_json(&result)),
                ("error", optional_json(&error)),
            ]);
        }
        actual.push(observation);
    }
    let expected = Json::arr(vec![
        Json::obj(vec![
            ("allowed", Json::b(true)),
            ("result", Json::s("timed-out")),
            ("error", Json::Null),
        ]),
        Json::obj(vec![
            ("allowed", Json::b(false)),
            ("result", Json::Null),
            (
                "error",
                Json::s("TypeError: Atomics.wait cannot be called in this context"),
            ),
        ]),
        Json::obj(vec![
            ("allowed", Json::b(true)),
            ("result", Json::s("timed-out")),
            ("error", Json::Null),
        ]),
    ]);
    vec![expect_eq(
        "controls/atomics_wait_toggle",
        expected,
        Json::arr(actual),
    )]
}

/// `has_pending_background_tasks` is false for a fresh isolate and stays
/// false after plain script execution (true requires background Wasm
/// compilation, which is out of scope for this slice).
fn has_pending_background_tasks() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let fresh = scope.has_pending_background_tasks();
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval_text(scope, "2 + 2").unwrap();
    let after_script = scope.has_pending_background_tasks();
    vec![expect_eq(
        "controls/has_pending_background_tasks",
        Json::obj(vec![
            ("fresh", Json::b(false)),
            ("after_script", Json::b(false)),
        ]),
        Json::obj(vec![
            ("fresh", Json::b(fresh)),
            ("after_script", Json::b(after_script)),
        ]),
    )]
}

/// `set_idle` must be called on the isolate's thread while no JS is
/// executing; the flag itself has no synchronous observable effect, so the
/// check pins that the surrounding script still evaluates normally.
fn set_idle() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    scope.set_idle(true);
    let during = eval_text(scope, "40 + 2").unwrap();
    scope.set_idle(false);
    vec![expect_eq(
        "controls/set_idle",
        Json::obj(vec![("script_result_during_idle", Json::s("42"))]),
        Json::obj(vec![("script_result_during_idle", Json::s(&during))]),
    )]
}

/// Both `TimeZoneDetection` modes are accepted; the notifications never
/// change UTC date math, and the host time zone offset is stable across them
/// on an unchanged host configuration.
fn date_time_configuration_change_notification() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    const ISO: &str = "new Date(Date.UTC(2020, 0, 2, 3, 4, 5)).toISOString()";
    const OFFSET: &str = "new Date(0).getTimezoneOffset()";
    let iso_before = eval_text(scope, ISO).unwrap();
    let offset_before = eval_text(scope, OFFSET).unwrap();
    scope.date_time_configuration_change_notification(v8::TimeZoneDetection::Skip);
    let iso_after_skip = eval_text(scope, ISO).unwrap();
    let offset_after_skip = eval_text(scope, OFFSET).unwrap();
    scope.date_time_configuration_change_notification(v8::TimeZoneDetection::Redetect);
    let iso_after_redetect = eval_text(scope, ISO).unwrap();
    let offset_after_redetect = eval_text(scope, OFFSET).unwrap();
    vec![expect_eq(
        "controls/date_time_configuration_change_notification",
        Json::obj(vec![
            ("iso_constant", Json::s("2020-01-02T03:04:05.000Z")),
            ("utc_unchanged_by_notifications", Json::b(true)),
            ("timezone_offset_unchanged", Json::b(true)),
        ]),
        Json::obj(vec![
            ("iso_constant", Json::s(&iso_before)),
            (
                "utc_unchanged_by_notifications",
                Json::b(iso_before == iso_after_skip && iso_after_skip == iso_after_redetect),
            ),
            (
                "timezone_offset_unchanged",
                Json::b(
                    offset_before == offset_after_skip
                        && offset_after_skip == offset_after_redetect,
                ),
            ),
        ]),
    )]
}

unsafe extern "C" fn promise_hook_cb(
    hook_type: v8::PromiseHookType,
    _promise: v8::Local<v8::Promise>,
    _parent: v8::Local<v8::Value>,
) {
    let name = match hook_type {
        v8::PromiseHookType::Init => "Init",
        v8::PromiseHookType::Resolve => "Resolve",
        v8::PromiseHookType::Before => "Before",
        v8::PromiseHookType::After => "After",
    };
    PROMISE_HOOK_SEQ.lock().unwrap().push(name);
}

/// `set_promise_hook` observes Init/Resolve synchronously and the reaction
/// job as Before/(resolve)/After at the microtask checkpoint. Sequence for
/// `const p1 = new Promise(r => r(1)); const p2 = p1.then(() => 2);`:
/// run = [Init, Resolve, Init]; checkpoint = [Before, Resolve, After]
/// (the inner Resolve fires during the reaction job when p2 is resolved);
/// a second checkpoint is empty.
fn promise_hook_sequence() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    isolate.set_promise_hook(promise_hook_cb);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval_text(
        scope,
        "const p1 = new Promise(r => r(1)); const p2 = p1.then(() => 2); globalThis.p2 = p2;",
    )
    .unwrap();
    let snapshot = |slot: &Mutex<Vec<&'static str>>| -> Json {
        let mut guard = slot.lock().unwrap();
        Json::arr(guard.drain(..).map(Json::s).collect())
    };
    let after_run = snapshot(&PROMISE_HOOK_SEQ);
    scope.perform_microtask_checkpoint();
    let after_checkpoint = snapshot(&PROMISE_HOOK_SEQ);
    scope.perform_microtask_checkpoint();
    let after_second = snapshot(&PROMISE_HOOK_SEQ);
    vec![expect_eq(
        "controls/promise_hook_sequence",
        Json::obj(vec![
            ("after_run", json_strings(&["Init", "Resolve", "Init"])),
            (
                "after_checkpoint",
                json_strings(&["Before", "Resolve", "After"]),
            ),
            ("after_second_checkpoint", json_strings(&[])),
        ]),
        Json::obj(vec![
            ("after_run", after_run),
            ("after_checkpoint", after_checkpoint),
            ("after_second_checkpoint", after_second),
        ]),
    )]
}

unsafe extern "C" fn promise_reject_cb(message: v8::PromiseRejectMessage<'_>) {
    let name = match message.get_event() {
        v8::PromiseRejectEvent::PromiseRejectWithNoHandler => "RejectWithNoHandler",
        v8::PromiseRejectEvent::PromiseHandlerAddedAfterReject => "HandlerAddedAfterReject",
        v8::PromiseRejectEvent::PromiseRejectAfterResolved => "RejectAfterResolved",
        v8::PromiseRejectEvent::PromiseResolveAfterResolved => "ResolveAfterResolved",
    };
    PROMISE_REJECT_SEQ.lock().unwrap().push(name);
}

/// `set_promise_reject_callback` delivers RejectWithNoHandler synchronously
/// at rejection time and HandlerAddedAfterReject when a handler is attached
/// later. Re-resolving or re-rejecting an already-settled promise delivers
/// NO event in this build even with handlers attached (gap; see module doc).
fn promise_reject_notification() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_microtasks_policy(v8::MicrotasksPolicy::Explicit);
    isolate.set_promise_reject_callback(promise_reject_cb);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval_text(
        scope,
        concat!(
            "globalThis.p1 = Promise.reject(1);",
            "globalThis.p2 = Promise.resolve(2);",
            "p1.catch(() => {});",
            "p2.then(x => x, y => y);",
        ),
    )
    .unwrap();
    let snapshot = |slot: &Mutex<Vec<&'static str>>| -> Json {
        let mut guard = slot.lock().unwrap();
        Json::arr(guard.drain(..).map(Json::s).collect())
    };
    let after_run = snapshot(&PROMISE_REJECT_SEQ);
    eval_text(
        scope,
        concat!(
            "const r = Promise.withResolvers();",
            "r.promise.then(x => {}, y => {});",
            "r.resolve(1);",
            "r.resolve(2);",
            "r.reject(3);",
            "'a'",
        ),
    )
    .unwrap();
    let re_resolve = snapshot(&PROMISE_REJECT_SEQ);
    eval_text(
        scope,
        concat!(
            "const r2 = Promise.withResolvers();",
            "r2.promise.then(x => {}, y => {});",
            "r2.reject(1);",
            "r2.reject(2);",
            "'b'",
        ),
    )
    .unwrap();
    let re_reject = snapshot(&PROMISE_REJECT_SEQ);
    vec![expect_eq(
        "controls/promise_reject_notification",
        Json::obj(vec![
            (
                "after_run",
                json_strings(&["RejectWithNoHandler", "HandlerAddedAfterReject"]),
            ),
            ("re_resolve_settled", json_strings(&[])),
            ("re_reject_settled", json_strings(&[])),
        ]),
        Json::obj(vec![
            ("after_run", after_run),
            ("re_resolve_settled", re_resolve),
            ("re_reject_settled", re_reject),
        ]),
    )]
}

/// `set_prepare_stack_trace_callback` replaces the `stack` value for every
/// error whose `stack` is first accessed, receives the formatted message and
/// the CallSite array, and disables the JS `Error.prepareStackTrace` hook.
/// The native callback runs once per distinct error.
fn prepare_stack_trace_callback() -> Vec<CheckOutcome> {
    fn callback<'s>(
        scope: &mut v8::PinScope<'s, '_>,
        error: v8::Local<'s, v8::Value>,
        sites: v8::Local<'s, v8::Array>,
    ) -> v8::Local<'s, v8::Value> {
        let message = v8::Exception::create_message(scope, error);
        let text = message.get(scope).to_rust_string_lossy(scope);
        PREPARE_STACK_CALLS
            .lock()
            .unwrap()
            .push(format!("{text}:{}", sites.length()));
        v8::Integer::new(scope, 42).into()
    }
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_prepare_stack_trace_callback(callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let stack = eval_text(
        scope,
        concat!(
            "function g() { throw new Error(\"boom\") }\n",
            "function f() { g() }\n",
            "try { f() } catch (e) { e.stack }\n",
        ),
    )
    .unwrap();
    let first_calls = drain_texts(&PREPARE_STACK_CALLS);
    eval_text(
        scope,
        "Error.prepareStackTrace = function(e, s) { globalThis.jsHookUsed = true; return 'js'; };",
    )
    .unwrap();
    let stack2 = eval_text(
        scope,
        "try { (function zq(){ throw new Error('boom2') })() } catch (e2) { e2.stack }",
    )
    .unwrap();
    let js_used = eval_text(scope, "globalThis.jsHookUsed === true").unwrap();
    let second_calls = drain_texts(&PREPARE_STACK_CALLS);
    vec![expect_eq(
        "controls/prepare_stack_trace_callback",
        Json::obj(vec![
            ("stack_value", Json::s("42")),
            ("first_call", json_strings(&["Uncaught Error: boom:3"])),
            ("second_stack_value", Json::s("42")),
            ("second_call", json_strings(&["Uncaught Error: boom2:2"])),
            ("js_prepare_stack_trace_disabled", Json::b(true)),
        ]),
        Json::obj(vec![
            ("stack_value", Json::s(&stack)),
            ("first_call", first_calls),
            ("second_stack_value", Json::s(&stack2)),
            ("second_call", second_calls),
            (
                "js_prepare_stack_trace_disabled",
                Json::b(&js_used != "true"),
            ),
        ]),
    )]
}

unsafe extern "C" fn use_counter_cb(_isolate: &mut v8::Isolate, feature: v8::UseCounterFeature) {
    USE_COUNTER_SEEN.lock().unwrap().push(feature as i64);
}

/// `set_use_counter_callback` receives engine feature IDs for a fixed
/// JS workload. Only deterministic triggers are pinned; IDs are the pinned
/// V8's `UseCounterFeature` discriminants (see the mapping below).
fn use_counter_features() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_use_counter_callback(use_counter_cb);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    // (label, script, expected features). ID mapping for this pinned V8
    // (gen/src_binding_release_x86_64-pc-windows-msvc.rs, UseCounterFeature):
    // 9=kStrictMode, 21=kHtmlComment, 20=kHtmlCommentInExternalScript,
    // 43=kErrorCaptureStackTrace, 159=kStringReplaceAll,
    // 155=kPromiseWithResolvers, 161=kWeakReferences, 75=kStringNormalize,
    // 160=kStringWellFormed, 23=kForInInitializer.
    const WORKLOAD: &[(&str, &str, &[i64])] = &[
        ("strict_script", "\"use strict\"; 1", &[9]),
        ("sloppy_script", "var x = 1; x", &[]),
        ("html_comment", "<!-- html comment\n 1", &[21, 20]),
        ("capture_stack_trace", "Error.captureStackTrace({})", &[43]),
        (
            "string_replace_all",
            "\"abc\".replaceAll(\"a\", \"b\")",
            &[159],
        ),
        ("promise_with_resolvers", "Promise.withResolvers()", &[155]),
        ("weak_ref", "new WeakRef({})", &[161]),
        ("string_normalize", "\"x\".normalize()", &[75]),
        ("string_to_well_formed", "\"x\".toWellFormed()", &[160]),
        ("for_in_initializer", "for (var i = 0 in {}) {}", &[23]),
    ];
    let mut expected = Vec::new();
    let mut actual = Vec::new();
    for (label, script, features) in WORKLOAD {
        let before = USE_COUNTER_SEEN.lock().unwrap().len();
        let _ = eval_text(scope, script);
        let fired: Vec<Json> = USE_COUNTER_SEEN.lock().unwrap()[before..]
            .iter()
            .map(|&id| Json::i(id))
            .collect();
        expected.push(Json::obj(vec![
            ("label", Json::s(label)),
            (
                "features",
                Json::arr(features.iter().map(|&id| Json::i(id)).collect()),
            ),
        ]));
        actual.push(Json::obj(vec![
            ("label", Json::s(label)),
            ("features", Json::arr(fired)),
        ]));
    }
    vec![expect_eq(
        "controls/use_counter_features",
        Json::arr(expected),
        Json::arr(actual),
    )]
}

fn codegen_callback<'s, 'i>(
    scope: &mut v8::PinScope<'s, 'i>,
    source: v8::Local<'s, v8::Value>,
    is_code_like: bool,
) -> v8::ModifyCodeGenerationFromStringsResult<'s> {
    let text = if source.is_string() {
        v8::Local::<v8::String>::try_from(source)
            .unwrap()
            .to_rust_string_lossy(scope)
    } else {
        "<not-a-string>".to_string()
    };
    CODEGEN_CALLS
        .lock()
        .unwrap()
        .push(format!("{text}:{is_code_like}"));
    match CODEGEN_MODE.load(Ordering::SeqCst) {
        CODEGEN_BLOCK => v8::ModifyCodeGenerationFromStringsResult {
            codegen_allowed: false,
            modified_source: None,
        },
        CODEGEN_ALLOW_AS_IS => v8::ModifyCodeGenerationFromStringsResult {
            codegen_allowed: true,
            modified_source: None,
        },
        _ => {
            let modified = v8::String::new(scope, "999").unwrap();
            v8::ModifyCodeGenerationFromStringsResult {
                codegen_allowed: true,
                modified_source: Some(modified),
            }
        }
    }
}

/// `set_modify_code_generation_from_strings_callback` is consulted only when
/// the context disallows code generation from strings OR the eval source is
/// not a string (src/isolate.rs:593-617). It can block (EvalError), rewrite
/// the compiled source, or pass a non-string source through unchanged.
fn modify_code_generation_from_strings() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_modify_code_generation_from_strings_callback(codegen_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // 1. Allowed context + string source: callback is skipped entirely.
    let allowed_default = context.is_code_generation_from_strings_allowed();
    let plain_eval = eval_text(scope, "eval('1+1')").unwrap();
    let calls_after_plain = CODEGEN_CALLS.lock().unwrap().len();

    // 2. Disallowed context + block: EvalError, callback saw the source.
    context.set_allow_generation_from_strings(false);
    let disallowed = context.is_code_generation_from_strings_allowed();
    CODEGEN_MODE.store(CODEGEN_BLOCK, Ordering::SeqCst);
    let (blocked_result, blocked_error) = {
        v8::tc_scope!(let tc, scope);
        let result = eval_text(tc, "eval('2+2')");
        let error = if tc.has_caught() {
            caught_text!(tc)
        } else {
            None
        };
        (result, error)
    };
    let calls_after_block = CODEGEN_CALLS.lock().unwrap().len();

    // 3. Disallowed context + allow with modified source: eval returns 999.
    CODEGEN_MODE.store(CODEGEN_MODIFY, Ordering::SeqCst);
    let rewritten = eval_text(scope, "eval('3+3')").unwrap();
    let calls_after_rewrite = CODEGEN_CALLS.lock().unwrap().len();

    // 4. Allowed context + non-string source: callback consulted; allow
    //    without modification hands the source back unchanged (the Symbol).
    CODEGEN_MODE.store(CODEGEN_ALLOW_AS_IS, Ordering::SeqCst);
    context.set_allow_generation_from_strings(true);
    let symbol_passthrough = eval_text(
        scope,
        "globalThis.sym = Symbol('s'); typeof eval(globalThis.sym)",
    )
    .unwrap();
    let calls_total = CODEGEN_CALLS.lock().unwrap().len();
    let call_log = drain_texts(&CODEGEN_CALLS);

    vec![expect_eq(
        "controls/modify_code_generation_from_strings",
        Json::obj(vec![
            ("allowed_by_default", Json::b(true)),
            ("plain_eval_skips_callback", Json::s("2")),
            ("calls_after_plain", Json::i(0)),
            ("context_disallowed_after_set_false", Json::b(true)),
            ("blocked_result", Json::Null),
            (
                "blocked_error",
                Json::s("EvalError: Code generation from strings disallowed for this context"),
            ),
            ("calls_after_block", Json::i(1)),
            ("rewritten_eval", Json::s("999")),
            ("calls_after_rewrite", Json::i(2)),
            ("symbol_passthrough_typeof", Json::s("symbol")),
            ("calls_total", Json::i(4)),
            (
                "call_log",
                json_strings(&[
                    "2+2:false",
                    "3+3:false",
                    "<not-a-string>:false",
                    "<not-a-string>:false",
                ]),
            ),
        ]),
        Json::obj(vec![
            ("allowed_by_default", Json::b(allowed_default)),
            ("plain_eval_skips_callback", Json::s(&plain_eval)),
            ("calls_after_plain", Json::i(calls_after_plain as i64)),
            ("context_disallowed_after_set_false", Json::b(!disallowed)),
            ("blocked_result", optional_json(&blocked_result)),
            ("blocked_error", optional_json(&blocked_error)),
            ("calls_after_block", Json::i(calls_after_block as i64)),
            ("rewritten_eval", Json::s(&rewritten)),
            ("calls_after_rewrite", Json::i(calls_after_rewrite as i64)),
            ("symbol_passthrough_typeof", Json::s(&symbol_passthrough)),
            ("calls_total", Json::i(calls_total as i64)),
            ("call_log", call_log),
        ]),
    )]
}

unsafe extern "C" fn message_listener_cb(
    message: v8::Local<v8::Message>,
    exception: v8::Local<v8::Value>,
) {
    v8::callback_scope!(unsafe scope, message);
    let text = message.get(scope).to_rust_string_lossy(scope);
    let line = message.get_line_number(scope);
    let level = message.error_level();
    let start = message.get_start_position();
    let end = message.get_end_position();
    let exception_text = exception
        .to_string(scope)
        .unwrap()
        .to_rust_string_lossy(scope);
    MESSAGE_LOG.lock().unwrap().push(format!(
        "{text}|{line:?}|{level}|{start}..{end}|{exception_text}"
    ));
}

unsafe extern "C" fn warning_listener_cb(
    _message: v8::Local<v8::Message>,
    _exception: v8::Local<v8::Value>,
) {
    MESSAGE_LOG.lock().unwrap().push("warning".to_string());
}

/// `add_message_listener` observes only exceptions that escape every
/// TryCatch. Registering the same listener twice delivers each message
/// twice; a listener filtered to WARNING never sees an ERROR-level throw;
/// the isolate stays usable after an uncaught exception.
fn message_listener_uncaught_only() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let added_1 = isolate.add_message_listener(message_listener_cb);
    let added_2 = isolate.add_message_listener(message_listener_cb);
    let added_warning = isolate
        .add_message_listener_with_error_level(warning_listener_cb, v8::MessageErrorLevel::WARNING);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    // Uncaught throw on line 2; no TryCatch active.
    let source = "let a = 1;\nthrow new Error('boom');\nlet b = 2;";
    let source_handle = v8::String::new(scope, source).unwrap();
    let script = v8::Script::compile(scope, source_handle, None).unwrap();
    let run_failed = script.run(scope).is_none();
    let uncaught = drain_texts(&MESSAGE_LOG);
    // The isolate remains usable after the uncaught exception.
    let recovered = eval_text(scope, "40 + 2").unwrap();
    // Exceptions caught by an active TryCatch are never reported.
    {
        v8::tc_scope!(let tc, scope);
        let _ = eval_text(tc, "try { null.x } catch (e) { e }");
        let _ = eval_text(tc, "null.x");
    }
    let caught = drain_texts(&MESSAGE_LOG);
    vec![expect_eq(
        "controls/message_listener_uncaught_only",
        Json::obj(vec![
            (
                "registrations_ok",
                Json::arr(vec![Json::b(true), Json::b(true), Json::b(true)]),
            ),
            ("run_failed", Json::b(true)),
            (
                "uncaught_calls",
                json_strings(&[
                    "Uncaught Error: boom|Some(2)|8|11..12|Error: boom",
                    "Uncaught Error: boom|Some(2)|8|11..12|Error: boom",
                ]),
            ),
            ("recovered_after_uncaught", Json::s("42")),
            ("caught_calls", json_strings(&[])),
        ]),
        Json::obj(vec![
            (
                "registrations_ok",
                Json::arr(vec![
                    Json::b(added_1),
                    Json::b(added_2),
                    Json::b(added_warning),
                ]),
            ),
            ("run_failed", Json::b(run_failed)),
            ("uncaught_calls", uncaught),
            ("recovered_after_uncaught", Json::s(&recovered)),
            ("caught_calls", caught),
        ]),
    )]
}

// ---------------------------------------------------------------------------
// Subprocess modes (this binary re-execs itself). Each mode prints protocol
// markers; stdout must stay clean of anything else.
// ---------------------------------------------------------------------------

#[derive(Default)]
struct HeapLimitState {
    first_calls: u64,
    second_calls: u64,
    second_initial: usize,
    second_current: usize,
    second_returned: usize,
}

unsafe extern "C" fn replaced_callback(
    _data: *mut std::ffi::c_void,
    _current_heap_limit: usize,
    _initial_heap_limit: usize,
) -> usize {
    // Must never run: the later registration replaces it.
    0
}

unsafe extern "C" fn doubling_callback(
    data: *mut std::ffi::c_void,
    current_heap_limit: usize,
    initial_heap_limit: usize,
) -> usize {
    let state = unsafe { &mut *(data as *mut HeapLimitState) };
    state.second_calls += 1;
    state.second_current = current_heap_limit;
    state.second_initial = initial_heap_limit;
    let raised = current_heap_limit * 2;
    state.second_returned = raised;
    raised
}

/// `add_near_heap_limit_callback`: only the most recently added callback is
/// invoked (src/isolate.rs:1889-1891). The heap is capped at 10 MiB; the
/// callback reports V8's configured limit (4 MiB after V8 splits the
/// budget between generations) and doubles it exactly once, after which the
/// JS loop stops. No OOM is reached.
fn near_heap_limit_subprocess() -> Vec<CheckOutcome> {
    let output = spawn_self("sub-near-heap-limit");
    let values = parse_result_values(&stdout_text(&output));
    let sentinel = |index: usize| -> Json {
        values
            .get(index)
            .map_or(Json::i(-1), |&value| Json::i(value))
    };
    vec![expect_eq(
        "controls/near_heap_limit_subprocess",
        Json::obj(vec![
            ("calls_first", Json::i(0)),
            ("calls_second", Json::i(1)),
            ("initial_limit_bytes", Json::i(4194304)),
            ("current_limit_bytes", Json::i(4194304)),
            ("returned_limit_bytes", Json::i(8388608)),
            ("exit_ok", Json::b(true)),
        ]),
        Json::obj(vec![
            ("calls_first", sentinel(0)),
            ("calls_second", sentinel(1)),
            ("initial_limit_bytes", sentinel(2)),
            ("current_limit_bytes", sentinel(3)),
            ("returned_limit_bytes", sentinel(4)),
            ("exit_ok", Json::b(output.status.success())),
        ]),
    )]
}

/// The isolate OOM handler observes (location="Reached heap limit",
/// is_heap_oom=true, detail="") and the process fatal handler observes
/// (file="", line=0, "API fatal error handler returned after process out of
/// memory"); then V8 aborts with STATUS_BREAKPOINT. Controlled: the heap is
/// capped at 10 MiB via CreateParams::heap_limits. Subprocess-only.
fn oom_fatal_handlers_subprocess() -> Vec<CheckOutcome> {
    let output = spawn_self("sub-oom-fatal");
    let stderr = stderr_text(&output);
    let stdout = stdout_text(&output);
    vec![expect_eq(
        "controls/oom_fatal_handlers_subprocess",
        Json::obj(vec![
            ("exit_code", Json::i(-2147483645)),
            ("exit_code_hex", Json::s("0x80000003")),
            (
                "oom_observation",
                Json::s("OOM location=\"Reached heap limit\" is_heap_oom=true detail=\"\""),
            ),
            (
                "fatal_observation",
                Json::s(
                    "FATAL file=\"\" line=0 message=\"API fatal error handler returned \
                     after process out of memory\"",
                ),
            ),
            ("survived", Json::b(false)),
        ]),
        Json::obj(vec![
            ("exit_code", exit_code_json(&output)),
            ("exit_code_hex", exit_code_hex_json(&output)),
            (
                "oom_observation",
                Json::s(
                    stderr
                        .lines()
                        .find(|line| line.starts_with("OOM location="))
                        .unwrap_or("<unmatched>"),
                ),
            ),
            (
                "fatal_observation",
                Json::s(
                    stderr
                        .lines()
                        .find(|line| line.starts_with("FATAL "))
                        .unwrap_or("<unmatched>"),
                ),
            ),
            ("survived", Json::b(stdout.contains("SURVIVED"))),
        ]),
    )]
}

fn sub_near_heap_limit() {
    ensure_v8_setup();
    let mut state = HeapLimitState::default();
    let state_ptr = &mut state as *mut HeapLimitState as *mut std::ffi::c_void;
    let params = v8::CreateParams::default().heap_limits(0, 10 << 20);
    let isolate = &mut v8::Isolate::new(params);
    isolate.add_near_heap_limit_callback(replaced_callback, state_ptr);
    isolate.add_near_heap_limit_callback(doubling_callback, state_ptr);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    const WORKLOAD: &str = concat!(
        "\"hello world\"\n  .repeat(10)\n  .split(\"w\")\n",
        "  .map((s) => s.repeat(100).split(\"o\"))\n",
    );
    for _ in 0..1_000_000 {
        if eval_text(scope, WORKLOAD).is_none() {
            break;
        }
        if state.second_calls > 0 {
            break;
        }
    }
    println!(
        "RESULT calls_first={} calls_second={} initial_limit_bytes={} current_limit_bytes={} returned_limit_bytes={}",
        state.first_calls,
        state.second_calls,
        state.second_initial,
        state.second_current,
        state.second_returned
    );
}

fn fatal_handler(file: &str, line: i32, message: &str) {
    eprintln!("FATAL file={file:?} line={line} message={message:?}");
}

unsafe extern "C" fn recording_oom_handler(
    location: *const std::os::raw::c_char,
    details: &v8::OomDetails,
) {
    let location = if location.is_null() {
        String::new()
    } else {
        unsafe { std::ffi::CStr::from_ptr(location) }
            .to_str()
            .unwrap_or_default()
            .to_string()
    };
    let detail = if details.detail.is_null() {
        String::new()
    } else {
        unsafe { std::ffi::CStr::from_ptr(details.detail) }
            .to_str()
            .unwrap_or_default()
            .to_string()
    };
    eprintln!(
        "OOM location={location:?} is_heap_oom={} detail={detail:?}",
        details.is_heap_oom
    );
}

fn sub_fatal_frozen_flags() {
    v8::V8::set_fatal_error_handler(fatal_handler);
    ensure_v8_setup();
    eprintln!("MARK:before-flag-change");
    // The flag set is frozen after initialize(); only a value CHANGING write
    // trips the CHECK (a no-op write of the current value does not).
    v8::V8::set_flags_from_string("--no-expose-gc");
    eprintln!("MARK:after-flag-change");
    println!("SURVIVED");
}

fn sub_gc_without_expose_gc(kind: &str) {
    v8::V8::set_fatal_error_handler(fatal_handler);
    // Deliberately does NOT set --expose-gc (unlike ensure_v8_setup).
    let platform = v8::new_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform);
    v8::V8::initialize();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let request_type = if kind == "minor" {
        v8::GarbageCollectionType::Minor
    } else {
        v8::GarbageCollectionType::Full
    };
    eprintln!("MARK:before-gc-request");
    scope.request_garbage_collection_for_testing(request_type);
    eprintln!("MARK:after-gc-request");
    println!("SURVIVED");
}

fn sub_oom_fatal() {
    v8::V8::set_fatal_error_handler(fatal_handler);
    ensure_v8_setup();
    let params = v8::CreateParams::default().heap_limits(0, 10 << 20);
    let isolate = &mut v8::Isolate::new(params);
    isolate.set_oom_error_handler(recording_oom_handler);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    run_until_oom(scope);
}

/// Negative-test mode: the near-heap-limit callback deliberately shrinks the
/// limit (current/2) so the controlled fatal OOM is reached quickly. Used by
/// tests/controls_hooks_negative.rs, not by the fixture.
fn sub_near_heap_limit_shrink() {
    ensure_v8_setup();
    struct ShrinkState(u64);
    let mut state = ShrinkState(0);
    let state_ptr = &mut state as *mut ShrinkState as *mut std::ffi::c_void;
    unsafe extern "C" fn shrink_callback(
        data: *mut std::ffi::c_void,
        current_heap_limit: usize,
        _initial_heap_limit: usize,
    ) -> usize {
        let state = unsafe { &mut *(data as *mut ShrinkState) };
        state.0 += 1;
        eprintln!("SHRINK call={} current={current_heap_limit}", state.0);
        current_heap_limit / 2
    }
    let params = v8::CreateParams::default().heap_limits(0, 10 << 20);
    let isolate = &mut v8::Isolate::new(params);
    isolate.set_oom_error_handler(recording_oom_handler);
    isolate.add_near_heap_limit_callback(shrink_callback, state_ptr);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    run_until_oom(scope);
}

/// Negative-test mode: fatal heap OOM with NO handlers installed, to pin the
/// default abort behavior. Used by tests/controls_hooks_negative.rs.
fn sub_oom_default() {
    ensure_v8_setup();
    let params = v8::CreateParams::default().heap_limits(0, 10 << 20);
    let isolate = &mut v8::Isolate::new(params);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    run_until_oom(scope);
}

/// Shared heap-pressure loop for the OOM subprocess modes. The heap is hard-
/// capped by `CreateParams::heap_limits(0, 10 MiB)`, so this always ends in
/// the controlled fatal OOM and never allocates unbounded process memory.
fn run_until_oom(scope: &v8::PinScope<'_, '_>) {
    const WORKLOAD: &str = concat!(
        "\"hello world\"\n  .repeat(10)\n  .split(\"w\")\n",
        "  .map((s) => s.repeat(100).split(\"o\"))\n",
    );
    eprintln!("MARK:before-loop");
    for _ in 0..1_000_000 {
        if eval_text(scope, WORKLOAD).is_none() {
            break;
        }
    }
    eprintln!("MARK:after-loop");
    println!("SURVIVED");
}

/// Negative-test mode: unrecognized V8 flags passed BEFORE initialization
/// print an error to stderr (V8's PrintF(stderr, ...) — still embedder
/// noise, hence a subprocess) but are otherwise ignored; recognized flags in
/// the same string still take effect.
fn sub_invalid_flag_preinit() {
    v8::V8::set_flags_from_string("--definitely-not-a-real-flag");
    v8::V8::set_flags_from_string("--expose-gc --and-another-bogus-one");
    ensure_v8_setup();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let result = eval_text(scope, "1 + 1").unwrap();
    // The bogus "--and-another-bogus-one" tail must not have disabled the
    // recognized "--expose-gc" flag that precedes it.
    let gc_type = eval_text(scope, "typeof gc").unwrap();
    println!(
        "RESULT result={result} gc_type={}",
        if gc_type == "function" { 1 } else { 0 }
    );
}

// ---------------------------------------------------------------------------
// Registry and runner.
// ---------------------------------------------------------------------------

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    flags_command_line_preinit,
    flags_expose_gc_preinit,
    entropy_source_before_init,
    entropy_source_replace_after_init,
    frozen_flags_fatal_subprocess,
    gc_request_requires_expose_gc_subprocess,
    gc_request_full_minor_clear_kept_objects,
    memory_pressure_levels,
    low_memory_notification_external_memory,
    atomics_wait_toggle,
    has_pending_background_tasks,
    set_idle,
    date_time_configuration_change_notification,
    promise_hook_sequence,
    promise_reject_notification,
    prepare_stack_trace_callback,
    use_counter_features,
    modify_code_generation_from_strings,
    message_listener_uncaught_only,
    near_heap_limit_subprocess,
    oom_fatal_handlers_subprocess,
];

fn main() -> ExitCode {
    if let Some(mode) = std::env::args().nth(1) {
        return match mode.as_str() {
            "sub-near-heap-limit" => {
                sub_near_heap_limit();
                ExitCode::SUCCESS
            }
            "sub-fatal-frozen-flags" => {
                sub_fatal_frozen_flags();
                ExitCode::SUCCESS
            }
            "sub-gc-without-expose-gc-full" => {
                sub_gc_without_expose_gc("full");
                ExitCode::SUCCESS
            }
            "sub-gc-without-expose-gc-minor" => {
                sub_gc_without_expose_gc("minor");
                ExitCode::SUCCESS
            }
            "sub-oom-fatal" => {
                sub_oom_fatal();
                ExitCode::SUCCESS
            }
            "sub-near-heap-limit-shrink" => {
                sub_near_heap_limit_shrink();
                ExitCode::SUCCESS
            }
            "sub-oom-default" => {
                sub_oom_default();
                ExitCode::SUCCESS
            }
            "sub-invalid-flag-preinit" => {
                sub_invalid_flag_preinit();
                ExitCode::SUCCESS
            }
            _ => {
                eprintln!("unknown mode: {mode}");
                ExitCode::FAILURE
            }
        };
    }

    let mut outcomes = Vec::new();
    for check in CHECKS {
        outcomes.extend(check());
    }
    let total = outcomes.len();
    let mut text = String::new();
    let mut failed = 0usize;
    for outcome in &outcomes {
        if !outcome.passed() {
            failed += 1;
        }
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    text.push_str(&oracle::report::summary_line(total, total - failed, failed));
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
