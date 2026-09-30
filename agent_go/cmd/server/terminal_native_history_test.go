package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/mux"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

func privateClaudeHistoryFixture(t *testing.T) (workingDir, path string) {
	t.Helper()
	workingDir = t.TempDir()
	privateHome := filepath.Join(workingDir, security.SandboxPersistentDirName, "cli-home", "claude-code")
	dir := filepath.Join(privateHome, ".claude", "projects", claudeNativeTranscriptProjectSlug(workingDir))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return workingDir, filepath.Join(dir, "native-private.jsonl")
}

func writeClaudeChatTurns(t *testing.T, path string, texts ...string) {
	t.Helper()
	var lines []string
	for i, text := range texts {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		row, err := json.Marshal(map[string]interface{}{
			"type": role, "timestamp": time.Now().Add(time.Duration(i) * time.Millisecond).UTC().Format(time.RFC3339Nano),
			"message": map[string]string{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(row))
	}
	writeTranscriptFixture(t, path, lines)
}

func TestNativeTerminalObserverReadsPrivateClaudeHome(t *testing.T) {
	workingDir, path := privateClaudeHistoryFixture(t)
	writeClaudeChatTurns(t, path, "initial", "initial reply")
	o := nativeInputObserverFixture(t)
	o.provider, o.nativeID, o.workingDir, o.home = "claude-code", "native-private", workingDir, t.TempDir()
	o.poll(true)
	if o.path != path {
		t.Fatalf("observer transcript = %q, want private home %q", o.path, path)
	}
	if _, _, _, found, err := nativeTranscriptMessagesForRuntimeUncapped("claude-code", "different-native-id", workingDir, o.home); err != nil || found {
		t.Fatalf("private home substituted a different session: found=%v err=%v", found, err)
	}
	tmuxinput.Default.NoteInteractiveInput(o.snapshot.TmuxSession, []byte("terminal one\rterminal two\r"))
	writeClaudeChatTurns(t, path, "initial", "initial reply", "terminal one", "reply one", "terminal two", "reply two")
	o.poll(false)
	counts := nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID)
	if counts["terminal one"] != 1 || counts["terminal two"] != 1 || counts["initial"] != 0 {
		t.Fatalf("terminal inputs = %v", counts)
	}
	if replies := liveAssistantMessageCounts(o.api.eventStore.GetAllEventsRaw(o.snapshot.SessionID)); replies["reply one"] != 1 || replies["reply two"] != 1 {
		t.Fatalf("terminal replies = %v", replies)
	}
	o.poll(false)
	if got := nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID); !reflect.DeepEqual(got, counts) {
		t.Fatalf("unchanged transcript replayed: %v", got)
	}
}

func TestTerminalToChatPageRecoversPrivateClaudeTurnsOnce(t *testing.T) {
	workingDir, path := privateClaudeHistoryFixture(t)
	writeClaudeChatTurns(t, path, "initial", "initial reply", "repeat", "reply one", "repeat", "reply two")
	const owner, session, workspacePath = "alice", "terminal-chat", "_users/alice/Chats/Work/projects/terminal"
	conversationPath := workspacePath + "/builder/conversation/2026-09-30/session-" + session + "-conversation.json"
	initial := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "initial"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "initial reply"}}},
	}
	raw, err := json.Marshal(map[string]interface{}{
		"session_id": session, "user_id": owner, "agent_mode": "workflow_phase", "conversation_history": initial,
		"runtime": map[string]interface{}{"provider": "claude-code", "external_session_id": "native-private", "agent_session_handle": map[string]interface{}{"provider": map[string]string{"provider": "claude-code", "working_dir": workingDir}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace := &mockWorkspaceAPI{files: map[string]string{conversationPath: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	t.Setenv("HOME", t.TempDir()) // no transcript in the server/account home
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner(session, owner)
	if err := store.SetSessionPersistenceClass(session, storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: store, runtimeCoordinator: NewRuntimeCoordinator(), sessionWorkspaceFolders: map[string]string{session: workspacePath}}
	store.AddEvent(session, nativeInputUserEvent("initial-user", "initial"))
	store.AddEvent(session, storeevents.Event{ID: "initial-reply", Type: "streaming_chunk", Data: agentevents.NewAgentEvent(&agentevents.StreamingChunkEvent{Content: "initial reply", Source: agentevents.StreamingChunkSourceTranscript})})
	read := func(user string) GetEventsResponse {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/sessions/"+session+"/events?durable_chat=1&limit=100&sync_native_transcript=1", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		req = mux.SetURLVars(req, map[string]string{"session_id": session})
		rec := httptest.NewRecorder()
		api.handleGetSessionEvents(rec, req)
		if user != owner {
			if rec.Code != 404 {
				t.Fatalf("foreign recovery status = %d", rec.Code)
			}
			return GetEventsResponse{}
		}
		if rec.Code != 200 {
			t.Fatalf("page status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response GetEventsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	read("bob")
	if got := len(store.GetAllEventsRaw(session)); got != 2 {
		t.Fatal("foreign read triggered recovery")
	}
	page := read(owner)
	var texts []string
	for _, row := range page.Events {
		text, _ := eventPayloadMap(row)["content"].(string)
		texts = append(texts, text)
	}
	if want := []string{"initial", "initial reply", "repeat", "reply one", "repeat", "reply two"}; !reflect.DeepEqual(texts, want) {
		t.Fatalf("recovered terminal conversation = %v, want %v", texts, want)
	}
	if next := read(owner); len(next.Events) != 6 || next.LatestSequence != page.LatestSequence {
		t.Fatalf("switch-back duplicated history: %+v", next)
	}
	// A restart loses the volatile owner cache, but a later terminal turn is
	// still recovered under the owner established by the durable journal.
	store.RemoveSession(session)
	writeClaudeChatTurns(t, path, "initial", "initial reply", "repeat", "reply one", "repeat", "reply two", "after restart", "restart reply")
	if next := read(owner); len(next.Events) != 8 || next.Events[6].Type != "user_message" {
		t.Fatalf("cold restore lost the next terminal turn: %+v", next)
	}
	var persisted builderConversationLog
	if err := json.Unmarshal([]byte(workspace.files[conversationPath]), &persisted); err != nil || len(persisted.ConversationHistory) != 8 {
		t.Fatalf("durable history lost terminal turns: %+v, %v", persisted, err)
	}
}
