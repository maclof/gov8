//go:build windows && amd64

package inspectorsessioncontrolsconformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gov8 "github.com/maclof/gov8"
)

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type channel struct {
	responses     map[int32][]byte
	notifications [][]byte
	flushes       int
}

func (c *channel) SendResponse(id int32, message *gov8.InspectorStringBuffer) {
	c.responses[id] = []byte(message.StringView().String())
}
func (c *channel) SendNotification(message *gov8.InspectorStringBuffer) {
	c.notifications = append(c.notifications, []byte(message.StringView().String()))
}
func (c *channel) FlushProtocolNotifications() { c.flushes++ }

type client struct {
	groups []int32
	quits  int
}

func (c *client) RunMessageLoopOnPause(group int32) { c.groups = append(c.groups, group) }
func (c *client) QuitMessageLoopOnPause()           { c.quits++ }

type outcome struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

type propertyResult struct {
	Success bool `json:"success"`
	Error   any  `json:"error"`
}

func dispatch(t *testing.T, session *gov8.InspectorSession, c *channel, id int32, request string) []byte {
	t.Helper()
	delete(c.responses, id)
	if err := session.DispatchProtocolMessage(gov8.NewInspectorStringView8([]byte(request))); err != nil {
		t.Fatal(err)
	}
	response := c.responses[id]
	if response == nil {
		t.Fatalf("request %d produced no response", id)
	}
	return response
}

