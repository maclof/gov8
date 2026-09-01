//! Residual non-callback Object API conformance for rusty_v8 152.2.0.

use std::io::Write as _;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn text(scope: &v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Value>) -> String {
    value.to_string(scope).unwrap().to_rust_string_lossy(scope)
}

fn construction() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let null: v8::Local<v8::Value> = v8::null(scope).into();
    let alpha: v8::Local<v8::Name> = v8::String::new(scope, "alpha").unwrap().into();
    let symbol: v8::Local<v8::Name> =
        v8::Symbol::for_key(scope, v8::String::new(scope, "sym").unwrap()).into();
    let forty_two: v8::Local<v8::Value> = v8::Integer::new(scope, 42).into();
    let payload: v8::Local<v8::Value> = v8::String::new(scope, "payload").unwrap().into();
    let object = v8::Object::with_prototype_and_properties(
        scope,
        null,
        &[alpha, symbol],
        &[forty_two, payload],
    );
    let proto = object.get_prototype(scope).unwrap();
    let alpha_value = object.get(scope, alpha.into()).unwrap();
    let symbol_value = object.get(scope, symbol.into()).unwrap();
    let alpha_attr = object.get_property_attributes(scope, alpha.into()).unwrap();

    let actual = Json::obj(vec![
        ("prototype_is_null", Json::b(proto.is_null())),
        ("alpha", Json::s(&text(scope, alpha_value))),
        ("symbol", Json::s(&text(scope, symbol_value))),
        (
            "attributes_none",
            Json::b(alpha_attr == v8::PropertyAttribute::NONE),
        ),
        (
            "has_alpha",
            Json::b(object.has_own_property(scope, alpha).unwrap()),
        ),
        (
            "has_symbol",
            Json::b(object.has_own_property(scope, symbol).unwrap()),
        ),
    ]);
    let expected = Json::obj(vec![
        ("prototype_is_null", Json::b(true)),
        ("alpha", Json::s("42")),
        ("symbol", Json::s("payload")),
        ("attributes_none", Json::b(true)),
        ("has_alpha", Json::b(true)),
        ("has_symbol", Json::b(true)),
    ]);
    expect_eq(
        "object-residual/constructor/prototype_properties",
        expected,
        actual,
    )
}

fn own_property_names() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = eval(
        scope,
        "(()=>{const p={inherited:1};const o=Object.create(p);o[2]='two';o.e=1;Object.defineProperty(o,'hidden',{value:1});o[Symbol.for('s')]=1;return o})()",
    )
    .to_object(scope)
    .unwrap();
    let symbol = v8::Symbol::for_key(scope, v8::String::new(scope, "s").unwrap());
    // mode and index_filter are deliberately contrary: this Object method's
    // binding forwards only property_filter and key_conversion.
    let names = object
        .get_own_property_names(
            scope,
            v8::GetPropertyNamesArgs {
                mode: v8::KeyCollectionMode::IncludePrototypes,
                property_filter: v8::PropertyFilter::ALL_PROPERTIES,
                index_filter: v8::IndexFilter::SkipIndices,
                key_conversion: v8::KeyConversionMode::KeepNumbers,
            },
        )
        .unwrap();
    let mut inherited = false;
    let mut enumerable = false;
    let mut hidden = false;
    let mut symbol_seen = false;
    let mut numeric = false;
    for index in 0..names.length() {
        let name = names.get_index(scope, index).unwrap();
        inherited |= name.is_string() && text(scope, name) == "inherited";
        enumerable |= name.is_string() && text(scope, name) == "e";
        hidden |= name.is_string() && text(scope, name) == "hidden";
        symbol_seen |= name.strict_equals(symbol.into());
        numeric |= name.is_uint32() && name.uint32_value(scope) == Some(2);
    }
    let actual = Json::obj(vec![
        ("length", Json::i(names.length() as i64)),
        ("inherited_excluded", Json::b(!inherited)),
        ("index_included_despite_arg", Json::b(numeric)),
        ("enumerable_present", Json::b(enumerable)),
        ("non_enumerable_present", Json::b(hidden)),
        ("symbol_present", Json::b(symbol_seen)),
    ]);
    let expected = Json::obj(vec![
        ("length", Json::i(4)),
        ("inherited_excluded", Json::b(true)),
        ("index_included_despite_arg", Json::b(true)),
        ("enumerable_present", Json::b(true)),
        ("non_enumerable_present", Json::b(true)),
        ("symbol_present", Json::b(true)),
    ]);
    expect_eq("object-residual/names/own_filters", expected, actual)
}

