//go:build windows && amd64

// The conformance-object-ops runner: it executes the 25 checks in the fixed
// oracle order, renders the same normalized JSON-lines report as the other
// slices, and compares the report byte-for-byte against the pinned fixture.
// The shape, order, and determinism tests mirror
// rust-oracle/tests/conformance_object_ops_fixture.rs.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../../rust-oracle/tests/fixtures/conformance-object-ops-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

// -emit writes the normalized report to a file (for fixture regeneration
// reviews); comparison against the pinned fixture always runs.
var emit = flag.String("emit", "", "write the normalized report to this file")

// TestMain initializes V8 once for the conformance process. Like the pinned
// oracle binary, this slice performs no platform shutdown, so it can be
// verified in-process.
func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		println("gov8 conformance-object-ops: Initialize:", err.Error())
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
	for _, c := range checks {
		o := c(t)
		total++
		var line string
		if o.passed() {
			passed++
			line = "{\"check\":\"" + o.id + "\",\"ok\":true,\"value\":" + jsonString(o.got) + "}"
		} else {
			line = "{\"check\":\"" + o.id + "\",\"ok\":false,\"expected\":" +
				jsonString(o.want) + ",\"actual\":" + jsonString(o.got) + "}"
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

// TestConformanceFixture runs all 25 checks in oracle order and compares the
// report byte-for-byte against the pinned fixture.
func TestConformanceFixture(t *testing.T) {
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
		t.Fatalf("report diverged from pinned object-ops fixture (%d want lines, %d got lines)",
			len(wantLines), len(gotLines))
	}
}

// TestConformanceFixtureShapeIsSane mirrors
// conformance_object_ops_fixture_shape_is_sane: check lines pass, the last
// line is the summary, the summary matches the check count, every check id
// carries the obj-ops/ prefix, and every id is unique.
func TestConformanceFixtureShapeIsSane(t *testing.T) {
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
	ids := make(map[string]bool, len(checkLines))
	for _, line := range checkLines {
		if !strings.HasPrefix(line, "{\"check\":\"obj-ops/") {
			t.Fatalf("object-ops fixture must only contain obj-ops/ checks: %s", line)
		}
		if !strings.Contains(line, "\"ok\":true") {
			t.Fatalf("fixture must only record passing checks: %s", line)
		}
		start := len("{\"check\":\"")
		end := strings.IndexByte(line[start:], '"') + start
		id := line[start:end]
		if ids[id] {
			t.Fatalf("duplicate check id: %s", id)
		}
		ids[id] = true
	}
	total := len(checkLines)
	wantSummary := "{\"summary\":{\"total\":" + strconv.Itoa(total) +
		",\"passed\":" + strconv.Itoa(total) + ",\"failed\":0}}"
	if summary != wantSummary {
		t.Fatalf("summary must match the check count:\n  want: %s\n  got:  %s",
			wantSummary, summary)
	}
}

// TestConformanceFixtureCoversAllAreasInOrder mirrors
// conformance_object_ops_fixture_covers_all_areas_in_order: prototype,
// property, identity, receiver, lazy, call, convert, instanceof, equality,
// typeof, predicates, Data, residual conversions, and residual helper checks
// appear exactly in the documented order.
func TestConformanceFixtureCoversAllAreasInOrder(t *testing.T) {
	report := runAll(t)
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	expectedPrefixes := []string{
		"obj-ops/proto/",
		"obj-ops/property/",
		"obj-ops/identity/",
		"obj-ops/receiver/",
		"obj-ops/lazy/",
		"obj-ops/call/",
		"obj-ops/convert/",
		"obj-ops/instanceof/",
		"obj-ops/equality/",
		"obj-ops/typeof/",
		"obj-ops/predicates/",
		"obj-ops/data/predicates_and_identity",
		"obj-ops/convert/residual_locals",
		"obj-ops/predicates/module_namespace_and_type_repr",
	}
	positions := make([]int, 0, len(expectedPrefixes))
	for _, prefix := range expectedPrefixes {
		found := -1
		for i, line := range lines {
			if strings.Contains(line, prefix) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("every area must be covered: %s missing", prefix)
		}
		positions = append(positions, found)
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] < positions[i-1] {
			t.Fatalf("areas must appear in the documented group order: %v", positions)
		}
	}
}

// TestConformanceReportDeterministicAcrossRuns pins the binary-output
// determinism contract: two full runs of the registry produce identical
// reports.
func TestConformanceReportDeterministicAcrossRuns(t *testing.T) {
	first := runAll(t)
	second := runAll(t)
	if first != second {
		fl := strings.Split(first, "\n")
		sl := strings.Split(second, "\n")
		for i := 0; i < len(fl) && i < len(sl); i++ {
			if fl[i] != sl[i] {
				t.Errorf("line %d differs across runs:\n  first:  %s\n  second: %s", i+1, fl[i], sl[i])
			}
		}
		t.Fatal("reports differ across runs")
	}
}
