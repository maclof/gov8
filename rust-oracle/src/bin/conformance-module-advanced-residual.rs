//! Residual advanced ES-module conformance for pinned `v8` =152.2.0.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::sync::atomic::{AtomicUsize, Ordering};

static MODULE_RESOLVES: AtomicUsize = AtomicUsize::new(0);
static SOURCE_RESOLVES: AtomicUsize = AtomicUsize::new(0);
static IMPORT_META_CALLS: AtomicUsize = AtomicUsize::new(0);
static DYNAMIC_CALLS: AtomicUsize = AtomicUsize::new(0);
static DYNAMIC_PHASE_EVALUATION_CALLS: AtomicUsize = AtomicUsize::new(0);
static DYNAMIC_PHASE_SOURCE_CALLS: AtomicUsize = AtomicUsize::new(0);
static SHADOW_CALLS: AtomicUsize = AtomicUsize::new(0);

fn status(value: v8::ModuleStatus) -> &'static str {
    match value {
        v8::ModuleStatus::Uninstantiated => "Uninstantiated",
        v8::ModuleStatus::Instantiating => "Instantiating",
        v8::ModuleStatus::Instantiated => "Instantiated",
        v8::ModuleStatus::Evaluating => "Evaluating",
        v8::ModuleStatus::Evaluated => "Evaluated",
        v8::ModuleStatus::Errored => "Errored",
    }
}

fn promise_state(value: v8::PromiseState) -> &'static str {
    match value {
        v8::PromiseState::Pending => "Pending",
        v8::PromiseState::Fulfilled => "Fulfilled",
        v8::PromiseState::Rejected => "Rejected",
    }
}

fn origin<'s>(scope: &v8::PinScope<'s, '_>, name: &str) -> v8::ScriptOrigin<'s> {
    let name = v8::String::new(scope, name).unwrap().into();
    v8::ScriptOrigin::new(scope, name, 0, 0, false, -1, None, false, false, true, None)
}

fn compile<'s>(scope: &v8::PinScope<'s, '_>, name: &str, text: &str) -> v8::Local<'s, v8::Module> {
    let text = v8::String::new(scope, text).unwrap();
    let origin = origin(scope, name);
    let mut source = v8::script_compiler::Source::new(text, Some(&origin));
    v8::script_compiler::compile_module(scope, &mut source).unwrap()
}

fn eval<'s>(scope: &v8::PinScope<'s, '_>, text: &str) -> Option<v8::Local<'s, v8::Value>> {
    let text = v8::String::new(scope, text).unwrap();
    v8::Script::compile(scope, text, None)?.run(scope)
}

#[allow(clippy::unnecessary_wraps)]
fn unexpected_module<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    MODULE_RESOLVES.fetch_add(1, Ordering::SeqCst);
    None
}

fn resolve_source<'s>(
    context: v8::Local<'s, v8::Context>,
    specifier: v8::Local<'s, v8::String>,
    attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Object>> {
    SOURCE_RESOLVES.fetch_add(1, Ordering::SeqCst);
    v8::callback_scope!(unsafe scope, context);
    assert_eq!(specifier.to_rust_string_lossy(scope), "source-dep");
    assert_eq!(attributes.length(), 0);
    let global = std::rc::Rc::into_inner(context.remove_slot::<v8::Global<v8::Object>>()?)?;
    Some(v8::Local::new(scope, global))
}

fn throwing_source<'s>(
    context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Object>> {
    SOURCE_RESOLVES.fetch_add(1, Ordering::SeqCst);
    v8::callback_scope!(unsafe scope, context);
    let message = v8::String::new(scope, "source link boom").unwrap();
    let exception = v8::Exception::type_error(scope, message);
    scope.throw_exception(exception);
    None
}

