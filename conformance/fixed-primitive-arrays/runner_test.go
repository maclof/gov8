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

const fixturePath = "../../rust-oracle/tests/fixtures/conformance-fixed-primitive-arrays-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"
const reportOutEnv = "GOV8_FIXED_PRIMITIVE_ARRAYS_REPORT_OUT"

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
		got := check(t)
		fmt.Fprintf(&b, "{\"check\":%s,\"ok\":true,\"value\":%s}\n", jstr(got.id), got.value)
	}
	fmt.Fprintf(&b, "{\"summary\":{\"total\":%d,\"passed\":%d,\"failed\":0}}\n", len(checks), len(checks))
	return b.String()
}

func TestReportSubprocessHelper(t *testing.T) {
	path := os.Getenv(reportOutEnv)
	if path == "" {
		t.Skip("subprocess helper")
	}
	if err := os.WriteFile(path, []byte(runAll(t)), 0o600); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	return string(report)
}

func TestConformanceFixture(t *testing.T) {
	got := subprocessReport(t)
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("report diverged from pinned fixture\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestReportDeterministic(t *testing.T) {
	first := subprocessReport(t)
	second := subprocessReport(t)
	if first != second {
		t.Fatalf("reports differ\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
