//go:build windows && amd64

package gov8

import "testing"

// BenchmarkFFINoopCall measures the pure Go->shim->Go transition cost
// (gov8_abi_version): the floor price of the no-cgo syscall path. It lives
// in the internal test package and uses the internal procedure table
// directly, so the raw DLL procedure table never becomes public API.
func BenchmarkFFINoopCall(b *testing.B) {
	p := proc("gov8_abi_version")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = p.Call()
	}
}

// BenchmarkFFIProcLookup measures the cached procedure-table lookup that
// precedes every shim dispatch (proc()): one atomic pointer load plus a map
// lookup against the resolved *syscall.Proc. Cold resolution (the
// GetProcAddress path) is out of scope by design: it runs at most once per
// export per process.
func BenchmarkFFIProcLookup(b *testing.B) {
	if err := loadShim(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = proc("gov8_abi_version")
	}
}

// BenchmarkFFIThreadID measures the isolate-affinity check primitive
// (currentThreadID) as used by every wrapper validation.
func BenchmarkFFIThreadID(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = currentThreadID()
	}
}
