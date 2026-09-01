//! Object-operation and value-conversion/predicate conformance slice for the
//! pinned `v8` crate (=152.2.0, V8 15.2.124.1, x86_64-pc-windows-msvc).
//!
//! Characterizes, in fixed order, the observable contract of the *object and
//! value* APIs that no other slice covers. Existing coverage this slice
//! deliberately does NOT duplicate:
//! - `values/*` and `script/value_types` (base slice): primitive
//!   construction, ToString conversions, `is_object`/`is_array`/`is_function`
//!   and a handful of primitive predicates.
//! - `runtime-values/*`: Map/Set/Proxy/Symbol/Date/RegExp/JSON objects,
//!   property descriptors (`get_own_property_descriptor`,
//!   `define_own_property`, `create_data_property`,
//!   `get_property_attributes`, `get_own_property_names`), integrity levels.
//! - `host` + `tpladv`: templates, callbacks, constructor-call semantics,
//!   template-level accessors (`ObjectTemplate::set_accessor` /
//!   `set_accessor_property`), interceptors observed from JS, and
//!   `ObjectTemplate::set_call_as_function_handler`.
//!
//! Areas covered here (all API-driven, not JS-driven, unless noted):
//! - **Prototype**: `Object::get_prototype` / `set_prototype` including the
//!   null prototype, `Object.prototype` having no prototype, and the cyclic
//!   `__proto__` rejection (`src/object.rs`).
//! - **Has/delete family**: `Object::has`, `has_index`, `has_own_property`
//!   (own vs. prototype chain), `delete`, `delete_index` (missing keys,
//!   non-configurable and frozen properties, holes in arrays, symbol keys).
//! - **Real-named queries**: `get_real_named_property`,
//!   `has_real_named_property`, `get_real_named_property_attributes` bypass
//!   named interceptors while `get`/`has` do not, and walk the prototype
//!   chain (`src/object.rs`, "interceptors in the prototype chain are not
//!   called").
//! - **Identity**: `Object::get_identity_hash` (never zero, stable),
//!   `Value::get_hash` cross-API equality and Smi determinism,
//!   `Object::get_creation_context`, `Object::get_constructor_name`
//!   (including the `Reflect.construct` new.target name).
//! - **Receivers**: `Object::get_with_receiver` / `set_with_receiver`
//!   receiver plumbing for accessors on and off the prototype chain, and
//!   the data-property redirect onto a foreign receiver.
//! - **Lazy/accessor properties on instances** (the template-level shapes
//!   live in `tpladv`): `Object::set_lazy_data_property` (getter fires once,
//!   then the property is an ordinary data property) and instance-level
//!   `Object::set_accessor_with_setter`.
//! - **Call-as-function/constructor**: `Object::is_callable`,
//!   `is_constructor`, `call_as_function[_with_context]`,
//!   `call_as_constructor[_with_context]` on plain objects (TypeError), JS
//!   functions, arrows, classes (no `new`), and constructors with primitive
//!   vs. object returns.
//! - **Callable/constructor predicates** over the full exotic-object matrix.
//! - **Conversions**: `Value::to_object`, `to_boolean`, `to_integer`,
//!   `to_big_int`, `to_detail_string`, plus the residual local-return
//!   `to_number`, `to_string`, `to_uint32`, and `to_int32` (`src/value.rs`).
//! - **instanceof** (`Value::instance_of`), the **same-value-zero** equality
//!   matrix (`same_value` / `same_value_zero` / `strict_equals`), and the
//!   **type representation** matrix (`Value::type_of`).
//! - **Missing predicates inventory**: every `Value::is_*` predicate that no
//!   other slice pins (25 of them, see `predicates_missing_inventory`).
//! - **Data**: every public `Data::is_*` predicate and Data identity across
//!   Value, Context, template, module, module-request, and private locals.
//! - **Residual helpers**: `Value::is_module_namespace_object` and every
//!   ordered branch of the crate's `Value::type_repr` convenience helper.
//!
//! Everything is normalized per `src/json.rs` rules: no addresses, no raw
//! hash values (identity hashes are per-isolate seeded), exact V8 error
//! strings for the pinned build. The runner emits the same JSON-lines
//! protocol as the other slices; every check id is prefixed `obj-ops/`.
//!
//! This slice performs no platform shutdown, so its fixture can be verified
//! in-process and compared byte-for-byte by
//! `tests/conformance_object_ops_fixture.rs`.
//!
//! # Benchmark workload spec (for a future `benches/object-ops.rs`)
//!
//! Methodology identical to the existing benches (`benches/common/mod.rs`):
//! 1 s warm-up, 3 s measurement, 50 samples, one full operation per
//! `criterion::black_box`-guarded iteration, one isolate + context for the
//! whole bench, fresh objects per iteration to keep GC pressure realistic,
//! no V8 flags, default platform, release profile. Workloads, each asserted
//! once for correctness outside the timed loop:
//!
//! - `object/has_delete_cycle`: per iteration `Object::new` + `set` +
//!   `has` + `delete` + `has` (the full own-property lifecycle).
//! - `object/get_with_receiver`: per iteration a `get_with_receiver` through
//!   a JS accessor defined once on a prototype object (receiver = fresh
//!   object).
//! - `object/get_identity_hash`: per iteration `get_identity_hash()` on a
//!   fresh object (first call materializes the hash; this measures the
//!   create-and-hash path).
//! - `object/to_object_number`: per iteration `Number::new(5).to_object()`
//!   (wrapper allocation, the dominant conversion cost).
//! - `object/to_boolean_matrix`: per iteration `to_boolean` over the 8-entry
//!   falsy/truthy matrix, XOR-folded into a `black_box`ed bool.
//! - `object/same_value_zero`: per iteration `same_value_zero` +
//!   `strict_equals` over a fixed pair set.
//! - `object/typeof_all`: per iteration `type_of` over the 10-entry
//!   representation matrix, compared to expected strings.
//! - `object/instance_of`: per iteration `instance_of` of a fresh object
//!   against `Object.prototype.constructor`.
//!
//! Go comparisons must use the same warm-up/sample policy, inputs, and V8
//! configuration (no flags, default platform, pointer compression off), a
//! release-mode build, and a fresh environment capture.

use std::cell::Cell;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

// ---------------------------------------------------------------------------
// Helpers (local to this binary; `checks::harness` is pub(crate) and shared
// registry files must not be modified by this slice).
// ---------------------------------------------------------------------------

/// Compiles and runs `source`, returning the completion value (`None` on
/// syntax error or runtime throw).
fn eval<'s>(scope: &mut v8::PinScope<'s, '_>, source: &str) -> Option<v8::Local<'s, v8::Value>> {
    let src = v8::String::new(scope, source)?;
    v8::Script::compile(scope, src, None)?.run(scope)
}

/// ToString of an arbitrary value ("" when conversion fails).
fn value_text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value
        .to_string(scope)
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

/// Runs `source` and returns its completion value rendered via ToString
/// ("" when it throws or does not convert).
fn eval_text(scope: &mut v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source)
        .and_then(|v| v.to_string(scope))
        .map(|s| s.to_rust_string_lossy(scope))
        .unwrap_or_default()
}

/// Fetches a global by name as a `Local<Value>`.
fn global_value<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    name: &str,
) -> Option<v8::Local<'s, v8::Value>> {
    eval(scope, name)
}

/// Fetches a global by name as a `Local<Object>` (functions and plain
/// objects are objects; primitives fail).
fn global_object<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    name: &str,
) -> Option<v8::Local<'s, v8::Object>> {
    eval(scope, name).and_then(|v| v.try_cast::<v8::Object>().ok())
}

/// Sets a global property (panics on failure; only used on plain globals).
fn set_global(
    scope: &mut v8::PinScope<'_, '_>,
    context: v8::Local<'_, v8::Context>,
    name: &str,
    value: v8::Local<'_, v8::Value>,
) {
    let key = v8::String::new(scope, name).unwrap();
    context
        .global(scope)
        .set(scope, key.into(), value)
        .expect("set global");
}

/// The exception message of the enclosing TryCatch scope ("" when none).
/// Written as a macro because the pinned TryCatch scope type carries the
/// `message()` method only before it coerces to a plain `PinScope`.
macro_rules! caught_message {
    ($tc:expr) => {
        $tc.message()
            .map(|m| m.get($tc).to_rust_string_lossy($tc))
            .unwrap_or_default()
    };
}

/// A `Local<Name>` string key.
fn name_key<'s, 'i>(scope: &v8::PinScope<'s, 'i>, name: &str) -> v8::Local<'s, v8::Name> {
    v8::String::new(scope, name).unwrap().into()
}

/// Integer property value readback (`-1` when absent or not a number).
fn int_of(scope: &mut v8::PinScope<'_, '_>, value: Option<v8::Local<'_, v8::Value>>) -> i64 {
    value.and_then(|v| v.integer_value(scope)).unwrap_or(-1)
}

/// `Object::get_constructor_name` of the object produced by evaluating
/// `source` ("" when it is not an object).
fn constructor_name_of(scope: &mut v8::PinScope<'_, '_>, source: &str) -> String {
    match global_value(scope, source) {
        Some(v) => match v.try_cast::<v8::Object>().ok() {
            Some(o) => o.get_constructor_name().to_rust_string_lossy(scope),
            None => String::new(),
        },
        None => String::new(),
    }
}

/// `Value::to_integer` read as a raw `Integer::value()` (`-1` when the
/// conversion throws).
fn to_integer_i64(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> i64 {
    value.to_integer(scope).map(|i| i.value()).unwrap_or(-1)
}

/// `Value::to_big_int` under a TryCatch: (present, i64 value or -1, caught).
fn to_big_int_triple(
    scope: &mut v8::PinScope<'_, '_>,
    value: v8::Local<'_, v8::Value>,
) -> (bool, i64, bool) {
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let r = value.to_big_int(tc);
    let present = r.is_some();
    let caught = tc.has_caught();
    let int_value = r.map(|b| b.i64_value().0).unwrap_or(-1);
    (present, int_value, caught)
}

/// `Value::to_detail_string` under a TryCatch: (present, text).
fn to_detail_pair(
    scope: &mut v8::PinScope<'_, '_>,
    value: v8::Local<'_, v8::Value>,
) -> (bool, String) {
    let tc = std::pin::pin!(v8::TryCatch::new(scope));
    let tc = &mut tc.init();
    let r = value.to_detail_string(tc);
    let text = r.map(|s| s.to_rust_string_lossy(tc)).unwrap_or_default();
    (r.is_some(), text)
}

/// A global by name, falling back to `undefined` when absent.
fn global_or_undefined<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    name: &str,
) -> v8::Local<'s, v8::Value> {
    global_value(scope, name).unwrap_or_else(|| v8::undefined(scope).into())
}

// Callback counters. Check execution is single-threaded and each check
// resets the counters it uses, so `Cell` statics are deterministic.
thread_local! {
    static INT_HITS: Cell<u32> = const { Cell::new(0) };
    static LAZY_HITS: Cell<u32> = const { Cell::new(0) };
    static ACC_GET_HITS: Cell<u32> = const { Cell::new(0) };
    static ACC_SET_HITS: Cell<u32> = const { Cell::new(0) };
    /// Value stored by the instance accessor setter.
    static ACC_STATE: Cell<i64> = const { Cell::new(0) };
}

fn reset_counters() {
    INT_HITS.with(|c| c.set(0));
    LAZY_HITS.with(|c| c.set(0));
    ACC_GET_HITS.with(|c| c.set(0));
    ACC_SET_HITS.with(|c| c.set(0));
    ACC_STATE.with(|c| c.set(0));
}

/// Named interceptor that answers ONLY the key "in_a" (with 10) and falls
/// through for everything else. Hit counter counts kYes answers only.
fn interceptor_in_a(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) -> v8::Intercepted {
    let key_text: v8::Local<v8::Value> = key.into();
    if value_text(scope, key_text) == "in_a" {
        INT_HITS.with(|c| c.set(c.get() + 1));
        rv.set_int32(10);
        v8::Intercepted::kYes
    } else {
        v8::Intercepted::kNo
    }
}

/// Lazy data property getter: returns 43 exactly once per installation.
fn lazy_getter_43(
    _scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    LAZY_HITS.with(|c| c.set(c.get() + 1));
    rv.set_int32(43);
}

/// Instance accessor getter: returns the setter-stored state.
fn instance_acc_get(
    _scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<'_, v8::Name>,
    _args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<'_, v8::Value>,
) {
    ACC_GET_HITS.with(|c| c.set(c.get() + 1));
    rv.set_int32(ACC_STATE.with(|c| c.get()) as i32);
}

/// Instance accessor setter: records the assigned integer.
fn instance_acc_set(
    scope: &mut v8::PinScope<'_, '_>,
    _key: v8::Local<'_, v8::Name>,
    value: v8::Local<'_, v8::Value>,
    _args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, ()>,
) {
    ACC_SET_HITS.with(|c| c.set(c.get() + 1));
    if let Some(n) = value.integer_value(scope) {
        ACC_STATE.with(|c| c.set(n));
    }
}

// ---------------------------------------------------------------------------
// Checks. Order is part of the observable contract (the fixture is ordered).
// ---------------------------------------------------------------------------

