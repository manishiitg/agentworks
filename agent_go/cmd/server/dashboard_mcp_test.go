package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Actual Streamable HTTP MCP -> authorized service -> real workspace handlers
// -> immutable files -> authenticated live report reads. No storage mock.
func TestDashboardMCPAuthoringAndTeamRead(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://example.com")
	owner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "dashboard-writer", Scopes: []string{"dashboards:read", "dashboards:write", "runs:execute"}, WorkflowIDs: []string{"invoices"}, AllCrews: true}}
	srv := serveExternalMCP(t, f.api, owner)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": name, "arguments": args})
		requireRemoteSuccess(t, result, name)
		var body map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, result)), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": []any{"create_dashboard", "preview_dashboard", "publish_dashboard"}})
	requireRemoteSuccess(t, spec, "dashboard schemas")
	created := call("create_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview", "title": "Overview", "files": map[string]any{"index.html": "<!doctype html><html><head><title>Overview</title></head><body><p>First</p></body></html>"}})
	rev1 := created["dashboard"].(map[string]any)["revision"].(string)
	validation := call("validate_dashboard", map[string]any{"workflow_id": "invoices", "dashboard_id": "overview", "revision": rev1})
	if validation["valid"] != true {
		t.Fatalf("validation: %v", validation)
	}
	// The merged tool runs the same operation; the old names above stay callable.
	call("dashboard", map[string]any{"action": "publish", "workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": rev1})
	link := call("get_dashboard_link", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview"})["url"].(string)
	if strings.Contains(link, "token=") || !strings.Contains(link, "document=") {
		t.Fatalf("bad live link: %s", link)
	}
	updated := call("update_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": rev1, "files": map[string]any{"index.html": "<html><title>Overview</title><body>Second</body></html>"}})
	rev2 := updated["dashboard"].(map[string]any)["revision"].(string)
	stale := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "publish_dashboard", "arguments": map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": rev1}})
	requireRemoteError(t, stale, "stale publication", "revision_conflict")
	reader := &UserClaims{UserID: "reader", Username: "reader", AccessToken: &accesstokens.Token{ID: "reader", Scopes: []string{"dashboards:read"}, WorkflowIDs: []string{"invoices"}, AllCrews: true}}
	rsrv := serveExternalMCP(t, f.api, reader)
	rcli := dialExternalMCP(t, ctx, rsrv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, rcli)
	guide := callRemoteTool(t, ctx, rcli, externalMCPToolCall, map[string]any{"name": "get_guidance_topic", "arguments": map[string]any{"topic": "dashboard-authoring"}})
	requireRemoteSuccess(t, guide, "dashboard-only connection usage guidance")
	// A read-only connection is shown one dashboard tool without write actions,
	// and no old names.
	menu := marshalStructured(t, callRemoteTool(t, ctx, rcli, externalMCPToolSpec, map[string]any{}))
	if !strings.Contains(menu, `"name":"dashboard"`) || strings.Contains(menu, "list_dashboards") {
		t.Fatalf("reader menu: %s", menu)
	}
	readerSpec := marshalStructured(t, callRemoteTool(t, ctx, rcli, externalMCPToolSpec, map[string]any{"names": []any{"dashboard"}}))
	if !strings.Contains(readerSpec, `"link"`) || strings.Contains(readerSpec, `"publish"`) || strings.Contains(readerSpec, `"create"`) {
		t.Fatalf("reader dashboard actions: %s", readerSpec)
	}
	requireRemoteError(t, callRemoteTool(t, ctx, rcli, externalMCPToolCall, map[string]any{"name": "dashboard", "arguments": map[string]any{"action": "publish", "workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": rev2}}), "reader publish", "insufficient_scope")
	discovered := callRemoteTool(t, ctx, rcli, externalMCPToolCall, map[string]any{"name": "dashboard", "arguments": map[string]any{"action": "list"}})
	requireRemoteSuccess(t, discovered, "reader discovery")
	text := marshalStructured(t, discovered)
	if !strings.Contains(text, "overview") || strings.Contains(text, "secret") {
		t.Fatalf("wrong discovery: %s", text)
	}
	denied := callRemoteTool(t, ctx, rcli, externalMCPToolCall, map[string]any{"name": "update_dashboard", "arguments": map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": rev2}})
	if !denied.IsError {
		t.Fatal("read connection can edit")
	}
	direct := callRemoteTool(t, ctx, rcli, externalMCPToolCall, map[string]any{"name": "get_report_link", "arguments": map[string]any{"workflow_id": "invoices", "document_path": "db/reports/managed/overview/index.html"}})
	requireRemoteSuccess(t, direct, "read-only direct URL")
	// The same stable link serves published content while another draft exists.
	parsed, _ := url.Parse(link)
	document := parsed.Query().Get("document")
	read := func(p string, claims *UserClaims) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, reportPreviewAPIPrefix+"file?workspace=Workflow/invoices&path="+url.QueryEscape(p), nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		f.api.handleReportPreviewFile(w, r)
		return w
	}
	if w := read(document, &UserClaims{UserID: "reader"}); w.Code != 200 || !strings.Contains(w.Body.String(), "First") {
		t.Fatalf("live read: %d %s", w.Code, w.Body.String())
	}
	if w := read("db/reports/managed/overview/"+rev2+"/index.html", &UserClaims{UserID: "reader"}); w.Code != 403 {
		t.Fatalf("draft leaked: %d %s", w.Code, w.Body.String())
	}
	if w := read(document, &UserClaims{UserID: "outsider"}); w.Code != 403 {
		t.Fatalf("link granted access: %d", w.Code)
	}
	call("publish_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": rev2})
	if again := call("get_dashboard_link", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview"})["url"]; again != link {
		t.Fatalf("URL changed: %s -> %v", link, again)
	}
	call("restore_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": rev2, "revision": rev1})
	bad := call("update_dashboard", map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": rev1, "files": map[string]any{"index.html": "<html><body><script>window.report.unknown()</script></body></html>"}})["dashboard"].(map[string]any)["revision"]
	rejected := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "publish_dashboard", "arguments": map[string]any{"workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": bad}})
	requireRemoteError(t, rejected, "invalid HTML", "invalid_dashboard")
	// Preview credentials retain their originating connection's live bounds,
	// even for a legacy document that has no bundle resolver.
	previewClaims := *owner
	previewClaims.Scope = reportPreviewScope
	previewClaims.ScopeWorkspace = "Workflow/invoices"
	previewClaims.PreviewConnectionID = owner.AccessToken.ID
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := reportPreviewReadable(request, &previewClaims, "Workflow/invoices"); err != nil {
		t.Fatalf("authorized preview: %v", err)
	}
	restricted := *owner.AccessToken
	restricted.WorkflowIDs = []string{"other"}
	previewClaims.AccessToken = &restricted
	if err := reportPreviewReadable(request, &previewClaims, "Workflow/invoices"); err == nil {
		t.Fatal("preview widened revoked workflow bounds")
	}
	// A migrated Crew shares this same authoring/read path. Legacy owner links
	// resolve to its canonical root, while readers cannot publish.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["agentworks","work"]},{"id":"reader","username":"reader","products":["agentworks","work"]}]}`)
	stubCrewLookups(t, map[string]string{"Crew/dashboard-team": "owner"}, map[string]string{"_users/owner/Chats/Work/projects/dashboard-team": "Crew/dashboard-team"})
	f.write(t, "Crew/dashboard-team/product.json", `{"id":"dashboard-team","product":"work","title":"Team"}`)
	crewCreated := call("create_dashboard", map[string]any{"workspace": "Crew/dashboard-team", "dashboard_id": "team", "title": "Team", "files": map[string]any{"index.html": "<html><title>Team</title><body>Shared Crew</body></html>"}})
	crewRevision := crewCreated["dashboard"].(map[string]any)["revision"].(string)
	call("publish_dashboard", map[string]any{"workspace": "Crew/dashboard-team", "dashboard_id": "team", "expected_revision": crewRevision})
	crewList := callRemoteTool(t, ctx, rcli, externalMCPToolCall, map[string]any{"name": "list_dashboards", "arguments": map[string]any{"workspace": "_users/owner/Chats/Work/projects/dashboard-team"}})
	requireRemoteSuccess(t, crewList, "Crew reader discovery through legacy root")
	if !strings.Contains(marshalStructured(t, crewList), "document=") {
		t.Fatal("Crew discovery lacks its live URL")
	}
	crewRequest := httptest.NewRequest(http.MethodGet, reportPreviewAPIPrefix+"file?workspace=Crew/dashboard-team&path=db/reports/managed/team/index.html", nil)
	crewRequest = crewRequest.WithContext(context.WithValue(crewRequest.Context(), UserContextKey, &UserClaims{UserID: "reader", Username: "reader"}))
	crewResponse := httptest.NewRecorder()
	f.api.handleReportPreviewFile(crewResponse, crewRequest)
	if crewResponse.Code != 200 || !strings.Contains(crewResponse.Body.String(), "Shared Crew") {
		t.Fatalf("Crew reader: %d %s", crewResponse.Code, crewResponse.Body.String())
	}

}
