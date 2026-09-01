//go:build windows && amd64

package gov8

import "testing"

type runOnlyInspectorClient struct{ calls []int32 }

func (c *runOnlyInspectorClient) RunMessageLoopOnPause(group int32) {
	c.calls = append(c.calls, group)
}

type quitOnlyInspectorClient struct{ calls int }

func (c *quitOnlyInspectorClient) QuitMessageLoopOnPause() { c.calls++ }

func TestInspectorPauseCapabilitiesIndependent(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	run := &runOnlyInspectorClient{}
	quit := &quitOnlyInspectorClient{}
	inspectorClients.Lock()
	inspectorClients.next += 2
	runID, quitID := inspectorClients.next-1, inspectorClients.next
	inspectorClients.byID[runID] = &inspectorClientEntry{iso: iso, client: run}
	inspectorClients.byID[quitID] = &inspectorClientEntry{iso: iso, client: quit}
	inspectorClients.Unlock()
	defer func() {
		inspectorClients.Lock()
		delete(inspectorClients.byID, runID)
		delete(inspectorClients.byID, quitID)
		inspectorClients.Unlock()
		_ = ReleaseIsolateHostState(iso)
		_ = iso.Close()
	}()

	if got := inspectorClientDispatchCallback(uintptr(runID), 0, 7); got != 0 {
		t.Fatalf("run dispatch = %d", got)
	}
	if got := inspectorClientDispatchCallback(uintptr(runID), 1, 0); got != 0 {
		t.Fatalf("absent quit dispatch = %d", got)
	}
	if got := inspectorClientDispatchCallback(uintptr(quitID), 0, 9); got != 0 {
		t.Fatalf("absent run dispatch = %d", got)
	}
	if got := inspectorClientDispatchCallback(uintptr(quitID), 1, 0); got != 0 {
		t.Fatalf("quit dispatch = %d", got)
	}
	if len(run.calls) != 1 || run.calls[0] != 7 || quit.calls != 1 {
		t.Fatalf("run=%v quit=%d", run.calls, quit.calls)
	}
}

type creationIDClient struct{ calls int }

func (c *creationIDClient) GenerateUniqueID() int64 { c.calls++; return 41 }

func TestInspectorExtendedClientRegistryDrainsOnce(t *testing.T) {
	iso, err := NewIsolate()
	if err != nil {
		t.Fatal(err)
	}
	client := &creationIDClient{}
	inspector, err := NewInspectorWithClient(iso, client)
	if err != nil {
		t.Fatal(err)
	}
	inspectorClients.Lock()
	id := inspectorClients.byInspector[inspector]
	entry := inspectorClients.byID[id]
	inspectorClients.Unlock()
	if client.calls != 1 || id == 0 || entry == nil || entry.inspector != inspector {
		t.Fatalf("creation publication: calls=%d id=%d entry=%p", client.calls, id, entry)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	inspectorClients.Lock()
	_, byID := inspectorClients.byID[id]
	_, byInspector := inspectorClients.byInspector[inspector]
	inspectorClients.Unlock()
	if byID || byInspector {
		t.Fatalf("client registry retained after Close: byID=%v byInspector=%v", byID, byInspector)
	}
	if err := inspector.Close(); err == nil {
		t.Fatal("second Inspector.Close succeeded")
	}
	inspectorClients.Lock()
	_, byID = inspectorClients.byID[id]
	inspectorClients.Unlock()
	if byID {
		t.Fatal("idempotent close restored registry entry")
	}
	if err := ReleaseIsolateHostState(iso); err != nil {
		t.Fatal(err)
	}
	if err := iso.Close(); err != nil {
		t.Fatal(err)
	}
}
