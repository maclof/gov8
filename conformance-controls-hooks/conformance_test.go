//go:build windows && amd64

// The conformance-controls-hooks runner: it executes the 22 checks in the
// fixed oracle order, renders the same normalized JSON-lines report as the
// other slices, and compares the report byte-for-byte against the pinned
// fixture. The shape and determinism tests mirror
// rust-oracle/tests/conformance_controls_hooks_fixture.rs.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-controls-hooks-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

// -emit writes the normalized report to a file (for fixture regeneration
// reviews); comparison against the pinned fixture always runs.
var emit = flag.String("emit", "", "write the normalized report to this file")

// TestMain initializes V8 once for the conformance process — but NOT in a
// subprocess mode: the mode bodies fully control their own setup order
// (missing --expose-gc, fatal handler before Initialize, pre-init flag
// strings), which is part of what they characterize.
func TestMain(m *testing.M) {
	if os.Getenv(gov8ModeEnv) != "" {
		os.Exit(m.Run())
	}
	// The in-process checks need the full contractual setup (flags,
	// entropy, --expose-gc) BEFORE Initialize; ensureSetup runs it exactly
	// once on first use, but the Initialize step itself must not race the
	// lifecycle checks, so run the setup eagerly here.
	ensureSetup(mainT{})
	os.Exit(m.Run())
}

// mainT adapts *testing.M-time reporting to the tester interface.
type mainT struct{}

func (mainT) Helper() {}

func (mainT) Fatalf(format string, args ...interface{}) {
	println("gov8 conformance-controls-hooks: " + fmt.Sprintf(format, args...))
	os.Exit(1)
}

func (mainT) Errorf(format string, args ...interface{}) {
	println("gov8 conformance-controls-hooks: " + fmt.Sprintf(format, args...))
}

// runAll runs every check in the fixed oracle order and renders the
// normalized JSON-lines report (checks + summary line). Like the oracle
// binary, the report is only valid for a FRESH process (the pre-init setup
// order and the entropy replacement are one-shot process state), so the
// tests run it in a dedicated subprocess via the sub-run-all mode.
func runAll(t tester) string {
	t.Helper()
	var sb strings.Builder
	total, passed := 0, 0
	for _, c := range checks {
		for _, o := range c(t) {
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
	}
	summary := fmt.Sprintf("{\"summary\":{\"total\":%d,\"passed\":%d,\"failed\":%d}}",
		total, passed, total-passed)
	sb.WriteString(summary)
	sb.WriteByte('\n')
	return sb.String()
}

// runFreshReport spawns a child process running the full check registry in
// a fresh V8 process and returns the rendered report (the Go analog of the
// oracle fixture tests' run_binary()).
func runFreshReport(t *testing.T) string {
	t.Helper()
	outPath := ""
	for i := 0; ; i++ {
		outPath = fmt.Sprintf("%s/ch-report-%d.jsonl", os.TempDir(), i)
		if _, err := os.Stat(outPath); os.IsNotExist(err) {
			break
		}
	}
	t.Cleanup(func() { _ = os.Remove(outPath) })
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run", "^TestCHSubMode$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		gov8ModeEnv+"=sub-run-all",
		"GOV8_CH_REPORT_OUT="+outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh report subprocess failed: %v\n%s", err, out)
	}
	report, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read subprocess report: %v\n%s", err, out)
	}
	return string(report)
}

// TestConformanceFixture runs all 22 checks in a fresh subprocess (the Go
// analog of the oracle's run_binary()) and compares the report byte-for-byte
// against the pinned fixture.
func TestConformanceFixture(t *testing.T) {
	report := runFreshReport(t)

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
		t.Fatalf("report diverged from pinned controls-hooks fixture (%d want lines, %d got lines)",
			len(wantLines), len(gotLines))
	}
}

// TestConformanceFixtureShapeIsSane mirrors
// controls_hooks_fixture_shape_is_sane: check lines pass, the last line is
// the summary, and the summary matches the check count. (Unlike the other
// slices, the oracle fixture contains a repeated check id — the full and
// minor gc-request lines — so uniqueness is intentionally NOT asserted.)
func TestConformanceFixtureShapeIsSane(t *testing.T) {
	report := runFreshReport(t)
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
		if !strings.HasPrefix(line, "{\"check\":\"controls/") {
			t.Fatalf("controls-hooks fixture must only contain controls/ checks: %s", line)
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

// TestConformanceReportDeterministicAcrossRuns pins the binary-output
// determinism contract (controls_hooks_binary_is_deterministic_across_runs):
// two fresh runs of the registry produce identical reports.
func TestConformanceReportDeterministicAcrossRuns(t *testing.T) {
	first := runFreshReport(t)
	second := runFreshReport(t)
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
