//go:build windows && amd64

package exceptionstringlocalconformance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

const fixturePath = "../../rust-oracle/tests/fixtures/conformance-exception-string-local-v8_152.2.0_x86_64-pc-windows-msvc.jsonl"

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

type localInput struct {
	Kind  string
	Value gov8.Value
}

func inputs(t *testing.T, scope *gov8.Scope) []localInput {
	t.Helper()
	ordinary, err := scope.NewStringFromUTF8([]byte("héllo 🦀"), gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	embeddedNUL, err := scope.NewStringFromUTF8([]byte("left\x00right"), gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	loneSurrogate, err := scope.NewStringFromTwoByte([]uint16{0xd800, 'X'}, gov8.StringNormal)
	if err != nil {
		t.Fatal(err)
	}
	internalized, err := scope.NewStringFromUTF8([]byte("internalized-value"), gov8.StringInternalized)
	if err != nil {
		t.Fatal(err)
	}
	externalOneByte, err := scope.NewExternalOneByteString([]byte{'e', 'x', 't', '-', 0xa9})
	if err != nil {
		t.Fatal(err)
	}
	externalTwoByte, err := scope.NewExternalTwoByteString([]uint16{'e', 'x', 't', 0x20ac})
	if err != nil {
		t.Fatal(err)
	}
	return []localInput{
		{"ordinary_utf8", ordinary},
		{"embedded_nul", embeddedNUL},
		{"utf16_lone_surrogate", loneSurrogate},
		{"internalized", internalized},
		{"external_one_byte", externalOneByte},
		{"external_two_byte", externalTwoByte},
	}
}

type constructor struct {
	Kind string
	New  func(*gov8.Scope, gov8.Value) (gov8.Value, error)
}

func constructors(ctx *gov8.Context) []constructor {
	return []constructor{
		{"Error", ctx.NewErrorFromStringValue},
		{"RangeError", ctx.NewRangeErrorFromStringValue},
		{"ReferenceError", ctx.NewReferenceErrorFromStringValue},
		{"SyntaxError", ctx.NewSyntaxErrorFromStringValue},
		{"TypeError", ctx.NewTypeErrorFromStringValue},
	}
}

func property(t *testing.T, r *runtimeState, object *gov8.Object, name string) gov8.Value {
	t.Helper()
	value, ok, err := object.GetByName(r.scope, r.ctx, name)
	if err != nil || !ok {
		t.Fatalf("property %q: ok=%v err=%v", name, ok, err)
	}
	return value
}

func stringText(t *testing.T, value gov8.Value) string {
	t.Helper()
	text, err := value.StringValue()
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func codeUnits(t *testing.T, value gov8.Value) []uint16 {
	t.Helper()
	length, err := value.Length()
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, length)
	if length == 0 {
		return units
	}
	if written, err := value.WriteTwoByte(0, units, 0); err != nil || written != length {
		t.Fatalf("WriteTwoByte = %d, %v; want %d", written, err, length)
	}
	return units
}

type stringFlags struct {
	External        bool `json:"external"`
	ExternalOneByte bool `json:"external_one_byte"`
	ExternalTwoByte bool `json:"external_two_byte"`
}

func flags(t *testing.T, value gov8.Value) stringFlags {
	t.Helper()
	external, err := value.IsExternalString()
	if err != nil {
		t.Fatal(err)
	}
	one, err := value.IsExternalOneByte()
	if err != nil {
		t.Fatal(err)
	}
	two, err := value.IsExternalTwoByte()
	if err != nil {
		t.Fatal(err)
	}
	return stringFlags{external, one, two}
}

type constructorObservation struct {
	Kind                string `json:"kind"`
	IsObject            bool   `json:"is_object"`
	IsNativeError       bool   `json:"is_native_error"`
	IsString            bool   `json:"is_string"`
	ConstructorName     string `json:"constructor_name"`
	InstanceOfMatching  bool   `json:"instance_of_matching"`
	PrototypeIsMatching bool   `json:"prototype_is_matching"`
	ToString            string `json:"to_string"`
	Message             string `json:"message"`
	StackIsString       bool   `json:"stack_is_string"`
	Stack               string `json:"stack"`
	ExceptionStackNone  bool   `json:"exception_stack_none"`
	MessageStackNone    bool   `json:"message_stack_none"`
	UncaughtMessage     string `json:"uncaught_message"`
}

func fullObservation(t *testing.T, r *runtimeState, constructor constructor, input gov8.Value) constructorObservation {
	t.Helper()
	exception, err := constructor.New(r.scope, input)
	if err != nil {
		t.Fatal(err)
	}
	object, err := gov8.AsObject(exception)
	if err != nil {
		t.Fatal(err)
	}
	message := property(t, r, object, "message")
	stack := property(t, r, object, "stack")
	global, err := r.ctx.GlobalObject(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	constructorValue := property(t, r, global, constructor.Kind)
	constructorObject, err := gov8.AsObject(constructorValue)
	if err != nil {
		t.Fatal(err)
	}
	expectedPrototype := property(t, r, constructorObject, "prototype")
	actualPrototype, err := object.GetPrototype(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	prototypeMatches, err := actualPrototype.StrictEquals(expectedPrototype)
	if err != nil {
		t.Fatal(err)
	}
	instanceMatches, err := exception.InstanceOf(r.scope, r.ctx, constructorObject, nil)
	if err != nil {
		t.Fatal(err)
	}
	constructorNameValue, err := object.GetConstructorName(r.scope)
	if err != nil {
		t.Fatal(err)
	}
	createdMessage, err := r.ctx.CreateMessage(r.scope, exception)
	if err != nil {
		t.Fatal(err)
	}
	_, exceptionStackSome, err := r.ctx.GetExceptionStackTrace(r.scope, exception)
	if err != nil {
		t.Fatal(err)
	}
	_, messageStackSome, err := createdMessage.StackTrace()
	if err != nil {
		t.Fatal(err)
	}
	uncaught, err := createdMessage.Text(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	isObject, _ := exception.IsObject()
	isNativeError, _ := exception.IsNativeError()
	isString, _ := exception.IsString()
	stackIsString, _ := stack.IsString()
	toString, err := exception.ToString(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return constructorObservation{
		constructor.Kind, isObject, isNativeError, isString,
		stringText(t, constructorNameValue), instanceMatches, prototypeMatches,
		toString, stringText(t, message), stackIsString, stringText(t, stack),
		!exceptionStackSome, !messageStackSome, uncaught,
	}
}

type constructorGroup struct {
	InputKind    string                   `json:"input_kind"`
	Constructors []constructorObservation `json:"constructors"`
}

func checkFiveConstructors(t *testing.T) any {
	r := newRuntime(t)
	defer r.close(t)
	groups := make([]constructorGroup, 0, 6)
	for _, input := range inputs(t, r.scope) {
		observations := make([]constructorObservation, 0, 5)
		for _, constructor := range constructors(r.ctx) {
			observations = append(observations, fullObservation(t, r, constructor, input.Value))
		}
		groups = append(groups, constructorGroup{input.Kind, observations})
	}
	return groups
}

type identityConstructorObservation struct {
	Kind                 string      `json:"kind"`
	StrictEquals         bool        `json:"strict_equals"`
	PropertyText         string      `json:"property_text"`
	PropertyCodeUnits    []uint16    `json:"property_code_units"`
	PropertyFlags        stringFlags `json:"property_flags"`
	SameExternalResource bool        `json:"same_external_resource"`
}

type identityGroup struct {
	InputKind      string                           `json:"input_kind"`
	InputText      string                           `json:"input_text"`
	InputCodeUnits []uint16                         `json:"input_code_units"`
	InputFlags     stringFlags                      `json:"input_flags"`
	Constructors   []identityConstructorObservation `json:"constructors"`
}

func checkIdentity(t *testing.T) any {
	r := newRuntime(t)
	defer r.close(t)
	groups := make([]identityGroup, 0, 6)
	for _, input := range inputs(t, r.scope) {
		inputResource, _, inputResourceOK, err := input.Value.GetExternalStringResourceBase()
		if err != nil {
			t.Fatal(err)
		}
		observations := make([]identityConstructorObservation, 0, 5)
		for _, constructor := range constructors(r.ctx) {
			exception, err := constructor.New(r.scope, input.Value)
			if err != nil {
				t.Fatal(err)
			}
			object, err := gov8.AsObject(exception)
			if err != nil {
				t.Fatal(err)
			}
			message := property(t, r, object, "message")
			strict, err := input.Value.StrictEquals(message)
			if err != nil {
				t.Fatal(err)
			}
			propertyResource, _, propertyResourceOK, err := message.GetExternalStringResourceBase()
			if err != nil {
				t.Fatal(err)
			}
			observations = append(observations, identityConstructorObservation{
				constructor.Kind, strict,
				stringText(t, message), codeUnits(t, message), flags(t, message),
				inputResourceOK && propertyResourceOK && inputResource == propertyResource,
			})
		}
		groups = append(groups, identityGroup{
			input.Kind, stringText(t, input.Value), codeUnits(t, input.Value),
			flags(t, input.Value), observations,
		})
	}
	return groups
}

type reportLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

func jsonLine(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func report(t *testing.T) string {
	t.Helper()
	var output strings.Builder
	output.WriteString(jsonLine(t, reportLine{"exception-string-local/five_constructors_by_string_kind", true, checkFiveConstructors(t)}))
	output.WriteString(jsonLine(t, reportLine{"exception-string-local/input_and_message_identity", true, checkIdentity(t)}))
	summary := struct {
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}{}
	summary.Summary.Total = 2
	summary.Summary.Passed = 2
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
		t.Fatal("Go report differs from pinned Rust exception-string-local fixture")
	}
}

func TestReportDeterministic(t *testing.T) {
	if first, second := report(t), report(t); first != second {
		t.Fatal("two exception-string-local reports differ")
	}
}
