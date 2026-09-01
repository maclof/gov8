//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestCppGCMemberEmptySetClearAndReuse(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	ownerObject, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 1, nil)
	first, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 2, nil)
	second, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 3, nil)
	owner, err := gov8.NewCppGCPersistent(ownerObject)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	edges, err := owner.MemberEdges()
	if err != nil {
		t.Fatal(err)
	}
	if edges.StrongPresent || edges.WeakPresent || edges.SameTarget {
		t.Fatalf("new edges = %#v", edges)
	}
	if err := owner.SetStrongMember(first); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetWeakMember(first); err != nil {
		t.Fatal(err)
	}
	edges, err = owner.MemberEdges()
	if err != nil {
		t.Fatal(err)
	}
	if !edges.StrongPresent || !edges.WeakPresent || !edges.SameTarget || edges.Strong.ObjectID != 2 || edges.Weak.ObjectID != 2 {
		t.Fatalf("assigned edges = %#v", edges)
	}
	if err := owner.SetStrongMember(second); err != nil {
		t.Fatal(err)
	}
	edges, err = owner.MemberEdges()
	if err != nil {
		t.Fatal(err)
	}
	if edges.Strong.ObjectID != 3 || edges.Weak.ObjectID != 2 || edges.SameTarget {
		t.Fatalf("reassigned edges = %#v", edges)
	}
	if err := owner.ClearStrongMember(); err != nil {
		t.Fatal(err)
	}
	if err := owner.ClearWeakMember(); err != nil {
		t.Fatal(err)
	}
	edges, err = owner.MemberEdges()
	if err != nil || edges.StrongPresent || edges.WeakPresent {
		t.Fatalf("cleared edges = %#v, %v", edges, err)
	}
	if err := owner.SetStrongMember(first); err != nil {
		t.Fatal(err)
	}
	if snapshot, ok, err := owner.StrongMember(); err != nil || !ok || snapshot.ObjectID != 2 {
		t.Fatalf("reused strong = %#v, %v, %v", snapshot, ok, err)
	}
}

func TestCppGCMemberSafetyAffinityAndIsolation(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	ownerObject, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 1, nil)
	child, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 2, nil)
	owner, err := gov8.NewCppGCPersistent(ownerObject)
	if err != nil {
		t.Fatal(err)
	}
	if err := (*gov8.CppGCPersistent)(nil).SetStrongMember(child); err == nil {
		t.Fatal("nil owner SetStrongMember succeeded")
	}
	if err := owner.SetStrongMember(nil); err == nil {
		t.Fatal("nil child SetStrongMember succeeded")
	}

	foreign, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	foreignCtx, err := foreign.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	foreignScope, err := foreign.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	foreignChild, _ := wrapCppGCPersistentTestObject(t, foreign, foreignCtx, foreignScope, 9, nil)
	if err := owner.SetWeakMember(foreignChild); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign child error = %v", err)
	}
	if err := foreignScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := foreignCtx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}

	wrongThread := make(chan error, 1)
	go func() {
		_, _, err := owner.StrongMember()
		wrongThread <- err
	}()
	if err := <-wrongThread; err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}

	closedScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedChild, _ := wrapCppGCPersistentTestObject(t, iso, ctx, closedScope, 4, nil)
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetStrongMember(closedChild); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("closed child error = %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := owner.WeakMember(); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("closed owner error = %v", err)
	}
}
