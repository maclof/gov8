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

func TestCppGCCallbackPanicIsFatal(t *testing.T) {
	if mode := os.Getenv("GOV8_CPPGC_PANIC_CHILD"); mode != "" {
		iso, err := gov8.NewIsolate()
		if err != nil {
			t.Fatal(err)
		}
		ctx, _ := iso.NewContext()
		scope, _ := iso.NewScope()
		wrapper := cppgcAPIWrapper(t, iso, ctx, scope)
		target, _ := scope.NewObject(ctx)
		callbacks := gov8.CppGCObjectCallbacks{}
		if mode == "trace" {
			callbacks.Trace = func() { panic("cppgc trace panic marker") }
		} else {
			callbacks.Destroy = func() { panic("cppgc destroy panic marker") }
		}
		if _, err := scope.WrapCppGCObject(wrapper, target.Value, 1, 1, callbacks); err != nil {
			t.Fatal(err)
		}
		if mode == "trace" {
			fmt.Fprintln(os.Stderr, "marker:before-cppgc-trace")
			_ = iso.LowMemoryNotification()
			fmt.Fprintln(os.Stderr, "marker:after-cppgc-trace")
			os.Exit(91)
		}
		_ = scope.Close()
		_ = ctx.Close()
		_ = gov8.ReleaseIsolateHostState(iso)
		fmt.Fprintln(os.Stderr, "marker:before-cppgc-destroy")
		_ = iso.Close()
		fmt.Fprintln(os.Stderr, "marker:after-cppgc-destroy")
		os.Exit(91)
	}

	for _, mode := range []string{"trace", "destroy"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCppGCCallbackPanicIsFatal$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOV8_CPPGC_PANIC_CHILD="+mode)
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || uint32(exit.ExitCode()) != 0xC0000409 {
			t.Fatalf("%s panic exit = %v, want 0xC0000409; output:\n%s", mode, err, output)
		}
		text := string(output)
		for _, want := range []string{"marker:before-cppgc-" + mode, "gov8: panic in cppgc callback", "cppgc " + mode + " panic marker"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q:\n%s", mode, want, text)
			}
		}
		if strings.Contains(text, "marker:after-cppgc-"+mode) {
			t.Fatalf("%s callback returned after panic:\n%s", mode, text)
		}
	}
}
