package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The decisions routes check the workspace they name: a workflow reader
// lists but cannot answer; a user without access gets nothing.
func TestReportHumanInputRoutesCheckWorkspaceAccess(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor"},{"id":"reader","username":"reader","role":"editor"},{"id":"stranger","username":"stranger","role":"editor"}]}`)
	server, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]}}`
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	call := func(user string, write bool, method, target string) int {
		req := httptest.NewRequest(method, target, strings.NewReader(`{"workspace_path":"Workflow/test"}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		requireReportHumanInputAccess(write, ok)(w, req)
		return w.Code
	}
	list := "/api/report-human-inputs?workspace_path=Workflow/test"
	if got := call("reader", false, http.MethodGet, list); got != http.StatusOK {
		t.Fatalf("reader list = %d", got)
	}
	if got := call("reader", true, http.MethodPost, "/api/report-human-inputs/x/answer"); got != http.StatusForbidden {
		t.Fatalf("reader answer = %d", got)
	}
	if got := call("stranger", false, http.MethodGet, list); got != http.StatusForbidden {
		t.Fatalf("stranger list = %d", got)
	}
	if got := call("owner", true, http.MethodPost, "/api/report-human-inputs/x/answer"); got != http.StatusOK {
		t.Fatalf("owner answer = %d", got)
	}
}
