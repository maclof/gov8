//go:build windows && amd64

package gov8

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

func mustCRDTPCBORT(t *testing.T, text string) []byte {
	t.Helper()
	result, ok, err := CRDTPJSONToCBOR([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("valid JSON rejected: %q", text)
	}
	return result
}

func TestCRDTPConversionsAndMalformedInput(t *testing.T) {
	cbor := mustCRDTPCBORT(t, `{"text":"a\u0000Ω","items":[1,true,null]}`)
	json, ok, err := CRDTPCBORToJSON(cbor)
	if err != nil || !ok {
		t.Fatalf("round trip: ok=%v err=%v", ok, err)
	}
	if got, want := string(json), `{"text":"a\u0000\u03a9","items":[1,true,null]}`; got != want {
		t.Fatalf("round trip = %q, want %q", got, want)
	}
	for _, input := range [][]byte{nil, {}, []byte("{"), []byte("garbage")} {
		if got, some, err := CRDTPJSONToCBOR(input); err != nil || some || got != nil {
			t.Fatalf("invalid JSON %q: got=%x some=%v err=%v", input, got, some, err)
		}
	}
	for _, input := range [][]byte{nil, {}, {0xff}, cbor[:len(cbor)/2]} {
		if got, some, err := CRDTPCBORToJSON(input); err != nil || some || got != nil {
			t.Fatalf("invalid CBOR %x: got=%q some=%v err=%v", input, got, some, err)
		}
	}
}

func TestCRDTPDispatchableOwnershipAndBoundaries(t *testing.T) {
	cbor := mustCRDTPCBORT(t, `{"id":42,"method":"Test.run","sessionId":"s","params":{"x":1}}`)
	wantCBOR := append([]byte(nil), cbor...)
	associated := []byte{1, 2, 3}
	d, err := NewCRDTPDispatchable(cbor, associated)
	if err != nil {
		t.Fatal(err)
	}
	cbor[0] ^= 0xff
	associated[0] = 9
	ok, err := d.OK()
	if err != nil || !ok {
		t.Fatalf("OK=%v err=%v", ok, err)
	}
	id, has, err := d.CallID()
	if err != nil || !has || id != 42 {
		t.Fatalf("CallID=(%d,%v,%v)", id, has, err)
	}
	method, err := d.Method()
	if err != nil || string(method) != "Test.run" {
		t.Fatalf("Method=%q err=%v", method, err)
	}
	method[0] = 'X'
	methodAgain, err := d.Method()
	if err != nil || string(methodAgain) != "Test.run" {
		t.Fatalf("second Method=%q err=%v", methodAgain, err)
	}
	gotAssociated, err := d.AssociatedData()
	if err != nil || !bytes.Equal(gotAssociated, []byte{1, 2, 3}) {
		t.Fatalf("AssociatedData=%x err=%v", gotAssociated, err)
	}
	if _, ok, err := CRDTPCBORToJSON(wantCBOR); err != nil || !ok {
		t.Fatalf("source copy sanity: ok=%v err=%v", ok, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("double Close: %v", err)
	}
	if _, err := d.Method(); !errors.Is(err, errCRDTPClosed) {
		t.Fatalf("Method after Close: %v", err)
	}
	if _, err := d.OK(); !errors.Is(err, errCRDTPClosed) {
		t.Fatalf("OK after Close: %v", err)
	}
	if err := (*CRDTPDispatchable)(nil).Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

func TestCRDTPInvalidDispatchableAccessorsAreSafe(t *testing.T) {
	d, err := NewCRDTPDispatchable([]byte{0xff}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ok, err := d.OK()
	if err != nil || ok {
		t.Fatalf("OK=%v err=%v", ok, err)
	}
	if _, _, err := d.CallID(); err == nil {
		t.Fatal("CallID accepted invalid Dispatchable")
	}
	if _, err := d.Method(); err == nil {
		t.Fatal("Method accepted invalid Dispatchable")
	}
}

func TestCRDTPResponsesAndConsumption(t *testing.T) {
	response, err := NewCRDTPServerError("server\x00Ω")
	if err != nil {
		t.Fatal(err)
	}
	isError, err := response.IsError()
	if err != nil || !isError {
		t.Fatalf("IsError=%v err=%v", isError, err)
	}
	code, err := response.Code()
	if err != nil || code != -32000 {
		t.Fatalf("Code=%d err=%v", code, err)
	}
	message, err := response.Message()
	if err != nil || message != "server\x00Ω" {
		t.Fatalf("Message=%q err=%v", message, err)
	}
	artifact, err := CreateCRDTPErrorNotification(response)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = artifact.Close() }()
	if _, err := response.Code(); !errors.Is(err, errCRDTPConsumed) {
		t.Fatalf("response not consumed: %v", err)
	}
	if _, err := CreateCRDTPErrorNotification(response); !errors.Is(err, errCRDTPConsumed) {
		t.Fatalf("response consumed twice: %v", err)
	}
	if err := response.Close(); err != nil {
		t.Fatalf("Close consumed response: %v", err)
	}
	if err := response.Close(); err != nil {
		t.Fatalf("double Close consumed response: %v", err)
	}
}

func TestCRDTPSerializablePrevalidationAndConsumption(t *testing.T) {
	params, err := CreateCRDTPResponse(7, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := params.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCRDTPNotification("bad\x00method", params); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("interior-NUL error=%v", err)
	}
	after, err := params.Bytes()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed prevalidation consumed/mutated params: equal=%v err=%v", bytes.Equal(before, after), err)
	}
	outer, err := CreateCRDTPNotification("Test.event", params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := params.Bytes(); !errors.Is(err, errCRDTPConsumed) {
		t.Fatalf("params not consumed: %v", err)
	}
	if _, err := CreateCRDTPResponse(1, params); !errors.Is(err, errCRDTPConsumed) {
		t.Fatalf("params consumed twice: %v", err)
	}
	if err := params.Close(); err != nil {
		t.Fatalf("Close consumed params: %v", err)
	}
	if err := outer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := outer.Close(); err != nil {
		t.Fatalf("double Close: %v", err)
	}
	if _, err := outer.Bytes(); !errors.Is(err, errCRDTPClosed) {
		t.Fatalf("Bytes after Close: %v", err)
	}
}

func TestCRDTPSerializableConcurrentBytesAndClose(t *testing.T) {
	serializable, err := CreateCRDTPResponse(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := serializable.Bytes()
		if err != nil && !errors.Is(err, errCRDTPClosed) {
			t.Errorf("Bytes: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := serializable.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	wg.Wait()
}
