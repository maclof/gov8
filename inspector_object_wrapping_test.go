//go:build windows && amd64

package gov8

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

type iowTestChannel struct{}

func (iowTestChannel) SendResponse(int32, *InspectorStringBuffer) {}
func (iowTestChannel) SendNotification(*InspectorStringBuffer)    {}
func (iowTestChannel) FlushProtocolNotifications()                {}

type iowRuntime struct {
	iso       *Isolate
	inspector *Inspector
	session   *InspectorSession
	ctx       *Context
	scope     *Scope
}

func newIOWRuntime(t *testing.T) *iowRuntime {
	t.Helper()
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	session, err := inspector.Connect(1, iowTestChannel{}, EmptyInspectorStringView(), InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	result := &iowRuntime{iso: iso, inspector: inspector, session: session, ctx: ctx, scope: scope}
	t.Cleanup(func() { result.close(t) })
	return result
}

func (r *iowRuntime) register(t *testing.T) {
	t.Helper()
	if err := r.inspector.ContextCreated(r.ctx, 1, EmptyInspectorStringView(), EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
}

func (r *iowRuntime) close(t *testing.T) {
	t.Helper()
	if _, registered := r.inspector.contexts[r.ctx]; registered {
		if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
			t.Error(err)
		}
	}
	if r.session != nil && !r.session.closed {
		if err := r.session.Close(); err != nil {
			t.Error(err)
		}
	}
	if r.scope != nil && !r.scope.closed {
		if err := r.scope.Close(); err != nil {
			t.Error(err)
		}
	}
	if r.ctx != nil && !r.ctx.closed {
		if err := r.ctx.Close(); err != nil {
			t.Error(err)
		}
	}
	if r.inspector != nil && !r.inspector.closed {
		if err := r.inspector.Close(); err != nil {
			t.Error(err)
		}
	}
	if r.iso != nil && !r.iso.closed {
		if err := ReleaseIsolateHostState(r.iso); err != nil {
			t.Error(err)
		}
		if err := r.iso.Close(); err != nil {
			t.Error(err)
		}
	}
}

func iowCBORToJSON(t *testing.T, cbor []byte) []byte {
	t.Helper()
	var length uintptr
	status, _, _ := proc("gov8_iow_test_cbor_to_json").Call(
		slicePointer(cbor), uintptr(len(cbor)), 0, 0, uintptr(unsafe.Pointer(&length)))
	runtime.KeepAlive(cbor)
	if int64(status) < 0 && int64(status) != -4 {
		t.Fatal(shimError("CBORToJSON", status))
	}
	result := make([]byte, int(length))
	if err := callErr("CBORToJSON", proc("gov8_iow_test_cbor_to_json"),
		slicePointer(cbor), uintptr(len(cbor)), slicePointer(result), uintptr(len(result)),
		uintptr(unsafe.Pointer(&length))); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(cbor)
	runtime.KeepAlive(result)
	return result
}

func iowObjectID(t *testing.T, remote *InspectorRemoteObject) string {
	t.Helper()
	data, err := remote.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ObjectID string `json:"objectId"`
	}
	if err := json.Unmarshal(iowCBORToJSON(t, data), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ObjectID == "" {
		t.Fatal("remote object has no objectId")
	}
	return decoded.ObjectID
}

func TestInspectorWrapObjectPresenceAndUnwrapIdentity(t *testing.T) {
	r := newIOWRuntime(t)
	defer r.close(t)
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := r.scope.Int32(42)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := object.SetByName(r.scope, r.ctx, "marker", marker); err != nil || !ok {
		t.Fatalf("set marker = %v, %v", ok, err)
	}
	if remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value,
		EmptyInspectorStringView(), false); err != nil || present || remote != nil {
		t.Fatalf("wrap before registration = %v, %v, %v", remote, present, err)
	}
	r.register(t)
	remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value,
		NewInspectorStringView8([]byte("identity")), true)
	if err != nil || !present || remote == nil {
		t.Fatalf("wrap = %v, %v, %v", remote, present, err)
	}
	objectID := iowObjectID(t, remote)
	value, returnedContext, group, err := r.session.UnwrapObject(
		r.scope, NewInspectorStringView8([]byte(objectID)))
	if err != nil {
		t.Fatal(err)
	}
	if returnedContext != r.ctx {
		t.Fatal("UnwrapObject fabricated or returned the wrong Context wrapper")
	}
	if got := group.StringView(); got.Is8Bit() || got.String() != "identity" {
		t.Fatalf("group = 8bit:%v %q", got.Is8Bit(), got.String())
	}
	same, err := value.StrictEquals(object.Value)
	if err != nil || !same {
		t.Fatalf("identity = %v, %v", same, err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	same, err = value.StrictEquals(object.Value)
	if err != nil || !same {
		t.Fatalf("local after RemoteObject.Close = %v, %v", same, err)
	}

	global, err := NewGlobal(r.scope, value)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.scope.Close(); err != nil {
		t.Fatal(err)
	}
	r.scope, err = r.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	value, err = global.ToLocal(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := value.ToObject(r.scope, r.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	markerValue, ok, err := wrapped.GetByName(r.scope, r.ctx, "marker")
	if err != nil || !ok {
		t.Fatalf("copied local after scope rotation = %v, %v", ok, err)
	}
	markerInteger, ok, err := markerValue.IntegerValue(r.ctx)
	if err != nil || !ok || markerInteger != 42 {
		t.Fatalf("marker after scope rotation = %d, %v, %v", markerInteger, ok, err)
	}
	if err := global.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectorUnwrapObjectTypedErrorsAndViews(t *testing.T) {
	r := newIOWRuntime(t)
	defer r.close(t)
	r.register(t)
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	groupView := NewInspectorStringView16([]uint16{'r', 'e', 'l', 'e', 'a', 's', 'e'})
	remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value, groupView, false)
	if err != nil || !present {
		t.Fatalf("wrap = %v, %v", present, err)
	}
	id := iowObjectID(t, remote)
	if err := r.session.ReleaseObjectGroup(groupView); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = r.session.UnwrapObject(r.scope, NewInspectorStringView8([]byte(id)))
	var idErr *InspectorObjectIDError
	if !errors.As(err, &idErr) || idErr.Kind() != InspectorObjectIDNotFound ||
		idErr.Message().Is8Bit() || idErr.Error() != "Could not find object with given id" {
		t.Fatalf("released ID error = %#v, %v", idErr, err)
	}
	invalid := []InspectorStringView{
		NewInspectorStringView8([]byte("bad")),
		NewInspectorStringView16([]uint16{'b', 'a', 'd'}),
		NewInspectorStringView8([]byte{'b', 'a', 'd', 0, 'i', 'd'}),
		EmptyInspectorStringView(), NewInspectorStringView16(nil),
	}
	for index, view := range invalid {
		_, _, _, err := r.session.UnwrapObject(r.scope, view)
		idErr = nil
		if !errors.As(err, &idErr) || idErr.Kind() != InspectorObjectIDInvalid ||
			idErr.Message().Is8Bit() || idErr.Error() != "Invalid remote object id" {
			t.Fatalf("invalid %d = %#v, %v", index, idErr, err)
		}
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectorObjectWrappingSafetyBoundaries(t *testing.T) {
	a := newIOWRuntime(t)
	b := newIOWRuntime(t)
	a.register(t)
	b.register(t)
	objectA, err := a.scope.NewObject(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	objectB, err := b.scope.NewObject(b.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.session.WrapObject(b.scope, a.ctx, objectA.Value, EmptyInspectorStringView(), false); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope = %v", err)
	}
	if _, _, err := a.session.WrapObject(a.scope, b.ctx, objectA.Value, EmptyInspectorStringView(), false); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign context = %v", err)
	}
	if _, _, err := a.session.WrapObject(a.scope, a.ctx, objectB.Value, EmptyInspectorStringView(), false); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign value = %v", err)
	}
	remote, present, err := a.session.WrapObject(a.scope, a.ctx, objectA.Value,
		NewInspectorStringView8([]byte("safety")), false)
	if err != nil || !present {
		t.Fatalf("safety wrap = %v, %v", present, err)
	}
	objectID := iowObjectID(t, remote)
	if _, _, _, err := a.session.UnwrapObject(b.scope, NewInspectorStringView8([]byte(objectID))); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign Unwrap scope = %v", err)
	}
	threadResult := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, _, err := a.session.WrapObject(a.scope, a.ctx, objectA.Value, EmptyInspectorStringView(), false)
		threadResult <- err
	}()
	if err := <-threadResult; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong thread = %v", err)
	}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, _, _, err := a.session.UnwrapObject(a.scope, NewInspectorStringView8([]byte(objectID)))
		threadResult <- err
	}()
	if err := <-threadResult; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong-thread Unwrap = %v", err)
	}
	closedScope, err := a.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.session.UnwrapObject(closedScope, EmptyInspectorStringView()); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed scope = %v", err)
	}
	if err := a.inspector.ContextDestroyed(a.ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.session.WrapObject(a.scope, a.ctx, objectA.Value, EmptyInspectorStringView(), false); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed session = %v", err)
	}
	if _, _, _, err := a.session.UnwrapObject(a.scope, NewInspectorStringView8([]byte(objectID))); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed-session Unwrap = %v", err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	b.close(t)
	a.close(t)
}

