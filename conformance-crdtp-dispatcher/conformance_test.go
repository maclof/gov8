//go:build windows && amd64

package crdtpdispatcherconformance

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gov8 "github.com/maclof/gov8"
)

var expectedChecks = []string{
	"crdtp-dispatcher/known_multiple_domains",
	"crdtp-dispatcher/unknown_routing",
	"crdtp-dispatcher/associated_data_handler",
	"crdtp-dispatcher/fallthrough",
	"crdtp-dispatcher/lifecycle",
}

type fixtureLine struct {
	Check   string         `json:"check"`
	OK      bool           `json:"ok"`
	Value   any            `json:"value"`
	Summary map[string]int `json:"summary"`
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures",
		"conformance-crdtp-dispatcher-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	result := map[string]fixtureLine{}
	index := 0
	summary := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if summary {
			t.Fatal("fixture data after summary")
		}
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Summary != nil {
			summary = true
			if index != len(expectedChecks) || line.Summary["total"] != len(expectedChecks) ||
				line.Summary["passed"] != len(expectedChecks) || line.Summary["failed"] != 0 {
				t.Fatalf("bad summary: %#v", line.Summary)
			}
			continue
		}
		if index >= len(expectedChecks) || line.Check != expectedChecks[index] || !line.OK {
			t.Fatalf("fixture check %d: %#v", index, line)
		}
		if _, exists := result[line.Check]; exists {
			t.Fatalf("duplicate fixture check %s", line.Check)
		}
		result[line.Check] = line
		index++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !summary || index != len(expectedChecks) {
		t.Fatalf("incomplete fixture: checks=%d summary=%v", index, summary)
	}
	return result
}

func compare(t *testing.T, fixture map[string]fixtureLine, id string, got any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(fixture[id].Value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

func mustCBOR(t *testing.T, text string) []byte {
	t.Helper()
	result, ok, err := gov8.CRDTPJSONToCBOR([]byte(text))
	if err != nil || !ok {
		t.Fatalf("JSONToCBOR ok=%v err=%v", ok, err)
	}
	return result
}

func mustJSON(t *testing.T, cbor []byte) string {
	t.Helper()
	result, ok, err := gov8.CRDTPCBORToJSON(cbor)
	if err != nil || !ok {
		t.Fatalf("CBORToJSON ok=%v err=%v", ok, err)
	}
	return string(result)
}

type response struct {
	callID int32
	json   string
}

type channelState struct {
	responses     []response
	notifications []string
	flushes       int
	drops         int
}

type recordingChannel struct{ state *channelState }

func (c *recordingChannel) SendProtocolResponse(callID int32, message *gov8.CRDTPSerializable) {
	bytes, err := message.Bytes()
	if err != nil {
		panic(err)
	}
	c.state.responses = append(c.state.responses, response{callID, mustJSONNoTest(bytes)})
	if err := message.Close(); err != nil {
		panic(err)
	}
}
func (c *recordingChannel) SendProtocolNotification(message *gov8.CRDTPSerializable) {
	bytes, err := message.Bytes()
	if err != nil {
		panic(err)
	}
	c.state.notifications = append(c.state.notifications, mustJSONNoTest(bytes))
	if err := message.Close(); err != nil {
		panic(err)
	}
}
func (c *recordingChannel) FlushProtocolNotifications() { c.state.flushes++ }
func (c *recordingChannel) CRDTPCallbackDropped()       { c.state.drops++ }

func mustJSONNoTest(cbor []byte) string {
	result, ok, err := gov8.CRDTPCBORToJSON(cbor)
	if err != nil || !ok {
		panic("invalid CRDTP CBOR")
	}
	return string(result)
}

type domainCall struct {
	domain, command string
	callID          int32
	associated      []byte
	delivered       bool
	handled         bool
}

type domainState struct {
	calls []domainCall
	drops int
}

type recordingDomain struct {
	name    string
	state   *domainState
	channel *channelState
}

func (d *recordingDomain) Dispatch(command []byte, request *gov8.CRDTPDispatchRequest, responder *gov8.CRDTPDomainResponder) bool {
	id, _, err := request.CallID()
	if err != nil {
		panic(err)
	}
	associated, err := request.AssociatedData()
	if err != nil {
		panic(err)
	}
	before := len(d.channel.responses)
	var handled bool
	switch string(command) {
	case "ok":
		response, err := gov8.NewCRDTPSuccessResponse()
		if err != nil {
			panic(err)
		}
		if err := responder.SendResponse(id, response, nil); err != nil {
			panic(err)
		}
		handled = true
	case "withResult":
		response, err := gov8.NewCRDTPSuccessResponse()
		if err != nil {
			panic(err)
		}
		result, err := gov8.CreateCRDTPNotification("Nested.result", nil)
		if err != nil {
			panic(err)
		}
		if err := responder.SendResponse(id, response, result); err != nil {
			panic(err)
		}
		handled = true
	case "bad":
		response, err := gov8.NewCRDTPInvalidParams("bad input")
		if err != nil {
			panic(err)
		}
		if err := responder.SendResponse(id, response, nil); err != nil {
			panic(err)
		}
		handled = true
	}
	d.state.calls = append(d.state.calls, domainCall{
		domain: d.name, command: string(command), callID: id, associated: associated,
		delivered: len(d.channel.responses) > before, handled: handled,
	})
	return handled
}
func (d *recordingDomain) CRDTPCallbackDropped() { d.state.drops++ }

func responseJSON(value response) any {
	return map[string]any{"call_id": value.callID, "json": value.json}
}
func callJSON(value domainCall) any {
	return map[string]any{
		"domain": value.domain, "command": value.command, "call_id": value.callID,
		"associated_data_hex":              hex.EncodeToString(value.associated),
		"response_delivered_before_return": value.delivered, "handled": value.handled,
	}
}
func responseSlice(values []response) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = responseJSON(value)
	}
	return result
}
func callSlice(values []domainCall) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = callJSON(value)
	}
	return result
}

