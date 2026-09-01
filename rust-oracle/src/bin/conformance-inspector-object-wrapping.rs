//! V8 Inspector object wrapping oracle.
//!
//! Pinned declarations: `inspector.rs` 824-906 (`wrap_object` and
//! `unwrap_object`) and 948-977 (`RemoteObject::to_bytes`). The corresponding
//! native bridges are `binding.cc` 3569-3604 and `crdtp_binding.cc` 257-271.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

struct NullChannel;

impl v8::inspector::ChannelImpl for NullChannel {
    fn send_response(&self, _call_id: i32, _message: v8::UniquePtr<v8::inspector::StringBuffer>) {}

    fn send_notification(&self, _message: v8::UniquePtr<v8::inspector::StringBuffer>) {}

    fn flush_protocol_notifications(&self) {}
}

#[derive(Clone, Copy)]
struct DefaultClient;
impl v8::inspector::V8InspectorClientImpl for DefaultClient {}

fn json_string_field(input: &str, key: &str) -> Option<String> {
    let needle = format!("\"{key}\":\"");
    let rest = input.get(input.find(&needle)? + needle.len()..)?;
    let mut output = String::new();
    let mut chars = rest.chars();
    while let Some(ch) = chars.next() {
        match ch {
            '"' => return Some(output),
            '\\' => match chars.next()? {
                '"' => output.push('"'),
                '\\' => output.push('\\'),
                '/' => output.push('/'),
                'b' => output.push('\u{0008}'),
                'f' => output.push('\u{000c}'),
                'n' => output.push('\n'),
                'r' => output.push('\r'),
                't' => output.push('\t'),
                'u' => {
                    let mut value = 0_u32;
                    for _ in 0..4 {
                        value = (value << 4) | chars.next()?.to_digit(16)?;
                    }
                    output.push(char::from_u32(value)?);
                }
                _ => return None,
            },
            _ => output.push(ch),
        }
    }
    None
}

fn remote_json(remote: &v8::inspector::RemoteObject) -> String {
    let bytes = remote.to_bytes();
    String::from_utf8(v8::crdtp::cbor_to_json(&bytes).expect("RemoteObject CBOR"))
        .expect("RemoteObject JSON UTF-8")
}

fn object_id(remote: &v8::inspector::RemoteObject) -> String {
    let json = remote_json(remote);
    json_string_field(&json, "objectId")
        .unwrap_or_else(|| panic!("wrapped object has no objectId: {json}"))
}

fn normalized_remote_json(remote: &v8::inspector::RemoteObject) -> String {
    let json = remote_json(remote);
    match json_string_field(&json, "objectId") {
        Some(id) => json.replace(&id, "<object-id>"),
        None => json,
    }
}

fn view_observation(mut buffer: v8::UniquePtr<v8::inspector::StringBuffer>) -> Json {
    let view = buffer.as_mut().expect("non-null StringBuffer").string();
    let units = view
        .into_iter()
        .map(|unit| Json::i(i64::from(unit)))
        .collect();
    Json::obj(vec![
        ("is_8bit", Json::b(view.is_8bit())),
        ("is_empty", Json::b(view.is_empty())),
        ("len", Json::i(view.len() as i64)),
        ("units", Json::arr(units)),
        ("text", Json::s(&view.to_string())),
    ])
}

fn error_observation(buffer: v8::UniquePtr<v8::inspector::StringBuffer>) -> Json {
    view_observation(buffer)
}

fn make_session(
    inspector: &v8::inspector::V8Inspector,
    group: i32,
) -> v8::inspector::V8InspectorSession {
    inspector.connect(
        group,
        v8::inspector::Channel::new(Box::new(NullChannel)),
        v8::inspector::StringView::empty(),
        v8::inspector::V8InspectorClientTrustLevel::FullyTrusted,
    )
}

fn integer_property<'s>(
    scope: &mut v8::PinScope<'s, '_>,
    object: v8::Local<'s, v8::Value>,
    name: &str,
) -> Option<i64> {
    let object = v8::Local::<v8::Object>::try_from(object).ok()?;
    let key = v8::String::new(scope, name)?;
    object.get(scope, key.into())?.integer_value(scope)
}

