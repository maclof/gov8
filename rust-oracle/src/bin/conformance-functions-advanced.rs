//! Advanced `v8::Function` characterization for the pinned `v8` 152.2.0
//! oracle. This deliberately complements (and does not repeat) the host
//! callback fixture, which already covers callback arguments/data, ordinary
//! receivers, basic call/construct/new-target behavior, re-entry, exceptions,
//! and callback performance.
//!
//! The public Rust surface characterized here is `FunctionBuilder` side-effect
//! policy, function names, source metadata, bound functions, and function code
//! cache. The crate does not bind V8's `Function::GetInferredName` or
//! `GetDebugName`/display-name APIs.

use std::cell::{Cell, RefCell};
use std::io::Write as _;
use std::process::ExitCode;
use std::rc::Rc;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

fn eval<'s>(scope: &mut v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let source = v8::String::new(scope, source)?;
    v8::Script::compile(scope, source, None)?.run(scope)
}

fn text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|value| value.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

fn eval_text(scope: &mut v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source)
        .map(|value| text(scope, value))
        .unwrap_or_default()
}

fn set_global<'s, V: Into<v8::Local<'s, v8::Value>>>(
    scope: &mut v8::PinScope<'s, '_>,
    context: v8::Local<'s, v8::Context>,
    name: &str,
    value: V,
) {
    let key = v8::String::new(scope, name).unwrap();
    context
        .global(scope)
        .set(scope, key.into(), value.into())
        .unwrap();
}

fn noop(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
}

fn names_and_bound_functions() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let native = v8::Function::new(scope, noop).unwrap();
    let native_initial = native.get_name(scope).to_rust_string_lossy(scope);
    native.set_name(v8::String::new(scope, "native-renamed").unwrap());
    set_global(scope, context, "nativeFn", native);

    let declared =
        v8::Local::<v8::Function>::try_from(eval(scope, "(function declaredName() {})").unwrap())
            .unwrap();
    let inferred = v8::Local::<v8::Function>::try_from(
        eval(
            scope,
            "globalThis.inferredSlot = function() {}; inferredSlot",
        )
        .unwrap(),
    )
    .unwrap();
    let bound = v8::Local::<v8::Function>::try_from(
        eval(
            scope,
            "globalThis.boundTarget = function target(a, b) { return this.tag + ':' + a + ':' + b; }; \
             boundTarget.bind({tag:'BOUND'}, 'A')",
        )
        .unwrap(),
    )
    .unwrap();
    let bound_before = bound.get_name(scope).to_rust_string_lossy(scope);
    bound.set_name(v8::String::new(scope, "ignored-on-bound").unwrap());
    set_global(scope, context, "boundFn", bound);

    let actual = Json::obj(vec![
        ("native_initial", Json::s(&native_initial)),
        (
            "native_after_set_name",
            Json::s(&native.get_name(scope).to_rust_string_lossy(scope)),
        ),
        (
            "native_js_name",
            Json::s(&eval_text(scope, "nativeFn.name")),
        ),
        (
            "declared_name",
            Json::s(&declared.get_name(scope).to_rust_string_lossy(scope)),
        ),
        (
            "assignment_inferred_name",
            Json::s(&inferred.get_name(scope).to_rust_string_lossy(scope)),
        ),
        ("bound_name", Json::s(&bound_before)),
        (
            "bound_set_name_is_noop",
            Json::s(&bound.get_name(scope).to_rust_string_lossy(scope)),
        ),
        ("bound_length", Json::s(&eval_text(scope, "boundFn.length"))),
        ("bound_call", Json::s(&eval_text(scope, "boundFn('B')"))),
        (
            "bound_host_call",
            Json::s(
                &bound
                    .call(
                        scope,
                        v8::Object::new(scope).into(),
                        &[v8::String::new(scope, "B").unwrap().into()],
                    )
                    .map(|value| text(scope, value))
                    .unwrap_or_default(),
            ),
        ),
    ]);
    let expected = Json::obj(vec![
        ("native_initial", Json::s("")),
        ("native_after_set_name", Json::s("native-renamed")),
        ("native_js_name", Json::s("native-renamed")),
        ("declared_name", Json::s("declaredName")),
        ("assignment_inferred_name", Json::s("")),
        ("bound_name", Json::s("bound target")),
        ("bound_set_name_is_noop", Json::s("bound target")),
        ("bound_length", Json::s("1")),
        ("bound_call", Json::s("BOUND:A:B")),
        ("bound_host_call", Json::s("BOUND:A:B")),
    ]);
    vec![expect_eq(
        "functions-advanced/names_and_bound",
        expected,
        actual,
    )]
}

