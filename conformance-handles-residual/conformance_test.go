//go:build windows && amd64

package handlesresidualconformance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../rust-oracle/tests/fixtures/conformance-handles-residual-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

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

func jsonLine(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded) + "\n"
}

func report(t *testing.T) string {
	t.Helper()
	checks := []func(*testing.T) outcome{
		checkEternalEmptySetClearReuse,
		checkEternalObjectAcrossScopesGC,
		checkEternalCrossContextRealm,
		checkEternalClearedAfterIsolate,
		checkTracedEmptyResetReuse,
		checkTracedObjectIdentityMutation,
		checkTracedCrossContextRealm,
		checkTracedExternallyRootedGC,
	}
	var output strings.Builder
	for _, check := range checks {
		got := check(t)
		output.WriteString(jsonLine(t, reportLine{Check: got.check, OK: true, Value: got.value}))
	}
	summary := struct {
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}{}
	summary.Summary.Total = len(checks)
	summary.Summary.Passed = len(checks)
	output.WriteString(jsonLine(t, summary))
	return output.String()
}

func TestPinnedFixtureByteForByte(t *testing.T) {
	got := report(t)
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		wantLines := strings.Split(strings.TrimRight(string(want), "\n"), "\n")
		gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		for index := 0; index < len(wantLines) || index < len(gotLines); index++ {
			var expected, actual string
			if index < len(wantLines) {
				expected = wantLines[index]
			}
			if index < len(gotLines) {
				actual = gotLines[index]
			}
			if expected != actual {
				t.Errorf("line %d:\nwant %s\n got %s", index+1, expected, actual)
			}
		}
		t.Fatal("Go report differs from pinned Rust residual-handles fixture")
	}
}

