package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

func openArtifactTestJournal(t *testing.T) *SQLiteEventJournal {
	t.Helper()
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close() })
	return journal
}

func largeToolResult(id string, size int) Event {
	return Event{ID: id, Type: "tool_call_end", Data: &pkgevents.AgentEvent{
		Type: pkgevents.EventType("tool_call_end"),
		Data: NewGenericEventData("tool_call_end", map[string]interface{}{
			"tool_name": "exec", "tool_call_id": "call-" + id, "result": strings.Repeat("r", size),
		}),
	}}
}

func payloadField(t *testing.T, event Event, key string) interface{} {
	t.Helper()
	return eventPayloadMap(&event)[key]
}

func setRowAge(t *testing.T, journal *SQLiteEventJournal, sessionID, eventID string, age time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-age).UTC().Format(time.RFC3339Nano)
	if _, err := journal.db.Exec(`UPDATE structured_chat_events SET created_at = ? WHERE session_id = ? AND event_id = ?`, stamp, sessionID, eventID); err != nil {
		t.Fatal(err)
	}
}

func TestOversizedTranscriptMessageMovesToArtifact(t *testing.T) {
	journal := openArtifactTestJournal(t)
	full := strings.Repeat("answer ", 30*1024)
	persisted, inserted, err := journal.Append("chat-1", transcriptMessage("big-answer", full))
	if err != nil || !inserted {
		t.Fatalf("append: inserted=%v err=%v", inserted, err)
	}
	encoded, _ := json.Marshal(persisted)
	if len(encoded) > maxDurableChatEventBytes {
		t.Fatalf("row is %d bytes, limit %d", len(encoded), maxDurableChatEventBytes)
	}
	artifactID, _ := payloadField(t, persisted, "artifact_id").(string)
	if payloadField(t, persisted, "truncated") != true || artifactID != ChatArtifactID("big-answer") {
		t.Fatalf("summary row missing artifact reference: %+v", eventPayloadMap(&persisted))
	}
	if content, _ := payloadField(t, persisted, "content").(string); content == "" || len(content) > liveArtifactSummaryBytes+8 {
		t.Fatalf("summary content length %d", len(content))
	}
	raw, err := journal.ReadChatArtifact("chat-1", artifactID)
	if err != nil {
		t.Fatal(err)
	}
	var original Event
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if content, _ := payloadField(t, original, "content").(string); content != full {
		t.Fatalf("artifact lost content: %d bytes, want %d", len(content), len(full))
	}
	// The page still renders the summary as the same event identity.
	page, err := journal.ReadPage("chat-1", DurableEventPageOptions{Limit: 10})
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != "big-answer" {
		t.Fatalf("page = %+v err=%v", page.Events, err)
	}
	// Small rows are untouched.
	small, _, err := journal.Append("chat-1", transcriptMessage("small", "short"))
	if err != nil || payloadField(t, small, "artifact_id") != nil {
		t.Fatalf("small row was summarized: %+v err=%v", eventPayloadMap(&small), err)
	}
}

