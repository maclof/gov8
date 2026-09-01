//go:build windows && amd64

package gov8_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Panic-boundary characterization for the serializer/deserializer delegate
// slice, mirroring rust-oracle/tests/serializer_delegates_negative.rs:
//
// A panic inside ANY delegate hook unwinds out of the Go trampoline. As
// with native callbacks (see the FunctionCallback ownership notes and
// TestCallbackPanicAbortsProcess), the panic is recovered and deliberately
// translated into the process fail-fast abort (0xC0000409 on Windows) after
// printing the message - the observable equivalent of the pinned oracle,
// where unwinding out of the crate's extern "C" delegate trampolines
// aborts the process. Each probe runs in its own child process (os.Executable
// + -test.run filter, gated by GOV8_PROBE so a plain `go test` never aborts).

const serDelAbortCode = 3221226505 // 0xC0000409 fail-fast

// runSerDelProbe spawns this test binary for one probe and asserts the
// fail-fast boundary: the hook marker printed, the panic message printed,
// no post-hook marker, and the 0xC0000409 exit code.
func runSerDelProbe(t *testing.T, probe, hookMarker string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run", "^"+probe+"$", "-test.v=false")
	cmd.Env = append(os.Environ(), "GOV8_PROBE="+probe)
	out, err := cmd.CombinedOutput()
	stdoutStderr := string(out)

	marker := "marker:" + hookMarker
	if !strings.Contains(stdoutStderr, marker) {
		t.Errorf("the hook's own marker %q must be printed; output:\n%s", marker, stdoutStderr)
	}
	if strings.Contains(stdoutStderr, marker+":after") {
		t.Errorf("execution must not return past the panicking hook; output:\n%s", stdoutStderr)
	}
	if err == nil {
		t.Fatalf("the process must not exit cleanly; output:\n%s", stdoutStderr)
	}
	var ee *exec.ExitError
	if !asExitError(err, &ee) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if got := ee.ExitCode(); got != serDelAbortCode {
		t.Errorf("exit code = %d; want %d (0xC0000409); output:\n%s", got, serDelAbortCode, stdoutStderr)
	}
}

func serDelProbeBody(t *testing.T, name string) bool {
	t.Helper()
	return os.Getenv("GOV8_PROBE") == name
}

func serDelPanicHook() {
	panic("serdel-panic")
}

// --- the eight hooks -------------------------------------------------------------

// 1. write_host_object (via the treat-views flag and a typed array).
func TestProbeSerDelPanicWriteHostObject(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerDelPanicWriteHostObject") {
		t.Skip("probe body")
	}
	_, ctx, scope := newTestRuntime(t)
	boom := serDelPanicThrow{counts: &sdCounts{}}
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, boom)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser.Close() }()
	if err := ser.SetTreatArrayBufferViewsAsHostObjects(true); err != nil {
		t.Fatal(err)
	}
	ab, err := gov8.NewArrayBuffer(scope, ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	ta, err := gov8.NewUint8Array(scope, ctx, ab, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr.WriteString("marker:write-host-object\n")
	_, _ = ser.WriteValue(ctx, ta.Value, nil)
	os.Stderr.WriteString("marker:write-host-object:after\n")
}

type serDelPanicThrow struct{ counts *sdCounts }

func (d serDelPanicThrow) ThrowDataCloneError(string) bool { return true }

func (d serDelPanicThrow) WriteHostObject(*gov8.Object, *gov8.DelegateValueSerializer) (bool, bool) {
	serDelPanicHook()
	return true, true
}

// 2. read_host_object.
func TestProbeSerDelPanicReadHostObject(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerDelPanicReadHostObject") {
		t.Skip("probe body")
	}
	_, ctx, scope := newTestRuntime(t)
	vd, err := gov8.NewDelegateValueDeserializer(scope, ctx,
		hexToBytes("5c2a686f73740000000000000c40"), serDelPanicRead{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vd.Close() }()
	os.Stderr.WriteString("marker:read-host-object\n")
	_, _ = vd.ReadValue(ctx, nil)
	os.Stderr.WriteString("marker:read-host-object:after\n")
}

type serDelPanicRead struct{}

func (serDelPanicRead) ReadHostObject(*gov8.DelegateValueDeserializer) (*gov8.Object, bool) {
	serDelPanicHook()
	return nil, false
}

// 3. is_host_object (requires has_custom_host_object = true).
func TestProbeSerDelPanicIsHostObject(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerDelPanicIsHostObject") {
		t.Skip("probe body")
	}
	_, ctx, scope := newTestRuntime(t)
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, serDelPanicIsHost{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser.Close() }()
	obj, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr.WriteString("marker:is-host-object\n")
	_, _ = ser.WriteValue(ctx, obj.Value, nil)
	os.Stderr.WriteString("marker:is-host-object:after\n")
}

