//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

type benchmarkInspectorChannel struct{ responses int }

func (c *benchmarkInspectorChannel) SendResponse(int32, *gov8.InspectorStringBuffer) {
	c.responses++
}
func (*benchmarkInspectorChannel) SendNotification(*gov8.InspectorStringBuffer) {}
func (*benchmarkInspectorChannel) FlushProtocolNotifications()                  {}

func BenchmarkInspectorDispatchProtocolMessage(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		b.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
		b.Fatal(err)
	}
	channel := &benchmarkInspectorChannel{}
	session, err := inspector.Connect(1, channel, gov8.EmptyInspectorStringView(), gov8.InspectorUntrusted)
	if err != nil {
		b.Fatal(err)
	}
	request := gov8.NewInspectorStringView8([]byte(`{"id":1,"method":"Runtime.evaluate","params":{"expression":"1+1","contextId":1,"returnByValue":true}}`))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := session.DispatchProtocolMessage(request); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if channel.responses != b.N {
		b.Fatalf("responses = %d, want %d", channel.responses, b.N)
	}
	if err := session.Close(); err != nil {
		b.Fatal(err)
	}
	if err := inspector.ContextDestroyed(ctx); err != nil {
		b.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		b.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		b.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		b.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		b.Fatal(err)
	}
}
