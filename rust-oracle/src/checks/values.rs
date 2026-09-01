//! Primitive value construction and conversion checks.

use crate::checks::harness;
use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};

pub(crate) fn undefined() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let value = v8::undefined(scope);
    let actual = Json::obj(vec![
        ("is_undefined", Json::b(value.is_undefined())),
        (
            "is_null_or_undefined",
            Json::b(value.is_null_or_undefined()),
        ),
        (
            "to_string",
            Json::s(&harness::value_text(scope, value.into())),
        ),
    ]);
    let expected = Json::obj(vec![
        ("is_undefined", Json::b(true)),
        ("is_null_or_undefined", Json::b(true)),
        ("to_string", Json::s("undefined")),
    ]);
    vec![expect_eq("values/undefined", expected, actual)]
}

pub(crate) fn null() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let value = v8::null(scope);
    let actual = Json::obj(vec![
        ("is_null", Json::b(value.is_null())),
        (
            "is_null_or_undefined",
            Json::b(value.is_null_or_undefined()),
        ),
        (
            "to_string",
            Json::s(&harness::value_text(scope, value.into())),
        ),
    ]);
    let expected = Json::obj(vec![
        ("is_null", Json::b(true)),
        ("is_null_or_undefined", Json::b(true)),
        ("to_string", Json::s("null")),
    ]);
    vec![expect_eq("values/null", expected, actual)]
}

pub(crate) fn booleans() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let truthy = v8::Boolean::new(scope, true);
    let falsy = v8::Boolean::new(scope, false);
    let actual = Json::obj(vec![
        ("true_is_boolean", Json::b(truthy.is_boolean())),
        ("true_value", Json::b(truthy.boolean_value(scope))),
        (
            "true_to_string",
            Json::s(&harness::value_text(scope, truthy.into())),
        ),
        ("false_is_boolean", Json::b(falsy.is_boolean())),
        ("false_value", Json::b(falsy.boolean_value(scope))),
        (
            "false_to_string",
            Json::s(&harness::value_text(scope, falsy.into())),
        ),
    ]);
    let expected = Json::obj(vec![
        ("true_is_boolean", Json::b(true)),
        ("true_value", Json::b(true)),
        ("true_to_string", Json::s("true")),
        ("false_is_boolean", Json::b(true)),
        ("false_value", Json::b(false)),
        ("false_to_string", Json::s("false")),
    ]);
    vec![expect_eq("values/booleans", expected, actual)]
}

pub(crate) fn integers() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let negative = v8::Integer::new(scope, -42);
    let unsigned = v8::Integer::new_from_unsigned(scope, u32::MAX);
    let actual = Json::obj(vec![
        (
            "negative",
            Json::obj(vec![
                ("value", Json::i(negative.value())),
                ("is_int32", Json::b(negative.is_int32())),
                ("is_number", Json::b(negative.is_number())),
                (
                    "to_string",
                    Json::s(&harness::value_text(scope, negative.into())),
                ),
            ]),
        ),
        (
            "unsigned_max",
            Json::obj(vec![
                ("value", Json::i(unsigned.value())),
                ("is_uint32", Json::b(unsigned.is_uint32())),
                ("is_int32", Json::b(unsigned.is_int32())),
                (
                    "to_string",
                    Json::s(&harness::value_text(scope, unsigned.into())),
                ),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "negative",
            Json::obj(vec![
                ("value", Json::i(-42)),
                ("is_int32", Json::b(true)),
                ("is_number", Json::b(true)),
                ("to_string", Json::s("-42")),
            ]),
        ),
        (
            "unsigned_max",
            Json::obj(vec![
                ("value", Json::i(i64::from(u32::MAX))),
                ("is_uint32", Json::b(true)),
                ("is_int32", Json::b(false)),
                ("to_string", Json::s("4294967295")),
            ]),
        ),
    ]);
    vec![expect_eq("values/integers", expected, actual)]
}

pub(crate) fn number_f64() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let mut pairs = Vec::new();
    for sample in [2.5f64, -1234.5, 0.5] {
        let number = v8::Number::new(scope, sample);
        pairs.push(Json::obj(vec![
            ("value", Json::f(number.value())),
            ("is_number", Json::b(number.is_number())),
            (
                "to_string",
                Json::s(&harness::value_text(scope, number.into())),
            ),
        ]));
    }
    let expected = Json::arr(vec![
        Json::obj(vec![
            ("value", Json::f(2.5)),
            ("is_number", Json::b(true)),
            ("to_string", Json::s("2.5")),
        ]),
        Json::obj(vec![
            ("value", Json::f(-1234.5)),
            ("is_number", Json::b(true)),
            ("to_string", Json::s("-1234.5")),
        ]),
        Json::obj(vec![
            ("value", Json::f(0.5)),
            ("is_number", Json::b(true)),
            ("to_string", Json::s("0.5")),
        ]),
    ]);
    vec![expect_eq("values/number_f64", expected, Json::arr(pairs))]
}

