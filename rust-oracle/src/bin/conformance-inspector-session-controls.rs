//! V8 Inspector safe session-control oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This slice covers
//! `can_dispatch_method`, object-group release, and scheduled-pause controls.
//! Inspector protocol IDs and object IDs are deliberately reduced to stable
//! semantic observations.

use std::cell::RefCell;
use std::rc::Rc;

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

#[derive(Default)]
struct ChannelState {
    responses: Vec<(i32, String)>,
    notifications: Vec<String>,
    flushes: usize,
}

#[derive(Clone)]
struct RecordingChannel(Rc<RefCell<ChannelState>>);

impl v8::inspector::ChannelImpl for RecordingChannel {
    fn send_response(&self, call_id: i32, mut message: v8::UniquePtr<v8::inspector::StringBuffer>) {
        self.0
            .borrow_mut()
            .responses
            .push((call_id, message.as_mut().unwrap().string().to_string()));
    }

    fn send_notification(&self, mut message: v8::UniquePtr<v8::inspector::StringBuffer>) {
        self.0
            .borrow_mut()
            .notifications
            .push(message.as_mut().unwrap().string().to_string());
    }

    fn flush_protocol_notifications(&self) {
        self.0.borrow_mut().flushes += 1;
    }
}

#[derive(Default)]
struct ClientState {
    pause_groups: Vec<i32>,
    quit_calls: usize,
}

#[derive(Clone)]
struct RecordingClient(Rc<RefCell<ClientState>>);

impl v8::inspector::V8InspectorClientImpl for RecordingClient {
    fn run_message_loop_on_pause(&self, context_group_id: i32) {
        // Returning from this embedder hook resumes execution. No task/message
        // pump is needed for this deliberately synchronous oracle.
        self.0.borrow_mut().pause_groups.push(context_group_id);
    }

    fn quit_message_loop_on_pause(&self) {
        self.0.borrow_mut().quit_calls += 1;
    }
}

struct PanicPauseClient;

impl v8::inspector::V8InspectorClientImpl for PanicPauseClient {
    fn run_message_loop_on_pause(&self, _context_group_id: i32) {
        panic!("inspector pause client panic boundary")
    }
}

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn dispatch(
    session: &v8::inspector::V8InspectorSession,
    state: &Rc<RefCell<ChannelState>>,
    call_id: i32,
    message: &str,
) -> String {
    let before = state.borrow().responses.len();
    session.dispatch_protocol_message(v8::inspector::StringView::from(message.as_bytes()));
    let state = state.borrow();
    assert_eq!(state.responses.len(), before + 1);
    let (actual_id, response) = state.responses.last().unwrap();
    assert_eq!(*actual_id, call_id);
    response.clone()
}

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

fn object_id(response: &str) -> String {
    json_string_field(response, "objectId")
        .unwrap_or_else(|| panic!("Runtime.evaluate returned no objectId: {response}"))
}

fn evaluate_object(
    session: &v8::inspector::V8InspectorSession,
    state: &Rc<RefCell<ChannelState>>,
    call_id: i32,
    object_group_json: Option<&str>,
) -> String {
    let group = object_group_json
        .map(|group| format!(",\"objectGroup\":\"{group}\""))
        .unwrap_or_default();
    let response = dispatch(
        session,
        state,
        call_id,
        &format!(
            "{{\"id\":{call_id},\"method\":\"Runtime.evaluate\",\"params\":{{\"expression\":\"({{marker:42}})\",\"contextId\":1{group}}}}}"
        ),
    );
    object_id(&response)
}

fn properties_result(
    session: &v8::inspector::V8InspectorSession,
    state: &Rc<RefCell<ChannelState>>,
    call_id: i32,
    id: &str,
) -> Json {
    let response = dispatch(
        session,
        state,
        call_id,
        &format!(
            "{{\"id\":{call_id},\"method\":\"Runtime.getProperties\",\"params\":{{\"objectId\":\"{id}\"}}}}"
        ),
    );
    Json::obj(vec![
        ("success", Json::b(!response.contains("\"error\""))),
        (
            "error",
            json_string_field(&response, "message").map_or(Json::Null, |value| Json::s(&value)),
        ),
    ])
}

