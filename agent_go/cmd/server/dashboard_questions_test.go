package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

func TestDashboardQuestionCapabilityMatrix(t *testing.T) {
	allowed := dashboardQuestionPolicy{Authenticated: true, Active: true, Product: true, View: true, Published: true, AskScope: true}
	for _, tc := range []struct {
		name   string
		change func(*dashboardQuestionPolicy)
		view   bool
		ask    bool
		reason string
		mode   string
	}{
		{"reader", func(*dashboardQuestionPolicy) {}, true, true, "", "question_only"},
		{"editor", func(*dashboardQuestionPolicy) {}, true, true, "", "question_only"},
		{"owner", func(p *dashboardQuestionPolicy) { p.Owner = true }, true, true, "", "owner"},
		{"signed_out", func(p *dashboardQuestionPolicy) { p.Authenticated = false }, false, false, "authentication_required", ""},
		{"disabled", func(p *dashboardQuestionPolicy) { p.Active = false }, false, false, "account_disabled", ""},
		{"product", func(p *dashboardQuestionPolicy) { p.Product = false }, false, false, "product_unavailable", ""},
		{"private", func(p *dashboardQuestionPolicy) { p.View = false }, false, false, "dashboard_unavailable", ""},
		{"draft", func(p *dashboardQuestionPolicy) { p.Published = false }, false, false, "not_published", ""},
		{"read_consent", func(p *dashboardQuestionPolicy) { p.AskScope = false }, true, false, "ask_consent_required", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := allowed
			tc.change(&policy)
			got := dashboardQuestionCapabilities(policy)
			if got.CanView != tc.view || got.CanAsk != tc.ask || got.Reason != tc.reason || got.Mode != tc.mode {
				t.Fatalf("capability: %+v", got)
			}
		})
	}
}

