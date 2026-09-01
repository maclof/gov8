//go:build windows && amd64

package gov8_test

import (
	"runtime"
	"strings"
	"sync"
	"testing"

	gov8 "gov8"
)

func cpsSnapshotBlob(t testing.TB, marker int) *gov8.StartupData {
	t.Helper()
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatal(err)
	}
	iso := creator.Isolate()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	script, err := ctx.Compile(scope, "globalThis.snapshotMarker = "+cpsDecimal(marker), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := script.Run(scope, nil); err != nil {
		t.Fatal(err)
	}
	if err := script.Close(); err != nil {
		t.Fatal(err)
	}
	if err := creator.SetDefaultContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(gov8.FunctionCodeKeep)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func cpsDecimal(value int) string {
	if value == 0 {
		return "0"
	}
	var reversed [20]byte
	n := 0
	for value > 0 {
		reversed[n] = byte('0' + value%10)
		n++
		value /= 10
	}
	var result strings.Builder
	result.Grow(n)
	for n > 0 {
		n--
		result.WriteByte(reversed[n])
	}
	return result.String()
}

func TestSnapshotCreateParamsSingleUseAndMutableView(t *testing.T) {
	blob := cpsSnapshotBlob(t, 21)
	defer func() {
		if err := blob.Release(); err != nil {
			t.Error(err)
		}
	}()
	params, err := gov8.NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	iso, err := gov8.NewIsolateWithSnapshotParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if !params.Consumed() {
		t.Fatal("params did not report consumed")
	}
	// The embedded builder intentionally remains an ordinary mutable value;
	// consumption guards creation, not subsequent getter observations.
	params.SetCodeRangeSizeInBytes(123)
	if got := params.CodeRangeSizeInBytes(); got != 123 {
		t.Fatalf("post-consume getter = %d", got)
	}
	if _, err := gov8.NewIsolateWithSnapshotParams(params); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("second creation error = %v", err)
	}
	if err := params.SetSnapshotBlob(blob); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("post-consume replacement error = %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotCreateParamsSafetyValidationConsumes(t *testing.T) {
	if _, err := gov8.NewSnapshotCreateParams(gov8.StartupDataFromBytes(nil)); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty snapshot error = %v", err)
	}
	blob := cpsSnapshotBlob(t, 21)
	defer func() { _ = blob.Release() }()
	params, err := gov8.NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := params.ConfigureHeapLimits(64<<20, 32<<20); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("inverted limits error = %v", err)
	}
	if params.Consumed() {
		t.Fatal("setter validation consumed params")
	}
	params.SetStackLimit(1)
	if _, err := gov8.NewIsolateWithSnapshotParams(params); err == nil || !strings.Contains(err.Error(), "Go stack") {
		t.Fatalf("unsafe stack error = %v", err)
	}
	if !params.Consumed() {
		t.Fatal("constructor validation failure did not consume params")
	}
}

func TestSnapshotCreateParamsRawBlobRequiresExplicitReferences(t *testing.T) {
	produced := cpsSnapshotBlob(t, 21)
	raw := gov8.StartupDataFromBytes(produced.Bytes())
	if err := produced.Release(); err != nil {
		t.Fatal(err)
	}
	params, err := gov8.NewSnapshotCreateParams(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gov8.NewIsolateWithSnapshotParams(params); err == nil || !strings.Contains(err.Error(), "requirements are unknown") {
		t.Fatalf("unknown external-reference error = %v", err)
	}
	if !params.Consumed() {
		t.Fatal("failed raw-blob constructor did not consume params")
	}

	explicit, err := gov8.NewSnapshotCreateParams(raw)
	if err != nil {
		t.Fatal(err)
	}
	explicit.UseEmptyExternalReferences()
	iso, err := gov8.NewIsolateWithSnapshotParams(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if err := raw.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotBlobReleaseTracksLiveConsumer(t *testing.T) {
	blob := cpsSnapshotBlob(t, 21)
	params, err := gov8.NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	iso, err := gov8.NewIsolateWithSnapshotParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if err := blob.Release(); err == nil || !strings.Contains(err.Error(), "still in use") {
		t.Fatalf("live Release error = %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if err := blob.Release(); err != nil {
		t.Fatal(err)
	}
	if err := blob.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestSnapshotIsolateWrongThread(t *testing.T) {
	blob := cpsSnapshotBlob(t, 21)
	defer func() { _ = blob.Release() }()
	params, err := gov8.NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	iso, err := gov8.NewIsolateWithSnapshotParams(params)
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := iso.NewContext()
		errCh <- err
	}()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotBlobConcurrentIndependentConsumers(t *testing.T) {
	blob := cpsSnapshotBlob(t, 21)
	const consumers = 4
	errs := make(chan error, consumers)
	var wg sync.WaitGroup
	for range consumers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			params, err := gov8.NewSnapshotCreateParams(blob)
			if err != nil {
				errs <- err
				return
			}
			iso, err := gov8.NewIsolateWithSnapshotParams(params)
			if err != nil {
				errs <- err
				return
			}
			ctx, err := iso.NewContext()
			if err == nil {
				err = ctx.Close()
			}
			if closeErr := iso.Close(); err == nil {
				err = closeErr
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := blob.Release(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkCreateParamsSnapshotConsume(b *testing.B) {
	blob := cpsSnapshotBlob(b, 21)
	b.Cleanup(func() { _ = blob.Release() })
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		params, err := gov8.NewSnapshotCreateParams(blob)
		if err != nil {
			b.Fatal(err)
		}
		iso, err := gov8.NewIsolateWithSnapshotParams(params)
		if err != nil {
			b.Fatal(err)
		}
		ctx, err := iso.NewContext()
		if err != nil {
			b.Fatal(err)
		}
		if err := ctx.Close(); err != nil {
			b.Fatal(err)
		}
		if err := iso.Close(); err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(params)
	}
}
