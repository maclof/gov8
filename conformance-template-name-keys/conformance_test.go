//go:build windows && amd64

package templatenamekeysconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gov8 "github.com/maclof/gov8"
)

var expectedIDs = []string{
	"template-name-keys/object/string_symbol_attributes",
	"template-name-keys/function/string_symbol",
	"template-name-keys/symbol/distinct_same_description",
	"template-name-keys/intrinsic/symbol",
	"template-name-keys/lifecycle/retained_and_late_mutation",
}

type fixtureLine struct {
	Check   string         `json:"check"`
	OK      bool           `json:"ok"`
	Value   map[string]any `json:"value"`
	Summary *struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) []fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-template-name-keys-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	var lines []fixtureLine
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != len(expectedIDs)+1 {
		t.Fatalf("fixture lines = %d, want %d", len(lines), len(expectedIDs)+1)
	}
	for index, id := range expectedIDs {
		if lines[index].Check != id || !lines[index].OK || lines[index].Summary != nil {
			t.Fatalf("fixture line %d = %+v, want successful %s", index, lines[index], id)
		}
	}
	summary := lines[len(expectedIDs)].Summary
	if summary == nil || summary.Total != 5 || summary.Passed != 5 || summary.Failed != 0 || lines[len(expectedIDs)].Check != "" {
		t.Fatalf("fixture summary = %+v", lines[len(expectedIDs)])
	}
	return lines[:len(expectedIDs)]
}

func compare(t *testing.T, line fixtureLine, got map[string]any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(line.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", line.Check, gotJSON, wantJSON)
	}
}

type runtime struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t *testing.T) *runtime {
	t.Helper()
	iso, err := gov8.NewIsolate()
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
	if r.scope != nil {
		if err := r.scope.Close(); err != nil {
			t.Error(err)
		}
	}
	if err := r.ctx.Close(); err != nil {
		t.Error(err)
	}
	if err := r.iso.Close(); err != nil {
		t.Error(err)
	}
}

