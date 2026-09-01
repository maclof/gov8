//go:build windows && amd64

package gov8_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Negative and fatal-misuse characterization for the core-advanced slice,
// mirroring rust-oracle/tests/core_advanced_negative.rs:
//
//   - Locker entry guards fire before any engine state changes, so they are
//     plain Go errors in-process (the pinned crate panics; this module's
//     documented panic-to-error deviation) and the isolate stays usable.
//   - Weak creation on a shared isolate is NOT rejected by this port: the
//     guard lives in the weak-handle implementation outside this slice's
//     file ownership and is tracked as a gap for the parity matrix (the
//     conversion-side rejection below covers the fixture-observable path).
//   - Module-flagged origins fed to a classic compile and corrupted code
//     caches reaching the deserializer are engine FATALS in this pinned
//     build. Each probe runs in a dedicated subprocess (spawned via
//     os.Executable with a -test.run filter, the Go analog of the oracle's
//     current_exe probes) and the parent asserts abnormal termination with
//     V8's deterministic fatal text instead of a Go panic.
//
// Unsupported (documented, not exercised): v8::SealHandleScope has no
// binding in the pinned crate at all — there is nothing observable to pin,
// and this module deliberately ships no API for it.

// --- in-process guards ---------------------------------------------------------

// TestLockerDoubleLockOnOneThreadFails mirrors
// locker_double_lock_on_one_thread_panics: the recursive lock is refused,
// the isolate keeps running JS, and a clean lock afterwards works.
func TestLockerDoubleLockOnOneThreadFails(t *testing.T) {
	iso := newIso(t)
	shared, err := iso.TryIntoShared()
	if err != nil {
		t.Fatalf("TryIntoShared: %v", err)
	}
	locker, err := shared.Lock()
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	_, secondErr := shared.Lock()
	if secondErr == nil {
		t.Fatal("second lock succeeded")
	}
	if !strings.Contains(secondErr.Error(), "already locked by this thread") {
		t.Fatalf("second lock error = %q", secondErr.Error())
	}
	// The guard fired before any state change: the locker still owns the
	// isolate and can run JS.
	if got := lockedEvalUnderLocker(t, locker, "5 * 5"); got != 25 {
		t.Errorf("still usable = %d, want 25", got)
	}
	if err := locker.Close(); err != nil {
		t.Fatalf("locker Close: %v", err)
	}
	// A clean lock afterwards works.
	if got := lockedEval(t, shared, "6 * 7"); got != 42 {
		t.Errorf("clean relock = %d, want 42", got)
	}
	if err := shared.Close(); err != nil {
		t.Fatalf("shared Close: %v", err)
	}
}

// lockedEvalUnderLocker runs source under an already-held locker.
func lockedEvalUnderLocker(t *testing.T, locker *gov8.Locker, source string) int64 {
	t.Helper()
	iso := locker.Isolate()
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope.Close() }()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer func() { _ = ctx.Close() }()
	n, ok := evalInt(t, scope, ctx, source)
	if !ok {
		t.Fatal("eval failed")
	}
	return n
}

// TestLockerLockWhileAnotherIsolateEnteredFails mirrors
// locker_lock_while_another_isolate_entered_panics.
func TestLockerLockWhileAnotherIsolateEnteredFails(t *testing.T) {
	sharedIso := newIso(t)
	shared, err := sharedIso.TryIntoShared()
	if err != nil {
		t.Fatalf("TryIntoShared: %v", err)
	}
	// A second (owned, entered) isolate on this thread.
	entered := newIso(t)
	_, lockErr := shared.Lock()
	if lockErr == nil {
		t.Fatal("lock succeeded with another isolate entered")
	}
	if !strings.Contains(lockErr.Error(), "while another isolate is entered") {
		t.Fatalf("lock error = %q", lockErr.Error())
	}
	// Reverse creation order teardown; the shared isolate was never locked.
	if err := entered.Close(); err != nil {
		t.Fatalf("entered Close: %v", err)
	}
	if err := shared.Close(); err != nil {
		t.Fatalf("shared Close: %v", err)
	}
}

// --- subprocess fatal probes --------------------------------------------------------

// probeEnv is the environment marker the parent sets for probe processes;
// without it the probe body is a no-op (so a plain `go test` run never
// fatals).
func probeBody(t *testing.T, name string) bool {
	t.Helper()
	if os.Getenv("GOV8_PROBE") != name {
		return false
	}
	return true
}

