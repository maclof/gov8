//go:build windows && amd64

package cppgc_persistent_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const childEnv = "GOV8_CPPGC_PERSISTENT_CONFORMANCE_CHILD"

type runtimeState struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	order *orderedDrops
}

type orderedDrops struct {
	mu  sync.Mutex
	ids []int32
}

func (order *orderedDrops) add(id int32) {
	order.mu.Lock()
	order.ids = append(order.ids, id)
	order.mu.Unlock()
}

func (order *orderedDrops) snapshot() []int32 {
	order.mu.Lock()
	defer order.mu.Unlock()
	return append([]int32(nil), order.ids...)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

func must[T any](value T, err error) T {
	if err != nil {
		fail("%v", err)
	}
	return value
}

func apiWrapper(r *runtimeState, scope *gov8.Scope) *gov8.Object {
	template := must(r.iso.NewFunctionTemplate(scope, func(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}, nil))
	function := must(template.GetFunction(scope, r.ctx))
	wrapper, ok, err := function.NewInstance(scope)
	if err != nil || !ok {
		fail("NewInstance: ok=%v err=%v", ok, err)
	}
	return wrapper
}

func allocate(r *runtimeState, scope *gov8.Scope, id int32, drops *atomic.Int32) (*gov8.CppGCObject, *gov8.Object) {
	wrapper := apiWrapper(r, scope)
	target := must(scope.NewObject(r.ctx))
	object, err := scope.WrapCppGCObject(wrapper, target.Value, id, 7, gov8.CppGCObjectCallbacks{
		Destroy: func() {
			drops.Add(1)
			r.order.add(id)
		},
	})
	if err != nil {
		fail("WrapCppGCObject(%d): %v", id, err)
	}
	return object, wrapper
}

func createPair(r *runtimeState, id int32, drops *atomic.Int32) (*gov8.CppGCPersistent, *gov8.CppGCWeakPersistent) {
	scope := must(r.iso.NewScope())
	object, wrapper := allocate(r, scope, id, drops)
	global := must(r.ctx.GlobalObject(scope))
	if ok, err := global.SetByName(scope, r.ctx, "wrapper", wrapper.Value); err != nil || !ok {
		fail("root wrapper: ok=%v err=%v", ok, err)
	}
	strong := must(gov8.NewCppGCPersistent(object))
	weak := must(gov8.NewCppGCWeakPersistent(object))
	if err := scope.Close(); err != nil {
		fail("createPair scope Close: %v", err)
	}
	return strong, weak
}

func createWeakOnly(r *runtimeState, id int32, drops *atomic.Int32) *gov8.CppGCWeakPersistent {
	scope := must(r.iso.NewScope())
	object, _ := allocate(r, scope, id, drops)
	weak := must(gov8.NewCppGCWeakPersistent(object))
	if err := scope.Close(); err != nil {
		fail("createWeakOnly scope Close: %v", err)
	}
	return weak
}

func assignStrong(r *runtimeState, target *gov8.CppGCPersistent, id int32, drops *atomic.Int32) *gov8.CppGCWeakPersistent {
	scope := must(r.iso.NewScope())
	object, _ := allocate(r, scope, id, drops)
	if err := target.Set(object); err != nil {
		fail("strong Set(%d): %v", id, err)
	}
	weak := must(gov8.NewCppGCWeakPersistent(object))
	if err := scope.Close(); err != nil {
		fail("assignStrong scope Close: %v", err)
	}
	return weak
}

func assignWeakWithStrong(r *runtimeState, target *gov8.CppGCWeakPersistent, id int32, drops *atomic.Int32) *gov8.CppGCPersistent {
	scope := must(r.iso.NewScope())
	object, _ := allocate(r, scope, id, drops)
	if err := target.Set(object); err != nil {
		fail("weak Set(%d): %v", id, err)
	}
	strong := must(gov8.NewCppGCPersistent(object))
	if err := scope.Close(); err != nil {
		fail("assignWeakWithStrong scope Close: %v", err)
	}
	return strong
}

func removeWrapper(r *runtimeState) {
	scope := must(r.iso.NewScope())
	global := must(r.ctx.GlobalObject(scope))
	undefined := must(scope.Undefined())
	if ok, err := global.SetByName(scope, r.ctx, "wrapper", undefined); err != nil || !ok {
		fail("remove wrapper: ok=%v err=%v", ok, err)
	}
	if err := scope.Close(); err != nil {
		fail("removeWrapper scope Close: %v", err)
	}
}

func fullGC(r *runtimeState) {
	scope := must(r.iso.NewScope())
	if err := r.iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		fail("full GC: %v", err)
	}
	if err := scope.Close(); err != nil {
		fail("fullGC scope Close: %v", err)
	}
}

