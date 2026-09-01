//! Full public `v8::simdutf` conformance for pinned `v8` 152.2.0.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use v8::simdutf::{self, Base64Options, ErrorCode, LastChunkHandling, SimdUtfResult};

fn error_name(error: ErrorCode) -> &'static str {
    match error {
        ErrorCode::Success => "Success",
        ErrorCode::HeaderBits => "HeaderBits",
        ErrorCode::TooShort => "TooShort",
        ErrorCode::TooLong => "TooLong",
        ErrorCode::Overlong => "Overlong",
        ErrorCode::TooLarge => "TooLarge",
        ErrorCode::Surrogate => "Surrogate",
        ErrorCode::InvalidBase64Character => "InvalidBase64Character",
        ErrorCode::Base64InputRemainder => "Base64InputRemainder",
        ErrorCode::Base64ExtraBits => "Base64ExtraBits",
        ErrorCode::OutputBufferTooSmall => "OutputBufferTooSmall",
        ErrorCode::Other => "Other",
    }
}

fn result_json(result: SimdUtfResult) -> Json {
    Json::obj(vec![
        ("error", Json::s(error_name(result.error))),
        ("code", Json::i(result.error as i32 as i64)),
        ("count", Json::i(result.count as i64)),
        ("ok", Json::b(result.is_ok())),
    ])
}

fn bytes(values: &[u8]) -> Json {
    Json::arr(
        values
            .iter()
            .map(|value| Json::i(i64::from(*value)))
            .collect(),
    )
}

fn units16(values: &[u16]) -> Json {
    Json::arr(
        values
            .iter()
            .map(|value| Json::i(i64::from(*value)))
            .collect(),
    )
}

fn units32(values: &[u32]) -> Json {
    Json::arr(
        values
            .iter()
            .map(|value| Json::i(i64::from(*value)))
            .collect(),
    )
}

fn validation() -> Vec<CheckOutcome> {
    let utf8_cases: &[(&str, &[u8])] = &[
        ("empty", b""),
        ("valid", "Aé€😀".as_bytes()),
        ("continuation", &[0x80]),
        ("header_bits", &[0xff]),
        ("short", &[0xe2, 0x82]),
        ("long", &[0xc2, 0xa2, 0x80]),
        ("overlong", &[0xc0, 0x80]),
        ("large", &[0xf4, 0x90, 0x80, 0x80]),
        ("surrogate", &[0xed, 0xa0, 0x80]),
    ];
    let utf8 = utf8_cases
        .iter()
        .map(|(name, input)| {
            Json::obj(vec![
                ("case", Json::s(name)),
                ("valid", Json::b(simdutf::validate_utf8(input))),
                (
                    "result",
                    result_json(simdutf::validate_utf8_with_errors(input)),
                ),
            ])
        })
        .collect();
    let ascii_cases: &[(&str, &[u8])] =
        &[("empty", b""), ("valid", b"ASCII"), ("invalid", b"A\x80B")];
    let ascii = ascii_cases
        .iter()
        .map(|(name, input)| {
            Json::obj(vec![
                ("case", Json::s(name)),
                ("valid", Json::b(simdutf::validate_ascii(input))),
                (
                    "result",
                    result_json(simdutf::validate_ascii_with_errors(input)),
                ),
            ])
        })
        .collect();
    let utf16le_cases: &[(&str, &[u16])] = &[
        ("empty", &[]),
        ("valid", &[0x41, 0xd83d, 0xde00]),
        ("high_alone", &[0x41, 0xd800]),
        ("low_alone", &[0x41, 0xdc00]),
    ];
    let utf16le = utf16le_cases
        .iter()
        .map(|(name, input)| {
            Json::obj(vec![
                ("case", Json::s(name)),
                ("valid", Json::b(simdutf::validate_utf16le(input))),
                (
                    "result",
                    result_json(simdutf::validate_utf16le_with_errors(input)),
                ),
            ])
        })
        .collect();
    let utf16be_cases: &[(&str, &[u16])] = &[
        ("empty", &[]),
        ("valid", &[0x4100, 0x3dd8, 0x00de]),
        ("high_alone", &[0x4100, 0x00d8]),
        ("low_alone", &[0x4100, 0x00dc]),
    ];
    let utf16be = utf16be_cases
        .iter()
        .map(|(name, input)| {
            Json::obj(vec![
                ("case", Json::s(name)),
                ("valid", Json::b(simdutf::validate_utf16be(input))),
                (
                    "result",
                    result_json(simdutf::validate_utf16be_with_errors(input)),
                ),
            ])
        })
        .collect();
    let utf32_cases: &[(&str, &[u32])] = &[
        ("empty", &[]),
        ("valid", &[0x41, 0x20ac, 0x1f600]),
        ("surrogate", &[0x41, 0xd800]),
        ("large", &[0x41, 0x110000]),
    ];
    let utf32 = utf32_cases
        .iter()
        .map(|(name, input)| {
            Json::obj(vec![
                ("case", Json::s(name)),
                ("valid", Json::b(simdutf::validate_utf32(input))),
                (
                    "result",
                    result_json(simdutf::validate_utf32_with_errors(input)),
                ),
            ])
        })
        .collect();
    vec![pass(
        "simdutf/validation",
        Json::obj(vec![
            ("utf8", Json::arr(utf8)),
            ("ascii", Json::arr(ascii)),
            ("utf16le", Json::arr(utf16le)),
            ("utf16be", Json::arr(utf16be)),
            ("utf32", Json::arr(utf32)),
        ]),
    )]
}

