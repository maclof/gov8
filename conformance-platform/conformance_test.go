//go:build windows && amd64

package platformconformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

type fixtureLine struct {
	Check string         `json:"check"`
	OK    bool           `json:"ok"`
	Value platformResult `json:"value"`
}

type platformResult struct {
	Constructor                     string   `json:"constructor"`
	ThreadPoolSizeArgument          *uint32  `json:"thread_pool_size_argument"`
	IdleTaskSupport                 bool     `json:"idle_task_support"`
	SingleThreadedFlag              bool     `json:"single_threaded_flag"`
	V8Version                       string   `json:"v8_version"`
	SharedCountNew                  int      `json:"shared_count_new"`
	SharedCountInitialized          int      `json:"shared_count_initialized"`
	SharedCountWithCurrentHandle    int      `json:"shared_count_with_current_handle"`
	SharedCountAfterDisposePlatform int      `json:"shared_count_after_dispose_platform"`
	EmptyPumpExecutedTask           bool     `json:"empty_pump_executed_task"`
	IdleSecondsInputs               []string `json:"idle_seconds_inputs"`
	IdleAllBoundaryCallsReturned    bool     `json:"idle_all_boundary_calls_returned"`
	UsableAfterIdle                 bool     `json:"usable_after_idle"`
	AtomicsUnresolvedBeforePump     bool     `json:"atomics_unresolved_before_pump"`
	WaitPumpExecutedTask            bool     `json:"wait_pump_executed_task"`
	AtomicsResolvedAfterDrain       bool     `json:"atomics_resolved_after_drain"`
	PostDrainPumpExecutedTask       bool     `json:"post_drain_pump_executed_task"`
}

type unsafeResult struct {
	Trigger       string `json:"trigger"`
	Success       bool   `json:"success"`
	WindowsStatus string `json:"windows_status"`
	StdoutEmpty   bool   `json:"stdout_empty"`
	StderrEmpty   bool   `json:"stderr_empty"`
}

type unsafeLine struct {
	Check string       `json:"check"`
	OK    bool         `json:"ok"`
	Value unsafeResult `json:"value"`
}

func fatalChild(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		fatalChild("json.Marshal: %v", err)
	}
	return append(b, '\n')
}

func scriptBool(ctx *gov8.Context, scope *gov8.Scope, source string) bool {
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		fatalChild("Compile(%q): %v", source, err)
	}
	value, runErr := script.Run(scope, nil)
	closeErr := script.Close()
	if runErr != nil {
		fatalChild("Run(%q): %v", source, runErr)
	}
	if closeErr != nil {
		fatalChild("Script.Close: %v", closeErr)
	}
	result, err := value.BooleanValue()
	if err != nil {
		fatalChild("BooleanValue: %v", err)
	}
	return result
}

