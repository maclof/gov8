//! Inspector client value/default-context oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. All callbacks are
//! reached through CDP `Runtime.evaluate`; none are called directly.

use std::cell::RefCell;
use std::rc::Rc;

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

#[derive(Default)]
struct ChannelState {
    responses: Vec<(i32, String)>,
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

    fn send_notification(&self, _message: v8::UniquePtr<v8::inspector::StringBuffer>) {}

    fn flush_protocol_notifications(&self) {}
}

struct Target {
    label: &'static str,
    value: v8::Global<v8::Value>,
    subtype: Option<&'static str>,
    description: Option<&'static str>,
}

struct CallbackObservation {
    phase: &'static str,
    label: &'static str,
    is_object: bool,
    current_context_matches: bool,
    value_identity_matches: bool,
}

struct EnsureObservation {
    group: i32,
    returned_context: bool,
}

#[derive(Default)]
struct ClientState {
    context: Option<v8::Global<v8::Context>>,
    // This local is created in the outer oracle HandleScope. The inspector and
    // every session are dropped before that scope, so its extended lifetime is
    // upheld. See its construction in `run_oracle`.
    default_context: Option<v8::Local<'static, v8::Context>>,
    targets: Vec<Target>,
    callbacks: Vec<CallbackObservation>,
    ensure_calls: Vec<EnsureObservation>,
    drops: usize,
}

struct RecordingClient(Rc<RefCell<ClientState>>);

impl RecordingClient {
    fn identify<'s>(
        state: &ClientState,
        scope: &v8::PinScope<'s, '_>,
        value: v8::Local<'s, v8::Value>,
    ) -> (usize, bool) {
        state
            .targets
            .iter()
            .enumerate()
            .find_map(|(index, target)| {
                let expected = v8::Local::new(scope, &target.value);
                value.strict_equals(expected).then_some((index, true))
            })
            .unwrap_or((usize::MAX, false))
    }

    fn context_matches<'s>(state: &ClientState, scope: &v8::PinScope<'s, '_>) -> bool {
        state
            .context
            .as_ref()
            .is_some_and(|expected| scope.get_current_context() == v8::Local::new(scope, expected))
    }
}

impl v8::inspector::V8InspectorClientImpl for RecordingClient {
    fn value_subtype<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        value: v8::Local<'s, v8::Value>,
    ) -> Option<v8::UniquePtr<v8::inspector::StringBuffer>> {
        let mut state = self.0.borrow_mut();
        let (index, identity) = Self::identify(&state, scope, value);
        let (label, subtype) = state
            .targets
            .get(index)
            .map_or(("other", None), |target| (target.label, target.subtype));
        let context_matches = Self::context_matches(&state, scope);
        state.callbacks.push(CallbackObservation {
            phase: "subtype",
            label,
            is_object: value.is_object(),
            current_context_matches: context_matches,
            value_identity_matches: identity,
        });
        subtype.map(|text| {
            v8::inspector::StringBuffer::create(v8::inspector::StringView::from(text.as_bytes()))
        })
    }

    fn description_for_value_subtype<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        value: v8::Local<'s, v8::Value>,
    ) -> Option<v8::UniquePtr<v8::inspector::StringBuffer>> {
        let mut state = self.0.borrow_mut();
        let (index, identity) = Self::identify(&state, scope, value);
        let (label, description) = state
            .targets
            .get(index)
            .map_or(("other", None), |target| (target.label, target.description));
        let context_matches = Self::context_matches(&state, scope);
        state.callbacks.push(CallbackObservation {
            phase: "description",
            label,
            is_object: value.is_object(),
            current_context_matches: context_matches,
            value_identity_matches: identity,
        });
        description.map(|text| {
            v8::inspector::StringBuffer::create(v8::inspector::StringView::from(text.as_bytes()))
        })
    }

    fn ensure_default_context_in_group(
        &self,
        context_group_id: i32,
    ) -> Option<v8::Local<'_, v8::Context>> {
        let mut state = self.0.borrow_mut();
        let result = (context_group_id == 7)
            .then_some(state.default_context)
            .flatten();
        state.ensure_calls.push(EnsureObservation {
            group: context_group_id,
            returned_context: result.is_some(),
        });
        result
    }
}

impl Drop for RecordingClient {
    fn drop(&mut self) {
        self.0.borrow_mut().drops += 1;
    }
}

#[derive(Copy, Clone, Eq, PartialEq)]
enum PanicKind {
    Subtype,
    Description,
    Ensure,
}

struct PanicClient(PanicKind);

