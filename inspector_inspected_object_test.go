//go:build windows && amd64

package gov8

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type inspectedObjectChannel struct {
	mu        sync.Mutex
	responses map[int32]string
}

func (c *inspectedObjectChannel) SendResponse(id int32, message *InspectorStringBuffer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.responses == nil {
		c.responses = make(map[int32]string)
	}
	c.responses[id] = message.StringView().String()
}
func (*inspectedObjectChannel) SendNotification(*InspectorStringBuffer) {}
func (*inspectedObjectChannel) FlushProtocolNotifications()             {}

type inspectedObjectRuntime struct {
	iso       *Isolate
	ctx       *Context
	scope     *Scope
	inspector *Inspector
	session   *InspectorSession
	channel   *inspectedObjectChannel
	globals   []*Global
	requestID int32
}

func newInspectedObjectRuntime(t *testing.T) *inspectedObjectRuntime {
	t.Helper()
	iso, err := NewIsolate()
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
	inspector, err := NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, EmptyInspectorStringView(),
		NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
		t.Fatal(err)
	}
	channel := &inspectedObjectChannel{}
	session, err := inspector.Connect(1, channel, NewInspectorStringView8([]byte(`{}`)), InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	r := &inspectedObjectRuntime{iso: iso, ctx: ctx, scope: scope, inspector: inspector, session: session, channel: channel}
	t.Cleanup(func() { r.close(t) })
	return r
}

