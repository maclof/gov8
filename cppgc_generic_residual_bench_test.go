//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

func BenchmarkCppGCGenericCellUpdate(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	object, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "bench-cell", Alignment: 1})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = object.Close()
		_ = iso.Close()
	})
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, err := object.UpdateCell(1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCppGCGenericMemberMutation(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	owner, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "bench-owner", Alignment: 1})
	if err != nil {
		b.Fatal(err)
	}
	first, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "bench-first", Alignment: 1})
	if err != nil {
		b.Fatal(err)
	}
	second, err := iso.NewCppGCGenericObject(gov8.CppGCGenericOptions{Name: "bench-second", Alignment: 1})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = second.Close()
		_ = first.Close()
		_ = owner.Close()
		_ = iso.Close()
	})
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		child := first
		if n&1 != 0 {
			child = second
		}
		if err := owner.SetOptionalMember(child); err != nil {
			b.Fatal(err)
		}
	}
}