fn unicode_conversions() -> Vec<CheckOutcome> {
    let utf8 = "Aé€😀".as_bytes();
    let utf16le = [0x41, 0xe9, 0x20ac, 0xd83d, 0xde00];
    let utf16be = [0x4100, 0xe900, 0xac20, 0x3dd8, 0x00de];
    let utf32 = [0x41, 0xe9, 0x20ac, 0x1f600];

    let mut to_le = vec![0xdead; utf8.len() + 2];
    let le_count = unsafe { simdutf::convert_utf8_to_utf16le(utf8, &mut to_le) };
    let mut to_le_errors = vec![0xdead; utf8.len() + 2];
    let le_result =
        unsafe { simdutf::convert_utf8_to_utf16le_with_errors(utf8, &mut to_le_errors) };
    let mut to_le_valid = vec![0xdead; utf8.len() + 2];
    let le_valid_count = unsafe { simdutf::convert_valid_utf8_to_utf16le(utf8, &mut to_le_valid) };

    let mut from_le = vec![0xcc; utf16le.len() * 3 + 2];
    let from_le_count = unsafe { simdutf::convert_utf16le_to_utf8(&utf16le, &mut from_le) };
    let mut from_le_errors = vec![0xcc; utf16le.len() * 3 + 2];
    let from_le_result =
        unsafe { simdutf::convert_utf16le_to_utf8_with_errors(&utf16le, &mut from_le_errors) };
    let mut from_le_valid = vec![0xcc; utf16le.len() * 3 + 2];
    let from_le_valid_count =
        unsafe { simdutf::convert_valid_utf16le_to_utf8(&utf16le, &mut from_le_valid) };

    let mut to_be = vec![0xdead; utf8.len() + 2];
    let to_be_count = unsafe { simdutf::convert_utf8_to_utf16be(utf8, &mut to_be) };
    let mut from_be = vec![0xcc; utf16be.len() * 3 + 2];
    let from_be_count = unsafe { simdutf::convert_utf16be_to_utf8(&utf16be, &mut from_be) };

    let mut to32 = vec![0xdeadbeef; utf8.len() + 2];
    let to32_count = unsafe { simdutf::convert_utf8_to_utf32(utf8, &mut to32) };
    let mut from32 = vec![0xcc; utf32.len() * 4 + 2];
    let from32_count = unsafe { simdutf::convert_utf32_to_utf8(&utf32, &mut from32) };

    let bad_utf8 = [b'A', 0xe2, 0x82];
    let mut bad_le = [0xdead; 5];
    let bad_le_count = unsafe { simdutf::convert_utf8_to_utf16le(&bad_utf8, &mut bad_le) };
    let mut bad_le_errors = [0xdead; 5];
    let bad_le_result =
        unsafe { simdutf::convert_utf8_to_utf16le_with_errors(&bad_utf8, &mut bad_le_errors) };
    let bad_utf16 = [0x41, 0xd800];
    let mut bad_utf8_out = [0xcc; 8];
    let bad_from_le_count =
        unsafe { simdutf::convert_utf16le_to_utf8(&bad_utf16, &mut bad_utf8_out) };
    let mut bad_utf8_errors = [0xcc; 8];
    let bad_from_le_result =
        unsafe { simdutf::convert_utf16le_to_utf8_with_errors(&bad_utf16, &mut bad_utf8_errors) };
    let mut bad_be_out = [0xcc; 8];
    let bad_from_be_count =
        unsafe { simdutf::convert_utf16be_to_utf8(&[0x4100, 0x00d8], &mut bad_be_out) };
    let mut bad_to_be = [0xdead; 5];
    let bad_to_be_count = unsafe { simdutf::convert_utf8_to_utf16be(&bad_utf8, &mut bad_to_be) };
    let mut bad_to32 = [0xdeadbeef; 5];
    let bad_to32_count = unsafe { simdutf::convert_utf8_to_utf32(&bad_utf8, &mut bad_to32) };
    let mut bad32_out = [0xcc; 8];
    let bad_from32_count =
        unsafe { simdutf::convert_utf32_to_utf8(&[0x41, 0x110000], &mut bad32_out) };

    vec![pass(
        "simdutf/unicode_conversions",
        Json::obj(vec![
            (
                "utf8_to_utf16le",
                Json::obj(vec![
                    ("count", Json::i(le_count as i64)),
                    ("output", units16(&to_le[..le_count])),
                    (
                        "tail_preserved",
                        Json::b(to_le[le_count..].iter().all(|v| *v == 0xdead)),
                    ),
                    ("with_errors", result_json(le_result)),
                    (
                        "with_errors_output",
                        units16(&to_le_errors[..le_result.count]),
                    ),
                    ("valid_count", Json::i(le_valid_count as i64)),
                    ("valid_output", units16(&to_le_valid[..le_valid_count])),
                ]),
            ),
            (
                "utf16le_to_utf8",
                Json::obj(vec![
                    ("count", Json::i(from_le_count as i64)),
                    ("output", bytes(&from_le[..from_le_count])),
                    (
                        "tail_preserved",
                        Json::b(from_le[from_le_count..].iter().all(|v| *v == 0xcc)),
                    ),
                    ("with_errors", result_json(from_le_result)),
                    (
                        "with_errors_output",
                        bytes(&from_le_errors[..from_le_result.count]),
                    ),
                    ("valid_count", Json::i(from_le_valid_count as i64)),
                    ("valid_output", bytes(&from_le_valid[..from_le_valid_count])),
                ]),
            ),
            (
                "utf8_utf16be",
                Json::obj(vec![
                    ("to_count", Json::i(to_be_count as i64)),
                    ("to_output", units16(&to_be[..to_be_count])),
                    ("from_count", Json::i(from_be_count as i64)),
                    ("from_output", bytes(&from_be[..from_be_count])),
                ]),
            ),
            (
                "utf8_utf32",
                Json::obj(vec![
                    ("to_count", Json::i(to32_count as i64)),
                    ("to_output", units32(&to32[..to32_count])),
                    ("from_count", Json::i(from32_count as i64)),
                    ("from_output", bytes(&from32[..from32_count])),
                ]),
            ),
            (
                "malformed",
                Json::obj(vec![
                    ("utf8_to_le_count", Json::i(bad_le_count as i64)),
                    ("utf8_to_le_result", result_json(bad_le_result)),
                    ("le_to_utf8_count", Json::i(bad_from_le_count as i64)),
                    ("le_to_utf8_result", result_json(bad_from_le_result)),
                    ("be_to_utf8_count", Json::i(bad_from_be_count as i64)),
                    ("utf8_to_be_count", Json::i(bad_to_be_count as i64)),
                    ("utf8_to_utf32_count", Json::i(bad_to32_count as i64)),
                    ("utf32_to_utf8_count", Json::i(bad_from32_count as i64)),
                ]),
            ),
        ]),
    )]
}

