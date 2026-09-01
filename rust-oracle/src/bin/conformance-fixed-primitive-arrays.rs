//! `v8::FixedArray` and `v8::PrimitiveArray` characterization for the pinned
//! `v8` 152.2.0 oracle.
//!
//! `FixedArray` is a read-only metadata container in the Rust API; there is no
//! public constructor or setter. `PrimitiveArray` is constructible and mutable,
//! but its index-taking methods do not perform Rust-side bounds checks. Fatal
//! index and length boundaries are therefore kept in the companion subprocess
//! probe rather than contaminating this deterministic success fixture.

use std::convert::TryFrom as _;
use std::io::Write as _;
use std::process::ExitCode;

use oracle::json::Json;
use oracle::report::{expect_eq, summary_line, CheckOutcome};

fn primitive_kind(value: v8::Local<'_, v8::Primitive>) -> &'static str {
    if value.is_undefined() {
        "undefined"
    } else if value.is_null() {
        "null"
    } else if value.is_boolean() {
        "boolean"
    } else if value.is_string() {
        "string"
    } else if value.is_symbol() {
        "symbol"
    } else if value.is_big_int() {
        "bigint"
    } else if value.is_number() {
        "number"
    } else {
        "unknown"
    }
}

fn primitive_text(scope: &mut v8::PinScope<'_, '_>, value: v8::Local<'_, v8::Primitive>) -> String {
    if value.is_symbol() {
        let symbol = v8::Local::<v8::Symbol>::try_from(value).unwrap();
        symbol.description(scope).to_rust_string_lossy(scope)
    } else {
        value.to_rust_string_lossy(scope)
    }
}

fn origin<'s>(scope: &v8::PinScope<'s, '_>) -> v8::ScriptOrigin<'s> {
    let resource: v8::Local<v8::Value> = v8::String::new(scope, "arrays.mjs").unwrap().into();
    v8::ScriptOrigin::new(
        scope, resource, 0, 0, false, -1, None, false, false, true, None,
    )
}

fn compile_module<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Module> {
    let source = v8::String::new(scope, source).unwrap();
    let origin = origin(scope);
    let mut source = v8::script_compiler::Source::new(source, Some(&origin));
    v8::script_compiler::compile_module(scope, &mut source).unwrap()
}

fn primitive_empty_and_defaults() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let empty = v8::PrimitiveArray::new(scope, 0);
    let defaults = v8::PrimitiveArray::new(scope, 3);
    let kinds = (0..defaults.length())
        .map(|index| Json::s(primitive_kind(defaults.get(scope, index))))
        .collect();

    let actual = Json::obj(vec![
        ("empty_length", Json::i(empty.length() as i64)),
        ("default_length", Json::i(defaults.length() as i64)),
        ("default_kinds", Json::arr(kinds)),
    ]);
    let expected = Json::obj(vec![
        ("empty_length", Json::i(0)),
        ("default_length", Json::i(3)),
        (
            "default_kinds",
            Json::arr(vec![
                Json::s("undefined"),
                Json::s("undefined"),
                Json::s("undefined"),
            ]),
        ),
    ]);
    expect_eq(
        "fixed-primitive-arrays/primitive_empty_and_defaults",
        expected,
        actual,
    )
}

