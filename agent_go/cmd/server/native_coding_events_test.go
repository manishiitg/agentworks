package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeevents"
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

func TestNativeWorkDoesNotBecomePlatformExecution(t *testing.T) {
	api := &StreamingAPI{nativeObservers: map[string]*nativeTerminalObserver{
		"chat": {snapshot: terminals.Snapshot{SessionID: "chat"}, nativeRecords: &nativeCodingRecordState{Work: map[string]llmtypes.NativeRecord{
			"helper":   {Kind: "helper", State: "started"},
			"command":  {Kind: "background_command", State: "started"},
			"proposal": {Kind: "helper", State: "proposed"},
		}}},
	}}
	items := api.sessionBackgroundWork("chat")
	if len(items) != 2 {
		t.Fatalf("running native work=%d, expected helper and command only", len(items))
	}
	kinds := map[string]bool{}
	for _, item := range items {
		kinds[item.Kind] = true
		if item.CanStop || api.stopSessionBackgroundWorkItem("chat", item.ID) {
			t.Fatal("native observation gained platform stop authority")
		}
	}
	if !kinds["native_helper"] || !kinds["native_command"] || api.bgAgentRegistry != nil {
		t.Fatal("native work was conflated with platform execution")
	}
}

// PLAT-872: after app restarts relaunched Claude Code with --resume, the chat
// kept listing "Background command" items started hours earlier by the killed
// process, because no completion is ever written for them.
func TestNativeBackgroundWorkEndsWithReplacedCLIProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "native.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	state := &nativeCodingRecordState{SessionID: "chat", Provider: "claude-code", NativeID: "native", Path: path, TmuxSession: "claude-old", Primed: true,
		Work:     map[string]llmtypes.NativeRecord{"bash1": {Kind: "background_command", State: "started", TaskID: "bash1"}},
		Turns:    map[string]llmtypes.NativeRecord{},
		filename: filepath.Join(dir, "native-records", "chat.json")}
	o := &nativeTerminalObserver{snapshot: terminals.Snapshot{SessionID: "chat", TmuxSession: "claude-old"}, provider: "claude-code", nativeID: "native", nativeRecords: state}
	api := &StreamingAPI{nativeObservers: map[string]*nativeTerminalObserver{"chat": o}}
	o.api = api
	o.pollNativeCodingRecords()
	if len(api.nativeCodingBackgroundWork("chat")) != 1 {
		t.Fatal("background command of the live CLI process was closed")
	}
	// The same native session resumed by a new CLI process in a new tmux session.
	o.snapshot.TmuxSession = "claude-new"
	o.pollNativeCodingRecords()
	if items := api.nativeCodingBackgroundWork("chat"); len(items) != 0 {
		t.Fatalf("replaced CLI process still lists %d running items", len(items))
	}
	var saved nativeCodingRecordState
	raw, _ := os.ReadFile(state.filename)
	if json.Unmarshal(raw, &saved) != nil || saved.Work["bash1"].State != "stopped" || saved.TmuxSession != "claude-new" {
		t.Fatal("closed background command was not checkpointed for the next restart")
	}
}

func TestNativeStartupObservesWithoutNewEvents(t *testing.T) {
	const sessionID = "native-startup-chat"
	const userID = "alice"
	const project = "Workflow/project"
	const conversation = "Workflow/project/builder/conversation/2026-10-25/session-native-startup-chat-conversation.json"
	raw, _ := json.Marshal(map[string]interface{}{"session_id": sessionID, "user_id": userID, "agent_mode": "workflow_phase", "conversation_history": []interface{}{}})
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	dir := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", dir)
	if err := nativereplies.Configure(filepath.Join(dir, "claims")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nativereplies.Configure("") })
	path := filepath.Join(dir, "owned-native.jsonl")
	rows := `{"type":"user","uuid":"u","origin":{"kind":"human"}}
{"type":"assistant","uuid":"a","parentUuid":"u","message":{"content":[{"type":"tool_use","id":"tool1","name":"Agent"}]}}
{"type":"user","uuid":"launch","parentUuid":"a","toolUseResult":{"agentId":"task1"},"message":{"content":[{"type":"tool_result","tool_use_id":"tool1"}]}}
{"type":"user","uuid":"notice","parentUuid":"launch","turnOrigin":"task_notification","origin":{"kind":"task-notification"},"message":{"content":"<task-notification><task-id>task1</task-id><status>completed</status></task-notification>"}}
{"type":"assistant","uuid":"answer","parentUuid":"notice","message":{"content":[{"type":"text","text":"Restart follow-up"}]}}
`
	if err := os.WriteFile(path, []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	state := &nativeCodingRecordState{SessionID: sessionID, OwnerID: userID, Workspace: project, Provider: "claude-code", NativeID: "native", Path: path, Primed: true, Cursor: nativeevents.Cursor{}, filename: filepath.Join(dir, "native-records", "owned.json")}
	if err := saveNativeCodingState(state); err != nil {
		t.Fatal(err)
	}
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(dir, "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner(sessionID, userID)
	if err := store.SetSessionPersistenceClass(sessionID, storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: store, sessionWorkspaceFolders: map[string]string{}}
	api.restoreNativeCodingObservers()
	defer api.stopNativeCodingObservers()
	api.nativeObserversMu.Lock()
	observer := api.nativeObservers[sessionID]
	api.nativeObserversMu.Unlock()
	if observer == nil {
		t.Fatal("startup waited for a browser or streaming event")
	}
	select {
	case <-observer.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("startup observer did not read owned source")
	}
	page, err := journal.ReadPage(sessionID, storeevents.DurableEventPageOptions{Limit: 30, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	answers := 0
	for _, event := range page.Events {
		if storeevents.IsTranscriptMessage(event) {
			answers++
		}
		if event.Type == "user_message" {
			t.Fatal("helper notification became human input")
		}
	}
	if answers != 1 {
		t.Fatalf("startup delivered %d replies", answers)
	}
}
