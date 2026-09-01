//go:build windows && amd64

package gov8_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gov8 "gov8"
)

type graphTestState struct {
	Label   string
	Numbers []int
	Nested  int
}

func cloneGraphTestState(value graphTestState) (graphTestState, error) {
	value.Numbers = append([]int(nil), value.Numbers...)
	return value, nil
}

func TestCppGCGenericGraphTypedStateOwnership(t *testing.T) {
	var mu sync.Mutex
	var drops []graphTestState
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if len(drops) != 2 || drops[1].Label != "beta" {
			t.Errorf("final drops = %+v", drops)
		}
	})
	iso, _, scope := newTestRuntime(t)
	graph, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[graphTestState]{
		State: graphTestState{Label: "alpha", Numbers: []int{1, 2, 3}, Nested: 7},
		Name:  "typed-state",
		Callbacks: gov8.CppGCGenericGraphCallbacks[graphTestState]{
			Clone: cloneGraphTestState,
			Drop: func(value graphTestState) {
				mu.Lock()
				drops = append(drops, value)
				mu.Unlock()
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := graph.State()
	if err != nil || state.Label != "alpha" || len(state.Numbers) != 3 {
		t.Fatalf("initial State = %+v, %v", state, err)
	}
	state.Numbers[0] = 99
	again, err := graph.State()
	if err != nil || again.Numbers[0] != 1 {
		t.Fatalf("State alias escaped = %+v, %v", again, err)
	}
	if err := graph.UpdateState(func(value *graphTestState) error {
		value.Label += "+"
		value.Numbers = append(value.Numbers, 4, 5)
		value.Nested += 5
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := graph.State()
	if err != nil || updated.Label != "alpha+" || updated.Nested != 12 || len(updated.Numbers) != 5 {
		t.Fatalf("updated State = %+v, %v", updated, err)
	}
	mu.Lock()
	if len(drops) != 0 {
		t.Fatalf("in-place update drops = %+v", drops)
	}
	mu.Unlock()
	if err := graph.ReplaceState(graphTestState{Label: "beta", Numbers: []int{8, 13}, Nested: 21}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(drops) != 1 || drops[0].Label != "alpha+" {
		t.Fatalf("synchronous replacement drops = %+v", drops)
	}
	mu.Unlock()
	if err := graph.UpdateState(func(*graphTestState) error { return errors.New("retry") }); err == nil {
		t.Fatal("failed update unexpectedly succeeded")
	}
	afterFailure, err := graph.State()
	if err != nil || afterFailure.Label != "beta" {
		t.Fatalf("state changed after failed update = %+v, %v", afterFailure, err)
	}
	if err := graph.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCppGCGenericGraphEdgesTracedNameAndLifecycle(t *testing.T) {
	var nameCalls, traceCalls, destroys atomic.Int32
	t.Cleanup(func() {
		if destroys.Load() != 4 {
			t.Errorf("destroy callbacks = %d", destroys.Load())
		}
	})
	iso, ctx, scope := newTestRuntime(t)
	newGraph := func(label string, strong, weak uint32) *gov8.CppGCGenericGraph[graphTestState] {
		graph, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[graphTestState]{
			State: graphTestState{Label: label}, Name: "CppGCGenericGraph-" + label,
			StrongSlots: strong, WeakSlots: weak,
			Callbacks: gov8.CppGCGenericGraphCallbacks[graphTestState]{
				Clone: cloneGraphTestState,
				NameObserved: func() {
					nameCalls.Add(1)
				},
				TraceObserved: func() { traceCalls.Add(1) },
				Destroy:       func() { destroys.Add(1) },
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return graph
	}
	owner := newGraph("owner", 2, 1)
	first := newGraph("first", 0, 0)
	second := newGraph("second", 0, 0)
	weak := newGraph("weak", 0, 0)
	if err := owner.SetStrong(0, first); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetStrong(1, second); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetWeak(0, weak); err != nil {
		t.Fatal(err)
	}
	marker, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	markerValue, _ := scope.Int32(42)
	if ok, err := marker.SetByName(scope, ctx, "marker", markerValue); err != nil || !ok {
		t.Fatalf("set marker = %v, %v", ok, err)
	}
	if err := owner.SetTraced(scope, marker.Value); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := weak.Close(); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := owner.Strong(0); err != nil || !ok || got.State.Label != "first" {
		t.Fatalf("first edge = %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := owner.Strong(1); err != nil || !ok || got.State.Label != "second" {
		t.Fatalf("second edge = %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := owner.Weak(0); err != nil || !ok || got.State.Label != "weak" {
		t.Fatalf("weak edge before collection = %+v, %v, %v", got, ok, err)
	}
	traced, ok, err := owner.Traced(scope)
	if err != nil || !ok {
		t.Fatalf("Traced = %v, %v", ok, err)
	}
	tracedObject, err := gov8.AsObject(traced)
	if err != nil {
		t.Fatal(err)
	}
	value, ok, err := tracedObject.GetByName(scope, ctx, "marker")
	if err != nil || !ok {
		t.Fatalf("traced marker = %v, %v", ok, err)
	}
	integer, ok, err := value.IntegerValue(ctx)
	if err != nil || !ok || integer != 42 {
		t.Fatalf("traced marker value = %d, %v, %v", integer, ok, err)
	}
	if err := owner.SetTraced(scope, gov8.Value{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := owner.Traced(scope); err != nil || ok {
		t.Fatalf("cleared traced value = %v, %v", ok, err)
	}
	if err := owner.SetTraced(scope, marker.Value); err != nil {
		t.Fatal(err)
	}
	var snapshot []byte
	if err := iso.TakeHeapSnapshot(func(chunk []byte) bool {
		snapshot = append(snapshot, chunk...)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot), "CppGCGenericGraph-owner") || nameCalls.Load() == 0 || traceCalls.Load() == 0 {
		t.Fatalf("snapshot callbacks missing, names=%d traces=%d", nameCalls.Load(), traceCalls.Load())
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCppGCGenericGraphValidationAffinityAndRace(t *testing.T) {
	isoA, isoB, _, _, scopeA, scopeB := twoIsolates(t)
	clone := cloneGraphTestState
	if _, err := gov8.NewCppGCGenericGraph(isoA, scopeA, gov8.CppGCGenericGraphOptions[graphTestState]{}); err == nil || !strings.Contains(err.Error(), "Clone") {
		t.Fatalf("nil clone error = %v", err)
	}
	if _, err := gov8.NewCppGCGenericGraph(isoA, scopeA, gov8.CppGCGenericGraphOptions[graphTestState]{
		Name: "bad\x00name", Callbacks: gov8.CppGCGenericGraphCallbacks[graphTestState]{Clone: clone},
	}); err == nil {
		t.Fatal("NUL name accepted")
	}
	inner, err := isoA.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := gov8.NewCppGCGenericGraph(isoA, inner, gov8.CppGCGenericGraphOptions[graphTestState]{
		Callbacks: gov8.CppGCGenericGraphCallbacks[graphTestState]{Clone: clone},
	}); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed scope error = %v", err)
	}
	foreignValue, err := scopeB.Int32(42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gov8.NewCppGCGenericGraph(isoA, scopeA, gov8.CppGCGenericGraphOptions[graphTestState]{
		Traced: foreignValue, Callbacks: gov8.CppGCGenericGraphCallbacks[graphTestState]{Clone: clone},
	}); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign initial traced value error = %v", err)
	}
	owner, err := gov8.NewCppGCGenericGraph(isoA, scopeA, gov8.CppGCGenericGraphOptions[graphTestState]{
		Name: "owner", StrongSlots: 2, WeakSlots: 1,
		Callbacks: gov8.CppGCGenericGraphCallbacks[graphTestState]{Clone: clone},
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := gov8.NewCppGCGenericGraph(isoB, scopeB, gov8.CppGCGenericGraphOptions[graphTestState]{
		Name: "foreign", Callbacks: gov8.CppGCGenericGraphCallbacks[graphTestState]{Clone: clone},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	if err := owner.SetStrong(0, foreign); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign edge error = %v", err)
	}
	if err := owner.SetStrong(2, owner); err == nil || !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("strong bounds error = %v", err)
	}
	if _, _, err := owner.Weak(1); err == nil || !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("weak bounds error = %v", err)
	}
	if err := owner.ClearStrong(2); err == nil || !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("clear bounds error = %v", err)
	}
	if err := owner.SetWeak(0, nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil child error = %v", err)
	}
	if err := owner.SetStrong(0, owner); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := owner.Strong(0); err != nil || !ok || got.State.Label != "" {
		t.Fatalf("self edge = %+v, %v, %v", got, ok, err)
	}
	if err := owner.ClearStrong(0); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := owner.Strong(0); err != nil || ok {
		t.Fatalf("cleared strong edge = %v, %v", ok, err)
	}
	if err := owner.SetTraced(scopeB, foreignValue); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign traced scope error = %v", err)
	}
	if _, _, err := owner.Traced(scopeB); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign traced read error = %v", err)
	}
	wrongThread := make(chan error, 1)
	go func() { _, err := owner.State(); wrongThread <- err }()
	if err := <-wrongThread; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong-thread State error = %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = owner.State()
			_ = owner.Close()
		}()
	}
	wg.Wait()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("idempotent Close = %v", err)
	}
	if _, err := owner.State(); err == nil {
		t.Fatal("closed graph remained readable")
	}
}

func TestCppGCGenericGraphFailedCloneAndDropReentrancy(t *testing.T) {
	iso, _, scope := newTestRuntime(t)
	var graph *gov8.CppGCGenericGraph[graphTestState]
	var reentrantErr error
	var probed atomic.Bool
	clone := func(value graphTestState) (graphTestState, error) {
		if value.Label == "reject" {
			return graphTestState{}, errors.New("clone rejected")
		}
		return cloneGraphTestState(value)
	}
	created, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[graphTestState]{
		State: graphTestState{Label: "initial"},
		Callbacks: gov8.CppGCGenericGraphCallbacks[graphTestState]{
			Clone: clone,
			Drop: func(graphTestState) {
				if probed.CompareAndSwap(false, true) {
					_, reentrantErr = graph.State()
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph = created
	if err := graph.ReplaceState(graphTestState{Label: "reject"}); err == nil || !strings.Contains(err.Error(), "clone rejected") {
		t.Fatalf("failed clone error = %v", err)
	}
	state, err := graph.State()
	if err != nil || state.Label != "initial" {
		t.Fatalf("state after failed clone = %+v, %v", state, err)
	}
	if err := graph.ReplaceState(graphTestState{Label: "replacement"}); err != nil {
		t.Fatal(err)
	}
	if reentrantErr == nil || !strings.Contains(reentrantErr.Error(), "active operation") {
		t.Fatalf("reentrant state error = %v", reentrantErr)
	}
	if err := graph.Close(); err != nil {
		t.Fatal(err)
	}
}
