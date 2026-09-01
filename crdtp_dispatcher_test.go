//go:build windows && amd64

package gov8

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type crdtpTestChannel struct {
	mu        sync.Mutex
	responses []string
	drops     int
	channel   *CRDTPFrontendChannelHandle
	closeErr  error
}

type crdtpRetainingChannel struct{ message *CRDTPSerializable }

func (c *crdtpRetainingChannel) SendProtocolResponse(_ int32, message *CRDTPSerializable) {
	c.message = message
}
func (c *crdtpRetainingChannel) SendProtocolNotification(message *CRDTPSerializable) {
	c.message = message
}
func (*crdtpRetainingChannel) FlushProtocolNotifications() {}

type crdtpViewRetainingChannel struct {
	message       *CRDTPSerializable
	first, second []byte
	err           error
}

func (c *crdtpViewRetainingChannel) SendProtocolResponse(_ int32, message *CRDTPSerializable) {
	c.message = message
	c.first, c.err = message.Bytes()
	if c.err == nil {
		c.second, c.err = message.Bytes()
	}
}
func (c *crdtpViewRetainingChannel) SendProtocolNotification(message *CRDTPSerializable) {
	c.message = message
}
func (*crdtpViewRetainingChannel) FlushProtocolNotifications() {}

type crdtpWrongThreadDomain struct {
	requestErr   error
	responderErr error
}

func (d *crdtpWrongThreadDomain) Dispatch(_ []byte, request *CRDTPDispatchRequest, responder *CRDTPDomainResponder) bool {
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, _, d.requestErr = request.CallID()
		response, err := NewCRDTPSuccessResponse()
		if err != nil {
			panic(err)
		}
		d.responderErr = responder.SendResponse(1, response, nil)
		_ = response.Close()
		close(done)
	}()
	<-done
	return false
}

func (c *crdtpTestChannel) SendProtocolResponse(_ int32, message *CRDTPSerializable) {
	data, err := message.Bytes()
	if err != nil {
		panic(err)
	}
	json, ok, err := CRDTPCBORToJSON(data)
	if err != nil || !ok {
		panic("invalid response")
	}
	c.mu.Lock()
	c.responses = append(c.responses, string(json))
	c.mu.Unlock()
	if c.channel != nil {
		c.closeErr = c.channel.Close()
	}
	if err := message.Close(); err != nil {
		panic(err)
	}
}
func (*crdtpTestChannel) SendProtocolNotification(message *CRDTPSerializable) {
	_ = message.Close()
}
func (*crdtpTestChannel) FlushProtocolNotifications() {}
func (c *crdtpTestChannel) CRDTPCallbackDropped() {
	c.mu.Lock()
	c.drops++
	c.mu.Unlock()
}

type crdtpTestDomain struct {
	dispatcher       *CRDTPUberDispatcher
	message          *CRDTPDispatchable
	reentrantMessage *CRDTPDispatchable
	request          *CRDTPDispatchRequest
	responder        *CRDTPDomainResponder
	closeErr         error
	messageCloseErr  error
	messageAccessErr error
	reentrantErr     error
	called           int
}

func (d *crdtpTestDomain) Dispatch(command []byte, request *CRDTPDispatchRequest, responder *CRDTPDomainResponder) bool {
	d.called++
	d.request, d.responder = request, responder
	if d.dispatcher != nil {
		d.closeErr = d.dispatcher.Close()
	}
	if d.message != nil {
		d.messageCloseErr = d.message.Close()
		_, d.messageAccessErr = d.message.Method()
	}
	if d.reentrantMessage != nil {
		d.reentrantErr = d.dispatcher.Dispatch(d.reentrantMessage)
	}
	id, _, err := request.CallID()
	if err != nil {
		panic(err)
	}
	if string(command) != "run" {
		return false
	}
	response, err := NewCRDTPSuccessResponse()
	if err != nil {
		panic(err)
	}
	if err := responder.SendResponse(id, response, nil); err != nil {
		panic(err)
	}
	if _, err := response.Code(); !errors.Is(err, errCRDTPConsumed) {
		panic("response was not consumed")
	}
	return true
}

