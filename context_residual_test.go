//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestContextResidualEmbedderDataPrevalidation(t *testing.T) {
	isolate, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := isolate.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	context, err := isolate.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	value, err := scope.Int32(1)
	if err != nil {
		t.Fatal(err)
	}

	for _, slot := range []int{-3, -1, 4094, 1_000_000} {
		if err := context.SetEmbedderData(scope, slot, value); err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("SetEmbedderData(%d) = %v; want range error", slot, err)
		}
		if _, _, err := context.GetEmbedderData(scope, slot); err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("GetEmbedderData(%d) = %v; want range error", slot, err)
		}
		if err := context.SetAlignedPointerInEmbedderData(slot, 0); err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("SetAlignedPointerInEmbedderData(%d) = %v; want range error", slot, err)
		}
		if _, err := context.GetAlignedPointerFromEmbedderData(slot); err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("GetAlignedPointerFromEmbedderData(%d) = %v; want range error", slot, err)
		}
	}
	if err := context.SetAlignedPointerInEmbedderData(0, 1); err == nil || !strings.Contains(err.Error(), "not aligned") {
		t.Fatalf("unaligned pointer = %v; want alignment error", err)
	}
	if err := context.SetEmbedderData(scope, 4093, value); err != nil {
		t.Fatalf("highest valid embedder slot: %v", err)
	}

	_ = context.Close()
	_ = scope.Close()
	_ = isolate.Close()
}

func TestSnapshotCreatorRejectsUnclearedContextSlots(t *testing.T) {
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatal(err)
	}
	isolate := creator.Isolate()
	scope, err := isolate.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	context, err := isolate.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	context.SetSlot("host", 7)
	if err := creator.SetDefaultContext(context); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := creator.CreateBlob(gov8.FunctionCodeClear); err == nil || !strings.Contains(err.Error(), "ClearAllSlots") {
		t.Fatalf("CreateBlob with uncleared slots = %v; want safe clear-slots error", err)
	}
	context.ClearAllSlots()
	if err := context.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(gov8.FunctionCodeClear)
	if err != nil {
		t.Fatalf("CreateBlob after ClearAllSlots: %v", err)
	}
	if !blob.IsValid() {
		t.Error("blob is invalid after clearing slots")
	}
	_ = blob.Release()
}

func TestContextFromSnapshotWithOptionsRejectsForeignInputs(t *testing.T) {
	isolateA, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scopeA, err := isolateA.NewScope()
	if err != nil {
		t.Fatal(err)
	}

	type foreignHandles struct {
		queue   *gov8.MicrotaskQueue
		global  *gov8.Object
		release chan struct{}
		done    chan struct{}
	}
	foreign := make(chan foreignHandles, 1)
	go func() {
		isolateB, createErr := gov8.NewIsolate()
		if createErr != nil {
			foreign <- foreignHandles{}
			return
		}
		scopeB, _ := isolateB.NewScope()
		queueB, _ := isolateB.NewMicrotaskQueue(gov8.PolicyExplicit)
		contextB, _ := isolateB.NewContext()
		globalB, _ := contextB.GlobalObject(scopeB)
		release := make(chan struct{})
		done := make(chan struct{})
		foreign <- foreignHandles{queue: queueB, global: globalB, release: release, done: done}
		<-release
		_ = contextB.Close()
		_ = scopeB.Close()
		_ = queueB.Close()
		_ = isolateB.Close()
		close(done)
	}()
	handles := <-foreign
	if handles.queue == nil || handles.global == nil {
		t.Fatal("failed to construct foreign handles")
	}
	if _, _, err := scopeA.ContextFromSnapshotWithOptions(0, &gov8.ContextOptions{MicrotaskQueue: handles.queue}); err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Errorf("foreign queue = %v; want safe affinity error", err)
	}
	if _, _, err := scopeA.ContextFromSnapshotWithOptions(0, &gov8.ContextOptions{GlobalObject: handles.global}); err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Errorf("foreign global = %v; want safe affinity error", err)
	}
	close(handles.release)
	<-handles.done

	// A closed scope is rejected before dynamic dispatch.
	if err := scopeA.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := scopeA.ContextFromSnapshotWithOptions(0, nil); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("closed scope = %v; want lifecycle error", err)
	}
	_ = isolateA.Close()
}
