package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkflowBuilderSummaryKeepsOneOwnChatAndHistory(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_RUNTIME_DEBUG", "0")
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	manifest := `{"id":"wf","kind":"workflow","created_by":"alice","access":{"owners":["alice"],"readers":["bob"]}}`
	workspace := &mockWorkspaceAPI{files: map[string]string{"Workflow/demo/workflow.json": manifest}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	api := lifecycleTestAPI()
	now := time.Now().UTC()
	api.activeSessions = map[string]*ActiveSessionInfo{}
	addLive := func(id, user string, age time.Duration, modify func(*ActiveSessionInfo)) {
		session := &ActiveSessionInfo{SessionID: id, UserID: user, AgentMode: "workflow_phase", PhaseID: "workflow-builder", WorkspacePath: "Workflow/demo", PresetQueryID: "wf", LastActivity: now.Add(age), Status: "running"}
		if modify != nil {
			modify(session)
		}
		api.activeSessions[id] = session
	}
	addLive("current", "alice", 0, nil)
	addLive("old-live", "alice", -time.Hour, nil)
	addLive("bob-private", "bob", time.Hour, nil)
	addLive("schedule-goallead--pulse_123", "alice", time.Hour, nil)
	addLive("bot-slack--route", "alice", time.Hour, nil)
	addLive("child", "alice", time.Hour, func(s *ActiveSessionInfo) { s.ParentSessionID = "current" })
	addLive("run", "alice", time.Hour, func(s *ActiveSessionInfo) { s.WorkshopMode = "run" })
	call := func(user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/workflow/builder-session?preset_query_id=wf&workspace_path=Workflow/demo&summary_only=true", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		api.handleGetWorkflowBuilderSession(w, r)
		return w
	}
	assertCurrent := func(user, id, source string) {
		t.Helper()
		w := call(user)
		var got workflowBuilderSessionResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.SessionID != id || got.Source != source || len(got.Events) != 0 || got.LastProcessedIndex != -1 {
			t.Fatalf("%s summary: status=%d response=%s", user, w.Code, w.Body.String())
		}
	}
	addLive("live-pulse-no-prefix", "alice", 2*time.Hour, nil)
	chatPolicyRoles.Store("live-pulse-no-prefix", chatPolicyRoleSnapshot{Origin: "pulse"})
	t.Cleanup(func() { chatPolicyRoles.Delete("live-pulse-no-prefix") })
	assertCurrent("alice", "current", "live")
	if _, exists := workspace.files[workflowBuilderBindingPath("alice", "wf")]; exists {
		t.Fatal("discovery created a pointer")
	}
	assertCurrent("bob", "bob-private", "live")
	anonymous := httptest.NewRecorder()
	api.handleGetWorkflowBuilderSession(anonymous, httptest.NewRequest("GET", "/api/workflow/builder-session?workspace_path=Workflow/demo&summary_only=true", nil))
	if anonymous.Code != 401 {
		t.Fatalf("anonymous summary admitted: %d", anonymous.Code)
	}
	mismatch := httptest.NewRequest("GET", "/api/workflow/builder-session?preset_query_id=other&workspace_path=Workflow/demo&summary_only=true", nil)
	mismatch = mismatch.WithContext(context.WithValue(mismatch.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	mismatchResponse := httptest.NewRecorder()
	api.handleGetWorkflowBuilderSession(mismatchResponse, mismatch)
	if mismatchResponse.Code != 400 {
		t.Fatalf("mixed workflow identities admitted: %d", mismatchResponse.Code)
	}
	if denied := call("mallory"); denied.Code != 403 {
		t.Fatalf("unshared workflow admitted: %d", denied.Code)
	}

	// Restart: pick exactly one of the caller's durable Builder chats. Newer
	// automated, Run and other-person transcripts cannot take over its identity.
	api.activeSessions = map[string]*ActiveSessionInfo{}
	assertCurrent("alice", "", "none")
	if _, exists := workspace.files[workflowBuilderBindingPath("alice", "wf")]; exists {
		t.Fatal("empty discovery minted a Builder")
	}
	delete(workspace.files, workflowBuilderBindingPath("alice", "wf"))
	delete(workspace.files, workflowBuilderBindingPath("bob", "wf"))
	write := func(id, user, mode string, age time.Duration) string {
		t.Helper()
		path := filepath.Join(docs, "Workflow/demo/builder/conversation/users", user, "2026-10-19", "session-"+id+"-conversation.json")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		origin := "interactive"
		if id == "pulse-marker" {
			origin = "pulse"
		}
		data := fmt.Sprintf(`{"session_id":%q,"user_id":%q,"workshop_mode":%q,"runtime":{"chat_policy_role_origin":%q},"updated_at":%q,"conversation_history":[{"Role":"human","Parts":[{"Text":"private history"}]}]}`, id, user, mode, origin, now.Add(age).Format(time.RFC3339Nano))
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	oldPath := write("old-saved", "alice", "workshop", -time.Hour)
	write("current-saved", "alice", "workshop", 0)
	write("bob-saved", "bob", "workshop", time.Hour)
	write("schedule-cron--job_123", "alice", "workshop", time.Hour)
	write("wfask-external", "alice", "workshop", time.Hour)
	write("run-saved", "alice", "run", time.Hour)
	write("pulse-marker", "alice", "workshop", time.Hour)
	assertCurrent("alice", "current-saved", "workspace")
	assertCurrent("bob", "bob-saved", "workspace")
	indexPath := filepath.Join(docs, "Workflow/demo/builder/conversation", chatHistoryIndexFileName)
	unchanged := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(indexPath, unchanged, unchanged); err != nil {
		t.Fatal(err)
	}
	assertCurrent("alice", "current-saved", "workspace")
	if info, err := os.Stat(indexPath); err != nil || !info.ModTime().Equal(unchanged) {
		t.Fatal("unchanged discovery rewrote the index", err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice"})
	collisionID := ""
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("wf-collision-%d", i)
		if chatHistoryIndexMutex(workflowBuilderBindingPath("alice", id)) == chatHistoryIndexMutex("Workflow/demo/builder/conversation/"+chatHistoryIndexFileName) {
			collisionID = id
			break
		}
	}
	if collisionID == "" {
		t.Fatal("could not construct history-lock hash collision")
	}
	done := make(chan error, 1)
	go func() { _, err := api.currentWorkflowBuilderSummary(ctx, collisionID, "Workflow/demo"); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pointer/history lock collision deadlocked discovery")
	}
	request := httptest.NewRequest("POST", "/api/workflow/builder-session", strings.NewReader(`{"preset_query_id":"wf","workspace_path":"Workflow/demo"}`))
	request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	response := httptest.NewRecorder()
	api.handleNewWorkflowBuilderSession(response, request)
	var fresh workflowBuilderSessionResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &fresh) != nil || fresh.SessionID == "" || fresh.SessionID == "current-saved" || !fresh.IsBuilder {
		t.Fatalf("New chat failed: %s", response.Body.String())
	}
	assertCurrent("alice", fresh.SessionID, "none")
	alias, err := api.currentWorkflowBuilderSummary(request.Context(), "wf", "/Workflow/demo/")
	if err != nil || alias.SessionID != fresh.SessionID {
		t.Fatalf("workspace alias lost manifest binding: %+v %v", alias, err)
	}
	// Pointer JSON is neither a legacy conversation.json nor a date-bucket
	// session-*-conversation.json. Both local and remote history scanners skip it.
	pointerPath := workflowBuilderBindingPath("alice", "wf")
	if _, isChat := chatHistorySessionIDFromWorkspacePath(chatHistoryRoot("alice"), pointerPath); isChat {
		t.Fatal("pointer became a history path")
	}
	localPointer := filepath.Join(docs, filepath.FromSlash(pointerPath))
	if err := os.MkdirAll(filepath.Dir(localPointer), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPointer, []byte(`{"session_id":"pointer-not-a-chat"}`), 0600); err != nil {
		t.Fatal(err)
	}
	personalHistory, handled, err := listChatHistorySessionsFromDisk("alice", chatHistoryRoot("alice"), "", 0, 0)
	if err != nil || !handled || len(personalHistory) != 0 {
		t.Fatalf("pointer listed as personal history: %+v %v", personalHistory, err)
	}
	if fresh.Sessions["pulse-marker"].Kind != "run" {
		t.Fatal("Pulse provenance was mistaken for a Builder")
	}
	if fresh.Sessions["current-saved"].IsBuilder || fresh.Sessions["current-saved"].Kind != "history" {
		t.Fatal("New chat did not archive old Builder projection")
	}
	readerRequest := request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: "bob"}))
	readerRequest.Body = io.NopCloser(strings.NewReader(`{"preset_query_id":"wf","workspace_path":"Workflow/demo"}`))
	reader := httptest.NewRecorder()
	api.handleNewWorkflowBuilderSession(reader, readerRequest)
	if reader.Code != 403 {
		t.Fatalf("reader replaced Builder: %d", reader.Code)
	}
	batchRequest := httptest.NewRequest("POST", "/api/workflow/builder-sessions", strings.NewReader(`{"workflows":[{"preset_query_id":"wf","workspace_path":"Workflow/demo","session_ids":["legacy-unknown"]},{"preset_query_id":"missing","workspace_path":"Workflow/missing"}]}`))
	batchRequest = batchRequest.WithContext(request.Context())
	batch := httptest.NewRecorder()
	api.handleWorkflowBuilderBindings(batch, batchRequest)
	var listed struct {
		Bindings map[string]workflowBuilderSessionResponse `json:"bindings"`
	}
	if batch.Code != 200 || json.Unmarshal(batch.Body.Bytes(), &listed) != nil || len(listed.Bindings) != 1 || listed.Bindings["wf"].SessionID != fresh.SessionID {
		t.Fatalf("wrong scoped batch: %s", batch.Body.String())
	}
	if listed.Bindings["wf"].Sessions["legacy-unknown"].Kind != "history" {
		t.Fatal("unknown visible tab was not classified as history")
	}
	if _, leaked := listed.Bindings["wf"].Sessions["bob-saved"]; leaked {
		t.Fatal("batch exposed another person's history")
	}
	if err := recordWorkflowBuilderSend(request.Context(), workflowBuilderScope{PresetID: "wf", Workspace: "Workflow/demo"}, "first-sent"); err != nil {
		t.Fatal(err)
	}
	assertCurrent("alice", "first-sent", "none")
	if err := recordWorkflowBuilderSend(readerRequest.Context(), workflowBuilderScope{PresetID: "wf", Workspace: "Workflow/demo"}, "reader-send"); err == nil {
		t.Fatal("reader recorded Builder")
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := api.resolveWorkflowBuilderBinding(request.Context(), "wf", "Workflow/demo", true)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	after, err := api.currentWorkflowBuilderSummary(request.Context(), "wf", "Workflow/demo")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, role := range after.Sessions {
		if role.IsBuilder {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("two windows exposed %d Builders", count)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("older Builder transcript was removed", err)
	}
	if got := call("mallory"); got.Code != 403 {
		t.Fatal("discovery broadened workflow access")
	}
}
