package agentworksclient

import (
	"path/filepath"
	"testing"
)

// A known failed older notification may retry after newer offers and restart;
// ambiguous successful offers and retired IDs must still never replay.
func TestDeliveryJournalOlderFailedOfferRetriesAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	j := &deliveryJournal{path: path, Messages: map[string]AgentLiveMessage{}}
	for _, seq := range []int{5, 6} {
		id := "older"
		if seq == 6 {
			id = "newer"
		}
		ok, err := j.offer(AgentLiveMessage{MessageID: id, Sequence: seq, Message: "clear this body"})
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if err := j.forget("older"); err != nil {
		t.Fatal(err)
	}
	restored := &deliveryJournal{path: path}
	if err := readLiveJSON(path, restored); err != nil {
		t.Fatal(err)
	}
	if restored.SeenThrough != 6 {
		t.Fatal("watermark moved backwards")
	}
	ok, err := restored.offer(AgentLiveMessage{MessageID: "older", Sequence: 5})
	if err != nil || !ok || restored.SeenThrough != 6 {
		t.Fatal("lost known failed retry", ok, err)
	}
	for _, id := range []string{"older", "newer"} {
		if ok, err := restored.offer(AgentLiveMessage{MessageID: id, Sequence: 5}); err != nil || ok {
			t.Fatal("duplicate automatic offer", id, ok, err)
		}
	}
}
