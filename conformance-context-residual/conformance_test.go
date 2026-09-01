//go:build windows && amd64

package contextresidualconformance

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	gov8 "gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-context-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type outcome struct {
	check string
	value any
}

type reportLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

type summaryLine struct {
	Summary struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

func encodeLine(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded) + "\n"
}

func runReport(t *testing.T) string {
	t.Helper()
	checks := []func(*testing.T) outcome{
		checkFromSnapshotOptions,
		checkFromSnapshotWithoutBlob,
		checkEmbedderDataGrowthAndPointer,
		checkClearSlotsBeforeSnapshot,
	}
	var report strings.Builder
	for _, check := range checks {
		result := check(t)
		report.WriteString(encodeLine(t, reportLine{Check: result.check, OK: true, Value: result.value}))
	}
	var summary summaryLine
	summary.Summary.Total = len(checks)
	summary.Summary.Passed = len(checks)
	report.WriteString(encodeLine(t, summary))
	return report.String()
}

func TestPinnedFixtureByteForByte(t *testing.T) {
	got := runReport(t)
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		wantLines := strings.Split(strings.TrimRight(string(want), "\n"), "\n")
		gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
			var expected, actual string
			if i < len(wantLines) {
				expected = wantLines[i]
			}
			if i < len(gotLines) {
				actual = gotLines[i]
			}
			if expected != actual {
				t.Errorf("line %d:\nwant %s\n got %s", i+1, expected, actual)
			}
		}
		t.Fatal("Go report differs from pinned Rust Context residual fixture")
	}
}

func TestReportDeterministic(t *testing.T) {
	first := runReport(t)
	second := runReport(t)
	if first != second {
		t.Fatal("two Context residual reports differ")
	}
}

type result[T any] struct {
	value T
	err   error
}

func resultOf[T any](value T, err error) result[T] {
	return result[T]{value: value, err: err}
}

func must[T any](t *testing.T, result result[T]) T {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	return result.value
}

func eval(t *testing.T, context *gov8.Context, scope *gov8.Scope, source string) gov8.Value {
	t.Helper()
	script := must(t, resultOf(context.Compile(scope, source, nil)))
	defer func() { _ = script.Close() }()
	return must(t, resultOf(script.Run(scope, nil)))
}

func evalText(t *testing.T, context *gov8.Context, scope *gov8.Scope, source string) string {
	t.Helper()
	return must(t, resultOf(eval(t, context, scope, source).ToString(context)))
}

func contextSnapshot(t *testing.T) (*gov8.StartupData, int) {
	t.Helper()
	creator := must(t, resultOf(gov8.NewSnapshotCreator()))
	isolate := creator.Isolate()
	scope := must(t, resultOf(isolate.NewScope()))
	defaultContext := must(t, resultOf(isolate.NewContext()))
	if err := creator.SetDefaultContext(defaultContext); err != nil {
		t.Fatal(err)
	}
	context := must(t, resultOf(isolate.NewContext()))
	_ = eval(t, context, scope, "globalThis.snapshotMarker = 'added-context'")
	index := must(t, resultOf(creator.AddContext(context)))
	_ = scope.Close()
	_ = context.Close()
	_ = defaultContext.Close()
	return must(t, resultOf(creator.CreateBlob(gov8.FunctionCodeClear))), index
}

