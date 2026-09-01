//! Dynamic `import.defer()` conformance for pinned `v8` =152.2.0.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::cell::RefCell;
use std::rc::Rc;

#[cfg(windows)]
const SEM_FAILCRITICALERRORS: u32 = 0x0001;
#[cfg(windows)]
const SEM_NOGPFAULTERRORBOX: u32 = 0x0002;
#[cfg(windows)]
const SEM_NOOPENFILEERRORBOX: u32 = 0x8000;

#[cfg(windows)]
unsafe extern "system" {
    #[link_name = "SetErrorMode"]
    fn set_error_mode(mode: u32) -> u32;
}

#[cfg(windows)]
fn suppress_windows_fatal_dialogs() {
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

#[derive(Clone)]
struct CallbackObservation {
    resource_name: String,
    specifier: String,
    phase: &'static str,
    attributes: Vec<String>,
}

struct PendingImport {
    resolver: v8::Global<v8::PromiseResolver>,
    promise: v8::Global<v8::Promise>,
    namespace: v8::Global<v8::Value>,
    module: v8::Global<v8::Module>,
    preparation: v8::Global<v8::Promise>,
}

#[derive(Default)]
struct CallbackState {
    legacy_calls: usize,
    observations: Vec<CallbackObservation>,
    pending: Vec<PendingImport>,
}

fn phase_name(phase: v8::ModuleImportPhase) -> &'static str {
    match phase {
        v8::ModuleImportPhase::kSource => "Source",
        v8::ModuleImportPhase::kDefer => "Defer",
        v8::ModuleImportPhase::kEvaluation => "Evaluation",
    }
}

fn module_status(status: v8::ModuleStatus) -> &'static str {
    match status {
        v8::ModuleStatus::Uninstantiated => "Uninstantiated",
        v8::ModuleStatus::Instantiating => "Instantiating",
        v8::ModuleStatus::Instantiated => "Instantiated",
        v8::ModuleStatus::Evaluating => "Evaluating",
        v8::ModuleStatus::Evaluated => "Evaluated",
        v8::ModuleStatus::Errored => "Errored",
    }
}

fn promise_state(state: v8::PromiseState) -> &'static str {
    match state {
        v8::PromiseState::Pending => "Pending",
        v8::PromiseState::Fulfilled => "Fulfilled",
        v8::PromiseState::Rejected => "Rejected",
    }
}

fn script_origin<'s>(scope: &v8::PinScope<'s, '_>, name: &str) -> v8::ScriptOrigin<'s> {
    let resource_name = v8::String::new(scope, name).unwrap().into();
    v8::ScriptOrigin::new(
        scope,
        resource_name,
        0,
        0,
        false,
        -1,
        None,
        false,
        false,
        false,
        None,
    )
}

fn module_origin<'s>(scope: &v8::PinScope<'s, '_>, name: &str) -> v8::ScriptOrigin<'s> {
    let resource_name = v8::String::new(scope, name).unwrap().into();
    v8::ScriptOrigin::new(
        scope,
        resource_name,
        0,
        0,
        false,
        -1,
        None,
        false,
        false,
        true,
        None,
    )
}

fn run_script<'s>(
    scope: &v8::PinScope<'s, '_>,
    name: &str,
    text: &str,
) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, text)?;
    let origin = script_origin(scope, name);
    v8::Script::compile(scope, source, Some(&origin))?.run(scope)
}

fn compile_module<'s>(
    scope: &v8::PinScope<'s, '_>,
    name: &str,
    text: &str,
) -> Option<v8::Local<'s, v8::Module>> {
    let source = v8::String::new(scope, text)?;
    let origin = module_origin(scope, name);
    let mut source = v8::script_compiler::Source::new(source, Some(&origin));
    v8::script_compiler::compile_module(scope, &mut source)
}

#[allow(clippy::unnecessary_wraps)]
fn no_imports<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    None
}

