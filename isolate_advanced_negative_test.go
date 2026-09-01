//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestAdvancedArrayBufferContextGuards(t *testing.T) {
	_, _, ctxA, ctxB, scopeA, _ := twoIsolates(t)
	if _, err := gov8.NewArrayBuffer(scopeA, nil, 1); err == nil || !strings.Contains(err.Error(), "entered context") {
		t.Fatalf("nil context error = %v", err)
	}
	if _, err := gov8.NewArrayBuffer(scopeA, ctxB, 1); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign context error = %v", err)
	}
	for name, call := range map[string]func() error{
		"shared": func() error { _, err := gov8.NewSharedArrayBuffer(scopeA, nil, 1); return err },
		"typed": func() error {
			buffer, err := gov8.NewArrayBuffer(scopeA, ctxA, 1)
			if err != nil {
				return err
			}
			_, err = gov8.NewUint8Array(scopeA, nil, buffer, 0, 1)
			return err
		},
		"data-view": func() error {
			buffer, err := gov8.NewArrayBuffer(scopeA, ctxA, 1)
			if err != nil {
				return err
			}
			_, err = gov8.NewDataView(scopeA, nil, buffer, 0, 1)
			return err
		},
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "entered context") {
			t.Fatalf("%s nil context error = %v", name, err)
		}
	}

	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	closedCtx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedCtx.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := gov8.NewArrayBuffer(scope, closedCtx, 1); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed context error = %v", err)
	}
	_ = scope.Close()
	_ = iso.Close()
}

func TestIsolateAdvancedLifecycleWrongThreadAndStackLimit(t *testing.T) {
	if _, err := gov8.NewIsolateWithParams(gov8.NewCreateParams().SetStackLimit(1)); err == nil || !strings.Contains(err.Error(), "Go stack") {
		t.Fatalf("stack-limit constructor error = %v", err)
	}
	iso, err := gov8.NewIsolateWithParams(nil)
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := iso.GetHeapStatistics()
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := iso.HasCppHeap(); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed-isolate error = %v", err)
	}
}

func TestIsolateAdvancedParallelIsolates(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			iso, err := gov8.NewIsolateWithParams(gov8.NewCreateParams().UseDefaultArrayBufferAllocator())
			if err != nil {
				errs <- err
				return
			}
			defer iso.Close()
			if _, err := iso.GetHeapStatistics(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestCounterLookupPanicAbortsProcess(t *testing.T) {
	if os.Getenv("GOV8_COUNTER_PANIC_PROBE") == "1" {
		fmt.Fprintln(os.Stderr, "marker:counter-before")
		_, _ = gov8.NewIsolateWithParams(gov8.NewCreateParams().SetCounterLookupCallback(func(string) {
			fmt.Fprintln(os.Stderr, "marker:counter-entered")
			panic("counter-lookup-panic")
		}))
		fmt.Fprintln(os.Stderr, "marker:counter-after")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestCounterLookupPanicAbortsProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOV8_COUNTER_PANIC_PROBE=1")
	out, err := cmd.CombinedOutput()
	text := string(out)
	for _, marker := range []string{"marker:counter-before", "marker:counter-entered", "counter-lookup-panic"} {
		if !strings.Contains(text, marker) {
			t.Errorf("missing %q; output:\n%s", marker, text)
		}
	}
	if strings.Contains(text, "marker:counter-after") {
		t.Errorf("panic returned; output:\n%s", text)
	}
	if err == nil {
		t.Fatalf("probe exited cleanly; output:\n%s", text)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 3221226505 {
		t.Fatalf("exit = %v; want 0xC0000409; output:\n%s", err, text)
	}
}
