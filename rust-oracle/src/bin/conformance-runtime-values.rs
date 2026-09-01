//! Built-in runtime values conformance slice for the pinned `v8` crate.
//!
//! Characterizes, in fixed order, the observable contract of the
//! JavaScript built-ins reachable through native APIs:
//! - `Date` (`v8::Date::new` / `value_of`, JS interop, invalid-time
//!   boundaries).
//! - `RegExp` (`v8::RegExp::new` / `exec` / `get_source`,
//!   `RegExpCreationFlags`, `lastIndex` semantics for global and sticky
//!   patterns, invalid-pattern SyntaxError).
//! - JSON (`v8::json::parse` / `stringify`: canonical round-trips, error
//!   messages, undefined/function/NaN/circular boundaries, `toJSON`).
//! - `Array` (`v8::Array::new` including the negative-length boundary,
//!   `new_with_elements`, index vs named property semantics).
//! - `Map` / `Set` (`v8::Map`, `v8::Set` native collections: NaN keys,
//!   SameValueZero for +0/-0, insertion order, `as_array`, JS interop).
//! - `Proxy` (`v8::Proxy::new` / `get_target` / `get_handler` /
//!   `revoke` / `is_revoked`, default traps, revoked-proxy and
//!   trap-invariant TypeErrors).
//! - `Symbol` (`v8::Symbol::new` / `for_key` / `for_api` / description /
//!   well-known symbols bridged into JS behavior) and private symbols
//!   (`v8::Private` + `Object::set_private` and their JS invisibility).
//! - Primitive wrapper objects (`Number`/`Boolean`/`String`/`BigInt`
//!   objects vs their primitives: type predicates, truthiness,
//!   conversions).
//! - Property attributes and descriptors
//!   (`Object::define_own_property`, `get_property_attributes`,
//!   `create_data_property`, `Object::set_integrity_level`,
//!   `v8::PropertyDescriptor` + `Object::define_property`,
//!   `Object::get_own_property_descriptor`,
//!   `Object::get_own_property_names` filters).
//!
//! Everything is normalized per `src/json.rs` rules: no addresses, no
//! timings, no random hashes, exact V8 error strings for the pinned
//! build. The runner emits the same JSON-lines protocol as the other
//! slices (`{"check":..,"ok":..,"value"|"expected"/"actual"}` + final
//! summary).
//!
//! This slice performs no platform shutdown, so it can be verified
//! in-process; its fixture is pinned by
//! `tests/conformance_runtime_values_fixture.rs` (binary output only:
//! the checks live in this binary because the existing `src/checks`
//! registries are shared files that this slice must not modify).

use std::io::Write as _;
use std::process::ExitCode;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

// ---------------------------------------------------------------------------
// Helpers (local to this binary; the crate's `checks::harness` is pub(crate)
// and existing files must not be modified to expose it).
// ---------------------------------------------------------------------------

/// Compiles and runs `source`, returning the completion value (`None` on
/// syntax error or runtime throw; every eval in this slice runs under a
/// surrounding TryCatch so a pending exception is always captured, and every
/// eval is expected to succeed unless a check says otherwise).
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

/// `v8::json::stringify` rendered as text (`None` keeps `undefined`-style
/// results distinguishable from the empty string).
fn json_stringify(
    scope: &mut v8::PinScope<'_, '_>,
    value: v8::Local<'_, v8::Value>,
) -> Option<String> {
    v8::json::stringify(scope, value).map(|s| s.to_rust_string_lossy(scope))
}

/// Reads the caught exception's formatted message from a TryCatch scope.
/// This must be a macro because `has_caught`/`message` live on the
/// `PinnedRef<TryCatch>` wrapper, not on the `PinScope` it derefs to.
macro_rules! caught_message {
    ($tc:expr) => {{
        let caught = $tc.has_caught();
        let message = $tc
            .message()
            .map(|m| m.get($tc).to_rust_string_lossy($tc))
            .unwrap_or_default();
        (caught, message)
    }};
}

/// JSON.stringify of an owned value, flattened to `""` when stringify
/// returns `None`.
fn stringify_text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    json_stringify(scope, value).unwrap_or_default()
}

/// `Object::get_own_property_descriptor` for a string key.
fn own_descriptor<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    obj: v8::Local<'_, v8::Object>,
    key: &str,
) -> Option<v8::Local<'s, v8::Value>> {
    let name = v8::String::new(scope, key).unwrap();
    obj.get_own_property_descriptor(scope, name.into())
}

/// True when an `Option<Local<Object>>` result wraps the JavaScript `null`
/// value (the pinned crate signals "no match" this way, reserving `None`
/// for thrown exceptions).
fn is_null_result(result: Option<v8::Local<'_, v8::Object>>) -> bool {
    match result {
        None => false,
        Some(obj) => {
            let value: v8::Local<v8::Value> = obj.into();
            value.is_null()
        }
    }
}

// ---------------------------------------------------------------------------
// Checks. Order is part of the observable contract (the fixture is ordered).
// ---------------------------------------------------------------------------

/// Native `Date` construction: `Date::new` accepts any double including
/// NaN, `value_of` returns the exact stored time value, JS-created Dates
/// are observable through the same native surface, and JS mutation is
/// reflected natively.
#[allow(clippy::too_many_lines)]
fn date_construction_and_value_of() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let epoch = v8::Date::new(scope, 0.0).unwrap();
    let epoch_value: v8::Local<v8::Value> = epoch.into();
    let is_date = epoch_value.is_date();
    let is_object = epoch_value.is_object();
    let native_value_of = epoch.value_of();

    // Publish the native date so JS can observe and mutate it.
    let global = context.global(scope);
    global.set(
        scope,
        v8::String::new(scope, "d").unwrap().into(),
        epoch_value,
    );

    let js_get_time = eval_text(scope, "d.getTime()");
    let js_to_iso = eval_text(scope, "d.toISOString()");
    eval(scope, "d.setUTCSeconds(30)").unwrap();
    let native_after_mutation = epoch.value_of() == 30_000.0;
    let js_to_iso_after = eval_text(scope, "d.toISOString()");

    let later = v8::Date::new(scope, 1.5e12).unwrap();
    let later_exact = later.value_of() == 1.5e12;

    let invalid = v8::Date::new(scope, f64::NAN).unwrap();
    let invalid_value_of = invalid.value_of();
    let js_invalid_is_nan = {
        let global = context.global(scope);
        let invalid_value: v8::Local<v8::Value> = invalid.into();
        global.set(
            scope,
            v8::String::new(scope, "di").unwrap().into(),
            invalid_value,
        );
        eval_text(scope, "Number.isNaN(di.getTime())")
    };

    let js_created = eval(scope, "new Date(86400500)").unwrap();
    let js_created_is_date = js_created.is_date();
    let js_created_native_value =
        js_created.try_cast::<v8::Date>().map(|d| d.value_of()).ok() == Some(86_400_500.0);

    let actual = Json::obj(vec![
        ("is_date", Json::b(is_date)),
        ("is_object", Json::b(is_object)),
        ("native_value_of_is_zero", Json::b(native_value_of == 0.0)),
        ("js_get_time", Json::s(&js_get_time)),
        ("js_to_iso", Json::s(&js_to_iso)),
        ("native_after_mutation", Json::b(native_after_mutation)),
        ("js_to_iso_after", Json::s(&js_to_iso_after)),
        ("later_exact", Json::b(later_exact)),
        (
            "invalid_value_of_is_nan",
            Json::b(invalid_value_of.is_nan()),
        ),
        ("js_invalid_is_nan", Json::s(&js_invalid_is_nan)),
        ("js_created_is_date", Json::b(js_created_is_date)),
        ("js_created_native_value", Json::b(js_created_native_value)),
    ]);
    let expected = Json::obj(vec![
        ("is_date", Json::b(true)),
        ("is_object", Json::b(true)),
        ("native_value_of_is_zero", Json::b(true)),
        ("js_get_time", Json::s("0")),
        ("js_to_iso", Json::s("1970-01-01T00:00:00.000Z")),
        ("native_after_mutation", Json::b(true)),
        ("js_to_iso_after", Json::s("1970-01-01T00:00:30.000Z")),
        ("later_exact", Json::b(true)),
        ("invalid_value_of_is_nan", Json::b(true)),
        ("js_invalid_is_nan", Json::s("true")),
        ("js_created_is_date", Json::b(true)),
        ("js_created_native_value", Json::b(true)),
    ]);
    vec![expect_eq(
        "runtime-values/date_construction_and_value_of",
        expected,
        actual,
    )]
}

/// The invalid-time boundary: every Date method that formats an invalid
/// time value throws the deterministic `RangeError: Invalid time value`.
fn date_invalid_time_value_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let (js_sees, caught, message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let js_sees = eval_text(tc, "new Date(NaN).toISOString()");
        let (caught, message) = caught_message!(tc);
        (js_sees, caught, message)
    };

    let actual = Json::obj(vec![
        ("result", Json::s(&js_sees)),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
    ]);
    let expected = Json::obj(vec![
        ("result", Json::s("")),
        ("caught", Json::b(true)),
        (
            "message",
            Json::s("Uncaught RangeError: Invalid time value"),
        ),
    ]);
    vec![expect_eq(
        "runtime-values/date_invalid_time_value_error",
        expected,
        actual,
    )]
}

/// Native RegExp construction: `get_source` returns the pattern source
/// verbatim, and the JS `flags` string reflects `RegExpCreationFlags` in
/// canonical order.
fn regexp_new_flags_and_source() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let pattern = v8::String::new(scope, "a(b)c").unwrap();
    let flags = v8::RegExpCreationFlags::GLOBAL | v8::RegExpCreationFlags::IGNORE_CASE;
    let re = v8::RegExp::new(scope, pattern, flags).unwrap();
    let re_value: v8::Local<v8::Value> = re.into();

    let source = re.get_source(scope).to_rust_string_lossy(scope);
    let global = context.global(scope);
    global.set(
        scope,
        v8::String::new(scope, "re").unwrap().into(),
        re_value,
    );

    let js_flags = eval_text(scope, "re.flags");
    let js_global = eval_text(scope, "re.global");
    let js_ignore_case = eval_text(scope, "re.ignoreCase");
    let js_sticky = eval_text(scope, "re.sticky");
    let js_multiline = eval_text(scope, "re.multiline");
    let js_typeof = eval_text(scope, "typeof re");

    let actual = Json::obj(vec![
        ("is_reg_exp", Json::b(re_value.is_reg_exp())),
        ("source", Json::s(&source)),
        ("js_flags", Json::s(&js_flags)),
        ("js_global", Json::s(&js_global)),
        ("js_ignore_case", Json::s(&js_ignore_case)),
        ("js_sticky", Json::s(&js_sticky)),
        ("js_multiline", Json::s(&js_multiline)),
        ("js_typeof", Json::s(&js_typeof)),
    ]);
    let expected = Json::obj(vec![
        ("is_reg_exp", Json::b(true)),
        ("source", Json::s("a(b)c")),
        ("js_flags", Json::s("gi")),
        ("js_global", Json::s("true")),
        ("js_ignore_case", Json::s("true")),
        ("js_sticky", Json::s("false")),
        ("js_multiline", Json::s("false")),
        ("js_typeof", Json::s("object")),
    ]);
    vec![expect_eq(
        "runtime-values/regexp_new_flags_and_source",
        expected,
        actual,
    )]
}

