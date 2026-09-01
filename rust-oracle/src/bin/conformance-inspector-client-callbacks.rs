//! V8 Inspector client-callback oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This slice drives
//! `generate_unique_id`, `run_if_waiting_for_debugger`,
//! `resource_name_to_url`, and `console_api_message` through actual Inspector
//! construction, CDP dispatch, script parsing, and console execution.

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
    // The fatal probes intentionally trigger fail-fast. Suppress Windows Error
    // Reporting UI so an unattended test process cannot wait for a dialog.
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

#[derive(Default)]
struct ChannelState {
    responses: Vec<(i32, String)>,
    notifications: Vec<String>,
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

    fn flush_protocol_notifications(&self) {}
}

#[derive(Clone)]
struct ViewObservation {
    text: String,
    is_8bit: bool,
    len: usize,
}

impl ViewObservation {
    fn new(view: &v8::inspector::StringView<'_>) -> Self {
        Self {
            text: view.to_string(),
            is_8bit: view.is_8bit(),
            len: view.len(),
        }
    }

    fn json(&self) -> Json {
        Json::obj(vec![
            ("text", Json::s(&self.text)),
            ("is_8bit", Json::b(self.is_8bit)),
            ("len", Json::i(self.len as i64)),
        ])
    }
}

struct ResourceObservation {
    input: ViewObservation,
    mapped: Option<&'static str>,
}

struct ConsoleObservation {
    group: i32,
    level: i32,
    message: ViewObservation,
    url: ViewObservation,
    line: u32,
    column: u32,
}

#[derive(Default)]
struct ClientState {
    unique_ids_returned: Vec<i64>,
    waiting_groups: Vec<i32>,
    resources: Vec<ResourceObservation>,
    console: Vec<ConsoleObservation>,
    drops: usize,
}

struct RecordingClient(Rc<RefCell<ClientState>>);

impl v8::inspector::V8InspectorClientImpl for RecordingClient {
    fn run_if_waiting_for_debugger(&self, context_group_id: i32) {
        self.0.borrow_mut().waiting_groups.push(context_group_id);
    }

    fn generate_unique_id(&self) -> i64 {
        let mut state = self.0.borrow_mut();
        let value = 7001 + state.unique_ids_returned.len() as i64;
        state.unique_ids_returned.push(value);
        value
    }

    fn resource_name_to_url(
        &self,
        resource_name: &v8::inspector::StringView,
    ) -> Option<v8::UniquePtr<v8::inspector::StringBuffer>> {
        use v8::inspector::{StringBuffer, StringView};

        let input = ViewObservation::new(resource_name);
        let mapped = match input.text.as_str() {
            "mapped.js" => Some("client://mapped"),
            "nul\0name.js" => Some("client://nul"),
            "console.js" => Some("client://console"),
            _ => None,
        };
        self.0
            .borrow_mut()
            .resources
            .push(ResourceObservation { input, mapped });
        mapped.map(|value| StringBuffer::create(StringView::from(value.as_bytes())))
    }

    fn console_api_message(
        &self,
        context_group_id: i32,
        level: i32,
        message: &v8::inspector::StringView,
        url: &v8::inspector::StringView,
        line_number: u32,
        column_number: u32,
        _stack_trace: &mut v8::inspector::V8StackTrace,
    ) {
        self.0.borrow_mut().console.push(ConsoleObservation {
            group: context_group_id,
            level,
            message: ViewObservation::new(message),
            url: ViewObservation::new(url),
            line: line_number,
            column: column_number,
        });
    }
}

impl Drop for RecordingClient {
    fn drop(&mut self) {
        self.0.borrow_mut().drops += 1;
    }
}

#[derive(Copy, Clone, Eq, PartialEq)]
enum PanicKind {
    Generate,
    Waiting,
    Resource,
    Console,
}

struct PanicClient(PanicKind);

impl v8::inspector::V8InspectorClientImpl for PanicClient {
    fn generate_unique_id(&self) -> i64 {
        if self.0 == PanicKind::Generate {
            panic!("inspector client generate_unique_id panic boundary");
        }
        9001
    }