fn direct_builder_constructor_behavior() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let concise = v8::Function::builder(noop)
        .constructor_behavior(v8::ConstructorBehavior::Throw)
        .build(scope)
        .unwrap();
    concise.set_name(v8::String::new(scope, "DirectConcise").unwrap());
    set_global(scope, context, "directConcise", concise);
    let regular = v8::Function::new(scope, noop).unwrap();
    regular.set_name(v8::String::new(scope, "DirectRegular").unwrap());
    set_global(scope, context, "directRegular", regular);

    let actual = Json::obj(vec![
        (
            "concise_prototype",
            Json::s(&eval_text(scope, "typeof directConcise.prototype")),
        ),
        (
            "concise_call",
            Json::s(&eval_text(scope, "String(directConcise())")),
        ),
        (
            "concise_construct",
            Json::s(&eval_text(
                scope,
                "try { new directConcise(); 'survived' } catch (e) { e.toString() }",
            )),
        ),
        (
            "regular_prototype",
            Json::s(&eval_text(scope, "typeof directRegular.prototype")),
        ),
        (
            "regular_construct",
            Json::s(&eval_text(
                scope,
                "new directRegular() instanceof directRegular",
            )),
        ),
    ]);
    let expected = Json::obj(vec![
        ("concise_prototype", Json::s("undefined")),
        ("concise_call", Json::s("undefined")),
        (
            "concise_construct",
            Json::s("TypeError: directConcise is not a constructor"),
        ),
        ("regular_prototype", Json::s("object")),
        ("regular_construct", Json::s("true")),
    ]);
    vec![expect_eq(
        "functions-advanced/direct_builder_constructor_behavior",
        expected,
        actual,
    )]
}

fn origin<'s>(
    scope: &v8::PinScope<'s, '_>,
    resource: &str,
    line_offset: i32,
    column_offset: i32,
    source_map: Option<&str>,
) -> v8::ScriptOrigin<'s> {
    let resource: v8::Local<v8::Value> = v8::String::new(scope, resource).unwrap().into();
    let source_map = source_map.map(|url| v8::String::new(scope, url).unwrap().into());
    v8::ScriptOrigin::new(
        scope,
        resource,
        line_offset,
        column_offset,
        true,
        777,
        source_map,
        true,
        false,
        false,
        None,
    )
}

fn origin_value_text(
    scope: &mut v8::PinScope<'_, '_>,
    value: Option<v8::Local<'_, v8::Value>>,
) -> String {
    value
        .map(|value| text(scope, value))
        .unwrap_or_else(|| "<none>".to_owned())
}