/// Normalized shape of one `RegExp::exec` result: the JSON view of the
/// match object (index properties only), its `index`, and its `input`.
fn describe_exec(scope: &mut v8::PinScope<'_, '_>, m: Option<v8::Local<'_, v8::Object>>) -> Json {
    match m {
        None => Json::obj(vec![("match", Json::Null)]),
        Some(obj) => {
            let value: v8::Local<v8::Value> = obj.into();
            let index = obj
                .get(scope, v8::String::new(scope, "index").unwrap().into())
                .and_then(|v| v.int32_value(scope))
                .unwrap_or_default();
            let input = obj
                .get(scope, v8::String::new(scope, "input").unwrap().into())
                .map(|v| value_text(scope, v))
                .unwrap_or_default();
            Json::obj(vec![
                ("match", Json::s(&stringify_text(scope, value))),
                ("index", Json::i(index as i64)),
                ("input", Json::s(&input)),
            ])
        }
    }
}

/// `RegExp::exec` honors `lastIndex` for global and sticky patterns and
/// updates it after each match; a failed global exec resets it to 0.
#[allow(clippy::too_many_lines)]
fn regexp_exec_and_last_index() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let pattern = v8::String::new(scope, "a(b)c").unwrap();
    let re = v8::RegExp::new(scope, pattern, v8::RegExpCreationFlags::GLOBAL).unwrap();
    let subject = v8::String::new(scope, "xxabcXXabc").unwrap();

    let first_match = re.exec(scope, subject);
    let first = describe_exec(scope, first_match);

    let re_value: v8::Local<v8::Value> = re.into();
    global.set(scope, v8::String::new(scope, "g").unwrap().into(), re_value);
    let last_index_after_first = eval_text(scope, "g.lastIndex");

    let second_match = re.exec(scope, subject);
    let second = describe_exec(scope, second_match);
    let last_index_after_second = eval_text(scope, "g.lastIndex");
    // Pinned nuance: a failed exec still returns Some(...) wrapping the
    // `null` value; only a thrown exception yields None.
    let third_is_null = is_null_result(re.exec(scope, subject));
    let last_index_after_fail = eval_text(scope, "g.lastIndex");

    // Sticky: exec is anchored at lastIndex (a failed match resets it).
    let spattern = v8::String::new(scope, "x").unwrap();
    let sticky = v8::RegExp::new(scope, spattern, v8::RegExpCreationFlags::STICKY).unwrap();
    let ssubject = v8::String::new(scope, "axxa").unwrap();
    let sticky_at_0_is_null = is_null_result(sticky.exec(scope, ssubject));
    let sticky_value: v8::Local<v8::Value> = sticky.into();
    global.set(
        scope,
        v8::String::new(scope, "s").unwrap().into(),
        sticky_value,
    );
    eval_text(scope, "s.lastIndex = 2");
    let sticky_match = sticky.exec(scope, ssubject);
    let sticky_at_2_is_match = !is_null_result(sticky_match);
    let sticky_index = match sticky_match {
        Some(m) => m
            .get(scope, v8::String::new(scope, "index").unwrap().into())
            .and_then(|v| v.int32_value(scope))
            .unwrap_or_default(),
        None => 0,
    };
    let sticky_exhausted_is_null = is_null_result(sticky.exec(scope, ssubject));

    let actual = Json::obj(vec![
        ("first", first),
        ("last_index_after_first", Json::s(&last_index_after_first)),
        ("second", second),
        ("last_index_after_second", Json::s(&last_index_after_second)),
        ("third_is_null", Json::b(third_is_null)),
        ("last_index_after_fail", Json::s(&last_index_after_fail)),
        ("sticky_at_0_is_null", Json::b(sticky_at_0_is_null)),
        ("sticky_at_2_is_match", Json::b(sticky_at_2_is_match)),
        ("sticky_index", Json::i(sticky_index as i64)),
        (
            "sticky_exhausted_is_null",
            Json::b(sticky_exhausted_is_null),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "first",
            Json::obj(vec![
                ("match", Json::s("[\"abc\",\"b\"]")),
                ("index", Json::i(2)),
                ("input", Json::s("xxabcXXabc")),
            ]),
        ),
        ("last_index_after_first", Json::s("5")),
        (
            "second",
            Json::obj(vec![
                ("match", Json::s("[\"abc\",\"b\"]")),
                ("index", Json::i(7)),
                ("input", Json::s("xxabcXXabc")),
            ]),
        ),
        ("last_index_after_second", Json::s("10")),
        ("third_is_null", Json::b(true)),
        ("last_index_after_fail", Json::s("0")),
        ("sticky_at_0_is_null", Json::b(true)),
        ("sticky_at_2_is_match", Json::b(true)),
        ("sticky_index", Json::i(2)),
        ("sticky_exhausted_is_null", Json::b(true)),
    ]);
    vec![expect_eq(
        "runtime-values/regexp_exec_and_last_index",
        expected,
        actual,
    )]
}

/// An invalid pattern is rejected: `RegExp::new` returns `None`, the
/// SyntaxError is pending on the isolate, and the message matches what JS
/// `new RegExp` produces for the same pattern.
fn regexp_invalid_pattern_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let (native_result, js_result, native_caught, native_message, js_caught, js_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let pattern = v8::String::new(tc, "(").unwrap();
        let native = v8::RegExp::new(tc, pattern, v8::RegExpCreationFlags::empty());
        let (native_caught, native_message) = caught_message!(tc);
        let native_result = native.is_none();

        let js = eval_text(tc, "new RegExp(\"(\")");
        let (js_caught, js_message) = caught_message!(tc);
        let js_result = js.is_empty();
        (
            native_result,
            js_result,
            native_caught,
            native_message,
            js_caught,
            js_message,
        )
    };

    let actual = Json::obj(vec![
        ("native_is_none", Json::b(native_result)),
        ("native_caught", Json::b(native_caught)),
        ("native_message", Json::s(&native_message)),
        ("js_failed", Json::b(js_result)),
        ("js_caught", Json::b(js_caught)),
        ("js_message", Json::s(&js_message)),
    ]);
    let expected = Json::obj(vec![
        ("native_is_none", Json::b(true)),
        ("native_caught", Json::b(true)),
        (
            "native_message",
            Json::s("Uncaught SyntaxError: Invalid regular expression: /(/: Unterminated group"),
        ),
        ("js_failed", Json::b(true)),
        ("js_caught", Json::b(true)),
        (
            "js_message",
            Json::s("Uncaught SyntaxError: Invalid regular expression: /(/: Unterminated group"),
        ),
    ]);
    vec![expect_eq(
        "runtime-values/regexp_invalid_pattern_error",
        expected,
        actual,
    )]
}

/// JS-created regexps expose their source through the native API and
/// native `exec` drives them.
fn regexp_js_created_source() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let value = eval(scope, "/ab+c/gi").unwrap();
    let re = value.try_cast::<v8::RegExp>().ok().unwrap();
    let source = re.get_source(scope).to_rust_string_lossy(scope);

    let subject = v8::String::new(scope, "xAbcXABBC").unwrap();
    let first = re
        .exec(scope, subject)
        .map(|m| stringify_text(scope, m.into()));
    let second = re
        .exec(scope, subject)
        .map(|m| stringify_text(scope, m.into()));

    let actual = Json::obj(vec![
        ("is_reg_exp", Json::b(value.is_reg_exp())),
        ("source", Json::s(&source)),
        ("first", Json::s(&first.clone().unwrap_or_default())),
        ("second", Json::s(&second.unwrap_or_default())),
    ]);
    let expected = Json::obj(vec![
        ("is_reg_exp", Json::b(true)),
        ("source", Json::s("ab+c")),
        ("first", Json::s("[\"Abc\"]")),
        ("second", Json::s("[\"ABBC\"]")),
    ]);
    vec![expect_eq(
        "runtime-values/regexp_js_created_source",
        expected,
        actual,
    )]
}

/// `JSON::parse` accepts exactly the JSON grammar and re-stringifies
/// canonically; the boundary entries pin number and lone-surrogate
/// behavior.
#[allow(clippy::too_many_lines)]
fn json_parse_canonical() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut roundtrip = |source: &str| -> String {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let text = v8::String::new(tc, source).unwrap();
        match v8::json::parse(tc, text) {
            Some(value) => json_stringify(tc, value).unwrap_or_default(),
            None => format!("<caught:{}>", tc.has_caught()),
        }
    };

    let actual = Json::obj(vec![
        (
            "object",
            Json::s(&roundtrip(
                "{\"a\":[1,2.5,\"s\",true,null],\"b\":{\"c\":1}}",
            )),
        ),
        ("whitespace", Json::s(&roundtrip("[ 1 , 2 ]"))),
        ("negative_zero", Json::s(&roundtrip("-0"))),
        ("overflow_number", Json::s(&roundtrip("1e999"))),
        ("precision", Json::s(&roundtrip("9007199254740993"))),
        ("lone_surrogate", Json::s(&roundtrip("\"\\ud800\""))),
        (
            "escapes",
            Json::s(&roundtrip("\"a\\/\\b\\f\\n\\r\\t\\u0041\"")),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "object",
            Json::s("{\"a\":[1,2.5,\"s\",true,null],\"b\":{\"c\":1}}"),
        ),
        ("whitespace", Json::s("[1,2]")),
        ("negative_zero", Json::s("0")),
        ("overflow_number", Json::s("null")),
        ("precision", Json::s("9007199254740992")),
        ("lone_surrogate", Json::s("\"\\ud800\"")),
        ("escapes", Json::s("\"a/\\b\\f\\n\\r\\tA\"")),
    ]);
    vec![expect_eq(
        "runtime-values/json_parse_canonical",
        expected,
        actual,
    )]
}

/// Deterministic JSON parse failures: each malformed input yields a caught
/// SyntaxError with a pinned message and no value.
fn json_parse_errors() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut entries: Vec<(&'static str, Json)> = Vec::new();
    for (name, input) in [
        ("empty", ""),
        ("truncated", "{"),
        ("single_quotes", "{'a':1}"),
        ("trailing", "[1,2],3"),
        ("bare_word", "undefined"),
    ] {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let text = v8::String::new(tc, input).unwrap();
        let parsed = v8::json::parse(tc, text);
        let (caught, message) = caught_message!(tc);
        entries.push((
            name,
            Json::obj(vec![
                ("is_none", Json::b(parsed.is_none())),
                ("caught", Json::b(caught)),
                ("message", Json::s(&message)),
            ]),
        ));
    }

    let actual = Json::obj(entries);
    let rejected = |message: &str| {
        Json::obj(vec![
            ("is_none", Json::b(true)),
            ("caught", Json::b(true)),
            ("message", Json::s(message)),
        ])
    };
    let expected = Json::obj(vec![
        ("empty", rejected("Uncaught SyntaxError: Unexpected end of JSON input")),
        (
            "truncated",
            rejected(
                "Uncaught SyntaxError: Expected property name or '}' in JSON at position 1 (line 1 column 2)",
            ),
        ),
        (
            "single_quotes",
            rejected(
                "Uncaught SyntaxError: Expected property name or '}' in JSON at position 1 (line 1 column 2)",
            ),
        ),
        (
            "trailing",
            rejected(
                "Uncaught SyntaxError: Unexpected non-whitespace character after JSON at position 5 (line 1 column 6)",
            ),
        ),
        (
            "bare_word",
            rejected("Uncaught SyntaxError: \"undefined\" is not valid JSON"),
        ),
    ]);
    vec![expect_eq(
        "runtime-values/json_parse_errors",
        expected,
        actual,
    )]
}