func TestInspectorUnwrapObjectRequiresCurrentScope(t *testing.T) {
	r := newIOWRuntime(t)
	defer r.close(t)
	r.register(t)
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value,
		NewInspectorStringView8([]byte("scope")), false)
	if err != nil || !present {
		t.Fatalf("wrap = %v, %v", present, err)
	}
	defer func() {
		if err := remote.Close(); err != nil {
			t.Error(err)
		}
	}()
	id := NewInspectorStringView8([]byte(iowObjectID(t, remote)))

	inner, err := r.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.session.UnwrapObject(r.scope, id); err == nil || !strings.Contains(err.Error(), "innermost") {
		t.Fatalf("outer scope with inner normal scope = %v", err)
	}
	value, context, group, err := r.session.UnwrapObject(inner, id)
	if err != nil || context != r.ctx || group == nil {
		t.Fatalf("inner-scope Unwrap = %v, %v, %v", value, context, err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}

	escapable, err := r.scope.NewEscapableScope()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.session.UnwrapObject(r.scope, id); err == nil || !strings.Contains(err.Error(), "innermost") {
		t.Fatalf("outer scope with EscapableScope = %v", err)
	}
	if err := escapable.Close(); err != nil {
		t.Fatal(err)
	}
	if _, context, group, err := r.session.UnwrapObject(r.scope, id); err != nil || context != r.ctx || group == nil {
		t.Fatalf("outer scope after nested scopes close = %v, %v, %v", context, group, err)
	}
}

