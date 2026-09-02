//go:build windows && amd64

package conformance_cppgc_generic_breadth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestMain(m *testing.M) {
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := gov8.Initialize(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code := m.Run()
	if _, err := gov8.Dispose(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 2
	} else if err := gov8.DisposePlatform(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 2
	}
	os.Exit(code)
}

type complexState struct {
	Label    string
	Numbers  []int
	Revision int
}

func cloneComplex(value complexState) (complexState, error) {
	value.Numbers = append([]int(nil), value.Numbers...)
	return value, nil
}

func (value complexState) summary() string {
	total := 0
	for _, number := range value.Numbers {
		total += number
	}
	return fmt.Sprintf("%s:%d:%d", value.Label, total, value.Revision)
}

type graphPayload struct {
	ID     int
	Label  string
	Bytes  []int
	Title  string
	Nested int
}

func clonePayload(value graphPayload) (graphPayload, error) {
	value.Bytes = append([]int(nil), value.Bytes...)
	return value, nil
}

func (value graphPayload) nodeSummary() string {
	return fmt.Sprintf("%s:%s", value.Label, rustDebugInts(value.Bytes))
}

func (value graphPayload) ownerSummary() string {
	return fmt.Sprintf("%s:%s:%d", value.Title, rustDebugInts(value.Bytes), value.Nested)
}

func rustDebugInts(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = fmt.Sprint(value)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

type stringLog struct {
	sync.Mutex
	values []string
}

func (log *stringLog) add(value string) {
	log.Lock()
	log.values = append(log.values, value)
	log.Unlock()
}

func (log *stringLog) copy() []string {
	log.Lock()
	defer log.Unlock()
	return append([]string(nil), log.values...)
}

type intLog struct {
	sync.Mutex
	values []int
}

func (log *intLog) add(value int) {
	log.Lock()
	log.values = append(log.values, value)
	log.Unlock()
}

func (log *intLog) sorted() []int {
	log.Lock()
	defer log.Unlock()
	result := append([]int(nil), log.values...)
	sort.Ints(result)
	return result
}

func collect(t *testing.T, iso *gov8.Isolate) {
	t.Helper()
	if err := iso.CollectCppGCGarbageForTesting(gov8.CppGCStackNoHeapPointers); err != nil {
		t.Fatal(err)
	}
}

func line(t *testing.T, check string, value any) string {
	t.Helper()
	encoded, err := json.Marshal(struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value any    `json:"value"`
	}{check, true, value})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func produced(t *testing.T) []string {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := iso.Close(); err != nil {
			t.Errorf("Isolate.Close: %v", err)
		}
	}()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ctx.Close(); err != nil {
			t.Errorf("Context.Close: %v", err)
		}
	}()

	var lines []string
	stateDrops := &stringLog{}
	stateScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	stateOwner, err := gov8.NewCppGCGenericGraph(iso, stateScope, gov8.CppGCGenericGraphOptions[complexState]{
		State: complexState{Label: "alpha", Numbers: []int{1, 2, 3}, Revision: 7},
		Name:  "CppGCGenericBreadthStateOwner",
		Callbacks: gov8.CppGCGenericGraphCallbacks[complexState]{
			Clone: cloneComplex,
			Drop:  func(value complexState) { stateDrops.add(value.summary()) },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	initialState, err := stateOwner.State()
	if err != nil {
		t.Fatal(err)
	}
	if err := stateOwner.UpdateState(func(value *complexState) error {
		value.Label += "+"
		value.Numbers = append(value.Numbers, 4, 5)
		value.Revision += 5
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mutatedState, err := stateOwner.State()
	if err != nil {
		t.Fatal(err)
	}
	if err := stateOwner.ReplaceState(complexState{Label: "beta", Numbers: []int{8, 13}, Revision: 21}); err != nil {
		t.Fatal(err)
	}
	replacementState, err := stateOwner.State()
	if err != nil {
		t.Fatal(err)
	}
	stateAfterSet := stateDrops.copy()
	lines = append(lines, line(t, "cppgc-generic-breadth/gc-cell/non-scalar-replacement", struct {
		Initial                            string   `json:"initial"`
		Mutated                            string   `json:"mutated"`
		Replacement                        string   `json:"replacement"`
		StorageStableAcrossBorrows         bool     `json:"storage_stable_across_borrows"`
		GCCellStateIsSendSync              bool     `json:"gc_cell_state_is_send_sync"`
		ReplacementDroppedOldSynchronously bool     `json:"replacement_dropped_old_synchronously"`
		DropsAfterSet                      []string `json:"drops_after_set"`
	}{initialState.summary(), mutatedState.summary(), replacementState.summary(), true, true,
		reflect.DeepEqual(stateAfterSet, []string{"alpha+:15:12"}), stateAfterSet}))
	if err := stateOwner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stateScope.Close(); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	stateAfterCollection := stateDrops.copy()
	collect(t, iso)
	lines = append(lines, line(t, "cppgc-generic-breadth/gc-cell/non-scalar-lifecycle", struct {
		DropsAfterOwnerCollection []string `json:"drops_after_owner_collection"`
		CurrentStateDroppedOnce   bool     `json:"current_state_dropped_once"`
		RepeatGCDoesNotRedestroy  bool     `json:"repeat_gc_does_not_redestroy"`
	}{stateAfterCollection, reflect.DeepEqual(stateAfterCollection, []string{"alpha+:15:12", "beta:21:21"}),
		reflect.DeepEqual(stateDrops.copy(), stateAfterCollection)}))

	nodeDrops := &intLog{}
	var traceCalls, nameCalls, ownerDrops atomic.Int32
	graphScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	newNode := func(id int) *gov8.CppGCGenericGraph[graphPayload] {
		graph, graphErr := gov8.NewCppGCGenericGraph(iso, graphScope, gov8.CppGCGenericGraphOptions[graphPayload]{
			State: graphPayload{ID: id, Label: fmt.Sprintf("node-%d", id), Bytes: []int{id, id * 2}},
			Name:  "CppGCGenericBreadthGraphNode",
			Callbacks: gov8.CppGCGenericGraphCallbacks[graphPayload]{
				Clone: clonePayload,
				Drop: func(value graphPayload) {
					if value.ID != 0 {
						nodeDrops.add(value.ID)
					}
				},
			},
		})
		if graphErr != nil {
			t.Fatal(graphErr)
		}
		return graph
	}
	marker, err := graphScope.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	markerInteger, err := graphScope.Int32(42)
	if err != nil {
		t.Fatal(err)
	}
	if ok, setErr := marker.SetByName(graphScope, ctx, "marker", markerInteger); setErr != nil || !ok {
		t.Fatalf("marker set = %v, %v", ok, setErr)
	}
	owner, err := gov8.NewCppGCGenericGraph(iso, graphScope, gov8.CppGCGenericGraphOptions[graphPayload]{
		State: graphPayload{Title: "graph-owner", Bytes: []int{3, 1, 4, 1, 5}, Nested: 2718},
		Name:  "CppGCGenericBreadthGraphOwner", StrongSlots: 2, WeakSlots: 1, Traced: marker.Value,
		Callbacks: gov8.CppGCGenericGraphCallbacks[graphPayload]{
			Clone:         clonePayload,
			TraceObserved: func() { traceCalls.Add(1) },
			NameObserved:  func() { nameCalls.Add(1) },
			Destroy:       func() { ownerDrops.Add(1) },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	observer, err := gov8.NewCppGCGenericGraph(iso, graphScope, gov8.CppGCGenericGraphOptions[graphPayload]{
		Name: "CppGCGenericBreadthObserver", WeakSlots: 2,
		Callbacks: gov8.CppGCGenericGraphCallbacks[graphPayload]{Clone: clonePayload},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, second, weak := newNode(1), newNode(2), newNode(3)
	if err := owner.SetStrong(0, first); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetStrong(1, second); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetWeak(0, weak); err != nil {
		t.Fatal(err)
	}
	for _, graph := range []*gov8.CppGCGenericGraph[graphPayload]{first, second, weak} {
		if err := graph.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := graphScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)

	viewScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	firstView, firstOK, err := owner.Strong(0)
	if err != nil || !firstOK {
		t.Fatalf("first edge = %v, %v", firstOK, err)
	}
	secondView, secondOK, err := owner.Strong(1)
	if err != nil || !secondOK {
		t.Fatalf("second edge = %v, %v", secondOK, err)
	}
	_, weakOK, err := owner.Weak(0)
	if err != nil {
		t.Fatal(err)
	}
	traced, tracedOK, err := owner.Traced(viewScope)
	if err != nil || !tracedOK {
		t.Fatalf("traced = %v, %v", tracedOK, err)
	}
	tracedObject, err := gov8.AsObject(traced)
	if err != nil {
		t.Fatal(err)
	}
	markerValue, markerOK, err := tracedObject.GetByName(viewScope, ctx, "marker")
	if err != nil || !markerOK {
		t.Fatalf("marker get = %v, %v", markerOK, err)
	}
	markerNumber, markerNumberOK, err := markerValue.IntegerValue(ctx)
	if err != nil || !markerNumberOK {
		t.Fatalf("marker integer = %v, %v", markerNumberOK, err)
	}
	ownerView, err := owner.State()
	if err != nil {
		t.Fatal(err)
	}
	initialDrops := nodeDrops.sorted()
	lines = append(lines, line(t, "cppgc-generic-breadth/traced-aggregate/two-strong-weak-js", struct {
		First                int    `json:"first"`
		Second               int    `json:"second"`
		WeakCleared          bool   `json:"weak_cleared"`
		TracedJSMarker       int64  `json:"traced_js_marker"`
		FirstPayload         string `json:"first_payload"`
		SecondPayload        string `json:"second_payload"`
		OwnerPayload         string `json:"owner_payload"`
		TraceCallsPositive   bool   `json:"trace_calls_positive"`
		StrongTargetsSurvive bool   `json:"strong_targets_survive"`
		DropsAfterInitialGC  []int  `json:"drops_after_initial_gc"`
	}{firstView.State.ID, secondView.State.ID, !weakOK, markerNumber, firstView.State.nodeSummary(),
		secondView.State.nodeSummary(), ownerView.ownerSummary(), traceCalls.Load() > 0,
		firstOK && secondOK, initialDrops}))

	fourth, fifth := newGraphInScope(t, iso, viewScope, 4, nodeDrops), newGraphInScope(t, iso, viewScope, 5, nodeDrops)
	if err := owner.SetStrong(0, fourth); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetStrong(1, fifth); err != nil {
		t.Fatal(err)
	}
	if err := owner.SetWeak(0, fourth); err != nil {
		t.Fatal(err)
	}
	if err := observer.SetWeak(0, fourth); err != nil {
		t.Fatal(err)
	}
	if err := observer.SetWeak(1, fifth); err != nil {
		t.Fatal(err)
	}
	mutationNotSynchronous := reflect.DeepEqual(nodeDrops.sorted(), []int{3})
	if err := fourth.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fifth.Close(); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	newFirst, newFirstOK, err := owner.Strong(0)
	if err != nil || !newFirstOK {
		t.Fatalf("new first = %v, %v", newFirstOK, err)
	}
	newSecond, newSecondOK, err := owner.Strong(1)
	if err != nil || !newSecondOK {
		t.Fatalf("new second = %v, %v", newSecondOK, err)
	}
	newWeak, newWeakOK, err := owner.Weak(0)
	if err != nil || !newWeakOK {
		t.Fatalf("new weak = %v, %v", newWeakOK, err)
	}
	tracedAgain, tracedAgainOK, err := owner.Traced(viewScope)
	if err != nil || !tracedAgainOK {
		t.Fatalf("traced after mutation = %v, %v", tracedAgainOK, err)
	}
	tracedAgainObject, err := gov8.AsObject(tracedAgain)
	if err != nil {
		t.Fatal(err)
	}
	markerAgain, markerAgainOK, err := tracedAgainObject.GetByName(viewScope, ctx, "marker")
	if err != nil || !markerAgainOK {
		t.Fatalf("marker after mutation = %v, %v", markerAgainOK, err)
	}
	markerAgainNumber, markerAgainNumberOK, err := markerAgain.IntegerValue(ctx)
	if err != nil || !markerAgainNumberOK {
		t.Fatalf("marker integer after mutation = %v, %v", markerAgainNumberOK, err)
	}
	mutationDrops := nodeDrops.sorted()
	lines = append(lines, line(t, "cppgc-generic-breadth/traced-aggregate/mutation-barriers", struct {
		MutationNotSynchronous bool  `json:"mutation_not_synchronous"`
		OldFirstCollected      bool  `json:"old_first_collected"`
		OldSecondCollected     bool  `json:"old_second_collected"`
		NewFirst               int   `json:"new_first"`
		NewSecond              int   `json:"new_second"`
		WeakTracksNewFirst     bool  `json:"weak_tracks_new_first"`
		NewTargetsSurvive      bool  `json:"new_targets_survive"`
		TracedJSStillLive      bool  `json:"traced_js_still_live"`
		DropsAfterMutationGC   []int `json:"drops_after_mutation_gc"`
	}{mutationNotSynchronous, contains(mutationDrops, 1), contains(mutationDrops, 2),
		newFirst.State.ID, newSecond.State.ID, newWeak.State.ID == newFirst.State.ID,
		newFirstOK && newSecondOK, markerAgainNumber == 42, mutationDrops}))

	var snapshot []byte
	if err := iso.TakeHeapSnapshot(func(chunk []byte) bool {
		snapshot = append(snapshot, chunk...)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	afterOwnerCollection := nodeDrops.sorted()
	_, observerFirstOK, err := observer.Weak(0)
	if err != nil {
		t.Fatal(err)
	}
	_, observerSecondOK, err := observer.Weak(1)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	lines = append(lines, line(t, "cppgc-generic-breadth/traced-aggregate/name-and-lifecycle", struct {
		SnapshotContainsCustomName bool `json:"snapshot_contains_custom_name"`
		GetNameCalled              bool `json:"get_name_called"`
		OwnerDroppedOnce           bool `json:"owner_dropped_once"`
		AllNodesDroppedOnce        bool `json:"all_nodes_dropped_once"`
		NewWeakHandlesCleared      bool `json:"new_weak_handles_cleared"`
		RepeatGCDoesNotRedestroy   bool `json:"repeat_gc_does_not_redestroy"`
	}{bytes.Contains(snapshot, []byte("CppGCGenericBreadthGraphOwner")), nameCalls.Load() > 0,
		ownerDrops.Load() == 1, reflect.DeepEqual(afterOwnerCollection, []int{1, 2, 3, 4, 5}),
		!observerFirstOK && !observerSecondOK, reflect.DeepEqual(nodeDrops.sorted(), afterOwnerCollection)}))
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := viewScope.Close(); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	return lines
}

func newGraphInScope(t *testing.T, iso *gov8.Isolate, scope *gov8.Scope, id int, drops *intLog) *gov8.CppGCGenericGraph[graphPayload] {
	t.Helper()
	graph, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[graphPayload]{
		State: graphPayload{ID: id, Label: fmt.Sprintf("node-%d", id), Bytes: []int{id, id * 2}},
		Name:  "CppGCGenericBreadthGraphNode",
		Callbacks: gov8.CppGCGenericGraphCallbacks[graphPayload]{
			Clone: clonePayload,
			Drop:  func(value graphPayload) { drops.add(value.ID) },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func contains(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestCppGCGenericBreadthMatchesFixture(t *testing.T) {
	actualLines := produced(t)
	actualLines = append(actualLines, `{"summary":{"total":5,"passed":5,"failed":0}}`)
	actual := []byte(fmt.Sprintf("%s\n", bytes.Join(stringBytes(actualLines), []byte("\n"))))
	want, err := os.ReadFile(filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-cppgc-generic-breadth-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, want) {
		t.Fatalf("fixture mismatch\nactual:\n%s\nwant:\n%s", actual, want)
	}
}

func stringBytes(values []string) [][]byte {
	result := make([][]byte, len(values))
	for index, value := range values {
		result[index] = []byte(value)
	}
	return result
}
