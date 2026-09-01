//go:build windows && amd64

package platformcustomconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"

	gov8 "github.com/maclof/gov8"
)

type immediateBenchmarkPlatform struct {
	gov8.PlatformImplFuncs
	isolate atomic.Pointer[gov8.Isolate]
	posts   atomic.Uint64
	err     atomic.Pointer[benchmarkCallbackError]
}

type benchmarkCallbackError struct{ err error }

func (p *immediateBenchmarkPlatform) PostTask(_ gov8.PlatformIsolate, task *gov8.Task) {
	p.posts.Add(1)
	if err := task.Run(p.isolate.Load()); err != nil {
		_ = task.Close()
		p.err.CompareAndSwap(nil, &benchmarkCallbackError{err: err})
	}
}

func (p *immediateBenchmarkPlatform) begin(isolate *gov8.Isolate) {
	p.isolate.Store(isolate)
	p.err.Store(nil)
	p.posts.Store(0)
}

func (p *immediateBenchmarkPlatform) callbackError() error {
	if callbackError := p.err.Load(); callbackError != nil {
		return callbackError.err
	}
	return nil
}

func loadBenchmarkShimDLL() (*syscall.DLL, error) {
	path := os.Getenv("GOV8_SHIM_DLL")
	if path == "" {
		dir, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		for range 8 {
			candidate := filepath.Join(dir, "build", "shim", "gov8_shim.dll")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if path == "" {
		return nil, fmt.Errorf("gov8_shim.dll not found")
	}
	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return nil, err
	}
	return dll, nil
}

func findBenchmarkProc(dll *syscall.DLL, name string) (*syscall.Proc, error) {
	proc, err := dll.FindProc(name)
	if err != nil {
		return nil, fmt.Errorf("missing benchmark shim export %s: %w", name, err)
	}
	return proc, nil
}

func benchmarkStatus(b *testing.B, operation string, result uintptr) {
	b.Helper()
	if int64(result) < 0 {
		b.Fatalf("%s failed with status %d", operation, int64(result))
	}
}

var customPlatformBenchmark = struct {
	sync.Once
	impl        *immediateBenchmarkPlatform
	dll         *syscall.DLL
	post        *syscall.Proc
	reset       *syscall.Proc
	counts      *syscall.Proc
	err         error
	initialized atomic.Bool
}{}

func initializeCustomPlatformBenchmark() {
	customPlatformBenchmark.Do(func() {
		impl := &immediateBenchmarkPlatform{}
		if err := gov8.ConfigureCustomPlatform(
			gov8.CustomPlatformOptions{ThreadPoolSize: 1, Unprotected: true}, impl,
		); err != nil {
			customPlatformBenchmark.err = err
			return
		}
		if err := gov8.Initialize(); err != nil {
			customPlatformBenchmark.err = err
			return
		}
		dll, err := loadBenchmarkShimDLL()
		if err != nil {
			customPlatformBenchmark.err = err
			return
		}
		post, err := findBenchmarkProc(dll, "gov8_pc_benchmark_post_noop_task")
		if err != nil {
			customPlatformBenchmark.err = err
			return
		}
		reset, err := findBenchmarkProc(dll, "gov8_pc_benchmark_reset_noop_task_counts")
		if err != nil {
			customPlatformBenchmark.err = err
			return
		}
		counts, err := findBenchmarkProc(dll, "gov8_pc_benchmark_noop_task_counts")
		if err != nil {
			customPlatformBenchmark.err = err
			return
		}
		customPlatformBenchmark.impl = impl
		customPlatformBenchmark.dll = dll
		customPlatformBenchmark.post = post
		customPlatformBenchmark.reset = reset
		customPlatformBenchmark.counts = counts
		customPlatformBenchmark.initialized.Store(true)
	})
}

func TestMain(m *testing.M) {
	code := m.Run()
	if customPlatformBenchmark.initialized.Load() {
		if err := gov8.Shutdown(); err != nil && code == 0 {
			fmt.Fprintln(os.Stderr, "custom platform benchmark shutdown:", err)
			code = 1
		}
		if err := customPlatformBenchmark.dll.Release(); err != nil && code == 0 {
			fmt.Fprintln(os.Stderr, "custom platform benchmark DLL release:", err)
			code = 1
		}
	}
	os.Exit(code)
}

// BenchmarkCustomPlatformNoopTaskDispatch measures one synthetic v8::Task
// allocation, CustomPlatform PostTask callback into Go, Task.Run, and delete
// per iteration. It uses the same one-probe/10,000-operation explicit warm-up
// and counter-reset boundaries as the pinned rusty_v8 benchmark. Platform and
// isolate setup plus all counter validation are outside the timed region.
func BenchmarkCustomPlatformNoopTaskDispatch(b *testing.B) {
	initializeCustomPlatformBenchmark()
	if customPlatformBenchmark.err != nil {
		b.Fatal(customPlatformBenchmark.err)
	}
	isolate, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		customPlatformBenchmark.impl.begin(nil)
		if err := isolate.Close(); err != nil {
			b.Error(err)
		}
	}()
	impl := customPlatformBenchmark.impl
	impl.begin(isolate)
	post := customPlatformBenchmark.post
	reset := customPlatformBenchmark.reset
	counts := customPlatformBenchmark.counts

	benchmarkStatus(b, "reset counts", func() uintptr { r, _, _ := reset.Call(); return r }())
	// Untimed correctness probe.
	benchmarkStatus(b, "post no-op task", func() uintptr { r, _, _ := post.Call(); return r }())
	if impl.posts.Load() != 1 || impl.callbackError() != nil {
		b.Fatalf("probe posts=%d callback_error=%v", impl.posts.Load(), impl.callbackError())
	}
	benchmarkStatus(b, "reset counts", func() uintptr { r, _, _ := reset.Call(); return r }())
	impl.begin(isolate)
	for range 10_000 {
		benchmarkStatus(b, "warm-up post no-op task", func() uintptr { r, _, _ := post.Call(); return r }())
	}
	if impl.posts.Load() != 10_000 || impl.callbackError() != nil {
		b.Fatalf("warm-up posts=%d callback_error=%v", impl.posts.Load(), impl.callbackError())
	}
	benchmarkStatus(b, "reset counts", func() uintptr { r, _, _ := reset.Call(); return r }())
	impl.begin(isolate)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r, _, _ := post.Call()
		if int64(r) < 0 {
			b.Fatal(fmt.Errorf("post no-op task failed with status %d", int64(r)))
		}
	}
	b.StopTimer()

	var created, run, destroyed uint64
	r, _, _ := counts.Call(
		uintptr(unsafe.Pointer(&created)),
		uintptr(unsafe.Pointer(&run)),
		uintptr(unsafe.Pointer(&destroyed)),
	)
	benchmarkStatus(b, "read counts", r)
	if err := impl.callbackError(); err != nil {
		b.Fatal(err)
	}
	want := uint64(b.N)
	if impl.posts.Load() != want || created != want || run != want || destroyed != want {
		b.Fatalf(
			"iterations=%d posts=%d created=%d run=%d destroyed=%d",
			b.N, impl.posts.Load(), created, run, destroyed,
		)
	}
}
