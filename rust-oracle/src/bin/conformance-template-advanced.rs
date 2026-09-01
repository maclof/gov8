//! Advanced template/object host-interaction conformance slice for the
//! pinned `v8` crate. Complements the base `conformance` and
//! `conformance-host` slices with the template/object APIs they do not
//! cover. Excludes modules, Wasm, and the inspector (out of scope for this
//! slice per coordination).
//!
//! Characterized contract (the Go port must reproduce):
//! - Named/indexed property interceptors: the full handler family
//!   (getter, setter, query, deleter, enumerator, definer, descriptor),
//!   `Intercepted::kYes`/`kNo` fall-through semantics, callback `data`
//!   round-trip, `holder` identity, `should_throw_on_error` (observed
//!   false even for strict-mode stores in this build), `hasOwnProperty`
//!   consulting the getter/query as the existence probe, and the
//!   numeric-string canonicity of indexed keys.
//! - `PropertyHandlerFlags`: `ONLY_INTERCEPT_STRINGS` (symbol keys bypass
//!   the handler entirely), `NON_MASKING` (an existing own data property
//!   wins over the getter; absent properties are still intercepted), and
//!   `HAS_NO_SIDE_EFFECT` (handler still runs in normal execution; its
//!   allowlisting effect is only observable under debug-evaluate with
//!   `throwOnSideEffect`, i.e. requires the inspector, which this slice
//!   deliberately excludes).
//! - `ReturnValue`: `get()` reads back the value that was set (undefined
//!   when nothing was set), and every setter variant maps to the JS-visible
//!   value (`set_undefined`, `set_null`, `set_empty_string` -> `""`,
//!   `set_bool`, `set_uint32`, `set_double`).
//! - Signatures: a `Signature` restricts the receiver to objects created
//!   from the signature's FunctionTemplate or from templates inheriting
//!   from it; a wrong receiver throws `TypeError: Illegal invocation`.
//!   Builder `.length(n)` is observable as `fn.length`, `.data(v)` via
//!   `args.data()`.
//! - Intrinsic data properties: `set_intrinsic_data_property` binds the
//!   context's real intrinsic object (e.g. `Array.prototype`,
//!   `IteratorPrototype`) as a data property at instantiation, with
//!   `PropertyAttribute` applied (READ_ONLY is observable in the
//!   descriptor).
//! - Constructor behavior: `ConstructorBehavior::Throw` and
//!   `remove_prototype` produce functions without `.prototype` that reject
//!   `new` with `TypeError: <name> is not a constructor`; default templates
//!   have a writable `.prototype` whose `.constructor` is the function;
//!   `read_only_prototype` makes `.prototype` non-writable (sloppy-mode
//!   assignment silently fails).
//! - Template inheritance via `FunctionTemplate::inherit`: derived
//!   `.prototype.__proto__` is the base prototype, `instanceof` works for
//!   both constructors, prototype properties chain, but template-level
//!   statics do NOT inherit.
//! - Accessor properties: `ObjectTemplate::set_accessor_property` installs
//!   an accessor-SHAPED property (unlike the native data property of
//!   `set_accessor`), observable via the descriptor's function-valued
//!   `get`/`set` fields and `PropertyAttribute` (DONT_ENUM).
//! - Access checks: the pinned crate does NOT bind V8 C++
//!   `ObjectTemplate::SetAccessCheckCallback` (`v8/include/v8-template.h`
//!   line 1160 vs. absent from `src/template.rs`); the observable surface
//!   is the context security-token API. Each context's DEFAULT token is
//!   its own global object (`api.cc`: `UseDefaultSecurityToken` sets
//!   `env->global_object()`), so fresh contexts mutually distrust each
//!   other: touching another context's global proxy throws
//!   `TypeError: no access`, while bridged plain objects (no access-check
//!   info) stay readable across tokens. Sharing one token value re-enables
//!   access; `use_default_security_token` restores the own-global token.
//! - Internal-field/aligned-pointer boundaries beyond the host slice:
//!   zero-count templates, the count frozen by the FIRST instantiation
//!   (later template re-sets affect neither existing nor future
//!   instances), crate-level rejection of
//!   impossible counts, both ends of the valid `EmbedderDataTypeTag` range
//!   (`0` and `14`), tag re-targeting, null aligned pointers, and mixed
//!   aligned/Data field usage on one object.
//! - `ObjectTemplate::set_call_as_function_handler`: instances become
//!   callable (`typeof` "function"), construct calls dispatch to the SAME
//!   handler with `is_construct_call() == true` and deliver even a
//!   primitive return value, and `set_immutable_proto` makes
//!   `setPrototypeOf` THROW (`Immutable prototype object ... cannot have
//!   their prototype set`) rather than silently fail.
//!
//! Everything is normalized per `src/json.rs` rules: no addresses, no
//! timings; object identity is recorded only through `get_hash()`
//! comparisons. The runner emits the same JSON-lines protocol as the base
//! and host slices. No platform shutdown, so the fixture can be verified
//! in-process or by re-running the binary. Checks live in this binary
//! because the shared `src/checks` registries must not be modified by this
//! slice. Fatal/panic boundaries that must not run here are characterized
//! out-of-process by `tests/template_advanced_negative.rs`.

use std::cell::Cell;
use std::cell::RefCell;
use std::ffi::c_void;
use std::io::Write as _;
use std::process::ExitCode;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Compiles and runs `source`, returning the completion value.
fn eval<'s>(scope: &mut v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let src = v8::String::new(scope, source)?;
    v8::Script::compile(scope, src, None)?.run(scope)
}

/// Compiles, runs and ToString's `source` ("" on failure).
fn eval_text(scope: &mut v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source)
        .and_then(|v| v.to_string(scope))
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

/// ToString of an arbitrary value ("" when conversion fails).
fn value_text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

/// Runs `source` in a fresh TryCatch and returns the ToString of the
/// completion value, or the exception message ("TypeError: ..." etc.) when
/// the script threw. Deterministic for the pinned build.
fn eval_caught(scope: &mut v8::PinScope<'_, '_>, source: &str) -> String {
    let src = v8::String::new(scope, source).unwrap();
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    match v8::Script::compile(tc, src, None).and_then(|s| s.run(tc)) {
        Some(v) => value_text(tc, v),
        None => tc
            .message()
            .map(|m| m.get(tc).to_rust_string_lossy(tc))
            .unwrap_or_default(),
    }
}

// Deterministic per-check callback log: entries are short normalized
// strings pushed by native callbacks and joined with `;` for the report.
// Single-threaded check execution makes this deterministic.
thread_local! {
    static LOG: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
    /// Identity hash of the object the current check compares `holder()`
    /// / `this()` against. 0 means "no comparison configured".
    static EXPECTED_HASH: Cell<u32> = const { Cell::new(0) };
}

fn log_clear() {
    LOG.with(|l| l.borrow_mut().clear());
}

fn log_push(entry: String) {
    LOG.with(|l| l.borrow_mut().push(entry));
}

fn log_join() -> String {
    LOG.with(|l| l.borrow_mut().join(";"))
}

/// ToString of a `Local<Name>` key ("" when conversion fails).
fn key_text(scope: &mut v8::PinScope<'_, '_>, key: v8::Local<'_, v8::Name>) -> String {
    value_text(scope, key.into())
}

fn hash_matches(object: v8::Local<'_, v8::Object>) -> bool {
    let expected = EXPECTED_HASH.with(Cell::get);
    expected != 0 && object.get_hash() == expected
}

fn cb_noop(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
}

fn cb_return_int(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    rv.set_int32(3);
}

fn cb_return_five(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    rv.set_int32(5);
}

/// Publishes `value` as a property on `context`'s global object.
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

// ---------------------------------------------------------------------------
// 1. named interceptor: getter + setter
// ---------------------------------------------------------------------------

fn named_getter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<'_, v8::Name>,
    args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) -> v8::Intercepted {
    let name = key_text(scope, key);
    if name == "in_a" || name == "in_b" {
        // "in_a" -> "A", "in_b" -> "B".
        let upper = name.to_uppercase();
        let mark = upper.strip_prefix("IN_").unwrap_or(&upper).to_owned();
        rv.set(v8::String::new(scope, &mark).unwrap().into());
        log_push(format!(
            "get:{name}:yes:holder={}:{}:data={}",
            hash_matches(args.holder()),
            args.should_throw_on_error(),
            value_text(scope, args.data()),
        ));
        v8::Intercepted::kYes
    } else {
        log_push(format!("get:{name}:no"));
        v8::Intercepted::kNo
    }
}

