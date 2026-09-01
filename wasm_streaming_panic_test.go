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

const wasmCallbackAbortCode = 3221226505 // 0xC0000409

func runWasmPanicProbe(t *testing.T, probe, entered, diagnostic, after string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run", "^"+probe+"$", "-test.count=1", "-test.v=false")
	cmd.Env = append(os.Environ(), "GOV8_WASM_PANIC_PROBE="+probe)
	output, err := cmd.CombinedOutput()
	text := string(output)
	for _, marker := range []string{"marker:wasm-before", entered, diagnostic} {
		if !strings.Contains(text, marker) {
			t.Errorf("%s: missing %q; output:\n%s", probe, marker, text)
		}
	}
	if strings.Contains(text, after) {
		t.Errorf("%s: returned past panic; output:\n%s", probe, text)
	}
	var exitErr *exec.ExitError
	if err == nil || !asExitError(err, &exitErr) {
		t.Fatalf("%s: expected process failure, got %v; output:\n%s", probe, err, text)
	}
	if got := exitErr.ExitCode(); got != wasmCallbackAbortCode {
		t.Errorf("%s: exit code %d, want %d; output:\n%s", probe, got, wasmCallbackAbortCode, text)
	}
}

func wasmPanicProbe(name string) bool { return os.Getenv("GOV8_WASM_PANIC_PROBE") == name }

func TestWasmStreamingCallbackPanicAbortsProcess(t *testing.T) {
	runWasmPanicProbe(t, "TestProbeWasmStreamingCallbackPanic", "marker:wasm-stream-entered",
		"gov8: panic in streaming callback: wasm-stream-panic", "marker:wasm-stream-after")
}

func TestWasmCachingCallbackPanicAbortsProcess(t *testing.T) {
	runWasmPanicProbe(t, "TestProbeWasmCachingCallbackPanic", "marker:wasm-cache-entered",
		"gov8: panic in module caching callback: wasm-cache-panic", "marker:wasm-cache-after")
}

func TestWasmResolutionCallbackPanicAbortsProcess(t *testing.T) {
	runWasmPanicProbe(t, "TestProbeWasmResolutionCallbackPanic", "marker:wasm-resolution-entered",
		"gov8: panic in module compilation callback: wasm-resolution-panic", "marker:wasm-resolution-after")
}

func TestProbeWasmStreamingCallbackPanic(t *testing.T) {
	if !wasmPanicProbe("TestProbeWasmStreamingCallbackPanic") {
		t.Skip("probe body")
	}
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if err := iso.SetWasmStreamingCallback(func(*gov8.CallbackScope, gov8.Value, *gov8.WasmStreaming) {
		fmt.Fprintln(os.Stderr, "marker:wasm-stream-entered")
		panic("wasm-stream-panic")
	}); err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "marker:wasm-before")
	_ = compileStreaming(t, iso, ctx, scope, "'panic'")
	fmt.Fprintln(os.Stderr, "marker:wasm-stream-after")
}

func TestProbeWasmCachingCallbackPanic(t *testing.T) {
	if !wasmPanicProbe("TestProbeWasmCachingCallbackPanic") {
		t.Skip("probe body")
	}
	streams := make(chan *gov8.WasmStreaming, 1)
	iso, ctx, scope := newStreamingRuntime(t, func(_ *gov8.CallbackScope, _ gov8.Value, stream *gov8.WasmStreaming) {
		streams <- stream
	})
	_ = compileStreaming(t, iso, ctx, scope, "'panic-cache'")
	stream := <-streams
	if err := stream.SetHasCompiledModuleBytes(); err != nil {
		t.Fatal(err)
	}
	if err := stream.OnBytesReceived(emptyWasmModule); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "marker:wasm-before")
	if err := stream.Finish(func(*gov8.ModuleCachingInterface) {
		fmt.Fprintln(os.Stderr, "marker:wasm-cache-entered")
		panic("wasm-cache-panic")
	}); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "marker:wasm-cache-after")
}

func TestProbeWasmResolutionCallbackPanic(t *testing.T) {
	if !wasmPanicProbe("TestProbeWasmResolutionCallbackPanic") {
		t.Skip("probe body")
	}
	iso, ctx, scope := newTestRuntime(t)
	compilation, err := gov8.NewWasmModuleCompilation()
	if err != nil {
		t.Fatal(err)
	}
	if err := compilation.OnBytesReceived(emptyWasmModule); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "marker:wasm-before")
	if err := compilation.Finish(scope, ctx, nil, func(*gov8.WasmModuleCompilationResult) {
		fmt.Fprintln(os.Stderr, "marker:wasm-resolution-entered")
		panic("wasm-resolution-panic")
	}); err != nil {
		t.Fatal(err)
	}
	pumpWasmUntil(t, iso, func() bool { return false })
	fmt.Fprintln(os.Stderr, "marker:wasm-resolution-after")
}