func dispatch(t *testing.T, dispatcher *gov8.CRDTPUberDispatcher, text string) {
	t.Helper()
	message, err := gov8.NewCRDTPDispatchable(mustCBOR(t, text), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(message); err != nil {
		t.Fatal(err)
	}
	if err := message.Close(); err != nil {
		t.Fatal(err)
	}
}

type recordingFallthrough struct {
	calls []map[string]any
	drops int
}

func (f *recordingFallthrough) FallThrough(callID int32, method, message, associated []byte) {
	f.calls = append(f.calls, map[string]any{
		"call_id": callID, "method": string(method), "message_json": mustJSONNoTest(message),
		"callback_associated_data_hex": hex.EncodeToString(associated),
	})
}
func (f *recordingFallthrough) CRDTPCallbackDropped() { f.drops++ }

func TestCRDTPDispatcherConformance(t *testing.T) {
	fixtures := fixture(t)
	channelState := &channelState{}
	channel, err := gov8.NewCRDTPFrontendChannel(&recordingChannel{channelState})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := gov8.NewCRDTPUberDispatcher(channel)
	if err != nil {
		t.Fatal(err)
	}
	domainState := &domainState{}
	for _, name := range []string{"Alpha", "Beta"} {
		if err := dispatcher.WireDomain(name, &recordingDomain{name, domainState, channelState}); err != nil {
			t.Fatal(err)
		}
	}

	responseStart, callStart := len(channelState.responses), len(domainState.calls)
	dispatch(t, dispatcher, `{"id":1,"method":"Alpha.ok","params":{}}`)
	dispatch(t, dispatcher, `{"id":2,"method":"Alpha.bad","params":{}}`)
	dispatch(t, dispatcher, `{"id":3,"method":"Beta.withResult","params":{}}`)
	compare(t, fixtures, expectedChecks[0], map[string]any{
		"responses": responseSlice(channelState.responses[responseStart:]),
		"callbacks": callSlice(domainState.calls[callStart:]),
	})

	responseStart, callStart = len(channelState.responses), len(domainState.calls)
	dispatch(t, dispatcher, `{"id":4,"method":"Alpha.unknown","params":{}}`)
	dispatch(t, dispatcher, `{"id":5,"method":"Gamma.ok","params":{}}`)
	compare(t, fixtures, expectedChecks[1], map[string]any{
		"responses": responseSlice(channelState.responses[responseStart:]),
		"callbacks": callSlice(domainState.calls[callStart:]),
		"responses_available_when_dispatch_returned": len(channelState.responses) == responseStart+2,
	})

	responseStart, callStart = len(channelState.responses), len(domainState.calls)
	associated := []byte{0, 0xff, 'x', 0}
	handlerFallthrough := &recordingFallthrough{}
	withAssociated, err := gov8.NewCRDTPDispatchableWithFallthrough(
		mustCBOR(t, `{"id":6,"method":"Alpha.ok","params":{"from":"associated"}}`), associated, handlerFallthrough)
	if err != nil {
		t.Fatal(err)
	}
	before, err := withAssociated.AssociatedData()
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(withAssociated); err != nil {
		t.Fatal(err)
	}
	after, err := withAssociated.AssociatedData()
	if err != nil {
		t.Fatal(err)
	}
	dropsBefore := handlerFallthrough.drops
	if err := withAssociated.Close(); err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[2], map[string]any{
		"accessor_before_hex": hex.EncodeToString(before), "accessor_after_hex": hex.EncodeToString(after),
		"handler":                            callJSON(domainState.calls[callStart]),
		"response":                           responseJSON(channelState.responses[responseStart]),
		"fallthrough_callback_invocations":   len(handlerFallthrough.calls),
		"callback_drops_before_dispatchable": dropsBefore,
		"callback_drops_after_dispatchable":  handlerFallthrough.drops,
		"producer_buffers_dropped":           true,
	})

	responseStart = len(channelState.responses)
	input := mustCBOR(t, `{"id":7,"method":"Gamma.missing","params":{"answer":42}}`)
	expectedJSON := mustJSON(t, input)
	fallthroughHandler := &recordingFallthrough{}
	fallthroughMessage, err := gov8.NewCRDTPDispatchableWithFallthrough(input,
		[]byte("request metadata\x00owned"), fallthroughHandler)
	if err != nil {
		t.Fatal(err)
	}
	memberAssociated, err := fallthroughMessage.AssociatedData()
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(fallthroughMessage); err != nil {
		t.Fatal(err)
	}
	dropsAfterDispatch := fallthroughHandler.drops
	if err := fallthroughMessage.Close(); err != nil {
		t.Fatal(err)
	}
	callback := fallthroughHandler.calls[0]
	callback["message_matches_input"] = callback["message_json"] == expectedJSON
	compare(t, fixtures, expectedChecks[3], map[string]any{
		"member_associated_data_hex": hex.EncodeToString(memberAssociated), "callback": callback,
		"response_count":                    len(channelState.responses) - responseStart,
		"callback_drops_after_dispatch":     dropsAfterDispatch,
		"callback_drops_after_dispatchable": fallthroughHandler.drops,
		"synchronous":                       true, "producer_buffers_dropped": true,
	})

	domainDropsBefore, channelDropsBefore := domainState.drops, channelState.drops
	if err := dispatcher.Close(); err != nil {
		t.Fatal(err)
	}
	domainDropsAfter, channelDropsAfterDispatcher := domainState.drops, channelState.drops
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[4], map[string]any{
		"wired_domains": 2, "domain_drops_before_dispatcher": domainDropsBefore,
		"domain_drops_after_dispatcher":      domainDropsAfter,
		"channel_drops_before_dispatcher":    channelDropsBefore,
		"channel_drops_after_dispatcher":     channelDropsAfterDispatcher,
		"channel_drops_after_channel":        channelState.drops,
		"notifications_during_public_routes": len(channelState.notifications),
		"flushes_during_public_routes":       channelState.flushes,
	})
}
