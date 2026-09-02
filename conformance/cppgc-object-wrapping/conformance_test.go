//go:build windows && amd64

package cppgc_object_wrapping_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const childEnv = "GOV8_CPPGC_CONFORMANCE_CHILD"

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

func must[T any](value T, err error) T {
	if err != nil {
		fail("%v", err)
	}
	return value
}

func apiWrapper(iso *gov8.Isolate, ctx *gov8.Context, scope *gov8.Scope) *gov8.Object {
	template := must(iso.NewFunctionTemplate(scope, func(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}, nil))
	function := must(template.GetFunction(scope, ctx))
	wrapper, ok, err := function.NewInstance(scope)
	if err != nil || !ok {
		fail("NewInstance: ok=%v err=%v", ok, err)
	}
	return wrapper
}

func wrap(scope *gov8.Scope, wrapper *gov8.Object, target gov8.Value, id int32, tag gov8.CppGCTag, traces, drops *atomic.Int32) *gov8.CppGCObject {
	object, err := scope.WrapCppGCObject(wrapper, target, id, tag, gov8.CppGCObjectCallbacks{
		Trace: func() { traces.Add(1) },
		Destroy: func() {
			drops.Add(1)
		},
	})
	if err != nil {
		fail("WrapCppGCObject: %v", err)
	}
	return object
}