/// `JSON::stringify` on objects: insertion key order, omission of
/// undefined/function/symbol members, array holes become null, string
/// escaping is canonical, and `toJSON` (Date) is honored.
#[allow(clippy::too_many_lines)]
fn json_stringify_objects() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut stringify = |source: &str| -> String {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        match eval(tc, source) {
            Some(value) => json_stringify(tc, value).unwrap_or_default(),
            None => format!("<caught:{}>", tc.has_caught()),
        }
    };

    let actual = Json::obj(vec![
        (
            "omissions",
            Json::s(&stringify(
                "({a: undefined, b: () => 1, c: [1, undefined, 2], d: null, e: 0})",
            )),
        ),
        (
            "symbol_keys_skipped",
            Json::s(&stringify("const s = Symbol('k'); ({[s]: 1, ok: 2})")),
        ),
        (
            "holes",
            Json::s(&stringify(
                "(function(){ const a = [1]; a[3] = 4; return a; })()",
            )),
        ),
        (
            "escapes",
            Json::s(&stringify("({q: \"a\\\"b\\\\c\\nd\\te\", f: \"\\u0001\"})")),
        ),
        ("date", Json::s(&stringify("new Date(0)"))),
        (
            "to_json",
            Json::s(&stringify(
                "({toJSON: () => ({replaced: true}), ignored: 1})",
            )),
        ),
        (
            "nested",
            Json::s(&stringify("({o: {a: [[1, {b: \"x\"}]]}})")),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "omissions",
            Json::s("{\"c\":[1,null,2],\"d\":null,\"e\":0}"),
        ),
        ("symbol_keys_skipped", Json::s("{\"ok\":2}")),
        ("holes", Json::s("[1,null,null,4]")),
        (
            "escapes",
            Json::s("{\"q\":\"a\\\"b\\\\c\\nd\\te\",\"f\":\"\\u0001\"}"),
        ),
        ("date", Json::s("\"1970-01-01T00:00:00.000Z\"")),
        ("to_json", Json::s("{\"replaced\":true}")),
        ("nested", Json::s("{\"o\":{\"a\":[[1,{\"b\":\"x\"}]]}}")),
    ]);
    vec![expect_eq(
        "runtime-values/json_stringify_objects",
        expected,
        actual,
    )]
}

/// `JSON::stringify` boundaries: `undefined` and functions produce no
/// string at all (None), non-finite numbers become `null`, wrapper objects
/// are unwrapped, and circular structures throw a deterministic TypeError.
#[allow(clippy::too_many_lines)]
fn json_stringify_boundaries() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut stringify = |source: &str| -> (bool, String) {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let value = eval(tc, source).unwrap();
        let out = v8::json::stringify(tc, value);
        let is_none = out.is_none();
        let text = out.map(|s| s.to_rust_string_lossy(tc)).unwrap_or_default();
        let (caught, message) = caught_message!(tc);
        (
            is_none,
            if caught {
                format!("<caught> {message}")
            } else {
                text
            },
        )
    };

    let (undefined_is_none, undefined_text) = stringify("undefined");
    let (function_is_none, function_text) = stringify("() => 1");
    let (nan_text_is_none, nan_text) = stringify("NaN");
    let (infinity_is_none, infinity_text) = stringify("Infinity");
    let (neg_infinity_is_none, neg_infinity_text) = stringify("-Infinity");
    let (wrapper_is_none, wrapper_text) = stringify("new Number(5)");
    let (boolean_wrapper_is_none, boolean_wrapper_text) = stringify("new Boolean(false)");
    let (string_wrapper_is_none, string_wrapper_text) = stringify("new String(\"ab\")");
    let (symbol_is_none, symbol_text) = stringify("Symbol('s')");
    let (circular_is_none, circular_text) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let value = eval(tc, "const c = {}; c.self = c; c").unwrap();
        let out = v8::json::stringify(tc, value);
        let (caught, message) = caught_message!(tc);
        (
            out.is_none(),
            if caught {
                format!("<caught> {message}")
            } else {
                String::new()
            },
        )
    };
    let _ = (nan_text_is_none, infinity_is_none, neg_infinity_is_none);

    let actual = Json::obj(vec![
        ("undefined_is_none", Json::b(undefined_is_none)),
        ("undefined", Json::s(&undefined_text)),
        ("function_is_none", Json::b(function_is_none)),
        ("function", Json::s(&function_text)),
        ("nan", Json::s(&nan_text)),
        ("infinity", Json::s(&infinity_text)),
        ("neg_infinity", Json::s(&neg_infinity_text)),
        ("number_wrapper", Json::s(&wrapper_text)),
        ("number_wrapper_is_none", Json::b(wrapper_is_none)),
        ("boolean_wrapper", Json::s(&boolean_wrapper_text)),
        ("boolean_wrapper_is_none", Json::b(boolean_wrapper_is_none)),
        ("string_wrapper", Json::s(&string_wrapper_text)),
        ("string_wrapper_is_none", Json::b(string_wrapper_is_none)),
        ("symbol_is_none", Json::b(symbol_is_none)),
        ("symbol", Json::s(&symbol_text)),
        ("circular_is_none", Json::b(circular_is_none)),
        ("circular", Json::s(&circular_text)),
    ]);
    let expected = Json::obj(vec![
        // Pinned nuance: unlike JS `JSON.stringify` (which returns the
        // undefined *value*), the C++ stringify renders these as the
        // literal string "undefined" and never an empty maybe-local.
        ("undefined_is_none", Json::b(false)),
        ("undefined", Json::s("undefined")),
        ("function_is_none", Json::b(false)),
        ("function", Json::s("undefined")),
        ("nan", Json::s("null")),
        ("infinity", Json::s("null")),
        ("neg_infinity", Json::s("null")),
        ("number_wrapper", Json::s("5")),
        ("number_wrapper_is_none", Json::b(false)),
        ("boolean_wrapper", Json::s("false")),
        ("boolean_wrapper_is_none", Json::b(false)),
        ("string_wrapper", Json::s("\"ab\"")),
        ("string_wrapper_is_none", Json::b(false)),
        ("symbol_is_none", Json::b(false)),
        ("symbol", Json::s("undefined")),
        ("circular_is_none", Json::b(true)),
        (
            "circular",
            Json::s(
                "<caught> Uncaught TypeError: Converting circular structure to JSON\n    --> starting at object with constructor 'Object'\n    --- property 'self' closes the circle",
            ),
        ),
    ]);
    vec![expect_eq(
        "runtime-values/json_stringify_boundaries",
        expected,
        actual,
    )]
}

/// Native Array construction: the documented negative-length boundary
/// collapses to an empty array (unlike the JS constructor, which throws),
/// elements arrays transfer verbatim, and fresh arrays are holey.
#[allow(clippy::too_many_lines)]
fn array_new_and_elements() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let three = v8::Array::new(scope, 3);
    let three_value: v8::Local<v8::Value> = three.into();
    let has_zero = three.has_index(scope, 0).unwrap_or_default();

    let negative = v8::Array::new(scope, -5);
    let negative_length = negative.length();

    let one = v8::Integer::new(scope, 1);
    let two = v8::Integer::new(scope, 2);
    let elements = v8::Array::new_with_elements(scope, &[one.into(), two.into()]);
    let elements_json = stringify_text(scope, elements.into());

    let (js_negative_caught, js_negative_message) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let _ = eval_text(tc, "new Array(-1)");
        let (caught, message) = caught_message!(tc);
        (caught, message)
    };

    let actual = Json::obj(vec![
        ("is_array", Json::b(three_value.is_array())),
        ("length", Json::i(three.length() as i64)),
        ("has_index_zero", Json::b(has_zero)),
        ("stringify", Json::s(&stringify_text(scope, three_value))),
        ("negative_length", Json::i(negative_length as i64)),
        ("elements_length", Json::i(elements.length() as i64)),
        ("elements_json", Json::s(&elements_json)),
        ("js_negative_caught", Json::b(js_negative_caught)),
        ("js_negative_message", Json::s(&js_negative_message)),
    ]);
    let expected = Json::obj(vec![
        ("is_array", Json::b(true)),
        ("length", Json::i(3)),
        ("has_index_zero", Json::b(false)),
        ("stringify", Json::s("[null,null,null]")),
        ("negative_length", Json::i(0)),
        ("elements_length", Json::i(2)),
        ("elements_json", Json::s("[1,2]")),
        ("js_negative_caught", Json::b(true)),
        (
            "js_negative_message",
            Json::s("Uncaught RangeError: Invalid array length"),
        ),
    ]);
    vec![expect_eq(
        "runtime-values/array_new_and_elements",
        expected,
        actual,
    )]
}

/// Index vs named property semantics: `set_index` grows `length`, JS
/// `push` is visible natively, negative subscripts are plain named
/// properties ignored by `JSON.stringify`, and the maximum index
/// (`2^32 - 2`) saturates `length` at `2^32 - 1`.
#[allow(clippy::too_many_lines)]
fn array_index_semantics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let arr = v8::Array::new(scope, 0);
    let arr_value: v8::Local<v8::Value> = arr.into();
    global.set(
        scope,
        v8::String::new(scope, "a").unwrap().into(),
        arr_value,
    );

    let b = v8::String::new(scope, "b").unwrap();
    arr.set_index(scope, 1, b.into());
    let length_after_set = arr.length();
    let got_one = arr.get_index(scope, 1).unwrap();
    let get_one = value_text(scope, got_one);
    let has_one = arr.has_index(scope, 1).unwrap_or_default();
    let has_two = arr.has_index(scope, 2).unwrap_or_default();

    let push = eval_text(scope, "a.push('pushed'); a.length");
    let push_native = arr.length();
    let negative_sub = eval_text(
        scope,
        "a[-1] = 'neg'; [a.length, a.hasOwnProperty(-1), JSON.stringify(a)].join('|')",
    );
    let negative_sub_native = arr.length();

    // The maximum array index on a fresh array; no bulk serialization.
    eval_text(
        scope,
        "(function(){ const mx = []; mx[4294967294] = 7; globalThis.mx = mx; return mx.length; })()",
    );
    let max_index_native = eval(scope, "mx")
        .and_then(|v| v.try_cast::<v8::Array>().ok())
        .map(|a| a.length())
        .unwrap_or_default();

    let actual = Json::obj(vec![
        ("length_after_set", Json::i(length_after_set as i64)),
        ("get_one", Json::s(&get_one)),
        ("has_one", Json::b(has_one)),
        ("has_two", Json::b(has_two)),
        ("push_js", Json::s(&push)),
        ("push_native", Json::i(push_native as i64)),
        ("negative_subscript", Json::s(&negative_sub)),
        (
            "negative_subscript_native_length",
            Json::i(negative_sub_native as i64),
        ),
        ("max_index_native", Json::i(max_index_native as i64)),
    ]);
    let expected = Json::obj(vec![
        ("length_after_set", Json::i(2)),
        ("get_one", Json::s("b")),
        ("has_one", Json::b(true)),
        ("has_two", Json::b(false)),
        ("push_js", Json::s("3")),
        ("push_native", Json::i(3)),
        (
            "negative_subscript",
            Json::s("3|true|[null,\"b\",\"pushed\"]"),
        ),
        ("negative_subscript_native_length", Json::i(3)),
        ("max_index_native", Json::i(4_294_967_295)),
    ]);
    vec![expect_eq(
        "runtime-values/array_index_semantics",
        expected,
        actual,
    )]
}

