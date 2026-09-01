//go:build windows && amd64

package inspectorruntimeeventsconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gov8 "gov8"
)

var expectedChecks = []string{
	"inspector-runtime-events/idle_transitions",
	"inspector-runtime-events/async_one_shot",
	"inspector-runtime-events/async_recurring_cancel",
	"inspector-runtime-events/async_all_canceled_and_null",
	"inspector-runtime-events/stack_trace_and_exception",
	"inspector-runtime-events/exception_without_stack",
	"inspector-runtime-events/unregistered_exception",
}

type fixtureLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

type channelResponse struct {
	callID  int32
	message string
}

type recordingChannel struct {
	responses     []channelResponse
	notifications []string
}

func (c *recordingChannel) SendResponse(callID int32, message *gov8.InspectorStringBuffer) {
	c.responses = append(c.responses, channelResponse{callID: callID, message: message.StringView().String()})
}
func (c *recordingChannel) SendNotification(message *gov8.InspectorStringBuffer) {
	c.notifications = append(c.notifications, message.StringView().String())
}
func (*recordingChannel) FlushProtocolNotifications() {}

type recordingClient struct{ pauseGroups []int32 }

func (c *recordingClient) RunMessageLoopOnPause(group int32) {
	c.pauseGroups = append(c.pauseGroups, group)
}
func (*recordingClient) QuitMessageLoopOnPause() {}

type runtimeState struct {
	iso               *gov8.Isolate
	ctx               *gov8.Context
	scope             *gov8.Scope
	entered           *gov8.ContextScope
	inspector         *gov8.Inspector
	session           *gov8.InspectorSession
	contextRegistered bool
	channel           *recordingChannel
	client            *recordingClient
}