/// `Object::get_prototype` / `set_prototype`: plain objects start on
/// `Object.prototype`, `Object.prototype` itself has no prototype, the
/// prototype can be re-pointed and set to null, and cycles are rejected.
fn proto_get_and_set() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let obj = v8::Object::new(scope);
    let object_prototype = eval(scope, "Object.prototype").unwrap();
    let proto_of_plain = obj.get_prototype(scope);
    let proto_present = proto_of_plain.is_some();
    let proto_matches_object_prototype = proto_of_plain
        .map(|p| p.strict_equals(object_prototype))
        .unwrap_or(false);

    // The crate reports a null prototype as a *present* null value, not as
    // an empty handle: `get_prototype` on `Object.prototype` (and on an
    // object whose prototype was set to null) yields Some(null).
    let object_prototype_object = v8::Local::<v8::Object>::try_from(object_prototype).unwrap();
    let object_prototype_proto_is_null = object_prototype_object
        .get_prototype(scope)
        .map(|p| p.is_null())
        .unwrap_or(false);

    let parent = v8::Object::new(scope);
    let set_ok = obj.set_prototype(scope, parent.into());
    let proto_is_parent = obj
        .get_prototype(scope)
        .map(|p| p.strict_equals(parent.into()))
        .unwrap_or(false);

    let set_null_ok = obj.set_prototype(scope, v8::null(scope).into());
    let proto_null_after_null = obj
        .get_prototype(scope)
        .map(|p| p.is_null())
        .unwrap_or(false);

    // Cycle: a -> b is fine, then b -> a is refused. The API-level
    // SetPrototype does NOT raise "Cyclic __proto__ value" (that message
    // belongs to the JS path): the attempt yields an empty result without a
    // pending exception and leaves both prototypes untouched.
    let a = v8::Object::new(scope);
    let b = v8::Object::new(scope);
    let chain_ok = a.set_prototype(scope, b.into());
    let a_proto_is_b = a
        .get_prototype(scope)
        .map(|p| p.strict_equals(b.into()))
        .unwrap_or(false);
    let (cyclic_result, cyclic_caught, b_proto_is_object_prototype, a_proto_still_b) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let r = b.set_prototype(tc, a.into());
        let b_still = b
            .get_prototype(tc)
            .map(|p| p.strict_equals(object_prototype))
            .unwrap_or(false);
        let a_still_b = a
            .get_prototype(tc)
            .map(|p| p.strict_equals(b.into()))
            .unwrap_or(false);
        (r, tc.has_caught(), b_still, a_still_b)
    };

    let opt_bool = |value: Option<bool>| match value {
        Some(v) => Json::b(v),
        None => Json::Null,
    };
    let actual = Json::obj(vec![
        ("proto_present", Json::b(proto_present)),
        (
            "proto_matches_object_prototype",
            Json::b(proto_matches_object_prototype),
        ),
        (
            "object_prototype_proto_is_null",
            Json::b(object_prototype_proto_is_null),
        ),
        ("set_ok", opt_bool(set_ok)),
        ("proto_is_parent", Json::b(proto_is_parent)),
        ("set_null_ok", opt_bool(set_null_ok)),
        ("proto_null_after_null", Json::b(proto_null_after_null)),
        ("chain_ok", opt_bool(chain_ok)),
        ("a_proto_is_b", Json::b(a_proto_is_b)),
        ("cyclic_result", opt_bool(cyclic_result)),
        ("cyclic_caught", Json::b(cyclic_caught)),
        (
            "b_proto_is_object_prototype",
            Json::b(b_proto_is_object_prototype),
        ),
        ("a_proto_still_b", Json::b(a_proto_still_b)),
    ]);
    let expected = Json::obj(vec![
        ("proto_present", Json::b(true)),
        ("proto_matches_object_prototype", Json::b(true)),
        ("object_prototype_proto_is_null", Json::b(true)),
        ("set_ok", Json::b(true)),
        ("proto_is_parent", Json::b(true)),
        ("set_null_ok", Json::b(true)),
        ("proto_null_after_null", Json::b(true)),
        ("chain_ok", Json::b(true)),
        ("a_proto_is_b", Json::b(true)),
        ("cyclic_result", Json::Null),
        ("cyclic_caught", Json::b(false)),
        ("b_proto_is_object_prototype", Json::b(true)),
        ("a_proto_still_b", Json::b(true)),
    ]);
    vec![expect_eq("obj-ops/proto/get_and_set", expected, actual)]
}

/// `has` / `has_index` / `has_own_property` / `delete` / `delete_index`:
/// own-vs-chain lookup, deletes of missing/non-configurable/frozen
/// properties, index holes, and symbol-keyed deletes.
fn has_delete_family() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let _ = eval(
        scope,
        "globalThis.o = {a: 1, 5: 'five'};
         Object.defineProperty(globalThis.o, 'fixed', {value: 1, configurable: false});
         function Base() {} Base.prototype.inherited = 1;
         globalThis.child = new Base();
         globalThis.arr = [1, 2, 3];
         globalThis.frozen = Object.freeze({x: 9});",
    );
    let o = global_object(scope, "o").unwrap();
    let child = global_object(scope, "child").unwrap();
    let arr = global_object(scope, "arr").unwrap();
    let frozen = global_object(scope, "frozen").unwrap();

    let has_a = o.has(scope, v8::String::new(scope, "a").unwrap().into());
    let has_missing = o.has(scope, v8::String::new(scope, "missing").unwrap().into());
    let has_index_5 = o.has_index(scope, 5);
    let has_index_7 = o.has_index(scope, 7);
    let child_has_inherited = child.has(scope, name_key(scope, "inherited").into());
    let child_own_inherited = child.has_own_property(scope, name_key(scope, "inherited"));
    let o_own_a = o.has_own_property(scope, name_key(scope, "a"));

    let del_a = o.delete(scope, v8::String::new(scope, "a").unwrap().into());
    let has_a_after = o.has(scope, v8::String::new(scope, "a").unwrap().into());
    let del_missing = o.delete(scope, v8::String::new(scope, "missing").unwrap().into());
    let del_fixed = o.delete(scope, v8::String::new(scope, "fixed").unwrap().into());
    let del_frozen_x = frozen.delete(scope, v8::String::new(scope, "x").unwrap().into());
    let del_arr_1 = arr.delete_index(scope, 1);
    let arr_hole_undefined = eval_text(scope, "arr[1] === undefined");
    let arr_hole_not_in = eval_text(scope, "!(1 in arr)");
    let arr_length_intact = eval_text(scope, "arr.length");

    // Symbol-keyed delete through the same API.
    let sym: v8::Local<v8::Value> = v8::Symbol::new(scope, None).into();
    let sym_set = o.set(scope, sym, v8::Integer::new(scope, 1).into());
    let sym_has = o.has(scope, sym);
    let sym_del = o.delete(scope, sym);
    let sym_has_after = o.has(scope, sym);

    // Key conversion: a plain object converts to "[object Object]" (no
    // throw, plain miss), while a null-prototype object cannot convert and
    // the API reports `None` under a TryCatch.
    let plain_key_value: v8::Local<v8::Value> = eval(scope, "({})").unwrap();
    let plain_key_lookup = o.has(scope, plain_key_value);
    let plain_key_miss = eval_text(scope, "o['[object Object]']");
    let (bad_key_has, bad_key_caught, bad_key_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let unconvertible = eval(tc, "Object.create(null)").unwrap();
        let r = o.has(tc, unconvertible);
        (r, tc.has_caught(), caught_message!(tc))
    };

    let opt_bool = |value: Option<bool>| match value {
        Some(v) => Json::b(v),
        None => Json::Null,
    };
    let actual = Json::obj(vec![
        ("has_a", opt_bool(has_a)),
        ("has_missing", opt_bool(has_missing)),
        ("has_index_5", opt_bool(has_index_5)),
        ("has_index_7", opt_bool(has_index_7)),
        ("child_has_inherited", opt_bool(child_has_inherited)),
        ("child_own_inherited", opt_bool(child_own_inherited)),
        ("o_own_a", opt_bool(o_own_a)),
        ("del_a", opt_bool(del_a)),
        ("has_a_after", opt_bool(has_a_after)),
        ("del_missing", opt_bool(del_missing)),
        ("del_fixed", opt_bool(del_fixed)),
        ("del_frozen_x", opt_bool(del_frozen_x)),
        ("del_arr_1", opt_bool(del_arr_1)),
        ("arr_hole_undefined", Json::s(&arr_hole_undefined)),
        ("arr_hole_not_in", Json::s(&arr_hole_not_in)),
        ("arr_length_intact", Json::s(&arr_length_intact)),
        ("sym_set", opt_bool(sym_set)),
        ("sym_has", opt_bool(sym_has)),
        ("sym_del", opt_bool(sym_del)),
        ("sym_has_after", opt_bool(sym_has_after)),
        ("plain_key_lookup", opt_bool(plain_key_lookup)),
        ("plain_key_miss", Json::s(&plain_key_miss)),
        ("bad_key_has", opt_bool(bad_key_has)),
        ("bad_key_caught", Json::b(bad_key_caught)),
        ("bad_key_message", Json::s(&bad_key_message)),
    ]);
    let expected = Json::obj(vec![
        ("has_a", Json::b(true)),
        ("has_missing", Json::b(false)),
        ("has_index_5", Json::b(true)),
        ("has_index_7", Json::b(false)),
        ("child_has_inherited", Json::b(true)),
        ("child_own_inherited", Json::b(false)),
        ("o_own_a", Json::b(true)),
        ("del_a", Json::b(true)),
        ("has_a_after", Json::b(false)),
        ("del_missing", Json::b(true)),
        ("del_fixed", Json::b(false)),
        ("del_frozen_x", Json::b(false)),
        ("del_arr_1", Json::b(true)),
        ("arr_hole_undefined", Json::s("true")),
        ("arr_hole_not_in", Json::s("true")),
        ("arr_length_intact", Json::s("3")),
        ("sym_set", Json::b(true)),
        ("sym_has", Json::b(true)),
        ("sym_del", Json::b(true)),
        ("sym_has_after", Json::b(false)),
        ("plain_key_lookup", Json::b(false)),
        ("plain_key_miss", Json::s("undefined")),
        ("bad_key_has", Json::Null),
        ("bad_key_caught", Json::b(true)),
        (
            "bad_key_message",
            Json::s("Uncaught TypeError: Cannot convert object to primitive value"),
        ),
    ]);
    vec![expect_eq(
        "obj-ops/property/has_delete_family",
        expected,
        actual,
    )]
}

/// Real-named queries bypass interceptors: `has`/`get` consult the
/// interceptor while `has_real_named_property` / `get_real_named_property` /
/// `get_real_named_property_attributes` see only real (own or inherited)
/// properties.
fn real_named_interceptor_bypass() -> Vec<CheckOutcome> {
    reset_counters();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    ot.set_named_property_handler(
        v8::NamedPropertyHandlerConfiguration::new().getter(interceptor_in_a),
    );
    let io = ot.new_instance(scope).unwrap();

    // A real own property, created through the API (the setter interceptor
    // is absent, so plain `set` creates a real property).
    let real_value = v8::Integer::new(scope, 3);
    let real_set = io.set(scope, name_key(scope, "real").into(), real_value.into());

    // An inherited real property on a plain parent.
    let parent = v8::Object::new(scope);
    parent.set(
        scope,
        name_key(scope, "inh").into(),
        v8::Integer::new(scope, 9).into(),
    );
    set_global(scope, context, "parent", parent.into());
    let child = global_object(scope, "Object.create(parent)").unwrap();

    // A real own read-only property.
    set_global(scope, context, "io", io.into());
    let _ = eval(
        scope,
        "Object.defineProperty(io, 'ro', {value: 4, writable: false})",
    );

    let key_in_a = name_key(scope, "in_a");
    let intercepted_get = io.get(scope, key_in_a.into());
    let intercepted_get_hits = INT_HITS.with(|c| c.get());
    let intercepted_has = io.has(scope, key_in_a.into());
    let intercepted_has_hits = INT_HITS.with(|c| c.get());
    let intercepted_own = io.has_own_property(scope, key_in_a);
    let intercepted_own_hits = INT_HITS.with(|c| c.get());

    // The real-named family never consults the interceptor: the hit count
    // stays at the value reached through the ordinary queries above.
    let real_get = io.get_real_named_property(scope, key_in_a);
    let real_get_hits = INT_HITS.with(|c| c.get());
    let real_has_intercepted = io.has_real_named_property(scope, key_in_a);
    let real_has_hits = INT_HITS.with(|c| c.get());

    let key_real = name_key(scope, "real");
    let real_get_real = io.get_real_named_property(scope, key_real);
    let real_has_real = io.has_real_named_property(scope, key_real);
    let real_attrs = io.get_real_named_property_attributes(scope, key_real);
    let key_ro = name_key(scope, "ro");
    let ro_attrs = io.get_real_named_property_attributes(scope, key_ro);

    let key_inh = name_key(scope, "inh");
    let child_real_get = child.get_real_named_property(scope, key_inh);
    let child_real_has = child.has_real_named_property(scope, key_inh);
    let child_real_attrs = child.get_real_named_property_attributes(scope, key_inh);

    let key_missing = name_key(scope, "missing");
    let missing_real_get = io.get_real_named_property(scope, key_missing);
    let missing_real_has = io.has_real_named_property(scope, key_missing);
    let missing_real_attrs = io.get_real_named_property_attributes(scope, key_missing);
    let final_hits = INT_HITS.with(|c| c.get());

    let attrs_u32 = |value: Option<v8::PropertyAttribute>| match value {
        Some(a) => Json::i(a.as_u32() as i64),
        None => Json::Null,
    };
    let opt_bool = |value: Option<bool>| match value {
        Some(v) => Json::b(v),
        None => Json::Null,
    };
    let actual = Json::obj(vec![
        ("real_set", opt_bool(real_set)),
        ("intercepted_get", Json::i(int_of(scope, intercepted_get))),
        ("intercepted_get_hits", Json::i(intercepted_get_hits as i64)),
        ("intercepted_has", opt_bool(intercepted_has)),
        ("intercepted_has_hits", Json::i(intercepted_has_hits as i64)),
        ("intercepted_own", opt_bool(intercepted_own)),
        ("intercepted_own_hits", Json::i(intercepted_own_hits as i64)),
        ("real_get_intercepted_is_none", Json::b(real_get.is_none())),
        (
            "real_get_hits_unchanged",
            Json::b(real_get_hits == intercepted_own_hits),
        ),
        ("real_has_intercepted", opt_bool(real_has_intercepted)),
        (
            "real_has_hits_unchanged",
            Json::b(real_has_hits == intercepted_own_hits),
        ),
        ("real_get_real", Json::i(int_of(scope, real_get_real))),
        ("real_has_real", opt_bool(real_has_real)),
        ("real_attrs", attrs_u32(real_attrs)),
        // defineProperty defaults: {value: 4, writable: false} leaves
        // enumerable=false and configurable=false, so the real attributes
        // are READ_ONLY | DONT_ENUM | DONT_DELETE = 7.
        ("ro_attrs", attrs_u32(ro_attrs)),
        ("child_real_get", Json::i(int_of(scope, child_real_get))),
        ("child_real_has", opt_bool(child_real_has)),
        ("child_real_attrs", attrs_u32(child_real_attrs)),
        (
            "missing_real_get_is_none",
            Json::b(missing_real_get.is_none()),
        ),
        ("missing_real_has", opt_bool(missing_real_has)),
        ("missing_real_attrs", attrs_u32(missing_real_attrs)),
        ("final_hits", Json::i(final_hits as i64)),
    ]);
    let expected = Json::obj(vec![
        ("real_set", Json::b(true)),
        ("intercepted_get", Json::i(10)),
        ("intercepted_get_hits", Json::i(1)),
        ("intercepted_has", Json::b(true)),
        ("intercepted_has_hits", Json::i(2)),
        ("intercepted_own", Json::b(true)),
        ("intercepted_own_hits", Json::i(3)),
        ("real_get_intercepted_is_none", Json::b(true)),
        ("real_get_hits_unchanged", Json::b(true)),
        ("real_has_intercepted", Json::b(false)),
        ("real_has_hits_unchanged", Json::b(true)),
        ("real_get_real", Json::i(3)),
        ("real_has_real", Json::b(true)),
        ("real_attrs", Json::i(0)),
        ("ro_attrs", Json::i(7)),
        ("child_real_get", Json::i(9)),
        // HasRealNamedProperty is own-only in this build: the inherited
        // real property is found by GetRealNamedProperty (9) but not by
        // HasRealNamedProperty.
        ("child_real_has", Json::b(false)),
        ("child_real_attrs", Json::i(0)),
        ("missing_real_get_is_none", Json::b(true)),
        ("missing_real_has", Json::b(false)),
        ("missing_real_attrs", Json::Null),
        ("final_hits", Json::i(3)),
    ]);
    vec![expect_eq(
        "obj-ops/property/real_named_interceptor_bypass",
        expected,
        actual,
    )]
}