func TestInspectorObjectWrappingClosedInputsAndDestroyedContext(t *testing.T) {
	r := newIOWRuntime(t)
	defer r.close(t)
	r.register(t)
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value,
		EmptyInspectorStringView(), false)
	if err != nil || !present {
		t.Fatalf("wrap = %v, %v", present, err)
	}
	id := NewInspectorStringView8([]byte(iowObjectID(t, remote)))
	if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
		t.Fatal(err)
	}
	if value, context, group, err := r.session.UnwrapObject(r.scope, id); err == nil || value.h != 0 || context != nil || group != nil {
		t.Fatalf("Unwrap after ContextDestroyed = %v, %v, %v, %v", value, context, group, err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.session.WrapObject(r.scope, r.ctx, object.Value, EmptyInspectorStringView(), false); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed context = %v", err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}

	r2 := newIOWRuntime(t)
	defer r2.close(t)
	r2.register(t)
	inner, err := r2.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedValue, err := inner.NewObject(r2.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r2.session.WrapObject(r2.scope, r2.ctx, closedValue.Value, EmptyInspectorStringView(), false); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed value scope = %v", err)
	}
}

func TestInspectorObjectWrappingNilAndZeroInputs(t *testing.T) {
	var nilSession *InspectorSession
	if _, _, err := nilSession.WrapObject(nil, nil, Value{}, EmptyInspectorStringView(), false); err == nil {
		t.Fatal("nil session WrapObject succeeded")
	}
	if _, _, _, err := nilSession.UnwrapObject(nil, EmptyInspectorStringView()); err == nil {
		t.Fatal("nil session UnwrapObject succeeded")
	}
	var nilRemote *InspectorRemoteObject
	if _, err := nilRemote.ToBytes(); err == nil {
		t.Fatal("nil RemoteObject.ToBytes succeeded")
	}
	if err := nilRemote.Close(); err == nil {
		t.Fatal("nil RemoteObject.Close succeeded")
	}
	zeroRemote := &InspectorRemoteObject{}
	if _, err := zeroRemote.ToBytes(); err == nil {
		t.Fatal("zero RemoteObject.ToBytes succeeded")
	}
	if err := zeroRemote.Close(); err == nil {
		t.Fatal("zero RemoteObject.Close succeeded")
	}

	r := newIOWRuntime(t)
	defer r.close(t)
	r.register(t)
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.session.WrapObject(nil, r.ctx, object.Value, EmptyInspectorStringView(), false); err == nil {
		t.Fatal("nil scope accepted")
	}
	if _, _, err := r.session.WrapObject(r.scope, nil, object.Value, EmptyInspectorStringView(), false); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, _, err := r.session.WrapObject(r.scope, r.ctx, Value{}, EmptyInspectorStringView(), false); err == nil {
		t.Fatal("zero Value accepted")
	}
}

func TestInspectorRemoteObjectConcurrentToBytesAndCrossThreadClose(t *testing.T) {
	r := newIOWRuntime(t)
	defer r.close(t)
	r.register(t)
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value, EmptyInspectorStringView(), true)
	if err != nil || !present {
		t.Fatalf("wrap = %v, %v", present, err)
	}
	want, err := remote.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 9)
	for range 8 {
		go func() {
			<-start
			got, err := remote.ToBytes()
			if err != nil && !strings.Contains(err.Error(), "Close") {
				results <- err
				return
			}
			if err == nil && !bytes.Equal(got, want) {
				results <- errors.New("concurrent ToBytes changed serialization")
				return
			}
			results <- nil
		}()
	}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		<-start
		results <- remote.Close()
	}()
	close(start)
	for range 9 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if _, err := remote.ToBytes(); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("ToBytes after concurrent Close = %v", err)
	}
}

func TestInspectorRemoteObjectIndependentLifetime(t *testing.T) {
	r := newIOWRuntime(t)
	r.register(t)
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value,
		EmptyInspectorStringView(), true)
	if err != nil || !present {
		t.Fatalf("wrap = %v, %v", present, err)
	}
	before, err := remote.ToBytes()
	if err != nil || len(before) == 0 {
		t.Fatalf("before = %d, %v", len(before), err)
	}
	repeated, err := remote.ToBytes()
	if err != nil || !bytes.Equal(before, repeated) {
		t.Fatalf("repeated = %v, %v", bytes.Equal(before, repeated), err)
	}
	r.close(t)
	after, err := remote.ToBytes()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("after isolate = %v, %v", bytes.Equal(before, after), err)
	}
	threadResult := make(chan error, 1)
	go func() {
		data, err := remote.ToBytes()
		if err == nil && !bytes.Equal(before, data) {
			err = errors.New("cross-thread bytes changed")
		}
		threadResult <- err
	}()
	if err := <-threadResult; err != nil {
		t.Fatal(err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ToBytes(); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("ToBytes after Close = %v", err)
	}
	if err := remote.Close(); err == nil {
		t.Fatal("second Close succeeded")
	}
}