pub(crate) fn number_special() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let nan = v8::Number::new(scope, f64::NAN);
    let infinity = v8::Number::new(scope, f64::INFINITY);
    let neg_infinity = v8::Number::new(scope, f64::NEG_INFINITY);
    let actual = Json::obj(vec![
        (
            "nan",
            Json::obj(vec![
                ("is_nan", Json::b(nan.value().is_nan())),
                (
                    "to_string",
                    Json::s(&harness::value_text(scope, nan.into())),
                ),
            ]),
        ),
        (
            "infinity",
            Json::obj(vec![
                ("is_infinite", Json::b(ninfinity_value(infinity.value()))),
                (
                    "to_string",
                    Json::s(&harness::value_text(scope, infinity.into())),
                ),
            ]),
        ),
        (
            "neg_infinity",
            Json::obj(vec![
                (
                    "is_infinite",
                    Json::b(ninfinity_value(neg_infinity.value())),
                ),
                (
                    "to_string",
                    Json::s(&harness::value_text(scope, neg_infinity.into())),
                ),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "nan",
            Json::obj(vec![
                ("is_nan", Json::b(true)),
                ("to_string", Json::s("NaN")),
            ]),
        ),
        (
            "infinity",
            Json::obj(vec![
                ("is_infinite", Json::b(true)),
                ("to_string", Json::s("Infinity")),
            ]),
        ),
        (
            "neg_infinity",
            Json::obj(vec![
                ("is_infinite", Json::b(true)),
                ("to_string", Json::s("-Infinity")),
            ]),
        ),
    ]);
    vec![expect_eq("values/number_special", expected, actual)]
}

fn ninfinity_value(v: f64) -> bool {
    v.is_infinite()
}

pub(crate) fn string_roundtrip() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ascii = v8::String::new(scope, "hello oracle").unwrap();
    let unicode = v8::String::new(scope, "h\u{e9}llo \u{1f980} gov8").unwrap();
    let empty = v8::String::new(scope, "").unwrap();
    let actual = Json::obj(vec![
        (
            "ascii",
            Json::obj(vec![
                ("length_utf16", Json::i(ascii.length() as i64)),
                ("utf8_length", Json::i(ascii.utf8_length(scope) as i64)),
                (
                    "roundtrip",
                    Json::b(ascii.to_rust_string_lossy(scope) == "hello oracle"),
                ),
            ]),
        ),
        (
            "unicode",
            Json::obj(vec![
                // "héllo 🦀 gov8": 13 UTF-16 code units (🦀 is a surrogate
                // pair), 16 UTF-8 bytes.
                ("length_utf16", Json::i(unicode.length() as i64)),
                ("utf8_length", Json::i(unicode.utf8_length(scope) as i64)),
                (
                    "roundtrip",
                    Json::b(unicode.to_rust_string_lossy(scope) == "h\u{e9}llo \u{1f980} gov8"),
                ),
            ]),
        ),
        (
            "empty",
            Json::obj(vec![
                ("length_utf16", Json::i(empty.length() as i64)),
                (
                    "roundtrip",
                    Json::b(empty.to_rust_string_lossy(scope).is_empty()),
                ),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "ascii",
            Json::obj(vec![
                ("length_utf16", Json::i(12)),
                ("utf8_length", Json::i(12)),
                ("roundtrip", Json::b(true)),
            ]),
        ),
        (
            "unicode",
            Json::obj(vec![
                ("length_utf16", Json::i(13)),
                ("utf8_length", Json::i(16)),
                ("roundtrip", Json::b(true)),
            ]),
        ),
        (
            "empty",
            Json::obj(vec![
                ("length_utf16", Json::i(0)),
                ("roundtrip", Json::b(true)),
            ]),
        ),
    ]);
    vec![expect_eq("values/string_roundtrip", expected, actual)]
}

pub(crate) fn value_to_string_conversions() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let samples: Vec<(&'static str, &str, v8::Local<v8::Value>)> = vec![
        (
            "number_1234567_125",
            "1234567.125",
            v8::Number::new(scope, 1234567.125).into(),
        ),
        (
            "integer_negative",
            "-987654",
            v8::Integer::new(scope, -987_654).into(),
        ),
        (
            "boolean_false",
            "false",
            v8::Boolean::new(scope, false).into(),
        ),
        (
            "bigint_2p53",
            "9007199254740992",
            v8::BigInt::new_from_i64(scope, 1i64 << 53).into(),
        ),
        (
            "string_abc",
            "abc",
            v8::String::new(scope, "abc").unwrap().into(),
        ),
        ("undefined", "undefined", v8::undefined(scope).into()),
        ("null", "null", v8::null(scope).into()),
    ];

    let mut observed = Vec::new();
    let mut expected = Vec::new();
    for (name, expected_text, value) in samples {
        let actual_text = harness::value_text(scope, value);
        observed.push((
            name,
            Json::obj(vec![
                ("expected", Json::s(expected_text)),
                ("actual", Json::s(&actual_text)),
            ]),
        ));
        expected.push((
            name,
            Json::obj(vec![
                ("expected", Json::s(expected_text)),
                ("actual", Json::s(expected_text)),
            ]),
        ));
    }

    vec![expect_eq(
        "values/value_to_string_conversions",
        Json::Object(expected),
        Json::Object(observed),
    )]
}

pub(crate) fn bigint_roundtrip() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let from_i64 = v8::BigInt::new_from_i64(scope, -1_234_567_890_123_456_789);
    let (i64_back, i64_lossless) = from_i64.i64_value();
    let from_u64 = v8::BigInt::new_from_u64(scope, u64::MAX);
    let (i64_of_u64max, u64max_lossless) = from_u64.i64_value();

    let actual = Json::obj(vec![
        (
            "from_i64",
            Json::obj(vec![
                ("value", Json::i(i64_back)),
                ("lossless", Json::b(i64_lossless)),
                (
                    "to_string",
                    Json::s(&harness::value_text(scope, from_i64.into())),
                ),
            ]),
        ),
        (
            "from_u64_max",
            Json::obj(vec![
                ("i64_value", Json::i(i64_of_u64max)),
                ("lossless", Json::b(u64max_lossless)),
                (
                    "to_string",
                    Json::s(&harness::value_text(scope, from_u64.into())),
                ),
            ]),
        ),
    ]);
    let expected = Json::obj(vec![
        (
            "from_i64",
            Json::obj(vec![
                ("value", Json::i(-1_234_567_890_123_456_789)),
                ("lossless", Json::b(true)),
                ("to_string", Json::s("-1234567890123456789")),
            ]),
        ),
        (
            "from_u64_max",
            Json::obj(vec![
                ("i64_value", Json::i(-1)),
                ("lossless", Json::b(false)),
                ("to_string", Json::s("18446744073709551615")),
            ]),
        ),
    ]);
    vec![expect_eq("values/bigint_roundtrip", expected, actual)]
}

pub(crate) fn script_number_formatting() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let source = concat!(
        "[ String(0.1), String(1 / 3), String(1e21), String(1e-7),",
        " String(-0), String(2 ** 53), String(100), String(0.5) ].join('|')"
    );
    let joined = harness::eval_text(scope, source).unwrap_or_default();
    let actual = Json::s(&joined);
    let expected = Json::s("0.1|0.3333333333333333|1e+21|1e-7|0|9007199254740992|100|0.5");
    vec![expect_eq(
        "values/script_number_formatting",
        expected,
        actual,
    )]
}
