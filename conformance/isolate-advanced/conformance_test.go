//go:build windows && amd64

package isolateadvancedconformance

import (
	"bufio"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type fixtureLine struct {
	Check string         `json:"check"`
	OK    bool           `json:"ok"`
	Value map[string]any `json:"value"`
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "..", "rust-oracle", "tests", "fixtures", "conformance-isolate-advanced-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checked-in Rust isolate-advanced oracle fixture is missing: %s", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result := map[string]fixtureLine{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err == nil && line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got map[string]any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok || !want.OK {
		t.Fatalf("missing or failed Rust fixture check %s", id)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want.Value)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t *testing.T, params *gov8.CreateParams) *runtime {
	t.Helper()
	iso, err := gov8.NewIsolateWithParams(params)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	return &runtime{iso: iso, ctx: ctx, scope: scope}
}

func (r *runtime) close(t *testing.T) {
	t.Helper()
	_ = r.scope.Close()
	_ = r.ctx.Close()
	_ = r.iso.Close()
}

func (r *runtime) eval(t *testing.T, source string, catcher *gov8.TryCatch) (gov8.Value, error) {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, catcher)
	if err != nil {
		return gov8.Value{}, err
	}
	defer script.Close()
	return script.Run(r.scope, catcher)
}

func TestRustOracleFixture(t *testing.T) {
	fixtures := fixture(t)
	t.Run("constraint_getters", func(t *testing.T) {
		defaults := gov8.NewCreateParams()
		configured := gov8.NewCreateParams().SetMaxOldGenerationSizeInBytes(128 << 20).
			SetMaxYoungGenerationSizeInBytes(16 << 20).SetCodeRangeSizeInBytes(64 << 20).
			SetInitialOldGenerationSizeInBytes(8 << 20).SetInitialYoungGenerationSizeInBytes(2 << 20)
		marker := uintptr(0x1234)
		withStack := gov8.NewCreateParams().SetStackLimit(marker)
		compare(t, fixtures, "isolate-advanced/create-params/constraint_getters", map[string]any{
			"defaults_zero":      defaults.MaxOldGenerationSizeInBytes() == 0 && defaults.MaxYoungGenerationSizeInBytes() == 0 && defaults.CodeRangeSizeInBytes() == 0 && defaults.InitialOldGenerationSizeInBytes() == 0 && defaults.InitialYoungGenerationSizeInBytes() == 0,
			"default_stack_null": defaults.StackLimit() == 0, "max_old": configured.MaxOldGenerationSizeInBytes(),
			"max_young": configured.MaxYoungGenerationSizeInBytes(), "code_range": configured.CodeRangeSizeInBytes(),
			"initial_old": configured.InitialOldGenerationSizeInBytes(), "initial_young": configured.InitialYoungGenerationSizeInBytes(),
			"stack_pointer_round_trip": withStack.StackLimit() == marker,
		})
	})

	t.Run("derived_heap_limits", func(t *testing.T) {
		heap, system := gov8.NewCreateParams(), gov8.NewCreateParams()
		if err := heap.ConfigureHeapLimits(32<<20, 96<<20); err != nil {
			t.Fatal(err)
		}
		if err := system.ConfigureHeapLimitsFromSystemMemory(512<<20, 1024<<20); err != nil {
			t.Fatal(err)
		}
		encode := func(p *gov8.CreateParams) map[string]any {
			return map[string]any{"max_old": p.MaxOldGenerationSizeInBytes(), "max_young": p.MaxYoungGenerationSizeInBytes(), "initial_old": p.InitialOldGenerationSizeInBytes(), "initial_young": p.InitialYoungGenerationSizeInBytes(), "code_range": p.CodeRangeSizeInBytes()}
		}
		compare(t, fixtures, "isolate-advanced/create-params/derived_heap_limits", map[string]any{"heap_bounds": encode(heap), "system_memory": encode(system)})
	})

	t.Run("allocator_external_references", func(t *testing.T) {
		defaults := gov8.NewCreateParams()
		configured := gov8.NewCreateParams().UseDefaultArrayBufferAllocator().UseEmptyExternalReferences()
		r := newRuntime(t, configured)
		defer r.close(t)
		buffer, err := gov8.NewArrayBuffer(r.scope, r.ctx, 17)
		if err != nil {
			t.Fatal(err)
		}
		length, err := buffer.ByteLength()
		if err != nil {
			t.Fatal(err)
		}
		_, evalErr := r.eval(t, "1 + 1", nil)
		compare(t, fixtures, "isolate-advanced/create-params/allocator_external_references", map[string]any{
			"default_has_allocator": defaults.HasSetArrayBufferAllocator(), "configured_has_allocator": configured.HasSetArrayBufferAllocator(),
			"array_buffer_length": length, "empty_external_references_usable": evalErr == nil,
		})
	})

	t.Run("allow_atomics_wait", func(t *testing.T) {
		observe := func(allow bool) map[string]any {
			r := newRuntime(t, gov8.NewCreateParams().SetAllowAtomicsWait(allow))
			defer r.close(t)
			tc, err := r.iso.NewTryCatch()
			if err != nil {
				t.Fatal(err)
			}
			defer tc.Close()
			v, runErr := r.eval(t, "Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 0)", tc)
			caught, _ := tc.HasCaught()
			var result any
			var exception any
			if runErr == nil {
				result, _ = v.StringValue()
			}
			if caught {
				exception, _ = tc.ExceptionText(r.scope, r.ctx)
			}
			return map[string]any{"result": result, "caught": caught, "exception": exception}
		}
		compare(t, fixtures, "isolate-advanced/create-params/allow_atomics_wait", map[string]any{"disabled": observe(false), "enabled": observe(true)})
	})

	t.Run("counter_lookup_callback", func(t *testing.T) {
		var mu sync.Mutex
		var names []string
		p := gov8.NewCreateParams().SetCounterLookupCallback(func(name string) { mu.Lock(); names = append(names, name); mu.Unlock() })
		r := newRuntime(t, p)
		defer r.close(t)
		for _, source := range []string{"function counterOne(){return 1};counterOne()", "function counterTwo(){return 2};counterTwo()", "function counterThree(){return 3};counterThree()"} {
			if _, err := r.eval(t, source, nil); err != nil {
				t.Fatal(err)
			}
		}
		value, found, err := r.iso.CounterValue("c:V8.CompilationCacheMisses")
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		observed := len(names) > 0
		mu.Unlock()
		compare(t, fixtures, "isolate-advanced/create-params/counter_lookup_callback", map[string]any{"callback_observed_names": observed, "compilation_cache_misses_positive": found && value > 0})
	})

	t.Run("heap_invariants", func(t *testing.T) {
		iso, err := gov8.NewIsolateWithParams(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer iso.Close()
		s, err := iso.GetHeapStatistics()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "isolate-advanced/statistics/heap_invariants", map[string]any{
			"used_le_total": s.UsedHeapSize <= s.TotalHeapSize, "executable_le_total": s.TotalHeapSizeExecutable <= s.TotalHeapSize,
			"physical_positive": s.TotalPhysicalSize > 0, "available_positive": s.TotalAvailableSize > 0, "heap_limit_positive": s.HeapSizeLimit > 0,
			"global_handles_coherent": s.UsedGlobalHandlesSize <= s.TotalGlobalHandlesSize, "malloced_positive": s.MallocedMemory > 0,
			"peak_malloced_positive": s.PeakMallocedMemory > 0, "external_memory_zero": s.ExternalMemory == 0, "allocated_positive": s.TotalAllocatedBytes > 0,
			"native_contexts": s.NumberOfNativeContexts, "detached_contexts": s.NumberOfDetachedContexts, "zaps_garbage": s.DoesZapGarbage,
		})
	})

	t.Run("heap_spaces", func(t *testing.T) {
		iso, err := gov8.NewIsolateWithParams(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer iso.Close()
		count, err := iso.NumberOfHeapSpaces()
		if err != nil {
			t.Fatal(err)
		}
		spaces := make([]map[string]any, 0, count)
		for index := int64(0); index < count; index++ {
			s, ok, err := iso.GetHeapSpaceStatistics(uint64(index))
			if err != nil || !ok {
				t.Fatalf("space %d: ok=%v err=%v", index, ok, err)
			}
			spaces = append(spaces, map[string]any{"name": s.Name, "used_le_size": s.UsedSize <= s.Size, "available_le_size": s.AvailableSize <= s.Size, "physical_le_size": s.PhysicalSize <= s.Size})
		}
		_, countOK, _ := iso.GetHeapSpaceStatistics(uint64(count))
		_, maxOK, _ := iso.GetHeapSpaceStatistics(math.MaxUint64)
		compare(t, fixtures, "isolate-advanced/statistics/heap_spaces", map[string]any{"count": count, "spaces": spaces, "index_at_count_none": !countOK, "usize_max_none": !maxOK})
	})

	t.Run("code_metadata", func(t *testing.T) {
		r := newRuntime(t, nil)
		defer r.close(t)
		before, available, err := r.iso.GetHeapCodeAndMetadataStatistics()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.eval(t, "function metadataProbe(a){return a+1}; metadataProbe(41)", nil); err != nil {
			t.Fatal(err)
		}
		after, afterAvailable, err := r.iso.GetHeapCodeAndMetadataStatistics()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "isolate-advanced/statistics/code_metadata", map[string]any{
			"available": available && afterAvailable, "before_external_source_zero": before.ExternalScriptSourceSize == 0,
			"before_profiler_metadata_zero": before.CPUProfilerMetadataSize == 0, "after_code_positive": after.CodeAndMetadataSize > 0,
			"after_bytecode_positive": after.BytecodeAndMetadataSize > 0, "code_not_decreased": after.CodeAndMetadataSize >= before.CodeAndMetadataSize,
		})
	})

	t.Run("notifications_profiler_controls", func(t *testing.T) {
		r := newRuntime(t, nil)
		defer r.close(t)
		cppHeap, err := r.iso.HasCppHeap()
		if err != nil {
			t.Fatal(err)
		}
		trace := uint64(7)
		usable := r.iso.UseDetailedSourcePositionsForProfiling() == nil && r.iso.CollectCPUProfilerSample(nil) == nil && r.iso.CollectCPUProfilerSample(&trace) == nil &&
			r.iso.MemoryPressureNotification(gov8.MemoryPressureModerate) == nil && r.iso.LowMemoryNotification() == nil && r.iso.ClearKeptObjects() == nil
		_, evalErr := r.eval(t, "40 + 2", nil)
		stats, statsErr := r.iso.GetHeapStatistics()
		compare(t, fixtures, "isolate-advanced/isolate/notifications_profiler_controls", map[string]any{
			"cpp_heap_present": cppHeap, "usable_after_notifications": usable && evalErr == nil,
			"heap_coherent": statsErr == nil && stats.UsedHeapSize <= stats.TotalHeapSize,
		})
	})
}
