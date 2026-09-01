//go:build windows && amd64

package gov8_test

import (
	"os"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// TestMain initializes V8 exactly once per test-binary process, mirroring the
// oracle's ensure_v8. The real full-shutdown path (Dispose + DisposePlatform
// consuming process state) is exercised in internal/lifecycle, which runs in
// its own process.
func TestMain(m *testing.M) {
	if os.Getenv("GOV8_CPPGC_PERSISTENT_GC_CHILD") == "1" {
		if err := gov8.SetFlagsFromString("--expose-gc"); err != nil {
			println("gov8 cppgc persistent test setup: SetFlagsFromString:", err.Error())
			os.Exit(1)
		}
	}
	if err := gov8.Initialize(); err != nil {
		println("gov8 test setup: Initialize:", err.Error())
		os.Exit(1)
	}
	os.Exit(m.Run())
}