/// `Object::get_identity_hash` is never zero, stable within the isolate's
/// lifetime, and identical to `Value::get_hash` for the same object.
fn identity_hash_contract() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let obj = v8::Object::new(scope);
    let hash1 = obj.get_identity_hash();
    let hash2 = obj.get_identity_hash();
    let as_value: v8::Local<v8::Value> = obj.into();
    let value_hash = as_value.get_hash();

    let actual = Json::obj(vec![
        ("nonzero", Json::b(hash1.get() != 0)),
        ("stable", Json::b(hash1 == hash2)),
        (
            "matches_value_get_hash",
            Json::b(value_hash as i32 == hash1.get()),
        ),
    ]);
    let expected = Json::obj(vec![
        ("nonzero", Json::b(true)),
        ("stable", Json::b(true)),
        ("matches_value_get_hash", Json::b(true)),
    ]);
    vec![expect_eq(
        "obj-ops/identity/identity_hash",
        expected,
        actual,
    )]
}

/// `Object::get_creation_context` reports the context an object was created
/// in, even when queried from a different context scope, and covers the
/// native objects of the context itself.
fn creation_context() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let ctx1 = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, ctx1);

    // Objects created inside ctx1.
    let plain = v8::Object::new(scope);
    let js_literal = eval(scope, "({made: 'in js'})").unwrap();
    let object_prototype = eval(scope, "Object.prototype").unwrap();
    let global_proxy = ctx1.global(scope);

    let plain_ctx = plain.get_creation_context(scope);
    let js_ctx = js_literal
        .try_cast::<v8::Object>()
        .ok()
        .and_then(|o| o.get_creation_context(scope));
    let prototype_ctx = v8::Local::<v8::Object>::try_from(object_prototype)
        .unwrap()
        .get_creation_context(scope);
    let global_ctx = global_proxy.get_creation_context(scope);

    // An object created in a second context keeps that context even while
    // ctx1 is the entered context.
    let ctx2 = v8::Context::new(scope, Default::default());
    let obj2 = {
        let inner = &mut v8::ContextScope::new(scope, ctx2);
        v8::Object::new(inner)
    };
    let obj2_ctx = obj2.get_creation_context(scope);

    let ctx_of = |value: Option<v8::Local<v8::Context>>, which: v8::Local<v8::Context>| {
        value.map(|c| c == which).unwrap_or(false)
    };
    let actual = Json::obj(vec![
        ("plain_is_ctx1", Json::b(ctx_of(plain_ctx, ctx1))),
        ("js_literal_is_ctx1", Json::b(ctx_of(js_ctx, ctx1))),
        (
            "object_prototype_is_ctx1",
            Json::b(ctx_of(prototype_ctx, ctx1)),
        ),
        ("global_is_ctx1", Json::b(ctx_of(global_ctx, ctx1))),
        ("obj2_is_ctx2", Json::b(ctx_of(obj2_ctx, ctx2))),
    ]);
    let expected = Json::obj(vec![
        ("plain_is_ctx1", Json::b(true)),
        ("js_literal_is_ctx1", Json::b(true)),
        ("object_prototype_is_ctx1", Json::b(true)),
        ("global_is_ctx1", Json::b(true)),
        ("obj2_is_ctx2", Json::b(true)),
    ]);
    vec![expect_eq(
        "obj-ops/identity/creation_context",
        expected,
        actual,
    )]
}

/// `Object::get_constructor_name`: the name of the function invoked as
/// constructor, including the `Reflect.construct` new.target name and the
/// builtin constructors.
fn constructor_name() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let _ = eval(
        scope,
        "function Foo() {}
         class Bar {}
         class Sub extends Bar {}
         globalThis.fooI = new Foo();
         globalThis.barI = new Bar();
         globalThis.subI = new Sub();
         globalThis.weirdI = Reflect.construct(Array, [], function Weird() {});",
    );

    let api_object = v8::Object::new(scope);
    let actual = Json::obj(vec![
        (
            "api_object",
            Json::s(
                &api_object
                    .get_constructor_name()
                    .to_rust_string_lossy(scope),
            ),
        ),
        ("js_literal", Json::s(&constructor_name_of(scope, "({})"))),
        ("foo_instance", Json::s(&constructor_name_of(scope, "fooI"))),
        ("bar_instance", Json::s(&constructor_name_of(scope, "barI"))),
        ("sub_instance", Json::s(&constructor_name_of(scope, "subI"))),
        ("array_literal", Json::s(&constructor_name_of(scope, "[]"))),
        (
            "error_instance",
            Json::s(&constructor_name_of(scope, "new Error('e')")),
        ),
        (
            "function_itself",
            Json::s(&constructor_name_of(scope, "(function f() {})")),
        ),
        ("class_itself", Json::s(&constructor_name_of(scope, "Bar"))),
        (
            "reflect_construct_target",
            Json::s(&constructor_name_of(scope, "weirdI")),
        ),
    ]);
    let expected = Json::obj(vec![
        ("api_object", Json::s("Object")),
        ("js_literal", Json::s("Object")),
        ("foo_instance", Json::s("Foo")),
        ("bar_instance", Json::s("Bar")),
        ("sub_instance", Json::s("Sub")),
        ("array_literal", Json::s("Array")),
        ("error_instance", Json::s("Error")),
        // A function object (declared function or class) reports the
        // generic "Function" constructor name; only *instances* carry the
        // constructing function's name.
        ("function_itself", Json::s("Function")),
        ("class_itself", Json::s("Function")),
        ("reflect_construct_target", Json::s("Weird")),
    ]);
    vec![expect_eq(
        "obj-ops/identity/constructor_name",
        expected,
        actual,
    )]
}

/// `get_with_receiver` / `set_with_receiver`: the receiver becomes `this`
/// for accessors (even when unrelated to the holder), is the lookup start
/// for prototype accessors, and receives redirected data-property writes.
fn get_set_with_receiver() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let _ = eval(
        scope,
        "globalThis.holder = {};
         Object.defineProperty(globalThis.holder, 'g', {
           get: function () {
             return (typeof this) + ':' + ((this && this.tag) ? this.tag : 'none');
           },
           configurable: true
         });
         globalThis.proto = {
           get t() { return this.x; },
           set s(v) { this.saved = v; }
         };
         globalThis.child = Object.create(globalThis.proto);
         globalThis.child.x = 7;
         globalThis.stranger = {tag: 'recv'};
         globalThis.plain = {p: 1};",
    );
    let holder = global_object(scope, "holder").unwrap();
    let proto = global_object(scope, "proto").unwrap();
    let child = global_object(scope, "child").unwrap();
    let stranger = global_object(scope, "stranger").unwrap();
    let plain = global_object(scope, "plain").unwrap();
    let other = v8::Object::new(scope);

    let key_g = name_key(scope, "g");
    let key_t = name_key(scope, "t");
    let key_s = name_key(scope, "s");
    let key_p = name_key(scope, "p");

    let get_default = holder.get(scope, key_g.into());
    let get_with_recv = holder.get_with_receiver(scope, key_g.into(), stranger);
    let proto_t_default = proto.get(scope, key_t.into());
    let proto_t_child = proto.get_with_receiver(scope, key_t.into(), child);

    let five: v8::Local<v8::Value> = v8::Integer::new(scope, 5).into();
    let setter_via_receiver = proto.set_with_receiver(scope, key_s.into(), five, child);
    let child_saved = eval_text(scope, "child.saved");
    let six: v8::Local<v8::Value> = v8::Integer::new(scope, 6).into();
    let setter_on_proto = proto.set(scope, key_s.into(), six);
    let proto_saved = eval_text(scope, "proto.saved");
    let child_saved_after = eval_text(scope, "child.saved");

    // Getter-only accessor: the write is silently dropped (sloppy mode).
    let one: v8::Local<v8::Value> = v8::Integer::new(scope, 1).into();
    let set_getter_only = holder.set_with_receiver(scope, key_g.into(), one, stranger);
    let got_unchanged = holder.get(scope, key_g.into());
    let getter_unchanged = value_text(scope, got_unchanged.unwrap());

    // Data property redirect: writing through an unrelated receiver creates
    // the property on the receiver.
    let forty_two: v8::Local<v8::Value> = v8::Integer::new(scope, 42).into();
    let redirect = plain.set_with_receiver(scope, key_p.into(), forty_two, other);
    let other_got = other.get(scope, key_p.into());
    let other_p = int_of(scope, other_got);
    let plain_got = plain.get(scope, key_p.into());
    let plain_p = int_of(scope, plain_got);

    let opt_bool = |value: Option<bool>| match value {
        Some(v) => Json::b(v),
        None => Json::Null,
    };
    let actual = Json::obj(vec![
        (
            "get_default",
            Json::s(&value_text(scope, get_default.unwrap())),
        ),
        (
            "get_with_receiver",
            Json::s(&value_text(scope, get_with_recv.unwrap())),
        ),
        (
            "proto_t_default_is_undefined",
            Json::b(proto_t_default.unwrap().is_undefined()),
        ),
        ("proto_t_child", Json::i(int_of(scope, proto_t_child))),
        ("setter_via_receiver", opt_bool(setter_via_receiver)),
        ("child_saved", Json::s(&child_saved)),
        ("setter_on_proto", opt_bool(setter_on_proto)),
        ("proto_saved", Json::s(&proto_saved)),
        ("child_saved_after", Json::s(&child_saved_after)),
        ("set_getter_only", opt_bool(set_getter_only)),
        ("getter_unchanged", Json::s(&getter_unchanged)),
        ("redirect", opt_bool(redirect)),
        ("other_p", Json::i(other_p)),
        ("plain_p", Json::i(plain_p)),
    ]);
    let expected = Json::obj(vec![
        ("get_default", Json::s("object:none")),
        ("get_with_receiver", Json::s("object:recv")),
        ("proto_t_default_is_undefined", Json::b(true)),
        ("proto_t_child", Json::i(7)),
        ("setter_via_receiver", Json::b(true)),
        ("child_saved", Json::s("5")),
        ("setter_on_proto", Json::b(true)),
        ("proto_saved", Json::s("6")),
        ("child_saved_after", Json::s("5")),
        ("set_getter_only", Json::b(true)),
        ("getter_unchanged", Json::s("object:none")),
        ("redirect", Json::b(true)),
        ("other_p", Json::i(42)),
        ("plain_p", Json::i(1)),
    ]);
    vec![expect_eq(
        "obj-ops/receiver/get_set_with_receiver",
        expected,
        actual,
    )]
}

