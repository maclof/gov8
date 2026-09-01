use oracle::json::Json;
use oracle::report::{pass as report_pass, summary_line as report_summary_line, CheckOutcome};
use std::ffi::CStr;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use v8::cppgc::{GarbageCollected, Visitor};

const WRAP_TAG: u16 = 1;
const MAX_VALID_TAG: u16 = 0x7ffe;
const SEM_FAILCRITICALERRORS: u32 = 0x0001;
const SEM_NOGPFAULTERRORBOX: u32 = 0x0002;
const SEM_NOOPENFILEERRORBOX: u32 = 0x8000;

#[link(name = "kernel32")]
unsafe extern "system" {
    #[link_name = "SetErrorMode"]
    fn set_error_mode(mode: u32) -> u32;
}

fn suppress_windows_fatal_dialogs() {
    // Fatal probes intentionally trigger V8 CHECKs or a non-unwinding Rust
    // panic. Disable Windows Error Reporting UI so an unattended oracle can
    // always reach its parent-enforced deadline.
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

struct OtherManaged;

unsafe impl GarbageCollected for OtherManaged {
    fn trace(&self, _visitor: &mut Visitor) {}

    fn get_name(&self) -> &'static CStr {
        c"CppGCObjectWrappingOther"
    }
}

fn pass(outcomes: &mut Vec<CheckOutcome>, id: &'static str, value: Json) {
    outcomes.push(report_pass(id, value));
}

fn summary_line(outcomes: &[CheckOutcome]) -> String {
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    report_summary_line(outcomes.len(), passed, outcomes.len() - passed)
}

struct Managed {
    id: i32,
    traced: v8::TracedReference<v8::Value>,
    traces: Arc<AtomicUsize>,
    drops: Arc<AtomicUsize>,
    panic_while_tracing: bool,
}

unsafe impl GarbageCollected for Managed {
    fn trace(&self, visitor: &mut Visitor) {
        self.traces.fetch_add(1, Ordering::SeqCst);
        assert!(!self.panic_while_tracing, "cppgc trace callback panic");
        visitor.trace(&self.traced);
    }

    fn get_name(&self) -> &'static CStr {
        c"CppGCObjectWrappingOracle"
    }
}

impl Drop for Managed {
    fn drop(&mut self) {
        self.drops.fetch_add(1, Ordering::SeqCst);
    }
}

fn empty_callback(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments,
    _rv: v8::ReturnValue<v8::Value>,
) {
}

fn api_wrapper<'s>(scope: &mut v8::PinScope<'s, '_>) -> v8::Local<'s, v8::Object> {
    let template = v8::FunctionTemplate::new(scope, empty_callback);
    let function = template.get_function(scope).expect("function");
    function.new_instance(scope, &[]).expect("instance")
}

fn managed<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    id: i32,
    target: v8::Local<'s, v8::Value>,
    traces: Arc<AtomicUsize>,
    drops: Arc<AtomicUsize>,
    panic_while_tracing: bool,
) -> v8::cppgc::UnsafePtr<Managed> {
    let traced = v8::TracedReference::new(scope, target);
    let heap = scope.get_cpp_heap().expect("default isolate cppgc heap");
    unsafe {
        v8::cppgc::make_garbage_collected(
            heap,
            Managed {
                id,
                traced,
                traces,
                drops,
                panic_while_tracing,
            },
        )
    }
}

#[inline(never)]
fn wrap_managed<'s, const TAG: u16>(
    scope: &mut v8::PinScope<'s, '_>,
    wrapper: v8::Local<'s, v8::Object>,
    target: v8::Local<'s, v8::Value>,
    id: i32,
    traces: Arc<AtomicUsize>,
    drops: Arc<AtomicUsize>,
    panic_while_tracing: bool,
) {
    let pointer = managed(scope, id, target, traces, drops, panic_while_tracing);
    unsafe { v8::Object::wrap::<TAG, Managed>(scope, wrapper, &pointer) };
}

#[inline(never)]
fn wrap_and_observe<'s, const TAG: u16>(
    scope: &mut v8::PinScope<'s, '_>,
    wrapper: v8::Local<'s, v8::Object>,
    target: v8::Local<'s, v8::Value>,
    id: i32,
    traces: Arc<AtomicUsize>,
    drops: Arc<AtomicUsize>,
) -> (i32, bool, bool) {
    let pointer = managed(scope, id, target, traces, drops, false);
    unsafe { v8::Object::wrap::<TAG, Managed>(scope, wrapper, &pointer) };
    let first = unsafe { v8::Object::unwrap::<TAG, Managed>(scope, wrapper) }.expect("wrapped");
    let second = unsafe { v8::Object::unwrap::<TAG, Managed>(scope, wrapper) }.expect("wrapped");
    let first_ref = unsafe { first.as_ref() };
    let second_ref = unsafe { second.as_ref() };
    let traced = first_ref.traced.get(scope).expect("traced target");
    (
        first_ref.id,
        std::ptr::eq(first_ref, second_ref),
        traced.strict_equals(target),
    )
}