type serDelPanicIsHost struct{}

func (serDelPanicIsHost) ThrowDataCloneError(string) bool { return true }

func (serDelPanicIsHost) HasCustomHostObject() bool { return true }

func (serDelPanicIsHost) IsHostObject(*gov8.Object) (bool, bool) {
	serDelPanicHook()
	return false, true
}

// 4. has_custom_host_object (consulted during serializer construction).
func TestProbeSerDelPanicHasCustomHostObject(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerDelPanicHasCustomHostObject") {
		t.Skip("probe body")
	}
	_, ctx, scope := newTestRuntime(t)
	os.Stderr.WriteString("marker:has-custom-host-object\n")
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, serDelPanicHasCustom{})
	if err == nil {
		_ = ser.Close()
	}
	os.Stderr.WriteString("marker:has-custom-host-object:after\n")
}

type serDelPanicHasCustom struct{}

func (serDelPanicHasCustom) ThrowDataCloneError(string) bool { return true }

func (serDelPanicHasCustom) HasCustomHostObject() bool {
	serDelPanicHook()
	return true
}

// 5. get_shared_array_buffer_id.
func TestProbeSerDelPanicGetSABID(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerDelPanicGetSABID") {
		t.Skip("probe body")
	}
	iso, ctx, scope := newTestRuntime(t)
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, serDelPanicSABID{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser.Close() }()
	bs, err := iso.NewSharedArrayBufferBackingStore(8)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bs.Close() }()
	sab, err := gov8.NewSharedArrayBufferWithBackingStore(scope, ctx, bs)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr.WriteString("marker:get-sab-id\n")
	_, _ = ser.WriteValue(ctx, sab.Value, nil)
	os.Stderr.WriteString("marker:get-sab-id:after\n")
}

type serDelPanicSABID struct{}

func (serDelPanicSABID) ThrowDataCloneError(string) bool { return true }

func (serDelPanicSABID) GetSharedArrayBufferID(*gov8.SharedArrayBuffer) (uint32, bool) {
	serDelPanicHook()
	return 0, false
}

// 6. get_shared_array_buffer_from_id.
func TestProbeSerDelPanicGetSABFromID(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerDelPanicGetSABFromID") {
		t.Skip("probe body")
	}
	_, ctx, scope := newTestRuntime(t)
	vd, err := gov8.NewDelegateValueDeserializer(scope, ctx,
		hexToBytes("752a"), serDelPanicSABFromID{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vd.Close() }()
	os.Stderr.WriteString("marker:get-sab-from-id\n")
	_, _ = vd.ReadValue(ctx, nil)
	os.Stderr.WriteString("marker:get-sab-from-id:after\n")
}

type serDelPanicSABFromID struct{}

func (serDelPanicSABFromID) GetSharedArrayBufferFromID(uint32) (*gov8.SharedArrayBuffer, bool) {
	serDelPanicHook()
	return nil, false
}

// 7. get_wasm_module_from_id.
func TestProbeSerDelPanicGetWasmFromID(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerDelPanicGetWasmFromID") {
		t.Skip("probe body")
	}
	_, ctx, scope := newTestRuntime(t)
	vd, err := gov8.NewDelegateValueDeserializer(scope, ctx,
		hexToBytes("7715"), serDelPanicWasmFromID{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vd.Close() }()
	os.Stderr.WriteString("marker:get-wasm-from-id\n")
	_, _ = vd.ReadValue(ctx, nil)
	os.Stderr.WriteString("marker:get-wasm-from-id:after\n")
}

type serDelPanicWasmFromID struct{}

func (serDelPanicWasmFromID) GetWasmModuleFromID(uint32) {
	serDelPanicHook()
}

