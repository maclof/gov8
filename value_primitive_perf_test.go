//go:build windows && amd64

package gov8

import "testing"

func TestPrimitiveConstructorsSteadyStateAllocations(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer iso.Close()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()

	// Warm export resolution before measuring the steady-state public path.
	if _, err := scope.Int32(0); err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		if _, err := scope.Int32(42); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("steady-state Int32 allocations = %v, want zero", allocations)
	}
	emptyAllocations := testing.AllocsPerRun(1000, func() {
		if _, err := scope.EmptyString(); err != nil {
			panic(err)
		}
	})
	if emptyAllocations != 0 {
		t.Fatalf("steady-state EmptyString allocations = %v, want zero", emptyAllocations)
	}
}

func BenchmarkPrimitiveEmptyString(b *testing.B) {
	iso, err := NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	defer iso.Close()
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	defer scope.Close()
	if _, err := scope.EmptyString(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := scope.EmptyString(); err != nil {
			b.Fatal(err)
		}
	}
}