func TestDeliveredAgentArtifactKeepsIdentityAndFullBodyAfterRestart(t *testing.T) {
	journal := openArtifactTestJournal(t)
	body := strings.Repeat("Evidence recorded. ", 30*1024)
	metadata := map[string]interface{}{
		"source": "agent_message", "sender_label": "Reports Builder chat",
		"message_id": "msg-test", "agent_message_inbox": "inbox-test",
		"agent_message_instructions": "Replies are optional.", "display_content": body,
	}
	event := Event{ID: "delivered-large", Type: "user_message", Data: &pkgevents.AgentEvent{
		Type: pkgevents.UserMessage, Data: NewGenericEventData("user_message", map[string]interface{}{
			"content": "Delivery instructions\n\n" + body, "metadata": metadata,
		}),
	}}
	if _, _, err := journal.Append("chat-1", event); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	restored, err := OpenSQLiteEventJournal(journal.path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	page, err := restored.ReadPage("chat-1", DurableEventPageOptions{Limit: 10})
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("restored page: %+v, %v", page, err)
	}
	summary := eventPayloadMap(&page.Events[0])
	stamp, ok := summary["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing summary attribution: %#v", summary)
	}
	for _, key := range []string{"source", "sender_label", "message_id", "agent_message_inbox", "agent_message_instructions"} {
		if stamp[key] != metadata[key] {
			t.Fatalf("lost %s: %#v", key, stamp)
		}
	}
	encoded, _ := json.Marshal(page.Events[0])
	if len(encoded) > maxDurableChatEventBytes || len(stamp["display_content"].(string)) > 2*1024+8 {
		t.Fatal("history summary exceeded its bound")
	}
	raw, err := restored.ReadChatArtifact("chat-1", summary["artifact_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var original Event
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	full := eventPayloadMap(&original)["metadata"].(map[string]interface{})
	if full["display_content"] != body {
		t.Fatal("artifact lost full delivered-message body")
	}
}

func TestChatArtifactIDsCannotEscapeTheSessionDirectory(t *testing.T) {
	journal := openArtifactTestJournal(t)
	if _, _, err := journal.Append("chat-1", largeToolResult("tool-1", 80*1024)); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(filepath.Dir(journal.path), "secret.json")
	if err := os.WriteFile(secret, []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../secret", "../../events.sqlite", "", ChatArtifactID("tool-1") + "/..", strings.ToUpper(ChatArtifactID("tool-1"))} {
		if _, err := journal.ReadChatArtifact("chat-1", id); err != ErrChatArtifactNotFound {
			t.Fatalf("artifact id %q: err=%v, want not found", id, err)
		}
	}
	if _, err := journal.ReadChatArtifact("other-chat", ChatArtifactID("tool-1")); err != ErrChatArtifactNotFound {
		t.Fatalf("artifact readable from another session: %v", err)
	}
}

func TestDeletingAChatRemovesItsArtifacts(t *testing.T) {
	journal := openArtifactTestJournal(t)
	if _, _, err := journal.Append("chat-1", largeToolResult("tool-1", 80*1024)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.Append("chat-2", largeToolResult("tool-2", 80*1024)); err != nil {
		t.Fatal(err)
	}
	if err := journal.DeleteSession("chat-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal.sessionArtifactDir("chat-1")); !os.IsNotExist(err) {
		t.Fatalf("chat-1 artifacts survived delete: %v", err)
	}
	if _, err := journal.ReadChatArtifact("chat-2", ChatArtifactID("tool-2")); err != nil {
		t.Fatalf("other chat's artifact was removed: %v", err)
	}
}

func TestCompactionSummarizesOnlyOldLargeNonUserRowsAndIsIdempotent(t *testing.T) {
	journal := openArtifactTestJournal(t)
	userText := strings.Repeat("u", 20*1024)
	user := Event{ID: "user-1", Type: "user_message", Data: &pkgevents.AgentEvent{
		Type: pkgevents.EventType("user_message"),
		Data: NewGenericEventData("user_message", map[string]interface{}{"content": userText}),
	}}
	for _, event := range []Event{user, largeToolResult("old-big", 20*1024), largeToolResult("old-small", 1024), largeToolResult("new-big", 20*1024)} {
		if _, _, err := journal.Append("chat-1", event); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"user-1", "old-big", "old-small"} {
		setRowAge(t, journal, "chat-1", id, 40*24*time.Hour)
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	count, err := journal.CompactSession("chat-1", cutoff, 8*1024)
	if err != nil || count != 1 {
		t.Fatalf("compacted %d (err %v), want only old-big", count, err)
	}
	page, err := journal.ReadPage("chat-1", DurableEventPageOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Event{}
	for _, event := range page.Events {
		byID[event.ID] = event
	}
	if payloadField(t, byID["old-big"], "artifact_id") == nil {
		t.Fatal("old large row was not compacted")
	}
	if text, _ := payloadField(t, byID["user-1"], "content").(string); text != userText {
		t.Fatal("user message text was touched")
	}
	for _, id := range []string{"old-small", "new-big"} {
		if payloadField(t, byID[id], "artifact_id") != nil {
			t.Fatalf("%s should not be compacted", id)
		}
	}
	if byID["old-big"].Sequence != 2 {
		t.Fatalf("compaction changed sequence: %d", byID["old-big"].Sequence)
	}
	raw, err := journal.ReadChatArtifact("chat-1", ChatArtifactID("old-big"))
	if err != nil || !strings.Contains(string(raw), strings.Repeat("r", 20*1024)) {
		t.Fatalf("artifact missing full result: err=%v", err)
	}
	again, err := journal.CompactSession("chat-1", cutoff, 8*1024)
	if err != nil || again != 0 {
		t.Fatalf("second pass compacted %d (err %v), want 0", again, err)
	}
}

func TestSizeGuardCompactsOldestFirstUntilUnderCap(t *testing.T) {
	journal := openArtifactTestJournal(t)
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	for i, age := range []time.Duration{20 * 24 * time.Hour, 10 * 24 * time.Hour, 2 * 24 * time.Hour} {
		session := fmt.Sprintf("chat-%d", i)
		id := fmt.Sprintf("big-%d", i)
		if _, _, err := journal.Append(session, largeToolResult(id, 40*1024)); err != nil {
			t.Fatal(err)
		}
		setRowAge(t, journal, session, id, age)
	}
	candidates, err := journal.CompactionCandidates(time.Now(), 8*1024)
	if err != nil || strings.Join(candidates, ",") != "chat-0,chat-1,chat-2" {
		t.Fatalf("candidates = %v (err %v), want oldest first", candidates, err)
	}
	// Nothing is 30 days old, so only the cap can trigger compaction; a tiny
	// cap forces the size guard down its age ladder.
	cfg := JournalMaintenanceConfig{CompactAfter: 30 * 24 * time.Hour, MinBytes: 8 * 1024, MaxBytes: 1}
	report, err := store.RunDurableJournalMaintenance(cfg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !report.OverCap || report.Compacted != 3 {
		t.Fatalf("report = %+v, want over-cap compaction of all 3", report)
	}
	// A generous cap leaves young rows alone.
	fresh := openArtifactTestJournal(t)
	store2 := NewEventStore(100)
	defer store2.Stop()
	store2.SetDurableJournal(fresh)
	if _, _, err := fresh.Append("chat-x", largeToolResult("young", 40*1024)); err != nil {
		t.Fatal(err)
	}
	report, err = store2.RunDurableJournalMaintenance(JournalMaintenanceConfig{CompactAfter: 30 * 24 * time.Hour, MinBytes: 8 * 1024, MaxBytes: 1 << 40}, time.Now())
	if err != nil || report.OverCap || report.Compacted != 0 {
		t.Fatalf("young row compacted under cap: %+v err=%v", report, err)
	}
	if report.NeedsFullVacuum {
		t.Fatal("new journals use incremental auto-vacuum and should not need a full VACUUM")
	}
}

func TestMaintenanceIsSafeWithConcurrentAppends(t *testing.T) {
	journal := openArtifactTestJournal(t)
	store := NewEventStore(1000)
	defer store.Stop()
	store.SetDurableJournal(journal)
	for i := 0; i < 3; i++ {
		classifyInteractiveTestSession(t, store, fmt.Sprintf("chat-%d", i))
	}
	for i := 0; i < 3; i++ {
		store.AddEvent(fmt.Sprintf("chat-%d", i), largeToolResult(fmt.Sprintf("seed-%d", i), 20*1024))
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				store.AddEvent(fmt.Sprintf("chat-%d", i), largeToolResult(fmt.Sprintf("e-%d-%d", i, n), 10*1024))
			}
		}(i)
	}
	cfg := JournalMaintenanceConfig{CompactAfter: 0, MinBytes: 8 * 1024, MaxBytes: 1 << 40}
	for n := 0; n < 5; n++ {
		if _, err := store.RunDurableJournalMaintenance(cfg, time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	for i := 0; i < 3; i++ {
		page, err := journal.ReadPage(fmt.Sprintf("chat-%d", i), DurableEventPageOptions{Limit: 100})
		if err != nil || len(page.Events) != 21 {
			t.Fatalf("chat-%d has %d rows (err %v), want 21", i, len(page.Events), err)
		}
		for j := 1; j < len(page.Events); j++ {
			if page.Events[j].Sequence <= page.Events[j-1].Sequence {
				t.Fatalf("chat-%d sequence regressed at %d", i, j)
			}
		}
	}
}
