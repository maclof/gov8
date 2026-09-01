//go:build windows && amd64

package gov8

import (
	"runtime"
	"sync"
	"syscall"
	"testing"
)

func TestCurrentThreadIDFastMatchesWin32(t *testing.T) {
	getCurrentThreadID := syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			for range 1_000 {
				want, _, _ := getCurrentThreadID.Call()
				if got := currentThreadIDFast(); got != uint32(want) {
					t.Errorf("currentThreadIDFast() = %d, GetCurrentThreadId() = %d", got, uint32(want))
					return
				}
				if got := currentThreadID(); got != uint32(want) {
					t.Errorf("currentThreadID() = %d, GetCurrentThreadId() = %d", got, uint32(want))
					return
				}
			}
		}()
	}
	wg.Wait()

	if !tidFast {
		t.Fatal("verified Windows amd64 TEB thread-id fast path was not enabled")
	}
}
