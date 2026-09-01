//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "gov8"
)

func TestResidualMessageListenerPanicAbortsProcess(t *testing.T) {
	if os.Getenv("GOV8_TLR_PANIC_CHILD") == "1" {
		iso, ctx, scope := newTestRuntime(t)
		_, _ = iso.AddMessageListener(func(*gov8.CallbackMessage, gov8.Value) {
			fmt.Fprintln(os.Stderr, "marker:listener-entered")
			panic("message listener panic boundary")
		})
		script, err := ctx.CompileUncaughtWithOrigin(scope, "throw new Error('trigger-listener-panic')", nil)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stderr, "marker:before-listener-throw")
		_, _ = script.RunUncaught(scope)
		fmt.Fprintln(os.Stderr, "marker:after-listener-throw")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestResidualMessageListenerPanicAbortsProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOV8_TLR_PANIC_CHILD=1")
	out, err := cmd.CombinedOutput()
	output := string(out)
	exit, ok := err.(*exec.ExitError)
	if !ok || uint32(exit.ExitCode()) != 0xC0000409 {
		t.Fatalf("panic exit = %v, want 0xC0000409; output:\n%s", err, output)
	}
	for _, want := range []string{"marker:before-listener-throw", "marker:listener-entered", "message listener panic boundary"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "marker:after-listener-throw") {
		t.Fatalf("returned after listener panic:\n%s", output)
	}
}
