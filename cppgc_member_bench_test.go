//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "github.com/maclof/gov8"
)

// BenchmarkCppGCMemberGraphSetGet measures the Member write barrier plus the
// safe copied-metadata read. Allocation and isolate setup are outside timing.
func BenchmarkCppGCMemberGraphSetGet(b *testing.B) {
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
	allocate := func(id int32) *gov8.CppGCObject {
		template, err := iso.NewFunctionTemplate(scope, func(*gov8.CallbackScope, gov8.FunctionCallbackArguments, gov8.ReturnValue) {}, nil)
		if err != nil {
			b.Fatal(err)
		}
		function, err := template.GetFunction(scope, ctx)
		if err != nil {
			b.Fatal(err)
		}
		wrapper, ok, err := function.NewInstance(scope)
		if err != nil || !ok {
			b.Fatalf("NewInstance = %v, %v", ok, err)
		}
		target, err := scope.NewObject(ctx)
		if err != nil {
			b.Fatal(err)
		}
		object, err := scope.WrapCppGCObject(wrapper, target.Value, id, 1, gov8.CppGCObjectCallbacks{})
		if err != nil {
			b.Fatal(err)
		}
		return object
	}
	ownerObject := allocate(1)
	first := allocate(2)
	second := allocate(3)
	owner, err := gov8.NewCppGCPersistent(ownerObject)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := owner.Close(); err != nil {
			b.Errorf("owner.Close: %v", err)
		}
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		child := first
		want := int32(2)
		if i&1 != 0 {
			child = second
			want = 3
		}
		if err := owner.SetStrongMember(child); err != nil {
			b.Fatal(err)
		}
		snapshot, ok, err := owner.StrongMember()
		if err != nil || !ok || snapshot.ObjectID != want {
			b.Fatalf("StrongMember = %#v, %v, %v; want %d", snapshot, ok, err, want)
		}
	}
	b.StopTimer()
}
