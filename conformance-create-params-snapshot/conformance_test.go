//go:build windows && amd64

package createparamssnapshotconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const mib = uint64(1024 * 1024)

type fixtureLine struct {
	Check string          `json:"check"`
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func openRuntime(t testing.TB, isolate *gov8.Isolate) *runtime {
	t.Helper()
	ctx, err := isolate.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := isolate.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	return &runtime{isolate, ctx, scope}
}

func (r *runtime) close(t testing.TB) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Error(err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Error(err)
	}
	if err := r.iso.Close(); err != nil {
		t.Error(err)
	}
}

func eval(t testing.TB, r *runtime, source string, tc *gov8.TryCatch) (gov8.Value, bool) {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, tc)
	if err != nil {
		return gov8.Value{}, false
	}
	defer func() { _ = script.Close() }()
	value, err := script.Run(r.scope, tc)
	return value, err == nil
}

func evalText(t testing.TB, r *runtime, source string, tc *gov8.TryCatch) (string, bool) {
	t.Helper()
	value, ok := eval(t, r, source, tc)
	if !ok {
		return "", false
	}
	text, err := value.ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text, true
}

func evalInt(t testing.TB, r *runtime, source string) int64 {
	t.Helper()
	value, ok := eval(t, r, source, nil)
	if !ok {
		t.Fatalf("eval %q failed", source)
	}
	n, ok, err := value.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("IntegerValue: ok=%v err=%v", ok, err)
	}
	return n
}