fn legacy_callback<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    _host_options: v8::Local<'s, v8::Data>,
    _resource_name: v8::Local<'s, v8::Value>,
    _specifier: v8::Local<'s, v8::String>,
    _attributes: v8::Local<'s, v8::FixedArray>,
) -> Option<v8::Local<'s, v8::Promise>> {
    let shared = scope.get_slot::<Rc<RefCell<CallbackState>>>()?.clone();
    shared.borrow_mut().legacy_calls += 1;
    let resolver = v8::PromiseResolver::new(scope)?;
    let unexpected = v8::String::new(scope, "unexpected legacy callback")?.into();
    resolver.resolve(scope, unexpected)?;
    Some(resolver.get_promise(scope))
}

fn phase_callback<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    _host_options: v8::Local<'s, v8::Data>,
    resource_name: v8::Local<'s, v8::Value>,
    specifier: v8::Local<'s, v8::String>,
    phase: v8::ModuleImportPhase,
    attributes: v8::Local<'s, v8::FixedArray>,
) -> Option<v8::Local<'s, v8::Promise>> {
    let shared = scope.get_slot::<Rc<RefCell<CallbackState>>>()?.clone();
    let specifier_text = specifier.to_rust_string_lossy(scope);
    let attribute_text = (0..attributes.length())
        .map(|index| {
            attributes
                .get(scope, index)
                .unwrap()
                .cast::<v8::String>()
                .to_rust_string_lossy(scope)
        })
        .collect();
    let observation = CallbackObservation {
        resource_name: resource_name.to_rust_string_lossy(scope),
        specifier: specifier_text.clone(),
        phase: phase_name(phase),
        attributes: attribute_text,
    };

    if specifier_text == "reject-me" {
        shared.borrow_mut().observations.push(observation);
        let resolver = v8::PromiseResolver::new(scope)?;
        let reason = v8::String::new(scope, "host rejected deferred import")?.into();
        resolver.reject(scope, reason)?;
        return Some(resolver.get_promise(scope));
    }

    let ordinal = shared.borrow().pending.len() + 1;
    let source = format!(
        "globalThis.deferBodyHits++; export const answer=42; export const ordinal={ordinal};"
    );
    let module = compile_module(scope, &format!("{specifier_text}.mjs"), &source)?;
    if module.instantiate_module(scope, no_imports) != Some(true) {
        return None;
    }
    let preparation = module
        .evaluate_for_import_defer(scope)?
        .cast::<v8::Promise>();
    let namespace = module.get_module_namespace_with_phase(v8::ModuleImportPhase::kDefer);
    let resolver = v8::PromiseResolver::new(scope)?;
    let promise = resolver.get_promise(scope);
    let pending = PendingImport {
        resolver: v8::Global::new(scope, resolver),
        promise: v8::Global::new(scope, promise),
        namespace: v8::Global::new(scope, namespace),
        module: v8::Global::new(scope, module),
        preparation: v8::Global::new(scope, preparation),
    };
    let mut state = shared.borrow_mut();
    state.observations.push(observation);
    state.pending.push(pending);
    Some(promise)
}

fn new_isolate() -> (v8::OwnedIsolate, Rc<RefCell<CallbackState>>) {
    let state = Rc::new(RefCell::new(CallbackState::default()));
    let mut isolate = v8::Isolate::new(Default::default());
    assert!(isolate.set_slot(Rc::clone(&state)));
    isolate.set_host_import_module_dynamically_callback(legacy_callback);
    isolate.set_host_import_module_with_phase_dynamically_callback(phase_callback);
    (isolate, state)
}

fn resolve_pending(
    scope: &v8::PinScope<'_, '_>,
    state: &Rc<RefCell<CallbackState>>,
    index: usize,
) -> bool {
    let state = state.borrow();
    let pending = &state.pending[index];
    let resolver = v8::Local::new(scope, &pending.resolver);
    let namespace = v8::Local::new(scope, &pending.namespace);
    resolver.resolve(scope, namespace).unwrap_or(false)
}

