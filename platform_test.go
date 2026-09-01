//go:build windows && amd64

package gov8_test

import (
	"math"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestRunIdleTasksBoundariesAndContinuedUse(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	for _, seconds := range []float64{0, 0.002, math.Inf(-1), -1, math.NaN(), math.Inf(1)} {
		if err := iso.RunIdleTasks(seconds); err != nil {
			t.Fatalf("RunIdleTasks(%v): %v", seconds, err)
		}
	}
	script, err := ctx.Compile(scope, "6 * 7 === 42", nil)
	if err != nil {
		t.Fatalf("Compile after idle boundaries: %v", err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Errorf("script.Close: %v", err)
		}
	}()
	value, err := script.Run(scope, nil)
	if err != nil {
		t.Fatalf("Run after idle boundaries: %v", err)
	}
	got, err := value.BooleanValue()
	if err != nil || !got {
		t.Fatalf("BooleanValue = %v, %v", got, err)
	}
}

func TestRunIdleTasksLifecycleAndAffinity(t *testing.T) {
	if err := (*gov8.Isolate)(nil).RunIdleTasks(0); err == nil {
		t.Fatal("nil isolate must fail")
	}
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- iso.RunIdleTasks(0) }()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread error = %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := iso.RunIdleTasks(0); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("after-close error = %v", err)
	}
}

func TestConfigurePlatformRejectsLateSelection(t *testing.T) {
	err := gov8.ConfigurePlatform(gov8.PlatformOptions{Kind: gov8.PlatformDefault})
	if err == nil || !strings.Contains(err.Error(), "before Initialize") {
		t.Fatalf("ConfigurePlatform after Initialize = %v", err)
	}
}