fn latin1_conversions() -> Vec<CheckOutcome> {
    let latin1 = [0x41, 0xe9, 0xff];
    let utf8 = [0x41, 0xc3, 0xa9, 0xc3, 0xbf];
    let utf16 = [0x41, 0xe9, 0xff];
    let mut to_latin1 = [0xcc; 7];
    let to_count = unsafe { simdutf::convert_utf8_to_latin1(&utf8, &mut to_latin1) };
    let mut to_latin1_errors = [0xcc; 7];
    let to_result =
        unsafe { simdutf::convert_utf8_to_latin1_with_errors(&utf8, &mut to_latin1_errors) };
    let mut to_latin1_valid = [0xcc; 7];
    let to_valid_count =
        unsafe { simdutf::convert_valid_utf8_to_latin1(&utf8, &mut to_latin1_valid) };
    let mut to_utf8 = [0xcc; 8];
    let to_utf8_count = unsafe { simdutf::convert_latin1_to_utf8(&latin1, &mut to_utf8) };
    let mut to_utf16 = [0xdead; 5];
    let to_utf16_count = unsafe { simdutf::convert_latin1_to_utf16le(&latin1, &mut to_utf16) };
    let mut from_utf16 = [0xcc; 5];
    let from_utf16_count = unsafe { simdutf::convert_utf16le_to_latin1(&utf16, &mut from_utf16) };

    let outside = "A€".as_bytes();
    let mut outside_out = [0xcc; 5];
    let outside_count = unsafe { simdutf::convert_utf8_to_latin1(outside, &mut outside_out) };
    let mut outside_errors = [0xcc; 5];
    let outside_result =
        unsafe { simdutf::convert_utf8_to_latin1_with_errors(outside, &mut outside_errors) };

    vec![pass(
        "simdutf/latin1_conversions",
        Json::obj(vec![
            ("utf8_to_count", Json::i(to_count as i64)),
            ("utf8_to_output", bytes(&to_latin1[..to_count])),
            ("utf8_to_result", result_json(to_result)),
            (
                "utf8_to_errors_output",
                bytes(&to_latin1_errors[..to_result.count]),
            ),
            ("valid_count", Json::i(to_valid_count as i64)),
            ("valid_output", bytes(&to_latin1_valid[..to_valid_count])),
            ("latin1_to_utf8_count", Json::i(to_utf8_count as i64)),
            ("latin1_to_utf8", bytes(&to_utf8[..to_utf8_count])),
            ("latin1_to_utf16_count", Json::i(to_utf16_count as i64)),
            ("latin1_to_utf16", units16(&to_utf16[..to_utf16_count])),
            ("utf16_to_latin1_count", Json::i(from_utf16_count as i64)),
            ("utf16_to_latin1", bytes(&from_utf16[..from_utf16_count])),
            ("outside_count", Json::i(outside_count as i64)),
            ("outside_result", result_json(outside_result)),
        ]),
    )]
}

