//go:build windows && amd64

package conformance_cppgc_generic_residual

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

type dropLog struct {
	sync.Mutex
	values []int32
}

func (d *dropLog) add(value int32) {
	d.Lock()
	d.values = append(d.values, value)
	d.Unlock()
}

func (d *dropLog) copy() []int32 {
	d.Lock()
	defer d.Unlock()
	return append([]int32(nil), d.values...)
}

func collect(t *testing.T, iso *gov8.Isolate) {
	t.Helper()
	if err := iso.CollectCppGCGarbageForTesting(gov8.CppGCStackNoHeapPointers); err != nil {
		t.Fatal(err)
	}
}

func generic(t *testing.T, iso *gov8.Isolate, options gov8.CppGCGenericOptions) *gov8.CppGCGenericObject {
	t.Helper()
	object, err := iso.NewCppGCGenericObject(options)
	if err != nil {
		t.Fatal(err)
	}
	return object
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
			t.Errorf("isolate close: %v", err)
		}
	}()
	var lines []string

	cellDrops := &dropLog{}
	cellOwner := generic(t, iso, gov8.CppGCGenericOptions{
		ObjectID: 10, Cell: 10, Name: "CppGCGenericResidualCellOwner", Alignment: 1,
		Callbacks: gov8.CppGCGenericCallbacks{CellDropped: cellDrops.add},
	})
	initial, err := cellOwner.Cell()
	if err != nil {
		t.Fatal(err)
	}
	if err := cellOwner.SetCell(20); err != nil {
		t.Fatal(err)
	}
	afterSet, err := cellOwner.Cell()
	if err != nil {
		t.Fatal(err)
	}
	dropsAfterSet := cellDrops.copy()
	lines = append(lines, line(t, "cppgc-generic-residual/gc-cell/new_get_set_drop", struct {
		Initial                      int32   `json:"initial"`
		AfterSet                     int32   `json:"after_set"`
		ReplacedDroppedSynchronously bool    `json:"replaced_value_dropped_synchronously"`
		DropsAfterSet                []int32 `json:"drops_after_set"`
	}{initial, afterSet, reflect.DeepEqual(dropsAfterSet, []int32{10}), dropsAfterSet}))

	getMutValue, err := cellOwner.UpdateCell(1)
	if err != nil {
		t.Fatal(err)
	}
	withValue, err := cellOwner.UpdateCell(9)
	if err != nil {
		t.Fatal(err)
	}
	finalValue, err := cellOwner.Cell()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := cellOwner.Layout()
	if err != nil {
		t.Fatal(err)
	}
	lines = append(lines, line(t, "cppgc-generic-residual/gc-cell/get-mut_with", struct {
		GetMutValue     int32 `json:"get_mut_value"`
		WithValue       int32 `json:"with_value"`
		FinalValue      int32 `json:"final_value"`
		SameCellStorage bool  `json:"same_cell_storage"`
	}{getMutValue, withValue, finalValue, layout.CellStorageStable}))
	if err := cellOwner.Close(); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	afterOwnerCollection := cellDrops.copy()
	collect(t, iso)
	lines = append(lines, line(t, "cppgc-generic-residual/gc-cell/lifecycle", struct {
		DropsAfterOwnerCollection []int32 `json:"drops_after_owner_collection"`
		CurrentValueDroppedOnce   bool    `json:"current_value_dropped_once"`
		RepeatDoesNotRedestroy    bool    `json:"repeat_gc_does_not_redestroy"`
	}{afterOwnerCollection, reflect.DeepEqual(afterOwnerCollection, []int32{10, 30}), reflect.DeepEqual(cellDrops.copy(), afterOwnerCollection)}))

	childDrops := &dropLog{}
	optionalOwner := generic(t, iso, gov8.CppGCGenericOptions{ObjectID: 100, Name: "CppGCGenericResidualOptionalOwner", Alignment: 1})
	defer optionalOwner.Close()
	_, initiallyPresent, err := optionalOwner.OptionalMember()
	if err != nil {
		t.Fatal(err)
	}
	makeChild := func(id int32) (*gov8.CppGCGenericObject, *gov8.CppGCWeakPersistent) {
		child := generic(t, iso, gov8.CppGCGenericOptions{
			ObjectID: id, Cell: id, Name: "CppGCGenericResidualChild", Alignment: 1,
			Callbacks: gov8.CppGCGenericCallbacks{Destroy: func() { childDrops.add(id) }},
		})
		weak, err := child.NewWeakPersistent()
		if err != nil {
			t.Fatal(err)
		}
		return child, weak
	}
	first, firstWeak := makeChild(1)
	defer firstWeak.Close()
	if err := optionalOwner.SetOptionalMember(first); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	firstSnapshot, firstSurvives, err := firstWeak.Get()
	if err != nil {
		t.Fatal(err)
	}
	memberSnapshot, memberPresent, err := optionalOwner.OptionalMember()
	if err != nil {
		t.Fatal(err)
	}
	firstSurvives = firstSurvives && firstSnapshot.ObjectID == 1 && memberPresent && memberSnapshot.ObjectID == 1
	beforeReplace := childDrops.copy()
	second, secondWeak := makeChild(2)
	defer secondWeak.Close()
	if err := optionalOwner.SetOptionalMember(second); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	replacementNotSynchronous := reflect.DeepEqual(childDrops.copy(), beforeReplace)
	collect(t, iso)
	_, firstStillPresent, err := firstWeak.Get()
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot, secondSurvives, err := secondWeak.Get()
	if err != nil {
		t.Fatal(err)
	}
	memberSnapshot, memberPresent, err = optionalOwner.OptionalMember()
	if err != nil {
		t.Fatal(err)
	}
	secondSurvives = secondSurvives && secondSnapshot.ObjectID == 2 && memberPresent && memberSnapshot.ObjectID == 2
	dropsAfterReplace := childDrops.copy()
	lines = append(lines, line(t, "cppgc-generic-residual/member/replacement_barrier", struct {
		InitiallyNone              bool    `json:"initially_none"`
		FirstSurvives              bool    `json:"first_survives_traced_some"`
		ReplacementNotSynchronous  bool    `json:"replacement_not_synchronous"`
		OldChildCleared            bool    `json:"old_child_cleared_after_gc"`
		ReplacementSurvivesBarrier bool    `json:"replacement_survives_write_barrier"`
		DropsAfterReplace          []int32 `json:"drops_after_replace"`
	}{!initiallyPresent, firstSurvives, replacementNotSynchronous, !firstStillPresent, secondSurvives, dropsAfterReplace}))

	if err := optionalOwner.ClearOptionalMember(); err != nil {
		t.Fatal(err)
	}
	_, visiblePresent, err := optionalOwner.OptionalMember()
	if err != nil {
		t.Fatal(err)
	}
	beforeNoneGC := childDrops.copy()
	collect(t, iso)
	afterNoneGC := childDrops.copy()
	_, secondStillPresent, err := secondWeak.Get()
	if err != nil {
		t.Fatal(err)
	}
	lines = append(lines, line(t, "cppgc-generic-residual/option-member/some_none_trace", struct {
		NoneVisibleImmediately       bool    `json:"none_visible_immediately"`
		NoneAssignmentNotSynchronous bool    `json:"none_assignment_not_synchronous"`
		FormerSomeCleared            bool    `json:"former_some_cleared"`
		DropsAfterNoneGC             []int32 `json:"drops_after_none_gc"`
		BothChildrenDroppedOnce      bool    `json:"both_children_dropped_once"`
	}{!visiblePresent, reflect.DeepEqual(beforeNoneGC, []int32{1}), !secondStillPresent, afterNoneGC, reflect.DeepEqual(afterNoneGC, []int32{1, 2})}))

	var nameCalls, namedDrops atomic.Int32
	named := generic(t, iso, gov8.CppGCGenericOptions{
		Name: "CppGCGenericResidualNamedObject", Alignment: 1,
		Callbacks: gov8.CppGCGenericCallbacks{
			NameObserved: func() { nameCalls.Add(1) },
			Destroy:      func() { namedDrops.Add(1) },
		},
	})
	var snapshot []byte
	if err := iso.TakeHeapSnapshot(func(chunk []byte) bool {
		snapshot = append(snapshot, chunk...)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	namedWeak, err := named.NewWeakPersistent()
	if err != nil {
		t.Fatal(err)
	}
	_, namedAlive, err := namedWeak.Get()
	if err != nil {
		t.Fatal(err)
	}
	if err := named.Close(); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	if err := namedWeak.Close(); err != nil {
		t.Fatal(err)
	}
	lines = append(lines, line(t, "cppgc-generic-residual/name/heap_snapshot", struct {
		SnapshotContainsName     bool `json:"snapshot_contains_custom_name"`
		GetNameCalled            bool `json:"get_name_called"`
		GetNameCallCountPositive bool `json:"get_name_call_count_positive"`
		NamedAliveDuringSnapshot bool `json:"named_alive_during_snapshot"`
		NamedDroppedAfterRelease bool `json:"named_dropped_after_root_release"`
	}{bytes.Contains(snapshot, []byte("CppGCGenericResidualNamedObject")), nameCalls.Load() > 0, nameCalls.Load() > 0, namedAlive, namedDrops.Load() == 1}))

	var zeroDrops, alignedDrops atomic.Int32
	zero := generic(t, iso, gov8.CppGCGenericOptions{Name: "zero", Alignment: 1, Callbacks: gov8.CppGCGenericCallbacks{Destroy: func() { zeroDrops.Add(1) }}})
	aligned := generic(t, iso, gov8.CppGCGenericOptions{Name: "aligned", Size: 16, Alignment: 16, Cell: 7, Callbacks: gov8.CppGCGenericCallbacks{Destroy: func() { alignedDrops.Add(1) }}})
	zeroLayout, err := zero.Layout()
	if err != nil {
		t.Fatal(err)
	}
	alignedLayout, err := aligned.Layout()
	if err != nil {
		t.Fatal(err)
	}
	marker, err := aligned.Cell()
	if err != nil {
		t.Fatal(err)
	}
	beforeReleaseZero, beforeReleaseAligned := zeroDrops.Load(), alignedDrops.Load()
	if err := zero.Close(); err != nil {
		t.Fatal(err)
	}
	if err := aligned.Close(); err != nil {
		t.Fatal(err)
	}
	collect(t, iso)
	afterReleaseZero, afterReleaseAligned := zeroDrops.Load(), alignedDrops.Load()
	collect(t, iso)
	lines = append(lines, line(t, "cppgc-generic-residual/layout/zero_align16_destruction", struct {
		ZeroSize               uint32 `json:"zero_size"`
		ZeroAlignment          uint32 `json:"zero_alignment"`
		AlignedSize            uint32 `json:"aligned_size"`
		AlignedAlignment       uint32 `json:"aligned_alignment"`
		AlignedAddress         bool   `json:"aligned_address"`
		AlignedMarker          int32  `json:"aligned_marker"`
		NoDropWhileRooted      bool   `json:"no_drop_while_rooted"`
		BothDroppedOnce        bool   `json:"both_dropped_once"`
		RepeatDoesNotRedestroy bool   `json:"repeat_gc_does_not_redestroy"`
	}{zeroLayout.Size, zeroLayout.Alignment, alignedLayout.Size, alignedLayout.Alignment, alignedLayout.AddressAligned, marker,
		beforeReleaseZero == 0 && beforeReleaseAligned == 0, afterReleaseZero == 1 && afterReleaseAligned == 1,
		zeroDrops.Load() == afterReleaseZero && alignedDrops.Load() == afterReleaseAligned}))

	return lines
}

func fixture(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-cppgc-generic-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestConformanceCppGCGenericResidual(t *testing.T) {
	got := produced(t)
	got = append(got, `{"summary":{"total":7,"passed":7,"failed":0}}`)
	want := fixture(t)
	if !reflect.DeepEqual(got, want) {
		for i := range got {
			if i >= len(want) || got[i] != want[i] {
				t.Errorf("line %d\n got: %s\nwant: %s", i+1, got[i], func() string {
					if i < len(want) {
						return want[i]
					}
					return "<missing>"
				}())
			}
		}
		if len(got) != len(want) {
			t.Errorf("line count got %d want %d", len(got), len(want))
		}
	}
}
