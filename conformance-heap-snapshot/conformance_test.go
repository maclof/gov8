//go:build windows && amd64

package heap_snapshot_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const heapSnapshotChildEnv = "GOV8_HEAP_SNAPSHOT_CONFORMANCE_CHILD"

type heapSnapshotRuntimeState struct {
	iso *gov8.Isolate
	ctx *gov8.Context
}

func conformanceFail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

func newHeapSnapshotRuntime() *heapSnapshotRuntimeState {
	iso, err := gov8.NewIsolate()
	if err != nil {
		conformanceFail("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		conformanceFail("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		conformanceFail("NewScope: %v", err)
	}
	if got := evalHeapSnapshotText(ctx, scope, "globalThis.gov8HeapSnapshotMarker={label:'gov8-snapshot-marker',values:[1,2,3]}; 'ready'"); got != "ready" {
		conformanceFail("marker setup = %q", got)
	}
	if err := scope.Close(); err != nil {
		conformanceFail("Scope.Close: %v", err)
	}
	return &heapSnapshotRuntimeState{iso: iso, ctx: ctx}
}

func (r *heapSnapshotRuntimeState) close() {
	if err := r.ctx.Close(); err != nil {
		conformanceFail("Context.Close: %v", err)
	}
	if err := r.iso.Close(); err != nil {
		conformanceFail("Isolate.Close: %v", err)
	}
}

func evalHeapSnapshotText(ctx *gov8.Context, scope *gov8.Scope, source string) string {
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		conformanceFail("Compile: %v", err)
	}
	value, err := script.Run(scope, nil)
	_ = script.Close()
	if err != nil {
		conformanceFail("Run: %v", err)
	}
	text, err := value.ToString(ctx)
	if err != nil {
		conformanceFail("ToString: %v", err)
	}
	return text
}

func (r *heapSnapshotRuntimeState) evalText(source string) string {
	scope, err := r.iso.NewScope()
	if err != nil {
		conformanceFail("NewScope: %v", err)
	}
	text := evalHeapSnapshotText(r.ctx, scope, source)
	if err := scope.Close(); err != nil {
		conformanceFail("Scope.Close: %v", err)
	}
	return text
}

func fullHeapSnapshot(iso *gov8.Isolate) ([]byte, []int) {
	var document []byte
	var sizes []int
	if err := iso.TakeHeapSnapshot(func(chunk []byte) bool {
		sizes = append(sizes, len(chunk))
		document = append(document, chunk...)
		return true
	}); err != nil {
		conformanceFail("TakeHeapSnapshot: %v", err)
	}
	return document, sizes
}

func runHeapSnapshotConformance() {
	if err := gov8.Initialize(); err != nil {
		conformanceFail("Initialize: %v", err)
	}

	success := newHeapSnapshotRuntime()
	document, sizes := fullHeapSnapshot(success.iso)
	lastEmpty := len(sizes) > 0 && sizes[len(sizes)-1] == 0
	nonempty := false
	for _, size := range sizes {
		nonempty = nonempty || size > 0
	}
	fmt.Printf("{\"check\":\"heap-snapshot/stream/success\",\"ok\":true,\"value\":{\"callback_called\":%t,\"final_empty_chunk\":%t,\"has_nonempty_data_chunk\":%t,\"json_document\":%t,\"has_snapshot_meta\":%t,\"has_nodes_edges_strings\":%t,\"contains_marker\":%t}}\n",
		len(sizes) > 0, lastEmpty, nonempty,
		bytes.HasPrefix(document, []byte("{")) && bytes.HasSuffix(document, []byte("}")),
		bytes.Contains(document, []byte("\"snapshot\"")) && bytes.Contains(document, []byte("\"meta\"")),
		bytes.Contains(document, []byte("\"nodes\"")) && bytes.Contains(document, []byte("\"edges\"")) && bytes.Contains(document, []byte("\"strings\"")),
		bytes.Contains(document, []byte("gov8-snapshot-marker")))
	success.close()

	abort := newHeapSnapshotRuntime()
	calls, delivered := 0, 0
	if err := abort.iso.TakeHeapSnapshot(func(chunk []byte) bool {
		calls++
		delivered += len(chunk)
		return false
	}); err != nil {
		conformanceFail("abort snapshot: %v", err)
	}
	usable := abort.evalText("String(40+2)") == "42"
	fmt.Printf("{\"check\":\"heap-snapshot/stream/callback_abort\",\"ok\":true,\"value\":{\"exactly_one_callback\":%t,\"first_chunk_nonempty\":%t,\"isolate_usable_after_abort\":%t}}\n", calls == 1, delivered > 0, usable)
	abort.close()

	repeat := newHeapSnapshotRuntime()
	if err := repeat.iso.TakeHeapSnapshot(func([]byte) bool { return false }); err != nil {
		conformanceFail("initial abort: %v", err)
	}
	first, firstSizes := fullHeapSnapshot(repeat.iso)
	second, secondSizes := fullHeapSnapshot(repeat.iso)
	fmt.Printf("{\"check\":\"heap-snapshot/lifecycle/repeat_after_abort\",\"ok\":true,\"value\":{\"first_complete\":%t,\"second_complete\":%t,\"callbacks_each_time\":%t,\"marker_each_time\":%t}}\n",
		bytes.HasPrefix(first, []byte("{")) && bytes.HasSuffix(first, []byte("}")),
		bytes.HasPrefix(second, []byte("{")) && bytes.HasSuffix(second, []byte("}")),
		len(firstSizes) > 0 && len(secondSizes) > 0,
		bytes.Contains(first, []byte("gov8-snapshot-marker")) && bytes.Contains(second, []byte("gov8-snapshot-marker")))
	repeat.close()
	fmt.Println("{\"summary\":{\"total\":3,\"passed\":3,\"failed\":0}}")
}

func TestMain(m *testing.M) {
	if os.Getenv(heapSnapshotChildEnv) == "1" {
		runHeapSnapshotConformance()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHeapSnapshotMatchesFixture(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), heapSnapshotChildEnv+"=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-heap-snapshot-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, fixture) {
		t.Fatalf("fixture mismatch\nactual:\n%s\nwant:\n%s", output, fixture)
	}
}
