//go:build windows && amd64

package inspectorobjectwrappingconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	gov8 "github.com/maclof/gov8"
	"github.com/maclof/gov8/internal/prebuilt"
)

type fixtureLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

type nullChannel struct{}

func (nullChannel) SendResponse(int32, *gov8.InspectorStringBuffer) {}
func (nullChannel) SendNotification(*gov8.InspectorStringBuffer)    {}
func (nullChannel) FlushProtocolNotifications()                     {}

var (
	testDLL  *syscall.DLL
	testOnce sync.Once
	testErr  error
)

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-inspector-object-wrapping-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	result := make(map[string]fixtureLine)
	expected := []string{
		"inspector-object-wrapping/wrap_presence_metadata",
		"inspector-object-wrapping/unwrap_identity_mutation",
		"inspector-object-wrapping/object_group_views",
		"inspector-object-wrapping/release_and_invalid_ids",
		"inspector-object-wrapping/cross_context",
		"inspector-object-wrapping/remote_object_lifetime",
	}
	checkIndex := 0
	seenSummary := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line struct {
			fixtureLine
			Summary *struct {
				Total  int `json:"total"`
				Passed int `json:"passed"`
				Failed int `json:"failed"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Check != "" {
			if seenSummary {
				t.Fatalf("fixture check %q appears after summary", line.Check)
			}
			if checkIndex >= len(expected) {
				t.Fatalf("unexpected extra fixture check %q", line.Check)
			}
			if line.Check != expected[checkIndex] {
				t.Fatalf("fixture check %d = %q, want %q", checkIndex, line.Check, expected[checkIndex])
			}
			if _, duplicate := result[line.Check]; duplicate {
				t.Fatalf("duplicate fixture check %q", line.Check)
			}
			if !line.OK {
				t.Fatalf("fixture check %q is not successful", line.Check)
			}
			result[line.Check] = line.fixtureLine
			checkIndex++
			continue
		}
		if line.Summary != nil {
			if seenSummary {
				t.Fatal("duplicate fixture summary")
			}
			seenSummary = true
			if line.Summary.Total != len(expected) || line.Summary.Passed != len(expected) || line.Summary.Failed != 0 {
				t.Fatalf("fixture summary = %+v", *line.Summary)
			}
			continue
		}
		t.Fatalf("fixture contains an unknown JSONL record: %s", scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) != len(expected) || checkIndex != len(expected) || !seenSummary {
		t.Fatalf("fixture completeness = checks %d/%d, summary %v", checkIndex, len(expected), seenSummary)
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, id string, got any) {
	t.Helper()
	want, ok := fixtures[id]
	if !ok || !want.OK {
		t.Fatalf("missing/failed fixture %s", id)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, gotJSON, wantJSON)
	}
}

func testProc(t *testing.T, name string) *syscall.Proc {
	t.Helper()
	testOnce.Do(func() {
		path := os.Getenv("GOV8_SHIM_DLL")
		if path == "" {
			path, testErr = prebuilt.Path()
			if testErr != nil {
				return
			}
		}
		testDLL, testErr = syscall.LoadDLL(path)
	})
	if testErr != nil {
		t.Fatal(testErr)
	}
	result, err := testDLL.FindProc(name)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func dataPointer(data []byte) uintptr {
	if len(data) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&data[0]))
}

func cborJSON(t *testing.T, cbor []byte) string {
	t.Helper()
	var length uintptr
	status, _, _ := testProc(t, "gov8_iow_test_cbor_to_json").Call(
		dataPointer(cbor), uintptr(len(cbor)), 0, 0, uintptr(unsafe.Pointer(&length)))
	runtime.KeepAlive(cbor)
	if int64(status) != -4 && int64(status) != 0 {
		t.Fatalf("CBOR JSON size status = %d", int64(status))
	}
	result := make([]byte, int(length))
	status, _, _ = testProc(t, "gov8_iow_test_cbor_to_json").Call(
		dataPointer(cbor), uintptr(len(cbor)), dataPointer(result), uintptr(len(result)),
		uintptr(unsafe.Pointer(&length)))
	if int64(status) != 0 {
		t.Fatalf("CBOR JSON copy status = %d", int64(status))
	}
	runtime.KeepAlive(cbor)
	runtime.KeepAlive(result)
	return string(result)
}

func remoteJSON(t *testing.T, remote *gov8.InspectorRemoteObject) string {
	t.Helper()
	data, err := remote.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return cborJSON(t, data)
}

func jsonString(t *testing.T, source, key string) (string, bool) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(source), &decoded); err != nil {
		t.Fatal(err)
	}
	value, ok := decoded[key].(string)
	return value, ok
}

func objectID(t *testing.T, remote *gov8.InspectorRemoteObject) string {
	t.Helper()
	id, ok := jsonString(t, remoteJSON(t, remote), "objectId")
	if !ok || id == "" {
		t.Fatal("remote object has no objectId")
	}
	return id
}

func normalizedRemoteJSON(t *testing.T, remote *gov8.InspectorRemoteObject) string {
	t.Helper()
	source := remoteJSON(t, remote)
	if id, ok := jsonString(t, source, "objectId"); ok {
		return strings.Replace(source, id, "<object-id>", 1)
	}
	return source
}

func viewObservation(view gov8.InspectorStringView) map[string]any {
	units := make([]uint16, 0, view.Len())
	if view.Is8Bit() {
		data, _ := view.Characters8()
		for _, value := range data {
			units = append(units, uint16(value))
		}
	} else {
		units, _ = view.Characters16()
	}
	return map[string]any{
		"is_8bit": view.Is8Bit(), "is_empty": view.IsEmpty(),
		"len": view.Len(), "units": units, "text": view.String(),
	}
}

func errorObservation(t *testing.T, err error) map[string]any {
	t.Helper()
	var objectErr *gov8.InspectorObjectIDError
	if !errors.As(err, &objectErr) {
		t.Fatalf("error type = %T: %v", err, err)
	}
	return viewObservation(objectErr.Message())
}

type runtimeState struct {
	iso       *gov8.Isolate
	inspector *gov8.Inspector
	session   *gov8.InspectorSession
	ctx       *gov8.Context
	scope     *gov8.Scope
}

func newRuntime(t *testing.T) *runtimeState {
	t.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	session, err := inspector.Connect(1, nullChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
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
	result := &runtimeState{iso: iso, inspector: inspector, session: session, ctx: ctx, scope: scope}
	t.Cleanup(func() { result.close(t) })
	return result
}

func (r *runtimeState) close(t *testing.T) {
	t.Helper()
	if r.session != nil {
		if err := r.session.Close(); err != nil {
			t.Error(err)
		} else {
			r.session = nil
		}
	}
	if r.scope != nil {
		if err := r.scope.Close(); err != nil {
			t.Error(err)
		} else {
			r.scope = nil
		}
	}
	if r.ctx != nil {
		if err := r.ctx.Close(); err != nil {
			t.Error(err)
		} else {
			r.ctx = nil
		}
	}
	if r.inspector != nil {
		if err := r.inspector.Close(); err != nil {
			t.Error(err)
		} else {
			r.inspector = nil
		}
	}
	if r.iso != nil {
		if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
			t.Error(err)
		}
		if err := r.iso.Close(); err != nil {
			t.Error(err)
		} else {
			r.iso = nil
		}
	}
}

func eval(t *testing.T, context *gov8.Context, scope *gov8.Scope, source string) gov8.Value {
	t.Helper()
	script, err := context.Compile(scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := script.Close(); err != nil {
			t.Error(err)
		}
	}()
	value, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func integerProperty(t *testing.T, context *gov8.Context, scope *gov8.Scope, value gov8.Value, name string) any {
	t.Helper()
	object, err := gov8.AsObject(value)
	if err != nil {
		t.Fatal(err)
	}
	property, ok, err := object.GetByName(scope, context, name)
	if err != nil || !ok {
		t.Fatalf("get %s = %v, %v", name, ok, err)
	}
	result, ok, err := property.IntegerValue(context)
	if err != nil || !ok {
		t.Fatalf("integer %s = %d, %v, %v", name, result, ok, err)
	}
	return result
}

func TestRustOracleFixture(t *testing.T) {
	fixtures := fixture(t)

	t.Run("session_observations", func(t *testing.T) {
		r := newRuntime(t)
		otherContext, err := r.iso.NewContext()
		if err != nil {
			t.Fatal(err)
		}
		mismatchSession, err := r.inspector.Connect(2, nullChannel{}, gov8.EmptyInspectorStringView(), gov8.InspectorFullyTrusted)
		if err != nil {
			t.Fatal(err)
		}
		var remotes []*gov8.InspectorRemoteObject
		defer func() {
			for _, remote := range remotes {
				if remote != nil {
					_ = remote.Close()
				}
			}
		}()

		primitive, err := r.scope.Number(7.5)
		if err != nil {
			t.Fatal(err)
		}
		object, err := r.scope.NewObject(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		initial, err := r.scope.Int32(42)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := object.SetByName(r.scope, r.ctx, "marker", initial); err != nil || !ok {
			t.Fatalf("set marker = %v, %v", ok, err)
		}
		before, beforePresent, err := r.session.WrapObject(r.scope, r.ctx, object.Value, gov8.EmptyInspectorStringView(), false)
		if err != nil {
			t.Fatal(err)
		}
		if before != nil {
			remotes = append(remotes, before)
		}
		if err := r.inspector.ContextCreated(r.ctx, 1, gov8.EmptyInspectorStringView(), gov8.NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
			t.Fatal(err)
		}
		if err := r.inspector.ContextCreated(otherContext, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
			t.Fatal(err)
		}
		mismatch, mismatchPresent, err := mismatchSession.WrapObject(r.scope, r.ctx, object.Value, gov8.EmptyInspectorStringView(), false)
		if err != nil {
			t.Fatal(err)
		}
		if mismatch != nil {
			remotes = append(remotes, mismatch)
		}
		primitiveRemote, primitivePresent, err := r.session.WrapObject(r.scope, r.ctx, primitive, gov8.EmptyInspectorStringView(), false)
		if err != nil || !primitivePresent {
			t.Fatalf("primitive wrap = %v, %v", primitivePresent, err)
		}
		remotes = append(remotes, primitiveRemote)
		noPreview, afterPresent, err := r.session.WrapObject(r.scope, r.ctx, object.Value, gov8.EmptyInspectorStringView(), false)
		if err != nil || !afterPresent {
			t.Fatalf("no-preview wrap = %v, %v", afterPresent, err)
		}
		remotes = append(remotes, noPreview)
		preview, previewPresent, err := r.session.WrapObject(r.scope, r.ctx, object.Value, gov8.EmptyInspectorStringView(), true)
		if err != nil || !previewPresent {
			t.Fatalf("preview wrap = %v, %v", previewPresent, err)
		}
		remotes = append(remotes, preview)
		primitiveJSON := remoteJSON(t, primitiveRemote)
		noPreviewJSON := remoteJSON(t, noPreview)
		previewJSON := remoteJSON(t, preview)
		primitiveType, _ := jsonString(t, primitiveJSON, "type")
		objectType, _ := jsonString(t, noPreviewJSON, "type")
		className, _ := jsonString(t, noPreviewJSON, "className")
		compare(t, fixtures, "inspector-object-wrapping/wrap_presence_metadata", map[string]any{
			"before_registration": beforePresent, "after_registration": afterPresent,
			"context_group_mismatch": mismatchPresent,
			"primitive": map[string]any{
				"type": primitiveType, "has_value_7_5": strings.Contains(primitiveJSON, `"value":7.5`),
				"has_object_id":        strings.Contains(primitiveJSON, `"objectId"`),
				"normalized_cbor_json": normalizedRemoteJSON(t, primitiveRemote),
			},
			"object_without_preview": map[string]any{
				"type": objectType, "class_name": className,
				"has_object_id":        strings.Contains(noPreviewJSON, `"objectId"`),
				"has_preview":          strings.Contains(noPreviewJSON, `"preview"`),
				"normalized_cbor_json": normalizedRemoteJSON(t, noPreview),
			},
			"object_with_preview": map[string]any{
				"has_preview":          strings.Contains(previewJSON, `"preview"`),
				"preview_has_marker":   strings.Contains(previewJSON, "marker"),
				"preview_has_42":       strings.Contains(previewJSON, "42"),
				"normalized_cbor_json": normalizedRemoteJSON(t, preview),
			},
		})

		identityRemote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value,
			gov8.NewInspectorStringView8([]byte("identity")), false)
		if err != nil || !present {
			t.Fatalf("identity wrap = %v, %v", present, err)
		}
		remotes = append(remotes, identityRemote)
		id := objectID(t, identityRemote)
		firstValue, firstContext, firstGroup, err := r.session.UnwrapObject(r.scope, gov8.NewInspectorStringView8([]byte(id)))
		if err != nil {
			t.Fatal(err)
		}
		firstIdentity, err := firstValue.StrictEquals(object.Value)
		if err != nil {
			t.Fatal(err)
		}
		changed, err := r.scope.Int32(99)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := object.SetByName(r.scope, r.ctx, "marker", changed); err != nil || !ok {
			t.Fatalf("mutate marker = %v, %v", ok, err)
		}
		secondValue, secondContext, secondGroup, err := r.session.UnwrapObject(r.scope, gov8.NewInspectorStringView8([]byte(id)))
		if err != nil {
			t.Fatal(err)
		}
		repeatedIdentity, err := firstValue.StrictEquals(secondValue)
		if err != nil {
			t.Fatal(err)
		}
		if err := identityRemote.Close(); err != nil {
			t.Fatal(err)
		}
		remotes[len(remotes)-1] = nil
		localAfterClose, err := secondValue.StrictEquals(object.Value)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "inspector-object-wrapping/unwrap_identity_mutation", map[string]any{
			"value_strict_equals": firstIdentity, "context_identity": firstContext == r.ctx,
			"repeated_value_identity":    repeatedIdentity,
			"repeated_context_identity":  firstContext == secondContext,
			"mutation_value":             integerProperty(t, r.ctx, r.scope, firstValue, "marker"),
			"first_group":                viewObservation(firstGroup.StringView()),
			"second_group":               viewObservation(secondGroup.StringView()),
			"locals_survive_remote_drop": localAfterClose,
		})

		groupCases := []struct {
			name string
			view gov8.InspectorStringView
		}{
			{"u8_ascii", gov8.NewInspectorStringView8([]byte("ascii"))},
			{"u16_non_ascii", gov8.NewInspectorStringView16([]uint16{'w', 'i', 'd', 'e', 0x03a9})},
			{"u8_nul", gov8.NewInspectorStringView8([]byte{'n', 'u', 'l', 0, 'g', 'r', 'o', 'u', 'p'})},
			{"empty_u8", gov8.EmptyInspectorStringView()},
			{"empty_u16", gov8.NewInspectorStringView16(nil)},
		}
		groupObservations := make([]any, 0, len(groupCases))
		for _, item := range groupCases {
			remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value, item.view, false)
			if err != nil || !present {
				t.Fatalf("%s wrap = %v, %v", item.name, present, err)
			}
			id := objectID(t, remote)
			_, returnedContext, returnedGroup, err := r.session.UnwrapObject(r.scope, gov8.NewInspectorStringView8([]byte(id)))
			if err != nil {
				t.Fatal(err)
			}
			groupObservations = append(groupObservations, map[string]any{
				"case": item.name, "context_identity": returnedContext == r.ctx,
				"group": viewObservation(returnedGroup.StringView()),
			})
			if err := remote.Close(); err != nil {
				t.Fatal(err)
			}
		}
		compare(t, fixtures, "inspector-object-wrapping/object_group_views", groupObservations)

		releaseGroup := gov8.NewInspectorStringView8([]byte("release-me"))
		releaseRemote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value, releaseGroup, false)
		if err != nil || !present {
			t.Fatalf("release wrap = %v, %v", present, err)
		}
		releaseID := objectID(t, releaseRemote)
		if err := r.session.ReleaseObjectGroup(releaseGroup); err != nil {
			t.Fatal(err)
		}
		_, _, _, releasedErr := r.session.UnwrapObject(r.scope, gov8.NewInspectorStringView8([]byte(releaseID)))
		invalidCases := []struct {
			name string
			view gov8.InspectorStringView
		}{
			{"invalid_u8", gov8.NewInspectorStringView8([]byte("not-an-object-id"))},
			{"invalid_u16", gov8.NewInspectorStringView16([]uint16{'n', 'o', 't'})},
			{"embedded_nul", gov8.NewInspectorStringView8([]byte{'b', 'a', 'd', 0, 'i', 'd'})},
			{"empty_u8", gov8.EmptyInspectorStringView()},
			{"empty_u16", gov8.NewInspectorStringView16(nil)},
		}
		invalidObservations := make([]any, 0, len(invalidCases))
		for _, item := range invalidCases {
			_, _, _, err := r.session.UnwrapObject(r.scope, item.view)
			invalidObservations = append(invalidObservations, map[string]any{
				"case": item.name, "error": errorObservation(t, err),
			})
		}
		compare(t, fixtures, "inspector-object-wrapping/release_and_invalid_ids", map[string]any{
			"released": errorObservation(t, releasedErr), "invalid": invalidObservations,
		})
		if err := releaseRemote.Close(); err != nil {
			t.Fatal(err)
		}

		otherValue := eval(t, otherContext, r.scope, "({other:17})")
		crossRemote, present, err := r.session.WrapObject(r.scope, r.ctx, otherValue,
			gov8.NewInspectorStringView8([]byte("cross")), false)
		if err != nil || !present {
			t.Fatalf("cross wrap = %v, %v", present, err)
		}
		crossID := objectID(t, crossRemote)
		crossValue, crossContext, crossGroup, err := r.session.UnwrapObject(r.scope, gov8.NewInspectorStringView8([]byte(crossID)))
		if err != nil {
			t.Fatal(err)
		}
		crossIdentity, err := crossValue.StrictEquals(otherValue)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "inspector-object-wrapping/cross_context", map[string]any{
			"value_identity": crossIdentity, "returned_supplied_context": crossContext == r.ctx,
			"returned_other_context": crossContext == otherContext,
			"other_property":         integerProperty(t, otherContext, r.scope, crossValue, "other"),
			"group":                  viewObservation(crossGroup.StringView()),
		})
		if err := crossRemote.Close(); err != nil {
			t.Fatal(err)
		}

		if err := r.inspector.ContextDestroyed(otherContext); err != nil {
			t.Fatal(err)
		}
		if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
			t.Fatal(err)
		}
		if err := mismatchSession.Close(); err != nil {
			t.Fatal(err)
		}
		if err := otherContext.Close(); err != nil {
			t.Fatal(err)
		}
		r.close(t)

	})

	t.Run("remote_object_lifetime", func(t *testing.T) {
		r := newRuntime(t)
		if err := r.inspector.ContextCreated(r.ctx, 1, gov8.EmptyInspectorStringView(), gov8.EmptyInspectorStringView()); err != nil {
			t.Fatal(err)
		}
		object, err := r.scope.NewObject(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		remote, present, err := r.session.WrapObject(r.scope, r.ctx, object.Value, gov8.EmptyInspectorStringView(), true)
		if err != nil || !present {
			t.Fatalf("wrap = %v, %v", present, err)
		}
		before, err := remote.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		repeat, err := remote.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.session.Close(); err != nil {
			t.Fatal(err)
		}
		r.session = nil
		afterSession, err := remote.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
			t.Fatal(err)
		}
		if err := r.scope.Close(); err != nil {
			t.Fatal(err)
		}
		r.scope = nil
		if err := r.ctx.Close(); err != nil {
			t.Fatal(err)
		}
		r.ctx = nil
		if err := r.inspector.Close(); err != nil {
			t.Fatal(err)
		}
		r.inspector = nil
		afterInspector, err := remote.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
			t.Fatal(err)
		}
		if err := r.iso.Close(); err != nil {
			t.Fatal(err)
		}
		r.iso = nil
		afterIsolate, err := remote.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fixtures, "inspector-object-wrapping/remote_object_lifetime", map[string]any{
			"nonempty": len(before) != 0, "repeat_equal": bytes.Equal(before, repeat),
			"after_session_equal":   bytes.Equal(before, afterSession),
			"after_inspector_equal": bytes.Equal(before, afterInspector),
			"after_isolate_equal":   bytes.Equal(before, afterIsolate),
		})
		if err := remote.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