func checkFromSnapshotOptions(t *testing.T) outcome {
	blob, index := contextSnapshot(t)
	defer func() { _ = blob.Release() }()
	isolate := must(t, resultOf(gov8.NewIsolateFromSnapshot(blob)))
	scope := must(t, resultOf(isolate.NewScope()))
	queue := must(t, resultOf(isolate.NewMicrotaskQueue(gov8.PolicyExplicit)))
	template := must(t, resultOf(isolate.NewObjectTemplate(scope)))
	templateValue := must(t, resultOf(scope.Int32(73)))
	if err := template.Set("ignoredTemplateValue", templateValue); err != nil {
		t.Fatal(err)
	}
	first, ok, err := scope.ContextFromSnapshotWithOptions(uint64(index), &gov8.ContextOptions{
		GlobalTemplate: template,
		MicrotaskQueue: queue,
	})
	if err != nil || !ok {
		t.Fatalf("first ContextFromSnapshotWithOptions = %v, %v", ok, err)
	}
	queueRaw := must(t, resultOf(queue.Raw()))
	queueAttachedFirst := must(t, resultOf(first.GetMicrotaskQueue())) == queueRaw
	markerFirst := evalText(t, first, scope, "snapshotMarker")
	templateIgnored := evalText(t, first, scope, "typeof ignoredTemplateValue")
	_ = eval(t, first, scope, "globalThis.transient = 99; globalThis.microtaskDone = false; Promise.resolve().then(() => microtaskDone = true)")
	microtaskBefore := evalText(t, first, scope, "microtaskDone") == "false"
	firstGlobal := must(t, resultOf(first.GlobalObject(scope)))
	if err := queue.PerformCheckpoint(first); err != nil {
		t.Fatal(err)
	}
	microtaskAfter := evalText(t, first, scope, "microtaskDone") == "true"

	second, ok, err := scope.ContextFromSnapshotWithOptions(uint64(index), &gov8.ContextOptions{
		GlobalObject:   firstGlobal,
		MicrotaskQueue: queue,
	})
	if err != nil || !ok {
		t.Fatalf("second ContextFromSnapshotWithOptions = %v, %v", ok, err)
	}
	secondGlobal := must(t, resultOf(second.GlobalObject(scope)))
	globalProxyReused := must(t, resultOf(firstGlobal.Value.SameValue(secondGlobal.Value)))
	markerSecond := evalText(t, second, scope, "snapshotMarker")
	transientReset := evalText(t, second, scope, "typeof transient")
	queueAttachedSecond := must(t, resultOf(second.GetMicrotaskQueue())) == queueRaw

	_ = second.Close()
	_ = first.Close()
	_ = scope.Close()
	_ = queue.Close()
	_ = isolate.Close()

	value := struct {
		SnapshotIndex              int    `json:"snapshot_index"`
		SnapshotMarkerFirst        string `json:"snapshot_marker_first"`
		GlobalTemplateFieldIgnored string `json:"global_template_field_ignored"`
		QueueAttachedFirst         bool   `json:"queue_attached_first"`
		MicrotaskBeforeCheckpoint  bool   `json:"microtask_before_checkpoint"`
		MicrotaskAfterCheckpoint   bool   `json:"microtask_after_checkpoint"`
		GlobalProxyReused          bool   `json:"global_proxy_reused"`
		SnapshotMarkerSecond       string `json:"snapshot_marker_second"`
		TransientAfterGlobalReuse  string `json:"transient_after_global_reuse"`
		QueueAttachedSecond        bool   `json:"queue_attached_second"`
	}{index, markerFirst, templateIgnored, queueAttachedFirst, microtaskBefore, microtaskAfter,
		globalProxyReused, markerSecond, transientReset, queueAttachedSecond}
	return outcome{"context-residual/from_snapshot_options", value}
}

func checkFromSnapshotWithoutBlob(t *testing.T) outcome {
	isolate := must(t, resultOf(gov8.NewIsolate()))
	scope := must(t, resultOf(isolate.NewScope()))
	zero, zeroOK, zeroErr := scope.ContextFromSnapshotWithOptions(0, nil)
	if zeroErr != nil || zero != nil {
		t.Fatalf("index zero = %v, %v, %v", zero, zeroOK, zeroErr)
	}
	below, belowOK, belowErr := scope.ContextFromSnapshotWithOptions(math.MaxUint64-1, nil)
	if belowErr != nil || below != nil {
		t.Fatalf("index MaxUint64-1 = %v, %v, %v", below, belowOK, belowErr)
	}
	maximum, maximumOK, err := scope.ContextFromSnapshotWithOptions(gov8.NoContextSnapshotIndex, nil)
	if err != nil {
		t.Fatal(err)
	}
	maximumHasBuiltins, maximumExecutes := false, false
	if maximumOK {
		maximumHasBuiltins = evalText(t, maximum, scope, "typeof Object") == "function"
		answer := eval(t, maximum, scope, "6 * 7")
		integer, ok, integerErr := answer.IntegerValue(maximum)
		maximumExecutes = integerErr == nil && ok && integer == 42
		_ = maximum.Close()
	}
	_ = scope.Close()
	_ = isolate.Close()

	value := struct {
		IndexZeroIsNone            bool `json:"index_zero_is_none"`
		IndexMaximumMinusOneIsNone bool `json:"index_usize_max_minus_one_is_none"`
		IndexMaximumIsSome         bool `json:"index_usize_max_is_some"`
		IndexMaximumHasBuiltins    bool `json:"index_usize_max_has_builtins"`
		IndexMaximumExecutes       bool `json:"index_usize_max_executes"`
	}{!zeroOK, !belowOK, maximumOK, maximumHasBuiltins, maximumExecutes}
	return outcome{"context-residual/from_snapshot_without_blob", value}
}