func evaluateObject(t *testing.T, session *gov8.InspectorSession, c *channel, id int32, group *string) string {
	t.Helper()
	params := map[string]any{"expression": "({marker:42})", "contextId": 1}
	if group != nil {
		params["objectGroup"] = *group
	}
	request, _ := json.Marshal(map[string]any{"id": id, "method": "Runtime.evaluate", "params": params})
	response := dispatch(t, session, c, id, string(request))
	var decoded struct {
		Result struct {
			Result struct {
				ObjectID string `json:"objectId"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil || decoded.Result.Result.ObjectID == "" {
		t.Fatalf("evaluate object response = %s, %v", response, err)
	}
	return decoded.Result.Result.ObjectID
}

func properties(t *testing.T, session *gov8.InspectorSession, c *channel, id int32, objectID string) propertyResult {
	t.Helper()
	request, _ := json.Marshal(map[string]any{"id": id, "method": "Runtime.getProperties", "params": map[string]any{"objectId": objectID}})
	response := dispatch(t, session, c, id, string(request))
	var decoded struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error == nil {
		return propertyResult{Success: true, Error: nil}
	}
	return propertyResult{Success: false, Error: decoded.Error.Message}
}

func runScript(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, source string) int64 {
	t.Helper()
	script, err := ctx.Compile(scope, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	value, err := script.Run(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := value.IntegerValue(ctx)
	if err != nil || !ok {
		t.Fatalf("IntegerValue = %d, %v, %v", result, ok, err)
	}
	return result
}

func countMethod(messages [][]byte, method string) int {
	want := `"method":"` + method + `"`
	count := 0
	for _, message := range messages {
		if bytes.Contains(message, []byte(want)) {
			count++
		}
	}
	return count
}

func pausedFields(messages [][]byte) (reason string, tag any) {
	for _, message := range messages {
		var notification struct {
			Method string `json:"method"`
			Params struct {
				Reason  string `json:"reason"`
				Details struct {
					Tag *string `json:"tag"`
				} `json:"data"`
			} `json:"params"`
		}
		if json.Unmarshal(message, &notification) == nil && notification.Method == "Debugger.paused" {
			if notification.Params.Details.Tag != nil {
				tag = *notification.Params.Details.Tag
			}
			return notification.Params.Reason, tag
		}
	}
	return "", nil
}

func boolDispatch(t *testing.T, view gov8.InspectorStringView) bool {
	t.Helper()
	value, err := gov8.InspectorCanDispatchMethod(view)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestInspectorSessionControlsFixture(t *testing.T) {
	results := []outcome{{
		Check: "inspector-session-controls/can_dispatch_method", OK: true,
		Value: struct {
			KnownU8                     bool `json:"known_u8"`
			KnownU16                    bool `json:"known_u16"`
			UnknownDomainU8             bool `json:"unknown_domain_u8"`
			KnownPrefixUnknownMethodU16 bool `json:"known_prefix_unknown_method_u16"`
			EmbeddedNULAfterKnownPrefix bool `json:"embedded_nul_after_known_prefix_u8"`
			EmptyU8                     bool `json:"empty_u8"`
			EmptyU16                    bool `json:"empty_u16"`
			StaticEmpty                 bool `json:"static_empty"`
		}{
			boolDispatch(t, gov8.NewInspectorStringView8([]byte("Runtime.evaluate"))),
			boolDispatch(t, gov8.NewInspectorStringView16([]uint16{'D', 'e', 'b', 'u', 'g', 'g', 'e', 'r', '.', 'e', 'n', 'a', 'b', 'l', 'e'})),
			boolDispatch(t, gov8.NewInspectorStringView8([]byte("Unknown.evaluate"))),
			boolDispatch(t, gov8.NewInspectorStringView16([]uint16{'R', 'u', 'n', 't', 'i', 'm', 'e', '.', 'n', 'o', 't', 'A', 'R', 'e', 'a', 'l', 'M', 'e', 't', 'h', 'o', 'd'})),
			boolDispatch(t, gov8.NewInspectorStringView8([]byte("Runtime.\x00suffix"))),
			boolDispatch(t, gov8.EmptyInspectorStringView()),
			boolDispatch(t, gov8.NewInspectorStringView16([]uint16{})),
			boolDispatch(t, gov8.EmptyInspectorStringView()),
		},
	}}

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
	cl := &client{}
	inspector, err := gov8.NewInspectorWithClient(iso, cl)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(), gov8.NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
		t.Fatal(err)
	}
	ch := &channel{responses: make(map[int32][]byte)}
	session, err := inspector.Connect(1, ch, gov8.NewInspectorStringView8([]byte(`{}`)), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}

	keep := "keep"
	kept := evaluateObject(t, session, ch, 1, &keep)
	if err := session.ReleaseObjectGroup(gov8.NewInspectorStringView8([]byte("unknown"))); err != nil {
		t.Fatal(err)
	}
	afterUnknown := properties(t, session, ch, 2, kept)
	if err := session.ReleaseObjectGroup(gov8.NewInspectorStringView8([]byte("keep"))); err != nil {
		t.Fatal(err)
	}
	afterU8 := properties(t, session, ch, 3, kept)
	wide := "wide-Ω"
	wideID := evaluateObject(t, session, ch, 4, &wide)
	if err := session.ReleaseObjectGroup(gov8.NewInspectorStringView16([]uint16{'w', 'i', 'd', 'e', '-', 0x03a9})); err != nil {
		t.Fatal(err)
	}
	afterU16 := properties(t, session, ch, 5, wideID)
	nul := "nul\x00group"
	nulID := evaluateObject(t, session, ch, 6, &nul)
	if err := session.ReleaseObjectGroup(gov8.NewInspectorStringView8([]byte(nul))); err != nil {
		t.Fatal(err)
	}
	afterNUL := properties(t, session, ch, 7, nulID)
	ungrouped := evaluateObject(t, session, ch, 8, nil)
	if err := session.ReleaseObjectGroup(gov8.EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	afterEmpty := properties(t, session, ch, 9, ungrouped)
	results = append(results, outcome{Check: "inspector-session-controls/release_object_group", OK: true, Value: struct {
		Unknown propertyResult `json:"unknown_preserves_object"`
		U8      propertyResult `json:"exact_u8_releases"`
		U16     propertyResult `json:"exact_u16_releases"`
		NUL     propertyResult `json:"embedded_nul_releases"`
		Empty   propertyResult `json:"empty_group_result"`
	}{afterUnknown, afterU8, afterU16, afterNUL, afterEmpty}})

	enable := dispatch(t, session, ch, 10, `{"id":10,"method":"Debugger.enable"}`)
	beforeGroups, beforeNotifications := len(cl.groups), len(ch.notifications)
	if err := session.SchedulePauseOnNextStatement(gov8.NewInspectorStringView8([]byte("cancelled")), gov8.NewInspectorStringView8([]byte(`{"tag":"cancelled"}`))); err != nil {
		t.Fatal(err)
	}
	if err := session.CancelPauseOnNextStatement(); err != nil {
		t.Fatal(err)
	}
	cancelValue := runScript(t, ctx, scope, "21*2")
	results = append(results, outcome{Check: "inspector-session-controls/schedule_then_cancel", OK: true, Value: struct {
		Enabled       bool  `json:"debugger_enable_response"`
		Script        int64 `json:"script_value"`
		Callbacks     int   `json:"new_pause_callbacks"`
		Notifications int   `json:"new_paused_notifications"`
	}{!bytes.Contains(enable, []byte(`"error"`)), cancelValue, len(cl.groups) - beforeGroups, countMethod(ch.notifications[beforeNotifications:], "Debugger.paused")}})

	type pauseCase struct {
		Case      string `json:"case"`
		Script    int64  `json:"script_value"`
		Callbacks int    `json:"pause_callbacks"`
		Paused    int    `json:"paused_notifications"`
		Resumed   int    `json:"resumed_notifications"`
		Reason    string `json:"reason"`
		Tag       any    `json:"detail_tag"`
	}
	cases := []struct {
		name           string
		reason, detail gov8.InspectorStringView
	}{
		{"valid_detail", gov8.NewInspectorStringView8([]byte("scheduled")), gov8.NewInspectorStringView8([]byte(`{"tag":"ok"}`))},
		{"nul_reason_empty_detail", gov8.NewInspectorStringView8([]byte("r\x00x")), gov8.EmptyInspectorStringView()},
		{"empty_reason_nul_detail", gov8.NewInspectorStringView16([]uint16{}), gov8.NewInspectorStringView8([]byte{'{', '"', 't', 'a', 'g', '"', ':', '"', 'd', 0, 'z', '"', '}'})},
	}
	pauseCases := make([]pauseCase, 0, len(cases))
	for index, item := range cases {
		nstart, cstart := len(ch.notifications), len(cl.groups)
		if err := session.SchedulePauseOnNextStatement(item.reason, item.detail); err != nil {
			t.Fatal(err)
		}
		value := runScript(t, ctx, scope, fmt.Sprint(index+1))
		messages := ch.notifications[nstart:]
		reason, tag := pausedFields(messages)
		pauseCases = append(pauseCases, pauseCase{item.name, value, len(cl.groups) - cstart, countMethod(messages, "Debugger.paused"), countMethod(messages, "Debugger.resumed"), reason, tag})
	}
	results = append(results, outcome{Check: "inspector-session-controls/scheduled_pause_notifications", OK: true, Value: struct {
		Cases   []pauseCase `json:"cases"`
		Groups  []int32     `json:"all_context_groups"`
		Quits   int         `json:"quit_calls"`
		Flushes int         `json:"flushes"`
	}{pauseCases, append([]int32(nil), cl.groups...), cl.quits, ch.flushes}})

	callbackStart := len(cl.groups)
	if err := session.SchedulePauseOnNextStatement(gov8.NewInspectorStringView8([]byte("dropped-session")), gov8.EmptyInspectorStringView()); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	valueAfterDrop := runScript(t, ctx, scope, "40+2")
	results = append(results, outcome{Check: "inspector-session-controls/drop_session_cancels_scheduled_pause", OK: true, Value: struct {
		Script    int64 `json:"script_value"`
		Callbacks int   `json:"new_pause_callbacks"`
	}{valueAfterDrop, len(cl.groups) - callbackStart}})
	if err := inspector.ContextDestroyed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}

	var actual bytes.Buffer
	encoder := json.NewEncoder(&actual)
	encoder.SetEscapeHTML(false)
	for _, result := range results {
		if err := encoder.Encode(result); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Encode(struct {
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}{Summary: struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	}{5, 5, 0}}); err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-inspector-session-controls-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual.Bytes(), want) {
		t.Fatalf("fixture mismatch\nactual:\n%s\nwant:\n%s", actual.Bytes(), want)
	}
}
