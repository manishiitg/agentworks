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
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","can_create":true,"products":[]},{"id":"other","products":[]}]}`)
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

// The single Relay Builder sees every caller's recorded run. Run-mode members,
// including legacy editors, see only their own runs; unattributed runs stay hidden.
func TestReportRelayRunsShowReadersOnlyTheirOwnRuns(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","can_create":true,"products":[]},{"id":"editor","can_edit":true,"products":[]},{"id":"reader-a","products":[]},{"id":"reader-b","products":[]}]}`)
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		"Workflow/relay/workflow.json":                  `{"id":"relay","kind":"relay","access":{"owners":["owner"],"editors":["editor"],"readers":["reader-a","reader-b"]}}`,
		"Workflow/relay/webhooks/deliveries/run-1.json": `{"run_id":"run-1","payload":{"relay_caller":"reader-a"}}`,
		"Workflow/relay/webhooks/deliveries/run-2.json": `{"run_id":"run-2","payload":{"relay_caller":"reader-b"}}`,
		"Workflow/relay/webhooks/deliveries/run-4.json": `{"run_id":"run-4","payload":{"relay_caller":"editor"}}`,
	}})
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	for number, id := range map[string]string{"1": "run-1", "2": "run-2", "3": "run-3", "4": "run-4"} {
		dir := filepath.Join(root, "Workflow/relay/runs/iteration-"+number+"-hook")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		trace := `{"workflow_id":"` + id + `","status":"completed","attempt_number":1,"calls":[]}`
		if err := os.WriteFile(filepath.Join(dir, "relay_trace.json"), []byte(trace), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "relay_result.json"), []byte(`{"owner_of":"`+id+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{}
	seen := func(user string) string {
		r := httptest.NewRequest(http.MethodGet, reportPreviewAPIPrefix+"relay-runs?workspace=Workflow/relay", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		api.handleReportRelayRuns(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", user, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	for _, user := range []string{"owner"} {
		body := seen(user)
		for _, id := range []string{"run-1", "run-2", "run-3", "run-4"} {
			if !strings.Contains(body, `"run_id":"`+id+`"`) {
				t.Fatalf("%s should see every run, missing %s: %s", user, id, body)
			}
		}
	}
	editor := seen("editor")
	if !strings.Contains(editor, `"run-4"`) || strings.Contains(editor, "run-1") || strings.Contains(editor, "run-2") || strings.Contains(editor, "run-3") || !strings.Contains(editor, `"scope":"own"`) {
		t.Fatalf("Run-mode editor must see only own run: %s", editor)
	}
	a, b := seen("reader-a"), seen("reader-b")
	if !strings.Contains(a, `"run-1"`) || strings.Contains(a, "run-2") || strings.Contains(a, "run-3") || strings.Contains(a, "run-4") || !strings.Contains(a, `"scope":"own"`) {
		t.Fatalf("reader-a must see only run-1: %s", a)
	}
	if !strings.Contains(b, `"run-2"`) || strings.Contains(b, "run-1") || strings.Contains(b, "run-3") || strings.Contains(b, "run-4") {
		t.Fatalf("reader-b must see only run-2: %s", b)
	}
}