type scheduleConfig struct {
	task      gov8.InspectorAsyncTaskID
	recurring bool
	name      string
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures",
		"conformance-inspector-runtime-events-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	result := make(map[string]fixtureLine, len(expectedChecks))
	checkIndex := 0
	seenSummary := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line struct {
			fixtureLine
			Summary *struct {
				Total  int `json:"total"`
				Passed int `json:"passed"`
				Failed int `json:"failed"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Check != "" {
			if seenSummary {
				t.Fatalf("fixture check %q appears after summary", line.Check)
			}
			if checkIndex >= len(expectedChecks) {
				t.Fatalf("unexpected extra fixture check %q", line.Check)
			}
			if line.Check != expectedChecks[checkIndex] {
				t.Fatalf("fixture check %d = %q, want %q", checkIndex, line.Check, expectedChecks[checkIndex])
			}
			if _, duplicate := result[line.Check]; duplicate {
				t.Fatalf("duplicate fixture check %q", line.Check)
			}
			if !line.OK {
				t.Fatalf("fixture check %q is not successful", line.Check)
			}
			result[line.Check] = line.fixtureLine
			checkIndex++
			continue
		}
		if line.Summary != nil {
			if seenSummary {
				t.Fatal("duplicate fixture summary")
			}
			seenSummary = true
			if line.Summary.Total != len(expectedChecks) ||
				line.Summary.Passed != len(expectedChecks) ||
				line.Summary.Failed != 0 {
				t.Fatalf("fixture summary = %+v", *line.Summary)
			}
			continue
		}
		t.Fatalf("fixture contains unknown JSONL record: %s", scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if checkIndex != len(expectedChecks) || len(result) != len(expectedChecks) || !seenSummary {
		t.Fatalf("fixture completeness = checks %d/%d, summary %v", checkIndex, len(expectedChecks), seenSummary)
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok || !want.OK {
		t.Fatalf("missing or failed fixture check %q", id)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

func newRuntime(t *testing.T) *runtimeState {
	t.Helper()
	r := &runtimeState{channel: &recordingChannel{}, client: &recordingClient{}}
	var err error
	r.iso, err = gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	r.inspector, err = gov8.NewInspectorWithClient(r.iso, r.client)
	if err != nil {
		t.Fatal(err)
	}
	r.ctx, err = r.iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	r.scope, err = r.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	r.entered, err = r.ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *runtimeState) close(t *testing.T) {
	t.Helper()
	if r.session != nil {
		if err := r.session.Close(); err != nil {
			t.Error(err)
		}
		r.session = nil
	}
	if r.contextRegistered {
		if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
			t.Error(err)
		} else {
			r.contextRegistered = false
		}
	}
	if r.entered != nil {
		if err := r.entered.Close(); err != nil {
			t.Error(err)
		}
		r.entered = nil
	}
	if r.scope != nil {
		if err := r.scope.Close(); err != nil {
			t.Error(err)
		}
		r.scope = nil
	}
	if r.ctx != nil {
		if err := r.ctx.Close(); err != nil {
			t.Error(err)
		}
		r.ctx = nil
	}
	if r.inspector != nil {
		if err := r.inspector.Close(); err != nil {
			t.Error(err)
		}
		r.inspector = nil
	}
	if r.iso != nil {
		if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
			t.Error(err)
		}
		if err := r.iso.Close(); err != nil {
			t.Error(err)
		}
		r.iso = nil
	}
}

func (r *runtimeState) runScript(t *testing.T, source, resource string) gov8.Value {
	t.Helper()
	script, err := r.ctx.CompileWithOrigin(r.scope, source, &gov8.Origin{ResourceName: resource}, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, runErr := script.Run(r.scope, nil)
	closeErr := script.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return value
}

func (r *runtimeState) runInteger(t *testing.T, source, resource string) int64 {
	t.Helper()
	value := r.runScript(t, source, resource)
	result, ok, err := value.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("IntegerValue = %d, %v, %v", result, ok, err)
	}
	return result
}

func (r *runtimeState) dispatch(t *testing.T, callID int32, request string) string {
	t.Helper()
	before := len(r.channel.responses)
	if err := r.session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(request))); err != nil {
		t.Fatal(err)
	}
	if len(r.channel.responses) != before+1 {
		t.Fatalf("request %d responses = %d, want %d", callID, len(r.channel.responses), before+1)
	}
	response := r.channel.responses[len(r.channel.responses)-1]
	if response.callID != callID {
		t.Fatalf("response call id = %d, want %d", response.callID, callID)
	}
	return response.message
}

func pausedObservation(r *runtimeState, t *testing.T, childName, resource string) map[string]any {
	t.Helper()
	notificationStart := len(r.channel.notifications)
	callbackStart := len(r.client.pauseGroups)
	r.runScript(t, fmt.Sprintf("function %s(){debugger;} %s();", childName, childName), resource)
	notifications := r.channel.notifications[notificationStart:]
	var paused []string
	for _, message := range notifications {
		if strings.Contains(message, `"method":"Debugger.paused"`) {
			paused = append(paused, message)
		}
	}
	if len(paused) != 1 {
		t.Fatalf("%s notifications = %#v", childName, notifications)
	}
	message := paused[0]
	var callbackGroup any
	if len(r.client.pauseGroups) != 0 {
		callbackGroup = r.client.pauseGroups[len(r.client.pauseGroups)-1]
	}
	return map[string]any{
		"pause_callbacks":      len(r.client.pauseGroups) - callbackStart,
		"callback_group":       callbackGroup,
		"child_frame":          strings.Contains(message, `"functionName":"`+childName+`"`),
		"async_stack_present":  strings.Contains(message, `"asyncStackTrace"`),
		"one_shot_description": strings.Contains(message, `"description":"one-shot-task"`),
		"recurring_description": strings.Contains(message,
			`"description":"recurring-task"`),
		"cleared_description": strings.Contains(message, `"description":"cleared-task"`),
		"schedule_parent_frame": strings.Contains(message,
			`"functionName":"scheduleParent"`),
	}
}

func jsonIntegerField(input, key string) any {
	var message any
	if err := json.Unmarshal([]byte(input), &message); err != nil {
		return nil
	}
	return findJSONField(message, key)
}

func findJSONField(value any, key string) any {
	switch value := value.(type) {
	case map[string]any:
		if result, ok := value[key]; ok {
			return result
		}
		for _, child := range value {
			if result := findJSONField(child, key); result != nil {
				return result
			}
		}
	case []any:
		for _, child := range value {
			if result := findJSONField(child, key); result != nil {
				return result
			}
		}
	}
	return nil
}

func TestInspectorRuntimeEventsConformance(t *testing.T) {
	fixtures := fixture(t)
	r := newRuntime(t)
	defer r.close(t)

	if err := r.inspector.IdleFinished(); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.IdleStarted(); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.IdleStarted(); err != nil {
		t.Fatal(err)
	}
	idleValue := r.runInteger(t, "6 * 7", "idle.js")
	if err := r.inspector.IdleFinished(); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.IdleFinished(); err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[0], map[string]any{
		"finish_before_start_returned": true,
		"repeated_start_returned":      true,
		"script_during_idle":           idleValue,
		"repeated_finish_returned":     true,
	})

	var config scheduleConfig
	var callbackErr error
	callbackCalls := 0
	nativeSchedule, err := r.iso.NewFunction(r.scope, r.ctx,
		func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {
			callbackCalls++
			callbackErr = r.inspector.AsyncTaskScheduled(
				gov8.NewInspectorStringView8([]byte(config.name)), config.task, config.recurring)
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, "nativeSchedule", nativeSchedule.Value); err != nil || !ok {
		t.Fatalf("set nativeSchedule = %v, %v", ok, err)
	}
	if err := r.inspector.ContextCreated(r.ctx, 1, gov8.EmptyInspectorStringView(),
		gov8.NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
		t.Fatal(err)
	}
	r.contextRegistered = true
	r.session, err = r.inspector.Connect(1, r.channel,
		gov8.NewInspectorStringView8([]byte(`{}`)), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	debuggerEnable := r.dispatch(t, 1, `{"id":1,"method":"Debugger.enable"}`)
	asyncDepth := r.dispatch(t, 2,
		`{"id":2,"method":"Debugger.setAsyncCallStackDepth","params":{"maxDepth":8}}`)

	scheduleFromJS := func(task gov8.InspectorAsyncTaskID, recurring bool, name string) {
		t.Helper()
		config = scheduleConfig{task: task, recurring: recurring, name: name}
		callbackErr = nil
		before := callbackCalls
		r.runScript(t, "function scheduleParent(){nativeSchedule();} scheduleParent();", "schedule-parent.js")
		if callbackErr != nil {
			t.Fatal(callbackErr)
		}
		if callbackCalls != before+1 {
			t.Fatalf("native schedule callback calls = %d, want %d", callbackCalls, before+1)
		}
	}

	const oneShot gov8.InspectorAsyncTaskID = 11
	scheduleFromJS(oneShot, false, "one-shot-task")
	if err := r.inspector.AsyncTaskStarted(oneShot); err != nil {
		t.Fatal(err)
	}
	oneShotFirst := pausedObservation(r, t, "childOne", "child-one.js")
	if err := r.inspector.AsyncTaskFinished(oneShot); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.AsyncTaskStarted(oneShot); err != nil {
		t.Fatal(err)
	}
	oneShotSecond := pausedObservation(r, t, "childTwo", "child-two.js")
	if err := r.inspector.AsyncTaskFinished(oneShot); err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[1], map[string]any{
		"debugger_enabled":     !strings.Contains(debuggerEnable, `"error"`),
		"async_depth_set":      !strings.Contains(asyncDepth, `"error"`),
		"first_start":          oneShotFirst,
		"after_finish_restart": oneShotSecond,
	})

	const recurring gov8.InspectorAsyncTaskID = 22
	scheduleFromJS(recurring, true, "recurring-task")
	recurringRuns := make([]map[string]any, 0, 2)
	for _, run := range []struct{ child, resource string }{
		{"recurringOne", "recurring-one.js"},
		{"recurringTwo", "recurring-two.js"},
	} {
		if err := r.inspector.AsyncTaskStarted(recurring); err != nil {
			t.Fatal(err)
		}
		recurringRuns = append(recurringRuns, pausedObservation(r, t, run.child, run.resource))
		if err := r.inspector.AsyncTaskFinished(recurring); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.inspector.AsyncTaskCanceled(recurring); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.AsyncTaskStarted(recurring); err != nil {
		t.Fatal(err)
	}
	afterCancel := pausedObservation(r, t, "recurringCanceled", "recurring-canceled.js")
	if err := r.inspector.AsyncTaskFinished(recurring); err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[2], map[string]any{
		"runs_before_cancel": recurringRuns,
		"after_cancel":       afterCancel,
	})

	const cleared gov8.InspectorAsyncTaskID = 33
	scheduleFromJS(cleared, true, "cleared-task")
	if err := r.inspector.AllAsyncTasksCanceled(); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.AsyncTaskStarted(cleared); err != nil {
		t.Fatal(err)
	}
	afterAllCanceled := pausedObservation(r, t, "allCanceled", "all-canceled.js")
	if err := r.inspector.AsyncTaskFinished(cleared); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.AsyncTaskScheduled(gov8.EmptyInspectorStringView(), 0, false); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.AsyncTaskStarted(0); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.AsyncTaskFinished(0); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.AsyncTaskCanceled(0); err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[3], map[string]any{
		"after_all_canceled":           afterAllCanceled,
		"null_identity_calls_returned": true,
	})

	runtimeEnable := r.dispatch(t, 3, `{"id":3,"method":"Runtime.enable"}`)
	exception := r.runScript(t,
		"function outerEvent(){return innerEvent();} function innerEvent(){return new Error('oracle boom');} outerEvent();",
		"event-source.js")
	emptyInspectorTrace, emptyTraceOK, err := r.inspector.CreateInspectorStackTrace(nil)
	if err != nil {
		t.Fatal(err)
	}
	if emptyInspectorTrace != nil {
		defer func() {
			if err := emptyInspectorTrace.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	v8Trace, v8TracePresent, err := gov8.ExceptionStackTrace(r.scope, exception)
	if err != nil {
		t.Fatal(err)
	}
	inspectorTrace, inspectorTraceOK, err := r.inspector.CreateInspectorStackTrace(v8Trace)
	if err != nil {
		t.Fatal(err)
	}
	orphanTrace, orphanTraceOK, err := r.inspector.CreateInspectorStackTrace(v8Trace)
	if err != nil {
		t.Fatal(err)
	}
	if !inspectorTraceOK || inspectorTrace == nil || !orphanTraceOK || orphanTrace == nil {
		t.Fatalf("owned traces = primary(%v,%v), orphan(%v,%v)",
			inspectorTrace, inspectorTraceOK, orphanTrace, orphanTraceOK)
	}
	notificationStart := len(r.channel.notifications)
	exceptionID, err := r.inspector.ExceptionThrown(
		r.scope, r.ctx,
		gov8.NewInspectorStringView8([]byte("oracle exception")), exception,
		gov8.NewInspectorStringView8([]byte("oracle detailed")),
		gov8.NewInspectorStringView8([]byte("embedder://event")),
		7, 9, inspectorTrace, 42)
	if err != nil {
		t.Fatal(err)
	}
	var exceptionNotifications []string
	for _, message := range r.channel.notifications[notificationStart:] {
		if strings.Contains(message, `"method":"Runtime.exceptionThrown"`) {
			exceptionNotifications = append(exceptionNotifications, message)
		}
	}
	if len(exceptionNotifications) != 1 {
		t.Fatalf("exception notifications = %#v", r.channel.notifications[notificationStart:])
	}
	exceptionMessage := exceptionNotifications[0]
	compare(t, fixtures, expectedChecks[4], map[string]any{
		"runtime_enabled":           !strings.Contains(runtimeEnable, `"error"`),
		"none_input_trace_non_null": emptyInspectorTrace != nil && emptyTraceOK,
		"v8_trace_present":          v8TracePresent,
		"inspector_trace_non_null":  inspectorTraceOK,
		"exception_id":              exceptionID,
		"notification_count":        len(exceptionNotifications),
		"line_number":               jsonIntegerField(exceptionMessage, "lineNumber"),
		"column_number":             jsonIntegerField(exceptionMessage, "columnNumber"),
		"script_id_42":              strings.Contains(exceptionMessage, `"scriptId":"42"`),
		"text_uses_detailed_message": strings.Contains(
			exceptionMessage, `"text":"oracle detailed"`),
		"original_message_hidden": !strings.Contains(exceptionMessage, "oracle exception"),
		"url":                     strings.Contains(exceptionMessage, `"url":"embedder://event"`),
		"exception_object_present": strings.Contains(
			exceptionMessage, `"exception":`),
		"inner_frame": strings.Contains(exceptionMessage, "innerEvent"),
		"outer_frame": strings.Contains(exceptionMessage, "outerEvent"),
	})

	notificationStart = len(r.channel.notifications)
	undefined, err := r.scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	noStackID, err := r.inspector.ExceptionThrown(
		r.scope, r.ctx,
		gov8.NewInspectorStringView8([]byte("without stack")), undefined,
		gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView(),
		0, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var noStackNotifications []string
	for _, message := range r.channel.notifications[notificationStart:] {
		if strings.Contains(message, `"method":"Runtime.exceptionThrown"`) {
			noStackNotifications = append(noStackNotifications, message)
		}
	}
	if len(noStackNotifications) != 1 {
		t.Fatalf("no-stack notifications = %#v", r.channel.notifications[notificationStart:])
	}
	noStackMessage := noStackNotifications[0]
	compare(t, fixtures, expectedChecks[5], map[string]any{
		"exception_id":        noStackID,
		"text_uses_message":   strings.Contains(noStackMessage, `"text":"without stack"`),
		"stack_trace_present": strings.Contains(noStackMessage, `"stackTrace":`),
		"line_number":         jsonIntegerField(noStackMessage, "lineNumber"),
		"column_number":       jsonIntegerField(noStackMessage, "columnNumber"),
	})

	if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.contextRegistered = false
	notificationsBefore := len(r.channel.notifications)
	unregisteredID, err := r.inspector.ExceptionThrown(
		r.scope, r.ctx,
		gov8.NewInspectorStringView8([]byte("after destroy")), exception,
		gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView(),
		1, 1, orphanTrace, 0)
	if err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[6], map[string]any{
		"exception_id": unregisteredID,
		"new_notifications": len(r.channel.notifications) -
			notificationsBefore,
	})
}