/// `Object::set_lazy_data_property`: the getter runs on first read, the
/// property is then an ordinary data property, and later reads never
/// re-invoke the getter.
fn lazy_data_property() -> Vec<CheckOutcome> {
    reset_counters();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let obj = v8::Object::new(scope);
    set_global(scope, context, "lo", obj.into());
    let key = name_key(scope, "lazy");

    let install = obj.set_lazy_data_property(scope, key, lazy_getter_43);
    let own_before_read = obj.has_own_property(scope, key);
    let hits_before_read = LAZY_HITS.with(|c| c.get());

    let first_got = obj.get(scope, key.into());
    let first = int_of(scope, first_got);
    let hits_after_first = LAZY_HITS.with(|c| c.get());
    let second_got = obj.get(scope, key.into());
    let second = int_of(scope, second_got);
    let hits_after_second = LAZY_HITS.with(|c| c.get());

    // After materialization the property is a plain data property.
    let descriptor_get = eval_text(
        scope,
        "typeof Object.getOwnPropertyDescriptor(lo, 'lazy').get",
    );
    let descriptor_value = eval_text(scope, "Object.getOwnPropertyDescriptor(lo, 'lazy').value");
    let js_read = eval_text(scope, "lo.lazy");
    let hits_after_js = LAZY_HITS.with(|c| c.get());

    let opt_bool = |value: Option<bool>| match value {
        Some(v) => Json::b(v),
        None => Json::Null,
    };
    let actual = Json::obj(vec![
        ("install", opt_bool(install)),
        ("own_before_read", opt_bool(own_before_read)),
        ("hits_before_read", Json::i(hits_before_read as i64)),
        ("first", Json::i(first)),
        ("hits_after_first", Json::i(hits_after_first as i64)),
        ("second", Json::i(second)),
        ("hits_after_second", Json::i(hits_after_second as i64)),
        ("descriptor_get", Json::s(&descriptor_get)),
        ("descriptor_value", Json::s(&descriptor_value)),
        ("js_read", Json::s(&js_read)),
        ("hits_after_js", Json::i(hits_after_js as i64)),
    ]);
    let expected = Json::obj(vec![
        ("install", Json::b(true)),
        ("own_before_read", Json::b(true)),
        ("hits_before_read", Json::i(0)),
        ("first", Json::i(43)),
        ("hits_after_first", Json::i(1)),
        ("second", Json::i(43)),
        ("hits_after_second", Json::i(1)),
        ("descriptor_get", Json::s("undefined")),
        ("descriptor_value", Json::s("43")),
        ("js_read", Json::s("43")),
        ("hits_after_js", Json::i(1)),
    ]);
    vec![expect_eq(
        "obj-ops/lazy/lazy_data_property",
        expected,
        actual,
    )]
}

/// Instance-level `Object::set_accessor_with_setter`: getter/setter pairs
/// live on the object (not the template); the setter routes through
/// `Object::set`. JS property descriptors report AccessorInfo-backed API
/// accessors as data-property-shaped (current value, no get/set slots) —
/// unlike template-level `set_accessor_property` accessors, which do
/// expose get/set functions (pinned by the tpladv slice).
fn instance_accessor() -> Vec<CheckOutcome> {
    reset_counters();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let obj = v8::Object::new(scope);
    set_global(scope, context, "ia", obj.into());
    let key = name_key(scope, "x");

    let install = obj.set_accessor_with_setter(scope, key, instance_acc_get, instance_acc_set);

    let got_first = obj.get(scope, key.into());
    let first = int_of(scope, got_first);
    let get_hits_after_first = ACC_GET_HITS.with(|c| c.get());

    let write_value: v8::Local<v8::Value> = v8::Integer::new(scope, 21).into();
    let write = obj.set(scope, key.into(), write_value);
    let set_hits = ACC_SET_HITS.with(|c| c.get());
    let got_second = obj.get(scope, key.into());
    let second = int_of(scope, got_second);
    let get_hits_after_second = ACC_GET_HITS.with(|c| c.get());

    let desc_has_get = eval_text(
        scope,
        "typeof Object.getOwnPropertyDescriptor(ia, 'x').get === 'function'",
    );
    let desc_has_set = eval_text(
        scope,
        "typeof Object.getOwnPropertyDescriptor(ia, 'x').set === 'function'",
    );
    let desc_value_is_undefined = eval_text(
        scope,
        "Object.getOwnPropertyDescriptor(ia, 'x').value === undefined",
    );
    // API-level accessors are AccessorInfo-backed: to JS descriptors they
    // appear as plain data properties carrying the current value, not as
    // get/set functions (unlike template-level `set_accessor_property`).
    let desc_value_text = eval_text(
        scope,
        "String(Object.getOwnPropertyDescriptor(ia, 'x').value)",
    );
    let js_write = eval_text(scope, "(ia.x = 33) === 33");
    let js_read = eval_text(scope, "ia.x");

    let opt_bool = |value: Option<bool>| match value {
        Some(v) => Json::b(v),
        None => Json::Null,
    };
    let actual = Json::obj(vec![
        ("install", opt_bool(install)),
        ("first", Json::i(first)),
        ("get_hits_after_first", Json::i(get_hits_after_first as i64)),
        ("write", opt_bool(write)),
        ("set_hits", Json::i(set_hits as i64)),
        ("second", Json::i(second)),
        (
            "get_hits_after_second",
            Json::i(get_hits_after_second as i64),
        ),
        ("desc_has_get", Json::s(&desc_has_get)),
        ("desc_has_set", Json::s(&desc_has_set)),
        ("desc_value_is_undefined", Json::s(&desc_value_is_undefined)),
        ("desc_value_text", Json::s(&desc_value_text)),
        ("js_write", Json::s(&js_write)),
        ("js_read", Json::s(&js_read)),
    ]);
    let expected = Json::obj(vec![
        ("install", Json::b(true)),
        ("first", Json::i(0)),
        ("get_hits_after_first", Json::i(1)),
        ("write", Json::b(true)),
        ("set_hits", Json::i(1)),
        ("second", Json::i(21)),
        ("get_hits_after_second", Json::i(2)),
        ("desc_has_get", Json::s("false")),
        ("desc_has_set", Json::s("false")),
        ("desc_value_is_undefined", Json::s("false")),
        // The descriptor snapshot reads the current accessor value (21).
        ("desc_value_text", Json::s("21")),
        ("js_write", Json::s("true")),
        ("js_read", Json::s("33")),
    ]);
    vec![expect_eq(
        "obj-ops/lazy/instance_accessor",
        expected,
        actual,
    )]
}

/// A plain object is neither callable nor a constructor: both call-as-*
/// entry points fail with a TypeError under the TryCatch.
fn call_plain_object_not_callable() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let obj = v8::Object::new(scope);
    let is_callable = obj.is_callable();
    let is_constructor = obj.is_constructor();

    let (call_result, call_caught, call_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let undef: v8::Local<v8::Value> = v8::undefined(tc).into();
        let r = obj.call_as_function(tc, undef, &[]);
        (r.is_some(), tc.has_caught(), caught_message!(tc))
    };
    let (ctor_result, ctor_caught, ctor_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let r = obj.call_as_constructor(tc, &[]);
        (r.is_some(), tc.has_caught(), caught_message!(tc))
    };

    let actual = Json::obj(vec![
        ("is_callable", Json::b(is_callable)),
        ("is_constructor", Json::b(is_constructor)),
        ("call_result", Json::b(call_result)),
        ("call_caught", Json::b(call_caught)),
        ("call_message", Json::s(&call_message)),
        ("ctor_result", Json::b(ctor_result)),
        ("ctor_caught", Json::b(ctor_caught)),
        ("ctor_message", Json::s(&ctor_message)),
    ]);
    let expected = Json::obj(vec![
        ("is_callable", Json::b(false)),
        ("is_constructor", Json::b(false)),
        ("call_result", Json::b(false)),
        ("call_caught", Json::b(true)),
        (
            "call_message",
            Json::s("Uncaught TypeError: object is not a function"),
        ),
        ("ctor_result", Json::b(false)),
        ("ctor_caught", Json::b(true)),
        (
            "ctor_message",
            Json::s("Uncaught TypeError: object is not a constructor"),
        ),
    ]);
    vec![expect_eq(
        "obj-ops/call/plain_object_not_callable",
        expected,
        actual,
    )]
}

/// `call_as_function[_with_context]` and `call_as_constructor[
/// _with_context]` on real JS functions: argument passing, receiver
/// binding, constructor `this` semantics, object-return constructors, and
/// the class-without-new rejection.
fn call_function_call_and_construct() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let add = global_object(scope, "(function add(a, b) { return a + b; })").unwrap();
    let what = global_object(scope, "(function what() { return this; })").unwrap();
    let maker = global_object(scope, "(function Maker() { this.tag = 'made'; })").unwrap();
    let returner = global_object(scope, "(function Returner() { return {custom: 1}; })").unwrap();
    let klass = global_object(scope, "(class K { constructor(v) { this.v = v; } })").unwrap();
    let arrow = global_object(scope, "((a) => a * 2)").unwrap();

    let context2 = v8::Context::new(scope, Default::default());
    let global = context.global(scope);

    let five: v8::Local<v8::Value> = v8::Integer::new(scope, 5).into();
    let seven: v8::Local<v8::Value> = v8::Integer::new(scope, 7).into();
    let add_args = [five, seven];
    let add_got = add.call_as_function(scope, v8::undefined(scope).into(), &add_args);
    let add_result = int_of(scope, add_got);

    let twenty: v8::Local<v8::Value> = v8::Integer::new(scope, 20).into();
    let twenty_two: v8::Local<v8::Value> = v8::Integer::new(scope, 22).into();
    let ctx_args = [twenty, twenty_two];
    let ctx_got =
        add.call_as_function_with_context(scope, context2, v8::undefined(scope).into(), &ctx_args);
    let with_context_result = int_of(scope, ctx_got);

    let receiver = v8::Object::new(scope);
    let receiver_got = what.call_as_function(scope, receiver.into(), &[]);
    let bound_receiver = receiver_got
        .map(|r| r.strict_equals(receiver.into()))
        .unwrap_or(false);
    let global_got = what.call_as_function(scope, v8::undefined(scope).into(), &[]);
    let undefined_receiver = global_got
        .map(|r| r.strict_equals(global.into()))
        .unwrap_or(false);

    let zero: v8::Local<v8::Value> = v8::Integer::new(scope, 0).into();
    let made = maker.call_as_constructor(scope, &[zero]);
    let made_is_object = made.as_ref().map(|m| m.is_object()).unwrap_or(false);
    let tag_key = name_key(scope, "tag");
    let made_tag = made
        .as_ref()
        .and_then(|m| m.try_cast::<v8::Object>().ok())
        .and_then(|m| m.get(scope, tag_key.into()))
        .map(|t| value_text(scope, t))
        .unwrap_or_default();
    let made_instanceof_maker = made
        .as_ref()
        .and_then(|m| m.instance_of(scope, maker))
        .unwrap_or(false);
    let made_with_context = maker
        .call_as_constructor_with_context(scope, context2, &[])
        .map(|m| m.is_object())
        .unwrap_or(false);

    let returned = returner.call_as_constructor(scope, &[]);
    let custom_key = name_key(scope, "custom");
    let returned_custom = returned
        .as_ref()
        .and_then(|r| r.try_cast::<v8::Object>().ok())
        .and_then(|r| r.get(scope, custom_key.into()))
        .map(|g| int_of(scope, Some(g)))
        .unwrap_or(-1);
    let returned_instanceof_returner = returned
        .as_ref()
        .and_then(|r| r.instance_of(scope, returner))
        .unwrap_or(false);

    let nine: v8::Local<v8::Value> = v8::Integer::new(scope, 9).into();
    let v_key = name_key(scope, "v");
    let klass_construct = klass
        .call_as_constructor(scope, &[nine])
        .and_then(|k| k.try_cast::<v8::Object>().ok())
        .and_then(|k| k.get(scope, v_key.into()))
        .map(|g| int_of(scope, Some(g)))
        .unwrap_or(-1);
    let (klass_call, klass_call_caught, klass_call_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let undef: v8::Local<v8::Value> = v8::undefined(tc).into();
        let r = klass.call_as_function(tc, undef, &[]);
        (r.is_some(), tc.has_caught(), caught_message!(tc))
    };

    let twenty_one: v8::Local<v8::Value> = v8::Integer::new(scope, 21).into();
    let arrow_args = [twenty_one];
    let arrow_got = arrow.call_as_function(scope, v8::undefined(scope).into(), &arrow_args);
    let arrow_call = int_of(scope, arrow_got);
    let (arrow_construct, arrow_construct_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let r = arrow.call_as_constructor(tc, &[]);
        (r.is_some(), tc.has_caught())
    };

    let actual = Json::obj(vec![
        ("add_result", Json::i(add_result)),
        ("with_context_result", Json::i(with_context_result)),
        ("bound_receiver", Json::b(bound_receiver)),
        ("undefined_receiver_is_global", Json::b(undefined_receiver)),
        ("made_is_object", Json::b(made_is_object)),
        ("made_tag", Json::s(&made_tag)),
        ("made_instanceof_maker", Json::b(made_instanceof_maker)),
        ("made_with_context", Json::b(made_with_context)),
        ("returned_custom", Json::i(returned_custom)),
        (
            "returned_instanceof_returner",
            Json::b(returned_instanceof_returner),
        ),
        ("klass_construct_v", Json::i(klass_construct)),
        ("klass_call", Json::b(klass_call)),
        ("klass_call_caught", Json::b(klass_call_caught)),
        ("klass_call_message", Json::s(&klass_call_message)),
        ("arrow_call", Json::i(arrow_call)),
        ("arrow_construct", Json::b(arrow_construct)),
        ("arrow_construct_caught", Json::b(arrow_construct_caught)),
    ]);
    let expected = Json::obj(vec![
        ("add_result", Json::i(12)),
        ("with_context_result", Json::i(42)),
        ("bound_receiver", Json::b(true)),
        ("undefined_receiver_is_global", Json::b(true)),
        ("made_is_object", Json::b(true)),
        ("made_tag", Json::s("made")),
        ("made_instanceof_maker", Json::b(true)),
        ("made_with_context", Json::b(true)),
        ("returned_custom", Json::i(1)),
        ("returned_instanceof_returner", Json::b(false)),
        ("klass_construct_v", Json::i(9)),
        ("klass_call", Json::b(false)),
        ("klass_call_caught", Json::b(true)),
        (
            "klass_call_message",
            Json::s("Uncaught TypeError: Class constructor K cannot be invoked without 'new'"),
        ),
        ("arrow_call", Json::i(42)),
        ("arrow_construct", Json::b(false)),
        ("arrow_construct_caught", Json::b(true)),
    ]);
    vec![expect_eq(
        "obj-ops/call/function_call_and_construct",
        expected,
        actual,
    )]
}