fn same_context<'s>(
    scope: &v8::PinScope<'s, '_, ()>,
    left: v8::Local<'s, v8::Context>,
    right: v8::Local<'s, v8::Context>,
) -> bool {
    left.global(scope).strict_equals(right.global(scope).into())
}

fn session_observations() -> Vec<CheckOutcome> {
    use v8::inspector::{StringView, V8Inspector, V8InspectorClient};

    let isolate = &mut v8::Isolate::new(Default::default());
    let inspector = V8Inspector::create(isolate, V8InspectorClient::new(Box::new(DefaultClient)));
    let session = make_session(&inspector, 1);
    let mismatch_session = make_session(&inspector, 2);
    let mut outcomes = Vec::new();

    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let other_context = v8::Context::new(scope, Default::default());

    let scope = &mut v8::ContextScope::new(scope, context);
    let primitive: v8::Local<v8::Value> = v8::Number::new(scope, 7.5).into();
    let object = v8::Object::new(scope);
    let marker = v8::String::new(scope, "marker").unwrap();
    let initial: v8::Local<v8::Value> = v8::Integer::new(scope, 42).into();
    assert_eq!(object.set(scope, marker.into(), initial), Some(true));

    let before_registration =
        session.wrap_object(scope, context, object.into(), StringView::empty(), false);
    inspector.context_created(
        context,
        1,
        StringView::empty(),
        StringView::from(&br#"{"isDefault":true}"#[..]),
    );
    inspector.context_created(other_context, 1, StringView::empty(), StringView::empty());
    let mismatch_group =
        mismatch_session.wrap_object(scope, context, object.into(), StringView::empty(), false);
    let primitive_remote = session
        .wrap_object(scope, context, primitive, StringView::empty(), false)
        .expect("registered primitive wrap");
    let no_preview_option =
        session.wrap_object(scope, context, object.into(), StringView::empty(), false);
    let after_registration = no_preview_option.is_some();
    let no_preview = no_preview_option.expect("registered object wrap");
    let preview = session
        .wrap_object(scope, context, object.into(), StringView::empty(), true)
        .expect("preview object wrap");
    let primitive_json = remote_json(&primitive_remote);
    let no_preview_json = remote_json(&no_preview);
    let preview_json = remote_json(&preview);
    outcomes.push(pass(
        "inspector-object-wrapping/wrap_presence_metadata",
        Json::obj(vec![
            (
                "before_registration",
                Json::b(before_registration.is_some()),
            ),
            ("after_registration", Json::b(after_registration)),
            ("context_group_mismatch", Json::b(mismatch_group.is_some())),
            (
                "primitive",
                Json::obj(vec![
                    (
                        "type",
                        json_string_field(&primitive_json, "type")
                            .map_or(Json::Null, |v| Json::s(&v)),
                    ),
                    (
                        "has_value_7_5",
                        Json::b(primitive_json.contains("\"value\":7.5")),
                    ),
                    (
                        "has_object_id",
                        Json::b(primitive_json.contains("\"objectId\"")),
                    ),
                    (
                        "normalized_cbor_json",
                        Json::s(&normalized_remote_json(&primitive_remote)),
                    ),
                ]),
            ),
            (
                "object_without_preview",
                Json::obj(vec![
                    (
                        "type",
                        json_string_field(&no_preview_json, "type")
                            .map_or(Json::Null, |v| Json::s(&v)),
                    ),
                    (
                        "class_name",
                        json_string_field(&no_preview_json, "className")
                            .map_or(Json::Null, |v| Json::s(&v)),
                    ),
                    (
                        "has_object_id",
                        Json::b(no_preview_json.contains("\"objectId\"")),
                    ),
                    (
                        "has_preview",
                        Json::b(no_preview_json.contains("\"preview\"")),
                    ),
                    (
                        "normalized_cbor_json",
                        Json::s(&normalized_remote_json(&no_preview)),
                    ),
                ]),
            ),
            (
                "object_with_preview",
                Json::obj(vec![
                    ("has_preview", Json::b(preview_json.contains("\"preview\""))),
                    (
                        "preview_has_marker",
                        Json::b(preview_json.contains("marker")),
                    ),
                    ("preview_has_42", Json::b(preview_json.contains("42"))),
                    (
                        "normalized_cbor_json",
                        Json::s(&normalized_remote_json(&preview)),
                    ),
                ]),
            ),
        ]),
    ));

    let identity_group = b"identity";
    let identity_remote = session
        .wrap_object(
            scope,
            context,
            object.into(),
            StringView::from(&identity_group[..]),
            false,
        )
        .unwrap();
    let identity_id = object_id(&identity_remote);
    let (first_value, first_context, first_group) = session
        .unwrap_object(scope, StringView::from(identity_id.as_bytes()))
        .unwrap();
    let first_identity = first_value.strict_equals(object.into());
    let context_identity = same_context(scope, first_context, context);
    assert_eq!(
        object.set(scope, marker.into(), v8::Integer::new(scope, 99).into(),),
        Some(true)
    );
    let mutation_value = integer_property(scope, first_value, "marker");
    let (second_value, second_context, second_group) = session
        .unwrap_object(scope, StringView::from(identity_id.as_bytes()))
        .unwrap();
    drop(identity_remote);
    outcomes.push(pass(
        "inspector-object-wrapping/unwrap_identity_mutation",
        Json::obj(vec![
            ("value_strict_equals", Json::b(first_identity)),
            ("context_identity", Json::b(context_identity)),
            (
                "repeated_value_identity",
                Json::b(first_value.strict_equals(second_value)),
            ),
            (
                "repeated_context_identity",
                Json::b(same_context(scope, first_context, second_context)),
            ),
            ("mutation_value", mutation_value.map_or(Json::Null, Json::i)),
            ("first_group", view_observation(first_group)),
            ("second_group", view_observation(second_group)),
            (
                "locals_survive_remote_drop",
                Json::b(second_value.strict_equals(object.into())),
            ),
        ]),
    ));

    let group_cases: Vec<(&str, StringView<'_>)> = vec![
        ("u8_ascii", StringView::from(&b"ascii"[..])),
        (
            "u16_non_ascii",
            StringView::from(&[b'w' as u16, b'i' as u16, b'd' as u16, b'e' as u16, 0x03a9][..]),
        ),
        ("u8_nul", StringView::from(&b"nul\0group"[..])),
        ("empty_u8", StringView::empty()),
        ("empty_u16", StringView::from(&[] as &[u16])),
    ];
    let mut group_observations = Vec::new();
    for (name, group) in group_cases {
        let remote = session
            .wrap_object(scope, context, object.into(), group, false)
            .unwrap();
        let id = object_id(&remote);
        let (_, returned_context, returned_group) = session
            .unwrap_object(scope, StringView::from(id.as_bytes()))
            .unwrap();
        group_observations.push(Json::obj(vec![
            ("case", Json::s(name)),
            (
                "context_identity",
                Json::b(same_context(scope, returned_context, context)),
            ),
            ("group", view_observation(returned_group)),
        ]));
    }
    outcomes.push(pass(
        "inspector-object-wrapping/object_group_views",
        Json::arr(group_observations),
    ));

    let release_group = b"release-me";
    let release_remote = session
        .wrap_object(
            scope,
            context,
            object.into(),
            StringView::from(&release_group[..]),
            false,
        )
        .unwrap();
    let release_id = object_id(&release_remote);
    session.release_object_group(StringView::from(&release_group[..]));
    let released = session
        .unwrap_object(scope, StringView::from(release_id.as_bytes()))
        .unwrap_err();
    let invalid_cases: Vec<(&str, StringView<'_>)> = vec![
        ("invalid_u8", StringView::from(&b"not-an-object-id"[..])),
        (
            "invalid_u16",
            StringView::from(&[b'n' as u16, b'o' as u16, b't' as u16][..]),
        ),
        ("embedded_nul", StringView::from(&b"bad\0id"[..])),
        ("empty_u8", StringView::empty()),
        ("empty_u16", StringView::from(&[] as &[u16])),
    ];
    let mut invalid_observations = Vec::new();
    for (name, id) in invalid_cases {
        let error = session.unwrap_object(scope, id).unwrap_err();
        invalid_observations.push(Json::obj(vec![
            ("case", Json::s(name)),
            ("error", error_observation(error)),
        ]));
    }
    outcomes.push(pass(
        "inspector-object-wrapping/release_and_invalid_ids",
        Json::obj(vec![
            ("released", error_observation(released)),
            ("invalid", Json::arr(invalid_observations)),
        ]),
    ));

    let other_object = {
        let other_scope = &mut v8::ContextScope::new(scope, other_context);
        let value = v8::Object::new(other_scope);
        let key = v8::String::new(other_scope, "other").unwrap();
        assert_eq!(
            value.set(
                other_scope,
                key.into(),
                v8::Integer::new(other_scope, 17).into()
            ),
            Some(true)
        );
        value
    };
    let cross_remote = session
        .wrap_object(
            scope,
            context,
            other_object.into(),
            StringView::from(&b"cross"[..]),
            false,
        )
        .unwrap();
    let cross_id = object_id(&cross_remote);
    let (cross_value, cross_context, cross_group) = session
        .unwrap_object(scope, StringView::from(cross_id.as_bytes()))
        .unwrap();
    outcomes.push(pass(
        "inspector-object-wrapping/cross_context",
        Json::obj(vec![
            (
                "value_identity",
                Json::b(cross_value.strict_equals(other_object.into())),
            ),
            (
                "returned_supplied_context",
                Json::b(same_context(scope, cross_context, context)),
            ),
            (
                "returned_other_context",
                Json::b(same_context(scope, cross_context, other_context)),
            ),
            (
                "other_property",
                integer_property(scope, cross_value, "other").map_or(Json::Null, Json::i),
            ),
            ("group", view_observation(cross_group)),
        ]),
    ));

    inspector.context_destroyed(other_context);
    inspector.context_destroyed(context);
    outcomes
}

fn lifetime_observation() -> CheckOutcome {
    use v8::inspector::{StringView, V8Inspector, V8InspectorClient};

    let remote;
    let before;
    let after_session;
    let after_inspector;
    let repeat_equal;
    {
        let mut isolate = v8::Isolate::new(Default::default());
        let inspector = V8Inspector::create(
            &mut isolate,
            V8InspectorClient::new(Box::new(DefaultClient)),
        );
        {
            v8::scope!(let scope, &mut isolate);
            let context = v8::Context::new(scope, Default::default());
            let scope = &mut v8::ContextScope::new(scope, context);
            inspector.context_created(context, 1, StringView::empty(), StringView::empty());
            let session = make_session(&inspector, 1);
            let object = v8::Object::new(scope);
            remote = session
                .wrap_object(scope, context, object.into(), StringView::empty(), true)
                .unwrap();
            before = remote.to_bytes();
            let repeated = remote.to_bytes();
            repeat_equal = before == repeated;
            drop(session);
            after_session = remote.to_bytes();
            inspector.context_destroyed(context);
        }
        drop(inspector);
        after_inspector = remote.to_bytes();
    }
    let after_isolate = remote.to_bytes();
    pass(
        "inspector-object-wrapping/remote_object_lifetime",
        Json::obj(vec![
            ("nonempty", Json::b(!before.is_empty())),
            ("repeat_equal", Json::b(repeat_equal)),
            ("after_session_equal", Json::b(before == after_session)),
            ("after_inspector_equal", Json::b(before == after_inspector)),
            ("after_isolate_equal", Json::b(before == after_isolate)),
        ]),
    )
}

fn invalid_id_mode() {
    use v8::inspector::{StringView, V8Inspector, V8InspectorClient};
    let isolate = &mut v8::Isolate::new(Default::default());
    let inspector = V8Inspector::create(isolate, V8InspectorClient::new(Box::new(DefaultClient)));
    let session = make_session(&inspector, 1);
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    inspector.context_created(context, 1, StringView::empty(), StringView::empty());
    let ids: [StringView<'_>; 4] = [
        StringView::from(&b"bad"[..]),
        StringView::from(&[b'b' as u16, b'a' as u16, b'd' as u16][..]),
        StringView::from(&b"bad\0id"[..]),
        StringView::empty(),
    ];
    let failures = ids
        .into_iter()
        .filter(|id| session.unwrap_object(scope, *id).is_err())
        .count();
    inspector.context_destroyed(context);
    println!("invalid-id-errors={failures}");
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    if std::env::args().nth(1).as_deref() == Some("mode=invalid-object-ids") {
        invalid_id_mode();
        return std::process::ExitCode::SUCCESS;
    }
    let mut outcomes = session_observations();
    outcomes.push(lifetime_observation());
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
