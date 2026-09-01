//go:build windows && amd64

// Negative/edge-case characterization for the process/isolate controls &
// hooks runner, mirroring rust-oracle/tests/controls_hooks_negative.rs.
// These paths abort the process (fatal CHECKs and the controlled fatal
// OOM), so each one is observed by spawning this test binary in a dedicated
// subprocess mode. Every heap-pressure subprocess caps its heap with
// NewIsolateWithLimits(0, 10 MiB): the OOMs here are the intended, bounded
// fatal path, never uncontrolled process growth.
//
// Exit codes are compared in the Rust-normalized signed representation
// (Rust's ExitStatus::code() reports 0x80000003 as -2147483645).
package main

import (
	"strings"
	"testing"
)

// chStatusBreakpointUnsigned is 0x80000003 in Go's ExitCode representation.
const chStatusBreakpointUnsigned = 2147483651

// chSpawnMode spawns this test binary for one mode and returns stdout,
// stderr, and the exit code (a thin typed wrapper over spawnSelf).
func chSpawnMode(t *testing.T, mode string) (string, string, int) {
	t.Helper()
	return spawnSelf(t, mode)
}

// TestUnknownModeIsCleanFailure mirrors unknown_mode_is_a_clean_failure.
func TestUnknownModeIsCleanFailure(t *testing.T) {
	stdout, stderr, code := chSpawnMode(t, "bogus-mode")
	_ = stdout
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "unknown mode: bogus-mode") {
		t.Fatalf("stderr:\n%s", stderr)
	}
}

// TestInvalidFlagsPreinitPrintToStderrAndAreIgnored mirrors
// unrecognized_flags_preinit_print_to_stderr_and_are_ignored.
func TestInvalidFlagsPreinitPrintToStderrAndAreIgnored(t *testing.T) {
	stdout, stderr, code := chSpawnMode(t, "sub-invalid-flag-preinit")
	if code != 0 {
		t.Fatalf("unrecognized flags before initialize must not abort; exit %d; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Error: unrecognized flag --definitely-not-a-real-flag") {
		t.Fatalf("stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Try --help for options") {
		t.Fatalf("stderr:\n%s", stderr)
	}
	// The recognized flag in the same string still took effect, and the
	// isolate evaluates normally.
	if !strings.Contains(stdout, "RESULT result=2 gc_type=1") {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

// TestNearHeapLimitShrinkForcesControlledOom mirrors
// shrinking_near_heap_limit_callback_forces_controlled_oom.
func TestNearHeapLimitShrinkForcesControlledOom(t *testing.T) {
	stdout, stderr, code := chSpawnMode(t, "sub-near-heap-limit-shrink")
	if !strings.Contains(stderr, "SHRINK call=1 current=4194304") {
		t.Fatalf("stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, `OOM location="Reached heap limit" is_heap_oom=true`) {
		t.Fatalf("stderr:\n%s", stderr)
	}
	if strings.Contains(stdout, "SURVIVED") {
		t.Fatalf("shrunk limit must end in fatal OOM; stdout:\n%s", stdout)
	}
	if code != chStatusBreakpointUnsigned {
		t.Fatalf("exit code = %d, want %d (STATUS_BREAKPOINT); stderr:\n%s",
			code, chStatusBreakpointUnsigned, stderr)
	}
}

// TestHeapOOMWithoutHandlersUsesDefaultFatalPath mirrors
// heap_oom_without_handlers_uses_default_fatal_path.
func TestHeapOOMWithoutHandlersUsesDefaultFatalPath(t *testing.T) {
	stdout, stderr, code := chSpawnMode(t, "sub-oom-default")
	if !strings.Contains(stderr, "MARK:before-loop") {
		t.Fatalf("stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "# Fatal JavaScript out of memory: Reached heap limit") {
		t.Fatalf("stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "OOM location=") {
		t.Fatalf("no embedder handlers were installed; stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "FATAL file=") {
		t.Fatalf("no embedder handlers were installed; stderr:\n%s", stderr)
	}
	if strings.Contains(stdout, "SURVIVED") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	if code != chStatusBreakpointUnsigned {
		t.Fatalf("exit code = %d, want %d (STATUS_BREAKPOINT); stderr:\n%s",
			code, chStatusBreakpointUnsigned, stderr)
	}
}