#[inline(never)]
fn unwrap_id<const TAG: u16>(
    scope: &mut v8::PinScope<'_, '_>,
    wrapper: v8::Local<v8::Object>,
) -> i32 {
    let pointer = unsafe { v8::Object::unwrap::<TAG, Managed>(scope, wrapper) }.expect("wrapped");
    unsafe { pointer.as_ref() }.id
}

#[inline(never)]
fn traced_marker<const TAG: u16>(
    scope: &mut v8::PinScope<'_, '_>,
    wrapper: v8::Local<v8::Object>,
) -> i64 {
    let pointer = unsafe { v8::Object::unwrap::<TAG, Managed>(scope, wrapper) }.expect("wrapped");
    let value = unsafe { pointer.as_ref() }
        .traced
        .get(scope)
        .expect("traced target");
    let object = v8::Local::<v8::Object>::try_from(value).expect("target object");
    let marker_key = key(scope, "marker");
    object
        .get(scope, marker_key.into())
        .and_then(|value| value.integer_value(scope))
        .expect("marker")
}

fn key<'s>(scope: &mut v8::PinScope<'s, '_>, value: &str) -> v8::Local<'s, v8::String> {
    v8::String::new(scope, value).expect("string")
}

fn initialize() -> v8::SharedRef<v8::Platform> {
    v8::V8::set_flags_from_string("--expose-gc");
    let platform = v8::new_unprotected_default_platform(0, false).make_shared();
    v8::V8::initialize_platform(platform.clone());
    v8::V8::initialize();
    platform
}

