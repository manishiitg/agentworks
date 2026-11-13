package server

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746: native task-notification follow-ups can be streamed by the SDK
// and then observed from JSONL. Only their exact completed native identity
// suppresses the observer copy, never the text or a generic SDK-active flag.
func TestNativeHelperReplySkipsStreamedBlockButImportsUnstreamed(t *testing.T) {
	const sid, owner, project = "helper-reply-chat", "alice", "Workflow/project"
	const path = project + "/builder/conversation/2026-11-13/session-" + sid + "-conversation.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{path: `{"session_id":"helper-reply-chat","user_id":"alice","conversation_history":[]}`}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil { t.Fatal(err) }
	defer journal.Close()
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner(sid, owner)
	if err := store.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil { t.Fatal(err) }
	api := &StreamingAPI{eventStore: store, sessionWorkspaceFolders: map[string]string{sid: project}}
	observer := &nativeTerminalObserver{api: api, snapshot: terminals.Snapshot{SessionID: sid}}
	addBlock := func(id, phase, terminalOwner string) {
		t.Helper()
		chunk := &agentevents.StreamingChunkEvent{BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{"assistant_block_id": id, "assistant_block_phase": phase}}, Source: "transcript", Content: "Same text"}
		if err := store.AddEventChecked(sid, storeevents.Event{ID: id+phase+terminalOwner, Type: "streaming_chunk", SessionID: sid, TerminalOwnerID: terminalOwner, Data: agentevents.NewAgentEvent(chunk)}); err != nil { t.Fatal(err) }
	}
	addBlock("claude:msg-live:1", "end", "main:"+sid)
	// A partial block and a child owner's block must not suppress main output.
	addBlock("claude:msg-partial:1", "", "main:"+sid)
	addBlock("claude:msg-child:1", "end", "child:"+sid)
	store.RemoveSession(sid) // The durable journal must prove the match after reload too.
	store.SetSessionOwner(sid, owner) // The admitted observer retains its caller-owned session.
	if !api.nativeReplyBlockAlreadyPublished(sid, "claude:msg-live:1") || api.nativeReplyBlockAlreadyPublished("other-chat", "claude:msg-live:1") || api.nativeReplyBlockAlreadyPublished(sid, "") || api.nativeReplyBlockAlreadyPublished(sid, "claude:msg-partial:1") || api.nativeReplyBlockAlreadyPublished(sid, "claude:msg-child:1") {
		t.Fatal("native match ignored the main chat/completed identity boundary")
	}
	for i, block := range []string{"claude:msg-live:1", "claude:msg-unstreamed:1"} {
		record := llmtypes.NativeRecord{ID: block, Provider: "claude-code", SessionID: t.Name(), TurnID: "helper-turn", Kind: "reply", State: "committed", Origin: "helper_finished", Text: "Same text", AssistantBlockID: block, Timestamp: time.Now()}
		if err := observer.acceptNativeCodingRecord(record); err != nil { t.Fatalf("record %d: %v", i, err) }
	}
	page, err := journal.ReadPage(sid, storeevents.DurableEventPageOptions{Limit: 100})
	if err != nil { t.Fatal(err) }
	nativeCopies := 0
	for _, event := range page.Events {
		data := eventPayloadMap(event)
		meta, _ := data["metadata"].(map[string]interface{})
		if event.Type == "streaming_chunk" && meta["native_continuation"] == true { nativeCopies++ }
	}
	if nativeCopies != 1 { t.Fatalf("want only never-streamed reply imported, got %d", nativeCopies) }
	var archive struct { History []builderConversationMessage `json:"conversation_history"` }
	if err := json.Unmarshal([]byte(workspace.files[path]), &archive); err != nil || len(archive.History) != 1 {
		t.Fatal("observer duplicated or lost independent unstreamed conversation text")
	}
}
