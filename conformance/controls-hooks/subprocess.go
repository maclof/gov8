//go:build windows && amd64

// Subprocess helpers for the conformance-controls-hooks runner: the fatal
// and heap-pressure paths abort the process by design, so they are
// characterized by re-invoking this test binary in a dedicated subprocess
// mode (the Go analog of the oracle binary's spawn_self).
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// gov8ModeEnv is the environment marker selecting a subprocess mode.
const gov8ModeEnv = "GOV8_CH_MODE"

// spawnSelf re-invokes this test binary for one mode and returns the
// captured stdout, stderr, and exit code (Go's unsigned representation of
// the Win32 exit code).
func spawnSelf(t tester, mode string) (stdout, stderr string, exitCode int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run", "^TestCHSubMode$", "-test.v=false")
	cmd.Env = append(os.Environ(), gov8ModeEnv+"="+mode)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		ee, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("spawn %s: %v", mode, runErr)
		}
		code = ee.ExitCode()
	}
	return outBuf.String(), errBuf.String(), code
}

// exitCodeJSON renders the exit code in the Rust-normalized signed form
// (Rust's ExitStatus::code() is the sign-extended int32; Go's ExitCode is
// the unsigned representation).
func exitCodeJSON(code int) jsonValue { return jint(int64(int32(code))) }

// exitCodeHexJSON renders the exit code as 0x%08X of the unsigned value.
func exitCodeHexJSON(code int) jsonValue {
	return jstr(fmt.Sprintf("0x%08X", uint32(code)))
}

// parseResultValues parses a `RESULT k=v k=v ...` line emitted by the
// near-heap-limit subprocess. Keys are a fixed, self-produced vocabulary in
// a stable order; a missing/malformed field yields the sentinel -1.
func parseResultValues(stdout string) []int64 {
	var line string
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "RESULT ") {
			line = l
			break
		}
	}
	var out []int64
	for _, pair := range strings.Fields(strings.TrimPrefix(line, "RESULT ")) {
		idx := strings.IndexByte(pair, '=')
		if idx < 0 {
			out = append(out, -1)
			continue
		}
		v, err := strconv.ParseInt(pair[idx+1:], 10, 64)
		if err != nil {
			out = append(out, -1)
			continue
		}
		out = append(out, v)
	}
	return out
}

// chModeExitCode returns the process exit code for the raw-registered
// representation used by mode bodies that terminate explicitly.
const chStatusBreakpointSigned = -2147483645 // 0x80000003

// wordToPtr converts an engine word to a pointer using the module-wide
// vet-clean idiom (the word round-trips through a local's address).
func wordToPtr(w uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&w))
}

// chRawAbortExit registers a first-chance vectored exception handler that
// terminates the process with the raw STATUS_BREAKPOINT code (see the
// module-side controls_hooks_fatal_test.go for the rationale: the Go
// runtime would otherwise intercept the engine's int3, dump goroutines, and
// exit 2). Mode subprocesses only.
func chRawAbortExit() {
	var (
		kernel32 = syscall.NewLazyDLL("kernel32.dll")
		addVEH   = kernel32.NewProc("AddVectoredExceptionHandler")
		exitProc = kernel32.NewProc("ExitProcess")
	)
	filter := syscall.NewCallback(func(excPointers uintptr) uintptr {
		// EXCEPTION_POINTERS { ExceptionRecord *; ContextRecord * }; the
		// record starts with the 4-byte ExceptionCode.
		record := *(*uintptr)(wordToPtr(excPointers))
		code := *(*uint32)(wordToPtr(record))
		if code == 0x80000003 { // STATUS_BREAKPOINT (V8 IMMEDIATE_CRASH int3)
			exitProc.Call(uintptr(code))
		}
		return 0 // EXCEPTION_CONTINUE_SEARCH
	})
	addVEH.Call(1, filter)
}

// chFatalHandler is the shared observing fatal handler (same output shape
// as the oracle's fatal_handler). It only observes and returns; the engine
// aborts afterwards and the vectored handler preserves the exit code.
func chFatalHandler(file string, line int32, message string) {
	fmt.Fprintf(os.Stderr, "FATAL file=%q line=%d message=%q\n", file, line, message)
}

// chOomHandler is the shared observing OOM handler (same output shape as
// the oracle's recording_oom_handler).
func chOomHandler(location, detail string, isHeapOOM bool) {
	fmt.Fprintf(os.Stderr, "OOM location=%q is_heap_oom=%v detail=%q\n", location, isHeapOOM, detail)
}

// chHeapWorkload is the shared heap-pressure loop body (the oracle's
// run_until_oom workload).
const chHeapWorkload = "\"hello world\"\n  .repeat(10)\n  .split(\"w\")\n" +
	"  .map((s) => s.repeat(100).split(\"o\"))\n"