/// Native `v8::Map`: identity of returned handle, SameValueZero keys
/// (including NaN), object keys by identity, overwrite-in-place,
/// insertion-order `as_array`, and `clear`.
#[allow(clippy::too_many_lines)]
fn map_native_ops() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let m = v8::Map::new(scope);
    let m_value: v8::Local<v8::Value> = m.into();
    let initial_size = m.size();

    let key_a = v8::String::new(scope, "a").unwrap();
    let one = v8::Integer::new(scope, 1);
    let returned = m.set(scope, key_a.into(), one.into());
    let returned_is_same = returned.map(|r| r == m).unwrap_or(false);

    let has_a = m.has(scope, key_a.into()).unwrap_or_default();
    let got_a = m.get(scope, key_a.into()).unwrap();
    let get_a = value_text(scope, got_a);
    let size_one = m.size();

    // NaN is a legal SameValueZero key.
    let nan = v8::Number::new(scope, f64::NAN);
    let two = v8::Integer::new(scope, 2);
    m.set(scope, nan.into(), two.into());
    let has_nan = m.has(scope, nan.into()).unwrap_or_default();
    let got_nan = m.get(scope, nan.into()).unwrap();
    let get_nan = value_text(scope, got_nan);

    // Distinct objects are distinct keys; re-setting the same object
    // overwrites in place.
    let k1 = v8::Object::new(scope);
    let k2 = v8::Object::new(scope);
    let three = v8::Integer::new(scope, 3);
    let four = v8::Integer::new(scope, 4);
    let nine = v8::Integer::new(scope, 9);
    m.set(scope, k1.into(), three.into());
    m.set(scope, k2.into(), four.into());
    let size_with_objects = m.size();
    m.set(scope, k1.into(), nine.into());
    let size_after_overwrite = m.size();
    let got_k1 = m.get(scope, k1.into()).unwrap();
    let get_k1_after_overwrite = value_text(scope, got_k1);

    let deleted = m.delete(scope, key_a.into()).unwrap_or_default();
    let deleted_missing = m.delete(scope, key_a.into()).unwrap_or_default();

    // Fresh map for order check.
    let ordered = v8::Map::new(scope);
    ordered.set(
        scope,
        v8::String::new(scope, "a").unwrap().into(),
        one.into(),
    );
    ordered.set(
        scope,
        v8::String::new(scope, "b").unwrap().into(),
        two.into(),
    );
    let as_array = ordered.as_array(scope);
    let as_array_json = stringify_text(scope, as_array.into());

    m.clear();
    let size_after_clear = m.size();
    let has_a_after_clear = m.has(scope, key_a.into()).unwrap_or_default();

    let actual = Json::obj(vec![
        ("is_map", Json::b(m_value.is_map())),
        ("initial_size", Json::i(initial_size as i64)),
        ("returned_is_same", Json::b(returned_is_same)),
        ("has_a", Json::b(has_a)),
        ("get_a", Json::s(&get_a)),
        ("size_one", Json::i(size_one as i64)),
        ("has_nan", Json::b(has_nan)),
        ("get_nan", Json::s(&get_nan)),
        ("size_with_objects", Json::i(size_with_objects as i64)),
        ("size_after_overwrite", Json::i(size_after_overwrite as i64)),
        ("get_k1_after_overwrite", Json::s(&get_k1_after_overwrite)),
        ("deleted", Json::b(deleted)),
        ("deleted_missing", Json::b(deleted_missing)),
        ("as_array", Json::s(&as_array_json)),
        ("size_after_clear", Json::i(size_after_clear as i64)),
        ("has_a_after_clear", Json::b(has_a_after_clear)),
    ]);
    let expected = Json::obj(vec![
        ("is_map", Json::b(true)),
        ("initial_size", Json::i(0)),
        ("returned_is_same", Json::b(true)),
        ("has_a", Json::b(true)),
        ("get_a", Json::s("1")),
        ("size_one", Json::i(1)),
        ("has_nan", Json::b(true)),
        ("get_nan", Json::s("2")),
        ("size_with_objects", Json::i(4)),
        ("size_after_overwrite", Json::i(4)),
        ("get_k1_after_overwrite", Json::s("9")),
        ("deleted", Json::b(true)),
        ("deleted_missing", Json::b(false)),
        ("as_array", Json::s("[\"a\",1,\"b\",2]")),
        ("size_after_clear", Json::i(0)),
        ("has_a_after_clear", Json::b(false)),
    ]);
    vec![expect_eq("runtime-values/map_native_ops", expected, actual)]
}

/// Native `v8::Set`: dedup by SameValueZero (NaN dedup, +0/-0 same key),
/// insertion-order `as_array`, `delete`, `clear`, and chaining identity.
#[allow(clippy::too_many_lines)]
fn set_native_ops() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let s = v8::Set::new(scope);
    let s_value: v8::Local<v8::Value> = s.into();
    let initial_size = s.size();

    let x = v8::String::new(scope, "x").unwrap();
    let returned = s.add(scope, x.into());
    let returned_is_same = returned.map(|r| r == s).unwrap_or(false);
    s.add(scope, x.into());
    let size_after_dup = s.size();

    let nan = v8::Number::new(scope, f64::NAN);
    s.add(scope, nan.into());
    s.add(scope, nan.into());
    let size_after_nan = s.size();
    let has_nan = s.has(scope, nan.into()).unwrap_or_default();

    let pos_zero = v8::Number::new(scope, 0.0);
    let neg_zero = v8::Number::new(scope, -0.0);
    s.add(scope, neg_zero.into());
    let has_pos_zero = s.has(scope, pos_zero.into()).unwrap_or_default();

    let as_array = s.as_array(scope);
    let as_array_json = stringify_text(scope, as_array.into());

    let deleted = s.delete(scope, x.into()).unwrap_or_default();
    let deleted_missing = s.delete(scope, x.into()).unwrap_or_default();
    let size_after_delete = s.size();
    s.clear();
    let size_after_clear = s.size();

    let actual = Json::obj(vec![
        ("is_set", Json::b(s_value.is_set())),
        ("initial_size", Json::i(initial_size as i64)),
        ("returned_is_same", Json::b(returned_is_same)),
        ("size_after_dup", Json::i(size_after_dup as i64)),
        ("size_after_nan", Json::i(size_after_nan as i64)),
        ("has_nan", Json::b(has_nan)),
        ("has_pos_zero_after_neg_zero", Json::b(has_pos_zero)),
        ("as_array", Json::s(&as_array_json)),
        ("deleted", Json::b(deleted)),
        ("deleted_missing", Json::b(deleted_missing)),
        ("size_after_delete", Json::i(size_after_delete as i64)),
        ("size_after_clear", Json::i(size_after_clear as i64)),
    ]);
    let expected = Json::obj(vec![
        ("is_set", Json::b(true)),
        ("initial_size", Json::i(0)),
        ("returned_is_same", Json::b(true)),
        ("size_after_dup", Json::i(1)),
        ("size_after_nan", Json::i(2)),
        ("has_nan", Json::b(true)),
        ("has_pos_zero_after_neg_zero", Json::b(true)),
        ("as_array", Json::s("[\"x\",null,0]")),
        ("deleted", Json::b(true)),
        ("deleted_missing", Json::b(false)),
        ("size_after_delete", Json::i(2)),
        ("size_after_clear", Json::i(0)),
    ]);
    vec![expect_eq("runtime-values/set_native_ops", expected, actual)]
}

/// JS-created Map/Set are observable through the native API in both
/// directions, and iteration order agrees with insertion order.
#[allow(clippy::too_many_lines)]
fn map_set_js_interop() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let js_map = eval(scope, "new Map([[\"a\", 1], [\"b\", 2]])").unwrap();
    let is_map = js_map.is_map();
    let m = js_map.try_cast::<v8::Map>().ok().unwrap();
    let size = m.size();
    let key_b = v8::String::new(scope, "b").unwrap();
    let got_b = m.get(scope, key_b.into()).unwrap();
    let get_b = value_text(scope, got_b);
    let map_typeof_string = js_map.type_of(scope);
    let map_typeof = value_text(scope, map_typeof_string.into());

    let native_map = v8::Map::new(scope);
    native_map.set(
        scope,
        v8::Integer::new(scope, 10).into(),
        v8::String::new(scope, "ten").unwrap().into(),
    );
    native_map.set(
        scope,
        v8::Integer::new(scope, 20).into(),
        v8::String::new(scope, "twenty").unwrap().into(),
    );
    let map_value: v8::Local<v8::Value> = native_map.into();
    let global = context.global(scope);
    global.set(
        scope,
        v8::String::new(scope, "nm").unwrap().into(),
        map_value,
    );
    let js_entries = eval_text(scope, "JSON.stringify([...nm.entries()])");
    let js_instanceof = eval_text(scope, "nm instanceof Map");

    let js_set = eval(
        scope,
        "(function(){ const s = new Set([1,2]); s.add(3); return s; })()",
    )
    .unwrap();
    let s = js_set.try_cast::<v8::Set>().ok().unwrap();
    let set_size = s.size();
    let three = v8::Integer::new(scope, 3);
    let set_has_three = s.has(scope, three.into()).unwrap_or_default();
    let set_array = s.as_array(scope);
    let set_as_array = stringify_text(scope, set_array.into());

    let actual = Json::obj(vec![
        ("js_map_is_map", Json::b(is_map)),
        ("size", Json::i(size as i64)),
        ("get_b", Json::s(&get_b)),
        ("map_typeof", Json::s(&map_typeof)),
        ("native_map_js_entries", Json::s(&js_entries)),
        ("native_map_instanceof", Json::s(&js_instanceof)),
        ("set_size", Json::i(set_size as i64)),
        ("set_has_three", Json::b(set_has_three)),
        ("set_as_array", Json::s(&set_as_array)),
    ]);
    let expected = Json::obj(vec![
        ("js_map_is_map", Json::b(true)),
        ("size", Json::i(2)),
        ("get_b", Json::s("2")),
        ("map_typeof", Json::s("object")),
        (
            "native_map_js_entries",
            Json::s("[[10,\"ten\"],[20,\"twenty\"]]"),
        ),
        ("native_map_instanceof", Json::s("true")),
        ("set_size", Json::i(3)),
        ("set_has_three", Json::b(true)),
        ("set_as_array", Json::s("[1,2,3]")),
    ]);
    vec![expect_eq(
        "runtime-values/map_set_js_interop",
        expected,
        actual,
    )]
}

