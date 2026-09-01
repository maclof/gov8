//go:build windows && amd64

package runtimevaluesresidualconformance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-runtime-values-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

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
	return &runtimeState{iso: iso, ctx: ctx, scope: scope}
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

type reportLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

type symbolObservation struct {
	AsyncIterator      bool `json:"async_iterator"`
	HasInstance        bool `json:"has_instance"`
	IsConcatSpreadable bool `json:"is_concat_spreadable"`
	Iterator           bool `json:"iterator"`
	Match              bool `json:"match"`
	Replace            bool `json:"replace"`
	Search             bool `json:"search"`
	Split              bool `json:"split"`
	ToPrimitive        bool `json:"to_primitive"`
	ToStringTag        bool `json:"to_string_tag"`
	Unscopables        bool `json:"unscopables"`
	RepeatedStable     bool `json:"repeated_stable"`
	AllDistinct        bool `json:"all_distinct"`
}

type privateObservation struct {
	EmptyIdempotent       bool   `json:"empty_idempotent"`
	EmptyNameIsString     bool   `json:"empty_name_is_string"`
	EmptyNameLengthZero   bool   `json:"empty_name_length_zero"`
	EmptyDiffersFreshNone bool   `json:"empty_differs_fresh_none"`
	NamedIdempotent       bool   `json:"named_idempotent"`
	NamedName             string `json:"named_name"`
}

func mustEqual(t *testing.T, a, b gov8.Value) bool {
	t.Helper()
	equal, err := a.StrictEquals(b)
	if err != nil {
		t.Fatal(err)
	}
	return equal
}

func symbols(t *testing.T, r *runtimeState) symbolObservation {
	t.Helper()
	getters := []struct {
		name string
		get  func() (*gov8.Symbol, error)
		js   string
	}{
		{"async_iterator", r.scope.GetAsyncIteratorSymbol, "Symbol.asyncIterator"},
		{"has_instance", r.scope.GetHasInstanceSymbol, "Symbol.hasInstance"},
		{"is_concat_spreadable", r.scope.GetIsConcatSpreadableSymbol, "Symbol.isConcatSpreadable"},
		{"iterator", r.scope.GetIteratorSymbol, "Symbol.iterator"},
		{"match", r.scope.GetMatchSymbol, "Symbol.match"},
		{"replace", r.scope.GetReplaceSymbol, "Symbol.replace"},
		{"search", r.scope.GetSearchSymbol, "Symbol.search"},
		{"split", r.scope.GetSplitSymbol, "Symbol.split"},
		{"to_primitive", r.scope.GetToPrimitiveSymbol, "Symbol.toPrimitive"},
		{"to_string_tag", r.scope.GetToStringTagSymbol, "Symbol.toStringTag"},
		{"unscopables", r.scope.GetUnscopablesSymbol, "Symbol.unscopables"},
	}
	values := make([]gov8.Value, len(getters))
	matches := make([]bool, len(getters))
	for index, getter := range getters {
		symbol, err := getter.get()
		if err != nil {
			t.Fatalf("%s: %v", getter.name, err)
		}
		values[index] = symbol.Value
		matches[index] = mustEqual(t, symbol.Value, r.eval(t, getter.js))
	}
	repeated, err := r.scope.GetAsyncIteratorSymbol()
	if err != nil {
		t.Fatal(err)
	}
	allDistinct := true
	for left := range values {
		for right := left + 1; right < len(values); right++ {
			if mustEqual(t, values[left], values[right]) {
				allDistinct = false
			}
		}
	}
	return symbolObservation{
		matches[0], matches[1], matches[2], matches[3], matches[4], matches[5],
		matches[6], matches[7], matches[8], matches[9], matches[10],
		mustEqual(t, values[0], repeated.Value), allDistinct,
	}
}

func dataEqual(t *testing.T, a, b *gov8.Private) bool {
	t.Helper()
	ad, err := a.Data()
	if err != nil {
		t.Fatal(err)
	}
	bd, err := b.Data()
	if err != nil {
		t.Fatal(err)
	}
	equal, err := ad.Equal(bd)
	if err != nil {
		t.Fatal(err)
	}
	return equal
}

func privateNames(t *testing.T, r *runtimeState) privateObservation {
	t.Helper()
	empty, err := r.scope.EmptyString()
	if err != nil {
		t.Fatal(err)
	}
	emptyA, err := r.scope.PrivateForApi(empty)
	if err != nil {
		t.Fatal(err)
	}
	emptyB, err := r.scope.PrivateForApi(empty)
	if err != nil {
		t.Fatal(err)
	}
	freshNone, err := r.scope.NewPrivate(gov8.Value{})
	if err != nil {
		t.Fatal(err)
	}
	namedText, err := r.scope.NewString("named")
	if err != nil {
		t.Fatal(err)
	}
	namedA, err := r.scope.PrivateForApi(namedText)
	if err != nil {
		t.Fatal(err)
	}
	namedB, err := r.scope.PrivateForApi(namedText)
	if err != nil {
		t.Fatal(err)
	}
	emptyName, err := emptyA.Name(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	namedName, err := namedA.Name(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	emptyIsString, err := emptyName.IsString()
	if err != nil {
		t.Fatal(err)
	}
	emptyLength, err := emptyName.Length()
	if err != nil {
		t.Fatal(err)
	}
	namedNameText, err := namedName.StringValue()
	if err != nil {
		t.Fatal(err)
	}
	return privateObservation{
		dataEqual(t, emptyA, emptyB), emptyIsString, emptyLength == 0,
		!dataEqual(t, emptyA, freshNone), dataEqual(t, namedA, namedB), namedNameText,
	}
}

func TestRuntimeValuesResidualConformance(t *testing.T) {
	r := newRuntime(t)
	defer r.close(t)
	lines := []reportLine{
		{"runtime-values-residual/symbol/all_well_known", true, symbols(t, r)},
		{"runtime-values-residual/private/for_api_some_names", true, privateNames(t, r)},
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
	output.WriteString("{\"summary\":{\"total\":2,\"passed\":2,\"failed\":0}}\n")
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != string(want) {
		t.Fatalf("report differs from fixture:\nwant:\n%s\ngot:\n%s", want, output.String())
	}
}
