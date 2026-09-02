//go:build windows && amd64

package inspectorclientcallbacksconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

var expectedChecks = []string{
	"inspector-client-callbacks/generate_unique_id",
	"inspector-client-callbacks/run_if_waiting_for_debugger",
	"inspector-client-callbacks/resource_name_to_url",
	"inspector-client-callbacks/console_api_message",
	"inspector-client-callbacks/client_lifecycle",
}

type fixtureLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

type response struct {
	callID  int32
	message string
}

type channel struct {
	responses     []response
	notifications []string
}

func (c *channel) SendResponse(callID int32, message *gov8.InspectorStringBuffer) {
	c.responses = append(c.responses, response{callID: callID, message: message.StringView().String()})
}
func (c *channel) SendNotification(message *gov8.InspectorStringBuffer) {
	c.notifications = append(c.notifications, message.StringView().String())
}
func (*channel) FlushProtocolNotifications() {}

type resourceObservation struct {
	input  gov8.InspectorStringView
	mapped *string
}

type consoleObservation struct {
	group, level int32
	message      gov8.InspectorStringView
	url          gov8.InspectorStringView
	line, column uint32
	stack        bool
}

type clientState struct {
	uniqueIDs []int64
	waiting   []int32
	resources []resourceObservation
	console   []consoleObservation
}

type client struct{ state *clientState }

func (c *client) GenerateUniqueID() int64 {
	value := int64(7001 + len(c.state.uniqueIDs))
	c.state.uniqueIDs = append(c.state.uniqueIDs, value)
	return value
}

func (c *client) RunIfWaitingForDebugger(group int32) {
	c.state.waiting = append(c.state.waiting, group)
}

func (c *client) ResourceNameToURL(name gov8.InspectorStringView) *gov8.InspectorStringBuffer {
	var mapped *string
	switch name.String() {
	case "mapped.js":
		value := "client://mapped"
		mapped = &value
	case "nul\x00name.js":
		value := "client://nul"
		mapped = &value
	case "console.js":
		value := "client://console"
		mapped = &value
	}
	c.state.resources = append(c.state.resources, resourceObservation{input: name, mapped: mapped})
	if mapped == nil {
		return nil
	}
	return gov8.NewInspectorStringBuffer(
		gov8.NewInspectorStringView8([]byte(*mapped)))
}

func (c *client) ConsoleAPIMessage(group, level int32,
	message, url gov8.InspectorStringView, line, column uint32,
	stack gov8.InspectorBorrowedStackTrace,
) {
	c.state.console = append(c.state.console, consoleObservation{
		group: group, level: level, message: message, url: url,
		line: line, column: column, stack: stack.Present(),
	})
}

