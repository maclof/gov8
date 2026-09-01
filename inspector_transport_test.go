//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

type recordingInspectorChannel struct {
	responses     []string
	notifications []string
	flushes       int
}

func (c *recordingInspectorChannel) SendResponse(_ int32, message *gov8.InspectorStringBuffer) {
	c.responses = append(c.responses, message.StringView().String())
}
func (c *recordingInspectorChannel) SendNotification(message *gov8.InspectorStringBuffer) {
	c.notifications = append(c.notifications, message.StringView().String())
}
func (c *recordingInspectorChannel) FlushProtocolNotifications() { c.flushes++ }

func TestInspectorStringViewAndBuffer(t *testing.T) {
	source8 := []byte{'a', 0, 0xd8, 0xff}
	view8 := gov8.NewInspectorStringView8(source8)
	source8[0] = 'z'
	if !view8.Is8Bit() || view8.IsEmpty() || view8.Len() != 4 {
		t.Fatalf("8-bit metadata = is8 %v, empty %v, len %d", view8.Is8Bit(), view8.IsEmpty(), view8.Len())
	}
	if got := view8.String(); got != "a\x00Øÿ" {
		t.Fatalf("8-bit String = %q", got)
	}
	chars8, ok := view8.Characters8()
	if !ok || chars8[0] != 'a' {
		t.Fatalf("Characters8 = %v, %v", chars8, ok)
	}
	chars8[0] = 'x'
	if view8.String()[0] != 'a' {
		t.Fatal("Characters8 exposed owned storage")
	}

	empty8 := gov8.EmptyInspectorStringView()
	empty16 := gov8.NewInspectorStringView16([]uint16{})
	if !empty8.Is8Bit() || empty16.Is8Bit() || !empty8.IsEmpty() || !empty16.IsEmpty() {
		t.Fatalf("empty encoding identity lost: 8=%v 16=%v", empty8.Is8Bit(), empty16.Is8Bit())
	}
	view16 := gov8.NewInspectorStringView16([]uint16{'A', 0, 0xd83d, 0xde00})
	if got := view16.String(); got != "A\x00😀" {
		t.Fatalf("16-bit String = %q", got)
	}
	buffer := gov8.NewInspectorStringBuffer(view16)
	copy1 := buffer.StringView()
	units, ok := copy1.Characters16()
	if !ok {
		t.Fatal("buffer lost 16-bit representation")
	}
	units[0] = 'Z'
	if got := buffer.StringView().String(); got != "A\x00😀" {
		t.Fatalf("buffer storage mutated through returned view: %q", got)
	}
}

func TestInspectorLifecycleAffinityAndProtocol(t *testing.T) {
	iso, ctx, _ := newTestRuntime(t)
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
		t.Fatal(err)
	}
	channel := &recordingInspectorChannel{}
	session, err := inspector.Connect(1, channel, gov8.NewInspectorStringView8([]byte(`{}`)), gov8.InspectorUntrusted)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err == nil || !strings.Contains(err.Error(), "live sessions") {
		t.Fatalf("Close with live session = %v", err)
	}
	request := gov8.NewInspectorStringView8([]byte(`{"id":7,"method":"Runtime.evaluate","params":{"expression":"6*7","contextId":1,"returnByValue":true}}`))
	if err := session.DispatchProtocolMessage(request); err != nil {
		t.Fatal(err)
	}
	if len(channel.responses) != 1 || !strings.Contains(channel.responses[0], `"value":42`) {
		t.Fatalf("responses = %#v", channel.responses)
	}
	for _, protocolMessage := range []string{
		`{"id":8,"method":"Runtime.enable"}`,
		`{"id":9,"method":"Runtime.evaluate","params":{"expression":"console.log('notice')","contextId":1}}`,
	} {
		if err := session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(protocolMessage))); err != nil {
			t.Fatal(err)
		}
	}
	if len(channel.notifications) == 0 {
		t.Fatal("Runtime.enable/console.log produced no inspector notification")
	}
	if channel.flushes == 0 {
		t.Fatal("inspector never flushed protocol notifications")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- session.DispatchProtocolMessage(request) }()
	if err := <-errCh; err == nil || (!strings.Contains(err.Error(), "affinity") && !strings.Contains(err.Error(), "wrong thread")) {
		t.Fatalf("wrong-thread dispatch = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.DispatchProtocolMessage(request); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("dispatch after Close = %v", err)
	}
	if err := inspector.Close(); err == nil || !strings.Contains(err.Error(), "registered contexts") {
		t.Fatalf("Close with registered context = %v", err)
	}
	if err := inspector.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("second Close = %v", err)
	}
}

func TestInspectorRejectsNilAndForeignContext(t *testing.T) {
	if _, err := gov8.NewInspector(nil); err == nil || !strings.Contains(err.Error(), "nil isolate") {
		t.Fatalf("NewInspector(nil) = %v", err)
	}
	iso, _, _ := newTestRuntime(t)
	_, foreign, _ := newTestRuntime(t)
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(foreign, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign context = %v", err)
	}
	if _, err := inspector.Connect(1, &recordingInspectorChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorClientTrustLevel(99)); err == nil || !strings.Contains(err.Error(), "trust level") {
		t.Fatalf("invalid trust level = %v", err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
}
