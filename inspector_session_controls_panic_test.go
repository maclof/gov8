//go:build windows && amd64

package gov8_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "gov8"
)

type panickingInspectorClient struct{}

func (*panickingInspectorClient) RunMessageLoopOnPause(int32) {
	panic("inspector pause client panic boundary")
}
func (*panickingInspectorClient) QuitMessageLoopOnPause() {}

func TestInspectorClientPanicIsProcessFatal(t *testing.T) {
	if os.Getenv("GOV8_INSPECTOR_CLIENT_PANIC_CHILD") == "1" {
		iso, err := gov8.NewIsolate()
		if err != nil {
			panic(err)
		}
		ctx, err := iso.NewContext()
		if err != nil {
			panic(err)
		}
		scope, err := iso.NewScope()
		if err != nil {
			panic(err)
		}
		inspector, err := gov8.NewInspectorWithClient(iso, &panickingInspectorClient{})
		if err != nil {
			panic(err)
		}
		if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
			panic(err)
		}
		session, err := inspector.Connect(1, &recordingInspectorChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
		if err != nil {
			panic(err)
		}
		if err := session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(`{"id":1,"method":"Debugger.enable"}`))); err != nil {
			panic(err)
		}
		if err := session.SchedulePauseOnNextStatement(gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
			panic(err)
		}
		script, err := ctx.Compile(scope, "1", nil)
		if err != nil {
			panic(err)
		}
		_, _ = script.Run(scope, nil)
		os.Exit(91)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInspectorClientPanicIsProcessFatal$")
	cmd.Env = append(os.Environ(), "GOV8_INSPECTOR_CLIENT_PANIC_CHILD=1")
	output, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("child error = %v, output=%s", err, output)
	}
	if uint32(exitErr.ExitCode()) != 0xC0000409 { // STATUS_STACK_BUFFER_OVERRUN
		t.Fatalf("exit = %#x (%d), want 0xC0000409; output=%s", uint32(exitErr.ExitCode()), exitErr.ExitCode(), output)
	}
	if !strings.Contains(string(output), "panic in inspector client callback") {
		t.Fatalf("missing panic diagnostic: %s", output)
	}
}
