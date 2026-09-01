//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

type valueCallbackClient struct {
	ctx            *gov8.Context
	target         gov8.Value
	subtypeCalls   int
	description    int
	ensureGroups   []int32
	identity       bool
	contextMatches bool
	leaked         gov8.Value
	inspector      *gov8.Inspector
	closeErrors    []error
	borrowedClose  error
}

func (c *valueCallbackClient) observe(scope *gov8.CallbackScope, value gov8.Value) {
	c.identity, _ = value.StrictEquals(c.target)
	current, err := scope.Isolate().CurrentContext(scope.Scope())
	if err == nil {
		c.contextMatches, _ = current.SameAs(c.ctx)
	}
	c.leaked = value
	c.borrowedClose = scope.Scope().Close()
	c.closeErrors = append(c.closeErrors, c.inspector.ContextDestroyed(c.ctx), c.ctx.Close(), c.inspector.Close())
}
func (c *valueCallbackClient) ValueSubtype(scope *gov8.CallbackScope, value gov8.Value) *gov8.InspectorStringBuffer {
	c.subtypeCalls++
	c.observe(scope, value)
	return gov8.NewInspectorStringBuffer(gov8.NewInspectorStringView8([]byte("node")))
}
func (c *valueCallbackClient) DescriptionForValueSubtype(scope *gov8.CallbackScope, value gov8.Value) *gov8.InspectorStringBuffer {
	c.description++
	c.observe(scope, value)
	return gov8.NewInspectorStringBuffer(gov8.NewInspectorStringView16([]uint16{'m', 'a', 'r', 'k', 'e', 'd'}))
}
func (c *valueCallbackClient) EnsureDefaultContextInGroup(group int32) *gov8.Context {
	c.ensureGroups = append(c.ensureGroups, group)
	c.closeErrors = append(c.closeErrors, c.inspector.ContextDestroyed(c.ctx), c.ctx.Close(), c.inspector.Close())
	if group == 7 {
		return c.ctx
	}
	return nil
}

func TestInspectorClientValueCallbacksAndDefaultContext(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	entered, err := ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	defer entered.Close()
	script, err := ctx.Compile(scope, "globalThis.marked={}; globalThis.answer=41; marked", nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = script.Close()
	client := &valueCallbackClient{ctx: ctx, target: target}
	inspector, err := gov8.NewInspectorWithClient(iso, client)
	if err != nil {
		t.Fatal(err)
	}
	client.inspector = inspector
	if err := inspector.ContextCreated(ctx, 7, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	channel := &recordingInspectorChannel{}
	session, err := inspector.Connect(7, channel, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	missingChannel := &recordingInspectorChannel{}
	missing, err := inspector.Connect(99, missingChannel, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = missing.Close()
		_ = session.Close()
		_ = inspector.ContextDestroyed(ctx)
		_ = inspector.Close()
	})

	requests := []struct {
		session *gov8.InspectorSession
		body    string
	}{
		{session, `{"id":1,"method":"Runtime.evaluate","params":{"expression":"marked","contextId":1}}`},
		{session, `{"id":2,"method":"Runtime.evaluate","params":{"expression":"answer += 1"}}`},
		{session, `{"id":3,"method":"Runtime.evaluate","params":{"expression":"answer"}}`},
		{missing, `{"id":4,"method":"Runtime.evaluate","params":{"expression":"1"}}`},
	}
	for _, request := range requests {
		if err := request.session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(request.body))); err != nil {
			t.Fatal(err)
		}
	}
	responses := strings.Join(channel.responses, "\n")
	if !strings.Contains(responses, `"subtype":"node"`) || !strings.Contains(responses, `"description":"marked"`) ||
		strings.Count(responses, `"value":42`) != 2 {
		t.Fatalf("responses = %s", responses)
	}
	if client.subtypeCalls == 0 || client.description == 0 || !client.identity || !client.contextMatches {
		t.Fatalf("subtype=%d description=%d identity=%v context=%v", client.subtypeCalls, client.description, client.identity, client.contextMatches)
	}
	if len(client.ensureGroups) != 3 || client.ensureGroups[0] != 7 || client.ensureGroups[2] != 99 {
		t.Fatalf("ensure groups = %v", client.ensureGroups)
	}
	if !strings.Contains(strings.Join(missingChannel.responses, "\n"), "Cannot find default execution context") {
		t.Fatalf("missing response = %v", missingChannel.responses)
	}
	for index, err := range client.closeErrors {
		if err == nil {
			t.Fatalf("reentrant close %d unexpectedly succeeded", index)
		}
	}
	if _, err := client.leaked.IsObject(); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("borrowed value escaped callback: %v", err)
	}
	if client.borrowedClose == nil || !strings.Contains(client.borrowedClose.Error(), "borrowed callback scope") {
		t.Fatalf("borrowed scope close = %v", client.borrowedClose)
	}
}
