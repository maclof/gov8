//go:build windows && amd64

package inspectorclientvaluesconformance

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
	"inspector-client-values/subtype_description",
	"inspector-client-values/default_context_success",
	"inspector-client-values/default_context_none",
	"inspector-client-values/lifecycle",
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

type channel struct{ responses []response }

func (c *channel) SendResponse(callID int32, message *gov8.InspectorStringBuffer) {
	c.responses = append(c.responses, response{callID: callID, message: message.StringView().String()})
}
func (*channel) SendNotification(*gov8.InspectorStringBuffer) {}
func (*channel) FlushProtocolNotifications()                  {}

type target struct {
	label              string
	value              gov8.Value
	subtype            string
	subtypePresent     bool
	description        string
	descriptionPresent bool
}

type callbackObservation struct {
	phase                 string
	label                 string
	isObject              bool
	currentContextMatches bool
	valueIdentityMatches  bool
}

type ensureObservation struct {
	group           int32
	returnedContext bool
}

type clientState struct {
	ctx         *gov8.Context
	targets     []target
	callbacks   []callbackObservation
	ensureCalls []ensureObservation
	callbackErr error
}

type client struct{ state *clientState }

func (c *client) identify(value gov8.Value) (target, bool) {
	for _, candidate := range c.state.targets {
		same, err := value.StrictEquals(candidate.value)
		if err != nil {
			c.state.callbackErr = err
			return target{}, false
		}
		if same {
			return candidate, true
		}
	}
	return target{label: "other"}, false
}

func (c *client) observe(scope *gov8.CallbackScope, value gov8.Value, phase string) (target, bool) {
	candidate, identity := c.identify(value)
	isObject, err := value.IsObject()
	if err != nil {
		c.state.callbackErr = err
	}
	contextMatches := false
	current, err := scope.Isolate().CurrentContext(scope.Scope())
	if err != nil {
		c.state.callbackErr = err
	} else {
		contextMatches, err = current.SameAs(c.state.ctx)
		if err != nil {
			c.state.callbackErr = err
		}
	}
	c.state.callbacks = append(c.state.callbacks, callbackObservation{
		phase: phase, label: candidate.label, isObject: isObject,
		currentContextMatches: contextMatches,
		valueIdentityMatches:  identity,
	})
	return candidate, identity
}

func (c *client) ValueSubtype(scope *gov8.CallbackScope, value gov8.Value) *gov8.InspectorStringBuffer {
	candidate, _ := c.observe(scope, value, "subtype")
	if !candidate.subtypePresent {
		return nil
	}
	return gov8.NewInspectorStringBuffer(
		gov8.NewInspectorStringView8([]byte(candidate.subtype)))
}

func (c *client) DescriptionForValueSubtype(scope *gov8.CallbackScope, value gov8.Value) *gov8.InspectorStringBuffer {
	candidate, _ := c.observe(scope, value, "description")
	if !candidate.descriptionPresent {
		return nil
	}
	return gov8.NewInspectorStringBuffer(
		gov8.NewInspectorStringView8([]byte(candidate.description)))
}

func (c *client) EnsureDefaultContextInGroup(group int32) *gov8.Context {
	returned := group == 7 && c.state.ctx != nil
	c.state.ensureCalls = append(c.state.ensureCalls, ensureObservation{
		group: group, returnedContext: returned,
	})
	if returned {
		return c.state.ctx
	}
	return nil
}

