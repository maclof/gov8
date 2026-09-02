//go:build windows && amd64

package main

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

// -emit writes this runner's normalized report to a file (for fixture
// regeneration reviews); comparison against the pinned fixture always runs.
var emit = flag.String("emit", "", "write the normalized report to this file")

// TestMain initializes V8 once for the conformance process. The host slice
// deliberately performs no platform shutdown, exactly like the oracle's
// host registry (src/checks/host/mod.rs).
func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		println("gov8 host-promise conformance: Initialize:", err.Error())
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// runPromiseChecks executes the promise checks in registry order and
// renders their normalized JSON-lines report (checks + summary line),
// mirroring oracle::run_host_all's encoding.
func runPromiseChecks(t *testing.T) []string {
	t.Helper()
	var lines []string
	total, passed := 0, 0
	for _, c := range promiseChecks() {
		o := c(t)
		total++
		if o.passed() {
			passed++
		}
		lines = append(lines, o.line())
	}
	lines = append(lines, summaryLine(total, passed, total-passed))
	return lines
}

// summaryLine mirrors report::summary_line.
func summaryLine(total, passed, failed int) string {
	return "{\"summary\":{\"total\":" + strconv.Itoa(total) +
		",\"passed\":" + strconv.Itoa(passed) +
		",\"failed\":" + strconv.Itoa(failed) + "}}"
}

// fixtureLines reads the pinned host fixture.
func fixtureLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// extractFixturePromiseLines returns the fixture's promise check lines in
// registry order plus the fixture summary line.
func extractFixturePromiseLines(t *testing.T) (promiseLines []string, summary string) {
	t.Helper()
	lines := fixtureLines(t)
	if len(lines) < 2 {
		t.Fatalf("fixture too short (%d lines)", len(lines))
	}
	summary = lines[len(lines)-1]
	if !strings.HasPrefix(summary, "{\"summary\":{") {
		t.Fatalf("last fixture line must be the summary: %s", summary)
	}
	byID := make(map[string]string, len(promiseCheckIDs))
	for _, line := range lines[:len(lines)-1] {
		if !strings.HasPrefix(line, "{\"check\":\"") {
			t.Fatalf("bad check line: %s", line)
		}
		for _, id := range promiseCheckIDs {
			if strings.HasPrefix(line, "{\"check\":\""+id+"\",") {
				if _, dup := byID[id]; dup {
					t.Fatalf("duplicate fixture line for %s", id)
				}
				byID[id] = line
			}
		}
	}
	for _, id := range promiseCheckIDs {
		line, ok := byID[id]
		if !ok {
			t.Fatalf("fixture is missing the %s check line", id)
		}
		if !strings.Contains(line, "\"ok\":true") {
			t.Fatalf("fixture must only record passing checks: %s", line)
		}
		promiseLines = append(promiseLines, line)
	}
	return promiseLines, summary
}

// TestHostPromiseFixture runs the promise checks in oracle order and
// compares each normalized line byte-for-byte against the pinned host
// fixture, including the summary line for this slice's subset.
func TestHostPromiseFixture(t *testing.T) {
	got := runPromiseChecks(t)
	if *emit != "" {
		if err := os.WriteFile(*emit, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	wantLines, fixtureSummary := extractFixturePromiseLines(t)
	wantSummary := got[len(got)-1]

	for i := 0; i < len(wantLines); i++ {
		if wantLines[i] != got[i] {
			t.Errorf("line %d diverged from pinned fixture:\n  want: %s\n  got:  %s",
				i+1, wantLines[i], got[i])
		}
	}
	if len(got) != len(wantLines)+1 {
		t.Fatalf("report has %d lines, want %d checks + summary", len(got), len(wantLines))
	}
	if got[len(got)-1] != wantSummary {
		t.Errorf("summary line diverged:\n  want: %s\n  got:  %s", wantSummary, got[len(got)-1])
	}

	// The subset summary must be derivable from the pinned fixture: the
	// fixture's own summary pins 19/19 host checks, of which this slice
	// reproduces the 4 promise checks.
	if !strings.Contains(fixtureSummary, "\"total\":19") ||
		!strings.Contains(fixtureSummary, "\"passed\":19") {
		t.Errorf("unexpected fixture summary %s; the pinned host fixture must record 19/19 checks",
			fixtureSummary)
	}
}

// TestHostFixtureShapeIsSane mirrors tests/conformance_host_fixture.rs
// host_fixture_shape_is_sane for the parts this slice consumes: check lines
// pass, the last line is the summary, and the summary matches the check
// count.
func TestHostFixtureShapeIsSane(t *testing.T) {
	lines := fixtureLines(t)
	if len(lines) < 3 {
		t.Fatalf("fixture must contain checks and a summary (%d lines)", len(lines))
	}
	summary := lines[len(lines)-1]
	if !strings.HasPrefix(summary, "{\"summary\":{") {
		t.Fatalf("last line must be the summary: %s", summary)
	}
	checkLines := lines[:len(lines)-1]
	for _, line := range checkLines {
		if !strings.HasPrefix(line, "{\"check\":\"") {
			t.Fatalf("bad check line: %s", line)
		}
		if !strings.Contains(line, "\"ok\":true") {
			t.Fatalf("fixture must only record passing checks: %s", line)
		}
	}
	total := len(checkLines)
	wantSummary := "{\"summary\":{\"total\":" + strconv.Itoa(total) +
		",\"passed\":" + strconv.Itoa(total) + ",\"failed\":0}}"
	if summary != wantSummary {
		t.Fatalf("summary must match the check count:\n  want: %s\n  got:  %s",
			wantSummary, summary)
	}
}
