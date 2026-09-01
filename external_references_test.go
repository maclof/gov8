//go:build windows && amd64

package gov8

import (
	"sync"
	"testing"
)

func closeExternalReferenceIsolate(t *testing.T, isolate *Isolate) {
	t.Helper()
	if err := ReleaseIsolateHostState(isolate); err != nil {
		t.Fatal(err)
	}
	if err := isolate.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalReferenceValueAndCallbackKinds(t *testing.T) {
	null := NewExternalReference(0)
	if !null.IsNull() || null.Address() != 0 || null.String() != "0x0" {
		t.Fatalf("null external reference = %#v, %q", null, null.String())
	}
	raw := NewExternalReference(0x1234)
	if raw.IsNull() || raw.Address() != 0x1234 || raw.String() != "0x1234" {
		t.Fatalf("raw external reference = %#v, %q", raw, raw.String())
	}
	if raw != NewExternalReference(0x1234) || raw == NewExternalReference(0x1235) {
		t.Fatal("external reference equality does not compare address words")
	}

	for kind := ExternalReferenceFunction; kind <= ExternalReferenceMessage; kind++ {
		first, err := NewCallbackExternalReference(kind)
		if err != nil {
			t.Fatalf("callback kind %d: %v", kind, err)
		}
		second, err := NewCallbackExternalReference(kind)
		if err != nil {
			t.Fatalf("callback kind %d repeated: %v", kind, err)
		}
		if first.IsNull() || first != second {
			t.Fatalf("callback kind %d = %v then %v", kind, first, second)
		}
	}
	for _, kind := range []ExternalReferenceCallbackKind{-1, ExternalReferenceMessage + 1, 1 << 30} {
		if reference, err := NewCallbackExternalReference(kind); err == nil || !reference.IsNull() {
			t.Fatalf("invalid callback kind %d = %v, %v", kind, reference, err)
		}
	}
}

func TestExternalReferenceFunctionTemplateValidation(t *testing.T) {
	isolateA, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scopeA, err := isolateA.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	externalA, err := scopeA.NewExternal(1)
	if err != nil {
		t.Fatal(err)
	}
	stringA, err := scopeA.NewString("not external")
	if err != nil {
		t.Fatal(err)
	}
	callback, err := NewCallbackExternalReference(ExternalReferenceFunction)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolateA.NewFunctionTemplateFromExternalReference(scopeA, NewExternalReference(1), externalA); err == nil {
		t.Fatal("arbitrary callback address was accepted by safe function-template constructor")
	}
	if _, err := isolateA.NewFunctionTemplateFromExternalReference(scopeA, callback, stringA); err == nil {
		t.Fatal("non-External callback data was accepted")
	}

	isolateB, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	scopeB, err := isolateB.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolateB.NewFunctionTemplateFromExternalReference(scopeB, callback, externalA); err == nil {
		t.Fatal("foreign-isolate callback data was accepted")
	}
	if err := scopeB.Close(); err != nil {
		t.Fatal(err)
	}
	closeExternalReferenceIsolate(t, isolateB)
	if err := scopeA.Close(); err != nil {
		t.Fatal(err)
	}
	closeExternalReferenceIsolate(t, isolateA)
}

func TestCreateParamsExternalReferenceNormalizationAndLifetime(t *testing.T) {
	unset, err := NewIsolateWithParams(NewCreateParams())
	if err != nil {
		t.Fatal(err)
	}
	if present, _, _, _, err := externalReferenceTableInfo(unset.handle); err != nil || present {
		t.Fatalf("unset table = %v, %v", present, err)
	}
	closeExternalReferenceIsolate(t, unset)

	emptyParams := NewCreateParams().UseEmptyExternalReferences()
	if !emptyParams.HasExternalReferences() || !emptyParams.HasEmptyExternalReferences() {
		t.Fatal("explicit empty external-reference state not recorded")
	}
	empty, err := NewIsolateWithParams(emptyParams)
	if err != nil {
		t.Fatal(err)
	}
	emptyHandle := empty.handle
	if present, length, first, last, err := externalReferenceTableInfo(emptyHandle); err != nil || !present || length != 1 || first != 0 || last != 0 {
		t.Fatalf("empty native table = present:%v length:%d first:%#x last:%#x err:%v", present, length, first, last, err)
	}
	closeExternalReferenceIsolate(t, empty)
	if present, _, _, _, err := externalReferenceTableInfo(emptyHandle); err != nil || present {
		t.Fatalf("empty table after isolate disposal = %v, %v", present, err)
	}

	callback, err := NewCallbackExternalReference(ExternalReferenceFunction)
	if err != nil {
		t.Fatal(err)
	}
	source := []ExternalReference{NewExternalReference(0x1234), callback}
	params := NewCreateParams().SetExternalReferences(source)
	if !params.HasExternalReferences() || params.HasEmptyExternalReferences() {
		t.Fatal("non-empty external-reference state not recorded")
	}
	// SetExternalReferences owns a copy; caller mutation must not alter it.
	source[0], source[1] = ExternalReference{}, ExternalReference{}
	isolate, err := NewIsolateWithParams(params)
	if err != nil {
		t.Fatal(err)
	}
	handle := isolate.handle
	if present, length, first, last, err := externalReferenceTableInfo(handle); err != nil || !present || length != 3 || first != 0x1234 || last != 0 {
		t.Fatalf("appended native table = present:%v length:%d first:%#x last:%#x err:%v", present, length, first, last, err)
	}
	closeExternalReferenceIsolate(t, isolate)
	if present, _, _, _, err := externalReferenceTableInfo(handle); err != nil || present {
		t.Fatalf("native table after isolate disposal = %v, %v", present, err)
	}

	terminatedParams := NewCreateParams().SetExternalReferences([]ExternalReference{
		NewExternalReference(0x4321), NewExternalReference(0),
	})
	terminated, err := NewIsolateWithParams(terminatedParams)
	if err != nil {
		t.Fatal(err)
	}
	if present, length, first, last, err := externalReferenceTableInfo(terminated.handle); err != nil || !present || length != 2 || first != 0x4321 || last != 0 {
		t.Fatalf("pre-terminated native table = present:%v length:%d first:%#x last:%#x err:%v", present, length, first, last, err)
	}
	closeExternalReferenceIsolate(t, terminated)
}

func TestCreateParamsExternalReferencesConcurrentReuse(t *testing.T) {
	callback, err := NewCallbackExternalReference(ExternalReferenceFunction)
	if err != nil {
		t.Fatal(err)
	}
	params := NewCreateParams().SetExternalReferences([]ExternalReference{callback})
	const workers = 4
	errorsOut := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			isolate, err := NewIsolateWithParams(params)
			if err != nil {
				errorsOut <- err
				return
			}
			present, length, first, last, err := externalReferenceTableInfo(isolate.handle)
			if err != nil {
				errorsOut <- err
			} else if !present || length != 2 || first != callback.Address() || last != 0 {
				errorsOut <- &externalReferenceTestError{present, length, first, last}
			}
			if err := ReleaseIsolateHostState(isolate); err != nil {
				errorsOut <- err
			}
			if err := isolate.Close(); err != nil {
				errorsOut <- err
			}
		}()
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		t.Error(err)
	}
}

