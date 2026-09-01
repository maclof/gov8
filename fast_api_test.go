//go:build windows && amd64

package gov8

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func fastTestRuntime(t *testing.T) (*Isolate, *Context, *Scope) {
	t.Helper()
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	return iso, ctx, scope
}

func fastTestAddress(t *testing.T, kind uintptr) uintptr {
	t.Helper()
	address, _, _ := proc("gov8_fast_api_test_address").Call(kind)
	if address == 0 {
		t.Fatalf("fast test address %d is zero", kind)
	}
	return address
}

func fastInfo(t *testing.T, kind FastType) FastTypeInfo {
	t.Helper()
	info, err := kind.Info()
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func fastFunction(t *testing.T, address uintptr, args ...FastType) CFunction {
	t.Helper()
	argumentInfo := make([]FastTypeInfo, len(args))
	for index, arg := range args {
		argumentInfo[index] = fastInfo(t, arg)
	}
	info, err := NewCFunctionInfo(fastInfo(t, FastTypeUint32), argumentInfo, FastInt64AsNumber)
	if err != nil {
		t.Fatal(err)
	}
	function, err := NewCFunction(address, info)
	if err != nil {
		t.Fatal(err)
	}
	return function
}

func TestFastAPIMetadataAndWireLayout(t *testing.T) {
	for value := FastTypeVoid; value <= FastTypeAny; value++ {
		info, err := value.Info()
		if err != nil || info.Type() != value || info.Flags() != 0 || info.Identifier() != uint32(value)<<8 {
			t.Fatalf("type %d info = %+v, %v", value, info, err)
		}
	}
	options, err := FastTypeCallbackOptions.Info()
	if err != nil || options.Identifier() != 0xff00 {
		t.Fatalf("CallbackOptions = %+v, %v", options, err)
	}
	for _, flag := range []FastTypeFlags{FastTypeAllowShared, FastTypeEnforceRange, FastTypeClamp, FastTypeIsRestricted} {
		if roundTrip, ok := FastTypeFlagsFromBits(uint8(flag)); !ok || roundTrip != flag {
			t.Fatalf("flag %#x round trip = %#x, %v", flag, roundTrip, ok)
		}
	}
	if _, ok := FastTypeFlagsFromBits(0x10); ok {
		t.Fatal("unknown flag bit accepted")
	}
	if got := FastTypeFlagsFromBitsTruncated(0x1f); got != fastTypeAllFlags {
		t.Fatalf("truncated flags = %#x", got)
	}
	if got := unsafe.Sizeof(fastTypeWire{}); got != 2 {
		t.Fatalf("fastTypeWire size = %d, want 2", got)
	}
	if got := unsafe.Sizeof(fastFunctionWire{}); got != 24 {
		t.Fatalf("fastFunctionWire size = %d, want 24", got)
	}
	if got := unsafe.Offsetof(fastFunctionWire{}.ReturnType); got != 16 {
		t.Fatalf("fastFunctionWire return offset = %d, want 16", got)
	}

	info, err := NewCFunctionInfo(fastInfo(t, FastTypeUint32), []FastTypeInfo{
		fastInfo(t, FastTypeV8Value), fastInfo(t, FastTypeUint32), options,
	}, FastInt64AsBigInt)
	if err != nil {
		t.Fatal(err)
	}
	if info.ArgumentCount() != 2 || !info.HasOptions() || info.Int64Representation() != FastInt64AsBigInt {
		t.Fatalf("CFunctionInfo public metadata = count %d, options %v, repr %d",
			info.ArgumentCount(), info.HasOptions(), info.Int64Representation())
	}
	if _, ok := info.ArgumentInfo(2); ok {
		t.Fatal("CallbackOptions exposed as an ordinary argument")
	}
	fn, err := NewCFunction(fastTestAddress(t, 0), info)
	if err != nil {
		t.Fatal(err)
	}
	copy := fn
	if copy.Address() != fn.Address() || copy.TypeInfo() != fn.TypeInfo() {
		t.Fatal("CFunction copy did not preserve address/type-info identity")
	}
}

func TestFastAPIRejectsInvalidMetadata(t *testing.T) {
	if _, err := NewFastTypeInfo(14, 0); err == nil {
		t.Fatal("unknown type accepted")
	}
	if _, err := NewFastTypeInfo(FastTypeUint32, 0x10); err == nil {
		t.Fatal("unknown flags accepted")
	}
	if _, err := NewFastTypeInfo(FastTypeFloat64, FastTypeClamp); err == nil {
		t.Fatal("integral flag on float accepted")
	}
	if _, err := NewFastTypeInfo(FastTypeUint32, FastTypeIsRestricted); err == nil {
		t.Fatal("restricted flag on integer accepted")
	}
	if _, err := NewFastTypeInfo(FastTypeCallbackOptions, FastTypeAllowShared); err == nil {
		t.Fatal("flagged CallbackOptions accepted")
	}
	if _, err := NewCFunctionInfo(fastInfo(t, FastTypeCallbackOptions), nil, FastInt64AsNumber); err == nil {
		t.Fatal("CallbackOptions return accepted")
	}
	if _, err := NewCFunctionInfo(fastInfo(t, FastTypeVoid), []FastTypeInfo{
		fastInfo(t, FastTypeCallbackOptions), fastInfo(t, FastTypeUint32),
	}, FastInt64AsNumber); err == nil {
		t.Fatal("non-final CallbackOptions accepted")
	}
	if _, err := NewCFunctionInfo(fastInfo(t, FastTypeVoid), nil, 2); err == nil {
		t.Fatal("unknown int64 representation accepted")
	}
	if _, err := NewCFunction(0, &CFunctionInfo{}); err == nil {
		t.Fatal("zero address accepted")
	}
	if _, err := NewCFunction(1, nil); err == nil {
		t.Fatal("nil type info accepted")
	}
}

func TestFastFunctionTemplateSafetyBoundaries(t *testing.T) {
	iso, ctx, scope := fastTestRuntime(t)
	defer func() {
		if err := scope.Close(); err != nil {
			t.Error(err)
		}
		if err := ctx.Close(); err != nil {
			t.Error(err)
		}
		if err := ReleaseIsolateHostState(iso); err != nil {
			t.Error(err)
		}
		if err := iso.Close(); err != nil {
			t.Error(err)
		}
	}()
	slow := func(_ *CallbackScope, args FunctionCallbackArguments, rv ReturnValue) {
		_ = rv.SetInt32(int32(700 + args.Length()))
	}
	if _, err := iso.NewFastFunctionTemplate(scope, nil, nil, nil); err == nil {
		t.Fatal("nil slow callback accepted")
	}
	var nilBuilder *FunctionBuilder
	if _, err := nilBuilder.BuildFast(scope, nil); err == nil {
		t.Fatal("nil function builder accepted")
	}

	optionsInfo := fastInfo(t, FastTypeCallbackOptions)
	withOptions, err := NewCFunctionInfo(fastInfo(t, FastTypeVoid), []FastTypeInfo{optionsInfo}, FastInt64AsNumber)
	if err != nil {
		t.Fatal(err)
	}
	optionsFunction, err := NewCFunction(fastTestAddress(t, 0), withOptions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iso.NewFastFunctionTemplate(scope, slow, nil, []CFunction{optionsFunction}); err == nil || !strings.Contains(err.Error(), "CallbackOptions") {
		t.Fatalf("executable CallbackOptions = %v", err)
	}

	one := fastFunction(t, fastTestAddress(t, 1), FastTypeV8Value, FastTypeUint32)
	if _, err := iso.NewFastFunctionTemplate(scope, slow, nil, []CFunction{one, one}); err == nil || !strings.Contains(err.Error(), "ArgumentCount") {
		t.Fatalf("duplicate arity = %v", err)
	}

	template, err := iso.FunctionBuilder(slow).BuildFast(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := fastAPIDescriptorCount(iso); err != nil || count != 0 {
		t.Fatalf("empty overload retained descriptor storage = %d, %v", count, err)
	}
	function, err := template.GetFunction(scope, ctx)
	if err != nil {
		t.Fatal(err)
	}
	undefined, err := scope.Undefined()
	if err != nil {
		t.Fatal(err)
	}
	oneArg, err := scope.Number(1)
	if err != nil {
		t.Fatal(err)
	}
	twoArg, err := scope.Number(2)
	if err != nil {
		t.Fatal(err)
	}
	value, ok, err := function.Call(scope, undefined, oneArg, twoArg)
	if err != nil || !ok {
		t.Fatalf("slow fallback = %v, %v", ok, err)
	}
	result, intOK, err := value.IntegerValue(ctx)
	if err != nil || !intOK || result != 702 {
		t.Fatalf("slow result = %d, %v, %v", result, intOK, err)
	}
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	if object, ok, err := function.NewInstance(scope); err != nil || ok || object != nil {
		t.Fatalf("fast template construct = %v, %v, %v", object, ok, err)
	}
	if caught, err := tc.HasCaught(); err != nil || !caught {
		t.Fatalf("constructor TypeError caught = %v, %v", caught, err)
	}
	if err := tc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFastFunctionTemplateOwnershipAndThread(t *testing.T) {
	isoA, ctxA, scopeA := fastTestRuntime(t)
	isoB, ctxB, scopeB := fastTestRuntime(t)
	slow := func(*CallbackScope, FunctionCallbackArguments, ReturnValue) {}
	if _, err := isoA.NewFastFunctionTemplate(scopeB, slow, nil, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign scope = %v", err)
	}
	foreignData, err := scopeB.Number(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isoA.NewFastFunctionTemplate(scopeA, slow, &FunctionOptions{Data: foreignData}, nil); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign data = %v", err)
	}
	threadResult := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, err := isoA.NewFastFunctionTemplate(scopeA, slow, nil, nil)
		threadResult <- err
	}()
	if err := <-threadResult; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong-thread build = %v", err)
	}
	closedScope, err := isoA.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := isoA.NewFastFunctionTemplate(closedScope, slow, nil, nil); err == nil || !strings.Contains(err.Error(), "Close") {
		t.Fatalf("closed-scope build = %v", err)
	}
	if err := scopeB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctxB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(isoB); err != nil {
		t.Fatal(err)
	}
	if err := isoB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scopeA.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctxA.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseIsolateHostState(isoA); err != nil {
		t.Fatal(err)
	}
	if err := isoA.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFastAPIDescriptorsLiveThroughIsolateDisposal(t *testing.T) {
	iso, ctx, scope := fastTestRuntime(t)
	fn := fastFunction(t, fastTestAddress(t, 0), FastTypeV8Value, FastTypeUint32, FastTypeUint32)
	if _, err := iso.NewFastFunctionTemplate(scope,
		func(*CallbackScope, FunctionCallbackArguments, ReturnValue) {}, nil, []CFunction{fn}); err != nil {
		t.Fatal(err)
	}
	if count, err := fastAPIDescriptorCount(iso); err != nil || count != 1 {
		t.Fatalf("descriptor count after build = %d, %v", count, err)
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if count, err := fastAPIDescriptorCount(iso); err != nil || count != 1 {
		t.Fatalf("descriptor freed before disposal = %d, %v", count, err)
	}
	handle := iso.handleAssumingCheck()
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if count, err := fastAPIDescriptorCountHandle(handle); err != nil || count != 0 {
		t.Fatalf("descriptor count after disposal = %d, %v", count, err)
	}
	if fastAPIIsolateTracked(iso) {
		t.Fatal("disposed isolate remained in Go fast API registry")
	}
}

func BenchmarkCFunctionInfoConstruction(b *testing.B) {
	returnInfo, err := FastTypeUint32.Info()
	if err != nil {
		b.Fatal(err)
	}
	args := make([]FastTypeInfo, 3)
	for index, kind := range []FastType{FastTypeV8Value, FastTypeUint32, FastTypeUint32} {
		args[index], err = kind.Info()
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, err := NewCFunctionInfo(returnInfo, args, FastInt64AsNumber); err != nil {
			b.Fatal(err)
		}
	}
}
