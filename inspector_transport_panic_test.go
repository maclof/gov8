//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

type panickingInspectorChannel struct{}

func (*panickingInspectorChannel) SendResponse(int32, *gov8.InspectorStringBuffer) {
	fmt.Fprintln(os.Stderr, "marker:inspector-channel-entered")
	panic("inspector-channel-panic")
}
func (*panickingInspectorChannel) SendNotification(*gov8.InspectorStringBuffer) {}
func (*panickingInspectorChannel) FlushProtocolNotifications()                  {}

func TestInspectorChannelPanicAbortsProcess(t *testing.T) {
	if os.Getenv("GOV8_INSPECTOR_PANIC_CHILD") == "1" {
		iso, ctx, _ := newTestRuntime(t)
		inspector, err := gov8.NewInspector(iso)
		if err != nil {
			t.Fatal(err)
		}
		if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
			t.Fatal(err)
		}
		session, err := inspector.Connect(1, &panickingInspectorChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorUntrusted)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stderr, "marker:before-inspector-dispatch")
		_ = session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(`{"id":1,"method":"Runtime.evaluate","params":{"expression":"1","contextId":1}}`)))
		fmt.Fprintln(os.Stderr, "marker:after-inspector-dispatch")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInspectorChannelPanicAbortsProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOV8_INSPECTOR_PANIC_CHILD=1")
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err == nil {
		t.Fatalf("panic child exited cleanly: %s", text)
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || uint32(exit.ExitCode()) != 0xC0000409 {
		t.Fatalf("panic child exit = %v, want 0xC0000409; output:\n%s", err, text)
	}
	for _, marker := range []string{"marker:before-inspector-dispatch", "marker:inspector-channel-entered", "inspector-channel-panic"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("panic output lacks %q:\n%s", marker, text)
		}
	}
	if strings.Contains(text, "marker:after-inspector-dispatch") {
		t.Fatalf("execution returned after callback panic:\n%s", text)
	}
}