fn preview_entries() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let keys = eval(scope, "new Set([1,2,3]).keys()")
        .to_object(scope)
        .unwrap();
    let (keys_preview, keys_kv) = keys.preview_entries(scope);
    let keys_preview = keys_preview.unwrap();
    let entries = eval(scope, "new Set([1,3]).entries()")
        .to_object(scope)
        .unwrap();
    let (entries_preview, entries_kv) = entries.preview_entries(scope);
    let entries_preview = entries_preview.unwrap();
    let map = eval(scope, "new Map([['a',1],['b',2]])")
        .to_object(scope)
        .unwrap();
    let (map_preview, map_kv) = map.preview_entries(scope);
    let map_preview = map_preview.unwrap();
    let plain = v8::Object::new(scope);
    let (plain_preview, plain_kv) = plain.preview_entries(scope);

    let actual = Json::obj(vec![
        ("keys_length", Json::i(keys_preview.length() as i64)),
        ("keys_key_value", Json::b(keys_kv)),
        (
            "keys_first",
            Json::s(&text(scope, keys_preview.get_index(scope, 0).unwrap())),
        ),
        ("entries_length", Json::i(entries_preview.length() as i64)),
        ("entries_key_value", Json::b(entries_kv)),
        ("map_length", Json::i(map_preview.length() as i64)),
        ("map_key_value", Json::b(map_kv)),
        ("plain_absent", Json::b(plain_preview.is_none())),
        ("plain_key_value", Json::b(plain_kv)),
    ]);
    let expected = Json::obj(vec![
        ("keys_length", Json::i(3)),
        ("keys_key_value", Json::b(false)),
        ("keys_first", Json::s("1")),
        ("entries_length", Json::i(4)),
        ("entries_key_value", Json::b(true)),
        ("map_length", Json::i(4)),
        ("map_key_value", Json::b(true)),
        ("plain_absent", Json::b(true)),
        ("plain_key_value", Json::b(false)),
    ]);
    expect_eq("object-residual/preview/collections", expected, actual)
}

fn api_wrapper() -> CheckOutcome {
    fn empty(
        _scope: &mut v8::PinScope<'_, '_>,
        _args: v8::FunctionCallbackArguments,
        _rv: v8::ReturnValue<v8::Value>,
    ) {
    }
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let plain = v8::Object::new(scope);
    let object_template = v8::ObjectTemplate::new(scope);
    object_template.set_internal_field_count(1);
    let fields = object_template.new_instance(scope).unwrap();
    let function_template = v8::FunctionTemplate::new(scope, empty);
    let function = function_template.get_function(scope).unwrap();
    let api = function.new_instance(scope, &[]).unwrap();

    let actual = Json::obj(vec![
        ("plain", Json::b(plain.is_api_wrapper())),
        ("internal_fields_only", Json::b(fields.is_api_wrapper())),
        ("function_template_instance", Json::b(api.is_api_wrapper())),
    ]);
    let expected = Json::obj(vec![
        ("plain", Json::b(false)),
        ("internal_fields_only", Json::b(true)),
        ("function_template_instance", Json::b(true)),
    ]);
    expect_eq(
        "object-residual/api_wrapper/classification",
        expected,
        actual,
    )
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let outcomes = [
        construction(),
        own_property_names(),
        preview_entries(),
        api_wrapper(),
    ];
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let mut output = String::new();
    for outcome in &outcomes {
        output.push_str(&outcome.to_line());
        output.push('\n');
    }
    output.push_str(&summary_line(
        outcomes.len(),
        passed,
        outcomes.len() - passed,
    ));
    output.push('\n');
    std::io::stdout()
        .lock()
        .write_all(output.as_bytes())
        .unwrap();
    if passed == outcomes.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
