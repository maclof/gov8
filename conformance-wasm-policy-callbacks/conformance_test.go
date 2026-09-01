//go:build windows && amd64

package wasmpolicycallbacksconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	gov8 "gov8"
)

type fixtureLine struct {
	Check string          `json:"check"`
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
}

type testRuntime struct {
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

func openRuntime(t testing.TB, configure func(*gov8.Isolate) error) *testRuntime {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		if err := configure(iso); err != nil {
			t.Fatal(err)
		}
	}
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	return &testRuntime{iso, ctx, scope}
}

func (r *testRuntime) close(t testing.TB) {
	t.Helper()
	if err := r.scope.Close(); err != nil {
		t.Error(err)
	}
	if err := r.ctx.Close(); err != nil {
		t.Error(err)
	}
	if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
		t.Error(err)
	}
	if err := r.iso.Close(); err != nil {
		t.Error(err)
	}
}

func eval(t testing.TB, r *testRuntime, source string, tc *gov8.TryCatch) (gov8.Value, error) {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, tc)
	if err != nil {
		return gov8.Value{}, err
	}
	defer func() { _ = script.Close() }()
	return script.Run(r.scope, tc)
}

func setMarker(t testing.TB, r *testRuntime, name string, value int) {
	t.Helper()
	if _, err := eval(t, r, "globalThis."+name+"="+string(rune('0'+value/10))+string(rune('0'+value%10)), nil); err != nil {
		t.Fatal(err)
	}
}

func markerSeen(cs *gov8.CallbackScope, name string, want int64) bool {
	global, err := cs.CurrentContextGlobal()
	if err != nil {
		return false
	}
	value, ok, err := cs.ObjectGet(global, name)
	if err != nil || !ok {
		return false
	}
	got, ok, err := cs.IntegerValue(value)
	return err == nil && ok && got == want
}

func loadFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-wasm-policy-callbacks-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result := map[string]json.RawMessage{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Check != "" {
			if !line.OK {
				t.Fatalf("Rust check %s failed", line.Check)
			}
			result[line.Check] = line.Value
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("fixture checks=%d, want 2", len(result))
	}
	return result
}

