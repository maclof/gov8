//go:build windows && amd64

package conformance_external_references_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	gov8 "gov8"
)

const fixtureName = "conformance-external-references-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

func emit(t *testing.T, output *bytes.Buffer, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	output.Write(encoded)
	output.WriteByte('\n')
}

func integerText(t *testing.T, value string) int {
	t.Helper()
	integer, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	return integer
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func closeIsolate(t *testing.T, isolate *gov8.Isolate) {
	t.Helper()
	if err := gov8.ReleaseIsolateHostState(isolate); err != nil {
		t.Fatal(err)
	}
	if err := isolate.Close(); err != nil {
		t.Fatal(err)
	}
}

func evalText(t *testing.T, isolate *gov8.Isolate, source string) string {
	t.Helper()
	context, err := isolate.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := isolate.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	script, err := context.Compile(scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.ToString(context)
	if err != nil {
		t.Fatal(err)
	}
	if err := script.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := context.Close(); err != nil {
		t.Fatal(err)
	}
	return text
}

func makeEmptyBlob(t *testing.T) *gov8.StartupData {
	t.Helper()
	creator, err := gov8.NewSnapshotCreatorWithExternalReferences(nil)
	if err != nil {
		t.Fatal(err)
	}
	isolate := creator.Isolate()
	context, err := isolate.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := isolate.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	script, err := context.Compile(scope, "globalThis.snapshotted = 41", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := script.Run(scope, nil); err != nil {
		t.Fatal(err)
	}
	if err := creator.SetDefaultContext(context); err != nil {
		t.Fatal(err)
	}
	if err := script.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := context.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(gov8.FunctionCodeClear)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func referenceTable(t *testing.T, callback gov8.ExternalReference, pointer uintptr, terminated bool) []gov8.ExternalReference {
	t.Helper()
	references := []gov8.ExternalReference{callback, gov8.NewExternalReference(pointer)}
	if terminated {
		references = append(references, gov8.NewExternalReference(0))
	}
	return references
}

func makeExternalBlob(t *testing.T, callback gov8.ExternalReference) *gov8.StartupData {
	t.Helper()
	creator, err := gov8.NewSnapshotCreatorWithExternalReferences(referenceTable(t, callback, 1, false))
	if err != nil {
		t.Fatal(err)
	}
	isolate := creator.Isolate()
	scope, err := isolate.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	context, err := isolate.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	data, err := scope.NewExternal(1)
	if err != nil {
		t.Fatal(err)
	}
	template, err := isolate.NewFunctionTemplateFromExternalReference(scope, callback, data)
	if err != nil {
		t.Fatal(err)
	}
	function, err := template.GetFunction(scope, context)
	if err != nil {
		t.Fatal(err)
	}
	global, err := context.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := global.SetByName(scope, context, "externalValue", function.Value); err != nil || !ok {
		t.Fatalf("set externalValue = %v, %v", ok, err)
	}
	if err := creator.SetDefaultContext(context); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := context.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := creator.CreateBlob(gov8.FunctionCodeKeep)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func consumeExternalBlob(t *testing.T, blob *gov8.StartupData, references []gov8.ExternalReference) string {
	t.Helper()
	params := gov8.NewCreateParams().SetExternalReferences(references)
	isolate, err := gov8.NewIsolateFromSnapshotWithParams(blob, params)
	if err != nil {
		t.Fatal(err)
	}
	result := evalText(t, isolate, "externalValue()")
	closeIsolate(t, isolate)
	return result
}

func fixtureOutput(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	nullA := gov8.NewExternalReference(0)
	nullB := gov8.NewExternalReference(0)
	one := gov8.NewExternalReference(1)
	oneCopy := one
	two := gov8.NewExternalReference(2)
	functionA, err := gov8.NewCallbackExternalReference(gov8.ExternalReferenceFunction)
	if err != nil {
		t.Fatal(err)
	}
	functionB := functionA
	emit(t, &output, struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value struct {
			Size                    uintptr `json:"size"`
			Align                   uintptr `json:"align"`
			NullEqual               bool    `json:"null_equal"`
			CopyEqual               bool    `json:"copy_equal"`
			DifferentPointerUnequal bool    `json:"different_pointer_unequal"`
			FunctionCopyEqual       bool    `json:"function_copy_equal"`
			NullDebug               string  `json:"null_debug"`
		} `json:"value"`
	}{
		Check: "external-references/value_semantics", OK: true,
		Value: struct {
			Size                    uintptr `json:"size"`
			Align                   uintptr `json:"align"`
			NullEqual               bool    `json:"null_equal"`
			CopyEqual               bool    `json:"copy_equal"`
			DifferentPointerUnequal bool    `json:"different_pointer_unequal"`
			FunctionCopyEqual       bool    `json:"function_copy_equal"`
			NullDebug               string  `json:"null_debug"`
		}{unsafe.Sizeof(nullA), unsafe.Alignof(nullA), nullA == nullB, one == oneCopy, one != two, functionA == functionB, nullA.String()},
	})

	ordinary, err := gov8.NewIsolateWithParams(gov8.NewCreateParams().UseEmptyExternalReferences())
	if err != nil {
		t.Fatal(err)
	}
	ordinaryResult := evalText(t, ordinary, "40 + 2")
	closeIsolate(t, ordinary)
	emptyBlob := makeEmptyBlob(t)
	withoutTable, err := gov8.NewIsolateFromSnapshot(emptyBlob)
	if err != nil {
		t.Fatal(err)
	}
	withoutResult := evalText(t, withoutTable, "snapshotted + 1")
	closeIsolate(t, withoutTable)
	withTable, err := gov8.NewIsolateFromSnapshotWithParams(emptyBlob, gov8.NewCreateParams().UseEmptyExternalReferences())
	if err != nil {
		t.Fatal(err)
	}
	withResult := evalText(t, withTable, "snapshotted + 2")
	closeIsolate(t, withTable)
	emit(t, &output, struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value struct {
			Ordinary int  `json:"ordinary_isolate_result"`
			Nonempty bool `json:"blob_nonempty"`
			Valid    bool `json:"blob_valid"`
			Without  int  `json:"reuse_without_table"`
			With     int  `json:"reuse_with_empty_table"`
		} `json:"value"`
	}{
		Check: "external-references/empty_table", OK: true,
		Value: struct {
			Ordinary int  `json:"ordinary_isolate_result"`
			Nonempty bool `json:"blob_nonempty"`
			Valid    bool `json:"blob_valid"`
			Without  int  `json:"reuse_without_table"`
			With     int  `json:"reuse_with_empty_table"`
		}{integerText(t, ordinaryResult), !emptyBlob.IsEmpty(), emptyBlob.IsValid(), integerText(t, withoutResult), integerText(t, withResult)},
	})
	if ordinaryResult != "42" || withoutResult != "42" || withResult != "43" {
		t.Fatalf("empty-table observations = %q, %q, %q", ordinaryResult, withoutResult, withResult)
	}
	if err := emptyBlob.Release(); err != nil {
		t.Fatal(err)
	}

	externalBlob := makeExternalBlob(t, functionA)
	auto := consumeExternalBlob(t, externalBlob, referenceTable(t, functionA, 2, false))
	explicit := consumeExternalBlob(t, externalBlob, referenceTable(t, functionA, 3, true))
	third := consumeExternalBlob(t, externalBlob, referenceTable(t, functionA, 4, false))
	emit(t, &output, struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Value struct {
			Nonempty bool   `json:"blob_nonempty"`
			Valid    bool   `json:"blob_valid"`
			Dropped  bool   `json:"producer_dropped"`
			Auto     string `json:"auto_terminated_table_result"`
			Explicit string `json:"explicitly_terminated_table_result"`
			Third    string `json:"third_reuse_result"`
		} `json:"value"`
	}{
		Check: "external-references/snapshot_remap_and_reuse", OK: true,
		Value: struct {
			Nonempty bool   `json:"blob_nonempty"`
			Valid    bool   `json:"blob_valid"`
			Dropped  bool   `json:"producer_dropped"`
			Auto     string `json:"auto_terminated_table_result"`
			Explicit string `json:"explicitly_terminated_table_result"`
			Third    string `json:"third_reuse_result"`
		}{!externalBlob.IsEmpty(), externalBlob.IsValid(), true, auto, explicit, third},
	})
	if err := externalBlob.Release(); err != nil {
		t.Fatal(err)
	}
	emit(t, &output, struct {
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}{Summary: struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	}{3, 3, 0}})
	return output.Bytes()
}

func TestRustOracleFixture(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "rust-oracle", "tests", "fixtures", fixtureName))
	if err != nil {
		t.Fatal(err)
	}
	got := fixtureOutput(t)
	if !bytes.Equal(got, want) {
		t.Fatalf("external-reference fixture mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestMissingAndShortExternalReferenceTables(t *testing.T) {
	callback, err := gov8.NewCallbackExternalReference(gov8.ExternalReferenceFunction)
	if err != nil {
		t.Fatal(err)
	}
	blob := makeExternalBlob(t, callback)
	if isolate, err := gov8.NewIsolateFromSnapshot(blob); err == nil || isolate != nil ||
		!strings.Contains(err.Error(), "requires external references") {
		t.Fatalf("missing external-reference table = %v, %v", isolate, err)
	}
	if isolate, err := gov8.NewIsolateFromSnapshotWithParams(blob, gov8.NewCreateParams().UseEmptyExternalReferences()); err == nil || isolate != nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("empty external-reference table = %v, %v", isolate, err)
	}
	// Rust/V8 maps the producer's absent second entry to null when the
	// consumer supplies only the callback entry. Go preserves that behavior;
	// only a wholly missing table is normalized from fatal to an error.
	if got := consumeExternalBlob(t, blob, []gov8.ExternalReference{callback}); got != "0" {
		t.Fatalf("short external-reference table result = %q", got)
	}
	if err := blob.Release(); err != nil {
		t.Fatal(err)
	}
}