    fn run_if_waiting_for_debugger(&self, _context_group_id: i32) {
        if self.0 == PanicKind::Waiting {
            panic!("inspector client run_if_waiting panic boundary");
        }
    }

    fn resource_name_to_url(
        &self,
        resource_name: &v8::inspector::StringView,
    ) -> Option<v8::UniquePtr<v8::inspector::StringBuffer>> {
        if self.0 == PanicKind::Resource && resource_name.to_string() == "panic-resource.js" {
            panic!("inspector client resource_name_to_url panic boundary");
        }
        None
    }

    fn console_api_message(
        &self,
        _context_group_id: i32,
        _level: i32,
        _message: &v8::inspector::StringView,
        _url: &v8::inspector::StringView,
        _line_number: u32,
        _column_number: u32,
        _stack_trace: &mut v8::inspector::V8StackTrace,
    ) {
        if self.0 == PanicKind::Console {
            panic!("inspector client console_api_message panic boundary");
        }
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

fn compile_and_run<'s>(
    scope: &v8::PinScope<'s, '_>,
    source: &str,
    resource_name: &str,
) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    let resource: v8::Local<v8::Value> = v8::String::new(scope, resource_name).unwrap().into();
    let origin = v8::ScriptOrigin::new(
        scope, resource, 0, 0, false, 0, None, false, false, false, None,
    );
    v8::Script::compile(scope, source, Some(&origin))
        .unwrap()
        .run(scope)
        .unwrap()
}

fn parsed_url(
    scope: &v8::PinScope<'_, '_>,
    channel: &Rc<RefCell<ChannelState>>,
    source: &str,
    resource_name: &str,
) -> String {
    let start = channel.borrow().notifications.len();
    compile_and_run(scope, source, resource_name);
    let notifications = channel.borrow().notifications[start..].to_vec();
    let parsed: Vec<_> = notifications
        .iter()
        .filter(|message| message.contains("\"method\":\"Debugger.scriptParsed\""))
        .collect();
    assert_eq!(parsed.len(), 1, "{notifications:?}");
    json_string_field(parsed[0], "url").unwrap()
}

fn resource_json(observation: &ResourceObservation) -> Json {
    Json::obj(vec![
        ("input", observation.input.json()),
        ("mapped", observation.mapped.map_or(Json::Null, Json::s)),
    ])
}