impl v8::inspector::V8InspectorClientImpl for PanicClient {
    fn value_subtype<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _value: v8::Local<'s, v8::Value>,
    ) -> Option<v8::UniquePtr<v8::inspector::StringBuffer>> {
        if self.0 == PanicKind::Subtype {
            panic!("inspector client value_subtype panic boundary");
        }
        Some(v8::inspector::StringBuffer::create(
            v8::inspector::StringView::from(&b"node"[..]),
        ))
    }

    fn description_for_value_subtype<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _value: v8::Local<'s, v8::Value>,
    ) -> Option<v8::UniquePtr<v8::inspector::StringBuffer>> {
        if self.0 == PanicKind::Description {
            panic!("inspector client description_for_value_subtype panic boundary");
        }
        None
    }

    fn ensure_default_context_in_group(
        &self,
        _context_group_id: i32,
    ) -> Option<v8::Local<'_, v8::Context>> {
        if self.0 == PanicKind::Ensure {
            panic!("inspector client ensure_default_context panic boundary");
        }
        None
    }
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

fn mirror_json(response: &str) -> Json {
    Json::obj(vec![
        ("success", Json::b(!response.contains("\"error\""))),
        (
            "type",
            json_string_field(response, "type").map_or(Json::Null, |value| Json::s(&value)),
        ),
        (
            "subtype",
            json_string_field(response, "subtype").map_or(Json::Null, |value| Json::s(&value)),
        ),
        (
            "class_name",
            json_string_field(response, "className").map_or(Json::Null, |value| Json::s(&value)),
        ),
        (
            "description",
            json_string_field(response, "description").map_or(Json::Null, |value| Json::s(&value)),
        ),
    ])
}

