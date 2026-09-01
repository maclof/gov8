//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "gov8"
)

func TestRuntimeValuesResidualWellKnownSymbols(t *testing.T) {
	_, _, scope := newTestRuntime(t)
	getters := []func() (*gov8.Symbol, error){
		scope.GetAsyncIteratorSymbol,
		scope.GetIsConcatSpreadableSymbol,
		scope.GetMatchSymbol,
		scope.GetReplaceSymbol,
		scope.GetSearchSymbol,
		scope.GetSplitSymbol,
		scope.GetToPrimitiveSymbol,
		scope.GetUnscopablesSymbol,
	}
	for _, get := range getters {
		first, err := get()
		if err != nil {
			t.Fatal(err)
		}
		second, err := get()
		if err != nil {
			t.Fatal(err)
		}
		equal, err := first.StrictEquals(second.Value)
		if err != nil || !equal {
			t.Fatalf("well-known Symbol identity = %v, %v", equal, err)
		}
	}
}

func TestRuntimeValuesResidualSafety(t *testing.T) {
	iso, _, scope := newTestRuntime(t)
	if _, err := scope.PrivateForApi(gov8.Value{}); err == nil {
		t.Fatal("PrivateForApi accepted absent name despite pinned access violation")
	}

	foreign, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	foreignScope, err := foreign.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	foreignName, err := foreignScope.NewString("foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.PrivateForApi(foreignName); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign PrivateForApi error = %v", err)
	}
	if err := foreignScope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}

	closedScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if err := closedScope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closedScope.GetMatchSymbol(); err == nil {
		t.Fatal("well-known Symbol getter accepted closed scope")
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := scope.GetMatchSymbol()
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Fatalf("wrong-thread getter error = %v", err)
	}
}
