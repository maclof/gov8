//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestCppGCCustomHeapTransferAndSharedRejection(t *testing.T) {
	if _, err := gov8.NewCppGCHeap(gov8.CppGCHeapCreateParams{MarkingSupport: 9}); err == nil {
		t.Fatal("invalid marking enum succeeded")
	}
	heap, err := gov8.NewCppGCHeap(gov8.DefaultCppGCHeapCreateParams())
	if err != nil {
		t.Fatal(err)
	}
	params := gov8.NewCreateParams()
	if err := params.SetCppGCHeap(heap); err != nil {
		t.Fatal(err)
	}
	if err := params.SetCppGCHeap(heap); err == nil {
		t.Fatal("duplicate heap setter succeeded")
	}
	if err := heap.EnableDetachedGarbageCollectionsForTesting(); err == nil {
		t.Fatal("claimed heap entered detached mode")
	}
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := heap.AttachedTo(iso); err != nil || !same {
		t.Fatalf("AttachedTo = %v, %v", same, err)
	}
	if err := heap.Close(); err != nil {
		t.Fatalf("transferred Close: %v", err)
	}
	if _, err := iso.TryIntoShared(); err == nil || !strings.Contains(err.Error(), "embedder_cpp_heap") {
		t.Fatalf("TryIntoShared = %v", err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCppGCHeapWrongThread(t *testing.T) {
	heap, err := gov8.NewCppGCHeap(gov8.DefaultCppGCHeapCreateParams())
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan error, 1)
	go func() { ch <- heap.Close() }()
	if err := <-ch; err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Fatalf("wrong-thread Close = %v", err)
	}
	if err := heap.Close(); err != nil {
		t.Fatal(err)
	}
}
