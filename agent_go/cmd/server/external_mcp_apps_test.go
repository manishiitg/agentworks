package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/mark3labs/mcp-go/mcp"
)

// Real Streamable HTTP -> shared Crew authorization -> immutable workspace
// storage. No model or function execution is needed to open/read the panel.
func TestExternalMCPDashboardAppPublishedAccessAndRevocation(t *testing.T) {
	f := newExternalToolsFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"reader","username":"reader","products":["work"]}]}`)
	registry := withProjectOwnerRegistry(t)
	const root = "Crew/dashboard-team"
	stubCrewLookups(t, map[string]string{root: "owner"}, nil)
	record := projectOwnerRecord{Product: "work", Folder: "dashboard-team", OwnerID: "owner", Shared: true, Private: true, RunPeople: []string{"reader"}}
	if err := registry.Register(record); err != nil {
		t.Fatal(err)
	}
	f.write(t, root+"/product.json", `{"id":"dashboard-team","product":"work","title":"Team"}`)
	t.Setenv("PUBLIC_URL", "https://example.com")
	static := t.TempDir()
	t.Setenv("STATIC_DIR", static)
	for _, name := range []string{"mcp-dashboard.js", "mcp-dashboard-document.js"} {
		if err := os.WriteFile(filepath.Join(static, name), []byte("/* credential-free runtime */"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(context.Background(), UserContextKey, owner)
	created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "create", ID: "overview", Title: "Published", IncludeDraft: true, Files: map[string]string{"index.html": "<p>Published total 42</p>", "data.json": `{"total":42}`}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "publish", ID: "overview", ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true}); err != nil {
		t.Fatal(err)
	}
	target, err := f.api.dashboardTarget(ctx, owner, root)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := registerDashboardQuestionResources(ctx, []dashboardQuestionDiscovery{{Target: target, Document: *created.Dashboard}})
	if err != nil {
		t.Fatal(err)
	}
	// An unpublished edit must not appear in the inline panel.
	if _, err = dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "update", ID: "overview", ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true, Files: map[string]string{"index.html": "<p>Private draft</p>"}}); err != nil {
		t.Fatal(err)
	}
	srv := serveExternalMCP(t, f.api, &UserClaims{UserID: "reader", Username: "reader"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	tools := listExternalMCPTools(t, ctx, cli)
	open, present := tools["open_dashboard"]
	if len(tools) != 5 || !present || open.Meta == nil || open.Meta.AdditionalFields["ui"] == nil {
		t.Fatal("missing MCP Apps render metadata")
	}
	resource, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: dashboardAppURI}})
	if err != nil || len(resource.Contents) != 1 {
		t.Fatalf("resource: %v %v", resource, err)
	}
	raw, _ := json.Marshal(resource)
	if !strings.Contains(string(raw), dashboardAppMIME) || strings.Contains(string(raw), "Published total") {
		t.Fatal("template must be generic, not cached project data")
	}
	opened := callRemoteTool(t, ctx, cli, "open_dashboard", map[string]any{"resource_id": ids[0]})
	requireRemoteSuccess(t, opened, "open shared Crew dashboard")
	page, _ := json.Marshal(opened.Meta.AdditionalFields["dashboard"])
	if !strings.Contains(string(page), "Published total 42") || strings.Contains(string(page), "Private draft") {
		t.Fatal(string(page))
	}
	read := callRemoteTool(t, ctx, cli, "read_dashboard_data", map[string]any{"resource_id": ids[0], "action": "file", "path": "data.json"})
	requireRemoteSuccess(t, read, "read immutable dashboard data")
	private := callRemoteTool(t, ctx, cli, "read_dashboard_data", map[string]any{"resource_id": ids[0], "action": "file", "path": "../builder/private.json"})
	if !private.IsError {
		t.Fatal("accepted private file path")
	}
	if err := registry.SetRunPeople("work", "dashboard-team", nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"open_dashboard", "read_dashboard_data"} {
		args := map[string]any{"resource_id": ids[0]}
		if name == "read_dashboard_data" {
			args["action"], args["path"] = "file", "data.json"
		}
		result := callRemoteTool(t, ctx, cli, name, args)
		if !result.IsError {
			t.Fatalf("%s kept revoked access", name)
		}
	}
	frame := httptest.NewRecorder()
	f.api.dashboardAppFrame(frame, httptest.NewRequest(http.MethodGet, dashboardAppFramePath, nil))
	csp := frame.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts;") || strings.Contains(csp, "allow-same-origin") {
		t.Fatal("public frame must enforce an opaque origin even when embedded without sandbox flags")
	}
	if frame.Code != 200 || strings.Contains(frame.Body.String(), "Published total") || !strings.Contains(frame.Header().Get("Content-Security-Policy"), "connect-src 'none'") {
		t.Fatal("public frame must contain only an isolated bootstrap")
	}
}

// The hosted ingress forwards API paths; only the inert GET bootstrap is
// public. Nearby API paths and other methods keep normal authentication.
func TestExternalMCPDashboardAppBootstrapAuth(t *testing.T) {
	api := &StreamingAPI{}
	called := false
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		api.dashboardAppFrame(w, r)
	}))
	for _, item := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, dashboardAppFramePath, http.StatusOK},
		{http.MethodPost, dashboardAppFramePath, http.StatusUnauthorized},
		{http.MethodHead, dashboardAppFramePath, http.StatusUnauthorized},
		{http.MethodGet, dashboardAppFramePath + "/x", http.StatusUnauthorized},
	} {
		called = false
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(item.method, item.path, nil))
		if res.Code != item.want || called != (item.want == http.StatusOK) {
			t.Fatalf("%s %s: status=%d handler=%t", item.method, item.path, res.Code, called)
		}
		if item.want == http.StatusOK && (res.Body.String() != dashboardAppFrameHTML || !strings.Contains(res.Header().Get("Content-Security-Policy"), "sandbox allow-scripts;")) {
			t.Fatal("public bootstrap lost its static body or opaque-origin CSP")
		}
	}
}
