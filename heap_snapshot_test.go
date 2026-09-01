//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

func heapSnapshotRuntime(t *testing.T) (*gov8.Isolate, *gov8.Context) {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = ctx.Close()
		_ = iso.Close()
		t.Fatal(err)
	}
	if _, err := eval(t, ctx, scope, "globalThis.gov8HeapSnapshotMarker={label:'gov8-snapshot-test-marker'}"); err != nil {
		_ = scope.Close()
		_ = ctx.Close()
		_ = iso.Close()
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	return iso, ctx
}

func closeHeapSnapshotRuntime(t testing.TB, iso *gov8.Isolate, ctx *gov8.Context) {
	t.Helper()
	if err := ctx.Close(); err != nil {
		t.Errorf("Context.Close: %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Errorf("Isolate.Close: %v", err)
	}
}

func TestTakeHeapSnapshotStreamAndAbort(t *testing.T) {
	iso, ctx := heapSnapshotRuntime(t)
	defer closeHeapSnapshotRuntime(t, iso, ctx)

	var chunks [][]byte
	if err := iso.TakeHeapSnapshot(func(chunk []byte) bool {
		chunks = append(chunks, chunk)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 || len(chunks[len(chunks)-1]) != 0 {
		t.Fatalf("chunk sizes do not end in terminal empty chunk: count=%d last=%d", len(chunks), len(chunks[len(chunks)-1]))
	}
	document := bytes.Join(chunks, nil)
	if !bytes.HasPrefix(document, []byte("{")) || !bytes.HasSuffix(document, []byte("}")) {
		t.Fatal("snapshot is not one complete JSON document")
	}
	if !bytes.Contains(document, []byte("gov8-snapshot-test-marker")) {
		t.Fatal("snapshot does not contain retained marker")
	}

	calls := 0
	firstAbortEmpty := false
	if err := iso.TakeHeapSnapshot(func(chunk []byte) bool {
		calls++
		if len(chunk) == 0 {
			firstAbortEmpty = true
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if firstAbortEmpty {
		t.Fatal("first abort chunk is empty")
	}
	if calls != 1 {
		t.Fatalf("abort callback calls = %d, want 1", calls)
	}
	if err := iso.TakeHeapSnapshot(func([]byte) bool { return false }); err != nil {
		t.Fatalf("repeat after abort: %v", err)
	}
}

func TestTakeHeapSnapshotValidationAndReentrancy(t *testing.T) {
	var nilIsolate *gov8.Isolate
	if err := nilIsolate.TakeHeapSnapshot(func([]byte) bool { return true }); err == nil {
		t.Fatal("nil isolate accepted")
	}

	iso, ctx := heapSnapshotRuntime(t)
	if err := iso.TakeHeapSnapshot(nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	var callbacks atomic.Int32
	var nestedErr, closeErr error
	if err := iso.TakeHeapSnapshot(func([]byte) bool {
		callbacks.Add(1)
		nestedErr = iso.TakeHeapSnapshot(func([]byte) bool { return true })
		closeErr = iso.Close()
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if nestedErr == nil || !strings.Contains(nestedErr.Error(), "already active") {
		t.Fatalf("nested snapshot error = %v", nestedErr)
	}
	if closeErr == nil || !strings.Contains(closeErr.Error(), "active heap snapshot") {
		t.Fatalf("reentrant Close error = %v", closeErr)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("callbacks = %d, want 1", callbacks.Load())
	}
	closeHeapSnapshotRuntime(t, iso, ctx)
	if err := iso.TakeHeapSnapshot(func([]byte) bool { return true }); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("post-Close error = %v", err)
	}
}

func TestTakeHeapSnapshotWrongThread(t *testing.T) {
	iso, ctx := heapSnapshotRuntime(t)
	defer closeHeapSnapshotRuntime(t, iso, ctx)
	done := make(chan error, 1)
	go func() {
		done <- iso.TakeHeapSnapshot(func([]byte) bool { return true })
	}()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
}

func TestTakeHeapSnapshotCallbackPanicIsFatal(t *testing.T) {
	if os.Getenv("GOV8_HEAP_SNAPSHOT_PANIC_CHILD") == "1" {
		iso, err := gov8.NewIsolate()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stderr, "marker:before-heap-snapshot-callback-panic")
		_ = iso.TakeHeapSnapshot(func([]byte) bool {
			panic("heap snapshot callback panic sentinel")
		})
		fmt.Fprintln(os.Stderr, "marker:after-heap-snapshot-callback-panic")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTakeHeapSnapshotCallbackPanicIsFatal$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOV8_HEAP_SNAPSHOT_PANIC_CHILD=1")
	output, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || uint32(exit.ExitCode()) != 0xC0000409 {
		t.Fatalf("panic exit = %v, want 0xC0000409; output:\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{"marker:before-heap-snapshot-callback-panic", "panic in heap snapshot callback", "heap snapshot callback panic sentinel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("panic output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "marker:after-heap-snapshot-callback-panic") {
		t.Fatalf("execution returned after callback panic:\n%s", text)
	}
}