fn console_json(observation: &ConsoleObservation) -> Json {
    Json::obj(vec![
        ("group", Json::i(i64::from(observation.group))),
        ("level", Json::i(i64::from(observation.level))),
        ("message", observation.message.json()),
        ("url", observation.url.json()),
        ("line", Json::i(i64::from(observation.line))),
        ("column", Json::i(i64::from(observation.column))),
        ("stack_trace_borrowed", Json::b(true)),
    ])
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
    let creation_ids = client_state.borrow().unique_ids_returned.clone();
    let mut outcomes = vec![pass(
        "inspector-client-callbacks/generate_unique_id",
        Json::obj(vec![
            ("calls_during_create", Json::i(creation_ids.len() as i64)),
            (
                "returned",
                Json::arr(creation_ids.into_iter().map(Json::i).collect()),
            ),
        ]),
    )];

    let channel_state = Rc::new(RefCell::new(ChannelState::default()));
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    inspector.context_created(
        context,
        7,
        StringView::from(&b"client-callbacks"[..]),
        StringView::from(&br#"{"isDefault":true}"#[..]),
    );
    let session = inspector.connect(
        7,
        Channel::new(Box::new(RecordingChannel(channel_state.clone()))),
        StringView::from(&b"{}"[..]),
        V8InspectorClientTrustLevel::FullyTrusted,
    );

    let wait_first = dispatch(
        &session,
        &channel_state,
        1,
        r#"{"id":1,"method":"Runtime.runIfWaitingForDebugger"}"#,
    );
    let wait_second = dispatch(
        &session,
        &channel_state,
        2,
        r#"{"id":2,"method":"Runtime.runIfWaitingForDebugger"}"#,
    );
    outcomes.push(pass(
        "inspector-client-callbacks/run_if_waiting_for_debugger",
        Json::obj(vec![
            (
                "responses_success",
                Json::arr(vec![
                    Json::b(!wait_first.contains("\"error\"")),
                    Json::b(!wait_second.contains("\"error\"")),
                ]),
            ),
            (
                "callback_groups",
                Json::arr(
                    client_state
                        .borrow()
                        .waiting_groups
                        .iter()
                        .map(|group| Json::i(i64::from(*group)))
                        .collect(),
                ),
            ),
        ]),
    ));

    let debugger_enable = dispatch(
        &session,
        &channel_state,
        3,
        r#"{"id":3,"method":"Debugger.enable"}"#,
    );
    let resources_start = client_state.borrow().resources.len();
    let mapped_url = parsed_url(scope, &channel_state, "1", "mapped.js");
    let plain_url = parsed_url(scope, &channel_state, "2", "plain.js");
    let nul_url = parsed_url(scope, &channel_state, "3", "nul\0name.js");
    let source_url = parsed_url(
        scope,
        &channel_state,
        "4\n//# sourceURL=source-override.js",
        "mapped.js",
    );
    let resource_calls: Vec<_> = client_state.borrow().resources[resources_start..]
        .iter()
        .map(resource_json)
        .collect();
    outcomes.push(pass(
        "inspector-client-callbacks/resource_name_to_url",
        Json::obj(vec![
            (
                "debugger_enabled",
                Json::b(!debugger_enable.contains("\"error\"")),
            ),
            ("mapped_url", Json::s(&mapped_url)),
            ("plain_url", Json::s(&plain_url)),
            ("nul_url", Json::s(&nul_url)),
            ("source_url_override", Json::s(&source_url)),
            ("callback_calls", Json::arr(resource_calls)),
        ]),
    ));

    let console_start = client_state.borrow().console.len();
    compile_and_run(scope, "console.log('one');", "console.js");
    compile_and_run(scope, "console.error('two');", "console.js");
    compile_and_run(
        scope,
        "function traceClient(){console.trace('three');}\ntraceClient();",
        "console.js",
    );
    compile_and_run(scope, "console.log('a\\0b');", "console.js");
    compile_and_run(scope, "console.warn('Ω');", "console.js");
    let console: Vec<_> = client_state.borrow().console[console_start..]
        .iter()
        .map(console_json)
        .collect();
    outcomes.push(pass(
        "inspector-client-callbacks/console_api_message",
        Json::obj(vec![("callbacks", Json::arr(console))]),
    ));

    let drops_before = client_state.borrow().drops;
    let total_unique_ids = client_state.borrow().unique_ids_returned.clone();
    drop(session);
    inspector.context_destroyed(context);
    drop(inspector);
    outcomes.push(pass(
        "inspector-client-callbacks/client_lifecycle",
        Json::obj(vec![
            ("drops_before_inspector", Json::i(drops_before as i64)),
            (
                "drops_after_inspector",
                Json::i(client_state.borrow().drops as i64),
            ),
            (
                "all_unique_ids_returned",
                Json::arr(total_unique_ids.into_iter().map(Json::i).collect()),
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
    if kind == PanicKind::Generate {
        unreachable!();
    }
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
    match kind {
        PanicKind::Generate => unreachable!(),
        PanicKind::Waiting => {
            session.dispatch_protocol_message(StringView::from(
                &br#"{"id":1,"method":"Runtime.runIfWaitingForDebugger"}"#[..],
            ));
        }
        PanicKind::Resource => {
            session.dispatch_protocol_message(StringView::from(
                &br#"{"id":1,"method":"Debugger.enable"}"#[..],
            ));
            compile_and_run(scope, "1", "panic-resource.js");
        }
        PanicKind::Console => {
            compile_and_run(scope, "console.log('panic');", "panic-console.js");
        }
    }
}

fn main() -> std::process::ExitCode {
    let mode = std::env::args().nth(1);
    let panic_kind = match mode.as_deref() {
        Some("mode=panic-generate") => Some(PanicKind::Generate),
        Some("mode=panic-waiting") => Some(PanicKind::Waiting),
        Some("mode=panic-resource") => Some(PanicKind::Resource),
        Some("mode=panic-console") => Some(PanicKind::Console),
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