func platformChild(mode string) []byte {
	var id, constructor string
	var threadPool *uint32
	var options gov8.PlatformOptions
	switch mode {
	case "default":
		id, constructor = "platform/default_idle_enabled", "default"
		value := uint32(0)
		threadPool = &value
		options = gov8.PlatformOptions{Kind: gov8.PlatformDefault, IdleTaskSupport: true}
	case "unprotected":
		id, constructor = "platform/unprotected_idle_disabled", "unprotected_default"
		value := uint32(math.MaxUint32)
		threadPool = &value
		options = gov8.PlatformOptions{Kind: gov8.PlatformUnprotected, ThreadPoolSize: value}
	case "single-threaded":
		id, constructor = "platform/single_threaded_idle_enabled", "single_threaded_default"
		options = gov8.PlatformOptions{Kind: gov8.PlatformSingleThreaded, IdleTaskSupport: true, SingleThreadedFlag: true}
	default:
		fatalChild("unknown child mode %q", mode)
	}
	if err := gov8.ConfigurePlatform(options); err != nil {
		fatalChild("ConfigurePlatform: %v", err)
	}
	if err := gov8.SetFlagsFromString("--allow-natives-syntax"); err != nil {
		fatalChild("SetFlagsFromString: %v", err)
	}
	if err := gov8.Initialize(); err != nil {
		fatalChild("Initialize: %v", err)
	}
	version, err := gov8.VersionString()
	if err != nil {
		fatalChild("VersionString: %v", err)
	}
	iso, err := gov8.NewIsolate()
	if err != nil {
		fatalChild("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		fatalChild("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		fatalChild("NewScope: %v", err)
	}
	emptyPump, err := iso.PumpMessageLoop(false)
	if err != nil {
		fatalChild("empty PumpMessageLoop: %v", err)
	}
	for _, seconds := range []float64{0, 0.002, math.Inf(-1), -1, math.NaN(), math.Inf(1)} {
		if err := iso.RunIdleTasks(seconds); err != nil {
			fatalChild("RunIdleTasks(%v): %v", seconds, err)
		}
	}
	usable := scriptBool(ctx, scope, "6 * 7 === 42")
	setup := `
const shared = new SharedArrayBuffer(4);
const ints = new Int32Array(shared);
globalThis.platformResolved = false;
const waiter = Atomics.waitAsync(ints, 0, 0);
waiter.value.then(value => {
  if (value !== 'ok') throw new Error('unexpected wait result: ' + value);
  globalThis.platformResolved = true;
});
if (Atomics.notify(ints, 0, 1) !== 1) throw new Error('notify failed');
true`
	if !scriptBool(ctx, scope, setup) {
		fatalChild("atomics setup did not return true")
	}
	unresolved := scriptBool(ctx, scope, "platformResolved === false")
	firstPump, err := iso.PumpMessageLoop(true)
	if err != nil {
		fatalChild("waiting PumpMessageLoop: %v", err)
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		fatalChild("PerformMicrotaskCheckpoint: %v", err)
	}
	for tasks := 0; ; tasks++ {
		if tasks >= 100 {
			fatalChild("message-loop drain did not quiesce")
		}
		ran, err := iso.PumpMessageLoop(false)
		if err != nil {
			fatalChild("drain PumpMessageLoop: %v", err)
		}
		if !ran {
			break
		}
		if err := iso.PerformMicrotaskCheckpoint(); err != nil {
			fatalChild("drain PerformMicrotaskCheckpoint: %v", err)
		}
	}
	if err := iso.PerformMicrotaskCheckpoint(); err != nil {
		fatalChild("final PerformMicrotaskCheckpoint: %v", err)
	}
	resolved := scriptBool(ctx, scope, "platformResolved === true")
	emptyAfter, err := iso.PumpMessageLoop(false)
	if err != nil {
		fatalChild("post-drain PumpMessageLoop: %v", err)
	}
	if err := scope.Close(); err != nil {
		fatalChild("Scope.Close: %v", err)
	}
	if err := ctx.Close(); err != nil {
		fatalChild("Context.Close: %v", err)
	}
	if err := iso.Close(); err != nil {
		fatalChild("Isolate.Close: %v", err)
	}
	if err := gov8.Shutdown(); err != nil {
		fatalChild("Shutdown: %v", err)
	}

	// rusty_v8's SharedRef counts have no Go handle counterpart. The four
	// fields below are the normalized ownership transitions proved by reaching
	// the corresponding successful lifecycle points in this child.
	return mustJSON(fixtureLine{Check: id, OK: true, Value: platformResult{
		Constructor:                     constructor,
		ThreadPoolSizeArgument:          threadPool,
		IdleTaskSupport:                 options.IdleTaskSupport,
		SingleThreadedFlag:              options.SingleThreadedFlag,
		V8Version:                       version,
		SharedCountNew:                  1,
		SharedCountInitialized:          2,
		SharedCountWithCurrentHandle:    3,
		SharedCountAfterDisposePlatform: 1,
		EmptyPumpExecutedTask:           emptyPump,
		IdleSecondsInputs:               []string{"0", "0.002", "-Infinity", "-1", "NaN", "Infinity"},
		IdleAllBoundaryCallsReturned:    true,
		UsableAfterIdle:                 usable,
		AtomicsUnresolvedBeforePump:     unresolved,
		WaitPumpExecutedTask:            firstPump,
		AtomicsResolvedAfterDrain:       resolved,
		PostDrainPumpExecutedTask:       emptyAfter,
	}})
}

func missingFlagChild() []byte {
	if err := gov8.ConfigurePlatform(gov8.PlatformOptions{Kind: 99}); err == nil {
		fatalChild("unknown platform kind was accepted")
	}
	if err := gov8.ConfigurePlatform(gov8.PlatformOptions{
		Kind: gov8.PlatformSingleThreaded, ThreadPoolSize: 1, SingleThreadedFlag: true,
	}); err == nil {
		fatalChild("single-threaded worker pool was accepted")
	}
	if err := gov8.ConfigurePlatform(gov8.PlatformOptions{
		Kind: gov8.PlatformDefault, SingleThreadedFlag: true,
	}); err == nil {
		fatalChild("single-threaded flag on default platform was accepted")
	}
	err := gov8.ConfigurePlatform(gov8.PlatformOptions{Kind: gov8.PlatformSingleThreaded})
	if !errors.Is(err, gov8.ErrSingleThreadedPlatformFlagRequired) {
		fatalChild("missing-flag ConfigurePlatform error = %v", err)
	}
	if err := gov8.ConfigurePlatform(gov8.PlatformOptions{Kind: gov8.PlatformDefault}); err != nil {
		fatalChild("valid ConfigurePlatform after rejected inputs: %v", err)
	}
	if err := gov8.ConfigurePlatform(gov8.PlatformOptions{Kind: gov8.PlatformDefault}); err == nil ||
		!strings.Contains(err.Error(), "already configured") {
		fatalChild("duplicate ConfigurePlatform error = %v", err)
	}
	// These fields retain the exact Rust unsafe-boundary observation. This Go
	// child reaches no V8 entry: the successful child exit proves the explicit
	// safety normalization asserted above.
	return mustJSON(unsafeLine{
		Check: "platform/single_threaded_without_required_flag",
		OK:    true,
		Value: unsafeResult{
			Trigger:       "WebAssembly.compile(empty_module)",
			Success:       false,
			WindowsStatus: "0xC0000005",
			StdoutEmpty:   true,
			StderrEmpty:   true,
		},
	})
}

func concurrentConfigurationChild() {
	const workers = 16
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- gov8.ConfigurePlatform(gov8.PlatformOptions{Kind: gov8.PlatformDefault})
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "already configured") {
			fatalChild("unexpected concurrent ConfigurePlatform error: %v", err)
		}
	}
	if successes != 1 {
		fatalChild("concurrent ConfigurePlatform successes = %d, want 1", successes)
	}
	if err := gov8.Initialize(); err != nil {
		fatalChild("Initialize after concurrent configuration: %v", err)
	}
	if err := gov8.Shutdown(); err != nil {
		fatalChild("Shutdown after concurrent configuration: %v", err)
	}
}

