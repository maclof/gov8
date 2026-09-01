//! V8 Inspector runtime-event and async-task oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. This slice covers
//! inspector idle transitions, async-task lifecycle controls,
//! `create_stack_trace`, and `exception_thrown`.

use std::cell::{Cell, RefCell};
use std::ffi::c_void;
use std::rc::Rc;

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

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

#[derive(Default)]
struct ClientState {
    pause_groups: Vec<i32>,
}

#[derive(Clone)]
struct RecordingClient(Rc<RefCell<ClientState>>);

impl v8::inspector::V8InspectorClientImpl for RecordingClient {
    fn run_message_loop_on_pause(&self, context_group_id: i32) {
        self.0.borrow_mut().pause_groups.push(context_group_id);
    }
}

#[derive(Copy, Clone)]
struct ScheduleConfig {
    inspector: *const v8::inspector::V8Inspector,
    task: *const c_void,
    recurring: bool,
    name: u8,
}

thread_local! {
    static SCHEDULE_CONFIG: Cell<Option<ScheduleConfig>> = const { Cell::new(None) };
}

fn native_schedule(
    _scope: &mut v8::PinScope,
    _args: v8::FunctionCallbackArguments,
    _rv: v8::ReturnValue,
) {
    SCHEDULE_CONFIG.with(|slot| {
        let config = slot.get().expect("schedule configuration");
        let name: &[u8] = match config.name {
            1 => b"one-shot-task",
            2 => b"recurring-task",
            3 => b"cleared-task",
            _ => unreachable!(),
        };
        // SAFETY: the boxed inspector and task identity both outlive every
        // schedule/start/finish/cancel operation in this oracle.
        unsafe {
            (*config.inspector).async_task_scheduled(
                v8::inspector::StringView::from(name),
                config.task,
                config.recurring,
            );
        }
    });
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

fn run_script<'s>(
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

fn paused_notification(
    scope: &v8::PinScope<'_, '_>,
    channel: &Rc<RefCell<ChannelState>>,
    client: &Rc<RefCell<ClientState>>,
    child_name: &str,
    resource_name: &str,
) -> Json {
    let notification_start = channel.borrow().notifications.len();
    let callback_start = client.borrow().pause_groups.len();
    run_script(
        scope,
        &format!("function {child_name}(){{debugger;}} {child_name}();"),
        resource_name,
    );
    let notifications = channel.borrow().notifications[notification_start..].to_vec();
    let paused: Vec<_> = notifications
        .iter()
        .filter(|message| message.contains("\"method\":\"Debugger.paused\""))
        .collect();
    assert_eq!(paused.len(), 1, "{notifications:?}");
    let message = paused[0];
    Json::obj(vec![
        (
            "pause_callbacks",
            Json::i((client.borrow().pause_groups.len() - callback_start) as i64),
        ),
        (
            "callback_group",
            client
                .borrow()
                .pause_groups
                .last()
                .map_or(Json::Null, |group| Json::i(i64::from(*group))),
        ),
        (
            "child_frame",
            Json::b(message.contains(&format!("\"functionName\":\"{child_name}\""))),
        ),
        (
            "async_stack_present",
            Json::b(message.contains("\"asyncStackTrace\"")),
        ),
        (
            "one_shot_description",
            Json::b(message.contains("\"description\":\"one-shot-task\"")),
        ),
        (
            "recurring_description",
            Json::b(message.contains("\"description\":\"recurring-task\"")),
        ),
        (
            "cleared_description",
            Json::b(message.contains("\"description\":\"cleared-task\"")),
        ),
        (
            "schedule_parent_frame",
            Json::b(message.contains("\"functionName\":\"scheduleParent\"")),
        ),
    ])
}

fn schedule_from_js(
    scope: &v8::PinScope<'_, '_>,
    inspector: *const v8::inspector::V8Inspector,
    task: *const c_void,
    recurring: bool,
    name: u8,
) {
    SCHEDULE_CONFIG.with(|slot| {
        slot.set(Some(ScheduleConfig {
            inspector,
            task,
            recurring,
            name,
        }));
    });
    run_script(
        scope,
        "function scheduleParent(){nativeSchedule();} scheduleParent();",
        "schedule-parent.js",
    );
    SCHEDULE_CONFIG.with(|slot| slot.set(None));
}

fn json_i64_field(input: &str, key: &str) -> Option<i64> {
    let needle = format!("\"{key}\":");
    let rest = input.get(input.find(&needle)? + needle.len()..)?;
    let end = rest
        .find(|ch: char| !ch.is_ascii_digit() && ch != '-')
        .unwrap_or(rest.len());
    rest.get(..end)?.parse().ok()
}

fn run_oracle() -> Vec<CheckOutcome> {
    use v8::inspector::{
        Channel, StringView, V8Inspector, V8InspectorClient, V8InspectorClientTrustLevel,
    };

    let isolate = &mut v8::Isolate::new(Default::default());
    let client_state = Rc::new(RefCell::new(ClientState::default()));
    let inspector = Box::new(V8Inspector::create(
        isolate,
        V8InspectorClient::new(Box::new(RecordingClient(client_state.clone()))),
    ));
    let inspector_ptr: *const V8Inspector = &*inspector;
    let channel_state = Rc::new(RefCell::new(ChannelState::default()));
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    inspector.idle_finished();
    inspector.idle_started();
    inspector.idle_started();
    let idle_script_value = run_script(scope, "6 * 7", "idle.js").integer_value(scope);
    inspector.idle_finished();
    inspector.idle_finished();
    let mut outcomes = vec![pass(
        "inspector-runtime-events/idle_transitions",
        Json::obj(vec![
            ("finish_before_start_returned", Json::b(true)),
            ("repeated_start_returned", Json::b(true)),
            (
                "script_during_idle",
                idle_script_value.map_or(Json::Null, Json::i),
            ),
            ("repeated_finish_returned", Json::b(true)),
        ]),
    )];

    let native = v8::Function::new(scope, native_schedule).unwrap();
    assert_eq!(
        context.global(scope).set(
            scope,
            v8::String::new(scope, "nativeSchedule").unwrap().into(),
            native.into(),
        ),
        Some(true)
    );
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
    let debugger_enable = dispatch(
        &session,
        &channel_state,
        1,
        r#"{"id":1,"method":"Debugger.enable"}"#,
    );
    let async_depth = dispatch(
        &session,
        &channel_state,
        2,
        r#"{"id":2,"method":"Debugger.setAsyncCallStackDepth","params":{"maxDepth":8}}"#,
    );

    let one_shot_id = Box::new(11_i32);
    let one_shot_ptr = (&*one_shot_id as *const i32).cast::<c_void>();
    schedule_from_js(scope, inspector_ptr, one_shot_ptr, false, 1);
    // SAFETY: one_shot_id remains boxed until after this complete lifecycle.
    unsafe { inspector.async_task_started(one_shot_ptr) };
    let one_shot_first = paused_notification(
        scope,
        &channel_state,
        &client_state,
        "childOne",
        "child-one.js",
    );
    // SAFETY: this matches the active task identity.
    unsafe { inspector.async_task_finished(one_shot_ptr) };
    // A completed non-recurring task loses its captured parent.
    unsafe { inspector.async_task_started(one_shot_ptr) };
    let one_shot_second = paused_notification(
        scope,
        &channel_state,
        &client_state,
        "childTwo",
        "child-two.js",
    );
    unsafe { inspector.async_task_finished(one_shot_ptr) };
    outcomes.push(pass(
        "inspector-runtime-events/async_one_shot",
        Json::obj(vec![
            (
                "debugger_enabled",
                Json::b(!debugger_enable.contains("\"error\"")),
            ),
            (
                "async_depth_set",
                Json::b(!async_depth.contains("\"error\"")),
            ),
            ("first_start", one_shot_first),
            ("after_finish_restart", one_shot_second),
        ]),
    ));

    let recurring_id = Box::new(22_i32);
    let recurring_ptr = (&*recurring_id as *const i32).cast::<c_void>();
    schedule_from_js(scope, inspector_ptr, recurring_ptr, true, 2);
    let mut recurring_runs = Vec::new();
    for (child, resource) in [
        ("recurringOne", "recurring-one.js"),
        ("recurringTwo", "recurring-two.js"),
    ] {
        unsafe { inspector.async_task_started(recurring_ptr) };
        recurring_runs.push(paused_notification(
            scope,
            &channel_state,
            &client_state,
            child,
            resource,
        ));
        unsafe { inspector.async_task_finished(recurring_ptr) };
    }
    unsafe { inspector.async_task_canceled(recurring_ptr) };
    unsafe { inspector.async_task_started(recurring_ptr) };
    let after_cancel = paused_notification(
        scope,
        &channel_state,
        &client_state,
        "recurringCanceled",
        "recurring-canceled.js",
    );
    unsafe { inspector.async_task_finished(recurring_ptr) };
    outcomes.push(pass(
        "inspector-runtime-events/async_recurring_cancel",
        Json::obj(vec![
            ("runs_before_cancel", Json::arr(recurring_runs)),
            ("after_cancel", after_cancel),
        ]),
    ));

    let cleared_id = Box::new(33_i32);
    let cleared_ptr = (&*cleared_id as *const i32).cast::<c_void>();
    schedule_from_js(scope, inspector_ptr, cleared_ptr, true, 3);
    inspector.all_async_tasks_canceled();
    unsafe { inspector.async_task_started(cleared_ptr) };
    let after_all_canceled = paused_notification(
        scope,
        &channel_state,
        &client_state,
        "allCanceled",
        "all-canceled.js",
    );
    unsafe { inspector.async_task_finished(cleared_ptr) };
    unsafe {
        inspector.async_task_scheduled(StringView::empty(), std::ptr::null(), false);
        inspector.async_task_started(std::ptr::null());
        inspector.async_task_finished(std::ptr::null());
        inspector.async_task_canceled(std::ptr::null());
    }
    outcomes.push(pass(
        "inspector-runtime-events/async_all_canceled_and_null",
        Json::obj(vec![
            ("after_all_canceled", after_all_canceled),
            ("null_identity_calls_returned", Json::b(true)),
        ]),
    ));

    let runtime_enable = dispatch(
        &session,
        &channel_state,
        3,
        r#"{"id":3,"method":"Runtime.enable"}"#,
    );
    let exception: v8::Local<v8::Value> = run_script(
        scope,
        "function outerEvent(){return innerEvent();} function innerEvent(){return new Error('oracle boom');} outerEvent();",
        "event-source.js",
    );
    let empty_inspector_trace = inspector.create_stack_trace(None);
    let empty_trace_non_null = empty_inspector_trace.as_ref().is_some();
    let v8_trace = v8::Exception::get_stack_trace(scope, exception);
    let inspector_trace = inspector.create_stack_trace(v8_trace);
    let created_trace_non_null = inspector_trace.as_ref().is_some();
    let orphan_trace = inspector.create_stack_trace(v8_trace);
    let notification_start = channel_state.borrow().notifications.len();
    let exception_id = inspector.exception_thrown(
        context,
        StringView::from(&b"oracle exception"[..]),
        exception,
        StringView::from(&b"oracle detailed"[..]),
        StringView::from(&b"embedder://event"[..]),
        7,
        9,
        inspector_trace,
        42,
    );
    let new_notifications = channel_state.borrow().notifications[notification_start..].to_vec();
    let exception_notifications: Vec<_> = new_notifications
        .iter()
        .filter(|message| message.contains("\"method\":\"Runtime.exceptionThrown\""))
        .collect();
    assert_eq!(exception_notifications.len(), 1, "{new_notifications:?}");
    let exception_message = exception_notifications[0];
    outcomes.push(pass(
        "inspector-runtime-events/stack_trace_and_exception",
        Json::obj(vec![
            (
                "runtime_enabled",
                Json::b(!runtime_enable.contains("\"error\"")),
            ),
            ("none_input_trace_non_null", Json::b(empty_trace_non_null)),
            ("v8_trace_present", Json::b(v8_trace.is_some())),
            ("inspector_trace_non_null", Json::b(created_trace_non_null)),
            ("exception_id", Json::i(i64::from(exception_id))),
            (
                "notification_count",
                Json::i(exception_notifications.len() as i64),
            ),
            (
                "line_number",
                json_i64_field(exception_message, "lineNumber").map_or(Json::Null, Json::i),
            ),
            (
                "column_number",
                json_i64_field(exception_message, "columnNumber").map_or(Json::Null, Json::i),
            ),
            (
                "script_id_42",
                Json::b(exception_message.contains("\"scriptId\":\"42\"")),
            ),
            (
                "text_uses_detailed_message",
                Json::b(exception_message.contains("\"text\":\"oracle detailed\"")),
            ),
            (
                "original_message_hidden",
                Json::b(!exception_message.contains("oracle exception")),
            ),
            (
                "url",
                Json::b(exception_message.contains("\"url\":\"embedder://event\"")),
            ),
            (
                "exception_object_present",
                Json::b(exception_message.contains("\"exception\":")),
            ),
            (
                "inner_frame",
                Json::b(exception_message.contains("innerEvent")),
            ),
            (
                "outer_frame",
                Json::b(exception_message.contains("outerEvent")),
            ),
        ]),
    ));

    let no_stack_notification_start = channel_state.borrow().notifications.len();
    let no_stack_id = inspector.exception_thrown(
        context,
        StringView::from(&b"without stack"[..]),
        v8::undefined(scope).into(),
        StringView::empty(),
        StringView::empty(),
        0,
        0,
        empty_inspector_trace,
        0,
    );
    let no_stack_notifications =
        channel_state.borrow().notifications[no_stack_notification_start..].to_vec();
    let no_stack: Vec<_> = no_stack_notifications
        .iter()
        .filter(|message| message.contains("\"method\":\"Runtime.exceptionThrown\""))
        .collect();
    assert_eq!(no_stack.len(), 1, "{no_stack_notifications:?}");
    outcomes.push(pass(
        "inspector-runtime-events/exception_without_stack",
        Json::obj(vec![
            ("exception_id", Json::i(i64::from(no_stack_id))),
            (
                "text_uses_message",
                Json::b(no_stack[0].contains("\"text\":\"without stack\"")),
            ),
            (
                "stack_trace_present",
                Json::b(no_stack[0].contains("\"stackTrace\":")),
            ),
            (
                "line_number",
                json_i64_field(no_stack[0], "lineNumber").map_or(Json::Null, Json::i),
            ),
            (
                "column_number",
                json_i64_field(no_stack[0], "columnNumber").map_or(Json::Null, Json::i),
            ),
        ]),
    ));

    inspector.context_destroyed(context);
    let notifications_before_unregistered = channel_state.borrow().notifications.len();
    let unregistered_id = inspector.exception_thrown(
        context,
        StringView::from(&b"after destroy"[..]),
        exception,
        StringView::empty(),
        StringView::empty(),
        1,
        1,
        orphan_trace,
        0,
    );
    outcomes.push(pass(
        "inspector-runtime-events/unregistered_exception",
        Json::obj(vec![
            ("exception_id", Json::i(i64::from(unregistered_id))),
            (
                "new_notifications",
                Json::i(
                    (channel_state.borrow().notifications.len() - notifications_before_unregistered)
                        as i64,
                ),
            ),
        ]),
    ));
    drop(session);
    outcomes
}

fn main() -> std::process::ExitCode {
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