fn can_dispatch_methods() -> Vec<CheckOutcome> {
    use v8::inspector::{StringView, V8InspectorSession};

    let known_u16: Vec<u16> = "Debugger.enable".encode_utf16().collect();
    let known_prefix_unknown_u16: Vec<u16> = "Runtime.notARealMethod".encode_utf16().collect();
    let empty_u16: [u16; 0] = [];
    vec![pass(
        "inspector-session-controls/can_dispatch_method",
        Json::obj(vec![
            (
                "known_u8",
                Json::b(V8InspectorSession::can_dispatch_method(StringView::from(
                    &b"Runtime.evaluate"[..],
                ))),
            ),
            (
                "known_u16",
                Json::b(V8InspectorSession::can_dispatch_method(StringView::from(
                    known_u16.as_slice(),
                ))),
            ),
            (
                "unknown_domain_u8",
                Json::b(V8InspectorSession::can_dispatch_method(StringView::from(
                    &b"Unknown.evaluate"[..],
                ))),
            ),
            (
                "known_prefix_unknown_method_u16",
                Json::b(V8InspectorSession::can_dispatch_method(StringView::from(
                    known_prefix_unknown_u16.as_slice(),
                ))),
            ),
            (
                "embedded_nul_after_known_prefix_u8",
                Json::b(V8InspectorSession::can_dispatch_method(StringView::from(
                    &b"Runtime.\0suffix"[..],
                ))),
            ),
            (
                "empty_u8",
                Json::b(V8InspectorSession::can_dispatch_method(StringView::from(
                    &b""[..],
                ))),
            ),
            (
                "empty_u16",
                Json::b(V8InspectorSession::can_dispatch_method(StringView::from(
                    &empty_u16[..],
                ))),
            ),
            (
                "static_empty",
                Json::b(V8InspectorSession::can_dispatch_method(StringView::empty())),
            ),
        ]),
    )]
}

