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

// Product membership grants all published recorded work, while draft access
// remains Builder-only and unrelated callers receive no product listing.
func TestReportRelayRunsSharePublishedDiagnosticsAcrossProductMembers(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","can_create":true,"products":[]},{"id":"editor","can_edit":true,"products":[]},{"id":"reader-a","products":[]},{"id":"reader-b","products":[]},{"id":"other","products":[]},{"id":"disabled","disabled":true,"products":[]}]}`)
	workspace := "Workflow/relay"
	releaseWorkspace := relayReleaseWorkspace(workspace, "v1")
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspace + "/workflow.json":                 `{"id":"relay","label":"Invoice OCR","kind":"relay","access":{"owners":["owner"],"editors":["editor"],"readers":["reader-a","reader-b","disabled","deleted"]}}`,
		relayReleaseRoot(workspace) + "/active.json": `{"version":"v1"}`,
		releaseWorkspace + "/release.json":           `{"version":"v1","hash":"fixture","file_count":1,"files":["relay.py"]}`,
	}})
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	for _, number := range []string{"1", "2", "3"} {
		dir := filepath.Join(root, releaseWorkspace, "runs/iteration-"+number+"-hook")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		trace := `{"workflow_id":"run-` + number + `","status":"failed","error":"validation failed","attempt_number":1,"calls":[{"id":"extract","name":"extract","status":"completed","started_at":10,"completed_at":12,"output":{"invoice_id":"invoice-` + number + `"}},{"id":"validate","name":"validate","status":"failed","started_at":12,"completed_at":14,"error":"missing invoice date","agent_calls":[{"system_prompt":"PRIVATE PROMPT"}]}]}`
		if err := os.WriteFile(filepath.Join(dir, "relay_trace.json"), []byte(trace), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "relay_result.json"), []byte(`{"invoice_id":"invoice-`+number+`","api_key":"PRIVATE KEY"}`), 0600); err != nil {
			t.Fatal(err)
		}
		deliveries := filepath.Join(root, releaseWorkspace, "webhooks/deliveries")
		if err := os.MkdirAll(deliveries, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(deliveries, "run-"+number+".json"), []byte(`{"payload":{"relay_caller":"reader-b","args":{"INPUT":{"invoice_id":"invoice-`+number+`","password":"PRIVATE INPUT"}}}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{}
	read := func(user, version string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, reportPreviewAPIPrefix+"relay-runs?workspace="+workspace+"&version="+version, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		api.handleReportRelayRuns(w, r)
		return w
	}
	for _, user := range []string{"owner", "editor", "reader-a", "reader-b"} {
		w := read(user, "v1")
		body := w.Body.String()
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", user, w.Code, body)
		}
		for _, id := range []string{"run-1", "run-2", "run-3", "invoice-2", "missing invoice date", `"started_at":12`, `"completed_at":14`, `"duration_s":4`, `"scope":"all"`, `"caller":"reader-b"`} {
			if !strings.Contains(body, id) {
				t.Fatalf("%s missing %s: %s", user, id, body)
			}
		}
		if strings.Contains(body, "PRIVATE") {
			t.Fatalf("%s exposed a credential or raw trace: %s", user, body)
		}
		if strings.Contains(body, `"can_ask":true`) != (user == "owner") {
			t.Fatalf("Run must not acquire Builder chat: %s", body)
		}
	}
	if w := read("reader-a", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"version":"v1"`) {
		t.Fatalf("Run must default to the active publication: %d %s", w.Code, w.Body.String())
	}
	if w := read("reader-a", "draft"); w.Code != 403 {
		t.Fatalf("Run read draft: %d %s", w.Code, w.Body.String())
	}
	for _, user := range []string{"disabled", "deleted"} {
		if w := read(user, "v1"); w.Code != 403 {
			t.Fatalf("inactive product member %s: %d %s", user, w.Code, w.Body.String())
		}
	}
	if w := read("other", "v1"); w.Code != 403 {
		t.Fatalf("Unrelated user read product: %d %s", w.Code, w.Body.String())
	}
	if w := read("reader-a", "v1&limit=2"); !strings.Contains(w.Body.String(), `"next_before":2`) || strings.Contains(w.Body.String(), `"run_id":"run-1"`) {
		t.Fatalf("bounded first page: %d %s", w.Code, w.Body.String())
	}
	if w := read("reader-a", "v1&before=2"); !strings.Contains(w.Body.String(), `"run_id":"run-1"`) || strings.Contains(w.Body.String(), `"run_id":"run-2"`) {
		t.Fatalf("older page: %d %s", w.Code, w.Body.String())
	}
	if w := read("reader-a", "v1&before=../private"); w.Code != 400 {
		t.Fatalf("invalid cursor: %d", w.Code)
	}
	largeResult := filepath.Join(root, releaseWorkspace, "runs/iteration-3-hook/relay_result.json")
	if err := os.WriteFile(largeResult, []byte(`{"large":"`+strings.Repeat("x", 20<<10)+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if w := read("reader-a", "v1"); !strings.Contains(w.Body.String(), `"recorded_bytes"`) || strings.Contains(w.Body.String(), strings.Repeat("x", 5000)) {
		t.Fatalf("oversized value must have a truncation marker: %s", w.Body.String())
	}
	// A run cannot use an authored delivery link to read another private file.
	inputPath := filepath.Join(root, releaseWorkspace, "webhooks/deliveries/run-2.json")
	if err := os.Remove(inputPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, releaseWorkspace, "private.json"), []byte(`{"payload":{"args":{"INPUT":{"invoice_id":"PRIVATE SYMLINK"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../private.json", inputPath); err != nil {
		t.Fatal(err)
	}
	if w := read("reader-a", "v1"); strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal("delivery projection followed a symlink")
	}
}
