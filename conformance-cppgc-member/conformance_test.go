//go:build windows && amd64

package cppgc_member_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const childEnv = "GOV8_CPPGC_MEMBER_CONFORMANCE_CHILD"

type dropOrder struct {
	mu  sync.Mutex
	ids []int32
}

func (o *dropOrder) add(id int32) {
	o.mu.Lock()
	o.ids = append(o.ids, id)
	o.mu.Unlock()
}

func (o *dropOrder) sorted() []int32 {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := append([]int32(nil), o.ids...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

type childRuntime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	drops atomic.Int32
	order dropOrder
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

func wrapper(r *childRuntime, scope *gov8.Scope) *gov8.Object {
	template := must(r.iso.NewFunctionTemplate(scope, func(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}, nil))
	function := must(template.GetFunction(scope, r.ctx))
	object, ok, err := function.NewInstance(scope)
	if err != nil || !ok {
		fail("NewInstance: %v, %v", ok, err)
	}
	return object
}

func allocate(r *childRuntime, scope *gov8.Scope, id int32) *gov8.CppGCObject {
	objectWrapper := wrapper(r, scope)
	target := must(scope.NewObject(r.ctx))
	object, err := scope.WrapCppGCObject(objectWrapper, target.Value, id, 7, gov8.CppGCObjectCallbacks{
		Destroy: func() {
			r.drops.Add(1)
			r.order.add(id)
		},
	})
	if err != nil {
		fail("allocate %d: %v", id, err)
	}
	return object
}

func createRoot(r *childRuntime, id int32) (*gov8.CppGCPersistent, *gov8.CppGCWeakPersistent) {
	scope := must(r.iso.NewScope())
	object := allocate(r, scope, id)
	root := must(gov8.NewCppGCPersistent(object))
	observer := must(gov8.NewCppGCWeakPersistent(object))
	if err := scope.Close(); err != nil {
		fail("createRoot scope: %v", err)
	}
	return root, observer
}

func createPair(r *childRuntime, ownerID, childID int32) (*gov8.CppGCPersistent, *gov8.CppGCWeakPersistent) {
	scope := must(r.iso.NewScope())
	child := allocate(r, scope, childID)
	ownerObject := allocate(r, scope, ownerID)
	owner := must(gov8.NewCppGCPersistent(ownerObject))
	if err := owner.SetStrongMember(child); err != nil {
		fail("initialized strong: %v", err)
	}
	if err := owner.SetWeakMember(child); err != nil {
		fail("initialized weak: %v", err)
	}
	observer := must(gov8.NewCppGCWeakPersistent(child))
	if err := scope.Close(); err != nil {
		fail("createPair scope: %v", err)
	}
	return owner, observer
}

func assignChild(r *childRuntime, owner *gov8.CppGCPersistent, id int32, strong, weak bool) *gov8.CppGCWeakPersistent {
	scope := must(r.iso.NewScope())
	child := allocate(r, scope, id)
	if strong {
		if err := owner.SetStrongMember(child); err != nil {
			fail("set strong %d: %v", id, err)
		}
	}
	if weak {
		if err := owner.SetWeakMember(child); err != nil {
			fail("set weak %d: %v", id, err)
		}
	}
	observer := must(gov8.NewCppGCWeakPersistent(child))
	if err := scope.Close(); err != nil {
		fail("assignChild scope: %v", err)
	}
	return observer
}

func fullGC(r *childRuntime) {
	if err := r.iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		fail("full GC: %v", err)
	}
}

func edgeID(snapshot gov8.CppGCObjectSnapshot, present bool, err error) any {
	if err != nil {
		fail("edge Get: %v", err)
	}
	if !present {
		return nil
	}
	return snapshot.ObjectID
}

func emit(check string, value map[string]any) {
	line, err := json.Marshal(map[string]any{"check": check, "ok": true, "value": value})
	if err != nil {
		fail("marshal %s: %v", check, err)
	}
	fmt.Println(string(line))
}

func runChild() {
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		fail("SetFlagsFromString: %v", err)
	}
	if err := gov8.Initialize(); err != nil {
		fail("Initialize: %v", err)
	}
	r := &childRuntime{iso: must(gov8.NewIsolate())}
	r.ctx = must(r.iso.NewContext())

	root, rootObserver := createRoot(r, 1)
	empty := must(root.MemberEdges())
	childTwo := assignChild(r, root, 2, true, true)
	assigned := must(root.MemberEdges())
	initializedRoot, initializedChild := createPair(r, 10, 11)
	initialized := must(initializedRoot.MemberEdges())
	emit("cppgc-member/handles/empty_new_set_get", map[string]any{
		"empty_strong_none":     !empty.StrongPresent,
		"empty_weak_none":       !empty.WeakPresent,
		"set_strong_id":         assigned.Strong.ObjectID,
		"set_weak_id":           assigned.Weak.ObjectID,
		"set_repeated_identity": assigned.SameTarget,
		"new_strong_id":         initialized.Strong.ObjectID,
		"new_weak_id":           initialized.Weak.ObjectID,
		"new_repeated_identity": initialized.SameTarget,
	})

	fullGC(r)
	childTwoSnapshot, childTwoOK, childTwoErr := childTwo.Get()
	beforeReassignDrops := r.drops.Load()
	childThree := assignChild(r, root, 3, true, false)
	noSynchronousDrop := r.drops.Load() == beforeReassignDrops
	fullGC(r)
	afterReassign := must(root.MemberEdges())
	_, childTwoStillOK, childTwoErr2 := childTwo.Get()
	if childTwoErr != nil || childTwoErr2 != nil {
		fail("child two observer: %v / %v", childTwoErr, childTwoErr2)
	}
	beforeClear := r.drops.Load()
	if err := root.ClearStrongMember(); err != nil {
		fail("clear strong: %v", err)
	}
	clearNotSynchronous := r.drops.Load() == beforeClear
	fullGC(r)
	_, childThreeOK, childThreeErr := childThree.Get()
	if childThreeErr != nil {
		fail("child three observer: %v", childThreeErr)
	}
	childFour := assignChild(r, root, 4, true, true)
	fullGC(r)
	reused := must(root.MemberEdges())
	childFourSnapshot, childFourOK, childFourErr := childFour.Get()
	if childFourErr != nil {
		fail("child four observer: %v", childFourErr)
	}
	emit("cppgc-member/strong/reassign_clear_reuse", map[string]any{
		"child_survives_member_only": childTwoOK && childTwoSnapshot.ObjectID == 2,
		"drops_while_strong":         beforeReassignDrops,
		"reassign_not_synchronous":   noSynchronousDrop,
		"old_child_and_weak_cleared": !childTwoStillOK && !afterReassign.WeakPresent,
		"new_strong_id":              afterReassign.Strong.ObjectID,
		"clear_not_synchronous":      clearNotSynchronous,
		"cleared_child_collected":    !childThreeOK,
		"reused_strong_id":           reused.Strong.ObjectID,
		"reused_weak_id":             reused.Weak.ObjectID,
		"reused_identity":            reused.SameTarget,
		"reused_observer_id":         edgeID(childFourSnapshot, childFourOK, nil),
	})

	weakRoot, weakRootObserver := createRoot(r, 20)
	weakOnly := assignChild(r, weakRoot, 21, false, true)
	fullGC(r)
	_, weakOnlyOK, weakOnlyErr := weakOnly.Get()
	weakEdges := must(weakRoot.MemberEdges())
	if weakOnlyErr != nil {
		fail("weak-only observer: %v", weakOnlyErr)
	}
	reusedWeak := assignChild(r, weakRoot, 22, true, true)
	fullGC(r)
	weakReusedAlive := must(weakRoot.MemberEdges())
	if err := weakRoot.ClearStrongMember(); err != nil {
		fail("clear weak-root strong: %v", err)
	}
	fullGC(r)
	_, reusedWeakOK, reusedWeakErr := reusedWeak.Get()
	weakAfterRelease := must(weakRoot.MemberEdges())
	if reusedWeakErr != nil {
		fail("reused weak observer: %v", reusedWeakErr)
	}
	emit("cppgc-member/weak/clearing_and_reuse", map[string]any{
		"weak_only_cleared":            !weakOnlyOK && !weakEdges.WeakPresent,
		"weak_only_drop_seen":          contains(r.order.sorted(), 21),
		"reused_alive_while_strong":    weakReusedAlive.StrongPresent && weakReusedAlive.WeakPresent && weakReusedAlive.Strong.ObjectID == 22 && weakReusedAlive.Weak.ObjectID == 22,
		"reused_identity":              weakReusedAlive.SameTarget,
		"reused_cleared_after_release": !reusedWeakOK && !weakAfterRelease.WeakPresent,
	})

	scope := must(r.iso.NewScope())
	cycleAObject := allocate(r, scope, 30)
	cycleBObject := allocate(r, scope, 31)
	cycleARoot := must(gov8.NewCppGCPersistent(cycleAObject))
	cycleBRoot := must(gov8.NewCppGCPersistent(cycleBObject))
	cycleA := must(gov8.NewCppGCWeakPersistent(cycleAObject))
	cycleB := must(gov8.NewCppGCWeakPersistent(cycleBObject))
	if err := cycleARoot.SetStrongMember(cycleBObject); err != nil {
		fail("cycle A->B: %v", err)
	}
	if err := cycleBRoot.SetStrongMember(cycleAObject); err != nil {
		fail("cycle B->A: %v", err)
	}
	if err := cycleARoot.Close(); err != nil {
		fail("cycle A root close: %v", err)
	}
	if err := cycleBRoot.Close(); err != nil {
		fail("cycle B root close: %v", err)
	}
	if err := scope.Close(); err != nil {
		fail("cycle scope close: %v", err)
	}
	fullGC(r)
	_, cycleAOK, cycleAErr := cycleA.Get()
	_, cycleBOK, cycleBErr := cycleB.Get()
	if cycleAErr != nil || cycleBErr != nil {
		fail("cycle observers: %v / %v", cycleAErr, cycleBErr)
	}
	cycleIDs := r.order.sorted()
	emit("cppgc-member/cycle/unreachable_strong_cycle", map[string]any{
		"first_cleared":  !cycleAOK,
		"second_cleared": !cycleBOK,
		"first_dropped":  contains(cycleIDs, 30),
		"second_dropped": contains(cycleIDs, 31),
	})

	for _, closeHandle := range []func() error{root.Close, initializedRoot.Close, weakRoot.Close} {
		if err := closeHandle(); err != nil {
			fail("owner close: %v", err)
		}
	}
	fullGC(r)
	beforeTeardownIDs := r.order.sorted()
	teardownRoot, teardownRootObserver := createRoot(r, 40)
	teardownChild := assignChild(r, teardownRoot, 41, true, true)
	if err := r.ctx.Close(); err != nil {
		fail("Context.Close: %v", err)
	}
	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		fail("ReleaseIsolateHostState: %v", err)
	}
	if err := r.iso.Close(); err != nil {
		fail("Isolate.Close: %v", err)
	}
	afterTeardownIDs := r.order.sorted()
	_, teardownRootOK, teardownRootErr := teardownRootObserver.Get()
	_, teardownChildOK, teardownChildErr := teardownChild.Get()
	teardownHandlesCleared := !teardownRootOK && !teardownChildOK &&
		strings.Contains(fmt.Sprint(teardownRootErr), "after Close") && strings.Contains(fmt.Sprint(teardownChildErr), "after Close")
	for _, closeHandle := range []func() error{
		teardownRoot.Close, teardownRootObserver.Close, teardownChild.Close,
		rootObserver.Close, childTwo.Close, childThree.Close, childFour.Close,
		initializedChild.Close, weakRootObserver.Close, weakOnly.Close,
		reusedWeak.Close, cycleA.Close, cycleB.Close,
	} {
		if err := closeHandle(); err != nil {
			fail("post-isolate handle close: %v", err)
		}
	}
	afterHandleDropIDs := r.order.sorted()
	expected := []int32{1, 2, 3, 4, 10, 11, 20, 21, 22, 30, 31, 40, 41}
	emit("cppgc-member/lifecycle/owner_isolate_teardown", map[string]any{
		"pre_teardown_ids":               beforeTeardownIDs,
		"teardown_handles_cleared":       teardownHandlesCleared,
		"teardown_added_owner_and_child": len(afterTeardownIDs) >= 2 && afterTeardownIDs[len(afterTeardownIDs)-2] == 40 && afterTeardownIDs[len(afterTeardownIDs)-1] == 41,
		"all_ids_once":                   equalIDs(afterTeardownIDs, expected),
		"handle_drop_does_not_redestroy": equalIDs(afterHandleDropIDs, afterTeardownIDs),
		"total_drops":                    r.drops.Load(),
	})
	if _, err := gov8.Dispose(); err != nil {
		fail("Dispose: %v", err)
	}
	if err := gov8.DisposePlatform(); err != nil {
		fail("DisposePlatform: %v", err)
	}
	fmt.Println(`{"summary":{"total":5,"passed":5,"failed":0}}`)
}