fn script_metadata() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let scripted_origin = origin(scope, "origin-file.js", 10, 20, Some("origin-file.map"));
    let source = v8::String::new(scope, "\n\n(function originFunction(){ return 1; })").unwrap();
    let script = v8::Script::compile(scope, source, Some(&scripted_origin)).unwrap();
    let function = v8::Local::<v8::Function>::try_from(script.run(scope).unwrap()).unwrap();
    let function_origin = function.get_script_origin(scope);
    let script_id = script.script_id();

    let source_url_source = v8::String::new(
        scope,
        "(function sourceUrlFunction(){})\n//# sourceURL=virtual-source.js",
    )
    .unwrap();
    let source_url_script = v8::Script::compile(scope, source_url_source, None).unwrap();
    let source_url_function =
        v8::Local::<v8::Function>::try_from(source_url_script.run(scope).unwrap()).unwrap();
    let source_url_origin = source_url_function.get_script_origin(scope);

    let native = v8::Function::new(scope, noop).unwrap();
    let native_origin = native.get_script_origin(scope);

    let actual = Json::obj(vec![
        (
            "line",
            Json::i(function.get_script_line_number().map_or(-1, i64::from)),
        ),
        (
            "column",
            Json::i(function.get_script_column_number().map_or(-1, i64::from)),
        ),
        (
            "function_id_matches_script",
            Json::b(function.script_id() == script_id),
        ),
        (
            "origin_id_matches_function",
            Json::b(function_origin.script_id() == function.script_id()),
        ),
        (
            "resource_name",
            Json::s(&origin_value_text(scope, function_origin.resource_name())),
        ),
        (
            "source_map_url",
            Json::s(&origin_value_text(scope, function_origin.source_map_url())),
        ),
        (
            "source_url_resource_name",
            Json::s(&origin_value_text(scope, source_url_origin.resource_name())),
        ),
        (
            "source_url_id_matches_script",
            Json::b(source_url_origin.script_id() == source_url_script.script_id()),
        ),
        (
            "native_line",
            Json::i(native.get_script_line_number().map_or(-1, i64::from)),
        ),
        (
            "native_column",
            Json::i(native.get_script_column_number().map_or(-1, i64::from)),
        ),
        ("native_script_id", Json::i(i64::from(native.script_id()))),
        (
            "native_resource_name",
            Json::s(&origin_value_text(scope, native_origin.resource_name())),
        ),
        (
            "native_source_map_url",
            Json::s(&origin_value_text(scope, native_origin.source_map_url())),
        ),
    ]);
    let expected = Json::obj(vec![
        ("line", Json::i(12)),
        ("column", Json::i(24)),
        ("function_id_matches_script", Json::b(true)),
        ("origin_id_matches_function", Json::b(true)),
        ("resource_name", Json::s("origin-file.js")),
        ("source_map_url", Json::s("origin-file.map")),
        ("source_url_resource_name", Json::s("virtual-source.js")),
        ("source_url_id_matches_script", Json::b(true)),
        ("native_line", Json::i(-1)),
        ("native_column", Json::i(-1)),
        ("native_script_id", Json::i(0)),
        ("native_resource_name", Json::s("<none>")),
        ("native_source_map_url", Json::s("<none>")),
    ]);
    vec![expect_eq(
        "functions-advanced/script_metadata",
        expected,
        actual,
    )]
}

fn bound_construct_semantics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let bound = v8::Local::<v8::Function>::try_from(
        eval(
            scope,
            "globalThis.Base = function Base(a,b) { this.args = a + ':' + b; this.nt = new.target.name; }; \
             globalThis.Bound = Base.bind({ignored:true}, 'A'); Bound",
        )
        .unwrap(),
    )
    .unwrap();
    let host_instance = bound
        .new_instance(scope, &[v8::String::new(scope, "B").unwrap().into()])
        .unwrap();
    set_global(scope, context, "hostBoundInstance", host_instance);

    let actual = Json::obj(vec![
        (
            "js_construct",
            Json::s(&eval_text(scope, "let x = new Bound('B'); x.args + ':' + x.nt")),
        ),
        (
            "host_construct",
            Json::s(&eval_text(
                scope,
                "hostBoundInstance.args + ':' + hostBoundInstance.nt",
            )),
        ),
        (
            "instanceof_target",
            Json::s(&eval_text(scope, "hostBoundInstance instanceof Base")),
        ),
        (
            "instanceof_bound",
            Json::s(&eval_text(scope, "hostBoundInstance instanceof Bound")),
        ),
        (
            "reflect_custom_new_target",
            Json::s(&eval_text(
                scope,
                "function Alternate(){}; let y = Reflect.construct(Bound, ['B'], Alternate); y.args + ':' + y.nt + ':' + (Object.getPrototypeOf(y) === Alternate.prototype)",
            )),
        ),
    ]);
    let expected = Json::obj(vec![
        ("js_construct", Json::s("A:B:Base")),
        ("host_construct", Json::s("A:B:Base")),
        ("instanceof_target", Json::s("true")),
        ("instanceof_bound", Json::s("true")),
        ("reflect_custom_new_target", Json::s("A:B:Alternate:true")),
    ]);
    vec![expect_eq(
        "functions-advanced/bound_construct_semantics",
        expected,
        actual,
    )]
}

