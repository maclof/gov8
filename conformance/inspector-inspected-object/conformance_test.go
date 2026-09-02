//go:build windows && amd64

package inspectorinspectedobjectconformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	gov8 "github.com/maclof/gov8"
)

type fixtureLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

func TestMain(m *testing.M) {
	if err := gov8.Initialize(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "..", "rust-oracle", "tests", "fixtures",
		"conformance-inspector-inspected-object-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	expected := []string{
		"inspector-inspected-object/missing_invalid_index",
		"inspector-inspected-object/unadded_lifetime",
		"inspector-inspected-object/live_identity_mutation",
		"inspector-inspected-object/replacement_and_eviction",
		"inspector-inspected-object/session_owns_retained_values",
	}
	result := make(map[string]fixtureLine)
	index := 0
	seenSummary := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line struct {
			fixtureLine
			Summary *struct {
				Total, Passed, Failed int
			} `json:"summary"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Check != "" {
			if seenSummary || index >= len(expected) || line.Check != expected[index] || !line.OK {
				t.Fatalf("fixture check %d = %+v", index, line.fixtureLine)
			}
			if _, duplicate := result[line.Check]; duplicate {
				t.Fatalf("duplicate fixture check %q", line.Check)
			}
			result[line.Check] = line.fixtureLine
			index++
			continue
		}
		if line.Summary != nil {
			if seenSummary || line.Summary.Total != len(expected) || line.Summary.Passed != len(expected) || line.Summary.Failed != 0 {
				t.Fatalf("fixture summary = %+v", line.Summary)
			}
			seenSummary = true
			continue
		}
		t.Fatalf("unknown fixture record: %s", scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(expected) || len(result) != len(expected) || !seenSummary {
		t.Fatalf("fixture completeness = %d/%d summary=%v", index, len(expected), seenSummary)
	}
	return result
}

func compare(t *testing.T, fixtures map[string]fixtureLine, check string, got any) {
	t.Helper()
	want, ok := fixtures[check]
	if !ok {
		t.Fatalf("missing fixture %q", check)
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
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", check, gotJSON, wantJSON)
	}
}

type channel struct {
	mu        sync.Mutex
	responses map[int32]string
}

func (c *channel) SendResponse(id int32, message *gov8.InspectorStringBuffer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.responses == nil {
		c.responses = make(map[int32]string)
	}
	c.responses[id] = message.StringView().String()
}
func (*channel) SendNotification(*gov8.InspectorStringBuffer) {}
func (*channel) FlushProtocolNotifications()                  {}

type runtimeState struct {
	iso       *gov8.Isolate
	ctx       *gov8.Context
	scope     *gov8.Scope
	inspector *gov8.Inspector
	session   *gov8.InspectorSession
	channel   *channel
	globals   []*gov8.Global
	nextID    int32
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
	inspector, err := gov8.NewInspector(iso)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspector.ContextCreated(ctx, 1, gov8.EmptyInspectorStringView(),
		gov8.NewInspectorStringView8([]byte(`{"isDefault":true}`))); err != nil {
		t.Fatal(err)
	}
	ch := &channel{}
	session, err := inspector.Connect(1, ch, gov8.NewInspectorStringView8([]byte(`{}`)), gov8.InspectorFullyTrusted)
	if err != nil {
		t.Fatal(err)
	}
	r := &runtimeState{iso: iso, ctx: ctx, scope: scope, inspector: inspector, session: session, channel: ch}
	t.Cleanup(func() { r.close(t) })
	return r
}

func (r *runtimeState) close(t *testing.T) {
	t.Helper()
	if r.session != nil {
		if err := r.session.Close(); err != nil {
			t.Error(err)
		}
		r.session = nil
	}
	for _, global := range r.globals {
		if err := global.Close(); err != nil {
			t.Error(err)
		}
	}
	r.globals = nil
	if r.inspector != nil && r.ctx != nil {
		if err := r.inspector.ContextDestroyed(r.ctx); err != nil {
			t.Error(err)
		}
	}
	if r.scope != nil {
		if err := r.scope.Close(); err != nil {
			t.Error(err)
		}
		r.scope = nil
	}
	if r.ctx != nil {
		if err := r.ctx.Close(); err != nil {
			t.Error(err)
		}
		r.ctx = nil
	}
	if r.inspector != nil {
		if err := r.inspector.Close(); err != nil {
			t.Error(err)
		}
		r.inspector = nil
	}
	if r.iso != nil {
		if err := gov8.ReleaseIsolateHostState(r.iso); err != nil {
			t.Error(err)
		}
		if err := r.iso.Close(); err != nil {
			t.Error(err)
		}
		r.iso = nil
	}
}

func (r *runtimeState) evaluateString(t *testing.T, expression string) string {
	t.Helper()
	r.nextID++
	request, err := json.Marshal(map[string]any{
		"id": r.nextID, "method": "Runtime.evaluate",
		"params": map[string]any{"expression": expression, "contextId": 1,
			"includeCommandLineAPI": true, "returnByValue": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.session.DispatchProtocolMessage(gov8.NewInspectorStringView8(request)); err != nil {
		t.Fatal(err)
	}
	r.channel.mu.Lock()
	response, ok := r.channel.responses[r.nextID]
	r.channel.mu.Unlock()
	if !ok {
		t.Fatalf("no response for request %d", r.nextID)
	}
	var decoded struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			Result struct {
				Value any `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Error) != 0 {
		t.Fatalf("protocol error: %s", decoded.Error)
	}
	value, ok := decoded.Result.Result.Value.(string)
	if !ok {
		t.Fatalf("response has no string value: %s", response)
	}
	return value
}

