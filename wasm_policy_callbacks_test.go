//go:build windows && amd64

package gov8_test

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	gov8 "github.com/maclof/gov8"
)

func wpcRuntime(t testing.TB, setup func(*gov8.Isolate) error) (*gov8.Isolate, *gov8.Context, *gov8.Scope) {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		if err := setup(iso); err != nil {
			t.Fatal(err)
		}
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	return iso, ctx, scope
}

func wpcEval(t testing.TB, ctx *gov8.Context, scope *gov8.Scope, source string) gov8.Value {
	t.Helper()
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	value, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func wpcClose(t testing.TB, iso *gov8.Isolate, ctx *gov8.Context, scope *gov8.Scope) {
	t.Helper()
	if err := scope.Close(); err != nil {
		t.Error(err)
	}
	if err := ctx.Close(); err != nil {
		t.Error(err)
	}
	if err := gov8.ReleaseIsolateHostState(iso); err != nil {
		t.Error(err)
	}
	if err := iso.Close(); err != nil {
		t.Error(err)
	}
}

func TestWasmPolicyCallbackValidationAndAffinity(t *testing.T) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if err := iso.SetAllowWasmCodeGenerationCallback(nil); err == nil {
		t.Fatal("nil allow callback accepted")
	}
	if err := iso.SetWasmAsyncResolvePromiseCallback(nil); err == nil {
		t.Fatal("nil async callback accepted")
	}
	errs := make(chan error, 1)
	go func() {
		errs <- iso.SetAllowWasmCodeGenerationCallback(func(*gov8.CallbackScope, gov8.Value) bool { return true })
	}()
	if err := <-errs; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.SetAllowWasmCodeGenerationCallback(func(*gov8.CallbackScope, gov8.Value) bool { return true }); err == nil {
		t.Fatal("closed isolate accepted callback")
	}
}

func TestWasmPolicyCallbackLifetimeAndActiveRelease(t *testing.T) {
	var retained *gov8.WasmAsyncResolution
	var releaseErr error
	iso, ctx, scope := wpcRuntime(t, func(iso *gov8.Isolate) error {
		return iso.SetWasmAsyncResolvePromiseCallback(func(resolution *gov8.WasmAsyncResolution) {
			retained = resolution
			releaseErr = gov8.ReleaseIsolateHostState(iso)
			if _, err := resolution.Settle(); err != nil {
				panic(err)
			}
		})
	})
	promise := gov8.Promise{Value: wpcEval(t, ctx, scope, "WebAssembly.compile(new Uint8Array([0,97,115,109,1,0,0,0]))")}
	deadline := time.Now().Add(10 * time.Second)
	for retained == nil {
		if time.Now().After(deadline) {
			t.Fatal("callback timeout")
		}
		ran, err := iso.PumpMessageLoop(false)
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			runtime.Gosched()
		}
	}
	if releaseErr == nil || !strings.Contains(releaseErr.Error(), "active wasm callback") {
		t.Fatalf("reentrant release error = %v", releaseErr)
	}
	if _, err := retained.Settle(); err == nil {
		t.Fatal("retained resolution remained usable")
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatal(err)
	}
	if state, err := promise.State(); err != nil || state != gov8.PromiseFulfilled {
		t.Fatalf("promise state = %v, %v", state, err)
	}
	wpcClose(t, iso, ctx, scope)
}

func TestWasmPolicyCallbackPanicsAbort(t *testing.T) {
	if mode := os.Getenv("GOV8_WPC_PANIC"); mode != "" {
		iso, ctx, scope := wpcRuntime(t, func(iso *gov8.Isolate) error {
			if mode == "allow" {
				return iso.SetAllowWasmCodeGenerationCallback(func(*gov8.CallbackScope, gov8.Value) bool {
					fmt.Fprintln(os.Stderr, "marker:wpc-allow-entered")
					panic("wpc-allow-panic")
				})
			}
			return iso.SetWasmAsyncResolvePromiseCallback(func(*gov8.WasmAsyncResolution) {
				fmt.Fprintln(os.Stderr, "marker:wpc-async-entered")
				panic("wpc-async-panic")
			})
		})
		fmt.Fprintln(os.Stderr, "marker:wpc-before")
		if mode == "allow" {
			_ = wpcEval(t, ctx, scope, "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))")
		} else {
			_ = wpcEval(t, ctx, scope, "WebAssembly.compile(new Uint8Array([0,97,115,109,1,0,0,0]))")
			for {
				_, _ = iso.PumpMessageLoop(false)
				runtime.Gosched()
			}
		}
		fmt.Fprintln(os.Stderr, "marker:wpc-after")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"allow", "async"} {
		cmd := exec.Command(exe, "-test.run=^TestWasmPolicyCallbackPanicsAbort$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOV8_WPC_PANIC="+mode)
		out, err := cmd.CombinedOutput()
		text := string(out)
		for _, marker := range []string{"marker:wpc-before", "marker:wpc-" + mode + "-entered", "wpc-" + mode + "-panic"} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s missing %q; output:\n%s", mode, marker, text)
			}
		}
		if strings.Contains(text, "marker:wpc-after") {
			t.Errorf("%s returned after panic", mode)
		}
		exit, ok := err.(*exec.ExitError)
		if !ok || uint32(exit.ExitCode()) != 0xC0000409 {
			t.Fatalf("%s exit = %v, want 0xC0000409; output:\n%s", mode, err, text)
		}
	}
}

func BenchmarkAllowWasmCodeGenerationCallback(b *testing.B) {
	iso, ctx, scope := wpcRuntime(b, func(iso *gov8.Isolate) error {
		return iso.SetAllowWasmCodeGenerationCallback(func(*gov8.CallbackScope, gov8.Value) bool { return true })
	})
	b.Cleanup(func() { wpcClose(b, iso, ctx, scope) })
	source := "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		child, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		script, err := ctx.Compile(child, source, nil)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := script.Run(child, nil); err != nil {
			b.Fatal(err)
		}
		if err := script.Close(); err != nil {
			b.Fatal(err)
		}
		if err := child.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