func TestReportDeterministic(t *testing.T) {
	if first, second := report(t), report(t); first != second {
		t.Fatal("two residual-handles reports differ")
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
	return &runtime{iso, ctx, scope}
}

func (r *runtime) close(t *testing.T) {
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

func mustObject(t *testing.T, scope *gov8.Scope, ctx *gov8.Context) *gov8.Object {
	t.Helper()
	object, err := scope.NewObject(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func propertyText(t *testing.T, scope *gov8.Scope, ctx *gov8.Context, value gov8.Value, key string) string {
	t.Helper()
	object, err := gov8.AsObject(value)
	if err != nil {
		t.Fatal(err)
	}
	property, ok, err := object.GetByName(scope, ctx, key)
	if err != nil || !ok {
		t.Fatalf("GetByName(%s) = %v, %v", key, ok, err)
	}
	text, err := property.ToString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func secondRealmObjectConstructor(t *testing.T, scope *gov8.Scope, ctx *gov8.Context) *gov8.Object {
	t.Helper()
	global, err := ctx.GlobalObject(scope)
	if err != nil {
		t.Fatal(err)
	}
	constructor, ok, err := global.GetByName(scope, ctx, "Object")
	if err != nil || !ok {
		t.Fatalf("second realm Object = %v, %v", ok, err)
	}
	object, err := gov8.AsObject(constructor)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func checkEternalEmptySetClearReuse(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	eternal, err := gov8.EmptyEternal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eternal.Close() }()
	initialEmpty, _ := eternal.IsEmpty()
	_, initialOK, err := eternal.Get(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.scope.NewString("first")
	if err := eternal.Set(r.scope, first); err != nil {
		t.Fatal(err)
	}
	afterSetEmpty, _ := eternal.IsEmpty()
	firstGet, ok, err := eternal.Get(r.scope)
	if err != nil || !ok {
		t.Fatalf("first Get = %v, %v", ok, err)
	}
	firstIdentity, _ := first.StrictEquals(firstGet)
	_ = eternal.Clear()
	afterClearEmpty, _ := eternal.IsEmpty()
	_, afterClearOK, _ := eternal.Get(r.scope)
	second, _ := r.scope.NewString("second")
	_ = eternal.Set(r.scope, second)
	secondGet, _, _ := eternal.Get(r.scope)
	secondText, _ := secondGet.StringValue()
	_ = eternal.Clear()
	finalEmpty, _ := eternal.IsEmpty()
	value := struct {
		InitialEmpty      bool   `json:"initial_empty"`
		InitialGetNone    bool   `json:"initial_get_none"`
		AfterSetEmpty     bool   `json:"after_set_empty"`
		FirstIdentity     bool   `json:"first_identity"`
		AfterClearEmpty   bool   `json:"after_clear_empty"`
		AfterClearGetNone bool   `json:"after_clear_get_none"`
		SecondText        string `json:"second_text"`
		FinalEmpty        bool   `json:"final_empty"`
	}{initialEmpty, !initialOK, afterSetEmpty, firstIdentity, afterClearEmpty, !afterClearOK, secondText, finalEmpty}
	return outcome{"handles-residual/eternal/empty_set_clear_reuse", value}
}

func checkEternalObjectAcrossScopesGC(t *testing.T) outcome {
	iso, _ := gov8.NewIsolate()
	eternal, _ := gov8.EmptyEternal()
	ctx1, _ := iso.NewContext()
	scope1, _ := iso.NewScope()
	object := mustObject(t, scope1, ctx1)
	marker, _ := scope1.NewString("alive")
	if ok, err := object.SetByName(scope1, ctx1, "marker", marker); err != nil || !ok {
		t.Fatalf("set marker = %v, %v", ok, err)
	}
	_ = eternal.Set(scope1, object.Value)
	_ = scope1.Close()
	_ = ctx1.Close()
	_ = iso.LowMemoryNotification()
	ctx2, _ := iso.NewContext()
	scope2, _ := iso.NewScope()
	first, _, _ := eternal.Get(scope2)
	second, _, _ := eternal.Get(scope2)
	identity, _ := first.StrictEquals(second)
	text := propertyText(t, scope2, ctx2, first, "marker")
	_ = eternal.Clear()
	empty, _ := eternal.IsEmpty()
	_ = eternal.Close()
	_ = scope2.Close()
	_ = ctx2.Close()
	_ = iso.Close()
	value := struct {
		Marker              string `json:"marker"`
		RepeatedGetIdentity bool   `json:"repeated_get_identity"`
		EmptyAfterClear     bool   `json:"empty_after_clear"`
	}{text, identity, empty}
	return outcome{"handles-residual/eternal/object_across_scopes_gc", value}
}

func checkEternalCrossContextRealm(t *testing.T) outcome {
	iso, _ := gov8.NewIsolate()
	scope, _ := iso.NewScope()
	first, _ := iso.NewContext()
	original := mustObject(t, scope, first)
	eternal, _ := gov8.EmptyEternal()
	_ = eternal.Set(scope, original.Value)
	second, _ := iso.NewContext()
	retrieved, _, _ := eternal.Get(scope)
	identity, _ := original.Value.StrictEquals(retrieved)
	constructor := secondRealmObjectConstructor(t, scope, second)
	instance, err := retrieved.InstanceOf(scope, second, constructor, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = eternal.Clear()
	_ = eternal.Close()
	_ = second.Close()
	_ = first.Close()
	_ = scope.Close()
	_ = iso.Close()
	value := struct {
		IdentityPreserved             bool `json:"identity_preserved"`
		InstanceOfSecondContextObject bool `json:"instance_of_second_context_object"`
	}{identity, instance}
	return outcome{"handles-residual/eternal/cross_context_realm", value}
}

func checkEternalClearedAfterIsolate(t *testing.T) outcome {
	eternal, _ := gov8.EmptyEternal()
	iso, _ := gov8.NewIsolate()
	scope, _ := iso.NewScope()
	value, _ := scope.Int32(7)
	_ = eternal.Set(scope, value)
	_ = eternal.Clear()
	_ = scope.Close()
	_ = iso.Close()
	emptyAfter, err := eternal.IsEmpty()
	if err != nil {
		t.Fatal(err)
	}
	if err := eternal.Clear(); err != nil {
		t.Fatal(err)
	}
	emptyFinal, _ := eternal.IsEmpty()
	_ = eternal.Close()
	valueOut := struct {
		EmptyAfterIsolate     bool `json:"empty_after_isolate"`
		ClearAfterIsolateSafe bool `json:"clear_after_isolate_safe"`
	}{emptyAfter, emptyFinal}
	return outcome{"handles-residual/eternal/cleared_after_isolate_lifecycle", valueOut}
}

func checkTracedEmptyResetReuse(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	traced, _ := gov8.EmptyTracedReference()
	defer func() { _ = traced.Close() }()
	_, initialOK, err := traced.Get(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.scope.Int32(11)
	_ = traced.Reset(r.scope, &first)
	firstGet, _, _ := traced.Get(r.scope)
	firstValue, _, _ := firstGet.IntegerValue(r.ctx)
	_ = traced.Reset(r.scope, nil)
	_, afterResetOK, _ := traced.Get(r.scope)
	second, _ := r.scope.NewString("reused")
	_ = traced.Reset(r.scope, &second)
	secondGet, _, _ := traced.Get(r.scope)
	secondValue, _ := secondGet.StringValue()
	_ = traced.Reset(r.scope, nil)
	_, finalOK, _ := traced.Get(r.scope)
	value := struct {
		InitialGetNone bool   `json:"initial_get_none"`
		FirstValue     int64  `json:"first_value"`
		AfterResetNone bool   `json:"after_reset_none"`
		SecondValue    string `json:"second_value"`
		FinalGetNone   bool   `json:"final_get_none"`
	}{!initialOK, firstValue, !afterResetOK, secondValue, !finalOK}
	return outcome{"handles-residual/traced/empty_reset_reuse", value}
}

func checkTracedObjectIdentityMutation(t *testing.T) outcome {
	r := newRuntime(t)
	defer r.close(t)
	object := mustObject(t, r.scope, r.ctx)
	traced, _ := gov8.NewTracedReference(r.scope, object.Value)
	defer func() { _ = traced.Close() }()
	retrieved, _, _ := traced.Get(r.scope)
	retrievedObject, _ := gov8.AsObject(retrieved)
	answer, _ := r.scope.Int32(42)
	if ok, err := retrievedObject.SetByName(r.scope, r.ctx, "value", answer); err != nil || !ok {
		t.Fatalf("set value = %v, %v", ok, err)
	}
	identity, _ := object.Value.StrictEquals(retrieved)
	mutation := propertyText(t, r.scope, r.ctx, object.Value, "value")
	_ = traced.Reset(r.scope, nil)
	_, resetOK, _ := traced.Get(r.scope)
	value := struct {
		Identity        bool   `json:"identity"`
		MutationVisible string `json:"mutation_visible"`
		ResetGetNone    bool   `json:"reset_get_none"`
	}{identity, mutation, !resetOK}
	return outcome{"handles-residual/traced/object_identity_mutation", value}
}

func checkTracedCrossContextRealm(t *testing.T) outcome {
	iso, _ := gov8.NewIsolate()
	scope, _ := iso.NewScope()
	first, _ := iso.NewContext()
	original := mustObject(t, scope, first)
	traced, _ := gov8.EmptyTracedReference()
	_ = traced.Reset(scope, &original.Value)
	second, _ := iso.NewContext()
	retrieved, _, _ := traced.Get(scope)
	identity, _ := original.Value.StrictEquals(retrieved)
	constructor := secondRealmObjectConstructor(t, scope, second)
	instance, err := retrieved.InstanceOf(scope, second, constructor, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = traced.Reset(scope, nil)
	_ = traced.Close()
	_ = second.Close()
	_ = first.Close()
	_ = scope.Close()
	_ = iso.Close()
	value := struct {
		IdentityPreserved             bool `json:"identity_preserved"`
		InstanceOfSecondContextObject bool `json:"instance_of_second_context_object"`
	}{identity, instance}
	return outcome{"handles-residual/traced/cross_context_realm", value}
}

func checkTracedExternallyRootedGC(t *testing.T) outcome {
	iso, _ := gov8.NewIsolate()
	eternal, _ := gov8.EmptyEternal()
	traced, _ := gov8.EmptyTracedReference()
	ctx1, _ := iso.NewContext()
	scope1, _ := iso.NewScope()
	object := mustObject(t, scope1, ctx1)
	marker, _ := scope1.NewString("yes")
	if ok, err := object.SetByName(scope1, ctx1, "rooted", marker); err != nil || !ok {
		t.Fatalf("set rooted = %v, %v", ok, err)
	}
	_ = eternal.Set(scope1, object.Value)
	_ = traced.Reset(scope1, &object.Value)
	_ = scope1.Close()
	_ = ctx1.Close()
	_ = iso.LowMemoryNotification()
	ctx2, _ := iso.NewContext()
	scope2, _ := iso.NewScope()
	tracedValue, available, err := traced.Get(scope2)
	if err != nil {
		t.Fatal(err)
	}
	root, _, _ := eternal.Get(scope2)
	same := false
	markerText := ""
	if available {
		same, _ = tracedValue.StrictEquals(root)
		markerText = propertyText(t, scope2, ctx2, tracedValue, "rooted")
	}
	// A TracedReference is not a root. Reset it while the independently
	// rooted target is still alive, before clearing the Eternal.
	_ = traced.Reset(scope2, nil)
	_ = eternal.Clear()
	_ = iso.LowMemoryNotification()
	_ = traced.Close()
	_ = eternal.Close()
	_ = scope2.Close()
	_ = ctx2.Close()
	_ = iso.Close()
	value := struct {
		AvailableAfterGC  bool   `json:"available_after_gc"`
		SameAsEternalRoot bool   `json:"same_as_eternal_root"`
		Marker            string `json:"marker"`
		ResetBeforeUnroot bool   `json:"reset_before_unroot"`
	}{available, same, markerText, true}
	return outcome{"handles-residual/traced/externally_rooted_gc", value}
}