fn named_setter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<'_, v8::Name>,
    value: v8::Local<'_, v8::Value>,
    args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Boolean>,
) -> v8::Intercepted {
    let name = key_text(scope, key);
    if name.starts_with("in_") {
        rv.set_bool(true);
        log_push(format!(
            "set:{name}:{}:strict={}",
            value.integer_value(scope).unwrap_or(-1),
            args.should_throw_on_error(),
        ));
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

#[allow(clippy::too_many_lines)]
fn named_interceptor_get_set() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    // A real template property: under the default (masking) flags the
    // getter is consulted first and only falls through via kNo.
    ot.set(
        v8::String::new(scope, "real").unwrap().into(),
        v8::String::new(scope, "R").unwrap().into(),
    );
    ot.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new()
            .getter(named_getter)
            .setter(named_setter)
            .data(v8::Integer::new(scope, 77).into()),
    );
    let obj = ot.new_instance(scope).unwrap();
    EXPECTED_HASH.with(|c| c.set(obj.get_hash()));
    set_global(scope, context, "o", obj);

    let real = eval_text(scope, "o.real");
    let intercepted = eval_text(scope, "o.in_a");
    let missing = eval_text(scope, "o.missing");
    let assignment_value = eval_text(scope, "(o.in_a = 11)");
    let still_intercepted = eval_text(scope, "o.in_a");
    let own_in_a = eval_text(scope, "Object.prototype.hasOwnProperty.call(o, 'in_a')");
    // A kNo setter fall-through creates a real own property.
    let fallback_assignment = eval_text(scope, "(o.plain_new = 42)");
    let fallback_read = eval_text(scope, "o.plain_new");
    let own_fallback = eval_text(
        scope,
        "Object.prototype.hasOwnProperty.call(o, 'plain_new')",
    );
    // Strict mode: the assignment still routes to the setter; observe that
    // should_throw_on_error does NOT flip (pinned build behavior) and the
    // IIFE itself evaluates to undefined.
    let strict_assignment = eval_caught(scope, "(() => { 'use strict'; o.in_b = 12; })()");
    // Existence queries: with no query handler installed, hasOwnProperty
    // reports the interceptor's names as existing only once the setter has
    // run for them (pinned below); a name the handler never intercepts is
    // not reported.
    let own_never_set = eval_text(
        scope,
        "Object.prototype.hasOwnProperty.call(o, 'never_set')",
    );
    let callback_log = log_join();

    let actual = Json::obj(vec![
        ("real", Json::s(&real)),
        ("intercepted", Json::s(&intercepted)),
        ("missing", Json::s(&missing)),
        ("assignment_value", Json::s(&assignment_value)),
        ("still_intercepted", Json::s(&still_intercepted)),
        ("own_in_a", Json::s(&own_in_a)),
        ("fallback_assignment", Json::s(&fallback_assignment)),
        ("fallback_read", Json::s(&fallback_read)),
        ("own_fallback", Json::s(&own_fallback)),
        ("strict_assignment", Json::s(&strict_assignment)),
        ("own_never_set", Json::s(&own_never_set)),
        ("callback_log", Json::s(&callback_log)),
    ]);
    let expected = Json::obj(vec![
        ("real", Json::s("R")),
        ("intercepted", Json::s("A")),
        ("missing", Json::s("undefined")),
        ("assignment_value", Json::s("11")),
        ("still_intercepted", Json::s("A")),
        ("own_in_a", Json::s("true")),
        ("fallback_assignment", Json::s("42")),
        ("fallback_read", Json::s("42")),
        ("own_fallback", Json::s("true")),
        ("strict_assignment", Json::s("undefined")),
        ("own_never_set", Json::s("false")),
        (
            "callback_log",
            Json::s(concat!(
                "get:real:no;",
                "get:in_a:yes:holder=true:false:data=77;",
                "get:missing:no;",
                "set:in_a:11:strict=false;",
                "get:in_a:yes:holder=true:false:data=77;",
                "get:in_a:yes:holder=true:false:data=77;",
                "get:plain_new:no;",
                "get:plain_new:no;",
                "get:plain_new:no;",
                "get:plain_new:no;",
                "set:in_b:12:strict=false;",
                "get:never_set:no"
            )),
        ),
    ]);
    vec![expect_eq(
        "tpladv/named_interceptor_get_set",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 2. named interceptor: query / deleter / enumerator / definer / descriptor
// ---------------------------------------------------------------------------

fn named_query(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Integer>,
) -> v8::Intercepted {
    if key_text(scope, key) == "q" {
        // READ_ONLY | DONT_ENUM == 3.
        let attr = v8::PropertyAttribute::READ_ONLY | v8::PropertyAttribute::DONT_ENUM;
        rv.set_int32(attr.as_u32() as i32);
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

fn named_deleter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Boolean>,
) -> v8::Intercepted {
    if key_text(scope, key) == "del" {
        rv.set_bool(false);
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

fn named_enumerator(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Array>,
) {
    // Non-Name elements are accepted on the NAMED path: V8 ToName-converts
    // each element (the Integer becomes "1"). The indexed path is stricter:
    // see indexed_enumerator below and tests/template_advanced_negative.rs.
    let names: Vec<v8::Local<v8::Value>> = vec![
        v8::Integer::new(scope, 1).into(),
        v8::String::new(scope, "a").unwrap().into(),
        v8::String::new(scope, "c").unwrap().into(),
        v8::String::new(scope, "b").unwrap().into(),
    ];
    let array = v8::Array::new_with_elements(scope, &names);
    rv.set(array);
}

fn named_definer(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<'_, v8::Name>,
    desc: &v8::PropertyDescriptor,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Boolean>,
) -> v8::Intercepted {
    let name = key_text(scope, key);
    if name == "def" {
        log_push(format!(
            "define:{name}:has_value={} value={} has_writable={} writable={} \
             has_enum={} enum={} has_conf={} conf={}",
            desc.has_value(),
            if desc.has_value() {
                value_text(scope, desc.value())
            } else {
                String::new()
            },
            desc.has_writable(),
            desc.writable(),
            desc.has_enumerable(),
            desc.enumerable(),
            desc.has_configurable(),
            desc.configurable(),
        ));
        rv.set_bool(true);
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

fn named_descriptor(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) -> v8::Intercepted {
    let name = key_text(scope, key);
    if name == "desc" {
        let obj = v8::Object::new(scope);
        let set_field = |name: &str, value: v8::Local<v8::Value>| {
            obj.set(scope, v8::String::new(scope, name).unwrap().into(), value)
                .unwrap();
        };
        set_field("value", v8::String::new(scope, "d-v").unwrap().into());
        set_field("writable", v8::Boolean::new(scope, false).into());
        set_field("enumerable", v8::Boolean::new(scope, true).into());
        set_field("configurable", v8::Boolean::new(scope, true).into());
        rv.set(obj.into());
        v8::Intercepted::kYes
    } else if name == "descnum" {
        // A plain Number is legal to set from Rust (ReturnValue<Value>);
        // V8 converts it into a value-only descriptor.
        rv.set(v8::Integer::new(scope, 7).into());
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

#[allow(clippy::too_many_lines)]
fn named_interceptor_query_delete_enum_define() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Query-only template: hasOwnProperty / propertyIsEnumerable consult it.
    let qt = v8::ObjectTemplate::new(scope);
    qt.set_named_property_handler(v8::NamedPropertyHandlerConfiguration::new().query(named_query));
    let q_obj = qt.new_instance(scope).unwrap();
    set_global(scope, context, "q_o", q_obj);
    let has_intercepted = eval_text(scope, "Object.prototype.hasOwnProperty.call(q_o, 'q')");
    let enumerable_intercepted = eval_text(
        scope,
        "Object.prototype.propertyIsEnumerable.call(q_o, 'q')",
    );
    let has_missing = eval_text(scope, "Object.prototype.hasOwnProperty.call(q_o, 'noq')");
    let value_without_getter = eval_text(scope, "q_o.q");

    // Deleter-only template.
    let dt = v8::ObjectTemplate::new(scope);
    dt.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new().deleter(named_deleter),
    );
    let d_obj = dt.new_instance(scope).unwrap();
    set_global(scope, context, "d_o", d_obj);
    let delete_intercepted = eval_text(scope, "(delete d_o.del)");
    let delete_fallback = eval_text(scope, "(delete d_o.other)");

    // Enumerator-only template: no real properties, keys come from the
    // returned Array in its order.
    let et = v8::ObjectTemplate::new(scope);
    et.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new().enumerator(named_enumerator),
    );
    let e_obj = et.new_instance(scope).unwrap();
    set_global(scope, context, "e_o", e_obj);
    let keys = eval_text(scope, "Object.keys(e_o).join(',')");
    let own_names = eval_text(scope, "Object.getOwnPropertyNames(e_o).join(',')");

    // Definer-only template.
    let ft = v8::ObjectTemplate::new(scope);
    ft.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new().definer(named_definer),
    );
    let def_obj = ft.new_instance(scope).unwrap();
    set_global(scope, context, "def_o", def_obj);
    let define_intercepted = eval_text(
        scope,
        "Object.defineProperty(def_o, 'def', {value: 42}) === def_o",
    );
    let define_fallback = eval_text(
        scope,
        "Object.defineProperty(def_o, 'other', {value: 1}) === def_o",
    );
    let fallback_stored = eval_text(scope, "def_o.other");
    let intercepted_not_stored = eval_text(scope, "def_o.def");
    let definer_log = log_join();

    // Descriptor-only template.
    let st = v8::ObjectTemplate::new(scope);
    st.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new().descriptor(named_descriptor),
    );
    let desc_obj = st.new_instance(scope).unwrap();
    set_global(scope, context, "desc_o", desc_obj);
    let descriptor = eval_text(
        scope,
        "JSON.stringify(Object.getOwnPropertyDescriptor(desc_o, 'desc'))",
    );
    let descriptor_missing = eval_text(scope, "Object.getOwnPropertyDescriptor(desc_o, 'nope')");
    let descriptor_type = eval_text(
        scope,
        "typeof Object.getOwnPropertyDescriptor(desc_o, 'desc')",
    );
    // A Number returned from the descriptor handler is converted into a
    // value-only descriptor (data-shaped, fully enumerable/configurable).
    let descriptor_number = eval_caught(
        scope,
        "JSON.stringify(Object.getOwnPropertyDescriptor(desc_o, 'descnum'))",
    );

    let actual = Json::obj(vec![
        ("has_intercepted", Json::s(&has_intercepted)),
        ("enumerable_intercepted", Json::s(&enumerable_intercepted)),
        ("has_missing", Json::s(&has_missing)),
        ("value_without_getter", Json::s(&value_without_getter)),
        ("delete_intercepted", Json::s(&delete_intercepted)),
        ("delete_fallback", Json::s(&delete_fallback)),
        ("keys", Json::s(&keys)),
        ("own_names", Json::s(&own_names)),
        ("define_intercepted", Json::s(&define_intercepted)),
        ("define_fallback", Json::s(&define_fallback)),
        ("fallback_stored", Json::s(&fallback_stored)),
        ("intercepted_not_stored", Json::s(&intercepted_not_stored)),
        ("definer_log", Json::s(&definer_log)),
        ("descriptor", Json::s(&descriptor)),
        ("descriptor_missing", Json::s(&descriptor_missing)),
        ("descriptor_type", Json::s(&descriptor_type)),
        ("descriptor_number", Json::s(&descriptor_number)),
    ]);
    let expected = Json::obj(vec![
        ("has_intercepted", Json::s("true")),
        ("enumerable_intercepted", Json::s("false")),
        ("has_missing", Json::s("false")),
        ("value_without_getter", Json::s("undefined")),
        ("delete_intercepted", Json::s("false")),
        ("delete_fallback", Json::s("true")),
        ("keys", Json::s("1,a,c,b")),
        ("own_names", Json::s("1,a,c,b")),
        ("define_intercepted", Json::s("true")),
        ("define_fallback", Json::s("true")),
        ("fallback_stored", Json::s("1")),
        ("intercepted_not_stored", Json::s("undefined")),
        (
            "definer_log",
            Json::s(concat!(
                "define:def:has_value=true value=42 has_writable=false writable=false ",
                "has_enum=false enum=false has_conf=false conf=false"
            )),
        ),
        (
            "descriptor",
            Json::s(
                "{\"value\":\"d-v\",\"writable\":false,\"enumerable\":true,\"configurable\":true}",
            ),
        ),
        ("descriptor_missing", Json::s("undefined")),
        ("descriptor_type", Json::s("object")),
        (
            "descriptor_number",
            Json::s("Uncaught TypeError: Property description must be an object: 7"),
        ),
    ]);
    vec![expect_eq(
        "tpladv/named_interceptor_query_delete_enum_define",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 3. indexed interceptor: full family
// ---------------------------------------------------------------------------

fn indexed_getter(
    _scope: &mut v8::PinScope<'_, '_>,
    index: u32,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) -> v8::Intercepted {
    if index == 42 {
        rv.set_int32(4242);
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

fn indexed_setter(
    scope: &mut v8::PinScope<'_, '_>,
    index: u32,
    value: v8::Local<'_, v8::Value>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Boolean>,
) -> v8::Intercepted {
    if index == 7 {
        rv.set_bool(true);
        log_push(format!("set:7:{}", value_text(scope, value)));
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

fn indexed_query(
    _scope: &mut v8::PinScope<'_, '_>,
    index: u32,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Integer>,
) -> v8::Intercepted {
    if index == 9 {
        // DONT_DELETE == 4.
        rv.set_int32(v8::PropertyAttribute::DONT_DELETE.as_u32() as i32);
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

fn indexed_deleter(
    _scope: &mut v8::PinScope<'_, '_>,
    index: u32,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Boolean>,
) -> v8::Intercepted {
    if index == 5 {
        rv.set_bool(false);
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

fn indexed_enumerator(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Array>,
) {
    // Deliberately out of ascending index order to expose whether V8
    // normalizes interceptor-provided element keys. Indexed enumerator
    // elements must be uint32-convertible values (Numbers, not Strings -
    // a String element CHECK-fails inside V8; see
    // tests/template_advanced_negative.rs).
    let names = [9_u32, 4, 0];
    let elements: Vec<v8::Local<v8::Value>> = names
        .iter()
        .map(|n| v8::Integer::new(scope, i32::try_from(*n).unwrap()).into())
        .collect();
    let array = v8::Array::new_with_elements(scope, &elements);
    rv.set(array);
}

#[allow(clippy::too_many_lines)]
fn indexed_interceptor_full_family() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    ot.set_indexed_property_handler(
        v8::IndexedPropertyHandlerConfiguration::new()
            .getter(indexed_getter)
            .setter(indexed_setter)
            .query(indexed_query)
            .deleter(indexed_deleter)
            .enumerator(indexed_enumerator),
    );
    let obj = ot.new_instance(scope).unwrap();
    set_global(scope, context, "io", obj);

    let get_intercepted = eval_text(scope, "io[42]");
    let get_missing = eval_text(scope, "io[43]");
    // Numeric strings canonicalize to the indexed handler.
    let get_numeric_string = eval_text(scope, "io['42']");
    // Non-index strings do not reach the indexed handler at all.
    let get_non_index_string = eval_text(scope, "io['43x']");
    let setter_intercepted = eval_text(scope, "(io[7] = 'x')");
    let setter_not_stored = eval_text(scope, "io[7]");
    let setter_log = log_join();
    // A kNo setter fall-through creates a real element.
    let fallback_assignment = eval_text(scope, "(io[8] = 8)");
    let fallback_read = eval_text(scope, "io[8]");
    let delete_intercepted = eval_text(scope, "(delete io[5])");
    let delete_fallback = eval_text(scope, "(delete io[6])");
    let has_intercepted = eval_text(scope, "Object.prototype.hasOwnProperty.call(io, 9)");
    let has_missing = eval_text(scope, "Object.prototype.hasOwnProperty.call(io, 10)");
    let value_without_getter = eval_text(scope, "io[9]");
    // Real element keys come first, then enumerator keys that survive the
    // enumerable filter (only the query-intercepted index 9 does; 4 and 0
    // have no query answer and are dropped).
    let keys = eval_text(scope, "Object.keys(io).join(',')");

    let actual = Json::obj(vec![
        ("get_intercepted", Json::s(&get_intercepted)),
        ("get_missing", Json::s(&get_missing)),
        ("get_numeric_string", Json::s(&get_numeric_string)),
        ("get_non_index_string", Json::s(&get_non_index_string)),
        ("setter_intercepted", Json::s(&setter_intercepted)),
        ("setter_not_stored", Json::s(&setter_not_stored)),
        ("setter_log", Json::s(&setter_log)),
        ("fallback_assignment", Json::s(&fallback_assignment)),
        ("fallback_read", Json::s(&fallback_read)),
        ("delete_intercepted", Json::s(&delete_intercepted)),
        ("delete_fallback", Json::s(&delete_fallback)),
        ("has_intercepted", Json::s(&has_intercepted)),
        ("has_missing", Json::s(&has_missing)),
        ("value_without_getter", Json::s(&value_without_getter)),
        ("keys", Json::s(&keys)),
    ]);
    let expected = Json::obj(vec![
        ("get_intercepted", Json::s("4242")),
        ("get_missing", Json::s("undefined")),
        ("get_numeric_string", Json::s("4242")),
        ("get_non_index_string", Json::s("undefined")),
        ("setter_intercepted", Json::s("x")),
        ("setter_not_stored", Json::s("undefined")),
        ("setter_log", Json::s("set:7:x")),
        ("fallback_assignment", Json::s("8")),
        ("fallback_read", Json::s("8")),
        ("delete_intercepted", Json::s("false")),
        ("delete_fallback", Json::s("true")),
        ("has_intercepted", Json::s("true")),
        ("has_missing", Json::s("false")),
        ("value_without_getter", Json::s("undefined")),
        ("keys", Json::s("8,9")),
    ]);
    vec![expect_eq(
        "tpladv/indexed_interceptor_full_family",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 4. property handler flags
// ---------------------------------------------------------------------------

fn flag_getter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) -> v8::Intercepted {
    if key.is_symbol() {
        rv.set(v8::String::new(scope, "SYM").unwrap().into());
        v8::Intercepted::kYes
    } else if key_text(scope, key) == "str" {
        rv.set(v8::String::new(scope, "S").unwrap().into());
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

fn masking_getter(
    scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) -> v8::Intercepted {
    rv.set(v8::String::new(scope, "G").unwrap().into());
    v8::Intercepted::kYes
}

fn flag_interceptors() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // ONLY_INTERCEPT_STRINGS: symbol-keyed lookups bypass the handler.
    let ot = v8::ObjectTemplate::new(scope);
    ot.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new()
            .getter(flag_getter)
            .flags(v8::PropertyHandlerFlags::ONLY_INTERCEPT_STRINGS),
    );
    let strings_only = ot.new_instance(scope).unwrap();
    set_global(scope, context, "strings_only", strings_only);
    let symbol = v8::Symbol::new(scope, Some(v8::String::new(scope, "s").unwrap()));
    set_global(scope, context, "sym", symbol);

    // Default flags: the handler sees symbol keys too.
    let ot2 = v8::ObjectTemplate::new(scope);
    ot2.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new().getter(flag_getter),
    );
    let all_keys = ot2.new_instance(scope).unwrap();
    set_global(scope, context, "all_keys", all_keys);

    let symbol_with_flag = eval_text(scope, "strings_only[sym]");
    let string_with_flag = eval_text(scope, "strings_only.str");
    let symbol_without_flag = eval_text(scope, "all_keys[sym]");
    let string_without_flag = eval_text(scope, "all_keys.str");

    // NON_MASKING: an existing own data property wins over the getter;
    // absent properties are still intercepted.
    let masked = v8::ObjectTemplate::new(scope);
    masked.set(
        v8::String::new(scope, "dup").unwrap().into(),
        v8::Integer::new(scope, 1).into(),
    );
    masked.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new().getter(masking_getter),
    );
    let masked_obj = masked.new_instance(scope).unwrap();
    set_global(scope, context, "masked", masked_obj);

    let non_masking = v8::ObjectTemplate::new(scope);
    non_masking.set(
        v8::String::new(scope, "dup").unwrap().into(),
        v8::Integer::new(scope, 1).into(),
    );
    non_masking.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new()
            .getter(masking_getter)
            .flags(v8::PropertyHandlerFlags::NON_MASKING),
    );
    let unmasked_obj = non_masking.new_instance(scope).unwrap();
    set_global(scope, context, "unmasked", unmasked_obj);

    let masking_real = eval_text(scope, "masked.dup");
    let masking_absent = eval_text(scope, "masked.absent");
    let non_masking_real = eval_text(scope, "unmasked.dup");
    let non_masking_absent = eval_text(scope, "unmasked.absent");

    // HAS_NO_SIDE_EFFECT: the handler still runs in normal execution; the
    // allowlisting itself is only observable under debug-evaluate
    // (inspector), which this slice excludes.
    let sfx = v8::ObjectTemplate::new(scope);
    sfx.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new()
            .getter(masking_getter)
            .flags(v8::PropertyHandlerFlags::HAS_NO_SIDE_EFFECT),
    );
    let sfx_obj = sfx.new_instance(scope).unwrap();
    set_global(scope, context, "sfx_o", sfx_obj);
    let no_side_effect_normal_mode = eval_text(scope, "sfx_o.anything");

    let actual = Json::obj(vec![
        ("symbol_with_flag", Json::s(&symbol_with_flag)),
        ("string_with_flag", Json::s(&string_with_flag)),
        ("symbol_without_flag", Json::s(&symbol_without_flag)),
        ("string_without_flag", Json::s(&string_without_flag)),
        ("masking_real", Json::s(&masking_real)),
        ("masking_absent", Json::s(&masking_absent)),
        ("non_masking_real", Json::s(&non_masking_real)),
        ("non_masking_absent", Json::s(&non_masking_absent)),
        (
            "no_side_effect_normal_mode",
            Json::s(&no_side_effect_normal_mode),
        ),
    ]);
    let expected = Json::obj(vec![
        ("symbol_with_flag", Json::s("undefined")),
        ("string_with_flag", Json::s("S")),
        ("symbol_without_flag", Json::s("SYM")),
        ("string_without_flag", Json::s("S")),
        ("masking_real", Json::s("G")),
        ("masking_absent", Json::s("G")),
        ("non_masking_real", Json::s("1")),
        ("non_masking_absent", Json::s("G")),
        ("no_side_effect_normal_mode", Json::s("G")),
    ]);
    vec![expect_eq("tpladv/flag_interceptors", expected, actual)]
}

// ---------------------------------------------------------------------------
// 5. ReturnValue.Get and the setter variants
// ---------------------------------------------------------------------------

fn rv_specials(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let mode = args.get(0).integer_value(scope).unwrap_or(-1);
    match mode {
        0 => rv.set_undefined(),
        1 => rv.set_null(),
        2 => rv.set_empty_string(),
        3 => rv.set_bool(true),
        4 => rv.set_uint32(4_294_967_295),
        5 => rv.set_double(2.5),
        _ => {}
    }
}

fn rv_get_probe(
    scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let before = rv.get(scope);
    log_push(format!("before_undefined={}", before.is_undefined()));
    rv.set_int32(7);
    let after = rv.get(scope);
    log_push(format!(
        "after_number={} value={}",
        after.is_number(),
        after.number_value(scope).map(|n| n as i64).unwrap_or(-1),
    ));
}

fn acc_get_probe(
    scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    let before = rv.get(scope);
    log_push(format!("acc_before_undefined={}", before.is_undefined()));
    rv.set(v8::String::new(scope, "acc-v").unwrap().into());
    let after = rv.get(scope);
    log_push(format!(
        "acc_after_same={}",
        after.strict_equals(v8::String::new(scope, "acc-v").unwrap().into())
    ));
}

fn interceptor_get_probe(
    scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) -> v8::Intercepted {
    let before = rv.get(scope);
    log_push(format!("int_before_undefined={}", before.is_undefined()));
    rv.set(v8::String::new(scope, "g").unwrap().into());
    let after = rv.get(scope);
    log_push(format!(
        "int_after_same={}",
        after.strict_equals(v8::String::new(scope, "g").unwrap().into())
    ));
    v8::Intercepted::kYes
}

#[allow(clippy::too_many_lines)]
fn return_value_get_and_specials() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let f_specials = v8::Function::builder(rv_specials).build(scope).unwrap();
    set_global(scope, context, "rv_specials", f_specials);
    // JSON.stringify distinguishes undefined ("undefined") from the empty
    // string ("\"\"") and null ("null").
    let undefined_out = eval_text(scope, "String(JSON.stringify(rv_specials(0)))");
    let null_out = eval_text(scope, "String(JSON.stringify(rv_specials(1)))");
    let empty_string_out = eval_text(scope, "String(JSON.stringify(rv_specials(2)))");
    let bool_out = eval_text(scope, "String(JSON.stringify(rv_specials(3)))");
    let uint32_out = eval_text(scope, "String(JSON.stringify(rv_specials(4)))");
    let double_out = eval_text(scope, "String(JSON.stringify(rv_specials(5)))");
    let unset_out = eval_text(scope, "String(JSON.stringify(rv_specials(9)))");

    let f_get = v8::Function::builder(rv_get_probe).build(scope).unwrap();
    set_global(scope, context, "rv_get", f_get);
    let get_probe_value = eval_text(scope, "rv_get()");

    let ot = v8::ObjectTemplate::new(scope);
    ot.set_accessor(v8::String::new(scope, "p").unwrap().into(), acc_get_probe);
    let acc_obj = ot.new_instance(scope).unwrap();
    set_global(scope, context, "acc_o", acc_obj);
    let accessor_value = eval_text(scope, "acc_o.p");

    let ot2 = v8::ObjectTemplate::new(scope);
    ot2.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new().getter(interceptor_get_probe),
    );
    let int_obj = ot2.new_instance(scope).unwrap();
    set_global(scope, context, "int_o", int_obj);
    let interceptor_value = eval_text(scope, "int_o.k");

    let callback_log = log_join();

    let actual = Json::obj(vec![
        ("undefined_out", Json::s(&undefined_out)),
        ("null_out", Json::s(&null_out)),
        ("empty_string_out", Json::s(&empty_string_out)),
        ("bool_out", Json::s(&bool_out)),
        ("uint32_out", Json::s(&uint32_out)),
        ("double_out", Json::s(&double_out)),
        ("unset_out", Json::s(&unset_out)),
        ("get_probe_value", Json::s(&get_probe_value)),
        ("accessor_value", Json::s(&accessor_value)),
        ("interceptor_value", Json::s(&interceptor_value)),
        ("callback_log", Json::s(&callback_log)),
    ]);
    let expected = Json::obj(vec![
        ("undefined_out", Json::s("undefined")),
        ("null_out", Json::s("null")),
        ("empty_string_out", Json::s("\"\"")),
        ("bool_out", Json::s("true")),
        ("uint32_out", Json::s("4294967295")),
        ("double_out", Json::s("2.5")),
        ("unset_out", Json::s("undefined")),
        ("get_probe_value", Json::s("7")),
        ("accessor_value", Json::s("acc-v")),
        ("interceptor_value", Json::s("g")),
        (
            "callback_log",
            Json::s(concat!(
                "before_undefined=true;after_number=true value=7;",
                "acc_before_undefined=true;acc_after_same=true;",
                "int_before_undefined=true;int_after_same=true"
            )),
        ),
    ]);
    vec![expect_eq(
        "tpladv/return_value_get_and_specials",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 6. signatures: receiver enforcement
// ---------------------------------------------------------------------------

fn signature_method(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    log_push(format!(
        "call:args={} data={} this_ok={}",
        args.length(),
        value_text(scope, args.data()),
        hash_matches(args.this()),
    ));
    rv.set(v8::String::new(scope, "ok").unwrap().into());
}

#[allow(clippy::too_many_lines)]
fn signature_receiver_enforcement() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let base_ft = v8::FunctionTemplate::new(scope, cb_noop);
    base_ft.set_class_name(v8::String::new(scope, "Gov8SigBase").unwrap());
    let signature = v8::Signature::new(scope, base_ft);
    let data = v8::String::new(scope, "sig-data").unwrap();
    let method_ft = v8::FunctionTemplate::builder(signature_method)
        .signature(signature)
        .length(2)
        .data(data.into())
        .build(scope);
    base_ft.prototype_template(scope).set(
        v8::String::new(scope, "m").unwrap().into(),
        method_ft.into(),
    );
    let base_ctor = base_ft.get_function(scope).unwrap();
    set_global(scope, context, "Gov8SigBase", base_ctor);

    // A derived template inherits from the base: its instances remain valid
    // receivers for the base signature.
    let derived_ft = v8::FunctionTemplate::new(scope, cb_noop);
    derived_ft.set_class_name(v8::String::new(scope, "Gov8SigDerived").unwrap());
    derived_ft.inherit(base_ft);
    let derived_ctor = derived_ft.get_function(scope).unwrap();
    set_global(scope, context, "Gov8SigDerived", derived_ctor);

    // Bind stable instances so receiver-identity checks are meaningful.
    let _ = eval_text(
        scope,
        "var sd = new Gov8SigDerived(); var sb = new Gov8SigBase()",
    );
    let derived_instance = eval(scope, "sd").unwrap();
    let derived_object = v8::Local::<v8::Object>::try_from(derived_instance).unwrap();
    let derived_hash = derived_object.get_hash();
    let base_instance = eval(scope, "sb").unwrap();
    let base_object = v8::Local::<v8::Object>::try_from(base_instance).unwrap();
    let base_hash = base_object.get_hash();

    EXPECTED_HASH.with(|c| c.set(derived_hash));
    let derived_call = eval_text(scope, "sd.m(5)");
    let fn_length = eval_text(scope, "sd.m.length");
    let wrong_receiver = eval_caught(scope, "sd.m.call({}, 5)");

    EXPECTED_HASH.with(|c| c.set(base_hash));
    let base_call = eval_text(scope, "sb.m(1)");
    let wrong_receiver_base = eval_caught(scope, "sb.m.call({}, 5)");

    let host_instance = derived_ft.get_function(scope).unwrap();
    let host_object = host_instance.new_instance(scope, &[]).unwrap();
    EXPECTED_HASH.with(|c| c.set(host_object.get_hash()));
    let method = host_object
        .get(scope, v8::String::new(scope, "m").unwrap().into())
        .unwrap();
    let method_fn = v8::Local::<v8::Function>::try_from(method).unwrap();
    let host_call = method_fn
        .call(
            scope,
            host_object.into(),
            &[v8::Integer::new(scope, 5).into()],
        )
        .map(|v| value_text(scope, v))
        .unwrap_or_default();
    let callback_log = log_join();

    let actual = Json::obj(vec![
        ("derived_call", Json::s(&derived_call)),
        ("base_call", Json::s(&base_call)),
        ("fn_length", Json::s(&fn_length)),
        ("wrong_receiver", Json::s(&wrong_receiver)),
        ("wrong_receiver_base", Json::s(&wrong_receiver_base)),
        ("host_call", Json::s(&host_call)),
        ("callback_log", Json::s(&callback_log)),
    ]);
    let expected = Json::obj(vec![
        ("derived_call", Json::s("ok")),
        ("base_call", Json::s("ok")),
        ("fn_length", Json::s("2")),
        (
            "wrong_receiver",
            Json::s("Uncaught TypeError: Illegal invocation"),
        ),
        (
            "wrong_receiver_base",
            Json::s("Uncaught TypeError: Illegal invocation"),
        ),
        ("host_call", Json::s("ok")),
        (
            "callback_log",
            Json::s(concat!(
                "call:args=1 data=sig-data this_ok=true;",
                "call:args=1 data=sig-data this_ok=true;",
                "call:args=1 data=sig-data this_ok=true"
            )),
        ),
    ]);
    vec![expect_eq(
        "tpladv/signature_receiver_enforcement",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 7. intrinsic data properties
// ---------------------------------------------------------------------------

fn intrinsic_data_property() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    ot.set_intrinsic_data_property(
        v8::String::new(scope, "arr").unwrap().into(),
        v8::Intrinsic::ArrayPrototype,
        v8::PropertyAttribute::NONE,
    );
    ot.set_intrinsic_data_property(
        v8::String::new(scope, "ro").unwrap().into(),
        v8::Intrinsic::ArrayPrototype,
        v8::PropertyAttribute::READ_ONLY,
    );
    ot.set_intrinsic_data_property(
        v8::String::new(scope, "iter").unwrap().into(),
        v8::Intrinsic::IteratorPrototype,
        v8::PropertyAttribute::NONE,
    );
    let obj = ot.new_instance(scope).unwrap();
    set_global(scope, context, "io", obj);

    let arr_is_intrinsic = eval_text(scope, "io.arr === Array.prototype");
    let same_intrinsic_object = eval_text(scope, "io.arr === io.ro");
    let read_only_attr = eval_text(scope, "Object.getOwnPropertyDescriptor(io, 'ro').writable");
    let plain_attr = eval_text(scope, "Object.getOwnPropertyDescriptor(io, 'arr').writable");
    let iterator_is_intrinsic = eval_text(scope, "io.iter[Symbol.iterator]() === io.iter");
    // The iterator prototype sits one link above the per-type iterator
    // prototypes (e.g. %ArrayIteratorPrototype%).
    let iterator_identity = eval_text(
        scope,
        "io.iter === Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]()))",
    );

    // Intrinsics also work on an instance template: every `new C()` gets
    // the context's real Array.prototype.
    let ft = v8::FunctionTemplate::new(scope, cb_noop);
    ft.instance_template(scope).set_intrinsic_data_property(
        v8::String::new(scope, "arr").unwrap().into(),
        v8::Intrinsic::ArrayPrototype,
        v8::PropertyAttribute::NONE,
    );
    let ctor = ft.get_function(scope).unwrap();
    set_global(scope, context, "C", ctor);
    let instance_intrinsic = eval_text(scope, "new C().arr === Array.prototype");

    let actual = Json::obj(vec![
        ("arr_is_intrinsic", Json::s(&arr_is_intrinsic)),
        ("same_intrinsic_object", Json::s(&same_intrinsic_object)),
        ("read_only_attr", Json::s(&read_only_attr)),
        ("plain_attr", Json::s(&plain_attr)),
        ("iterator_is_intrinsic", Json::s(&iterator_is_intrinsic)),
        ("iterator_identity", Json::s(&iterator_identity)),
        ("instance_intrinsic", Json::s(&instance_intrinsic)),
    ]);
    let expected = Json::obj(vec![
        ("arr_is_intrinsic", Json::s("true")),
        ("same_intrinsic_object", Json::s("true")),
        ("read_only_attr", Json::s("false")),
        ("plain_attr", Json::s("true")),
        ("iterator_is_intrinsic", Json::s("true")),
        ("iterator_identity", Json::s("true")),
        ("instance_intrinsic", Json::s("true")),
    ]);
    vec![expect_eq(
        "tpladv/intrinsic_data_property",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 8. constructor behavior: Throw / Allow / read-only / removed prototype
// ---------------------------------------------------------------------------

#[allow(clippy::too_many_lines)]
fn constructor_behavior_and_prototype() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // ConstructorBehavior::Throw: "concise" API function, no .prototype.
    let concise_ft = v8::FunctionTemplate::builder(cb_return_int)
        .constructor_behavior(v8::ConstructorBehavior::Throw)
        .build(scope);
    concise_ft.set_class_name(v8::String::new(scope, "Gov8Concise").unwrap());
    let concise_fn = concise_ft.get_function(scope).unwrap();
    set_global(scope, context, "Concise", concise_fn);
    let concise_prototype = eval_text(scope, "typeof Concise.prototype");
    let concise_call = eval_text(scope, "Concise()");
    let concise_name = eval_text(scope, "Concise.name");
    let concise_new = eval_caught(scope, "new Concise()");

    // Default (Allow): full constructor with a writable prototype.
    let plain_ft = v8::FunctionTemplate::new(scope, cb_noop);
    plain_ft.set_class_name(v8::String::new(scope, "Gov8Plain").unwrap());
    let plain_fn = plain_ft.get_function(scope).unwrap();
    set_global(scope, context, "Gov8Plain", plain_fn);
    let plain_prototype = eval_text(scope, "typeof Gov8Plain.prototype");
    let plain_constructor_link = eval_text(scope, "Gov8Plain.prototype.constructor === Gov8Plain");
    let plain_writable = eval_text(
        scope,
        "Object.getOwnPropertyDescriptor(Gov8Plain, 'prototype').writable",
    );

    // read_only_prototype: sloppy assignment silently fails.
    let ro_ft = v8::FunctionTemplate::new(scope, cb_noop);
    ro_ft.set_class_name(v8::String::new(scope, "Gov8RO").unwrap());
    ro_ft.read_only_prototype();
    let ro_fn = ro_ft.get_function(scope).unwrap();
    set_global(scope, context, "Gov8RO", ro_fn);
    let ro_assignment_ignored = eval_text(
        scope,
        "(Gov8RO.prototype = {}, Gov8RO.prototype.constructor === Gov8RO)",
    );
    let ro_writable = eval_text(
        scope,
        "Object.getOwnPropertyDescriptor(Gov8RO, 'prototype').writable",
    );

    // remove_prototype: like Throw, but retrofitted on a default template.
    let removed_ft = v8::FunctionTemplate::new(scope, cb_noop);
    removed_ft.set_class_name(v8::String::new(scope, "Gov8NoProto").unwrap());
    removed_ft.remove_prototype();
    let removed_fn = removed_ft.get_function(scope).unwrap();
    set_global(scope, context, "Gov8NoProto", removed_fn);
    let removed_prototype = eval_text(scope, "typeof Gov8NoProto.prototype");
    let removed_call = eval_text(scope, "Gov8NoProto()");
    let removed_new = eval_caught(scope, "new Gov8NoProto()");

    let actual = Json::obj(vec![
        ("concise_prototype", Json::s(&concise_prototype)),
        ("concise_name", Json::s(&concise_name)),
        ("concise_call", Json::s(&concise_call)),
        ("concise_new", Json::s(&concise_new)),
        ("plain_prototype", Json::s(&plain_prototype)),
        ("plain_constructor_link", Json::s(&plain_constructor_link)),
        ("plain_writable", Json::s(&plain_writable)),
        ("ro_assignment_ignored", Json::s(&ro_assignment_ignored)),
        ("ro_writable", Json::s(&ro_writable)),
        ("removed_prototype", Json::s(&removed_prototype)),
        ("removed_call", Json::s(&removed_call)),
        ("removed_new", Json::s(&removed_new)),
    ]);
    let expected = Json::obj(vec![
        ("concise_prototype", Json::s("undefined")),
        ("concise_name", Json::s("Gov8Concise")),
        ("concise_call", Json::s("3")),
        (
            "concise_new",
            Json::s("Uncaught TypeError: Concise is not a constructor"),
        ),
        ("plain_prototype", Json::s("object")),
        ("plain_constructor_link", Json::s("true")),
        ("plain_writable", Json::s("true")),
        ("ro_assignment_ignored", Json::s("true")),
        ("ro_writable", Json::s("false")),
        ("removed_prototype", Json::s("undefined")),
        ("removed_call", Json::s("undefined")),
        (
            "removed_new",
            Json::s("Uncaught TypeError: Gov8NoProto is not a constructor"),
        ),
    ]);
    vec![expect_eq(
        "tpladv/constructor_behavior_and_prototype",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 9. template inheritance
// ---------------------------------------------------------------------------

#[allow(clippy::too_many_lines)]
fn inheritance_chain() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let isolate_scope, isolate);
    let context = v8::Context::new(isolate_scope, Default::default());
    let scope = &mut v8::ContextScope::new(isolate_scope, context);

    let base_ft = v8::FunctionTemplate::new(scope, cb_noop);
    base_ft.set_class_name(v8::String::new(scope, "Gov8Base").unwrap());
    base_ft.prototype_template(scope).set(
        v8::String::new(scope, "baseMark").unwrap().into(),
        v8::String::new(scope, "B").unwrap().into(),
    );
    // Template-level statics: properties on the function itself.
    base_ft.set(
        v8::String::new(scope, "baseStatic").unwrap().into(),
        v8::String::new(scope, "s").unwrap().into(),
    );

    let derived_ft = v8::FunctionTemplate::new(scope, cb_noop);
    derived_ft.set_class_name(v8::String::new(scope, "Gov8Derived").unwrap());
    derived_ft.inherit(base_ft);
    derived_ft.prototype_template(scope).set(
        v8::String::new(scope, "derivedMark").unwrap().into(),
        v8::String::new(scope, "D").unwrap().into(),
    );

    let base_ctor = base_ft.get_function(scope).unwrap();
    set_global(scope, context, "Gov8Base", base_ctor);
    let derived_ctor = derived_ft.get_function(scope).unwrap();
    set_global(scope, context, "Gov8Derived", derived_ctor);

    let proto_chain = eval_text(
        scope,
        "Object.getPrototypeOf(Gov8Derived.prototype) === Gov8Base.prototype",
    );
    let instance_of = eval_text(
        scope,
        "(new Gov8Derived() instanceof Gov8Derived) + '|' + (new Gov8Derived() instanceof Gov8Base)",
    );
    let marks = eval_text(
        scope,
        "new Gov8Derived().baseMark + '|' + new Gov8Derived().derivedMark",
    );
    let statics = eval_text(scope, "Gov8Base.baseStatic + '|' + Gov8Derived.baseStatic");
    let constructor_identity = eval_text(scope, "new Gov8Derived().constructor === Gov8Derived");
    let derived_constructor_link =
        eval_text(scope, "Gov8Derived.prototype.constructor === Gov8Derived");
    let base_proto_static_not_inherited = eval_text(
        scope,
        "Object.prototype.hasOwnProperty.call(Gov8Derived.prototype, 'baseMark')",
    );

    let actual = Json::obj(vec![
        ("proto_chain", Json::s(&proto_chain)),
        ("instance_of", Json::s(&instance_of)),
        ("marks", Json::s(&marks)),
        ("statics", Json::s(&statics)),
        ("constructor_identity", Json::s(&constructor_identity)),
        (
            "derived_constructor_link",
            Json::s(&derived_constructor_link),
        ),
        (
            "base_proto_static_not_inherited",
            Json::s(&base_proto_static_not_inherited),
        ),
    ]);
    let expected = Json::obj(vec![
        ("proto_chain", Json::s("true")),
        ("instance_of", Json::s("true|true")),
        ("marks", Json::s("B|D")),
        ("statics", Json::s("s|undefined")),
        ("constructor_identity", Json::s("true")),
        ("derived_constructor_link", Json::s("true")),
        ("base_proto_static_not_inherited", Json::s("false")),
    ]);
    vec![expect_eq("tpladv/inheritance_chain", expected, actual)]
}

// ---------------------------------------------------------------------------
// 10. accessor properties (accessor-shaped) on object templates
// ---------------------------------------------------------------------------

fn acc_property_setter(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
    log_push(format!(
        "set:args={} arg0={} this_ok={}",
        args.length(),
        value_text(scope, args.get(0)),
        hash_matches(args.this()),
    ));
}

#[allow(clippy::too_many_lines)]
fn accessor_property_shapes() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    let getter_ft = v8::FunctionTemplate::new(scope, cb_return_five);
    let setter_ft = v8::FunctionTemplate::new(scope, acc_property_setter);
    ot.set_accessor_property(
        v8::String::new(scope, "acc").unwrap().into(),
        Some(getter_ft),
        Some(setter_ft),
        v8::PropertyAttribute::NONE,
    );
    let hidden_getter_ft = v8::FunctionTemplate::new(scope, cb_return_five);
    ot.set_accessor_property(
        v8::String::new(scope, "hidden").unwrap().into(),
        Some(hidden_getter_ft),
        None,
        v8::PropertyAttribute::DONT_ENUM,
    );
    let obj = ot.new_instance(scope).unwrap();
    EXPECTED_HASH.with(|c| c.set(obj.get_hash()));
    set_global(scope, context, "ao", obj);

    // Reading an accessor property invokes the getter template; the
    // getter function itself is only reachable through the descriptor.
    let read_invokes_getter = eval_text(scope, "typeof ao.acc");
    let getter_call = eval_text(
        scope,
        "Object.getOwnPropertyDescriptor(ao, 'acc').get.call(ao)",
    );
    let descriptor_get = eval_text(
        scope,
        "typeof Object.getOwnPropertyDescriptor(ao, 'acc').get",
    );
    let descriptor_set = eval_text(
        scope,
        "typeof Object.getOwnPropertyDescriptor(ao, 'acc').set",
    );
    let setter_seen = eval_text(scope, "(ao.acc = 9)");
    let hidden_readable = eval_text(scope, "ao.hidden");
    let enumeration = eval_text(scope, "Object.keys(ao).join(',')");

    let actual = Json::obj(vec![
        ("read_invokes_getter", Json::s(&read_invokes_getter)),
        ("getter_call", Json::s(&getter_call)),
        ("descriptor_get", Json::s(&descriptor_get)),
        ("descriptor_set", Json::s(&descriptor_set)),
        ("setter_seen", Json::s(&setter_seen)),
        ("hidden_readable", Json::s(&hidden_readable)),
        ("enumeration", Json::s(&enumeration)),
        ("callback_log", Json::s(&log_join())),
    ]);
    let expected = Json::obj(vec![
        ("read_invokes_getter", Json::s("number")),
        ("getter_call", Json::s("5")),
        ("descriptor_get", Json::s("function")),
        ("descriptor_set", Json::s("function")),
        ("setter_seen", Json::s("9")),
        ("hidden_readable", Json::s("5")),
        ("enumeration", Json::s("acc")),
        ("callback_log", Json::s("set:args=1 arg0=9 this_ok=true")),
    ]);
    vec![expect_eq(
        "tpladv/accessor_property_shapes",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 11. internal-field / aligned-pointer boundaries
// ---------------------------------------------------------------------------

#[allow(clippy::too_many_lines)]
fn internal_field_boundaries() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // Default template: zero internal fields.
    let default_ot = v8::ObjectTemplate::new(scope);
    let default_count = default_ot.internal_field_count();
    let zero_set = default_ot.set_internal_field_count(0);
    let default_count_after_zero = default_ot.internal_field_count();
    let zero_instance = default_ot.new_instance(scope).unwrap();
    let zero_instance_count = zero_instance.internal_field_count();
    let zero_get = zero_instance.get_internal_field(scope, 0).is_none();
    let zero_set_field = zero_instance.set_internal_field(0, v8::Integer::new(scope, 1).into());

    // The count is frozen by the FIRST instantiation: instances created
    // before and after a template re-set both carry the original count
    // (later set_internal_field_count calls are silently inert).
    let growing_ot = v8::ObjectTemplate::new(scope);
    let _ = growing_ot.set_internal_field_count(1);
    let early_instance = growing_ot.new_instance(scope).unwrap();
    let _ = growing_ot.set_internal_field_count(3);
    let late_instance = growing_ot.new_instance(scope).unwrap();
    let early_count = early_instance.internal_field_count();
    let late_count = late_instance.internal_field_count();

    // Impossible counts are rejected at the crate boundary (no V8 call).
    let huge_count_set = growing_ot.set_internal_field_count(usize::MAX);

    // Aligned pointers across the valid tag range 0..15 and mixed usage.
    let native_a = Box::new(111_u32);
    let ptr_a = Box::into_raw(native_a);
    let native_b = Box::new(222_u32);
    let ptr_b = Box::into_raw(native_b);

    let aligned_ot = v8::ObjectTemplate::new(scope);
    let _ = aligned_ot.set_internal_field_count(2);
    let aligned = aligned_ot.new_instance(scope).unwrap();
    aligned.set_aligned_pointer_in_internal_field(0, ptr_a.cast::<c_void>(), 0);
    aligned.set_aligned_pointer_in_internal_field(1, ptr_b.cast::<c_void>(), 14);
    let tag_zero_roundtrip =
        unsafe { aligned.get_aligned_pointer_from_internal_field(0, 0) } == ptr_a.cast::<c_void>();
    let tag_max_roundtrip =
        unsafe { aligned.get_aligned_pointer_from_internal_field(1, 14) } == ptr_b.cast::<c_void>();

    // Re-targeting the same field with a different tag: the last write wins.
    let native_c = Box::new(333_u32);
    let ptr_c = Box::into_raw(native_c);
    aligned.set_aligned_pointer_in_internal_field(0, ptr_c.cast::<c_void>(), 5);
    let retarget_roundtrip =
        unsafe { aligned.get_aligned_pointer_from_internal_field(0, 5) } == ptr_c.cast::<c_void>();

    // A null aligned pointer round-trips as null.
    let null_ot = v8::ObjectTemplate::new(scope);
    let _ = null_ot.set_internal_field_count(1);
    let null_instance = null_ot.new_instance(scope).unwrap();
    null_instance.set_aligned_pointer_in_internal_field(0, std::ptr::null(), 3);
    let null_roundtrip =
        unsafe { null_instance.get_aligned_pointer_from_internal_field(0, 3) }.is_null();

    // Aligned and Data fields coexist on one object when used consistently.
    let mixed_ot = v8::ObjectTemplate::new(scope);
    let _ = mixed_ot.set_internal_field_count(2);
    let mixed = mixed_ot.new_instance(scope).unwrap();
    mixed.set_aligned_pointer_in_internal_field(0, ptr_a.cast::<c_void>(), 7);
    let data_stored = mixed.set_internal_field(1, v8::Integer::new(scope, 42).into());
    let data_roundtrip = mixed
        .get_internal_field(scope, 1)
        .and_then(|d| v8::Local::<v8::Integer>::try_from(d).ok())
        .map(|i| i.value())
        .unwrap_or(-1);
    let aligned_side_roundtrip =
        unsafe { mixed.get_aligned_pointer_from_internal_field(0, 7) } == ptr_a.cast::<c_void>();

    // Reclaim the native allocations and verify the pointees survived.
    let a_ok = unsafe { Box::from_raw(ptr_a) };
    let b_ok = unsafe { Box::from_raw(ptr_b) };
    let c_ok = unsafe { Box::from_raw(ptr_c) };
    let native_roundtrip = *a_ok == 111 && *b_ok == 222 && *c_ok == 333;

    let actual = Json::obj(vec![
        ("default_count", Json::i(default_count as i64)),
        ("zero_set", Json::b(zero_set)),
        (
            "default_count_after_zero",
            Json::i(default_count_after_zero as i64),
        ),
        ("zero_instance_count", Json::i(zero_instance_count as i64)),
        ("zero_get_is_none", Json::b(zero_get)),
        ("zero_set_field", Json::b(zero_set_field)),
        ("early_count", Json::i(early_count as i64)),
        ("late_count", Json::i(late_count as i64)),
        ("huge_count_set", Json::b(huge_count_set)),
        ("tag_zero_roundtrip", Json::b(tag_zero_roundtrip)),
        ("tag_max_roundtrip", Json::b(tag_max_roundtrip)),
        ("retarget_roundtrip", Json::b(retarget_roundtrip)),
        ("null_roundtrip", Json::b(null_roundtrip)),
        ("data_stored", Json::b(data_stored)),
        ("data_roundtrip", Json::i(data_roundtrip)),
        ("aligned_side_roundtrip", Json::b(aligned_side_roundtrip)),
        ("native_roundtrip", Json::b(native_roundtrip)),
    ]);
    let expected = Json::obj(vec![
        ("default_count", Json::i(0)),
        ("zero_set", Json::b(true)),
        ("default_count_after_zero", Json::i(0)),
        ("zero_instance_count", Json::i(0)),
        ("zero_get_is_none", Json::b(true)),
        ("zero_set_field", Json::b(false)),
        ("early_count", Json::i(1)),
        ("late_count", Json::i(1)),
        ("huge_count_set", Json::b(false)),
        ("tag_zero_roundtrip", Json::b(true)),
        ("tag_max_roundtrip", Json::b(true)),
        ("retarget_roundtrip", Json::b(true)),
        ("null_roundtrip", Json::b(true)),
        ("data_stored", Json::b(true)),
        ("data_roundtrip", Json::i(42)),
        ("aligned_side_roundtrip", Json::b(true)),
        ("native_roundtrip", Json::b(true)),
    ]);
    vec![expect_eq(
        "tpladv/internal_field_boundaries",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 12. security tokens (the crate's whole access-check surface)
// ---------------------------------------------------------------------------

#[allow(clippy::too_many_lines)]
fn security_token_contexts() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let ctx1 = v8::Context::new(scope, Default::default());
    let ctx2 = v8::Context::new(scope, Default::default());
    let ctx3 = v8::Context::new(scope, Default::default());
    // Enter one context for the cross-context handle manipulation below;
    // entering affects only which context is "current".
    let scope = &mut v8::ContextScope::new(scope, ctx2);

    // The DEFAULT security token of a context is the context's own global
    // object (V8 api.cc: UseDefaultSecurityToken sets `env->global_object()`),
    // so every fresh context carries a distinct token.
    let token1 = ctx1.get_security_token(scope);
    let token2 = ctx2.get_security_token(scope);
    let token3 = ctx3.get_security_token(scope);
    let defaults_equal_1_2 = token1.strict_equals(token2);
    let defaults_equal_2_3 = token2.strict_equals(token3);

    // A plain host object created in ctx1. Plain objects carry no
    // access-check info, so once bridged they are readable from any
    // context regardless of tokens.
    let shared = {
        let scope1 = &mut v8::ContextScope::new(scope, ctx1);
        let obj = v8::Object::new(scope1);
        obj.set(
            scope1,
            v8::String::new(scope1, "mark").unwrap().into(),
            v8::String::new(scope1, "m1").unwrap().into(),
        )
        .unwrap();
        ctx1.global(scope1)
            .set(
                scope1,
                v8::String::new(scope1, "o1").unwrap().into(),
                obj.into(),
            )
            .unwrap();
        v8::Global::new(scope1, obj)
    };
    // Bridging into the ENTERED context's own global always works.
    let own_global_set = ctx2
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "o1").unwrap().into(),
            v8::Local::new(scope, &shared).into(),
        )
        .unwrap_or(false);
    let read_from_ctx2 = {
        let scope2 = &mut v8::ContextScope::new(scope, ctx2);
        let src = v8::String::new(scope2, "o1.mark").unwrap();
        v8::Script::compile(scope2, src, None)
            .and_then(|s| s.run(scope2))
            .map(|v| value_text(scope2, v))
            .unwrap_or_default()
    };

    // Setting a property on ANOTHER context's global proxy while tokens
    // differ is denied: the set throws (set returns None) and the
    // exception carries V8's SecurityError text.
    let (cross_token_set_denied, denial_caught, denial_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let set_result = ctx3.global(tc).set(
            tc,
            v8::String::new(tc, "o1").unwrap().into(),
            v8::Local::new(tc, &shared).into(),
        );
        (
            set_result.is_none(),
            tc.has_caught(),
            tc.message()
                .map(|m| m.get(tc).to_rust_string_lossy(tc))
                .unwrap_or_default(),
        )
    };
    let read_from_ctx3_before = {
        let scope3 = &mut v8::ContextScope::new(scope, ctx3);
        let src = v8::String::new(scope3, "typeof o1").unwrap();
        v8::Script::compile(scope3, src, None)
            .and_then(|s| s.run(scope3))
            .map(|v| value_text(scope3, v))
            .unwrap_or_default()
    };

    // Sharing a token (any Value) re-enables cross-context global access.
    ctx3.set_security_token(token2);
    let tokens_shared = ctx3.get_security_token(scope).strict_equals(token2);
    let shared_token_set = ctx3
        .global(scope)
        .set(
            scope,
            v8::String::new(scope, "o1").unwrap().into(),
            v8::Local::new(scope, &shared).into(),
        )
        .unwrap_or(false);
    let read_from_ctx3_after = {
        let scope3 = &mut v8::ContextScope::new(scope, ctx3);
        let src = v8::String::new(scope3, "o1.mark").unwrap();
        v8::Script::compile(scope3, src, None)
            .and_then(|s| s.run(scope3))
            .map(|v| value_text(scope3, v))
            .unwrap_or_default()
    };

    // Resetting restores the context's own global object as the token.
    ctx3.use_default_security_token();
    let reset_token_is_ctx3_own = !ctx3.get_security_token(scope).strict_equals(token2);
    let (denied_again, denial_caught_again) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let set_result = ctx1.global(tc).set(
            tc,
            v8::String::new(tc, "o1").unwrap().into(),
            v8::Local::new(tc, &shared).into(),
        );
        (set_result.is_none(), tc.has_caught())
    };

    let actual = Json::obj(vec![
        ("defaults_equal_1_2", Json::b(defaults_equal_1_2)),
        ("defaults_equal_2_3", Json::b(defaults_equal_2_3)),
        ("own_global_set", Json::b(own_global_set)),
        ("read_from_ctx2", Json::s(&read_from_ctx2)),
        ("cross_token_set_denied", Json::b(cross_token_set_denied)),
        ("denial_caught", Json::b(denial_caught)),
        ("denial_message", Json::s(&denial_message)),
        ("read_from_ctx3_before", Json::s(&read_from_ctx3_before)),
        ("tokens_shared", Json::b(tokens_shared)),
        ("shared_token_set", Json::b(shared_token_set)),
        ("read_from_ctx3_after", Json::s(&read_from_ctx3_after)),
        ("reset_token_is_ctx3_own", Json::b(reset_token_is_ctx3_own)),
        ("denied_again", Json::b(denied_again)),
        ("denial_caught_again", Json::b(denial_caught_again)),
    ]);
    let expected = Json::obj(vec![
        ("defaults_equal_1_2", Json::b(false)),
        ("defaults_equal_2_3", Json::b(false)),
        ("own_global_set", Json::b(true)),
        ("read_from_ctx2", Json::s("m1")),
        ("cross_token_set_denied", Json::b(true)),
        ("denial_caught", Json::b(true)),
        ("denial_message", Json::s("Uncaught TypeError: no access")),
        ("read_from_ctx3_before", Json::s("undefined")),
        ("tokens_shared", Json::b(true)),
        ("shared_token_set", Json::b(true)),
        ("read_from_ctx3_after", Json::s("m1")),
        ("reset_token_is_ctx3_own", Json::b(true)),
        ("denied_again", Json::b(true)),
        ("denial_caught_again", Json::b(true)),
    ]);
    vec![expect_eq(
        "tpladv/security_token_contexts",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 13. call-as-function handler on object templates
// ---------------------------------------------------------------------------

fn call_as_function_callback(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    log_push(format!(
        "caf:construct={} data={} this_ok={} arg0={}",
        args.is_construct_call(),
        value_text(scope, args.data()),
        hash_matches(args.this()),
        value_text(scope, args.get(0)),
    ));
    let doubled = args.get(0).integer_value(scope).unwrap_or(0) * 2;
    rv.set_int32(doubled as i32);
}

#[allow(clippy::too_many_lines)]
fn call_as_function_handler() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    let data = v8::String::new(scope, "caf-data").unwrap();
    ot.set_call_as_function_handler(call_as_function_callback, Some(data.into()));
    let obj = ot.new_instance(scope).unwrap();
    EXPECTED_HASH.with(|c| c.set(obj.get_hash()));
    set_global(scope, context, "co", obj);

    let call_result = eval_text(scope, "co(4)");
    let type_of = eval_text(scope, "typeof co");
    let to_string_tag = eval_text(scope, "Object.prototype.toString.call(co)");
    // Construct calls dispatch to the SAME handler: is_construct_call()
    // is true, `this` is the instance, and even a primitive return value
    // is delivered as the construct result (no TypeError, unlike JS
    // constructors).
    let construct_attempt = eval_caught(scope, "new co(1)");
    let callback_log = log_join();

    let actual = Json::obj(vec![
        ("call_result", Json::s(&call_result)),
        ("type_of", Json::s(&type_of)),
        ("to_string_tag", Json::s(&to_string_tag)),
        ("construct_attempt", Json::s(&construct_attempt)),
        ("callback_log", Json::s(&callback_log)),
    ]);
    let expected = Json::obj(vec![
        ("call_result", Json::s("8")),
        ("type_of", Json::s("function")),
        ("to_string_tag", Json::s("[object Object]")),
        ("construct_attempt", Json::s("2")),
        (
            "callback_log",
            Json::s(concat!(
                "caf:construct=false data=caf-data this_ok=true arg0=4;",
                "caf:construct=true data=caf-data this_ok=true arg0=1"
            )),
        ),
    ]);
    vec![expect_eq(
        "tpladv/call_as_function_handler",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// 14. immutable prototype object templates
// ---------------------------------------------------------------------------

fn immutable_proto() -> Vec<CheckOutcome> {
    log_clear();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    ot.set_immutable_proto();
    let obj = ot.new_instance(scope).unwrap();
    set_global(scope, context, "ip", obj);

    // Immutable-prototype instances REJECT prototype mutations by throwing
    // (not the silent sloppy-mode failure of ordinary objects), while
    // ordinary property reads keep working through the default chain.
    let set_proto_throws = eval_caught(scope, "Object.setPrototypeOf(ip, {x: 1})");
    let new_prop_missing = eval_text(scope, "ip.x");
    let dunder_throws = eval_caught(scope, "(ip.__proto__ = {y: 2})");
    let default_proto_ok = eval_text(scope, "ip.toString === Object.prototype.toString");

    let actual = Json::obj(vec![
        ("set_proto_throws", Json::s(&set_proto_throws)),
        ("new_prop_missing", Json::s(&new_prop_missing)),
        ("dunder_throws", Json::s(&dunder_throws)),
        ("default_proto_ok", Json::s(&default_proto_ok)),
    ]);
    let expected = Json::obj(vec![
        ("set_proto_throws", Json::s("Uncaught TypeError: Immutable prototype object '#<Object>' cannot have their prototype set")),
        ("new_prop_missing", Json::s("undefined")),
        ("dunder_throws", Json::s("Uncaught TypeError: Immutable prototype object '#<Object>' cannot have their prototype set")),
        ("default_proto_ok", Json::s("true")),
    ]);
    vec![expect_eq("tpladv/immutable_proto", expected, actual)]
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

const CHECKS: &[fn() -> Vec<CheckOutcome>] = &[
    named_interceptor_get_set,
    named_interceptor_query_delete_enum_define,
    indexed_interceptor_full_family,
    flag_interceptors,
    return_value_get_and_specials,
    signature_receiver_enforcement,
    intrinsic_data_property,
    constructor_behavior_and_prototype,
    inheritance_chain,
    accessor_property_shapes,
    internal_field_boundaries,
    security_token_contexts,
    call_as_function_handler,
    immutable_proto,
];

fn main() -> ExitCode {
    oracle::ensure_v8();
    let stdout = std::io::stdout();
    let mut out = stdout.lock();
    let mut total = 0usize;
    let mut passed = 0usize;
    for check in CHECKS {
        for outcome in check() {
            total += 1;
            if outcome.passed() {
                passed += 1;
            }
            let _ = writeln!(out, "{}", outcome.to_line());
            let _ = out.flush();
        }
    }
    let failed = total - passed;
    let _ = writeln!(out, "{}", summary_line(total, passed, failed));
    let _ = out.flush();
    if failed == 0 {
        ExitCode::SUCCESS
    } else {
        ExitCode::FAILURE
    }
}