thread_local! {
    static SIDE_EFFECT_CALLS: Cell<u32> = const { Cell::new(0) };
}

fn side_effect_callback(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    SIDE_EFFECT_CALLS.with(|calls| calls.set(calls.get() + 1));
    rv.set_int32(7);
}

fn receiver_side_effect_callback(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    SIDE_EFFECT_CALLS.with(|calls| calls.set(calls.get() + 1));
    let key = v8::String::new(scope, "touched").unwrap();
    args.this()
        .set(scope, key.into(), v8::Boolean::new(scope, true).into())
        .unwrap();
    rv.set_int32(9);
}

#[derive(Clone)]
struct ResponseChannel(Rc<RefCell<Vec<String>>>);

impl v8::inspector::ChannelImpl for ResponseChannel {
    fn send_response(&self, _call_id: i32, message: v8::UniquePtr<v8::inspector::StringBuffer>) {
        self.0
            .borrow_mut()
            .push(message.unwrap().string().to_string());
    }

    fn send_notification(&self, _message: v8::UniquePtr<v8::inspector::StringBuffer>) {}

    fn flush_protocol_notifications(&self) {}
}

struct InspectorClient;
impl v8::inspector::V8InspectorClientImpl for InspectorClient {}

fn inspector_eval(
    session: &v8::inspector::V8InspectorSession,
    responses: &Rc<RefCell<Vec<String>>>,
    id: i32,
    expression: &str,
) -> String {
    let request = format!(
        "{{\"id\":{id},\"method\":\"Runtime.evaluate\",\"params\":{{\"expression\":\"{expression}\",\"contextId\":1,\"throwOnSideEffect\":true,\"returnByValue\":true}}}}"
    );
    session.dispatch_protocol_message(v8::inspector::StringView::from(request.as_bytes()));
    responses.borrow_mut().pop().expect("inspector response")
}