/// `Proxy::new` identity (`get_target`/`get_handler` return the original
/// handles) and default trap forwarding for get/has/set, observed natively
/// and from JS.
#[allow(clippy::too_many_lines)]
fn proxy_identity_and_default_traps() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let target_value = eval(scope, "({x: 1})").unwrap();
    let target = target_value.try_cast::<v8::Object>().ok().unwrap();
    let handler_value = eval(scope, "({})").unwrap();
    let handler = handler_value.try_cast::<v8::Object>().ok().unwrap();

    let proxy = v8::Proxy::new(scope, target, handler).unwrap();
    let proxy_value: v8::Local<v8::Value> = proxy.into();
    let is_proxy = proxy_value.is_proxy();
    let is_object = proxy_value.is_object();
    let target_same = proxy.get_target(scope) == target_value;
    let handler_same = proxy.get_handler(scope) == handler_value;
    let not_revoked = !proxy.is_revoked();

    global.set(
        scope,
        v8::String::new(scope, "p").unwrap().into(),
        proxy_value,
    );

    let x = v8::String::new(scope, "x").unwrap();
    let got_x = target.get(scope, x.into()).unwrap();
    let native_get_x = value_text(scope, got_x);
    let proxy_obj_for_get = proxy_value.try_cast::<v8::Object>().ok().unwrap();
    let proxy_got_x = proxy_obj_for_get.get(scope, x.into()).unwrap();
    let proxy_get_x = value_text(scope, proxy_got_x);
    let two = v8::Integer::new(scope, 2);
    let native_set_y = proxy_value
        .try_cast::<v8::Object>()
        .ok()
        .unwrap()
        .set(
            scope,
            v8::String::new(scope, "y").unwrap().into(),
            two.into(),
        )
        .unwrap_or_default();
    let js_sees = eval_text(scope, "[p.x, p.y, 'x' in p, JSON.stringify(p)].join('|')");

    let actual = Json::obj(vec![
        ("is_proxy", Json::b(is_proxy)),
        ("is_object", Json::b(is_object)),
        ("target_same", Json::b(target_same)),
        ("handler_same", Json::b(handler_same)),
        ("not_revoked", Json::b(not_revoked)),
        ("target_get_x", Json::s(&native_get_x)),
        ("proxy_get_x", Json::s(&proxy_get_x)),
        ("proxy_set_y", Json::b(native_set_y)),
        ("js_sees", Json::s(&js_sees)),
    ]);
    let expected = Json::obj(vec![
        ("is_proxy", Json::b(true)),
        ("is_object", Json::b(true)),
        ("target_same", Json::b(true)),
        ("handler_same", Json::b(true)),
        ("not_revoked", Json::b(true)),
        ("target_get_x", Json::s("1")),
        ("proxy_get_x", Json::s("1")),
        ("proxy_set_y", Json::b(true)),
        ("js_sees", Json::s("1|2|true|{\"x\":1,\"y\":2}")),
    ]);
    vec![expect_eq(
        "runtime-values/proxy_identity_and_default_traps",
        expected,
        actual,
    )]
}

/// Native revoke: `is_revoked` flips, property operations on the revoked
/// proxy throw a deterministic TypeError (natively and from JS), and
/// `get_target` degrades to the JavaScript `null` value (V8 clears the
/// revoked proxy's internal target).
#[allow(clippy::too_many_lines)]
fn proxy_revoke_semantics() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let (
        target_same_before_revoke,
        not_revoked_before,
        revoked_after,
        native_get_after_revoke,
        native_caught,
        native_message,
        js_error_name,
        target_undefined_after_revoke,
        target_null_after_revoke,
    ) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let target_value = eval(tc, "({x: 1})").unwrap();
        let target = target_value.try_cast::<v8::Object>().ok().unwrap();
        let handler_value = eval(tc, "({})").unwrap();
        let handler = handler_value.try_cast::<v8::Object>().ok().unwrap();
        let proxy = v8::Proxy::new(tc, target, handler).unwrap();
        let proxy_value: v8::Local<v8::Value> = proxy.into();
        let target_same_before = proxy.get_target(tc) == target_value;
        let not_revoked_before = !proxy.is_revoked();

        global.set(tc, v8::String::new(tc, "rp").unwrap().into(), proxy_value);
        proxy.revoke();
        let revoked_after = proxy.is_revoked();

        let x = v8::String::new(tc, "x").unwrap();
        let proxy_obj = proxy_value.try_cast::<v8::Object>().ok().unwrap();
        let native_get = proxy_obj.get(tc, x.into()).is_none();
        let (native_caught, native_message) = caught_message!(tc);

        let js_error_name = eval_text(
            tc,
            "(function(){ try { return String(rp.x); } catch (e) { return e.name; } })()",
        );
        // Pinned nuance: a revoked proxy's native `get_target` still
        // resolves, but to the JavaScript `null` value (V8 clears the
        // revoked proxy's target to null).
        let target_after = proxy.get_target(tc);
        let target_undefined_after = target_after.is_undefined();
        let target_null_after = target_after.is_null();
        (
            target_same_before,
            not_revoked_before,
            revoked_after,
            native_get,
            native_caught,
            native_message,
            js_error_name,
            target_undefined_after,
            target_null_after,
        )
    };

    let actual = Json::obj(vec![
        (
            "target_same_before_revoke",
            Json::b(target_same_before_revoke),
        ),
        ("not_revoked_before", Json::b(not_revoked_before)),
        ("revoked_after", Json::b(revoked_after)),
        (
            "native_get_after_revoke_is_none",
            Json::b(native_get_after_revoke),
        ),
        ("native_caught", Json::b(native_caught)),
        ("native_message", Json::s(&native_message)),
        ("js_error_name", Json::s(&js_error_name)),
        (
            "target_undefined_after_revoke",
            Json::b(target_undefined_after_revoke),
        ),
        (
            "target_null_after_revoke",
            Json::b(target_null_after_revoke),
        ),
    ]);
    let expected = Json::obj(vec![
        ("target_same_before_revoke", Json::b(true)),
        ("not_revoked_before", Json::b(true)),
        ("revoked_after", Json::b(true)),
        ("native_get_after_revoke_is_none", Json::b(true)),
        ("native_caught", Json::b(true)),
        (
            "native_message",
            Json::s("Uncaught TypeError: Cannot perform 'get' on a proxy that has been revoked"),
        ),
        ("js_error_name", Json::s("TypeError")),
        ("target_undefined_after_revoke", Json::b(false)),
        ("target_null_after_revoke", Json::b(true)),
    ]);
    vec![expect_eq(
        "runtime-values/proxy_revoke_semantics",
        expected,
        actual,
    )]
}

/// A non-callable trap raises the deterministic invariant TypeError on
/// first use; `Proxy.revocable` from JS is visible to `is_revoked`
/// natively.
#[allow(clippy::too_many_lines)]
fn proxy_trap_invariant_error() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let (native_get_is_none, caught, message, js_revocable_sees) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let target = eval(tc, "({x: 1})").unwrap();
        let target = target.try_cast::<v8::Object>().ok().unwrap();
        let handler_value = eval(tc, "({get: 1})").unwrap();
        let handler = handler_value.try_cast::<v8::Object>().ok().unwrap();
        let proxy = v8::Proxy::new(tc, target, handler).unwrap();
        let proxy_value: v8::Local<v8::Value> = proxy.into();
        let proxy_obj = proxy_value.try_cast::<v8::Object>().ok().unwrap();
        let x = v8::String::new(tc, "x").unwrap();
        let native_get = proxy_obj.get(tc, x.into()).is_none();
        let (caught, message) = caught_message!(tc);

        let js_revocable_sees = eval_text(
            tc,
            "(function(){ const r = Proxy.revocable({a: 1}, {}); globalThis.rpr = r; r.revoke(); return 'revoked'; })()",
        );
        (native_get, caught, message, js_revocable_sees)
    };

    let revoked_via_native = {
        let proxy_value = eval(scope, "rpr.proxy").unwrap();
        let is_revoked = proxy_value
            .try_cast::<v8::Proxy>()
            .map(|p| p.is_revoked())
            .ok();
        is_revoked == Some(true)
    };

    let actual = Json::obj(vec![
        ("native_get_is_none", Json::b(native_get_is_none)),
        ("caught", Json::b(caught)),
        ("message", Json::s(&message)),
        ("js_revocable", Json::s(&js_revocable_sees)),
        ("js_revocable_is_revoked", Json::b(revoked_via_native)),
    ]);
    let expected = Json::obj(vec![
        ("native_get_is_none", Json::b(true)),
        ("caught", Json::b(true)),
        ("message", Json::s("Uncaught TypeError: '1' returned for property 'get' of object '#<Object>' is not a function")),
        ("js_revocable", Json::s("revoked")),
        ("js_revocable_is_revoked", Json::b(true)),
    ]);
    vec![expect_eq(
        "runtime-values/proxy_trap_invariant_error",
        expected,
        actual,
    )]
}

/// `v8::Symbol::new` identity/description/type-of behavior, observable
/// both natively and from JS.
#[allow(clippy::too_many_lines)]
fn symbol_identity_and_description() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let desc = v8::String::new(scope, "gov8").unwrap();
    let s1 = v8::Symbol::new(scope, Some(desc));
    let s2 = v8::Symbol::new(scope, None);

    let s1_value: v8::Local<v8::Value> = s1.into();
    let description_value = s1.description(scope);
    let description_text = value_text(scope, description_value);
    let s2_description_is_undefined = s2.description(scope).is_undefined();
    let typeof_string = s1_value.type_of(scope);
    let typeof_text = value_text(scope, typeof_string.into());
    // Pinned nuance: ToString of a symbol throws (it is not a string
    // primitive conversion); the JS-facing `String(sym)`/description
    // paths below are the well-defined spellings.
    let (to_string_text, to_string_caught) = {
        let tc = std::pin::pin!(v8::TryCatch::new(scope));
        let tc = &mut tc.init();
        let text = value_text(tc, s1_value);
        (text, tc.has_caught())
    };
    let strict_different = !s1_value.strict_equals(
        v8::Symbol::new(scope, Some(v8::String::new(scope, "gov8").unwrap())).into(),
    );

    global.set(
        scope,
        v8::String::new(scope, "sym1").unwrap().into(),
        s1_value,
    );
    let js_typeof = eval_text(scope, "typeof sym1");
    let js_string = eval_text(scope, "String(sym1)");
    let js_description = eval_text(scope, "sym1.description");

    let actual = Json::obj(vec![
        ("is_symbol", Json::b(s1_value.is_symbol())),
        ("description", Json::s(&description_text)),
        (
            "no_description_is_undefined",
            Json::b(s2_description_is_undefined),
        ),
        ("typeof", Json::s(&typeof_text)),
        ("to_string", Json::s(&to_string_text)),
        ("to_string_throws", Json::b(to_string_caught)),
        ("fresh_symbols_differ", Json::b(strict_different)),
        ("js_typeof", Json::s(&js_typeof)),
        ("js_string", Json::s(&js_string)),
        ("js_description", Json::s(&js_description)),
    ]);
    let expected = Json::obj(vec![
        ("is_symbol", Json::b(true)),
        ("description", Json::s("gov8")),
        ("no_description_is_undefined", Json::b(true)),
        ("typeof", Json::s("symbol")),
        ("to_string", Json::s("")),
        ("to_string_throws", Json::b(true)),
        ("fresh_symbols_differ", Json::b(true)),
        ("js_typeof", Json::s("symbol")),
        ("js_string", Json::s("Symbol(gov8)")),
        ("js_description", Json::s("gov8")),
    ]);
    vec![expect_eq(
        "runtime-values/symbol_identity_and_description",
        expected,
        actual,
    )]
}

