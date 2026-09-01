//! CRDTP callback and dispatcher oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This slice drives
//! FrontendChannel response callbacks, UberDispatcher, DomainDispatcher,
//! DomainDispatcherHandle, and Dispatchable fallthrough callbacks.

use std::cell::{Cell, RefCell};
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

fn cbor_json(bytes: &[u8]) -> String {
    String::from_utf8(v8::crdtp::cbor_to_json(bytes).expect("valid CRDTP CBOR"))
        .expect("CRDTP JSON is UTF-8")
}

fn hex(bytes: &[u8]) -> String {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        output.push(char::from(DIGITS[usize::from(byte >> 4)]));
        output.push(char::from(DIGITS[usize::from(byte & 0x0f)]));
    }
    output
}

struct ChannelResponse {
    call_id: i32,
    json: String,
}

#[derive(Default)]
struct ChannelState {
    responses: Vec<ChannelResponse>,
    notifications: Vec<String>,
    flushes: usize,
    drops: usize,
}

struct RecordingChannel(Rc<RefCell<ChannelState>>);

impl v8::crdtp::FrontendChannelImpl for RecordingChannel {
    fn send_protocol_response(&mut self, call_id: i32, message: v8::crdtp::Serializable) {
        let bytes = message.to_bytes();
        self.0.borrow_mut().responses.push(ChannelResponse {
            call_id,
            json: cbor_json(&bytes),
        });
    }

    fn send_protocol_notification(&mut self, message: v8::crdtp::Serializable) {
        let bytes = message.to_bytes();
        self.0.borrow_mut().notifications.push(cbor_json(&bytes));
    }

    fn flush_protocol_notifications(&mut self) {
        self.0.borrow_mut().flushes += 1;
    }
}

impl Drop for RecordingChannel {
    fn drop(&mut self) {
        self.0.borrow_mut().drops += 1;
    }
}

struct DomainCall {
    domain: &'static str,
    command: String,
    call_id: i32,
    associated_data: Vec<u8>,
    response_delivered_before_return: bool,
    handled: bool,
}

#[derive(Default)]
struct DomainState {
    calls: Vec<DomainCall>,
    drops: usize,
}

struct RecordingDomain {
    name: &'static str,
    state: Rc<RefCell<DomainState>>,
    channel: Rc<RefCell<ChannelState>>,
}

impl v8::crdtp::DomainDispatcherImpl for RecordingDomain {
    fn dispatch(
        &mut self,
        command: &[u8],
        dispatchable: &v8::crdtp::Dispatchable,
        handle: &v8::crdtp::DomainDispatcherHandle,
    ) -> bool {
        let command = std::str::from_utf8(command).unwrap().to_owned();
        let responses_before = self.channel.borrow().responses.len();
        let handled = match command.as_str() {
            "ok" => {
                handle.send_response(
                    dispatchable.call_id(),
                    v8::crdtp::DispatchResponse::success(),
                    None,
                );
                true
            }
            "withResult" => {
                handle.send_response(
                    dispatchable.call_id(),
                    v8::crdtp::DispatchResponse::success(),
                    Some(v8::crdtp::create_notification("Nested.result", None)),
                );
                true
            }
            "bad" => {
                handle.send_response(
                    dispatchable.call_id(),
                    v8::crdtp::DispatchResponse::invalid_params("bad input"),
                    None,
                );
                true
            }
            _ => false,
        };
        let delivered = self.channel.borrow().responses.len() > responses_before;
        self.state.borrow_mut().calls.push(DomainCall {
            domain: self.name,
            command,
            call_id: dispatchable.call_id(),
            associated_data: dispatchable.associated_data(),
            response_delivered_before_return: delivered,
            handled,
        });
        handled
    }
}

impl Drop for RecordingDomain {
    fn drop(&mut self) {
        self.state.borrow_mut().drops += 1;
    }
}

struct DropMarker(Rc<Cell<usize>>);

impl Drop for DropMarker {
    fn drop(&mut self) {
        self.0.set(self.0.get() + 1);
    }
}

struct FallthroughCall {
    call_id: i32,
    method: Vec<u8>,
    message_json: String,
    associated_data: Vec<u8>,
}

fn dispatch_json(
    dispatcher: &mut v8::crdtp::UberDispatcher,
    message: &str,
) -> v8::crdtp::OwnedDispatchable {
    let cbor = v8::crdtp::json_to_cbor(message.as_bytes()).unwrap();
    let mut dispatchable = v8::crdtp::Dispatchable::new(&cbor);
    assert!(dispatchable.ok());
    dispatcher.dispatch(&mut dispatchable);
    dispatchable
}

fn response_json(response: &ChannelResponse) -> Json {
    Json::obj(vec![
        ("call_id", Json::i(i64::from(response.call_id))),
        ("json", Json::s(&response.json)),
    ])
}