func child() {
	if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
		fail("SetFlagsFromString: %v", err)
	}
	if err := gov8.Initialize(); err != nil {
		fail("Initialize: %v", err)
	}
	iso := must(gov8.NewIsolate())
	ctx := must(iso.NewContext())

	var identityTraces, identityDrops atomic.Int32
	var survivalTraces, survivalDrops atomic.Int32
	var boundaryTraces, boundaryDrops atomic.Int32

	scope := must(iso.NewScope())
	present := must(iso.HasCppHeap())
	plain := must(scope.NewObject(ctx))
	plainAPI := must(plain.IsAPIWrapper())
	identityWrapper := apiWrapper(iso, ctx, scope)
	identityAPI := must(identityWrapper.IsAPIWrapper())
	_, _, before, err := scope.UnwrapCppGCObject(identityWrapper, 1)
	if err != nil {
		fail("unwrap before: %v", err)
	}
	fmt.Printf("{\"check\":\"cppgc-object-wrapping/default_heap_and_api_wrapper\",\"ok\":true,\"value\":{\"default_heap_present\":%t,\"plain_is_api_wrapper\":%t,\"api_is_api_wrapper\":%t,\"unwrapped_before_wrap\":%t}}\n", present, plainAPI, identityAPI, before)

	identityTarget := must(scope.NewObject(ctx))
	identity := wrap(scope, identityWrapper, identityTarget.Value, 7, 1, &identityTraces, &identityDrops)
	first, firstTarget, ok, err := scope.UnwrapCppGCObject(identityWrapper, 1)
	if err != nil || !ok {
		fail("first unwrap: ok=%v err=%v", ok, err)
	}
	second, _, ok, err := scope.UnwrapCppGCObject(identityWrapper, 1)
	if err != nil || !ok {
		fail("second unwrap: ok=%v err=%v", ok, err)
	}
	id := must(identity.ID())
	samePointer := must(first.Same(second))
	tracedIdentity := must(firstTarget.StrictEquals(identityTarget.Value))
	fmt.Printf("{\"check\":\"cppgc-object-wrapping/wrap_unwrap_identity\",\"ok\":true,\"value\":{\"id\":%d,\"same_pointer\":%t,\"traced_identity\":%t}}\n", id, samePointer, tracedIdentity)

	survivalTarget := must(scope.NewObject(ctx))
	marker := must(scope.Int32(42))
	if ok, err := survivalTarget.SetByName(scope, ctx, "marker", marker); err != nil || !ok {
		fail("set marker: ok=%v err=%v", ok, err)
	}
	survivalWrapper := apiWrapper(iso, ctx, scope)
	wrap(scope, survivalWrapper, survivalTarget.Value, 42, 1, &survivalTraces, &survivalDrops)
	global := must(ctx.GlobalObject(scope))
	if ok, err := global.SetByName(scope, ctx, "wrapped", survivalWrapper.Value); err != nil || !ok {
		fail("root wrapper: ok=%v err=%v", ok, err)
	}
	if err := scope.Close(); err != nil {
		fail("first scope Close: %v", err)
	}

	scope = must(iso.NewScope())
	if err := iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		fail("first full GC: %v", err)
	}
	global = must(ctx.GlobalObject(scope))
	rootedValue, ok, err := global.GetByName(scope, ctx, "wrapped")
	if err != nil || !ok {
		fail("get rooted wrapper: ok=%v err=%v", ok, err)
	}
	rootedWrapper := &gov8.Object{Value: rootedValue}
	_, traced, ok, err := scope.UnwrapCppGCObject(rootedWrapper, 1)
	if err != nil || !ok {
		fail("rooted unwrap: ok=%v err=%v", ok, err)
	}
	tracedObject := must(traced.ToObject(scope, ctx, nil))
	markerValue, ok, err := tracedObject.GetByName(scope, ctx, "marker")
	if err != nil || !ok {
		fail("marker get: ok=%v err=%v", ok, err)
	}
	markerInt, ok, err := markerValue.IntegerValue(ctx)
	if err != nil || !ok {
		fail("marker int: ok=%v err=%v", ok, err)
	}
	fmt.Printf("{\"check\":\"cppgc-object-wrapping/traced_reference_survival\",\"ok\":true,\"value\":{\"drops_while_rooted\":%d,\"marker\":%d,\"trace_calls_positive\":%t}}\n", survivalDrops.Load(), markerInt, survivalTraces.Load() > 0)
	if err := scope.Close(); err != nil {
		fail("second scope Close: %v", err)
	}

	scope = must(iso.NewScope())
	global = must(ctx.GlobalObject(scope))
	undefined := must(scope.Undefined())
	if ok, err := global.SetByName(scope, ctx, "wrapped", undefined); err != nil || !ok {
		fail("release wrapper: ok=%v err=%v", ok, err)
	}
	if err := scope.Close(); err != nil {
		fail("third scope Close: %v", err)
	}

	scope = must(iso.NewScope())
	if err := iso.RequestGarbageCollectionForTesting(gov8.GcFull); err != nil {
		fail("second full GC: %v", err)
	}
	fmt.Printf("{\"check\":\"cppgc-object-wrapping/gc_destruction\",\"ok\":true,\"value\":{\"drops_after_release\":%d,\"identity_collected\":%t}}\n", survivalDrops.Load(), identityDrops.Load() == 1)

	maxWrapper := apiWrapper(iso, ctx, scope)
	maxTarget := must(scope.NewObject(ctx))
	wrap(scope, maxWrapper, maxTarget.Value, int32(gov8.MaxCppGCTag), gov8.MaxCppGCTag, &boundaryTraces, &boundaryDrops)
	maxObject, _, ok, err := scope.UnwrapCppGCObject(maxWrapper, gov8.MaxCppGCTag)
	if err != nil || !ok {
		fail("max unwrap: ok=%v err=%v", ok, err)
	}
	zeroWrapper := apiWrapper(iso, ctx, scope)
	zeroTarget := must(scope.NewObject(ctx))
	wrap(scope, zeroWrapper, zeroTarget.Value, 0, 0, &boundaryTraces, &boundaryDrops)
	zeroObject, _, ok, err := scope.UnwrapCppGCObject(zeroWrapper, 0)
	if err != nil || !ok {
		fail("zero unwrap: ok=%v err=%v", ok, err)
	}
	fmt.Printf("{\"check\":\"cppgc-object-wrapping/tag_boundaries\",\"ok\":true,\"value\":{\"min_tag\":0,\"min_unwrap_id\":%d,\"max_tag\":32766,\"max_unwrap_id\":%d}}\n", must(zeroObject.ID()), must(maxObject.ID()))

	if err := scope.Close(); err != nil {
		fail("fourth scope Close: %v", err)
	}
	if err := ctx.Close(); err != nil {
		fail("Context.Close: %v", err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		fail("ReleaseIsolateHostState: %v", err)
	}
	if err := iso.Close(); err != nil {
		fail("Isolate.Close: %v", err)
	}
	disposed, err := gov8.Dispose()
	if err != nil {
		fail("Dispose: %v", err)
	}
	if err := gov8.DisposePlatform(); err != nil {
		fail("DisposePlatform: %v", err)
	}
	fmt.Printf("{\"check\":\"cppgc-object-wrapping/orderly_teardown\",\"ok\":true,\"value\":{\"boundary_drops\":%d,\"cppgc_shutdown\":true,\"platform_dispose\":true,\"v8_dispose\":%t}}\n", boundaryDrops.Load(), disposed)
	fmt.Println("{\"summary\":{\"total\":6,\"passed\":6,\"failed\":0}}")
}

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		child()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCppGCObjectWrappingMatchesFixture(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-cppgc-object-wrapping-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != string(fixture) {
		t.Fatalf("fixture mismatch\nactual:\n%s\nwant:\n%s", output, fixture)
	}
}