type runtimeState struct {
	iso               *gov8.Isolate
	ctx               *gov8.Context
	scope             *gov8.Scope
	entered           *gov8.ContextScope
	inspector         *gov8.Inspector
	session           *gov8.InspectorSession
	missingSession    *gov8.InspectorSession
	contextRegistered bool
	channel           *channel
	missingChannel    *channel
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
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures",
		"conformance-inspector-client-values-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
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
	r := &runtimeState{
		channel: &channel{}, missingChannel: &channel{},
		clientState: &clientState{},
	}
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
	r.clientState.ctx = r.ctx
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
	if r.missingSession != nil {
		if err := r.missingSession.Close(); err != nil {
			t.Error(err)
		}
		r.missingSession = nil
	}
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
			// Rust drops a boxed client. Go clients remain caller-owned, so one
			// successful synchronous callback-owner unregister is the exact
			// deterministic lifecycle equivalent.
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

func (r *runtimeState) runScript(t *testing.T, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
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

func dispatch(t *testing.T, session *gov8.InspectorSession,
	channel *channel, callID int32, request string,
) string {
	t.Helper()
	before := len(channel.responses)
	if err := session.DispatchProtocolMessage(
		gov8.NewInspectorStringView8([]byte(request))); err != nil {
		t.Fatal(err)
	}
	if len(channel.responses) != before+1 {
		t.Fatalf("request %d responses = %d, want %d",
			callID, len(channel.responses), before+1)
	}
	response := channel.responses[len(channel.responses)-1]
	if response.callID != callID {
		t.Fatalf("response call id = %d, want %d", response.callID, callID)
	}
	return response.message
}

func mirrorJSON(t *testing.T, response string) map[string]any {
	t.Helper()
	var decoded struct {
		Error  any `json:"error"`
		Result struct {
			Result struct {
				Type        *string `json:"type"`
				Subtype     *string `json:"subtype"`
				ClassName   *string `json:"className"`
				Description *string `json:"description"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatal(err)
	}
	var value = func(pointer *string) any {
		if pointer == nil {
			return nil
		}
		return *pointer
	}
	return map[string]any{
		"success":     decoded.Error == nil,
		"type":        value(decoded.Result.Result.Type),
		"subtype":     value(decoded.Result.Result.Subtype),
		"class_name":  value(decoded.Result.Result.ClassName),
		"description": value(decoded.Result.Result.Description),
	}
}

func callbackJSON(observation callbackObservation) map[string]any {
	return map[string]any{
		"phase":                   observation.phase,
		"label":                   observation.label,
		"is_object":               observation.isObject,
		"current_context_matches": observation.currentContextMatches,
		"value_identity_matches":  observation.valueIdentityMatches,
	}
}

func ensureJSON(observation ensureObservation) map[string]any {
	return map[string]any{
		"group":            observation.group,
		"returned_context": observation.returnedContext,
	}
}

func responseErrorMessage(t *testing.T, response string) (bool, any) {
	t.Helper()
	var decoded struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error == nil {
		return false, nil
	}
	return true, decoded.Error.Message
}

func TestInspectorClientValuesConformance(t *testing.T) {
	fixtures := fixture(t)
	r := newRuntime(t)

	r.runScript(t, "globalThis.marked={}; globalThis.noDescription={}; "+
		"globalThis.invalidSubtype={}; globalThis.plain={}; globalThis.answer=41;")
	for _, spec := range []struct {
		label, expression, subtype, description string
		subtypePresent, descriptionPresent      bool
	}{
		{"marked", "marked", "node", "marked object", true, true},
		{"no_description", "noDescription", "node", "", true, false},
		{"invalid_subtype", "invalidSubtype", "not-a-protocol-subtype", "invalid subtype object", true, true},
		{"plain", "plain", "", "", false, false},
	} {
		r.clientState.targets = append(r.clientState.targets, target{
			label: spec.label, value: r.runScript(t, spec.expression),
			subtype: spec.subtype, subtypePresent: spec.subtypePresent,
			description: spec.description, descriptionPresent: spec.descriptionPresent,
		})
	}

	if err := r.inspector.ContextCreated(
		r.ctx, 7,
		gov8.NewInspectorStringView8([]byte("client-values")),
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

	expressions := []string{"marked", "noDescription", "invalidSubtype", "plain", "answer"}
	callbackStart := len(r.clientState.callbacks)
	mirrors := make([]map[string]any, 0, len(expressions))
	for index, expression := range expressions {
		callID := int32(index + 1)
		request, err := json.Marshal(map[string]any{
			"id": callID, "method": "Runtime.evaluate",
			"params": map[string]any{"expression": expression, "contextId": 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		mirrors = append(mirrors, mirrorJSON(t,
			dispatch(t, r.session, r.channel, callID, string(request))))
		if r.clientState.callbackErr != nil {
			t.Fatal(r.clientState.callbackErr)
		}
	}
	callbacks := make([]map[string]any, 0,
		len(r.clientState.callbacks)-callbackStart)
	for _, observation := range r.clientState.callbacks[callbackStart:] {
		callbacks = append(callbacks, callbackJSON(observation))
	}
	compare(t, fixtures, expectedChecks[0], map[string]any{
		"expressions": expressions,
		"mirrors":     mirrors,
		"callbacks":   callbacks,
	})

	ensureStart := len(r.clientState.ensureCalls)
	defaultFirst := dispatch(t, r.session, r.channel, 10,
		`{"id":10,"method":"Runtime.evaluate","params":{"expression":"answer += 1"}}`)
	defaultSecond := dispatch(t, r.session, r.channel, 11,
		`{"id":11,"method":"Runtime.evaluate","params":{"expression":"answer"}}`)
	ensureCalls := make([]map[string]any, 0,
		len(r.clientState.ensureCalls)-ensureStart)
	for _, observation := range r.clientState.ensureCalls[ensureStart:] {
		ensureCalls = append(ensureCalls, ensureJSON(observation))
	}
	compare(t, fixtures, expectedChecks[1], map[string]any{
		"responses_success": []bool{
			!strings.Contains(defaultFirst, `"error"`),
			!strings.Contains(defaultSecond, `"error"`),
		},
		"results_are_42": []bool{
			strings.Contains(defaultFirst, `"value":42`),
			strings.Contains(defaultSecond, `"value":42`),
		},
		"callback_calls": ensureCalls,
	})

	r.missingSession, err = r.inspector.Connect(
		99, r.missingChannel, gov8.NewInspectorStringView8([]byte(`{}`)),
		gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	missing := dispatch(t, r.missingSession, r.missingChannel, 20,
		`{"id":20,"method":"Runtime.evaluate","params":{"expression":"1"}}`)
	responseError, errorMessage := responseErrorMessage(t, missing)
	lastEnsure := r.clientState.ensureCalls[len(r.clientState.ensureCalls)-1]
	compare(t, fixtures, expectedChecks[2], map[string]any{
		"response_error":   responseError,
		"error_message":    errorMessage,
		"callback_group":   lastEnsure.group,
		"returned_context": lastEnsure.returnedContext,
	})

	dropsBefore := r.clientReleases
	if err := r.missingSession.Close(); err != nil {
		t.Fatal(err)
	}
	r.missingSession = nil
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
	compare(t, fixtures, expectedChecks[3], map[string]any{
		"drops_before_inspector":          dropsBefore,
		"drops_after_inspector":           r.clientReleases,
		"total_value_callbacks":           len(r.clientState.callbacks),
		"total_default_context_callbacks": len(r.clientState.ensureCalls),
	})
}
