//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Snapshot and public-handle benchmarks. The startup shapes mirror
// rust-oracle/benches/startup.rs (same workload, criterion vs `go test`
// harness differences must be accounted for when comparing); the
// snapshot-blob and handle benchmarks measure this slice's new paths:
// serialization (blob creation), snapshot-backed startup versus fresh
// startup, and persistent/weak handle churn (cgo + engine overhead).

// BenchmarkSnapshotBlobCreateDefault mirrors the creator side of
// make_blob(false): a fresh creator isolate whose default context gets a
// tiny payload, serialized with FunctionCodeHandling::Clear. Each
// iteration pays full isolate startup + serialization; the dominant cost
// is the engine's snapshot serialization path.
func BenchmarkSnapshotBlobCreateDefault(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		creator, err := gov8.NewSnapshotCreator()
		if err != nil {
			b.Fatalf("NewSnapshotCreator: %v", err)
		}
		iso := creator.Isolate()
		ctx, err := iso.NewContext()
		if err != nil {
			b.Fatalf("NewContext: %v", err)
		}
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatalf("NewScope: %v", err)
		}
		if _, err := benchEval(b, ctx, scope, "globalThis.a = 7;"); err != nil {
			b.Fatalf("seed: %v", err)
		}
		_ = scope.Close()
		if err := creator.SetDefaultContext(ctx); err != nil {
			b.Fatalf("SetDefaultContext: %v", err)
		}
		_ = ctx.Close()
		blob, err := creator.CreateBlob(gov8.FunctionCodeClear)
		if err != nil {
			b.Fatalf("CreateBlob: %v", err)
		}
		if blob.IsEmpty() {
			b.Fatal("empty blob")
		}
	}
}

// BenchmarkSnapshotIsolateContextFromBlob is the snapshot-backed
// counterpart of startup/isolate_context_new_dispose: isolate + default
// context instantiation from an existing blob.
func BenchmarkSnapshotIsolateContextFromBlob(b *testing.B) {
	blob := snapMakeBlobB(b, gov8.FunctionCodeClear)
	defer func() { _ = blob.Release() }()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		iso, err := gov8.NewIsolateFromSnapshot(blob)
		if err != nil {
			b.Fatalf("NewIsolateFromSnapshot: %v", err)
		}
		ctx, err := iso.NewContext()
		if err != nil {
			b.Fatalf("NewContext: %v", err)
		}
		_ = ctx.Close()
		_ = iso.Close()
	}
}

// BenchmarkSnapshotContextNewFromBlob is the snapshot-backed counterpart
// of startup/context_new_dispose: context instantiation in one persistent
// snapshot-backed isolate (the engine reads the blob for each context).
func BenchmarkSnapshotContextNewFromBlob(b *testing.B) {
	blob := snapMakeBlobB(b, gov8.FunctionCodeClear)
	defer func() { _ = blob.Release() }()
	iso, err := gov8.NewIsolateFromSnapshot(blob)
	if err != nil {
		b.Fatalf("NewIsolateFromSnapshot: %v", err)
	}
	defer func() { _ = iso.Close() }()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, err := iso.NewContext()
		if err != nil {
			b.Fatalf("NewContext: %v", err)
		}
		_ = ctx.Close()
	}
}

// BenchmarkGlobalNewCloneClose measures strong-handle churn: cell
// creation, cloning (a second cell over the same object) and reset.
func BenchmarkGlobalNewCloneClose(b *testing.B) {
	iso, ctx, scope := benchRuntime(b)
	defer func() {
		_ = scope.Close()
		_ = ctx.Close()
		_ = iso.Close()
	}()
	obj, err := benchEval(b, ctx, scope, "({})")
	if err != nil {
		b.Fatalf("object: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g, err := gov8.NewGlobal(scope, obj)
		if err != nil {
			b.Fatalf("NewGlobal: %v", err)
		}
		clone, err := g.Clone()
		if err != nil {
			b.Fatalf("Clone: %v", err)
		}
		if eq, err := g.Equal(clone); err != nil || !eq {
			b.Fatalf("Equal = %v, %v", eq, err)
		}
		_ = clone.Close()
		_ = g.Close()
	}
}

// BenchmarkWeakNewClose measures weak-cell churn without finalizers
// (Global::NewWeak + SetWeak + Reset through the artifact bindings).
func BenchmarkWeakNewClose(b *testing.B) {
	iso, ctx, scope := benchRuntime(b)
	defer func() {
		_ = scope.Close()
		_ = ctx.Close()
		_ = iso.Close()
	}()
	obj, err := benchEval(b, ctx, scope, "({})")
	if err != nil {
		b.Fatalf("object: %v", err)
	}
	g, err := gov8.NewGlobal(scope, obj)
	if err != nil {
		b.Fatalf("NewGlobal: %v", err)
	}
	defer func() { _ = g.Close() }()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w, err := g.NewWeak()
		if err != nil {
			b.Fatalf("NewWeak: %v", err)
		}
		if empty, _ := w.IsEmpty(); empty {
			b.Fatal("weak empty right after creation")
		}
		_ = w.Close()
	}
}

// benchEval compiles and runs src for benchmarks (testing.B-safe eval).
func benchEval(b *testing.B, ctx *gov8.Context, scope *gov8.Scope, src string) (gov8.Value, error) {
	b.Helper()
	script, err := ctx.Compile(scope, src, nil)
	if err != nil {
		return gov8.Value{}, err
	}
	defer func() { _ = script.Close() }()
	return script.Run(scope, nil)
}

// benchRuntime is newTestRuntime for benchmarks.
func benchRuntime(b *testing.B) (*gov8.Isolate, *gov8.Context, *gov8.Scope) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	return iso, ctx, scope
}

// snapMakeBlobB is snapMakeBlob for benchmarks.
func snapMakeBlobB(b *testing.B, keep gov8.FunctionCodeHandling) *gov8.StartupData {
	b.Helper()
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		b.Fatalf("NewSnapshotCreator: %v", err)
	}
	iso := creator.Isolate()
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		b.Fatalf("NewScope: %v", err)
	}
	if _, err := benchEval(b, ctx, scope, "globalThis.a = 7;"); err != nil {
		b.Fatalf("seed: %v", err)
	}
	_ = scope.Close()
	if err := creator.SetDefaultContext(ctx); err != nil {
		b.Fatalf("SetDefaultContext: %v", err)
	}
	_ = ctx.Close()
	blob, err := creator.CreateBlob(keep)
	if err != nil {
		b.Fatalf("CreateBlob: %v", err)
	}
	return blob
}