func TestSnapshotCreatorExternalReferenceTableCleanup(t *testing.T) {
	makeCreator := func() (*SnapshotCreator, uintptr) {
		creator, err := NewSnapshotCreatorWithExternalReferences(nil)
		if err != nil {
			t.Fatal(err)
		}
		handle := creator.Isolate().handle
		present, length, first, last, err := externalReferenceTableInfo(handle)
		if err != nil || !present || length != 1 || first != 0 || last != 0 {
			t.Fatalf("creator table = present:%v length:%d first:%#x last:%#x err:%v", present, length, first, last, err)
		}
		return creator, handle
	}

	creator, creatorHandle := makeCreator()
	context, err := creator.Isolate().NewContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.SetDefaultContext(context); err != nil {
		t.Fatal(err)
	}
	if err := context.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(FunctionCodeClear)
	if err != nil {
		t.Fatal(err)
	}
	if present, _, _, _, err := externalReferenceTableInfo(creatorHandle); err != nil || present {
		t.Fatalf("creator table after CreateBlob = %v, %v", present, err)
	}
	loaded := StartupDataFromBytes(blob.Bytes())
	if isolate, err := NewIsolateFromSnapshot(loaded); err == nil || isolate != nil {
		t.Fatalf("raw snapshot with unknown external-reference requirements = %v, %v", isolate, err)
	}
	loadedIsolate, err := NewIsolateFromSnapshotWithParams(loaded, NewCreateParams().UseEmptyExternalReferences())
	if err != nil {
		t.Fatal(err)
	}
	closeExternalReferenceIsolate(t, loadedIsolate)
	if err := loaded.Release(); err != nil {
		t.Fatal(err)
	}
	if err := blob.Release(); err != nil {
		t.Fatal(err)
	}

	closedCreator, closedHandle := makeCreator()
	if err := closedCreator.Close(); err != nil {
		t.Fatal(err)
	}
	if present, _, _, _, err := externalReferenceTableInfo(closedHandle); err != nil || present {
		t.Fatalf("creator table after Close = %v, %v", present, err)
	}
}

type externalReferenceTestError struct {
	present     bool
	length      int
	first, last uintptr
}

func (e *externalReferenceTestError) Error() string {
	return "unexpected native external-reference table"
}
