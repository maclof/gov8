//go:build windows && amd64

package gov8_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

type clientConsoleObservation struct {
	group, level int32
	message, url gov8.InspectorStringView
	line, column uint32
	stack        bool
}

type callbackCapabilityClient struct {
	ids       []int64
	waiting   []int32
	resources []gov8.InspectorStringView
	console   []clientConsoleObservation
}

func (c *callbackCapabilityClient) GenerateUniqueID() int64 {
	id := int64(7001 + len(c.ids))
	c.ids = append(c.ids, id)
	return id
}
func (c *callbackCapabilityClient) RunIfWaitingForDebugger(group int32) {
	c.waiting = append(c.waiting, group)
}
func (c *callbackCapabilityClient) ResourceNameToURL(name gov8.InspectorStringView) *gov8.InspectorStringBuffer {
	c.resources = append(c.resources, name)
	switch name.String() {
	case "mapped.js":
		return gov8.NewInspectorStringBuffer(gov8.NewInspectorStringView8([]byte("client://mapped")))
	case "nul\x00name.js":
		return gov8.NewInspectorStringBuffer(gov8.NewInspectorStringView16([]uint16{'c', 'l', 'i', 'e', 'n', 't', ':', '/', '/', 'n', 'u', 'l'}))
	default:
		return nil
	}
}
func (c *callbackCapabilityClient) ConsoleAPIMessage(group, level int32,
	message, url gov8.InspectorStringView, line, column uint32, stack gov8.InspectorBorrowedStackTrace) {
	c.console = append(c.console, clientConsoleObservation{group, level, message, url, line, column, stack.Present()})
}

func TestInspectorClientOptionalCallbacksActualCDP(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	client := &callbackCapabilityClient{}
	inspector, err := gov8.NewInspectorWithClient(iso, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.ids) != 1 || client.ids[0] != 7001 {
		t.Fatalf("creation ids = %v", client.ids)
	}
	if err := inspector.ContextCreated(ctx, 7, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	channel := &recordingInspectorChannel{}
	session, err := inspector.Connect(7, channel, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		_ = inspector.ContextDestroyed(ctx)
		_ = inspector.Close()
	})
	for _, request := range []string{
		`{"id":1,"method":"Runtime.runIfWaitingForDebugger"}`,
		`{"id":2,"method":"Runtime.runIfWaitingForDebugger"}`,
		`{"id":3,"method":"Debugger.enable"}`,
	} {
		if err := session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(request))); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.waiting) != 2 || client.waiting[0] != 7 || client.waiting[1] != 7 {
		t.Fatalf("waiting groups = %v", client.waiting)
	}
	entered, err := ctx.Enter()
	if err != nil {
		t.Fatal(err)
	}
	defer entered.Close()
	for _, origin := range []string{"mapped.js", "plain.js", "nul\x00name.js"} {
		script, err := ctx.CompileWithOrigin(scope, "1", &gov8.Origin{ResourceName: origin}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := script.Run(scope, nil); err != nil {
			t.Fatal(err)
		}
		_ = script.Close()
	}
	if len(client.resources) < 3 {
		t.Fatalf("resource callbacks = %d", len(client.resources))
	}
	seenNUL16 := false
	for _, resource := range client.resources {
		if resource.String() == "nul\x00name.js" && !resource.Is8Bit() && resource.Len() == 11 {
			seenNUL16 = true
		}
	}
	if !seenNUL16 {
		t.Fatalf("missing UTF-16 NUL resource: %#v", client.resources)
	}
	notifications := strings.Join(channel.notifications, "\n")
	if !strings.Contains(notifications, `"url":"client://mapped"`) ||
		!strings.Contains(notifications, `"url":"client://nul"`) ||
		!strings.Contains(notifications, `"url":"plain.js"`) {
		t.Fatalf("resource mappings missing from scriptParsed notifications: %s", notifications)
	}
	script, err := ctx.CompileWithOrigin(scope, "console.warn('Ω')", &gov8.Origin{ResourceName: "console.js"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := script.Run(scope, nil); err != nil {
		t.Fatal(err)
	}
	_ = script.Close()
	if len(client.console) != 1 {
		t.Fatalf("console callbacks = %d", len(client.console))
	}
	got := client.console[0]
	if got.group != 7 || got.level != 16 || got.message.String() != "Ω" || got.url.String() != "console.js" || got.line != 1 || got.column != 9 || !got.stack {
		t.Fatalf("console callback = %+v", got)
	}
}

type panicCapabilityClient struct{ mode string }

func (c panicCapabilityClient) GenerateUniqueID() int64 {
	if c.mode == "generate" {
		panic("generate unique id panic")
	}
	return 9001
}
func (c panicCapabilityClient) RunIfWaitingForDebugger(int32) {
	if c.mode == "waiting" {
		panic("run if waiting panic")
	}
}
func (c panicCapabilityClient) ResourceNameToURL(gov8.InspectorStringView) *gov8.InspectorStringBuffer {
	if c.mode == "resource" {
		panic("resource name panic")
	}
	return nil
}
func (c panicCapabilityClient) ConsoleAPIMessage(int32, int32, gov8.InspectorStringView,
	gov8.InspectorStringView, uint32, uint32, gov8.InspectorBorrowedStackTrace) {
	if c.mode == "console" {
		panic("console message panic")
	}
}

func TestInspectorGenerateUniqueIDPanicIsFatal(t *testing.T) {
	if mode := os.Getenv("GOV8_INSPECTOR_CAPABILITY_PANIC_CHILD"); mode != "" {
		iso, _ := gov8.NewIsolate()
		client := panicCapabilityClient{mode: mode}
		inspector, _ := gov8.NewInspectorWithClient(iso, client)
		if mode == "generate" {
			os.Exit(91)
		}
		ctx, _ := iso.NewContext()
		scope, _ := iso.NewScope()
		_ = inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView())
		session, _ := inspector.Connect(1, &recordingInspectorChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
		switch mode {
		case "waiting":
			_ = session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(`{"id":1,"method":"Runtime.runIfWaitingForDebugger"}`)))
		case "resource":
			_ = session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(`{"id":1,"method":"Debugger.enable"}`)))
			script, _ := ctx.CompileWithOrigin(scope, "1", &gov8.Origin{ResourceName: "panic-resource.js"}, nil)
			_, _ = script.Run(scope, nil)
		case "console":
			script, _ := ctx.CompileWithOrigin(scope, "console.log('panic')", &gov8.Origin{ResourceName: "panic-console.js"}, nil)
			_, _ = script.Run(scope, nil)
		}
		os.Exit(91)
	}
	for _, mode := range []string{"generate", "waiting", "resource", "console"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestInspectorGenerateUniqueIDPanicIsFatal$")
		cmd.Env = append(os.Environ(), "GOV8_INSPECTOR_CAPABILITY_PANIC_CHILD="+mode)
		output, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || uint32(exitErr.ExitCode()) != 0xC0000409 {
			t.Fatalf("%s exit=%v, err=%v, output=%s", mode, func() int {
				if ok {
					return exitErr.ExitCode()
				}
				return 0
			}(), err, output)
		}
		if !strings.Contains(string(output), "panic in inspector client callback") {
			t.Fatalf("%s missing diagnostic: %s", mode, output)
		}
	}
}