/// `Object::is_callable` / `is_constructor` over the exotic-object matrix.
fn callable_constructor_predicates() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let samples: &[(&'static str, &str)] = &[
        ("plain_object", "({})"),
        ("function", "(function f() {})"),
        ("arrow", "((a) => a)"),
        ("generator_function", "(function* g() {})"),
        ("async_function", "(async function af() {})"),
        ("class_constructor", "(class K {})"),
        ("method", "({ m() {} }).m"),
        ("bound_function", "(function g() {}.bind({}))"),
        ("proxy_of_function", "(new Proxy(function () {}, {}))"),
        ("proxy_of_object", "(new Proxy({}, {}))"),
        ("builtin_nonconstructable", "Math.max"),
        ("builtin_constructable", "Date"),
    ];

    let mut entries: Vec<(&'static str, Json)> = Vec::new();
    for (name, source) in samples {
        let (callable, constructor) = match global_object(scope, source) {
            Some(o) => (o.is_callable(), o.is_constructor()),
            None => (false, false),
        };
        entries.push((
            name,
            Json::obj(vec![
                ("is_callable", Json::b(callable)),
                ("is_constructor", Json::b(constructor)),
            ]),
        ));
    }
    let actual = Json::obj(entries);
    let expected = Json::obj(vec![
        (
            "plain_object",
            Json::obj(vec![
                ("is_callable", Json::b(false)),
                ("is_constructor", Json::b(false)),
            ]),
        ),
        (
            "function",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(true)),
            ]),
        ),
        (
            "arrow",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(false)),
            ]),
        ),
        (
            "generator_function",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(false)),
            ]),
        ),
        (
            "async_function",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(false)),
            ]),
        ),
        (
            "class_constructor",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(true)),
            ]),
        ),
        (
            "method",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(false)),
            ]),
        ),
        (
            "bound_function",
            // IsConstructor follows the bound target: a bound function of a
            // constructable function is itself constructable (`new (f.bind())`
            // forwards to f); its [[Call]] is the bound call.
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(true)),
            ]),
        ),
        (
            "proxy_of_function",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(true)),
            ]),
        ),
        (
            "proxy_of_object",
            Json::obj(vec![
                ("is_callable", Json::b(false)),
                ("is_constructor", Json::b(false)),
            ]),
        ),
        (
            "builtin_nonconstructable",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(false)),
            ]),
        ),
        (
            "builtin_constructable",
            Json::obj(vec![
                ("is_callable", Json::b(true)),
                ("is_constructor", Json::b(true)),
            ]),
        ),
    ]);
    vec![expect_eq(
        "obj-ops/call/callable_constructor_predicates",
        expected,
        actual,
    )]
}

/// `Value::to_object`: primitive wrappers for number/string/boolean/symbol/
/// bigint, identity for objects, and the null/undefined TypeError.
fn to_object_matrix() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let number_proto = eval(scope, "Number.prototype").unwrap();
    let plain = v8::Object::new(scope);

    // (name, value) pairs - factories run inside a TryCatch where needed.
    let number = v8::Number::new(scope, 5.0);
    let string = v8::String::new(scope, "hi").unwrap();
    let boolean = v8::Boolean::new(scope, true);
    let symbol = v8::Symbol::new(scope, None);
    let bigint = v8::BigInt::new_from_i64(scope, 7);

    let (undefined_result, undefined_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let r = v8::undefined(tc).to_object(tc);
        (r.is_some(), tc.has_caught())
    };
    let (null_result, null_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let r = v8::null(tc).to_object(tc);
        (r.is_some(), tc.has_caught())
    };
    let wrapper_number = number.to_object(scope);
    let number_wrapped = match wrapper_number.as_ref() {
        Some(o) => {
            let value: v8::Local<v8::Value> = (*o).into();
            let is_number_object = value.is_number_object();
            let type_of_value = value.type_of(scope);
            let type_of_text = value_text(scope, type_of_value.into());
            let string_value = value.to_string(scope);
            let to_string_text = string_value
                .map(|s| s.to_rust_string_lossy(scope))
                .unwrap_or_default();
            let proto_matches = o
                .get_prototype(scope)
                .map(|p| p.strict_equals(number_proto))
                .unwrap_or(false);
            Json::obj(vec![
                ("is_number_object", Json::b(is_number_object)),
                ("type_of", Json::s(&type_of_text)),
                ("to_string", Json::s(&to_string_text)),
                ("proto_is_number_prototype", Json::b(proto_matches)),
            ])
        }
        None => Json::Null,
    };
    let string_wrapped = match string.to_object(scope) {
        Some(o) => {
            let value: v8::Local<v8::Value> = o.into();
            value.is_string_object()
        }
        None => false,
    };
    let boolean_wrapped = match boolean.to_object(scope) {
        Some(o) => {
            let value: v8::Local<v8::Value> = o.into();
            value.is_boolean_object()
        }
        None => false,
    };
    let symbol_wrapped = match symbol.to_object(scope) {
        Some(o) => {
            let value: v8::Local<v8::Value> = o.into();
            value.is_symbol_object()
        }
        None => false,
    };
    let bigint_wrapped = match bigint.to_object(scope) {
        Some(o) => {
            let value: v8::Local<v8::Value> = o.into();
            value.is_big_int_object()
        }
        None => false,
    };
    let identity = match plain.to_object(scope) {
        Some(o) => o.strict_equals(plain.into()),
        None => false,
    };

    let actual = Json::obj(vec![
        ("undefined_result", Json::b(undefined_result)),
        ("undefined_caught", Json::b(undefined_caught)),
        ("null_result", Json::b(null_result)),
        ("null_caught", Json::b(null_caught)),
        ("number_wrapper", number_wrapped),
        ("string_wrapper", Json::b(string_wrapped)),
        ("boolean_wrapper", Json::b(boolean_wrapped)),
        ("symbol_wrapper", Json::b(symbol_wrapped)),
        ("bigint_wrapper", Json::b(bigint_wrapped)),
        ("object_identity", Json::b(identity)),
    ]);
    let expected = Json::obj(vec![
        ("undefined_result", Json::b(false)),
        ("undefined_caught", Json::b(true)),
        ("null_result", Json::b(false)),
        ("null_caught", Json::b(true)),
        (
            "number_wrapper",
            Json::obj(vec![
                ("is_number_object", Json::b(true)),
                ("type_of", Json::s("object")),
                ("to_string", Json::s("5")),
                ("proto_is_number_prototype", Json::b(true)),
            ]),
        ),
        ("string_wrapper", Json::b(true)),
        ("boolean_wrapper", Json::b(true)),
        ("symbol_wrapper", Json::b(true)),
        ("bigint_wrapper", Json::b(true)),
        ("object_identity", Json::b(true)),
    ]);
    vec![expect_eq("obj-ops/convert/to_object", expected, actual)]
}

/// `Value::to_boolean`: the falsy/truthy matrix. Never throws.
fn to_boolean_matrix() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let samples: &[(&'static str, v8::Local<v8::Value>)] = &[
        ("undefined", v8::undefined(scope).into()),
        ("null", v8::null(scope).into()),
        ("false", v8::Boolean::new(scope, false).into()),
        ("zero", v8::Integer::new(scope, 0).into()),
        ("neg_zero", v8::Number::new(scope, -0.0).into()),
        ("nan", v8::Number::new(scope, f64::NAN).into()),
        ("empty_string", v8::String::new(scope, "").unwrap().into()),
        ("string_zero", v8::String::new(scope, "0").unwrap().into()),
        ("int_42", v8::Integer::new(scope, 42).into()),
        ("float_1p5", v8::Number::new(scope, 1.5).into()),
        ("true", v8::Boolean::new(scope, true).into()),
        ("empty_object", v8::Object::new(scope).into()),
        (
            "empty_array",
            eval(scope, "[]").unwrap_or(v8::undefined(scope).into()),
        ),
        (
            "string_false",
            v8::String::new(scope, "false").unwrap().into(),
        ),
    ];

    let mut actual_pairs: Vec<(&'static str, Json)> = Vec::new();
    let mut expected_pairs: Vec<(&'static str, Json)> = Vec::new();
    for (name, value) in samples {
        let observed = value.to_boolean(scope).boolean_value(scope);
        let expected_value = !matches!(
            *name,
            "undefined" | "null" | "false" | "zero" | "neg_zero" | "nan" | "empty_string"
        );
        actual_pairs.push((name, Json::b(observed)));
        expected_pairs.push((name, Json::b(expected_value)));
    }
    vec![expect_eq(
        "obj-ops/convert/to_boolean",
        Json::obj(expected_pairs),
        Json::obj(actual_pairs),
    )]
}

/// `Value::to_integer`: JS ToInteger truncation semantics, string
/// conversion, and the BigInt TypeError. Non-finite magnitudes are pinned
/// to the x86_64 Windows int64 saturation this build produces.
fn to_integer_matrix() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let nan: v8::Local<v8::Value> = v8::Number::new(scope, f64::NAN).into();
    let infinity = v8::Number::new(scope, f64::INFINITY);
    let neg_infinity = v8::Number::new(scope, f64::NEG_INFINITY);

    let v_3p75: v8::Local<v8::Value> = v8::Number::new(scope, 3.75).into();
    let v_neg_3p75: v8::Local<v8::Value> = v8::Number::new(scope, -3.75).into();
    let v_string_42: v8::Local<v8::Value> = v8::String::new(scope, "42").unwrap().into();
    let v_string_empty: v8::Local<v8::Value> = v8::String::new(scope, "").unwrap().into();
    let v_null: v8::Local<v8::Value> = v8::null(scope).into();
    let v_undefined: v8::Local<v8::Value> = v8::undefined(scope).into();
    let v_true: v8::Local<v8::Value> = v8::Boolean::new(scope, true).into();
    let v_empty_object: v8::Local<v8::Value> = eval(scope, "({})").unwrap();

    let samples: Vec<(&'static str, i64)> = vec![
        ("float_3p75", to_integer_i64(scope, v_3p75)),
        ("float_neg_3p75", to_integer_i64(scope, v_neg_3p75)),
        ("string_42", to_integer_i64(scope, v_string_42)),
        ("string_empty", to_integer_i64(scope, v_string_empty)),
        ("null", to_integer_i64(scope, v_null)),
        ("undefined", to_integer_i64(scope, v_undefined)),
        ("true", to_integer_i64(scope, v_true)),
        ("nan_truncates_to_zero", to_integer_i64(scope, nan)),
        ("object_empty", to_integer_i64(scope, v_empty_object)),
        ("big_int_throws", {
            let tc = std::pin::pin!(v8::TryCatch::new(scope));
            let tc = &mut tc.init();
            let one = v8::BigInt::new_from_i64(tc, 1);
            let r = one.to_integer(tc);
            match r {
                Some(i) => i.value(),
                None => -1,
            }
        }),
    ];
    let infinity_got = infinity.to_integer(scope);
    let infinity_raw = infinity_got.map(|i| i.value()).unwrap_or(-1);
    let neg_infinity_got = neg_infinity.to_integer(scope);
    let neg_infinity_raw = neg_infinity_got.map(|i| i.value()).unwrap_or(-1);
    let (infinity_caught, _) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let _ = infinity.to_integer(tc);
        (tc.has_caught(), ())
    };

    let actual = Json::obj(vec![
        (
            "samples",
            Json::obj(samples.into_iter().map(|(k, v)| (k, Json::i(v))).collect()),
        ),
        ("infinity", Json::i(infinity_raw)),
        ("neg_infinity", Json::i(neg_infinity_raw)),
        ("infinity_caught", Json::b(infinity_caught)),
    ]);
    let expected = Json::obj(vec![
        (
            "samples",
            Json::obj(vec![
                ("float_3p75", Json::i(3)),
                ("float_neg_3p75", Json::i(-3)),
                ("string_42", Json::i(42)),
                ("string_empty", Json::i(0)),
                ("null", Json::i(0)),
                ("undefined", Json::i(0)),
                ("true", Json::i(1)),
                ("nan_truncates_to_zero", Json::i(0)),
                ("object_empty", Json::i(0)),
                ("big_int_throws", Json::i(-1)),
            ]),
        ),
        // ToInteger keeps the double at +/-Infinity; `Integer::value()`
        // saturates through the C++ double -> int64 cast on x86_64
        // (cvttsd2si returns 0x8000000000000000 for out-of-range inputs).
        ("infinity", Json::i(i64::MIN)),
        ("neg_infinity", Json::i(i64::MIN)),
        ("infinity_caught", Json::b(false)),
    ]);
    vec![expect_eq("obj-ops/convert/to_integer", expected, actual)]
}

