//go:build windows && amd64

// Package lifecycle exercises the process-global V8 state machine and the
// full real shutdown path. It lives in its own package so `go test ./...`
// runs it in a dedicated process: Initialize/Dispose are one-shot per
// process, so the whole state machine must be walked in one ordered test,
// mirroring the oracle's one-binary-per-negative-test approach.
package lifecycle_test

import (
	"strings"
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestStateMachineAndFullShutdown(t *testing.T) {
	// --- positive initialize --------------------------------------------
	if err := gov8.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !gov8.PlatformPresent() {
		t.Fatal("platform not present after Initialize")
	}

	// --- double initialize (pinned crate: panic "Invalid global state") --
	err := gov8.Initialize()
	if err == nil || !strings.Contains(err.Error(), "invalid global state") {
		t.Fatalf("second Initialize = %v, want invalid-global-state error", err)
	}

	// --- engine work before shutdown --------------------------------------
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	script, err := ctx.Compile(scope, "7 * 6", nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if txt, _ := res.ToString(ctx); txt != "42" {
		t.Fatalf("result = %q", txt)
	}
	for _, c := range []interface{ Close() error }{scope, ctx, script, iso} {
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	// --- dispose ordering: DisposePlatform before Dispose must fail --------
	if err := gov8.DisposePlatform(); err == nil {
		t.Fatal("DisposePlatform before Dispose must fail")
	}

	// --- Dispose is refused while isolates are live --------------------------
	live, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate (live probe): %v", err)
	}
	if _, err := gov8.Dispose(); err == nil {
		t.Fatal("Dispose with a live isolate must fail")
	} else if !strings.Contains(err.Error(), "live isolate") {
		t.Fatalf("Dispose with live isolate = %v, want a live-isolate error", err)
	}
	// The platform must still be usable after the refusal.
	if !gov8.PlatformPresent() {
		t.Fatal("platform presence lost after refused Dispose")
	}
	if err := live.Close(); err != nil {
		t.Fatalf("live.Close: %v", err)
	}

	// --- concurrent isolate create/teardown must drain the registry ---------
	// Exercises the NewIsolate/Dispose synchronization and the live-isolate
	// accounting under -race; Dispose below only succeeds if every isolate
	// created here was registered and closed exactly once.
	const workers = 6
	const perWorker = 3
	var wg sync.WaitGroup
	stormErrs := make([][]error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				iso, err := gov8.NewIsolate()
				if err != nil {
					stormErrs[w] = append(stormErrs[w], err)
					continue
				}
				ctx, err := iso.NewContext()
				if err == nil {
					scope, serr := iso.NewScope()
					if serr == nil {
						if script, cerr := ctx.Compile(scope, "6 * 7", nil); cerr == nil {
							if _, rerr := script.Run(scope, nil); rerr != nil {
								stormErrs[w] = append(stormErrs[w], rerr)
							}
							if err := script.Close(); err != nil {
								stormErrs[w] = append(stormErrs[w], err)
							}
						} else {
							stormErrs[w] = append(stormErrs[w], cerr)
						}
						if err := scope.Close(); err != nil {
							stormErrs[w] = append(stormErrs[w], err)
						}
					} else {
						stormErrs[w] = append(stormErrs[w], serr)
					}
					if err := ctx.Close(); err != nil {
						stormErrs[w] = append(stormErrs[w], err)
					}
				} else {
					stormErrs[w] = append(stormErrs[w], err)
				}
				if err := iso.Close(); err != nil {
					stormErrs[w] = append(stormErrs[w], err)
				}
			}
		}(w)
	}
	wg.Wait()
	for w, list := range stormErrs {
		for j, err := range list {
			if err != nil {
				t.Fatalf("storm worker %d iteration %d: %v", w, j, err)
			}
		}
	}

	// --- Dispose returns true exactly once ---------------------------------
	disposed, err := gov8.Dispose()
	if err != nil {
		t.Fatalf("Dispose: %v", err)
	}
	if !disposed {
		t.Fatal("Dispose returned false (oracle: dispose_returns_true)")
	}
	if _, err := gov8.Dispose(); err == nil {
		t.Fatal("second Dispose must fail (pinned crate: panic)")
	}
	if gov8.PlatformPresent() {
		t.Fatal("platform presence flag must still reflect installation state")
	}

	// --- wrapper refuses engine work after dispose --------------------------
	// Every isolate created above was closed, so Dispose succeeded and the
	// wrapper's lifecycle state machine now refuses all engine-touching
	// entry points before they can reach the disposed engine.
	if err := gov8.DisposePlatform(); err != nil {
		t.Fatalf("DisposePlatform: %v", err)
	}
	if err := gov8.DisposePlatform(); err == nil {
		t.Fatal("second DisposePlatform must fail")
	}
	if gov8.PlatformPresent() {
		t.Fatal("platform still present after DisposePlatform")
	}
	if _, err := gov8.NewIsolate(); err == nil {
		t.Fatal("NewIsolate after shutdown must fail")
	}
	if _, err := gov8.EngineVersion(); err != nil {
		// Version metadata is wrapper state and stays readable.
		t.Fatalf("EngineVersion after shutdown: %v", err)
	}
}
