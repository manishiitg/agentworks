package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// A short code names one dashboard. It opens only for a signed-in person who can
// already open that dashboard; an unknown code and a hidden dashboard look the same.
func TestDashboardShortLinkOpensOnlyForPeopleWithAccess(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	t.Setenv("AUTH_SECRET", "dashboard-short-link-test-secret")
	t.Setenv("PUBLIC_URL", "https://example.com")
	router := mux.NewRouter()
	routes := router.PathPrefix("/api").Subrouter()
	routes.HandleFunc("/dashboards", f.api.handleDashboards).Methods(http.MethodPost)
	f.api.registerDashboardShortLinkRoutes(routes)
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
		if user != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[user])
		}
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
	got := op("get", "")
	shortPath, _ := got["short_path"].(string)
	if !strings.HasPrefix(shortPath, "/d/") || !validDashboardShortCode(strings.TrimPrefix(shortPath, "/d/")) {
		t.Fatalf("a registered dashboard carries a short path: %v", got["short_path"])
	}
	if again := op("get", ""); again["short_path"] != shortPath {
		t.Fatalf("the short code must stay the same: %v vs %v", again["short_path"], shortPath)
	}
	code := strings.TrimPrefix(shortPath, "/d/")
	status, out := do("reader", http.MethodGet, "/api/dashboards/by-code/"+code, nil)
	if status != 200 || out["document_path"] != "db/reports/managed/overview/index.html" || out["root"] != "Workflow/invoices" || out["resource_id"] != got["resource_id"] {
		t.Fatalf("a person with access resolves the code: %d %v", status, out)
	}
	if status, _ := do("outsider", http.MethodGet, "/api/dashboards/by-code/"+code, nil); status != 404 {
		t.Fatalf("a person without access must not resolve it: %d", status)
	}
	if status, _ := do("", http.MethodGet, "/api/dashboards/by-code/"+code, nil); status != 401 {
		t.Fatalf("signed-out must be refused: %d", status)
	}
	for _, bad := range []string{"aaaaaaaaaa", "short", "../../etc/passwd", strings.Repeat("a", 40)} {
		if status, _ := do("reader", http.MethodGet, "/api/dashboards/by-code/"+bad, nil); status != 404 {
			t.Fatalf("an unknown or malformed code must be 404: %q %d", bad, status)
		}
	}
}
