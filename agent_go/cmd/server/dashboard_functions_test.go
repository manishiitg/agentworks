package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

// The dashboard function routes resolve the project from the server-owned
// dashboard record and reuse the typed-call path, so a viewer lists only what
// they can use, a stranger gets nothing, and the page cannot name a project.
func TestDashboardFunctionRoutesFollowTheDashboardProject(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	t.Setenv("AUTH_SECRET", "dashboard-function-test-secret")
	t.Setenv("PUBLIC_URL", "https://example.com")
	router := mux.NewRouter()
	routes := router.PathPrefix("/api").Subrouter()
	routes.HandleFunc("/dashboards", f.api.handleDashboards).Methods(http.MethodPost)
	f.api.registerDashboardQuestionRoutes(routes)
	f.api.registerDashboardFunctionRoutes(routes)
	server := httptest.NewServer(AuthMiddleware(router))
	t.Cleanup(server.Close)
	tokens := map[string]string{}
	for _, user := range []string{"owner", "reader", "outsider"} {
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		tokens[user] = token
	}
	do := func(user, method, path string, body any) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokens[user])
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return response.StatusCode, out
	}
	op := func(action, revision string) map[string]any {
		t.Helper()
		status, out := do("owner", http.MethodPost, "/api/dashboards", map[string]any{"action": action, "workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": revision, "title": "Overview", "files": map[string]string{"index.html": "<p>Total</p>"}})
		if status != 200 {
			t.Fatalf("%s: %d %v", action, status, out)
		}
		return out
	}
	created := op("create", "")
	op("publish", created["dashboard"].(map[string]any)["revision"].(string))
	resource := op("get", "")["resource_id"].(string)
	base := "/api/dashboards/" + resource

	if status, out := do("reader", http.MethodGet, base+"/functions", nil); status != 200 || out["functions"] == nil {
		t.Fatalf("a viewer lists the project's functions: %d %v", status, out)
	}
	if status, _ := do("outsider", http.MethodGet, base+"/functions", nil); status != 404 {
		t.Fatalf("a person without access to the project must not list functions: %d", status)
	}
	if status, _ := do("outsider", http.MethodPost, base+"/functions/call", map[string]any{"function": "anything", "submission_id": "s1"}); status != 404 {
		t.Fatalf("a person without access must not call: %d", status)
	}
	if status, _ := do("reader", http.MethodPost, base+"/functions/call", map[string]any{"function": "not_published", "submission_id": "s2"}); status != 404 {
		t.Fatalf("an unpublished function must be refused: %d", status)
	}
	// The page cannot choose another project or account through the body.
	if status, _ := do("reader", http.MethodPost, base+"/functions/call", map[string]any{"function": "x", "submission_id": "s3", "workflow_id": "wf_other", "crew_id": "other"}); status != 400 {
		t.Fatalf("a body naming a project must be refused: %d", status)
	}
	if status, _ := do("reader", http.MethodGet, base+"/function-calls/does-not-exist", nil); status != 404 {
		t.Fatalf("an unknown call must be refused: %d", status)
	}
}