fn callback_json(observation: &CallbackObservation) -> Json {
    Json::obj(vec![
        ("phase", Json::s(observation.phase)),
        ("label", Json::s(observation.label)),
        ("is_object", Json::b(observation.is_object)),
        (
            "current_context_matches",
            Json::b(observation.current_context_matches),
        ),
        (
            "value_identity_matches",
            Json::b(observation.value_identity_matches),
        ),
    ])
}

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn run_oracle() -> Vec<CheckOutcome> {
    use v8::inspector::{
        Channel, StringView, V8Inspector, V8InspectorClient, V8InspectorClientTrustLevel,
    };

    let isolate = &mut v8::Isolate::new(Default::default());
    let client_state = Rc::new(RefCell::new(ClientState::default()));
    let inspector = V8Inspector::create(
        isolate,
        V8InspectorClient::new(Box::new(RecordingClient(client_state.clone()))),
    );
    let channel_state = Rc::new(RefCell::new(ChannelState::default()));

    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eval(
        scope,
        "globalThis.marked={}; globalThis.noDescription={}; \
         globalThis.invalidSubtype={}; globalThis.plain={}; globalThis.answer=41;",
    );

    let target_specs = [
        ("marked", "marked", Some("node"), Some("marked object")),
        ("no_description", "noDescription", Some("node"), None),
        (
            "invalid_subtype",
            "invalidSubtype",
            Some("not-a-protocol-subtype"),
            Some("invalid subtype object"),
        ),
        ("plain", "plain", None, None),
    ];
    {
        let mut state = client_state.borrow_mut();
        state.context = Some(v8::Global::new(scope, context));
        // SAFETY: `context` belongs to the HandleScope enclosing this entire
        // function. `session` and `inspector` are explicitly dropped before
        // leaving it, so callbacks cannot use this local after its scope ends.
        state.default_context =
            Some(unsafe { context.extend_lifetime_unchecked::<v8::Local<'static, v8::Context>>() });
        for (label, expression, subtype, description) in target_specs {
            let value = eval(scope, expression);
            state.targets.push(Target {
                label,
                value: v8::Global::new(scope, value),
                subtype,
                description,
            });
        }
    }

    inspector.context_created(
        context,
        7,
        StringView::from(&b"client-values"[..]),
        StringView::from(&br#"{"isDefault":true}"#[..]),
    );
    let session = inspector.connect(
        7,
        Channel::new(Box::new(RecordingChannel(channel_state.clone()))),
        StringView::from(&b"{}"[..]),
        V8InspectorClientTrustLevel::FullyTrusted,
    );

    let mut outcomes = Vec::new();
    let expressions = [
        "marked",
        "noDescription",
        "invalidSubtype",
        "plain",
        "answer",
    ];
    let callbacks_start = client_state.borrow().callbacks.len();
    let mirrors: Vec<_> = expressions
        .iter()
        .enumerate()
        .map(|(index, expression)| {
            let call_id = index as i32 + 1;
            let request = format!(
                r#"{{"id":{call_id},"method":"Runtime.evaluate","params":{{"expression":"{expression}","contextId":1}}}}"#
            );
            mirror_json(&dispatch(&session, &channel_state, call_id, &request))
        })
        .collect();
    let callbacks: Vec<_> = client_state.borrow().callbacks[callbacks_start..]
        .iter()
        .map(callback_json)
        .collect();
    outcomes.push(pass(
        "inspector-client-values/subtype_description",
        Json::obj(vec![
            (
                "expressions",
                Json::arr(expressions.iter().map(|value| Json::s(value)).collect()),
            ),
            ("mirrors", Json::arr(mirrors)),
            ("callbacks", Json::arr(callbacks)),
        ]),
    ));

    let ensure_start = client_state.borrow().ensure_calls.len();
    let default_first = dispatch(
        &session,
        &channel_state,
        10,
        r#"{"id":10,"method":"Runtime.evaluate","params":{"expression":"answer += 1"}}"#,
    );
    let default_second = dispatch(
        &session,
        &channel_state,
        11,
        r#"{"id":11,"method":"Runtime.evaluate","params":{"expression":"answer"}}"#,
    );
    let ensure_calls: Vec<_> = client_state.borrow().ensure_calls[ensure_start..]
        .iter()
        .map(|call| {
            Json::obj(vec![
                ("group", Json::i(i64::from(call.group))),
                ("returned_context", Json::b(call.returned_context)),
            ])
        })
        .collect();
    outcomes.push(pass(
        "inspector-client-values/default_context_success",
        Json::obj(vec![
            (
                "responses_success",
                Json::arr(vec![
                    Json::b(!default_first.contains("\"error\"")),
                    Json::b(!default_second.contains("\"error\"")),
                ]),
            ),
            (
                "results_are_42",
                Json::arr(vec![
                    Json::b(default_first.contains("\"value\":42")),
                    Json::b(default_second.contains("\"value\":42")),
                ]),
            ),
            ("callback_calls", Json::arr(ensure_calls)),
        ]),
    ));

    let missing_channel = Rc::new(RefCell::new(ChannelState::default()));
    let missing_session = inspector.connect(
        99,
        Channel::new(Box::new(RecordingChannel(missing_channel.clone()))),
        StringView::from(&b"{}"[..]),
        V8InspectorClientTrustLevel::FullyTrusted,
    );
    let missing = dispatch(
        &missing_session,
        &missing_channel,
        20,
        r#"{"id":20,"method":"Runtime.evaluate","params":{"expression":"1"}}"#,
    );
    let (missing_group, missing_returned) = {
        let state = client_state.borrow();
        let call = state.ensure_calls.last().unwrap();
        (call.group, call.returned_context)
    };
    outcomes.push(pass(
        "inspector-client-values/default_context_none",
        Json::obj(vec![
            ("response_error", Json::b(missing.contains("\"error\""))),
            (
                "error_message",
                json_string_field(&missing, "message").map_or(Json::Null, |value| Json::s(&value)),
            ),
            ("callback_group", Json::i(i64::from(missing_group))),
            ("returned_context", Json::b(missing_returned)),
        ]),
    ));

    let drops_before = client_state.borrow().drops;
    drop(missing_session);
    drop(session);
    inspector.context_destroyed(context);
    drop(inspector);
    outcomes.push(pass(
        "inspector-client-values/lifecycle",
        Json::obj(vec![
            ("drops_before_inspector", Json::i(drops_before as i64)),
            (
                "drops_after_inspector",
                Json::i(client_state.borrow().drops as i64),
            ),
            (
                "total_value_callbacks",
                Json::i(client_state.borrow().callbacks.len() as i64),
            ),
            (
                "total_default_context_callbacks",
                Json::i(client_state.borrow().ensure_calls.len() as i64),
            ),
        ]),
    ));
    outcomes
}

fn panic_mode(kind: PanicKind) {
    use v8::inspector::{
        Channel, StringView, V8Inspector, V8InspectorClient, V8InspectorClientTrustLevel,
    };

    let isolate = &mut v8::Isolate::new(Default::default());
    let inspector =
        V8Inspector::create(isolate, V8InspectorClient::new(Box::new(PanicClient(kind))));
    let channel_state = Rc::new(RefCell::new(ChannelState::default()));
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let _context_scope = &mut v8::ContextScope::new(scope, context);
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
    let request = if kind == PanicKind::Ensure {
        r#"{"id":1,"method":"Runtime.evaluate","params":{"expression":"1"}}"#
    } else {
        r#"{"id":1,"method":"Runtime.evaluate","params":{"expression":"({})","contextId":1}}"#
    };
    session.dispatch_protocol_message(StringView::from(request.as_bytes()));
}

fn main() -> std::process::ExitCode {
    let panic_kind = match std::env::args().nth(1).as_deref() {
        Some("mode=panic-subtype") => Some(PanicKind::Subtype),
        Some("mode=panic-description") => Some(PanicKind::Description),
        Some("mode=panic-ensure") => Some(PanicKind::Ensure),
        _ => None,
    };
    if let Some(kind) = panic_kind {
        suppress_windows_fatal_dialogs();
        oracle::ensure_v8();
        panic_mode(kind);
        return std::process::ExitCode::FAILURE;
    }

    oracle::ensure_v8();
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