// 8. throw_data_clone_error (a function write triggers the engine's
// data-clone error path).
func TestProbeSerDelPanicThrowDataCloneError(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerDelPanicThrowDataCloneError") {
		t.Skip("probe body")
	}
	iso, ctx, scope := newTestRuntime(t)
	ser, err := gov8.NewDelegateValueSerializer(scope, ctx, serDelPanicClone{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser.Close() }()
	tc := sdTryCatch(t, iso)
	defer func() { _ = tc.Close() }()
	f, ok := sdEval(t, ctx, scope, tc, "() => 1")
	if !ok {
		t.Fatal("eval failed")
	}
	os.Stderr.WriteString("marker:throw-data-clone-error\n")
	_, _ = ser.WriteValue(ctx, f, tc)
	os.Stderr.WriteString("marker:throw-data-clone-error:after\n")
}

func sdEval(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, tc *gov8.TryCatch, src string) (gov8.Value, bool) {
	t.Helper()
	script, cerr := ctx.Compile(scope, src, tc)
	if cerr != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	v, rerr := script.Run(scope, tc)
	if rerr != nil {
		return gov8.Value{}, false
	}
	return v, true
}

type serDelPanicClone struct{}

func (serDelPanicClone) ThrowDataCloneError(string) bool {
	serDelPanicHook()
	return true
}

// --- parent-side probes ---------------------------------------------------------

func TestSerDelPanicWriteHostObjectAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerDelPanicWriteHostObject", "write-host-object")
}

func TestSerDelPanicReadHostObjectAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerDelPanicReadHostObject", "read-host-object")
}

func TestSerDelPanicIsHostObjectAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerDelPanicIsHostObject", "is-host-object")
}

func TestSerDelPanicHasCustomHostObjectAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerDelPanicHasCustomHostObject", "has-custom-host-object")
}

func TestSerDelPanicGetSABIDAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerDelPanicGetSABID", "get-sab-id")
}

func TestSerDelPanicGetSABFromIDAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerDelPanicGetSABFromID", "get-sab-from-id")
}

func TestSerDelPanicGetWasmFromIDAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerDelPanicGetWasmFromID", "get-wasm-from-id")
}

func TestSerDelPanicThrowDataCloneErrorAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerDelPanicThrowDataCloneError", "throw-data-clone-error")
}

// TestSerDelHasCustomHostObjectCache mirrors the oracle's
// probe_has_custom_host_object_cache diagnostic (kept out of its fixture):
// the pinned build consults has_custom_host_object exactly once per
// ValueSerializer construction, for both true and false answers.
func TestSerDelHasCustomHostObjectCache(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	counts := new(int)
	deleg := sdCountingCustom{counts: counts, answer: true}
	ser1, err := gov8.NewDelegateValueSerializer(scope, ctx, deleg)
	if err != nil {
		t.Fatal(err)
	}
	if got := *counts; got != 1 {
		t.Fatalf("after 1st construction count = %d, want 1", got)
	}
	obj, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The default is_host_object throws, so the write legitimately fails;
	// the point of this probe is the consultation count, not the write.
	_, _ = ser1.WriteValue(ctx, obj.Value, nil)
	if got := *counts; got != 1 {
		t.Fatalf("after first write count = %d, want 1 (constructor-cached)", got)
	}
	_ = ser1.Close()

	// A second serializer with the SAME delegate consults once more.
	ser2, err := gov8.NewDelegateValueSerializer(scope, ctx, deleg)
	if err != nil {
		t.Fatal(err)
	}
	_ = ser2.Close()
	if got := *counts; got != 2 {
		t.Fatalf("after 2nd construction count = %d, want 2", got)
	}

	// The false answer is consulted the same way.
	falses := new(int)
	ser3, err := gov8.NewDelegateValueSerializer(scope, ctx, sdCountingCustom{counts: falses, answer: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ser3.Close() }()
	if got := *falses; got != 1 {
		t.Fatalf("false-answer count = %d, want 1", got)
	}
}

type sdCountingCustom struct {
	counts *int
	answer bool
}

func (d sdCountingCustom) ThrowDataCloneError(string) bool { return true }

func (d sdCountingCustom) HasCustomHostObject() bool {
	*d.counts++
	return d.answer
}
