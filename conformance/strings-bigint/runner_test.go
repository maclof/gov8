//go:build windows && amd64

// The conformance-strings-bigint runner: it executes the 16 advanced
// string/BigInt checks in the fixed oracle order, renders the same
// normalized JSON-lines report as the other slices, and compares the
// report byte-for-byte against the pinned fixture. The shape test mirrors
// rust-oracle/tests/conformance_strings_bigint_fixture.rs.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../../rust-oracle/tests/fixtures/conformance-strings-bigint-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

// -emit writes the normalized report to a file (for fixture regeneration
// reviews); comparison against the pinned fixture always runs.
var emit = flag.String("emit", "", "write the normalized report to this file")

// TestMain initializes V8 once for the conformance process. Like the pinned
// oracle binary, this slice performs no platform shutdown, so it can be
// verified in-process.
func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		println("gov8 conformance-strings-bigint: Initialize:", err.Error())
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// runAll runs every check in the fixed oracle order and renders the
// normalized JSON-lines report (checks + summary line).
func runAll(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	total, passed := 0, 0
	for _, c := range allChecks() {
		o := c(t)
		total++
		var line string
		if !o.fail {
			passed++
			line = "{\"check\":" + jsonString(s(o.id)) + ",\"ok\":true,\"value\":" + jsonString(o.val) + "}"
		} else {
			line = "{\"check\":" + jsonString(s(o.id)) + ",\"ok\":false,\"expected\":" +
				jsonString(o.want) + ",\"actual\":" + jsonString(o.val) + "}"
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	summary := fmt.Sprintf("{\"summary\":{\"total\":%d,\"passed\":%d,\"failed\":%d}}",
		total, passed, total-passed)
	sb.WriteString(summary)
	sb.WriteByte('\n')
	return sb.String()
}

// TestConformanceStringsBigIntFixture runs all 16 checks in oracle order
// and compares the report byte-for-byte against the pinned fixture.
func TestConformanceStringsBigIntFixture(t *testing.T) {
	report := runAll(t)

	if *emit != "" {
		if err := os.WriteFile(*emit, []byte(report), 0o644); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}
	if report != string(want) {
		// Report every differing line for fast diagnosis.
		wantLines := strings.Split(strings.TrimRight(string(want), "\n"), "\n")
		gotLines := strings.Split(strings.TrimRight(report, "\n"), "\n")
		for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
			var w, g string
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if w != g {
				t.Errorf("line %d:\n  want: %s\n  got:  %s", i+1, w, g)
			}
		}
		t.Fatalf("report diverged from pinned strings-bigint fixture (%d want lines, %d got lines)",
			len(wantLines), len(gotLines))
	}
}

// TestConformanceStringsBigIntFixtureShapeIsSane mirrors
// strings_bigint_fixture_shape_is_sane: at least three lines, every check
// line passing and prefixed with strings/ or bigint/, a self-consistent
// summary, and all strings/ checks preceding all bigint/ checks.
func TestConformanceStringsBigIntFixtureShapeIsSane(t *testing.T) {
	report := runAll(t)
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("fixture must contain checks and a summary; got %d lines", len(lines))
	}
	summary := lines[len(lines)-1]
	if !strings.HasPrefix(summary, "{\"summary\":{") {
		t.Fatalf("last line must be the summary: %s", summary)
	}
	checkLines := lines[:len(lines)-1]
	for _, line := range checkLines {
		if !strings.HasPrefix(line, "{\"check\":\"strings/") && !strings.HasPrefix(line, "{\"check\":\"bigint/") {
			t.Fatalf("strings-bigint fixture must only contain strings/ or bigint/ checks: %s", line)
		}
		if !strings.Contains(line, "\"ok\":true") {
			t.Fatalf("fixture must only record passing checks: %s", line)
		}
	}
	total := len(checkLines)
	expectedSummary := fmt.Sprintf("{\"summary\":{\"total\":%d,\"passed\":%d,\"failed\":0}}", total, total)
	if summary != expectedSummary {
		t.Fatalf("summary must match the check count: %s != %s", summary, expectedSummary)
	}
	lastStrings, firstBigint := -1, -1
	for idx, line := range checkLines {
		if strings.HasPrefix(line, "{\"check\":\"strings/") {
			lastStrings = idx
		}
		if firstBigint < 0 && strings.HasPrefix(line, "{\"check\":\"bigint/") {
			firstBigint = idx
		}
	}
	if lastStrings >= 0 && firstBigint >= 0 && lastStrings > firstBigint {
		t.Fatalf("strings/ checks must precede bigint/ checks in the report")
	}
}
