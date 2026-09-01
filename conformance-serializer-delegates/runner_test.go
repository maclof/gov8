//go:build windows && amd64

package main

// Exact-output conformance runner for the serializer/deserializer delegate
// slice. The checks run in the fixed oracle registry order and their
// normalized JSON-lines report is compared byte-for-byte against the
// pinned fixture
// (rust-oracle/tests/fixtures/conformance-serializer-delegates-v8_152.2.0_x86_64-pc-windows-msvc.jsonl).
// Panic/fatal delegate boundaries are characterized out-of-process on the
// Go side by the root module's serializer_delegates_negative_test.go.

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-serializer-delegates-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

// -emit writes this runner's normalized report to a file (for fixture
// regeneration reviews); comparison against the pinned fixture always runs.
var emit = flag.String("emit", "", "write the normalized report to this file")

// TestMain initializes V8 once for the conformance process. This slice
// performs no platform shutdown, exactly like the oracle's
// serializer-delegates runner.
func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		println("gov8 serializer-delegates conformance: Initialize:", err.Error())
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// runSerdelChecks executes the 25 checks in registry order and renders
// their normalized JSON-lines report (checks + summary line), mirroring
// the oracle runner's output.
func runSerdelChecks(t *testing.T) []string {
	t.Helper()
	var lines []string
	total, passed := 0, 0
	for _, c := range allSerdelChecks() {
		o := c.fn(t)
		total++
		if !o.fail {
			passed++
		} else {
			t.Logf("check %s failed:\n  want: %s\n  got:  %s", c.id, jsonString(o.want), jsonString(o.val))
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

// fixtureLines reads the pinned fixture.
func fixtureLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// extractFixtureSerdelLines returns the fixture's 25 check lines in
// registry order (verified complete and passing) plus the summary line.
func extractFixtureSerdelLines(t *testing.T) (checkLines []string, summary string) {
	t.Helper()
	lines := fixtureLines(t)
	if len(lines) < 2 {
		t.Fatalf("fixture too short (%d lines)", len(lines))
	}
	summary = lines[len(lines)-1]
	if !strings.HasPrefix(summary, "{\"summary\":{") {
		t.Fatalf("last fixture line must be the summary: %s", summary)
	}
	ids := serdelCheckIDs()
	byID := make(map[string]string, len(ids))
	for _, line := range lines[:len(lines)-1] {
		if !strings.HasPrefix(line, "{\"check\":\"") {
			t.Fatalf("bad check line: %s", line)
		}
		for _, id := range ids {
			if strings.HasPrefix(line, "{\"check\":\""+id+"\",") {
				if _, dup := byID[id]; dup {
					t.Fatalf("duplicate fixture line for %s", id)
				}
				byID[id] = line
			}
		}
	}
	for _, id := range ids {
		line, ok := byID[id]
		if !ok {
			t.Fatalf("fixture is missing the %s check line", id)
		}
		if !strings.Contains(line, "\"ok\":true") {
			t.Fatalf("fixture must only record passing checks: %s", line)
		}
		checkLines = append(checkLines, line)
	}
	return checkLines, summary
}

// TestSerdelFixture runs the 25 checks in oracle order and compares each
// normalized line byte-for-byte against the pinned fixture, including the
// summary line.
func TestSerdelFixture(t *testing.T) {
	got := runSerdelChecks(t)
	if *emit != "" {
		if err := os.WriteFile(*emit, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	wantLines, _ := extractFixtureSerdelLines(t)

	if len(got) != len(wantLines)+1 {
		t.Fatalf("report has %d lines, want %d checks + summary", len(got), len(wantLines))
	}
	for i := 0; i < len(wantLines); i++ {
		if wantLines[i] != got[i] {
			t.Errorf("line %d diverged from pinned fixture:\n  want: %s\n  got:  %s",
				i+1, wantLines[i], got[i])
		}
	}
	wantSummary := summaryLine(len(wantLines), len(wantLines), 0)
	if got[len(got)-1] != wantSummary {
		t.Errorf("summary line diverged:\n  want: %s\n  got:  %s", wantSummary, got[len(got)-1])
	}
}

// TestSerdelFixtureShapeIsSane mirrors
// tests/conformance_serializer_delegates_fixture.rs
// serdel_fixture_shape_is_sane: check lines pass, the last line is the
// summary, the summary matches the check count, and every check id carries
// the serdel/ prefix.
func TestSerdelFixtureShapeIsSane(t *testing.T) {
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
		if !strings.HasPrefix(line, "{\"check\":\"serdel/") {
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

// TestSerdelReportDeterministicAcrossRuns mirrors the oracle's deterministic
// binary output property: two full runs of the registry produce identical
// reports.
func TestSerdelReportDeterministicAcrossRuns(t *testing.T) {
	first := runSerdelChecks(t)
	second := runSerdelChecks(t)
	if len(first) != len(second) {
		t.Fatalf("run lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("line %d differs across runs:\n  first:  %s\n  second: %s",
				i+1, first[i], second[i])
		}
	}
}