fn side_effect_policies() -> Vec<CheckOutcome> {
    SIDE_EFFECT_CALLS.with(|calls| calls.set(0));
    let isolate = &mut v8::Isolate::new(Default::default());
    let inspector_client = v8::inspector::V8InspectorClient::new(Box::new(InspectorClient));
    let inspector = v8::inspector::V8Inspector::create(isolate, inspector_client);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let empty = v8::inspector::StringView::from(&b""[..]);
    let aux_data = v8::inspector::StringView::from(&b"{\"isDefault\":true}"[..]);
    inspector.context_created(context, 1, empty, aux_data);
    let responses = Rc::new(RefCell::new(Vec::new()));
    let channel = ResponseChannel(Rc::clone(&responses));
    let session = inspector.connect(
        1,
        v8::inspector::Channel::new(Box::new(channel)),
        v8::inspector::StringView::from(&b"{}"[..]),
        v8::inspector::V8InspectorClientTrustLevel::Untrusted,
    );

    let regular = v8::Function::builder(side_effect_callback)
        .side_effect_type(v8::SideEffectType::HasSideEffect)
        .build(scope)
        .unwrap();
    let no_effect = v8::Function::builder(side_effect_callback)
        .side_effect_type(v8::SideEffectType::HasNoSideEffect)
        .build(scope)
        .unwrap();
    let template = v8::FunctionTemplate::builder(side_effect_callback)
        .side_effect_type(v8::SideEffectType::HasNoSideEffect)
        .build(scope);
    let template_function = template.get_function(scope).unwrap();
    let receiver_effect = v8::Function::builder(receiver_side_effect_callback)
        .side_effect_type(v8::SideEffectType::HasSideEffectToReceiver)
        .build(scope)
        .unwrap();
    set_global(scope, context, "regularEffect", regular);
    set_global(scope, context, "declaredNoEffect", no_effect);
    set_global(scope, context, "templateNoEffect", template_function);
    set_global(scope, context, "receiverEffect", receiver_effect);
    eval(scope, "globalThis.persistentReceiver = {f: receiverEffect}").unwrap();

    let regular_response = inspector_eval(&session, &responses, 1, "regularEffect()");
    let after_regular = SIDE_EFFECT_CALLS.with(Cell::get);
    let no_effect_response = inspector_eval(&session, &responses, 2, "declaredNoEffect()");
    let after_no_effect = SIDE_EFFECT_CALLS.with(Cell::get);
    let template_response = inspector_eval(&session, &responses, 3, "templateNoEffect()");
    let after_template = SIDE_EFFECT_CALLS.with(Cell::get);
    let constructed_receiver_response = inspector_eval(
        &session,
        &responses,
        4,
        "(()=>{let o=new receiverEffect();return Number(o.touched)})()",
    );
    let after_constructed_receiver = SIDE_EFFECT_CALLS.with(Cell::get);
    let persistent_receiver_response =
        inspector_eval(&session, &responses, 5, "persistentReceiver.f()");
    let after_persistent_receiver = SIDE_EFFECT_CALLS.with(Cell::get);
    let normal_receiver_value = eval_text(scope, "new receiverEffect().touched");
    let after_normal_receiver = SIDE_EFFECT_CALLS.with(Cell::get);

    inspector.context_destroyed(context);
    let actual = Json::obj(vec![
        (
            "regular_rejected",
            Json::b(regular_response.contains("exceptionDetails")),
        ),
        ("regular_callback_calls", Json::i(i64::from(after_regular))),
        (
            "function_no_effect_allowed",
            Json::b(
                !no_effect_response.contains("exceptionDetails")
                    && no_effect_response.contains("\"value\":7"),
            ),
        ),
        (
            "function_no_effect_callback_calls",
            Json::i(i64::from(after_no_effect)),
        ),
        (
            "template_no_effect_allowed",
            Json::b(
                !template_response.contains("exceptionDetails")
                    && template_response.contains("\"value\":7"),
            ),
        ),
        (
            "template_no_effect_callback_calls",
            Json::i(i64::from(after_template)),
        ),
        (
            "receiver_effect_construct_rejected",
            Json::b(constructed_receiver_response.contains("exceptionDetails")),
        ),
        (
            "receiver_effect_construct_callback_calls",
            Json::i(i64::from(after_constructed_receiver)),
        ),
        (
            "receiver_effect_persistent_rejected",
            Json::b(persistent_receiver_response.contains("exceptionDetails")),
        ),
        (
            "receiver_effect_persistent_callback_calls",
            Json::i(i64::from(after_persistent_receiver)),
        ),
        (
            "receiver_effect_normal_value",
            Json::s(&normal_receiver_value),
        ),
        (
            "receiver_effect_normal_callback_calls",
            Json::i(i64::from(after_normal_receiver)),
        ),
    ]);
    let expected = Json::obj(vec![
        ("regular_rejected", Json::b(true)),
        ("regular_callback_calls", Json::i(0)),
        ("function_no_effect_allowed", Json::b(true)),
        ("function_no_effect_callback_calls", Json::i(1)),
        ("template_no_effect_allowed", Json::b(true)),
        ("template_no_effect_callback_calls", Json::i(2)),
        ("receiver_effect_construct_rejected", Json::b(true)),
        ("receiver_effect_construct_callback_calls", Json::i(2)),
        ("receiver_effect_persistent_rejected", Json::b(true)),
        ("receiver_effect_persistent_callback_calls", Json::i(2)),
        ("receiver_effect_normal_value", Json::s("true")),
        ("receiver_effect_normal_callback_calls", Json::i(3)),
    ]);
    vec![expect_eq(
        "functions-advanced/side_effect_policies",
        expected,
        actual,
    )]
}

