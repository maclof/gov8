//go:build windows && amd64

package gov8_test

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestCppGCGenericCellAndLifecycle(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	var mu sync.Mutex
	var dropped []int32
	object, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{
		ObjectID: 10, Cell: 10, Name: "generic-cell", Alignment: 1,
		Callbacks: gov8.CppGCGenericCallbacks{
			CellDropped: func(value int32) { mu.Lock(); dropped = append(dropped, value); mu.Unlock() },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := object.Cell(); err != nil || got != 10 {
		t.Fatalf("Cell = %d, %v", got, err)
	}
	if err := object.SetCell(20); err != nil {
		t.Fatal(err)
	}
	if got, err := object.UpdateCell(1); err != nil || got != 21 {
		t.Fatalf("first update = %d, %v", got, err)
	}
	if got, err := object.UpdateCell(9); err != nil || got != 30 {
		t.Fatalf("second update = %d, %v", got, err)
	}
	layout, err := object.Layout()
	if err != nil || !layout.AddressAligned || !layout.CellStorageStable {
		t.Fatalf("Layout = %+v, %v", layout, err)
	}
	mu.Lock()
	if len(dropped) != 1 || dropped[0] != 10 {
		t.Fatalf("replacement drops = %v", dropped)
	}
	mu.Unlock()
	if _, err := object.UpdateCell(math.MaxInt32); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("overflow error = %v", err)
	}
	if err := object.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := object.Cell(); err == nil {
		t.Fatal("closed generic object remained readable")
	}
}

func TestCppGCGenericOptionalMemberBarrier(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	owner, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{ObjectID: 100, Name: "owner", Alignment: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, ok, err := owner.OptionalMember(); err != nil || ok {
		t.Fatalf("initial member = %v, %v", ok, err)
	}
	var dropsMu sync.Mutex
	var drops []int32
	child := func(id int32) (*gov8.CppGCGenericObject, *gov8.CppGCWeakPersistent) {
		value, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{
			ObjectID: id, Cell: id, Name: "child", Alignment: 1,
			Callbacks: gov8.CppGCGenericCallbacks{Destroy: func() {
				dropsMu.Lock()
				drops = append(drops, id)
				dropsMu.Unlock()
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		weak, err := value.NewWeakPersistent()
		if err != nil {
			t.Fatal(err)
		}
		return value, weak
	}
	first, weakFirst := child(1)
	defer weakFirst.Close()
	if err := owner.SetOptionalMember(first); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := weakFirst.Get(); err != nil || !ok || got.ObjectID != 1 {
		t.Fatalf("first through traced member = %+v, %v, %v", got, ok, err)
	}
	second, weakSecond := child(2)
	defer weakSecond.Close()
	if err := owner.SetOptionalMember(second); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	dropsMu.Lock()
	if len(drops) != 0 {
		t.Fatalf("replacement destroyed synchronously: %v", drops)
	}
	dropsMu.Unlock()
	if got, ok, err := owner.OptionalMember(); err != nil || !ok || got.ObjectID != 2 {
		t.Fatalf("replacement member = %+v, %v, %v", got, ok, err)
	}
	if err := owner.ClearOptionalMember(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := owner.OptionalMember(); err != nil || ok {
		t.Fatalf("member after clear = %v, %v", ok, err)
	}
	if _, ok, err := weakSecond.Get(); err != nil || !ok {
		t.Fatalf("weak target was synchronously destroyed by clear: %v, %v", ok, err)
	}
}

func TestCppGCGenericNameLayoutAndValidation(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	var nameCalls atomic.Int32
	named, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{
		ObjectID: 1, Name: "CppGCGenericResidualNamedObject", Alignment: 16, Size: 16,
		Callbacks: gov8.CppGCGenericCallbacks{NameObserved: func() { nameCalls.Add(1) }},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer named.Close()
	layout, err := named.Layout()
	if err != nil || layout.Size != 16 || layout.Alignment != 16 || !layout.AddressAligned {
		t.Fatalf("layout = %+v, %v", layout, err)
	}
	var snapshot []byte
	if err := iso.TakeHeapSnapshot(func(chunk []byte) bool {
		snapshot = append(snapshot, chunk...)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot), "CppGCGenericResidualNamedObject") || nameCalls.Load() == 0 {
		t.Fatalf("custom name absent or unobserved: calls=%d", nameCalls.Load())
	}
	for _, options := range []gov8.CppGCGenericOptions{
		{Name: "x", Alignment: 32}, {Name: "x", Alignment: 3}, {Name: "bad\x00name", Alignment: 1},
	} {
		if _, err := iso.NewCppGCGenericObject(options); err == nil {
			t.Fatalf("invalid options accepted: %+v", options)
		}
	}
}

func TestCppGCGenericWrongThreadForeignAndClosed(t *testing.T) {
	iso, _, _ := newTestRuntime(t)
	object, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "owner", Alignment: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	foreign, err := other.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "foreign", Alignment: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	if err := object.SetOptionalMember(foreign); err == nil {
		t.Fatal("foreign member accepted")
	}
	done := make(chan error, 1)
	go func() { _, err := object.Cell(); done <- err }()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if err := object.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := object.Cell(); err == nil {
		t.Fatal("closed object remained readable")
	}
}
