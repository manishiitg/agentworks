package server

import (
	"context"
	"encoding/json"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// PLAT-856-569: read access to another owner's chat is not export authority;
// the original owner can always supersede a terminal handoff without attach.
func TestChatTerminalOwnerRecoverySupersedesOldSession(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	crewOwnerTabsFixture(t, env, "other")
	profile, e := env.svc.registry.Resolve("work", 0, "owner")
	if e != nil {
		t.Fatal(e)
	}
	ctx := internalBotRequestContext(t.Context(), "owner")
	binding, _, e := resolveConversationBindingForUser(ctx, "owner", profile, "beta:chat:terminal-own")
	if e != nil {
		t.Fatal(e)
	}
	store := defaultProductConversationRegistryStore()
	initial, e := store.resolveOrCreate(ctx, "owner", profile, binding, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = setCodeTabName(ctx, "owner", initial.ConversationKey, "Own chat", "work"); e != nil {
		t.Fatal(e)
	}
	doc, e := store.loadDocument(ctx, productConversationRegistryPath("owner"))
	if e != nil {
		t.Fatal(e)
	}
	var source ProductConversationRecord
	for k, record := range doc.Entries {
		if record.ResourceID == "beta" || record.ConversationKey == "beta" || crewResourceProjectID(record.ConversationKey) == "beta" {
			record.Provider = "claude-code"
			doc.Entries[k] = record
			source = record
			break
		}
	}
	if source.SessionID == "" {
		t.Fatal("missing own source tab")
	}
	if e = store.writeDocument(ctx, productConversationRegistryPath("owner"), doc); e != nil {
		t.Fatal(e)
	}
	marker := chatTerminalState{Owner: "owner", Chat: source.ConversationKey, Session: source.SessionID, Native: "11111111-1111-4111-8111-111111111111", Receipt: "original", Out: true}
	if e = saveChatTerminal(marker); e != nil {
		t.Fatal(e)
	}
	call := func(user, method, op string, claims *UserClaims) *httptest.ResponseRecorder {
		q := url.Values{"conversation_key": {source.ConversationKey}, "action": {op}}
		r := httptest.NewRequest(method, "/api/external/v1/chats/terminal?"+q.Encode(), nil)
		if claims == nil {
			claims = &UserClaims{UserID: user}
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		env.api.handleChatTerminal(w, r)
		return w
	}
	if w := call("owner", "GET", "status", nil); w.Code != 200 {
		t.Fatalf("owner status %d %s", w.Code, w.Body.String())
	}
	if w := call("other", "POST", "fetch", nil); w.Code != 403 {
		t.Fatalf("peer exported: %d %s", w.Code, w.Body.String())
	}
	if w := call("owner", "POST", "here", &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}); w.Code != 403 {
		t.Fatalf("read grant changed handoff: %d", w.Code)
	}
	if chatTerminalTurnRefusal(source.SessionID) == "" {
		t.Fatal("terminal-out chat allowed a platform turn")
	}
	if !env.api.autoNotificationSessionUnreachable(source.SessionID) || env.api.executeSyntheticTurnWithContext(source.SessionID, "wake", "", nil, nil) {
		t.Fatal("background completion started while terminal owns chat")
	}
	nativeWakes.addCodex(source.SessionID, codexFinishedCommand{id: "queued"})
	nativeWakes.addClaude(source.SessionID)
	env.api.runQueuedNativeWakes(source.SessionID)
	commands, claude, muse := nativeWakes.take(source.SessionID)
	if len(commands) != 0 || claude || len(muse) != 0 {
		t.Fatal("terminal-out native wakes were not dropped")
	}
	oldTasks := claudeBackgroundTasks
	claudeBackgroundTasks = func(session string) []claudecode.SDKBackgroundTask {
		if session == source.SessionID {
			return []claudecode.SDKBackgroundTask{{TaskID: "running-task"}}
		}
		return nil
	}
	t.Cleanup(func() { claudeBackgroundTasks = oldTasks })
	if w := call("owner", "POST", "fetch", nil); w.Code != 409 || !strings.Contains(w.Body.String(), "turn_running") {
		t.Fatalf("handoff ignored native background task: %d %s", w.Code, w.Body.String())
	}
	claudeBackgroundTasks = oldTasks
	env.api.activeSessions = map[string]*ActiveSessionInfo{source.SessionID: {Status: "running"}}
	if w := call("owner", "POST", "here", nil); w.Code != 409 {
		t.Fatalf("busy recovery: %d %s", w.Code, w.Body.String())
	}
	env.api.activeSessions = map[string]*ActiveSessionInfo{}
	if w := call("owner", "POST", "here", nil); w.Code != 200 {
		t.Fatalf("owner recovery: %d %s", w.Code, w.Body.String())
	}
	after, e := readChatTerminal(source.SessionID)
	if e != nil || after.Out || after.Receipt == marker.Receipt {
		t.Fatalf("old handoff retained: %+v %v", after, e)
	}
	if chatTerminalTurnRefusal(source.SessionID) != "" {
		t.Fatal("recovered chat still locked")
	}
	if w := call("owner", "POST", "return", nil); w.Code != 409 {
		t.Fatalf("old terminal return: %d %s", w.Code, w.Body.String())
	}
	// An interrupted receipt must not hide the only recovery button.
	p, _ := chatTerminalPath(source.SessionID)
	if e = os.WriteFile(p, []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	w := call("owner", http.MethodGet, "status", nil)
	var status map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if w.Code != 200 || status["out"] != true {
		t.Fatalf("corrupt receipt hid recovery: %d %s", w.Code, w.Body.String())
	}
	if w = call("owner", http.MethodPost, "here", nil); w.Code != 200 {
		t.Fatalf("corrupt receipt recovery: %d %s", w.Code, w.Body.String())
	}
}

// PLAT-856-569: returning a native tail must never replace older provider
// history, attachments or tool cards, and a retried return appends only once.
func TestChatTerminalReturnAppendsHistoryWithoutLosingEarlierCards(t *testing.T) {
	const user = "owner"
	const session = "terminal-history"
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	workspace := registerTestCrew(t, "terminal-history-crew", user, true)
	date := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	path, ok := workProjectChatHistoryConversationPath(user, workspace, session, date)
	if !ok {
		t.Fatal("missing own canonical history path")
	}
	original := `{"session_id":"terminal-history","user_id":"owner","conversation_history":[{"Role":"human","Parts":[{"Text":"Pre-switch question","attachment":{"file":"kept.png"}}],"unknown_metadata":"keep"},{"Role":"tool","Parts":[{"Text":"Earlier tool card"}]}],"ui_events":[{"id":"prior-tool"}]}`
	physical, ok := resolveLocalWorkflowDir(workspace)
	if !ok {
		t.Fatal("missing private Crew storage")
	}
	physical = filepath.Join(physical, "builder", "conversation", "2026-11-14", chatHistoryConversationFileName(session))
	if e := os.MkdirAll(filepath.Dir(physical), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(physical, []byte(original), 0600); e != nil {
		t.Fatal(e)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{path: original}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	journal, e := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer journal.Close()
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner(session, user)
	if e = store.SetSessionPersistenceClass(session, storeevents.SessionPersistenceInteractiveChat); e != nil {
		t.Fatal(e)
	}
	var prior storeevents.Event
	if e = json.Unmarshal([]byte(`{"id":"prior-card","type":"tool_call_end","session_id":"terminal-history","data":{"type":"tool_call_end","data":{"tool_name":"prior_tool","result":"kept"}}}`), &prior); e != nil {
		t.Fatal(e)
	}
	if e = store.ImportDurableChatEvents(session, []storeevents.Event{prior}); e != nil {
		t.Fatal(e)
	}
	api := &StreamingAPI{eventStore: store}
	stage := t.TempDir()
	prefix := `{"type":"user","timestamp":"2026-11-14T00:00:00Z","message":{"content":"Already in saved history"}}` + "\n"
	// More than the native UI window: the authoritative return must keep them all.
	var tail strings.Builder
	for range 205 {
		tail.WriteString(`{"type":"assistant","timestamp":"2026-11-14T01:00:00Z","message":{"content":"New answer"}}` + "\n")
	}
	if e = os.WriteFile(filepath.Join(stage, "session.jsonl"), []byte(prefix+tail.String()), 0600); e != nil {
		t.Fatal(e)
	}
	state := chatTerminalState{Receipt: "same-return", Session: session, JournalBytes: int64(len(prefix))}
	c := ProductConversationRecord{SessionID: session, WorkspacePath: workspace}
	req := httptest.NewRequest("POST", "/", nil)
	for attempt := 0; attempt < 2; attempt++ {
		var doc map[string]json.RawMessage
		mock.mu.Lock()
		raw := mock.files[path]
		mock.mu.Unlock()
		if e = json.Unmarshal([]byte(raw), &doc); e != nil {
			t.Fatal(e)
		}
		if e = api.saveTerminalReturnedHistory(req, crewroot.Roots{}, c, user, doc, stage, state); e != nil {
			t.Fatal(e)
		}
	}
	mock.mu.Lock()
	raw := mock.files[path]
	mock.mu.Unlock()
	var doc struct {
		History  []json.RawMessage `json:"conversation_history"`
		UIEvents []json.RawMessage `json:"ui_events"`
	}
	if e = json.Unmarshal([]byte(raw), &doc); e != nil {
		t.Fatal(e)
	}
	if len(doc.History) != 207 || len(doc.UIEvents) != 1 || !strings.Contains(string(doc.History[0]), `"attachment":{"file":"kept.png"}`) || !strings.Contains(string(doc.History[0]), `"unknown_metadata":"keep"`) || !strings.Contains(string(doc.History[1]), "Earlier tool card") {
		t.Fatalf("return lost older content or duplicated tail: history=%d raw=%s", len(doc.History), raw)
	}
	page, e := journal.ReadPage(session, storeevents.DurableEventPageOptions{Limit: 500})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Events) != 206 || page.Events[0].ID != "prior-card" {
		t.Fatalf("journal lost old card or duplicated tail: %d", len(page.Events))
	}
}
