//go:build windows && amd64

package gov8

import (
	"testing"
	"unsafe"
)

func TestHostCallbackFrameBorrowedScopeLayout(t *testing.T) {
	var frame hostCallbackFrame
	if got := unsafe.Offsetof(frame.scopeWire); got != 40 {
		t.Fatalf("scopeWire offset = %d, want 40", got)
	}
	if got := unsafe.Sizeof(frame); got != 160 {
		t.Fatalf("hostCallbackFrame size = %d, want 160", got)
	}
	if got := unsafe.Sizeof(ReturnValue{}); got != 16 {
		t.Fatalf("ReturnValue size = %d, want 16", got)
	}
}

func TestDeferredInt32CapabilityIsFunctionOnly(t *testing.T) {
	oldDLLFrame := &hostCallbackFrame{kind: cbKindFunction}
	if deferredInt32Capable(oldDLLFrame) {
		t.Fatal("unmarked old-DLL function frame enabled deferred return")
	}
	functionFrame := &hostCallbackFrame{
		kind:  cbKindFunction,
		pdPad: callbackDeferredRVInt32Magic,
	}
	if !deferredInt32Capable(functionFrame) {
		t.Fatal("marked function frame did not enable deferred return")
	}
	accessorFrame := &hostCallbackFrame{
		kind:  cbKindAccessorGet,
		pdPad: callbackDeferredRVInt32Magic,
	}
	if deferredInt32Capable(accessorFrame) {
		t.Fatal("marked non-function frame enabled deferred return")
	}
}