/// `Value::to_big_int`: per-spec ToBigInt (booleans and integral decimal
/// strings convert; numbers and non-integers throw).
fn to_big_int_matrix() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // (present, i64 value or -1, caught)
    let v_number_42: v8::Local<v8::Value> = v8::Integer::new(scope, 42).into();
    let v_float_1p5: v8::Local<v8::Value> = v8::Number::new(scope, 1.5).into();
    let v_bool_true: v8::Local<v8::Value> = v8::Boolean::new(scope, true).into();
    let v_string_123: v8::Local<v8::Value> = v8::String::new(scope, "123").unwrap().into();
    let v_string_1p5: v8::Local<v8::Value> = v8::String::new(scope, "1.5").unwrap().into();
    let v_string_hex: v8::Local<v8::Value> = v8::String::new(scope, "0x10").unwrap().into();
    let v_undefined: v8::Local<v8::Value> = v8::undefined(scope).into();
    let v_bigint: v8::Local<v8::Value> = v8::BigInt::new_from_i64(scope, -9).into();

    let number_42 = to_big_int_triple(scope, v_number_42);
    let float_1p5 = to_big_int_triple(scope, v_float_1p5);
    let bool_true = to_big_int_triple(scope, v_bool_true);
    let string_123 = to_big_int_triple(scope, v_string_123);
    let string_1p5 = to_big_int_triple(scope, v_string_1p5);
    let string_hex = to_big_int_triple(scope, v_string_hex);
    let undefined = to_big_int_triple(scope, v_undefined);
    let bigint_identity = to_big_int_triple(scope, v_bigint);
    let (number_message, _) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let _ = v8::Integer::new(tc, 42).to_big_int(tc);
        (caught_message!(tc), ())
    };

    let triple = |value: (bool, i64, bool)| {
        Json::obj(vec![
            ("present", Json::b(value.0)),
            ("value", Json::i(value.1)),
            ("caught", Json::b(value.2)),
        ])
    };
    let actual = Json::obj(vec![
        ("number_42", triple(number_42)),
        ("float_1p5", triple(float_1p5)),
        ("bool_true", triple(bool_true)),
        ("string_123", triple(string_123)),
        ("string_1p5", triple(string_1p5)),
        ("string_hex", triple(string_hex)),
        ("undefined", triple(undefined)),
        ("bigint_identity", triple(bigint_identity)),
        ("number_message", Json::s(&number_message)),
    ]);
    let expected = Json::obj(vec![
        ("number_42", triple((false, -1, true))),
        ("float_1p5", triple((false, -1, true))),
        ("bool_true", triple((true, 1, false))),
        ("string_123", triple((true, 123, false))),
        ("string_1p5", triple((false, -1, true))),
        ("string_hex", triple((true, 16, false))),
        ("undefined", triple((false, -1, true))),
        ("bigint_identity", triple((true, -9, false))),
        // The message embeds the concrete number, not the type name.
        (
            "number_message",
            Json::s("Uncaught TypeError: Cannot convert 42 to a BigInt"),
        ),
    ]);
    vec![expect_eq("obj-ops/convert/to_big_int", expected, actual)]
}

/// `Value::to_detail_string`: identical to ToString for primitives;
/// symbols render as `Symbol(desc)`, errors as their ToString message
/// without the "Uncaught" prefix, and plain JSReceiver objects as
/// V8's compact `#<Object>` detail form.
fn to_detail_string_matrix() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let symbol = v8::Symbol::new(scope, Some(v8::String::new(scope, "gov8").unwrap()));
    let error = eval(scope, "new TypeError('bad')").unwrap();
    let plain = v8::Object::new(scope);

    let v_undefined: v8::Local<v8::Value> = v8::undefined(scope).into();
    let v_null: v8::Local<v8::Value> = v8::null(scope).into();
    let v_int_42: v8::Local<v8::Value> = v8::Integer::new(scope, 42).into();
    let v_float_2p5: v8::Local<v8::Value> = v8::Number::new(scope, 2.5).into();
    let v_true: v8::Local<v8::Value> = v8::Boolean::new(scope, true).into();
    let v_string: v8::Local<v8::Value> = v8::String::new(scope, "plain").unwrap().into();

    let undefined = to_detail_pair(scope, v_undefined);
    let null = to_detail_pair(scope, v_null);
    let int_42 = to_detail_pair(scope, v_int_42);
    let float_2p5 = to_detail_pair(scope, v_float_2p5);
    let true_value = to_detail_pair(scope, v_true);
    let string = to_detail_pair(scope, v_string);
    let symbol_detail = to_detail_pair(scope, symbol.into());
    let error_detail = to_detail_pair(scope, error);
    let plain_detail = to_detail_pair(scope, plain.into());

    let pair = |value: (bool, String)| {
        Json::obj(vec![
            ("present", Json::b(value.0)),
            ("text", Json::s(&value.1)),
        ])
    };
    let actual = Json::obj(vec![
        ("undefined", pair(undefined)),
        ("null", pair(null)),
        ("int_42", pair(int_42)),
        ("float_2p5", pair(float_2p5)),
        ("true", pair(true_value)),
        ("string", pair(string)),
        ("symbol", pair(symbol_detail)),
        ("error", pair(error_detail)),
        ("plain_object", pair(plain_detail)),
    ]);
    let expected = Json::obj(vec![
        ("undefined", pair((true, "undefined".to_owned()))),
        ("null", pair((true, "null".to_owned()))),
        ("int_42", pair((true, "42".to_owned()))),
        ("float_2p5", pair((true, "2.5".to_owned()))),
        ("true", pair((true, "true".to_owned()))),
        ("string", pair((true, "plain".to_owned()))),
        ("symbol", pair((true, "Symbol(gov8)".to_owned()))),
        // ToDetailString renders errors via ToString WITHOUT the "Uncaught"
        // prefix (that prefix belongs to Message::get), and non-string
        // JSReceiver objects as V8's compact "#<Object>" form rather than
        // the JS ToString "[object Object]".
        ("error", pair((true, "TypeError: bad".to_owned()))),
        ("plain_object", pair((true, "#<Object>".to_owned()))),
    ]);
    vec![expect_eq(
        "obj-ops/convert/to_detail_string",
        expected,
        actual,
    )]
}

/// `Value::instance_of`: prototype-chain membership via the API, including
/// the non-callable right-hand side rejection.
fn api_instance_of() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let _ = eval(
        scope,
        "function P() {}
         globalThis.pi = Object.create(P.prototype);",
    );

    let plain = v8::Object::new(scope);
    let object_ctor = global_object(scope, "Object").unwrap();
    let p_ctor = global_object(scope, "P").unwrap();
    let pi = global_value(scope, "pi").unwrap();
    let function_ctor = global_object(scope, "Function").unwrap();
    let number_ctor = global_object(scope, "Number").unwrap();
    let function_value = global_value(scope, "(function f() {})").unwrap();
    let arrow = global_value(scope, "((a) => a)").unwrap();
    let five: v8::Local<v8::Value> = v8::Number::new(scope, 5.0).into();
    let null_v: v8::Local<v8::Value> = v8::null(scope).into();

    let plain_is_object = plain.instance_of(scope, object_ctor).unwrap_or(false);
    let pi_is_p = pi.instance_of(scope, p_ctor).unwrap_or(false);
    let number_is_number_ctor = five.instance_of(scope, number_ctor).unwrap_or(false);
    let function_is_function = function_value
        .instance_of(scope, function_ctor)
        .unwrap_or(false);
    let arrow_is_function = arrow.instance_of(scope, function_ctor).unwrap_or(false);
    let null_is_object = null_v.instance_of(scope, object_ctor).unwrap_or(false);

    let (rhs_non_callable, rhs_caught, rhs_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let rhs = v8::Object::new(tc);
        let r = plain.instance_of(tc, rhs);
        (r, tc.has_caught(), caught_message!(tc))
    };

    let actual = Json::obj(vec![
        ("plain_is_object", Json::b(plain_is_object)),
        ("proto_linked_is_p", Json::b(pi_is_p)),
        ("number_is_number_ctor", Json::b(number_is_number_ctor)),
        ("function_is_function", Json::b(function_is_function)),
        ("arrow_is_function", Json::b(arrow_is_function)),
        ("null_is_object", Json::b(null_is_object)),
        (
            "rhs_non_callable_is_none",
            Json::b(rhs_non_callable.is_none()),
        ),
        ("rhs_caught", Json::b(rhs_caught)),
        ("rhs_message", Json::s(&rhs_message)),
    ]);
    let expected = Json::obj(vec![
        ("plain_is_object", Json::b(true)),
        ("proto_linked_is_p", Json::b(true)),
        ("number_is_number_ctor", Json::b(false)),
        ("function_is_function", Json::b(true)),
        ("arrow_is_function", Json::b(true)),
        ("null_is_object", Json::b(false)),
        ("rhs_non_callable_is_none", Json::b(true)),
        ("rhs_caught", Json::b(true)),
        (
            "rhs_message",
            Json::s("Uncaught TypeError: Right-hand side of 'instanceof' is not callable"),
        ),
    ]);
    vec![expect_eq(
        "obj-ops/instanceof/api_instance_of",
        expected,
        actual,
    )]
}

/// The equality matrix: `same_value` vs `same_value_zero` vs
/// `strict_equals` over the pairs where the three algorithms disagree
/// (NaN, signed zero) and where they agree.
fn equality_same_value_zero() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // All pairs are held as `Local<Value>` so the three algorithms are
    // exercised through the same `Value` receiver type.
    let nan: v8::Local<v8::Value> = v8::Number::new(scope, f64::NAN).into();
    let nan2: v8::Local<v8::Value> = v8::Number::new(scope, f64::NAN).into();
    let plus_zero: v8::Local<v8::Value> = v8::Number::new(scope, 0.0).into();
    let minus_zero: v8::Local<v8::Value> = v8::Number::new(scope, -0.0).into();
    let string_a: v8::Local<v8::Value> = v8::String::new(scope, "ab").unwrap().into();
    let string_a_copy: v8::Local<v8::Value> = v8::String::new(scope, "ab").unwrap().into();
    let int_seven: v8::Local<v8::Value> = v8::Integer::new(scope, 7).into();
    let float_seven: v8::Local<v8::Value> = v8::Number::new(scope, 7.0).into();
    let bigint_one: v8::Local<v8::Value> = v8::BigInt::new_from_i64(scope, 1).into();
    let number_one: v8::Local<v8::Value> = v8::Integer::new(scope, 1).into();
    let obj: v8::Local<v8::Value> = v8::Object::new(scope).into();
    let obj_clone: v8::Local<v8::Value> = v8::Local::<v8::Object>::try_from(obj).unwrap().into();
    let obj_other: v8::Local<v8::Value> = v8::Object::new(scope).into();
    let undefined_v: v8::Local<v8::Value> = v8::undefined(scope).into();
    let null_v: v8::Local<v8::Value> = v8::null(scope).into();
    let true_v: v8::Local<v8::Value> = v8::Boolean::new(scope, true).into();

    // (name, a, b, same_value, same_value_zero, strict_equals)
    let cases: Vec<(&'static str, bool, bool, bool)> = vec![
        (
            "nan_nan",
            nan.same_value(nan2),
            nan.same_value_zero(nan2),
            nan.strict_equals(nan2),
        ),
        (
            "plus_minus_zero",
            plus_zero.same_value(minus_zero),
            plus_zero.same_value_zero(minus_zero),
            plus_zero.strict_equals(minus_zero),
        ),
        (
            "string_copies",
            string_a.same_value(string_a_copy),
            string_a.same_value_zero(string_a_copy),
            string_a.strict_equals(string_a_copy),
        ),
        (
            "int_vs_float_seven",
            int_seven.same_value(float_seven),
            int_seven.same_value_zero(float_seven),
            int_seven.strict_equals(float_seven),
        ),
        (
            "bigint_vs_number_one",
            bigint_one.same_value(number_one),
            bigint_one.same_value_zero(number_one),
            bigint_one.strict_equals(number_one),
        ),
        (
            "same_object",
            obj.same_value(obj_clone),
            obj.same_value_zero(obj_clone),
            obj.strict_equals(obj_clone),
        ),
        (
            "distinct_objects",
            obj.same_value(obj_other),
            obj.same_value_zero(obj_other),
            obj.strict_equals(obj_other),
        ),
        (
            "undefined_vs_null",
            undefined_v.same_value(null_v),
            undefined_v.same_value_zero(null_v),
            undefined_v.strict_equals(null_v),
        ),
        (
            "true_vs_one",
            true_v.same_value(number_one),
            true_v.same_value_zero(number_one),
            true_v.strict_equals(number_one),
        ),
    ];

    let actual = Json::obj(
        cases
            .iter()
            .map(|(name, same, zero, strict)| {
                (
                    *name,
                    Json::obj(vec![
                        ("same_value", Json::b(*same)),
                        ("same_value_zero", Json::b(*zero)),
                        ("strict_equals", Json::b(*strict)),
                    ]),
                )
            })
            .collect(),
    );
    let expected_entry = |same: bool, zero: bool, strict: bool| {
        Json::obj(vec![
            ("same_value", Json::b(same)),
            ("same_value_zero", Json::b(zero)),
            ("strict_equals", Json::b(strict)),
        ])
    };
    let expected = Json::obj(vec![
        ("nan_nan", expected_entry(true, true, false)),
        ("plus_minus_zero", expected_entry(false, true, true)),
        ("string_copies", expected_entry(true, true, true)),
        ("int_vs_float_seven", expected_entry(true, true, true)),
        ("bigint_vs_number_one", expected_entry(false, false, false)),
        ("same_object", expected_entry(true, true, true)),
        ("distinct_objects", expected_entry(false, false, false)),
        ("undefined_vs_null", expected_entry(false, false, false)),
        ("true_vs_one", expected_entry(false, false, false)),
    ]);
    vec![expect_eq(
        "obj-ops/equality/same_value_zero",
        expected,
        actual,
    )]
}

