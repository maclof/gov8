//go:build windows && amd64

package gov8_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

type panicValueClient struct{ mode string }

func (c panicValueClient) ValueSubtype(*gov8.CallbackScope, gov8.Value) *gov8.InspectorStringBuffer {
	if c.mode == "subtype" {
		panic("value subtype panic")
	}
	return gov8.NewInspectorStringBuffer(gov8.NewInspectorStringView8([]byte("node")))
}

type invalidDefaultContextClient struct{ context *gov8.Context }

func (c invalidDefaultContextClient) EnsureDefaultContextInGroup(int32) *gov8.Context {
	return c.context
}
func (c panicValueClient) DescriptionForValueSubtype(*gov8.CallbackScope, gov8.Value) *gov8.InspectorStringBuffer {
	if c.mode == "description" {
		panic("value description panic")
	}
	return nil
}

func TestInspectorInvalidDefaultContextIsFatal(t *testing.T) {
	if mode := os.Getenv("GOV8_INSPECTOR_INVALID_CONTEXT_CHILD"); mode != "" {
		iso, _ := gov8.NewIsolate()
		ctx, _ := iso.NewContext()
		returned, _ := iso.NewContext()
		if mode == "foreign" {
			foreign, _ := gov8.NewIsolate()
			returned, _ = foreign.NewContext()
		}
		inspector, _ := gov8.NewInspectorWithClient(iso, invalidDefaultContextClient{returned})
		_ = inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView())
		session, _ := inspector.Connect(1, &recordingInspectorChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
		_ = session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(
			`{"id":1,"method":"Runtime.evaluate","params":{"expression":"1"}}`)))
		os.Exit(91)
	}
	for _, mode := range []string{"unregistered", "foreign"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestInspectorInvalidDefaultContextIsFatal$")
		cmd.Env = append(os.Environ(), "GOV8_INSPECTOR_INVALID_CONTEXT_CHILD="+mode)
		output, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || uint32(exitErr.ExitCode()) != 0xC0000409 {
			t.Fatalf("%s exit=%v err=%v output=%s", mode, func() int {
				if ok {
					return exitErr.ExitCode()
				}
				return 0
			}(), err, output)
		}
		if !strings.Contains(string(output), "gov8: fatal:") {
			t.Fatalf("%s missing diagnostic: %s", mode, output)
		}
	}
}
func (c panicValueClient) EnsureDefaultContextInGroup(int32) *gov8.Context {
	if c.mode == "ensure" {
		panic("default context panic")
	}
	return nil
}

func TestInspectorClientValuePanicIsFatal(t *testing.T) {
	if mode := os.Getenv("GOV8_INSPECTOR_VALUE_PANIC_CHILD"); mode != "" {
		iso, _ := gov8.NewIsolate()
		ctx, _ := iso.NewContext()
		scope, _ := iso.NewScope()
		inspector, _ := gov8.NewInspectorWithClient(iso, panicValueClient{mode})
		_ = inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView())
		session, _ := inspector.Connect(1, &recordingInspectorChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
		request := `{"id":1,"method":"Runtime.evaluate","params":{"expression":"({})","contextId":1}}`
		if mode == "ensure" {
			request = `{"id":1,"method":"Runtime.evaluate","params":{"expression":"1"}}`
		}
		_ = scope
		_ = session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(request)))
		os.Exit(91)
	}
	for _, mode := range []string{"subtype", "description", "ensure"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestInspectorClientValuePanicIsFatal$")
		cmd.Env = append(os.Environ(), "GOV8_INSPECTOR_VALUE_PANIC_CHILD="+mode)
		output, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || uint32(exitErr.ExitCode()) != 0xC0000409 {
			t.Fatalf("%s exit=%v err=%v output=%s", mode, func() int {
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