func contains(values []int32, target int32) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func equalIDs(left, right []int32) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		runChild()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fixtureLine struct {
	Check   string         `json:"check"`
	OK      bool           `json:"ok"`
	Value   map[string]any `json:"value"`
	Summary map[string]int `json:"summary"`
}

func parseLines(t *testing.T, data []byte) []fixtureLine {
	t.Helper()
	var lines []fixtureLine
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("decode line %q: %v", scanner.Bytes(), err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestCppGCMemberMatchesFixture(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	actualBytes, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, actualBytes)
	}
	wantBytes, err := os.ReadFile(filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-cppgc-member-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	actual := parseLines(t, actualBytes)
	want := parseLines(t, wantBytes)
	if len(actual) != 6 || len(want) != 6 {
		t.Fatalf("line count actual/want = %d/%d", len(actual), len(want))
	}
	seen := make(map[string]bool)
	for i := range want {
		if actual[i].Check != want[i].Check || actual[i].OK != want[i].OK {
			t.Fatalf("line %d identity actual=%q/%v want=%q/%v", i, actual[i].Check, actual[i].OK, want[i].Check, want[i].OK)
		}
		if actual[i].Check != "" {
			if seen[actual[i].Check] {
				t.Fatalf("duplicate check %q", actual[i].Check)
			}
			seen[actual[i].Check] = true
		}
		actualJSON, _ := json.Marshal(actual[i])
		wantJSON, _ := json.Marshal(want[i])
		if !bytes.Equal(actualJSON, wantJSON) {
			t.Fatalf("line %d mismatch\nactual: %s\nwant:   %s", i, actualJSON, wantJSON)
		}
	}
}
