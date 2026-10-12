package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Exercise real MCP, the shared native attachment executor and disk writer.
// Detachment must release live read grants without bypassing Crew deletion checks.
func TestExternalWorkflowCrewDetachOverMCP(t *testing.T) {
	f := newBuilderOperationFixture(t)
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	workspace := httptest.NewServer(router)
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	m := NewWorkflowManifest("Attachment cleanup")
	m.ID, m.CreatedBy = "invoices", "owner"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}, Editors: []string{"outsider"}, Readers: []string{"reader"}}
	const removedRoot = "_users/owner/Chats/Work/projects/remove-crew"
	m.CrewAttachments = []workflowtypes.CrewAttachment{
		{ID: "remove-binding", Alias: "remove-crew", CrewProfileID: "work", CrewProjectID: "remove-project", CrewWorkspacePath: removedRoot},
		{ID: "keep-binding", Alias: "keep-crew", CrewProfileID: "work", CrewProjectID: "keep-project", CrewWorkspacePath: "_users/owner/Chats/Work/projects/keep-crew"},
	}
	save := func() {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		f.write(t, "Workflow/invoices/workflow.json", string(raw))
	}
	save()
	token, raw := f.issue(t, "owner")
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	server := serveExternalMCP(t, f.api, claims)
	cli := dialExternalMCP(t, t.Context(), server.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	result := callRemoteTool(t, t.Context(), cli, "get_api_spec", map[string]any{"names": []any{"workflow_crews"}})
	requireRemoteSuccess(t, result, "attachment discovery")
	f.call(t, raw, "workflow_crews", map[string]any{"action": "list"}, 200)
	_, editor := f.issue(t, "outsider")
	f.call(t, editor, "workflow_crews", map[string]any{"action": "list"}, 200)
	_, reader := f.issue(t, "reader")
	f.call(t, reader, "workflow_crews", map[string]any{"action": "detach", "alias": "remove-crew"}, 403)
	f.call(t, raw, "workflow_crews", map[string]any{"action": "attach", "alias": "new-crew"}, 400)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, guarded, err := store.Issue(t.Context(), accesstokens.Token{Name: "Source only", UserID: "outsider", Scopes: builderConsentScopes, WorkflowIDs: []string{"invoices"}, FileGuard: &wf.FolderGuard{ReadPaths: []string{"code"}, WritePaths: []string{"code"}}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, guarded, "workflow_crews", map[string]any{"action": "detach", "alias": "remove-crew"}, 403)
	f.write(t, "Workflow/invoices/planning/plan.json", `{"steps":[{"id":"fetch-invoices","type":"scripted","title":"Fetch","description":"Read remove-crew/report.md."}]}`)
	if used, err := crewUsedByWorkflow(t.Context(), "work", "remove-project", removedRoot); err != nil || !used {
		t.Fatalf("saved attachment must block Crew deletion: %v %v", used, err)
	}
	const session = "attachment-cleanup-session"
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	common.ApplySessionWorkflowFolderAccess(session, "Workflow/invoices", []string{"Workflow/invoices/runs"}, nil, nil, map[string]string{"WORKFLOW_CREW_remove": removedRoot})
	common.GrantSessionCrewAttachmentReads(session, []string{removedRoot})
	result = callRemoteTool(t, t.Context(), cli, "call_tool", map[string]any{"name": "workflow_crews", "arguments": map[string]any{"workflow_id": "invoices", "action": "detach", "alias": "remove-crew"}})
	requireRemoteSuccess(t, result, "native detach")
	reply, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(reply), "warnings") || !strings.Contains(string(reply), "fetch-invoices") {
		t.Fatal("detach did not warn about remaining step alias references")
	}
	var saved WorkflowManifest
	if err := json.Unmarshal([]byte(f.read(t, "Workflow/invoices/workflow.json")), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.CrewAttachments) != 1 || saved.CrewAttachments[0].Alias != "keep-crew" || saved.Label != m.Label || len(saved.Access.Editors) != 1 {
		t.Fatalf("detach changed unrelated workflow state: %+v", saved)
	}
	config := common.GetSessionShellConfig(session)
	if config == nil {
		t.Fatal("live session lost its unrelated grants")
	}
	for _, path := range config.ReadPaths {
		if strings.TrimRight(path, "/") == removedRoot {
			t.Fatal("detached Crew read grant survived")
		}
	}
	for _, value := range config.Env {
		if value == removedRoot {
			t.Fatal("detached Crew environment binding survived")
		}
	}
	if used, err := crewUsedByWorkflow(t.Context(), "work", "remove-project", removedRoot); err != nil || used {
		t.Fatalf("unused detached Crew should be deletable: %v %v", used, err)
	}
	f.write(t, "Workflow/invoices/planning/plan.json", `{"steps":[{"type":"crew","id":"crew-step","crew_profile_id":"work","crew_project_id":"remove-project"}]}`)
	if used, err := crewUsedByWorkflow(t.Context(), "work", "remove-project", removedRoot); err != nil || !used {
		t.Fatalf("remaining Crew step must still block deletion: %v %v", used, err)
	}
	m = &saved
	release, err := teamLock(t.Context(), "Workflow/invoices")
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			release()
		}
	}()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest("POST", "/api/external/v1/call", strings.NewReader(`{"name":"workflow_crews","arguments":{"workflow_id":"invoices","action":"detach","alias":"keep-crew"}}`)).WithContext(t.Context())
		request.Header.Set("Authorization", "Bearer "+raw)
		response := httptest.NewRecorder()
		AuthMiddleware(http.HandlerFunc(f.api.handleExternalCall)).ServeHTTP(response, request)
		done <- response
	}()
	select {
	case response := <-done:
		t.Fatalf("detach did not wait for the workflow source lock: %d", response.Code)
	case <-time.After(100 * time.Millisecond):
	}
	m.Access = &WorkflowAccess{Owners: []string{"outsider"}, Readers: []string{"owner"}}
	save()
	release()
	locked = false
	select {
	case response := <-done:
		if response.Code != http.StatusForbidden {
			t.Fatalf("queued detach kept stale authoring access: %d", response.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued detach did not finish after the lock was released")
	}
	f.call(t, raw, "workflow_crews", map[string]any{"action": "detach", "alias": "keep-crew"}, 403)
}
