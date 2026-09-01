//go:build windows && amd64

package gov8_test

import (
	"math"
	"strings"
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func newAdvancedRuntime(t *testing.T, params *gov8.CreateParams) (*gov8.Isolate, *gov8.Context, *gov8.Scope) {
	t.Helper()
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		t.Fatalf("NewIsolateWithParams: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		_ = iso.Close()
		t.Fatalf("NewContext: %v", err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		_ = ctx.Close()
		_ = iso.Close()
		t.Fatalf("NewScope: %v", err)
	}
	t.Cleanup(func() {
		_ = scope.Close()
		_ = ctx.Close()
		_ = iso.Close()
	})
	return iso, ctx, scope
}

func TestCreateParamsConstraints(t *testing.T) {
	p := gov8.NewCreateParams()
	if p.MaxOldGenerationSizeInBytes() != 0 || p.MaxYoungGenerationSizeInBytes() != 0 ||
		p.CodeRangeSizeInBytes() != 0 || p.InitialOldGenerationSizeInBytes() != 0 ||
		p.InitialYoungGenerationSizeInBytes() != 0 || p.StackLimit() != 0 ||
		p.HasSetArrayBufferAllocator() {
		t.Fatal("CreateParams defaults differ from the oracle")
	}
	p.SetMaxOldGenerationSizeInBytes(128 << 20).
		SetMaxYoungGenerationSizeInBytes(16 << 20).
		SetCodeRangeSizeInBytes(64 << 20).
		SetInitialOldGenerationSizeInBytes(8 << 20).
		SetInitialYoungGenerationSizeInBytes(2 << 20).
		SetStackLimit(0x1234)
	if got := [6]uint64{p.MaxOldGenerationSizeInBytes(), p.MaxYoungGenerationSizeInBytes(),
		p.CodeRangeSizeInBytes(), p.InitialOldGenerationSizeInBytes(),
		p.InitialYoungGenerationSizeInBytes(), uint64(p.StackLimit())}; got != [6]uint64{128 << 20, 16 << 20, 64 << 20, 8 << 20, 2 << 20, 0x1234} {
		t.Fatalf("configured constraints = %v", got)
	}

	heap := gov8.NewCreateParams()
	if err := heap.ConfigureHeapLimits(32<<20, 96<<20); err != nil {
		t.Fatal(err)
	}
	if got := [5]uint64{heap.MaxOldGenerationSizeInBytes(), heap.MaxYoungGenerationSizeInBytes(),
		heap.CodeRangeSizeInBytes(), heap.InitialOldGenerationSizeInBytes(),
		heap.InitialYoungGenerationSizeInBytes()}; got != [5]uint64{91226112, 9437184, 100663296, 27262976, 6291456} {
		t.Fatalf("derived heap limits = %v", got)
	}
	system := gov8.NewCreateParams()
	if err := system.ConfigureHeapLimitsFromSystemMemory(512<<20, 1<<30); err != nil {
		t.Fatal(err)
	}
	if got := [5]uint64{system.MaxOldGenerationSizeInBytes(), system.MaxYoungGenerationSizeInBytes(),
		system.CodeRangeSizeInBytes(), system.InitialOldGenerationSizeInBytes(),
		system.InitialYoungGenerationSizeInBytes()}; got != [5]uint64{268435456, 12582912, 134217728, 0, 0} {
		t.Fatalf("system-memory limits = %v", got)
	}
}

func TestCreateParamsAllocatorExternalReferencesAndAtomics(t *testing.T) {
	p := gov8.NewCreateParams().UseDefaultArrayBufferAllocator().UseEmptyExternalReferences()
	if !p.HasSetArrayBufferAllocator() || !p.HasEmptyExternalReferences() {
		t.Fatal("configured flags not retained")
	}
	_, ctx, scope := newAdvancedRuntime(t, p)
	ab, err := gov8.NewArrayBuffer(scope, ctx, 17)
	if err != nil {
		t.Fatalf("NewArrayBuffer: %v", err)
	}
	length, lengthErr := ab.ByteLength()
	if lengthErr != nil || length != 17 {
		t.Fatalf("NewArrayBuffer = length %d, %v", length, lengthErr)
	}

	for _, tc := range []struct {
		allow bool
		want  string
		throw bool
	}{{false, "TypeError: Atomics.wait cannot be called in this context", true}, {true, "timed-out", false}} {
		t.Run(tc.want, func(t *testing.T) {
			iso, ctx, scope := newAdvancedRuntime(t, gov8.NewCreateParams().SetAllowAtomicsWait(tc.allow))
			catcher, err := iso.NewTryCatch()
			if err != nil {
				t.Fatal(err)
			}
			defer catcher.Close()
			script, err := ctx.Compile(scope, "Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 0)", catcher)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			defer script.Close()
			value, runErr := script.Run(scope, catcher)
			caught, _ := catcher.HasCaught()
			if tc.throw {
				text, _ := catcher.ExceptionText(scope, ctx)
				if runErr == nil || !caught || text != tc.want {
					t.Fatalf("disabled = err %v caught %v text %q", runErr, caught, text)
				}
			} else if runErr != nil || caught {
				t.Fatalf("enabled = err %v caught %v", runErr, caught)
			} else if text, err := value.StringValue(); err != nil || text != tc.want {
				t.Fatalf("enabled result = %q, %v", text, err)
			}
		})
	}
}

func TestIsolateAdvancedCounterStatisticsAndControls(t *testing.T) {
	var mu sync.Mutex
	var names []string
	p := gov8.NewCreateParams().SetCounterLookupCallback(func(name string) {
		mu.Lock()
		names = append(names, name)
		mu.Unlock()
	})
	iso, ctx, scope := newAdvancedRuntime(t, p)
	for n := 0; n < 3; n++ {
		script, err := ctx.Compile(scope, "function uniqueCounterProbe"+string(rune('A'+n))+"(){ return 1 }; uniqueCounterProbe"+string(rune('A'+n))+"()", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = script.Run(scope, nil)
		_ = script.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	observed := len(names) > 0
	mu.Unlock()
	if !observed {
		t.Fatal("counter callback observed no names")
	}
	if value, found, err := iso.CounterValue("c:V8.CompilationCacheMisses"); err != nil || !found || value <= 0 {
		t.Fatalf("compilation counter = %d found=%v err=%v", value, found, err)
	}

	count, err := iso.NumberOfHeapSpaces()
	if err != nil || count != 13 {
		t.Fatalf("heap spaces = %d, %v", count, err)
	}
	wantNames := []string{"read_only_space", "new_space", "old_space", "code_space", "shared_space", "trusted_space", "shared_trusted_space", "new_large_object_space", "large_object_space", "code_large_object_space", "shared_large_object_space", "shared_trusted_large_object_space", "trusted_large_object_space"}
	for index, want := range wantNames {
		space, ok, err := iso.GetHeapSpaceStatistics(uint64(index))
		if err != nil || !ok || space.Name != want || space.UsedSize > space.Size || space.PhysicalSize > space.Size {
			t.Fatalf("space[%d] = %+v ok=%v err=%v", index, space, ok, err)
		}
	}
	for _, index := range []uint64{uint64(count), math.MaxUint64} {
		if value, ok, err := iso.GetHeapSpaceStatistics(index); err != nil || ok || value != nil {
			t.Fatalf("space[%d] = %+v ok=%v err=%v", index, value, ok, err)
		}
	}
	stats, err := iso.GetHeapStatistics()
	if err != nil || stats.UsedHeapSize > stats.TotalHeapSize || stats.HeapSizeLimit == 0 {
		t.Fatalf("heap stats = %+v, %v", stats, err)
	}
	code, available, err := iso.GetHeapCodeAndMetadataStatistics()
	if err != nil || !available || code.CodeAndMetadataSize == 0 || code.BytecodeAndMetadataSize == 0 {
		t.Fatalf("code stats = %+v available=%v err=%v", code, available, err)
	}
	if present, err := iso.HasCppHeap(); err != nil || !present {
		t.Fatalf("HasCppHeap = %v, %v", present, err)
	}
	traceID := uint64(42)
	for name, action := range map[string]func() error{
		"detailed":   iso.UseDetailedSourcePositionsForProfiling,
		"sample":     func() error { return iso.CollectCPUProfilerSample(nil) },
		"trace":      func() error { return iso.CollectCPUProfilerSample(&traceID) },
		"clear":      iso.ClearKeptObjects,
		"pressure":   func() error { return iso.MemoryPressureNotification(gov8.MemoryPressureModerate) },
		"low-memory": iso.LowMemoryNotification,
	} {
		if err := action(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	script, err := ctx.Compile(scope, "'still-usable'", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	v, err := script.Run(scope, nil)
	if text, textErr := v.StringValue(); err != nil || textErr != nil || !strings.Contains(text, "usable") {
		t.Fatalf("post-control eval = %q, run=%v text=%v", text, err, textErr)
	}
}