func mustString(t *testing.T, scope *gov8.Scope, value string) gov8.Value {
	t.Helper()
	result, err := scope.NewString(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustInt(t *testing.T, scope *gov8.Scope, value int32) gov8.Value {
	t.Helper()
	result, err := scope.Int32(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustSymbol(t *testing.T, scope *gov8.Scope, description *string) *gov8.Symbol {
	t.Helper()
	var value gov8.Value
	if description != nil {
		value = mustString(t, scope, *description)
	}
	result, err := scope.NewSymbol(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func integer(t *testing.T, r *runtime, object *gov8.Object, key gov8.Value) int64 {
	t.Helper()
	value, err := object.GetByKey(r.scope, r.ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := value.IntegerValue(r.ctx)
	if err != nil || !ok {
		t.Fatalf("IntegerValue = %d, %v, %v", result, ok, err)
	}
	return result
}

func attributes(attr gov8.PropertyAttribute) map[string]any {
	return map[string]any{
		"bits":        uint8(attr),
		"read_only":   attr&gov8.AttrReadOnly != 0,
		"dont_enum":   attr&gov8.AttrDontEnum != 0,
		"dont_delete": attr&gov8.AttrDontDelete != 0,
	}
}

func eval(t *testing.T, r *runtime, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Error(err)
		}
	}()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRustOracleFixture(t *testing.T) {
	fixtures := fixture(t)

	t.Run("object_string_symbol_attributes", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		template, err := r.iso.NewObjectTemplate(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		stringKey := mustString(t, r.scope, "plain")
		description := "hidden"
		symbolKey := mustSymbol(t, r.scope, &description)
		emptyKey := mustString(t, r.scope, "")
		anonymousSymbol := mustSymbol(t, r.scope, nil)
		if err := template.SetName(stringKey, mustInt(t, r.scope, 11)); err != nil {
			t.Fatal(err)
		}
		if err := template.SetNameWithAttr(symbolKey.Value, mustInt(t, r.scope, 22), gov8.AttrReadOnly|gov8.AttrDontEnum|gov8.AttrDontDelete); err != nil {
			t.Fatal(err)
		}
		if err := template.SetName(emptyKey, mustInt(t, r.scope, 33)); err != nil {
			t.Fatal(err)
		}
		if err := template.SetName(anonymousSymbol.Value, mustInt(t, r.scope, 44)); err != nil {
			t.Fatal(err)
		}
		first, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("first instance = %v, %v", ok, err)
		}
		second, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("second instance = %v, %v", ok, err)
		}
		attr, present, err := first.GetPropertyAttributes(r.scope, r.ctx, symbolKey.Value)
		if err != nil || !present {
			t.Fatalf("attributes = %d, %v, %v", attr, present, err)
		}
		write, err := first.SetByKey(r.scope, r.ctx, symbolKey.Value, mustInt(t, r.scope, 99))
		if err != nil {
			t.Fatal(err)
		}
		deleted, err := first.Delete(r.scope, r.ctx, symbolKey.Value, nil)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures[0], map[string]any{
			"first_string": integer(t, r, first, stringKey), "second_string": integer(t, r, second, stringKey),
			"first_symbol": integer(t, r, first, symbolKey.Value), "second_symbol": integer(t, r, second, symbolKey.Value),
			"empty_string": integer(t, r, first, emptyKey), "anonymous_symbol": integer(t, r, first, anonymousSymbol.Value),
			"symbol_attributes": attributes(attr), "read_only_write_result": write,
			"symbol_after_write": integer(t, r, first, symbolKey.Value), "dont_delete_result": deleted,
			"symbol_after_delete": integer(t, r, first, symbolKey.Value),
		})
	})

	t.Run("function_string_symbol", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		template, err := r.iso.NewFunctionTemplate(r.scope, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			_ = rv.SetInt32(7)
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		stringKey := mustString(t, r.scope, "plain")
		description := "static"
		symbolKey := mustSymbol(t, r.scope, &description)
		if err := template.SetName(stringKey, mustInt(t, r.scope, 51)); err != nil {
			t.Fatal(err)
		}
		if err := template.SetNameWithAttr(symbolKey.Value, mustInt(t, r.scope, 52), gov8.AttrDontEnum); err != nil {
			t.Fatal(err)
		}
		function, err := template.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		repeated, err := template.GetFunction(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		undefined, err := r.scope.Undefined()
		if err != nil {
			t.Fatal(err)
		}
		result, ok, err := function.Call(r.scope, undefined)
		if err != nil || !ok {
			t.Fatalf("Call = %v, %v", ok, err)
		}
		callResult, ok, err := result.IntegerValue(r.ctx)
		if err != nil || !ok {
			t.Fatalf("call result = %d, %v, %v", callResult, ok, err)
		}
		functionObject, err := gov8.AsObject(function.Value)
		if err != nil {
			t.Fatal(err)
		}
		attr, present, err := functionObject.GetPropertyAttributes(r.scope, r.ctx, symbolKey.Value)
		if err != nil || !present {
			t.Fatalf("attributes = %d, %v, %v", attr, present, err)
		}
		equal, err := function.Value.StrictEquals(repeated.Value)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures[1], map[string]any{
			"call_result": callResult, "string_value": integer(t, r, functionObject, stringKey),
			"symbol_value": integer(t, r, functionObject, symbolKey.Value), "symbol_attributes": attributes(attr),
			"same_function_in_context": equal,
		})
	})

	t.Run("distinct_same_description", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		template, err := r.iso.NewObjectTemplate(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		description := "same-description"
		a := mustSymbol(t, r.scope, &description)
		b := mustSymbol(t, r.scope, &description)
		if err := template.SetName(a.Value, mustInt(t, r.scope, 5)); err != nil {
			t.Fatal(err)
		}
		if err := template.SetName(b.Value, mustInt(t, r.scope, 6)); err != nil {
			t.Fatal(err)
		}
		object, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("instance = %v, %v", ok, err)
		}
		equal, err := a.Value.StrictEquals(b.Value)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures[2], map[string]any{
			"same_description_symbols_distinct": !equal,
			"distinct_a_value":                  integer(t, r, object, a.Value), "distinct_b_value": integer(t, r, object, b.Value),
		})
	})

	t.Run("intrinsic_symbol", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		template, err := r.iso.NewObjectTemplate(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		description := "intrinsic"
		key := mustSymbol(t, r.scope, &description)
		if err := template.SetIntrinsicDataPropertyName(key.Value, gov8.IntrinsicArrayPrototype, gov8.AttrReadOnly|gov8.AttrDontEnum); err != nil {
			t.Fatal(err)
		}
		object, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("instance = %v, %v", ok, err)
		}
		got, err := object.GetByKey(r.scope, r.ctx, key.Value)
		if err != nil {
			t.Fatal(err)
		}
		want := eval(t, r, "Array.prototype")
		equal, err := got.StrictEquals(want)
		if err != nil {
			t.Fatal(err)
		}
		attr, present, err := object.GetPropertyAttributes(r.scope, r.ctx, key.Value)
		if err != nil || !present {
			t.Fatalf("attributes = %d, %v, %v", attr, present, err)
		}
		compare(t, fixtures[3], map[string]any{"is_current_context_array_prototype": equal, "attributes": attributes(attr)})
	})

	t.Run("retained_and_late_mutation", func(t *testing.T) {
		r := newRuntime(t)
		defer r.close(t)
		template, err := r.iso.NewObjectTemplate(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		keyScope, err := r.iso.NewScope()
		if err != nil {
			t.Fatal(err)
		}
		description := "retained-after-scope"
		retained := mustSymbol(t, keyScope, &description)
		if err := template.SetName(retained.Value, mustInt(t, keyScope, 71)); err != nil {
			t.Fatal(err)
		}
		if err := keyScope.Close(); err != nil {
			t.Fatal(err)
		}
		if err := r.iso.LowMemoryNotification(); err != nil {
			t.Fatal(err)
		}
		first, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("first = %v, %v", ok, err)
		}
		lateDescription := "late"
		late := mustSymbol(t, r.scope, &lateDescription)
		if err := template.SetName(late.Value, mustInt(t, r.scope, 72)); err != nil {
			t.Fatal(err)
		}
		second, ok, err := template.NewInstance(r.scope, r.ctx)
		if err != nil || !ok {
			t.Fatalf("second = %v, %v", ok, err)
		}
		global, err := r.ctx.GlobalObject(r.scope)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := global.SetByName(r.scope, r.ctx, "first", first.Value); err != nil || !ok {
			t.Fatalf("set first = %v, %v", ok, err)
		}
		if ok, err := global.SetByName(r.scope, r.ctx, "second", second.Value); err != nil || !ok {
			t.Fatalf("set second = %v, %v", ok, err)
		}
		summary, err := eval(t, r, "(()=>{const s=Object.getOwnPropertySymbols(second).find(s=>s.description==='retained-after-scope');return `${s.description}|${second[s]}`})()").ToString(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		firstHasLate, err := first.HasOwnProperty(r.scope, r.ctx, late.Value, nil)
		if err != nil {
			t.Fatal(err)
		}
		secondHasLate, err := second.HasOwnProperty(r.scope, r.ctx, late.Value, nil)
		if err != nil {
			t.Fatal(err)
		}
		lateValue, err := second.GetByKey(r.scope, r.ctx, late.Value)
		if err != nil {
			t.Fatal(err)
		}
		undefined, err := lateValue.IsUndefined()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures[4], map[string]any{
			"retained_summary": summary, "first_has_late": firstHasLate,
			"second_has_late": secondHasLate, "second_late_is_undefined": undefined,
		})
	})
}