fn lengths_counts_detection() -> Vec<CheckOutcome> {
    let utf8 = "Aé€😀".as_bytes();
    let utf16le = [0x41, 0xe9, 0x20ac, 0xd83d, 0xde00];
    let utf16be = [0x4100, 0xe900, 0xac20, 0x3dd8, 0x00de];
    let utf32 = [0x41, 0xe9, 0x20ac, 0x1f600];
    let latin1 = [0x41, 0xe9, 0xff];
    let detection_inputs: &[(&str, &[u8])] = &[
        ("empty", b""),
        ("ascii", b"ABC"),
        ("utf8", "€".as_bytes()),
        ("utf16le_bom", &[0xff, 0xfe, 0x41, 0x00]),
        ("utf16be_bom", &[0xfe, 0xff, 0x00, 0x41]),
        (
            "utf32le_bom",
            &[0xff, 0xfe, 0x00, 0x00, 0x41, 0x00, 0x00, 0x00],
        ),
        (
            "utf32be_bom",
            &[0x00, 0x00, 0xfe, 0xff, 0x00, 0x00, 0x00, 0x41],
        ),
    ];
    let detection = detection_inputs
        .iter()
        .map(|(name, input)| {
            let mask = simdutf::detect_encodings(input);
            Json::obj(vec![
                ("case", Json::s(name)),
                ("mask", Json::i(i64::from(mask))),
                ("utf8", Json::b(mask & simdutf::encoding::UTF8 != 0)),
                ("utf16le", Json::b(mask & simdutf::encoding::UTF16_LE != 0)),
                ("utf16be", Json::b(mask & simdutf::encoding::UTF16_BE != 0)),
                ("utf32le", Json::b(mask & simdutf::encoding::UTF32_LE != 0)),
                ("utf32be", Json::b(mask & simdutf::encoding::UTF32_BE != 0)),
                ("latin1", Json::b(mask & simdutf::encoding::LATIN1 != 0)),
            ])
        })
        .collect();
    vec![pass(
        "simdutf/lengths_counts_detection",
        Json::obj(vec![
            (
                "lengths",
                Json::obj(vec![
                    (
                        "utf8_from_utf16le",
                        Json::i(simdutf::utf8_length_from_utf16le(&utf16le) as i64),
                    ),
                    (
                        "utf8_from_utf16be",
                        Json::i(simdutf::utf8_length_from_utf16be(&utf16be) as i64),
                    ),
                    (
                        "utf16_from_utf8",
                        Json::i(simdutf::utf16_length_from_utf8(utf8) as i64),
                    ),
                    (
                        "utf8_from_latin1",
                        Json::i(simdutf::utf8_length_from_latin1(&latin1) as i64),
                    ),
                    (
                        "latin1_from_utf8",
                        Json::i(simdutf::latin1_length_from_utf8(&[0x41, 0xc3, 0xa9]) as i64),
                    ),
                    (
                        "utf32_from_utf8",
                        Json::i(simdutf::utf32_length_from_utf8(utf8) as i64),
                    ),
                    (
                        "utf8_from_utf32",
                        Json::i(simdutf::utf8_length_from_utf32(&utf32) as i64),
                    ),
                    (
                        "utf16_from_utf32",
                        Json::i(simdutf::utf16_length_from_utf32(&utf32) as i64),
                    ),
                    (
                        "utf32_from_utf16le",
                        Json::i(simdutf::utf32_length_from_utf16le(&utf16le) as i64),
                    ),
                ]),
            ),
            (
                "empty_lengths",
                Json::arr(vec![
                    Json::i(simdutf::utf8_length_from_utf16le(&[]) as i64),
                    Json::i(simdutf::utf8_length_from_utf16be(&[]) as i64),
                    Json::i(simdutf::utf16_length_from_utf8(&[]) as i64),
                    Json::i(simdutf::utf8_length_from_latin1(&[]) as i64),
                    Json::i(simdutf::latin1_length_from_utf8(&[]) as i64),
                    Json::i(simdutf::utf32_length_from_utf8(&[]) as i64),
                    Json::i(simdutf::utf8_length_from_utf32(&[]) as i64),
                    Json::i(simdutf::utf16_length_from_utf32(&[]) as i64),
                    Json::i(simdutf::utf32_length_from_utf16le(&[]) as i64),
                ]),
            ),
            (
                "counts",
                Json::obj(vec![
                    ("utf8", Json::i(simdutf::count_utf8(utf8) as i64)),
                    ("utf16le", Json::i(simdutf::count_utf16le(&utf16le) as i64)),
                    ("utf16be", Json::i(simdutf::count_utf16be(&utf16be) as i64)),
                    ("empty_utf8", Json::i(simdutf::count_utf8(&[]) as i64)),
                    ("empty_utf16le", Json::i(simdutf::count_utf16le(&[]) as i64)),
                    ("empty_utf16be", Json::i(simdutf::count_utf16be(&[]) as i64)),
                ]),
            ),
            ("detection", Json::arr(detection)),
            (
                "encoding_constants",
                Json::arr(vec![
                    Json::i(i64::from(simdutf::encoding::UTF8)),
                    Json::i(i64::from(simdutf::encoding::UTF16_LE)),
                    Json::i(i64::from(simdutf::encoding::UTF16_BE)),
                    Json::i(i64::from(simdutf::encoding::UTF32_LE)),
                    Json::i(i64::from(simdutf::encoding::UTF32_BE)),
                    Json::i(i64::from(simdutf::encoding::LATIN1)),
                ]),
            ),
        ]),
    )]
}