fn source_phase_instantiate2() -> Vec<CheckOutcome> {
    MODULE_RESOLVES.store(0, Ordering::SeqCst);
    SOURCE_RESOLVES.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = compile(
        scope,
        "source-entry.mjs",
        "import source mod from 'source-dep'; export default mod;",
    );
    let request = module
        .get_module_requests()
        .get(scope, 0)
        .unwrap()
        .cast::<v8::ModuleRequest>();
    let location = module.source_offset_to_location(request.get_source_offset());
    let wasm = eval(
        scope,
        "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))",
    )
    .unwrap()
    .cast::<v8::Object>();
    context.set_slot(std::rc::Rc::new(v8::Global::new(scope, wasm)));
    let linked = module.instantiate_module2(scope, unexpected_module, resolve_source);
    let evaluated = module.evaluate(scope).unwrap().cast::<v8::Promise>();
    scope.perform_microtask_checkpoint();
    let namespace = module.get_module_namespace().cast::<v8::Object>();
    let default_key = v8::String::new(scope, "default").unwrap().into();
    let exported = namespace.get(scope, default_key).unwrap();
    vec![pass(
        "module-advanced-residual/instantiate2_source_phase",
        Json::obj(vec![
            ("request_phase", Json::s("Source")),
            (
                "request_line",
                Json::i(i64::from(location.get_line_number())),
            ),
            (
                "request_column",
                Json::i(i64::from(location.get_column_number())),
            ),
            ("linked", Json::b(linked == Some(true))),
            (
                "module_resolves",
                Json::i(MODULE_RESOLVES.load(Ordering::SeqCst) as i64),
            ),
            (
                "source_resolves",
                Json::i(SOURCE_RESOLVES.load(Ordering::SeqCst) as i64),
            ),
            ("promise_state", Json::s(promise_state(evaluated.state()))),
            ("status", Json::s(status(module.get_status()))),
            ("export_same", Json::b(exported.strict_equals(wasm.into()))),
        ]),
    )]
}

fn source_phase_exception() -> Vec<CheckOutcome> {
    SOURCE_RESOLVES.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = compile(scope, "source-error.mjs", "import source x from 'bad';");
    v8::tc_scope!(let tc, scope);
    let linked = module.instantiate_module2(tc, unexpected_module, throwing_source);
    let exception = tc
        .exception()
        .map_or(String::new(), |value| value.to_rust_string_lossy(tc));
    vec![pass(
        "module-advanced-residual/instantiate2_source_exception",
        Json::obj(vec![
            ("linked_none", Json::b(linked.is_none())),
            ("caught", Json::b(tc.has_caught())),
            ("exception", Json::s(&exception)),
            ("status", Json::s(status(module.get_status()))),
            (
                "source_resolves",
                Json::i(SOURCE_RESOLVES.load(Ordering::SeqCst) as i64),
            ),
        ]),
    )]
}

