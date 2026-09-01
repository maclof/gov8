//go:build windows && amd64

package gov8

import "testing"

func TestInspectorClientRegistryDropIsIdempotentAndIdentityKeyed(t *testing.T) {
	iso := &Isolate{}
	// Equal native handles model allocator address reuse. Registry ownership is
	// the Go Inspector identity, so draining the old wrapper cannot remove the
	// later wrapper's callback.
	first := &Inspector{iso: iso, handle: 0x1234}
	second := &Inspector{iso: iso, handle: 0x1234}
	firstEntry := &inspectorClientEntry{iso: iso, inspector: first}
	secondEntry := &inspectorClientEntry{iso: iso, inspector: second}
	inspectorClients.Lock()
	inspectorClients.next += 2
	firstID := inspectorClients.next - 1
	secondID := inspectorClients.next
	inspectorClients.byID[firstID] = firstEntry
	inspectorClients.byID[secondID] = secondEntry
	inspectorClients.byInspector[first] = firstID
	inspectorClients.byInspector[second] = secondID
	inspectorClients.Unlock()

	dropInspectorClient(first)
	dropInspectorClient(first)
	inspectorClients.Lock()
	_, firstPresent := inspectorClients.byID[firstID]
	gotSecond := inspectorClients.byID[secondID]
	delete(inspectorClients.byID, secondID)
	delete(inspectorClients.byInspector, second)
	inspectorClients.Unlock()
	if firstPresent || gotSecond != secondEntry {
		t.Fatalf("registry cleanup first=%v second=%p, want false %p", firstPresent, gotSecond, secondEntry)
	}
}