fn primitive_supported_kinds() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let array = v8::PrimitiveArray::new(scope, 8);
    let symbol = v8::Symbol::new(scope, Some(v8::String::new(scope, "token").unwrap()));
    let values: [v8::Local<'_, v8::Primitive>; 8] = [
        v8::undefined(scope),
        v8::null(scope),
        v8::Boolean::new(scope, true).into(),
        v8::String::new(scope, "hello").unwrap().into(),
        symbol.into(),
        v8::Number::new(scope, 2.5).into(),
        v8::Integer::new(scope, -7).into(),
        v8::BigInt::new_from_i64(scope, 9_007_199_254_740_993).into(),
    ];
    for (index, value) in values.into_iter().enumerate() {
        array.set(scope, index, value);
    }

    let observed = (0..array.length())
        .map(|index| {
            let value = array.get(scope, index);
            Json::obj(vec![
                ("kind", Json::s(primitive_kind(value))),
                ("text", Json::s(&primitive_text(scope, value))),
            ])
        })
        .collect();
    let symbol_roundtrip = v8::Local::<v8::Symbol>::try_from(array.get(scope, 4)).unwrap();

    let actual = Json::obj(vec![
        ("length", Json::i(array.length() as i64)),
        ("values", Json::arr(observed)),
        ("symbol_identity", Json::b(symbol_roundtrip == symbol)),
    ]);
    let expected = Json::obj(vec![
        ("length", Json::i(8)),
        (
            "values",
            Json::arr(vec![
                Json::obj(vec![
                    ("kind", Json::s("undefined")),
                    ("text", Json::s("undefined")),
                ]),
                Json::obj(vec![("kind", Json::s("null")), ("text", Json::s("null"))]),
                Json::obj(vec![
                    ("kind", Json::s("boolean")),
                    ("text", Json::s("true")),
                ]),
                Json::obj(vec![
                    ("kind", Json::s("string")),
                    ("text", Json::s("hello")),
                ]),
                Json::obj(vec![
                    ("kind", Json::s("symbol")),
                    ("text", Json::s("token")),
                ]),
                Json::obj(vec![("kind", Json::s("number")), ("text", Json::s("2.5"))]),
                Json::obj(vec![("kind", Json::s("number")), ("text", Json::s("-7"))]),
                Json::obj(vec![
                    ("kind", Json::s("bigint")),
                    ("text", Json::s("9007199254740993")),
                ]),
            ]),
        ),
        ("symbol_identity", Json::b(true)),
    ]);
    expect_eq(
        "fixed-primitive-arrays/primitive_supported_kinds",
        expected,
        actual,
    )
}

fn primitive_overwrite_and_context_independence() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);

    let held = {
        let first_context = v8::Context::new(scope, Default::default());
        let first_scope = &mut v8::ContextScope::new(scope, first_context);
        let array = v8::PrimitiveArray::new(first_scope, 2);
        array.set(
            first_scope,
            0,
            v8::String::new(first_scope, "from-first-context")
                .unwrap()
                .into(),
        );
        array.set(first_scope, 1, v8::Integer::new(first_scope, 1).into());
        array.set(first_scope, 1, v8::Integer::new(first_scope, 2).into());
        v8::Global::new(first_scope, array)
    };

    let second_context = v8::Context::new(scope, Default::default());
    let second_scope = &mut v8::ContextScope::new(scope, second_context);
    let reopened = v8::Local::new(second_scope, &held);
    let first_slot = reopened.get(second_scope, 0);
    let first_slot = primitive_text(second_scope, first_slot);
    let overwritten_slot = reopened.get(second_scope, 1);
    let overwritten_slot = primitive_text(second_scope, overwritten_slot);
    let actual = Json::obj(vec![
        ("length", Json::i(reopened.length() as i64)),
        ("first_slot", Json::s(&first_slot)),
        ("overwritten_slot", Json::s(&overwritten_slot)),
    ]);
    let expected = Json::obj(vec![
        ("length", Json::i(2)),
        ("first_slot", Json::s("from-first-context")),
        ("overwritten_slot", Json::s("2")),
    ]);
    expect_eq(
        "fixed-primitive-arrays/primitive_overwrite_and_context_independence",
        expected,
        actual,
    )
}

fn primitive_length_conversion() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    // `PrimitiveArray::new` accepts usize but the binding uses `length as int`.
    // These allocations are small after truncation and therefore safely expose
    // the wrapper's 64-bit conversion behavior without attempting a huge V8
    // allocation.
    let wraps_to_zero = u32::MAX as usize + 1;
    let wraps_to_one = u32::MAX as usize + 2;
    let zero = v8::PrimitiveArray::new(scope, wraps_to_zero);
    let one = v8::PrimitiveArray::new(scope, wraps_to_one);
    let actual = Json::obj(vec![
        ("requested_zero", Json::i(wraps_to_zero as i64)),
        ("observed_zero", Json::i(zero.length() as i64)),
        ("requested_one", Json::i(wraps_to_one as i64)),
        ("observed_one", Json::i(one.length() as i64)),
        (
            "wrapped_slot_default",
            Json::s(primitive_kind(one.get(scope, 0))),
        ),
    ]);
    let expected = Json::obj(vec![
        ("requested_zero", Json::i(4_294_967_296)),
        ("observed_zero", Json::i(0)),
        ("requested_one", Json::i(4_294_967_297)),
        ("observed_one", Json::i(1)),
        ("wrapped_slot_default", Json::s("undefined")),
    ]);
    expect_eq(
        "fixed-primitive-arrays/primitive_length_conversion",
        expected,
        actual,
    )
}