/// `Value::get_hash` vs `Object::get_identity_hash`: object hashes are the
/// identity hash (seeded per isolate - never pinned raw), Smi hashes are
/// the value itself, and other primitive hashes are only stable within the
/// isolate.
fn value_hash_semantics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let obj = v8::Object::new(scope);
    let identity = obj.get_identity_hash();
    let as_value: v8::Local<v8::Value> = obj.into();
    let object_value_hash = as_value.get_hash();
    let object_value_hash_again = as_value.get_hash();

    let smi_42 = v8::Integer::new(scope, 42);
    let smi_41 = v8::Integer::new(scope, 41);
    let smi_hash: u32 = smi_42.get_hash();
    let smi_hash_again: u32 = smi_42.get_hash();
    let smi_41_hash: u32 = smi_41.get_hash();

    let undefined_hash_first = v8::undefined(scope).get_hash();
    let undefined_hash_again = v8::undefined(scope).get_hash();
    let string_hash_first = v8::String::new(scope, "seeded").unwrap().get_hash();
    let string_hash_again = v8::String::new(scope, "seeded").unwrap().get_hash();
    let bigint_hash_first = v8::BigInt::new_from_i64(scope, 5).get_hash();
    let bigint_hash_again = v8::BigInt::new_from_i64(scope, 5).get_hash();

    let actual = Json::obj(vec![
        ("identity_nonzero", Json::b(identity.get() != 0)),
        (
            "object_value_hash_matches_identity",
            Json::b(object_value_hash as i32 == identity.get()),
        ),
        (
            "object_value_hash_stable",
            Json::b(object_value_hash == object_value_hash_again),
        ),
        ("smi_hash_stable", Json::b(smi_hash == smi_hash_again)),
        ("smi_41_differs", Json::b(smi_41_hash != smi_hash)),
        (
            "undefined_hash_stable",
            Json::b(undefined_hash_first == undefined_hash_again),
        ),
        (
            "string_hash_stable",
            Json::b(string_hash_first == string_hash_again),
        ),
        (
            "bigint_hash_stable",
            Json::b(bigint_hash_first == bigint_hash_again),
        ),
    ]);
    let expected = Json::obj(vec![
        ("identity_nonzero", Json::b(true)),
        ("object_value_hash_matches_identity", Json::b(true)),
        ("object_value_hash_stable", Json::b(true)),
        // Integer hashes are NOT the raw value in this build (unlike the
        // simple Smi hashing in older V8); they are only stable within the
        // isolate and differ between distinct small integers.
        ("smi_hash_stable", Json::b(true)),
        ("smi_41_differs", Json::b(true)),
        ("undefined_hash_stable", Json::b(true)),
        ("string_hash_stable", Json::b(true)),
        ("bigint_hash_stable", Json::b(true)),
    ]);
    vec![expect_eq("obj-ops/equality/value_hash", expected, actual)]
}

/// `Value::type_of`: the API-level type representation. Matches JS
/// `typeof` for every type including `type_of(null) == "object"`, and
/// reflects callability as `"function"` for proxies of functions.
fn type_representation() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let samples: &[(&'static str, v8::Local<v8::Value>)] = &[
        ("undefined", v8::undefined(scope).into()),
        ("null", v8::null(scope).into()),
        ("boolean", v8::Boolean::new(scope, true).into()),
        ("integer", v8::Integer::new(scope, 7).into()),
        ("float", v8::Number::new(scope, 2.5).into()),
        ("string", v8::String::new(scope, "s").unwrap().into()),
        ("symbol", v8::Symbol::new(scope, None).into()),
        ("bigint", v8::BigInt::new_from_i64(scope, 1).into()),
        (
            "function",
            eval(scope, "(function f() {})").unwrap_or(v8::undefined(scope).into()),
        ),
        ("plain_object", v8::Object::new(scope).into()),
        (
            "array",
            eval(scope, "[]").unwrap_or(v8::undefined(scope).into()),
        ),
        (
            "error",
            eval(scope, "new Error('e')").unwrap_or(v8::undefined(scope).into()),
        ),
        (
            "proxy_of_function",
            eval(scope, "new Proxy(function () {}, {})").unwrap_or(v8::undefined(scope).into()),
        ),
    ];

    let mut actual_pairs: Vec<(&'static str, Json)> = Vec::new();
    let mut expected_pairs: Vec<(&'static str, Json)> = Vec::new();
    for (name, value) in samples {
        let type_of_value = value.type_of(scope);
        let text = value_text(scope, type_of_value.into());
        actual_pairs.push((name, Json::s(&text)));
        expected_pairs.push((
            name,
            Json::s(match *name {
                "undefined" => "undefined",
                // V8's Value::TypeOf reports null as "object" (same as the
                // JS typeof operator), not "null".
                "null" => "object",
                "boolean" => "boolean",
                "integer" | "float" => "number",
                "string" => "string",
                "symbol" => "symbol",
                "bigint" => "bigint",
                "function" | "proxy_of_function" => "function",
                _ => "object",
            }),
        ));
    }
    vec![expect_eq(
        "obj-ops/typeof/type_representation",
        Json::obj(expected_pairs),
        Json::obj(actual_pairs),
    )]
}

/// The missing-predicates inventory: every `Value::is_*` predicate that no
/// other slice pins, exercised on a correctly-typed instance. Construction
/// happens through JS for exotic script types; `Float16Array` support is
/// itself part of the pinned contract.
fn predicates_missing_inventory() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let isolate_scope, isolate);
    let context = v8::Context::new(isolate_scope, Default::default());
    let scope = &mut v8::ContextScope::new(isolate_scope, context);

    let _ = eval(
        scope,
        "globalThis.argsObj = (function () { return arguments; })(1, 2);
         globalThis.symbolObj = Object(Symbol('s'));
         globalThis.nativeError = new Error('e');
         globalThis.asyncFn = async function () {};
         globalThis.genFn = function* () {};
         globalThis.genObj = (function* () { yield 1; })();
         globalThis.promiseVal = Promise.resolve(1);
         globalThis.mapIter = new Map().keys();
         globalThis.setIter = new Set().values();
         globalThis.weakMap = new WeakMap();
         globalThis.weakSet = new WeakSet();
         globalThis.u8 = new Uint8Array(2);
         globalThis.u8c = new Uint8ClampedArray(2);
         globalThis.i8 = new Int8Array(2);
         globalThis.u16 = new Uint16Array(2);
         globalThis.i16 = new Int16Array(2);
         globalThis.u32 = new Uint32Array(2);
         globalThis.i32 = new Int32Array(2);
         globalThis.f32 = new Float32Array(2);
         globalThis.f64 = new Float64Array(2);
         globalThis.bi64 = new BigInt64Array(2);
         globalThis.bu64 = new BigUint64Array(2);",
    );

    let float16_constructs = eval_text(scope, "typeof Float16Array") == "function";
    if float16_constructs {
        let _ = eval(scope, "globalThis.f16 = new Float16Array(2);");
    }

    let u8 = global_or_undefined(scope, "u8");
    let u8c = global_or_undefined(scope, "u8c");
    let i8 = global_or_undefined(scope, "i8");
    let u16 = global_or_undefined(scope, "u16");
    let i16 = global_or_undefined(scope, "i16");
    let u32 = global_or_undefined(scope, "u32");
    let i32 = global_or_undefined(scope, "i32");
    let f16 = global_or_undefined(scope, "f16");
    let f32 = global_or_undefined(scope, "f32");
    let f64 = global_or_undefined(scope, "f64");
    let bi64 = global_or_undefined(scope, "bi64");
    let bu64 = global_or_undefined(scope, "bu64");

    let external = v8::External::new(scope, std::ptr::null_mut());
    let external_value: v8::Local<v8::Value> = external.into();

    let actual = Json::obj(vec![
        (
            "is_false",
            Json::b(v8::Boolean::new(scope, false).is_false()),
        ),
        ("is_external", Json::b(external_value.is_external())),
        (
            "is_arguments_object",
            Json::b(global_or_undefined(scope, "argsObj").is_arguments_object()),
        ),
        (
            "is_symbol_object",
            Json::b(global_or_undefined(scope, "symbolObj").is_symbol_object()),
        ),
        (
            "is_native_error",
            Json::b(global_or_undefined(scope, "nativeError").is_native_error()),
        ),
        (
            "is_async_function",
            Json::b(global_or_undefined(scope, "asyncFn").is_async_function()),
        ),
        (
            "is_generator_function",
            Json::b(global_or_undefined(scope, "genFn").is_generator_function()),
        ),
        (
            "is_promise",
            Json::b(global_or_undefined(scope, "promiseVal").is_promise()),
        ),
        (
            "is_map_iterator",
            Json::b(global_or_undefined(scope, "mapIter").is_map_iterator()),
        ),
        (
            "is_set_iterator",
            Json::b(global_or_undefined(scope, "setIter").is_set_iterator()),
        ),
        (
            "is_generator_object",
            Json::b(global_or_undefined(scope, "genObj").is_generator_object()),
        ),
        (
            "is_weak_map",
            Json::b(global_or_undefined(scope, "weakMap").is_weak_map()),
        ),
        (
            "is_weak_set",
            Json::b(global_or_undefined(scope, "weakSet").is_weak_set()),
        ),
        ("is_uint8_array", Json::b(u8.is_uint8_array())),
        (
            "is_uint8_clamped_array",
            Json::b(u8c.is_uint8_clamped_array()),
        ),
        ("is_int8_array", Json::b(i8.is_int8_array())),
        ("is_uint16_array", Json::b(u16.is_uint16_array())),
        ("is_int16_array", Json::b(i16.is_int16_array())),
        ("is_uint32_array", Json::b(u32.is_uint32_array())),
        ("is_int32_array", Json::b(i32.is_int32_array())),
        ("float16_constructs", Json::b(float16_constructs)),
        ("is_float16_array", Json::b(f16.is_float16_array())),
        ("is_float32_array", Json::b(f32.is_float32_array())),
        ("is_float64_array", Json::b(f64.is_float64_array())),
        ("is_big_int64_array", Json::b(bi64.is_big_int64_array())),
        ("is_big_uint64_array", Json::b(bu64.is_big_uint64_array())),
        // Cross-controls: each typed-array predicate answers false on a
        // plain Uint8Array except its own.
        ("u8_is_not_i8", Json::b(!u8.is_int8_array())),
        ("u8_is_typed_array", Json::b(u8.is_typed_array())),
    ]);
    let expected = Json::obj(vec![
        ("is_false", Json::b(true)),
        ("is_external", Json::b(true)),
        ("is_arguments_object", Json::b(true)),
        ("is_symbol_object", Json::b(true)),
        ("is_native_error", Json::b(true)),
        ("is_async_function", Json::b(true)),
        ("is_generator_function", Json::b(true)),
        ("is_promise", Json::b(true)),
        ("is_map_iterator", Json::b(true)),
        ("is_set_iterator", Json::b(true)),
        ("is_generator_object", Json::b(true)),
        // Script-level WeakMap/WeakSet objects report true: they are
        // JSWeakMap/JSWeakSet instances in this V8.
        ("is_weak_map", Json::b(true)),
        ("is_weak_set", Json::b(true)),
        ("is_uint8_array", Json::b(true)),
        ("is_uint8_clamped_array", Json::b(true)),
        ("is_int8_array", Json::b(true)),
        ("is_uint16_array", Json::b(true)),
        ("is_int16_array", Json::b(true)),
        ("is_uint32_array", Json::b(true)),
        ("is_int32_array", Json::b(true)),
        ("float16_constructs", Json::b(true)),
        ("is_float16_array", Json::b(true)),
        ("is_float32_array", Json::b(true)),
        ("is_float64_array", Json::b(true)),
        ("is_big_int64_array", Json::b(true)),
        ("is_big_uint64_array", Json::b(true)),
        ("u8_is_not_i8", Json::b(true)),
        ("u8_is_typed_array", Json::b(true)),
    ]);
    vec![expect_eq(
        "obj-ops/predicates/missing_inventory",
        expected,
        actual,
    )]
}

fn residual_noop(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<'_, v8::Value>,
) {
}

#[allow(clippy::unnecessary_wraps)]
fn residual_unexpected_resolve<'s>(
    _context: v8::Local<'s, v8::Context>,
    _specifier: v8::Local<'s, v8::String>,
    _import_attributes: v8::Local<'s, v8::FixedArray>,
    _referrer: v8::Local<'s, v8::Module>,
) -> Option<v8::Local<'s, v8::Module>> {
    panic!("resolve callback must not run for a module without imports")
}

fn compile_residual_module<'s>(
    scope: &v8::PinScope<'s, '_>,
    source_text: &str,
) -> v8::Local<'s, v8::Module> {
    let source_text = v8::String::new(scope, source_text).unwrap();
    let resource: v8::Local<v8::Value> = v8::String::new(scope, "residual.mjs").unwrap().into();
    let origin = v8::ScriptOrigin::new(
        scope, resource, 0, 0, false, -1, None, false, false, true, None,
    );
    let mut source = v8::script_compiler::Source::new(source_text, Some(&origin));
    v8::script_compiler::compile_module(scope, &mut source).unwrap()
}

