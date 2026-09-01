//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

type graphBenchState struct {
	Revision int
	Payload  []byte
}

func cloneGraphBenchState(value graphBenchState) (graphBenchState, error) {
	value.Payload = append([]byte(nil), value.Payload...)
	return value, nil
}

func newCppGCGraphBenchmarkRuntime(b *testing.B) (*gov8.Isolate, *gov8.Scope) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := scope.Close(); err != nil {
			b.Errorf("scope.Close: %v", err)
		}
		if err := ctx.Close(); err != nil {
			b.Errorf("context.Close: %v", err)
		}
		if err := iso.Close(); err != nil {
			b.Errorf("isolate.Close: %v", err)
		}
	})
	return iso, scope
}

func BenchmarkCppGCGenericGraphStateUpdate(b *testing.B) {
	iso, scope := newCppGCGraphBenchmarkRuntime(b)
	graph, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[graphBenchState]{
		State: graphBenchState{Payload: make([]byte, 64)}, Name: "CppGCGenericGraphBenchmark",
		Callbacks: gov8.CppGCGenericGraphCallbacks[graphBenchState]{Clone: cloneGraphBenchState},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := graph.Close(); err != nil {
			b.Errorf("graph.Close: %v", err)
		}
	})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := graph.UpdateState(func(value *graphBenchState) error {
			value.Revision++
			value.Payload[0]++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCppGCGenericGraphEdgeMutation(b *testing.B) {
	iso, scope := newCppGCGraphBenchmarkRuntime(b)
	callbacks := gov8.CppGCGenericGraphCallbacks[int]{Clone: func(value int) (int, error) { return value, nil }}
	owner, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[int]{
		Name: "CppGCGenericGraphBenchmarkOwner", StrongSlots: 1, Callbacks: callbacks,
	})
	if err != nil {
		b.Fatal(err)
	}
	first, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[int]{State: 1, Callbacks: callbacks})
	if err != nil {
		b.Fatal(err)
	}
	second, err := gov8.NewCppGCGenericGraph(iso, scope, gov8.CppGCGenericGraphOptions[int]{State: 2, Callbacks: callbacks})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		for _, graph := range []*gov8.CppGCGenericGraph[int]{owner, first, second} {
			if err := graph.Close(); err != nil {
				b.Errorf("graph.Close: %v", err)
			}
		}
	})
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		child := first
		if index&1 != 0 {
			child = second
		}
		if err := owner.SetStrong(0, child); err != nil {
			b.Fatal(err)
		}
	}
}