type runtimeState struct {
	iso               *gov8.Isolate
	ctx               *gov8.Context
	scope             *gov8.Scope
	entered           *gov8.ContextScope
	inspector         *gov8.Inspector
	session           *gov8.InspectorSession
	contextRegistered bool
	channel           *channel
	clientState       *clientState
	clientReleases    int
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "..", "rust-oracle", "tests", "fixtures",
		"conformance-inspector-client-callbacks-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
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
				t.Fatalf("fixture check %d = %q, want %q",
					checkIndex, line.Check, expectedChecks[checkIndex])
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
		t.Fatalf("fixture completeness = checks %d/%d, summary %v",
			checkIndex, len(expectedChecks), seenSummary)
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
	r := &runtimeState{channel: &channel{}, clientState: &clientState{}}
	t.Cleanup(func() { r.close(t) })
	var err error
	r.iso, err = gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	r.inspector, err = gov8.NewInspectorWithClient(
		r.iso, &client{state: r.clientState})
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
	if r.inspector != nil {
		if err := r.inspector.Close(); err != nil {
			t.Error(err)
		} else {
			// Rust owns and drops its boxed client. Go intentionally leaves
			// client ownership with the caller, so the deterministic equivalent
			// is the one synchronous callback-owner unregister performed by a
			// successful Inspector.Close.
			r.clientReleases++
			r.inspector = nil
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

func (r *runtimeState) dispatch(t *testing.T, callID int32, request string) string {
	t.Helper()
	before := len(r.channel.responses)
	if err := r.session.DispatchProtocolMessage(
		gov8.NewInspectorStringView8([]byte(request))); err != nil {
		t.Fatal(err)
	}
	if len(r.channel.responses) != before+1 {
		t.Fatalf("request %d responses = %d, want %d",
			callID, len(r.channel.responses), before+1)
	}
	response := r.channel.responses[len(r.channel.responses)-1]
	if response.callID != callID {
		t.Fatalf("response call id = %d, want %d", response.callID, callID)
	}
	return response.message
}

func (r *runtimeState) runScript(t *testing.T, source, resource string) gov8.Value {
	t.Helper()
	script, err := r.ctx.CompileWithOrigin(
		r.scope, source, &gov8.Origin{ResourceName: resource}, nil)
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

func (r *runtimeState) parsedURL(t *testing.T, source, resource string) string {
	t.Helper()
	start := len(r.channel.notifications)
	r.runScript(t, source, resource)
	var parsed []string
	for _, message := range r.channel.notifications[start:] {
		var notification struct {
			Method string `json:"method"`
			Params struct {
				URL string `json:"url"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(message), &notification); err != nil {
			t.Fatal(err)
		}
		if notification.Method == "Debugger.scriptParsed" {
			parsed = append(parsed, notification.Params.URL)
		}
	}
	if len(parsed) != 1 {
		t.Fatalf("scriptParsed for %q = %#v; notifications %#v",
			resource, parsed, r.channel.notifications[start:])
	}
	return parsed[0]
}

func viewJSON(view gov8.InspectorStringView) map[string]any {
	return map[string]any{
		"text":    view.String(),
		"is_8bit": view.Is8Bit(),
		"len":     view.Len(),
	}
}

func resourceJSON(observation resourceObservation) map[string]any {
	var mapped any
	if observation.mapped != nil {
		mapped = *observation.mapped
	}
	return map[string]any{
		"input":  viewJSON(observation.input),
		"mapped": mapped,
	}
}

func consoleJSON(observation consoleObservation) map[string]any {
	return map[string]any{
		"group":                observation.group,
		"level":                observation.level,
		"message":              viewJSON(observation.message),
		"url":                  viewJSON(observation.url),
		"line":                 observation.line,
		"column":               observation.column,
		"stack_trace_borrowed": observation.stack,
	}
}

func TestInspectorClientCallbacksConformance(t *testing.T) {
	fixtures := fixture(t)
	r := newRuntime(t)

	creationIDs := append([]int64(nil), r.clientState.uniqueIDs...)
	compare(t, fixtures, expectedChecks[0], map[string]any{
		"calls_during_create": len(creationIDs),
		"returned":            creationIDs,
	})

	if err := r.inspector.ContextCreated(
		r.ctx, 7,
		gov8.NewInspectorStringView8([]byte("client-callbacks")),
		gov8.NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
		t.Fatal(err)
	}
	r.contextRegistered = true
	var err error
	r.session, err = r.inspector.Connect(
		7, r.channel, gov8.NewInspectorStringView8([]byte(`{}`)),
		gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}

	waitFirst := r.dispatch(t, 1,
		`{"id":1,"method":"Runtime.runIfWaitingForDebugger"}`)
	waitSecond := r.dispatch(t, 2,
		`{"id":2,"method":"Runtime.runIfWaitingForDebugger"}`)
	compare(t, fixtures, expectedChecks[1], map[string]any{
		"responses_success": []bool{
			!strings.Contains(waitFirst, `"error"`),
			!strings.Contains(waitSecond, `"error"`),
		},
		"callback_groups": append([]int32(nil), r.clientState.waiting...),
	})

	debuggerEnable := r.dispatch(t, 3, `{"id":3,"method":"Debugger.enable"}`)
	resourcesStart := len(r.clientState.resources)
	mappedURL := r.parsedURL(t, "1", "mapped.js")
	plainURL := r.parsedURL(t, "2", "plain.js")
	nulURL := r.parsedURL(t, "3", "nul\x00name.js")
	sourceURL := r.parsedURL(t,
		"4\n//# sourceURL=source-override.js", "mapped.js")
	resourceCalls := make([]map[string]any, 0,
		len(r.clientState.resources)-resourcesStart)
	for _, observation := range r.clientState.resources[resourcesStart:] {
		resourceCalls = append(resourceCalls, resourceJSON(observation))
	}
	compare(t, fixtures, expectedChecks[2], map[string]any{
		"debugger_enabled":    !strings.Contains(debuggerEnable, `"error"`),
		"mapped_url":          mappedURL,
		"plain_url":           plainURL,
		"nul_url":             nulURL,
		"source_url_override": sourceURL,
		"callback_calls":      resourceCalls,
	})

	consoleStart := len(r.clientState.console)
	r.runScript(t, "console.log('one');", "console.js")
	r.runScript(t, "console.error('two');", "console.js")
	r.runScript(t,
		"function traceClient(){console.trace('three');}\ntraceClient();",
		"console.js")
	r.runScript(t, "console.log('a\\0b');", "console.js")
	r.runScript(t, "console.warn('\\u03a9');", "console.js")
	consoleCalls := make([]map[string]any, 0,
		len(r.clientState.console)-consoleStart)
	for _, observation := range r.clientState.console[consoleStart:] {
		consoleCalls = append(consoleCalls, consoleJSON(observation))
	}
	compare(t, fixtures, expectedChecks[3], map[string]any{
		"callbacks": consoleCalls,
	})

	dropsBefore := r.clientReleases
	allIDs := append([]int64(nil), r.clientState.uniqueIDs...)
	if err := r.session.Close(); err != nil {
		t.Fatal(err)
	}
	r.session = nil
	if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.contextRegistered = false
	if err := r.inspector.Close(); err != nil {
		t.Fatal(err)
	}
	r.inspector = nil
	r.clientReleases++
	compare(t, fixtures, expectedChecks[4], map[string]any{
		"drops_before_inspector":  dropsBefore,
		"drops_after_inspector":   r.clientReleases,
		"all_unique_ids_returned": allIDs,
	})
}
