//go:build windows && amd64

package crdtpcoreconformance

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
	"crdtp-core/conversion_success",
	"crdtp-core/conversion_failures",
	"crdtp-core/dispatchable_valid_owned",
	"crdtp-core/dispatchable_optional_fields",
	"crdtp-core/dispatchable_invalid",
	"crdtp-core/dispatch_responses",
	"crdtp-core/serializable_helpers",
}

type fixtureLine struct {
	Check   string         `json:"check"`
	OK      bool           `json:"ok"`
	Value   any            `json:"value"`
	Summary map[string]int `json:"summary"`
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "..", "rust-oracle", "tests", "fixtures",
		"conformance-crdtp-core-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open checked-in Rust CRDTP fixture %s: %v", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	}()
	result := make(map[string]fixtureLine, len(expectedChecks))
	index := 0
	summarySeen := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if summarySeen {
			t.Fatal("fixture contains data after summary")
		}
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("decode fixture line: %v", err)
		}
		if line.Summary != nil {
			summarySeen = true
			if index != len(expectedChecks) || line.Summary["total"] != len(expectedChecks) ||
				line.Summary["passed"] != len(expectedChecks) || line.Summary["failed"] != 0 {
				t.Fatalf("invalid fixture summary at check %d: %#v", index, line.Summary)
			}
			continue
		}
		if index >= len(expectedChecks) || line.Check != expectedChecks[index] {
			t.Fatalf("fixture check %d = %q, want %q", index, line.Check, expectedChecks[index])
		}
		if !line.OK {
			t.Fatalf("Rust fixture check %s failed", line.Check)
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
	if !summarySeen || index != len(expectedChecks) {
		t.Fatalf("incomplete fixture: checks=%d summary=%v", index, summarySeen)
	}
	return result
}

