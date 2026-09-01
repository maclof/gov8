//go:build windows && amd64

package gov8

import (
	"math"
	"syscall"
	"testing"
	"unsafe"
)

func legacyNumberValueForTest(value Value, context *Context) (float64, bool, uintptr) {
	var out float64
	var ok int32
	status, _, _ := proc("gov8_value_number_value").Call(
		value.iso.handle, context.handle, value.h,
		uintptr(unsafe.Pointer(&out)), uintptr(unsafe.Pointer(&ok)))
	return out, ok == 1, status
}

func TestNumberValueDirectLegacyDifferentialAndStatuses(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = iso.Close() })
	context, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = context.Close() })
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scope.Close() })

	for _, input := range []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN()} {
		value, err := scope.Number(input)
		if err != nil {
			t.Fatal(err)
		}
		direct, directOK, directErr := value.NumberValue(context)
		legacy, legacyOK, legacyStatus := legacyNumberValueForTest(value, context)
		if directErr != nil || int64(legacyStatus) < 0 || directOK != legacyOK {
			t.Fatalf("input %v: direct=(%v,%v,%v) legacy=(%v,%v,%d)", input, direct, directOK, directErr, legacy, legacyOK, int64(legacyStatus))
		}
		if math.Float64bits(direct) != math.Float64bits(legacy) {
			t.Fatalf("input %v: direct bits=%#x legacy bits=%#x", input, math.Float64bits(direct), math.Float64bits(legacy))
		}
	}

	numberValueDirectOnce.Do(resolveNumberValueDirect)
	status, _, _ := syscall.Syscall(numberValueDirectAddr, 3, 0, 0, 0)
	if int64(status) != errBadArg {
		t.Fatalf("direct null status = %d, want %d", int64(status), errBadArg)
	}

	other, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	otherContext, err := other.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherContext.Close() })
	value, err := scope.Number(1)
	if err != nil {
		t.Fatal(err)
	}
	status, _, _ = syscall.Syscall(numberValueDirectAddr, 3,
		iso.handleAssumingCheck(), otherContext.handle, value.h)
	if int64(status) != errBadArg {
		t.Fatalf("direct cross-isolate context status = %d, want %d", int64(status), errBadArg)
	}
}

func TestShimABIVersion42(t *testing.T) {
	version, _, _ := proc("gov8_abi_version").Call()
	if version != shimABIVersion {
		t.Fatalf("shim ABI = %d, Go expects %d", version, shimABIVersion)
	}
	if shimABIVersion != 42 {
		t.Fatalf("Go shim ABI = %d, want 42", shimABIVersion)
	}
}
