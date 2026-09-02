//go:build windows && amd64

package gov8

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/maclof/gov8/internal/prebuilt"
)

func TestEmbeddedShimMaterializesAndLoads(t *testing.T) {
	path, err := prebuilt.Materialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := prebuilt.Verify(path); err != nil {
		t.Fatal(err)
	}
	dll, err := loadShimDLL(path)
	if err != nil {
		t.Fatalf("LoadDLL(%s): %v", path, err)
	}
	defer dll.Release()
	abiProc, err := dll.FindProc("gov8_abi_version")
	if err != nil {
		t.Fatal(err)
	}
	abi, _, _ := abiProc.Call()
	if abi != shimABIVersion {
		t.Fatalf("embedded DLL ABI = %d, want %d", abi, shimABIVersion)
	}
}

func TestEmbeddedShimRepairsCorruptCache(t *testing.T) {
	root := t.TempDir()
	path, err := prebuilt.Materialize(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	repaired, err := prebuilt.Materialize(root)
	if err != nil {
		t.Fatal(err)
	}
	if repaired != path {
		t.Fatalf("repaired path = %q, want %q", repaired, path)
	}
	if err := prebuilt.Verify(repaired); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedShimConcurrentMaterialization(t *testing.T) {
	root := t.TempDir()
	const workers = 8
	paths := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := prebuilt.Materialize(root)
			paths <- path
			errs <- err
		}()
	}
	wg.Wait()
	close(paths)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(root, "gov8", "shim", "abi-44", prebuilt.SHA256, "gov8_shim.dll")
	for path := range paths {
		if path != want {
			t.Fatalf("materialized path = %q, want %q", path, want)
		}
	}
}