type probe struct {
	id             int32
	gets           int
	contextMatches bool
}

func (r *runtimeState) newProbe(t *testing.T, id, marker int32, drops *[]int32) (*gov8.InspectorInspectable, *probe) {
	t.Helper()
	object, err := r.scope.NewObject(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	idValue, err := r.scope.Int32(id)
	if err != nil {
		t.Fatal(err)
	}
	markerValue, err := r.scope.Int32(marker)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := object.SetByName(r.scope, r.ctx, "id", idValue); err != nil || !ok {
		t.Fatalf("set id = %v, %v", ok, err)
	}
	if ok, err := object.SetByName(r.scope, r.ctx, "marker", markerValue); err != nil || !ok {
		t.Fatalf("set marker = %v, %v", ok, err)
	}
	global, err := gov8.NewGlobal(r.scope, object.Value)
	if err != nil {
		t.Fatal(err)
	}
	r.globals = append(r.globals, global)
	p := &probe{id: id, contextMatches: true}
	inspectable, err := r.iso.NewInspectorInspectable(func(cs *gov8.CallbackScope, context *gov8.Context) (gov8.Value, error) {
		p.gets++
		p.contextMatches = p.contextMatches && context == r.ctx
		return global.ToLocal(cs.Scope())
	}, func() { *drops = append(*drops, id) })
	if err != nil {
		t.Fatal(err)
	}
	return inspectable, p
}

func sorted(values []int32) []int32 {
	result := append(make([]int32, 0, len(values)), values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func probeCalls(probes []*probe) []any {
	result := make([]any, 0, len(probes))
	for _, p := range probes {
		result = append(result, map[string]any{"id": p.id, "calls": p.gets})
	}
	return result
}

func TestInspectorInspectedObjectConformance(t *testing.T) {
	fixtures := fixture(t)
	r := newRuntime(t)
	drops := []int32{}

	compare(t, fixtures, "inspector-inspected-object/missing_invalid_index", map[string]any{
		"missing_dollar_0": r.evaluateString(t, "typeof $0"),
		"invalid_dollar_5": r.evaluateString(t, "(()=>{try{return String($5)}catch(e){return e.name}})()"),
	})

	unaddedDrops := []int32{}
	unadded, unaddedProbe := r.newProbe(t, -1, 0, &unaddedDrops)
	before := sorted(unaddedDrops)
	if err := unadded.Close(); err != nil {
		t.Fatal(err)
	}
	compare(t, fixtures, "inspector-inspected-object/unadded_lifetime", map[string]any{
		"drops_before": before, "drops_after": sorted(unaddedDrops), "get_calls": unaddedProbe.gets,
	})

	identity, identityProbe := r.newProbe(t, 100, 1, &drops)
	if err := r.session.AddInspectedObject(identity); err != nil {
		t.Fatal(err)
	}
	first := r.evaluateString(t, "[$0===$0,++$0.marker,$0.marker].join(',')")
	second := r.evaluateString(t, "String($0.marker)")
	compare(t, fixtures, "inspector-inspected-object/live_identity_mutation", map[string]any{
		"first": first, "second": second, "get_calls": identityProbe.gets,
		"callback_context_matches_current": identityProbe.contextMatches, "drops": sorted(drops),
	})

	probes := make([]*probe, 0, 6)
	for id := int32(1); id <= 2; id++ {
		value, p := r.newProbe(t, id, id*10, &drops)
		if err := r.session.AddInspectedObject(value); err != nil {
			t.Fatal(err)
		}
		probes = append(probes, p)
	}
	shifted := r.evaluateString(t, "[$0.id,$1.id,$2.id].join(',')")
	for id := int32(3); id <= 6; id++ {
		value, p := r.newProbe(t, id, id*10, &drops)
		if err := r.session.AddInspectedObject(value); err != nil {
			t.Fatal(err)
		}
		probes = append(probes, p)
	}
	retained := r.evaluateString(t, "[$0.id,$1.id,$2.id,$3.id,$4.id].join(',')")
	beyond := r.evaluateString(t, "(()=>{try{return String($5)}catch(e){return e.name}})()")
	contextsMatch := true
	for _, p := range probes {
		contextsMatch = contextsMatch && p.contextMatches
	}
	compare(t, fixtures, "inspector-inspected-object/replacement_and_eviction", map[string]any{
		"after_two_adds": shifted, "retained_newest_first": retained, "beyond_buffer": beyond,
		"evicted_drops": sorted(drops), "all_callback_contexts_match": contextsMatch,
		"get_calls_by_id": probeCalls(probes),
	})

	if err := r.session.Close(); err != nil {
		t.Fatal(err)
	}
	r.session = nil
	compare(t, fixtures, "inspector-inspected-object/session_owns_retained_values", map[string]any{
		"all_drops_after_session": sorted(drops), "identity_get_calls": identityProbe.gets,
	})
	if len(fixtures) != 5 {
		t.Fatalf("fixture count = %d", len(fixtures))
	}
}