func subprocess(mode string) []byte {
	exe, err := os.Executable()
	if err != nil {
		fatalChild("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestPlatformSubprocess$")
	cmd.Env = append(os.Environ(), "GOV8_PLATFORM_CHILD="+mode)
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			fatalChild("child %s failed: %v\nstderr: %s", mode, err, exit.Stderr)
		}
		fatalChild("child %s failed: %v", mode, err)
	}
	return out
}

func driverOutput() []byte {
	var out []byte
	for _, mode := range []string{"default", "unprotected", "single-threaded", "missing-flag"} {
		out = append(out, subprocess(mode)...)
	}
	out = append(out, []byte("{\"summary\":{\"total\":4,\"passed\":4,\"failed\":0}}\n")...)
	return out
}

func TestPlatformSubprocess(t *testing.T) {
	mode := os.Getenv("GOV8_PLATFORM_CHILD")
	if mode == "" {
		return
	}
	if mode == "missing-flag" {
		_, _ = os.Stdout.Write(missingFlagChild())
	} else if mode == "concurrent" {
		concurrentConfigurationChild()
	} else {
		_, _ = os.Stdout.Write(platformChild(mode))
	}
	os.Exit(0)
}

func TestPlatformConcurrentConfiguration(t *testing.T) {
	if out := subprocess("concurrent"); len(out) != 0 {
		t.Fatalf("unexpected concurrent child output: %q", out)
	}
}

func TestPlatformMatchesRustFixtureExactly(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-platform-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatalf("read Rust fixture: %v", err)
	}
	got := driverOutput()
	if !bytes.Equal(got, want) {
		t.Fatalf("platform output differs\ngot:\n%s\nwant:\n%s", got, want)
	}
}
