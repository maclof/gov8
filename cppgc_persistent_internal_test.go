//go:build windows && amd64

package gov8

import (
	"strings"
	"testing"
	"time"
)

func TestCppGCPersistentWrongThreadChecksBeforeHandleLock(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	persistent, err := NewEmptyCppGCPersistent(iso)
	if err != nil {
		t.Fatal(err)
	}

	// Holding the handle lock models isolate teardown after it has acquired the
	// isolate lifecycle lock. A wrong-thread operation must reject on affinity
	// without waiting for this lock, otherwise the inverse ordering can deadlock.
	persistent.persistent.mu.Lock()
	result := make(chan error, 1)
	go func() {
		_, _, err := persistent.Get()
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "affinity") {
			t.Fatalf("wrong-thread Get = %v", err)
		}
	case <-time.After(2 * time.Second):
		persistent.persistent.mu.Unlock()
		t.Fatal("wrong-thread Get waited for persistent handle lock")
	}
	persistent.persistent.mu.Unlock()

	if err := persistent.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
