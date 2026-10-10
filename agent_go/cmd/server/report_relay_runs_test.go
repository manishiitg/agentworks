package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportRelayRunsUsesProjectAccessAndDoesNotExposePrivateTrace(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","products":[]},{"id":"other","products":[]}]}`)
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{"Workflow/relay/workflow.json": `{"id":"relay","kind":"relay","access":{"owners":["owner"]}}`}})
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	dir := filepath.Join(root, "Workflow/relay/runs/iteration-2-hook")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "relay_trace.json"), []byte(`{"workflow_id":"run-2","status":"completed","attempt_number":2,"calls":[{"name":"lookup","status":"completed","checkpoint_reused":true,"system_prompt":"PRIVATE PROMPT","arguments":{"password":"PRIVATE ARG"},"started_at":10,"completed_at":12}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "relay_result.json"), []byte(`{"answer":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	read := func(claims *UserClaims, query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, reportPreviewAPIPrefix+"relay-runs?"+query, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		api.handleReportRelayRuns(w, r)
		return w
	}
	for _, claims := range []*UserClaims{nil, {UserID: "other"}} {
		if w := read(claims, "workspace=Workflow/relay"); w.Code != http.StatusForbidden {
			t.Fatalf("unauthorized: %d %s", w.Code, w.Body.String())
		}
	}
	w := read(&UserClaims{UserID: "owner"}, "workspace=Workflow/relay")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"answer":42`) || !strings.Contains(w.Body.String(), `"reused":true`) || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}
	if w := read(&UserClaims{Scope: reportPreviewScope, ScopeWorkspace: "Workflow/relay"}, "workspace=Workflow/other"); w.Code != http.StatusBadRequest {
		t.Fatalf("preview escaped: %d", w.Code)
	}
	if err := os.Remove(filepath.Join(dir, "relay_result.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Workflow/relay/private.json"), []byte(`{"secret":"PRIVATE"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../private.json", filepath.Join(dir, "relay_result.json")); err != nil {
		t.Fatal(err)
	}
	if w := read(&UserClaims{UserID: "owner"}, "workspace=Workflow/relay"); strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal("summary followed a private-file symlink")
	}
}
