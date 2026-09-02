//go:build windows && amd64

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../../rust-oracle/tests/fixtures/conformance-exceptions-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

const (
	stackBoundaryCheck = "exceptions-advanced/stack/current_frames_and_limits"
	unsafeRustBoundary = "\"index_equal_count_some\":true"
	safeGoBoundary     = "\"index_equal_count_some\":false"
)

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		fmt.Fprintln(os.Stderr, "Initialize:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func runAll(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, check := range checks {
		outcome := check(t)
		b.WriteString("{\"check\":\"")
		b.WriteString(outcome.id)
		b.WriteString("\",\"ok\":true,\"value\":")
		b.WriteString(jsonString(outcome.value))
		b.WriteString("}\n")
	}
	fmt.Fprintf(&b, "{\"summary\":{\"total\":%d,\"passed\":%d,\"failed\":0}}\n", len(checks), len(checks))
	return b.String()
}

const reportOutEnv = "GOV8_EXCEPTIONS_ADVANCED_REPORT_OUT"

// TestReportSubprocessHelper runs exactly one report per process. This mirrors
// the Rust fixture's process-isolated determinism check and matters for V8's
// append-only message-listener registration.
func TestReportSubprocessHelper(t *testing.T) {
	path := os.Getenv(reportOutEnv)
	if path == "" {
		t.Skip("subprocess helper")
	}
	if err := os.WriteFile(path, []byte(runAll(t)), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
}

func subprocessReport(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "\\report.jsonl"
	cmd := exec.Command(os.Args[0], "-test.run=^TestReportSubprocessHelper$")
	cmd.Env = append(os.Environ(), reportOutEnv+"="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("report subprocess: %v\n%s", err, output)
	}
	report, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read subprocess report: %v", err)
	}
	return string(report)
}

// normalizeOracleFrameBoundary applies the sole intentional fixture
// deviation. A Rust subprocess probe observed get_frame(frame_count) return
// Some and then access-violate (0xC0000005) on dereference in 8/8 runs. Go
// rejects that index before the frame-getter FFI, so all four live-stack
// captures truthfully report false. Every other byte remains oracle-owned.
func normalizeOracleFrameBoundary(report string) (string, error) {
	lines := strings.SplitAfter(report, "\n")
	found := false
	for i, line := range lines {
		if !strings.Contains(line, "\"check\":\""+stackBoundaryCheck+"\"") {
			if strings.Contains(line, unsafeRustBoundary) {
				return "", fmt.Errorf("unsafe frame boundary appeared outside %s", stackBoundaryCheck)
			}
			continue
		}
		if found {
			return "", fmt.Errorf("duplicate %s fixture line", stackBoundaryCheck)
		}
		found = true
		if count := strings.Count(line, unsafeRustBoundary); count != 4 {
			return "", fmt.Errorf("%s boundary count = %d, want 4", stackBoundaryCheck, count)
		}
		lines[i] = strings.ReplaceAll(line, unsafeRustBoundary, safeGoBoundary)
	}
	if !found {
		return "", fmt.Errorf("missing %s fixture line", stackBoundaryCheck)
	}
	return strings.Join(lines, ""), nil
}

func TestConformanceFixture(t *testing.T) {
	got := subprocessReport(t)
	rawWant, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	want, err := normalizeOracleFrameBoundary(string(rawWant))
	if err != nil {
		t.Fatalf("normalize fixture: %v", err)
	}
	if got != want {
		wantLines := strings.Split(strings.TrimRight(want, "\n"), "\n")
		gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
			var w, g string
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if w != g {
				t.Errorf("line %d:\nwant %s\n got %s", i+1, w, g)
			}
		}
		t.Fatal("report diverged from pinned exceptions-advanced fixture")
	}
}

func TestFrameCountBoundarySafetyDeviationIsNarrow(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	normalized, err := normalizeOracleFrameBoundary(string(raw))
	if err != nil {
		t.Fatalf("normalize fixture: %v", err)
	}
	if strings.Count(string(raw), unsafeRustBoundary) != 4 {
		t.Fatal("Rust fixture must retain all four unsafe Some observations")
	}
	if strings.Count(normalized, safeGoBoundary) != 4 || strings.Contains(normalized, unsafeRustBoundary) {
		t.Fatal("normalization must change exactly the four frame-count boundary observations")
	}
	actual := subprocessReport(t)
	if strings.Count(actual, safeGoBoundary) != 4 || strings.Contains(actual, unsafeRustBoundary) {
		t.Fatal("Go report must truthfully record four safe boundary rejections")
	}
}

func TestReportDeterministic(t *testing.T) {
	first := subprocessReport(t)
	second := subprocessReport(t)
	if first != second {
		firstLines := strings.Split(first, "\n")
		secondLines := strings.Split(second, "\n")
		for i := 0; i < len(firstLines) && i < len(secondLines); i++ {
			if firstLines[i] != secondLines[i] {
				t.Fatalf("reports differ at line %d:\nfirst  %s\nsecond %s", i+1, firstLines[i], secondLines[i])
			}
		}
		t.Fatal("reports differ in length")
	}
}