func getID(snapshot gov8.CppGCObjectSnapshot, ok bool, err error) int32 {
	if err != nil || !ok {
		return -1
	}
	return snapshot.ObjectID
}

func child() {
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		fail("SetFlagsFromString: %v", err)
	}
	if err := gov8.Initialize(); err != nil {
		fail("Initialize: %v", err)
	}
	r := &runtimeState{iso: must(gov8.NewIsolate()), order: &orderedDrops{}}
	r.ctx = must(r.iso.NewContext())

	var rootDrops, weakDrops, firstDrops, secondDrops, reusedDrops, teardownDrops atomic.Int32
	emptyStrong := must(gov8.NewEmptyCppGCPersistent(r.iso))
	emptyWeak := must(gov8.NewEmptyCppGCWeakPersistent(r.iso))
	_, emptyStrongOK, emptyStrongErr := emptyStrong.Get()
	_, emptyWeakOK, emptyWeakErr := emptyWeak.Get()
	if emptyStrongErr != nil || emptyWeakErr != nil {
		fail("empty Get: strong=%v weak=%v", emptyStrongErr, emptyWeakErr)
	}
	root, rootWeak := createPair(r, 10, &rootDrops)
	rootFirst, rootOK, rootErr := root.Get()
	rootSecond, rootSecondOK, rootSecondErr := root.Get()
	rootWeakSnapshot, rootWeakOK, rootWeakErr := rootWeak.Get()
	if rootErr != nil || rootSecondErr != nil || rootWeakErr != nil {
		fail("root Get: %v %v %v", rootErr, rootSecondErr, rootWeakErr)
	}
	fmt.Printf("{\"check\":\"cppgc-persistent/handles/empty_new_get_identity\",\"ok\":true,\"value\":{\"empty_persistent_none\":%t,\"empty_weak_none\":%t,\"new_id\":%d,\"repeated_get_identity\":%t,\"strong_weak_identity\":%t}}\n",
		!emptyStrongOK, !emptyWeakOK, rootFirst.ObjectID,
		rootOK && rootSecondOK && rootFirst == rootSecond,
		rootOK && rootWeakOK && rootFirst == rootWeakSnapshot)

	removeWrapper(r)
	fullGC(r)
	rootAfter, rootAfterOK, rootAfterErr := root.Get()
	_, weakAfterOK, weakAfterErr := rootWeak.Get()
	if rootAfterErr != nil || weakAfterErr != nil {
		fail("root after GC: %v %v", rootAfterErr, weakAfterErr)
	}
	fmt.Printf("{\"check\":\"cppgc-persistent/strong/root_after_wrapper_removal\",\"ok\":true,\"value\":{\"strong_id_after_full_gc\":%d,\"weak_still_present\":%t,\"drops_while_strong\":%d}}\n",
		getID(rootAfter, rootAfterOK, nil), weakAfterOK, rootDrops.Load())

	weakOnly := createWeakOnly(r, 20, &weakDrops)
	fullGC(r)
	_, weakClearedOK, weakClearedErr := weakOnly.Get()
	if weakClearedErr != nil {
		fail("weak after GC: %v", weakClearedErr)
	}
	fullGC(r)
	_, weakStillOK, weakStillErr := weakOnly.Get()
	if weakStillErr != nil {
		fail("weak after second GC: %v", weakStillErr)
	}
	fmt.Printf("{\"check\":\"cppgc-persistent/weak/clearing_and_destruction\",\"ok\":true,\"value\":{\"cleared_after_full_gc\":%t,\"still_clear_after_second_gc\":%t,\"drops_after_two_gcs\":%d}}\n",
		!weakClearedOK, !weakStillOK, weakDrops.Load())

	reusableStrong := must(gov8.NewEmptyCppGCPersistent(r.iso))
	weakFirst := assignStrong(r, reusableStrong, 30, &firstDrops)
	firstID := getID(reusableStrong.Get())
	weakSecond := assignStrong(r, reusableStrong, 31, &secondDrops)
	fullGC(r)
	_, firstWeakOK, firstWeakErr := weakFirst.Get()
	secondSnapshot, secondOK, secondErr := reusableStrong.Get()
	if firstWeakErr != nil || secondErr != nil {
		fail("reassign first/second: %v %v", firstWeakErr, secondErr)
	}
	reusableWeak := must(gov8.NewEmptyCppGCWeakPersistent(r.iso))
	if err := reusableWeak.SetFromPersistent(reusableStrong); err != nil {
		fail("weak SetFromPersistent: %v", err)
	}
	weakSetSnapshot, weakSetOK, weakSetErr := reusableWeak.Get()
	if weakSetErr != nil {
		fail("weak set Get: %v", weakSetErr)
	}
	emptySource := must(gov8.NewEmptyCppGCPersistent(r.iso))
	if err := reusableStrong.SetFromPersistent(emptySource); err != nil {
		fail("strong clear from empty: %v", err)
	}
	_, strongAfterEmptyOK, strongAfterEmptyErr := reusableStrong.Get()
	if strongAfterEmptyErr != nil {
		fail("strong after empty: %v", strongAfterEmptyErr)
	}
	_ = emptySource.Close()
	releaseNotSynchronous := secondDrops.Load() == 0
	fullGC(r)
	_, weakSecondOK, weakSecondErr := weakSecond.Get()
	_, reusableWeakOK, reusableWeakErr := reusableWeak.Get()
	if weakSecondErr != nil || reusableWeakErr != nil {
		fail("second weak clear: %v %v", weakSecondErr, reusableWeakErr)
	}
	reusedObserver := assignStrong(r, reusableStrong, 32, &reusedDrops)
	if err := reusableWeak.SetFromPersistent(reusableStrong); err != nil {
		fail("reuse weak SetFromPersistent: %v", err)
	}
	reusedStrongSnapshot, reusedStrongOK, reusedStrongErr := reusableStrong.Get()
	reusedWeakSnapshot, reusedWeakOK, reusedWeakErr := reusableWeak.Get()
	if reusedStrongErr != nil || reusedWeakErr != nil {
		fail("reused Get: %v %v", reusedStrongErr, reusedWeakErr)
	}
	reusedIdentity := reusedStrongOK && reusedWeakOK && reusedStrongSnapshot == reusedWeakSnapshot
	_ = reusableStrong.Close()
	fullGC(r)
	_, reusableWeakAfterOK, reusableWeakAfterErr := reusableWeak.Get()
	_, observerAfterOK, observerAfterErr := reusedObserver.Get()
	if reusableWeakAfterErr != nil || observerAfterErr != nil {
		fail("reused weak clear: %v %v", reusableWeakAfterErr, observerAfterErr)
	}
	fmt.Printf("{\"check\":\"cppgc-persistent/reassign/release_and_reuse\",\"ok\":true,\"value\":{\"first_id_before_reassign\":%d,\"first_weak_cleared\":%t,\"first_drops\":%d,\"second_id_after_reassign\":%d,\"weak_set_identity\":%t,\"strong_none_after_empty_set\":%t,\"release_not_synchronous\":%t,\"second_weaks_cleared\":%t,\"second_drops\":%d,\"persistent_reused_id\":%d,\"reused_weak_id\":%d,\"persistent_weak_reused_identity\":%t,\"reused_weaks_cleared\":%t,\"reused_drops\":%d}}\n",
		firstID, !firstWeakOK, firstDrops.Load(), getID(secondSnapshot, secondOK, nil),
		weakSetOK && secondOK && weakSetSnapshot == secondSnapshot, !strongAfterEmptyOK,
		releaseNotSynchronous, !weakSecondOK && !reusableWeakOK, secondDrops.Load(),
		getID(reusedStrongSnapshot, reusedStrongOK, nil), getID(reusedWeakSnapshot, reusedWeakOK, nil),
		reusedIdentity, !reusableWeakAfterOK && !observerAfterOK, reusedDrops.Load())

	_ = rootWeak.Close()
	fullGC(r)
	aliveAfterWeakDrop := rootDrops.Load() == 0
	_ = root.Close()
	strongDropNotSynchronous := rootDrops.Load() == 0
	fullGC(r)
	dropsAfterRelease := rootDrops.Load()
	fullGC(r)

	for _, closeHandle := range []func() error{emptyStrong.Close, emptyWeak.Close, weakOnly.Close, weakFirst.Close, weakSecond.Close, reusedObserver.Close, reusableWeak.Close} {
		if err := closeHandle(); err != nil {
			fail("pre-teardown handle Close: %v", err)
		}
	}
	teardownWeak := must(gov8.NewEmptyCppGCWeakPersistent(r.iso))
	teardownStrong := assignWeakWithStrong(r, teardownWeak, 90, &teardownDrops)
	if err := r.ctx.Close(); err != nil {
		fail("Context.Close: %v", err)
	}
	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		fail("ReleaseIsolateHostState: %v", err)
	}
	if err := r.iso.Close(); err != nil {
		fail("Isolate.Close: %v", err)
	}
	teardownDropsAfterIsolate := teardownDrops.Load()
	_, teardownWeakOK, teardownWeakErr := teardownWeak.Get()
	// Go closes the native weak wrapper during isolate teardown, so post-isolate
	// Get reports the closed isolate instead of exposing Rust's cleared pointer.
	weakClearedByTeardown := !teardownWeakOK && teardownWeakErr != nil && strings.Contains(teardownWeakErr.Error(), "after Close")
	if err := teardownWeak.Close(); err != nil {
		fail("post-isolate weak Close: %v", err)
	}
	if err := teardownStrong.Close(); err != nil {
		fail("post-isolate strong Close: %v", err)
	}
	handlesDoNotRedestroy := teardownDrops.Load() == teardownDropsAfterIsolate
	disposed := must(gov8.Dispose())
	if err := gov8.DisposePlatform(); err != nil {
		fail("DisposePlatform: %v", err)
	}
	order := r.order.snapshot()
	if len(order) != 6 {
		fail("drop order length = %d, values=%v", len(order), order)
	}
	fmt.Printf("{\"check\":\"cppgc-persistent/lifecycle/drop_order_exactly_once\",\"ok\":true,\"value\":{\"alive_after_weak_handle_drop\":%t,\"strong_drop_not_synchronous\":%t,\"drops_after_release_gc\":%d,\"drops_after_repeated_gc\":%d,\"target_drops_during_isolate_teardown\":%d,\"weak_cleared_by_isolate_teardown\":%t,\"handle_drops_after_isolate_do_not_redestroy\":%t,\"all_object_drop_order\":[%d,%d,%d,%d,%d,%d],\"v8_dispose\":%t,\"cppgc_shutdown\":true}}\n",
		aliveAfterWeakDrop, strongDropNotSynchronous, dropsAfterRelease, rootDrops.Load(),
		teardownDropsAfterIsolate, weakClearedByTeardown, handlesDoNotRedestroy,
		order[0], order[1], order[2], order[3], order[4], order[5], disposed)
	fmt.Println("{\"summary\":{\"total\":5,\"passed\":5,\"failed\":0}}")
}

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		child()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCppGCPersistentMatchesFixture(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-cppgc-persistent-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != string(fixture) {
		t.Fatalf("fixture mismatch\nactual:\n%s\nwant:\n%s", output, fixture)
	}
}