fn deferred_namespace() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval(scope, "globalThis.deferHits = 0").unwrap();
    let module = compile(
        scope,
        "deferred.mjs",
        "globalThis.deferHits++; export const answer = 42;",
    );
    let linked = module.instantiate_module(scope, unexpected_module);
    let before_status = status(module.get_status());
    let gathered = module.evaluate_for_import_defer(scope);
    let gathered_is_promise = gathered.is_some_and(|value| value.is_promise());
    let gathered_promise = gathered.map(|value| value.cast::<v8::Promise>());
    let gather_before = gathered_promise.map_or("None", |promise| promise_state(promise.state()));
    scope.perform_microtask_checkpoint();
    let gather_after = gathered_promise.map_or("None", |promise| promise_state(promise.state()));
    let after_gather_status = status(module.get_status());
    let hits_before = eval(scope, "deferHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let deferred = module.get_module_namespace_with_phase(v8::ModuleImportPhase::kDefer);
    let deferred_again = module.get_module_namespace_with_phase(v8::ModuleImportPhase::kDefer);
    let source_phase = module.get_module_namespace_with_phase(v8::ModuleImportPhase::kSource);
    let hits_after_namespace = eval(scope, "deferHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let deferred_object = deferred.cast::<v8::Object>();
    let key = v8::String::new(scope, "answer").unwrap().into();
    let answer = deferred_object
        .get(scope, key)
        .unwrap()
        .integer_value(scope)
        .unwrap();
    scope.perform_microtask_checkpoint();
    let hits_after_access = eval(scope, "deferHits")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let evaluation_namespace =
        module.get_module_namespace_with_phase(v8::ModuleImportPhase::kEvaluation);
    vec![pass(
        "module-advanced-residual/deferred_namespace",
        Json::obj(vec![
            ("linked", Json::b(linked == Some(true))),
            ("before_status", Json::s(before_status)),
            ("gathered_is_promise", Json::b(gathered_is_promise)),
            ("gather_state_before", Json::s(gather_before)),
            ("gather_state_after", Json::s(gather_after)),
            ("after_gather_status", Json::s(after_gather_status)),
            ("hits_before_namespace", Json::i(hits_before)),
            (
                "deferred_namespace_stable",
                Json::b(deferred.strict_equals(deferred_again)),
            ),
            ("source_phase_is_object", Json::b(source_phase.is_object())),
            (
                "source_phase_is_undefined",
                Json::b(source_phase.is_undefined()),
            ),
            (
                "source_phase_same_deferred",
                Json::b(source_phase.strict_equals(deferred)),
            ),
            ("hits_after_namespace", Json::i(hits_after_namespace)),
            ("answer", Json::i(answer)),
            ("hits_after_access", Json::i(hits_after_access)),
            ("after_access_status", Json::s(status(module.get_status()))),
            (
                "evaluation_namespace_same",
                Json::b(deferred.strict_equals(evaluation_namespace)),
            ),
        ]),
    )]
}

fn stalled_top_level_await() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let source = "await new Promise((_resolve, _reject) => {});";
    let module = compile(scope, "stalled-fixture.mjs", source);
    let linked = module.instantiate_module(scope, unexpected_module);
    let before = module.get_stalled_top_level_await_message(scope).len();
    let evaluation = module.evaluate(scope).unwrap().cast::<v8::Promise>();
    scope.perform_microtask_checkpoint();
    let stalled = module.get_stalled_top_level_await_message(scope);
    let (stalled_module, message) = stalled[0];
    let resource = message
        .get_script_resource_name(scope)
        .map_or(String::new(), |value| value.to_rust_string_lossy(scope));
    let source_line = message
        .get_source_line(scope)
        .map_or(String::new(), |value| value.to_rust_string_lossy(scope));
    vec![pass(
        "module-advanced-residual/stalled_top_level_await",
        Json::obj(vec![
            ("linked", Json::b(linked == Some(true))),
            ("before_count", Json::i(before as i64)),
            ("after_count", Json::i(stalled.len() as i64)),
            (
                "module_same",
                Json::b(stalled_module.get_identity_hash() == module.get_identity_hash()),
            ),
            ("status", Json::s(status(module.get_status()))),
            ("has_tla", Json::b(module.has_top_level_await())),
            ("graph_async", Json::b(module.is_graph_async())),
            ("promise_state", Json::s(promise_state(evaluation.state()))),
            (
                "message",
                Json::s(&message.get(scope).to_rust_string_lossy(scope)),
            ),
            (
                "line",
                message
                    .get_line_number(scope)
                    .map_or(Json::Null, |value| Json::i(value as i64)),
            ),
            ("resource", Json::s(&resource)),
            ("source_line", Json::s(&source_line)),
            (
                "start_position",
                Json::i(i64::from(message.get_start_position())),
            ),
            (
                "end_position",
                Json::i(i64::from(message.get_end_position())),
            ),
            ("start_column", Json::i(message.get_start_column() as i64)),
            ("end_column", Json::i(message.get_end_column() as i64)),
            (
                "wasm_function_index",
                Json::i(i64::from(message.get_wasm_function_index())),
            ),
            ("error_level", Json::i(i64::from(message.error_level()))),
            (
                "shared_cross_origin",
                Json::b(message.is_shared_cross_origin()),
            ),
            ("opaque", Json::b(message.is_opaque())),
        ]),
    )]
}

