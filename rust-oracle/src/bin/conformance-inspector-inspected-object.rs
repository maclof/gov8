//! V8 Inspector inspected-object oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This slice covers
//! `Inspectable::new`, `InspectableImpl::get`, and
//! `V8InspectorSession::add_inspected_object` through the inspector command
//! line API's `$0` through `$4` bindings.

use std::cell::RefCell;
use std::rc::Rc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

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

struct DefaultClient;

impl v8::inspector::V8InspectorClientImpl for DefaultClient {}

struct ProbeInspectable {
    id: i32,
    value: v8::Global<v8::Value>,
    gets: Arc<AtomicUsize>,
    context_matches: Arc<AtomicBool>,
    drops: Arc<Mutex<Vec<i32>>>,
}

impl v8::inspector::InspectableImpl for ProbeInspectable {
    fn get<'s>(
        &self,
        scope: &mut v8::PinScope<'s, '_>,
        context: v8::Local<'s, v8::Context>,
    ) -> v8::Local<'s, v8::Value> {
        self.gets.fetch_add(1, Ordering::SeqCst);
        self.context_matches
            .fetch_and(scope.get_current_context() == context, Ordering::SeqCst);
        v8::Local::new(scope, &self.value)
    }
}

impl Drop for ProbeInspectable {
    fn drop(&mut self) {
        self.drops.lock().unwrap().push(self.id);
    }
}

struct PanicInspectable;

impl v8::inspector::InspectableImpl for PanicInspectable {
    fn get<'s>(
        &self,
        _scope: &mut v8::PinScope<'s, '_>,
        _context: v8::Local<'s, v8::Context>,
    ) -> v8::Local<'s, v8::Value> {
        panic!("inspector inspected-object callback panic boundary")
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

fn json_escape(value: &str) -> String {
    value
        .replace('\\', "\\\\")
        .replace('"', "\\\"")
        .replace('\n', "\\n")
        .replace('\r', "\\r")
}

fn evaluate_string(
    session: &v8::inspector::V8InspectorSession,
    state: &Rc<RefCell<ChannelState>>,
    call_id: i32,
    expression: &str,
) -> String {
    let response = dispatch(
        session,
        state,
        call_id,
        &format!(
            "{{\"id\":{call_id},\"method\":\"Runtime.evaluate\",\"params\":{{\"expression\":\"{}\",\"contextId\":1,\"includeCommandLineAPI\":true,\"returnByValue\":true}}}}",
            json_escape(expression)
        ),
    );
    assert!(!response.contains("\"error\""), "{response}");
    json_string_field(&response, "value")
        .unwrap_or_else(|| panic!("Runtime.evaluate returned no string value: {response}"))
}

fn new_probe<'s>(
    scope: &v8::PinScope<'s, '_>,
    id: i32,
    marker: i32,
    drops: Arc<Mutex<Vec<i32>>>,
) -> (
    v8::inspector::Inspectable,
    Arc<AtomicUsize>,
    Arc<AtomicBool>,
) {
    let object = v8::Object::new(scope);
    let id_key = v8::String::new(scope, "id").unwrap();
    let marker_key = v8::String::new(scope, "marker").unwrap();
    assert_eq!(
        object.set(scope, id_key.into(), v8::Integer::new(scope, id).into()),
        Some(true)
    );
    assert_eq!(
        object.set(
            scope,
            marker_key.into(),
            v8::Integer::new(scope, marker).into()
        ),
        Some(true)
    );
    let value: v8::Local<v8::Value> = object.into();
    let gets = Arc::new(AtomicUsize::new(0));
    let context_matches = Arc::new(AtomicBool::new(true));
    let inspectable = v8::inspector::Inspectable::new(Box::new(ProbeInspectable {
        id,
        value: v8::Global::new(scope, value),
        gets: gets.clone(),
        context_matches: context_matches.clone(),
        drops,
    }));
    (inspectable, gets, context_matches)
}

fn sorted_drops(drops: &Arc<Mutex<Vec<i32>>>) -> Vec<i32> {
    let mut values = drops.lock().unwrap().clone();
    values.sort_unstable();
    values
}

fn ints(values: &[i32]) -> Json {
    Json::arr(
        values
            .iter()
            .map(|value| Json::i(i64::from(*value)))
            .collect(),
    )
}