fn fixed_empty_and_safe_bounds() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let module = compile_module(scope, "export const answer = 42;");
    let fixed = module.get_module_requests();
    let actual = Json::obj(vec![
        ("is_fixed_array", Json::b(fixed.is_fixed_array())),
        ("length", Json::i(fixed.length() as i64)),
        ("get_zero_none", Json::b(fixed.get(scope, 0).is_none())),
        (
            "get_usize_max_none",
            Json::b(fixed.get(scope, usize::MAX).is_none()),
        ),
    ]);
    let expected = Json::obj(vec![
        ("is_fixed_array", Json::b(true)),
        ("length", Json::i(0)),
        ("get_zero_none", Json::b(true)),
        ("get_usize_max_none", Json::b(true)),
    ]);
    expect_eq(
        "fixed-primitive-arrays/fixed_empty_and_safe_bounds",
        expected,
        actual,
    )
}

fn fixed_data_kinds() -> CheckOutcome {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let module = compile_module(
        scope,
        "import value from './data.json' with { kind: 'fixture' }; export default value;",
    );
    let requests = module.get_module_requests();
    let request_data = requests.get(scope, 0).unwrap();
    let request = v8::Local::<v8::ModuleRequest>::try_from(request_data).unwrap();
    let attributes = request.get_import_attributes();
    let attribute_kinds = (0..attributes.length())
        .map(|index| {
            let data = attributes.get(scope, index).unwrap();
            if data.is_string() {
                "string"
            } else if data.is_number() {
                "number"
            } else {
                "other"
            }
        })
        .map(Json::s)
        .collect();
    let attribute_values = (0..attributes.length())
        .map(|index| {
            let data = attributes.get(scope, index).unwrap();
            let value = v8::Local::<v8::Value>::try_from(data).unwrap();
            Json::s(&value.to_rust_string_lossy(scope))
        })
        .collect();

    let actual = Json::obj(vec![
        ("request_count", Json::i(requests.length() as i64)),
        (
            "request_is_module_request",
            Json::b(request_data.is_module_request()),
        ),
        ("request_is_primitive", Json::b(request_data.is_primitive())),
        (
            "attributes_is_fixed_array",
            Json::b(attributes.is_fixed_array()),
        ),
        ("attribute_length", Json::i(attributes.length() as i64)),
        ("attribute_kinds", Json::arr(attribute_kinds)),
        ("attribute_values", Json::arr(attribute_values)),
        (
            "request_at_count_none",
            Json::b(requests.get(scope, requests.length()).is_none()),
        ),
        (
            "attribute_at_count_none",
            Json::b(attributes.get(scope, attributes.length()).is_none()),
        ),
    ]);
    let expected = Json::obj(vec![
        ("request_count", Json::i(1)),
        ("request_is_module_request", Json::b(true)),
        ("request_is_primitive", Json::b(false)),
        ("attributes_is_fixed_array", Json::b(true)),
        ("attribute_length", Json::i(3)),
        (
            "attribute_kinds",
            Json::arr(vec![
                Json::s("string"),
                Json::s("string"),
                Json::s("number"),
            ]),
        ),
        (
            "attribute_values",
            Json::arr(vec![Json::s("kind"), Json::s("fixture"), Json::s("39")]),
        ),
        ("request_at_count_none", Json::b(true)),
        ("attribute_at_count_none", Json::b(true)),
    ]);
    expect_eq("fixed-primitive-arrays/fixed_data_kinds", expected, actual)
}

fn main() -> ExitCode {
    oracle::ensure_v8();
    let outcomes = [
        primitive_empty_and_defaults(),
        primitive_supported_kinds(),
        primitive_overwrite_and_context_independence(),
        primitive_length_conversion(),
        fixed_empty_and_safe_bounds(),
        fixed_data_kinds(),
    ];
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    let mut stdout = std::io::stdout().lock();
    for outcome in &outcomes {
        writeln!(stdout, "{}", outcome.to_line()).unwrap();
    }
    writeln!(
        stdout,
        "{}",
        summary_line(outcomes.len(), passed, outcomes.len() - passed)
    )
    .unwrap();
    if passed == outcomes.len() {
        ExitCode::SUCCESS
    } else {
        ExitCode::FAILURE
    }
}
