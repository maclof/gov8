//! CRDTP core conversion and value API oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This slice is
//! isolate-free and intentionally excludes FrontendChannel, UberDispatcher,
//! DomainDispatcher, and fallthrough callback execution.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

fn hex(bytes: &[u8]) -> String {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        output.push(char::from(DIGITS[usize::from(byte >> 4)]));
        output.push(char::from(DIGITS[usize::from(byte & 0x0f)]));
    }
    output
}

fn json_text(bytes: &[u8]) -> Json {
    v8::crdtp::cbor_to_json(bytes).map_or(Json::Null, |json| {
        Json::s(std::str::from_utf8(&json).expect("CRDTP JSON is UTF-8"))
    })
}

fn conversion_success() -> CheckOutcome {
    let cases = [
        ("empty_object", r#"{}"#),
        (
            "protocol",
            r#" { "id" : 7, "method" : "Test.echo", "params" : { "text" : "a\u0000Ω", "items" : [1,true,null] } } "#,
        ),
    ];
    let observations = cases
        .into_iter()
        .map(|(case, input)| {
            let cbor = v8::crdtp::json_to_cbor(input.as_bytes()).expect("valid JSON");
            Json::obj(vec![
                ("case", Json::s(case)),
                ("cbor_len", Json::i(cbor.len() as i64)),
                ("cbor_hex", Json::s(&hex(&cbor))),
                ("round_trip", json_text(&cbor)),
            ])
        })
        .collect();
    pass("crdtp-core/conversion_success", Json::arr(observations))
}

fn conversion_failures() -> CheckOutcome {
    let valid =
        v8::crdtp::json_to_cbor(br#"{"id":1,"method":"Test.run","params":{"value":42}}"#).unwrap();
    let truncated_cbor = &valid[..valid.len() / 2];
    let json_cases: [(&str, &[u8]); 3] = [
        ("empty", b""),
        ("garbage", b"not json {{{"),
        ("truncated", br#"{"id":1,"method":"Test.run""#),
    ];
    let cbor_cases: [(&str, &[u8]); 3] = [
        ("empty", b""),
        ("garbage", &[0xff, 0xfe, 0x00]),
        ("truncated", truncated_cbor),
    ];
    pass(
        "crdtp-core/conversion_failures",
        Json::obj(vec![
            (
                "json_to_cbor",
                Json::arr(
                    json_cases
                        .into_iter()
                        .map(|(case, input)| {
                            let result = v8::crdtp::json_to_cbor(input);
                            Json::obj(vec![
                                ("case", Json::s(case)),
                                ("some", Json::b(result.is_some())),
                                (
                                    "len",
                                    result.map_or(Json::Null, |bytes| Json::i(bytes.len() as i64)),
                                ),
                            ])
                        })
                        .collect(),
                ),
            ),
            (
                "cbor_to_json",
                Json::arr(
                    cbor_cases
                        .into_iter()
                        .map(|(case, input)| {
                            let result = v8::crdtp::cbor_to_json(input);
                            Json::obj(vec![
                                ("case", Json::s(case)),
                                ("some", Json::b(result.is_some())),
                                (
                                    "text",
                                    result.map_or(Json::Null, |bytes| {
                                        Json::s(std::str::from_utf8(&bytes).unwrap())
                                    }),
                                ),
                            ])
                        })
                        .collect(),
                ),
            ),
        ]),
    )
}

fn valid_dispatchable() -> CheckOutcome {
    let dispatchable = {
        let json = r#"{"id":42,"method":"Network.enable","sessionId":"session-a","params":{"maxPostDataSize":65536,"enabled":true}}"#;
        let cbor = v8::crdtp::json_to_cbor(json.as_bytes()).unwrap();
        v8::crdtp::Dispatchable::new(&cbor)
    };
    let method_first = dispatchable.method();
    let method_second = dispatchable.method();
    let params_first = dispatchable.params();
    let params_second = dispatchable.params();
    pass(
        "crdtp-core/dispatchable_valid_owned",
        Json::obj(vec![
            ("ok", Json::b(dispatchable.ok())),
            ("has_call_id", Json::b(dispatchable.has_call_id())),
            ("call_id", Json::i(i64::from(dispatchable.call_id()))),
            ("method", Json::s(&dispatchable.method_str())),
            ("method_hex", Json::s(&hex(&method_first))),
            (
                "session_id",
                Json::s(std::str::from_utf8(&dispatchable.session_id()).unwrap()),
            ),
            ("params", json_text(&params_first)),
            (
                "associated_data",
                Json::s(std::str::from_utf8(&dispatchable.associated_data()).unwrap()),
            ),
            (
                "repeated_access_equal",
                Json::b(method_first == method_second && params_first == params_second),
            ),
            ("input_owner_dropped", Json::b(true)),
        ]),
    )
}

fn dispatchable_without_params() -> CheckOutcome {
    let cbor = v8::crdtp::json_to_cbor(br#"{"id":-7,"method":"Runtime.run"}"#).unwrap();
    let dispatchable = v8::crdtp::Dispatchable::new(&cbor);
    pass(
        "crdtp-core/dispatchable_optional_fields",
        Json::obj(vec![
            ("ok", Json::b(dispatchable.ok())),
            ("has_call_id", Json::b(dispatchable.has_call_id())),
            ("call_id", Json::i(i64::from(dispatchable.call_id()))),
            ("method", Json::s(&dispatchable.method_str())),
            (
                "session_id_len",
                Json::i(dispatchable.session_id().len() as i64),
            ),
            ("params_len", Json::i(dispatchable.params().len() as i64)),
            (
                "associated_data_len",
                Json::i(dispatchable.associated_data().len() as i64),
            ),
        ]),
    )
}

fn invalid_dispatchables() -> CheckOutcome {
    let valid = v8::crdtp::json_to_cbor(br#"{"id":1,"method":"Test.run"}"#).unwrap();
    let missing_method = v8::crdtp::json_to_cbor(br#"{"id":1,"params":{}}"#).unwrap();
    let missing_id = v8::crdtp::json_to_cbor(br#"{"method":"Test.run","params":{}}"#).unwrap();
    let wrong_id = v8::crdtp::json_to_cbor(br#"{"id":"1","method":"Test.run"}"#).unwrap();
    let unknown_property =
        v8::crdtp::json_to_cbor(br#"{"id":1,"method":"Test.run","extra":true}"#).unwrap();
    let non_ascii_session =
        v8::crdtp::json_to_cbor(r#"{"id":1,"method":"Test.run","sessionId":"α"}"#.as_bytes())
            .unwrap();
    let inputs: [(&str, &[u8]); 8] = [
        ("empty", b""),
        ("garbage", &[0xff, 0xfe, 0x00, 0x01]),
        ("truncated", &valid[..valid.len() / 2]),
        ("missing_method", &missing_method),
        ("missing_id", &missing_id),
        ("wrong_id_type", &wrong_id),
        ("unknown_property", &unknown_property),
        ("non_ascii_session", &non_ascii_session),
    ];
    let observations = inputs
        .into_iter()
        .map(|(case, input)| {
            let dispatchable = v8::crdtp::Dispatchable::new(input);
            Json::obj(vec![
                ("case", Json::s(case)),
                ("ok", Json::b(dispatchable.ok())),
            ])
        })
        .collect();
    pass("crdtp-core/dispatchable_invalid", Json::arr(observations))
}

fn response_json(case: &'static str, response: v8::crdtp::DispatchResponse) -> Json {
    Json::obj(vec![
        ("case", Json::s(case)),
        ("success", Json::b(response.is_success())),
        ("error", Json::b(response.is_error())),
        ("fall_through", Json::b(response.is_fall_through())),
        ("code", Json::i(i64::from(response.code()))),
        ("message", Json::s(&response.message())),
    ])
}

fn dispatch_responses() -> CheckOutcome {
    let responses = vec![
        response_json("success", v8::crdtp::DispatchResponse::success()),
        response_json("fall_through", v8::crdtp::DispatchResponse::fall_through()),
        response_json(
            "parse_error",
            v8::crdtp::DispatchResponse::parse_error("parse"),
        ),
        response_json(
            "invalid_request",
            v8::crdtp::DispatchResponse::invalid_request("invalid request"),
        ),
        response_json(
            "method_not_found",
            v8::crdtp::DispatchResponse::method_not_found("not found"),
        ),
        response_json(
            "invalid_params",
            v8::crdtp::DispatchResponse::invalid_params("invalid params"),
        ),
        response_json(
            "server_error",
            v8::crdtp::DispatchResponse::server_error("server\0Ω"),
        ),
    ];
    pass("crdtp-core/dispatch_responses", Json::arr(responses))
}

fn serializable_json(serializable: &v8::crdtp::Serializable) -> Json {
    let first = serializable.to_bytes();
    let second = serializable.to_bytes();
    Json::obj(vec![
        ("bytes_len", Json::i(first.len() as i64)),
        ("bytes_hex", Json::s(&hex(&first))),
        ("json", json_text(&first)),
        ("repeated_equal", Json::b(first == second)),
    ])
}

fn serializable_helpers() -> CheckOutcome {
    let error_response = v8::crdtp::create_error_response(
        123,
        v8::crdtp::DispatchResponse::invalid_params("bad\0value"),
    );
    let error_notification = v8::crdtp::create_error_notification(
        v8::crdtp::DispatchResponse::server_error("notify failed"),
    );
    let success_empty = v8::crdtp::create_response(42, None);
    let nested_params = v8::crdtp::create_notification("Inner.event", None);
    let success_with_params = v8::crdtp::create_response(-5, Some(nested_params));
    let notification_empty = v8::crdtp::create_notification("Test.event", None);
    let notification_with_params =
        v8::crdtp::create_notification("Test.Ω", Some(v8::crdtp::create_response(9, None)));
    let notification_empty_method = v8::crdtp::create_notification("", None);
    pass(
        "crdtp-core/serializable_helpers",
        Json::obj(vec![
            ("error_response", serializable_json(&error_response)),
            ("error_notification", serializable_json(&error_notification)),
            ("success_empty", serializable_json(&success_empty)),
            (
                "success_with_params",
                serializable_json(&success_with_params),
            ),
            ("notification_empty", serializable_json(&notification_empty)),
            (
                "notification_with_params",
                serializable_json(&notification_with_params),
            ),
            (
                "notification_empty_method",
                serializable_json(&notification_empty_method),
            ),
        ]),
    )
}

fn run_oracle() -> Vec<CheckOutcome> {
    vec![
        conversion_success(),
        conversion_failures(),
        valid_dispatchable(),
        dispatchable_without_params(),
        invalid_dispatchables(),
        dispatch_responses(),
        serializable_helpers(),
    ]
}

fn main() -> std::process::ExitCode {
    if std::env::args().nth(1).as_deref() == Some("mode=notification-interior-nul") {
        let _ = v8::crdtp::create_notification("bad\0method", None);
        return std::process::ExitCode::FAILURE;
    }

    let outcomes = run_oracle();
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!(
        "{}",
        summary_line(outcomes.len(), passed, outcomes.len() - passed)
    );
    if passed == outcomes.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
