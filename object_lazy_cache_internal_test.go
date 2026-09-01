//go:build windows && amd64

package gov8

import "testing"

func TestLazyGetterRegistrationReusesOnlyExactZeroDataFunctionValue(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ReleaseIsolateHostState(iso); err != nil {
			t.Errorf("ReleaseIsolateHostState: %v", err)
		}
		if err := iso.Close(); err != nil {
			t.Errorf("Isolate.Close: %v", err)
		}
	}()

	getter := AccessorGetterCallback(func(_ *CallbackScope, _ PropertyCallbackArguments, rv ReturnValue) {
		_ = rv.SetInt32(42)
	})
	first, firstEntry, firstKey, firstCreated, err := registerLazyGetter(iso, getter, Value{})
	if err != nil {
		t.Fatal(err)
	}
	if first == 0 || firstEntry == nil || firstKey.identity == 0 || !firstCreated {
		t.Fatalf("first registration = handle %d entry %p key %#x created %v", first, firstEntry, firstKey.identity, firstCreated)
	}
	second, secondEntry, secondKey, secondCreated, err := registerLazyGetter(iso, getter, Value{})
	if err != nil {
		t.Fatal(err)
	}
	if second != first || secondEntry != firstEntry || secondKey != firstKey || secondCreated {
		t.Fatalf("reused registration = handle %d entry %p key %#v created %v; want handle %d entry %p key %#v created false",
			second, secondEntry, secondKey, secondCreated, first, firstEntry, firstKey)
	}

	makeGetter := func(value int32) AccessorGetterCallback {
		return func(_ *CallbackScope, _ PropertyCallbackArguments, rv ReturnValue) {
			_ = rv.SetInt32(value)
		}
	}
	distinctGetter := makeGetter(43)
	distinct, distinctEntry, distinctKey, distinctCreated, err := registerLazyGetter(iso, distinctGetter, Value{})
	if err != nil {
		t.Fatal(err)
	}
	if distinct == first || distinctEntry == firstEntry || distinctKey == firstKey || !distinctCreated {
		t.Fatalf("distinct closure aliased first registration: handle %d entry %p key %#v created %v",
			distinct, distinctEntry, distinctKey, distinctCreated)
	}

	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	hostCallbackRegistry.mu.Lock()
	_, firstCached := hostCallbackRegistry.lazyGetters[firstKey]
	_, distinctCached := hostCallbackRegistry.lazyGetters[distinctKey]
	_, firstRetained := hostCallbackRegistry.entries[first]
	_, distinctRetained := hostCallbackRegistry.entries[distinct]
	hostCallbackRegistry.mu.Unlock()
	if firstCached || distinctCached || firstRetained || distinctRetained {
		t.Fatalf("release retained cache/entries: first=(%v,%v) distinct=(%v,%v)",
			firstCached, firstRetained, distinctCached, distinctRetained)
	}
}
