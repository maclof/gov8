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

func TestCppGCGenericGraphCallbackPanicsFailFast(t *testing.T) {
	if mode := os.Getenv("GOV8_CPPGC_GRAPH_PANIC_CHILD"); mode != "" {
		chRawAbortExit()
		iso, err := gov8.NewIsolate()
		if err != nil {
			panic(err)
		}
		scope, err := iso.NewScope()
		if err != nil {
			panic(err)
		}
		callbacks := gov8.CppGCGenericGraphCallbacks[int]{Clone: func(value int) (int, error) { return value, nil }}
		switch mode {
		case "drop":
			callbacks.Drop = func(int) { panic("graph drop panic marker") }
		case "name":
			callbacks.NameObserved = func() { panic("graph name panic marker") }
		case "trace":
			callbacks.TraceObserved = func() { panic("graph trace panic marker") }
		case "destroy":
			callbacks.Destroy = func() { panic("graph destroy panic marker") }
		default:
			os.Exit(92)
		}
		graph, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[int]{
			State: 1, Name: "CppGCGenericGraphPanic", Callbacks: callbacks,
		})
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(os.Stderr, "marker:before-"+mode)
		switch mode {
		case "drop":
			_ = graph.ReplaceState(2)
		case "name":
			_ = iso.TakeHeapSnapshot(func([]byte) bool { return true })
		case "trace":
			_ = iso.LowMemoryNotification()
		case "destroy":
			_ = graph.Close()
			_ = scope.Close()
			_ = iso.Close()
		}
		fmt.Fprintln(os.Stderr, "marker:after-"+mode)
		os.Exit(91)
	}

	for _, mode := range []string{"drop", "name", "trace", "destroy"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCppGCGenericGraphCallbackPanicsFailFast$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOV8_CPPGC_GRAPH_PANIC_CHILD="+mode)
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || uint32(exit.ExitCode()) != 0xC0000409 {
			t.Fatalf("%s exit = %v; output:\n%s", mode, err, output)
		}
		text := string(output)
		if !strings.Contains(text, "marker:before-"+mode) ||
			strings.Contains(text, "marker:after-"+mode) ||
			!strings.Contains(text, "panic in generic cppgc graph callback") ||
			!strings.Contains(text, "graph "+mode+" panic marker") {
			t.Fatalf("%s markers/output invalid:\n%s", mode, text)
		}
	}
}