fn data_predicates_and_identity() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let bigint: v8::Local<v8::Data> = v8::BigInt::new_from_i64(scope, 1).into();
    let boolean: v8::Local<v8::Data> = v8::Boolean::new(scope, true).into();
    let context_data: v8::Local<v8::Data> = context.into();
    let function_template: v8::Local<v8::Data> =
        v8::FunctionTemplate::new(scope, residual_noop).into();
    let module = compile_residual_module(scope, "import './dep.mjs'; export const x = 1;");
    let module_data: v8::Local<v8::Data> = module.into();
    let requests = module.get_module_requests();
    let fixed_array: v8::Local<v8::Data> = requests.into();
    let module_request = requests.get(scope, 0).unwrap();
    let string = v8::String::new(scope, "name").unwrap();
    let name: v8::Local<v8::Data> = string.into();
    let number: v8::Local<v8::Data> = v8::Number::new(scope, 1.5).into();
    let object_template: v8::Local<v8::Data> = v8::ObjectTemplate::new(scope).into();
    let primitive: v8::Local<v8::Data> = string.into();
    let private_name = v8::String::new(scope, "private").unwrap();
    let private: v8::Local<v8::Data> = v8::Private::new(scope, Some(private_name)).into();
    let string_data: v8::Local<v8::Data> = string.into();
    let symbol: v8::Local<v8::Data> = v8::Symbol::new(scope, None).into();
    let object = v8::Object::new(scope);
    let object_same = object;
    let object_other = v8::Object::new(scope);
    let value: v8::Local<v8::Data> = object.into();

    let actual = Json::obj(vec![
        ("is_big_int", Json::b(bigint.is_big_int())),
        ("is_boolean", Json::b(boolean.is_boolean())),
        ("is_context", Json::b(context_data.is_context())),
        ("is_fixed_array", Json::b(fixed_array.is_fixed_array())),
        (
            "is_function_template",
            Json::b(function_template.is_function_template()),
        ),
        ("is_module", Json::b(module_data.is_module())),
        (
            "is_module_request",
            Json::b(module_request.is_module_request()),
        ),
        ("is_name", Json::b(name.is_name())),
        ("is_number", Json::b(number.is_number())),
        (
            "is_object_template",
            Json::b(object_template.is_object_template()),
        ),
        ("is_primitive", Json::b(primitive.is_primitive())),
        ("is_private", Json::b(private.is_private())),
        ("is_string", Json::b(string_data.is_string())),
        ("is_symbol", Json::b(symbol.is_symbol())),
        ("is_value", Json::b(value.is_value())),
        ("number_is_not_string", Json::b(!number.is_string())),
        ("string_is_not_number", Json::b(!string_data.is_number())),
        ("context_is_not_value", Json::b(!context_data.is_value())),
        ("module_is_not_value", Json::b(!module_data.is_value())),
        ("private_is_not_value", Json::b(!private.is_value())),
        (
            "object_template_is_not_function_template",
            Json::b(!object_template.is_function_template()),
        ),
        (
            "value_is_not_module_request",
            Json::b(!value.is_module_request()),
        ),
        ("fixed_array_is_not_value", Json::b(!fixed_array.is_value())),
        (
            "same_data_identity",
            Json::b(v8::Local::<v8::Data>::from(object) == object_same),
        ),
        (
            "distinct_data_identity",
            Json::b(v8::Local::<v8::Data>::from(object) != object_other),
        ),
    ]);
    let expected = Json::obj(vec![
        ("is_big_int", Json::b(true)),
        ("is_boolean", Json::b(true)),
        ("is_context", Json::b(true)),
        ("is_fixed_array", Json::b(true)),
        ("is_function_template", Json::b(true)),
        ("is_module", Json::b(true)),
        ("is_module_request", Json::b(true)),
        ("is_name", Json::b(true)),
        ("is_number", Json::b(true)),
        ("is_object_template", Json::b(true)),
        ("is_primitive", Json::b(true)),
        ("is_private", Json::b(true)),
        ("is_string", Json::b(true)),
        ("is_symbol", Json::b(true)),
        ("is_value", Json::b(true)),
        ("number_is_not_string", Json::b(true)),
        ("string_is_not_number", Json::b(true)),
        ("context_is_not_value", Json::b(true)),
        ("module_is_not_value", Json::b(true)),
        ("private_is_not_value", Json::b(true)),
        ("object_template_is_not_function_template", Json::b(true)),
        ("value_is_not_module_request", Json::b(true)),
        ("fixed_array_is_not_value", Json::b(true)),
        ("same_data_identity", Json::b(true)),
        ("distinct_data_identity", Json::b(true)),
    ]);
    vec![expect_eq(
        "obj-ops/data/predicates_and_identity",
        expected,
        actual,
    )]
}

fn residual_local_conversions() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let input: v8::Local<v8::Value> = v8::String::new(scope, "4294967297.9").unwrap().into();
    let number = input.to_number(scope).unwrap();
    let string = input.to_string(scope).unwrap();
    let uint32 = input.to_uint32(scope).unwrap();
    let int32 = input.to_int32(scope).unwrap();
    let negative: v8::Local<v8::Value> = v8::Number::new(scope, -1.9).into();
    let negative_uint32 = negative.to_uint32(scope).unwrap();
    let negative_int32 = negative.to_int32(scope).unwrap();
    let (symbol_number_none, symbol_number_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let symbol: v8::Local<v8::Value> = v8::Symbol::new(tc, None).into();
        (symbol.to_number(tc).is_none(), tc.has_caught())
    };
    let (symbol_string_none, symbol_string_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let symbol: v8::Local<v8::Value> = v8::Symbol::new(tc, None).into();
        (symbol.to_string(tc).is_none(), tc.has_caught())
    };
    let (bigint_uint32_none, bigint_uint32_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bigint: v8::Local<v8::Value> = v8::BigInt::new_from_i64(tc, 1).into();
        (bigint.to_uint32(tc).is_none(), tc.has_caught())
    };
    let (bigint_int32_none, bigint_int32_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let bigint: v8::Local<v8::Value> = v8::BigInt::new_from_i64(tc, 1).into();
        (bigint.to_int32(tc).is_none(), tc.has_caught())
    };

    let actual = Json::obj(vec![
        ("number", Json::f(number.value())),
        ("string", Json::s(&string.to_rust_string_lossy(scope))),
        ("uint32", Json::i(uint32.value() as i64)),
        ("int32", Json::i(int32.value() as i64)),
        ("negative_uint32", Json::i(negative_uint32.value() as i64)),
        ("negative_int32", Json::i(negative_int32.value() as i64)),
        ("symbol_number_none", Json::b(symbol_number_none)),
        ("symbol_number_caught", Json::b(symbol_number_caught)),
        ("symbol_string_none", Json::b(symbol_string_none)),
        ("symbol_string_caught", Json::b(symbol_string_caught)),
        ("bigint_uint32_none", Json::b(bigint_uint32_none)),
        ("bigint_uint32_caught", Json::b(bigint_uint32_caught)),
        ("bigint_int32_none", Json::b(bigint_int32_none)),
        ("bigint_int32_caught", Json::b(bigint_int32_caught)),
    ]);
    let expected = Json::obj(vec![
        ("number", Json::f(4294967297.9)),
        ("string", Json::s("4294967297.9")),
        ("uint32", Json::i(1)),
        ("int32", Json::i(1)),
        ("negative_uint32", Json::i(4294967295)),
        ("negative_int32", Json::i(-1)),
        ("symbol_number_none", Json::b(true)),
        ("symbol_number_caught", Json::b(true)),
        ("symbol_string_none", Json::b(true)),
        ("symbol_string_caught", Json::b(true)),
        ("bigint_uint32_none", Json::b(true)),
        ("bigint_uint32_caught", Json::b(true)),
        ("bigint_int32_none", Json::b(true)),
        ("bigint_int32_caught", Json::b(true)),
    ]);
    vec![expect_eq(
        "obj-ops/convert/residual_locals",
        expected,
        actual,
    )]
}

fn module_namespace_and_type_repr() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let module = compile_residual_module(scope, "export const x = 1;");
    assert_eq!(
        module.instantiate_module(scope, residual_unexpected_resolve),
        Some(true)
    );
    let namespace = module.get_module_namespace();
    let namespace_value: v8::Local<v8::Value> = namespace;

    let mut samples = vec![
        ("module_namespace", namespace_value),
        (
            "wasm_module",
            eval(
                scope,
                "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))",
            )
            .unwrap(),
        ),
        (
            "wasm_memory",
            eval(scope, "new WebAssembly.Memory({initial:1})").unwrap(),
        ),
        ("proxy", eval(scope, "new Proxy({}, {})").unwrap()),
        (
            "shared_array_buffer",
            eval(scope, "new SharedArrayBuffer(1)").unwrap(),
        ),
        (
            "data_view",
            eval(scope, "new DataView(new ArrayBuffer(1))").unwrap(),
        ),
        (
            "big_uint64_array",
            eval(scope, "new BigUint64Array(1)").unwrap(),
        ),
        (
            "big_int64_array",
            eval(scope, "new BigInt64Array(1)").unwrap(),
        ),
        ("float64_array", eval(scope, "new Float64Array(1)").unwrap()),
        ("float32_array", eval(scope, "new Float32Array(1)").unwrap()),
        ("int32_array", eval(scope, "new Int32Array(1)").unwrap()),
        ("uint32_array", eval(scope, "new Uint32Array(1)").unwrap()),
        ("int16_array", eval(scope, "new Int16Array(1)").unwrap()),
        ("uint16_array", eval(scope, "new Uint16Array(1)").unwrap()),
        ("int8_array", eval(scope, "new Int8Array(1)").unwrap()),
        (
            "uint8_clamped_array",
            eval(scope, "new Uint8ClampedArray(1)").unwrap(),
        ),
        ("uint8_array", eval(scope, "new Uint8Array(1)").unwrap()),
        ("float16_array", eval(scope, "new Float16Array(1)").unwrap()),
        ("array_buffer", eval(scope, "new ArrayBuffer(1)").unwrap()),
        ("weak_set", eval(scope, "new WeakSet()").unwrap()),
        ("weak_map", eval(scope, "new WeakMap()").unwrap()),
        ("set_iterator", eval(scope, "new Set().values()").unwrap()),
        ("map_iterator", eval(scope, "new Map().keys()").unwrap()),
        ("set", eval(scope, "new Set()").unwrap()),
        ("map", eval(scope, "new Map()").unwrap()),
        ("promise", eval(scope, "Promise.resolve(1)").unwrap()),
        (
            "generator_function",
            eval(scope, "(function*(){})").unwrap(),
        ),
        (
            "async_function",
            eval(scope, "(async function(){})").unwrap(),
        ),
        ("regexp", eval(scope, "/x/").unwrap()),
        ("date", eval(scope, "new Date(0)").unwrap()),
        ("number", eval(scope, "1").unwrap()),
        ("boolean", eval(scope, "true").unwrap()),
        ("bigint", eval(scope, "1n").unwrap()),
        ("array", eval(scope, "[]").unwrap()),
        ("function", eval(scope, "(function(){})").unwrap()),
        ("symbol", eval(scope, "Symbol('s')").unwrap()),
        ("string", eval(scope, "'s'").unwrap()),
        ("null", eval(scope, "null").unwrap()),
        ("undefined", eval(scope, "undefined").unwrap()),
        ("plain_object", eval(scope, "({})").unwrap()),
    ];
    let mut actual_pairs = Vec::with_capacity(samples.len() + 1);
    actual_pairs.push((
        "is_module_namespace_object",
        Json::b(namespace_value.is_module_namespace_object()),
    ));
    for (name, value) in samples.drain(..) {
        actual_pairs.push((name, Json::s(value.type_repr())));
    }
    let actual = Json::obj(actual_pairs);
    let expected = Json::obj(vec![
        ("is_module_namespace_object", Json::b(true)),
        ("module_namespace", Json::s("Module")),
        ("wasm_module", Json::s("WASM module")),
        ("wasm_memory", Json::s("WASM memory object")),
        ("proxy", Json::s("Proxy")),
        ("shared_array_buffer", Json::s("SharedArrayBuffer")),
        ("data_view", Json::s("DataView")),
        ("big_uint64_array", Json::s("BigUint64Array")),
        ("big_int64_array", Json::s("BigInt64Array")),
        ("float64_array", Json::s("Float64Array")),
        ("float32_array", Json::s("Float32Array")),
        ("int32_array", Json::s("Int32Array")),
        ("uint32_array", Json::s("Uint32Array")),
        ("int16_array", Json::s("Int16Array")),
        ("uint16_array", Json::s("Uint16Array")),
        ("int8_array", Json::s("Int8Array")),
        ("uint8_clamped_array", Json::s("Uint8ClampedArray")),
        ("uint8_array", Json::s("Uint8Array")),
        ("float16_array", Json::s("TypedArray")),
        ("array_buffer", Json::s("ArrayBuffer")),
        ("weak_set", Json::s("WeakSet")),
        ("weak_map", Json::s("WeakMap")),
        ("set_iterator", Json::s("Set Iterator")),
        ("map_iterator", Json::s("Map Iterator")),
        ("set", Json::s("Set")),
        ("map", Json::s("Map")),
        ("promise", Json::s("Promise")),
        ("generator_function", Json::s("Generator function")),
        ("async_function", Json::s("Async function")),
        ("regexp", Json::s("RegExp")),
        ("date", Json::s("Date")),
        ("number", Json::s("Number")),
        ("boolean", Json::s("Boolean")),
        ("bigint", Json::s("bigint")),
        ("array", Json::s("array")),
        ("function", Json::s("function")),
        ("symbol", Json::s("symbol")),
        ("string", Json::s("string")),
        ("null", Json::s("null")),
        ("undefined", Json::s("undefined")),
        ("plain_object", Json::s("unknown")),
    ]);
    vec![expect_eq(
        "obj-ops/predicates/module_namespace_and_type_repr",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// Registry and entry point (order is the observable contract).
// ---------------------------------------------------------------------------

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    proto_get_and_set,
    has_delete_family,
    real_named_interceptor_bypass,
    identity_hash_contract,
    creation_context,
    constructor_name,
    get_set_with_receiver,
    lazy_data_property,
    instance_accessor,
    call_plain_object_not_callable,
    call_function_call_and_construct,
    callable_constructor_predicates,
    to_object_matrix,
    to_boolean_matrix,
    to_integer_matrix,
    to_big_int_matrix,
    to_detail_string_matrix,
    api_instance_of,
    equality_same_value_zero,
    value_hash_semantics,
    type_representation,
    predicates_missing_inventory,
    data_predicates_and_identity,
    residual_local_conversions,
    module_namespace_and_type_repr,
];

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let mut outcomes = Vec::new();
    for check in CHECKS {
        outcomes.extend(check());
    }
    let total = outcomes.len();
    let mut passed = 0usize;
    let mut text = String::new();
    for outcome in &outcomes {
        if outcome.passed() {
            passed += 1;
        }
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    let failed = total - passed;
    text.push_str(&summary_line(total, passed, failed));
    text.push('\n');

    use std::io::Write as _;
    let stdout = std::io::stdout();
    let mut lock = stdout.lock();
    let _ = lock.write_all(text.as_bytes());
    let _ = lock.flush();

    if failed == 0 {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
