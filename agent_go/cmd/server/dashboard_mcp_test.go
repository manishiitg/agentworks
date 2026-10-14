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
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Actual Streamable HTTP MCP -> authorized service -> real workspace handlers
// -> immutable files -> authenticated live report reads. No storage mock.
func TestDashboardMCPAuthoringAndTeamRead(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	t.Setenv("PUBLIC_URL", "https://example.com")
	owner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "dashboard-writer", Scopes: []string{"dashboards:read", "workflows:read", "files:read", "runs:execute", "builder:chat", "crews:write"}, WorkflowIDs: []string{"invoices"}, AllCrews: true}}
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
	// Website ownership cannot upgrade a read-only or retired dashboard grant.
	for _, scopes := range [][]string{{"dashboards:read"}, {"dashboards:read", "dashboards:write"}, {"dashboards:read", "code:run"}} {
		limited := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "limited", Scopes: scopes, WorkflowIDs: []string{"invoices"}, AllCrews: true}}
		limitedSrv := serveExternalMCP(t, f.api, limited)
		limitedClient := dialExternalMCP(t, ctx, limitedSrv.URL)
		initializeExternalMCP(t, ctx, limitedClient)
		got := callRemoteTool(t, ctx, limitedClient, externalMCPToolCall, map[string]any{"name": "get_dashboard", "arguments": map[string]any{"workflow_id": "invoices", "dashboard_id": "overview", "revision": rev2}})
		if !got.IsError {
			t.Fatal("draft leaked to non-authoring consent", scopes)
		}
		got = callRemoteTool(t, ctx, limitedClient, externalMCPToolCall, map[string]any{"name": "update_dashboard", "arguments": map[string]any{"workflow_id": "invoices", "dashboard_id": "overview", "expected_revision": rev2}})
		if !got.IsError {
			t.Fatal("non-authoring consent edited a dashboard", scopes)
		}
	}
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
	// The changed product grants invalidate the owner's catalog; reconnect
	// both clients before testing Crew operations under the new grants.
	cli = reconnectAfterCatalogChange(t, ctx, cli, srv.URL+externalMCPPath)
	rcli = dialExternalMCP(t, ctx, rsrv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, rcli)
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
	// Relay authoring reuses relays:write and requires its current Builder owner.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor","products":["agentworks","work","relays","code"]},{"id":"reader","username":"reader","role":"editor","products":["agentworks","work","relays","code"]}]}`)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","kind":"relay","access":{"owners":["other"],"editors":["owner"],"readers":["reader"]}}`)
	owner.AccessToken.Scopes = []string{"dashboards:read", "workflows:read", "files:read", "runs:execute", "relays:write"}
	// Changed consent changes the catalog and requires a fresh MCP session.
	cli = dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	// Old editor grants confer Run access, never Relay Builder ownership.
	requireRemoteError(t, callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "create_dashboard", "arguments": map[string]any{"workflow_id": "invoices", "dashboard_id": "editor-denied", "title": "Denied", "files": map[string]any{"index.html": "<html><body>Denied</body></html>"}}}), "legacy Relay editor cannot build", "forbidden")
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","kind":"relay","access":{"owners":["owner"],"readers":["reader"]}}`)
	relayDraft := call("create_dashboard", map[string]any{"workflow_id": "invoices", "dashboard_id": "relay-view", "title": "Relay view", "files": map[string]any{"index.html": "<html><title>Relay</title><body>Relay</body></html>"}})
	relayRev := relayDraft["dashboard"].(map[string]any)["revision"].(string)
	call("publish_dashboard", map[string]any{"workflow_id": "invoices", "dashboard_id": "relay-view", "expected_revision": relayRev})
	// Relay consent cannot mutate a Crew, even when the caller owns that Crew.
	noCrew := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "update_dashboard", "arguments": map[string]any{"workspace": "Crew/dashboard-team", "dashboard_id": "team", "expected_revision": crewRevision}})
	if !noCrew.IsError {
		t.Fatal("Relay consent edited Crew dashboard")
	}
	// Code authoring is owner-only and reuses code:run. Reviewers gain no writes.
	const codeRoot = "_users/owner/Chats/Code/projects/dashboard-code"
	f.write(t, codeRoot+"/product.json", `{"id":"dashboard-code","product":"code","title":"Code"}`)
	owner.AccessToken.Scopes = []string{"dashboards:read", "code:run"}
	// Changed consent changes the catalog and requires a fresh MCP session.
	cli = dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	codeDraft := call("create_dashboard", map[string]any{"workspace": codeRoot, "dashboard_id": "code-view", "title": "Code view", "files": map[string]any{"index.html": "<html><title>Code</title><body>Code</body></html>"}})
	codeRev := codeDraft["dashboard"].(map[string]any)["revision"].(string)
	call("publish_dashboard", map[string]any{"workspace": codeRoot, "dashboard_id": "code-view", "expected_revision": codeRev})
	for _, deniedCase := range []struct {
		user   string
		scopes []string
		ids    []string
		root   string
		id     string
		rev    string
	}{
		{"reader", []string{"dashboards:read", "code:run"}, []string{"invoices"}, codeRoot, "code-view", codeRev},
		{"owner", []string{"dashboards:read", "code:review"}, []string{"invoices"}, codeRoot, "code-view", codeRev},
		{"owner", []string{"dashboards:read", "workflows:read", "files:read", "runs:execute", "relays:write"}, []string{"invoices"}, codeRoot, "code-view", codeRev},
		{"reader", []string{"dashboards:read", "workflows:read", "files:read", "runs:execute", "relays:write"}, []string{"invoices"}, "Workflow/invoices", "relay-view", relayRev},
		{"owner", []string{"dashboards:read", "workflows:read", "files:read", "runs:execute", "relays:write"}, []string{"other"}, "Workflow/invoices", "relay-view", relayRev},
	} {
		claims := &UserClaims{UserID: deniedCase.user, Username: deniedCase.user, AccessToken: &accesstokens.Token{ID: "denied", Scopes: deniedCase.scopes, WorkflowIDs: deniedCase.ids, AllCrews: true}}
		data, _ := json.Marshal(map[string]any{"workspace": deniedCase.root, "action": "update", "dashboard_id": deniedCase.id, "expected_revision": deniedCase.rev})
		response := httptest.NewRecorder()
		f.api.handleDashboards(response, adminRequest(http.MethodPost, "/api/dashboards", string(data), claims, nil))
		if response.Code == 200 {
			t.Fatal("denied author edited", deniedCase.user, deniedCase.scopes, deniedCase.root)
		}
	}
	// Folder caps still refuse a dashboard mutation with otherwise valid consent.
	owner.AccessToken.FileGuard = &wf.FolderGuard{WritePaths: []string{"docs"}}
	folderDenied := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "update_dashboard", "arguments": map[string]any{"workspace": codeRoot, "dashboard_id": "code-view", "expected_revision": codeRev, "files": map[string]any{"index.html": "<html><title>Code</title><body>Changed</body></html>"}}})
	if !folderDenied.IsError {
		t.Fatal("dashboard edit bypassed folder caps")
	}
	owner.AccessToken.FileGuard = nil
	// The existing connection loses editing immediately when the account is disabled.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor","disabled":true,"products":["code"]}]}`)
	data, _ := json.Marshal(map[string]any{"workspace": codeRoot, "action": "update", "dashboard_id": "code-view", "expected_revision": codeRev})
	roleDenied := httptest.NewRecorder()
	f.api.handleDashboards(roleDenied, adminRequest(http.MethodPost, "/api/dashboards", string(data), owner, nil))
	if roleDenied.Code != http.StatusForbidden {
		t.Fatal("disabled account retained dashboard editing", roleDenied.Code, roleDenied.Body.String())
	}
}
