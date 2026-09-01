//go:build windows && amd64

package gov8_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gov8 "github.com/maclof/gov8"
)

func wrapCppGCPersistentTestObject(t *testing.T, iso *gov8.Isolate, ctx *gov8.Context, scope *gov8.Scope, id int32, drops *atomic.Int32) (*gov8.CppGCObject, *gov8.Object) {
	t.Helper()
	wrapper := cppgcAPIWrapper(t, iso, ctx, scope)
	target, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	callbacks := gov8.CppGCObjectCallbacks{}
	if drops != nil {
		callbacks.Destroy = func() { drops.Add(1) }
	}
	object, err := scope.WrapCppGCObject(wrapper, target.Value, id, 1, callbacks)
	if err != nil {
		t.Fatalf("WrapCppGCObject: %v", err)
	}
	return object, wrapper
}

func TestCppGCPersistentEmptySetGetAndReuse(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	t.Cleanup(func() {
		if err := gov8.ReleaseIsolateHostState(iso); err != nil {
			t.Errorf("ReleaseIsolateHostState: %v", err)
		}
	})

	first, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 11, nil)
	second, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 22, nil)

	strong, err := gov8.NewEmptyCppGCPersistent(iso)
	if err != nil {
		t.Fatal(err)
	}
	weak, err := gov8.NewEmptyCppGCWeakPersistent(iso)
	if err != nil {
		t.Fatal(err)
	}
	for name, get := range map[string]func() (gov8.CppGCObjectSnapshot, bool, error){
		"strong": strong.Get,
		"weak":   weak.Get,
	} {
		if _, ok, err := get(); err != nil || ok {
			t.Fatalf("empty %s Get = ok=%v err=%v", name, ok, err)
		}
	}

	if err := strong.Set(first); err != nil {
		t.Fatal(err)
	}
	if err := weak.Set(first); err != nil {
		t.Fatal(err)
	}
	if snapshot, ok, err := strong.Get(); err != nil || !ok || snapshot.ObjectID != 11 || snapshot.Tag != 1 {
		t.Fatalf("strong Get = %#v, %v, %v", snapshot, ok, err)
	}
	if same, err := strong.Matches(first); err != nil || !same {
		t.Fatalf("strong Matches(first) = %v, %v", same, err)
	}
	if same, err := weak.Matches(first); err != nil || !same {
		t.Fatalf("weak Matches(first) = %v, %v", same, err)
	}

	if err := strong.Set(second); err != nil {
		t.Fatal(err)
	}
	if same, err := strong.Matches(first); err != nil || same {
		t.Fatalf("strong Matches(first) after Set = %v, %v", same, err)
	}
	if same, err := strong.Matches(second); err != nil || !same {
		t.Fatalf("strong Matches(second) after Set = %v, %v", same, err)
	}
	if snapshot, ok, err := strong.Get(); err != nil || !ok || snapshot.ObjectID != 22 {
		t.Fatalf("strong Get after Set = %#v, %v, %v", snapshot, ok, err)
	}
	if err := weak.SetFromPersistent(strong); err != nil {
		t.Fatal(err)
	}
	if same, err := weak.Matches(second); err != nil || !same {
		t.Fatalf("weak Matches(second) after SetFromPersistent = %v, %v", same, err)
	}
	emptyStrong, err := gov8.NewEmptyCppGCPersistent(iso)
	if err != nil {
		t.Fatal(err)
	}
	if err := weak.SetFromPersistent(emptyStrong); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := weak.Get(); err != nil || ok {
		t.Fatalf("weak Get after empty source = %v, %v", ok, err)
	}
	if err := emptyStrong.Close(); err != nil {
		t.Fatal(err)
	}

	initializedStrong, err := gov8.NewCppGCPersistent(first)
	if err != nil {
		t.Fatal(err)
	}
	initializedWeak, err := gov8.NewCppGCWeakPersistent(second)
	if err != nil {
		t.Fatal(err)
	}
	for _, closeHandle := range []func() error{initializedWeak.Close, initializedStrong.Close, weak.Close, strong.Close} {
		if err := closeHandle(); err != nil {
			t.Fatal(err)
		}
		if err := closeHandle(); err != nil {
			t.Fatalf("idempotent Close: %v", err)
		}
	}
	if _, _, err := strong.Get(); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("Get after Close = %v", err)
	}
}