func checkEmbedderDataGrowthAndPointer(t *testing.T) outcome {
	isolate := must(t, resultOf(gov8.NewIsolate()))
	scope := must(t, resultOf(isolate.NewScope()))
	context := must(t, resultOf(isolate.NewContext()))
	stringValue := must(t, resultOf(scope.NewString("high-slot")))
	if err := context.SetEmbedderData(scope, 1024, stringValue); err != nil {
		t.Fatal(err)
	}
	stringRead, ok, err := context.GetEmbedderData(scope, 1024)
	if err != nil || !ok {
		t.Fatalf("read high slot = %v, %v", ok, err)
	}
	highIdentity := must(t, resultOf(stringRead.StrictEquals(stringValue)))
	highText := must(t, resultOf(stringRead.ToString(context)))
	object := must(t, resultOf(scope.NewObject(context)))
	if err := context.SetEmbedderData(scope, 1025, object.Value); err != nil {
		t.Fatal(err)
	}
	objectRead, ok, err := context.GetEmbedderData(scope, 1025)
	if err != nil || !ok {
		t.Fatalf("read adjacent slot = %v, %v", ok, err)
	}
	objectIdentity := must(t, resultOf(objectRead.StrictEquals(object.Value)))
	stringAgain, ok, err := context.GetEmbedderData(scope, 1024)
	if err != nil || !ok {
		t.Fatalf("read high slot again = %v, %v", ok, err)
	}
	highUnchanged := must(t, resultOf(stringAgain.StrictEquals(stringValue)))

	if err := context.SetAlignedPointerInEmbedderData(8, 0); err != nil {
		t.Fatal(err)
	}
	nullRoundtrip := must(t, resultOf(context.GetAlignedPointerFromEmbedderData(8))) == 0
	const pointer = uintptr(0x12345678)
	if err := context.SetAlignedPointerInEmbedderData(8, pointer); err != nil {
		t.Fatal(err)
	}
	pointerRoundtrip := must(t, resultOf(context.GetAlignedPointerFromEmbedderData(8))) == pointer
	if err := context.SetAlignedPointerInEmbedderData(8, 0); err != nil {
		t.Fatal(err)
	}
	clearedPointer := must(t, resultOf(context.GetAlignedPointerFromEmbedderData(8))) == 0
	_ = context.Close()
	_ = scope.Close()
	_ = isolate.Close()

	value := struct {
		HighSlot                int    `json:"high_slot"`
		HighSlotIdentity        bool   `json:"high_slot_identity"`
		HighSlotText            string `json:"high_slot_text"`
		AdjacentObjectIdentity  bool   `json:"adjacent_object_identity"`
		HighSlotUnchanged       bool   `json:"high_slot_unchanged"`
		NullPointerRoundtrip    bool   `json:"null_pointer_roundtrip"`
		NonNullPointerRoundtrip bool   `json:"nonnull_pointer_roundtrip"`
		ClearedPointerRoundtrip bool   `json:"cleared_pointer_roundtrip"`
	}{1024, highIdentity, highText, objectIdentity, highUnchanged, nullRoundtrip, pointerRoundtrip, clearedPointer}
	return outcome{"context-residual/embedder_data_growth_and_pointer", value}
}

func checkClearSlotsBeforeSnapshot(t *testing.T) outcome {
	creator := must(t, resultOf(gov8.NewSnapshotCreator()))
	isolate := creator.Isolate()
	scope := must(t, resultOf(isolate.NewScope()))
	context := must(t, resultOf(isolate.NewContext()))
	_, wasEmpty := context.SetSlot("string", "host-only")
	_, slotPresent := context.GetSlot("string")
	context.ClearAllSlots()
	_ = eval(t, context, scope, "globalThis.afterClear = 42")
	if err := creator.SetDefaultContext(context); err != nil {
		t.Fatal(err)
	}
	_ = scope.Close()
	_ = context.Close()
	blob := must(t, resultOf(creator.CreateBlob(gov8.FunctionCodeClear)))
	valid := blob.IsValid()
	consumer := must(t, resultOf(gov8.NewIsolateFromSnapshot(blob)))
	consumerScope := must(t, resultOf(consumer.NewScope()))
	restoredContext := must(t, resultOf(consumer.NewContext()))
	_, slotAfterRestore := restoredContext.GetSlot("string")
	restoredValue := eval(t, restoredContext, consumerScope, "afterClear")
	restored, ok, err := restoredValue.IntegerValue(restoredContext)
	if err != nil || !ok {
		t.Fatalf("snapshot value = %d, %v, %v", restored, ok, err)
	}
	_ = restoredContext.Close()
	_ = consumerScope.Close()
	_ = consumer.Close()
	_ = blob.Release()

	value := struct {
		SlotPresentBeforeClear bool  `json:"slot_present_before_clear"`
		SlotAbsentAfterRestore bool  `json:"slot_absent_after_restore"`
		BlobValid              bool  `json:"blob_valid"`
		SnapshotValue          int64 `json:"snapshot_value"`
	}{wasEmpty && slotPresent, !slotAfterRestore, valid, restored}
	return outcome{"context-residual/clear_slots_before_snapshot", value}
}