func (r *inspectedObjectRuntime) close(t *testing.T) {
	t.Helper()
	if r.session != nil && !r.session.closed {
		if err := r.session.Close(); err != nil {
			t.Error(err)
		}
	}
	for _, global := range r.globals {
		if global != nil && !global.closed {
			if err := global.Close(); err != nil {
				t.Error(err)
			}
		}
	}
	if r.inspector != nil && r.ctx != nil {
		if _, ok := r.inspector.contexts[r.ctx]; ok {
			if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
				t.Error(err)
			}
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

func (r *inspectedObjectRuntime) evaluateString(t *testing.T, expression string) string {
	t.Helper()
	r.requestID++
	request, err := json.Marshal(map[string]any{
		"id": r.requestID, "method": "Runtime.evaluate",
		"params": map[string]any{"expression": expression, "contextId": 1,
			"includeCommandLineAPI": true, "returnByValue": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.session.DispatchProtocolMessage(NewInspectorStringView8(request)); err != nil {
		t.Fatal(err)
	}
	r.channel.mu.Lock()
	response := r.channel.responses[r.requestID]
	r.channel.mu.Unlock()
	var decoded struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			Result struct {
				Value any `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatalf("decode response %q: %v", response, err)
	}
	if len(decoded.Error) != 0 {
		t.Fatalf("protocol error: %s", decoded.Error)
	}
	value, ok := decoded.Result.Result.Value.(string)
	if !ok {
		t.Fatalf("response has no string value: %s", response)
	}
	return value
}

func (r *inspectedObjectRuntime) newProbe(t *testing.T, id, marker int32, drops *[]int32,
	getterHook func(*CallbackScope, *Context)) (*InspectorInspectable, *int) {
	t.Helper()
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	idValue, err := r.scope.Int32(id)
	if err != nil {
		t.Fatal(err)
	}
	markerValue, err := r.scope.Int32(marker)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := object.SetByName(r.scope, r.ctx, "id", idValue); err != nil || !ok {
		t.Fatalf("set id = %v, %v", ok, err)
	}
	if ok, err := object.SetByName(r.scope, r.ctx, "marker", markerValue); err != nil || !ok {
		t.Fatalf("set marker = %v, %v", ok, err)
	}
	global, err := NewGlobal(r.scope, object.Value)
	if err != nil {
		t.Fatal(err)
	}
	r.globals = append(r.globals, global)
	gets := new(int)
	inspectable, err := r.iso.NewInspectorInspectable(func(cs *CallbackScope, context *Context) (Value, error) {
		(*gets)++
		if getterHook != nil {
			getterHook(cs, context)
		}
		return global.ToLocal(cs.Scope())
	}, func() { *drops = append(*drops, id) })
	if err != nil {
		t.Fatal(err)
	}
	return inspectable, gets
}

func TestInspectorInspectableLifetimeAndBorrowedScope(t *testing.T) {
	r := newInspectedObjectRuntime(t)
	drops := []int32{}
	baseline := inspectorInspectableRegistryCount()
	unadded, unaddedGets := r.newProbe(t, -1, 0, &drops, nil)
	if inspectorInspectableRegistryCount() != baseline+1 {
		t.Fatal("unadded inspectable was not registered")
	}
	if err := unadded.Close(); err != nil {
		t.Fatal(err)
	}
	if *unaddedGets != 0 || fmt.Sprint(drops) != "[-1]" {
		t.Fatalf("unadded = gets %d drops %v", *unaddedGets, drops)
	}
	if err := unadded.Close(); err == nil {
		t.Fatal("second unadded Close succeeded")
	}

	var retainedScope *Scope
	var retainedValue Value
	contextMatched := true
	added, gets := r.newProbe(t, 1, 10, &drops, func(cs *CallbackScope, context *Context) {
		contextMatched = contextMatched && context == r.ctx
		retainedScope = cs.Scope()
		retainedValue, _ = retainedScope.Undefined()
	})
	if err := r.session.AddInspectedObject(added); err != nil {
		t.Fatal(err)
	}
	if err := added.Close(); err == nil || !strings.Contains(err.Error(), "transferred") {
		t.Fatalf("Close after transfer = %v", err)
	}
	if err := r.scope.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	r.scope, err = r.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if got := r.evaluateString(t, "[$0===$0,$0.id].join(',')"); got != "true,1" {
		t.Fatalf("evaluate = %q", got)
	}
	if *gets != 3 || !contextMatched {
		t.Fatalf("callback = gets %d context %v", *gets, contextMatched)
	}
	if _, err := retainedScope.Undefined(); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("borrowed scope after callback = %v", err)
	}
	if _, err := retainedValue.IsUndefined(); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("callback Value after callback = %v", err)
	}
	if err := r.session.Close(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(drops) != "[-1 1]" || inspectorInspectableRegistryCount() != baseline {
		t.Fatalf("session drop = %v registry %d/%d", drops, inspectorInspectableRegistryCount(), baseline)
	}
}

func TestInspectorInspectableEvictionAndReentrantGuards(t *testing.T) {
	r := newInspectedObjectRuntime(t)
	drops := []int32{}
	var closeErr, addErr, dispatchErr, destroyErr error
	var pending *InspectorInspectable
	first, _ := r.newProbe(t, 1, 10, &drops, func(*CallbackScope, *Context) {
		closeErr = r.session.Close()
		if pending != nil {
			addErr = r.session.AddInspectedObject(pending)
		}
		dispatchErr = r.session.DispatchProtocolMessage(NewInspectorStringView8([]byte(`{"id":99,"method":"Runtime.enable"}`)))
		destroyErr = r.inspector.ContextDestroyed(r.ctx)
	})
	pending, _ = r.newProbe(t, 99, 99, &drops, nil)
	if err := r.session.AddInspectedObject(first); err != nil {
		t.Fatal(err)
	}
	if got := r.evaluateString(t, "String($0.id)"); got != "1" {
		t.Fatal(got)
	}
	for name, err := range map[string]error{"close": closeErr, "dispatch": dispatchErr, "destroy": destroyErr} {
		if err == nil || (!strings.Contains(err.Error(), "active") && !strings.Contains(err.Error(), "callback")) {
			t.Fatalf("reentrant %s = %v", name, err)
		}
	}
	if addErr == nil || !strings.Contains(addErr.Error(), "during its callback") {
		t.Fatalf("reentrant add = %v", addErr)
	}
	if pending.transferred || pending.closed {
		t.Fatal("rejected reentrant Add consumed pending inspectable")
	}
	if err := pending.Close(); err != nil {
		t.Fatal(err)
	}
	for id := int32(2); id <= 7; id++ {
		value, _ := r.newProbe(t, id, id*10, &drops, nil)
		if err := r.session.AddInspectedObject(value); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.evaluateString(t, "[$0.id,$1.id,$2.id,$3.id,$4.id].join(',')"); got != "7,6,5,4,3" {
		t.Fatalf("retained = %q", got)
	}
	if !strings.Contains(fmt.Sprint(drops), "1") || !strings.Contains(fmt.Sprint(drops), "2") {
		t.Fatalf("synchronous eviction drops = %v", drops)
	}
}

func TestInspectorInspectableNegativeAndAffinity(t *testing.T) {
	r := newInspectedObjectRuntime(t)
	if _, err := (*Isolate)(nil).NewInspectorInspectable(func(*CallbackScope, *Context) (Value, error) { return Value{}, nil }, nil); err == nil {
		t.Fatal("nil isolate accepted")
	}
	if _, err := r.iso.NewInspectorInspectable(nil, nil); err == nil {
		t.Fatal("nil getter accepted")
	}
	if err := r.session.AddInspectedObject(nil); err == nil {
		t.Fatal("nil inspectable accepted")
	}
	if err := (*InspectorInspectable)(nil).Close(); err == nil {
		t.Fatal("nil Close accepted")
	}

	drops := []int32{}
	value, _ := r.newProbe(t, 1, 1, &drops, nil)
	errCh := make(chan error, 1)
	go func() { errCh <- r.session.AddInspectedObject(value) }()
	if err := <-errCh; err == nil || (!strings.Contains(err.Error(), "thread") && !strings.Contains(err.Error(), "affinity")) {
		t.Fatalf("wrong-thread Add = %v", err)
	}
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
	wrongThreadClose, _ := r.newProbe(t, 11, 11, &drops, nil)
	go func() { errCh <- wrongThreadClose.Close() }()
	if err := <-errCh; err == nil || (!strings.Contains(err.Error(), "thread") && !strings.Contains(err.Error(), "affinity")) {
		t.Fatalf("wrong-thread Close = %v", err)
	}
	if err := wrongThreadClose.Close(); err != nil {
		t.Fatal(err)
	}

	preOwnership, _ := r.newProbe(t, 2, 2, &drops, nil)
	forgedSession := &InspectorSession{inspector: r.inspector, channelID: r.session.channelID}
	if err := forgedSession.AddInspectedObject(preOwnership); err == nil {
		t.Fatal("null native session unexpectedly accepted")
	}
	if preOwnership.transferred || preOwnership.closed || preOwnership.handle == 0 {
		t.Fatal("pre-ownership native error consumed inspectable")
	}
	if err := preOwnership.Close(); err != nil {
		t.Fatal(err)
	}

	otherIso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	otherValue, err := otherIso.NewInspectorInspectable(func(*CallbackScope, *Context) (Value, error) { return Value{}, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.session.AddInspectedObject(otherValue); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign Add = %v", err)
	}
	if err := otherValue.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(otherIso); err != nil {
		t.Fatal(err)
	}
	if err := otherIso.Close(); err != nil {
		t.Fatal(err)
	}

	closedSessionValue, _ := r.newProbe(t, 3, 3, &drops, nil)
	if err := r.session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.session.AddInspectedObject(closedSessionValue); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("Add after session Close = %v", err)
	}
	if closedSessionValue.transferred {
		t.Fatal("closed-session Add consumed inspectable")
	}
	if err := closedSessionValue.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectorInspectableBlocksIsolateHostRelease(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	value, err := iso.NewInspectorInspectable(func(cs *CallbackScope, _ *Context) (Value, error) {
		return cs.Scope().Undefined()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(iso); err == nil || !strings.Contains(err.Error(), "Inspectable") {
		t.Fatalf("release with live Inspectable = %v", err)
	}
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectorInspectableRegistryOverflowRollback(t *testing.T) {
	r := newInspectedObjectRuntime(t)
	inspectorInspectables.Lock()
	old := inspectorInspectables.next
	inspectorInspectables.next = ^uint64(0)
	before := len(inspectorInspectables.entries)
	inspectorInspectables.Unlock()
	defer func() {
		inspectorInspectables.Lock()
		inspectorInspectables.next = old
		inspectorInspectables.Unlock()
	}()
	if _, err := r.iso.NewInspectorInspectable(func(*CallbackScope, *Context) (Value, error) { return Value{}, nil }, nil); err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("overflow = %v", err)
	}
	if got := inspectorInspectableRegistryCount(); got != before {
		t.Fatalf("overflow leaked registry entry: %d != %d", got, before)
	}
}

func TestInspectorInspectableCallbackPanicBoundary(t *testing.T) {
	if kind := os.Getenv("GOV8_INSPECTABLE_PANIC_HELPER"); kind != "" {
		r := newInspectedObjectRuntime(t)
		getter := func(*CallbackScope, *Context) (Value, error) {
			fmt.Fprintln(os.Stderr, "inspectable-entered")
			panic("inspector inspected-object callback panic boundary")
		}
		var onDrop func()
		if kind == "drop" {
			getter = func(cs *CallbackScope, _ *Context) (Value, error) { return cs.Scope().Undefined() }
			onDrop = func() {
				fmt.Fprintln(os.Stderr, "inspectable-entered")
				panic("inspector inspected-object drop callback panic boundary")
			}
		}
		value, err := r.iso.NewInspectorInspectable(getter, onDrop)
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(os.Stderr, "inspectable-before")
		if kind == "drop" {
			_ = value.Close()
		} else {
			if err := r.session.AddInspectedObject(value); err != nil {
				panic(err)
			}
			r.evaluateString(t, "String($0)")
		}
		fmt.Fprintln(os.Stderr, "inspectable-after")
		return
	}
	for _, kind := range []string{"get", "drop"} {
		command := exec.Command(os.Args[0], "-test.run=^TestInspectorInspectableCallbackPanicBoundary$")
		command.Env = append(os.Environ(), "GOV8_INSPECTABLE_PANIC_HELPER="+kind)
		output, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("%s panic helper succeeded: %s", kind, output)
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok || uint32(exitErr.ExitCode()) != uint32(0xC0000409) {
			t.Fatalf("%s exit = %v, output = %s", kind, err, output)
		}
		text := string(output)
		if !strings.Contains(text, "inspectable-before") || !strings.Contains(text, "inspectable-entered") || strings.Contains(text, "inspectable-after") {
			t.Fatalf("%s panic markers = %s", kind, text)
		}
	}
}

func TestInspectorInspectableInvalidReturnBoundary(t *testing.T) {
	if os.Getenv("GOV8_INSPECTABLE_INVALID_RETURN_HELPER") == "1" {
		r := newInspectedObjectRuntime(t)
		outside, err := r.scope.Int32(42)
		if err != nil {
			panic(err)
		}
		value, err := r.iso.NewInspectorInspectable(func(*CallbackScope, *Context) (Value, error) {
			fmt.Fprintln(os.Stderr, "invalid-return-entered")
			return outside, nil
		}, nil)
		if err != nil {
			panic(err)
		}
		if err := r.session.AddInspectedObject(value); err != nil {
			panic(err)
		}
		fmt.Fprintln(os.Stderr, "invalid-return-before")
		r.evaluateString(t, "String($0)")
		fmt.Fprintln(os.Stderr, "invalid-return-after")
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestInspectorInspectableInvalidReturnBoundary$")
	command.Env = append(os.Environ(), "GOV8_INSPECTABLE_INVALID_RETURN_HELPER=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid-return helper succeeded: %s", output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || uint32(exitErr.ExitCode()) != uint32(0xC0000409) {
		t.Fatalf("exit = %v, output = %s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "invalid-return-before") || !strings.Contains(text, "invalid-return-entered") || strings.Contains(text, "invalid-return-after") {
		t.Fatalf("invalid-return markers = %s", text)
	}
}

func TestInspectorInspectableContextDestroyedPreventsDereference(t *testing.T) {
	r := newInspectedObjectRuntime(t)
	drops := []int32{}
	value, gets := r.newProbe(t, 1, 1, &drops, nil)
	if err := r.session.AddInspectedObject(value); err != nil {
		t.Fatal(err)
	}
	if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
		t.Fatal(err)
	}
	request := NewInspectorStringView8([]byte(`{"id":77,"method":"Runtime.evaluate","params":{"expression":"String($0)","contextId":1,"includeCommandLineAPI":true}}`))
	if err := r.session.DispatchProtocolMessage(request); err != nil {
		t.Fatal(err)
	}
	if *gets != 0 {
		t.Fatalf("getter ran after ContextDestroyed: %d", *gets)
	}
	runtime.KeepAlive(request)
}
