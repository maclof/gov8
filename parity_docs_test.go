//go:build windows && amd64

package gov8_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParityDocumentationTracksFixtureCount(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("rust-oracle", "tests", "fixtures", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, path := range fixtures {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), `"check":`) {
				total++
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			t.Fatal(scanErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if total == 0 {
		t.Fatal("no Rust fixture checks found")
	}
	needle := fmt.Sprintf("%d normalized", total)
	for _, path := range []string{"README.md", "PARITY.md"} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), needle) {
			t.Errorf("%s does not contain current fixture count %q", path, needle)
		}
	}
}

func TestVerificationScriptListsEveryGoConformancePackage(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("scripts", "verify_windows.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || (entry.Name() != "conformance" && !strings.HasPrefix(entry.Name(), "conformance-")) {
			continue
		}
		goFiles, err := filepath.Glob(filepath.Join(entry.Name(), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(goFiles) == 0 {
			continue
		}
		needle := "'./" + entry.Name() + "'"
		if !strings.Contains(string(script), needle) {
			t.Errorf("scripts/verify_windows.ps1 does not list %s", entry.Name())
		}
	}
}
