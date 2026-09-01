//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

type recordingInspectorClient struct {
	groups []int32
	quits  int
}

func (c *recordingInspectorClient) RunMessageLoopOnPause(group int32) {
	c.groups = append(c.groups, group)
}
func (c *recordingInspectorClient) QuitMessageLoopOnPause() { c.quits++ }

type closingInspectorClient struct {
	inspector    *gov8.Inspector
	session      *gov8.InspectorSession
	sessionErr   error
	inspectorErr error
}

func (c *closingInspectorClient) RunMessageLoopOnPause(int32) {
	c.sessionErr = c.session.Close()
	c.inspectorErr = c.inspector.Close()
}
func (c *closingInspectorClient) QuitMessageLoopOnPause() {}

func TestInspectorCanDispatchMethodRepresentations(t *testing.T) {
	tests := []struct {
		name string
		view gov8.InspectorStringView
		want bool
	}{
		{"known_u8", gov8.NewInspectorStringView8([]byte("Runtime.evaluate")), true},
		{"known_u16", gov8.NewInspectorStringView16([]uint16{'D', 'e', 'b', 'u', 'g', 'g', 'e', 'r', '.', 'e', 'n', 'a', 'b', 'l', 'e'}), true},
		{"unknown_domain", gov8.NewInspectorStringView8([]byte("Unknown.evaluate")), false},
		{"domain_prefix", gov8.NewInspectorStringView16([]uint16{'R', 'u', 'n', 't', 'i', 'm', 'e', '.', 'x'}), true},
		{"embedded_nul", gov8.NewInspectorStringView8([]byte("Runtime.\x00suffix")), true},
		{"empty_u8", gov8.EmptyInspectorStringView(), false},
		{"empty_u16", gov8.NewInspectorStringView16([]uint16{}), false},
	}
	for _, tt := range tests {
		got, err := gov8.InspectorCanDispatchMethod(tt.view)
		if err != nil || got != tt.want {
			t.Fatalf("%s = %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}
}

func TestInspectorSessionControlsLifecycleAndThread(t *testing.T) {
	iso, ctx, _ := newTestRuntime(t)
	client := &recordingInspectorClient{}
	inspector, err := gov8.NewInspectorWithClient(iso, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	session, err := inspector.Connect(1, &recordingInspectorChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.ReleaseObjectGroup(gov8.NewInspectorStringView8([]byte("unknown"))); err != nil {
		t.Fatal(err)
	}
	if err := session.SchedulePauseOnNextStatement(gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	if err := session.CancelPauseOnNextStatement(); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- session.CancelPauseOnNextStatement() }()
	if err := <-errCh; err == nil || (!strings.Contains(err.Error(), "thread") && !strings.Contains(err.Error(), "affinity")) {
		t.Fatalf("wrong-thread cancel = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.ReleaseObjectGroup(gov8.EmptyInspectorStringView()); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("release after close = %v", err)
	}
	if err := inspector.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("second close = %v", err)
	}
}

func TestNewInspectorWithClientRejectsNil(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	if _, err := gov8.NewInspectorWithClient(iso, nil); err == nil || !strings.Contains(err.Error(), "nil inspector client") {
		t.Fatalf("NewInspectorWithClient(nil) = %v", err)
	}
}

func TestInspectorCloseRejectsActiveClientCallback(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	client := &closingInspectorClient{}
	inspector, err := gov8.NewInspectorWithClient(iso, client)
	if err != nil {
		t.Fatal(err)
	}
	client.inspector = inspector
	if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	session, err := inspector.Connect(1, &recordingInspectorChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	client.session = session
	if err := session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(`{"id":1,"method":"Debugger.enable"}`))); err != nil {
		t.Fatal(err)
	}
	if err := session.SchedulePauseOnNextStatement(gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	script, err := ctx.Compile(scope, "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := script.Run(scope, nil); err != nil {
		t.Fatal(err)
	}
	if err := script.Close(); err != nil {
		t.Fatal(err)
	}
	if client.sessionErr == nil || !strings.Contains(client.sessionErr.Error(), "client callback is active") {
		t.Fatalf("InspectorSession.Close in callback = %v", client.sessionErr)
	}
	if client.inspectorErr == nil || !strings.Contains(client.inspectorErr.Error(), "live sessions") {
		t.Fatalf("Inspector.Close in callback = %v", client.inspectorErr)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkInspectorCanDispatchMethod(b *testing.B) {
	view := gov8.NewInspectorStringView8([]byte("Runtime.evaluate"))
	b.ResetTimer()
	for range b.N {
		if ok, err := gov8.InspectorCanDispatchMethod(view); err != nil || !ok {
			b.Fatal(ok, err)
		}
	}
}