func TestCRDTPDispatcherRoutingLifecycleAndBorrowedValues(t *testing.T) {
	channelHandler := &crdtpTestChannel{}
	channel, err := NewCRDTPFrontendChannel(channelHandler)
	if err != nil {
		t.Fatal(err)
	}
	channelHandler.channel = channel
	dispatcher, err := NewCRDTPUberDispatcher(channel)
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(); err == nil {
		t.Fatal("channel closed before attached dispatcher")
	}
	domain := &crdtpTestDomain{dispatcher: dispatcher}
	if err := dispatcher.WireDomain("Test", domain); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.WireDomain("Test", domain); err == nil {
		t.Fatal("duplicate domain accepted")
	}
	message := mustCRDTPCBORT(t, `{"id":7,"method":"Test.run","params":{}}`)
	dispatchable, err := NewCRDTPDispatchable(message, []byte{0, 0xff, 1})
	if err != nil {
		t.Fatal(err)
	}
	reentrantMessage, err := NewCRDTPDispatchable(mustCRDTPCBORT(t, `{"id":8,"method":"Test.run"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	domain.message = dispatchable
	domain.reentrantMessage = reentrantMessage
	if err := dispatcher.Dispatch(dispatchable); err != nil {
		t.Fatal(err)
	}
	if domain.closeErr == nil || !strings.Contains(domain.closeErr.Error(), "active") {
		t.Fatalf("reentrant Close error=%v", domain.closeErr)
	}
	if domain.messageCloseErr == nil || domain.messageAccessErr == nil ||
		!strings.Contains(domain.messageCloseErr.Error(), "active") ||
		!strings.Contains(domain.messageAccessErr.Error(), "active") {
		t.Fatalf("reentrant message errors: close=%v accessor=%v", domain.messageCloseErr, domain.messageAccessErr)
	}
	if domain.reentrantErr == nil || !strings.Contains(domain.reentrantErr.Error(), "active") {
		t.Fatalf("reentrant Dispatch error=%v", domain.reentrantErr)
	}
	if channelHandler.closeErr == nil || !strings.Contains(channelHandler.closeErr.Error(), "active") {
		t.Fatalf("channel Close during callback error=%v", channelHandler.closeErr)
	}
	if got, err := dispatchable.AssociatedData(); err != nil || !bytes.Equal(got, []byte{0, 0xff, 1}) {
		t.Fatalf("post-dispatch associated=%x err=%v", got, err)
	}
	if _, _, err := domain.request.CallID(); err == nil {
		t.Fatal("captured request remained usable")
	}
	response, err := NewCRDTPSuccessResponse()
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.responder.SendResponse(7, response, nil); err == nil {
		t.Fatal("captured responder remained usable")
	}
	_ = response.Close()
	if domain.called != 1 || len(channelHandler.responses) != 1 || channelHandler.responses[0] != `{"id":7,"result":{}}` {
		t.Fatalf("called=%d responses=%v", domain.called, channelHandler.responses)
	}
	if err := dispatchable.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reentrantMessage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Close(); err != nil {
		t.Fatalf("double dispatcher Close: %v", err)
	}
	if err := dispatcher.Dispatch(dispatchable); !errors.Is(err, errCRDTPClosed) {
		t.Fatalf("dispatch after Close: %v", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(); err != nil {
		t.Fatalf("double channel Close: %v", err)
	}
	if channelHandler.drops != 1 {
		t.Fatalf("channel drops=%d", channelHandler.drops)
	}
	crdtpCallbacks.Lock()
	_, channelRegistered := crdtpCallbacks.channels[channel.id]
	domainRegistered := false
	for _, id := range dispatcher.domains {
		if crdtpCallbacks.domains[id] != nil {
			domainRegistered = true
		}
	}
	crdtpCallbacks.Unlock()
	if channelRegistered || domainRegistered {
		t.Fatalf("callback registry did not drain: channel=%v domain=%v", channelRegistered, domainRegistered)
	}
}

type crdtpBlockingDomain struct {
	entered chan struct{}
	release chan struct{}
}

func (d *crdtpBlockingDomain) Dispatch([]byte, *CRDTPDispatchRequest, *CRDTPDomainResponder) bool {
	close(d.entered)
	<-d.release
	return false
}

func TestCRDTPDispatcherRejectsConcurrentOperations(t *testing.T) {
	channel, err := NewCRDTPFrontendChannel(&crdtpTestChannel{})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCRDTPUberDispatcher(channel)
	if err != nil {
		t.Fatal(err)
	}
	domain := &crdtpBlockingDomain{entered: make(chan struct{}), release: make(chan struct{})}
	if err := dispatcher.WireDomain("Block", domain); err != nil {
		t.Fatal(err)
	}
	one, err := NewCRDTPDispatchable(mustCRDTPCBORT(t, `{"id":1,"method":"Block.run"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	two, err := NewCRDTPDispatchable(mustCRDTPCBORT(t, `{"id":2,"method":"Block.run"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- dispatcher.Dispatch(one) }()
	<-domain.entered
	if err := one.Close(); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("message Close during Dispatch: %v", err)
	}
	if err := dispatcher.Dispatch(two); err == nil {
		t.Fatal("concurrent Dispatch accepted")
	}
	if err := dispatcher.Close(); err == nil {
		t.Fatal("Close during Dispatch accepted")
	}
	close(domain.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = one.Close()
	_ = two.Close()
	if err := dispatcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCRDTPDispatcherRejectsMalformedDispatchable(t *testing.T) {
	handler := &crdtpTestChannel{}
	channel, err := NewCRDTPFrontendChannel(handler)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCRDTPUberDispatcher(channel)
	if err != nil {
		t.Fatal(err)
	}
	domain := &crdtpTestDomain{}
	if err := dispatcher.WireDomain("Test", domain); err != nil {
		t.Fatal(err)
	}
	message, err := NewCRDTPDispatchable([]byte{0xff, 0xfe}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(message); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("malformed Dispatch error=%v", err)
	}
	if domain.called != 0 || len(handler.responses) != 0 {
		t.Fatalf("malformed Dispatch invoked callbacks: domain=%d responses=%v", domain.called, handler.responses)
	}
	ok, err := message.OK()
	if err != nil || ok {
		t.Fatalf("message after rejected Dispatch: ok=%v err=%v", ok, err)
	}
	_ = message.Close()
	_ = dispatcher.Close()
	_ = channel.Close()
}

func TestCRDTPDispatcherOwnedChannelMessageAndWrongThreadBorrow(t *testing.T) {
	handler := &crdtpRetainingChannel{}
	channel, err := NewCRDTPFrontendChannel(handler)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCRDTPUberDispatcher(channel)
	if err != nil {
		t.Fatal(err)
	}
	domain := &crdtpWrongThreadDomain{}
	if err := dispatcher.WireDomain("Thread", domain); err != nil {
		t.Fatal(err)
	}
	message, err := NewCRDTPDispatchable(mustCRDTPCBORT(t, `{"id":1,"method":"Thread.run"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(message); err != nil {
		t.Fatal(err)
	}
	if domain.requestErr == nil || domain.responderErr == nil ||
		!strings.Contains(domain.requestErr.Error(), "another thread") ||
		!strings.Contains(domain.responderErr.Error(), "another thread") {
		t.Fatalf("wrong-thread errors: request=%v responder=%v", domain.requestErr, domain.responderErr)
	}
	if handler.message == nil {
		t.Fatal("automatic response was not delivered")
	}
	data, err := handler.message.Bytes()
	if err != nil {
		t.Fatalf("retained owned message: %v", err)
	}
	if json, ok, err := CRDTPCBORToJSON(data); err != nil || !ok ||
		string(json) != `{"id":1,"error":{"code":-32601,"message":"'Thread.run' wasn't found"}}` {
		t.Fatalf("retained response=%q ok=%v err=%v", json, ok, err)
	}
	_ = handler.message.Close()
	_ = message.Close()
	_ = dispatcher.Close()
	_ = channel.Close()
}

func TestCRDTPDispatcherCallbackViewClearsForRetainedMessage(t *testing.T) {
	handler := &crdtpViewRetainingChannel{}
	channel, err := NewCRDTPFrontendChannel(handler)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCRDTPUberDispatcher(channel)
	if err != nil {
		t.Fatal(err)
	}
	domain := &crdtpTestDomain{}
	if err := dispatcher.WireDomain("Test", domain); err != nil {
		t.Fatal(err)
	}
	message, err := NewCRDTPDispatchable(mustCRDTPCBORT(t, `{"id":9,"method":"Test.run"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(message); err != nil {
		t.Fatal(err)
	}
	if handler.err != nil || handler.message == nil || !bytes.Equal(handler.first, handler.second) {
		t.Fatalf("callback Bytes repeats: first=%x second=%x message=%v err=%v",
			handler.first, handler.second, handler.message != nil, handler.err)
	}
	if handler.message.callbackView != 0 || handler.message.callbackViewLen != 0 {
		t.Fatalf("retained message kept callback view %x/%d",
			handler.message.callbackView, handler.message.callbackViewLen)
	}
	retained, err := handler.message.Bytes()
	if err != nil || !bytes.Equal(retained, handler.first) {
		t.Fatalf("retained Bytes=%x want=%x err=%v", retained, handler.first, err)
	}
	json, ok, err := CRDTPCBORToJSON(retained)
	if err != nil || !ok || string(json) != `{"id":9,"result":{}}` {
		t.Fatalf("retained JSON=%q ok=%v err=%v", json, ok, err)
	}
	_ = handler.message.Close()
	_ = message.Close()
	_ = dispatcher.Close()
	_ = channel.Close()
}

type crdtpTestFallthrough struct {
	calls        int
	drops        int
	data         []byte
	message      *CRDTPDispatchable
	dropCloseErr error
}

func (f *crdtpTestFallthrough) FallThrough(_ int32, _, _ []byte, associated []byte) {
	f.calls++
	f.data = append([]byte(nil), associated...)
}
func (f *crdtpTestFallthrough) CRDTPCallbackDropped() {
	f.drops++
	if f.message != nil {
		f.dropCloseErr = f.message.Close()
	}
}

func TestCRDTPFallthroughOwnership(t *testing.T) {
	crdtpCallbacks.Lock()
	fallBefore := len(crdtpCallbacks.fall)
	crdtpCallbacks.Unlock()
	channel, err := NewCRDTPFrontendChannel(&crdtpTestChannel{})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCRDTPUberDispatcher(channel)
	if err != nil {
		t.Fatal(err)
	}
	handler := &crdtpTestFallthrough{}
	message, err := NewCRDTPDispatchableWithFallthrough(
		mustCRDTPCBORT(t, `{"id":9,"method":"Missing.run"}`), []byte("metadata"), handler)
	if err != nil {
		t.Fatal(err)
	}
	handler.message = message
	if err := dispatcher.Dispatch(message); err != nil {
		t.Fatal(err)
	}
	if handler.calls != 1 || handler.drops != 1 {
		t.Fatalf("calls=%d drops=%d", handler.calls, handler.drops)
	}
	if err := dispatcher.Dispatch(message); err != nil {
		t.Fatal(err)
	}
	if handler.calls != 1 || handler.drops != 1 {
		t.Fatalf("one-shot callback calls=%d drops=%d", handler.calls, handler.drops)
	}
	if err := message.Close(); err != nil {
		t.Fatal(err)
	}
	if handler.dropCloseErr == nil || !strings.Contains(handler.dropCloseErr.Error(), "active") {
		t.Fatalf("reentrant Close from fallthrough drop: %v", handler.dropCloseErr)
	}
	_ = dispatcher.Close()
	_ = channel.Close()
	crdtpCallbacks.Lock()
	fallAfter := len(crdtpCallbacks.fall)
	crdtpCallbacks.Unlock()
	if fallAfter != fallBefore {
		t.Fatalf("fallthrough registry did not drain: before=%d after=%d", fallBefore, fallAfter)
	}
}

func TestCRDTPDispatcherRegistryOverflowRollsBack(t *testing.T) {
	if err := ensureCRDTPDispatchers(); err != nil {
		t.Fatal(err)
	}
	crdtpCallbacks.Lock()
	oldNext := crdtpCallbacks.next
	before := len(crdtpCallbacks.channels)
	crdtpCallbacks.next = math.MaxUint64
	crdtpCallbacks.Unlock()
	defer func() {
		crdtpCallbacks.Lock()
		crdtpCallbacks.next = oldNext
		crdtpCallbacks.Unlock()
	}()
	if _, err := NewCRDTPFrontendChannel(&crdtpTestChannel{}); err == nil {
		t.Fatal("registry overflow accepted")
	}
	crdtpCallbacks.Lock()
	after := len(crdtpCallbacks.channels)
	crdtpCallbacks.Unlock()
	if after != before {
		t.Fatalf("registry size changed: before=%d after=%d", before, after)
	}
}

type crdtpNestedOutStress struct {
	callbacks, deliveries int
	err                   error
}

func (s *crdtpNestedOutStress) SendProtocolResponse(callID int32, message *CRDTPSerializable) {
	data, err := message.Bytes()
	if err == nil {
		var ok bool
		data, ok, err = CRDTPCBORToJSON(data)
		if err == nil && (!ok || callID != 1 || string(data) != `{"id":1,"result":{}}`) {
			err = errors.New("unexpected stress response")
		}
	}
	if closeErr := message.Close(); err == nil {
		err = closeErr
	}
	if err != nil && s.err == nil {
		s.err = err
		return
	}
	s.deliveries++
}
func (*crdtpNestedOutStress) SendProtocolNotification(message *CRDTPSerializable) {
	_ = message.Close()
}
func (*crdtpNestedOutStress) FlushProtocolNotifications() {}

func (s *crdtpNestedOutStress) Dispatch(command []byte, request *CRDTPDispatchRequest, responder *CRDTPDomainResponder) bool {
	s.callbacks++
	callID, present, err := request.CallID()
	method, methodErr := request.Method()
	if err != nil || methodErr != nil || !present || callID != 1 ||
		string(command) != "ok" || string(method) != "Stress.ok" {
		if s.err == nil {
			s.err = fmt.Errorf("stress request command=%q method=%q id=%d present=%v errors=%v/%v",
				command, method, callID, present, err, methodErr)
		}
		return false
	}
	response, err := NewCRDTPSuccessResponse()
	if err == nil {
		err = responder.SendResponse(callID, response, nil)
	}
	if err != nil {
		if s.err == nil {
			s.err = err
		}
		return false
	}
	return true
}

func TestCRDTPDispatcherNestedOutParametersStress(t *testing.T) {
	const iterations = 350000
	state := &crdtpNestedOutStress{}
	channel, err := NewCRDTPFrontendChannel(state)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewCRDTPUberDispatcher(channel)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.WireDomain("Stress", state); err != nil {
		t.Fatal(err)
	}
	message, err := NewCRDTPDispatchable(mustCRDTPCBORT(t,
		`{"id":1,"method":"Stress.ok","params":{}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	for range iterations {
		if err := dispatcher.Dispatch(message); err != nil {
			t.Fatalf("iteration %d: %v", state.callbacks, err)
		}
		if state.err != nil {
			t.Fatalf("iteration %d callback: %v", state.callbacks, state.err)
		}
	}
	if state.callbacks != iterations || state.deliveries != iterations {
		t.Fatalf("callbacks=%d deliveries=%d", state.callbacks, state.deliveries)
	}
	if err := message.Close(); err != nil {
		t.Error(err)
	}
	if err := dispatcher.Close(); err != nil {
		t.Error(err)
	}
	if err := channel.Close(); err != nil {
		t.Error(err)
	}
}