func TestCppGCPersistentStrongRootsAndWeakClears(t *testing.T) {
	if os.Getenv("GOV8_CPPGC_PERSISTENT_GC_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCppGCPersistentStrongRootsAndWeakClears$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOV8_CPPGC_PERSISTENT_GC_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			if ctx.Err() != nil {
				t.Fatalf("full-GC child timed out: %v", ctx.Err())
			}
			t.Fatalf("full-GC child: %v\n%s", err, output)
		}
		return
	}
	iso, err := gov8.NewIsolate()
	if err != nil {
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
	var drops atomic.Int32
	object, wrapper := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 42, &drops)
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(scope, ctx, "cppgcPersistentRoot", wrapper.Value); err != nil || !ok {
		t.Fatalf("set global root = %v, %v", ok, err)
	}
	strong, err := gov8.NewCppGCPersistent(object)
	if err != nil {
		t.Fatal(err)
	}
	weak, err := gov8.NewCppGCWeakPersistent(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}

	scope, err = iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	global, err = ctx.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(scope, ctx, "cppgcPersistentRoot", undefined); err != nil || !ok {
		t.Fatalf("clear global root = %v, %v", ok, err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}

	if err := iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		t.Fatal(err)
	}
	if got := drops.Load(); got != 0 {
		t.Fatalf("drops while strongly rooted = %d", got)
	}
	if snapshot, ok, err := strong.Get(); err != nil || !ok || snapshot.ObjectID != 42 {
		t.Fatalf("strong rooted Get = %#v, %v, %v", snapshot, ok, err)
	}
	if snapshot, ok, err := weak.Get(); err != nil || !ok || snapshot.ObjectID != 42 {
		t.Fatalf("weak alongside strong Get = %#v, %v, %v", snapshot, ok, err)
	}

	if err := strong.Close(); err != nil {
		t.Fatal(err)
	}
	if got := drops.Load(); got != 0 {
		t.Fatalf("strong Close destroyed synchronously = %d", got)
	}
	if err := iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		t.Fatal(err)
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("drops after strong release = %d", got)
	}
	if _, ok, err := weak.Get(); err != nil || ok {
		t.Fatalf("weak Get after collection = %v, %v", ok, err)
	}
	if err := iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		t.Fatal(err)
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("drops after repeated collection = %d", got)
	}
	if _, ok, err := weak.Get(); err != nil || ok {
		t.Fatalf("weak Get after repeated collection = %v, %v", ok, err)
	}
	if err := weak.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCppGCPersistentLifecycleAffinityAndIsolation(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	t.Cleanup(func() {
		if err := gov8.ReleaseIsolateHostState(iso); err != nil {
			t.Errorf("ReleaseIsolateHostState: %v", err)
		}
	})
	object, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 7, nil)
	persistent, err := gov8.NewCppGCPersistent(object)
	if err != nil {
		t.Fatal(err)
	}

	getErr := make(chan error, 1)
	go func() {
		_, _, err := persistent.Get()
		getErr <- err
	}()
	if err := <-getErr; err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Fatalf("wrong-thread Get = %v", err)
	}
	closeErr := make(chan error, 1)
	go func() { closeErr <- persistent.Close() }()
	if err := <-closeErr; err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Fatalf("wrong-thread Close = %v", err)
	}

	other, otherCtx, otherScope := newTestRuntime(t)
	otherObject, _ := wrapCppGCPersistentTestObject(t, other, otherCtx, otherScope, 8, nil)
	if err := persistent.Set(otherObject); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign-isolate Set = %v", err)
	}
	otherPersistent, err := gov8.NewCppGCPersistent(otherObject)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistent.SetFromPersistent(otherPersistent); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign-isolate persistent source = %v", err)
	}
	if err := otherPersistent.Close(); err != nil {
		t.Fatal(err)
	}
	if err := persistent.Close(); err != nil {
		t.Fatal(err)
	}

	closedScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedObject, _ := wrapCppGCPersistentTestObject(t, iso, ctx, closedScope, 9, nil)
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := gov8.NewCppGCPersistent(closedObject); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("constructor from closed object = %v", err)
	}
}

func TestCppGCPersistentDrainsOnIsolateClose(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
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
	var drops atomic.Int32
	object, _ := wrapCppGCPersistentTestObject(t, iso, ctx, scope, 1, &drops)
	strong, err := gov8.NewCppGCPersistent(object)
	if err != nil {
		t.Fatal(err)
	}
	weak, err := gov8.NewCppGCWeakPersistent(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatalf("Close with persistent handles = %v", err)
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("drops during isolate teardown = %d", got)
	}
	if _, ok, err := weak.Get(); err == nil || ok || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("weak Get after isolate Close = %v, %v", ok, err)
	}
	if _, ok, err := strong.Get(); err == nil || ok || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("strong Get after isolate Close = %v, %v", ok, err)
	}
	if err := strong.Set(object); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("strong Set after isolate Close = %v", err)
	}
	if err := weak.SetFromPersistent(strong); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("weak SetFromPersistent after isolate Close = %v", err)
	}
	if err := strong.Close(); err != nil {
		t.Fatal(err)
	}
	if err := weak.Close(); err != nil {
		t.Fatal(err)
	}
	if err := strong.Close(); err != nil {
		t.Fatalf("second strong Close: %v", err)
	}
	if err := weak.Close(); err != nil {
		t.Fatalf("second weak Close: %v", err)
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("post-isolate handle Close redestroyed target = %d", got)
	}
}