fn function_code_cache_roundtrip() -> Vec<CheckOutcome> {
    const SOURCE: &str = "return left * 10 + right;";
    let cache_bytes = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let source_string = v8::String::new(scope, SOURCE).unwrap();
        let mut source = v8::script_compiler::Source::new(source_string, None);
        let left = v8::String::new(scope, "left").unwrap();
        let right = v8::String::new(scope, "right").unwrap();
        let function = v8::script_compiler::compile_function(
            scope,
            &mut source,
            &[left, right],
            &[],
            v8::script_compiler::CompileOptions::NoCompileOptions,
            v8::script_compiler::NoCacheReason::NoReason,
        )
        .unwrap();
        function
            .create_code_cache()
            .expect("CompileFunction result must be cacheable")
            .iter()
            .copied()
            .collect::<Vec<_>>()
    };

    let (compiled, rejected, value) = {
        let isolate = &mut v8::Isolate::new(Default::default());
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let cached = v8::script_compiler::CachedData::new(&cache_bytes);
        let source_string = v8::String::new(scope, SOURCE).unwrap();
        let mut source =
            v8::script_compiler::Source::new_with_cached_data(source_string, None, cached);
        let left = v8::String::new(scope, "left").unwrap();
        let right = v8::String::new(scope, "right").unwrap();
        let function = v8::script_compiler::compile_function(
            scope,
            &mut source,
            &[left, right],
            &[],
            v8::script_compiler::CompileOptions::ConsumeCodeCache,
            v8::script_compiler::NoCacheReason::NoReason,
        );
        let rejected = source
            .get_cached_data()
            .is_none_or(v8::script_compiler::CachedData::rejected);
        let value = function
            .and_then(|function| {
                function.call(
                    scope,
                    v8::undefined(scope).into(),
                    &[
                        v8::Integer::new(scope, 4).into(),
                        v8::Integer::new(scope, 2).into(),
                    ],
                )
            })
            .and_then(|value| value.integer_value(scope))
            .unwrap_or(-1);
        (function.is_some(), rejected, value)
    };

    let actual = Json::obj(vec![
        ("cache_non_empty", Json::b(!cache_bytes.is_empty())),
        ("consume_compiles", Json::b(compiled)),
        ("cache_rejected", Json::b(rejected)),
        ("call_value", Json::i(value)),
    ]);
    let expected = Json::obj(vec![
        ("cache_non_empty", Json::b(true)),
        ("consume_compiles", Json::b(true)),
        ("cache_rejected", Json::b(false)),
        ("call_value", Json::i(42)),
    ]);
    vec![expect_eq(
        "functions-advanced/code_cache_roundtrip",
        expected,
        actual,
    )]
}

fn checks() -> Vec<CheckOutcome> {
    let mut checks = Vec::new();
    checks.extend(names_and_bound_functions());
    checks.extend(direct_builder_constructor_behavior());
    checks.extend(script_metadata());
    checks.extend(bound_construct_semantics());
    checks.extend(side_effect_policies());
    checks.extend(function_code_cache_roundtrip());
    checks
}

fn main() -> ExitCode {
    oracle::ensure_v8();
    let checks = checks();
    let passed = checks.iter().filter(|check| check.passed()).count();
    let mut output = String::new();
    for check in &checks {
        output.push_str(&check.to_line());
        output.push('\n');
    }
    output.push_str(&summary_line(checks.len(), passed, checks.len() - passed));
    output.push('\n');
    let stdout = std::io::stdout();
    let mut stdout = stdout.lock();
    let _ = stdout.write_all(output.as_bytes());
    let _ = stdout.flush();
    if passed == checks.len() {
        ExitCode::SUCCESS
    } else {
        ExitCode::FAILURE
    }
}
