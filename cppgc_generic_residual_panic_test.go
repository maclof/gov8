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

func TestCppGCGenericCallbackPanicsFailFast(t *testing.T) {
	if mode := os.Getenv("GOV8_CPPGC_GENERIC_PANIC_CHILD"); mode != "" {
		chRawAbortExit()
		iso, err := gov8.NewIsolate()
		if err != nil {
			panic(err)
		}
		callbacks := gov8.CppGCGenericCallbacks{}
		switch mode {
		case "cell":
			callbacks.CellDropped = func(int32) { panic("generic cell panic marker") }
		case "name":
			callbacks.NameObserved = func() { panic("generic name panic marker") }
		case "destroy":
			callbacks.Destroy = func() { panic("generic destroy panic marker") }
		default:
			os.Exit(92)
		}
		object, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{
			Cell: 1, Name: "panic-object", Alignment: 1, Callbacks: callbacks,
		})
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(os.Stderr, "marker:before-"+mode)
		switch mode {
		case "cell":
			_ = object.SetCell(2)
		case "name":
			_ = iso.TakeHeapSnapshot(func([]byte) bool { return true })
		case "destroy":
			_ = object.Close()
			_ = iso.Close()
		}
		fmt.Fprintln(os.Stderr, "marker:after-"+mode)
		os.Exit(91)
	}

	for _, mode := range []string{"cell", "name", "destroy"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCppGCGenericCallbackPanicsFailFast$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOV8_CPPGC_GENERIC_PANIC_CHILD="+mode)
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || uint32(exit.ExitCode()) != 0xC0000409 {
			t.Fatalf("%s exit = %v, %v; output:\n%s", mode, func() any {
				if ok {
					return uint32(exit.ExitCode())
				}
				return nil
			}(), err, output)
		}
		text := string(output)
		if !strings.Contains(text, "marker:before-"+mode) || strings.Contains(text, "marker:after-"+mode) ||
			!strings.Contains(text, "panic in generic cppgc callback") {
			t.Fatalf("%s markers/output invalid:\n%s", mode, text)
		}
	}
}
