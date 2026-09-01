//go:build windows && amd64

package gov8

import (
	"strings"
	"testing"
)

func callbackEntriesForIsolate(iso *Isolate) int {
	hostCallbackRegistry.mu.Lock()
	defer hostCallbackRegistry.mu.Unlock()
	count := 0
	for _, entry := range hostCallbackRegistry.entries {
		if entry.iso == iso {
			count++
		}
	}
	return count
}

func TestFunctionNewDirectNegativeAndRegistrationSafety(t *testing.T) {
	status, _, _ := proc("gov8_fa_function_new_direct").Call(0, 0, 0, 0, 0, 0, 0)
	if int64(status) >= 0 {
		t.Fatalf("direct export nil-argument status = %d, want negative", int64(status))
	}

	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	closedScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}

	before := callbackEntriesForIsolate(iso)
	_, err = iso.NewFunction(closedScope, ctx,
		func(*CallbackScope, FunctionCallbackArguments, ReturnValue) {}, nil)
	if err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("NewFunction with closed scope = %v, want closed-scope error", err)
	}
	if after := callbackEntriesForIsolate(iso); after != before {
		t.Fatalf("callback registrations after failed NewFunction = %d, want %d", after, before)
	}

	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
