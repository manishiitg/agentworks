package server

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativereplies"
)

// A real SQLite journal and the normal conversation writer: no terminal view,
// native keyboard input, fabricated prompt or new model invocation is involved.
func TestNativeContinuationDurableReplyOnce(t *testing.T) {
	const sessionID = "native-chat"
	const userID = "alice"
	const project = "Workflow/project"
	const conversation = "Workflow/project/builder/conversation/2026-10-25/session-native-chat-conversation.json"
	raw, _ := json.Marshal(map[string]interface{}{"session_id": sessionID, "user_id": userID, "agent_mode": "workflow_phase", "conversation_history": []interface{}{}})
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	dir := t.TempDir()
	if err := nativereplies.Configure(filepath.Join(dir, "claims")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nativereplies.Configure("") })
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(dir, "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	defer journal.Close()
	store.SetDurableJournal(journal)
	store.SetSessionOwner(sessionID, userID)
	if err := store.SetSessionPersistenceClass(sessionID, storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: store, sessionWorkspaceFolders: map[string]string{sessionID: project}}
	o := &nativeTerminalObserver{api: api, snapshot: terminals.Snapshot{SessionID: sessionID}}
	record := llmtypes.NativeRecord{Provider: "claude-code", SessionID: "native", ID: "assistant-block:0", TurnID: "notification", Kind: "reply", State: "committed", Origin: "helper_finished", Text: "The helper result is ready.", Timestamp: time.Now()}
	for i := 0; i < 2; i++ {
		if err := o.acceptNativeCodingRecord(record); err != nil {
			t.Fatal(err)
		}
	}
	// Re-open the publication claim store as after a server restart. Replay of
	// the exact item uses the same event and conversation checkpoints.
	if err := nativereplies.Configure(filepath.Join(dir, "claims")); err != nil {
		t.Fatal(err)
	}
	if err := o.acceptNativeCodingRecord(record); err != nil {
		t.Fatal(err)
	}
	page, err := journal.ReadPage(sessionID, storeevents.DurableEventPageOptions{Limit: 20, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Events[0].Type != "coding_agent_native_activity" || !storeevents.IsTranscriptMessage(page.Events[1]) {
		t.Fatalf("unexpected persisted event kinds: %d", len(page.Events))
	}
	var saved struct {
		History []builderConversationMessage `json:"conversation_history"`
	}
	if err := json.Unmarshal([]byte(workspace.files[conversation]), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 1 || saved.History[0].Role != "ai" || saved.History[0].Parts[0].Text != record.Text {
		t.Fatal("native reply was lost or repeated in saved history")
	}
	// A foreground-owned ordinary record may be observed but not republished.
	record.ID = "foreground-block:0"
	record.Origin = "human"
	record.Text = "Already owned by SDK"
	if err := o.acceptNativeCodingRecord(record); err != nil {
		t.Fatal(err)
	}
	var after struct {
		History []builderConversationMessage `json:"conversation_history"`
	}
	_ = json.Unmarshal([]byte(workspace.files[conversation]), &after)
	if len(after.History) != 1 {
		t.Fatal("observer republished foreground output")
	}
}
