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
}