fn domain_call_json(call: &DomainCall) -> Json {
    Json::obj(vec![
        ("domain", Json::s(call.domain)),
        ("command", Json::s(&call.command)),
        ("call_id", Json::i(i64::from(call.call_id))),
        ("associated_data_hex", Json::s(&hex(&call.associated_data))),
        (
            "response_delivered_before_return",
            Json::b(call.response_delivered_before_return),
        ),
        ("handled", Json::b(call.handled)),
    ])
}

fn run_oracle() -> Vec<CheckOutcome> {
    let channel_state = Rc::new(RefCell::new(ChannelState::default()));
    let channel =
        v8::crdtp::FrontendChannel::new(Box::new(RecordingChannel(channel_state.clone())));
    let mut dispatcher = v8::crdtp::UberDispatcher::new(&channel);
    let domain_state = Rc::new(RefCell::new(DomainState::default()));

    for (domain, name) in [
        (String::from("Alpha"), "Alpha"),
        (String::from("Beta"), "Beta"),
    ] {
        v8::crdtp::DomainDispatcher::wire(
            &mut dispatcher,
            &domain,
            Box::new(RecordingDomain {
                name,
                state: domain_state.clone(),
                channel: channel_state.clone(),
            }),
        );
        // `DomainDispatcher::wire` must retain its own domain-name bytes.
        drop(domain);
    }

    let response_start = channel_state.borrow().responses.len();
    let call_start = domain_state.borrow().calls.len();
    drop(dispatch_json(
        &mut dispatcher,
        r#"{"id":1,"method":"Alpha.ok","params":{}}"#,
    ));
    drop(dispatch_json(
        &mut dispatcher,
        r#"{"id":2,"method":"Alpha.bad","params":{}}"#,
    ));
    drop(dispatch_json(
        &mut dispatcher,
        r#"{"id":3,"method":"Beta.withResult","params":{}}"#,
    ));
    let routing_responses: Vec<_> = channel_state.borrow().responses[response_start..]
        .iter()
        .map(response_json)
        .collect();
    let routing_calls: Vec<_> = domain_state.borrow().calls[call_start..]
        .iter()
        .map(domain_call_json)
        .collect();
    let mut outcomes = vec![pass(
        "crdtp-dispatcher/known_multiple_domains",
        Json::obj(vec![
            ("responses", Json::arr(routing_responses)),
            ("callbacks", Json::arr(routing_calls)),
        ]),
    )];

    let response_start = channel_state.borrow().responses.len();
    let call_start = domain_state.borrow().calls.len();
    drop(dispatch_json(
        &mut dispatcher,
        r#"{"id":4,"method":"Alpha.unknown","params":{}}"#,
    ));
    drop(dispatch_json(
        &mut dispatcher,
        r#"{"id":5,"method":"Gamma.ok","params":{}}"#,
    ));
    let unknown_responses: Vec<_> = channel_state.borrow().responses[response_start..]
        .iter()
        .map(response_json)
        .collect();
    let unknown_calls: Vec<_> = domain_state.borrow().calls[call_start..]
        .iter()
        .map(domain_call_json)
        .collect();
    outcomes.push(pass(
        "crdtp-dispatcher/unknown_routing",
        Json::obj(vec![
            ("responses", Json::arr(unknown_responses)),
            ("callbacks", Json::arr(unknown_calls)),
            (
                "responses_available_when_dispatch_returned",
                Json::b(channel_state.borrow().responses.len() == response_start + 2),
            ),
        ]),
    ));

    let handler_fallthrough_calls = Rc::new(RefCell::new(Vec::<FallthroughCall>::new()));
    let handler_fallthrough_drops = Rc::new(Cell::new(0));
    let response_start = channel_state.borrow().responses.len();
    let call_start = domain_state.borrow().calls.len();
    let associated_data = vec![0x00, 0xff, b'x', 0x00];
    let cbor =
        v8::crdtp::json_to_cbor(br#"{"id":6,"method":"Alpha.ok","params":{"from":"associated"}}"#)
            .unwrap();
    let callback_calls = handler_fallthrough_calls.clone();
    let marker = DropMarker(handler_fallthrough_drops.clone());
    let mut with_associated = v8::crdtp::Dispatchable::new_with_fallthrough(
        &cbor,
        &associated_data,
        move |call_id, method, message, data| {
            let _keep_marker_alive = &marker;
            callback_calls.borrow_mut().push(FallthroughCall {
                call_id,
                method: method.to_vec(),
                message_json: cbor_json(message),
                associated_data: data.to_vec(),
            });
        },
    );
    drop(cbor);
    drop(associated_data);
    let accessor_before = with_associated.associated_data();
    dispatcher.dispatch(&mut with_associated);
    let accessor_after = with_associated.associated_data();
    let response = {
        let channel = channel_state.borrow();
        response_json(&channel.responses[response_start])
    };
    let callback = {
        let domain = domain_state.borrow();
        domain_call_json(&domain.calls[call_start])
    };
    let callback_invocations = handler_fallthrough_calls.borrow().len();
    let drops_before_dispatchable = handler_fallthrough_drops.get();
    drop(with_associated);
    outcomes.push(pass(
        "crdtp-dispatcher/associated_data_handler",
        Json::obj(vec![
            ("accessor_before_hex", Json::s(&hex(&accessor_before))),
            ("accessor_after_hex", Json::s(&hex(&accessor_after))),
            ("handler", callback),
            ("response", response),
            (
                "fallthrough_callback_invocations",
                Json::i(callback_invocations as i64),
            ),
            (
                "callback_drops_before_dispatchable",
                Json::i(drops_before_dispatchable as i64),
            ),
            (
                "callback_drops_after_dispatchable",
                Json::i(handler_fallthrough_drops.get() as i64),
            ),
            ("producer_buffers_dropped", Json::b(true)),
        ]),
    ));

    let fallthrough_calls = Rc::new(RefCell::new(Vec::<FallthroughCall>::new()));
    let fallthrough_drops = Rc::new(Cell::new(0));
    let response_start = channel_state.borrow().responses.len();
    let cbor =
        v8::crdtp::json_to_cbor(br#"{"id":7,"method":"Gamma.missing","params":{"answer":42}}"#)
            .unwrap();
    let expected_message_json = cbor_json(&cbor);
    let associated_data = b"request metadata\0owned".to_vec();
    let callback_calls = fallthrough_calls.clone();
    let marker = DropMarker(fallthrough_drops.clone());
    let mut fallthrough = v8::crdtp::Dispatchable::new_with_fallthrough(
        &cbor,
        &associated_data,
        move |call_id, method, message, data| {
            let _keep_marker_alive = &marker;
            callback_calls.borrow_mut().push(FallthroughCall {
                call_id,
                method: method.to_vec(),
                message_json: cbor_json(message),
                associated_data: data.to_vec(),
            });
        },
    );
    drop(cbor);
    drop(associated_data);
    let accessor = fallthrough.associated_data();
    dispatcher.dispatch(&mut fallthrough);
    let response_count = channel_state.borrow().responses.len() - response_start;
    let call_json = {
        let calls = fallthrough_calls.borrow();
        assert_eq!(calls.len(), 1);
        let call = &calls[0];
        Json::obj(vec![
            ("call_id", Json::i(i64::from(call.call_id))),
            (
                "method",
                Json::s(std::str::from_utf8(&call.method).unwrap()),
            ),
            ("message_json", Json::s(&call.message_json)),
            (
                "message_matches_input",
                Json::b(call.message_json == expected_message_json),
            ),
            (
                "callback_associated_data_hex",
                Json::s(&hex(&call.associated_data)),
            ),
        ])
    };
    let drops_after_dispatch = fallthrough_drops.get();
    drop(fallthrough);
    outcomes.push(pass(
        "crdtp-dispatcher/fallthrough",
        Json::obj(vec![
            ("member_associated_data_hex", Json::s(&hex(&accessor))),
            ("callback", call_json),
            ("response_count", Json::i(response_count as i64)),
            (
                "callback_drops_after_dispatch",
                Json::i(drops_after_dispatch as i64),
            ),
            (
                "callback_drops_after_dispatchable",
                Json::i(fallthrough_drops.get() as i64),
            ),
            ("synchronous", Json::b(true)),
            ("producer_buffers_dropped", Json::b(true)),
        ]),
    ));

    let domain_drops_before = domain_state.borrow().drops;
    let channel_drops_before = channel_state.borrow().drops;
    drop(dispatcher);
    let domain_drops_after = domain_state.borrow().drops;
    let channel_drops_after_dispatcher = channel_state.borrow().drops;
    let notifications = channel_state.borrow().notifications.len();
    let flushes = channel_state.borrow().flushes;
    drop(channel);
    outcomes.push(pass(
        "crdtp-dispatcher/lifecycle",
        Json::obj(vec![
            ("wired_domains", Json::i(2)),
            (
                "domain_drops_before_dispatcher",
                Json::i(domain_drops_before as i64),
            ),
            (
                "domain_drops_after_dispatcher",
                Json::i(domain_drops_after as i64),
            ),
            (
                "channel_drops_before_dispatcher",
                Json::i(channel_drops_before as i64),
            ),
            (
                "channel_drops_after_dispatcher",
                Json::i(channel_drops_after_dispatcher as i64),
            ),
            (
                "channel_drops_after_channel",
                Json::i(channel_state.borrow().drops as i64),
            ),
            (
                "notifications_during_public_routes",
                Json::i(notifications as i64),
            ),
            ("flushes_during_public_routes", Json::i(flushes as i64)),
        ]),
    ));
    outcomes
}

struct NoopChannel;

impl v8::crdtp::FrontendChannelImpl for NoopChannel {
    fn send_protocol_response(&mut self, _call_id: i32, _message: v8::crdtp::Serializable) {}
    fn send_protocol_notification(&mut self, _message: v8::crdtp::Serializable) {}
    fn flush_protocol_notifications(&mut self) {}
}

struct PanicResponseChannel;

impl v8::crdtp::FrontendChannelImpl for PanicResponseChannel {
    fn send_protocol_response(&mut self, _call_id: i32, _message: v8::crdtp::Serializable) {
        panic!("CRDTP FrontendChannel response panic boundary");
    }
    fn send_protocol_notification(&mut self, _message: v8::crdtp::Serializable) {}
    fn flush_protocol_notifications(&mut self) {}
}

struct PanicDomain;

impl v8::crdtp::DomainDispatcherImpl for PanicDomain {
    fn dispatch(
        &mut self,
        _command: &[u8],
        _dispatchable: &v8::crdtp::Dispatchable,
        _handle: &v8::crdtp::DomainDispatcherHandle,
    ) -> bool {
        panic!("CRDTP DomainDispatcher panic boundary");
    }
}

struct PanicDropDomain;

impl v8::crdtp::DomainDispatcherImpl for PanicDropDomain {
    fn dispatch(
        &mut self,
        _command: &[u8],
        _dispatchable: &v8::crdtp::Dispatchable,
        _handle: &v8::crdtp::DomainDispatcherHandle,
    ) -> bool {
        false
    }
}

impl Drop for PanicDropDomain {
    fn drop(&mut self) {
        panic!("CRDTP DomainDispatcher drop panic boundary");
    }
}

struct PanicDropMarker;

impl Drop for PanicDropMarker {
    fn drop(&mut self) {
        panic!("CRDTP fallthrough drop panic boundary");
    }
}

fn panic_mode(mode: &str) {
    let channel: std::pin::Pin<Box<v8::crdtp::FrontendChannel>> = if mode == "panic-channel" {
        v8::crdtp::FrontendChannel::new(Box::new(PanicResponseChannel))
    } else {
        v8::crdtp::FrontendChannel::new(Box::new(NoopChannel))
    };
    let mut dispatcher = v8::crdtp::UberDispatcher::new(&channel);
    if mode == "panic-domain" {
        v8::crdtp::DomainDispatcher::wire(&mut dispatcher, "Panic", Box::new(PanicDomain));
        drop(dispatch_json(
            &mut dispatcher,
            r#"{"id":1,"method":"Panic.run","params":{}}"#,
        ));
    } else if mode == "panic-channel" {
        drop(dispatch_json(
            &mut dispatcher,
            r#"{"id":1,"method":"Missing.run","params":{}}"#,
        ));
    } else if mode == "panic-fallthrough" {
        let cbor =
            v8::crdtp::json_to_cbor(br#"{"id":1,"method":"Missing.run","params":{}}"#).unwrap();
        let mut dispatchable = v8::crdtp::Dispatchable::new_with_fallthrough(
            &cbor,
            b"data",
            |_call_id, _method, _message, _data| {
                panic!("CRDTP fallthrough callback panic boundary");
            },
        );
        dispatcher.dispatch(&mut dispatchable);
    } else if mode == "panic-domain-drop" {
        v8::crdtp::DomainDispatcher::wire(&mut dispatcher, "PanicDrop", Box::new(PanicDropDomain));
        drop(dispatcher);
    } else {
        let cbor =
            v8::crdtp::json_to_cbor(br#"{"id":1,"method":"Known.run","params":{}}"#).unwrap();
        let marker = PanicDropMarker;
        let dispatchable = v8::crdtp::Dispatchable::new_with_fallthrough(
            &cbor,
            b"data",
            move |_call_id, _method, _message, _data| {
                let _keep_marker_alive = &marker;
            },
        );
        drop(dispatchable);
    }
}

fn main() -> std::process::ExitCode {
    let mode = std::env::args().nth(1);
    if let Some(
        mode @ ("mode=panic-domain"
        | "mode=panic-channel"
        | "mode=panic-fallthrough"
        | "mode=panic-domain-drop"
        | "mode=panic-fallthrough-drop"),
    ) = mode.as_deref()
    {
        suppress_windows_fatal_dialogs();
        panic_mode(mode.trim_start_matches("mode="));
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
