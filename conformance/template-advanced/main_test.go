//go:build windows && amd64

// Package main implements the gov8 template-advanced conformance runner.
//
// It re-implements the 14 advanced template/object checks of the pinned Rust
// oracle binary (rust-oracle/src/bin/conformance-template-advanced.rs, in the
// binary's fixed order) on top of the Go binding and compares every rendered
// check line byte-for-byte against the pinned fixture
// rust-oracle/tests/fixtures/conformance-template-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl.
//
// Checks: named/indexed property interceptor families and flags,
// ReturnValue.Get/specials, signatures, intrinsic data properties,
// constructor behavior and prototype controls, template inheritance,
// accessor-shaped properties, internal-field/aligned-pointer boundaries,
// security tokens (the crate's whole access-check surface — no access-check
// callback exists in the pinned crate and none is invented), the
// call-as-function handler, and immutable prototypes.
//
// The oracle's process-fatal boundaries (out-of-range embedder data tags,
// non-uint32 indexed enumerator elements) are characterized out-of-process by
// rust-oracle/tests/template_advanced_negative.rs; the Go wrapper
// prevalidates the tag bounds instead (see the PARITY matrix deviations).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../../rust-oracle/tests/fixtures/conformance-template-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

// -emit writes the normalized report to a file (for fixture regeneration
// reviews); comparison against the pinned fixture always runs.
var emit = flag.String("emit", "", "write the normalized report to this file")

// TestMain initializes V8 once for the conformance process.
func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		println("gov8 template-advanced conformance: Initialize:", err.Error())
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// advCheck is one oracle check rendered in the fixture's fixed order.
type advCheck struct {
	id string
	fn func(t *testing.T, r *runtime) string // returns the rendered value JSON
}

// TestTemplateAdvancedFixtureSubset runs the 14 checks in oracle order,
// compares each rendered line byte-for-byte against the pinned fixture, and
// prints the normalized report.
func TestTemplateAdvancedFixtureSubset(t *testing.T) {
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
	for _, c := range allAdvChecks() {
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
		t.Fatalf("template-advanced subset: %d/%d checks match the pinned fixture", passed, total)
	}
}
