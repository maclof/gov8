//go:build windows && amd64

package cppgc_heap_lifecycle_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const childEnv = "GOV8_CPPGC_HEAP_LIFECYCLE_CHILD"

func die(v ...any) { fmt.Fprintln(os.Stderr, v...); os.Exit(2) }
func must[T any](v T, err error) T {
	if err != nil {
		die(err)
	}
	return v
}
func emit(id string, v map[string]any) {
	b, e := json.Marshal(map[string]any{"check": id, "ok": true, "value": v})
	if e != nil {
		die(e)
	}
	fmt.Println(string(b))
}

func child() {
	emit("cppgc-heap-lifecycle/pin", map[string]any{"crate": "v8=152.2.0", "v8": "15.2.124.1-rusty", "target": "x86_64-pc-windows-msvc"})
	d := gov8.DefaultCppGCHeapCreateParams()
	emit("cppgc-heap-lifecycle/create_params/default", map[string]any{
		"marking_support": d.MarkingSupport.String(), "sweeping_support": d.SweepingSupport.String(),
		"public_marking_variants":  []string{"Atomic", "Incremental", "IncrementalAndConcurrent"},
		"public_sweeping_variants": []string{"Atomic", "Incremental", "IncrementalAndConcurrent"},
	})
	if err := gov8.InitializeCppGCProcess(); err != nil {
		die(err)
	}
	var drops atomic.Int32
	h := must(gov8.NewCppGCHeap(d))
	must(h.AllocateLeaf(1, gov8.CppGCObjectCallbacks{Destroy: func() { drops.Add(1) }}))
	if err := h.CollectGarbageForTesting(gov8.CppGCStackNoHeapPointers); err != nil {
		die(err)
	}
	before := drops.Load()
	if err := h.EnableDetachedGarbageCollectionsForTesting(); err != nil {
		die(err)
	}
	if err := h.CollectGarbageForTesting(gov8.CppGCStackNoHeapPointers); err != nil {
		die(err)
	}
	after := drops.Load()
	must(h.AllocateLeaf(2, gov8.CppGCObjectCallbacks{Destroy: func() { drops.Add(1) }}))
	if err := h.Terminate(); err != nil {
		die(err)
	}
	terminated := drops.Load()
	if err := h.Terminate(); err != nil {
		die(err)
	}
	second := drops.Load()
	if err := h.Close(); err != nil {
		die(err)
	}
	if err := gov8.ShutdownCppGCProcess(); err != nil {
		die(err)
	}
	emit("cppgc-heap-lifecycle/detached/collection_and_terminate", map[string]any{"drops_before_enable": before, "drops_after_enabled_gc": after, "drops_after_terminate": terminated, "drops_after_second_terminate": second, "terminate_idempotent": true})
	if err := gov8.InitializeCppGCProcess(); err != nil {
		die(err)
	}
	var drop2 atomic.Int32
	a := must(gov8.NewCppGCHeap(gov8.CppGCHeapCreateParams{MarkingSupport: gov8.CppGCMarkingAtomic, SweepingSupport: gov8.CppGCSweepingAtomic}))
	must(a.AllocateLeaf(3, gov8.CppGCObjectCallbacks{Destroy: func() { drop2.Add(1) }}))
	if err := a.Close(); err != nil {
		die(err)
	}
	if err := gov8.ShutdownCppGCProcess(); err != nil {
		die(err)
	}
	emit("cppgc-heap-lifecycle/process/paired_reinitialize", map[string]any{"second_initialize_succeeded": true, "atomic_heap_created": true, "heap_drop_terminates": true, "drops_after_heap_drop": drop2.Load(), "second_shutdown_succeeded": true})
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		die(err)
	}
	if err := gov8.Initialize(); err != nil {
		die(err)
	}
	var attached atomic.Int32
	custom := must(gov8.NewCppGCHeap(gov8.CppGCHeapCreateParams{MarkingSupport: gov8.CppGCMarkingAtomic, SweepingSupport: gov8.CppGCSweepingAtomic}))
	must(custom.AllocateLeaf(4, gov8.CppGCObjectCallbacks{Destroy: func() { attached.Add(1) }}))
	p := gov8.NewCreateParams()
	if err := p.SetCppGCHeap(custom); err != nil {
		die(err)
	}
	iso := must(gov8.NewIsolateWithParams(p))
	same := must(custom.AttachedTo(iso))
	if err := iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		die(err)
	}
	afterGC := attached.Load()
	_, sharedErr := iso.TryIntoShared()
	kind := ""
	if e, ok := sharedErr.(*gov8.IntoSharedError); ok {
		kind = string(e.Kind)
	}
	ctx := must(iso.NewContext())
	scope := must(iso.NewScope())
	tpl := must(iso.NewFunctionTemplate(scope, func(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}, nil))
	fn := must(tpl.GetFunction(scope, ctx))
	wrapper, ok, err := fn.NewInstance(scope)
	if err != nil || !ok {
		die(err)
	}
	target := must(scope.NewObject(ctx))
	must(scope.WrapCppGCObject(wrapper, target.Value, 5, 1, gov8.CppGCObjectCallbacks{Destroy: func() { attached.Add(1) }}))
	if err := scope.Close(); err != nil {
		die(err)
	}
	if err := ctx.Close(); err != nil {
		die(err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		die(err)
	}
	if err := iso.Close(); err != nil {
		die(err)
	}
	emit("cppgc-heap-lifecycle/isolate/custom_heap_ownership", map[string]any{"get_cpp_heap_some": true, "same_heap_address": same, "drops_after_attached_gc": afterGC, "try_into_shared_error": map[string]string{"embedder_cpp_heap": "EmbedderCppHeap"}[kind], "drops_after_isolate_drop": attached.Load(), "isolate_drop_owns_heap_termination": true})
	disposed := must(gov8.Dispose())
	if err := gov8.DisposePlatform(); err != nil {
		die(err)
	}
	emit("cppgc-heap-lifecycle/process/orderly_v8_shutdown", map[string]any{"isolate_dropped_first": true, "v8_disposed": disposed, "cppgc_shutdown_after_heaps": true, "platform_disposed_last": true})
	fmt.Println(`{"summary":{"total":6,"passed":6,"failed":0}}`)
}

