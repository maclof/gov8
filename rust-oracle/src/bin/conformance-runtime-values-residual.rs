//! Residual specialized runtime-value declarations for rusty_v8 152.2.0.

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

fn symbol_all_well_known() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let symbols = [
        (
            "async_iterator",
            v8::Symbol::get_async_iterator(scope),
            "Symbol.asyncIterator",
        ),
        (
            "has_instance",
            v8::Symbol::get_has_instance(scope),
            "Symbol.hasInstance",
        ),
        (
            "is_concat_spreadable",
            v8::Symbol::get_is_concat_spreadable(scope),
            "Symbol.isConcatSpreadable",
        ),
        (
            "iterator",
            v8::Symbol::get_iterator(scope),
            "Symbol.iterator",
        ),
        ("match", v8::Symbol::get_match(scope), "Symbol.match"),
        ("replace", v8::Symbol::get_replace(scope), "Symbol.replace"),
        ("search", v8::Symbol::get_search(scope), "Symbol.search"),
        ("split", v8::Symbol::get_split(scope), "Symbol.split"),
        (
            "to_primitive",
            v8::Symbol::get_to_primitive(scope),
            "Symbol.toPrimitive",
        ),
        (
            "to_string_tag",
            v8::Symbol::get_to_string_tag(scope),
            "Symbol.toStringTag",
        ),
        (
            "unscopables",
            v8::Symbol::get_unscopables(scope),
            "Symbol.unscopables",
        ),
    ];

    let mut actual_fields = Vec::with_capacity(symbols.len() + 2);
    let mut expected_fields = Vec::with_capacity(symbols.len() + 2);
    for (name, symbol, expression) in symbols {
        actual_fields.push((name, Json::b(symbol.strict_equals(eval(scope, expression)))));
        expected_fields.push((name, Json::b(true)));
    }
    let repeated = v8::Symbol::get_async_iterator(scope)
        .strict_equals(v8::Symbol::get_async_iterator(scope).into());
    let all_distinct = symbols.iter().enumerate().all(|(index, (_, symbol, _))| {
        symbols[index + 1..]
            .iter()
            .all(|(_, other, _)| !symbol.strict_equals((*other).into()))
    });
    actual_fields.push(("repeated_stable", Json::b(repeated)));
    actual_fields.push(("all_distinct", Json::b(all_distinct)));
    expected_fields.push(("repeated_stable", Json::b(true)));
    expected_fields.push(("all_distinct", Json::b(true)));

    expect_eq(
        "runtime-values-residual/symbol/all_well_known",
        Json::obj(expected_fields),
        Json::obj(actual_fields),
    )
}

fn private_for_api_some_names() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let fresh_none = v8::Private::new(scope, None);
    let empty = v8::String::empty(scope);
    let empty_a = v8::Private::for_api(scope, Some(empty));
    let empty_b = v8::Private::for_api(scope, Some(empty));
    let named_text = v8::String::new(scope, "named").unwrap();
    let named_a = v8::Private::for_api(scope, Some(named_text));
    let named_b = v8::Private::for_api(scope, Some(named_text));

    let empty_name = empty_a.name(scope);
    let named_name = named_a.name(scope);
    let named_name_text = named_name
        .to_string(scope)
        .unwrap()
        .to_rust_string_lossy(scope);

    let actual = Json::obj(vec![
        ("empty_idempotent", Json::b(empty_a == empty_b)),
        ("empty_name_is_string", Json::b(empty_name.is_string())),
        (
            "empty_name_length_zero",
            Json::b(empty_name.to_string(scope).unwrap().length() == 0),
        ),
        ("empty_differs_fresh_none", Json::b(empty_a != fresh_none)),
        ("named_idempotent", Json::b(named_a == named_b)),
        ("named_name", Json::s(&named_name_text)),
    ]);
    let expected = Json::obj(vec![
        ("empty_idempotent", Json::b(true)),
        ("empty_name_is_string", Json::b(true)),
        ("empty_name_length_zero", Json::b(true)),
        ("empty_differs_fresh_none", Json::b(true)),
        ("named_idempotent", Json::b(true)),
        ("named_name", Json::s("named")),
    ]);
    expect_eq(
        "runtime-values-residual/private/for_api_some_names",
        expected,
        actual,
    )
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    let outcomes = [symbol_all_well_known(), private_for_api_some_names()];
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
