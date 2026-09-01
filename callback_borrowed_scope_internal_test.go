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
	if got := unsafe.Sizeof(FunctionCallbackArguments{}); got != 24 {
		t.Fatalf("FunctionCallbackArguments size = %d, want 24", got)
	}
	if got := unsafe.Sizeof(CallbackScope{}); got != 32 {
		t.Fatalf("CallbackScope size = %d, want 32", got)
	}
	if got := unsafe.Sizeof(hostCallbackInvocation{}); got != 80 {
		t.Fatalf("hostCallbackInvocation size = %d, want 80", got)
	}
	for _, field := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{name: "pdFlags", got: unsafe.Offsetof(frame.pdFlags), want: 132},
		{name: "pdWritable", got: unsafe.Offsetof(frame.pdWritable), want: 136},
		{name: "pdEnumerable", got: unsafe.Offsetof(frame.pdEnumerable), want: 140},
		{name: "pdConfigurable", got: unsafe.Offsetof(frame.pdConfigurable), want: 144},
		{name: "pdPad", got: unsafe.Offsetof(frame.pdPad), want: 148},
	} {
		if field.got != field.want {
			t.Fatalf("%s offset = %d, want %d", field.name, field.got, field.want)
		}
	}
}

func TestFunctionCallbackArgumentShapePacking(t *testing.T) {
	for _, tc := range []struct {
		length    int64
		construct bool
	}{
		{length: 0},
		{length: 1, construct: true},
		{length: 1<<31 - 1},
		{length: 1<<31 - 1, construct: true},
	} {
		args := FunctionCallbackArguments{
			shape: packFunctionCallbackArgumentShape(tc.length, tc.construct),
		}
		if got := args.Length(); got != int(tc.length) {
			t.Fatalf("Length(%d, %v) = %d", tc.length, tc.construct, got)
		}
		if got := args.IsConstructCall(); got != tc.construct {
			t.Fatalf("IsConstructCall(%d, %v) = %v", tc.length, tc.construct, got)
		}
	}
}

func TestCallbackInt32ArgumentMetadataCapabilityAndWireCorrelation(t *testing.T) {
	argv := [...]uintptr{0x101, 0x202, 0x303}
	frame := &hostCallbackFrame{
		kind:           cbKindFunction,
		argc:           int64(len(argv)),
		argv:           unsafe.Pointer(&argv[0]),
		pdFlags:        callbackInt32Arg0Valid | callbackInt32Arg1Valid,
		pdWritable:     -2147483648,
		pdEnumerable:   2147483647,
		pdConfigurable: callbackInt32ArgsMagic,
	}
	cs := &CallbackScope{frame: frame}
	if got, ok := cs.cachedInt32Argument(Value{h: argv[0]}); !ok || got != -2147483648 {
		t.Fatalf("arg0 snapshot = %d, %v", got, ok)
	}
	if got, ok := cs.cachedInt32Argument(Value{h: argv[1]}); !ok || got != 2147483647 {
		t.Fatalf("arg1 snapshot = %d, %v", got, ok)
	}
	if _, ok := cs.cachedInt32Argument(Value{h: argv[2]}); ok {
		t.Fatal("third argument unexpectedly used two-slot metadata")
	}
	if _, ok := cs.cachedInt32Argument(Value{h: 0x404}); ok {
		t.Fatal("different local-handle wire unexpectedly matched metadata")
	}

	frame.pdConfigurable = 0
	if _, ok := cs.cachedInt32Argument(Value{h: argv[0]}); ok {
		t.Fatal("unmarked old-DLL frame enabled argument metadata")
	}
	frame.pdConfigurable = callbackInt32ArgsMagic
	frame.kind = cbKindAccessorGet
	if _, ok := cs.cachedInt32Argument(Value{h: argv[0]}); ok {
		t.Fatal("marked non-function frame enabled argument metadata")
	}
	frame.kind = cbKindFunction
	frame.pdFlags = callbackInt32Arg0Valid
	if _, ok := cs.cachedInt32Argument(Value{h: argv[1]}); ok {
		t.Fatal("negative IsInt32 result was cached")
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
