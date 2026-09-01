//go:build windows && amd64

package gov8

import (
	"errors"
	"fmt"
	"testing"
)

type crdtpBenchmarkState struct {
	callbacks  int
	deliveries int
	err        error
}

func (s *crdtpBenchmarkState) fail(err error) {
	if s.err == nil {
		s.err = err
	}
}

type crdtpBenchmarkChannel struct{ state *crdtpBenchmarkState }

func (c *crdtpBenchmarkChannel) SendProtocolResponse(callID int32, message *CRDTPSerializable) {
	defer func() {
		if err := message.Close(); err != nil {
			c.state.fail(errors.Join(c.state.err, err))
		}
	}()
	bytes, err := message.Bytes()
	if err != nil {
		c.state.fail(err)
		return
	}
	json, ok, err := CRDTPCBORToJSON(bytes)
	if err != nil || !ok || callID != 1 || string(json) != `{"id":1,"result":{}}` {
		c.state.fail(fmt.Errorf("response call_id=%d json=%q ok=%v err=%v", callID, json, ok, err))
		return
	}
	c.state.deliveries++
}
func (*crdtpBenchmarkChannel) SendProtocolNotification(message *CRDTPSerializable) {
	_ = message.Close()
}
func (*crdtpBenchmarkChannel) FlushProtocolNotifications() {}

type crdtpBenchmarkDomain struct{ state *crdtpBenchmarkState }

func (d *crdtpBenchmarkDomain) Dispatch(command []byte, request *CRDTPDispatchRequest, responder *CRDTPDomainResponder) bool {
	d.state.callbacks++
	if string(command) != "ok" {
		d.state.fail(fmt.Errorf("command=%q", command))
		return false
	}
	callID, present, err := request.CallID()
	if err != nil || !present || callID != 1 {
		d.state.fail(fmt.Errorf("call ID=%d present=%v err=%v", callID, present, err))
		return false
	}
	response, err := NewCRDTPSuccessResponse()
	if err != nil {
		d.state.fail(err)
		return false
	}
	if err := responder.SendResponse(callID, response, nil); err != nil {
		d.state.fail(err)
		return false
	}
	return true
}

func BenchmarkCRDTPDispatcherDispatchSuccess(b *testing.B) {
	state := &crdtpBenchmarkState{}
	channel, err := NewCRDTPFrontendChannel(&crdtpBenchmarkChannel{state})
	if err != nil {
		b.Fatal(err)
	}
	dispatcher, err := NewCRDTPUberDispatcher(channel)
	if err != nil {
		_ = channel.Close()
		b.Fatal(err)
	}
	if err := dispatcher.WireDomain("Bench", &crdtpBenchmarkDomain{state}); err != nil {
		_ = dispatcher.Close()
		_ = channel.Close()
		b.Fatal(err)
	}
	cbor, ok, err := CRDTPJSONToCBOR([]byte(`{"id":1,"method":"Bench.ok","params":{}}`))
	if err != nil || !ok {
		_ = dispatcher.Close()
		_ = channel.Close()
		b.Fatalf("request conversion: ok=%v err=%v", ok, err)
	}
	message, err := NewCRDTPDispatchable(cbor, nil)
	if err != nil {
		_ = dispatcher.Close()
		_ = channel.Close()
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		callbacksBefore, deliveriesBefore := state.callbacks, state.deliveries
		if err := dispatcher.Dispatch(message); err != nil {
			b.Fatal(err)
		}
		if state.callbacks != callbacksBefore+1 || state.deliveries != deliveriesBefore+1 {
			b.Fatalf("iteration callbacks=%d->%d deliveries=%d->%d err=%v",
				callbacksBefore, state.callbacks, deliveriesBefore, state.deliveries, state.err)
		}
	}
	b.StopTimer()

	if state.err != nil {
		b.Fatal(state.err)
	}
	if state.callbacks != b.N || state.deliveries != b.N {
		b.Fatalf("callbacks=%d deliveries=%d iterations=%d", state.callbacks, state.deliveries, b.N)
	}
	if _, err := message.Method(); err != nil {
		b.Fatalf("borrowed dispatchable was not reusable: %v", err)
	}
	if err := message.Close(); err != nil {
		b.Error(err)
	}
	if err := dispatcher.Close(); err != nil {
		b.Error(err)
	}
	if err := channel.Close(); err != nil {
		b.Error(err)
	}
}

func TestCRDTPDispatcherDispatchSuccessAllocations(t *testing.T) {
	result := testing.Benchmark(BenchmarkCRDTPDispatcherDispatchSuccess)
	if allocations := result.AllocsPerOp(); allocations > 5 {
		t.Fatalf("dispatch success allocations = %d, want at most 5", allocations)
	}
}
