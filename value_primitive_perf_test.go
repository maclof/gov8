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
}