/// The global symbol registry: `for_key` is `Symbol.for`, `for_api` is a
/// separate embedder-only registry, both idempotent within the isolate,
/// and the JS registry matches `for_key`.
#[allow(clippy::too_many_lines)]
fn symbol_registry() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let d1 = v8::String::new(scope, "gov8.slice").unwrap();
    let d2 = v8::String::new(scope, "gov8.other").unwrap();
    let k1a = v8::Symbol::for_key(scope, d1);
    let k1b = v8::Symbol::for_key(scope, v8::String::new(scope, "gov8.slice").unwrap());
    let k2 = v8::Symbol::for_key(scope, d2);
    let k1_value: v8::Local<v8::Value> = k1a.into();
    let k2_value: v8::Local<v8::Value> = k2.into();

    let a1 = v8::Symbol::for_api(scope, v8::String::new(scope, "gov8.slice").unwrap());
    let a1b = v8::Symbol::for_api(scope, v8::String::new(scope, "gov8.slice").unwrap());
    let a1_value: v8::Local<v8::Value> = a1.into();
    let a1b_value: v8::Local<v8::Value> = a1b.into();

    let js_registry_symbol = eval(scope, "Symbol.for('gov8.slice')").unwrap();
    {
        let global = context.global(scope);
        global.set(
            scope,
            v8::String::new(scope, "symk").unwrap().into(),
            k1_value,
        );
    }
    let js_key_for = eval_text(scope, "Symbol.keyFor(symk)");
    let registry_matches_js = k1_value.strict_equals(js_registry_symbol);
    let fresh_js_symbol_differs =
        !k1_value.strict_equals(eval(scope, "Symbol('gov8.slice')").unwrap());

    let actual = Json::obj(vec![
        (
            "for_key_idempotent",
            Json::b(k1_value.strict_equals(k1b.into())),
        ),
        (
            "for_key_different_descriptions_differ",
            Json::b(!k1_value.strict_equals(k2_value)),
        ),
        (
            "for_api_idempotent",
            Json::b(a1_value.strict_equals(a1b_value)),
        ),
        (
            "for_api_differs_from_for_key",
            Json::b(!a1_value.strict_equals(k1_value)),
        ),
        (
            "registry_matches_js_symbol_for",
            Json::b(registry_matches_js),
        ),
        ("fresh_js_symbol_differs", Json::b(fresh_js_symbol_differs)),
        ("js_key_for", Json::s(&js_key_for)),
    ]);
    let expected = Json::obj(vec![
        ("for_key_idempotent", Json::b(true)),
        ("for_key_different_descriptions_differ", Json::b(true)),
        ("for_api_idempotent", Json::b(true)),
        ("for_api_differs_from_for_key", Json::b(true)),
        ("registry_matches_js_symbol_for", Json::b(true)),
        ("fresh_js_symbol_differs", Json::b(true)),
        ("js_key_for", Json::s("gov8.slice")),
    ]);
    vec![expect_eq(
        "runtime-values/symbol_registry",
        expected,
        actual,
    )]
}

/// Well-known symbols set natively change JS behavior: `Symbol.toStringTag`
/// rewrites `Object.prototype.toString`, `Symbol.iterator` drives spread,
/// `Symbol.hasInstance` overrides `instanceof`, and the JS
/// `Symbol.iterator` is the same symbol the crate exposes.
#[allow(clippy::too_many_lines)]
fn symbol_wellknown_interop() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    // toStringTag
    let tag_target = eval(scope, "({})").unwrap();
    let tag_target = tag_target.try_cast::<v8::Object>().ok().unwrap();
    let tag = v8::String::new(scope, "Gov8").unwrap();
    tag_target.set(
        scope,
        v8::Symbol::get_to_string_tag(scope).into(),
        tag.into(),
    );
    global.set(
        scope,
        v8::String::new(scope, "tagged").unwrap().into(),
        tag_target.into(),
    );
    let js_to_string_tag = eval_text(scope, "Object.prototype.toString.call(tagged)");

    // iterator
    let iterable = eval(scope, "({length: 2, 0: 'a', 1: 'b'})").unwrap();
    let iterable = iterable.try_cast::<v8::Object>().ok().unwrap();
    let generator = eval(scope, "(function*(){ yield 1; yield 2; })").unwrap();
    iterable.set(scope, v8::Symbol::get_iterator(scope).into(), generator);
    global.set(
        scope,
        v8::String::new(scope, "it").unwrap().into(),
        iterable.into(),
    );
    let js_spread = eval_text(scope, "[...it].join('-')");

    // hasInstance. `Symbol.hasInstance` is non-writable on
    // Function.prototype, so a plain `set` is silently ignored;
    // define_own_property creates the own property instead.
    let ctor = eval(scope, "function C(){}; C").unwrap();
    let ctor = ctor.try_cast::<v8::Object>().ok().unwrap();
    let always_true = eval(scope, "() => true").unwrap();
    let has_instance_sym: v8::Local<v8::Value> = v8::Symbol::get_has_instance(scope).into();
    ctor.set(scope, has_instance_sym, always_true);
    let got_after_set = ctor.get(scope, has_instance_sym).unwrap();
    let plain_set_ignored = !got_after_set.strict_equals(always_true);
    let defined_hi = ctor
        .define_own_property(
            scope,
            v8::Symbol::get_has_instance(scope).into(),
            always_true,
            v8::PropertyAttribute::NONE,
        )
        .unwrap_or_default();
    global.set(
        scope,
        v8::String::new(scope, "C").unwrap().into(),
        ctor.into(),
    );
    let js_instanceof = eval_text(scope, "({}) instanceof C");

    // JS Symbol.iterator identity with the native getter.
    let js_iterator = eval(scope, "Symbol.iterator").unwrap();
    let native_iterator: v8::Local<v8::Value> = v8::Symbol::get_iterator(scope).into();
    let iterator_identity = native_iterator.strict_equals(js_iterator);

    let actual = Json::obj(vec![
        ("js_to_string_tag", Json::s(&js_to_string_tag)),
        ("js_spread", Json::s(&js_spread)),
        ("plain_set_ignored", Json::b(plain_set_ignored)),
        ("defined_has_instance", Json::b(defined_hi)),
        ("js_instanceof", Json::s(&js_instanceof)),
        ("iterator_identity", Json::b(iterator_identity)),
    ]);
    let expected = Json::obj(vec![
        ("js_to_string_tag", Json::s("[object Gov8]")),
        ("js_spread", Json::s("1-2")),
        ("plain_set_ignored", Json::b(true)),
        ("defined_has_instance", Json::b(true)),
        ("js_instanceof", Json::s("true")),
        ("iterator_identity", Json::b(true)),
    ]);
    vec![expect_eq(
        "runtime-values/symbol_wellknown_interop",
        expected,
        actual,
    )]
}

/// Private symbols: native `Object::set_private` family stores data that
/// is completely invisible to JS property machinery, and `Private::for_api`
/// interns per isolate.
#[allow(clippy::too_many_lines)]
fn private_symbol_invisibility() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let name = v8::String::new(scope, "gov8.secret").unwrap();
    let priv1 = v8::Private::new(scope, Some(name));
    let priv1_name_value = priv1.name(scope);
    let priv1_name = value_text(scope, priv1_name_value);
    let anonymous = v8::Private::new(scope, None);
    let anonymous_name_is_undefined = anonymous.name(scope).is_undefined();

    let obj = eval(scope, "({visible: 1})").unwrap();
    let obj = obj.try_cast::<v8::Object>().ok().unwrap();
    let value = v8::Integer::new(scope, 42);
    let set_ok = obj
        .set_private(scope, priv1, value.into())
        .unwrap_or_default();
    let has_private = obj.has_private(scope, priv1).unwrap_or_default();
    let got_private = obj.get_private(scope, priv1).unwrap();
    let get_private = value_text(scope, got_private);

    let p2 = v8::Private::for_api(scope, Some(v8::String::new(scope, "gov8.api").unwrap()));
    let p2b = v8::Private::for_api(scope, Some(v8::String::new(scope, "gov8.api").unwrap()));
    let fresh = v8::Private::for_api(scope, Some(v8::String::new(scope, "gov8.api2").unwrap()));
    let for_api_idempotent = p2 == p2b;
    let for_api_distinct = !(p2 == fresh);

    global.set(
        scope,
        v8::String::new(scope, "po").unwrap().into(),
        obj.into(),
    );
    let js_sees = eval_text(
        scope,
        "[JSON.stringify(po), Object.keys(po).length, 'gov8.secret' in po].join('|')",
    );

    let delete_ok = obj.delete_private(scope, priv1).unwrap_or_default();
    let has_after_delete = obj.has_private(scope, priv1).unwrap_or_default();

    let actual = Json::obj(vec![
        ("name", Json::s(&priv1_name)),
        (
            "anonymous_name_is_undefined",
            Json::b(anonymous_name_is_undefined),
        ),
        ("set_ok", Json::b(set_ok)),
        ("has_private", Json::b(has_private)),
        ("get_private", Json::s(&get_private)),
        ("for_api_idempotent", Json::b(for_api_idempotent)),
        ("for_api_distinct", Json::b(for_api_distinct)),
        ("js_sees", Json::s(&js_sees)),
        ("delete_ok", Json::b(delete_ok)),
        ("has_after_delete", Json::b(has_after_delete)),
    ]);
    let expected = Json::obj(vec![
        ("name", Json::s("gov8.secret")),
        ("anonymous_name_is_undefined", Json::b(true)),
        ("set_ok", Json::b(true)),
        ("has_private", Json::b(true)),
        ("get_private", Json::s("42")),
        ("for_api_idempotent", Json::b(true)),
        ("for_api_distinct", Json::b(true)),
        ("js_sees", Json::s("{\"visible\":1}|1|false")),
        ("delete_ok", Json::b(true)),
        ("has_after_delete", Json::b(false)),
    ]);
    vec![expect_eq(
        "runtime-values/private_symbol_invisibility",
        expected,
        actual,
    )]
}

