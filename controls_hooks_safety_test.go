//go:build windows && amd64

package gov8

import (
	"runtime"
	"strings"
	"testing"
)

func TestValidatePrepareStackTraceResult(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	iso := &Isolate{handle: 1, tid: currentThreadID()}
	scope := &Scope{iso: iso, handle: 2}
	valid := Value{iso: iso, sc: scope, h: 3}
	if err := validatePrepareStackTraceResult(iso, scope, valid); err != nil {
		t.Fatalf("valid callback result: %v", err)
	}

	tests := []struct {
		name string
		v    Value
		want string
	}{
		{name: "empty", v: Value{}, want: "empty value"},
		{name: "foreign isolate", v: Value{
			iso: &Isolate{handle: 4, tid: currentThreadID()}, sc: scope, h: 5,
		}, want: "different isolate"},
		{name: "different scope", v: Value{
			iso: iso, sc: &Scope{iso: iso, handle: 6}, h: 7,
		}, want: "callback scope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePrepareStackTraceResult(iso, scope, tt.v)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}

	scope.closed = true
	if err := validatePrepareStackTraceResult(iso, scope, valid); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("closed callback scope error = %v", err)
	}
}

func TestCallbackScopeThrowExceptionRejectsInvalidInputsBeforeFFI(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	iso := &Isolate{handle: 11, tid: currentThreadID()}
	scope := &Scope{iso: iso, handle: 12}
	value := Value{iso: iso, sc: scope, h: 13}

	var nilScope *CallbackScope
	if err := nilScope.ThrowException(value); err == nil || !strings.Contains(err.Error(), "invalid callback scope") {
		t.Fatalf("nil callback scope error = %v", err)
	}

	callback := &CallbackScope{iso: iso, sc: scope, ctxWire: 14}
	foreignIso := &Isolate{handle: 15, tid: currentThreadID()}
	foreignScope := &Scope{iso: foreignIso, handle: 16}
	foreign := Value{iso: foreignIso, sc: foreignScope, h: 17}
	if err := callback.ThrowException(foreign); err == nil || !strings.Contains(err.Error(), "exception belongs to a different isolate") {
		t.Fatalf("foreign exception error = %v", err)
	}

	scope.closed = true
	if err := callback.ThrowException(value); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("closed callback scope error = %v", err)
	}
}

func TestReleaseCHIsolateEntriesIsIdempotentAndAddressReuseSafe(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	const reusedAddress = uintptr(0x76543210)
	oldIsolate := &Isolate{handle: reusedAddress, tid: currentThreadID()}
	newIsolate := &Isolate{handle: reusedAddress}

	oldKeyA := chKey{kind: 100001, engine: reusedAddress}
	oldKeyB := chKey{kind: 100002, engine: reusedAddress}
	newKey := chKey{kind: 100003, engine: reusedAddress}
	globalKey := chKey{kind: 100004, engine: 0}
	oldEntryA := &chEntry{iso: oldIsolate}
	oldEntryB := &chEntry{iso: oldIsolate}
	newEntry := &chEntry{iso: newIsolate}
	globalEntry := &chEntry{}

	chRegistry.mu.Lock()
	chRegistry.entries[oldKeyA] = oldEntryA
	chRegistry.entries[oldKeyB] = oldEntryB
	chRegistry.entries[newKey] = newEntry
	chRegistry.entries[globalKey] = globalEntry
	chRegistry.mu.Unlock()
	t.Cleanup(func() {
		chRegistry.mu.Lock()
		delete(chRegistry.entries, oldKeyA)
		delete(chRegistry.entries, oldKeyB)
		delete(chRegistry.entries, newKey)
		delete(chRegistry.entries, globalKey)
		chRegistry.mu.Unlock()
	})

	for attempt := 0; attempt < 2; attempt++ {
		if err := ReleaseIsolateHostState(oldIsolate); err != nil {
			t.Fatalf("ReleaseIsolateHostState attempt %d: %v", attempt+1, err)
		}
	}

	chRegistry.mu.Lock()
	defer chRegistry.mu.Unlock()
	if _, ok := chRegistry.entries[oldKeyA]; ok {
		t.Error("first old-isolate entry survived release")
	}
	if _, ok := chRegistry.entries[oldKeyB]; ok {
		t.Error("second old-isolate entry survived release")
	}
	if got := chRegistry.entries[newKey]; got != newEntry {
		t.Fatalf("new isolate entry at reused address = %p, want %p", got, newEntry)
	}
	if got := chRegistry.entries[globalKey]; got != globalEntry {
		t.Fatalf("process-global entry = %p, want %p", got, globalEntry)
	}
}