fn deferred_exception() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = compile(
        scope,
        "deferred-error.mjs",
        "throw new RangeError('deferred boom'); export const answer = 1;",
    );
    let linked = module.instantiate_module(scope, unexpected_module);
    let gathered = module
        .evaluate_for_import_defer(scope)
        .unwrap()
        .cast::<v8::Promise>();
    let namespace = module
        .get_module_namespace_with_phase(v8::ModuleImportPhase::kDefer)
        .cast::<v8::Object>();
    v8::tc_scope!(let tc, scope);
    let key = v8::String::new(tc, "answer").unwrap().into();
    let value = namespace.get(tc, key);
    let exception = tc
        .exception()
        .map_or(String::new(), |value| value.to_rust_string_lossy(tc));
    vec![pass(
        "module-advanced-residual/deferred_exception",
        Json::obj(vec![
            ("linked", Json::b(linked == Some(true))),
            ("gather_state", Json::s(promise_state(gathered.state()))),
            ("property_none", Json::b(value.is_none())),
            ("caught", Json::b(tc.has_caught())),
            ("exception", Json::s(&exception)),
            ("status", Json::s(status(module.get_status()))),
            (
                "stored_exception_same",
                Json::b(
                    tc.exception()
                        .is_some_and(|value| value.strict_equals(module.get_exception())),
                ),
            ),
        ]),
    )]
}

fn stalled_tla_resolution_lifecycle() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let resolver = v8::PromiseResolver::new(scope).unwrap();
    let pending = resolver.get_promise(scope);
    let global = context.global(scope);
    let pending_key = v8::String::new(scope, "pendingFixture").unwrap().into();
    global.set(scope, pending_key, pending.into()).unwrap();
    let module = compile(
        scope,
        "settled-tla.mjs",
        "await globalThis.pendingFixture; export const done = 1;",
    );
    module.instantiate_module(scope, unexpected_module).unwrap();
    let evaluation = module.evaluate(scope).unwrap().cast::<v8::Promise>();
    scope.perform_microtask_checkpoint();
    let stalled_before = module.get_stalled_top_level_await_message(scope).len();
    resolver
        .resolve(scope, v8::undefined(scope).into())
        .unwrap();
    scope.perform_microtask_checkpoint();
    let stalled_after = module.get_stalled_top_level_await_message(scope).len();
    let namespace = module.get_module_namespace().cast::<v8::Object>();
    let done_key = v8::String::new(scope, "done").unwrap().into();
    let done = namespace
        .get(scope, done_key)
        .unwrap()
        .integer_value(scope)
        .unwrap();
    vec![pass(
        "module-advanced-residual/stalled_tla_resolution_lifecycle",
        Json::obj(vec![
            ("stalled_before", Json::i(stalled_before as i64)),
            ("promise_after", Json::s(promise_state(evaluation.state()))),
            ("stalled_after", Json::i(stalled_after as i64)),
            ("status", Json::s(status(module.get_status()))),
            ("done", Json::i(done)),
        ]),
    )]
}

extern "C" fn initialize_import_meta(
    context: v8::Local<v8::Context>,
    _module: v8::Local<v8::Module>,
    meta: v8::Local<v8::Object>,
) {
    IMPORT_META_CALLS.fetch_add(1, Ordering::SeqCst);
    v8::callback_scope!(unsafe scope, context);
    let key = v8::String::new(scope, "oracle").unwrap().into();
    let value = v8::Integer::new(scope, 42).into();
    meta.create_data_property(scope, key, value).unwrap();
}