/// Wrapper objects vs primitives: type predicates split, wrappers are
/// always truthy (even `new Boolean(false)`), conversions go through
/// `valueOf`/`toString`, and `strict_equals` never identifies a wrapper
/// with its primitive.
#[allow(clippy::too_many_lines)]
fn primitive_wrapper_objects() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let number_wrapper = eval(scope, "new Number(5)").unwrap();
    let boolean_wrapper = eval(scope, "new Boolean(false)").unwrap();
    let string_wrapper = eval(scope, "new String('ab')").unwrap();
    let bigint_wrapper = eval(scope, "Object(123n)").unwrap();
    let primitive = eval(scope, "5").unwrap();

    let number_to_string = value_text(scope, number_wrapper);
    let boolean_to_string = value_text(scope, boolean_wrapper);
    let string_to_string = value_text(scope, string_wrapper);
    let bigint_to_string = value_text(scope, bigint_wrapper);

    let strict_wrapper_primitive = number_wrapper.strict_equals(primitive);

    let actual = Json::obj(vec![
        ("number_is_number", Json::b(number_wrapper.is_number())),
        (
            "number_is_number_object",
            Json::b(number_wrapper.is_number_object()),
        ),
        ("number_is_object", Json::b(number_wrapper.is_object())),
        ("boolean_is_boolean", Json::b(boolean_wrapper.is_boolean())),
        (
            "boolean_is_boolean_object",
            Json::b(boolean_wrapper.is_boolean_object()),
        ),
        ("boolean_object_is_true", Json::b(boolean_wrapper.is_true())),
        ("string_is_string", Json::b(string_wrapper.is_string())),
        (
            "string_is_string_object",
            Json::b(string_wrapper.is_string_object()),
        ),
        ("string_is_name", Json::b(string_wrapper.is_name())),
        ("bigint_is_big_int", Json::b(bigint_wrapper.is_big_int())),
        (
            "bigint_is_big_int_object",
            Json::b(bigint_wrapper.is_big_int_object()),
        ),
        ("number_to_string", Json::s(&number_to_string)),
        ("boolean_to_string", Json::s(&boolean_to_string)),
        ("string_to_string", Json::s(&string_to_string)),
        ("bigint_to_string", Json::s(&bigint_to_string)),
        (
            "strict_wrapper_primitive",
            Json::b(strict_wrapper_primitive),
        ),
        (
            "js",
            Json::s(&{
                let tc = std::pin::pin!(v8::TryCatch::new(scope));
                let tc = &mut tc.init();
                eval_text(
                    tc,
                    "const nw = new Number(5), bw = new Boolean(false), sw = new String('ab'); \
                     [typeof nw, nw + 1, nw.valueOf(), bw ? 1 : 0, sw.length, typeof sw].join('|')",
                )
            }),
        ),
    ]);
    let expected = Json::obj(vec![
        ("number_is_number", Json::b(false)),
        ("number_is_number_object", Json::b(true)),
        ("number_is_object", Json::b(true)),
        ("boolean_is_boolean", Json::b(false)),
        ("boolean_is_boolean_object", Json::b(true)),
        ("boolean_object_is_true", Json::b(false)),
        ("string_is_string", Json::b(false)),
        ("string_is_string_object", Json::b(true)),
        ("string_is_name", Json::b(false)),
        ("bigint_is_big_int", Json::b(false)),
        ("bigint_is_big_int_object", Json::b(true)),
        ("number_to_string", Json::s("5")),
        ("boolean_to_string", Json::s("false")),
        ("string_to_string", Json::s("ab")),
        ("bigint_to_string", Json::s("123")),
        ("strict_wrapper_primitive", Json::b(false)),
        ("js", Json::s("object|6|5|1|2|object")),
    ]);
    vec![expect_eq(
        "runtime-values/primitive_wrapper_objects",
        expected,
        actual,
    )]
}

/// `PropertyAttribute` bits through `define_own_property` /
/// `get_property_attributes`: round-trip of all three bits, the
/// missing-property result, JS descriptor agreement, silent non-strict
/// write failure, failed delete, and key enumeration excluding
/// non-enumerable entries.
#[allow(clippy::too_many_lines)]
fn property_attributes_bits() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let obj = v8::Object::new(scope);
    let obj_value: v8::Local<v8::Value> = obj.into();
    global.set(
        scope,
        v8::String::new(scope, "oa").unwrap().into(),
        obj_value,
    );

    let plain = v8::String::new(scope, "plain").unwrap();
    let one = v8::Integer::new(scope, 1);
    let created = obj
        .create_data_property(scope, plain.into(), one.into())
        .unwrap_or_default();
    let plain_attrs = obj.get_property_attributes(scope, plain.into()).unwrap();

    let ro = v8::String::new(scope, "ro").unwrap();
    let two = v8::Integer::new(scope, 2);
    let defined_ro = obj
        .define_own_property(
            scope,
            ro.into(),
            two.into(),
            v8::PropertyAttribute::READ_ONLY,
        )
        .unwrap_or_default();
    let ro_attrs = obj.get_property_attributes(scope, ro.into()).unwrap();

    let locked = v8::String::new(scope, "locked").unwrap();
    let three = v8::Integer::new(scope, 3);
    obj.define_own_property(
        scope,
        locked.into(),
        three.into(),
        v8::PropertyAttribute::READ_ONLY
            | v8::PropertyAttribute::DONT_ENUM
            | v8::PropertyAttribute::DONT_DELETE,
    );
    let locked_attrs = obj.get_property_attributes(scope, locked.into()).unwrap();

    let missing = v8::String::new(scope, "missing").unwrap();
    // Pinned nuance: a missing property yields Some(NONE), not None (the
    // doc comment in object.rs matches the value, not the Option shape).
    let missing_is_some = obj.get_property_attributes(scope, missing.into()).is_some();

    let js_descriptor = eval_text(
        scope,
        "JSON.stringify(Object.getOwnPropertyDescriptor(oa, 'locked'))",
    );
    let js_write = eval_text(scope, "(function(){ oa.locked = 99; return oa.locked; })()");
    let js_delete = eval_text(scope, "delete oa.locked");
    let js_keys = eval_text(scope, "JSON.stringify(Object.keys(oa))");

    let actual = Json::obj(vec![
        ("create_ok", Json::b(created)),
        ("plain_attrs", Json::i(plain_attrs.as_u32() as i64)),
        ("defined_ro", Json::b(defined_ro)),
        ("ro_attrs", Json::i(ro_attrs.as_u32() as i64)),
        ("locked_attrs", Json::i(locked_attrs.as_u32() as i64)),
        ("missing_is_some", Json::b(missing_is_some)),
        ("js_descriptor", Json::s(&js_descriptor)),
        ("js_write_result", Json::s(&js_write)),
        ("js_delete", Json::s(&js_delete)),
        ("js_keys", Json::s(&js_keys)),
    ]);
    let expected = Json::obj(vec![
        ("create_ok", Json::b(true)),
        ("plain_attrs", Json::i(0)),
        ("defined_ro", Json::b(true)),
        ("ro_attrs", Json::i(1)),
        ("locked_attrs", Json::i(7)),
        ("missing_is_some", Json::b(true)),
        (
            "js_descriptor",
            Json::s("{\"value\":3,\"writable\":false,\"enumerable\":false,\"configurable\":false}"),
        ),
        ("js_write_result", Json::s("3")),
        ("js_delete", Json::s("false")),
        ("js_keys", Json::s("[\"plain\",\"ro\"]")),
    ]);
    vec![expect_eq(
        "runtime-values/property_attributes_bits",
        expected,
        actual,
    )]
}

/// `Object::set_integrity_level`: sealed forbids deletion and additions,
/// frozen additionally makes existing data properties read-only, both
/// observable through `get_property_attributes` and JS predicates.
#[allow(clippy::too_many_lines)]
fn integrity_levels() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let sealed = v8::Object::new(scope);
    let sealed_value: v8::Local<v8::Value> = sealed.into();
    let a = v8::String::new(scope, "a").unwrap();
    let one = v8::Integer::new(scope, 1);
    sealed.create_data_property(scope, a.into(), one.into());
    let sealed_ok = sealed
        .set_integrity_level(scope, v8::IntegrityLevel::Sealed)
        .unwrap_or_default();
    let sealed_attrs = sealed.get_property_attributes(scope, a.into()).unwrap();
    global.set(
        scope,
        v8::String::new(scope, "sl").unwrap().into(),
        sealed_value,
    );
    let js_sealed = eval_text(scope, "Object.isSealed(sl)");
    let js_add = eval_text(
        scope,
        "(function(){ sl.newProp = 1; return sl.newProp === undefined; })()",
    );
    let js_delete = eval_text(scope, "delete sl.a");

    let frozen = v8::Object::new(scope);
    let frozen_value: v8::Local<v8::Value> = frozen.into();
    let b = v8::String::new(scope, "b").unwrap();
    let two = v8::Integer::new(scope, 2);
    frozen.create_data_property(scope, b.into(), two.into());
    let frozen_ok = frozen
        .set_integrity_level(scope, v8::IntegrityLevel::Frozen)
        .unwrap_or_default();
    let frozen_attrs = frozen.get_property_attributes(scope, b.into()).unwrap();
    global.set(
        scope,
        v8::String::new(scope, "fz").unwrap().into(),
        frozen_value,
    );
    let js_frozen = eval_text(scope, "Object.isFrozen(fz)");
    let js_write = eval_text(scope, "(function(){ fz.b = 99; return fz.b; })()");

    let actual = Json::obj(vec![
        ("sealed_ok", Json::b(sealed_ok)),
        ("sealed_attrs", Json::i(sealed_attrs.as_u32() as i64)),
        ("js_is_sealed", Json::s(&js_sealed)),
        ("js_add_silently_fails", Json::s(&js_add)),
        ("js_delete", Json::s(&js_delete)),
        ("frozen_ok", Json::b(frozen_ok)),
        ("frozen_attrs", Json::i(frozen_attrs.as_u32() as i64)),
        ("js_is_frozen", Json::s(&js_frozen)),
        ("js_write_result", Json::s(&js_write)),
    ]);
    let expected = Json::obj(vec![
        ("sealed_ok", Json::b(true)),
        ("sealed_attrs", Json::i(4)),
        ("js_is_sealed", Json::s("true")),
        ("js_add_silently_fails", Json::s("true")),
        ("js_delete", Json::s("false")),
        ("frozen_ok", Json::b(true)),
        ("frozen_attrs", Json::i(5)),
        ("js_is_frozen", Json::s("true")),
        ("js_write_result", Json::s("2")),
    ]);
    vec![expect_eq(
        "runtime-values/integrity_levels",
        expected,
        actual,
    )]
}