fn session_controls() -> Vec<CheckOutcome> {
    use v8::inspector::{
        Channel, StringView, V8Inspector, V8InspectorClient, V8InspectorClientTrustLevel,
    };

    let isolate = &mut v8::Isolate::new(Default::default());
    let client_state = Rc::new(RefCell::new(ClientState::default()));
    let client = V8InspectorClient::new(Box::new(RecordingClient(Rc::clone(&client_state))));
    let inspector = V8Inspector::create(isolate, client);
    let channel_state = Rc::new(RefCell::new(ChannelState::default()));
    let mut outcomes = Vec::new();

    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    inspector.context_created(
        context,
        1,
        StringView::empty(),
        StringView::from(&br#"{"isDefault":true}"#[..]),
    );
    let session = inspector.connect(
        1,
        Channel::new(Box::new(RecordingChannel(Rc::clone(&channel_state)))),
        StringView::from(&b"{}"[..]),
        V8InspectorClientTrustLevel::FullyTrusted,
    );

    let kept = evaluate_object(&session, &channel_state, 1, Some("keep"));
    session.release_object_group(StringView::from(&b"unknown"[..]));
    let after_unknown = properties_result(&session, &channel_state, 2, &kept);
    session.release_object_group(StringView::from(&b"keep"[..]));
    let after_exact_u8 = properties_result(&session, &channel_state, 3, &kept);

    let wide = evaluate_object(&session, &channel_state, 4, Some("wide-\\u03a9"));
    let wide_group = [
        b'w' as u16,
        b'i' as u16,
        b'd' as u16,
        b'e' as u16,
        b'-' as u16,
        0x03a9,
    ];
    session.release_object_group(StringView::from(wide_group.as_slice()));
    let after_exact_u16 = properties_result(&session, &channel_state, 5, &wide);

    let nul = evaluate_object(&session, &channel_state, 6, Some("nul\\u0000group"));
    session.release_object_group(StringView::from(&b"nul\0group"[..]));
    let after_embedded_nul = properties_result(&session, &channel_state, 7, &nul);

    let ungrouped = evaluate_object(&session, &channel_state, 8, None);
    session.release_object_group(StringView::empty());
    let after_empty = properties_result(&session, &channel_state, 9, &ungrouped);
    outcomes.push(pass(
        "inspector-session-controls/release_object_group",
        Json::obj(vec![
            ("unknown_preserves_object", after_unknown),
            ("exact_u8_releases", after_exact_u8),
            ("exact_u16_releases", after_exact_u16),
            ("embedded_nul_releases", after_embedded_nul),
            ("empty_group_result", after_empty),
        ]),
    ));

    let enable = dispatch(
        &session,
        &channel_state,
        10,
        r#"{"id":10,"method":"Debugger.enable"}"#,
    );
    let pauses_before_cancel = client_state.borrow().pause_groups.len();
    let notifications_before_cancel = channel_state.borrow().notifications.len();
    session.schedule_pause_on_next_statement(
        StringView::from(&b"cancelled"[..]),
        StringView::from(&br#"{"tag":"cancelled"}"#[..]),
    );
    session.cancel_pause_on_next_statement();
    let cancelled_value = run_script(scope, "21 * 2").integer_value(scope);
    let pauses_after_cancel = client_state.borrow().pause_groups.len();
    let new_cancel_notifications = channel_state.borrow().notifications
        [notifications_before_cancel..]
        .iter()
        .filter(|message| message.contains("\"method\":\"Debugger.paused\""))
        .count();
    outcomes.push(pass(
        "inspector-session-controls/schedule_then_cancel",
        Json::obj(vec![
            (
                "debugger_enable_response",
                Json::b(!enable.contains("\"error\"")),
            ),
            ("script_value", cancelled_value.map_or(Json::Null, Json::i)),
            (
                "new_pause_callbacks",
                Json::i((pauses_after_cancel - pauses_before_cancel) as i64),
            ),
            (
                "new_paused_notifications",
                Json::i(new_cancel_notifications as i64),
            ),
        ]),
    ));

    let mut pause_cases = Vec::new();
    let cases: [(&str, StringView<'_>, StringView<'_>); 3] = [
        (
            "valid_detail",
            StringView::from(&b"scheduled"[..]),
            StringView::from(&br#"{"tag":"ok"}"#[..]),
        ),
        (
            "nul_reason_empty_detail",
            StringView::from(&b"r\0x"[..]),
            StringView::empty(),
        ),
        (
            "empty_reason_nul_detail",
            StringView::from(&[] as &[u16]),
            StringView::from(&b"{\"tag\":\"d\0z\"}"[..]),
        ),
    ];
    for (index, (name, reason, detail)) in cases.into_iter().enumerate() {
        let notification_start = channel_state.borrow().notifications.len();
        let callback_start = client_state.borrow().pause_groups.len();
        session.schedule_pause_on_next_statement(reason, detail);
        let value = run_script(scope, &(index + 1).to_string()).integer_value(scope);
        let new_notifications = channel_state.borrow().notifications[notification_start..].to_vec();
        let paused: Vec<_> = new_notifications
            .iter()
            .filter(|message| message.contains("\"method\":\"Debugger.paused\""))
            .collect();
        let resumed = new_notifications
            .iter()
            .filter(|message| message.contains("\"method\":\"Debugger.resumed\""))
            .count();
        let paused_message = paused.first().copied();
        pause_cases.push(Json::obj(vec![
            ("case", Json::s(name)),
            ("script_value", value.map_or(Json::Null, Json::i)),
            (
                "pause_callbacks",
                Json::i((client_state.borrow().pause_groups.len() - callback_start) as i64),
            ),
            ("paused_notifications", Json::i(paused.len() as i64)),
            ("resumed_notifications", Json::i(resumed as i64)),
            (
                "reason",
                paused_message
                    .and_then(|message| json_string_field(message, "reason"))
                    .map_or(Json::Null, |value| Json::s(&value)),
            ),
            (
                "detail_tag",
                paused_message
                    .and_then(|message| json_string_field(message, "tag"))
                    .map_or(Json::Null, |value| Json::s(&value)),
            ),
        ]));
    }
    let client = client_state.borrow();
    let channel = channel_state.borrow();
    outcomes.push(pass(
        "inspector-session-controls/scheduled_pause_notifications",
        Json::obj(vec![
            ("cases", Json::arr(pause_cases)),
            (
                "all_context_groups",
                Json::arr(
                    client
                        .pause_groups
                        .iter()
                        .map(|value| Json::i(i64::from(*value)))
                        .collect(),
                ),
            ),
            ("quit_calls", Json::i(client.quit_calls as i64)),
            ("flushes", Json::i(channel.flushes as i64)),
        ]),
    ));
    drop(client);
    drop(channel);

    let callbacks_before_drop = client_state.borrow().pause_groups.len();
    session.schedule_pause_on_next_statement(
        StringView::from(&b"dropped-session"[..]),
        StringView::empty(),
    );
    drop(session);
    let value_after_drop = run_script(scope, "40 + 2").integer_value(scope);
    outcomes.push(pass(
        "inspector-session-controls/drop_session_cancels_scheduled_pause",
        Json::obj(vec![
            ("script_value", value_after_drop.map_or(Json::Null, Json::i)),
            (
                "new_pause_callbacks",
                Json::i((client_state.borrow().pause_groups.len() - callbacks_before_drop) as i64),
            ),
        ]),
    ));
    inspector.context_destroyed(context);
    outcomes
}

fn panic_pause_mode() {
    use v8::inspector::{
        Channel, StringView, V8Inspector, V8InspectorClient, V8InspectorClientTrustLevel,
    };

    let isolate = &mut v8::Isolate::new(Default::default());
    let client = V8InspectorClient::new(Box::new(PanicPauseClient));
    let inspector = V8Inspector::create(isolate, client);
    let channel_state = Rc::new(RefCell::new(ChannelState::default()));
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    inspector.context_created(
        context,
        1,
        StringView::empty(),
        StringView::from(&br#"{"isDefault":true}"#[..]),
    );
    let session = inspector.connect(
        1,
        Channel::new(Box::new(RecordingChannel(channel_state))),
        StringView::from(&b"{}"[..]),
        V8InspectorClientTrustLevel::FullyTrusted,
    );
    session.dispatch_protocol_message(StringView::from(
        &br#"{"id":1,"method":"Debugger.enable"}"#[..],
    ));
    session.schedule_pause_on_next_statement(StringView::empty(), StringView::empty());
    let _ = run_script(scope, "1");
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    if std::env::args().nth(1).as_deref() == Some("mode=panic-pause-client") {
        panic_pause_mode();
        return std::process::ExitCode::FAILURE;
    }

    let mut outcomes = can_dispatch_methods();
    outcomes.extend(session_controls());
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
