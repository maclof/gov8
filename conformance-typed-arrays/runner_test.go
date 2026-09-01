//go:build windows && amd64

// The conformance-typed-arrays runner: it executes the 14 typed-array checks
// in the fixed oracle order, renders the same normalized JSON-lines report as
// the other slices, and compares the report byte-for-byte against the pinned
// fixture. The shape tests mirror
// rust-oracle/tests/conformance_typed_arrays_fixture.rs.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-typed-arrays-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

// -emit writes the normalized report to a file (for fixture regeneration
// reviews); comparison against the pinned fixture always runs.
var emit = flag.String("emit", "", "write the normalized report to this file")

// TestMain initializes V8 once for the conformance process. Like the pinned
// oracle binary, this slice performs no platform shutdown, so it can be
// verified in-process.
func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		println("gov8 conformance-typed-arrays: Initialize:", err.Error())
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// allChecks is the fixed oracle order (rust-oracle
// src/bin/conformance-typed-arrays.rs CHECKS).
func allChecks() []func(tester) obs {
	return []func(tester) obs{
		checkKindPredicates,
		checkConstants,
		checkElementSizes,
		checkNativeGeometry,
		checkReadBitPatterns,
		checkWriteBitPatterns,
		checkJSViewGeometry,
		checkViewSurface,
		checkCopyContentsBounds,
		checkDetachedView,
		checkDataViewSurface,
		checkSABViews,
		checkJSErrorPaths,
		checkFloat16Availability,
	}
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

// TestConformanceTypedArraysFixture runs all 14 checks in oracle order and
// compares the report byte-for-byte against the pinned fixture.
func TestConformanceTypedArraysFixture(t *testing.T) {
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
		t.Fatalf("report diverged from pinned typed-arrays fixture (%d want lines, %d got lines)",
			len(wantLines), len(gotLines))
	}
}

// TestConformanceTypedArraysFixtureShapeIsSane mirrors
// typed_arrays_fixture_shape_is_sane: at least three lines, every check line
// passing and prefixed with typedarrays/, and a self-consistent summary.
func TestConformanceTypedArraysFixtureShapeIsSane(t *testing.T) {
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
		if !strings.HasPrefix(line, "{\"check\":\"typedarrays/") {
			t.Fatalf("typed-arrays fixture must only contain typedarrays/ checks: %s", line)
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
}

// TestConformanceTypedArraysFixtureCoversAllTwelveKinds mirrors
// typed_arrays_fixture_covers_all_twelve_kinds: every kind must appear as a
// JSON key in the fixture's per-kind rows, so the fixture cannot silently
// drop a kind (e.g. if Float16Array were ever removed upstream).
func TestConformanceTypedArraysFixtureCoversAllTwelveKinds(t *testing.T) {
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fixture := string(want)
	for _, key := range []string{
		"int8", "uint8", "uint8_clamped", "int16", "uint16", "int32",
		"uint32", "float16", "float32", "float64", "bigint64", "biguint64",
	} {
		needle := "\"" + key + "\":{"
		if hits := strings.Count(fixture, needle); hits < 3 {
			t.Fatalf("kind %s under-covered in fixture (%d object rows)", key, hits)
		}
	}
}