fn import_meta_callback() -> Vec<CheckOutcome> {
    IMPORT_META_CALLS.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_host_initialize_import_meta_object_callback(initialize_import_meta);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = compile(
        scope,
        "meta.mjs",
        "export const value=import.meta.oracle; export const same=import.meta===import.meta;",
    );
    module.instantiate_module(scope, unexpected_module).unwrap();
    module.evaluate(scope).unwrap();
    scope.perform_microtask_checkpoint();
    let namespace = module.get_module_namespace().cast::<v8::Object>();
    let value_key = v8::String::new(scope, "value").unwrap().into();
    let same_key = v8::String::new(scope, "same").unwrap().into();
    let value = namespace
        .get(scope, value_key)
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let same = namespace.get(scope, same_key).unwrap().is_true();
    vec![pass(
        "module-advanced-residual/import_meta_callback",
        Json::obj(vec![
            (
                "calls",
                Json::i(IMPORT_META_CALLS.load(Ordering::SeqCst) as i64),
            ),
            ("value", Json::i(value)),
            ("same_within_module", Json::b(same)),
            ("status", Json::s(status(module.get_status()))),
        ]),
    )]
}

fn dynamic_import<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    _host_options: v8::Local<'s, v8::Data>,
    resource_name: v8::Local<'s, v8::Value>,
    specifier: v8::Local<'s, v8::String>,
    attributes: v8::Local<'s, v8::FixedArray>,
) -> Option<v8::Local<'s, v8::Promise>> {
    DYNAMIC_CALLS.fetch_add(1, Ordering::SeqCst);
    let _ = (resource_name, specifier, attributes);
    let resolver = v8::PromiseResolver::new(scope)?;
    let result = v8::String::new(scope, "dynamic-result").unwrap().into();
    resolver.resolve(scope, result)?;
    Some(resolver.get_promise(scope))
}

fn dynamic_import_with_phase<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    _host_options: v8::Local<'s, v8::Data>,
    resource_name: v8::Local<'s, v8::Value>,
    specifier: v8::Local<'s, v8::String>,
    phase: v8::ModuleImportPhase,
    attributes: v8::Local<'s, v8::FixedArray>,
) -> Option<v8::Local<'s, v8::Promise>> {
    let _ = (resource_name, specifier, attributes);
    let result = match phase {
        v8::ModuleImportPhase::kEvaluation => {
            DYNAMIC_PHASE_EVALUATION_CALLS.fetch_add(1, Ordering::SeqCst);
            v8::String::new(scope, "phase-evaluation-result")?.into()
        }
        v8::ModuleImportPhase::kSource => {
            DYNAMIC_PHASE_SOURCE_CALLS.fetch_add(1, Ordering::SeqCst);
            eval(
                scope,
                "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))",
            )?
        }
        v8::ModuleImportPhase::kDefer => return None,
    };
    let resolver = v8::PromiseResolver::new(scope)?;
    resolver.resolve(scope, result)?;
    Some(resolver.get_promise(scope))
}

fn dynamic_import_callbacks() -> Vec<CheckOutcome> {
    DYNAMIC_CALLS.store(0, Ordering::SeqCst);
    DYNAMIC_PHASE_EVALUATION_CALLS.store(0, Ordering::SeqCst);
    DYNAMIC_PHASE_SOURCE_CALLS.store(0, Ordering::SeqCst);
    let (old_state, old_result) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        isolate.set_host_import_module_dynamically_callback(dynamic_import);
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let promise = eval(scope, "import('dynamic-dep')")
            .unwrap()
            .cast::<v8::Promise>();
        scope.perform_microtask_checkpoint();
        (
            promise_state(promise.state()),
            promise.result(scope).to_rust_string_lossy(scope),
        )
    };
    let (evaluation_state, evaluation_result, source_state, source_is_wasm) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        isolate.set_host_import_module_dynamically_callback(dynamic_import);
        isolate.set_host_import_module_with_phase_dynamically_callback(dynamic_import_with_phase);
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let evaluation = eval(scope, "import('phase-evaluation')")
            .unwrap()
            .cast::<v8::Promise>();
        let source = eval(scope, "import.source('source-dynamic')")
            .unwrap()
            .cast::<v8::Promise>();
        scope.perform_microtask_checkpoint();
        (
            promise_state(evaluation.state()),
            evaluation.result(scope).to_rust_string_lossy(scope),
            promise_state(source.state()),
            source.result(scope).is_wasm_module_object(),
        )
    };
    vec![pass(
        "module-advanced-residual/dynamic_import_callbacks",
        Json::obj(vec![
            (
                "old_callback_calls",
                Json::i(DYNAMIC_CALLS.load(Ordering::SeqCst) as i64),
            ),
            ("old_state", Json::s(old_state)),
            ("old_result", Json::s(&old_result)),
            (
                "phase_evaluation_calls",
                Json::i(DYNAMIC_PHASE_EVALUATION_CALLS.load(Ordering::SeqCst) as i64),
            ),
            ("phase_evaluation_state", Json::s(evaluation_state)),
            ("phase_evaluation_result", Json::s(&evaluation_result)),
            (
                "phase_source_calls",
                Json::i(DYNAMIC_PHASE_SOURCE_CALLS.load(Ordering::SeqCst) as i64),
            ),
            ("phase_source_state", Json::s(source_state)),
            ("phase_source_is_wasm", Json::b(source_is_wasm)),
        ]),
    )]
}