fn normal_checks() {
    let platform = initialize();
    let mut outcomes = Vec::new();
    let identity_traces = Arc::new(AtomicUsize::new(0));
    let identity_drops = Arc::new(AtomicUsize::new(0));
    let survival_traces = Arc::new(AtomicUsize::new(0));
    let survival_drops = Arc::new(AtomicUsize::new(0));
    let boundary_traces = Arc::new(AtomicUsize::new(0));
    let boundary_drops = Arc::new(AtomicUsize::new(0));

    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    let context_global;

    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, v8::ContextOptions::default());
        context_global = v8::Global::new(scope, context);
        let scope = &mut v8::ContextScope::new(scope, context);

        let plain = v8::Object::new(scope);
        let wrapper = api_wrapper(scope);
        let unwrapped_before = unsafe { v8::Object::unwrap::<WRAP_TAG, Managed>(scope, wrapper) };
        pass(
            &mut outcomes,
            "cppgc-object-wrapping/default_heap_and_api_wrapper",
            Json::obj(vec![
                (
                    "default_heap_present",
                    Json::b(scope.get_cpp_heap().is_some()),
                ),
                ("plain_is_api_wrapper", Json::b(plain.is_api_wrapper())),
                ("api_is_api_wrapper", Json::b(wrapper.is_api_wrapper())),
                ("unwrapped_before_wrap", Json::b(unwrapped_before.is_some())),
            ]),
        );

        let target = v8::Object::new(scope);
        let target_value = target.into();
        let (identity_id, same_pointer, traced_identity) = wrap_and_observe::<WRAP_TAG>(
            scope,
            wrapper,
            target_value,
            7,
            identity_traces.clone(),
            identity_drops.clone(),
        );
        pass(
            &mut outcomes,
            "cppgc-object-wrapping/wrap_unwrap_identity",
            Json::obj(vec![
                ("id", Json::i(i64::from(identity_id))),
                ("same_pointer", Json::b(same_pointer)),
                ("traced_identity", Json::b(traced_identity)),
            ]),
        );

        let survival_target = v8::Object::new(scope);
        let marker = key(scope, "marker");
        let marker_value = v8::Integer::new(scope, 42);
        assert!(survival_target
            .set(scope, marker.into(), marker_value.into())
            .is_some());
        let survival_wrapper = api_wrapper(scope);
        wrap_managed::<WRAP_TAG>(
            scope,
            survival_wrapper,
            survival_target.into(),
            42,
            survival_traces.clone(),
            survival_drops.clone(),
            false,
        );
        let global = context.global(scope);
        let wrapped_key = key(scope, "wrapped");
        assert!(global
            .set(scope, wrapped_key.into(), survival_wrapper.into())
            .is_some());
    }

    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Local::new(scope, &context_global);
        let scope = &mut v8::ContextScope::new(scope, context);
        scope.request_garbage_collection_for_testing(v8::GarbageCollectionType::Full);

        let global = context.global(scope);
        let wrapped_key = key(scope, "wrapped");
        let wrapper = global
            .get(scope, wrapped_key.into())
            .and_then(|value| v8::Local::<v8::Object>::try_from(value).ok())
            .expect("rooted wrapper");
        let marker = traced_marker::<WRAP_TAG>(scope, wrapper);
        pass(
            &mut outcomes,
            "cppgc-object-wrapping/traced_reference_survival",
            Json::obj(vec![
                (
                    "drops_while_rooted",
                    Json::i(survival_drops.load(Ordering::SeqCst) as i64),
                ),
                ("marker", Json::i(marker)),
                (
                    "trace_calls_positive",
                    Json::b(survival_traces.load(Ordering::SeqCst) > 0),
                ),
            ]),
        );
    }

    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Local::new(scope, &context_global);
        let scope = &mut v8::ContextScope::new(scope, context);
        let global = context.global(scope);
        let wrapped_key = key(scope, "wrapped");
        let undefined = v8::undefined(scope);
        assert!(global
            .set(scope, wrapped_key.into(), undefined.into())
            .is_some());
    }

    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Local::new(scope, &context_global);
        let scope = &mut v8::ContextScope::new(scope, context);
        scope.request_garbage_collection_for_testing(v8::GarbageCollectionType::Full);
        pass(
            &mut outcomes,
            "cppgc-object-wrapping/gc_destruction",
            Json::obj(vec![
                (
                    "drops_after_release",
                    Json::i(survival_drops.load(Ordering::SeqCst) as i64),
                ),
                (
                    "identity_collected",
                    Json::b(identity_drops.load(Ordering::SeqCst) == 1),
                ),
            ]),
        );

        let wrapper = api_wrapper(scope);
        let target = v8::Object::new(scope);
        wrap_managed::<MAX_VALID_TAG>(
            scope,
            wrapper,
            target.into(),
            i32::from(MAX_VALID_TAG),
            boundary_traces.clone(),
            boundary_drops.clone(),
            false,
        );
        let unwrapped_id = unwrap_id::<MAX_VALID_TAG>(scope, wrapper);
        let zero_wrapper = api_wrapper(scope);
        let zero_target = v8::Object::new(scope);
        wrap_managed::<0>(
            scope,
            zero_wrapper,
            zero_target.into(),
            0,
            boundary_traces.clone(),
            boundary_drops.clone(),
            false,
        );
        let zero_id = unwrap_id::<0>(scope, zero_wrapper);
        pass(
            &mut outcomes,
            "cppgc-object-wrapping/tag_boundaries",
            Json::obj(vec![
                ("min_tag", Json::i(0)),
                ("min_unwrap_id", Json::i(i64::from(zero_id))),
                ("max_tag", Json::i(i64::from(MAX_VALID_TAG))),
                ("max_unwrap_id", Json::i(i64::from(unwrapped_id))),
            ]),
        );
    }

    drop(context_global);
    drop(isolate);
    let v8_dispose = unsafe { v8::V8::dispose() };
    unsafe { v8::cppgc::shutdown_process() };
    v8::V8::dispose_platform();
    drop(platform);

    pass(
        &mut outcomes,
        "cppgc-object-wrapping/orderly_teardown",
        Json::obj(vec![
            (
                "boundary_drops",
                Json::i(boundary_drops.load(Ordering::SeqCst) as i64),
            ),
            ("cppgc_shutdown", Json::b(true)),
            ("platform_dispose", Json::b(true)),
            ("v8_dispose", Json::b(v8_dispose)),
        ]),
    );

    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(&outcomes));
}

fn tag_edge<const TAG: u16>() {
    let _platform = initialize();
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, v8::ContextOptions::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let wrapper = api_wrapper(scope);
    let target = v8::Object::new(scope);
    let pointer = managed(
        scope,
        i32::from(TAG),
        target.into(),
        Arc::new(AtomicUsize::new(0)),
        Arc::new(AtomicUsize::new(0)),
        false,
    );
    unsafe { v8::Object::wrap::<TAG, Managed>(scope, wrapper, &pointer) };
    println!("tag_edge_survived={TAG}");
}