fn base64() -> Vec<CheckOutcome> {
    let binary = [0xfb, 0xff];
    let options = [
        ("default", Base64Options::Default),
        ("url", Base64Options::Url),
        ("default_no_padding", Base64Options::DefaultNoPadding),
        ("url_with_padding", Base64Options::UrlWithPadding),
    ];
    let encoded = options
        .iter()
        .map(|(name, option)| {
            let len = simdutf::base64_length_from_binary(binary.len(), *option);
            let mut output = vec![0xcc; len + 2];
            let count = unsafe { simdutf::binary_to_base64(&binary, &mut output, *option) };
            Json::obj(vec![
                ("option", Json::s(name)),
                ("length", Json::i(len as i64)),
                ("count", Json::i(count as i64)),
                (
                    "text",
                    Json::s(std::str::from_utf8(&output[..count]).unwrap()),
                ),
                (
                    "tail_preserved",
                    Json::b(output[count..].iter().all(|value| *value == 0xcc)),
                ),
            ])
        })
        .collect();
    let lengths = options
        .iter()
        .map(|(name, option)| {
            Json::obj(vec![
                ("option", Json::s(name)),
                (
                    "n0_to_n4",
                    Json::arr(
                        (0..=4)
                            .map(|length| {
                                Json::i(simdutf::base64_length_from_binary(length, *option) as i64)
                            })
                            .collect(),
                    ),
                ),
                (
                    "usize_max_minus_one",
                    Json::s(
                        &simdutf::base64_length_from_binary(usize::MAX - 1, *option).to_string(),
                    ),
                ),
                (
                    "usize_max",
                    Json::s(&simdutf::base64_length_from_binary(usize::MAX, *option).to_string()),
                ),
            ])
        })
        .collect();
    let decode_cases: &[(&str, &[u8], Base64Options, LastChunkHandling)] = &[
        (
            "empty",
            b"",
            Base64Options::Default,
            LastChunkHandling::Loose,
        ),
        (
            "default_padded",
            b"+/8=",
            Base64Options::Default,
            LastChunkHandling::Strict,
        ),
        (
            "url_unpadded",
            b"-_8",
            Base64Options::Url,
            LastChunkHandling::Loose,
        ),
        (
            "default_no_padding_strict",
            b"+/8",
            Base64Options::DefaultNoPadding,
            LastChunkHandling::Strict,
        ),
        (
            "default_no_padding_loose",
            b"+/8",
            Base64Options::DefaultNoPadding,
            LastChunkHandling::Loose,
        ),
        (
            "url_with_padding",
            b"-_8=",
            Base64Options::UrlWithPadding,
            LastChunkHandling::Strict,
        ),
        (
            "default_rejects_url_alphabet",
            b"-_8=",
            Base64Options::Default,
            LastChunkHandling::Strict,
        ),
        (
            "url_accepts_standard_alphabet",
            b"+/8=",
            Base64Options::Url,
            LastChunkHandling::Strict,
        ),
        (
            "whitespace",
            b" T W\nE= ",
            Base64Options::Default,
            LastChunkHandling::Strict,
        ),
        (
            "invalid_character",
            b"TW$u",
            Base64Options::Default,
            LastChunkHandling::Loose,
        ),
        (
            "partial_loose",
            b"TQ",
            Base64Options::Default,
            LastChunkHandling::Loose,
        ),
        (
            "partial_strict",
            b"TQ",
            Base64Options::Default,
            LastChunkHandling::Strict,
        ),
        (
            "partial_stop",
            b"TQ",
            Base64Options::Default,
            LastChunkHandling::StopBeforePartial,
        ),
        (
            "partial_full_only",
            b"TQ",
            Base64Options::Default,
            LastChunkHandling::OnlyFullChunks,
        ),
        (
            "extra_bits",
            b"TR==",
            Base64Options::Default,
            LastChunkHandling::Strict,
        ),
    ];
    let decoded = decode_cases
        .iter()
        .map(|(name, input, option, last)| {
            let max = simdutf::maximal_binary_length_from_base64(input);
            let mut output = vec![0xcc; max + 2];
            let result = unsafe { simdutf::base64_to_binary(input, &mut output, *option, *last) };
            let output_prefix = if result.is_ok() {
                bytes(&output[..result.count])
            } else {
                Json::Null
            };
            Json::obj(vec![
                ("case", Json::s(name)),
                ("max", Json::i(max as i64)),
                ("result", result_json(result)),
                ("output", output_prefix),
                (
                    "guard_preserved",
                    Json::b(output[max..].iter().all(|value| *value == 0xcc)),
                ),
            ])
        })
        .collect();
    vec![pass(
        "simdutf/base64",
        Json::obj(vec![
            ("encoded", Json::arr(encoded)),
            ("lengths", Json::arr(lengths)),
            ("decoded", Json::arr(decoded)),
        ]),
    )]
}

fn main() -> std::process::ExitCode {
    let mut checks = validation();
    checks.extend(unicode_conversions());
    checks.extend(latin1_conversions());
    checks.extend(lengths_counts_detection());
    checks.extend(base64());
    let passed = checks.iter().filter(|check| check.passed()).count();
    for check in &checks {
        println!("{}", check.to_line());
    }
    println!(
        "{}",
        summary_line(checks.len(), passed, checks.len() - passed)
    );
    if passed == checks.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