// Real JWT-authenticated HTTP routes, the production workspace document and
// dashboard handlers, and disk persistence. There is no model or storage mock.
func TestDashboardQuestionHTTPContract(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	t.Setenv("AUTH_SECRET", "dashboard-question-test-secret")
	t.Setenv("PUBLIC_URL", "https://example.com")
	api := f.api
	start := func() *httptest.Server {
		router := mux.NewRouter()
		routes := router.PathPrefix("/api").Subrouter()
		routes.HandleFunc("/dashboards", api.handleDashboards).Methods(http.MethodPost)
		api.registerDashboardQuestionRoutes(routes)
		server := httptest.NewServer(AuthMiddleware(router))
		t.Cleanup(server.Close)
		return server
	}
	server := start()
	tokens := map[string]string{}
	for _, user := range []string{"owner", "reader", "readonly-owner", "outsider"} {
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		tokens[user] = token
	}
	type result struct {
		status int
		body   map[string]any
		err    error
	}
	request := func(user, method, p string, body any) result {
		raw, err := json.Marshal(body)
		if err != nil {
			return result{err: err}
		}
		req, err := http.NewRequest(method, server.URL+p, bytes.NewReader(raw))
		if err != nil {
			return result{err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		if user != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[user])
		}
		response, err := server.Client().Do(req)
		if err != nil {
			return result{err: err}
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		out := result{status: response.StatusCode, err: err}
		if err == nil {
			out.err = json.Unmarshal(data, &out.body)
		}
		if response.Header.Get("Cache-Control") != "no-store" && user != "" {
			out.err = fmt.Errorf("private response without no-store: %s", data)
		}
		return out
	}
	call := func(user, method, p string, body any, status int) map[string]any {
		t.Helper()
		out := request(user, method, p, body)
		if out.err != nil || out.status != status {
			t.Fatalf("%s %s as %s: %d %+v (%v), want %d", method, p, user, out.status, out.body, out.err, status)
		}
		return out.body
	}
	op := func(action, root, slug, revision, text string) map[string]any {
		t.Helper()
		return call("owner", http.MethodPost, "/api/dashboards", map[string]any{"action": action, "workspace": root, "dashboard_id": slug, "expected_revision": revision, "title": "Overview", "files": map[string]string{"index.html": "<!doctype html><html><head><title>Overview</title></head><body>" + text + "</body></html>"}}, 200)
	}
	root, slug := "Workflow/invoices", "overview"
	created := op("create", root, slug, "", "Total: 42")
	revision := created["dashboard"].(map[string]any)["revision"].(string)
	op("publish", root, slug, revision, "")
	get := op("get", root, slug, "", "")
	resource := get["resource_id"].(string)
	if _, err := uuid.Parse(resource); err != nil || strings.Contains(resource, "owner") {
		t.Fatalf("not an opaque resource: %s", resource)
	}
	base := "/api/dashboards/" + resource
	ownerView := call("owner", http.MethodGet, base, nil, 200)
	readerView := call("reader", http.MethodGet, base, nil, 200)
	if ownerView["capability"].(map[string]any)["mode"] != "owner" || readerView["capability"].(map[string]any)["mode"] != "question_only" {
		t.Fatalf("owner/reader modes: %v %v", ownerView, readerView)
	}
	readonlyView := call("readonly-owner", http.MethodGet, base, nil, 200)
	if readonlyView["capability"].(map[string]any)["mode"] != "question_only" {
		t.Fatal("read-only account gained full owner mode")
	}
	call("", http.MethodGet, base, nil, 401)
	call("outsider", http.MethodGet, base, nil, 404)
	second := op("create", root, "summary", "", "Second dashboard")
	op("publish", root, "summary", second["dashboard"].(map[string]any)["revision"].(string), "")
	registryReads := f.questionRegistryReads.Load()
	listed := call("reader", http.MethodPost, "/api/dashboards", map[string]any{"action": "list", "workspace": root}, 200)
	if f.questionRegistryReads.Load()-registryReads != 1 || len(listed["dashboards"].([]any)) != 2 {
		t.Fatal("dashboard listing did not batch resource registration")
	}
	if listed["dashboards"].([]any)[0].(map[string]any)["resource_id"] != resource {
		t.Fatal("discovery did not reuse the resource id")
	}

	// Drafts never become published dashboard evidence, even for the Builder.
	updated := op("update", root, slug, revision, "Private draft: 99")
	draft := updated["dashboard"].(map[string]any)["revision"].(string)
	if _, err := api.resolveDashboardQuestion(context.Background(), &UserClaims{UserID: "owner", Username: "owner"}, resource, draft); err == nil {
		t.Fatal("unpublished draft became shared dashboard evidence")
	}
	op("publish", root, slug, draft, "")
	call("reader", http.MethodGet, base, nil, 200)
	// Removing sharing rechecks access even for a previously discovered resource.
	f.write(t, root+"/workflow.json", `{"id":"invoices","created_by":"owner","access":{"owners":["owner"]}}`)
	call("reader", http.MethodGet, base, nil, 404)
	// The unused question execution routes are gone. Native Ask is the one path.
	for _, path := range []string{base + "/questions", base + "/questions/old", base + "/questions/old/cancel", base + "/conversations/old"} {
		req, _ := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+tokens["owner"])
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("retired execution route still exists: %s status=%d", path, response.StatusCode)
		}
	}
	t.Run("registry_failure_preserves_dashboard_reads", func(t *testing.T) {
		if err := writeFileToWorkspace(context.Background(), dashboardQuestionRegistryPath, "{invalid"); err != nil {
			t.Fatal(err)
		}
		body := call("owner", http.MethodPost, "/api/dashboards", map[string]any{"action": "get", "workspace": root, "dashboard_id": slug}, 200)
		if body["resource_id"] != nil || body["dashboard"].(map[string]any)["id"] != slug {
			t.Fatalf("registry failure broke get: %v", body)
		}
	})
}
