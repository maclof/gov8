//go:build windows && amd64

package gov8

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSnapshotCounterCallbackCanAttemptRelease(t *testing.T) {
	creator, err := NewSnapshotCreator()
	if err != nil {
		t.Fatal(err)
	}
	iso := creator.Isolate()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.SetDefaultContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(FunctionCodeKeep)
	if err != nil {
		t.Fatal(err)
	}
	params, err := NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var releaseErr error
	params.SetCounterLookupCallback(func(string) {
		once.Do(func() { releaseErr = blob.Release() })
	})
	consumer, err := NewIsolateWithSnapshotParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if releaseErr == nil || !strings.Contains(releaseErr.Error(), "being consumed") {
		t.Fatalf("reentrant Release = %v", releaseErr)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := blob.Release(); err != nil {
		t.Fatal(err)
	}
}

// This exercises the failure path after the counter callback has been
// registered and the constructor goroutine has locked its OS thread, but
// before native entry. It verifies both registry and lifecycle-lock rollback.
func TestSnapshotConstructorFailureRollsBackHostState(t *testing.T) {
	creator, err := NewSnapshotCreator()
	if err != nil {
		t.Fatal(err)
	}
	iso := creator.Isolate()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.SetDefaultContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(FunctionCodeKeep)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blob.Release() }()
	params, err := NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	params.SetCounterLookupCallback(func(string) {})

	isolateCounterRegistry.Lock()
	target := isolateCounterRegistry.next + 1
	isolateCounterRegistry.Unlock()

	// Hold creation serialization so the constructor registers its callback
	// and locks its OS thread before beginIsolateCreate observes the forced
	// lifecycle transition.
	lifecycleMu.Lock()
	result := make(chan error, 1)
	go func() {
		_, err := NewIsolateWithSnapshotParams(params)
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		isolateCounterRegistry.Lock()
		_, registered := isolateCounterRegistry.entries[target]
		isolateCounterRegistry.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			lifecycleMu.Unlock()
			t.Fatal("counter callback was not registered")
		}
		runtime.Gosched()
	}
	storePlatform(stateDisposed)
	lifecycleMu.Unlock()
	err = <-result
	lifecycleMu.Lock()
	storePlatform(stateInitialized)
	lifecycleMu.Unlock()
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("constructor error = %v", err)
	}
	if !params.Consumed() {
		t.Fatal("failed constructor did not consume params")
	}
	isolateCounterRegistry.Lock()
	_, leaked := isolateCounterRegistry.entries[target]
	isolateCounterRegistry.Unlock()
	if leaked {
		t.Fatal("failed constructor leaked counter registry entry")
	}
	// A subsequent creation proves abandonIsolateCreate released the global
	// lifecycle lock. The failed goroutine has also returned through its
	// runtime.UnlockOSThread path.
	probe, err := NewIsolate()
	if err != nil {
		t.Fatalf("subsequent isolate: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
}