// runProbe spawns this test binary for one probe test and returns the exit
// error plus stderr.
func runProbe(t *testing.T, probeName string) (string, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run", "^"+probeName+"$", "-test.v=false")
	cmd.Env = append(os.Environ(), "GOV8_PROBE="+probeName)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestModuleOriginClassicCompileIsEngineFatal mirrors
// module_origin_classic_compile_is_v8_fatal.
func TestModuleOriginClassicCompileIsEngineFatal(t *testing.T) {
	stderr, err := runProbe(t, "TestProbeModuleOriginClassicCompile")
	if err == nil {
		t.Fatalf("module-origin classic compile unexpectedly survived; output:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Check failed") && !strings.Contains(stderr, "Fatal") {
		t.Fatalf("expected a V8 ApiCheck fatal; output:\n%s", stderr)
	}
	if !strings.Contains(stderr, "CompileModule must be used to compile modules") {
		t.Fatalf("expected the pinned ApiCheck message; output:\n%s", stderr)
	}
}

// TestProbeModuleOriginClassicCompile is the child process of
// TestModuleOriginClassicCompileIsEngineFatal. It must never run in a
// normal test pass (the env marker gates it).
func TestProbeModuleOriginClassicCompile(t *testing.T) {
	if !probeBody(t, "TestProbeModuleOriginClassicCompile") {
		t.Skip("probe body")
	}
	iso := newIso(t)
	ctx := newCtx(t, iso)
	scope := newScope(t, iso)
	// is_module = true with a classic compile: the engine ApiCheck-fatals
	// inside the call ("CompileModule must be used to compile modules").
	origin := &gov8.Origin{ResourceName: "m.js", IsModule: true}
	script, err := ctx.CompileWithOrigin(scope, "export const x = 1;", origin, nil)
	if err == nil {
		_ = script.Close()
	}
	println("probe:survived")
}

// TestCodeCacheCorruptionIsEngineFatal mirrors
// code_cache_corruption_is_v8_fatal: a mid-payload flip passes the header
// sanity precheck (release V8 skips checksum verification of code caches)
// and the deserializer fatal-aborts on "unreachable code".
func TestCodeCacheCorruptionIsEngineFatal(t *testing.T) {
	stderr, err := runProbe(t, "TestProbeCodeCacheCorruption")
	if err == nil {
		t.Fatalf("corrupted code cache unexpectedly survived; output:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Fatal") && !strings.Contains(stderr, "Check failed") {
		t.Fatalf("expected a V8 fatal; output:\n%s", stderr)
	}
}

// TestProbeCodeCacheCorruption is the child process of
// TestCodeCacheCorruptionIsEngineFatal.
func TestProbeCodeCacheCorruption(t *testing.T) {
	if !probeBody(t, "TestProbeCodeCacheCorruption") {
		t.Skip("probe body")
	}
	const source = benchFibIIFESource
	origin := &gov8.Origin{ResourceName: "cached.js"}

	var cache []byte
	func() {
		producer := newIso(t)
		defer func() { _ = producer.Close() }()
		pctx := newCtx(t, producer)
		defer func() { _ = pctx.Close() }()
		pscope := newScope(t, producer)
		defer func() { _ = pscope.Close() }()
		unbound, err := pctx.CompileUnbound(pscope, source, origin, gov8.OptNoCompileOptions, nil)
		if err != nil {
			t.Fatalf("producer compile: %v", err)
		}
		defer func() { _ = unbound.Close() }()
		cache, err = unbound.CreateCodeCache()
		if err != nil {
			t.Fatalf("CreateCodeCache: %v", err)
		}
	}()

	// Mid-cache flip (header checks stay intact; the payload is corrupt).
	flip := len(cache) / 2
	cache[flip] ^= 0xFF

	consumer := newIso(t)
	defer func() { _ = consumer.Close() }()
	cctx := newCtx(t, consumer)
	defer func() { _ = cctx.Close() }()
	cscope := newScope(t, consumer)
	defer func() { _ = cscope.Close() }()

	// The header precheck passes (documented residual boundary): the
	// deserializer below is what aborts.
	if status, err := consumer.CheckCodeCache(cache); err != nil || status != 0 {
		t.Fatalf("precheck unexpectedly rejected the flipped cache: %d (%v)", status, err)
	}
	script, rejected, err := cctx.CompileCached(cscope, source, origin, cache, nil)
	if err == nil {
		_ = script.Close()
		_ = rejected
	}
	println("probe:survived")
}
