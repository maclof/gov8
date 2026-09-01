//go:build windows && amd64

package inspectortransportconformance

import (
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type channel struct {
	responses     int
	notifications int
	flushes       int
	last          string
}

func (c *channel) SendResponse(_ int32, message *gov8.InspectorStringBuffer) {
	c.responses++
	c.last = message.StringView().String()
}
func (c *channel) SendNotification(*gov8.InspectorStringBuffer) { c.notifications++ }
func (c *channel) FlushProtocolNotifications()                  { c.flushes++ }

func TestInspectorTransportProtocolCallbacks(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, gov8.NewInspectorStringView16([]uint16{}), gov8.NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
		t.Fatal(err)
	}
	c := &channel{}
	session, err := inspector.Connect(1, c, gov8.NewInspectorStringView8([]byte(`{}`)), gov8.InspectorUntrusted)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{
		`{"id":1,"method":"Runtime.enable"}`,
		`{"id":2,"method":"Runtime.evaluate","params":{"expression":"console.log('transport'); 21*2","contextId":1,"returnByValue":true}}`,
	} {
		if err := session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(request))); err != nil {
			t.Fatal(err)
		}
	}
	if c.responses != 2 || !strings.Contains(c.last, `"value":42`) || c.notifications == 0 || c.flushes == 0 {
		t.Fatalf("callbacks = responses %d notifications %d flushes %d last %s", c.responses, c.notifications, c.flushes, c.last)
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
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
