//! Arbitrary `Name` keys accepted by template APIs.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This slice covers the
//! shared `Template::{set,set_with_attr,set_intrinsic_data_property}` methods
//! with both `String` and `Symbol` keys on object and function templates.

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

fn name<'s>(scope: &v8::PinScope<'s, '_, ()>, value: &str) -> v8::Local<'s, v8::Name> {
    v8::String::new(scope, value).unwrap().into()
}

fn symbol<'s>(scope: &v8::PinScope<'s, '_, ()>, description: &str) -> v8::Local<'s, v8::Symbol> {
    let description = v8::String::new(scope, description).unwrap();
    v8::Symbol::new(scope, Some(description))
}

fn integer(
    scope: &v8::PinScope<'_, '_>,
    object: v8::Local<v8::Object>,
    key: v8::Local<v8::Name>,
) -> i64 {
    object
        .get(scope, key.into())
        .and_then(|value| value.integer_value(scope))
        .unwrap_or(-1)
}

fn attributes(value: v8::PropertyAttribute) -> Json {
    Json::obj(vec![
        ("bits", Json::i(i64::from(value.as_u32()))),
        ("read_only", Json::b(value.is_read_only())),
        ("dont_enum", Json::b(value.is_dont_enum())),
        ("dont_delete", Json::b(value.is_dont_delete())),
    ])
}

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn object_template_names() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    let string_key = name(scope, "plain");
    let symbol_key = symbol(scope, "hidden");
    let empty_key = name(scope, "");
    let anonymous_symbol = v8::Symbol::new(scope, None);

    template.set(string_key, v8::Integer::new(scope, 11).into());
    template.set_with_attr(
        symbol_key.into(),
        v8::Integer::new(scope, 22).into(),
        v8::PropertyAttribute::READ_ONLY
            | v8::PropertyAttribute::DONT_ENUM
            | v8::PropertyAttribute::DONT_DELETE,
    );
    template.set(empty_key, v8::Integer::new(scope, 33).into());
    template.set(anonymous_symbol.into(), v8::Integer::new(scope, 44).into());

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let first = template.new_instance(scope).unwrap();
    let second = template.new_instance(scope).unwrap();
    let symbol_attributes = first
        .get_property_attributes(scope, symbol_key.into())
        .unwrap();
    let write = first.set(scope, symbol_key.into(), v8::Integer::new(scope, 99).into());
    let delete = first.delete(scope, symbol_key.into());

    pass(
        "template-name-keys/object/string_symbol_attributes",
        Json::obj(vec![
            ("first_string", Json::i(integer(scope, first, string_key))),
            ("second_string", Json::i(integer(scope, second, string_key))),
            (
                "first_symbol",
                Json::i(integer(scope, first, symbol_key.into())),
            ),
            (
                "second_symbol",
                Json::i(integer(scope, second, symbol_key.into())),
            ),
            ("empty_string", Json::i(integer(scope, first, empty_key))),
            (
                "anonymous_symbol",
                Json::i(integer(scope, first, anonymous_symbol.into())),
            ),
            ("symbol_attributes", attributes(symbol_attributes)),
            ("read_only_write_result", Json::b(write.unwrap_or(false))),
            (
                "symbol_after_write",
                Json::i(integer(scope, first, symbol_key.into())),
            ),
            ("dont_delete_result", Json::b(delete.unwrap_or(false))),
            (
                "symbol_after_delete",
                Json::i(integer(scope, first, symbol_key.into())),
            ),
        ]),
    )
}

fn return_seven(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    rv.set_int32(7);
}

fn function_template_names() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::FunctionTemplate::new(scope, return_seven);
    let string_key = name(scope, "plain");
    let symbol_key = symbol(scope, "static");
    template.set(string_key, v8::Integer::new(scope, 51).into());
    template.set_with_attr(
        symbol_key.into(),
        v8::Integer::new(scope, 52).into(),
        v8::PropertyAttribute::DONT_ENUM,
    );

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let function = template.get_function(scope).unwrap();
    let repeated = template.get_function(scope).unwrap();
    let result = function
        .call(scope, v8::undefined(scope).into(), &[])
        .and_then(|value| value.integer_value(scope));
    let symbol_attributes = function
        .get_property_attributes(scope, symbol_key.into())
        .unwrap();

    pass(
        "template-name-keys/function/string_symbol",
        Json::obj(vec![
            ("call_result", Json::i(result.unwrap_or(-1))),
            (
                "string_value",
                Json::i(integer(scope, function.into(), string_key)),
            ),
            (
                "symbol_value",
                Json::i(integer(scope, function.into(), symbol_key.into())),
            ),
            ("symbol_attributes", attributes(symbol_attributes)),
            (
                "same_function_in_context",
                Json::b(function.strict_equals(repeated.into())),
            ),
        ]),
    )
}

