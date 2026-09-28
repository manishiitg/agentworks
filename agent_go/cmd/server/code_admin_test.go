package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// newCodeAdminFixture is the privacy fixture plus an admin and a Code HOME
// holding credentials.
func newCodeAdminFixture(t *testing.T, adminInspection bool) (*StreamingAPI, *mockWorkspaceAPI) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true},{"id":"boss","username":"boss","admin":true,"can_create":true}]}`)
	mock := &mockWorkspaceAPI{files: map[string]string{
		codePrivacyOwnerRoot + "/product.json":                       `{"schema_version":1,"product":"code","id":"c0de0001-0000","title":"App","session_id":"code:project:c0de0001-0000"}`,
		codePrivacyOwnerRoot + "/workflow.json":                      `{"schema_version":1,"id":"c0de0001-0000","label":"App","capabilities":{}}`,
		codePrivacyOwnerRoot + "/code/main.go":                       "package main",
		codePrivacyOwnerRoot + "/.sandbox-cache/home/.git-credentials": "https://token@github.com",
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "code", Name: "Code", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "code", AdminInspection: adminInspection,
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Code/projects"},
		},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	return &StreamingAPI{agentProfiles: registry}, mock
}

func adminGet(api *StreamingAPI, handler func(*StreamingAPI, http.ResponseWriter, *http.Request), caller, target string, vars map[string]string) *httptest.ResponseRecorder {
	req := mux.SetURLVars(profileRouteRequest(http.MethodGet, target, nil, caller), vars)
	rec := httptest.NewRecorder()
	handler(api, rec, req)
	return rec
}

func TestCodeAdminInspectionIsReadOnlyAndAudited(t *testing.T) {
	api, mock := newCodeAdminFixture(t, true)
	project := map[string]string{"owner": "owner", "project_id": "c0de0001-0000"}

	// Not an admin: refused everywhere, and nothing is logged.
	for _, caller := range []string{"other", "owner"} {
		if rec := adminGet(api, (*StreamingAPI).handleAdminListCodeWorkspaces, caller, "/api/admin/code/workspaces", nil); rec.Code != http.StatusForbidden {
			t.Fatalf("%s listed every Code: %d", caller, rec.Code)
		}
		if rec := adminGet(api, (*StreamingAPI).handleAdminCodeFiles, caller, "/x", project); rec.Code != http.StatusForbidden {
			t.Fatalf("%s read files as admin: %d", caller, rec.Code)
		}
	}

	rec := adminGet(api, (*StreamingAPI).handleAdminListCodeWorkspaces, "boss", "/api/admin/code/workspaces", nil)
	var listed struct {
		Workspaces []codeAdminWorkspace `json:"workspaces"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Workspaces) != 1 || listed.Workspaces[0].OwnerID != "owner" {
		t.Fatalf("admin listing = %d %s", rec.Code, rec.Body.String())
	}

	rec = adminGet(api, (*StreamingAPI).handleAdminCodeFiles, "boss", "/x", project)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "code/main.go") || strings.Contains(rec.Body.String(), "sandbox-cache") {
		t.Fatalf("admin files = %d %s", rec.Code, rec.Body.String())
	}
	rec = adminGet(api, (*StreamingAPI).handleAdminCodeFile, "boss", "/x?path=code/main.go", project)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "package main") {
		t.Fatalf("admin file = %d %s", rec.Code, rec.Body.String())
	}
	for _, hidden := range []string{".sandbox-cache/home/.git-credentials", "../../other/x"} {
		if rec := adminGet(api, (*StreamingAPI).handleAdminCodeFile, "boss", "/x?path="+hidden, project); rec.Code != http.StatusNotFound {
			t.Fatalf("admin read %s: %d %s", hidden, rec.Code, rec.Body.String())
		}
	}

	// Every admin view is in the log, with who, which Code and what.
	mock.mu.Lock()
	log := mock.files[codeAdminAuditPath(time.Now())]
	mock.mu.Unlock()
	for _, want := range []string{`"action":"list_workspaces"`, `"action":"list_files"`, `"target":"code/main.go"`, `"admin_id":"boss"`, `"owner_id":"owner"`} {
		if !strings.Contains(log, want) {
			t.Fatalf("audit log lacks %s:\n%s", want, log)
		}
	}
	if strings.Contains(log, `"admin_id":"other"`) {
		t.Fatal("a refused request was logged as a view")
	}
	rec = adminGet(api, (*StreamingAPI).handleAdminCodeAudit, "boss", "/api/admin/code/audit", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "read_file") {
		t.Fatalf("audit read = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCodeAdminInspectionNeedsTheProductSetting(t *testing.T) {
	api, _ := newCodeAdminFixture(t, false)
	if rec := adminGet(api, (*StreamingAPI).handleAdminListCodeWorkspaces, "boss", "/api/admin/code/workspaces", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("inspection without admin_inspection = %d", rec.Code)
	}
	// Crew and personal chat history stay owner-only even for admins.
	if chatHistoryVisibleTo("owner", "boss", true) {
		t.Fatal("an admin sees another user's chat history")
	}
}