/// The native `PropertyDescriptor` struct: presence flags and defaults for
/// each constructor flavor, in-place setters, and its effect through
/// `Object::define_property`.
#[allow(clippy::too_many_lines)]
fn native_property_descriptors() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let describe = |pd: &v8::PropertyDescriptor| {
        Json::obj(vec![
            ("has_value", Json::b(pd.has_value())),
            ("has_writable", Json::b(pd.has_writable())),
            ("has_enumerable", Json::b(pd.has_enumerable())),
            ("has_configurable", Json::b(pd.has_configurable())),
            ("has_get", Json::b(pd.has_get())),
            ("has_set", Json::b(pd.has_set())),
            ("writable", Json::b(pd.writable())),
            ("enumerable", Json::b(pd.enumerable())),
            ("configurable", Json::b(pd.configurable())),
        ])
    };

    let default_pd = v8::PropertyDescriptor::new();
    let default_described = describe(&default_pd);

    let value = v8::Integer::new(scope, 5);
    let value_pd = v8::PropertyDescriptor::new_from_value(value.into());
    let value_described = describe(&value_pd);
    let value_is_five = value_pd.value().strict_equals(value.into());

    let writable_pd = v8::PropertyDescriptor::new_from_value_writable(value.into(), true);
    let writable_described = describe(&writable_pd);
    let mut open_pd = v8::PropertyDescriptor::new_from_value_writable(value.into(), false);
    open_pd.set_enumerable(true);
    open_pd.set_configurable(true);
    let open_described = describe(&open_pd);

    let getter = eval(scope, "(() => 7)").unwrap();
    let setter = eval(scope, "(() => {})").unwrap();
    let accessor_pd = v8::PropertyDescriptor::new_from_get_set(getter, setter);
    let accessor_described = Json::obj(vec![
        ("has_value", Json::b(accessor_pd.has_value())),
        ("has_get", Json::b(accessor_pd.has_get())),
        ("has_set", Json::b(accessor_pd.has_set())),
        ("get_same", Json::b(accessor_pd.get().strict_equals(getter))),
        ("set_same", Json::b(accessor_pd.set().strict_equals(setter))),
    ]);

    // Effect through define_property: descriptor with only value+writable
    // leaves enumerable/configurable at their spec defaults (false).
    let target = v8::Object::new(scope);
    let defined = target
        .define_property(
            scope,
            v8::String::new(scope, "d").unwrap().into(),
            &writable_pd,
        )
        .unwrap_or_default();
    let defined_ro = target
        .define_property(
            scope,
            v8::String::new(scope, "ro").unwrap().into(),
            &v8::PropertyDescriptor::new_from_value_writable(value.into(), false),
        )
        .unwrap_or_default();
    global.set(
        scope,
        v8::String::new(scope, "dt").unwrap().into(),
        target.into(),
    );
    let js_descriptor = eval_text(
        scope,
        "JSON.stringify(Object.getOwnPropertyDescriptor(dt, 'ro'))",
    );
    let js_write = eval_text(scope, "(function(){ dt.ro = 50; return dt.ro; })()");

    let actual = Json::obj(vec![
        ("default", default_described),
        ("from_value", value_described),
        ("from_value_value_is_five", Json::b(value_is_five)),
        ("from_value_writable_true", writable_described),
        ("after_setters", open_described),
        ("accessor", accessor_described),
        ("defined", Json::b(defined)),
        ("defined_ro", Json::b(defined_ro)),
        ("js_descriptor", Json::s(&js_descriptor)),
        ("js_write_result", Json::s(&js_write)),
    ]);
    let expected = Json::obj(vec![
        (
            "default",
            Json::obj(vec![
                ("has_value", Json::b(false)),
                ("has_writable", Json::b(false)),
                ("has_enumerable", Json::b(false)),
                ("has_configurable", Json::b(false)),
                ("has_get", Json::b(false)),
                ("has_set", Json::b(false)),
                ("writable", Json::b(false)),
                ("enumerable", Json::b(false)),
                ("configurable", Json::b(false)),
            ]),
        ),
        (
            "from_value",
            Json::obj(vec![
                ("has_value", Json::b(true)),
                ("has_writable", Json::b(false)),
                ("has_enumerable", Json::b(false)),
                ("has_configurable", Json::b(false)),
                ("has_get", Json::b(false)),
                ("has_set", Json::b(false)),
                ("writable", Json::b(false)),
                ("enumerable", Json::b(false)),
                ("configurable", Json::b(false)),
            ]),
        ),
        ("from_value_value_is_five", Json::b(true)),
        (
            "from_value_writable_true",
            Json::obj(vec![
                ("has_value", Json::b(true)),
                ("has_writable", Json::b(true)),
                ("has_enumerable", Json::b(false)),
                ("has_configurable", Json::b(false)),
                ("has_get", Json::b(false)),
                ("has_set", Json::b(false)),
                ("writable", Json::b(true)),
                ("enumerable", Json::b(false)),
                ("configurable", Json::b(false)),
            ]),
        ),
        (
            "after_setters",
            Json::obj(vec![
                ("has_value", Json::b(true)),
                ("has_writable", Json::b(true)),
                ("has_enumerable", Json::b(true)),
                ("has_configurable", Json::b(true)),
                ("has_get", Json::b(false)),
                ("has_set", Json::b(false)),
                ("writable", Json::b(false)),
                ("enumerable", Json::b(true)),
                ("configurable", Json::b(true)),
            ]),
        ),
        (
            "accessor",
            Json::obj(vec![
                ("has_value", Json::b(false)),
                ("has_get", Json::b(true)),
                ("has_set", Json::b(true)),
                ("get_same", Json::b(true)),
                ("set_same", Json::b(true)),
            ]),
        ),
        ("defined", Json::b(true)),
        ("defined_ro", Json::b(true)),
        (
            "js_descriptor",
            Json::s("{\"value\":5,\"writable\":false,\"enumerable\":false,\"configurable\":false}"),
        ),
        ("js_write_result", Json::s("5")),
    ]);
    vec![expect_eq(
        "runtime-values/native_property_descriptors",
        expected,
        actual,
    )]
}

/// `Object::get_own_property_descriptor` mirrors
/// `Object.getOwnPropertyDescriptor` (including descriptor key order and
/// accessor shape), returns `None` for missing keys, and
/// `get_own_property_names` honors symbol/index filters.
#[allow(clippy::too_many_lines)]
fn js_property_descriptor_view() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let global = context.global(scope);

    let obj = v8::Object::new(scope);
    let obj_value: v8::Local<v8::Value> = obj.into();
    global.set(
        scope,
        v8::String::new(scope, "od").unwrap().into(),
        obj_value,
    );

    let data = v8::String::new(scope, "data").unwrap();
    let one = v8::Integer::new(scope, 1);
    obj.create_data_property(scope, data.into(), one.into());

    let hidden = v8::String::new(scope, "hidden").unwrap();
    let two = v8::Integer::new(scope, 2);
    obj.define_own_property(
        scope,
        hidden.into(),
        two.into(),
        v8::PropertyAttribute::DONT_ENUM,
    );

    eval_text(
        scope,
        "Object.defineProperty(od, 'acc', {get(){ return 7; }, configurable: true})",
    );

    let data_json = own_descriptor(scope, obj, "data").map(|d| stringify_text(scope, d));
    let hidden_json = own_descriptor(scope, obj, "hidden").map(|d| stringify_text(scope, d));
    let accessor = own_descriptor(scope, obj, "acc");
    let accessor_json = accessor.as_ref().map(|d| stringify_text(scope, *d));
    let accessor_keys = accessor
        .as_ref()
        .map(|d| {
            let d = *d;
            let d = d.try_cast::<v8::Object>().ok().unwrap();
            let names = d
                .get_own_property_names(
                    scope,
                    v8::GetPropertyNamesArgs {
                        mode: v8::KeyCollectionMode::OwnOnly,
                        property_filter: v8::PropertyFilter::ALL_PROPERTIES,
                        index_filter: v8::IndexFilter::SkipIndices,
                        key_conversion: v8::KeyConversionMode::ConvertToString,
                    },
                )
                .unwrap();
            let mut parts = Vec::new();
            for i in 0..names.length() {
                let name = names.get_index(scope, i).unwrap();
                parts.push(value_text(scope, name));
            }
            format!("[{}]", parts.join(","))
        })
        .unwrap_or_default();
    let accessor_get_is_function = accessor
        .map(|d| {
            let obj = d.try_cast::<v8::Object>().ok().unwrap();
            obj.get(scope, v8::String::new(scope, "get").unwrap().into())
                .map(|g| g.is_function())
                .unwrap_or_default()
        })
        .unwrap_or_default();
    // Pinned nuance: a missing key resolves to the `undefined` value (a
    // Some result), not None.
    let missing_stringify = own_descriptor(scope, obj, "missing")
        .map(|d| stringify_text(scope, d))
        .unwrap_or_default();

    // Property-name filters on an object with string, symbol and index keys.
    let mixed = eval(scope, "({s: 1, [Symbol('y')]: 2, 42: 3})").unwrap();
    let mixed = mixed.try_cast::<v8::Object>().ok().unwrap();
    let mut names_of = |filter: v8::PropertyFilter, conversion: v8::KeyConversionMode| {
        let names = mixed
            .get_own_property_names(
                scope,
                v8::GetPropertyNamesArgs {
                    mode: v8::KeyCollectionMode::OwnOnly,
                    property_filter: filter,
                    index_filter: v8::IndexFilter::IncludeIndices,
                    key_conversion: conversion,
                },
            )
            .unwrap();
        let mut parts = Vec::new();
        for i in 0..names.length() {
            let name = names.get_index(scope, i).unwrap();
            parts.push(stringify_text(scope, name));
        }
        format!("[{}]", parts.join(","))
    };
    let default_names = names_of(
        v8::PropertyFilter::ONLY_ENUMERABLE | v8::PropertyFilter::SKIP_SYMBOLS,
        v8::KeyConversionMode::KeepNumbers,
    );
    let with_symbols = names_of(
        v8::PropertyFilter::ONLY_ENUMERABLE,
        v8::KeyConversionMode::KeepNumbers,
    );
    let strings_converted = names_of(
        v8::PropertyFilter::ONLY_ENUMERABLE | v8::PropertyFilter::SKIP_SYMBOLS,
        v8::KeyConversionMode::ConvertToString,
    );

    let actual = Json::obj(vec![
        ("data", Json::s(&data_json.unwrap_or_default())),
        ("hidden", Json::s(&hidden_json.unwrap_or_default())),
        ("accessor", Json::s(&accessor_json.unwrap_or_default())),
        ("accessor_keys", Json::s(&accessor_keys)),
        (
            "accessor_get_is_function",
            Json::b(accessor_get_is_function),
        ),
        ("missing_stringify", Json::s(&missing_stringify)),
        ("names_default", Json::s(&default_names)),
        ("names_with_symbols", Json::s(&with_symbols)),
        ("names_keys_converted", Json::s(&strings_converted)),
    ]);
    let expected = Json::obj(vec![
        (
            "data",
            Json::s("{\"value\":1,\"writable\":true,\"enumerable\":true,\"configurable\":true}"),
        ),
        (
            "hidden",
            Json::s("{\"value\":2,\"writable\":true,\"enumerable\":false,\"configurable\":true}"),
        ),
        (
            "accessor",
            Json::s("{\"enumerable\":false,\"configurable\":true}"),
        ),
        (
            "accessor_keys",
            Json::s("[get,set,enumerable,configurable]"),
        ),
        ("accessor_get_is_function", Json::b(true)),
        ("missing_stringify", Json::s("undefined")),
        ("names_default", Json::s("[42,\"s\"]")),
        ("names_with_symbols", Json::s("[42,\"s\",undefined]")),
        ("names_keys_converted", Json::s("[\"42\",\"s\"]")),
    ]);
    vec![expect_eq(
        "runtime-values/js_property_descriptor_view",
        expected,
        actual,
    )]
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

const CHECKS: &[fn() -> Vec<CheckOutcome>] = &[
    date_construction_and_value_of,
    date_invalid_time_value_error,
    regexp_new_flags_and_source,
    regexp_exec_and_last_index,
    regexp_invalid_pattern_error,
    regexp_js_created_source,
    json_parse_canonical,
    json_parse_errors,
    json_stringify_objects,
    json_stringify_boundaries,
    array_new_and_elements,
    array_index_semantics,
    map_native_ops,
    set_native_ops,
    map_set_js_interop,
    proxy_identity_and_default_traps,
    proxy_revoke_semantics,
    proxy_trap_invariant_error,
    symbol_identity_and_description,
    symbol_registry,
    symbol_wellknown_interop,
    private_symbol_invisibility,
    primitive_wrapper_objects,
    property_attributes_bits,
    integrity_levels,
    native_property_descriptors,
    js_property_descriptor_view,
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