func compare(t *testing.T, fixture map[string]fixtureLine, id string, got any) {
	t.Helper()
	want, exists := fixture[id]
	if !exists {
		t.Fatalf("missing fixture check %s", id)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal Go observation %s: %v", id, err)
	}
	wantJSON, err := json.Marshal(want.Value)
	if err != nil {
		t.Fatalf("marshal Rust observation %s: %v", id, err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

func mustCBOR(t *testing.T, text string) []byte {
	t.Helper()
	result, ok, err := gov8.CRDTPJSONToCBOR([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("valid JSON rejected: %s", text)
	}
	return result
}

func mustJSON(t *testing.T, cbor []byte) string {
	t.Helper()
	result, ok, err := gov8.CRDTPCBORToJSON(cbor)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("valid CBOR rejected: %x", cbor)
	}
	return string(result)
}

func closeDispatchable(t *testing.T, value *gov8.CRDTPDispatchable) {
	t.Helper()
	if err := value.Close(); err != nil {
		t.Errorf("close Dispatchable: %v", err)
	}
}

func closeResponse(t *testing.T, value *gov8.CRDTPDispatchResponse) {
	t.Helper()
	if err := value.Close(); err != nil {
		t.Errorf("close DispatchResponse: %v", err)
	}
}

func closeSerializable(t *testing.T, value *gov8.CRDTPSerializable) {
	t.Helper()
	if err := value.Close(); err != nil {
		t.Errorf("close Serializable: %v", err)
	}
}

func TestCRDTPCoreConformance(t *testing.T) {
	fixtures := fixture(t)

	conversion := make([]any, 0, 2)
	for _, test := range []struct{ name, input string }{
		{"empty_object", `{}`},
		{"protocol", ` { "id" : 7, "method" : "Test.echo", "params" : { "text" : "a\u0000Ω", "items" : [1,true,null] } } `},
	} {
		cbor := mustCBOR(t, test.input)
		conversion = append(conversion, map[string]any{
			"case": test.name, "cbor_len": len(cbor), "cbor_hex": hex.EncodeToString(cbor),
			"round_trip": mustJSON(t, cbor),
		})
	}
	compare(t, fixtures, expectedChecks[0], conversion)

	valid := mustCBOR(t, `{"id":1,"method":"Test.run","params":{"value":42}}`)
	jsonFailures := make([]any, 0, 3)
	for _, test := range []struct {
		name  string
		input []byte
	}{{"empty", nil}, {"garbage", []byte("not json {{{")}, {"truncated", []byte(`{"id":1,"method":"Test.run"`)}} {
		result, some, err := gov8.CRDTPJSONToCBOR(test.input)
		if err != nil {
			t.Fatal(err)
		}
		var length any
		if some {
			length = len(result)
		}
		jsonFailures = append(jsonFailures, map[string]any{"case": test.name, "some": some, "len": length})
	}
	cborFailures := make([]any, 0, 3)
	for _, test := range []struct {
		name  string
		input []byte
	}{{"empty", nil}, {"garbage", []byte{0xff, 0xfe, 0x00}}, {"truncated", valid[:len(valid)/2]}} {
		result, some, err := gov8.CRDTPCBORToJSON(test.input)
		if err != nil {
			t.Fatal(err)
		}
		var text any
		if some {
			text = string(result)
		}
		cborFailures = append(cborFailures, map[string]any{"case": test.name, "some": some, "text": text})
	}
	compare(t, fixtures, expectedChecks[1], map[string]any{
		"json_to_cbor": jsonFailures, "cbor_to_json": cborFailures,
	})

	ownedInput := mustCBOR(t, `{"id":42,"method":"Network.enable","sessionId":"session-a","params":{"maxPostDataSize":65536,"enabled":true}}`)
	dispatchable, err := gov8.NewCRDTPDispatchable(ownedInput, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := range ownedInput {
		ownedInput[index] = 0
	}
	methodFirst, err := dispatchable.Method()
	if err != nil {
		t.Fatal(err)
	}
	methodSecond, err := dispatchable.Method()
	if err != nil {
		t.Fatal(err)
	}
	paramsFirst, err := dispatchable.Params()
	if err != nil {
		t.Fatal(err)
	}
	paramsSecond, err := dispatchable.Params()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := dispatchable.OK()
	if err != nil {
		t.Fatal(err)
	}
	callID, hasCallID, err := dispatchable.CallID()
	if err != nil {
		t.Fatal(err)
	}
	session, err := dispatchable.SessionID()
	if err != nil {
		t.Fatal(err)
	}
	associated, err := dispatchable.AssociatedData()
	if err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[2], map[string]any{
		"ok": ok, "has_call_id": hasCallID, "call_id": callID,
		"method": string(methodFirst), "method_hex": hex.EncodeToString(methodFirst),
		"session_id": string(session), "params": mustJSON(t, paramsFirst),
		"associated_data":       string(associated),
		"repeated_access_equal": bytes.Equal(methodFirst, methodSecond) && bytes.Equal(paramsFirst, paramsSecond),
		"input_owner_dropped":   true,
	})
	closeDispatchable(t, dispatchable)

	optional, err := gov8.NewCRDTPDispatchable(mustCBOR(t, `{"id":-7,"method":"Runtime.run"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	ok, err = optional.OK()
	if err != nil {
		t.Fatal(err)
	}
	callID, hasCallID, err = optional.CallID()
	if err != nil {
		t.Fatal(err)
	}
	method, err := optional.MethodString()
	if err != nil {
		t.Fatal(err)
	}
	session, err = optional.SessionID()
	if err != nil {
		t.Fatal(err)
	}
	paramsFirst, err = optional.Params()
	if err != nil {
		t.Fatal(err)
	}
	associated, err = optional.AssociatedData()
	if err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, expectedChecks[3], map[string]any{
		"ok": ok, "has_call_id": hasCallID, "call_id": callID, "method": method,
		"session_id_len": len(session), "params_len": len(paramsFirst),
		"associated_data_len": len(associated),
	})
	closeDispatchable(t, optional)

	invalidValid := mustCBOR(t, `{"id":1,"method":"Test.run"}`)
	invalidCases := []struct {
		name  string
		input []byte
	}{
		{"empty", nil}, {"garbage", []byte{0xff, 0xfe, 0x00, 0x01}},
		{"truncated", invalidValid[:len(invalidValid)/2]},
		{"missing_method", mustCBOR(t, `{"id":1,"params":{}}`)},
		{"missing_id", mustCBOR(t, `{"method":"Test.run","params":{}}`)},
		{"wrong_id_type", mustCBOR(t, `{"id":"1","method":"Test.run"}`)},
		{"unknown_property", mustCBOR(t, `{"id":1,"method":"Test.run","extra":true}`)},
		{"non_ascii_session", mustCBOR(t, `{"id":1,"method":"Test.run","sessionId":"α"}`)},
	}
	invalidObservations := make([]any, 0, len(invalidCases))
	for _, test := range invalidCases {
		value, err := gov8.NewCRDTPDispatchable(test.input, nil)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := value.OK()
		if err != nil {
			t.Fatal(err)
		}
		invalidObservations = append(invalidObservations, map[string]any{"case": test.name, "ok": ok})
		closeDispatchable(t, value)
	}
	compare(t, fixtures, expectedChecks[4], invalidObservations)

	responseFactories := []struct {
		name    string
		factory func() (*gov8.CRDTPDispatchResponse, error)
	}{
		{"success", gov8.NewCRDTPSuccessResponse},
		{"fall_through", gov8.NewCRDTPFallThroughResponse},
		{"parse_error", func() (*gov8.CRDTPDispatchResponse, error) { return gov8.NewCRDTPParseError("parse") }},
		{"invalid_request", func() (*gov8.CRDTPDispatchResponse, error) { return gov8.NewCRDTPInvalidRequest("invalid request") }},
		{"method_not_found", func() (*gov8.CRDTPDispatchResponse, error) { return gov8.NewCRDTPMethodNotFound("not found") }},
		{"invalid_params", func() (*gov8.CRDTPDispatchResponse, error) { return gov8.NewCRDTPInvalidParams("invalid params") }},
		{"server_error", func() (*gov8.CRDTPDispatchResponse, error) { return gov8.NewCRDTPServerError("server\x00Ω") }},
	}
	responseObservations := make([]any, 0, len(responseFactories))
	for _, test := range responseFactories {
		response, err := test.factory()
		if err != nil {
			t.Fatal(err)
		}
		success, err := response.IsSuccess()
		if err != nil {
			t.Fatal(err)
		}
		isError, err := response.IsError()
		if err != nil {
			t.Fatal(err)
		}
		fallThrough, err := response.IsFallThrough()
		if err != nil {
			t.Fatal(err)
		}
		code, err := response.Code()
		if err != nil {
			t.Fatal(err)
		}
		message, err := response.Message()
		if err != nil {
			t.Fatal(err)
		}
		responseObservations = append(responseObservations, map[string]any{
			"case": test.name, "success": success, "error": isError,
			"fall_through": fallThrough, "code": code, "message": message,
		})
		closeResponse(t, response)
	}
	compare(t, fixtures, expectedChecks[5], responseObservations)

	invalidParams, err := gov8.NewCRDTPInvalidParams("bad\x00value")
	if err != nil {
		t.Fatal(err)
	}
	errorResponse, err := gov8.CreateCRDTPErrorResponse(123, invalidParams)
	if err != nil {
		t.Fatal(err)
	}
	serverError, err := gov8.NewCRDTPServerError("notify failed")
	if err != nil {
		t.Fatal(err)
	}
	errorNotification, err := gov8.CreateCRDTPErrorNotification(serverError)
	if err != nil {
		t.Fatal(err)
	}
	successEmpty, err := gov8.CreateCRDTPResponse(42, nil)
	if err != nil {
		t.Fatal(err)
	}
	nestedParams, err := gov8.CreateCRDTPNotification("Inner.event", nil)
	if err != nil {
		t.Fatal(err)
	}
	successWithParams, err := gov8.CreateCRDTPResponse(-5, nestedParams)
	if err != nil {
		t.Fatal(err)
	}
	notificationEmpty, err := gov8.CreateCRDTPNotification("Test.event", nil)
	if err != nil {
		t.Fatal(err)
	}
	nestedResponse, err := gov8.CreateCRDTPResponse(9, nil)
	if err != nil {
		t.Fatal(err)
	}
	notificationWithParams, err := gov8.CreateCRDTPNotification("Test.Ω", nestedResponse)
	if err != nil {
		t.Fatal(err)
	}
	notificationEmptyMethod, err := gov8.CreateCRDTPNotification("", nil)
	if err != nil {
		t.Fatal(err)
	}
	serializables := []struct {
		name  string
		value *gov8.CRDTPSerializable
	}{
		{"error_response", errorResponse}, {"error_notification", errorNotification},
		{"success_empty", successEmpty}, {"success_with_params", successWithParams},
		{"notification_empty", notificationEmpty}, {"notification_with_params", notificationWithParams},
		{"notification_empty_method", notificationEmptyMethod},
	}
	serializableObservations := make(map[string]any, len(serializables))
	for _, item := range serializables {
		first, err := item.value.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		second, err := item.value.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		serializableObservations[item.name] = map[string]any{
			"bytes_len": len(first), "bytes_hex": hex.EncodeToString(first),
			"json": mustJSON(t, first), "repeated_equal": bytes.Equal(first, second),
		}
		closeSerializable(t, item.value)
	}
	compare(t, fixtures, expectedChecks[6], serializableObservations)
}