fn run_oracle() -> Vec<CheckOutcome> {
    use v8::inspector::{
        Channel, StringView, V8Inspector, V8InspectorClient, V8InspectorClientTrustLevel,
    };

    let isolate = &mut v8::Isolate::new(Default::default());
    let inspector = V8Inspector::create(isolate, V8InspectorClient::new(Box::new(DefaultClient)));
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
        Channel::new(Box::new(RecordingChannel(channel_state.clone()))),
        StringView::from(&b"{}"[..]),
        V8InspectorClientTrustLevel::FullyTrusted,
    );
    let drops = Arc::new(Mutex::new(Vec::new()));
    let mut outcomes = Vec::new();

    let missing = evaluate_string(&session, &channel_state, 1, "typeof $0");
    let invalid = evaluate_string(
        &session,
        &channel_state,
        2,
        "(()=>{try{return String($5)}catch(e){return e.name}})()",
    );
    outcomes.push(pass(
        "inspector-inspected-object/missing_invalid_index",
        Json::obj(vec![
            ("missing_dollar_0", Json::s(&missing)),
            ("invalid_dollar_5", Json::s(&invalid)),
        ]),
    ));

    let unadded_drops = Arc::new(Mutex::new(Vec::new()));
    let (unadded, unadded_gets, _) = new_probe(scope, -1, 0, unadded_drops.clone());
    let drops_before = sorted_drops(&unadded_drops);
    drop(unadded);
    outcomes.push(pass(
        "inspector-inspected-object/unadded_lifetime",
        Json::obj(vec![
            ("drops_before", ints(&drops_before)),
            ("drops_after", ints(&sorted_drops(&unadded_drops))),
            (
                "get_calls",
                Json::i(unadded_gets.load(Ordering::SeqCst) as i64),
            ),
        ]),
    ));

    let (identity, identity_gets, identity_context) = new_probe(scope, 100, 1, drops.clone());
    session.add_inspected_object(identity);
    let first = evaluate_string(
        &session,
        &channel_state,
        3,
        "[$0===$0,++$0.marker,$0.marker].join(',')",
    );
    let second = evaluate_string(&session, &channel_state, 4, "String($0.marker)");
    outcomes.push(pass(
        "inspector-inspected-object/live_identity_mutation",
        Json::obj(vec![
            ("first", Json::s(&first)),
            ("second", Json::s(&second)),
            (
                "get_calls",
                Json::i(identity_gets.load(Ordering::SeqCst) as i64),
            ),
            (
                "callback_context_matches_current",
                Json::b(identity_context.load(Ordering::SeqCst)),
            ),
            ("drops", ints(&sorted_drops(&drops))),
        ]),
    ));

    let mut probes = Vec::new();
    for id in 1..=2 {
        let (inspectable, gets, context_matches) = new_probe(scope, id, id * 10, drops.clone());
        session.add_inspected_object(inspectable);
        probes.push((id, gets, context_matches));
    }
    let shifted = evaluate_string(&session, &channel_state, 5, "[$0.id,$1.id,$2.id].join(',')");
    for id in 3..=6 {
        let (inspectable, gets, context_matches) = new_probe(scope, id, id * 10, drops.clone());
        session.add_inspected_object(inspectable);
        probes.push((id, gets, context_matches));
    }
    let retained = evaluate_string(
        &session,
        &channel_state,
        6,
        "[$0.id,$1.id,$2.id,$3.id,$4.id].join(',')",
    );
    let beyond_buffer = evaluate_string(
        &session,
        &channel_state,
        7,
        "(()=>{try{return String($5)}catch(e){return e.name}})()",
    );
    outcomes.push(pass(
        "inspector-inspected-object/replacement_and_eviction",
        Json::obj(vec![
            ("after_two_adds", Json::s(&shifted)),
            ("retained_newest_first", Json::s(&retained)),
            ("beyond_buffer", Json::s(&beyond_buffer)),
            ("evicted_drops", ints(&sorted_drops(&drops))),
            (
                "all_callback_contexts_match",
                Json::b(
                    probes
                        .iter()
                        .all(|(_, _, matches)| matches.load(Ordering::SeqCst)),
                ),
            ),
            (
                "get_calls_by_id",
                Json::arr(
                    probes
                        .iter()
                        .map(|(id, gets, _)| {
                            Json::obj(vec![
                                ("id", Json::i(i64::from(*id))),
                                ("calls", Json::i(gets.load(Ordering::SeqCst) as i64)),
                            ])
                        })
                        .collect(),
                ),
            ),
        ]),
    ));

    drop(session);
    outcomes.push(pass(
        "inspector-inspected-object/session_owns_retained_values",
        Json::obj(vec![
            ("all_drops_after_session", ints(&sorted_drops(&drops))),
            (
                "identity_get_calls",
                Json::i(identity_gets.load(Ordering::SeqCst) as i64),
            ),
        ]),
    ));
    inspector.context_destroyed(context);
    outcomes
}

fn panic_callback_mode() {
    use v8::inspector::{
        Channel, Inspectable, StringView, V8Inspector, V8InspectorClient,
        V8InspectorClientTrustLevel,
    };

    let isolate = &mut v8::Isolate::new(Default::default());
    let inspector = V8Inspector::create(isolate, V8InspectorClient::new(Box::new(DefaultClient)));
    let channel_state = Rc::new(RefCell::new(ChannelState::default()));
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let _scope = &mut v8::ContextScope::new(scope, context);
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
    session.add_inspected_object(Inspectable::new(Box::new(PanicInspectable)));
    session.dispatch_protocol_message(StringView::from(
        &br#"{"id":1,"method":"Runtime.evaluate","params":{"expression":"$0","contextId":1,"includeCommandLineAPI":true}}"#[..],
    ));
}

fn main() -> std::process::ExitCode {
    oracle::ensure_v8();
    if std::env::args().nth(1).as_deref() == Some("mode=panic-callback") {
        panic_callback_mode();
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
