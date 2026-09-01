//go:build windows && amd64

package objectresidualconformance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	gov8 "gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-object-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type runtimeState struct {
	iso   *gov8.Isolate
	ctx   *gov8.Context
	scope *gov8.Scope
}

func newRuntime(t *testing.T) *runtimeState {
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
	return &runtimeState{iso, ctx, scope}
}

func (r *runtimeState) close(t *testing.T) {
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

func (r *runtimeState) eval(t *testing.T, source string) gov8.Value {
	t.Helper()
	script, err := r.ctx.Compile(r.scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = script.Close() }()
	value, err := script.Run(r.scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func valueText(t *testing.T, r *runtimeState, value gov8.Value) string {
	t.Helper()
	text, err := value.ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

type reportLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

type constructionObservation struct {
	PrototypeIsNull bool   `json:"prototype_is_null"`
	Alpha           string `json:"alpha"`
	Symbol          string `json:"symbol"`
	AttributesNone  bool   `json:"attributes_none"`
	HasAlpha        bool   `json:"has_alpha"`
	HasSymbol       bool   `json:"has_symbol"`
}

func construction(t *testing.T, r *runtimeState) constructionObservation {
	t.Helper()
	null, _ := r.scope.Null()
	alpha, _ := r.scope.NewString("alpha")
	symText, _ := r.scope.NewString("sym")
	symbol, err := r.scope.SymbolForKey(symText)
	if err != nil {
		t.Fatal(err)
	}
	fortyTwo, _ := r.scope.Int32(42)
	payload, _ := r.scope.NewString("payload")
	object, err := r.scope.NewObjectWithPrototypeAndProperties(
		r.ctx, null, []gov8.Value{alpha, symbol.Value}, []gov8.Value{fortyTwo, payload})
	if err != nil {
		t.Fatal(err)
	}
	proto, err := object.GetPrototype(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	protoNull, _ := proto.IsNull()
	alphaValue, _ := object.GetByKey(r.scope, r.ctx, alpha)
	symbolValue, _ := object.GetByKey(r.scope, r.ctx, symbol.Value)
	attr, present, err := object.GetPropertyAttributes(r.scope, r.ctx, alpha)
	if err != nil {
		t.Fatal(err)
	}
	hasAlpha, err := object.HasOwnProperty(r.scope, r.ctx, alpha, nil)
	if err != nil {
		t.Fatal(err)
	}
	hasSymbol, err := object.HasOwnProperty(r.scope, r.ctx, symbol.Value, nil)
	if err != nil {
		t.Fatal(err)
	}
	return constructionObservation{protoNull, valueText(t, r, alphaValue), valueText(t, r, symbolValue), present && attr == gov8.AttrNone, hasAlpha, hasSymbol}
}

type namesObservation struct {
	Length                  int64 `json:"length"`
	InheritedExcluded       bool  `json:"inherited_excluded"`
	IndexIncludedDespiteArg bool  `json:"index_included_despite_arg"`
	EnumerablePresent       bool  `json:"enumerable_present"`
	NonEnumerablePresent    bool  `json:"non_enumerable_present"`
	SymbolPresent           bool  `json:"symbol_present"`
}

func ownNames(t *testing.T, r *runtimeState) namesObservation {
	t.Helper()
	value := r.eval(t, "(()=>{const p={inherited:1};const o=Object.create(p);o[2]='two';o.e=1;Object.defineProperty(o,'hidden',{value:1});o[Symbol.for('s')]=1;return o})()")
	object, _ := gov8.AsObject(value)
	names, err := object.GetOwnPropertyNames(r.scope, r.ctx, gov8.PropertyFilterAllProperties, gov8.KeyConversionKeepNumbers)
	if err != nil {
		t.Fatal(err)
	}
	length, _ := names.Length()
	symbol := r.eval(t, "Symbol.for('s')")
	observation := namesObservation{Length: length, InheritedExcluded: true, IndexIncludedDespiteArg: false}
	for index := uint32(0); index < uint32(length); index++ {
		name, err := names.GetIndex(r.scope, r.ctx, index)
		if err != nil {
			t.Fatal(err)
		}
		isUint32, _ := name.IsUint32()
		if isUint32 {
			got, ok, err := name.Uint32Value(r.ctx)
			if err != nil || !ok {
				t.Fatalf("Uint32Value: %v, %v", ok, err)
			}
			observation.IndexIncludedDespiteArg = got == 2
			continue
		}
		equal, _ := name.StrictEquals(symbol)
		if equal {
			observation.SymbolPresent = true
			continue
		}
		isString, _ := name.IsString()
		if !isString {
			continue
		}
		switch valueText(t, r, name) {
		case "inherited":
			observation.InheritedExcluded = false
		case "e":
			observation.EnumerablePresent = true
		case "hidden":
			observation.NonEnumerablePresent = true
		}
	}
	return observation
}

type previewObservation struct {
	KeysLength      int64  `json:"keys_length"`
	KeysKeyValue    bool   `json:"keys_key_value"`
	KeysFirst       string `json:"keys_first"`
	EntriesLength   int64  `json:"entries_length"`
	EntriesKeyValue bool   `json:"entries_key_value"`
	MapLength       int64  `json:"map_length"`
	MapKeyValue     bool   `json:"map_key_value"`
	PlainAbsent     bool   `json:"plain_absent"`
	PlainKeyValue   bool   `json:"plain_key_value"`
}

func preview(t *testing.T, r *runtimeState) previewObservation {
	t.Helper()
	previewOf := func(source string) (*gov8.Array, bool, bool) {
		object, err := gov8.AsObject(r.eval(t, source))
		if err != nil {
			t.Fatal(err)
		}
		array, kv, present, err := object.PreviewEntries(r.scope, r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		return array, kv, present
	}
	keys, keysKV, _ := previewOf("new Set([1,2,3]).keys()")
	entries, entriesKV, _ := previewOf("new Set([1,3]).entries()")
	mapEntries, mapKV, _ := previewOf("new Map([['a',1],['b',2]])")
	plain, _ := r.scope.NewObject(r.ctx)
	_, plainKV, plainPresent, err := plain.PreviewEntries(r.scope, r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	keysLength, _ := keys.Length()
	entriesLength, _ := entries.Length()
	mapLength, _ := mapEntries.Length()
	first, _ := keys.GetIndex(r.scope, r.ctx, 0)
	return previewObservation{keysLength, keysKV, valueText(t, r, first), entriesLength, entriesKV, mapLength, mapKV, !plainPresent, plainKV}
}

type wrapperObservation struct {
	Plain                    bool `json:"plain"`
	InternalFieldsOnly       bool `json:"internal_fields_only"`
	FunctionTemplateInstance bool `json:"function_template_instance"`
}

func wrappers(t *testing.T, r *runtimeState) wrapperObservation {
	t.Helper()
	plain, _ := r.scope.NewObject(r.ctx)
	pl, _ := plain.IsAPIWrapper()
	ot, err := r.iso.NewObjectTemplate(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ot.SetInternalFieldCount(1); err != nil {
		t.Fatal(err)
	}
	fields, ok, err := ot.NewInstance(r.scope, r.ctx)
	if err != nil || !ok {
		t.Fatalf("ObjectTemplate.NewInstance: %v, %v", ok, err)
	}
	fieldsWrapper, _ := fields.IsAPIWrapper()
	ft, err := r.iso.NewFunctionTemplate(r.scope, func(_ *gov8.CallbackScope, _ gov8.FunctionCallbackArguments, _ gov8.ReturnValue) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	function, err := ft.GetFunction(r.scope, r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	instance, ok, err := function.NewInstance(r.scope)
	if err != nil || !ok {
		t.Fatalf("Function.NewInstance: %v, %v", ok, err)
	}
	functionWrapper, _ := instance.IsAPIWrapper()
	return wrapperObservation{pl, fieldsWrapper, functionWrapper}
}

func TestObjectResidualConformance(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)
	lines := []reportLine{
		{"object-residual/constructor/prototype_properties", true, construction(t, r)},
		{"object-residual/names/own_filters", true, ownNames(t, r)},
		{"object-residual/preview/collections", true, preview(t, r)},
		{"object-residual/api_wrapper/classification", true, wrappers(t, r)},
	}
	var output strings.Builder
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(encoded)
		output.WriteByte('\n')
	}
	output.WriteString("{\"summary\":{\"total\":4,\"passed\":4,\"failed\":0}}\n")
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != string(want) {
		t.Fatalf("report differs from fixture:\nwant:\n%s\ngot:\n%s", want, output.String())
	}
}
