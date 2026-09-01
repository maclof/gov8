//go:build windows && amd64

package gov8

import "testing"

type drainingInspectorChannel struct{}

func (*drainingInspectorChannel) SendResponse(int32, *InspectorStringBuffer) {}
func (*drainingInspectorChannel) SendNotification(*InspectorStringBuffer)    {}
func (*drainingInspectorChannel) FlushProtocolNotifications()                {}

func TestInspectorChannelRegistryDrainsOnSessionClose(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, EmptyInspectorStringView(), EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	inspectorChannels.Lock()
	before := len(inspectorChannels.entries)
	inspectorChannels.Unlock()
	session, err := inspector.Connect(1, &drainingInspectorChannel{}, EmptyInspectorStringView(), InspectorUntrusted)
	if err != nil {
		t.Fatal(err)
	}
	inspectorChannels.Lock()
	during := len(inspectorChannels.entries)
	inspectorChannels.Unlock()
	if during != before+1 {
		t.Fatalf("registry size while connected = %d, want %d", during, before+1)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	inspectorChannels.Lock()
	after := len(inspectorChannels.entries)
	inspectorChannels.Unlock()
	if after != before {
		t.Fatalf("registry size after Close = %d, want %d", after, before)
	}
	if err := inspector.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
