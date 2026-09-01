//go:build windows && amd64

package gov8

import (
	"strings"
	"testing"
)

func TestScopeNewCloseLifecycleSemantics(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer iso.Close()

	outer, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	inner, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := outer.Close(); err == nil || !strings.Contains(err.Error(), "innermost") {
		t.Fatalf("out-of-order outer Close = %v", err)
	}

	wrongThread := make(chan error, 1)
	go func() { wrongThread <- inner.Close() }()
	if err := <-wrongThread; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread inner Close = %v", err)
	}
	if err := inner.Close(); err != nil {
		t.Fatalf("owner-thread inner Close: %v", err)
	}
	if err := inner.Close(); err == nil || !strings.Contains(err.Error(), "already closed") {
		t.Fatalf("double inner Close = %v", err)
	}
	if err := outer.Close(); err != nil {
		t.Fatalf("owner-thread outer Close: %v", err)
	}
}

func TestScopeNewCloseSteadyStateAllocations(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer iso.Close()

	allocations := testing.AllocsPerRun(1000, func() {
		scope, err := iso.NewScope()
		if err != nil {
			panic(err)
		}
		if err := scope.Close(); err != nil {
			panic(err)
		}
	})
	if allocations > 1 {
		t.Fatalf("steady-state NewScope+Close allocations = %v, want at most 1", allocations)
	}
}

func BenchmarkScopeNewClose(b *testing.B) {
	iso, err := NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	defer iso.Close()

	// Untimed lifecycle probe also warms the reusable Go scope stack.
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
