package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The /api/wp proxy enforces per-workflow access, keeps server configuration
// and the whole-workspace view admin-only, and lets only owners change a
// workflow's access record.
func TestWorkspaceProxyPolicy(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"root","username":"root","role":"admin"},
		{"id":"alice","username":"alice","role":"creator"},
		{"id":"bob","username":"bob","role":"editor","products":["agentworks"]}]}`)
	private, _ := json.Marshal(WorkflowManifest{ID: "private", Label: "Private", Access: &WorkflowAccess{Owners: []string{"alice"}}})
	shared, _ := json.Marshal(WorkflowManifest{ID: "shared", Label: "Shared", Access: &WorkflowAccess{Owners: []string{"alice"}, Editors: []string{"bob"}}})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/private"): string(private),
		manifestPath("Workflow/shared"):  string(shared),
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	type call struct {
		user, method, url string
		body              map[string]any
		allowed           bool
	}
	for _, tc := range []call{
		// Whole-workspace export and server configuration.
		{"bob", http.MethodPost, "/api/wp/api/workspace/export", map[string]any{"workspace_path": "."}, false},
		{"root", http.MethodPost, "/api/wp/api/workspace/export", map[string]any{"workspace_path": "."}, true},
		{"bob", http.MethodGet, "/api/wp/api/documents/config/users.json", nil, false},
		{"bob", http.MethodGet, "/api/wp/api/documents/_system/costs.jsonl", nil, false},
		{"bob", http.MethodGet, "/api/wp/api/search?query=x", nil, false},
		// Reads of a workflow follow its access record.
		{"bob", http.MethodGet, "/api/wp/api/documents/Workflow/private/notes.md", nil, false},
		{"alice", http.MethodGet, "/api/wp/api/documents/Workflow/private/notes.md", nil, true},
		{"bob", http.MethodPost, "/api/wp/api/query", map[string]any{"db_path": "Workflow/private/db/db.sqlite", "sql": "select 1"}, false},
		{"bob", http.MethodPost, "/api/wp/api/query", map[string]any{"db_path": "Workflow/shared/db/db.sqlite", "sql": "select 1"}, true},
		{"bob", http.MethodPost, "/api/wp/api/workspace/export", map[string]any{"workspace_path": "Workflow/private"}, false},
		// Writes need write access; body-path routes count too.
		{"bob", http.MethodPut, "/api/wp/api/documents/Workflow/shared/notes.md", map[string]any{"content": "x"}, true},
		{"bob", http.MethodPost, "/api/wp/api/folders/copy", map[string]any{"source_path": "Chats/a", "destination_path": "Workflow/private/a"}, false},
		{"bob", http.MethodPost, "/api/wp/api/report-field", map[string]any{"db_path": "Workflow/private/db/db.sqlite"}, false},
		// workflow.json (the access record) is owner-only.
		{"bob", http.MethodPut, "/api/wp/api/documents/Workflow/shared/workflow.json", map[string]any{"content": "{}"}, false},
		{"alice", http.MethodPut, "/api/wp/api/documents/Workflow/shared/workflow.json", map[string]any{"content": "{}"}, true},
		// The caller's own tree is untouched by these rules.
		{"bob", http.MethodPut, "/api/wp/api/documents/Chats/notes.md", map[string]any{"content": "x"}, true},
		{"bob", http.MethodGet, "/api/wp/api/documents", nil, true},
	} {
		var body []byte
		if tc.body != nil {
			body, _ = json.Marshal(tc.body)
		}
		req := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(body))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: tc.user, Username: tc.user}))
		status, detail, cleanup := workspaceProxyCrossUserBlock(req, tc.user)
		if cleanup != nil {
			cleanup()
		}
		if allowed := status == 0; allowed != tc.allowed {
			t.Errorf("%s %s %s %v: status=%d (%s), want allowed=%v", tc.user, tc.method, tc.url, tc.body, status, detail, tc.allowed)
		}
	}
}

// Workflow routes behind requireWorkflowWriteAccess check the named
// workflow's own access record, not only the account tier; deleting a
// workflow needs a true owner.
func TestWorkflowRoutesCheckTheNamedWorkflow(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"alice","username":"alice","role":"creator"},
		{"id":"bob","username":"bob","role":"editor","products":["agentworks"]}]}`)
	private, _ := json.Marshal(WorkflowManifest{ID: "private", Label: "Private", Access: &WorkflowAccess{Owners: []string{"alice"}}})
	shared, _ := json.Marshal(WorkflowManifest{ID: "shared", Label: "Shared", Access: &WorkflowAccess{Owners: []string{"alice"}, Editors: []string{"bob"}}})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/private"): string(private),
		manifestPath("Workflow/shared"):  string(shared),
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	as := func(user string, req *http.Request) *http.Request {
		return req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
	}
	reached := ""
	handler := requireWorkflowWriteAccess(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			WorkspacePath string `json:"workspace_path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		reached = body.WorkspacePath + r.URL.Query().Get("workspace_path")
		w.WriteHeader(http.StatusOK)
	})
	for _, tc := range []struct {
		target string
		code   int
	}{{"Workflow/private", http.StatusForbidden}, {"Workflow/shared", http.StatusOK}, {"Workflow/new-one", http.StatusOK}} {
		reached = ""
		req := as("bob", httptest.NewRequest(http.MethodPost, "/api/workflow/plan/update-step", bytes.NewReader([]byte(`{"workspace_path":"`+tc.target+`"}`))))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != tc.code || (tc.code == http.StatusOK && reached != tc.target) {
			t.Fatalf("bob body %s: code=%d reached=%q", tc.target, rec.Code, reached)
		}
		rec = httptest.NewRecorder()
		handler(rec, as("bob", httptest.NewRequest(http.MethodDelete, "/api/workflow/run?workspace_path="+tc.target, nil)))
		if rec.Code != tc.code {
			t.Fatalf("bob query %s: code=%d", tc.target, rec.Code)
		}
	}

	api := &StreamingAPI{}
	for _, tc := range []struct {
		user, target string
		code         int
	}{{"bob", "Workflow/shared", http.StatusForbidden}, {"bob", "Workflow/private", http.StatusForbidden}, {"alice", "_users/bob/Chats", http.StatusBadRequest}} {
		rec := httptest.NewRecorder()
		api.handleDeleteWorkflowFolder(rec, as(tc.user, httptest.NewRequest(http.MethodDelete, "/api/workflows/folder?workspace_path="+tc.target, nil)))
		if rec.Code != tc.code {
			t.Fatalf("%s delete %s: code=%d body=%s", tc.user, tc.target, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	api.handleGetPlanChangelog(rec, as("bob", httptest.NewRequest(http.MethodGet, "/api/workflow/plan-changelog?workspace_path=Workflow/private", nil)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("changelog of a private workflow: %d", rec.Code)
	}
}
