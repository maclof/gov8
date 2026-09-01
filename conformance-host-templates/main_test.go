//go:build windows && amd64

// Package main implements the gov8 host-template/callback/accessor/external
// conformance runner.
//
// It re-implements the 13 template/callback/accessor/external checks of the
// pinned Rust host oracle (rust-oracle/src/checks/host, in the oracle's
// fixed order) on top of the Go binding and compares every rendered check
// line byte-for-byte against the pinned fixture
// rust-oracle/tests/fixtures/conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl.
//
// The promise and lifetime checks of the same fixture belong to other
// feature slices and are intentionally not exercised here; this runner's
// summary line therefore reports 13 checks (the fixture's own summary line
// covers the full 18-check registry and is not part of the comparison).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

// -emit writes the normalized report to a file (for fixture regeneration
// reviews); comparison against the pinned fixture always runs.
var emit = flag.String("emit", "", "write the normalized report to this file")

// TestMain initializes V8 once for the conformance process.
func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		println("gov8 host conformance: Initialize:", err.Error())
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// hostCheck is one oracle check rendered in the fixture's fixed order.
type hostCheck struct {
	id string
	fn func(t *testing.T, r *runtime) string // returns the rendered value JSON
}

// TestHostFixtureSubset runs the 13 checks in oracle order, compares each
// rendered line byte-for-byte against the pinned fixture, and prints the
// normalized report.
func TestHostFixtureSubset(t *testing.T) {
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}
	wantLines := make(map[string]string)
	for _, line := range strings.Split(strings.TrimRight(string(fixture), "\n"), "\n") {
		const key = "{\"check\":\""
		if !strings.HasPrefix(line, key) {
			continue
		}
		rest := line[len(key):]
		end := strings.Index(rest, "\",")
		if end < 0 {
			t.Fatalf("malformed fixture line: %s", line)
		}
		wantLines[rest[:end]] = line
	}

	var sb strings.Builder
	total, passed := 0, 0
	for _, c := range allHostChecks() {
		r := newRuntime(t)
		val := c.fn(t, r)
		r.close(t)
		total++
		line := "{\"check\":" + jsonString(jstr(c.id)) + ",\"ok\":true,\"value\":" + val + "}"
		want, pinned := wantLines[c.id]
		if !pinned {
			t.Errorf("check %s has no fixture line; got: %s", c.id, line)
			continue
		}
		if line == want {
			passed++
		} else {
			t.Errorf("check %s diverged from pinned fixture:\n  want: %s\n  got:  %s",
				c.id, want, line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	summary := fmt.Sprintf("{\"summary\":{\"total\":%d,\"passed\":%d,\"failed\":%d}}",
		total, passed, total-passed)
	sb.WriteString(summary)
	sb.WriteByte('\n')

	if *emit != "" {
		if err := os.WriteFile(*emit, []byte(sb.String()), 0o644); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
	if passed != total {
		t.Fatalf("host subset: %d/%d checks match the pinned fixture", passed, total)
	}
}
