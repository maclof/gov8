//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

func BenchmarkCppGCHeapCreateClose(b *testing.B) {
	params := gov8.DefaultCppGCHeapCreateParams()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		heap, err := gov8.NewCppGCHeap(params)
		if err != nil {
			b.Fatal(err)
		}
		if err := heap.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