fn create_shadow_context<'s, 'i>(
    scope: &mut v8::PinScope<'s, 'i>,
) -> Option<v8::Local<'s, v8::Context>> {
    SHADOW_CALLS.fetch_add(1, Ordering::SeqCst);
    let context = v8::Context::new(scope, Default::default());
    {
        let scope = &mut v8::ContextScope::new(scope, context);
        let key = v8::String::new(scope, "answer").unwrap().into();
        let value = v8::Integer::new(scope, 42).into();
        context.global(scope).set(scope, key, value).unwrap();
    }
    Some(context)
}

fn shadow_realm_callback() -> Vec<CheckOutcome> {
    SHADOW_CALLS.store(0, Ordering::SeqCst);
    let (without_callback_none, without_callback_caught, without_callback_exception) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        v8::tc_scope!(let tc, scope);
        let result = eval(tc, "new ShadowRealm()");
        (
            result.is_none(),
            tc.has_caught(),
            tc.exception()
                .map_or(String::new(), |value| value.to_rust_string_lossy(tc)),
        )
    };
    let isolate = &mut v8::Isolate::new(Default::default());
    isolate.set_host_create_shadow_realm_context_callback(create_shadow_context);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let value = eval(scope, "new ShadowRealm().evaluate('globalThis.answer')")
        .unwrap()
        .integer_value(scope)
        .unwrap();
    vec![pass(
        "module-advanced-residual/shadow_realm_callback",
        Json::obj(vec![
            ("without_callback_none", Json::b(without_callback_none)),
            ("without_callback_caught", Json::b(without_callback_caught)),
            (
                "without_callback_exception",
                Json::s(&without_callback_exception),
            ),
            ("calls", Json::i(SHADOW_CALLS.load(Ordering::SeqCst) as i64)),
            ("evaluated_value", Json::i(value)),
        ]),
    )]
}

fn panic_source<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Object>> {
    panic!("resolve source panic boundary")
}

fn run_panic_source() {
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let module = compile(scope, "panic-source.mjs", "import source x from 'bad';");
    let _ = module.instantiate_module2(scope, unexpected_module, panic_source);
}

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    source_phase_instantiate2,
    source_phase_exception,
    deferred_namespace,
    stalled_top_level_await,
    deferred_exception,
    stalled_tla_resolution_lifecycle,
    import_meta_callback,
    dynamic_import_callbacks,
    shadow_realm_callback,
];

fn main() -> std::process::ExitCode {
    v8::V8::set_flags_from_string("--js-source-phase-imports --harmony-shadow-realm");
    let args: Vec<_> = std::env::args().collect();
    if args.iter().any(|arg| arg == "--panic-resolve-source") {
        run_panic_source();
        return std::process::ExitCode::SUCCESS;
    }
    oracle::ensure_v8();
    let only = args.iter().find_map(|arg| arg.strip_prefix("--only="));
    let outcomes: Vec<_> = CHECKS
        .iter()
        .enumerate()
        .filter(|(index, _)| only.is_none_or(|value| value == index.to_string()))
        .flat_map(|(_, check)| check())
        .collect();
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