func plainBlob(t testing.TB, marker int) *gov8.StartupData {
	t.Helper()
	creator, err := gov8.NewSnapshotCreator()
	if err != nil {
		t.Fatal(err)
	}
	isolate := creator.Isolate()
	r := openRuntime(t, isolate)
	// Use JSON encoding instead of locale-sensitive formatting.
	markerJSON, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eval(t, r, "globalThis.snapshotMarker = "+string(markerJSON), nil); !ok {
		t.Fatal("snapshot marker evaluation failed")
	}
	if err := creator.SetDefaultContext(r.ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(gov8.FunctionCodeKeep)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func externalTable(callback gov8.ExternalReference, pointer uintptr) []gov8.ExternalReference {
	return []gov8.ExternalReference{callback, gov8.NewExternalReference(pointer)}
}

func externalBlob(t testing.TB, callback gov8.ExternalReference) *gov8.StartupData {
	t.Helper()
	creator, err := gov8.NewSnapshotCreatorWithExternalReferences(externalTable(callback, 1))
	if err != nil {
		t.Fatal(err)
	}
	isolate := creator.Isolate()
	r := openRuntime(t, isolate)
	if _, ok := eval(t, r, "globalThis.snapshotMarker = 21", nil); !ok {
		t.Fatal("marker evaluation failed")
	}
	data, err := r.scope.NewExternal(1)
	if err != nil {
		t.Fatal(err)
	}
	template, err := isolate.NewFunctionTemplateFromExternalReference(r.scope, callback, data)
	if err != nil {
		t.Fatal(err)
	}
	function, err := template.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(r.scope, r.ctx, "externalValue", function.Value); err != nil || !ok {
		t.Fatalf("set external function: ok=%v err=%v", ok, err)
	}
	if err := creator.SetDefaultContext(r.ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(gov8.FunctionCodeKeep)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func consume(t testing.TB, params *gov8.SnapshotCreateParams) *runtime {
	t.Helper()
	isolate, err := gov8.NewIsolateWithSnapshotParams(params)
	if err != nil {
		t.Fatal(err)
	}
	return openRuntime(t, isolate)
}

func newParams(t testing.TB, blob *gov8.StartupData) *gov8.SnapshotCreateParams {
	t.Helper()
	p, err := gov8.NewSnapshotCreateParams(blob)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func loadFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-create-params-snapshot-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	values := map[string]json.RawMessage{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Check != "" {
			if !line.OK {
				t.Fatalf("Rust fixture %s failed", line.Check)
			}
			values[line.Check] = line.Value
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(values) != 5 {
		t.Fatalf("fixture checks=%d, want 5", len(values))
	}
	return values
}

func compare(t *testing.T, fixture map[string]json.RawMessage, check string, got any) {
	t.Helper()
	want, ok := fixture[check]
	if !ok {
		t.Fatalf("missing fixture %s", check)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gv, wv any
	if err := json.Unmarshal(gotJSON, &gv); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wv); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ = json.Marshal(gv)
	wantJSON, _ := json.Marshal(wv)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s differs\ngot  %s\nwant %s", check, gotJSON, wantJSON)
	}
}

func TestCreateParamsSnapshotFixture(t *testing.T) {
	fixture := loadFixture(t)

	t.Run("independent allocator lifetime", func(t *testing.T) {
		produced := plainBlob(t, 21)
		if !produced.IsValid() {
			t.Fatal("produced blob is invalid")
		}
		blob, err := produced.Clone()
		if err != nil {
			t.Fatal(err)
		}
		if err := produced.Release(); err != nil {
			t.Fatal(err)
		}
		implicit := newParams(t, blob)
		implicitUnset := !implicit.HasSetArrayBufferAllocator()
		r := consume(t, implicit)
		implicitMarker := evalInt(t, r, "snapshotMarker")
		buffer, err := gov8.NewArrayBuffer(r.scope, r.ctx, 17)
		if err != nil {
			t.Fatal(err)
		}
		implicitLength, err := buffer.ByteLength()
		if err != nil {
			t.Fatal(err)
		}
		r.close(t)

		explicit := newParams(t, blob)
		explicit.UseDefaultArrayBufferAllocator()
		explicitSet := explicit.HasSetArrayBufferAllocator()
		r = consume(t, explicit)
		explicitMarker := evalInt(t, r, "snapshotMarker")
		buffer, err = gov8.NewArrayBuffer(r.scope, r.ctx, 17)
		if err != nil {
			t.Fatal(err)
		}
		explicitLength, err := buffer.ByteLength()
		if err != nil {
			t.Fatal(err)
		}
		r.close(t)

		first := plainBlob(t, 11)
		second := plainBlob(t, 22)
		replaced := newParams(t, first)
		if err := replaced.SetSnapshotBlob(second); err != nil {
			t.Fatal(err)
		}
		r = consume(t, replaced)
		replacedMarker := evalInt(t, r, "snapshotMarker")
		r.close(t)
		compare(t, fixture, "create-params-snapshot/independent_allocator_lifetime", map[string]any{
			"blob_valid": true, "producer_and_original_dropped": true,
			"implicit_reports_unset_before_finalize": implicitUnset,
			"implicit_marker":                        implicitMarker, "implicit_array_buffer_length": implicitLength,
			"explicit_reports_set": explicitSet, "explicit_marker": explicitMarker,
			"explicit_array_buffer_length": explicitLength, "second_snapshot_replaces_first": replacedMarker,
		})
		_ = first.Release()
		_ = second.Release()
		_ = blob.Release()
	})

	t.Run("atomics wait combination", func(t *testing.T) {
		blob := plainBlob(t, 21)
		defer func() { _ = blob.Release() }()
		observe := func(allowed bool) map[string]any {
			p := newParams(t, blob)
			p.SetAllowAtomicsWait(allowed)
			r := consume(t, p)
			defer r.close(t)
			marker := evalInt(t, r, "snapshotMarker")
			tc, err := r.iso.NewTryCatch()
			if err != nil {
				t.Fatal(err)
			}
			result, present := evalText(t, r, "Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,1)", tc)
			caught, _ := tc.HasCaught()
			exception, _ := tc.ExceptionText(r.scope, r.ctx)
			_ = tc.Close()
			var resultValue, exceptionValue any
			if present {
				resultValue = result
			}
			if caught {
				exceptionValue = exception
			}
			return map[string]any{"marker": marker, "result": resultValue, "caught": caught, "exception": exceptionValue}
		}
		compare(t, fixture, "create-params-snapshot/atomics_wait_combination", map[string]any{"disabled": observe(false), "enabled": observe(true)})
	})

	t.Run("all safe parameters", func(t *testing.T) {
		callback, err := gov8.NewCallbackExternalReference(gov8.ExternalReferenceFunction)
		if err != nil {
			t.Fatal(err)
		}
		blob := externalBlob(t, callback)
		defer func() { _ = blob.Release() }()
		var counterMu sync.Mutex
		counterNames := []string{}
		p := newParams(t, blob)
		if err := p.ConfigureHeapLimits(8*mib, 64*mib); err != nil {
			t.Fatal(err)
		}
		p.SetAllowAtomicsWait(false).SetExternalReferences(externalTable(callback, 23)).UseDefaultArrayBufferAllocator().SetCounterLookupCallback(func(name string) {
			counterMu.Lock()
			counterNames = append(counterNames, name)
			counterMu.Unlock()
		})
		limits := []uint64{p.MaxOldGenerationSizeInBytes(), p.MaxYoungGenerationSizeInBytes(), p.InitialOldGenerationSizeInBytes(), p.InitialYoungGenerationSizeInBytes(), p.CodeRangeSizeInBytes()}
		allocatorSet := p.HasSetArrayBufferAllocator()
		r := consume(t, p)
		marker := evalInt(t, r, "snapshotMarker")
		externalValue, ok := evalText(t, r, "externalValue()", nil)
		if !ok {
			t.Fatal("externalValue failed")
		}
		buffer, err := gov8.NewArrayBuffer(r.scope, r.ctx, 19)
		if err != nil {
			t.Fatal(err)
		}
		length, _ := buffer.ByteLength()
		tc, _ := r.iso.NewTryCatch()
		_, atomicsPresent := eval(t, r, "Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,1)", tc)
		exception, _ := tc.ExceptionText(r.scope, r.ctx)
		_ = tc.Close()
		r.close(t)
		counterMu.Lock()
		observed := len(counterNames) != 0
		counterMu.Unlock()
		compare(t, fixture, "create-params-snapshot/all_safe_parameters", map[string]any{
			"max_old": limits[0], "max_young": limits[1], "initial_old": limits[2], "initial_young": limits[3], "code_range": limits[4],
			"allocator_set": allocatorSet, "marker": marker, "external_value": externalValue,
			"array_buffer_length": length, "atomics_none": !atomicsPresent,
			"atomics_exception": exception, "counter_names_observed": observed,
		})
	})

	t.Run("constraint builder boundaries", func(t *testing.T) {
		encode := func(p *gov8.CreateParams) []uint64 {
			return []uint64{p.MaxOldGenerationSizeInBytes(), p.MaxYoungGenerationSizeInBytes(), p.InitialOldGenerationSizeInBytes(), p.InitialYoungGenerationSizeInBytes(), p.CodeRangeSizeInBytes()}
		}
		defaults := gov8.NewCreateParams()
		zeroBlob := plainBlob(t, 21)
		zero := newParams(t, zeroBlob)
		if err := zero.ConfigureHeapLimits(0, 0); err != nil {
			t.Fatal(err)
		}
		one := gov8.NewCreateParams().SetMaxOldGenerationSizeInBytes(1).SetMaxYoungGenerationSizeInBytes(1).SetInitialOldGenerationSizeInBytes(1).SetInitialYoungGenerationSizeInBytes(1).SetCodeRangeSizeInBytes(1)
		max := gov8.NewCreateParams().SetMaxOldGenerationSizeInBytes(math.MaxUint64).SetMaxYoungGenerationSizeInBytes(math.MaxUint64).SetInitialOldGenerationSizeInBytes(math.MaxUint64).SetInitialYoungGenerationSizeInBytes(math.MaxUint64).SetCodeRangeSizeInBytes(math.MaxUint64)
		inconsistent := gov8.NewCreateParams().SetMaxOldGenerationSizeInBytes(16 * mib).SetInitialOldGenerationSizeInBytes(32 * mib)

		inconsistentParams := newParams(t, zeroBlob)
		inconsistentParams.SetMaxOldGenerationSizeInBytes(16 * mib).SetInitialOldGenerationSizeInBytes(32 * mib)
		r := consume(t, inconsistentParams)
		inconsistentMarker := evalInt(t, r, "snapshotMarker")
		r.close(t)
		tiny := newParams(t, zeroBlob)
		tiny.SetMaxOldGenerationSizeInBytes(1).SetMaxYoungGenerationSizeInBytes(1).SetInitialOldGenerationSizeInBytes(1).SetInitialYoungGenerationSizeInBytes(1).SetCodeRangeSizeInBytes(1)
		r = consume(t, tiny)
		tinyMarker := evalInt(t, r, "snapshotMarker")
		r.close(t)
		compare(t, fixture, "create-params-snapshot/constraint_builder_boundaries", map[string]any{
			"order":    "max_old,max_young,initial_old,initial_young,code_range",
			"defaults": encode(defaults), "heap_limits_zero": encode(zero.CreateParams), "ones": encode(one),
			"usize_max_round_trips":              encode(max)[0] == math.MaxUint64 && encode(max)[4] == math.MaxUint64,
			"inconsistent_builder_round_trips":   inconsistent.MaxOldGenerationSizeInBytes() == 16*mib && inconsistent.InitialOldGenerationSizeInBytes() == 32*mib,
			"inconsistent_direct_isolate_marker": inconsistentMarker, "tiny_direct_isolate_marker": tinyMarker,
		})
		_ = zeroBlob.Release()
	})

	t.Run("cloned blob parameter reuse", func(t *testing.T) {
		blob := plainBlob(t, 21)
		markers := []int64{}
		allConsumed := true
		for _, allowed := range []bool{true, false, true} {
			p := newParams(t, blob)
			p.SetAllowAtomicsWait(allowed)
			r := consume(t, p)
			markers = append(markers, evalInt(t, r, "snapshotMarker"))
			allConsumed = allConsumed && p.Consumed()
			r.close(t)
		}
		if err := blob.Release(); err != nil {
			t.Fatal(err)
		}
		compare(t, fixture, "create-params-snapshot/cloned_blob_parameter_reuse", map[string]any{
			"consumer_markers": markers, "create_params_is_single_use": allConsumed,
			"startup_data_clone_is_reusable": true, "all_consumers_and_blob_dropped": true,
		})
	})
}