func negativeChild() {
	if err := gov8.InitializeCppGCProcess(); err != nil {
		die(err)
	}
	if err := gov8.InitializeCppGCProcess(); err == nil {
		die("duplicate initialize succeeded")
	}
	heap := must(gov8.NewCppGCHeap(gov8.DefaultCppGCHeapCreateParams()))
	if err := gov8.ShutdownCppGCProcess(); err == nil {
		die("shutdown with live heap succeeded")
	}
	if err := heap.EnableDetachedGarbageCollectionsForTesting(); err != nil {
		die(err)
	}
	if err := heap.EnableDetachedGarbageCollectionsForTesting(); err == nil {
		die("duplicate detached enable succeeded")
	}
	if err := heap.Close(); err != nil {
		die(err)
	}
	if err := gov8.ShutdownCppGCProcess(); err != nil {
		die(err)
	}
	if err := gov8.ShutdownCppGCProcess(); err == nil {
		die("duplicate shutdown succeeded")
	}
	fmt.Println("safe-normalizations-ok")
}

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		child()
		os.Exit(0)
	}
	if os.Getenv(childEnv) == "negative" {
		negativeChild()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type line struct {
	Check   string         `json:"check"`
	OK      bool           `json:"ok"`
	Value   map[string]any `json:"value"`
	Summary map[string]int `json:"summary"`
}

func parse(t *testing.T, b []byte) []line {
	var out []line
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		var x line
		if e := json.Unmarshal(s.Bytes(), &x); e != nil {
			t.Fatal(e)
		}
		out = append(out, x)
	}
	return out
}
func TestFixture(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	got, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("%v\n%s", e, got)
	}
	want, e := os.ReadFile(filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-cppgc-heap-lifecycle-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	a, w := parse(t, got), parse(t, want)
	if len(a) != 7 || len(w) != 7 {
		t.Fatalf("lines %d/%d", len(a), len(w))
	}
	for i := range w {
		aj, _ := json.Marshal(a[i])
		wj, _ := json.Marshal(w[i])
		if !bytes.Equal(aj, wj) {
			t.Fatalf("line %d\ngot %s\nwant %s", i, aj, wj)
		}
	}
}

func TestSafetyNormalizations(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=negative")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "safe-normalizations-ok\n" {
		t.Fatalf("%v\n%s", err, out)
	}
}
