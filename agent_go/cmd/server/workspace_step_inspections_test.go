package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func TestWorkspaceStepInspectionsDiscoveryAndAccess(t *testing.T) {
	t.Setenv("AGENTWORKS_RUNTIME_DEBUG", "0")
	t.Setenv("MULTI_USER_MODE", "true")
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	api := lifecycleTestAPI()
	api.activeSessions = map[string]*ActiveSessionInfo{}
	manifest := func(owner, kind string) {
		t.Helper()
		if err := writeFileToWorkspace(context.Background(), "Workflow/example/workflow.json", fmt.Sprintf(`{"id":"goal","kind":%q,"created_by":%q,"access":{"owners":[%q],"readers":["bob"]}}`, kind, owner, owner)); err != nil {
			t.Fatal(err)
		}
	}
	manifest("alice", "workflow")
	// PLAT-825: a fresh Goal may have a database before any background work.
	// Inspection stays an empty read; it must not require the retired engine.
	_, freshDB, err := openWorkflowStoreDB(context.Background(), "Workflow/example", true)
	if err != nil {
		t.Fatal(err)
	}
	freshDB.Close()
	if rows, err := backgroundAgentLogForSession(context.Background(), "Workflow/example", "never-started"); err != nil || len(rows) != 0 {
		t.Fatalf("fresh log read: %v %v", rows, err)
	}
	emptyRequest := httptest.NewRequest("GET", "/api/workflow/step-inspections?workspace_path=Workflow%2Fexample", nil)
	emptyRequest = emptyRequest.WithContext(context.WithValue(emptyRequest.Context(), UserContextKey, &UserClaims{UserID: "alice", Username: "alice"}))
	emptyReply := httptest.NewRecorder()
	api.handleWorkspaceStepInspections(emptyReply, emptyRequest)
	var emptyResponse workspaceStepInspectionsResponse
	if emptyReply.Code != 200 || json.Unmarshal(emptyReply.Body.Bytes(), &emptyResponse) != nil || len(emptyResponse.Items) != 0 {
		t.Fatalf("fresh inspection: %d %s", emptyReply.Code, emptyReply.Body)
	}
	step := func(session, id, parent, kind string) {
		t.Helper()
		api.bgAgentRegistry.Register(session, &BackgroundAgent{ID: id, Name: "Research MCP_API_TOKEN=fixture-secret", Kind: kind, ParentExecutionID: parent, Status: BGAgentRunning, CreatedAt: time.Now(), Metadata: map[string]string{"step_id": "research"}})
		api.emitBackgroundAgentStarted(session, id, "Research MCP_API_TOKEN=fixture-secret", "", parent, orchEvents.ExecutionKind(kind))
	}
	add := func(session, user, origin string) {
		t.Helper()
		api.activeSessions[session] = &ActiveSessionInfo{SessionID: session, WorkspacePath: "Workflow/example", UserID: user, TriggeredBy: origin}
		if origin == "schedule" {
			// PLAT-746-265: whole runs need the same durable recorder as
			// workshop turns, without a second chat notification per step.
			notifier := &workshopExecutionBgNotifier{api: api, sessionID: session, workspacePath: "Workflow/example", userID: user, suppressAutoNotification: true}
			notifier.OnExecutionStart(stepworkflow.WorkshopExecutionStart{ID: "step-" + session, Name: "Research", Kind: "workflow_step", ParentExecutionID: "conversation-turn-root", Metadata: map[string]string{"step_id": "research", "run_folder": "iteration-0"}})
			notifier.OnExecutionComplete("step-"+session, "Research", "PRIVATE RESULT", nil, nil)
			select {
			case message := <-api.bgAgentRegistry.GetNotificationChannel(session):
				t.Fatalf("whole-run step queued another chat notification: %s", message)
			default:
			}
		} else {
			step(session, "step-"+session, "conversation-turn-root", "workflow_step")
			api.emitBackgroundAgentCompleted(session, "step-"+session, "Research", "completed", "PRIVATE RESULT", "", "1s")
		}
		directory := filepath.Join(docs, "Workflow/example/builder/conversation/2026-10-18")
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(map[string]any{"session_id": session, "user_id": user, "triggered_by": origin, "runtime": map[string]any{"workspace_path": "Workflow/example"}, "conversation_history": []any{}})
		if err := os.WriteFile(filepath.Join(directory, "session-"+session+"-conversation.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	add("mcp-parent", "alice", "mcp")
	add("schedule-job--123_456", "alice", "schedule")
	add("bob-parent", "bob", "schedule")
	step("mcp-parent", "nested", "step-mcp-parent", "agent")
	step("mcp-parent", "wrapper", "step-mcp-parent", "full_run")
	step("mcp-parent", "nested-grandchild", "wrapper", "agent")
	step("mcp-parent", "cycle-a", "cycle-b", "full_run")
	step("mcp-parent", "cycle-b", "cycle-a", "full_run")
	step("mcp-parent", "cycle-child", "cycle-a", "agent")
	// No browser tabs, active sessions, registry, or event store survive restart.
	api.activeSessions = map[string]*ActiveSessionInfo{}
	api.bgAgentRegistry = NewBackgroundAgentRegistry()
	api.eventStore = nil
	call := func(user string, query url.Values) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", "/api/workflow/step-inspections?"+query.Encode(), nil)
		request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		recorder := httptest.NewRecorder()
		api.handleWorkspaceStepInspections(recorder, request)
		return recorder
	}
	query := url.Values{"workspace_path": {"Workflow/example"}}
	anonymous := httptest.NewRecorder()
	api.handleWorkspaceStepInspections(anonymous, httptest.NewRequest("GET", "/api/workflow/step-inspections?"+query.Encode(), nil))
	if anonymous.Code != 401 {
		t.Fatalf("multi-user route admitted an anonymous caller: %d", anonymous.Code)
	}
	owner := call("alice", query)
	var response workspaceStepInspectionsResponse
	if owner.Code != 200 || json.Unmarshal(owner.Body.Bytes(), &response) != nil || len(response.Items) != 2 {
		t.Fatalf("own orphan discovery with debug off: %d %s", owner.Code, owner.Body)
	}
	foundMCP := false
	for _, item := range response.Items {
		if item.Parent.SessionID == "bob-parent" || item.Inspection.ExecutionID == "nested" || item.Inspection.SessionID != item.Parent.SessionID || item.Inspection.WorkspacePath != "Workflow/example" {
			t.Fatalf("foreign/nested receipt: %+v", item)
		}
		if item.Parent.TriggeredBy == "mcp" {
			foundMCP = true
		}
	}
	if !foundMCP || strings.Contains(owner.Body.String(), "PRIVATE RESULT") || strings.Contains(owner.Body.String(), "fixture-secret") || strings.Contains(owner.Body.String(), "conversation_history") {
		t.Fatalf("noncompact or unredacted response: %s", owner.Body)
	}
	other := call("bob", query)
	if other.Code != 200 || json.Unmarshal(other.Body.Bytes(), &response) != nil || len(response.Items) != 1 || response.Items[0].Parent.SessionID != "bob-parent" {
		t.Fatalf("caller separation: %d %s", other.Code, other.Body)
	}
	if err := writeFileToWorkspace(context.Background(), "Workflow/example/workflow.json", `{"id":"goal","created_by":"alice","access":{"owners":["alice","bob"]}}`); err != nil {
		t.Fatal(err)
	}
	coowner := call("alice", query)
	if coowner.Code != 200 || strings.Contains(coowner.Body.String(), "bob-parent") {
		t.Fatalf("co-owner's run leaked: %d %s", coowner.Code, coowner.Body)
	}
	manifest("alice", "workflow")
	query.Set("session_id", "bob-parent")
	if denied := call("alice", query); denied.Code != 404 {
		t.Fatalf("owner acquired collaborator's chat: %d", denied.Code)
	}
	query.Del("session_id")
	query.Add("exclude_session_id", "mcp-parent")
	excluded := call("alice", query)
	if excluded.Code != 200 || strings.Contains(excluded.Body.String(), "mcp-parent") {
		t.Fatalf("listed parent duplicated: %d %s", excluded.Code, excluded.Body)
	}
	query.Del("exclude_session_id")
	// A long continuing parent is paged by executions, not dropped at a cap.
	api.activeSessions["mcp-parent"] = &ActiveSessionInfo{SessionID: "mcp-parent", UserID: "alice", WorkspacePath: "Workflow/example"}
	for i := 0; i < 21; i++ {
		step("mcp-parent", fmt.Sprintf("page-%02d", i), "", "workflow_step")
	}
	delete(api.activeSessions, "mcp-parent")
	query.Set("session_id", "mcp-parent")
	page := call("alice", query)
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &response) != nil || len(response.Items) == 0 || len(response.Items) > 20 || response.NextCursor == nil {
		t.Fatalf("bounded page: %d %s", page.Code, page.Body)
	}
	seen := map[string]bool{}
	for _, item := range response.Items {
		seen[item.Inspection.ExecutionID] = true
	}
	cursor := *response.NextCursor
	api.activeSessions["mcp-parent"] = &ActiveSessionInfo{SessionID: "mcp-parent", UserID: "alice", WorkspacePath: "Workflow/example"}
	step("mcp-parent", "newer-during-page", "conversation-turn-root", "workflow_step")
	delete(api.activeSessions, "mcp-parent")
	_, db, err := openWorkflowStoreDB(context.Background(), "Workflow/example", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE background_agent_log SET updated_at='2099-01-01T00:00:00Z' WHERE agent_id='newer-during-page'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	query.Set("cursor", cursor)
	page = call("alice", query)
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &response) != nil || len(response.Items) == 0 {
		t.Fatalf("older page: %d %s", page.Code, page.Body)
	}
	for _, item := range response.Items {
		if seen[item.Inspection.ExecutionID] || item.Inspection.ExecutionID == "newer-during-page" {
			t.Fatal("new arrivals shifted the older page")
		}
	}
	query.Set("cursor", cursor)
	if denied := call("bob", query); denied.Code != 400 {
		t.Fatalf("cursor reused across callers: %d", denied.Code)
	}
	query.Del("cursor")
	query.Del("session_id")
	_, db, err = openWorkflowStoreDB(context.Background(), "Workflow/example", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err = db.Exec(`INSERT INTO background_agent_log(workspace_path,session_id,agent_id,name,kind,status,updated_at) VALUES('Workflow/example','bob-parent',?,'PRIVATE','workflow_step','completed','2100-01-01T00:00:00Z')`, fmt.Sprintf("foreign-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	hiddenPage := call("alice", query)
	if hiddenPage.Code != 200 || json.Unmarshal(hiddenPage.Body.Bytes(), &response) != nil || len(response.Items) != 0 || response.NextCursor == nil {
		t.Fatalf("bounded foreign scan: %d %s", hiddenPage.Code, hiddenPage.Body)
	}
	raw, err := base64.RawURLEncoding.DecodeString(*response.NextCursor)
	if err != nil || strings.Contains(string(raw), "bob-parent") {
		t.Fatal("cursor exposed another caller's session identity")
	}
	query.Set("cursor", *response.NextCursor)
	visiblePage := call("alice", query)
	if visiblePage.Code != 200 || json.Unmarshal(visiblePage.Body.Bytes(), &response) != nil || len(response.Items) != 20 {
		t.Fatalf("visible receipts not filled after foreign scan: %d %s", visiblePage.Code, visiblePage.Body)
	}
	query.Del("cursor")
	if err := writeFileToWorkspace(context.Background(), userProductAccessFilePath(), `{"alice":{"products":["work"]}}`); err != nil {
		t.Fatal(err)
	}
	if denied := call("alice", query); denied.Code != 404 {
		t.Fatalf("product restriction: %d", denied.Code)
	}
	if err := writeFileToWorkspace(context.Background(), userProductAccessFilePath(), `{"alice":{"products":["agentworks"],"workflow_ids":["other-goal"]}}`); err != nil {
		t.Fatal(err)
	}
	if denied := call("alice", query); denied.Code != 404 {
		t.Fatalf("workflow restriction: %d", denied.Code)
	}
	if err := writeFileToWorkspace(context.Background(), userProductAccessFilePath(), `{}`); err != nil {
		t.Fatal(err)
	}
	manifest("other", "workflow")
	if denied := call("alice", query); denied.Code != 404 {
		t.Fatalf("revocation bypassed warmed cache: %d", denied.Code)
	}
	manifest("alice", "relay")
	if denied := call("alice", query); denied.Code != 404 {
		t.Fatalf("Relay advertised Goal steps: %d", denied.Code)
	}
}