func compare(t *testing.T, fixture map[string]json.RawMessage, check string, got any) {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := fixture[check]
	var gotValue, wantValue any
	if err := json.Unmarshal(encoded, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(gotValue)
	want, _ = json.Marshal(wantValue)
	if !bytes.Equal(encoded, want) {
		t.Fatalf("%s differs\ngot  %s\nwant %s", check, encoded, want)
	}
}

type syncObservation struct {
	Source string
	Marker bool
}

func syncCase(t *testing.T, mode string) map[string]any {
	var observations []syncObservation
	r := openRuntime(t, func(iso *gov8.Isolate) error {
		return iso.SetAllowWasmCodeGenerationCallback(func(cs *gov8.CallbackScope, source gov8.Value) bool {
			text, err := cs.ToString(source)
			if err != nil {
				panic(err)
			}
			observations = append(observations, syncObservation{text, markerSeen(cs, "policyMarker", 73)})
			switch mode {
			case "allow":
				return true
			case "throw":
				exception, err := cs.NewTypeError("wasm policy boom")
				if err != nil {
					panic(err)
				}
				if err := cs.ThrowException(exception); err != nil {
					panic(err)
				}
			}
			return false
		})
	})
	defer r.close(t)
	setMarker(t, r, "policyMarker", 73)
	tc, err := r.iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	value, runErr := eval(t, r, "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))", tc)
	compiled := false
	if runErr == nil {
		compiled, err = value.IsWasmModuleObject()
		if err != nil {
			t.Fatal(err)
		}
	}
	caught, err := tc.HasCaught()
	if err != nil {
		t.Fatal(err)
	}
	var exception any
	if caught {
		text, err := tc.ExceptionText(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		exception = text
	}
	return map[string]any{"compiled": compiled, "caught": caught, "exception": exception,
		"calls": len(observations), "source": observations[0].Source, "context_marker_seen": observations[0].Marker}
}

func asyncCase(t *testing.T, valid, replace bool) map[string]any {
	var mu sync.Mutex
	var original gov8.Promise
	var observation map[string]any
	callbackCalls := 0
	configure := func(iso *gov8.Isolate) error {
		if replace {
			if err := iso.SetWasmAsyncResolvePromiseCallback(func(*gov8.WasmAsyncResolution) { panic("replaced callback ran") }); err != nil {
				return err
			}
		}
		return iso.SetWasmAsyncResolvePromiseCallback(func(resolution *gov8.WasmAsyncResolution) {
			mu.Lock()
			callbackCalls++
			mu.Unlock()
			promise, err := resolution.Promise()
			if err != nil {
				panic(err)
			}
			same, err := promise.StrictEquals(original.Value)
			if err != nil {
				panic(err)
			}
			isModule, _ := resolution.Result.IsWasmModuleObject()
			isError, _ := resolution.Result.IsNativeError()
			text, err := resolution.CallbackScope.ToString(resolution.Result)
			if err != nil {
				panic(err)
			}
			if ok, err := resolution.Settle(); err != nil || !ok {
				panic("settlement failed")
			}
			settled, err := promise.Result(resolution.CallbackScope.Scope())
			if err != nil {
				panic(err)
			}
			settledSame, _ := settled.StrictEquals(resolution.Result)
			mu.Lock()
			observation = map[string]any{"success": resolution.Success.String(),
				"context_marker_seen":   markerSeen(resolution.CallbackScope, "asyncMarker", 91),
				"resolver_promise_same": same, "result_is_wasm_module": isModule,
				"result_is_native_error": isError, "result_text": text, "settled_value_same": settledSame}
			mu.Unlock()
		})
	}
	r := openRuntime(t, configure)
	defer r.close(t)
	setMarker(t, r, "asyncMarker", 91)
	bytesSource := "new Uint8Array([0,97,115,109,1,0,0,0])"
	if !valid {
		bytesSource = "new Uint8Array([0,1,2,3])"
	}
	value, err := eval(t, r, "WebAssembly.compile("+bytesSource+")", nil)
	if err != nil {
		t.Fatal(err)
	}
	original = gov8.Promise{Value: value}
	before, err := original.State()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		done := observation != nil
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for async callback")
		}
		ran, err := r.iso.PumpMessageLoop(false)
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			runtime.Gosched()
		}
	}
	if err := r.iso.PerformMicrotaskCheckpoint(); err != nil {
		t.Fatal(err)
	}
	after, err := original.State()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	result := observation
	calls := callbackCalls
	mu.Unlock()
	result["state_before"], result["state_after"], result["callback_calls"] = before.String(), after.String(), calls
	return result
}

func TestWasmPolicyCallbacksFixture(t *testing.T) {
	fixture := loadFixture(t)
	allow, deny, thrown := syncCase(t, "allow"), syncCase(t, "deny"), syncCase(t, "throw")
	replacementCalls := 0
	r := openRuntime(t, func(iso *gov8.Isolate) error {
		if err := iso.SetAllowWasmCodeGenerationCallback(func(*gov8.CallbackScope, gov8.Value) bool { return false }); err != nil {
			return err
		}
		return iso.SetAllowWasmCodeGenerationCallback(func(*gov8.CallbackScope, gov8.Value) bool {
			replacementCalls++
			return true
		})
	})
	setMarker(t, r, "policyMarker", 73)
	value, err := eval(t, r, "new WebAssembly.Module(new Uint8Array([0,97,115,109,1,0,0,0]))", nil)
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := value.IsWasmModuleObject()
	if err != nil {
		t.Fatal(err)
	}
	r.close(t)
	compare(t, fixture, "wasm-policy-callbacks/sync_allow_deny_exception", map[string]any{
		"allow": allow, "deny": deny, "throw": thrown,
		"replacement": map[string]any{"last_setter_wins": replaced, "calls": replacementCalls, "clear_api_exposed": false},
	})
	compare(t, fixture, "wasm-policy-callbacks/async_success_failure_settlement", map[string]any{
		"valid": asyncCase(t, true, false), "invalid": asyncCase(t, false, false),
		"replacement": asyncCase(t, true, true), "clear_api_exposed": false,
	})
}