fn distinct_symbol_identity() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    let distinct_a = symbol(scope, "same-description");
    let distinct_b = symbol(scope, "same-description");

    template.set(distinct_a.into(), v8::Integer::new(scope, 5).into());
    template.set(distinct_b.into(), v8::Integer::new(scope, 6).into());

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = template.new_instance(scope).unwrap();

    pass(
        "template-name-keys/symbol/distinct_same_description",
        Json::obj(vec![
            (
                "same_description_symbols_distinct",
                Json::b(!distinct_a.strict_equals(distinct_b.into())),
            ),
            (
                "distinct_a_value",
                Json::i(integer(scope, object, distinct_a.into())),
            ),
            (
                "distinct_b_value",
                Json::i(integer(scope, object, distinct_b.into())),
            ),
        ]),
    )
}

fn duplicate_negative(mode: &str) {
    suppress_windows_fatal_dialogs();
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    match mode {
        "duplicate-string" => {
            let first_key = name(scope, "duplicate");
            let equal_key = name(scope, "duplicate");
            template.set(first_key, v8::Integer::new(scope, 1).into());
            template.set_with_attr(
                equal_key,
                v8::Integer::new(scope, 2).into(),
                v8::PropertyAttribute::DONT_ENUM,
            );
        }
        "duplicate-symbol" => {
            let key = symbol(scope, "duplicate");
            template.set(key.into(), v8::Integer::new(scope, 1).into());
            template.set_with_attr(
                key.into(),
                v8::Integer::new(scope, 2).into(),
                v8::PropertyAttribute::READ_ONLY,
            );
        }
        _ => panic!("unknown negative mode: {mode}"),
    }
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eprintln!("marker:before-instantiation:{mode}");
    let _ = template.new_instance(scope);
    eprintln!("marker:after-instantiation:{mode}");
}

fn intrinsic_symbol_key() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    let symbol_key = symbol(scope, "intrinsic");
    template.set_intrinsic_data_property(
        symbol_key.into(),
        v8::Intrinsic::ArrayPrototype,
        v8::PropertyAttribute::READ_ONLY | v8::PropertyAttribute::DONT_ENUM,
    );

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = template.new_instance(scope).unwrap();
    let value = object.get(scope, symbol_key.into()).unwrap();
    let array_prototype = eval(scope, "Array.prototype");
    let attrs = object
        .get_property_attributes(scope, symbol_key.into())
        .unwrap();

    pass(
        "template-name-keys/intrinsic/symbol",
        Json::obj(vec![
            (
                "is_current_context_array_prototype",
                Json::b(value.strict_equals(array_prototype)),
            ),
            ("attributes", attributes(attrs)),
        ]),
    )
}

fn retained_key_and_late_mutation() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    let held_template = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let template = v8::ObjectTemplate::new(scope);
        let retained = symbol(scope, "retained-after-scope");
        template.set(retained.into(), v8::Integer::new(scope, 71).into());
        v8::Global::new(scope, template)
    };

    isolate.low_memory_notification();

    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let template = v8::Local::new(scope, &held_template);
    let first = template.new_instance(scope).unwrap();
    let late = symbol(scope, "late");
    template.set(late.into(), v8::Integer::new(scope, 72).into());
    let second = template.new_instance(scope).unwrap();

    let global = context.global(scope);
    global
        .set(scope, name(scope, "first").into(), first.into())
        .unwrap();
    global
        .set(scope, name(scope, "second").into(), second.into())
        .unwrap();
    let retained_summary = eval(
        scope,
        "(()=>{const s=Object.getOwnPropertySymbols(second).find(s=>s.description==='retained-after-scope');return `${s.description}|${second[s]}`})()",
    )
    .to_rust_string_lossy(scope);

    pass(
        "template-name-keys/lifecycle/retained_and_late_mutation",
        Json::obj(vec![
            ("retained_summary", Json::s(&retained_summary)),
            (
                "first_has_late",
                Json::b(first.has_own_property(scope, late.into()).unwrap()),
            ),
            (
                "second_has_late",
                Json::b(second.has_own_property(scope, late.into()).unwrap()),
            ),
            (
                "second_late_is_undefined",
                Json::b(second.get(scope, late.into()).unwrap().is_undefined()),
            ),
        ]),
    )
}

fn run_fixture() {
    oracle::ensure_v8();
    let outcomes = [
        object_template_names(),
        function_template_names(),
        distinct_symbol_identity(),
        intrinsic_symbol_key(),
        retained_key_and_late_mutation(),
    ];
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(outcomes.len(), outcomes.len(), 0));
}

fn main() {
    let args: Vec<_> = std::env::args().collect();
    if args.len() == 3 && args[1] == "--negative" {
        duplicate_negative(&args[2]);
    } else {
        assert_eq!(
            args.len(),
            1,
            "usage: conformance-template-name-keys [--negative MODE]"
        );
        run_fixture();
    }
}