fn pending_status(
    scope: &v8::PinScope<'_, '_>,
    state: &Rc<RefCell<CallbackState>>,
    index: usize,
) -> (&'static str, &'static str) {
    let state = state.borrow();
    let pending = &state.pending[index];
    let module = v8::Local::new(scope, &pending.module);
    let preparation = v8::Local::new(scope, &pending.preparation);
    (
        module_status(module.get_status()),
        promise_state(preparation.state()),
    )
}

fn clear_pending(state: &Rc<RefCell<CallbackState>>) {
    state.borrow_mut().pending.clear();
}

fn pin() -> Vec<CheckOutcome> {
    vec![pass(
        "dynamic-import-defer/pin",
        Json::obj(vec![
            ("rust_crate", Json::s("v8=152.2.0")),
            ("v8", Json::s(v8::V8::get_version())),
            ("flag", Json::s("--js-defer-import-eval")),
            ("syntax", Json::s("import.defer(...)")),
        ]),
    )]
}

fn phase_payload_and_lazy_evaluation() -> Vec<CheckOutcome> {
    let (mut isolate, state) = new_isolate();
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let outer = run_script(
        scope,
        "defer-entry.js",
        "globalThis.deferBodyHits=0; globalThis.deferFulfilled=false; \
         globalThis.deferNS=undefined; \
         globalThis.deferOuter=import.defer('deferred-ok',{with:{type:'json',mode:'lazy'}}); \
         deferOuter.then(ns=>{deferFulfilled=true; deferNS=ns;}); deferOuter",
    )
    .unwrap()
    .cast::<v8::Promise>();
    let (status_before, preparation_before) = pending_status(scope, &state, 0);
    let outer_initial = promise_state(outer.state());
    let returned_is_callback_promise = {
        let state = state.borrow();
        let callback_promise = v8::Local::new(scope, &state.pending[0].promise);
        outer.strict_equals(callback_promise.into())
    };
    let body_before = run_script(scope, "probe.js", "deferBodyHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let resolved = resolve_pending(scope, &state, 0);
    let state_immediately_after_resolve = promise_state(outer.state());
    scope.perform_microtask_checkpoint();
    let state_after_checkpoint = promise_state(outer.state());
    let fulfilled = run_script(scope, "probe.js", "deferFulfilled")
        .unwrap()
        .is_true();
    let body_after_fulfillment = run_script(scope, "probe.js", "deferBodyHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let status_after_fulfillment = pending_status(scope, &state, 0).0;
    let answer = run_script(scope, "probe.js", "deferNS.answer")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let body_after_access = run_script(scope, "probe.js", "deferBodyHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let answer_again = run_script(scope, "probe.js", "deferNS.answer")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let body_after_second_access = run_script(scope, "probe.js", "deferBodyHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let status_after_access = pending_status(scope, &state, 0).0;
    let observation = state.borrow().observations[0].clone();
    let phase_calls = state.borrow().observations.len();
    let legacy_calls = state.borrow().legacy_calls;
    clear_pending(&state);

    vec![pass(
        "dynamic-import-defer/phase_payload_and_lazy_evaluation",
        Json::obj(vec![
            ("phase_calls", Json::i(phase_calls as i64)),
            ("legacy_calls", Json::i(legacy_calls as i64)),
            ("resource_name", Json::s(&observation.resource_name)),
            ("specifier", Json::s(&observation.specifier)),
            ("phase", Json::s(observation.phase)),
            (
                "attributes",
                Json::arr(observation.attributes.iter().map(|v| Json::s(v)).collect()),
            ),
            ("outer_initial", Json::s(outer_initial)),
            (
                "returned_is_callback_promise",
                Json::b(returned_is_callback_promise),
            ),
            ("preparation_initial", Json::s(preparation_before)),
            ("module_before_settlement", Json::s(status_before)),
            ("body_before_settlement", Json::i(body_before)),
            ("resolver_resolved", Json::b(resolved)),
            (
                "outer_immediately_after_resolve",
                Json::s(state_immediately_after_resolve),
            ),
            ("outer_after_checkpoint", Json::s(state_after_checkpoint)),
            ("then_ran", Json::b(fulfilled)),
            ("body_after_fulfillment", Json::i(body_after_fulfillment)),
            (
                "module_after_fulfillment",
                Json::s(status_after_fulfillment),
            ),
            ("answer", Json::i(answer)),
            ("body_after_access", Json::i(body_after_access)),
            ("answer_again", Json::i(answer_again)),
            (
                "body_after_second_access",
                Json::i(body_after_second_access),
            ),
            ("module_after_access", Json::s(status_after_access)),
        ]),
    )]
}

fn rejected_callback_promise() -> Vec<CheckOutcome> {
    let (mut isolate, state) = new_isolate();
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let promise = run_script(
        scope,
        "reject-entry.js",
        "globalThis.rejectSeen='unset'; \
         globalThis.rejectOuter=import.defer('reject-me'); \
         rejectOuter.catch(e=>{rejectSeen=String(e);}); rejectOuter",
    )
    .unwrap()
    .cast::<v8::Promise>();
    let before_checkpoint = promise_state(promise.state());
    let reason = promise.result(scope).to_rust_string_lossy(scope);
    scope.perform_microtask_checkpoint();
    let after_checkpoint = promise_state(promise.state());
    let caught = run_script(scope, "probe.js", "rejectSeen")
        .unwrap()
        .to_rust_string_lossy(scope);
    let observation = state.borrow().observations[0].clone();
    let phase_calls = state.borrow().observations.len();
    let legacy_calls = state.borrow().legacy_calls;
    vec![pass(
        "dynamic-import-defer/rejected_callback_promise",
        Json::obj(vec![
            ("phase_calls", Json::i(phase_calls as i64)),
            ("legacy_calls", Json::i(legacy_calls as i64)),
            ("specifier", Json::s(&observation.specifier)),
            ("phase", Json::s(observation.phase)),
            ("before_checkpoint", Json::s(before_checkpoint)),
            ("after_checkpoint", Json::s(after_checkpoint)),
            ("reason", Json::s(&reason)),
            ("caught", Json::s(&caught)),
        ]),
    )]
}

fn invalid_attributes_before_callback() -> Vec<CheckOutcome> {
    let (mut isolate, state) = new_isolate();
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let promise = run_script(
        scope,
        "invalid-attributes.js",
        "globalThis.invalidSeen='unset'; \
         globalThis.invalidOuter=import.defer('must-not-call',{with:{type:1}}); \
         invalidOuter.catch(e=>{invalidSeen=String(e);}); invalidOuter",
    )
    .unwrap()
    .cast::<v8::Promise>();
    let before_checkpoint = promise_state(promise.state());
    let reason = promise.result(scope).to_rust_string_lossy(scope);
    scope.perform_microtask_checkpoint();
    let caught = run_script(scope, "probe.js", "invalidSeen")
        .unwrap()
        .to_rust_string_lossy(scope);
    let phase_calls = state.borrow().observations.len();
    let legacy_calls = state.borrow().legacy_calls;
    vec![pass(
        "dynamic-import-defer/invalid_attributes_before_callback",
        Json::obj(vec![
            ("phase_calls", Json::i(phase_calls as i64)),
            ("legacy_calls", Json::i(legacy_calls as i64)),
            ("before_checkpoint", Json::s(before_checkpoint)),
            ("after_checkpoint", Json::s(promise_state(promise.state()))),
            ("reason", Json::s(&reason)),
            ("caught", Json::s(&caught)),
        ]),
    )]
}

fn repeated_delayed_settlement() -> Vec<CheckOutcome> {
    let (mut isolate, state) = new_isolate();
    v8::scope!(let scope, &mut isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    run_script(
        scope,
        "repeat-entry.js",
        "globalThis.deferBodyHits=0; globalThis.repeatNS1=undefined; globalThis.repeatNS2=undefined; \
         globalThis.repeatP1=import.defer('repeat'); globalThis.repeatP2=import.defer('repeat'); \
         repeatP1.then(ns=>{repeatNS1=ns;}); repeatP2.then(ns=>{repeatNS2=ns;});",
    )
    .unwrap();
    let p1 = run_script(scope, "probe.js", "repeatP1")
        .unwrap()
        .cast::<v8::Promise>();
    let p2 = run_script(scope, "probe.js", "repeatP2")
        .unwrap()
        .cast::<v8::Promise>();
    let initial_distinct = !p1.strict_equals(p2.into());
    let resolved_second = resolve_pending(scope, &state, 1);
    scope.perform_microtask_checkpoint();
    let after_second = (promise_state(p1.state()), promise_state(p2.state()));
    let body_after_second = run_script(scope, "probe.js", "deferBodyHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let resolved_first = resolve_pending(scope, &state, 0);
    scope.perform_microtask_checkpoint();
    let after_both = (promise_state(p1.state()), promise_state(p2.state()));
    let namespaces_distinct = run_script(scope, "probe.js", "repeatNS1!==repeatNS2")
        .unwrap()
        .is_true();
    let first_ordinal = run_script(scope, "probe.js", "repeatNS1.ordinal")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let hits_after_first_access = run_script(scope, "probe.js", "deferBodyHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let second_ordinal = run_script(scope, "probe.js", "repeatNS2.ordinal")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let hits_after_second_access = run_script(scope, "probe.js", "deferBodyHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let statuses = [
        pending_status(scope, &state, 0).0,
        pending_status(scope, &state, 1).0,
    ];
    let observations = state.borrow().observations.clone();
    let legacy_calls = state.borrow().legacy_calls;
    clear_pending(&state);
    vec![pass(
        "dynamic-import-defer/repeated_delayed_settlement",
        Json::obj(vec![
            ("phase_calls", Json::i(observations.len() as i64)),
            ("legacy_calls", Json::i(legacy_calls as i64)),
            (
                "specifiers",
                Json::arr(
                    observations
                        .iter()
                        .map(|value| Json::s(&value.specifier))
                        .collect(),
                ),
            ),
            ("promises_distinct", Json::b(initial_distinct)),
            ("resolved_second", Json::b(resolved_second)),
            ("after_second_p1", Json::s(after_second.0)),
            ("after_second_p2", Json::s(after_second.1)),
            ("body_after_second_settlement", Json::i(body_after_second)),
            ("resolved_first", Json::b(resolved_first)),
            ("after_both_p1", Json::s(after_both.0)),
            ("after_both_p2", Json::s(after_both.1)),
            ("namespaces_distinct", Json::b(namespaces_distinct)),
            ("first_ordinal", Json::i(first_ordinal)),
            ("hits_after_first_access", Json::i(hits_after_first_access)),
            ("second_ordinal", Json::i(second_ordinal)),
            (
                "hits_after_second_access",
                Json::i(hits_after_second_access),
            ),
            (
                "module_statuses",
                Json::arr(statuses.into_iter().map(Json::s).collect()),
            ),
        ]),
    )]
}

fn run_legacy_only_fatal() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_host_import_module_dynamically_callback(legacy_callback);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let _ = run_script(scope, "legacy-only.js", "import.defer('legacy-only')");
}

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    pin,
    phase_payload_and_lazy_evaluation,
    rejected_callback_promise,
    invalid_attributes_before_callback,
    repeated_delayed_settlement,
];

fn main() -> std::process::ExitCode {
    v8::V8::set_flags_from_string("--js-defer-import-eval --harmony-import-attributes");
    let args: Vec<_> = std::env::args().collect();
    if args.iter().any(|arg| arg == "--legacy-only-fatal") {
        #[cfg(windows)]
        suppress_windows_fatal_dialogs();
        run_legacy_only_fatal();
        return std::process::ExitCode::SUCCESS;
    }
    oracle::ensure_v8();
    let outcomes: Vec<_> = CHECKS.iter().flat_map(|check| check()).collect();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let failed = outcomes.len() - passed;
    let mut text = String::new();
    for outcome in &outcomes {
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    text.push_str(&summary_line(outcomes.len(), passed, failed));
    text.push('\n');
    print!("{text}");
    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