fn wrong_tag() {
    let _platform = initialize();
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, v8::ContextOptions::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let wrapper = api_wrapper(scope);
    let target = v8::Object::new(scope);
    let pointer = managed(
        scope,
        1,
        target.into(),
        Arc::new(AtomicUsize::new(0)),
        Arc::new(AtomicUsize::new(0)),
        false,
    );
    unsafe { v8::Object::wrap::<1, Managed>(scope, wrapper, &pointer) };
    let wrong = unsafe { v8::Object::unwrap::<2, Managed>(scope, wrapper) };
    println!("wrong_tag_some={}", wrong.is_some());
}

fn wrong_type() {
    let _platform = initialize();
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, v8::ContextOptions::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let wrapper = api_wrapper(scope);
    let target = v8::Object::new(scope);
    wrap_managed::<1>(
        scope,
        wrapper,
        target.into(),
        1,
        Arc::new(AtomicUsize::new(0)),
        Arc::new(AtomicUsize::new(0)),
        false,
    );
    let wrong = unsafe { v8::Object::unwrap::<1, OtherManaged>(scope, wrapper) };
    println!("wrong_type_some={}", wrong.is_some());
}

fn rewrap<const SECOND_TAG: u16>() {
    let _platform = initialize();
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, v8::ContextOptions::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let wrapper = api_wrapper(scope);
    let first_target = v8::Object::new(scope);
    let first = managed(
        scope,
        1,
        first_target.into(),
        Arc::new(AtomicUsize::new(0)),
        Arc::new(AtomicUsize::new(0)),
        false,
    );
    unsafe { v8::Object::wrap::<1, Managed>(scope, wrapper, &first) };
    let second_target = v8::Object::new(scope);
    let second = managed(
        scope,
        2,
        second_target.into(),
        Arc::new(AtomicUsize::new(0)),
        Arc::new(AtomicUsize::new(0)),
        false,
    );
    unsafe { v8::Object::wrap::<SECOND_TAG, Managed>(scope, wrapper, &second) };
    let id = unwrap_id::<SECOND_TAG>(scope, wrapper);
    println!("rewrap_tag={SECOND_TAG},id={id}");
}

fn panic_trace() {
    let _platform = initialize();
    let mut isolate = v8::Isolate::new(v8::CreateParams::default());
    let context_global;
    {
        v8::scope!(let scope, &mut isolate);
        let context = v8::Context::new(scope, v8::ContextOptions::default());
        context_global = v8::Global::new(scope, context);
        let scope = &mut v8::ContextScope::new(scope, context);
        let wrapper = api_wrapper(scope);
        let target = v8::Object::new(scope);
        let pointer = managed(
            scope,
            1,
            target.into(),
            Arc::new(AtomicUsize::new(0)),
            Arc::new(AtomicUsize::new(0)),
            true,
        );
        unsafe { v8::Object::wrap::<1, Managed>(scope, wrapper, &pointer) };
        let wrapped_key = key(scope, "wrapped");
        assert!(context
            .global(scope)
            .set(scope, wrapped_key.into(), wrapper.into())
            .is_some());
    }
    v8::scope!(let scope, &mut isolate);
    let context = v8::Local::new(scope, &context_global);
    let scope = &mut v8::ContextScope::new(scope, context);
    scope.request_garbage_collection_for_testing(v8::GarbageCollectionType::Full);
    println!("panic_trace_survived");
}

fn explicit_init_after_v8() {
    let platform = initialize();
    v8::cppgc::initialize_process(platform);
    println!("explicit_init_after_v8_survived");
}

fn main() {
    let mode = std::env::args().nth(1);
    if matches!(
        mode.as_deref(),
        Some("panic-trace" | "explicit-init-after-v8")
    ) {
        suppress_windows_fatal_dialogs();
    }
    match mode.as_deref() {
        None => normal_checks(),
        Some("tag-min") => tag_edge::<0>(),
        Some("tag-max") => tag_edge::<MAX_VALID_TAG>(),
        Some("wrong-tag") => wrong_tag(),
        Some("wrong-type") => wrong_type(),
        Some("rewrap-same") => rewrap::<1>(),
        Some("rewrap-different") => rewrap::<2>(),
        Some("panic-trace") => panic_trace(),
        Some("explicit-init-after-v8") => explicit_init_after_v8(),
        Some(mode) => panic!("unknown mode: {mode}"),
    }
}
